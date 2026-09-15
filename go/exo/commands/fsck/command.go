package fsck

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/Genentech/exohub/go/exo/commandutil"
	manifestutil "github.com/Genentech/exohub/go/exo/commandutil"
)

var command = commandutil.Command

const longText = `Inspect annex logs and repair bad files.

Modes:
  --scan-bad [--print]                          Scan logs for bad files and record them
  --deep [<path>] [--remote NAME]               Run git-annex fsck on bad files or a path
  --path <path>... [--remote NAME] [--manifest] Verify annexed paths
  --mark-bad [<path>] [--remote NAME]           Mark bad files in git-annex
  --drop-bad [<path>] --remote NAME             Drop bad files from a remote
  --show-bad                                    Show stored bad file paths
  --clear-bad [--no-confirm]                    Clear stored bad file paths`

type stringList []string

func (s *stringList) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		if strings.TrimSpace(node.Value) == "" {
			return nil
		}
		*s = append(*s, strings.TrimSpace(node.Value))
		return nil
	case yaml.SequenceNode:
		for _, item := range node.Content {
			if item.Kind != yaml.ScalarNode {
				return fmt.Errorf("unsupported list value: %v", item.Kind)
			}
			if strings.TrimSpace(item.Value) != "" {
				*s = append(*s, strings.TrimSpace(item.Value))
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported yaml kind: %v", node.Kind)
	}
}

type manifest struct {
	RepoDir       string     `yaml:"repo_dir"`
	RepoDirHyphen string     `yaml:"repo-dir"`
	Path          stringList `yaml:"path"`
	Paths         stringList `yaml:"paths"`
}

type repoInfo struct {
	root   string
	gitDir string
}

var repo *repoInfo

func (m manifest) allPaths() []string {
	out := append([]string{}, m.Path...)
	out = append(out, m.Paths...)
	return out
}

func (m manifest) repoDir() string {
	if m.RepoDir != "" {
		return m.RepoDir
	}
	return m.RepoDirHyphen
}

type cliOptions struct {
	modeScanBad  bool
	modePrint    bool
	modeShow     bool
	modeDeep     bool
	modeMarkBad  bool
	modeDropBad  bool
	modeClearBad bool
	noConfirm    bool
	deepRemote   string
	manifestPath string
	pathMode     bool
	jobs         string
}

func NewCommand() *cobra.Command {
	var opts cliOptions
	var jobsFlag string

	// Default jobs from EXOHUB_JOBS env var, else 1
	defaultJobs := strings.TrimSpace(os.Getenv("EXOHUB_JOBS"))
	if defaultJobs == "" {
		defaultJobs = "1"
	}
	opts.jobs = defaultJobs

	rootCmd := &cobra.Command{
		Use:   "fsck",
		Short: "Inspect annex logs and repair bad files",
		Long:  longText,
		Args:  cobra.ArbitraryArgs,
		Run: func(cmd *cobra.Command, args []string) {
			// CLI flag overrides env default
			if jobsFlag != "" {
				opts.jobs = jobsFlag
			}
			run(opts, args)
		},
	}

	rootCmd.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		fmt.Fprintln(os.Stderr, err.Error())
		_ = cmd.Help()
		os.Exit(1)
		return nil
	})

	rootCmd.Flags().BoolVar(&opts.modeScanBad, "scan-bad", false, "Scan logs for bad files and record them")
	rootCmd.Flags().BoolVar(&opts.modePrint, "print", false, "Print bad file paths without recording them")
	rootCmd.Flags().BoolVar(&opts.modeShow, "show-bad", false, "Show stored bad file paths")
	rootCmd.Flags().BoolVar(&opts.modeClearBad, "clear-bad", false, "Clear stored bad file paths")
	rootCmd.Flags().BoolVar(&opts.modeDeep, "deep", false, "Run git-annex fsck on bad files or a single path")
	rootCmd.Flags().BoolVar(&opts.pathMode, "path", false, "Verify annexed paths (provide paths as arguments)")
	rootCmd.Flags().StringVar(&opts.manifestPath, "manifest", "", "Path to manifest YAML")
	rootCmd.Flags().BoolVar(&opts.modeMarkBad, "mark-bad", false, "Mark bad files in git-annex")
	rootCmd.Flags().BoolVar(&opts.modeDropBad, "drop-bad", false, "Drop bad files from a remote")
	rootCmd.Flags().BoolVar(&opts.noConfirm, "no-confirm", false, "Skip confirmation prompt")
	rootCmd.Flags().StringVar(&opts.deepRemote, "remote", "", "Annex remote")
	rootCmd.Flags().StringVarP(&jobsFlag, "jobs", "J", "", "Number of parallel jobs (default: $EXOHUB_JOBS or 1)")

	return rootCmd
}

