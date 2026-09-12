package sync

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	gosync "sync"
	"syscall"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	initcmd "github.com/Genentech/exohub/go/exo/commands/init"
	"github.com/Genentech/exohub/go/exo/commandutil"
	"github.com/Genentech/exohub/go/exo/configdir"
	manifestutil "github.com/Genentech/exohub/go/exo/commandutil"
	logging "github.com/Genentech/exohub/go/exo/commandutil/logging"
	"github.com/Genentech/exohub/go/exo/internal/defaults"
	"github.com/Genentech/exohub/go/exo/palette"
)

type fileInfo struct {
	Path string `json:"file"`
	Size int64  `json:"bytes,omitempty"`
}

type dryRunResult struct {
	Label string
	Files []fileInfo
	Total int64
}

const (
	longText = `Sync git-annex content and record bad files.

Filtering options (for import and annex remotes):
  --include and --exclude accept git-annex glob patterns:
    *       matches any characters except /
    **      matches any characters including /
    ?       matches single character
  Examples: "*.bam", "results/**/*.csv", "data/2024-*/*.fastq.gz"
  Note: Filtering is not supported for export remotes (they mirror entire branches).

Import options (for import remotes):
  --no-content imports metadata only without storing file content locally.
               Creates tracking info (checksums, keys, locations) but drops
               content immediately. Useful for populating repos without storage.

Dry-run guarantee (--dry-run):
  The --dry-run flag produces a faithful, remote-type-aware preview of exactly
  what the real sync would do. Specifically:
  - Export remotes: actions are computed against the remote's annex-tracking-branch
    (not HEAD). If the current branch differs from the tracking branch, the dry-run
    reports no export actions and emits a clear warning.
  - Annex remotes with preferred content: the "to copy" list is filtered by the
    preferred content expression using a matcher that correctly handles ** globs.
  - No "drop candidates" section is ever shown. The real annex/export sync never
    drops local content, so reporting drops would be misleading.`
)

type manifest struct {
	RepoDir     string      `yaml:"repo-dir"`
	WithRemotes stringList  `yaml:"with-remotes"`
	Paths       stringList  `yaml:"paths"`
	Jobs        stringValue `yaml:"jobs"`
}

type stringList []string
type stringValue string

func (s *stringList) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		if strings.TrimSpace(node.Value) == "" {
			return nil
		}
		*s = append(*s, strings.TrimSpace(node.Value))
	case yaml.SequenceNode:
		for _, item := range node.Content {
			if item.Kind != yaml.ScalarNode {
				return fmt.Errorf("unsupported list value: %v", item.Kind)
			}
			if strings.TrimSpace(item.Value) != "" {
				*s = append(*s, strings.TrimSpace(item.Value))
			}
		}
	default:
		return fmt.Errorf("unsupported yaml kind: %v", node.Kind)
	}
	return nil
}

func (s *stringValue) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode {
		return fmt.Errorf("unsupported yaml kind: %v", node.Kind)
	}
	*s = stringValue(strings.TrimSpace(node.Value))
	return nil
}

func (m manifest) repoDir() string {
	return m.RepoDir
}

func NewCommand() *cobra.Command {
	var manifestPath string
	var withRemotes []string
	var paths []string
	var includePatterns []string
	var excludePatterns []string
	var importDir string
	var dryRun bool
	var outputJSON bool
	var logPrefix string
	var jobsFlag string
	var noContent bool

	rootCmd := &cobra.Command{
		Use:   "sync",
		Short: "Sync git-annex content and record bad files",
		Long:  longText,
		Run: func(cmd *cobra.Command, args []string) {
			if len(args) > 0 {
				_ = cmd.Help()
				os.Exit(1)
			}
			if outputJSON && !dryRun {
				fmt.Fprintln(os.Stderr, "--json requires --dry-run")
				os.Exit(1)
			}
			runSync(manifestPath, withRemotes, paths, includePatterns, excludePatterns, importDir, dryRun, outputJSON, logPrefix, jobsFlag, noContent)
		},
	}

	rootCmd.Flags().StringVar(&manifestPath, "manifest", "", "Manifest YAML file")
	rootCmd.Flags().StringArrayVar(&withRemotes, "with", nil, "Annex remote to sync")
	rootCmd.Flags().StringArrayVar(&paths, "path", nil, "Path to limit sync (repeatable)")
	rootCmd.Flags().StringArrayVar(&includePatterns, "include", nil, "Include files matching glob pattern (repeatable, for import/annex remotes)")
	rootCmd.Flags().StringArrayVar(&excludePatterns, "exclude", nil, "Exclude files matching glob pattern (repeatable, for import/annex remotes)")
	rootCmd.Flags().StringVar(&importDir, "import-dir", "", "Directory to place imported files (for import remotes, e.g. 'input/')")
	rootCmd.Flags().BoolVar(&dryRun, "dry-run", false, "Report planned sync actions without executing git-annex sync")
	rootCmd.Flags().BoolVar(&outputJSON, "json", false, "Emit a single JSON dry-run report to stdout")
	rootCmd.Flags().StringVar(&logPrefix, "log-prefix", "", "Prefix for log filenames (useful for Temporal workflows)")
	rootCmd.Flags().StringVarP(&jobsFlag, "jobs", "J", "", "Number of parallel jobs (default: 1)")
	rootCmd.Flags().BoolVar(&noContent, "no-content", false, "Import metadata only without storing content locally (import remotes only)")

	return rootCmd
}

