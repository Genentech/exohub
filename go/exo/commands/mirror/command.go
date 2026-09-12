package mirror

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/Genentech/exohub/go/exo/commandutil"
)

const longText = `Resolve annex keys under repo paths and fetch them from S3 via s5cmd.

Subcommands:
  plan              Generate an s5cmd plan file (no execution)
  execute [FILE]    Execute a previously generated plan (default: .git/exohub/exo-mirror-plan.txt)

The execute subcommand reads metadata from a sidecar file (same basename with .env)
to recover recorded settings. Honors ignore-errors if present; otherwise fails on s5cmd errors.

Examples:
  exo mirror plan --s3-annex s3://my-bucket/annex --store /mnt/store --path db
  exo mirror execute                      # uses .git/exohub/exo-mirror-plan.txt
  exo mirror execute /path/to/plan.txt    # execute a specific plan file`

type planOptions struct {
	s3Prefix     string
	store        string
	repo         string
	mode         string
	jobs         int
	planOut      string
	keysOut      string
	mapOut       string
	noClobber    bool
	ignoreErrors bool
	execute      bool
	dryRun       bool
	verbose      bool
	paths        []string
}

type executeOptions struct {
	planFile string
}

func NewCommand() *cobra.Command {
	var legacy planOptions
	rootCmd := &cobra.Command{
		Use:   "mirror",
		Short: "Resolve annex keys and mirror them from S3",
		Long:  longText,
		Run: func(cmd *cobra.Command, args []string) {
			runLegacy(legacy, args)
		},
	}

	rootCmd.Flags().StringVar(&legacy.s3Prefix, "s3-annex", "", "S3 prefix like s3://bucket/path")
	rootCmd.Flags().StringVar(&legacy.store, "store", "", "Local store directory")
	rootCmd.Flags().StringVar(&legacy.repo, "repo", "", "Git repository root")
	rootCmd.Flags().StringArrayVar(&legacy.paths, "path", nil, "Restrict scan to specific paths")
	rootCmd.Flags().StringVar(&legacy.mode, "mode", "auto", "auto or direct")
	rootCmd.Flags().IntVar(&legacy.jobs, "jobs", 16, "s5cmd worker count")
	rootCmd.Flags().BoolVar(&legacy.execute, "execute", false, "Execute plan immediately")
	rootCmd.Flags().StringVar(&legacy.planOut, "plan-out", "", "Write s5cmd command file")
	rootCmd.Flags().StringVar(&legacy.keysOut, "keys-out", "", "Write resolved keys")
	rootCmd.Flags().StringVar(&legacy.mapOut, "map-out", "", "Write key->path map")
	rootCmd.Flags().BoolVar(&legacy.noClobber, "no-clobber", false, "Avoid overwriting existing files")
	rootCmd.Flags().BoolVar(&legacy.ignoreErrors, "ignore-errors", false, "Ignore s5cmd failures")
	rootCmd.Flags().BoolVar(&legacy.dryRun, "dry-run", false, "Show summary without writing files")
	rootCmd.Flags().BoolVarP(&legacy.verbose, "verbose", "v", false, "Extra logging")

	rootCmd.AddCommand(newPlanCmd())
	rootCmd.AddCommand(newExecuteCmd())

	return rootCmd
}

func newPlanCmd() *cobra.Command {
	var opts planOptions
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Generate an s5cmd plan file without executing",
		Run: func(cmd *cobra.Command, args []string) {
			runPlan(opts, true, args)
		},
	}
	cmd.Flags().StringVar(&opts.s3Prefix, "s3-annex", "", "S3 prefix like s3://bucket/path")
	cmd.Flags().StringVar(&opts.store, "store", "", "Local store directory")
	cmd.Flags().StringVar(&opts.repo, "repo", "", "Git repository root")
	cmd.Flags().StringArrayVar(&opts.paths, "path", nil, "Restrict scan to specific paths")
	cmd.Flags().StringVar(&opts.mode, "mode", "auto", "auto or direct")
	cmd.Flags().IntVar(&opts.jobs, "jobs", 16, "s5cmd worker count")
	cmd.Flags().StringVar(&opts.planOut, "plan-out", "", "Write s5cmd command file")
	cmd.Flags().StringVar(&opts.keysOut, "keys-out", "", "Write resolved keys")
	cmd.Flags().StringVar(&opts.mapOut, "map-out", "", "Write key->path map")
	cmd.Flags().BoolVar(&opts.noClobber, "no-clobber", false, "Avoid overwriting existing files")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "Show summary without writing files")
	cmd.Flags().BoolVarP(&opts.verbose, "verbose", "v", false, "Extra logging")
	return cmd
}