func run(opts cliOptions, args []string) {
	if len(os.Args) == 1 {
		usage()
	}

	modeVerify := opts.pathMode || opts.manifestPath != ""
	var deepPath string
	var markPath string
	var dropPath string
	var paths []string
	var pathPatterns []string

	if opts.pathMode && len(args) == 0 {
		fail("Missing value after --path")
	}

	if len(args) > 0 {
		switch {
		case opts.pathMode:
			pathPatterns = append(pathPatterns, args...)
		case opts.modeMarkBad:
			if len(args) > 1 {
				fmt.Fprintln(os.Stderr, "Too many arguments for --mark-bad")
				usage()
			}
			markPath = args[0]
		case opts.modeDropBad:
			if len(args) > 1 {
				fmt.Fprintln(os.Stderr, "Too many arguments for --drop-bad")
				usage()
			}
			dropPath = args[0]
		case opts.modeDeep:
			if len(args) > 1 {
				fmt.Fprintln(os.Stderr, "Too many arguments for --deep")
				usage()
			}
			deepPath = args[0]
		default:
			fmt.Fprintf(os.Stderr, "Unknown argument: %s\n", args[0])
			usage()
		}
	}

	if !opts.modeScanBad && !opts.modeShow && !opts.modeDeep && !opts.modeMarkBad && !opts.modeDropBad && !opts.modeClearBad && !modeVerify {
		usage()
	}

	if modeVerify {
		if opts.modeScanBad || opts.modeShow || opts.modeDeep || opts.modeMarkBad || opts.modeDropBad || opts.modeClearBad {
			fmt.Fprintln(os.Stderr, "Options --path/--manifest cannot be combined with other modes")
			os.Exit(2)
		}
	}

	if opts.modeScanBad || opts.modeDeep || opts.modeMarkBad || opts.modeDropBad || modeVerify {
		if err := requireBins("git-annex"); err != nil {
			os.Exit(127)
		}
	}

	if modeVerify {
		if opts.manifestPath != "" {
			if err := verifyManifest(opts.manifestPath); err != nil {
				fmt.Fprintln(os.Stderr, err.Error())
				os.Exit(1)
			}
			m, err := parseManifest(opts.manifestPath)
			if err != nil {
				fmt.Fprintln(os.Stderr, err.Error())
				os.Exit(1)
			}
			paths = append(paths, m.allPaths()...)
			if repoDir := m.repoDir(); repoDir != "" {
				if err := os.Chdir(repoDir); err != nil {
					fmt.Fprintf(os.Stderr, "Failed to enter repo dir: %s\n", repoDir)
					os.Exit(1)
				}
			}
		}

		if err := ensureGitRepo(); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}

		if len(pathPatterns) > 0 {
			paths = append(paths, expandPaths(pathPatterns)...)
		}
		paths = normalizePaths(paths)

		if len(paths) == 0 {
			fmt.Fprintln(os.Stderr, "No paths provided via --path or --manifest")
			os.Exit(1)
		}

		if opts.deepRemote != "" {
			if !annexRemoteEnabled(opts.deepRemote) {
				fmt.Fprintf(os.Stderr, "Annex remote '%s' is not enabled\n", opts.deepRemote)
				os.Exit(1)
			}
			fmt.Printf("fsck (remote=%s): %d path(s)\n", opts.deepRemote, len(paths))
			missing, err := runCommandOutput(append([]string{"git", "annex", "find", "--not", "--in", opts.deepRemote, "--"}, paths...))
			if err == nil && strings.TrimSpace(missing) != "" {
				fmt.Fprintf(os.Stderr, "Missing in remote '%s':\n", opts.deepRemote)
				fmt.Fprint(os.Stderr, missing)
				os.Exit(1)
			}
			exit(runCommand(append([]string{"git", "annex", "fsck", "--jobs", opts.jobs, "--from", opts.deepRemote, "--"}, paths...)))
		}

		fmt.Printf("fsck (remote=here): %d path(s)\n", len(paths))
		missing, err := runCommandOutput(append([]string{"git", "annex", "find", "--not", "--in=here", "--"}, paths...))
		if err == nil && strings.TrimSpace(missing) != "" {
			fmt.Fprintln(os.Stderr, "Missing in local annex 'here':")
			fmt.Fprint(os.Stderr, missing)
			os.Exit(1)
		}
		exit(runCommand(append([]string{"git", "annex", "fsck", "--jobs", opts.jobs, "--"}, paths...)))
	}

	if opts.modeMarkBad {
		if err := ensureGitRepo(); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
		if markPath != "" {
			markPath = normalizePath(markPath)
		}
		fmt.Fprintln(os.Stderr, "mark-bad: resolving annex remote")
		if opts.deepRemote == "" {
			var err error
			opts.deepRemote, err = pickEnabledRemote()
			if err != nil {
				fmt.Fprintln(os.Stderr, "No enabled git-annex remote found")
				os.Exit(1)
			}
		} else if !annexRemoteEnabled(opts.deepRemote) {
			fmt.Fprintf(os.Stderr, "Annex remote '%s' is not enabled\n", opts.deepRemote)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "mark-bad: using remote '%s'\n", opts.deepRemote)
		fmt.Fprintln(os.Stderr, "mark-bad: resolving remote UUID")
		uuid := remoteUUID(opts.deepRemote)
		if uuid == "" {
			fmt.Fprintf(os.Stderr, "Failed to resolve UUID for remote '%s'\n", opts.deepRemote)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "mark-bad: remote UUID '%s'\n", uuid)
		markCount := 0
		var markPaths []string
		if markPath != "" {
			markCount = 1
			markPaths = append(markPaths, markPath)
		} else {
			lines, err := readLines(repoGitPath("exohub", "bad"))
			if err != nil {
				fmt.Fprintln(os.Stderr, "No bad file list found at .git/exohub/bad")
				os.Exit(1)
			}
			for _, line := range lines {
				if strings.TrimSpace(line) != "" {
					markCount++
					markPaths = append(markPaths, line)
				}
			}
		}

		if !opts.noConfirm {
			fmt.Printf("About to mark %d path(s) as bad on remote '%s'. Proceed? [y/N] ", markCount, opts.deepRemote)
			if !readYes() {
				fmt.Fprintln(os.Stderr, "Aborted.")
				os.Exit(1)
			}
		}

		if len(markPaths) == 0 {
			os.Exit(1)
		}

		failed := false
		for _, p := range markPaths {
			if !pathExists(p) {
				fmt.Fprintf(os.Stderr, "Path not found: %s\n", p)
				failed = true
				continue
			}
			key, err := runCommandOutput([]string{"git", "annex", "lookupkey", "--", p})
			if err != nil || strings.TrimSpace(key) == "" {
				fmt.Fprintf(os.Stderr, "No annex key found for path: %s\n", p)
				failed = true
				continue
			}
			fmt.Fprintf(os.Stdout, "mark-bad (remote=%s): %s\n", opts.deepRemote, p)
			if err := appendBadKey(uuid, strings.TrimSpace(key)); err != nil {
				fmt.Fprintln(os.Stderr, err.Error())
				failed = true
			}
		}
		if failed {
			os.Exit(1)
		}
		os.Exit(0)
	}

	if opts.modeDropBad {
		if err := ensureGitRepo(); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
		if dropPath != "" {
			dropPath = normalizePath(dropPath)
		}
		if opts.deepRemote == "" {
			fmt.Fprintln(os.Stderr, "--drop-bad requires --remote <annex-remote>")
			os.Exit(2)
		}
		if !annexRemoteEnabled(opts.deepRemote) {
			fmt.Fprintf(os.Stderr, "Annex remote '%s' is not enabled\n", opts.deepRemote)
			os.Exit(1)
		}
		dropCount := 0
		var dropPaths []string
		if dropPath != "" {
			dropCount = 1
			dropPaths = append(dropPaths, dropPath)
		} else {
			lines, err := readLines(repoGitPath("exohub", "bad"))
			if err != nil {
				fmt.Fprintln(os.Stderr, "No bad file list found at .git/exohub/bad")
				os.Exit(1)
			}
			for _, line := range lines {
				if strings.TrimSpace(line) != "" {
					dropCount++
					dropPaths = append(dropPaths, line)
				}
			}
		}

		if !opts.noConfirm {
			fmt.Printf("About to drop %d path(s) from remote '%s'. Proceed? [y/N] ", dropCount, opts.deepRemote)
			if !readYes() {
				fmt.Fprintln(os.Stderr, "Aborted.")
				os.Exit(1)
			}
		}

		failed := false
		for _, p := range dropPaths {
			if !pathExists(p) {
				fmt.Fprintf(os.Stderr, "Path not found: %s\n", p)
				failed = true
				continue
			}
			fmt.Fprintf(os.Stdout, "drop-bad (remote=%s): %s\n", opts.deepRemote, p)
			if err := runCommand([]string{"git", "annex", "drop", "--force", "--from", opts.deepRemote, "--", p}); err != nil {
				failed = true
			}
		}
		if failed {
			os.Exit(1)
		}
		os.Exit(0)
	}

	if opts.modeClearBad {
		if err := ensureGitRepo(); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
		if !opts.noConfirm {
			fmt.Printf("About to remove .git/exohub/bad. Proceed? [y/N] ")
			if !readYes() {
				fmt.Fprintln(os.Stderr, "Aborted.")
				os.Exit(1)
			}
		}
		_ = os.Remove(repoGitPath("exohub", "bad"))
		os.Exit(0)
	}

	if opts.modeDeep && !opts.modeScanBad && !opts.modeShow {
		if err := ensureGitRepo(); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
		if opts.deepRemote == "" {
			var err error
			opts.deepRemote, err = pickEnabledRemote()
			if err != nil {
				fmt.Fprintln(os.Stderr, "No enabled git-annex remote found")
				os.Exit(1)
			}
		} else if !annexRemoteEnabled(opts.deepRemote) {
			fmt.Fprintf(os.Stderr, "Annex remote '%s' is not enabled\n", opts.deepRemote)
			os.Exit(1)
		}
		if deepPath != "" {
			deepPath = normalizePath(deepPath)
			if !pathExists(deepPath) {
				fmt.Fprintf(os.Stderr, "Path not found: %s\n", deepPath)
				os.Exit(1)
			}
			fmt.Printf("fsck (remote=%s): %s\n", opts.deepRemote, deepPath)
			exit(runCommand([]string{"git", "annex", "fsck", "--jobs", opts.jobs, "--from", opts.deepRemote, "--", deepPath}))
		}
		lines, err := readLines(repoGitPath("exohub", "bad"))
		if err != nil {
			fmt.Fprintln(os.Stderr, "No bad file list found at .git/exohub/bad")
			os.Exit(1)
		}
		failed := false
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			fmt.Printf("fsck (remote=%s): %s\n", opts.deepRemote, line)
			if err := runCommand([]string{"git", "annex", "fsck", "--jobs", opts.jobs, "--from", opts.deepRemote, "--", line}); err != nil {
				failed = true
			}
		}
		if failed {
			os.Exit(1)
		}
		os.Exit(0)
	}

	if opts.modeShow && !opts.modeScanBad {
		if err := ensureGitRepo(); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
		if content, err := os.ReadFile(repoGitPath("exohub", "bad")); err == nil {
			fmt.Print(string(content))
		}
		os.Exit(0)
	}

	logs, err := collectLogs(opts.modePrint)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
	if len(logs) == 0 {
		fmt.Fprintln(os.Stderr, "No logs found under .git/exohub/logs")
		os.Exit(0)
	}

	pathsFound := map[string]struct{}{}
	for _, logPath := range logs {
		for _, p := range parseLog(logPath) {
			pathsFound[p] = struct{}{}
		}
	}

	var results []string
	for p := range pathsFound {
		results = append(results, p)
	}
	sortStrings(results)

	if opts.modePrint {
		for _, p := range results {
			fmt.Fprintln(os.Stdout, p)
		}
	} else {
		if opts.modeScanBad {
			fmt.Fprintf(os.Stderr, "Found %d invalid file(s)\n", len(results))
		}
		for _, p := range results {
			if err := appendBadPath(p); err != nil {
				fmt.Fprintln(os.Stderr, err.Error())
			}
		}
	}

	if opts.modeShow {
		if content, err := os.ReadFile(repoGitPath("exohub", "bad")); err == nil {
			fmt.Print(string(content))
		}
	}
}

