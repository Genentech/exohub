package bundle

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"github.com/yuin/goldmark"
	meta "github.com/yuin/goldmark-meta"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"golang.org/x/sync/errgroup"
	"gopkg.in/yaml.v3"

	"github.com/Genentech/exohub/go/exo/commandutil"
)

var (
	statusStyle   = lipgloss.NewStyle().Foreground(commandutil.ColorDarkOrange).Bold(true)
	progressFull  = lipgloss.NewStyle().Foreground(commandutil.ColorPink).Render("█")
	progressEmpty = lipgloss.NewStyle().Foreground(commandutil.ColorDim).Render("░")
)

func logStatus(msg string) {
	fmt.Fprintf(os.Stderr, "%s %s\n", statusStyle.Render("▸"), msg)
}

func printProgress(label string, current, total int) {
	if total == 0 {
		return
	}
	pct := float64(current) / float64(total)
	width := 30
	filled := int(pct * float64(width))
	bar := strings.Repeat(progressFull, filled) + strings.Repeat(progressEmpty, width-filled)
	fmt.Fprintf(os.Stderr, "\r  %s %s %d/%d (%d%%)", bar, label, current, total, int(pct*100))
	if current == total {
		fmt.Fprintf(os.Stderr, "\n")
	}
}

// progressTracker allows sharing progress state across sequential phases.
type progressTracker struct {
	label   string
	current int
	total   int
}

func (p *progressTracker) inc() {
	p.current++
	printProgress(p.label, p.current, p.total)
}

const longText = `Generate JSON metadata files describing a dataset repository at the current git revision.

Output consists of bundle.json (bundle manifest), per-commit JSONs under commits/
(exohub-commit/v1.json), and per-file artifact JSONs under files/ (exohub-artifact/v1.json).

Output is written to .git/exohub/bundle/<scope>/ where <scope> is derived
from the current ref context (tag, branch, or commit hash).

Examples:
  exo bundle                          # generate bundle with default preset
  exo bundle --preset parquet-only    # use a named preset
  exo bundle --from-ref v1.0          # commits since v1.0
  exo bundle --scope annex            # annexed file artifacts + markdown artifacts
  exo bundle --output /tmp/bundle     # custom output directory
  exo bundle --dry-run                # preview what the bundle would do
  exo bundle --ref v5.3.0             # build bundle for a specific tag
  exo bundle --ref v5.3.0 --dry-run   # preview bundle for a specific tag`

// Commit represents a git commit in the bundle.
type Commit struct {
	Hash           string   `json:"hash"`
	Parents        []string `json:"parents"`
	AuthorName     string   `json:"author_name"`
	AuthorEmail    string   `json:"author_email"`
	AuthorDate     string   `json:"author_date"`
	CommitterName  string   `json:"committer_name"`
	CommitterEmail string   `json:"committer_email"`
	CommitterDate  string   `json:"committer_date"`
	Subject        string   `json:"subject"`
	Body           string   `json:"body"`
}

// CommitArtifact represents a per-commit JSON (exohub-commit/v1.json).
type CommitArtifact struct {
	Schema         string   `json:"$schema"`
	RepoURL        string   `json:"repo_url"`
	Ref            string   `json:"ref"`
	Hash           string   `json:"hash"`
	Parents        []string `json:"parents"`
	AuthorName     string   `json:"author_name"`
	AuthorEmail    string   `json:"author_email"`
	AuthorDate     string   `json:"author_date"`
	CommitterName  string   `json:"committer_name"`
	CommitterEmail string   `json:"committer_email"`
	CommitterDate  string   `json:"committer_date"`
	Subject        string   `json:"subject"`
	Body           string   `json:"body"`
	AnnexFiles     []string `json:"annex_files"`
	GitFiles       []string `json:"git_files"`
}

// BundleInfo represents the bundle.json manifest (exohub-bundle/v1.json).
type BundleInfo struct {
	Schema            string   `json:"$schema"`
	RepoURL           string   `json:"repo_url"`
	Ref               string   `json:"ref"`
	RefName           string   `json:"ref_name"`
	RefType           string   `json:"ref_type"`
	FromRef           string   `json:"from_ref"`
	GeneratedAt       string   `json:"generated_at"`
	Preset            string   `json:"preset"`
	Scope             string   `json:"scope"`
	Incremental       bool     `json:"incremental"`
	CommitHashes      []string `json:"commit_hashes"`
	CommitCount       int      `json:"commit_count"`
	FileCount         int      `json:"file_count"`
	AnnexFileCount    int      `json:"annex_file_count"`
	GitFileCount      int      `json:"git_file_count"`
	MarkdownFileCount int      `json:"markdown_file_count"`
	TotalSize         int64    `json:"total_size"`
	ExoVersion        string   `json:"exo_version"`
}

// Artifact represents a per-file artifact JSON (exohub-artifact/v1.json).
type Artifact struct {
	Schema    string              `json:"$schema"`
	RepoURL   string              `json:"repo_url"`
	Ref       string              `json:"ref"`
	Path      string              `json:"path"`
	Key       string              `json:"key"`
	Backend   string              `json:"backend"`
	Size      int64               `json:"size"`
	Metadata  map[string][]string `json:"metadata"`
	Commits   []Commit            `json:"commits"`
	Locations []Location          `json:"locations,omitempty"`
}

// Location represents a remote storage location for an annexed file.
type Location struct {
	Remote     string  `json:"remote"`
	Type       string  `json:"type"`
	UUID       string  `json:"uuid"`
	Untrusted  bool    `json:"untrusted,omitempty"`
	S3URL      string  `json:"s3url,omitempty"`
	S3Object   string  `json:"s3_object,omitempty"`
	ExportPath string  `json:"export_path,omitempty"`
	Chunks     []Chunk `json:"chunks,omitempty"`
}

// Chunk represents a chunk of a large annexed file.
type Chunk struct {
	ChunkKey    string `json:"chunk_key"`
	ChunkNumber int    `json:"chunk_number"`
	ChunkSize   int64  `json:"chunk_size"`
	S3Path      string `json:"s3_path"`
}

// Section represents a heading extracted from a markdown file.
type Section struct {
	Level int    `json:"level"`
	Text  string `json:"text"`
}

// MarkdownArtifact represents a per-file markdown artifact JSON (exohub-markdown/v1.json).
type MarkdownArtifact struct {
	Schema    string              `json:"$schema"`
	RepoURL   string              `json:"repo_url"`
	Ref       string              `json:"ref"`
	Path      string              `json:"path"`
	Key       string              `json:"key"`
	Backend   string              `json:"backend"`
	Size      int64               `json:"size"`
	Metadata  map[string][]string `json:"metadata"`
	Commits   []Commit            `json:"commits"`
	Locations []Location          `json:"locations,omitempty"`
	Title     string              `json:"title"`
	Sections  []Section           `json:"sections"`
	Content   string              `json:"content"`
	IsReadme  bool                `json:"is_readme"`
}

// BundleManifest represents the .exohub/bundle configuration.
type BundleManifest struct {
	Presets []Preset `yaml:"presets"`
}

// Preset represents a named bundle preset.
type Preset struct {
	Name              string   `yaml:"name"`
	Scope             string   `yaml:"scope,omitempty"`
	Include           []string `yaml:"include,omitempty"`
	Exclude           []string `yaml:"exclude,omitempty"`
	IncludeHidden     bool     `yaml:"include_hidden,omitempty"`
	MarkdownRecursive bool     `yaml:"markdown_recursive,omitempty"`
	Incremental       bool     `yaml:"incremental,omitempty"`
}

// RemoteConfig mirrors the remote config from .exohub/remotes (read-only).
type RemoteConfig struct {
	Name  string `yaml:"name"`
	Type  string `yaml:"type"`
	UUID  string `yaml:"uuid,omitempty"`
	S3URL string `yaml:"s3url,omitempty"`
	Chunk string `yaml:"chunk,omitempty"`
}

type remotesFile struct {
	Remotes []RemoteConfig `yaml:"remotes"`
}

// annexFindEntry is a single entry from `git annex find --json`.
type annexFindEntry struct {
	File     string `json:"file"`
	Key      string `json:"key"`
	Backend  string `json:"backend"`
	Bytesize string `json:"bytesize"`
}

// annexWhereisEntry is a single entry from `git annex whereis --json`.
type annexWhereisEntry struct {
	File      string               `json:"file"`
	Key       string               `json:"key"`
	Whereis   []annexWhereisRemote `json:"whereis"`
	Untrusted []annexWhereisRemote `json:"untrusted"`
}

// annexWhereisRemote represents a remote in whereis output.
type annexWhereisRemote struct {
	UUID        string   `json:"uuid"`
	Description string   `json:"description"`
	Here        bool     `json:"here"`
	URLs        []string `json:"urls"`
}

// annexMetadataEntry is a single entry from `git annex metadata --json`.
type annexMetadataEntry struct {
	File   string                 `json:"file"`
	Key    string                 `json:"key"`
	Fields map[string]interface{} `json:"fields"`
}

type annexMetadataLookup struct {
	ByFile map[string]map[string][]string
	ByKey  map[string]map[string][]string
}

// blobInfo holds the git blob hash and size for a tracked file.
type blobInfo struct {
	Hash string
	Size int64
}

