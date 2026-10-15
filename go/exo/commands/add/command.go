package add

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
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

	initcmd "github.com/Genentech/exohub/go/exo/commands/init"
	"github.com/Genentech/exohub/go/exo/commandutil"
)

const largeTextFileThreshold = 5 * 1024 * 1024 // 5 MB

// maxFilesPerBatch caps how many file paths are passed to a single git or
// git-annex invocation. Without this, a large directory (further inflated by
// .git/objects if the walk isn't filtered) can exceed the OS argv length
// limit (E2BIG), which is smaller on macOS than Linux.
//
// Trade-off: batching trades one failure mode for another. A single
// over-the-limit invocation fails atomically (nothing staged); a failure in
// batch N of execCommandBatched leaves batches 1..N-1 already staged. This
// is an acceptable default (E2BIG would otherwise hard-fail the whole
// command on large datasets), but it means --force migrations and large
// `exo add` runs are only atomic within a single maxFilesPerBatch chunk, not
// across the whole file list.
const maxFilesPerBatch = 1000

var command = commandutil.Command

func NewCommand() *cobra.Command {
	var (
		annexMode bool
		gitMode   bool
	)

	cmd := &cobra.Command{
		Use:   "add [--annex|--git] [--force] [paths/URLs]...",
		Short: "Add files or URLs with repository-level git/annex routing",
		Long: `Add files or URLs to git/git-annex tracking.

Auto mode routes new (untracked) files in this order:
  1. Protected paths: .exohub/**, .gitignore, .gitattributes → git
  2. annex.routing in .exohub/config: first match wins (top-down)
  3. Fallback: binary → annex; text → git unless size >= threshold
     (default 5 MiB; large text prompts only on a TTY, otherwise → annex)
Directories are expanded and ignored files are excluded. Tracked files keep
their current backend; changing rules never silently migrates them.

The fallback threshold resolves as --large-text-threshold >
EXOHUB_LARGE_TEXT_THRESHOLD > 5 MiB. Overrides affect only the fallback,
never explicit rules. Sizes accept bytes, KB/MB or KiB/MiB.

Use --explain to inspect destinations and reasons without staging anything.
Use --reroute to preview tracked-file migrations; add --yes to apply them.
Protected paths are never moved by --reroute.

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

Routing flags (consumed by exo, not forwarded to git):
  --explain                    Show routing decisions without side effects
  --json                       Machine-readable output (requires --explain)
  --reroute                    Preview tracked files whose backend would change
  --yes                        Apply migrations (requires --reroute)
  --large-text-threshold SIZE   Override fallback large-text threshold
Environment: EXOHUB_LARGE_TEXT_THRESHOLD sets the fallback threshold.

Flags:
  -J, --jobs NUMBER  Number of parallel jobs for git-annex operations
                     (default: $EXOHUB_JOBS or 1)`,
		Example: `  exo add data/                           # route new files by repository rules
  exo add --annex results.csv             # force annex tracking
  exo add --git README.md                 # force git tracking
  exo add --annex --force images/         # migrate git files to annex
  exo add --git --force config.yaml       # migrate annexed file back to git
  exo add https://example.com/data.csv   # add URL, use server filename
  exo add --file raw/data.csv https://example.com/data.csv  # override filename
  exo add -J 4 data/                      # parallel with 4 jobs
  exo add --explain data/                 # inspect without staging
  exo add --explain --json data/          # machine-readable decisions
  exo add --reroute data/                 # preview tracked-file migrations
  exo add --reroute --yes data/           # apply migrations
  exo add --large-text-threshold 1MiB .   # override fallback only`,
		DisableFlagParsing:    true,
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			mode, force, remaining := extractMode(args)
			locals, urls, flags, threshold, explain, jsonOutput, reroute, yes, err := splitArgs(remaining)
			if err != nil {
				return err
			}

			// If --help is in flags, show our help
			for _, f := range flags {
				if f == "--help" || f == "-h" {
					return cmd.Help()
				}
			}

			if jsonOutput && !explain {
				return fmt.Errorf("--json requires --explain")
			}

			if reroute && explain {
				return fmt.Errorf("--reroute cannot be combined with --explain")
			}

			if yes && !reroute {
				return fmt.Errorf("--yes requires --reroute")
			}

			if explain {
				return runExplain(cmd, locals, threshold, jsonOutput)
			}

			if reroute {
				if mode != "auto" || force {
					return fmt.Errorf("--reroute cannot be combined with --annex, --git, or --force")
				}
				return runReroute(cmd, locals, threshold, flags, yes)
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
						baseArgs := append([]string{"annex", "add"}, jobsArgs...)
						baseArgs = append(baseArgs, flags...)
						if err := execCommandBatched("git", baseArgs, locals); err != nil {
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
						baseArgs := append([]string{"add"}, flags...)
						if err := execCommandBatched("git", baseArgs, locals); err != nil {
							return err
						}
					}
				default: // "auto"
					plan, err := PlanAutoAdd(".", locals, threshold)
					if err != nil {
						return err
					}
					var gitFiles, annexFiles, largeTextFiles []string
					for _, file := range plan {
						if file.LargeTextFallback {
							largeTextFiles = append(largeTextFiles, file.Path)
						} else if file.Backend == Annex {
							annexFiles = append(annexFiles, file.Path)
						} else {
							gitFiles = append(gitFiles, file.Path)
						}
					}

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
						baseArgs := append([]string{"-c", "annex.largefiles=nothing", "add"}, flags...)
						baseArgs = append(baseArgs, "--")
						if err := execCommandBatched("git", baseArgs, gitFiles); err != nil {
							return err
						}
					}
					if len(annexFiles) > 0 {
						baseArgs := append([]string{"annex", "add", "--force-large"}, jobsArgs...)
						baseArgs = append(baseArgs, flags...)
						baseArgs = append(baseArgs, "--")
						if err := execCommandBatched("git", baseArgs, annexFiles); err != nil {
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
//
// Directories are expanded via listGitAwareFiles, so paths excluded by
// .gitignore (or other git exclude mechanisms) are never classified or
// staged. A path given explicitly (not discovered via directory expansion)
// is classified as-is; if it happens to be ignored, the underlying git/
// git-annex invocation will error, matching plain `git add <ignored-file>`.
func classifyFiles(paths []string) (gitFiles, annexFiles, largeTextFiles []string) {
	for _, f := range paths {
		info, err := os.Stat(f)
		if err != nil {
			annexFiles = append(annexFiles, f)
			continue
		}
		if info.IsDir() {
			for _, p := range listGitAwareFiles(f) {
				fi, err := os.Lstat(p)
				if err != nil || fi.IsDir() {
					continue
				}
				classifySingleFile(p, fi, &gitFiles, &annexFiles, &largeTextFiles)
			}
		} else {
			classifySingleFile(f, info, &gitFiles, &annexFiles, &largeTextFiles)
		}
	}
	return
}

// listGitAwareFiles returns the files under dir that git considers relevant
// for `git add .`: tracked files, plus untracked files that are not excluded
// by .gitignore or other git exclude mechanisms. Because git never lists
// paths inside .git as part of the working tree, this also naturally keeps
// .git/** out of the result.
//
// The `-c` (cached) component of `git ls-files` reports index entries
// regardless of whether they still exist in the working tree, so a tracked
// file that was removed with a plain `rm` (not yet staged as a deletion)
// would otherwise be returned here. Every result is filtered through
// os.Lstat so callers (classifyFiles' direct use, and collectFiles' --force
// migrations) never see a path that doesn't actually exist on disk.
//
// If dir is not inside a usable git working tree at all (e.g. in tests that
// exercise classification against a plain temp directory with no .git), this
// falls back to a plain recursive walk so non-git usage is unaffected. Any
// other git failure (e.g. a permissions error inside a real repo) is treated
// as "no files" rather than silently falling back to an ignore-unaware walk,
// which would reintroduce inconsistent .gitignore handling.
func listGitAwareFiles(dir string) []string {
	if err := exec.Command("git", "-C", dir, "rev-parse", "--is-inside-work-tree").Run(); err != nil {
		return walkAllFiles(dir)
	}

	cmd := exec.Command("git", "ls-files", "-co", "--exclude-standard", "-z", "--", dir)
	out, err := cmd.Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: git ls-files failed for %s, skipping: %v\n", dir, err)
		return nil
	}
	trimmed := strings.TrimRight(string(out), "\x00")
	if trimmed == "" {
		return nil
	}

	var files []string
	for _, p := range strings.Split(trimmed, "\x00") {
		if p == "" {
			continue
		}
		if _, err := os.Lstat(p); err != nil {
			continue
		}
		files = append(files, p)
	}
	return files
}

// walkAllFiles recursively lists every file under dir with no ignore
// filtering. It is the fallback used when git-aware listing is unavailable.
func walkAllFiles(dir string) []string {
	var files []string
	_ = filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return err
		}
		files = append(files, p)
		return nil
	})
	return files
}

