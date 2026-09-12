package link

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
)

const longText = `Reconnect git-annex symlinks to an external store of annex key files.

Examples:
  exo link -s /mnt/annex-store
  exo link -s /mnt/annex-store -r /path/to/repo --mode auto -v`

type options struct {
	store        string
	repo         string
	mode         string
	paths        []string
	dryRun       bool
	force        bool
	strict       bool
	noIndex      bool
	cacheFile    string
	refreshCache bool
	keys         []string
	keyFile      string
	mapOut       string
	verbose      bool
}

func NewCommand() *cobra.Command {
	var opts options
	rootCmd := &cobra.Command{
		Use:   "link",
		Short: "Reconnect git-annex symlinks to an external store of annex key files",
		Long:  longText,
		Run: func(cmd *cobra.Command, args []string) {
			run(opts, args)
		},
	}

	rootCmd.Flags().StringVarP(&opts.store, "store", "s", "", "Path to directory containing annex key files")
	rootCmd.Flags().StringVarP(&opts.repo, "repo", "r", "", "Path to git-annex repo")
	rootCmd.Flags().StringVarP(&opts.mode, "mode", "m", "hardlink", "One of: auto | hardlink | symlink | copy")
	rootCmd.Flags().StringArrayVarP(&opts.paths, "path", "p", nil, "Path within repo to limit scan")
	rootCmd.Flags().BoolVarP(&opts.dryRun, "dry-run", "n", false, "Show actions without making changes")
	rootCmd.Flags().BoolVarP(&opts.force, "force", "f", false, "Overwrite existing targets instead of skipping")
	rootCmd.Flags().BoolVar(&opts.strict, "strict", false, "Exit non-zero if any objects are missing")
	rootCmd.Flags().BoolVar(&opts.noIndex, "no-index", false, "Skip pre-indexing store; do on-demand lookups")
	rootCmd.Flags().StringVar(&opts.cacheFile, "cache", "", "Load/save a basename->path index to this file")
	rootCmd.Flags().BoolVar(&opts.refreshCache, "refresh-cache", false, "Rebuild cache from store even if cache file exists")
	rootCmd.Flags().StringArrayVarP(&opts.keys, "key", "k", nil, "Query mapping for this annex key basename")
	rootCmd.Flags().StringVar(&opts.keyFile, "key-file", "", "Read annex keys (one per line) to query")
	rootCmd.Flags().StringVar(&opts.mapOut, "map-out", "", "Write TSV of annex_key_basename->repo_path for all symlinks")
	rootCmd.Flags().BoolVarP(&opts.verbose, "verbose", "v", false, "Increase logging")

	return rootCmd
}