func NewCommand(version string) *cobra.Command {
	var (
		flagScope             string
		flagFromRef           string
		flagPreset            string
		flagOutput            string
		flagRef               string
		flagIncludeHidden     bool
		flagIncremental       bool
		flagYes               bool
		flagDryRun            bool
		flagResume            bool
		flagMarkdownRecursive bool
	)

	cmd := &cobra.Command{
		Use:   "bundle [PATH...]",
		Short: "Generate JSON metadata bundle for catalog ingest",
		Long:  longText,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBundle(flagScope, flagFromRef, flagPreset, flagOutput, flagRef, flagIncludeHidden, flagIncremental, flagYes, flagDryRun, flagResume, flagMarkdownRecursive, version, args)
		},
	}

	cmd.Flags().StringVar(&flagScope, "scope", "", "Which metadata to generate: all, git, or annex (default: from preset or all)")
	cmd.Flags().StringVar(&flagFromRef, "from-ref", "", "Start of commit range (ref or regex)")
	cmd.Flags().StringVar(&flagPreset, "preset", "default", "Bundle manifest preset name")
	cmd.Flags().StringVar(&flagOutput, "output", "", "Custom output directory")
	cmd.Flags().StringVar(&flagRef, "ref", "", "Commit hash or tag to build the bundle for (default: HEAD)")
	cmd.Flags().BoolVar(&flagIncludeHidden, "include-hidden", false, "Include files in hidden directories (default: skip)")
	cmd.Flags().BoolVar(&flagIncremental, "incremental", false, "Only generate file artifacts for files modified in the commit range")
	cmd.Flags().BoolVarP(&flagYes, "yes", "y", false, "Skip confirmation prompts (remove and regenerate)")
	cmd.Flags().BoolVar(&flagDryRun, "dry-run", false, "Show what the bundle would do without generating anything")
	cmd.Flags().BoolVar(&flagResume, "resume", false, "Resume a previous bundle run, skipping existing artifacts")
	cmd.Flags().BoolVar(&flagMarkdownRecursive, "markdown-recursive", false, "Parse markdown files in all directories (default: root-level only)")

	return cmd
}