func usage() {
	fmt.Fprint(os.Stdout, longText)
	os.Exit(1)
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	usage()
}

func readYes() bool {
	reader := bufio.NewReader(os.Stdin)
	resp, _ := reader.ReadString('\n')
	resp = strings.TrimSpace(resp)
	return strings.EqualFold(resp, "y")
}

func requireBins(names ...string) error {
	missing := false
	for _, name := range names {
		if !commandExists(name) {
			fmt.Fprintf(os.Stderr, "Required binary '%s' not found in PATH\n", name)
			missing = true
		}
	}
	if missing {
		return errors.New("missing binaries")
	}
	return nil
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func ensureGitRepo() error {
	if repo != nil {
		return nil
	}
	root, err := gitRevParse("--show-toplevel")
	if err != nil {
		return errors.New("Current directory is not a git repository")
	}
	gitDir, err := gitRevParse("--absolute-git-dir")
	if err != nil {
		return errors.New("Current directory is not a git repository")
	}
	repo = &repoInfo{
		root:   strings.TrimSpace(root),
		gitDir: strings.TrimSpace(gitDir),
	}
	return nil
}

func annexRemoteEnabled(name string) bool {
	cmd := command("git", "config", "--get", fmt.Sprintf("remote.%s.annex-uuid", name))
	if repo != nil {
		cmd.Dir = repo.root
	}
	return cmd.Run() == nil
}

func verifyManifest(path string) error {
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("Manifest not found: %s", path)
	}
	return manifestutil.ValidateManifestFile("sync", path)
}