// classifySingleFile routes a single file into git, annex, or largeText buckets.
func classifySingleFile(path string, info os.FileInfo, gitFiles, annexFiles, largeTextFiles *[]string) {
	mime := detectMimeType(path)
	if mime == "" {
		// Could not detect — treat as binary
		*annexFiles = append(*annexFiles, path)
		return
	}
	isText := strings.HasPrefix(mime, "text/")
	if !isText {
		*annexFiles = append(*annexFiles, path)
		return
	}
	if info.Size() >= largeTextFileThreshold {
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

// splitArgs partitions args into local paths, URLs, and flags. --explain,
// --json, --reroute and --yes (design D6/D8/D10) are consumed here, like
// --large-text-threshold, and are never forwarded to git.
func splitArgs(args []string) (locals, urls, flags []string, threshold string, explain, jsonOutput, reroute, yes bool, err error) {
	valueFlagSet := map[string]bool{
		"-J": true, "--jobs": true, "--file": true,
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--explain" {
			explain = true
			continue
		}
		if arg == "--json" {
			jsonOutput = true
			continue
		}
		if arg == "--reroute" {
			reroute = true
			continue
		}
		if arg == "--yes" {
			yes = true
			continue
		}
		if arg == "--large-text-threshold" || strings.HasPrefix(arg, "--large-text-threshold=") {
			if arg == "--large-text-threshold" {
				if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
					return nil, nil, nil, "", false, false, false, false, fmt.Errorf("--large-text-threshold requires a value")
				}
				i++
				threshold = args[i]
			} else {
				threshold = strings.TrimPrefix(arg, "--large-text-threshold=")
			}
			if threshold == "" {
				return nil, nil, nil, "", false, false, false, false, fmt.Errorf("--large-text-threshold requires a value")
			}
			if _, parseErr := resolveThreshold(threshold); parseErr != nil {
				return nil, nil, nil, "", false, false, false, false, parseErr
			}
			continue
		}
		if arg == "--" {
			for _, path := range args[i+1:] {
				if isURL(path) {
					urls = append(urls, path)
				} else {
					locals = append(locals, path)
				}
			}
			break
		}
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

// runExplain implements `exo add --explain [paths] [--json]` (design D8):
// it runs enumeration and routing with no side effects (PlanAutoAdd never
// stages anything) and prints, per file, its size, destination, and why.
// Routing config shadow warnings (design D9) are printed to stderr in both
// modes, so stdout's --json output stays a clean array.
func runExplain(cmd *cobra.Command, locals []string, threshold string, jsonOutput bool) error {
	if len(locals) == 0 {
		locals = []string{"."}
	}
	plan, err := PlanAutoAddWithOptions(".", locals, threshold, PlanOptions{RerouteHints: true})
	if err != nil {
		return err
	}
	warnShadowedRoutingRules(cmd.ErrOrStderr(), ".")
	if jsonOutput {
		return writeExplainJSON(cmd.OutOrStdout(), plan)
	}
	writeExplainTable(cmd.OutOrStdout(), plan)
	return nil
}

// runReroute implements `exo add --reroute [paths]` (design D6): the
// tracked files among paths whose current backend differs from what the
// Router decides today. It previews the moves by default (an --explain
// style table, with no side effects) and needs --yes to execute them,
// reusing migrateToAnnex/migrateToGit so the actual move is identical to a
// manual `exo add --annex --force` / `exo add --git --force`. Protected
// paths (.exohub/**, .gitignore, .gitattributes) are never candidates
// (RerouteCandidates), regardless of their current backend.
func runReroute(cmd *cobra.Command, locals []string, threshold string, flags []string, yes bool) error {
	if len(locals) == 0 {
		locals = []string{"."}
	}
	plan, err := PlanReroute(".", locals, threshold)
	if err != nil {
		return err
	}
	warnShadowedRoutingRules(cmd.ErrOrStderr(), ".")

	candidates := plan.Moves
	skippedOutput := cmd.OutOrStdout()
	if yes {
		skippedOutput = cmd.ErrOrStderr()
	}
	for _, file := range plan.Skipped {
		fmt.Fprintf(skippedOutput, "%s: skipped: %s\n", file.Path, file.rerouteError)
	}
	if len(plan.Skipped) > 0 {
		fmt.Fprintf(skippedOutput, "%d file(s) skipped.\n", len(plan.Skipped))
	}
	if len(candidates) == 0 {
		if len(plan.Skipped) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "no tracked files need rerouting")
		} else {
			fmt.Fprintln(cmd.OutOrStdout(), "no evaluable tracked files need rerouting")
		}
		return nil
	}

	if !yes {
		writeRerouteTable(cmd.OutOrStdout(), candidates)
		fmt.Fprintf(cmd.OutOrStdout(), "\n%d file(s) would move. Re-run with --yes to apply.\n", len(candidates))
		return nil
	}

	var toAnnex, toGit []string
	for _, file := range candidates {
		if file.rerouteTo == Annex {
			toAnnex = append(toAnnex, file.Path)
		} else {
			toGit = append(toGit, file.Path)
		}
	}

	// Check every annex-to-git move before either migration can mutate the index.
	for _, file := range candidates {
		if file.rerouteTo != Git {
			continue
		}
		if _, err := trackedContentInfo(file.Path, file.annexKey); err != nil {
			return fmt.Errorf("cannot reroute %s to git: %w", file.Path, err)
		}
		f, err := os.Open(file.Path)
		if err != nil {
			return fmt.Errorf("cannot reroute %s to git: content unavailable (run exo get): %w", file.Path, err)
		}
		f.Close()
	}
	jobsArgs := jobsFlags(flags)
	if len(toAnnex) > 0 {
		if err := migrateToAnnex(toAnnex, jobsArgs, flags); err != nil {
			return err
		}
	}
	if len(toGit) > 0 {
		if err := migrateToGit(toGit, flags); err != nil {
			return err
		}
	}
	return nil
}

