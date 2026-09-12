package add

import (
	"bufio"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/dustin/go-humanize"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/Genentech/exohub/go/exo/commandutil"
)

const largeTextFileThreshold = 5 * 1024 * 1024 // 5 MB

var command = commandutil.Command

func NewCommand() *cobra.Command {
	var (
		annexMode bool
		gitMode   bool
	)

	cmd := &cobra.Command{
		Use:   "add [--annex|--git] [--force] [paths/URLs]...",
		Short: "Add files or URLs to tracking with auto content-type detection",
		Long: `Add files or URLs to git/git-annex tracking.

By default (auto mode), files are routed based on content type:
  - Text files (text/*, JSON, XML) → git add
  - Binary/data files             → git annex add
  - Directories                   → expanded, each file detected individually

Override with --annex (force git-annex) or --git (force git tracking).

Use --force with --annex or --git to migrate files that are already tracked:
  --annex --force  migrates git-tracked files to git-annex
  --git --force    migrates annexed files back to git

URLs (http://, https://, ftp://) are always added via git annex addurl.
By default, --preserve-filename is passed to git-annex, which instructs it
to use the filename provided by the server as-is (git-annex checks the
Content-Disposition response header automatically). If the URL has no
usable path component, the last path component of the URL is used as a
--file fallback.
Use --file to override the local filename (may include a subdirectory path).

Flags:
  -J, --jobs NUMBER  Number of parallel jobs for git-annex operations
                     (default: $EXOHUB_JOBS or 1)`,
		Example: `  exo add data/                           # auto-detect per file
  exo add --annex results.csv             # force annex tracking
  exo add --git README.md                 # force git tracking
  exo add --annex --force images/         # migrate git files to annex
  exo add --git --force config.yaml       # migrate annexed file back to git
  exo add https://example.com/data.csv   # add URL, use server filename
  exo add --file raw/data.csv https://example.com/data.csv  # override filename
  exo add -J 4 data/                      # parallel with 4 jobs`,
		DisableFlagParsing:    true,
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			mode, force, remaining := extractMode(args)
			locals, urls, flags := splitArgs(remaining)

			// If --help is in flags, show our help
			for _, f := range flags {
				if f == "--help" || f == "-h" {
					return cmd.Help()
				}
			}

			if force && mode == "auto" {
				return fmt.Errorf("--force requires --annex or --git to specify the migration direction")
			}

			jobsArgs := jobsFlags(flags)

			// Handle local files
			if len(locals) > 0 {
				switch mode {
				case "annex":
					if force {
						if err := migrateToAnnex(locals, jobsArgs, flags); err != nil {
							return err
						}
					} else {
						cmdArgs := []string{"annex", "add"}
						cmdArgs = append(cmdArgs, jobsArgs...)
						cmdArgs = append(cmdArgs, flags...)
						cmdArgs = append(cmdArgs, locals...)
						if err := execCommand("git", cmdArgs...); err != nil {
							return err
						}
						if err := reconcileExecBit(locals); err != nil {
							return err
						}
					}
				case "git":
					if force {
						if err := migrateToGit(locals, flags); err != nil {
							return err
						}
					} else {
						cmdArgs := []string{"add"}
						cmdArgs = append(cmdArgs, flags...)
						cmdArgs = append(cmdArgs, locals...)
						if err := execCommand("git", cmdArgs...); err != nil {
							return err
						}
					}
				default: // "auto"
					gitFiles, annexFiles, largeTextFiles := classifyFiles(locals)

					// Handle large text files: prompt or default to annex
					if len(largeTextFiles) > 0 {
						useAnnex := true
						if term.IsTerminal(int(os.Stdin.Fd())) {
							var err error
							useAnnex, err = confirmLargeTextFiles(largeTextFiles)
							if err != nil {
								return err
							}
						}
						if useAnnex {
							annexFiles = append(annexFiles, largeTextFiles...)
						} else {
							gitFiles = append(gitFiles, largeTextFiles...)
						}
					}

					if len(gitFiles) > 0 {
						cmdArgs := []string{"add"}
						cmdArgs = append(cmdArgs, flags...)
						cmdArgs = append(cmdArgs, gitFiles...)
						if err := execCommand("git", cmdArgs...); err != nil {
							return err
						}
					}
					if len(annexFiles) > 0 {
						cmdArgs := []string{"annex", "add"}
						cmdArgs = append(cmdArgs, jobsArgs...)
						cmdArgs = append(cmdArgs, flags...)
						cmdArgs = append(cmdArgs, annexFiles...)
						if err := execCommand("git", cmdArgs...); err != nil {
							return err
						}
						if err := reconcileExecBit(annexFiles); err != nil {
							return err
						}
					}
				}
			}

			// Run git annex addurl for each URL (regardless of mode)
			if len(urls) > 0 {
				for _, u := range urls {
					cmdArgs := buildAddURLArgs(u, jobsArgs, flags)
					if err := execCommand("git", cmdArgs...); err != nil {
						return err
					}
				}
			}

			// If no args at all, fall through to git annex add
			if len(locals) == 0 && len(urls) == 0 {
				cmdArgs := []string{"annex", "add"}
				cmdArgs = append(cmdArgs, jobsArgs...)
				cmdArgs = append(cmdArgs, flags...)
				if err := execCommand("git", cmdArgs...); err != nil {
					return err
				}
			}

			return nil
		},
	}

	_ = annexMode
	_ = gitMode

	return cmd
}