func parseManifest(path string) (manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return manifest{}, err
	}
	var m manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return manifest{}, err
	}
	if repoDir := m.repoDir(); repoDir != "" {
		if info, err := os.Stat(repoDir); err != nil || !info.IsDir() {
			return manifest{}, fmt.Errorf("Repo directory does not exist: %s", repoDir)
		}
	}
	return m, nil
}

func expandPaths(patterns []string) []string {
	var out []string
	for _, pat := range patterns {
		matches, err := filepath.Glob(pat)
		if err != nil || len(matches) == 0 {
			out = append(out, pat)
			continue
		}
		out = append(out, matches...)
	}
	return out
}

func collectLogs(allowNoRepo bool) ([]string, error) {
	stdinIsTTY := isCharDevice(os.Stdin)
	if stdinIsTTY {
		if err := ensureGitRepo(); err != nil {
			return nil, err
		}
		ensureExohubDirs()
		logs, _ := filepath.Glob(filepath.Join(repoGitPath("exohub", "logs"), "*.log"))
		return logs, nil
	}

	if !isGitRepo() && !allowNoRepo {
		return nil, errors.New("Not in a git repository; use --print when piping logs")
	}
	if isGitRepo() {
		ensureExohubDirs()
	}
	return []string{"-"}, nil
}