func newExecuteCmd() *cobra.Command {
	var opts executeOptions
	cmd := &cobra.Command{
		Use:   "execute [PLAN_FILE]",
		Short: "Execute a previously generated plan",
		Args:  cobra.RangeArgs(0, 1),
		Run: func(cmd *cobra.Command, args []string) {
			if len(args) == 1 {
				opts.planFile = args[0]
			}
			runExecute(opts)
		},
	}
	return cmd
}

func runLegacy(opts planOptions, args []string) {
	if len(args) > 0 {
		fmt.Fprintf(os.Stderr, "Unknown option: %s\n", args[0])
		printUsageStderr()
		os.Exit(2)
	}
	runPlan(opts, false, nil)
}

func runPlan(opts planOptions, explicitPlan bool, args []string) {
	if len(args) > 0 {
		fmt.Fprintf(os.Stderr, "Unknown option: %s\n", args[0])
		printUsageStderr()
		os.Exit(2)
	}
	if opts.repo == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
		opts.repo = cwd
	}
	repoAbs, err := filepath.Abs(opts.repo)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
	opts.repo = repoAbs

	opts.s3Prefix = strings.TrimRight(opts.s3Prefix, "/")
	opts.store = strings.TrimRight(opts.store, "/")

	if !isDir(filepath.Join(opts.repo, ".git")) {
		fmt.Fprintf(os.Stderr, "Error: '%s' is not a git repository (missing .git)\n", opts.repo)
		os.Exit(1)
	}

	defaultPlan := filepath.Join(opts.repo, ".git", "exohub", "exo-mirror-plan.txt")
	defaultMeta := filepath.Join(opts.repo, ".git", "exohub", "exo-mirror-plan.env")

	if explicitPlan {
		if opts.planOut == "" {
			opts.planOut = defaultPlan
		}
		if opts.mapOut == "" {
			base := strings.TrimSuffix(opts.planOut, filepath.Ext(opts.planOut))
			opts.mapOut = base + ".map.tsv"
		}
	}

	if opts.s3Prefix == "" || opts.store == "" {
		fmt.Fprintln(os.Stderr, "Error: --s3-annex and --store are required")
		printUsageStderr()
		os.Exit(2)
	}

	if opts.mode != "auto" && opts.mode != "direct" {
		fmt.Fprintf(os.Stderr, "Error: invalid --mode '%s' (use auto|direct)\n", opts.mode)
		os.Exit(2)
	}

	if err := ensureStore(opts.store); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}

	storeAbs, err := filepath.Abs(opts.store)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
	opts.store = storeAbs

	logf(opts.verbose, "Repo:    %s", opts.repo)
	logf(opts.verbose, "S3 Annex: %s", opts.s3Prefix)
	logf(opts.verbose, "Store:   %s", opts.store)
	logf(opts.verbose, "Mode:    %s%s%s%s%s%s%s%s", opts.mode,
		flagTag(opts.dryRun, " (dry-run)"),
		flagTag(opts.verbose, ", verbose"),
		flagTag(opts.execute, ", execute"),
		flagTag(opts.planOut != "", ", plan="+opts.planOut),
		flagTag(opts.keysOut != "", ", keys="+opts.keysOut),
		flagTag(opts.noClobber, ", no-clobber"),
		flagTag(opts.ignoreErrors, ", ignore-errors"),
	)

	search := buildSearchPaths(opts.repo, opts.paths, opts.verbose)
	keysList, keyMap, countLinks := collectKeys(opts.repo, search)
	sort.Strings(keysList)

	logf(opts.verbose, "Symlinks scanned: %d", countLinks)
	logf(opts.verbose, "Unique keys:      %d", len(keysList))

	if opts.keysOut != "" {
		if opts.dryRun {
			logf(opts.verbose, "Would write keys: %s", opts.keysOut)
		} else {
			if err := writeKeys(opts.keysOut, keysList); err != nil {
				fmt.Fprintln(os.Stderr, err.Error())
				os.Exit(1)
			}
		}
	}

	if opts.mapOut != "" {
		if opts.dryRun {
			logf(opts.verbose, "Would write map: %s", opts.mapOut)
		} else {
			if err := writeKeyMap(opts.mapOut, keyMap); err != nil {
				fmt.Fprintln(os.Stderr, err.Error())
				os.Exit(1)
			}
		}
	}

	plan := buildPlan(opts, keysList)
	logf(opts.verbose, "Plan commands:   %d", len(plan))

	var metaOut string
	if explicitPlan {
		base := strings.TrimSuffix(opts.planOut, filepath.Ext(opts.planOut))
		metaOut = base + ".env"
	} else if opts.planOut != "" {
		metaOut = ""
	} else {
		metaOut = ""
	}

	if opts.planOut != "" {
		if opts.dryRun {
			logf(opts.verbose, "Would write plan: %s", opts.planOut)
		} else {
			if err := writePlan(opts.planOut, plan); err != nil {
				fmt.Fprintln(os.Stderr, err.Error())
				os.Exit(1)
			}
			if explicitPlan {
				if err := writeMeta(metaOut, opts, len(keysList), opts.mapOut, opts.keysOut); err != nil {
					fmt.Fprintln(os.Stderr, err.Error())
					os.Exit(1)
				}
			}
		}
	}

	if opts.dryRun {
		showSample(plan)
		return
	}

	if opts.execute {
		if err := ensureS5cmd(); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(127)
		}
		rc := runS5cmd(opts, plan)
		if rc != nil {
			if opts.ignoreErrors {
				logf(opts.verbose, "s5cmd exited with %s; continuing due to --ignore-errors", rc.Error())
				os.Exit(0)
			}
			exitWithError(rc)
		}
		return
	}

	logf(opts.verbose, "Plan generated. Use s5cmd to execute, e.g.:")
	if opts.planOut != "" {
		fmt.Printf("  s5cmd run -p %d '%s'\n", opts.jobs, opts.planOut)
	} else {
		fmt.Printf("  printf '%%s\\n' \"${PLAN[@]}\" | s5cmd run -p %d\n", opts.jobs)
	}
	_ = defaultMeta
}