func runBundle(scope, fromRef, presetName, outputDir, targetRef string, includeHidden, incremental, forceYes, dryRun, flagResume, markdownRecursive bool, version string, pathFilters []string) error {
	// Verify we're in a git repo
	if _, err := os.Stat(".git"); os.IsNotExist(err) {
		return fmt.Errorf("not a git repository")
	}

	// Load remote configs BEFORE any checkout — .exohub/remotes describes
	// where data lives now, not at the target ref.
	remoteConfigs, err := loadRemoteConfigs()
	if err != nil {
		return fmt.Errorf("failed to load .exohub/remotes: %w", err)
	}
	if len(remoteConfigs) == 0 {
		return fmt.Errorf(".exohub/remotes not found or empty — cannot determine remote locations")
	}

	// Load bundle manifest and select preset
	preset, err := loadPreset(presetName)
	if err != nil {
		return err
	}

	// CLI --include-hidden overrides preset value
	if includeHidden {
		preset.IncludeHidden = true
	}

	// CLI --markdown-recursive overrides preset value
	if markdownRecursive {
		preset.MarkdownRecursive = true
	}

	// CLI --incremental overrides preset value; otherwise use preset value
	incremental = incremental || preset.Incremental

	// Determine effective scope: CLI flag > preset > default "all"
	effectiveScope := "all"
	if preset.Scope != "" {
		effectiveScope = preset.Scope
	}
	if scope != "" {
		effectiveScope = scope
	}
	if effectiveScope != "all" && effectiveScope != "git" && effectiveScope != "annex" {
		return fmt.Errorf("--scope must be all, git, or annex (got %q)", effectiveScope)
	}

	// Resolve output directory — when --ref is provided (even in dry-run),
	// derive scope dir from the target ref rather than HEAD.
	if outputDir == "" {
		if targetRef != "" {
			refName, _ := resolveRefInfoFor(targetRef)
			outputDir = filepath.Join(".exohub", "bundles", refName)
		} else {
			scopeDir, err := resolveScopeDir()
			if err != nil {
				return fmt.Errorf("failed to resolve scope directory: %w", err)
			}
			outputDir = filepath.Join(".exohub", "bundles", scopeDir)
		}
	}

	// Dry-run: resolve all parameters, print summary, exit early
	if dryRun {
		var refHash, refName, refType string
		if targetRef != "" {
			cmd := commandutil.Command("git", "rev-parse", "--verify", targetRef)
			out, err := cmd.Output()
			if err != nil {
				return fmt.Errorf("--ref %q does not resolve to a valid commit", targetRef)
			}
			refHash = strings.TrimSpace(string(out))
			refName, refType = resolveRefInfoFor(targetRef)
		} else {
			var err error
			refHash, err = resolveHEAD()
			if err != nil {
				return fmt.Errorf("failed to resolve HEAD: %w", err)
			}
			refName, refType = resolveRefInfo()
		}

		fromCommit, err := resolveFromRef(fromRef, targetRef)
		if err != nil {
			return fmt.Errorf("failed to resolve commit range: %w", err)
		}

		fmt.Fprintf(os.Stderr, "Dry run — bundle parameters:\n")
		fmt.Fprintf(os.Stderr, "  ref:        %s (%s)\n", refName, refType)
		fmt.Fprintf(os.Stderr, "  ref hash:   %s\n", refHash)
		fmt.Fprintf(os.Stderr, "  from-ref:   %s\n", displayFromRef(fromCommit))
		fmt.Fprintf(os.Stderr, "  scope:      %s\n", effectiveScope)
		fmt.Fprintf(os.Stderr, "  preset:     %s\n", presetName)
		fmt.Fprintf(os.Stderr, "  output:     %s\n", outputDir)
		fmt.Fprintf(os.Stderr, "  hidden:     %v\n", preset.IncludeHidden)
		fmt.Fprintf(os.Stderr, "  incremental: %v\n", incremental)
		fmt.Fprintf(os.Stderr, "  remotes:    %d configured\n", len(remoteConfigs))

		if status := checkCommitReachability(refHash); status != nil {
			printReachabilityWarning(status)
		}
		return nil
	}

	// If output directory exists, decide whether to resume or regenerate
	resume := flagResume
	if info, err := os.Stat(outputDir); err == nil && info.IsDir() {
		if flagResume {
			// --resume: skip existing artifacts
			resume = true
		} else if forceYes {
			// --yes: remove and regenerate
			if err := os.RemoveAll(outputDir); err != nil {
				return fmt.Errorf("failed to clean output directory: %w", err)
			}
		} else {
			var choice int
			err := huh.NewForm(
				huh.NewGroup(
					huh.NewSelect[int]().
						Title("Bundle output already exists: "+outputDir).
						Options(
							huh.NewOption("Resume (skip existing artifacts)", 0),
							huh.NewOption("Remove and regenerate", 1),
							huh.NewOption("Abort", 2),
						).
						Value(&choice),
				),
			).WithTheme(commandutil.ExoTheme()).WithKeyMap(commandutil.ExoKeyMap()).Run()
			if err != nil {
				return fmt.Errorf("aborted: %w", err)
			}
			switch choice {
			case 0:
				resume = true
			case 1:
				if err := os.RemoveAll(outputDir); err != nil {
					return fmt.Errorf("failed to clean output directory: %w", err)
				}
			case 2:
				return fmt.Errorf("aborted")
			}
		}
	}

	// Checkout target ref if needed (after all prompts and validation)
	if targetRef != "" {
		cmd := commandutil.Command("git", "rev-parse", "--verify", targetRef)
		targetHash, err := cmd.Output()
		if err != nil {
			return fmt.Errorf("--ref %q does not resolve to a valid commit", targetRef)
		}

		cmd = commandutil.Command("git", "rev-parse", "HEAD")
		currentHash, err := cmd.Output()
		if err != nil {
			return fmt.Errorf("failed to resolve HEAD: %w", err)
		}

		if strings.TrimSpace(string(targetHash)) != strings.TrimSpace(string(currentHash)) {
			originalRef := ""
			cmd = commandutil.Command("git", "symbolic-ref", "--short", "HEAD")
			if out, err := cmd.Output(); err == nil {
				originalRef = strings.TrimSpace(string(out))
			} else {
				originalRef = strings.TrimSpace(string(currentHash))
			}

			logStatus(fmt.Sprintf("Checking out %s...", targetRef))
			cmd = commandutil.Command("git", "checkout", "--quiet", targetRef)
			if out, err := cmd.CombinedOutput(); err != nil {
				return fmt.Errorf("failed to checkout --ref %q: %s", targetRef, strings.TrimSpace(string(out)))
			}

			defer func() {
				logStatus(fmt.Sprintf("Restoring %s...", originalRef))
				cmd := commandutil.Command("git", "checkout", "--quiet", originalRef)
				if out, err := cmd.CombinedOutput(); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: failed to restore %s: %s\n", originalRef, strings.TrimSpace(string(out)))
				}
			}()
		}
	}

	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	logStatus("Resolving repository metadata...")
	// Fan out independent lookups concurrently
	var (
		headHash   string
		repoURL    string
		fromCommit string
	)
	{
		var g errgroup.Group
		g.Go(func() error {
			var err error
			headHash, err = resolveHEAD()
			if err != nil {
				return fmt.Errorf("failed to resolve HEAD: %w", err)
			}
			return nil
		})
		g.Go(func() error {
			repoURL = getRepoURL()
			return nil
		})
		g.Go(func() error {
			var err error
			fromCommit, err = resolveFromRef(fromRef, targetRef)
			if err != nil {
				return fmt.Errorf("failed to resolve commit range: %w", err)
			}
			return nil
		})
		if err := g.Wait(); err != nil {
			return err
		}
	}

	if status := checkCommitReachability(headHash); status != nil {
		printReachabilityWarning(status)
	}

	if incremental && fromCommit != "" {
		refLabel := targetRef
		if refLabel == "" {
			refLabel = "HEAD"
		}
		logStatus(fmt.Sprintf("Incremental: extracting between %s and %s", refLabel, fromCommit))
	} else {
		logStatus("Extracting commit history...")
	}

	// Run extractCommits and buildPathCommitIndex concurrently (both depend on fromCommit)
	var (
		commits     []Commit
		pathCommits map[string][]string
		commitFiles map[string][]string
	)
	{
		var g errgroup.Group
		g.Go(func() error {
			var err error
			commits, err = extractCommits(fromCommit)
			if err != nil {
				return fmt.Errorf("failed to extract commits: %w", err)
			}
			return nil
		})
		g.Go(func() error {
			var err error
			pathCommits, commitFiles, err = buildPathCommitIndex(fromCommit)
			if err != nil {
				return fmt.Errorf("failed to build path-commit index: %w", err)
			}
			return nil
		})
		if err := g.Wait(); err != nil {
			return err
		}
	}

	// Build commit lookup map
	commitMap := make(map[string]Commit, len(commits))
	for _, c := range commits {
		commitMap[c.Hash] = c
	}

	logStatus("Identifying annexed files...")
	// Build set of annexed files for classifying commit files.
	// In incremental mode, use --batch to avoid CLI arg overflow.
	annexedFileSet := make(map[string]bool)
	{
		if incremental {
			// Batch mode: feed paths via stdin
			var paths []string
			for path := range pathCommits {
				paths = append(paths, path)
			}
			cmd := commandutil.Command("git", "annex", "find", "--anything", "--batch", "--format=${file}\\n")
			cmd.Stdin = strings.NewReader(strings.Join(paths, "\n") + "\n")
			if out, err := cmd.Output(); err == nil {
				for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
					if line != "" {
						annexedFileSet[line] = true
					}
				}
			}
		} else {
			args := []string{"annex", "find", "--anything", "--format=${file}\\n"}
			if len(pathFilters) > 0 {
				args = append(args, "--")
				args = append(args, pathFilters...)
			}
			cmd := commandutil.Command("git", args...)
			if out, err := cmd.Output(); err == nil {
				for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
					if line != "" {
						annexedFileSet[line] = true
					}
				}
			}
		}
	}

	logStatus(fmt.Sprintf("Generating %d commit artifacts...", len(commits)))
	// Generate per-commit JSONs under commits/
	commitsDir := filepath.Join(outputDir, "commits")
	commitCount, err := generateCommitArtifacts(commitsDir, headHash, repoURL, commits, commitFiles, annexedFileSet)
	if err != nil {
		return fmt.Errorf("failed to generate commit artifacts: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Wrote %d commit artifacts to %s\n", commitCount, commitsDir)

	filesDir := filepath.Join(outputDir, "files")

	// Build blob info map for git-tracked files (single git ls-tree call).
	// Always built because generateMarkdownArtifacts needs blob hashes/sizes
	// regardless of scope.
	var blobInfoMap map[string]blobInfo
	blobInfoMap, err = buildBlobInfoMap(pathFilters)
	if err != nil {
		return fmt.Errorf("failed to build blob info map: %w", err)
	}

	logStatus("Generating file artifacts...")
	// Generate per-file artifacts based on scope
	var annexFileCount, gitFileCount, markdownFileCount int
	var totalSize int64
	var noLocations []string

	// Generate markdown artifacts first (to build the markdownFiles set for git exclusion).
	// Markdown is a documentation artifact class orthogonal to scope; always generated.
	markdownFiles := make(map[string]bool)
	{
		n, sz, mdFiles, err := generateMarkdownArtifacts(filesDir, headHash, repoURL, preset, pathCommits, commitMap, blobInfoMap, incremental, pathFilters, resume, annexedFileSet, nil)
		if err != nil {
			return fmt.Errorf("failed to generate markdown artifacts: %w", err)
		}
		markdownFileCount = n
		totalSize += sz
		markdownFiles = mdFiles
	}

	if incremental {
		// Incremental: single progress bar across annex + git phases.
		// Total is set by annex (its filePaths includes git files in incremental mode).
		progress := &progressTracker{label: "file artifacts", total: 0}

		if effectiveScope == "all" || effectiveScope == "annex" {
			n, sz, noloc, processedAnnex, err := generateAnnexArtifacts(filesDir, headHash, repoURL, preset, pathCommits, commitMap, remoteConfigs, incremental, pathFilters, resume, annexedFileSet, progress)
			if err != nil {
				return fmt.Errorf("failed to generate annex artifacts: %w", err)
			}
			annexFileCount = n
			totalSize += sz
			noLocations = append(noLocations, noloc...)
			for f := range processedAnnex {
				annexedFileSet[f] = true
			}
		}
		if effectiveScope == "all" || effectiveScope == "git" {
			n, sz, err := generateGitArtifacts(filesDir, headHash, repoURL, preset, pathCommits, commitMap, blobInfoMap, incremental, pathFilters, resume, annexedFileSet, markdownFiles, progress)
			if err != nil {
				return fmt.Errorf("failed to generate git artifacts: %w", err)
			}
			gitFileCount = n
			totalSize += sz
		}
	} else {
		// Full mode: annexedFileSet is already reliable, run both concurrently
		var mu sync.Mutex
		var g errgroup.Group
		if effectiveScope == "all" || effectiveScope == "annex" {
			g.Go(func() error {
				n, sz, noloc, _, err := generateAnnexArtifacts(filesDir, headHash, repoURL, preset, pathCommits, commitMap, remoteConfigs, incremental, pathFilters, resume, annexedFileSet, nil)
				if err != nil {
					return fmt.Errorf("failed to generate annex artifacts: %w", err)
				}
				mu.Lock()
				annexFileCount = n
				totalSize += sz
				noLocations = append(noLocations, noloc...)
				mu.Unlock()
				return nil
			})
		}
		if effectiveScope == "all" || effectiveScope == "git" {
			g.Go(func() error {
				n, sz, err := generateGitArtifacts(filesDir, headHash, repoURL, preset, pathCommits, commitMap, blobInfoMap, incremental, pathFilters, resume, annexedFileSet, markdownFiles, nil)
				if err != nil {
					return fmt.Errorf("failed to generate git artifacts: %w", err)
				}
				mu.Lock()
				gitFileCount = n
				totalSize += sz
				mu.Unlock()
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			return err
		}
	}

	// Write .nolocations file if any annexed files have no configured remote locations
	if len(noLocations) > 0 {
		sort.Strings(noLocations)
		nolocPath := filepath.Join(outputDir, ".nolocations")
		if err := os.WriteFile(nolocPath, []byte(strings.Join(noLocations, "\n")+"\n"), 0o644); err != nil {
			return fmt.Errorf("failed to write .nolocations: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Warning: %d annexed files have no remote location registered (see %s)\n", len(noLocations), nolocPath)
		fmt.Fprintf(os.Stderr, "  This usually means 'exo sync' should be run before 'exo bundle'.\n")
		fmt.Fprintf(os.Stderr, "  Files without remote locations cannot be downloaded by bundle consumers.\n")
	}

	fileCount := annexFileCount + gitFileCount + markdownFileCount
	logStatus(fmt.Sprintf("Wrote %d file artifacts (%d annex, %d git, %d markdown)", fileCount, annexFileCount, gitFileCount, markdownFileCount))

	// Resolve ref name and type for bundle.json
	var refName, refType string
	if targetRef != "" {
		refName, refType = resolveRefInfoFor(targetRef)
	} else {
		refName, refType = resolveRefInfo()
	}

	// Write bundle.json
	commitHashes := make([]string, len(commits))
	for i, c := range commits {
		commitHashes[i] = c.Hash
	}

	bundleInfo := BundleInfo{
		Schema:            "exohub-bundle/v1.json",
		RepoURL:           repoURL,
		Ref:               headHash,
		RefName:           refName,
		RefType:           refType,
		FromRef:           fromCommit,
		GeneratedAt:       time.Now().UTC().Format(time.RFC3339),
		Preset:            presetName,
		Scope:             effectiveScope,
		Incremental:       incremental,
		CommitHashes:      commitHashes,
		CommitCount:       commitCount,
		FileCount:         fileCount,
		AnnexFileCount:    annexFileCount,
		GitFileCount:      gitFileCount,
		MarkdownFileCount: markdownFileCount,
		TotalSize:         totalSize,
		ExoVersion:        version,
	}
	logStatus("Writing bundle.json...")
	bundlePath := filepath.Join(outputDir, "bundle.json")
	if err := writeJSON(bundlePath, bundleInfo); err != nil {
		return fmt.Errorf("failed to write bundle.json: %w", err)
	}

	logStatus("Packing artipacks...")
	// Pack into artipacks if file count exceeds threshold (same env vars as go-artifactdb-cli)
	if err := maybeCreateArtipacks(outputDir); err != nil {
		return fmt.Errorf("failed to create artipacks: %w", err)
	}

	logStatus(fmt.Sprintf("Bundle complete: %s", outputDir))

	return nil
}

// resolveScopeDir determines the output scope directory name from the current git state.
func resolveScopeDir() (string, error) {
	// Check if HEAD is a tag
	cmd := commandutil.Command("git", "describe", "--tags", "--exact-match", "HEAD")
	if out, err := cmd.Output(); err == nil {
		return strings.TrimSpace(string(out)), nil
	}

	// Check if HEAD is on a branch
	cmd = commandutil.Command("git", "symbolic-ref", "--short", "HEAD")
	if out, err := cmd.Output(); err == nil {
		branch := strings.TrimSpace(string(out))
		// Sanitize branch name for directory use (replace / with -)
		return strings.ReplaceAll(branch, "/", "-"), nil
	}

	// Detached HEAD: use short commit hash
	cmd = commandutil.Command("git", "rev-parse", "--short", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("cannot determine HEAD: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// resolveHEAD returns the full commit hash of HEAD.
func resolveHEAD() (string, error) {
	cmd := commandutil.Command("git", "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// getRepoURL returns the git remote origin URL.
func getRepoURL() string {
	cmd := commandutil.Command("git", "remote", "get-url", "origin")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// resolveFromRef determines the start commit for the commit range.
// targetRef is the --ref value (empty means use HEAD for auto-detection).
// Returns empty string if all commits should be included.
func resolveFromRef(fromRef, targetRef string) (string, error) {
	if fromRef != "" {
		// Try as a direct ref first
		cmd := commandutil.Command("git", "rev-parse", "--verify", fromRef)
		if out, err := cmd.Output(); err == nil {
			return strings.TrimSpace(string(out)), nil
		}

		// Try as a regex against tags
		cmd = commandutil.Command("git", "tag", "--sort=-creatordate")
		out, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("--from-ref %q does not resolve as a ref and could not list tags: %w", fromRef, err)
		}
		re, err := regexp.Compile(fromRef)
		if err != nil {
			return "", fmt.Errorf("--from-ref %q is not a valid ref or regex: %w", fromRef, err)
		}
		for _, tag := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			tag = strings.TrimSpace(tag)
			if tag == "" {
				continue
			}
			if re.MatchString(tag) {
				return tag, nil
			}
		}
		return "", fmt.Errorf("--from-ref %q: no matching ref or tag found", fromRef)
	}

	// Auto-detect: if --ref is a tag (or HEAD is a tag), find previous tag
	if targetRef != "" {
		// Check if targetRef is a tag
		cmd := commandutil.Command("git", "tag", "-l", targetRef)
		if out, err := cmd.Output(); err == nil && strings.TrimSpace(string(out)) != "" {
			prevCmd := commandutil.Command("git", "describe", "--tags", "--abbrev=0", targetRef+"^")
			if prevOut, err := prevCmd.Output(); err == nil {
				return strings.TrimSpace(string(prevOut)), nil
			}
			// No previous tag: all commits up to this tag
			return "", nil
		}
	}

	// Auto-detect: HEAD is a tag → previous tag
	cmd := commandutil.Command("git", "describe", "--tags", "--exact-match", "HEAD")
	if out, err := cmd.Output(); err == nil {
		currentTag := strings.TrimSpace(string(out))
		// Find previous tag
		prevCmd := commandutil.Command("git", "describe", "--tags", "--abbrev=0", currentTag+"^")
		if prevOut, err := prevCmd.Output(); err == nil {
			return strings.TrimSpace(string(prevOut)), nil
		}
		// No previous tag: all commits up to this tag
		return "", nil
	}

	// Auto-detect: HEAD on a non-main branch → main..HEAD
	cmd = commandutil.Command("git", "symbolic-ref", "--short", "HEAD")
	if out, err := cmd.Output(); err == nil {
		branch := strings.TrimSpace(string(out))
		if branch != "main" && branch != "master" {
			// Check if main exists
			for _, base := range []string{"main", "master"} {
				checkCmd := commandutil.Command("git", "rev-parse", "--verify", base)
				if _, err := checkCmd.Output(); err == nil {
					return base, nil
				}
			}
		}
	}

	// Fallback: all commits
	return "", nil
}

// extractCommits runs git log and parses commits in the given range.
func extractCommits(fromRef string) ([]Commit, error) {
	// Use NUL-delimited fields with ASCII Record Separator (\x1e) between records.
	// We avoid using double-NUL as record separator because an empty %b (body)
	// produces three consecutive NULs, which corrupts strings.Split boundaries.
	format := strings.Join([]string{
		"%H",  // hash
		"%P",  // parent hashes (space-separated)
		"%an", // author name
		"%ae", // author email
		"%aI", // author date (ISO 8601)
		"%cn", // committer name
		"%ce", // committer email
		"%cI", // committer date (ISO 8601)
		"%s",  // subject
		"%b",  // body
	}, "%x00")
	format += "%x1e" // ASCII Record Separator — unambiguous record boundary

	args := []string{"log", "--format=" + format}
	if fromRef != "" {
		args = append(args, fromRef+"..HEAD")
	} else {
		args = append(args, "HEAD")
	}
	// Exclude git-annex branch (--not must come after positive refs)
	args = append(args, "--not", "--glob=refs/heads/git-annex")

	cmd := commandutil.Command("git", args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git log failed: %w", err)
	}

	raw := string(out)
	if strings.TrimSpace(raw) == "" {
		return []Commit{}, nil
	}

	// Split by ASCII Record Separator
	records := strings.Split(raw, "\x1e")
	var commits []Commit
	for _, record := range records {
		record = strings.TrimSpace(record)
		if record == "" {
			continue
		}
		fields := strings.SplitN(record, "\x00", 10)
		if len(fields) < 10 {
			continue
		}
		var parents []string
		if p := strings.TrimSpace(fields[1]); p != "" {
			parents = strings.Split(p, " ")
		}
		commits = append(commits, Commit{
			Hash:           fields[0],
			Parents:        parents,
			AuthorName:     fields[2],
			AuthorEmail:    fields[3],
			AuthorDate:     fields[4],
			CommitterName:  fields[5],
			CommitterEmail: fields[6],
			CommitterDate:  fields[7],
			Subject:        fields[8],
			Body:           strings.TrimSpace(fields[9]),
		})
	}
	return commits, nil
}

// buildPathCommitIndex builds bidirectional indexes:
//   - pathCommits: file path → commit hashes that modified it
//   - commitFiles: commit hash → file paths modified by it
func buildPathCommitIndex(fromRef string) (pathCommits map[string][]string, commitFiles map[string][]string, err error) {
	args := []string{"log", "--name-only", "--format=%H"}
	if fromRef != "" {
		args = append(args, fromRef+"..HEAD")
	} else {
		args = append(args, "HEAD")
	}
	// Exclude git-annex branch (--not must come after positive refs)
	args = append(args, "--not", "--glob=refs/heads/git-annex")

	cmd := commandutil.Command("git", args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, nil, err
	}

	pathCommits = make(map[string][]string)
	commitFiles = make(map[string][]string)
	lines := strings.Split(string(out), "\n")
	var currentHash string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Lines that look like a commit hash (40 hex chars)
		if len(line) == 40 && isHex(line) {
			currentHash = line
		} else if currentHash != "" {
			pathCommits[line] = append(pathCommits[line], currentHash)
			commitFiles[currentHash] = append(commitFiles[currentHash], line)
		}
	}
	return pathCommits, commitFiles, nil
}

func isHex(s string) bool {
	if len(s) == 0 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// loadPreset loads the bundle manifest and returns the selected preset.
func loadPreset(name string) (Preset, error) {
	path := filepath.Join(".exohub", "bundle")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		// No manifest: return default preset
		if name != "default" {
			return Preset{}, fmt.Errorf("bundle manifest .exohub/bundle not found (preset %q unavailable)", name)
		}
		return Preset{Name: "default", Scope: "all"}, nil
	}
	if err != nil {
		return Preset{}, fmt.Errorf("failed to read .exohub/bundle: %w", err)
	}

	var manifest BundleManifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return Preset{}, fmt.Errorf("failed to parse .exohub/bundle: %w", err)
	}

	for _, p := range manifest.Presets {
		if p.Name == name {
			return p, nil
		}
	}

	// Preset not found: list available
	var available []string
	for _, p := range manifest.Presets {
		available = append(available, p.Name)
	}
	return Preset{}, fmt.Errorf("preset %q not found in .exohub/bundle; available: %s", name, strings.Join(available, ", "))
}

// loadRemoteConfigs reads .exohub/remotes and returns a map of remote name → config.
func loadRemoteConfigs() (map[string]RemoteConfig, error) {
	path := filepath.Join(".exohub", "remotes")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var rf remotesFile
	if err := yaml.Unmarshal(data, &rf); err != nil {
		return nil, err
	}

	m := make(map[string]RemoteConfig, len(rf.Remotes))
	for _, r := range rf.Remotes {
		m[r.Name] = r
	}
	return m, nil
}

// generateAnnexArtifacts generates per-file artifact JSONs for annexed files.
// Returns (count, totalSize, noLocations, error).
// generateAnnexArtifacts generates per-file artifact JSONs for annexed files.
// Returns (count, totalSize, noLocations, processedFiles, error).
// processedFiles contains paths of files that were identified as annexed.
func generateAnnexArtifacts(filesDir, headHash, repoURL string, preset Preset, pathCommits map[string][]string, commitMap map[string]Commit, remoteConfigs map[string]RemoteConfig, incremental bool, pathFilters []string, resume bool, annexedFileSet map[string]bool, progress *progressTracker) (int, int64, []string, map[string]bool, error) {
	// Build list of annexed files to process
	var filePaths []string
	if incremental {
		// Incremental: modified files at HEAD, whereis determines which are annexed
		lsCmd := commandutil.Command("git", "ls-files")
		lsOut, err := lsCmd.Output()
		if err != nil {
			return 0, 0, nil, nil, fmt.Errorf("git ls-files failed: %w", err)
		}
		headFiles := make(map[string]bool)
		for _, f := range strings.Split(strings.TrimSpace(string(lsOut)), "\n") {
			if f != "" {
				headFiles[f] = true
			}
		}
		for path := range pathCommits {
			if headFiles[path] {
				filePaths = append(filePaths, path)
			}
		}
	} else {
		// Full: scan all annexed files, filter to HEAD
		args := []string{"annex", "find", "--anything", "--format=${file}\\n"}
		if len(pathFilters) > 0 {
			args = append(args, "--")
			args = append(args, pathFilters...)
		}
		cmd := commandutil.Command("git", args...)
		out, err := cmd.Output()
		if err != nil {
			// git annex find fails if not a git-annex repo — skip silently
			return 0, 0, nil, nil, nil
		}

		// Filter to files that exist at HEAD (--anything includes files from all refs)
		lsArgs := []string{"ls-files"}
		if len(pathFilters) > 0 {
			lsArgs = append(lsArgs, "--")
			lsArgs = append(lsArgs, pathFilters...)
		}
		lsCmd := commandutil.Command("git", lsArgs...)
		lsOut, err := lsCmd.Output()
		if err != nil {
			return 0, 0, nil, nil, fmt.Errorf("git ls-files failed: %w", err)
		}
		headFiles := make(map[string]bool)
		for _, f := range strings.Split(strings.TrimSpace(string(lsOut)), "\n") {
			if f != "" {
				headFiles[f] = true
			}
		}

		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if line != "" && headFiles[line] {
				filePaths = append(filePaths, line)
			}
		}
	}

	if len(filePaths) == 0 {
		return 0, 0, nil, nil, nil
	}

	// Apply filters (preset, skip, resume)
	{
		var filtered []string
		for _, f := range filePaths {
			if !matchesFilters(f, preset) {
				continue
			}
			if skip, reason := shouldSkipPath(f, preset); skip {
				if reason != "" && commandutil.IsDebug() {
					fmt.Fprintf(os.Stderr, "DEBUG: %s\n", reason)
				}
				continue
			}
			if resume {
				artifactPath := filepath.Join(filesDir, f+".json")
				if _, err := os.Stat(artifactPath); err == nil {
					continue
				}
			}
			filtered = append(filtered, f)
		}
		filePaths = filtered
	}

	if len(filePaths) == 0 {
		return 0, 0, nil, nil, nil
	}

	// Fetch metadata concurrently while setting up whereis streaming
	var (
		metadataLookup annexMetadataLookup
		metaWarn       error
		metaDone       = make(chan struct{})
	)
	go func() {
		defer close(metaDone)
		var err error
		// In incremental mode, scope metadata to modified files only.
		// In full mode, use --all (too many files for CLI args).
		if incremental {
			metadataLookup, err = getAnnexMetadata(filePaths)
		} else {
			metadataLookup, err = getAnnexMetadata(pathFilters)
		}
		if err != nil {
			metaWarn = err
		}
	}()

	// Start whereis in batch mode — stream results and write artifacts as they arrive
	whereisCmd := commandutil.Command("git", "annex", "whereis", "--batch", "--json")
	stdinPipe, err := whereisCmd.StdinPipe()
	if err != nil {
		return 0, 0, nil, nil, fmt.Errorf("failed to create stdin pipe: %w", err)
	}
	stdout, err := whereisCmd.StdoutPipe()
	if err != nil {
		return 0, 0, nil, nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	var stderrBuf bytes.Buffer
	whereisCmd.Stderr = &stderrBuf
	if err := whereisCmd.Start(); err != nil {
		return 0, 0, nil, nil, fmt.Errorf("failed to start git annex whereis: %w", err)
	}

	// Feed file paths to stdin in background
	go func() {
		for _, p := range filePaths {
			fmt.Fprintln(stdinPipe, p)
		}
		stdinPipe.Close()
	}()

	// Wait for metadata before processing
	<-metaDone
	if metaWarn != nil {
		if commandutil.IsDebug() {
			fmt.Fprintf(os.Stderr, "DEBUG: annex metadata: %v\n", metaWarn)
		}
		metadataLookup = newAnnexMetadataLookup()
	}

	// Read whereis results and write artifacts as they stream in
	if progress != nil {
		progress.total = len(filePaths)
		logStatus(fmt.Sprintf("Processing %d files...", progress.total))
	} else {
		logStatus(fmt.Sprintf("Processing %d annexed files...", len(filePaths)))
	}
	filePathSet := make(map[string]bool, len(filePaths))
	for _, f := range filePaths {
		filePathSet[f] = true
	}

	count := 0
	var totalSize int64
	processed := make(map[string]bool, len(filePaths))
	var noLocations []string
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		var whereis annexWhereisEntry
		if err := json.Unmarshal([]byte(line), &whereis); err != nil {
			continue
		}

		// Skip files not in our target set or without a key (non-annexed)
		if !filePathSet[whereis.File] || whereis.Key == "" {
			continue
		}
		processed[whereis.File] = true

		// Parse backend and size from key (e.g. "SHA256E-s12345--hash.ext")
		key := whereis.Key
		backend, size := parseAnnexKey(key)

		meta := metadataLookup.ByFile[whereis.File]
		if meta == nil {
			meta = metadataLookup.ByKey[key]
		}
		if meta == nil {
			meta = make(map[string][]string)
		}

		fileCommits := expandCommits(pathCommits[whereis.File], commitMap)
		locations := buildLocations(whereis.File, key, size, whereis, remoteConfigs)

		if len(locations) == 0 {
			noLocations = append(noLocations, whereis.File)
		}

		artifact := Artifact{
			Schema:    "exohub-artifact/v1.json",
			RepoURL:   repoURL,
			Ref:       headHash,
			Path:      whereis.File,
			Key:       key,
			Backend:   backend,
			Size:      size,
			Metadata:  meta,
			Commits:   fileCommits,
			Locations: locations,
		}

		artifactPath := filepath.Join(filesDir, whereis.File+".json")
		if err := writeJSON(artifactPath, artifact); err != nil {
			return count, totalSize, noLocations, processed, fmt.Errorf("failed to write artifact for %s: %w", whereis.File, err)
		}
		count++
		totalSize += size
		if progress != nil {
			progress.inc()
		} else {
			printProgress("annex artifacts", count, len(filePaths))
		}
	}
	// End progress line (only if using own progress, not shared tracker)
	if progress == nil && count > 0 && count < len(filePaths) {
		fmt.Fprintf(os.Stderr, "\n")
	}

	if err := scanner.Err(); err != nil {
		return count, totalSize, noLocations, processed, fmt.Errorf("reading whereis output: %w", err)
	}

	// Files that didn't get a whereis response (or were non-annexed in incremental mode)
	for _, f := range filePaths {
		if !processed[f] {
			if !incremental {
				// In full mode, all files should be annexed — missing response means no locations
				noLocations = append(noLocations, f)
			}
			// In incremental mode, non-annexed files are handled by generateGitArtifacts
		}
	}

	if err := whereisCmd.Wait(); err != nil {
		// Non-fatal: whereis may exit 1 if some files had issues, but we still
		// have valid results for the files that were processed successfully.
		stderr := strings.TrimSpace(stderrBuf.String())
		if stderr != "" {
			fmt.Fprintf(os.Stderr, "Warning: git annex whereis exited with error: %s\n", stderr)
		} else {
			fmt.Fprintf(os.Stderr, "Warning: git annex whereis exited with: %v\n", err)
		}
	}

	return count, totalSize, noLocations, processed, nil
}

// isMarkdownFile returns true if the path has a .md extension.
func isMarkdownFile(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".md")
}

// isReadme returns true if the filename is README.md or README (case-insensitive).
func isReadme(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	return base == "readme.md" || base == "readme"
}

// isRootLevel returns true if the path has no directory component.
func isRootLevel(path string) bool {
	return !strings.Contains(path, "/")
}

// parseMarkdown parses markdown content using goldmark with the meta extension.
// Returns title (first H1), sections (all headings), content (body without front matter),
// and metadata (from YAML front matter as map[string][]string).
func parseMarkdown(source []byte) (string, []Section, string, map[string][]string) {
	md := goldmark.New(goldmark.WithExtensions(meta.Meta))
	ctx := parser.NewContext()
	reader := text.NewReader(source)
	doc := md.Parser().Parse(reader, parser.WithContext(ctx))

	// Extract front matter metadata
	rawMeta := meta.Get(ctx)
	metadata := make(map[string][]string)
	for k, v := range rawMeta {
		switch val := v.(type) {
		case []interface{}:
			strs := make([]string, 0, len(val))
			for _, item := range val {
				strs = append(strs, fmt.Sprintf("%v", item))
			}
			metadata[k] = strs
		case string:
			metadata[k] = []string{val}
		default:
			metadata[k] = []string{fmt.Sprintf("%v", val)}
		}
	}

	// Walk AST to extract headings
	var title string
	var sections []Section
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if heading, ok := n.(*ast.Heading); ok {
			text := string(heading.Text(source))
			sections = append(sections, Section{Level: heading.Level, Text: text})
			if title == "" && heading.Level == 1 {
				title = text
			}
		}
		return ast.WalkContinue, nil
	})

	if sections == nil {
		sections = []Section{}
	}

	// Strip front matter from content: find the closing --- and return everything after
	content := string(source)
	if strings.HasPrefix(content, "---") {
		if idx := strings.Index(content[3:], "---"); idx >= 0 {
			content = strings.TrimLeft(content[3+idx+3:], "\n")
		}
	}

	return title, sections, content, metadata
}

// readGitBlob reads a file's content from the git index (HEAD) without touching the working tree.
func readGitBlob(path string) ([]byte, error) {
	cmd := commandutil.Command("git", "show", "HEAD:"+path)
	return cmd.Output()
}

// generateMarkdownArtifacts generates per-file artifact JSONs for markdown files.
// When preset.MarkdownRecursive is false, only root-level *.md files are processed.
// Returns (count, totalSize, markdownFiles set, error).
func generateMarkdownArtifacts(filesDir, headHash, repoURL string, preset Preset, pathCommits map[string][]string, commitMap map[string]Commit, blobInfoMap map[string]blobInfo, incremental bool, pathFilters []string, resume bool, annexedFiles map[string]bool, progress *progressTracker) (int, int64, map[string]bool, error) {
	// Get all tracked files via git ls-files
	lsArgs := []string{"ls-files"}
	if len(pathFilters) > 0 {
		lsArgs = append(lsArgs, "--")
		lsArgs = append(lsArgs, pathFilters...)
	}
	cmd := commandutil.Command("git", lsArgs...)
	out, err := cmd.Output()
	if err != nil {
		return 0, 0, nil, fmt.Errorf("git ls-files failed: %w", err)
	}

	markdownFiles := make(map[string]bool)
	var eligible []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" || annexedFiles[line] {
			continue
		}
		if !isMarkdownFile(line) {
			continue
		}
		if !preset.MarkdownRecursive && !isRootLevel(line) {
			continue
		}
		if skip, reason := shouldSkipPath(line, preset); skip {
			if reason != "" && commandutil.IsDebug() {
				fmt.Fprintf(os.Stderr, "DEBUG: %s\n", reason)
			}
			continue
		}
		if !matchesFilters(line, preset) {
			continue
		}
		if incremental {
			if _, modified := pathCommits[line]; !modified {
				continue
			}
		}
		if resume {
			artifactPath := filepath.Join(filesDir, line+".json")
			if _, err := os.Stat(artifactPath); err == nil {
				continue
			}
		}
		eligible = append(eligible, line)
		markdownFiles[line] = true
	}

	if len(eligible) == 0 {
		return 0, 0, markdownFiles, nil
	}

	if progress == nil {
		logStatus(fmt.Sprintf("Processing %d markdown files...", len(eligible)))
	}
	count := 0
	var totalSize int64
	for _, line := range eligible {
		info := blobInfoMap[line]
		blobHash, size := info.Hash, info.Size
		fileCommits := expandCommits(pathCommits[line], commitMap)

		// Read file content from git and parse markdown
		source, err := readGitBlob(line)
		if err != nil {
			return count, totalSize, markdownFiles, fmt.Errorf("failed to read %s from git: %w", line, err)
		}
		title, sections, content, mdMetadata := parseMarkdown(source)

		artifact := MarkdownArtifact{
			Schema:    "exohub-markdown/v1.json",
			RepoURL:   repoURL,
			Ref:       headHash,
			Path:      line,
			Key:       blobHash,
			Backend:   "git",
			Size:      size,
			Metadata:  mdMetadata,
			Commits:   fileCommits,
			Locations: nil,
			Title:     title,
			Sections:  sections,
			Content:   content,
			IsReadme:  isReadme(line),
		}

		artifactPath := filepath.Join(filesDir, line+".json")
		if err := writeJSON(artifactPath, artifact); err != nil {
			return count, totalSize, markdownFiles, fmt.Errorf("failed to write markdown artifact for %s: %w", line, err)
		}
		count++
		totalSize += size
		if progress != nil {
			progress.inc()
		} else {
			printProgress("markdown artifacts", count, len(eligible))
		}
	}

	return count, totalSize, markdownFiles, nil
}