func isGitRepo() bool {
	return ensureGitRepo() == nil
}

func ensureExohubDirs() {
	_ = os.MkdirAll(repoGitPath("exohub", "logs"), 0o755)
}

func parseLog(path string) []string {
	var lines []string
	if path == "-" {
		lines = readAllLines(os.Stdin)
	} else {
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()
		lines = readAllLines(f)
	}

	var out []string
	reErr := regexp.MustCompile(`(?i)(checksum|hash|verification)`)
	reBad := regexp.MustCompile(`(?i)(mismatch|fail|failed|bad|corrupt|corrupted)`)
	reKey := regexp.MustCompile(`[A-Z0-9]+E?-s[0-9]+--[A-Za-z0-9]{16,}`)
	reGet := regexp.MustCompile(`^[[:space:]]*get[[:space:]]+([^[:space:]]+)[[:space:]]*[(]from[[:space:]]+[^)]*[)]`)
	reAnsi := regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

	for i := 0; i < len(lines); i++ {
		line := stripLine(lines[i], reAnsi)
		if matches := reGet.FindStringSubmatch(line); len(matches) == 2 {
			if i+1 < len(lines) {
				nextLine := stripLine(lines[i+1], reAnsi)
				if reErr.MatchString(nextLine) && reBad.MatchString(nextLine) {
					out = append(out, matches[1])
					continue
				}
			}
		}

		if reErr.MatchString(line) && reBad.MatchString(line) {
			out = append(out, extractQuoted(line)...)
			keys := uniqueStrings(reKey.FindAllString(line, -1))
			for _, key := range keys {
				out = append(out, findFilesForKey(key)...)
			}
		}
	}

	return uniqueStrings(out)
}