// extractMode extracts --annex, --git, and --force from args and returns the mode
// ("auto", "annex", or "git"), whether --force was set, and the remaining args.
func extractMode(args []string) (string, bool, []string) {
	mode := "auto"
	force := false
	var remaining []string
	for _, arg := range args {
		switch arg {
		case "--annex":
			mode = "annex"
		case "--git":
			mode = "git"
		case "--force":
			force = true
		default:
			remaining = append(remaining, arg)
		}
	}
	return mode, force, remaining
}

// classifyFiles takes a list of paths (files and/or directories), expands
// directories recursively, and classifies each file as text (git), binary (annex),
// or large text (text/* mimetype but exceeding largeTextFileThreshold).
func classifyFiles(paths []string) (gitFiles, annexFiles, largeTextFiles []string) {
	for _, f := range paths {
		info, err := os.Stat(f)
		if err != nil {
			annexFiles = append(annexFiles, f)
			continue
		}
		if info.IsDir() {
			_ = filepath.Walk(f, func(p string, fi os.FileInfo, err error) error {
				if err != nil || fi.IsDir() {
					return err
				}
				classifySingleFile(p, fi, &gitFiles, &annexFiles, &largeTextFiles)
				return nil
			})
		} else {
			classifySingleFile(f, info, &gitFiles, &annexFiles, &largeTextFiles)
		}
	}
	return
}

// classifySingleFile routes a single file into git, annex, or largeText buckets.
func classifySingleFile(path string, info os.FileInfo, gitFiles, annexFiles, largeTextFiles *[]string) {
	mime := detectMimeType(path)
	if mime == "" {
		// Could not detect — treat as binary
		*annexFiles = append(*annexFiles, path)
		return
	}
	isText := strings.HasPrefix(mime, "text/") || mime == "application/json" || mime == "application/xml"
	if !isText {
		*annexFiles = append(*annexFiles, path)
		return
	}
	// Only text/* gets the large-file guardrail (not JSON/XML)
	if strings.HasPrefix(mime, "text/") && info.Size() >= largeTextFileThreshold {
		*largeTextFiles = append(*largeTextFiles, path)
		return
	}
	*gitFiles = append(*gitFiles, path)
}