func runSync(manifestPath string, withRemotes []string, paths []string, includePatterns []string, excludePatterns []string, importDir string, dryRun bool, outputJSON bool, logPrefix string, jobsFlag string, noContent bool) {
	if err := requireBins("git-annex"); err != nil {
		os.Exit(127)
	}

	// Initialize jobs from environment variable or default
	jobs := os.Getenv("EXOHUB_JOBS")
	if jobs == "" {
		jobs = "1"
	}

	if manifestPath != "" {
		if err := runManifestValidation(manifestPath); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
		m, err := parseManifest(manifestPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
		if len(withRemotes) == 0 {
			withRemotes = append(withRemotes, m.WithRemotes...)
		}
		if len(paths) == 0 {
			paths = append(paths, m.Paths...)
		}
		if m.Jobs != "" {
			jobs = string(m.Jobs)
		}
		if repoDir := m.repoDir(); repoDir != "" {
			if !isDir(repoDir) {
				fmt.Fprintf(os.Stderr, "Repo directory does not exist: %s\n", repoDir)
				os.Exit(1)
			}
			if err := os.Chdir(repoDir); err != nil {
				fmt.Fprintf(os.Stderr, "Failed to enter repo dir: %s\n", repoDir)
				os.Exit(1)
			}
		}
	}

	// CLI flag takes precedence over manifest and environment variable
	if jobsFlag != "" {
		jobs = jobsFlag
	}

	_ = os.Setenv("EXOHUB_JOBS", jobs)

	if !isDir(".git") {
		fmt.Fprintln(os.Stderr, "Current directory is not a git repository")
		os.Exit(1)
	}

	if !annexInitialized() {
		fmt.Printf("Initializing git-annex in %s\n", logging.MustCwd())
		if err := runCommand([]string{"git", "annex", "init"}); err != nil {
			os.Exit(1)
		}
	}

	expandedPaths := expandPaths(paths)

	// Load remote configs from .exohub/remotes if available
	remotesConfig, err := initcmd.LoadRemotesConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load .exohub/remotes: %v\n", err)
		os.Exit(1)
	}

	if dryRun {
		if err := runDryRun(withRemotes, expandedPaths, outputJSON, jobs, logPrefix, remotesConfig); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
		return
	}

	// When no --with specified, discover all git-annex remotes
	if len(withRemotes) == 0 {
		discoveredRemotes, err := listAnnexRemotes()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to discover git-annex remotes: %v\n", err)
			os.Exit(1)
		}

		if len(discoveredRemotes) == 0 {
			fmt.Println("No git-annex remotes found")
			return
		}

		// Check if any discovered remotes have patterns or grants
		hasAnyPatterns := false
		hasAnyGrants := false
		for _, remote := range discoveredRemotes {
			include, exclude := getPatternsForRemote(remote, remotesConfig, includePatterns, excludePatterns)
			if len(include) > 0 || len(exclude) > 0 {
				hasAnyPatterns = true
			}
			if remoteHasGrantsConfig(remote) {
				hasAnyGrants = true
			}
		}

		// If no remotes have patterns AND no CLI patterns AND no grants, use unified sync.
		// Grants remotes must go through per-remote loop so we can check read-only access.
		if !hasAnyPatterns && !hasAnyGrants && len(includePatterns) == 0 && len(excludePatterns) == 0 {
			fmt.Println("Syncing content with all remotes")
			var err error
			if len(expandedPaths) > 0 {
				cmd := []string{"git", "annex", "sync", "--content", "--no-commit", "--jobs", jobs}
				for _, p := range expandedPaths {
					fmt.Printf("  - path: %s\n", p)
					cmd = append(cmd, "--content-of", p)
				}
				err = logging.ExecuteWithLoggingPrefixRetry("content", "all", cmd, expandedPaths, jobs, logPrefix, true)
			} else {
				err = logging.ExecuteWithLoggingPrefixRetry("content", "all", []string{"git", "annex", "sync", "--content", "--no-commit", "--jobs", jobs}, nil, jobs, logPrefix, true)
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "Sync failed: %v\n", err)
				os.Exit(1)
			}
			fmt.Println("Metadata sync skipped; run exo broadcast to publish metadata")
			return
		}

		// Otherwise, sync each remote individually (some may have patterns)
		fmt.Println("Syncing content with all remotes")
		withRemotes = discoveredRemotes
		// Continue to the individual remote loop below
	}

	for _, remote := range withRemotes {
		// Handle catalog remotes (non-standard types with a publish subcommand)
		if isCatalogRemoteType(remote, remotesConfig) {
			catalogBinary, ok := getCatalogRemoteBinary(remote, remotesConfig)
			if !ok {
				rc := getRemoteConfigByName(remote, remotesConfig)
				if rc != nil && rc.Mode == "" {
					fmt.Fprintf(os.Stderr, "Error: catalog remote '%s' (type: %s) requires 'mode' field (e.g. mode: export)\n", remote, rc.Type)
				} else {
					fmt.Fprintf(os.Stderr, "Error: binary 'git-annex-remote-%s-%s' not found in PATH\n", rc.Type, rc.Mode)
				}
				os.Exit(1)
			}
			// Check publish_on constraint
			if shouldSkipPublish(remote, remotesConfig) {
				fmt.Printf("💎  Skipping catalog remote '%s' (publish_on: tag, HEAD is not a tag)\n", remote)
				continue
			}
			if dryRun {
				rc := getRemoteConfigByName(remote, remotesConfig)
				apiURL := defaults.APIBase()
				fmt.Printf("Would publish to catalog remote '%s' via %s\n", remote, catalogBinary)
				fmt.Printf("  api-url:      %s\n", apiURL)
				fmt.Printf("  s3url:        %s\n", rc.S3URL)
				if rc.InstanceURL != "" {
					fmt.Printf("  instance-url: %s\n", rc.InstanceURL)
				}
				fmt.Printf("  grants:       %v\n", rc.Grants)
				continue
			}
			if err := syncCatalogRemote(remote, remotesConfig, catalogBinary); err != nil {
				fmt.Fprintf(os.Stderr, "Publish failed for '%s': %v\n", remote, err)
				os.Exit(1)
			}
			continue
		}

		// Get patterns for this remote (CLI overrides manifest)
		includeForRemote, excludeForRemote := getPatternsForRemote(remote, remotesConfig, includePatterns, excludePatterns)
		hasPatterns := len(includeForRemote) > 0 || len(excludeForRemote) > 0

		// Ensure AWS credentials are available for import remotes
		if isImportRemote(remote) {
			if err := ensureAWSCredentials(); err != nil {
				fmt.Fprintf(os.Stderr, "⚠️  AWS credentials not available for import remote '%s': %v\n", remote, err)
				fmt.Fprintln(os.Stderr, "Please set AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY or configure ~/.aws/credentials")
				os.Exit(1)
			}
		}

		// For drive remotes, check auth token before attempting enableremote
		if isDriveRemote(remote) {
			if _, err := os.Stat(driveTokenPath(remote)); os.IsNotExist(err) {
				fmt.Fprintf(os.Stderr, "❌ Drive remote '%s' is not authorized on this machine.\n", remote)
				fmt.Fprintf(os.Stderr, "   Run: exo auth %s\n", remote)
				os.Exit(1)
			}
		}

		fmt.Printf("Enabling git-annex remote '%s'\n", remote)
		if err := runCommand([]string{"git", "annex", "enableremote", remote}); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to enable remote '%s'\n", remote)
			os.Exit(1)
		}

		// For grants-enabled remotes, check write access.
		// Viewers only have READ grants - skip export/store and pull content only.
		if remoteHasGrantsConfig(remote) && !hasGrantsWriteAccess(remote) {
			fmt.Printf("Syncing content from remote '%s' (read-only access)\n", remote)
			cmd := []string{"git", "annex", "sync", "--content", "--no-commit", remote, "--jobs", jobs}
			if len(expandedPaths) > 0 {
				for _, p := range expandedPaths {
					fmt.Printf("  - path: %s\n", p)
					cmd = append(cmd, "--content-of", p)
				}
			}
			if err := runCommand(cmd); err != nil {
				fmt.Fprintf(os.Stderr, "Sync from remote '%s' failed: %v\n", remote, err)
				os.Exit(1)
			}
			continue
		}

		// Handle import remotes (use preferred content, error on CLI flags)
		if isImportRemote(remote) {
			// Import remotes only use preferred content (set via exo init)
			// Pass CLI flags to check and error if provided
			// Get import directory (CLI overrides manifest)
			remoteImportDir := getImportDirForRemote(remote, remotesConfig, importDir)
			if err := syncImportRemoteWithPatterns(remote, includePatterns, excludePatterns, remoteImportDir, jobs, logPrefix, noContent); err != nil {
				fmt.Fprintf(os.Stderr, "%v\n", err)
				os.Exit(1)
			}
			fmt.Printf("Metadata sync skipped for '%s'; run exo broadcast to publish metadata\n", remote)
			continue
		}

		// Handle export remotes with preferred content (must use export, not sync)
		if isExportRemote(remote) {
			currentWanted, _ := runCommandOutput([]string{"git", "annex", "wanted", remote})
			currentWanted = strings.TrimSpace(currentWanted)
			hasPreferredContent := currentWanted != "" && currentWanted != "standard"

			if hasPreferredContent {
				fmt.Printf("Exporting to remote '%s'\n", remote)
				fmt.Printf("  preferred content: %s\n", currentWanted)
				// Get tracking branch for this remote
				trackingBranch, _ := runCommandOutput([]string{"git", "config", "--get", fmt.Sprintf("remote.%s.annex-tracking-branch", remote)})
				trackingBranch = strings.TrimSpace(trackingBranch)
				if trackingBranch == "" {
					trackingBranch = "HEAD"
				}
				// Build export refs - use ref:path notation if paths specified
				var exportRefs []string
				if len(expandedPaths) > 0 {
					fmt.Printf("  limited to paths:\n")
					for _, p := range expandedPaths {
						fmt.Printf("    - %s\n", p)
						exportRefs = append(exportRefs, fmt.Sprintf("%s:%s", trackingBranch, p))
					}
				} else {
					exportRefs = []string{trackingBranch}
				}
				// Use git annex export which respects preferred content for export remotes
				cmd := []string{"git", "annex", "export"}
				cmd = append(cmd, exportRefs...)
				cmd = append(cmd, "--to", remote, "--jobs", jobs)
				if err := runCommand(cmd); err != nil {
					fmt.Fprintf(os.Stderr, "Export failed: %v\n", err)
					os.Exit(1)
				}
				fmt.Printf("Metadata sync skipped for '%s'; run exo broadcast to publish metadata\n", remote)
				continue
			}
		}

		// Handle annex/exospace remotes with patterns or preferred content
		if !isExportRemote(remote) && hasPatterns {
			// Check if remote has preferred content configured
			currentWanted, _ := runCommandOutput([]string{"git", "annex", "wanted", remote})
			currentWanted = strings.TrimSpace(currentWanted)
			hasPreferredContent := currentWanted != "" && currentWanted != "standard"

			// If preferred content exists, use sync --content with optional path limiting
			if hasPreferredContent {
				fmt.Printf("Syncing content with remote '%s'\n", remote)
				fmt.Printf("  preferred content: %s\n", currentWanted)
				cmd := []string{"git", "annex", "sync", "--content", "--no-commit"}
				if len(expandedPaths) > 0 {
					fmt.Printf("  limited to paths:\n")
					for _, p := range expandedPaths {
						fmt.Printf("    - %s\n", p)
						cmd = append(cmd, "--content-of", p)
					}
				}
				cmd = append(cmd, remote, "--jobs", jobs)
				if err := runCommand(cmd); err != nil {
					fmt.Fprintf(os.Stderr, "Sync failed: %v\n", err)
					os.Exit(1)
				}
				fmt.Printf("Metadata sync skipped for '%s'; run exo broadcast to publish metadata\n", remote)
				continue
			}

			// No preferred content, use split sync with patterns (pass CLI flag indicator)
			fromCLI := len(includePatterns) > 0 || len(excludePatterns) > 0
			if err := syncAnnexRemoteWithPatterns(remote, includeForRemote, excludeForRemote, expandedPaths, jobs, logPrefix, fromCLI); err != nil {
				fmt.Fprintf(os.Stderr, "%v\n", err)
				os.Exit(1)
			}
			fmt.Printf("Metadata sync skipped for '%s'; run exo broadcast to publish metadata\n", remote)
			continue
		}

		// Handle remotes without patterns (current behavior)
		fmt.Printf("Syncing content with remote '%s'\n", remote)
		var err error
		if len(expandedPaths) > 0 {
			cmd := []string{"git", "annex", "sync", "--content", "--no-commit"}
			for _, p := range expandedPaths {
				fmt.Printf("  - path: %s\n", p)
				cmd = append(cmd, "--content-of", p)
			}
			cmd = append(cmd, remote, "--jobs", jobs)
			err = logging.ExecuteWithLoggingPrefixRetry("content", remote, cmd, expandedPaths, jobs, logPrefix, true)
		} else {
			cmd := []string{"git", "annex", "sync", "--content", "--no-commit", remote, "--jobs", jobs}
			err = logging.ExecuteWithLoggingPrefixRetry("content", remote, cmd, nil, jobs, logPrefix, true)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "Sync with remote '%s' failed: %v\n", remote, err)
			os.Exit(1)
		}
		fmt.Printf("Metadata sync skipped for '%s'; run exo broadcast to publish metadata\n", remote)
	}
}

func runDryRun(withRemotes []string, paths []string, outputJSON bool, jobs string, logPrefix string, remotesConfig *initcmd.RemotesConfig) error {
	remotes := withRemotes
	if len(remotes) == 0 {
		var err error
		remotes, err = listAnnexRemotes()
		if err != nil {
			return err
		}
	}
	if outputJSON {
		var buf bytes.Buffer
		writer := io.MultiWriter(os.Stdout, &buf)

		// Check if any remotes are import remotes - use custom JSON generation
		hasImportRemote := false
		for _, remote := range remotes {
			if isImportRemote(remote) {
				hasImportRemote = true
				break
			}
		}

		if hasImportRemote {
			// Generate JSON with special handling for import remotes
			if err := streamDryRunJSONWithImport(writer, remotes, paths, defaults.DryRunReportSchema(), jobs); err != nil {
				return err
			}
		} else {
			// Standard JSON generation for annex/export remotes
			if err := commandutil.StreamDryRunJSON(writer, remotes, paths, true, defaults.DryRunReportSchema()); err != nil {
				return err
			}
		}

		remoteLabel := strings.Join(remotes, "+")
		if remoteLabel == "" {
			remoteLabel = "none"
		}
		extras := map[string]any{
			"dry_run":          true,
			"remote_mutations": false,
			"json":             true,
		}
		cmd := buildSyncDryRunCmd(remotes, true)
		return logging.RecordDryRunLogPrefix("sync", remoteLabel, cmd, paths, jobs, buf.String(), extras, logPrefix)
	}
	if len(remotes) == 0 {
		var buf bytes.Buffer
		writer := io.MultiWriter(os.Stdout, &buf)
		fmt.Fprintln(writer, "No git-annex remotes found for dry-run")
		cmd := buildSyncDryRunCmd(remotes, false)
		extras := map[string]any{
			"dry_run":          true,
			"remote_mutations": false,
		}
		return logging.RecordDryRunLogPrefix("sync", "none", cmd, paths, jobs, buf.String(), extras, logPrefix)
	}
	for _, remote := range remotes {
		var buf bytes.Buffer
		writer := io.MultiWriter(os.Stdout, &buf)

		// Handle catalog remotes: show publish info
		if isCatalogRemoteType(remote, remotesConfig) {
			rc := getRemoteConfigByName(remote, remotesConfig)
			apiURL := defaults.APIBase()
			catalogBinary, _ := getCatalogRemoteBinary(remote, remotesConfig)
			fmt.Fprintf(writer, "Would publish to catalog remote '%s' via %s\n", remote, catalogBinary)
			fmt.Fprintf(writer, "  api-url:      %s\n", apiURL)
			fmt.Fprintf(writer, "  s3url:        %s\n", rc.S3URL)
			if rc.InstanceURL != "" {
				fmt.Fprintf(writer, "  instance-url: %s\n", rc.InstanceURL)
			}
			fmt.Fprintf(writer, "  grants:       %v\n", rc.Grants)
		} else if isImportRemote(remote) {
			// Handle import remotes specially: use S3 API directly
			if err := runImportDryRun(writer, remote, jobs); err != nil {
				return err
			}
		} else {
			// Standard dry-run for annex/export remotes
			if err := runAnnexDryRun(writer, remote, paths); err != nil {
				return err
			}
		}

		extras := map[string]any{
			"dry_run":          true,
			"remote_mutations": false,
		}
		cmd := buildSyncDryRunCmd([]string{remote}, false)
		if err := logging.RecordDryRunLogPrefix("sync", remote, cmd, paths, jobs, buf.String(), extras, logPrefix); err != nil {
			return err
		}
	}
	return nil
}