func stripLine(line string, reAnsi *regexp.Regexp) string {
	line = strings.TrimRight(line, "\r\n")
	line = reAnsi.ReplaceAllString(line, "")
	return line
}

func extractQuoted(line string) []string {
	var out []string
	reSingle := regexp.MustCompile(`'([^']+)'`)
	reDouble := regexp.MustCompile(`"([^"]+)"`)
	for _, m := range reSingle.FindAllStringSubmatch(line, -1) {
		if len(m) == 2 && m[1] != "" {
			out = append(out, m[1])
		}
	}
	for _, m := range reDouble.FindAllStringSubmatch(line, -1) {
		if len(m) == 2 && m[1] != "" {
			out = append(out, m[1])
		}
	}
	return out
}

func findFilesForKey(key string) []string {
	if repo == nil {
		return nil
	}
	output, err := runCommandOutputRaw([]string{"git", "annex", "find", "--json", "--key", key})
	if err == nil && len(output) > 0 {
		var files []string
		for _, line := range strings.Split(string(output), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var payload struct {
				File string `json:"file"`
			}
			if json.Unmarshal([]byte(line), &payload) == nil {
				if payload.File != "" {
					files = append(files, payload.File)
				}
			}
		}
		if len(files) > 0 {
			return files
		}
	}

	output, err = runCommandOutputRaw([]string{"git", "annex", "find", "--key", key})
	if err != nil {
		return nil
	}
	var files []string
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			files = append(files, line)
		}
	}
	return files
}

func readAllLines(r io.Reader) []string {
	var lines []string
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines
}