func runExecute(opts executeOptions) {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
	repo := cwd
	if !isDir(filepath.Join(repo, ".git")) {
		fmt.Fprintf(os.Stderr, "Error: '%s' is not a git repository (missing .git)\n", repo)
		os.Exit(1)
	}

	defaultPlan := filepath.Join(repo, ".git", "exohub", "exo-mirror-plan.txt")
	defaultMeta := filepath.Join(repo, ".git", "exohub", "exo-mirror-plan.env")

	planFile := opts.planFile
	if planFile == "" {
		planFile = defaultPlan
	}
	if !fileExists(planFile) {
		fmt.Fprintf(os.Stderr, "Error: plan file not found: %s\n", planFile)
		os.Exit(2)
	}

	metaFile := strings.TrimSuffix(planFile, filepath.Ext(planFile)) + ".env"
	if !fileExists(metaFile) && fileExists(defaultMeta) {
		metaFile = defaultMeta
	}

	execOpts := planOptions{
		repo:    repo,
		jobs:    16,
		verbose: false,
	}

	if fileExists(metaFile) {
		if err := loadEnv(metaFile, &execOpts); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
	}

	if execOpts.store != "" {
		_ = os.MkdirAll(execOpts.store, 0o755)
	}

	logf(execOpts.verbose, "Repo:    %s", execOpts.repo)
	logf(execOpts.verbose, "Plan:    %s", planFile)
	logf(execOpts.verbose, "Execute: s5cmd workers=%d", execOpts.jobs)

	if err := ensureS5cmd(); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(127)
	}

	cmd := commandutil.Command("s5cmd", "--numworkers", strconv.Itoa(execOpts.jobs), "run", planFile)
	if execOpts.verbose {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	} else {
		cmd.Stdout = os.Stdout
		cmd.Stderr = io.Discard
	}
	cmd.Stdin = os.Stdin
	err = cmd.Run()
	if err != nil {
		if execOpts.ignoreErrors {
			logf(execOpts.verbose, "s5cmd exited with %s; continuing due to ignore-errors=1", err.Error())
			os.Exit(0)
		}
		exitWithError(err)
	}
}