// runAnnexDryRun performs a faithful dry-run preview for annex/export remotes.
//
// Design contract (fixes #19, #20, #26):
//   - Export remotes: simulate against annex-tracking-branch, not HEAD (#19).
//     When current branch ≠ tracking branch, report zero export actions + warning.
//     Export/unexport candidates computed via getExportUnexportCandidates.
//   - Annex (non-export) with preferred content: enumerate candidates then filter
//     through matchesPreferredContent() which handles ** globs correctly (#20).
//     git annex find --want-get-by cannot evaluate ** globs (git-annex bug).
//   - No "drop candidates" section is ever shown (#26). The real annex/export sync
//     never drops local content, so reporting drops is misleading.
func runAnnexDryRun(writer io.Writer, remote string, paths []string) error {
	fmt.Fprintf(writer, "Dry-run plan for remote '%s'\n", remote)

	// Check if remote has preferred content configured
	currentWanted, _ := runCommandOutput([]string{"git", "annex", "wanted", remote})
	currentWanted = strings.TrimSpace(currentWanted)
	hasPreferredContent := currentWanted != "" && currentWanted != "standard"

	if hasPreferredContent {
		if len(paths) > 0 {
			fmt.Fprintf(writer, "  limited to paths:\n")
			for _, p := range paths {
				fmt.Fprintf(writer, "    - %s\n", p)
			}
		}

		if isExportRemote(remote) {
			// --- Fix #19: Export dry-run simulates against the tracking branch ---
			//
			// The real sync runs: git annex export <tracking-branch> --to <remote>
			// So the dry-run must compute candidates against that same ref.
			// If current branch ≠ tracking branch, HEAD-only content will NOT be
			// exported — report nothing and emit a clear warning instead.
			fmt.Fprintf(writer, "  export (preferred content: %s)\n", currentWanted)

			trackingBranch, _ := runCommandOutput([]string{"git", "config", "--get",
				fmt.Sprintf("remote.%s.annex-tracking-branch", remote)})
			trackingBranch = strings.TrimSpace(trackingBranch)
			if trackingBranch == "" {
				trackingBranch = "HEAD"
			}

			// Resolve current branch (empty string on detached HEAD).
			currentBranch, _ := runCommandOutput([]string{"git", "rev-parse", "--abbrev-ref", "HEAD"})
			currentBranch = strings.TrimSpace(currentBranch)

			// Normalize: if trackingBranch is HEAD or current branch resolves to HEAD,
			// treat it as a branch-match only when not in detached HEAD state.
			branchesMatch := (trackingBranch == "HEAD") || (currentBranch != "" && currentBranch != "HEAD" && currentBranch == trackingBranch)

			if !branchesMatch {
				// #19: warn and report nothing — the real export will also export nothing
				// from the current branch's unique content.
				branchDesc := currentBranch
				if branchDesc == "" || branchDesc == "HEAD" {
					branchDesc = "(detached HEAD)"
				}
				writeBranchMismatchWarning(writer, branchDesc, remote, trackingBranch)
				return nil
			}

			// Branches match: compute export/unexport against the tracking branch ref.
			// getExportUnexportCandidates already uses matchesPreferredContent internally
			// and reads export.log to detect unexports — same as the real sync's logic.
			type exportResult struct {
				files []fileInfo
				err   error
			}
			unexportCh := make(chan exportResult, 1)
			go func() {
				f, e := getExportUnexportCandidates(remote, paths)
				unexportCh <- exportResult{f, e}
			}()

			// Enumerate files present here but not on the remote, then filter by
			// preferred content using the reliable Go evaluator (handles ** globs).
			hereFiles, hereErr := runAnnexFindWithSize(
				[]string{"git", "annex", "find", "--in", "here", "--not", "--in", remote},
				paths,
			)

			unexportRes := <-unexportCh

			// Check error before using hereFiles to avoid iterating partial/corrupt data.
			if hereErr != nil {
				return fmt.Errorf("dry-run query failed for here -> %s: %w", remote, hereErr)
			}

			// Filter here-files by preferred content (fixes ** glob evaluation).
			var toExport []fileInfo
			for _, f := range hereFiles {
				if matchesPreferredContent(currentWanted, f.Path) {
					toExport = append(toExport, f)
				}
			}

			// Print here -> remote (export)
			var h2rSize int64
			for _, f := range toExport {
				h2rSize += f.Size
			}
			fmt.Fprintf(writer, "  here -> %s (tracking branch: %s)", remote, trackingBranch)
			if h2rSize > 0 {
				fmt.Fprintf(writer, " (%s)", formatBytes(h2rSize))
			}
			fmt.Fprintln(writer)
			if len(toExport) == 0 {
				fmt.Fprintln(writer, "    (none)")
			} else {
				for _, f := range toExport {
					fmt.Fprintf(writer, "    %s\n", f.Path)
				}
			}

			// Print unexport
			var totalSize int64
			for _, f := range unexportRes.files {
				totalSize += f.Size
			}
			fmt.Fprintf(writer, "  unexport from %s", remote)
			if totalSize > 0 {
				fmt.Fprintf(writer, " (%s)", formatBytes(totalSize))
			}
			fmt.Fprintln(writer)
			if unexportRes.err != nil {
				fmt.Fprintf(writer, "    (could not compute: %v)\n", unexportRes.err)
			} else if len(unexportRes.files) == 0 {
				fmt.Fprintln(writer, "    (none)")
			} else {
				for _, f := range unexportRes.files {
					fmt.Fprintf(writer, "    %s\n", f.Path)
				}
			}
			// Real export sync never drops local content — no drop section shown (#26).
			return nil
		}

		// --- Fix #20: Non-export annex remote with preferred content ---
		//
		// The real sync runs: git annex sync --content --no-commit <remote>
		// which evaluates preferred content via git-annex internally (correctly).
		// We cannot use --want-get-by because git-annex find matchers do NOT handle
		// ** globs (e.g. include=gwasdb-studies/**/*.parquet evaluates as nothing).
		// Instead: enumerate candidate files and filter with matchesPreferredContent.
		//
		// Direction semantics:
		//   here -> remote: governed by the remote's preferred content (currentWanted)
		//   remote -> here: governed by the LOCAL preferred content (git annex wanted here)
		fmt.Fprintf(writer, "  preferred content: %s\n", currentWanted)

		// Fetch local (here) preferred content expression for remote->here filtering.
		localWanted, _ := runCommandOutput([]string{"git", "annex", "wanted", "here"})
		localWanted = strings.TrimSpace(localWanted)
		if localWanted == "standard" {
			localWanted = ""
		}

		// Run here->remote and remote->here enumeration in parallel.
		h2rCh := make(chan annexFindResult, 1)
		r2hCh := make(chan annexFindResult, 1)
		go func() {
			files, err := runAnnexFindWithSize(
				[]string{"git", "annex", "find", "--in", "here", "--not", "--in", remote},
				paths,
			)
			h2rCh <- annexFindResult{Files: files, Err: err}
		}()
		go func() {
			files, err := runAnnexFindWithSize(
				[]string{"git", "annex", "find", "--in", remote, "--not", "--in", "here"},
				paths,
			)
			r2hCh <- annexFindResult{Files: files, Err: err}
		}()
		h2rRes := <-h2rCh
		r2hRes := <-r2hCh

		if h2rRes.Err != nil {
			return fmt.Errorf("dry-run query failed for here -> %s: %w", remote, h2rRes.Err)
		}
		if r2hRes.Err != nil {
			return fmt.Errorf("dry-run query failed for %s -> here: %w", remote, r2hRes.Err)
		}

		// Filter here->remote by the remote's preferred content expression.
		var toCopy []fileInfo
		for _, f := range h2rRes.Files {
			if matchesPreferredContent(currentWanted, f.Path) {
				toCopy = append(toCopy, f)
			}
		}
		// Filter remote->here by the LOCAL preferred content expression.
		// (What the local side wants to pull is governed by 'git annex wanted here'.)
		var toFetch []fileInfo
		for _, f := range r2hRes.Files {
			if matchesPreferredContent(localWanted, f.Path) {
				toFetch = append(toFetch, f)
			}
		}

		// Print here -> remote
		var h2rSize int64
		for _, f := range toCopy {
			h2rSize += f.Size
		}
		fmt.Fprintf(writer, "  here -> %s", remote)
		if h2rSize > 0 {
			fmt.Fprintf(writer, " (%s)", formatBytes(h2rSize))
		}
		fmt.Fprintln(writer)
		if len(toCopy) == 0 {
			fmt.Fprintln(writer, "    (none)")
		} else {
			for _, f := range toCopy {
				fmt.Fprintf(writer, "    %s\n", f.Path)
			}
		}

		// Print remote -> here
		var r2hSize int64
		for _, f := range toFetch {
			r2hSize += f.Size
		}
		fmt.Fprintf(writer, "  %s -> here", remote)
		if r2hSize > 0 {
			fmt.Fprintf(writer, " (%s)", formatBytes(r2hSize))
		}
		fmt.Fprintln(writer)
		if len(toFetch) == 0 {
			fmt.Fprintln(writer, "    (none)")
		} else {
			for _, f := range toFetch {
				fmt.Fprintf(writer, "    %s\n", f.Path)
			}
		}
		// Real annex sync never drops local content — no drop section shown (#26).
		return nil
	}

	// Standard dry-run for remotes without preferred content.
	// Fan out here->remote and remote->here in parallel; no drop section (#26).
	queries := commandutil.BuildDryRunQueries(remote, false)
	argSets := make([][]string, len(queries))
	for i, q := range queries {
		argSets[i] = q.Args
	}
	results := runAnnexFindParallel(paths, argSets...)
	for i, res := range results {
		if res.Err != nil {
			return fmt.Errorf("dry-run query failed for %s: %w", queries[i].Label, res.Err)
		}
		var totalSize int64
		for _, f := range res.Files {
			totalSize += f.Size
		}
		fmt.Fprintf(writer, "  %s", queries[i].Label)
		if totalSize > 0 {
			fmt.Fprintf(writer, " (%s)", formatBytes(totalSize))
		}
		fmt.Fprintln(writer)
		if len(res.Files) == 0 {
			fmt.Fprintln(writer, "    (none)")
			continue
		}
		for _, file := range res.Files {
			fmt.Fprintf(writer, "    %s\n", file.Path)
		}
	}

	return nil
}

// writeBranchMismatchWarning writes the #19 branch-mismatch warning and the
// zero-action export/unexport sections to writer. Extracted so that tests can
// call it directly and assert against real production output.
func writeBranchMismatchWarning(w io.Writer, branchDesc, remote, trackingBranch string) {
	fmt.Fprintf(w, "  ⚠️  WARNING: current branch '%s' differs from remote '%s' tracking branch '%s'\n",
		branchDesc, remote, trackingBranch)
	fmt.Fprintf(w, "     Files on the current branch will NOT be exported until they are merged into '%s'.\n", trackingBranch)
	fmt.Fprintf(w, "  here -> %s (tracking branch: %s)\n", remote, trackingBranch)
	fmt.Fprintln(w, "    (none — current branch is not the tracking branch)")
	fmt.Fprintf(w, "  unexport from %s\n", remote)
	fmt.Fprintln(w, "    (none — current branch is not the tracking branch)")
}

// annexFindResult holds the result of a single parallel git annex find query.
type annexFindResult struct {
	Files []fileInfo
	Err   error
}