func run(opts options, args []string) {
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

	if opts.cacheFile == "" {
		opts.cacheFile = filepath.Join(opts.repo, ".git", "exohub", "annex-index.tsv")
	}

	if len(args) > 0 {
		fmt.Fprintf(os.Stderr, "Unknown option: %s\n", args[0])
		printUsageStderr()
		os.Exit(2)
	}

	lookupOnly := len(opts.keys) > 0 || opts.keyFile != "" || opts.mapOut != ""

	if opts.store == "" && !lookupOnly {
		fmt.Fprintln(os.Stderr, "Error: --store is required")
		printUsageStderr()
		os.Exit(2)
	}

	storeAbs := ""
	if opts.store != "" {
		storeAbs, err = filepath.Abs(opts.store)
		if err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
	}

	if !isDir(filepath.Join(opts.repo, ".git")) {
		fmt.Fprintf(os.Stderr, "Error: '%s' does not look like a git repository (missing .git)\n", opts.repo)
		os.Exit(1)
	}

	if !lookupOnly && !isDir(filepath.Join(opts.repo, ".git", "annex")) {
		if opts.dryRun {
			logf(opts, "Would create directory: %s", filepath.Join(opts.repo, ".git", "annex"))
		} else {
			if err := os.MkdirAll(filepath.Join(opts.repo, ".git", "annex"), 0o755); err != nil {
				fmt.Fprintln(os.Stderr, err.Error())
				os.Exit(1)
			}
		}
	}

	if !lookupOnly {
		if storeAbs == "" || !isDir(storeAbs) {
			fmt.Fprintf(os.Stderr, "Error: store directory not found: %s\n", storeAbsOrUnset(storeAbs))
			os.Exit(1)
		}
	}

	opts.store = storeAbs

	if err := validateMode(opts.mode); err != nil {
		fmt.Fprintf(os.Stderr, "Error: invalid --mode '%s' (use auto|hardlink|symlink|copy)\n", opts.mode)
		os.Exit(2)
	}

	logf(opts, "Repo:  %s", opts.repo)
	logf(opts, "Store: %s", opts.store)
	logf(opts, "Mode:  %s%s%s%s%s%s%s", opts.mode,
		flagTag(opts.dryRun, " (dry-run)"),
		flagTag(opts.verbose, ", verbose"),
		flagTag(opts.noIndex, ", no-index"),
		flagTag(opts.cacheFile != "", fmt.Sprintf(", cache=%s", opts.cacheFile)),
		flagTag(opts.refreshCache, ", refresh-cache"),
		flagTag(lookupOnly, ", lookup"),
	)

	searchPaths := buildSearchPaths(opts, opts.repo)
	allLinks := collectSymlinks(searchPaths, opts.repo, opts.verbose)

	storeIndex := map[string]string{}
	if opts.cacheFile != "" && fileExists(opts.cacheFile) && !opts.refreshCache {
		if err := loadCache(opts.cacheFile, storeIndex); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
	}

	if lookupOnly {
		runLookupOnly(opts, allLinks)
		return
	}

	if shouldIndexStore(opts) {
		if opts.noIndex && !opts.refreshCache {
			vlogf(opts, "Skipping store index; using on-demand lookups%s.", cacheSuffix(opts.cacheFile))
		} else {
			if err := indexStore(opts, storeIndex); err != nil {
				fmt.Fprintln(os.Stderr, err.Error())
				os.Exit(1)
			}
		}
	} else {
		vlogf(opts, "Skipping store index; using on-demand lookups%s.", cacheSuffix(opts.cacheFile))
	}

	results := linkSymlinks(opts, allLinks, storeIndex)
	logf(opts, "Symlinks scanned: %d", results.found)
	logf(opts, "Linked objects:   %d", results.linked)
	logf(opts, "Missing in store: %d", results.missing)
	logf(opts, "Skipped:          %d", results.skipped)

	if results.missing > 0 {
		logf(opts, "Some annex objects were not found in the store. Ensure your store contains files named exactly as the annex keys (basenames like MD5E-... or SHA256E-...).")
	}

	if opts.strict && results.missing > 0 {
		os.Exit(3)
	}
}

func printUsageStderr() {
	fmt.Fprint(os.Stderr, longText)
}

func validateMode(mode string) error {
	switch mode {
	case "auto", "hardlink", "symlink", "copy":
		return nil
	default:
		return errors.New("invalid mode")
	}
}

func flagTag(ok bool, tag string) string {
	if ok {
		return tag
	}
	return ""
}

func cacheSuffix(cacheFile string) string {
	if cacheFile == "" {
		return ""
	}
	return " (plus cache if loaded)"
}

func storeAbsOrUnset(store string) string {
	if store == "" {
		return "<unset>"
	}
	return store
}

func logf(opts options, format string, args ...any) {
	fmt.Printf("[exo-link] %s\n", fmt.Sprintf(format, args...))
}

func vlogf(opts options, format string, args ...any) {
	if opts.verbose {
		logf(opts, format, args...)
	}
}