// writeRerouteTable prints the planned moves for `exo add --reroute`: PATH,
// SIZE, CURRENT backend, → the Router's DEST, and the matched RULE, each
// column padded to its widest entry (same layout convention as
// writeExplainTable).
func writeRerouteTable(w io.Writer, candidates []PlannedFile) {
	pathWidth, sizeWidth, curWidth, destWidth := len("PATH"), len("SIZE"), len("CURRENT"), len("DEST")
	sizes := make([]string, len(candidates))
	currents := make([]string, len(candidates))
	dests := make([]string, len(candidates))
	for i, file := range candidates {
		sizes[i] = humanize.Bytes(uint64(file.Size))
		currents[i] = file.Backend.String()
		dests[i] = file.rerouteTo.String()
		if len(file.Path) > pathWidth {
			pathWidth = len(file.Path)
		}
		if len(sizes[i]) > sizeWidth {
			sizeWidth = len(sizes[i])
		}
		if len(currents[i]) > curWidth {
			curWidth = len(currents[i])
		}
		if len(dests[i]) > destWidth {
			destWidth = len(dests[i])
		}
	}
	fmt.Fprintf(w, "%s  %s  %s  → %s  %s\n", padRight("PATH", pathWidth), padRight("SIZE", sizeWidth), padRight("CURRENT", curWidth), padRight("DEST", destWidth), "RULE")
	for i, file := range candidates {
		fmt.Fprintf(w, "%s  %s  %s  → %s  %s\n", padRight(file.Path, pathWidth), padRight(sizes[i], sizeWidth), padRight(currents[i], curWidth), padRight(dests[i], destWidth), file.rerouteRule)
	}
}