// getExportUnexportCandidates returns the list of files that would be unexported
// from an export remote on the next sync. It merges two sources:
//
//  1. Files deleted from HEAD since the last export: these no longer exist in the
//     working tree so git annex find cannot see them. We detect them by reading
//     export.log from the git-annex branch (which stores the tree SHA that was last
//     exported to the remote) and diffing it against the current HEAD.
//
//  2. Files still in HEAD that the remote now wants to drop (preferred content
//     changed): detected via git annex find --want-drop-by.
//
// The two sets are merged and deduplicated. Sizes for (1) are zero because the
// files no longer exist locally.
// getExportUnexportCandidates returns files that git annex export would unexport
// from the remote on the next sync. These are files currently on the remote
// (recorded in export.log on the git-annex branch) that would NOT be in the
// next export.
//
// git annex export unexports a file when it is either:
//
//	(a) deleted from HEAD (no longer in the git tree at all), or
//	(b) still in HEAD but no longer matches the remote's preferred content.
//
// We cannot use git annex find --want-drop-by for this because:
//  1. For S3/s5cmd export remotes the per-key location log is not populated,
//     so --in <remote> returns nothing.
//  2. git-annex does not handle ** globs in find matchers (--include, --want-*),
//     so expressions like include=gwasdb-studies/**/*.parquet evaluate as
//     matching nothing, giving wrong results.
//
// Instead we read the last-exported tree from export.log and compare it against
// the HEAD tree filtered by our own preferred content evaluator.
func getExportUnexportCandidates(remote string, paths []string) ([]fileInfo, error) {
	// Get remote UUID
	remoteUUID, err := runCommandOutput([]string{"git", "config", "--get",
		fmt.Sprintf("remote.%s.annex-uuid", remote)})
	if err != nil {
		return nil, nil // no UUID → no export history
	}
	remoteUUID = strings.TrimSpace(remoteUUID)

	// Read export.log from the git-annex branch.
	// Format: "<timestamp> <local-uuid>:<remote-uuid> <tree-sha>"
	exportLog, err := runCommandOutput([]string{"git", "show", "git-annex:export.log"})
	if err != nil {
		return nil, nil // no export.log → never exported
	}

	// Parse: find the line for our remote and extract the tree SHA.
	var lastExportedTree string
	for _, line := range strings.Split(exportLog, "\n") {
		if strings.Contains(line, remoteUUID) {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				lastExportedTree = fields[len(fields)-1]
			}
			break
		}
	}
	if lastExportedTree == "" {
		return nil, nil // remote not found in export.log
	}

	// List files currently on the remote (last exported tree).
	exportedOut, err := runCommandOutput([]string{"git", "ls-tree", "-r", "--name-only", lastExportedTree})
	if err != nil {
		return nil, nil // tree may not exist locally; skip gracefully
	}
	var exportedFiles []string
	for _, f := range strings.Split(strings.TrimSpace(exportedOut), "\n") {
		if f != "" {
			exportedFiles = append(exportedFiles, f)
		}
	}
	if len(exportedFiles) == 0 {
		return nil, nil
	}

	// Get preferred content expression for this remote.
	preferredContent, _ := runCommandOutput([]string{"git", "annex", "wanted", remote})
	preferredContent = strings.TrimSpace(preferredContent)

	// Build the set of HEAD files that WILL be in the next export
	// = HEAD files that match the preferred content expression.
	// We evaluate preferred content ourselves because git-annex find matchers
	// do not handle ** globs correctly.
	headOut, err := runCommandOutput([]string{"git", "ls-tree", "-r", "--name-only", "HEAD"})
	if err != nil {
		return nil, fmt.Errorf("could not list HEAD files: %w", err)
	}
	nextExportSet := make(map[string]bool)
	for _, f := range strings.Split(strings.TrimSpace(headOut), "\n") {
		if f == "" {
			continue
		}
		// If no preferred content (or "standard" / unparseable), assume all HEAD files
		// will be exported. matchesPreferredContent returns true for empty expression.
		if matchesPreferredContent(preferredContent, f) {
			nextExportSet[f] = true
		}
	}

	// Files currently on remote that won't be in the next export = will be unexported.
	var unexported []fileInfo
	for _, f := range exportedFiles {
		if !nextExportSet[f] {
			unexported = append(unexported, fileInfo{Path: f})
		}
	}
	return unexported, nil
}

// matchesPreferredContent evaluates a git-annex preferred content expression
// against a file path. It supports include=<glob> / exclude=<glob> patterns
// with ** glob semantics (** matches zero or more path components).
// For complex expressions it cannot fully parse, it returns true (conservative:
// assume the file is wanted, so it won't be incorrectly listed as an unexport candidate).
func matchesPreferredContent(expr, filePath string) bool {
	if expr == "" || expr == "standard" || expr == "anything" {
		return true
	}
	if expr == "nothing" {
		return false
	}

	// Extract all include= and exclude= clauses.
	// This handles the common case for export remotes.
	// Example: "include=gwasdb-studies/**/*.parquet"
	type clause struct {
		kind    string // "include" or "exclude"
		pattern string
	}
	var clauses []clause
	rest := expr
	for {
		idxIncl := strings.Index(rest, "include=")
		idxExcl := strings.Index(rest, "exclude=")
		if idxIncl < 0 && idxExcl < 0 {
			break
		}
		var kind string
		var idx int
		if idxIncl >= 0 && (idxExcl < 0 || idxIncl < idxExcl) {
			kind, idx = "include", idxIncl
		} else {
			kind, idx = "exclude", idxExcl
		}
		rest = rest[idx+len(kind)+1:]
		// Pattern ends at whitespace or at a ')' that closes the surrounding group
		// (i.e. a ')' at paren-depth 0 within the pattern). This handles expressions
		// like "(include=a/** or include=b/**)" without truncating patterns that
		// themselves contain parentheses (e.g. "include=path/with(parens)/**").
		var pattern string
		depth := 0
		patEnd := -1
		for i := 0; i < len(rest); i++ {
			switch rest[i] {
			case '(':
				depth++
			case ')':
				if depth == 0 {
					patEnd = i
				} else {
					depth--
				}
			case ' ', '\t', '\n':
				patEnd = i
			}
			if patEnd >= 0 {
				break
			}
		}
		if patEnd < 0 {
			pattern = rest
			rest = ""
		} else {
			pattern = rest[:patEnd]
			rest = rest[patEnd:]
		}
		if pattern != "" {
			clauses = append(clauses, clause{kind, pattern})
		}
	}

	if len(clauses) == 0 {
		// Expression exists but we couldn't parse include/exclude clauses.
		// Return true conservatively (don't falsely flag files as unexported).
		return true
	}

	// Apply excludes first.
	var hasIncludes bool
	for _, c := range clauses {
		if c.kind == "exclude" && matchAnnexGlob(c.pattern, filePath) {
			return false
		}
		if c.kind == "include" {
			hasIncludes = true
		}
	}
	if !hasIncludes {
		// Only excludes, no includes: file matches if it wasn't excluded above.
		return true
	}
	for _, c := range clauses {
		if c.kind == "include" && matchAnnexGlob(c.pattern, filePath) {
			return true
		}
	}
	// Had include clauses but none matched.
	return false
}

// matchAnnexGlob matches a git-annex glob pattern against a file path.
// It supports ** which matches zero or more path components (including none),
// * which matches within a single path component, and ? for a single character.
func matchAnnexGlob(pattern, filePath string) bool {
	if !strings.Contains(pattern, "**") {
		// No **: use filepath.Match against full path or basename.
		if m, _ := filepath.Match(pattern, filePath); m {
			return true
		}
		if m, _ := filepath.Match(pattern, filepath.Base(filePath)); m {
			return true
		}
		return false
	}
	// Convert git-annex glob to regex.
	// **/ matches zero or more path components (the slash is optional after last **).
	var regexBuf strings.Builder
	i := 0
	for i < len(pattern) {
		if pattern[i] == '*' && i+1 < len(pattern) && pattern[i+1] == '*' {
			// **
			regexBuf.WriteString(".*") // matches any chars including /
			i += 2
			if i < len(pattern) && pattern[i] == '/' {
				// **/  → the .* already allows empty match, so the / is optional
				regexBuf.WriteString("/?") // optional slash (** matches zero components)
				i++
			}
		} else if pattern[i] == '*' {
			regexBuf.WriteString("[^/]*") // * matches within one path segment
			i++
		} else if pattern[i] == '?' {
			regexBuf.WriteString("[^/]") // ? matches one char within segment
			i++
		} else {
			regexBuf.WriteString(regexp.QuoteMeta(string(pattern[i])))
			i++
		}
	}
	m, err := regexp.MatchString("^"+regexBuf.String()+"$", filePath)
	return err == nil && m
}

// writeJSONFilesSection writes a single key:{files:[...],total:{...}} section to writer.
func writeJSONFilesSection(writer io.Writer, key string, files []fileInfo, err error) {
	fmt.Fprintf(writer, ",\"%s\":{\"files\":[", key)
	if err == nil {
		var total int64
		for j, file := range files {
			total += file.Size
			if j > 0 {
				fmt.Fprint(writer, ",")
			}
			if file.Size > 0 {
				fmt.Fprintf(writer, "\n    {\"file\":%s,\"bytes\":%d}", jsonString(file.Path), file.Size)
			} else {
				fmt.Fprintf(writer, "\n    {\"file\":%s}", jsonString(file.Path))
			}
		}
		if len(files) > 0 {
			fmt.Fprint(writer, "\n  ")
		}
		fmt.Fprintf(writer, "],\"total\":{\"count\":%d,\"bytes\":%d,\"size\":%s}}",
			len(files), total, jsonString(formatBytes(total)))
	} else {
		fmt.Fprint(writer, "],\"total\":{\"count\":0,\"bytes\":0,\"size\":\"0 B\"}}")
	}
}

// runAnnexFindParallel runs multiple git annex find queries concurrently.
// git annex find is read-only (no flock/write syscalls), so concurrent execution
// is safe. Results are returned in the same order as the input args slices.
func runAnnexFindParallel(paths []string, argSets ...[]string) []annexFindResult {
	results := make([]annexFindResult, len(argSets))
	var wg gosync.WaitGroup
	for i, args := range argSets {
		wg.Add(1)
		go func(idx int, baseArgs []string) {
			defer wg.Done()
			files, err := runAnnexFindWithSize(baseArgs, paths)
			results[idx] = annexFindResult{Files: files, Err: err}
		}(i, args)
	}
	wg.Wait()
	return results
}

// runAnnexFindWithSize runs git annex find and parses file paths with sizes
func runAnnexFindWithSize(baseArgs []string, paths []string) ([]fileInfo, error) {
	args := append([]string{}, baseArgs...)
	args = append(args, "--json")
	args = append(args, paths...)

	out, err := runCommandOutput(args)
	if err != nil {
		return nil, err
	}

	return parseAnnexFindJSONWithSize(out), nil
}

// parseAnnexFindJSONWithSize parses git annex find JSON output with size info
func parseAnnexFindJSONWithSize(output string) []fileInfo {
	type annexJSON struct {
		File string `json:"file"`
		Size int64  `json:"size,omitempty"`
	}

	var files []fileInfo
	seen := make(map[string]bool)

	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var item annexJSON
		if json.Unmarshal([]byte(line), &item) != nil {
			continue
		}
		if item.File != "" && !seen[item.File] {
			files = append(files, fileInfo{
				Path: item.File,
				Size: item.Size,
			})
			seen[item.File] = true
		}
	}
	return files
}

// formatBytes formats byte count as human-readable string
func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

// runImportDryRun performs a dry-run for import remotes by querying S3 directly
// using the AWS SDK to list objects, then comparing with what's already tracked locally.
// This avoids downloading any content.
func runImportDryRun(writer io.Writer, remote string, jobs string) error {
	fmt.Fprintf(writer, "Dry-run plan for import remote '%s' (experimental: using AWS SDK)\n", remote)

	// Get files that would be imported
	newImports, err := getImportDryRunFilesWithSize(remote, jobs)
	if err != nil {
		return err
	}

	var totalSize int64
	for _, f := range newImports {
		totalSize += f.Size
	}

	// Display in same format as annex/export remotes
	// Import remotes are one-way: remote -> here only
	fmt.Fprintf(writer, "  here -> %s\n", remote)
	fmt.Fprintln(writer, "    (none - import remotes are read-only)")

	fmt.Fprintf(writer, "  %s -> here", remote)
	if totalSize > 0 {
		fmt.Fprintf(writer, " (%s)", formatBytes(totalSize))
	}
	fmt.Fprintln(writer)

	if len(newImports) == 0 {
		fmt.Fprintln(writer, "    (none)")
	} else {
		for _, file := range newImports {
			fmt.Fprintf(writer, "    %s\n", file.Path)
		}
	}

	// Import remotes are read-only; drops are not applicable.
	fmt.Fprintf(writer, "  drop candidates (here missing in %s)\n", remote)
	fmt.Fprintln(writer, "    (none - import remotes don't support drops)")

	return nil
}