func buildPlan(opts planOptions, keys []string) []string {
	var plan []string
	for _, key := range keys {
		plan = append(plan, cpLine(opts, key))
		if opts.mode == "auto" {
			if pat := chunkPatternForKey(key); pat != "" {
				plan = append(plan, cpLine(opts, pat))
			}
		}
	}
	return plan
}

func cpLine(opts planOptions, key string) string {
	if opts.noClobber {
		return fmt.Sprintf("cp -n %s/%s %s/", opts.s3Prefix, key, opts.store)
	}
	return fmt.Sprintf("cp %s/%s %s/", opts.s3Prefix, key, opts.store)
}

func chunkPatternForKey(base string) string {
	ext := ""
	if idx := strings.LastIndex(base, "."); idx != -1 {
		ext = base[idx:]
	}
	noExt := base
	if ext != "" {
		noExt = strings.TrimSuffix(base, ext)
	}
	parts := strings.SplitN(noExt, "--", 2)
	if len(parts) != 2 {
		return ""
	}
	prefix := parts[0]
	hash := parts[1]
	if prefix == "" || hash == "" {
		return ""
	}
	return fmt.Sprintf("%s-S*-C*--%s%s", prefix, hash, ext)
}

func collectKeys(repo string, search []string) ([]string, map[string][]string, int) {
	keys := map[string]struct{}{}
	keyMap := map[string][]string{}
	repoObjects := filepath.Join(repo, ".git", "annex", "objects") + string(os.PathSeparator)
	links := collectSymlinks(search, filepath.Join(repo, ".git"))
	countLinks := len(links)
	for _, link := range links {
		target, err := resolveLinkTarget(link)
		if err != nil {
			continue
		}
		if !strings.HasPrefix(target, repoObjects) {
			continue
		}
		key := filepath.Base(target)
		keys[key] = struct{}{}
		rel, err := filepath.Rel(repo, link)
		if err != nil || strings.HasPrefix(rel, "..") {
			rel = link
		}
		keyMap[key] = append(keyMap[key], rel)
	}
	var list []string
	for key := range keys {
		list = append(list, key)
	}
	return list, keyMap, countLinks
}

func writeKeys(path string, keys []string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data := strings.Join(keys, "\n") + "\n"
	return os.WriteFile(path, []byte(data), 0o644)
}

func writeKeyMap(path string, keyMap map[string][]string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var keys []string
	for key := range keyMap {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		for _, p := range keyMap[key] {
			if p == "" {
				continue
			}
			if _, err := fmt.Fprintf(f, "%s\t%s\n", key, p); err != nil {
				return err
			}
		}
	}
	return nil
}

func writePlan(path string, lines []string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data := strings.Join(lines, "\n") + "\n"
	return os.WriteFile(path, []byte(data), 0o644)
}

func writeMeta(path string, opts planOptions, keyCount int, mapOut, keysOut string) error {
	if path == "" {
		return nil
	}
	content := fmt.Sprintf(`FORMAT=exo-mirror-plan-v1
REPO="%s"
S3_PREFIX="%s"
STORE="%s"
MODE="%s"
JOBS=%d
NO_CLOBBER=%d
IGNORE_ERRORS=%d
VERBOSE=%d
PLAN_FILE="%s"
KEYS_COUNT=%d
KEYMAP_FILE="%s"
KEYS_FILE="%s"
`,
		opts.repo,
		opts.s3Prefix,
		opts.store,
		opts.mode,
		opts.jobs,
		boolToInt(opts.noClobber),
		boolToInt(opts.ignoreErrors),
		boolToInt(opts.verbose),
		opts.planOut,
		keyCount,
		mapOut,
		keysOut,
	)
	return os.WriteFile(path, []byte(content), 0o644)
}

func showSample(plan []string) {
	sample := 10
	if len(plan) < sample {
		sample = len(plan)
	}
	if sample == 0 {
		return
	}
	fmt.Println("[exo-mirror] Sample commands:")
	for _, line := range plan[:sample] {
		fmt.Printf("  %s\n", line)
	}
}