// confirmLargeTextFiles prompts the user to confirm whether large text files
// should be tracked with git-annex instead of git.
func confirmLargeTextFiles(files []string) (useAnnex bool, err error) {
	// Build a summary of the large files
	var lines []string
	for _, f := range files {
		info, _ := os.Stat(f)
		size := "unknown"
		if info != nil {
			size = humanize.IBytes(uint64(info.Size()))
		}
		lines = append(lines, fmt.Sprintf("  %s (%s)", f, size))
	}

	plural := "file is"
	if len(files) > 1 {
		plural = "files are"
	}
	fmt.Fprintf(os.Stderr, "Warning: %d %s detected as text but larger than %s:\n%s\n",
		len(files), plural, humanize.IBytes(largeTextFileThreshold), strings.Join(lines, "\n"))

	confirm := true
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("Use git-annex for these files? (recommended)").
				Affirmative("Yes, use git-annex").
				Negative("No, use git").
				Value(&confirm),
		),
	).WithTheme(commandutil.ExoTheme()).WithKeyMap(commandutil.ExoKeyMap())
	if err := form.Run(); err != nil {
		return false, err
	}
	return confirm, nil
}

// detectMimeType returns the MIME type of a file based on its first 512 bytes.
// Returns "" if the file cannot be read or is a directory.
func detectMimeType(filePath string) string {
	info, err := os.Stat(filePath)
	if err != nil || info.IsDir() {
		return ""
	}
	f, err := os.Open(filePath)
	if err != nil {
		return ""
	}
	defer f.Close()

	buf := make([]byte, 512)
	n, _ := f.Read(buf)
	if n == 0 {
		return "text/plain" // empty files are text
	}
	return http.DetectContentType(buf[:n])
}

// IsTextFile detects whether a file is text by reading its first 512 bytes
// and using Go's http.DetectContentType. Exported for use by MCP tools.
func IsTextFile(filePath string) bool {
	mime := detectMimeType(filePath)
	if mime == "" {
		return false
	}
	if strings.HasPrefix(mime, "text/") {
		return true
	}
	switch mime {
	case "application/json", "application/xml":
		return true
	}
	return false
}

// jobsFlags returns the -J flags to inject, unless the user already passed one.
func jobsFlags(flags []string) []string {
	if hasJobsFlag(flags) {
		return nil
	}
	jobs := os.Getenv("EXOHUB_JOBS")
	if jobs == "" {
		jobs = "1"
	}
	return []string{"-J", jobs}
}

// hasJobsFlag checks if args contain -J or --jobs flag
func hasJobsFlag(args []string) bool {
	for _, arg := range args {
		if arg == "-J" || arg == "--jobs" {
			return true
		}
		if len(arg) > 2 && arg[:2] == "-J" {
			return true
		}
		if len(arg) > 7 && arg[:7] == "--jobs=" {
			return true
		}
	}
	return false
}

// isURL reports whether arg looks like a URL that git-annex addurl can handle.
func isURL(arg string) bool {
	if strings.HasPrefix(arg, "magnet:") {
		return true
	}
	u, err := url.Parse(arg)
	if err != nil {
		return false
	}
	switch u.Scheme {
	case "http", "https", "ftp":
		return true
	}
	return false
}

// filenameFromURL extracts the last path component from a URL to use as local filename.
func filenameFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	base := path.Base(u.Path)
	if base == "" || base == "." || base == "/" {
		return ""
	}
	return base
}

// buildAddURLArgs constructs the git annex addurl argument list for a single URL.
// jobsArgs are the -J flags. flags are the user-supplied flags from the command line.
// If the user supplied --file, it is passed through as the local filename override.
// Otherwise, --preserve-filename is passed so git-annex uses the server-provided
// filename as-is (git-annex checks the Content-Disposition response header
// internally). A --file fallback derived from the URL path is also appended so
// that URLs with no meaningful path component still get a sensible local name.
func buildAddURLArgs(rawURL string, jobsArgs, flags []string) []string {
	cmdArgs := []string{"annex", "addurl"}
	cmdArgs = append(cmdArgs, jobsArgs...)
	if hasFileFlag(flags) {
		for i, f := range flags {
			if f == "--file" && i+1 < len(flags) {
				cmdArgs = append(cmdArgs, "--file", flags[i+1])
			} else if strings.HasPrefix(f, "--file=") {
				cmdArgs = append(cmdArgs, f)
			}
		}
	} else {
		cmdArgs = append(cmdArgs, "--preserve-filename")
		if fname := filenameFromURL(rawURL); fname != "" {
			cmdArgs = append(cmdArgs, "--file", fname)
		}
	}
	cmdArgs = append(cmdArgs, rawURL)
	return cmdArgs
}