// listS3Objects lists all objects in an S3 bucket with the given prefix, including sizes
func listS3Objects(ctx context.Context, client *s3.Client, bucket, prefix string) ([]fileInfo, error) {
	var objects []fileInfo
	paginator := s3.NewListObjectsV2Paginator(client, &s3.ListObjectsV2Input{
		Bucket: aws.String(bucket),
		Prefix: aws.String(prefix),
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}

		for _, obj := range page.Contents {
			if obj.Key != nil {
				// Keep the full S3 key (don't strip prefix)
				key := *obj.Key
				if key != "" && !strings.HasSuffix(key, "/") {
					size := int64(0)
					if obj.Size != nil {
						size = *obj.Size
					}
					objects = append(objects, fileInfo{
						Path: key,
						Size: size,
					})
				}
			}
		}
	}

	return objects, nil
}

// filterObjects filters S3 objects based on include/exclude patterns
func filterObjects(objects []fileInfo, includePatterns, excludePatterns []string) []fileInfo {
	var filtered []fileInfo

	for _, obj := range objects {
		excluded := false

		// Check exclude patterns first
		for _, pattern := range excludePatterns {
			if matchGlob(pattern, obj.Path) {
				excluded = true
				break
			}
		}

		if excluded {
			continue
		}

		// Check include patterns (if any specified, file must match at least one)
		if len(includePatterns) > 0 {
			included := false
			for _, pattern := range includePatterns {
				if matchGlob(pattern, obj.Path) {
					included = true
					break
				}
			}
			if included {
				filtered = append(filtered, obj)
			}
		} else {
			filtered = append(filtered, obj)
		}
	}

	return filtered
}

// matchGlob matches a glob pattern against a file path.
// Supports **/ prefix for matching at any directory depth.
func matchGlob(pattern, filePath string) bool {
	if strings.HasPrefix(pattern, "**/") {
		// Match the rest of the pattern against the basename
		rest := strings.TrimPrefix(pattern, "**/")
		if matched, _ := filepath.Match(rest, filepath.Base(filePath)); matched {
			return true
		}
	}
	// Standard pattern matching against basename
	if matched, _ := filepath.Match(pattern, filepath.Base(filePath)); matched {
		return true
	}
	return false
}

// getTrackedFiles returns a map of all files currently tracked in git (HEAD)
func getTrackedFiles() (map[string]bool, error) {
	tracked := make(map[string]bool)

	output, err := runCommandOutput([]string{"git", "ls-files"})
	if err != nil {
		return tracked, err
	}

	files := strings.Split(strings.TrimSpace(output), "\n")
	for _, file := range files {
		if file != "" {
			tracked[file] = true
		}
	}

	return tracked, nil
}

// getRemoteConfig reads remote configuration from .exohub/remotes
// remoteConfig holds parsed remote configuration from .exohub/remotes.
// Scalar fields are stored in Strings, list fields in StringLists.
type remoteConfig struct {
	Strings     map[string]string
	StringLists map[string][]string
}

func getRemoteConfig(remoteName string) (*remoteConfig, error) {
	remotesFile := ".exohub/remotes"
	data, err := os.ReadFile(remotesFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read .exohub/remotes: %w", err)
	}

	var remotesYAML struct {
		Remotes []map[string]interface{} `yaml:"remotes"`
	}

	if err := yaml.Unmarshal(data, &remotesYAML); err != nil {
		return nil, fmt.Errorf("failed to parse .exohub/remotes: %w", err)
	}

	for _, remote := range remotesYAML.Remotes {
		if name, ok := remote["name"].(string); ok && name == remoteName {
			cfg := &remoteConfig{
				Strings:     make(map[string]string),
				StringLists: make(map[string][]string),
			}
			for k, v := range remote {
				switch val := v.(type) {
				case string:
					cfg.Strings[k] = val
				case []interface{}:
					var list []string
					for _, item := range val {
						if s, ok := item.(string); ok {
							list = append(list, s)
						}
					}
					cfg.StringLists[k] = list
				}
			}
			return cfg, nil
		}
	}

	return nil, fmt.Errorf("remote '%s' not found in .exohub/remotes", remoteName)
}

// streamDryRunJSONWithImport generates JSON output for dry-run with import remotes
func streamDryRunJSONWithImport(writer io.Writer, remotes []string, paths []string, schema string, jobs string) error {
	fmt.Fprintf(writer, "{\"$schema\":%s,\"remotes\":[", jsonString(schema))

	for i, remote := range remotes {
		if i > 0 {
			fmt.Fprint(writer, ",")
		}
		fmt.Fprintf(writer, "{\"name\":%s", jsonString(remote))

		if isImportRemote(remote) {
			// For import remotes, query S3 directly
			files, err := getImportDryRunFilesWithSize(remote, jobs)
			if err != nil {
				return err
			}

			var totalSize int64
			for _, f := range files {
				totalSize += f.Size
			}

			// Import remotes: files go in remote_to_here (files available on S3 but not local)
			fmt.Fprint(writer, ",\"here_to_remote\":{\"files\":[],\"total\":{\"count\":0,\"bytes\":0,\"size\":\"0 B\"}}")

			fmt.Fprint(writer, ",\"remote_to_here\":{\"files\":[")
			for j, file := range files {
				if j > 0 {
					fmt.Fprint(writer, ",")
				}
				fmt.Fprintf(writer, "\n    {\"file\":%s,\"bytes\":%d}", jsonString(file.Path), file.Size)
			}
			if len(files) > 0 {
				fmt.Fprint(writer, "\n  ")
			}
			fmt.Fprintf(writer, "],\"total\":{\"count\":%d,\"bytes\":%d,\"size\":%s}}",
				len(files), totalSize, jsonString(formatBytes(totalSize)))

			// Import remotes don't support drops
			fmt.Fprint(writer, ",\"drops\":{\"files\":[],\"total\":{\"count\":0,\"bytes\":0,\"size\":\"0 B\"}}")
		} else {
			// Standard annex/export remotes: build queries then fan out in parallel.
			type jsonQuery struct {
				key  string
				args []string
			}
			var jsonQueries []jsonQuery

			if isExportRemote(remote) {
				// For export remotes, run here->remote and unexport computation concurrently.
				currentWanted := commandutil.GetPreferredContent(remote)

				type unexportRes struct {
					files []fileInfo
					err   error
				}
				unexportCh := make(chan unexportRes, 1)
				if currentWanted != "" {
					go func() {
						f, e := getExportUnexportCandidates(remote, paths)
						unexportCh <- unexportRes{f, e}
					}()
				} else {
					unexportCh <- unexportRes{} // no preferred content, no unexport
				}

				// here_to_remote
				var hereToRemoteArgs []string
				if currentWanted != "" {
					hereToRemoteArgs = []string{"git", "annex", "find", "--in", "here", "--not", "--in", remote, "--want-get-by", remote}
				} else {
					hereToRemoteArgs = []string{"git", "annex", "find", "--not", "--in", remote, "--in", "here"}
				}
				h2r := runAnnexFindParallel(paths, hereToRemoteArgs)[0]
				unexport := <-unexportCh

				// Emit here_to_remote
				writeJSONFilesSection(writer, "here_to_remote", h2r.Files, h2r.Err)

				// Emit unexport (or empty remote_to_here for schema compat if no preferred content)
				if currentWanted != "" {
					writeJSONFilesSection(writer, "unexport", unexport.files, unexport.err)
				} else {
					fmt.Fprint(writer, ",\"remote_to_here\":{\"files\":[],\"total\":{\"count\":0,\"bytes\":0,\"size\":\"0 B\"}}")
				}

				drops := runAnnexFindParallel(paths,
					[]string{"git", "annex", "find", "--in", "here", "--not", "--in", remote, "--want-drop"},
				)[0]
				writeJSONFilesSection(writer, "drops", drops.Files, drops.Err)
			} else {
				qs := commandutil.BuildDryRunQueries(remote, true)
				for _, q := range qs {
					jsonQueries = append(jsonQueries, jsonQuery{q.Kind, q.Args})
				}

				// Fan out all queries in parallel
				argSets := make([][]string, len(jsonQueries))
				for i, q := range jsonQueries {
					argSets[i] = q.args
				}
				results := runAnnexFindParallel(paths, argSets...)
				for i, res := range results {
					writeJSONFilesSection(writer, jsonQueries[i].key, res.Files, res.Err)
				}
			}
		}

		fmt.Fprint(writer, "}")
	}

	fmt.Fprint(writer, "]")

	if len(paths) > 0 {
		fmt.Fprintf(writer, ",\"paths\":%s", jsonString(paths))
	}

	fmt.Fprintln(writer, "}")
	return nil
}

// getImportDryRunFilesWithSize queries S3 and returns the list of files that would be imported with sizes
func getImportDryRunFilesWithSize(remote string, jobs string) ([]fileInfo, error) {
	// Ensure AWS credentials are available
	if err := ensureAWSCredentials(); err != nil {
		return nil, fmt.Errorf("AWS credentials not available for import remote '%s': %w", remote, err)
	}

	// Get remote configuration from .exohub/remotes
	cfg, err := getRemoteConfig(remote)
	if err != nil {
		return nil, fmt.Errorf("failed to get remote config: %w", err)
	}

	bucket := cfg.Strings["bucket"]
	prefix := cfg.Strings["prefix"]
	region := cfg.Strings["datacenter"]
	importDir := cfg.Strings["import_dir"]
	includePatterns := cfg.StringLists["include"]
	excludePatterns := cfg.StringLists["exclude"]

	if bucket == "" {
		return nil, fmt.Errorf("remote '%s' has no bucket configured", remote)
	}
	if region == "" {
		region = "us-east-1"
	}

	// Load AWS config
	ctx := context.Background()
	awsCfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	// Create S3 client
	client := s3.NewFromConfig(awsCfg)

	// List objects from S3
	s3Objects, err := listS3Objects(ctx, client, bucket, prefix)
	if err != nil {
		return nil, fmt.Errorf("failed to list S3 objects: %w", err)
	}

	// Filter objects based on include/exclude patterns
	filteredObjects := filterObjects(s3Objects, includePatterns, excludePatterns)

	// Get currently tracked files from git
	trackedFiles, err := getTrackedFiles()
	if err != nil {
		trackedFiles = make(map[string]bool)
	}

	// Compare S3 objects with tracked files to find new imports.
	// S3 keys include the prefix (e.g. "SRA29909/CANL.../file.gz") but
	// git-annex import strips the prefix, so local paths are relative to
	// the prefix (e.g. "file.gz" or "subdir/file.gz").
	var newImports []fileInfo
	for _, obj := range filteredObjects {
		// Strip the S3 prefix to get the path as git-annex would import it
		relativePath := obj.Path
		if prefix != "" {
			relativePath = strings.TrimPrefix(obj.Path, prefix)
		}

		localPath := relativePath
		if importDir != "" {
			localPath = filepath.Join(importDir, relativePath)
		}

		if !trackedFiles[localPath] {
			newImports = append(newImports, fileInfo{
				Path: localPath,
				Size: obj.Size,
			})
		}
	}

	return newImports, nil
}

// jsonString marshals a value to JSON string
func jsonString(v interface{}) string {
	data, _ := json.Marshal(v)
	return string(data)
}

func buildSyncDryRunCmd(remotes []string, outputJSON bool) []string {
	args := []string{"exo", "sync", "--dry-run"}
	if outputJSON {
		args = append(args, "--json")
	}
	for _, remote := range remotes {
		args = append(args, "--with", remote)
	}
	return args
}