// generateGitArtifacts generates per-file artifact JSONs for git-only tracked files.
// Returns (count, totalSize, error).
func generateGitArtifacts(filesDir, headHash, repoURL string, preset Preset, pathCommits map[string][]string, commitMap map[string]Commit, blobInfoMap map[string]blobInfo, incremental bool, pathFilters []string, resume bool, annexedFiles map[string]bool, markdownFiles map[string]bool, progress *progressTracker) (int, int64, error) {
	// Get all tracked files via git ls-files
	lsArgs := []string{"ls-files"}
	if len(pathFilters) > 0 {
		lsArgs = append(lsArgs, "--")
		lsArgs = append(lsArgs, pathFilters...)
	}
	cmd := commandutil.Command("git", lsArgs...)
	out, err := cmd.Output()
	if err != nil {
		return 0, 0, fmt.Errorf("git ls-files failed: %w", err)
	}

	// Pre-filter to eligible git-only files (annexed and markdown files excluded via passed-in sets)
	var eligible []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" || annexedFiles[line] || markdownFiles[line] {
			continue
		}
		if skip, reason := shouldSkipPath(line, preset); skip {
			if reason != "" && commandutil.IsDebug() {
				fmt.Fprintf(os.Stderr, "DEBUG: %s\n", reason)
			}
			continue
		}
		if !matchesFilters(line, preset) {
			continue
		}
		if incremental {
			if _, modified := pathCommits[line]; !modified {
				continue
			}
		}
		if resume {
			artifactPath := filepath.Join(filesDir, line+".json")
			if _, err := os.Stat(artifactPath); err == nil {
				continue
			}
		}
		eligible = append(eligible, line)
	}

	if len(eligible) == 0 {
		return 0, 0, nil
	}

	if progress == nil {
		logStatus(fmt.Sprintf("Processing %d git-tracked files...", len(eligible)))
	}
	count := 0
	var totalSize int64
	for _, line := range eligible {
		info := blobInfoMap[line]
		blobHash, size := info.Hash, info.Size
		fileCommits := expandCommits(pathCommits[line], commitMap)

		artifact := Artifact{
			Schema:    "exohub-artifact/v1.json",
			RepoURL:   repoURL,
			Ref:       headHash,
			Path:      line,
			Key:       blobHash,
			Backend:   "git",
			Size:      size,
			Metadata:  map[string][]string{},
			Commits:   fileCommits,
			Locations: nil,
		}

		artifactPath := filepath.Join(filesDir, line+".json")
		if err := writeJSON(artifactPath, artifact); err != nil {
			return count, totalSize, fmt.Errorf("failed to write artifact for %s: %w", line, err)
		}
		count++
		totalSize += size
		if progress != nil {
			progress.inc()
		} else {
			printProgress("git artifacts", count, len(eligible))
		}
	}

	return count, totalSize, nil
}