func buildSearchPaths(repo string, paths []string, verbose bool) []string {
	if len(paths) == 0 {
		return []string{repo}
	}
	var search []string
	for _, p := range paths {
		if !filepath.IsAbs(p) {
			p = filepath.Join(repo, p)
		}
		abs, err := filepath.Abs(p)
		if err != nil || !fileExists(abs) {
			if verbose {
				fmt.Printf("[exo-mirror] Skip missing path: %s\n", p)
			}
			continue
		}
		search = append(search, abs)
	}
	return search
}

func collectSymlinks(paths []string, gitDir string) []string {
	var out []string
	for _, root := range paths {
		info, err := os.Lstat(root)
		if err != nil {
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			if !isUnderGit(root, gitDir) {
				out = append(out, root)
			}
			continue
		}
		if !info.IsDir() {
			continue
		}
		rootDev, ok := statDevice(info)
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if isUnderGit(path, gitDir) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.Type()&os.ModeSymlink != 0 {
				out = append(out, path)
				return nil
			}
			if d.IsDir() && ok {
				if info, statErr := os.Lstat(path); statErr == nil {
					if dev, ok := statDevice(info); ok && dev != rootDev {
						return filepath.SkipDir
					}
				}
			}
			return nil
		})
	}
	return out
}

func resolveLinkTarget(link string) (string, error) {
	target, err := os.Readlink(link)
	if err != nil {
		return "", err
	}
	if filepath.IsAbs(target) {
		return filepath.Clean(target), nil
	}
	base := filepath.Dir(link)
	return filepath.Clean(filepath.Join(base, target)), nil
}

func statDevice(info os.FileInfo) (uint64, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return uint64(stat.Dev), true
}

func isUnderGit(path, gitDir string) bool {
	if path == gitDir {
		return true
	}
	prefix := gitDir + string(os.PathSeparator)
	return strings.HasPrefix(path, prefix)
}

func ensureStore(path string) error {
	if err := os.MkdirAll(path, 0o755); err != nil {
		return err
	}
	return nil
}

func ensureS5cmd() error {
	_, err := exec.LookPath("s5cmd")
	if err != nil {
		return errors.New("s5cmd not found in PATH; install it to execute plans")
	}
	return nil
}

func runS5cmd(opts planOptions, plan []string) error {
	args := []string{"--numworkers", strconv.Itoa(opts.jobs), "run"}
	if opts.planOut != "" {
		args = append(args, opts.planOut)
		cmd := commandutil.Command("s5cmd", args...)
		if opts.verbose {
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
		} else {
			cmd.Stdout = os.Stdout
			cmd.Stderr = io.Discard
		}
		cmd.Stdin = os.Stdin
		return cmd.Run()
	}
	cmd := commandutil.Command("s5cmd", args...)
	cmd.Stdin = strings.NewReader(strings.Join(plan, "\n") + "\n")
	cmd.Stdout = os.Stdout
	if opts.verbose {
		cmd.Stderr = os.Stderr
	}
	return cmd.Run()
}

func loadEnv(path string, opts *planOptions) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		val = strings.Trim(val, `"'`)
		switch key {
		case "REPO":
			opts.repo = val
		case "S3_PREFIX":
			opts.s3Prefix = val
		case "STORE":
			opts.store = val
		case "MODE":
			opts.mode = val
		case "JOBS":
			if n, err := strconv.Atoi(val); err == nil {
				opts.jobs = n
			}
		case "NO_CLOBBER":
			opts.noClobber = val == "1"
		case "IGNORE_ERRORS":
			opts.ignoreErrors = val == "1"
		case "VERBOSE":
			opts.verbose = val == "1"
		case "PLAN_FILE":
			opts.planOut = val
		}
	}
	return scanner.Err()
}

func logf(verbose bool, format string, args ...any) {
	fmt.Printf("[exo-mirror] %s\n", fmt.Sprintf(format, args...))
}

func flagTag(ok bool, tag string) string {
	if ok {
		return tag
	}
	return ""
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func printUsageStderr() {
	fmt.Fprint(os.Stderr, longText)
}

func fileExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func exitWithError(err error) {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		os.Exit(exitErr.ExitCode())
	}
	os.Exit(1)
}