// hasFileFlag checks if args contain --file flag
func hasFileFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--file" {
			return true
		}
		if strings.HasPrefix(arg, "--file=") {
			return true
		}
	}
	return false
}

// splitArgs partitions args into local paths, URLs, and flags.
func splitArgs(args []string) (locals, urls, flags []string) {
	valueFlagSet := map[string]bool{
		"-J": true, "--jobs": true, "--file": true,
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") {
			flags = append(flags, arg)
			if valueFlagSet[arg] && !strings.Contains(arg, "=") && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
		} else if isURL(arg) {
			urls = append(urls, arg)
		} else {
			locals = append(locals, arg)
		}
	}
	return
}

// reconcileExecBit checks each file in paths: if the worktree entry is a regular
// file with any execute bit set and the index currently has mode 100644, it applies
// git update-index --chmod=+x to fix the mode without re-staging content.
// This is needed because git-annex unlocked adds always stage the annex pointer
// with mode 100644, ignoring the worktree execute bit.
func reconcileExecBit(paths []string) error {
	if len(paths) == 0 {
		return nil
	}

	// Collect worktree-executable files (regular files with any exec bit).
	var candidates []string
	for _, p := range paths {
		info, err := os.Lstat(p)
		if err != nil {
			continue
		}
		// Skip symlinks (locked-mode annex entries) and non-regular files.
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			continue
		}
		if info.Mode()&0111 != 0 {
			candidates = append(candidates, p)
		}
	}
	if len(candidates) == 0 {
		return nil
	}

	// Get the repo-root-relative prefix for the current working directory so we
	// can normalize cwd-relative candidate paths to match git ls-files --full-name
	// output (which is always repo-root-relative).
	prefixCmd := exec.Command("git", "rev-parse", "--show-prefix")
	prefixOut, err := prefixCmd.Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not determine repo prefix, exec bit not reconciled: %v\n", err)
		return nil
	}
	prefix := strings.TrimRight(string(prefixOut), "\n")

	// Query the index mode for each candidate using git ls-files -s --full-name
	// so paths in the output are always repo-root-relative.
	lsArgs := append([]string{"ls-files", "-s", "--full-name", "--"}, candidates...)
	lsCmd := exec.Command("git", lsArgs...)
	out, err := lsCmd.Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not query index mode, exec bit not reconciled: %v\n", err)
		return nil
	}

	// Build a set of repo-root-relative paths that are staged at 100644.
	need := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		// Format: <mode> SP <hash> SP <stage> TAB <path>
		fields := strings.SplitN(line, "\t", 2)
		if len(fields) != 2 {
			continue
		}
		indexPath := fields[1]
		parts := strings.Fields(fields[0])
		if len(parts) < 1 {
			continue
		}
		if parts[0] == "100644" {
			need[indexPath] = true
		}
	}

	for _, p := range candidates {
		// Normalize the cwd-relative candidate to repo-root-relative for lookup.
		fullPath := p
		if prefix != "" {
			fullPath = prefix + p
		}
		if need[fullPath] {
			chmodArgs := []string{"update-index", "--chmod=+x", "--", p}
			if err := execCommand("git", chmodArgs...); err != nil {
				return fmt.Errorf("git update-index --chmod=+x %s: %w", p, err)
			}
		}
	}
	return nil
}

// execCommand runs a command with inherited stdio.
func execCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	fmt.Fprintf(os.Stderr, "")
	return cmd.Run()
}