// generateCommitArtifacts generates per-commit JSON files under commits/.
func generateCommitArtifacts(commitsDir, headHash, repoURL string, commits []Commit, commitFiles map[string][]string, annexedFiles map[string]bool) (int, error) {
	count := 0
	for _, c := range commits {
		files := commitFiles[c.Hash]
		if files == nil {
			files = []string{}
		}

		var annexFiles, gitFiles []string
		for _, f := range files {
			if annexedFiles[f] {
				annexFiles = append(annexFiles, f)
			} else {
				gitFiles = append(gitFiles, f)
			}
		}
		if annexFiles == nil {
			annexFiles = []string{}
		}
		if gitFiles == nil {
			gitFiles = []string{}
		}

		artifact := CommitArtifact{
			Schema:         "exohub-commit/v1.json",
			RepoURL:        repoURL,
			Ref:            headHash,
			Hash:           c.Hash,
			Parents:        c.Parents,
			AuthorName:     c.AuthorName,
			AuthorEmail:    c.AuthorEmail,
			AuthorDate:     c.AuthorDate,
			CommitterName:  c.CommitterName,
			CommitterEmail: c.CommitterEmail,
			CommitterDate:  c.CommitterDate,
			Subject:        c.Subject,
			Body:           c.Body,
			AnnexFiles:     annexFiles,
			GitFiles:       gitFiles,
		}

		artifactPath := filepath.Join(commitsDir, c.Hash+".json")
		if err := writeJSON(artifactPath, artifact); err != nil {
			return count, fmt.Errorf("failed to write commit artifact for %s: %w", c.Hash[:8], err)
		}
		count++
	}
	return count, nil
}