func runManifestValidation(path string) error {
	return manifestutil.ValidateManifestFile("sync", path)
}

func parseManifest(path string) (manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return manifest{}, fmt.Errorf("Manifest not found: %s", path)
	}
	var m manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return manifest{}, err
	}
	return m, nil
}

func requireBins(names ...string) error {
	missing := false
	for _, name := range names {
		if _, err := exec.LookPath(name); err != nil {
			fmt.Fprintf(os.Stderr, "Required binary '%s' not found in PATH\n", name)
			missing = true
		}
	}
	if missing {
		return errors.New("missing binaries")
	}
	return nil
}

func annexInitialized() bool {
	cmd := commandutil.Command("git", "config", "--get", "annex.uuid")
	return cmd.Run() == nil
}

func listAnnexRemotes() ([]string, error) {
	out, err := runCommandCombinedOutput([]string{"git", "annex", "info", "--fast", "--json"})
	remotes := parseAnnexInfoRemotesJSON(out)
	if len(remotes) > 0 {
		return remotes, nil
	}
	out, cfgErr := runCommandCombinedOutput([]string{"git", "config", "--get-regexp", "^remote\\..*\\.annex-uuid$"})
	if cfgErr == nil {
		remotes = parseAnnexRemoteNamesFromConfig(out)
		if len(remotes) > 0 {
			return remotes, nil
		}
	}
	if cfgErr != nil {
		return nil, cfgErr
	}
	if err != nil {
		return nil, err
	}
	return nil, nil
}

func parseAnnexInfoRemotesJSON(output string) []string {
	type repo struct {
		Description string `json:"description"`
		Here        bool   `json:"here"`
	}
	type payload struct {
		SemiTrusted []repo `json:"semitrusted repositories"`
		Trusted     []repo `json:"trusted repositories"`
		Untrusted   []repo `json:"untrusted repositories"`
	}
	var info payload
	if json.Unmarshal([]byte(output), &info) != nil {
		return nil
	}
	var remotes []string
	collect := func(repos []repo) {
		for _, r := range repos {
			if r.Here {
				continue
			}
			name := extractAnnexRemoteName(r.Description)
			if name != "" {
				remotes = append(remotes, name)
			}
		}
	}
	collect(info.SemiTrusted)
	collect(info.Trusted)
	collect(info.Untrusted)
	return uniqueStrings(remotes)
}

func extractAnnexRemoteName(desc string) string {
	desc = strings.TrimSpace(desc)
	if desc == "" {
		return ""
	}
	if strings.HasPrefix(desc, "[") && strings.HasSuffix(desc, "]") {
		name := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(desc, "["), "]"))
		if name == "" || name == "here" {
			return ""
		}
		return name
	}
	return ""
}

func parseAnnexListRemotesText(output string) []string {
	var remotes []string
	reParen := regexp.MustCompile(`\(([^)]+)\)\s*$`)
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if isAnnexListRemotesNoise(line) {
			continue
		}
		name := ""
		if match := reParen.FindStringSubmatch(line); len(match) == 2 {
			name = match[1]
		} else {
			fields := strings.Fields(line)
			if len(fields) > 0 {
				name = fields[len(fields)-1]
			}
		}
		name = strings.TrimSpace(name)
		if name == "" || name == "here" {
			continue
		}
		remotes = append(remotes, name)
	}
	return uniqueStrings(remotes)
}

func parseAnnexRemoteNamesFromConfig(output string) []string {
	var remotes []string
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		key := fields[0]
		if !strings.HasPrefix(key, "remote.") || !strings.HasSuffix(key, ".annex-uuid") {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(key, "remote."), ".annex-uuid")
		name = strings.TrimSpace(name)
		if name == "" || name == "here" {
			continue
		}
		remotes = append(remotes, name)
	}
	return uniqueStrings(remotes)
}

func isAnnexListRemotesNoise(line string) bool {
	if strings.HasPrefix(line, "DEBUG:") {
		return true
	}
	if strings.HasPrefix(line, "git-annex:") {
		return true
	}
	if strings.HasPrefix(line, "warning:") {
		return true
	}
	if strings.Contains(line, "exit status") {
		return true
	}
	return false
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

func parseAnnexSyncLog(logfile string, printOnly bool) error {
	data, err := os.ReadFile(logfile)
	if err != nil {
		return err
	}
	reErr := regexp.MustCompile(`(?i)(checksum|hash|verification)`)
	reBad := regexp.MustCompile(`(?i)(mismatch|fail|failed|bad|corrupt|corrupted)`)
	reKey := regexp.MustCompile(`[A-Z0-9]+E?-s[0-9]+--[A-Za-z0-9]{16,}`)

	var candidates []string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Text()
		if !reErr.MatchString(line) || !reBad.MatchString(line) {
			continue
		}
		candidates = append(candidates, extractQuotedPaths(line)...)
		for _, key := range uniqueStrings(reKey.FindAllString(line, -1)) {
			candidates = append(candidates, findFilesForKey(key)...)
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	candidates = uniqueStrings(candidates)
	if printOnly {
		for _, p := range candidates {
			fmt.Println(p)
		}
		return nil
	}
	for _, p := range candidates {
		if err := appendBadPath(p); err != nil {
			return err
		}
	}
	return nil
}

func extractQuotedPaths(line string) []string {
	var out []string
	reSingle := regexp.MustCompile(`'([^']+)'`)
	reDouble := regexp.MustCompile(`"([^"]+)"`)
	for _, m := range reSingle.FindAllStringSubmatch(line, -1) {
		if len(m) == 2 && fileExists(m[1]) {
			out = append(out, m[1])
		}
	}
	for _, m := range reDouble.FindAllStringSubmatch(line, -1) {
		if len(m) == 2 && fileExists(m[1]) {
			out = append(out, m[1])
		}
	}
	return out
}

func findFilesForKey(key string) []string {
	if _, err := exec.LookPath("jq"); err == nil {
		out, err := runCommandOutput([]string{"git", "annex", "find", "--json", "--key", key})
		if err == nil {
			return parseJSONFiles(out)
		}
	}
	out, err := runCommandOutput([]string{"git", "annex", "find", "--key", key})
	if err != nil {
		return nil
	}
	var files []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			files = append(files, line)
		}
	}
	return files
}

func parseJSONFiles(output string) []string {
	var files []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var payload struct {
			File string `json:"file"`
		}
		if json.Unmarshal([]byte(line), &payload) == nil && payload.File != "" {
			files = append(files, payload.File)
		}
	}
	return files
}

func appendBadPath(path string) error {
	logging.EnsureLogsDir()
	if filepath.IsAbs(path) {
		if rel, err := filepath.Rel(".", path); err == nil {
			path = rel
		}
	}
	lock, err := os.OpenFile(filepath.Join(".git", "exohub", "bad.lock"), os.O_CREATE|os.O_RDWR, 0o600)
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
	if data, err := os.ReadFile(filepath.Join(".git", "exohub", "bad")); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				existing[line] = struct{}{}
			}
		}
	}
	if _, ok := existing[path]; ok {
		return nil
	}
	f, err := os.OpenFile(filepath.Join(".git", "exohub", "bad"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintln(f, path)
	return err
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

func quoteArgs(args []string) []string {
	return logging.QuoteArgs(args)
}

func shellQuote(val string) string {
	return logging.ShellQuote(val)
}

// parallelMoveFiles moves files from their current paths to destDir using
// parallel filesystem renames, respecting the EXOHUB_JOBS concurrency limit.
// Returns the list of successfully moved destination paths and their original paths.
func parallelMoveFiles(files []string, destDir string, jobs string) (movedFiles, oldPaths []string) {
	numJobs, err := strconv.Atoi(jobs)
	if err != nil || numJobs < 1 {
		numJobs = 1
	}

	fmt.Printf("Moving %d files to %s/ (jobs: %d)\n", len(files), destDir, numJobs)

	// Create destination directory
	if err := os.MkdirAll(destDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to create import directory: %v\n", err)
		return nil, nil
	}

	type moveResult struct {
		oldPath string
		newPath string
		err     error
	}

	results := make([]moveResult, len(files))
	sem := make(chan struct{}, numJobs)
	var wg gosync.WaitGroup

	for i, file := range files {
		wg.Add(1)
		go func(idx int, file string) {
			defer wg.Done()
			sem <- struct{}{}        // acquire
			defer func() { <-sem }() // release

			destPath := filepath.Join(destDir, file)
			destPathDir := filepath.Dir(destPath)

			// Skip if file already exists at destination (from previous sync)
			if _, err := os.Lstat(destPath); err == nil {
				os.RemoveAll(file)
				cleanEmptyParents(file)
				results[idx] = moveResult{err: fmt.Errorf("skipped: already exists")}
				return
			}

			// Create subdirectories in destination
			if err := os.MkdirAll(destPathDir, 0755); err != nil {
				results[idx] = moveResult{err: err}
				return
			}

			// Filesystem rename (no git lock needed)
			if err := os.Rename(file, destPath); err != nil {
				results[idx] = moveResult{err: err}
				return
			}

			cleanEmptyParents(file)
			results[idx] = moveResult{oldPath: file, newPath: destPath}
		}(i, file)
	}

	wg.Wait()

	for _, r := range results {
		if r.err != nil {
			if r.err.Error() != "skipped: already exists" {
				fmt.Fprintf(os.Stderr, "Warning: %v\n", r.err)
			}
			continue
		}
		if r.newPath != "" {
			movedFiles = append(movedFiles, r.newPath)
			oldPaths = append(oldPaths, r.oldPath)
		}
	}

	return movedFiles, oldPaths
}

// cleanEmptyParents removes empty parent directories up to the repo root.
func cleanEmptyParents(file string) {
	parentDir := filepath.Dir(file)
	for parentDir != "." && parentDir != "/" {
		if err := os.Remove(parentDir); err != nil {
			break
		}
		parentDir = filepath.Dir(parentDir)
	}
}

func runCommand(args []string) error {
	cmd := commandutil.Command(args[0], args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func runCommandOutput(args []string) (string, error) {
	cmd := commandutil.Command(args[0], args[1:]...)
	out, err := cmd.Output()
	return string(out), err
}

func runCommandCombinedOutput(args []string) (string, error) {
	cmd := commandutil.Command(args[0], args[1:]...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func exitWithError(err error) {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		os.Exit(exitErr.ExitCode())
	}
	os.Exit(1)
}

func fileExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// isImportRemote checks if a remote has importtree=yes in git-annex remote.log
func isImportRemote(name string) bool {
	// Get remote UUID
	cmd := exec.Command("git", "config", "--get", fmt.Sprintf("remote.%s.annex-uuid", name))
	uuidOut, err := cmd.Output()
	if err != nil {
		return false
	}
	uuid := strings.TrimSpace(string(uuidOut))
	if uuid == "" {
		return false
	}

	// Check git-annex remote.log for importtree=yes
	cmd = exec.Command("git", "show", "git-annex:remote.log")
	output, err := cmd.Output()
	if err != nil {
		return false
	}

	// Search for the UUID and check if it has importtree=yes
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if strings.Contains(line, uuid) && strings.Contains(line, "importtree=yes") {
			return true
		}
	}
	return false
}

// isDriveRemote checks if a remote uses the drive external special remote type
func isDriveRemote(name string) bool {
	cmd := exec.Command("git", "show", "git-annex:remote.log")
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(output), "\n") {
		if strings.Contains(line, fmt.Sprintf("name=%s", name)) {
			return strings.Contains(line, "externaltype=drive")
		}
	}
	return false
}

// driveTokenPath returns the path where the OAuth token for a drive remote is stored
func driveTokenPath(remoteName string) string {
	exoDir, err := configdir.ExoConfigDir()
	if err != nil {
		exoDir = filepath.Join(os.Getenv("HOME"), ".config", "exo")
	}
	return filepath.Join(exoDir, "drive-tokens", remoteName+".json")
}

// isExportRemote checks if a remote has exporttree=yes in git-annex remote.log
func isExportRemote(name string) bool {
	// Get remote UUID
	cmd := exec.Command("git", "config", "--get", fmt.Sprintf("remote.%s.annex-uuid", name))
	uuidOut, err := cmd.Output()
	if err != nil {
		return false
	}
	uuid := strings.TrimSpace(string(uuidOut))
	if uuid == "" {
		return false
	}

	// Check git-annex remote.log for exporttree=yes
	cmd = exec.Command("git", "show", "git-annex:remote.log")
	output, err := cmd.Output()
	if err != nil {
		return false
	}

	// Search for the UUID and check if it has exporttree=yes (but NOT importtree=yes)
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if strings.Contains(line, uuid) && strings.Contains(line, "exporttree=yes") && !strings.Contains(line, "importtree=yes") {
			return true
		}
	}
	return false
}

// remoteHasGrantsConfig checks if a remote has grants=true in its git-annex config.
func remoteHasGrantsConfig(name string) bool {
	cmd := exec.Command("git", "config", "--get", fmt.Sprintf("remote.%s.annex-uuid", name))
	uuidOut, err := cmd.Output()
	if err != nil {
		return false
	}
	uuid := strings.TrimSpace(string(uuidOut))
	if uuid == "" {
		return false
	}
	cmd = exec.Command("git", "show", "git-annex:remote.log")
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(output), "\n") {
		if strings.Contains(line, uuid) && strings.Contains(line, "grants=true") {
			return true
		}
	}
	return false
}

// remoteS3URL returns the s3url config for a remote from git-annex remote.log.
func remoteS3URL(name string) string {
	cmd := exec.Command("git", "config", "--get", fmt.Sprintf("remote.%s.annex-uuid", name))
	uuidOut, err := cmd.Output()
	if err != nil {
		return ""
	}
	uuid := strings.TrimSpace(string(uuidOut))
	if uuid == "" {
		return ""
	}
	cmd = exec.Command("git", "show", "git-annex:remote.log")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(output), "\n") {
		if !strings.Contains(line, uuid) {
			continue
		}
		for _, field := range strings.Fields(line) {
			if strings.HasPrefix(field, "s3url=") {
				return strings.TrimPrefix(field, "s3url=")
			}
		}
	}
	return ""
}