// warnShadowedRoutingRules prints a warning line to w for every
// annex.routing rule that DetectShadowedRules finds can never match
// (design D9). It is best-effort: an unreadable or invalid config is
// silently skipped here, since `exo doctor` is the authoritative place to
// report config errors, and --explain's job is only to warn about routing
// results, not to duplicate validation.
func warnShadowedRoutingRules(w io.Writer, dir string) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return
	}
	root := strings.TrimSuffix(string(out), "\n")
	cfg, err := initcmd.LoadExohubConfigAt(root)
	if err != nil || cfg == nil || cfg.Annex == nil || len(cfg.Annex.Routing) == 0 {
		return
	}
	if err := cfg.Annex.ValidateRouting(); err != nil {
		return
	}
	for _, idx := range DetectShadowedRules(cfg.Annex.Routing) {
		fmt.Fprintf(w, "warning: annex.routing rule %d can never match (fully shadowed by an earlier rule)\n", idx)
	}
}

// explainEntry is the --json shape for one planned file (design D8).
type explainEntry struct {
	Path        string `json:"path"`
	Size        int64  `json:"size"`
	Destination string `json:"destination"`
	Reason      string `json:"reason"`
}

func writeExplainJSON(w io.Writer, plan []PlannedFile) error {
	entries := make([]explainEntry, len(plan))
	for i, file := range plan {
		entries[i] = explainEntry{Path: file.Path, Size: file.Size, Destination: file.Backend.String(), Reason: file.Reason}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(entries)
}

// writeExplainTable prints the design D8 table: PATH, SIZE, → DEST and WHY,
// each column padded to its widest entry.
func writeExplainTable(w io.Writer, plan []PlannedFile) {
	pathWidth, sizeWidth, destWidth := len("PATH"), len("SIZE"), len("DEST")
	sizes := make([]string, len(plan))
	dests := make([]string, len(plan))
	for i, file := range plan {
		sizes[i] = humanize.Bytes(uint64(file.Size))
		dests[i] = file.Backend.String()
		if len(file.Path) > pathWidth {
			pathWidth = len(file.Path)
		}
		if len(sizes[i]) > sizeWidth {
			sizeWidth = len(sizes[i])
		}
		if len(dests[i]) > destWidth {
			destWidth = len(dests[i])
		}
	}
	fmt.Fprintf(w, "%s  %s  → %s  %s\n", padRight("PATH", pathWidth), padRight("SIZE", sizeWidth), padRight("DEST", destWidth), "WHY")
	for i, file := range plan {
		fmt.Fprintf(w, "%s  %s  → %s  %s\n", padRight(file.Path, pathWidth), padRight(sizes[i], sizeWidth), padRight(dests[i], destWidth), file.Reason)
	}
}

func padRight(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
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
	// so paths in the output are always repo-root-relative. Batched to avoid
	// exceeding argv limits for large candidate lists.
	need := make(map[string]bool)
	for i := 0; i < len(candidates); i += maxFilesPerBatch {
		end := i + maxFilesPerBatch
		if end > len(candidates) {
			end = len(candidates)
		}
		lsArgs := append([]string{"ls-files", "-s", "--full-name", "--"}, candidates[i:end]...)
		lsCmd := exec.Command("git", lsArgs...)
		out, err := lsCmd.Output()
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not query index mode, exec bit not reconciled: %v\n", err)
			return nil
		}

		// Format: <mode> SP <hash> SP <stage> TAB <path>
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if line == "" {
				continue
			}
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

// execCommandBatched runs name with baseArgs followed by chunks of files,
// invoking the command multiple times (in maxFilesPerBatch-sized chunks) if
// necessary so the combined argv never grows unbounded with the size of the
// dataset being added. If files is empty, no invocation is made. Returns the
// first error encountered, if any.
func execCommandBatched(name string, baseArgs, files []string) error {
	for i := 0; i < len(files); i += maxFilesPerBatch {
		end := i + maxFilesPerBatch
		if end > len(files) {
			end = len(files)
		}
		args := make([]string, 0, len(baseArgs)+(end-i))
		args = append(args, baseArgs...)
		args = append(args, files[i:end]...)
		if err := execCommand(name, args...); err != nil {
			return err
		}
	}
	return nil
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
		rmBase := []string{"rm", "--cached", "--quiet", "--"}
		if err := execCommandBatched("git", rmBase, toMigrate); err != nil {
			return fmt.Errorf("git rm --cached: %w", err)
		}

		addBase := append([]string{"annex", "add"}, jobsArgs...)
		addBase = append(addBase, flags...)
		if err := execCommandBatched("git", addBase, toMigrate); err != nil {
			return err
		}
		if err := reconcileExecBit(toMigrate); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "migrated %d file(s) from git to git-annex\n", len(toMigrate))
	}

	if len(rest) > 0 {
		addBase := append([]string{"annex", "add"}, jobsArgs...)
		addBase = append(addBase, flags...)
		if err := execCommandBatched("git", addBase, rest); err != nil {
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
		// git annex unannex drops the annex key association and restores the
		// real content as a plain working-tree file (unlike `git annex
		// unlock`, which keeps the file annexed so it can be edited in
		// place). annex.largefiles=nothing then forces the repo-wide `*
		// filter=annex` clean filter to keep it in git rather than silently
		// re-annexing it (design D7: exo's decision is authoritative).
		unannexBase := []string{"annex", "unannex"}
		if err := execCommandBatched("git", unannexBase, toMigrate); err != nil {
			return fmt.Errorf("git annex unannex: %w", err)
		}

		addBase := append([]string{"-c", "annex.largefiles=nothing", "add"}, flags...)
		if err := execCommandBatched("git", addBase, toMigrate); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "migrated %d file(s) from git-annex to git\n", len(toMigrate))
	}

	if len(rest) > 0 {
		addBase := append([]string{"add"}, flags...)
		if err := execCommandBatched("git", addBase, rest); err != nil {
			return err
		}
	}

	return nil
}

// collectFiles expands directories recursively and returns a flat list of
// file paths, using the same git-ignore-aware enumeration as classifyFiles
// (see listGitAwareFiles) so --force migrations never touch ignored paths or
// .git/** either.
func collectFiles(paths []string) []string {
	var files []string
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			files = append(files, p)
			continue
		}
		if info.IsDir() {
			files = append(files, listGitAwareFiles(p)...)
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
	var tracked []string
	for i := 0; i < len(paths); i += maxFilesPerBatch {
		end := i + maxFilesPerBatch
		if end > len(paths) {
			end = len(paths)
		}
		args := append([]string{"ls-files", "--"}, paths[i:end]...)
		cmd := exec.Command("git", args...)
		out, err := cmd.Output()
		if err != nil {
			return nil
		}
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if line != "" {
				tracked = append(tracked, line)
			}
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
