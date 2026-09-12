package export

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/Genentech/exohub/go/exo/commandutil"
	logging "github.com/Genentech/exohub/go/exo/commandutil/logging"
	"github.com/Genentech/exohub/go/exo/internal/defaults"
)

type stringValue string
type stringList []string

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

type manifest struct {
	From            stringValue `yaml:"from"`
	To              stringValue `yaml:"to"`
	Ref             stringValue `yaml:"ref"`
	Jobs            stringValue `yaml:"jobs"`
	Paths           stringList  `yaml:"paths"`
	WithRemotes     stringList  `yaml:"with-remotes"`
}

func NewCommand() *cobra.Command {
	var (
		manifestPath string
		fromRemote   string
		toRemote     string
		ref          = "HEAD"
		jobs         = strings.TrimSpace(os.Getenv("EXOHUB_JOBS"))
		jobsFlag     string
		paths        []string
		toRemotes    []string
		dryRun       bool
		outputJSON   bool
		logPrefix    string
		branchPrefix bool
		autoYes      bool
	)
	if jobs == "" {
		jobs = "1"
	}

	rootCmd := &cobra.Command{
		Use:   "export",
		Short: "Export annexed content",
		Long: `Export annexed content to a remote.

Branch Prefix Mode:
  Controls whether branch names appear in S3 paths when using --path flag.

  Default behavior (no flag):
    Uses branch only (no colon notation)
    Example: main → s3://bucket/project/data/
    Assumes s3url contains the full target path

  With --branch-prefix flag:
    Uses branch:subdir notation (creates branch directories in S3)
    Example: main:data/ → s3://bucket/_export/refs/heads/main/data/
    Branch name becomes part of the S3 path structure

Examples:
  # Default (no colon)
  exo export --to my-remote --path data/
  → Uses: main
  → Result: s3://bucket/target/file.txt
  (Assumes s3url is s3://bucket/target/)

  # With branch prefix
  exo export --to my-remote --path data/ --branch-prefix
  → Uses: main:data/
  → Result: s3://bucket/_export/refs/heads/main/data/file.txt

  # Multiple paths require --branch-prefix
  exo export --to my-remote --path dir1/ --path dir2/ --branch-prefix

Note: Multiple --path flags require --branch-prefix flag.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireBins("git-annex"); err != nil {
				exitWithError(err)
			}

			if outputJSON && !dryRun {
				exitWithError(fmt.Errorf("--json requires --dry-run"))
			}

			if manifestPath != "" {
				if err := runManifestValidation(manifestPath); err != nil {
					exitWithError(err)
				}
				m, err := parseManifest(manifestPath)
				if err != nil {
					exitWithError(err)
				}
				if fromRemote == "" && m.From != "" {
					fromRemote = string(m.From)
				}
				if toRemote == "" && m.To != "" {
					toRemote = string(m.To)
				}
				if ref == "HEAD" && m.Ref != "" {
					ref = string(m.Ref)
				}
				if m.Jobs != "" {
					jobs = string(m.Jobs)
				}
				// CLI flag takes precedence over manifest
				if jobsFlag != "" {
					jobs = jobsFlag
				}
				if len(paths) == 0 {
					paths = append(paths, m.Paths...)
				}
				if toRemote == "" && len(toRemotes) == 0 {
					toRemotes = append(toRemotes, m.WithRemotes...)
				}
				if toRemote == "" && len(toRemotes) == 0 && m.To != "" {
					toRemotes = append(toRemotes, string(m.To))
				}
			}

			if toRemote != "" {
				toRemotes = []string{toRemote}
			}

			if len(toRemotes) == 0 {
				exitWithError(fmt.Errorf("--to is required"))
			}

			if err := ensureGitRepo(); err != nil {
				exitWithError(err)
			}

			if err := verifyRef(ref); err != nil {
				exitWithError(fmt.Errorf("ref '%s' not found (must be a commit, tag, or branch)", ref))
			}

			if fromRemote != "" {
				fmt.Printf("Enabling git-annex remote '%s'\n", fromRemote)
				if err := runCommand([]string{"git", "annex", "enableremote", fromRemote}); err != nil {
					exitWithError(fmt.Errorf("failed to enable remote '%s'", fromRemote))
				}
			}

			for _, remote := range toRemotes {
				remote = strings.TrimSpace(remote)
				if remote == "" {
					continue
				}

				fmt.Printf("Enabling git-annex remote '%s'\n", remote)
				if err := runCommand([]string{"git", "annex", "enableremote", remote}); err != nil {
					exitWithError(fmt.Errorf("failed to enable remote '%s'", remote))
				}

				// Validate and update tracking branch if needed
				if err := validateAndUpdateTrackingBranch(remote, ref, autoYes); err != nil {
					exitWithError(err)
				}

				expandedPaths := expandPaths(paths)

				// Determine if branch-prefix mode should be enabled
				// Default is OFF (no colon), unless --branch-prefix flag is used
				useBranchPrefix := branchPrefix

				// Validate: multiple paths require branch-prefix mode
				if len(expandedPaths) > 1 && !useBranchPrefix {
					exitWithError(fmt.Errorf("Multiple --path flags require --branch-prefix flag.\nWithout branch prefixes, each path would export the full branch to the same location.\nAdd --branch-prefix to enable branch:subdir notation for multiple paths."))
				}

				if dryRun {
					if err := runExportDryRun(remote, expandedPaths, jobs, fromRemote, ref, outputJSON, logPrefix); err != nil {
						exitWithError(err)
					}
					continue
				}

				if fromRemote != "" {
					fmt.Printf("Exporting ref '%s' from '%s' to '%s'\n", ref, fromRemote, remote)
				} else {
					fmt.Printf("Exporting ref '%s' from 'here' to '%s'\n", ref, remote)
				}

				// Build export refs: either single ref or multiple ref:path entries
				var exportRefs []string
				var metaPaths []string

				if len(expandedPaths) == 0 {
					// No paths specified: export the entire ref
					exportRefs = []string{ref}
				} else {
					// Paths specified: build ref:path for each (if branch-prefix mode)
					for _, exportPath := range expandedPaths {
						exportPath = strings.TrimSpace(exportPath)
						if exportPath == "" {
							continue
						}
						if useBranchPrefix {
							exportRefs = append(exportRefs, fmt.Sprintf("%s:%s", ref, exportPath))
						} else {
							exportRefs = append(exportRefs, ref)
						}
						metaPaths = append(metaPaths, exportPath)
					}
				}

				// Build single git annex export command with all refs
				cmdArgs := []string{"git", "annex", "export"}
				cmdArgs = append(cmdArgs, exportRefs...)
				cmdArgs = append(cmdArgs, "--to", remote)
				if fromRemote != "" {
					cmdArgs = append(cmdArgs, "--from", fromRemote)
				}
				cmdArgs = append(cmdArgs, "--jobs", jobs)

				// Set GIT_ANNEX_EXPORT_REF to first ref for compatibility
				if len(exportRefs) > 0 {
					os.Setenv("GIT_ANNEX_EXPORT_REF", exportRefs[0])
				}

				if err := logging.ExecuteWithLoggingPrefixRetry("export", remote, cmdArgs, metaPaths, jobs, logPrefix, true); err != nil {
					exitWithError(err)
				}
			}
			return nil
		},
	}

	rootCmd.Flags().StringVar(&manifestPath, "manifest", "", "Path to manifest YAML")
	rootCmd.Flags().StringVar(&fromRemote, "from", "", "Source annex remote")
	rootCmd.Flags().StringVar(&toRemote, "to", "", "Destination annex remote")
	rootCmd.Flags().StringVar(&ref, "ref", "HEAD", "Commit, tag, or branch")
	rootCmd.Flags().StringArrayVar(&paths, "path", nil, "Path to limit export (repeatable)")
	rootCmd.Flags().BoolVar(&dryRun, "dry-run", false, "Report planned exports without running git-annex export")
	rootCmd.Flags().BoolVar(&outputJSON, "json", false, "Emit a single JSON dry-run report to stdout")
	rootCmd.Flags().StringVar(&logPrefix, "log-prefix", "", "Prefix for log filenames (useful for Temporal workflows)")
	rootCmd.Flags().StringVarP(&jobsFlag, "jobs", "J", "", "Number of parallel jobs (default: 1)")
	rootCmd.Flags().BoolVar(&branchPrefix, "branch-prefix", false, "Use branch:subdir notation (creates branch directories in S3 paths)")
	rootCmd.Flags().BoolVar(&autoYes, "yes", false, "Automatically confirm tracking branch updates")

	return rootCmd
}

func exitWithError(err error) {
	var exitErr exitError
	if errors.As(err, &exitErr) {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(exitErr.code)
	}
	fmt.Fprintln(os.Stderr, err.Error())
	os.Exit(1)
}

type exitError struct {
	code int
	err  error
}

func (e exitError) Error() string {
	if e.err != nil {
		return e.err.Error()
	}
	return fmt.Sprintf("exit code %d", e.code)
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

func parseManifest(path string) (manifest, error) {
	if _, err := os.Stat(path); err != nil {
		return manifest{}, fmt.Errorf("Manifest not found: %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return manifest{}, err
	}
	var m manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return manifest{}, err
	}
	return m, nil
}

func runManifestValidation(path string) error {
	return commandutil.ValidateManifestFile("export", path)
}

func ensureGitRepo() error {
	info, err := os.Stat(".git")
	if err != nil || !info.IsDir() {
		return errors.New("Current directory is not a git repository")
	}
	return nil
}

func verifyRef(ref string) error {
	cmd := commandutil.Command("git", "rev-parse", "--quiet", "--verify", fmt.Sprintf("%s^{commit}", ref))
	return cmd.Run()
}

func runCommand(args []string) error {
	cmd := commandutil.Command(args[0], args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
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

func runExportDryRun(remote string, paths []string, jobs string, fromRemote string, ref string, outputJSON bool, logPrefix string) error {
	if outputJSON {
		var buf bytes.Buffer
		writer := io.MultiWriter(os.Stdout, &buf)
		if err := commandutil.StreamDryRunJSONExport(writer, []string{remote}, paths, defaults.DryRunReportSchema()); err != nil {
			return err
		}
		extras := map[string]any{
			"dry_run":          true,
			"remote_mutations": false,
			"json":             true,
		}
		cmd := buildExportDryRunCmd(remote, paths, fromRemote, ref, outputJSON)
		return logging.RecordDryRunLogPrefix("export", remote, cmd, paths, jobs, buf.String(), extras, logPrefix)
	}

	var buf bytes.Buffer
	writer := io.MultiWriter(os.Stdout, &buf)
	fmt.Fprintf(writer, "Dry-run plan for remote '%s'\n", remote)

	// Check for preferred content and display it
	preferredContent := commandutil.GetPreferredContent(remote)
	if preferredContent != "" {
		fmt.Fprintf(writer, "  export (preferred content: %s)\n", preferredContent)
	}

	// Use export-specific queries that respect preferred content
	for _, query := range commandutil.BuildDryRunQueriesWithPreferredContent(remote, false, true) {
		files, err := commandutil.RunAnnexFindFiles(query.Args, paths)
		if err != nil {
			return fmt.Errorf("dry-run query failed for %s: %w", query.Label, err)
		}
		fmt.Fprintf(writer, "  %s\n", query.Label)
		if len(files) == 0 {
			fmt.Fprintln(writer, "    (none)")
			continue
		}
		for _, file := range files {
			fmt.Fprintf(writer, "    %s\n", file)
		}
	}
	extras := map[string]any{
		"dry_run":          true,
		"remote_mutations": false,
	}
	cmd := buildExportDryRunCmd(remote, paths, fromRemote, ref, outputJSON)
	return logging.RecordDryRunLogPrefix("export", remote, cmd, paths, jobs, buf.String(), extras, logPrefix)
}

func buildExportDryRunCmd(remote string, paths []string, fromRemote string, ref string, outputJSON bool) []string {
	args := []string{"exo", "export", "--dry-run"}
	if outputJSON {
		args = append(args, "--json")
	}
	args = append(args, "--to", remote)
	if fromRemote != "" {
		args = append(args, "--from", fromRemote)
	}
	if ref != "" {
		args = append(args, "--ref", ref)
	}
	for _, path := range paths {
		args = append(args, "--path", path)
	}
	return args
}

// getTrackingBranch returns the configured tracking branch for an export remote
func getTrackingBranch(remoteName string) string {
	cmd := commandutil.Command("git", "config", "--get", fmt.Sprintf("remote.%s.annex-tracking-branch", remoteName))
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// setTrackingBranch sets the tracking branch for an export remote
func setTrackingBranch(remoteName, branch string) error {
	cmd := commandutil.Command("git", "config", fmt.Sprintf("remote.%s.annex-tracking-branch", remoteName), branch)
	return cmd.Run()
}

// getS3URLForRemote attempts to get the s3url for a remote (for display purposes)
func getS3URLForRemote(remoteName string) string {
	// Try to get from git config
	cmd := commandutil.Command("git", "config", "--get", fmt.Sprintf("remote.%s.annex-s3url", remoteName))
	out, err := cmd.Output()
	if err == nil && len(out) > 0 {
		return strings.TrimSpace(string(out))
	}

	// Try annex-remote config
	cmd = commandutil.Command("git", "config", "--get-regexp", fmt.Sprintf("annex-remote.*.config.s3url"))
	out, err = cmd.Output()
	if err == nil {
		// Parse output to find matching remote
		lines := strings.Split(string(out), "\n")
		for _, line := range lines {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				return parts[len(parts)-1]
			}
		}
	}

	return "(unknown s3url)"
}

// askTrackingBranchConfirmation prompts the user to confirm tracking branch update
func askTrackingBranchConfirmation(remoteName, currentBranch, newBranch string, autoYes bool) (bool, error) {
	if autoYes {
		fmt.Fprintf(os.Stderr, "ℹ️  Updating tracking branch from '%s' to '%s'\n", currentBranch, newBranch)
		return true, nil
	}

	s3url := getS3URLForRemote(remoteName)

	fmt.Fprintf(os.Stderr, "\n⚠️  Warning: Tracking branch mismatch\n")
	fmt.Fprintf(os.Stderr, "    Current tracking branch: %s\n", currentBranch)
	fmt.Fprintf(os.Stderr, "    Export ref:              %s\n\n", newBranch)
	fmt.Fprintf(os.Stderr, "This will:\n")
	fmt.Fprintf(os.Stderr, "  • Remove all files from %s/%s/\n", s3url, currentBranch)
	fmt.Fprintf(os.Stderr, "  • Export all files to %s/%s/\n\n", s3url, newBranch)
	fmt.Fprintf(os.Stderr, "If you want to KEEP files in %s/ and ALSO export to %s/:\n", currentBranch, newBranch)
	fmt.Fprintf(os.Stderr, "  1. Create a second export remote in .exohub/remotes:\n\n")
	fmt.Fprintf(os.Stderr, "     - name: %s-%s\n", remoteName, newBranch)
	fmt.Fprintf(os.Stderr, "       type: export\n")
	fmt.Fprintf(os.Stderr, "       s3url: %s-%s\n", s3url, newBranch)
	fmt.Fprintf(os.Stderr, "       tracking_branch: %s\n", newBranch)
	fmt.Fprintf(os.Stderr, "       include: [...]\n\n")
	fmt.Fprintf(os.Stderr, "  2. Run: exo init\n")
	fmt.Fprintf(os.Stderr, "  3. Run: exo export --to %s-%s --ref %s\n\n", remoteName, newBranch, newBranch)

	fmt.Fprintf(os.Stderr, "Update tracking branch to '%s' and switch export location? [y/N]: ", newBranch)

	var response string
	if _, err := fmt.Scanln(&response); err != nil {
		// Handle empty input (just Enter pressed)
		return false, nil
	}

	response = strings.ToLower(strings.TrimSpace(response))
	return response == "y" || response == "yes", nil
}

// getCurrentBranchName returns the current branch name, or empty string if detached HEAD
func getCurrentBranchName() string {
	cmd := commandutil.Command("git", "symbolic-ref", "--short", "HEAD")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

// validateAndUpdateTrackingBranch checks if tracking branch matches ref and prompts for update if needed
func validateAndUpdateTrackingBranch(remoteName, ref string, autoYes bool) error {
	currentTrackingBranch := getTrackingBranch(remoteName)

	// Resolve HEAD to actual branch name for comparison
	resolvedRef := ref
	if ref == "HEAD" {
		if branch := getCurrentBranchName(); branch != "" {
			resolvedRef = branch
		}
	}

	// If tracking branch not set, set it automatically without prompting
	if currentTrackingBranch == "" {
		fmt.Fprintf(os.Stderr, "ℹ️  Setting tracking branch to '%s' (not previously configured)\n", resolvedRef)
		return setTrackingBranch(remoteName, resolvedRef)
	}

	// If they match, nothing to do
	if currentTrackingBranch == resolvedRef {
		return nil
	}

	// They don't match - ask for confirmation (use resolvedRef for display)
	confirmed, err := askTrackingBranchConfirmation(remoteName, currentTrackingBranch, resolvedRef, autoYes)
	if err != nil {
		return fmt.Errorf("failed to get confirmation: %w", err)
	}

	if !confirmed {
		fmt.Fprintf(os.Stderr, "✗ Export cancelled\n")
		fmt.Fprintf(os.Stderr, "  Keeping tracking branch as '%s'\n", currentTrackingBranch)
		return fmt.Errorf("export cancelled by user")
	}

	// Update tracking branch
	if err := setTrackingBranch(remoteName, resolvedRef); err != nil {
		return fmt.Errorf("failed to update tracking branch: %w", err)
	}

	fmt.Fprintf(os.Stderr, "✓ Updated tracking branch to '%s'\n\n", resolvedRef)
	return nil
}