// migrateToAnnex moves git-tracked files to git-annex tracking.
func migrateToAnnex(paths, jobsArgs, flags []string) error {
	allFiles := collectFiles(paths)
	if len(allFiles) == 0 {
		return nil
	}

	var toMigrate, rest []string
	tracked := gitTrackedFiles(allFiles)
	trackedSet := make(map[string]bool, len(tracked))
	for _, f := range tracked {
		trackedSet[f] = true
	}

	annexSet := annexedFileSet(allFiles)
	for _, f := range allFiles {
		if trackedSet[f] && !annexSet[f] {
			toMigrate = append(toMigrate, f)
		} else {
			rest = append(rest, f)
		}
	}

	if len(toMigrate) > 0 {
		rmArgs := []string{"rm", "--cached", "--quiet", "--"}
		rmArgs = append(rmArgs, toMigrate...)
		if err := execCommand("git", rmArgs...); err != nil {
			return fmt.Errorf("git rm --cached: %w", err)
		}

		addArgs := []string{"annex", "add"}
		addArgs = append(addArgs, jobsArgs...)
		addArgs = append(addArgs, flags...)
		addArgs = append(addArgs, toMigrate...)
		if err := execCommand("git", addArgs...); err != nil {
			return err
		}
		if err := reconcileExecBit(toMigrate); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "migrated %d file(s) from git to git-annex\n", len(toMigrate))
	}

	if len(rest) > 0 {
		addArgs := []string{"annex", "add"}
		addArgs = append(addArgs, jobsArgs...)
		addArgs = append(addArgs, flags...)
		addArgs = append(addArgs, rest...)
		if err := execCommand("git", addArgs...); err != nil {
			return err
		}
		if err := reconcileExecBit(rest); err != nil {
			return err
		}
	}

	return nil
}

// migrateToGit moves annexed files back to git tracking.
func migrateToGit(paths, flags []string) error {
	allFiles := collectFiles(paths)
	if len(allFiles) == 0 {
		return nil
	}

	var toMigrate, rest []string
	annexSet := annexedFileSet(allFiles)
	for _, f := range allFiles {
		if annexSet[f] {
			toMigrate = append(toMigrate, f)
		} else {
			rest = append(rest, f)
		}
	}

	if len(toMigrate) > 0 {
		unlockArgs := []string{"annex", "unlock"}
		unlockArgs = append(unlockArgs, toMigrate...)
		if err := execCommand("git", unlockArgs...); err != nil {
			return fmt.Errorf("git annex unlock: %w", err)
		}

		addArgs := []string{"add"}
		addArgs = append(addArgs, flags...)
		addArgs = append(addArgs, toMigrate...)
		if err := execCommand("git", addArgs...); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "migrated %d file(s) from git-annex to git\n", len(toMigrate))
	}

	if len(rest) > 0 {
		addArgs := []string{"add"}
		addArgs = append(addArgs, flags...)
		addArgs = append(addArgs, rest...)
		if err := execCommand("git", addArgs...); err != nil {
			return err
		}
	}

	return nil
}

// collectFiles expands directories recursively and returns a flat list of file paths.
func collectFiles(paths []string) []string {
	var files []string
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			files = append(files, p)
			continue
		}
		if info.IsDir() {
			_ = filepath.Walk(p, func(path string, fi os.FileInfo, err error) error {
				if err != nil || fi.IsDir() {
					return err
				}
				files = append(files, path)
				return nil
			})
		} else {
			files = append(files, p)
		}
	}
	return files
}

// gitTrackedFiles returns the subset of paths that are tracked by git.
func gitTrackedFiles(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	args := []string{"ls-files", "--"}
	args = append(args, paths...)
	cmd := exec.Command("git", args...)
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var tracked []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			tracked = append(tracked, line)
		}
	}
	return tracked
}

// annexedFileSet returns the set of paths that are tracked by git-annex,
// using git annex lookupkey --batch to handle both symlink and unlocked modes.
func annexedFileSet(paths []string) map[string]bool {
	result := make(map[string]bool)
	if len(paths) == 0 {
		return result
	}

	cmd := exec.Command("git", "annex", "lookupkey", "--batch")
	cmd.Stderr = nil
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return result
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return result
	}
	if err := cmd.Start(); err != nil {
		return result
	}

	go func() {
		for _, p := range paths {
			fmt.Fprintln(stdin, p)
		}
		stdin.Close()
	}()

	scanner := bufio.NewScanner(stdout)
	for i := 0; i < len(paths) && scanner.Scan(); i++ {
		if scanner.Text() != "" {
			result[paths[i]] = true
		}
	}

	cmd.Wait()
	return result
}