func appendBadPath(path string) error {
	ensureExohubDirs()
	if repo != nil {
		path = normalizePath(path)
	}
	lock, err := os.OpenFile(repoGitPath("exohub", "bad.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer func() {
		_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	}()

	existing := map[string]struct{}{}
	if content, err := os.ReadFile(repoGitPath("exohub", "bad")); err == nil {
		for _, line := range strings.Split(string(content), "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				existing[line] = struct{}{}
			}
		}
	}
	if _, ok := existing[path]; ok {
		return nil
	}
	f, err := os.OpenFile(repoGitPath("exohub", "bad"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintln(f, path)
	return err
}

func appendBadKey(uuid, key string) error {
	if err := os.MkdirAll(repoGitPath("annex"), 0o755); err != nil {
		return err
	}
	lock, err := os.OpenFile(repoGitPath("annex", "bad.log.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer func() {
		_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	}()
	f, err := os.OpenFile(repoGitPath("annex", "bad.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "%s %s\n", uuid, key)
	return err
}

func pickEnabledRemote() (string, error) {
	output, err := runCommandOutputRaw([]string{"git", "config", "--get-regexp", "^remote\\..*\\.annex-uuid$"})
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) == 0 {
			continue
		}
		key := parts[0]
		if strings.HasPrefix(key, "remote.") && strings.HasSuffix(key, ".annex-uuid") {
			name := strings.TrimPrefix(key, "remote.")
			name = strings.TrimSuffix(name, ".annex-uuid")
			if name != "here" && name != "" {
				return name, nil
			}
		}
	}
	return "", errors.New("no remotes")
}

func remoteUUID(name string) string {
	output, err := runCommandOutputRaw([]string{"git", "config", "--get", fmt.Sprintf("remote.%s.annex-uuid", name)})
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func runCommand(args []string) error {
	cmd := command(args[0], args[1:]...)
	if repo != nil && args[0] == "git" {
		cmd.Dir = repo.root
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func runCommandOutput(args []string) (string, error) {
	cmd := command(args[0], args[1:]...)
	if repo != nil && args[0] == "git" {
		cmd.Dir = repo.root
	}
	out, err := cmd.Output()
	return string(out), err
}

func runCommandOutputRaw(args []string) ([]byte, error) {
	cmd := command(args[0], args[1:]...)
	if repo != nil && args[0] == "git" {
		cmd.Dir = repo.root
	}
	return cmd.Output()
}

func exit(err error) {
	if err == nil {
		os.Exit(0)
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		os.Exit(exitErr.ExitCode())
	}
	os.Exit(1)
}

func pathExists(path string) bool {
	if repo != nil && !filepath.IsAbs(path) {
		path = filepath.Join(repo.root, path)
	}
	if _, err := os.Stat(path); err == nil {
		return true
	}
	if _, err := os.Lstat(path); err == nil {
		return true
	}
	return false
}

func readLines(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines, nil
}

func uniqueStrings(input []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, item := range input {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

func sortStrings(input []string) {
	if len(input) < 2 {
		return
	}
	sort.Slice(input, func(i, j int) bool { return input[i] < input[j] })
}

func isCharDevice(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

func gitRevParse(arg string) (string, error) {
	cmd := command("git", "rev-parse", arg)
	out, err := cmd.Output()
	return string(out), err
}

func repoGitPath(parts ...string) string {
	base := ".git"
	if repo != nil && repo.gitDir != "" {
		base = repo.gitDir
	}
	return filepath.Join(append([]string{base}, parts...)...)
}

func normalizePath(path string) string {
	if path == "" {
		return path
	}
	if repo == nil || repo.root == "" {
		return path
	}
	cwd, err := os.Getwd()
	if err != nil {
		return path
	}
	abs := path
	if !filepath.IsAbs(path) {
		abs = filepath.Join(cwd, path)
	}
	rel, err := filepath.Rel(repo.root, abs)
	if err != nil {
		return path
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return abs
	}
	if rel == "." {
		return "."
	}
	return rel
}

func normalizePaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if p == "" {
			continue
		}
		out = append(out, normalizePath(p))
	}
	return out
}