// resolveRefInfo returns the human-readable ref name and its type (tag, branch, or commit).
func resolveRefInfo() (string, string) {
	// Check if HEAD is a tag
	cmd := commandutil.Command("git", "describe", "--tags", "--exact-match", "HEAD")
	if out, err := cmd.Output(); err == nil {
		return strings.TrimSpace(string(out)), "tag"
	}

	// Check if HEAD is on a branch
	cmd = commandutil.Command("git", "symbolic-ref", "--short", "HEAD")
	if out, err := cmd.Output(); err == nil {
		return strings.TrimSpace(string(out)), "branch"
	}

	// Detached HEAD: use short commit hash
	cmd = commandutil.Command("git", "rev-parse", "--short", "HEAD")
	if out, err := cmd.Output(); err == nil {
		return strings.TrimSpace(string(out)), "commit"
	}

	return "unknown", "commit"
}

// resolveRefInfoFor returns the human-readable ref name and type for a given ref string.
// Unlike resolveRefInfo which inspects HEAD, this determines the type from the ref argument itself.
func resolveRefInfoFor(ref string) (string, string) {
	// Check if the ref is a tag
	cmd := commandutil.Command("git", "tag", "-l", ref)
	if out, err := cmd.Output(); err == nil && strings.TrimSpace(string(out)) != "" {
		return ref, "tag"
	}

	// Check if the ref is a branch
	cmd = commandutil.Command("git", "show-ref", "--verify", "--quiet", "refs/heads/"+ref)
	if err := cmd.Run(); err == nil {
		return ref, "branch"
	}

	// Treat as a commit hash
	cmd = commandutil.Command("git", "rev-parse", "--short", ref)
	if out, err := cmd.Output(); err == nil {
		return strings.TrimSpace(string(out)), "commit"
	}

	return ref, "commit"
}

// displayFromRef returns a display string for the from-ref value.
func displayFromRef(fromCommit string) string {
	if fromCommit == "" {
		return "(all commits)"
	}
	return fromCommit
}

// commitReachability holds the result of checking whether bundle commits are
// reachable from the remote tracking branch.
type commitReachability struct {
	Upstream   string
	LocalOnly  int
	RemoteOnly int
	Diverged   bool
}

// checkCommitReachability checks whether the given HEAD hash is reachable from
// the remote tracking branch. Returns nil if the check cannot be performed
// (no remote configured) or if everything is in sync.
func checkCommitReachability(headHash string) *commitReachability {
	var upstream string
	cmd := commandutil.Command("git", "rev-parse", "--abbrev-ref", "@{upstream}")
	if out, err := cmd.Output(); err == nil {
		upstream = strings.TrimSpace(string(out))
	} else {
		for _, candidate := range []string{"origin/main", "origin/master"} {
			cmd = commandutil.Command("git", "rev-parse", "--verify", candidate)
			if _, err := cmd.Output(); err == nil {
				upstream = candidate
				break
			}
		}
	}
	if upstream == "" {
		return nil
	}

	cmd = commandutil.Command("git", "rev-parse", upstream)
	upstreamHashOut, err := cmd.Output()
	if err != nil {
		return nil
	}
	upstreamHash := strings.TrimSpace(string(upstreamHashOut))
	if headHash == upstreamHash {
		return nil
	}

	cmd = commandutil.Command("git", "rev-list", "--count", upstream+"..HEAD")
	localOnlyOut, err := cmd.Output()
	if err != nil {
		return nil
	}
	localOnly, _ := strconv.Atoi(strings.TrimSpace(string(localOnlyOut)))

	cmd = commandutil.Command("git", "rev-list", "--count", "HEAD.."+upstream)
	remoteOnlyOut, err := cmd.Output()
	if err != nil {
		return nil
	}
	remoteOnly, _ := strconv.Atoi(strings.TrimSpace(string(remoteOnlyOut)))

	if localOnly == 0 && remoteOnly == 0 {
		return nil
	}

	return &commitReachability{
		Upstream:   upstream,
		LocalOnly:  localOnly,
		RemoteOnly: remoteOnly,
		Diverged:   localOnly > 0 && remoteOnly > 0,
	}
}