func buildSearchPaths(opts options, repo string) []string {
	var out []string
	if len(opts.paths) == 0 {
		out = append(out, repo)
		return out
	}
	for _, p := range opts.paths {
		if !filepath.IsAbs(p) {
			p = filepath.Join(repo, p)
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			vlogf(opts, "Path not found, skipping: %s", p)
			continue
		}
		if !fileExists(abs) {
			vlogf(opts, "Path not found, skipping: %s", abs)
			continue
		}
		out = append(out, abs)
	}
	return out
}

func collectSymlinks(paths []string, repo string, verbose bool) []string {
	var out []string
	repoGit := filepath.Join(repo, ".git")
	for _, root := range paths {
		info, err := os.Lstat(root)
		if err != nil {
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			if !isUnderGit(root, repoGit) {
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
			if isUnderGit(path, repoGit) {
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
	if verbose {
		fmt.Printf("[exo-link] Scanning repo symlinks...\n")
	}
	return out
}

func isUnderGit(path, gitDir string) bool {
	if path == gitDir {
		return true
	}
	prefix := gitDir + string(os.PathSeparator)
	return strings.HasPrefix(path, prefix)
}

func statDevice(info os.FileInfo) (uint64, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return uint64(stat.Dev), true
}

func loadCache(path string, storeIndex map[string]string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		base := strings.TrimSpace(parts[0])
		target := strings.TrimSpace(parts[1])
		if base == "" || target == "" {
			continue
		}
		if _, ok := storeIndex[base]; !ok {
			storeIndex[base] = target
		}
	}
	return scanner.Err()
}

func shouldIndexStore(opts options) bool {
	if opts.noIndex {
		return opts.refreshCache || (opts.cacheFile != "" && !fileExists(opts.cacheFile))
	}
	return true
}

func indexStore(opts options, storeIndex map[string]string) error {
	vlogf(opts, "Indexing store files (this may take time)...")

	var cacheWriter io.Writer
	if opts.cacheFile != "" {
		if opts.dryRun {
			logf(opts, "Would write cache: %s", opts.cacheFile)
		} else {
			if err := os.MkdirAll(filepath.Dir(opts.cacheFile), 0o755); err != nil {
				return err
			}
			f, err := os.Create(opts.cacheFile)
			if err != nil {
				return err
			}
			defer f.Close()
			cacheWriter = f
		}
	}

	return filepath.WalkDir(opts.store, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		base := filepath.Base(path)
		if base == "" {
			return nil
		}
		if _, ok := storeIndex[base]; ok {
			return nil
		}
		storeIndex[base] = path
		if cacheWriter != nil {
			_, _ = fmt.Fprintf(cacheWriter, "%s\t%s\n", base, path)
		}
		return nil
	})
}

type lookupResult struct {
	found   int
	linked  int
	missing int
	skipped int
}

func runLookupOnly(opts options, links []string) {
	keyMap := map[string][]string{}
	repoObjects := filepath.Join(opts.repo, ".git", "annex", "objects") + string(os.PathSeparator)
	for _, link := range links {
		target, err := resolveLinkTarget(link)
		if err != nil {
			continue
		}
		if !strings.HasPrefix(target, repoObjects) {
			continue
		}
		base := filepath.Base(target)
		if base == "" {
			continue
		}
		keyMap[base] = append(keyMap[base], link)
	}

	if opts.keyFile != "" {
		keys, err := readKeyFile(opts.keyFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
		opts.keys = append(opts.keys, keys...)
	}

	missingQ := 0
	if len(opts.keys) > 0 {
		for _, key := range opts.keys {
			paths := keyMap[key]
			if len(paths) == 0 {
				missingQ++
				logf(opts, "Key not found: %s", key)
				continue
			}
			for _, p := range paths {
				fmt.Printf("%s\t%s\n", key, p)
			}
		}
	}

	if opts.mapOut != "" {
		if opts.dryRun {
			logf(opts, "Would write map: %s", opts.mapOut)
		} else {
			if err := os.MkdirAll(filepath.Dir(opts.mapOut), 0o755); err != nil {
				fmt.Fprintln(os.Stderr, err.Error())
				os.Exit(1)
			}
			f, err := os.Create(opts.mapOut)
			if err != nil {
				fmt.Fprintln(os.Stderr, err.Error())
				os.Exit(1)
			}
			defer f.Close()
			for key, paths := range keyMap {
				for _, p := range paths {
					fmt.Fprintf(f, "%s\t%s\n", key, p)
				}
			}
		}
	}

	if opts.strict && missingQ > 0 {
		os.Exit(3)
	}
	os.Exit(0)
}

func readKeyFile(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out, scanner.Err()
}

func linkSymlinks(opts options, links []string, storeIndex map[string]string) lookupResult {
	results := lookupResult{found: len(links)}
	repoObjects := filepath.Join(opts.repo, ".git", "annex", "objects") + string(os.PathSeparator)

	for _, link := range links {
		target, err := resolveLinkTarget(link)
		if err != nil {
			results.skipped++
			continue
		}
		if !strings.HasPrefix(target, repoObjects) {
			results.skipped++
			continue
		}

		base := filepath.Base(target)
		src := storeIndex[base]
		if src == "" {
			found, err := findFirstMatch(opts.store, base)
			if err == nil && found != "" {
				src = found
				storeIndex[base] = found
				if opts.cacheFile != "" && !opts.dryRun {
					if err := appendCache(opts.cacheFile, base, found); err != nil {
						fmt.Fprintln(os.Stderr, err.Error())
					}
				}
			}
		}

		if !opts.dryRun {
			_ = os.MkdirAll(filepath.Dir(target), 0o755)
		}

		if fileExists(target) && !opts.force {
			results.skipped++
			vlogf(opts, "Exists (skip): %s", target)
			continue
		}

		actionDesc := ""
		doLink := func(mode string) error {
			switch mode {
			case "hardlink":
				actionDesc = fmt.Sprintf("ln %s -> %s", src, target)
				if opts.dryRun {
					return nil
				}
				if opts.force {
					_ = os.Remove(target)
				}
				return os.Link(src, target)
			case "symlink":
				actionDesc = fmt.Sprintf("ln -s %s -> %s", src, target)
				if opts.dryRun {
					return nil
				}
				if opts.force {
					_ = os.Remove(target)
				}
				return os.Symlink(src, target)
			case "copy":
				actionDesc = fmt.Sprintf("cp %s -> %s", src, target)
				if opts.dryRun {
					return nil
				}
				if opts.force {
					_ = os.Remove(target)
				}
				return copyFile(src, target)
			default:
				return errors.New("unknown mode")
			}
		}

		assembled := false
		switch opts.mode {
		case "hardlink":
			if src != "" && fileExists(src) {
				if err := doLink("hardlink"); err != nil {
					logf(opts, "Hardlink failed (cross-device?). Consider --mode auto/symlink/copy for: %s", target)
					results.skipped++
					continue
				}
			} else {
				if err := assembleFromChunks(opts, base, target); err != nil {
					results.missing++
					vlogf(opts, "Missing in store (no file or chunks): %s", base)
					continue
				}
				assembled = true
			}
		case "symlink":
			if src != "" && fileExists(src) {
				_ = doLink("symlink")
			} else {
				if err := assembleFromChunks(opts, base, target); err != nil {
					results.missing++
					vlogf(opts, "Missing in store (no file or chunks): %s", base)
					continue
				}
				assembled = true
			}
		case "copy":
			if src != "" && fileExists(src) {
				_ = doLink("copy")
			} else {
				if err := assembleFromChunks(opts, base, target); err != nil {
					results.missing++
					vlogf(opts, "Missing in store (no file or chunks): %s", base)
					continue
				}
				assembled = true
			}
		case "auto":
			if src != "" && fileExists(src) {
				if err := doLink("hardlink"); err != nil {
					_ = doLink("copy")
				}
			} else {
				if err := assembleFromChunks(opts, base, target); err != nil {
					results.missing++
					vlogf(opts, "Missing in store (no file or chunks): %s", base)
					continue
				}
				assembled = true
			}
		}

		results.linked++
		if opts.verbose {
			if assembled {
				vlogf(opts, "Linked: ")
			} else {
				vlogf(opts, "Linked: %s", actionDesc)
			}
		}
	}

	return results
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

func findFirstMatch(root, base string) (string, error) {
	var found string
	stop := errors.New("found")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Base(path) == base {
			found = path
			return stop
		}
		return nil
	})
	if err != nil && !errors.Is(err, stop) {
		return "", err
	}
	return found, nil
}

func appendCache(cacheFile, base, path string) error {
	if err := os.MkdirAll(filepath.Dir(cacheFile), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(cacheFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "%s\t%s\n", base, path)
	return err
}

func assembleFromChunks(opts options, targetBase, dst string) error {
	ext := ""
	if idx := strings.LastIndex(targetBase, "."); idx != -1 {
		ext = targetBase[idx:]
	}
	noExt := targetBase
	if ext != "" {
		noExt = strings.TrimSuffix(targetBase, ext)
	}
	parts := strings.SplitN(noExt, "--", 2)
	if len(parts) != 2 {
		return errors.New("no chunks")
	}
	prefix := parts[0]
	hash := parts[1]
	if prefix == "" || hash == "" {
		return errors.New("no chunks")
	}

	totalSize := ""
	reSize := regexp.MustCompile(`-s([0-9]+)`)
	if matches := reSize.FindStringSubmatch(prefix); len(matches) == 2 {
		totalSize = matches[1]
	}

	glob := fmt.Sprintf("%s-S*-C*--%s%s", prefix, hash, ext)
	chunks, err := findChunks(opts.store, glob)
	if err != nil || len(chunks) == 0 {
		return errors.New("no chunks")
	}

	sort.Slice(chunks, func(i, j int) bool {
		return chunks[i].num < chunks[j].num
	})

	sum := int64(0)
	for _, chunk := range chunks {
		if info, err := os.Stat(chunk.path); err == nil {
			sum += info.Size()
		}
	}

	if totalSize != "" {
		if expected, err := strconv.ParseInt(totalSize, 10, 64); err == nil && expected != sum {
			vlogf(opts, "Chunk total size (%d) differs from declared total (%d) for %s", sum, expected, targetBase)
		}
	}

	if opts.dryRun {
		logf(opts, "Would assemble from %d chunks -> %s", len(chunks), dst)
		return nil
	}

	tmp := fmt.Sprintf("%s.tmp.%d", dst, os.Getpid())
	tmpFile, err := os.Create(tmp)
	if err != nil {
		return err
	}
	for _, chunk := range chunks {
		if err := appendFile(tmpFile, chunk.path); err != nil {
			_ = tmpFile.Close()
			_ = os.Remove(tmp)
			return err
		}
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

type chunkInfo struct {
	num  int
	path string
}

func findChunks(root, glob string) ([]chunkInfo, error) {
	reChunk := regexp.MustCompile(`-C([0-9]+)--`)
	var out []chunkInfo
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		base := filepath.Base(path)
		match, err := filepath.Match(glob, base)
		if err != nil || !match {
			return nil
		}
		num := 0
		if m := reChunk.FindStringSubmatch(base); len(m) == 2 {
			if n, err := strconv.Atoi(m[1]); err == nil {
				num = n
			}
		}
		out = append(out, chunkInfo{num: num, path: path})
		return nil
	})
	return out, err
}

func appendFile(dst *os.File, path string) error {
	src, err := os.Open(path)
	if err != nil {
		return err
	}
	defer src.Close()
	_, err = io.Copy(dst, src)
	return err
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() {
		_ = out.Close()
	}()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

func fileExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