// hasGrantsWriteAccess checks if the current user has READWRITE access
// to a grants-enabled remote by probing the credential helper.
func hasGrantsWriteAccess(remote string) bool {
	s3url := remoteS3URL(remote)
	if s3url == "" {
		return true // can't determine, assume yes
	}
	credHelper := os.Getenv("EXO_CREDENTIAL_HELPER_BIN")
	if credHelper == "" {
		credHelper = "exo-credential-helper"
	}
	cmd := exec.Command(credHelper, "--s3url", s3url, "--permission", "READWRITE")
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	cmd.Stdout = io.Discard
	if err := cmd.Run(); err != nil {
		return false
	}
	return true
}

// getPatternsForRemote returns include/exclude patterns for a remote, with CLI overriding manifest
func getPatternsForRemote(remoteName string, remotesConfig *initcmd.RemotesConfig, cliInclude, cliExclude []string) (include, exclude []string) {
	// CLI patterns override manifest patterns
	if len(cliInclude) > 0 || len(cliExclude) > 0 {
		return cliInclude, cliExclude
	}

	// Look for remote in manifest
	if remotesConfig != nil {
		for _, remote := range remotesConfig.Remotes {
			if remote.Name == remoteName {
				return remote.Include, remote.Exclude
			}
		}
	}

	return nil, nil
}

func getImportDirForRemote(remoteName string, remotesConfig *initcmd.RemotesConfig, cliImportDir string) string {
	// CLI flag overrides manifest
	if cliImportDir != "" {
		return cliImportDir
	}

	// Look for remote in manifest
	if remotesConfig != nil {
		for _, remote := range remotesConfig.Remotes {
			if remote.Name == remoteName {
				return remote.ImportDir
			}
		}
	}

	return ""
}

// syncImportRemoteWithPatterns syncs an import remote using git annex import with include/exclude patterns
func syncImportRemoteWithPatterns(remote string, include, exclude []string, importDir, jobs, logPrefix string, noContent bool) error {
	// Check if CLI flags are provided
	if len(include) > 0 || len(exclude) > 0 {
		return fmt.Errorf("--include and --exclude CLI flags are not supported for import remotes.\nTo filter import remotes, set include/exclude patterns in .exohub/remotes and run:\n  exo init remote %s", remote)
	}

	// Get tracking branch
	trackingBranch, err := runCommandOutput([]string{"git", "config", "--get", fmt.Sprintf("remote.%s.annex-tracking-branch", remote)})
	if err != nil || strings.TrimSpace(trackingBranch) == "" {
		trackingBranch = "main"
	} else {
		trackingBranch = strings.TrimSpace(trackingBranch)
	}

	// Check if preferred content is configured
	currentWanted, _ := runCommandOutput([]string{"git", "annex", "wanted", remote})
	currentWanted = strings.TrimSpace(currentWanted)

	if currentWanted != "" && currentWanted != "standard" {
		fmt.Printf("Importing with preferred content filter: %s\n", currentWanted)
	}

	// Build git annex import command (honors preferred content if set)
	cmd := []string{"git", "annex", "import", trackingBranch, "--from", remote, "--jobs", jobs}

	// Run import - this downloads content and updates tracking branch
	if err := logging.ExecuteWithLoggingPrefixRetry("import", remote, cmd, nil, jobs, logPrefix, true); err != nil {
		return err
	}

	// Get list of files that were imported or modified (diff between current HEAD and tracking branch)
	trackingRef := fmt.Sprintf("%s/%s", remote, trackingBranch)

	// If --no-content flag is set, drop content immediately after import
	// This creates metadata-only tracking without storing file content locally
	if noContent {
		fmt.Printf("Dropping content (--no-content mode)\n")

		// Drop all content from the tracking branch
		// Use --force to drop even if this is the only copy
		dropCmd := []string{"git", "annex", "drop", "--force", "--from", "here", trackingRef}
		if err := runCommand(dropCmd); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to drop some content: %v\n", err)
		}

		fmt.Printf("Import complete (metadata only, no local content stored)\n")
		return nil
	}
	// Use --diff-filter=AM to get both Added and Modified files
	diffCmd := []string{"git", "diff", "--name-only", "--diff-filter=AM", "HEAD", trackingRef}
	diffOut, err := runCommandOutput(diffCmd)
	if err != nil {
		// If diff fails (e.g., unrelated histories), try listing all files in tracking branch
		lsCmd := []string{"git", "ls-tree", "-r", "--name-only", trackingRef}
		diffOut, err = runCommandOutput(lsCmd)
		if err != nil {
			return fmt.Errorf("failed to list imported files: %w", err)
		}
	}

	importedFiles := strings.Split(strings.TrimSpace(diffOut), "\n")
	if len(importedFiles) > 0 && importedFiles[0] != "" {
		destDir := importDir
		if destDir != "" {
			destDir = strings.TrimSuffix(destDir, "/")
		}

		// Collect files to checkout (new or modified)
		var filesToCheckout []string
		var filesToUpdate []string
		for _, file := range importedFiles {
			if file == "" {
				continue
			}

			checkPath := file
			if destDir != "" {
				checkPath = filepath.Join(destDir, file)
			}

			// Check if file exists
			if _, err := os.Lstat(checkPath); os.IsNotExist(err) {
				// New file - needs checkout and possible move
				filesToCheckout = append(filesToCheckout, file)
			} else {
				// File exists - might be modified, needs update in place
				filesToUpdate = append(filesToUpdate, checkPath)
			}
		}

		newFileCount := len(filesToCheckout)
		updateFileCount := len(filesToUpdate)
		totalCount := newFileCount + updateFileCount

		if totalCount == 0 {
			fmt.Printf("All files already up to date\n")
			return nil
		}

		if newFileCount > 0 && updateFileCount > 0 {
			fmt.Printf("Processing %d new and %d updated file(s)\n", newFileCount, updateFileCount)
		} else if newFileCount > 0 {
			fmt.Printf("Checking out %d new file(s)\n", newFileCount)
		} else {
			fmt.Printf("Updating %d modified file(s)\n", updateFileCount)
		}

		// First, update existing files in place
		if updateFileCount > 0 {
			for _, filePath := range filesToUpdate {
				checkoutCmd := []string{"git", "checkout", trackingRef, "--", filePath}
				if err := runCommand(checkoutCmd); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: failed to update %s: %v\n", filePath, err)
				}
			}
		}

		// Checkout new files individually
		if newFileCount > 0 {
			for _, file := range filesToCheckout {
				checkoutCmd := []string{"git", "checkout", trackingRef, "--", file}
				if err := runCommand(checkoutCmd); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: failed to checkout %s: %v\n", file, err)
				}
			}
		}

		// Move files to import directory if specified
		if destDir != "" && newFileCount > 0 {
			movedFiles, oldPaths := parallelMoveFiles(filesToCheckout, destDir, jobs)

			// Batch git index updates (no lock contention - single invocations)
			if len(movedFiles) > 0 {
				fmt.Printf("Updating git index for %d moved files\n", len(movedFiles))

				// 1. Fix symlinks first (needs files still tracked in index at old paths)
				fmt.Printf("Fixing symlinks for moved files\n")
				fixCmd := append([]string{"git", "annex", "fix"}, movedFiles...)
				if err := runCommand(fixCmd); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: failed to fix symlinks: %v\n", err)
				}

				// 2. Remove old paths from index
				rmCmd := append([]string{"git", "rm", "--cached", "--quiet", "--"}, oldPaths...)
				if err := runCommand(rmCmd); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: git rm --cached failed: %v\n", err)
				}

				// 3. Add new paths to index (plain git add - files are already
				// annex symlinks after fix, no need for git annex add)
				addCmd := append([]string{"git", "add", "--"}, movedFiles...)
				if err := runCommand(addCmd); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: git add failed: %v\n", err)
				}
			}
		}

		// Stage all changes in batches to avoid OOM from git-annex's
		// filter-process accumulating state for all files in a single invocation
		fmt.Printf("Staging imported files\n")
		var filesToStage []string
		for _, f := range importedFiles {
			if f != "" {
				filesToStage = append(filesToStage, f)
			}
		}
		filesToStage = append(filesToStage, filesToUpdate...)
		stageFilesBatched(filesToStage)
	}

	return nil
}

// stageFilesBatched runs "git add" in batches to avoid OOM from git-annex's
// filter-process accumulating state for all files in a single invocation.
func stageFilesBatched(files []string) {
	if len(files) == 0 {
		return
	}

	const batchSize = 500
	for i := 0; i < len(files); i += batchSize {
		end := i + batchSize
		if end > len(files) {
			end = len(files)
		}
		batch := files[i:end]
		cmd := append([]string{"git", "add", "--"}, batch...)
		if err := runCommand(cmd); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to stage batch of %d files: %v\n", len(batch), err)
		}
	}
}