func printReachabilityWarning(status *commitReachability) {
	if status.Diverged {
		fmt.Fprintf(os.Stderr, "Warning: local history has diverged from %s (%d local, %d remote-only commits)\n", status.Upstream, status.LocalOnly, status.RemoteOnly)
		fmt.Fprintf(os.Stderr, "  Bundle references %d commits that may be unreachable after a force-push.\n", status.LocalOnly)
		fmt.Fprintf(os.Stderr, "  Consider running: git pull --rebase && exo bundle\n")
	} else if status.LocalOnly > 0 {
		fmt.Fprintf(os.Stderr, "Warning: %d bundle commits are not yet pushed to %s\n", status.LocalOnly, status.Upstream)
		fmt.Fprintf(os.Stderr, "  Bundle consumers cannot fetch these commits until pushed.\n")
	}
}

func newAnnexMetadataLookup() annexMetadataLookup {
	return annexMetadataLookup{
		ByFile: make(map[string]map[string][]string),
		ByKey:  make(map[string]map[string][]string),
	}
}

func cleanAnnexMetadataFields(fields map[string]interface{}) map[string][]string {
	cleaned := make(map[string][]string)
	for k, raw := range fields {
		if k == "lastchanged" || strings.HasSuffix(k, "-lastchanged") {
			continue
		}
		var vals []string
		switch v := raw.(type) {
		case string:
			vals = []string{v}
		case []interface{}:
			for _, item := range v {
				if item == nil {
					continue
				}
				vals = append(vals, fmt.Sprintf("%v", item))
			}
		case nil:
			continue
		default:
			vals = []string{fmt.Sprintf("%v", v)}
		}
		if len(vals) > 0 {
			cleaned[k] = vals
		}
	}
	return cleaned
}

func parseAnnexMetadataOutput(out []byte) annexMetadataLookup {
	result := newAnnexMetadataLookup()
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		var entry annexMetadataEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		cleaned := cleanAnnexMetadataFields(entry.Fields)
		if len(cleaned) == 0 {
			continue
		}
		if entry.File != "" {
			result.ByFile[entry.File] = cleaned
		}
		if entry.Key != "" {
			result.ByKey[entry.Key] = cleaned
		}
	}
	return result
}

// getAnnexMetadata retrieves metadata for annexed files.
// When paths is non-empty, queries those paths directly.
// When paths is empty, uses --all to retrieve metadata for all annexed files.
func getAnnexMetadata(paths []string) (annexMetadataLookup, error) {
	if len(paths) > 0 {
		var out bytes.Buffer
		var firstErr error
		const chunkSize = 512

		for start := 0; start < len(paths); start += chunkSize {
			end := start + chunkSize
			if end > len(paths) {
				end = len(paths)
			}

			args := append([]string{"annex", "metadata", "--json", "--"}, paths[start:end]...)
			chunkOut, err := commandutil.Command("git", args...).Output()
			if len(chunkOut) > 0 {
				out.Write(chunkOut)
				if chunkOut[len(chunkOut)-1] != '\n' {
					out.WriteByte('\n')
				}
			}
			if err != nil && firstErr == nil {
				firstErr = err
			}
		}

		if out.Len() == 0 && firstErr != nil {
			return annexMetadataLookup{}, firstErr
		}
		return parseAnnexMetadataOutput(out.Bytes()), nil
	}

	out, err := commandutil.Command("git", "annex", "metadata", "--json", "--all").Output()
	if err != nil {
		// --all may not be supported in older versions; try without
		out, err = commandutil.Command("git", "annex", "metadata", "--json").Output()
		if err != nil {
			return annexMetadataLookup{}, err
		}
	}
	return parseAnnexMetadataOutput(out), nil
}

// getAnnexWhereis retrieves location data for annexed files using batch mode.
// files is the list of annexed file paths to query. Output is streamed to
// avoid buffering potentially gigabytes of JSON in memory.
func getAnnexWhereis(files []string) (map[string]annexWhereisEntry, error) {
	if len(files) == 0 {
		return make(map[string]annexWhereisEntry), nil
	}

	cmd := commandutil.Command("git", "annex", "whereis", "--batch", "--json")
	cmd.Stdin = strings.NewReader(strings.Join(files, "\n") + "\n")

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start git annex whereis: %w", err)
	}

	result := make(map[string]annexWhereisEntry, len(files))
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024) // 10MB max line
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		var entry annexWhereisEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		result[entry.File] = entry
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading whereis output: %w", err)
	}

	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("git annex whereis failed: %w", err)
	}

	return result, nil
}

// buildLocations constructs Location entries for an annexed file,
// filtering out local remotes and enriching with .exohub/remotes config.
func buildLocations(filePath, key string, fileSize int64, whereis annexWhereisEntry, remoteConfigs map[string]RemoteConfig) []Location {
	var locations []Location

	// Process trusted and untrusted remotes
	allRemotes := make([]struct {
		remote    annexWhereisRemote
		untrusted bool
	}, 0)
	for _, r := range whereis.Whereis {
		allRemotes = append(allRemotes, struct {
			remote    annexWhereisRemote
			untrusted bool
		}{r, false})
	}
	for _, r := range whereis.Untrusted {
		allRemotes = append(allRemotes, struct {
			remote    annexWhereisRemote
			untrusted bool
		}{r, true})
	}

	for _, entry := range allRemotes {
		r := entry.remote

		// Skip local remotes
		if r.Here {
			continue
		}

		// Match remote by UUID first, then fall back to name extraction
		var rc RemoteConfig
		var remoteName string
		matched := false
		for _, cfg := range remoteConfigs {
			if cfg.UUID != "" && cfg.UUID == r.UUID {
				rc = cfg
				remoteName = cfg.Name
				matched = true
				break
			}
		}
		if !matched {
			// Fall back to name extraction from description
			remoteName = extractRemoteName(r.Description)
			if remoteName == "" {
				continue
			}
			var ok bool
			rc, ok = remoteConfigs[remoteName]
			if !ok {
				continue
			}
		}

		s3url := strings.TrimRight(rc.S3URL, "/")

		loc := Location{
			Remote:    remoteName,
			UUID:      r.UUID,
			Untrusted: entry.untrusted,
			Type:      rc.Type,
			S3URL:     s3url,
		}

		switch rc.Type {
		case "annex":
			// S3 object path for direct (non-chunked) files
			loc.S3Object = s3url + "/" + key

			// Check if file exceeds chunk size → derive chunk keys
			if rc.Chunk != "" {
				chunkSize := parseChunkSize(rc.Chunk)
				if chunkSize > 0 {
					loc.Chunks = deriveChunks(key, fileSize, chunkSize, s3url)
					loc.S3Object = "" // chunked files don't have a single S3 object
				}
			}

		case "export":
			loc.ExportPath = s3url + "/" + filePath
		}

		locations = append(locations, loc)
	}

	return locations
}

// extractRemoteName extracts the remote name from a git-annex whereis description.
// Format: "uuid -- name [origin]" or just "name" or "[name]"
func extractRemoteName(desc string) string {
	name := desc
	if idx := strings.Index(desc, " -- "); idx != -1 {
		name = desc[idx+4:]
		// Remove bracketed suffix like " [origin]"
		if bidx := strings.Index(name, " ["); bidx != -1 {
			name = name[:bidx]
		}
	}
	name = strings.TrimSpace(name)
	// Strip surrounding brackets (e.g. "[s3-annex]" → "s3-annex")
	if strings.HasPrefix(name, "[") && strings.HasSuffix(name, "]") {
		name = name[1 : len(name)-1]
	}
	return name
}

// parseChunkSize parses a chunk size string like "1GiB" or "500MiB" into bytes.
func parseChunkSize(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}

	multiplier := int64(1)
	numStr := s

	// Check for suffixes (case-insensitive)
	upper := strings.ToUpper(s)
	switch {
	case strings.HasSuffix(upper, "GIB"):
		multiplier = 1024 * 1024 * 1024
		numStr = s[:len(s)-3]
	case strings.HasSuffix(upper, "GB"):
		multiplier = 1000 * 1000 * 1000
		numStr = s[:len(s)-2]
	case strings.HasSuffix(upper, "MIB"):
		multiplier = 1024 * 1024
		numStr = s[:len(s)-3]
	case strings.HasSuffix(upper, "MB"):
		multiplier = 1000 * 1000
		numStr = s[:len(s)-2]
	case strings.HasSuffix(upper, "KIB"):
		multiplier = 1024
		numStr = s[:len(s)-3]
	case strings.HasSuffix(upper, "KB"):
		multiplier = 1000
		numStr = s[:len(s)-2]
	}

	n, err := strconv.ParseInt(numStr, 10, 64)
	if err != nil {
		return 0
	}
	return n * multiplier
}

// deriveChunks derives chunk keys and S3 paths for a chunked annexed file.
// git-annex chunk key format: {prefix}-S{chunksize}-C{N}--{hash}{ext}
func deriveChunks(key string, fileSize, chunkSize int64, s3URL string) []Chunk {
	// Parse key components: prefix--hash.ext
	ext := ""
	if idx := strings.LastIndex(key, "."); idx != -1 {
		ext = key[idx:]
	}
	noExt := key
	if ext != "" {
		noExt = strings.TrimSuffix(key, ext)
	}
	parts := strings.SplitN(noExt, "--", 2)
	if len(parts) != 2 {
		return nil
	}
	prefix := parts[0]
	hash := parts[1]

	numChunks := (fileSize + chunkSize - 1) / chunkSize
	chunks := make([]Chunk, 0, numChunks)
	remaining := fileSize

	for i := int64(1); i <= numChunks; i++ {
		thisChunkSize := chunkSize
		if remaining < chunkSize {
			thisChunkSize = remaining
		}
		chunkKey := fmt.Sprintf("%s-S%d-C%d--%s%s", prefix, chunkSize, i, hash, ext)
		chunks = append(chunks, Chunk{
			ChunkKey:    chunkKey,
			ChunkNumber: int(i),
			ChunkSize:   thisChunkSize,
			S3Path:      s3URL + "/" + chunkKey,
		})
		remaining -= thisChunkSize
	}

	return chunks
}