// syncAnnexRemoteWithPatterns syncs an annex remote using split sync (sync + copy with patterns)
func syncAnnexRemoteWithPatterns(remote string, include, exclude []string, paths []string, jobs, logPrefix string, fromCLI bool) error {
	// Check if preferred content is configured
	currentWanted, _ := runCommandOutput([]string{"git", "annex", "wanted", remote})
	currentWanted = strings.TrimSpace(currentWanted)

	hasPreferredContent := currentWanted != "" && currentWanted != "standard"
	hasCLIFlags := fromCLI && (len(include) > 0 || len(exclude) > 0)

	// Error if both preferred content and CLI flags are provided
	if hasPreferredContent && hasCLIFlags {
		return fmt.Errorf("remote '%s' has preferred content configured: %s\nCannot use --include or --exclude CLI flags when preferred content is set.\nTo change filtering, edit .exohub/remotes and run:\n  exo init remote %s", remote, currentWanted, remote)
	}

	// If preferred content exists, use git annex sync --content (honors preferred content)
	if hasPreferredContent {
		fmt.Printf("Syncing with preferred content filter: %s\n", currentWanted)
		cmd := []string{"git", "annex", "sync", "--content", "--no-commit", remote, "--jobs", jobs}
		return runCommand(cmd)
	}

	// Otherwise use split sync with CLI patterns
	// Step 1: Sync metadata/refs only (no content)
	fmt.Printf("  - Syncing metadata with '%s'\n", remote)
	if err := runCommand([]string{"git", "annex", "sync", "--no-commit", remote}); err != nil {
		return fmt.Errorf("metadata sync failed: %w", err)
	}

	// Step 2: Copy content TO remote (filtered)
	fmt.Printf("  - Uploading filtered content to '%s'\n", remote)
	copyToCmd := []string{"git", "annex", "copy", "--to", remote}

	// Add include patterns
	for _, pattern := range include {
		copyToCmd = append(copyToCmd, "--include", pattern)
		fmt.Printf("    - include: %s\n", pattern)
	}

	// Add exclude patterns
	for _, pattern := range exclude {
		copyToCmd = append(copyToCmd, "--exclude", pattern)
		fmt.Printf("    - exclude: %s\n", pattern)
	}

	// Add matching: files we have that remote doesn't
	copyToCmd = append(copyToCmd, "--in", "here", "--not", "--in", remote)

	// Add jobs flag
	copyToCmd = append(copyToCmd, "--jobs", jobs)

	if err := runCommand(copyToCmd); err != nil {
		return fmt.Errorf("upload failed: %w", err)
	}

	// Step 3: Copy content FROM remote (filtered)
	fmt.Printf("  - Downloading filtered content from '%s'\n", remote)
	copyFromCmd := []string{"git", "annex", "copy", "--from", remote}

	// Add include patterns
	for _, pattern := range include {
		copyFromCmd = append(copyFromCmd, "--include", pattern)
	}

	// Add exclude patterns
	for _, pattern := range exclude {
		copyFromCmd = append(copyFromCmd, "--exclude", pattern)
	}

	// Add matching: files remote has that we don't
	copyFromCmd = append(copyFromCmd, "--not", "--in", "here")

	// Add jobs flag
	copyFromCmd = append(copyFromCmd, "--jobs", jobs)

	if err := runCommand(copyFromCmd); err != nil {
		return fmt.Errorf("download failed: %w", err)
	}

	return nil
}

// ensureAWSCredentials checks if AWS credentials are available and sets them from the credentials file if needed
func ensureAWSCredentials() error {
	// Check if credentials are already set
	if os.Getenv("AWS_ACCESS_KEY_ID") != "" && os.Getenv("AWS_SECRET_ACCESS_KEY") != "" {
		return nil // Already set, don't override
	}

	// Get AWS profile - use exohub profile by default
	profile := os.Getenv("EXOHUB_AWS_PROFILE")
	if profile == "" {
		profile = "exohub"
	}

	credsFile, err := configdir.AWSCredentialsFile()
	if err != nil {
		return fmt.Errorf("failed to resolve AWS credentials path: %w", err)
	}

	// Parse credentials file
	accessKey, secretKey, sessionToken, err := parseAWSCredentials(credsFile, profile)
	if err != nil {
		return err
	}

	// Set environment variables for this process and child processes
	os.Setenv("AWS_ACCESS_KEY_ID", accessKey)
	os.Setenv("AWS_SECRET_ACCESS_KEY", secretKey)
	if sessionToken != "" {
		os.Setenv("AWS_SESSION_TOKEN", sessionToken)
	}

	return nil
}

// parseAWSCredentials reads AWS credentials from the credentials file for a given profile
func parseAWSCredentials(filePath, profile string) (accessKey, secretKey, sessionToken string, err error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to open AWS credentials file %s: %w", filePath, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	inProfile := false
	profileHeader := "[" + profile + "]"

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Check for profile section
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			inProfile = (line == profileHeader)
			continue
		}

		// Parse key-value pairs within the profile
		if inProfile && strings.Contains(line, "=") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) != 2 {
				continue
			}
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])

			switch key {
			case "aws_access_key_id":
				accessKey = value
			case "aws_secret_access_key":
				secretKey = value
			case "aws_session_token":
				sessionToken = value
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return "", "", "", fmt.Errorf("error reading credentials file: %w", err)
	}

	if accessKey == "" || secretKey == "" {
		return "", "", "", fmt.Errorf("AWS credentials not found for profile '%s' in %s", profile, filePath)
	}

	return accessKey, secretKey, sessionToken, nil
}

// isCatalogRemoteType checks if a remote has a non-standard type (not annex/export/import/exospace).
func isCatalogRemoteType(name string, remotesConfig *initcmd.RemotesConfig) bool {
	if remotesConfig == nil {
		return false
	}
	for _, r := range remotesConfig.Remotes {
		if r.Name == name && !standardRemoteTypes[r.Type] {
			return true
		}
	}
	return false
}

// standardRemoteTypes are the built-in types handled by git-annex directly.
var standardRemoteTypes = map[string]bool{
	"annex": true, "export": true, "import": true, "exospace": true, "drive": true,
}

// getCatalogRemoteBinary checks if a remote is a catalog type (non-standard)
// and returns the binary name if a matching git-annex-remote-<type> binary exists.
func getCatalogRemoteBinary(name string, remotesConfig *initcmd.RemotesConfig) (string, bool) {
	if remotesConfig == nil {
		return "", false
	}
	for _, r := range remotesConfig.Remotes {
		if r.Name != name {
			continue
		}
		if standardRemoteTypes[r.Type] {
			return "", false
		}
		// Non-standard type - look for git-annex-remote-<type>-<mode> binary
		if r.Mode == "" {
			return "", false
		}
		binaryName := fmt.Sprintf("git-annex-remote-%s-%s", r.Type, r.Mode)
		if _, err := exec.LookPath(binaryName); err == nil {
			return binaryName, true
		}
		return "", false
	}
	return "", false
}

// shouldSkipPublish checks if a catalog remote should be skipped based on publish_on constraint.
func shouldSkipPublish(name string, remotesConfig *initcmd.RemotesConfig) bool {
	rc := getRemoteConfigByName(name, remotesConfig)
	if rc == nil || rc.PublishOn == "" || rc.PublishOn == "always" {
		return false
	}
	if rc.PublishOn == "tag" {
		cmd := exec.Command("git", "describe", "--tags", "--exact-match", "HEAD")
		return cmd.Run() != nil // skip if HEAD is not a tag
	}
	return false
}

// getRemoteConfigByName returns the RemoteConfig for a named remote.
func getRemoteConfigByName(name string, remotesConfig *initcmd.RemotesConfig) *initcmd.RemoteConfig {
	if remotesConfig == nil {
		return nil
	}
	for _, r := range remotesConfig.Remotes {
		if r.Name == name {
			rc := r
			return &rc
		}
	}
	return nil
}

// bundleManifest is a minimal view of bundle.json.
type bundleManifest struct {
	RefName string `json:"ref_name"`
	Ref     string `json:"ref"`
	RepoURL string `json:"repo_url"`
}

// syncCatalogRemote delegates publishing to a catalog remote's binary.
// The binary is called with: <binary> publish --s3url=... --bundle-dir=... [--artifactdb-dir=...] [--grants] [--instance-url=...] [--api-url=...]
func syncCatalogRemote(remote string, remotesConfig *initcmd.RemotesConfig, binaryName string) error {
	rc := getRemoteConfigByName(remote, remotesConfig)
	if rc == nil {
		return fmt.Errorf("remote '%s' not found in .exohub/remotes", remote)
	}

	remoteStyle := lipgloss.NewStyle().Foreground(palette.Current().Error.Adaptive()).Bold(true)
	fmt.Printf("💎↑ Publishing to catalog remote '%s' via %s\n", remoteStyle.Render(remote), binaryName)

	// Find bundle directory
	bundleDir, _, _, _, err := findBundleInfoForNotification()
	if err != nil {
		return fmt.Errorf("no bundle output found (run 'exo bundle' first): %w", err)
	}

	// Build publish command args
	args := []string{"publish",
		"--s3url", rc.S3URL,
		"--bundle-dir", bundleDir,
	}

	// Include .artifactdb/ if present
	if info, statErr := os.Stat(".artifactdb"); statErr == nil && info.IsDir() {
		args = append(args, "--artifactdb-dir", ".artifactdb")
	}

	if rc.Grants {
		args = append(args, "--grants")
	}
	if rc.InstanceURL != "" {
		args = append(args, "--instance-url", rc.InstanceURL)
	}
	if rc.ProjectID != "" {
		args = append(args, "--project-id", rc.ProjectID)
	}

	// Pass ExoHub API URL
	apiURL := defaults.APIBase()
	args = append(args, "--api-url", apiURL)

	if commandutil.IsDebug() {
		fmt.Fprintf(os.Stderr, "DEBUG: EXOHUB_API_URL=%s\n", apiURL)
		fmt.Fprintf(os.Stderr, "DEBUG: catalog binary=%s args=%v\n", binaryName, args)
		if rc.InstanceURL != "" {
			fmt.Fprintf(os.Stderr, "DEBUG: instance_url=%s\n", rc.InstanceURL)
		}
	}

	// Run the catalog binary with AWS_PROFILE injected so s5cmd picks up exohub credentials
	cmd := commandutil.Command(binaryName, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s publish failed: %w", binaryName, err)
	}

	return nil
}

// findBundleInfoForNotification locates bundle.json and extracts ref info.
func findBundleInfoForNotification() (bundleDir, refName, ref, repoURL string, err error) {
	baseDir := filepath.Join(".exohub", "bundles")
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return "", "", "", "", fmt.Errorf("no bundle output found: %w", err)
	}

	// Find the bundle directory (same logic as the remote binary)
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, filepath.Join(baseDir, e.Name()))
		}
	}
	if len(dirs) == 0 {
		return "", "", "", "", fmt.Errorf("no bundle output found")
	}

	// Try current HEAD tag first, then branch, then last dir
	dir := dirs[len(dirs)-1]
	cmd := exec.Command("git", "describe", "--tags", "--exact-match", "HEAD")
	if out, err := cmd.Output(); err == nil {
		tag := strings.TrimSpace(string(out))
		candidate := filepath.Join(baseDir, tag)
		if info, statErr := os.Stat(candidate); statErr == nil && info.IsDir() {
			dir = candidate
		}
	} else {
		cmd = exec.Command("git", "symbolic-ref", "--short", "HEAD")
		if out, err := cmd.Output(); err == nil {
			branch := strings.TrimSpace(string(out))
			candidate := filepath.Join(baseDir, branch)
			if info, statErr := os.Stat(candidate); statErr == nil && info.IsDir() {
				dir = candidate
			}
		}
	}

	// Read bundle.json
	data, err := os.ReadFile(filepath.Join(dir, "bundle.json"))
	if err != nil {
		return "", "", "", "", err
	}
	var manifest bundleManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return "", "", "", "", err
	}

	return dir, manifest.RefName, manifest.Ref, manifest.RepoURL, nil
}