// parseAnnexKey extracts backend and size from a git-annex key.
// Key format: "BACKEND-sNNN--hash.ext" (e.g. "SHA256E-s12345--abc.parquet")
func parseAnnexKey(key string) (backend string, size int64) {
	// Backend is everything before the first "-"
	dashIdx := strings.IndexByte(key, '-')
	if dashIdx < 0 {
		return key, 0
	}
	backend = key[:dashIdx]

	// Size is after "-s" prefix
	rest := key[dashIdx+1:]
	if strings.HasPrefix(rest, "s") {
		sizeStr := rest[1:]
		// Size ends at next "-"
		if idx := strings.IndexByte(sizeStr, '-'); idx >= 0 {
			sizeStr = sizeStr[:idx]
		}
		size, _ = strconv.ParseInt(sizeStr, 10, 64)
	}
	return backend, size
}

// expandCommits converts a list of commit hashes to full Commit objects.
func expandCommits(hashes []string, commitMap map[string]Commit) []Commit {
	if len(hashes) == 0 {
		return []Commit{}
	}
	result := make([]Commit, 0, len(hashes))
	seen := make(map[string]bool)
	for _, h := range hashes {
		if seen[h] {
			continue
		}
		seen[h] = true
		if c, ok := commitMap[h]; ok {
			result = append(result, c)
		}
	}
	// Sort by date descending (most recent first)
	sort.Slice(result, func(i, j int) bool {
		return result[i].CommitterDate > result[j].CommitterDate
	})
	return result
}

// buildBlobInfoMap runs a single `git ls-tree -r -l HEAD` and returns a map
// from file path to its blob hash and size. This replaces N per-file
// getGitBlobInfo calls with a single subprocess.
func buildBlobInfoMap(pathFilters []string) (map[string]blobInfo, error) {
	args := []string{"ls-tree", "-r", "-l", "HEAD"}
	if len(pathFilters) > 0 {
		args = append(args, "--")
		args = append(args, pathFilters...)
	}
	cmd := commandutil.Command("git", args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-tree failed: %w", err)
	}

	result := make(map[string]blobInfo)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		// Format: <mode> <type> <hash> <size>\t<path>
		tabIdx := strings.IndexByte(line, '\t')
		if tabIdx < 0 {
			continue
		}
		path := line[tabIdx+1:]
		fields := strings.Fields(line[:tabIdx])
		if len(fields) < 4 {
			continue
		}
		hash := fields[2]
		size, _ := strconv.ParseInt(fields[3], 10, 64)
		result[path] = blobInfo{Hash: hash, Size: size}
	}
	return result, nil
}

// isExohubPath returns true if the path is under the .exohub/ directory.
func isExohubPath(path string) bool {
	return path == ".exohub" || strings.HasPrefix(path, ".exohub/")
}

// isArtifactDBPath returns true if the path is under the .artifactdb/ directory.
func isArtifactDBPath(path string) bool {
	return path == ".artifactdb" || strings.HasPrefix(path, ".artifactdb/")
}

// isHiddenPath returns true if any path component starts with ".".
func isHiddenPath(path string) bool {
	for _, part := range strings.Split(path, "/") {
		if strings.HasPrefix(part, ".") {
			return true
		}
	}
	return false
}

// shouldSkipPath determines whether a file path should be excluded from the bundle.
// Returns (skip, reason) where reason is non-empty only for debug-worthy skips.
func shouldSkipPath(path string, preset Preset) (bool, string) {
	// Tier 1: .exohub/ is always excluded unconditionally
	if isExohubPath(path) {
		return true, ""
	}

	// Tier 2: .artifactdb/ is excluded unless explicitly matched by include patterns
	if isArtifactDBPath(path) {
		if len(preset.Include) > 0 {
			for _, pattern := range preset.Include {
				if globMatch(pattern, path) {
					return false, ""
				}
			}
		}
		return true, fmt.Sprintf("skipping %s (under .artifactdb/; add to include: to override)", path)
	}

	// Tier 3: other hidden paths excluded unless include_hidden is set
	if !preset.IncludeHidden && isHiddenPath(path) {
		return true, ""
	}

	return false, ""
}

// filterAnnexFiles applies include/exclude patterns from the preset to a list of annexed files.
func filterAnnexFiles(files []annexFindEntry, preset Preset) []annexFindEntry {
	if len(preset.Include) == 0 && len(preset.Exclude) == 0 {
		return files
	}

	var result []annexFindEntry
	for _, f := range files {
		if matchesFilters(f.File, preset) {
			result = append(result, f)
		}
	}
	return result
}

// matchesFilters checks if a file path matches the include/exclude patterns.
func matchesFilters(path string, preset Preset) bool {
	// If include patterns are set, the file must match at least one
	if len(preset.Include) > 0 {
		matched := false
		for _, pattern := range preset.Include {
			if globMatch(pattern, path) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	// If exclude patterns are set, the file must not match any
	for _, pattern := range preset.Exclude {
		if globMatch(pattern, path) {
			return false
		}
	}

	return true
}

// globMatch performs glob matching supporting ** for recursive matching.
func globMatch(pattern, path string) bool {
	// Handle ** patterns by trying filepath.Match on each path segment
	if strings.Contains(pattern, "**") {
		// Convert **/ to match any number of path segments
		// Simple approach: replace ** with a regex-like match
		regexPat := "^" + regexp.QuoteMeta(pattern) + "$"
		regexPat = strings.ReplaceAll(regexPat, regexp.QuoteMeta("**/"), "(.*/)?")
		regexPat = strings.ReplaceAll(regexPat, regexp.QuoteMeta("/**"), "(/.*)?")
		regexPat = strings.ReplaceAll(regexPat, regexp.QuoteMeta("**"), ".*")
		regexPat = strings.ReplaceAll(regexPat, regexp.QuoteMeta("*"), "[^/]*")
		regexPat = strings.ReplaceAll(regexPat, regexp.QuoteMeta("?"), "[^/]")
		if re, err := regexp.Compile(regexPat); err == nil {
			return re.MatchString(path)
		}
	}

	// Simple glob matching
	matched, _ := filepath.Match(pattern, path)
	if matched {
		return true
	}
	// Also try matching against the basename
	matched, _ = filepath.Match(pattern, filepath.Base(path))
	return matched
}

// writeJSON writes a value as indented JSON to a file, creating directories as needed.
func writeJSON(path string, v interface{}) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

// exitWithError prints an error and exits.
func exitWithError(err error) {
	var exitErr *exec.ExitError
	if ok := false; ok {
		_ = exitErr
	}
	fmt.Fprintln(os.Stderr, err.Error())
	os.Exit(1)
}

// --- Artipack support ---

const (
	defaultArtipackThreshold = 100
	defaultArtipackMaxFiles  = 100000
	artipackExtension        = ".artipack"
)

// maybeCreateArtipacks packs bundle output files into .artipack ZIP archives
// when the file count exceeds the threshold. Controlled by env vars:
//   - ARTIFACTDB_ZIP_ENABLED (default: true)
//   - ARTIFACTDB_ZIP_THRESHOLD (default: 1000)
//   - ARTIFACTDB_ZIP_MAX_FILES (default: 100000)
func maybeCreateArtipacks(outputDir string) error {
	if !envBool("ARTIFACTDB_ZIP_ENABLED", true) {
		return nil
	}

	threshold := envInt("ARTIFACTDB_ZIP_THRESHOLD", defaultArtipackThreshold)
	maxFiles := envInt("ARTIFACTDB_ZIP_MAX_FILES", defaultArtipackMaxFiles)

	// Collect all files in the output directory
	var files []string
	err := filepath.Walk(outputDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && filepath.Ext(path) != artipackExtension && filepath.Base(path) != "bundle.json" && filepath.Base(path) != ".nolocations" {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return err
	}

	if len(files) <= threshold {
		return nil
	}

	fmt.Fprintf(os.Stderr, "Packing %d files into artipacks (threshold: %d)\n", len(files), threshold)

	// Create artipack archive(s)
	for i := 0; i*maxFiles < len(files); i++ {
		start := i * maxFiles
		end := (i + 1) * maxFiles
		if end > len(files) {
			end = len(files)
		}

		archivePath := filepath.Join(outputDir, fmt.Sprintf("%d%s", i, artipackExtension))
		if err := createArtipackArchive(archivePath, files[start:end], outputDir); err != nil {
			return fmt.Errorf("failed to create artipack %s: %w", archivePath, err)
		}
		fmt.Fprintf(os.Stderr, "  created %s (%d files)\n", archivePath, end-start)
	}

	// Delete original files (they're now in the artipack)
	for _, f := range files {
		_ = os.Remove(f)
	}

	// Clean up empty directories
	cleanEmptyDirs(outputDir)

	return nil
}

func createArtipackArchive(archivePath string, files []string, basePath string) error {
	f, err := os.Create(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	defer zw.Close()

	for _, file := range files {
		if err := addFileToArtipack(zw, file, basePath); err != nil {
			return err
		}
	}
	return nil
}

func addFileToArtipack(zw *zip.Writer, filePath, basePath string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return err
	}

	header, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}

	relPath, err := filepath.Rel(basePath, filePath)
	if err != nil {
		return err
	}
	header.Name = relPath
	header.Method = zip.Deflate

	w, err := zw.CreateHeader(header)
	if err != nil {
		return err
	}

	_, err = io.Copy(w, f)
	return err
}

// cleanEmptyDirs removes empty directories under root (deepest first).
func cleanEmptyDirs(root string) {
	// Collect all directories, then process deepest first so that removing
	// a leaf directory allows its parent to be detected as empty too.
	var dirs []string
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || !info.IsDir() || path == root {
			return err
		}
		dirs = append(dirs, path)
		return nil
	})
	sort.Slice(dirs, func(i, j int) bool {
		return len(dirs[i]) > len(dirs[j])
	})
	for _, d := range dirs {
		entries, _ := os.ReadDir(d)
		if len(entries) == 0 {
			os.Remove(d)
		}
	}
}

func envBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}
