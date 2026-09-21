package init

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"sync"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/Genentech/exohub/go/exo/palette"

	"gopkg.in/yaml.v3"

	"github.com/Genentech/exohub/go/exo/commandutil"
	"github.com/Genentech/exohub/go/exo/configdir"
	contextcmd "github.com/Genentech/exohub/go/exo/commands/context"
	"github.com/Genentech/exohub/go/exo/gitprovider"
)

// getRepoNameFromCwd returns the current directory name, used as default repo name.
func getRepoNameFromCwd() string {
	return getRepoName()
}

var command = commandutil.Command

// errRemoteSkipped is returned when a remote is intentionally skipped (e.g. read-only access).
var errRemoteSkipped = errors.New("remote skipped")

const longText = `Initialize repository and remotes.

The 'exo init' command orchestrates repository creation and remote validation:
  - With --profile <name>: fetch a server-authored profile, resolve vars, prompt, render files, run actions
  - With --create-repo <url>: creates repo via provider API using the URL
  - With --create-repo + --context: creates repo using context defaults
  - Without --create-repo (existing git repo): validates remotes from .exohub/remotes

Profile mode (--profile):
  Fetches a named profile from GET /api/profiles/{name} and performs all
  variable resolution, prompting, Go-template rendering, file writing, and
  actions client-side, then proceeds with the normal init flow.

  Template variables (all top-level, no namespace prefix):
    .UnixID    - JWT preferred_username or OS username
    .Folder    - current directory base name
    .<var>     - each key from the profile vars block
    .<NAME>    - value of EXOHUB_TPL_<NAME> environment variable
    .<answer>  - each answer from the prompts block
    (prompt answers override vars; env vars override vars; on key collision, last write wins)

Repository creation:
  - Auto-detects provider (Gitea, GitHub, GitLab) from host via HTTP inspection
  - Repo name precedence: --name > URL-derived > context repo > cwd folder name
  - Creates repository via provider API if it doesn't exist
  - Prints summary and asks for confirmation (use --yes to auto-accept)
  - Initializes git repository and sets origin remote
  - Configures git-annex remotes from .exohub/remotes if present
  - Reads shared settings from .exohub/config (e.g., thin)

Context is resolved from: --context flag > EXOHUB_CONTEXT env var

Examples:
  exo init --profile sandbox
  exo init --profile sandbox --yes --dry-run
  exo init --profile sandbox --force  # overwrite existing .exohub/
  EXOHUB_PROFILE=sandbox exo init  # equivalent to --profile sandbox (ignored in existing repos)
  exo init --create-repo https://github.com/org/mynewrepo
  exo --context myctx init --create-repo --name mynewrepo
  EXOHUB_CONTEXT=default exo init --create-repo --name mynewrepo
  exo init  # validate remotes in existing git repo
  exo init --remote s5-annex  # only configure/validate the s5-annex remote

Authentication:
  Set token via environment variable:
    EXOHUB_GITEA_TOKEN  - For Gitea providers
    EXOHUB_GITHUB_TOKEN - For GitHub providers
    EXOHUB_GITLAB_TOKEN - For GitLab providers
    EXOHUB_GIT_TOKEN    - Generic fallback for any provider

UUID Cleanup (for handling duplicate remotes):
  --dead <uuid>    Mark a remote UUID as dead and remove local config (SAFE, recommended).
                   Data on S3/rsync remains safe. Can be undone by re-running exo init.
  --destroy <uuid> Completely remove UUID from git-annex history (DESTRUCTIVE).
                   WARNING: Rewrites git-annex branch, cannot be easily undone.
                   Creates automatic backup branch for recovery.

UUID Cleanup Examples:
  # Safe cleanup of duplicate UUID (recommended)
  exo init --dead 018c3a7d-fff2-4128-8dbb-73596ecdc680

  # Preview what would be removed
  exo init --dead 018c3a7d-fff2-4128-8dbb-73596ecdc680 --dry-run

  # Non-interactive cleanup
  exo init --dead 018c3a7d-fff2-4128-8dbb-73596ecdc680 --yes

  # Complete destruction (use with caution)
  exo init --destroy 018c3a7d-fff2-4128-8dbb-73596ecdc680

  # Non-interactive destruction (requires confirmation)
  exo init --destroy <uuid> --yes --confirm <uuid>

The 'exo init remote' subcommand creates git-annex remotes:
  - With --type and --name: creates remote declaratively
  - Without args: launches interactive TUI

Supported remote types: annex, export, import, exospace, drive`

var (
	flagCreateRepo  bool
	flagProfile     string
	flagPickProfile bool
	flagForce       bool
	flagRepoName   string
	flagYes        bool
	flagReset      bool
	flagDead       string
	flagDestroy    string
	flagDryRun     bool
	flagConfirm    string
	flagRemote     string
)

var initStylesOnce sync.Once

func initInitStyles() {
	initStylesOnce.Do(func() {
		p := palette.Current()
		SuccessStyle = lipgloss.NewStyle().Foreground(p.Success.Adaptive()).Bold(true)
		ErrorStyle = lipgloss.NewStyle().Foreground(p.Error.Adaptive()).Bold(true)
		WarningStyle = lipgloss.NewStyle().Foreground(p.Warning.Adaptive()).Bold(true)
		CommandStyle = lipgloss.NewStyle().Foreground(p.Highlight.Adaptive())
	})
}

var (
	SuccessStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
	ErrorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
	WarningStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	CommandStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("86"))
)

func NewCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "init [url]",
		Short: "Initialize repository and remotes",
		Long:  longText,
		Args:  cobra.MaximumNArgs(1),
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			initInitStyles()
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInit(args)
		},
	}

	rootCmd.Flags().BoolVar(&flagCreateRepo, "create-repo", false, "Create repository on the git host")
	rootCmd.Flags().StringVar(&flagProfile, "profile", "", "Fetch and apply a server-authored profile by name")
	rootCmd.Flags().BoolVar(&flagPickProfile, "profile-select", false, "Interactively select a profile to apply")
	rootCmd.Flags().BoolVar(&flagForce, "force", false, "Overwrite existing .exohub/ when applying a profile")
	rootCmd.Flags().StringVar(&flagRepoName, "name", "", "Use explicit repository name")
	rootCmd.Flags().BoolVar(&flagYes, "yes", false, "Accept default choices automatically")
	rootCmd.Flags().BoolVar(&flagReset, "reset", false, "Remove .exohub/ and .git/ directories before initializing")
	rootCmd.Flags().StringVar(&flagDead, "dead", "", "Mark a remote UUID as dead and remove its configuration")
	rootCmd.Flags().StringVar(&flagDestroy, "destroy", "", "Completely destroy a UUID from git-annex history (DESTRUCTIVE)")
	rootCmd.Flags().BoolVar(&flagDryRun, "dry-run", false, "Show what would be done without actually doing it")
	rootCmd.Flags().StringVar(&flagConfirm, "confirm", "", "Confirmation UUID for --destroy (required in non-interactive mode)")
	rootCmd.Flags().StringVar(&flagRemote, "remote", "", "Only configure or validate the named remote (can be used with existing git repos)")

	rootCmd.AddCommand(NewRemoteCommand())

	return rootCmd
}

func exit(err error) {
	if err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

// runInit is the main logic for 'exo init'
func runInit(args []string) error {
	// Handle UUID cleanup operations (mutually exclusive with other operations)
	if flagDead != "" {
		return markUUIDasDead(flagDead, flagDryRun)
	}
	if flagDestroy != "" {
		return destroyUUID(flagDestroy, flagDryRun, flagConfirm)
	}

	// Handle reset if requested - this is a standalone action
	if flagReset {
		if err := resetInitState(); err != nil {
			return fmt.Errorf("failed to reset: %w", err)
		}
		return nil // Stop here after reset
	}

	isGit := IsGitRepo()

	// Load shared config from .exohub/config
	exohubConfig, err := LoadExohubConfig()
	if err != nil {
		return err
	}

	// Load remotes config.
	remotesConfig, remotesConfigErr := LoadRemotesConfig()
	if remotesConfigErr != nil {
		remotesConfig = &RemotesConfig{}
	}

	// Check for legacy .exohub/context and warn about thin
	checkLegacyContext(exohubConfig)

	// Resolve effective profile name: --profile flag, then EXOHUB_PROFILE env var.
	// --profile-select triggers interactive picker (empty name passed to runProfileInit).
	effectiveProfile := flagProfile
	profileRequested := flagProfile != "" || flagPickProfile
	if effectiveProfile == "" && !flagPickProfile && !isGit {
		if envProfile := strings.TrimSpace(os.Getenv("EXOHUB_PROFILE")); envProfile != "" {
			effectiveProfile = envProfile
			profileRequested = true
		}
	}

	if profileRequested {
		if err := runProfileInit(effectiveProfile, args, nil); err != nil {
			return err
		}
		if flagDryRun {
			return nil
		}
		// Reload configs written by the profile before continuing into full init.
		exohubConfig, _ = LoadExohubConfig()
		if rc, err := LoadRemotesConfig(); err == nil {
			remotesConfig = rc
		} else {
			remotesConfig = &RemotesConfig{}
		}
		// Ensure a git repo exists (auto git-init for fresh directories).
		if !IsGitRepo() {
			if err := ensureGitRepo(); err != nil {
				return fmt.Errorf("profile written but git init failed: %w", err)
			}
		}
		// Always initialize git-annex regardless of whether remotes are defined.
		if err := ensureAnnexInitialized(); err != nil {
			return fmt.Errorf("git-annex init failed: %w", err)
		}
		applyAnnexDefaults(exohubConfig)
		// Set up any remotes defined by the profile.
		if len(remotesConfig.Remotes) > 0 {
			return handleRemoteValidation(exohubConfig, remotesConfig, flagRemote)
		}
		return nil
	}

	// Non-profile paths need a valid remotes config — surface parse error now.
	if remotesConfigErr != nil {
		return remotesConfigErr
	}

	// Determine if we should create a repo
	hasURLArg := len(args) > 0
	shouldCreateRepo := flagCreateRepo || hasURLArg

	if shouldCreateRepo {
		return handleRepoCreation(exohubConfig, remotesConfig, args)
	}

	// Existing git repo: validate remotes
	if isGit {
		return handleRemoteValidation(exohubConfig, remotesConfig, flagRemote)
	}

	return fmt.Errorf("not a git repository. Use --create-repo with a URL or context to create one, or --profile <name> to apply a profile")
}

// resetInitState removes .exohub/ and .git/ directories to allow starting fresh
func resetInitState() error {
	fmt.Println("🔄 Resetting initialization state...")

	// Check if git-annex is initialized and uninit it safely
	gitPath := ".git"
	if _, err := os.Stat(gitPath); err == nil {
		annexPath := filepath.Join(gitPath, "annex")
		if _, err := os.Stat(annexPath); err == nil {
			// Confirm git annex uninit unless --yes flag is set
			shouldUninit := true
			if !flagYes {
				ok, err := askYesNoTUI("Run 'git annex uninit' to safely preserve annex data?", false)
				if err != nil {
					return err
				}
				shouldUninit = ok
			}

			if shouldUninit {
				fmt.Println("   Running git annex uninit to preserve data...")
				// Use --fast to skip converting symlinks to regular files
				// This prevents failures when annexed file content is not present locally
				cmd := command("git", "annex", "uninit", "--fast")
				cmd.Stdout = os.Stdout
				cmd.Stderr = os.Stderr
				if err := cmd.Run(); err != nil {
					fmt.Printf("   ⚠️  git annex uninit failed (continuing anyway): %v\n", err)
				} else {
					fmt.Println("   ✓ git annex uninit completed")
				}
			} else {
				fmt.Println("   Skipping git annex uninit")
			}
		}

		// Confirm .git removal unless --yes flag is set
		shouldRemoveGit := true
		if !flagYes {
			ok, err := askYesNoTUI("Remove .git/ directory? This will delete all local git history.", false)
			if err != nil {
				return err
			}
			shouldRemoveGit = ok
		}

		if shouldRemoveGit {
			fmt.Printf("   Removing %s/\n", gitPath)
			if err := os.RemoveAll(gitPath); err != nil {
				return fmt.Errorf("failed to remove %s: %w", gitPath, err)
			}
		} else {
			fmt.Println("   Skipping .git/ removal")
		}
	}

	// Check and remove .exohub directory
	exohubPath := ".exohub"
	if _, err := os.Stat(exohubPath); err == nil {
		// Confirm .exohub removal unless --yes flag is set
		shouldRemoveExohub := true
		if !flagYes {
			ok, err := askYesNoTUI("Remove .exohub/ directory? This will delete your configuration.", false)
			if err != nil {
				return err
			}
			shouldRemoveExohub = ok
		}

		if shouldRemoveExohub {
			fmt.Printf("   Removing %s/\n", exohubPath)
			if err := os.RemoveAll(exohubPath); err != nil {
				return fmt.Errorf("failed to remove %s: %w", exohubPath, err)
			}
		} else {
			fmt.Println("   Skipping .exohub/ removal")
		}
	}

	fmt.Println("✅ Reset complete. You can now run 'exo init' again.")
	return nil
}

// checkLegacyContext warns if a legacy .exohub/context file contains thin setting
func checkLegacyContext(exohubConfig *ExohubConfig) {
	data, err := os.ReadFile(filepath.Join(".exohub", "context"))
	if err != nil {
		return // No legacy context file
	}

	// Try to parse for thin field
	var legacy struct {
		Thin *bool `yaml:"thin,omitempty"`
	}
	if err := yaml.Unmarshal(data, &legacy); err != nil {
		return
	}

	if legacy.Thin != nil && *legacy.Thin {
		// Check if .exohub/config already has thin set
		if exohubConfig != nil && exohubConfig.Annex != nil && exohubConfig.Annex.Thin != nil && *exohubConfig.Annex.Thin {
			return // Already migrated
		}
		fmt.Printf("%s .exohub/context is deprecated. Move 'thin: true' to .exohub/config\n",
			WarningStyle.Render("⚠️  Warning:"))
	}
}

// handleRepoCreation creates a git repository via provider API
func handleRepoCreation(exohubConfig *ExohubConfig, remotesConfig *RemotesConfig, args []string) error {
	ctx := context.Background()

	fmt.Println("==> Repository Creation")

	// Resolve host, org, repoName from URL or context
	var host, org, repoName, explicitProvider string

	// Try positional URL argument first
	if len(args) > 0 {
		parsedHost, parsedOrg, parsedRepo, err := ParseRepoURL(args[0])
		if err != nil {
			return fmt.Errorf("failed to parse URL: %w", err)
		}
		host = parsedHost
		org = parsedOrg
		repoName = parsedRepo
	}

	// Try global context (--context flag or EXOHUB_CONTEXT env var)
	globalCtx, ctxErr := contextcmd.GetCurrentContext()
	if ctxErr == nil {
		// Context found — apply defaults for unset fields
		if host == "" {
			host = normalizeHostURL(globalCtx.Host)
		}
		if org == "" {
			org = globalCtx.Org
		}
		if explicitProvider == "" && globalCtx.Provider != "" {
			explicitProvider = globalCtx.Provider
		}
		if repoName == "" && globalCtx.Repo != "" {
			repoName = globalCtx.Repo
		}
	}

	// Validate we have host and org
	if host == "" || org == "" {
		return fmt.Errorf("cannot determine host and org: provide a URL argument or set --context / EXOHUB_CONTEXT")
	}

	// Repo name precedence: --name > URL-derived/context > cwd folder name
	if flagRepoName != "" {
		repoName = flagRepoName
	}
	if repoName == "" {
		repoName = getRepoNameFromCwd()
	}
	if repoName == "" {
		return fmt.Errorf("cannot determine repository name: use --name flag")
	}

	// Detect provider
	providerType, err := gitprovider.DetectProvider(ctx, host, explicitProvider)
	if err != nil {
		return fmt.Errorf("failed to detect provider: %w", err)
	}

	// Print summary and ask for confirmation
	repoURL := fmt.Sprintf("%s/%s/%s", strings.TrimSuffix(host, "/"), org, repoName)
	fmt.Printf("\n  Host:     %s\n", host)
	fmt.Printf("  Org:      %s\n", org)
	fmt.Printf("  Repo:     %s\n", repoName)
	fmt.Printf("  Provider: %s\n", string(providerType))
	fmt.Printf("  URL:      %s\n", repoURL)

	if !flagYes {
		ok, err := askYesNoTUI("Proceed?", false)
		if err != nil {
			return err
		}
		if !ok {
			fmt.Println("Aborted.")
			return nil
		}
	}

	// Create provider instance
	provider, err := gitprovider.NewProvider(providerType, host)
	if err != nil {
		return fmt.Errorf("failed to initialize provider: %w", err)
	}

	// Check if repository already exists
	fmt.Printf("\n🔍 Checking if repository exists...\n")
	exists, err := provider.RepositoryExists(ctx, org, repoName)
	if err != nil {
		return fmt.Errorf("failed to check repository: %w", err)
	}

	var repo *gitprovider.Repository
	created := false
	if exists {
		fmt.Printf("⚠️  Repository %s already exists\n", WarningStyle.Render(repoName))
		fmt.Println("Continuing with existing repository...")
		repo, err = provider.GetRepository(ctx, org, repoName)
		if err != nil {
			return fmt.Errorf("failed to get repository details: %w", err)
		}
	} else {
		// Create repository
		fmt.Printf("\n🚀 Creating repository %s/%s...\n", org, repoName)
		repo, err = provider.CreateRepository(ctx, gitprovider.CreateRepoOptions{
			Org:         org,
			Name:        repoName,
			Description: fmt.Sprintf("Created by exo CLI"),
			// Visibility unset: the provider picks its default — internal on
			// GitLab, private on GitHub and Gitea.
		})
		if err != nil {
			if errors.Is(err, gitprovider.ErrAlreadyExists) {
				fmt.Printf("⚠️  Repository %s already exists\n", WarningStyle.Render(repoName))
				fmt.Println("Continuing with existing repository...")
				repo, err = provider.GetRepository(ctx, org, repoName)
				if err != nil {
					return fmt.Errorf("failed to get repository details: %w", err)
				}
			} else {
				return fmt.Errorf("failed to create repository: %w", err)
			}
		} else {
			created = true
			fmt.Printf("✅ Repository created: %s\n", SuccessStyle.Render(repo.HTTPURL))
		}
	}

	// Initialize git repo locally if needed
	if !IsGitRepo() {
		fmt.Printf("\n📁 Initializing local git repository...\n")
		if err := ensureGitRepo(); err != nil {
			return fmt.Errorf("failed to initialize git repo: %w", err)
		}
	}

	// Initialize git-annex
	fmt.Printf("\n🔧 Initializing git-annex...\n")
	if err := ensureAnnexInitialized(); err != nil {
		return fmt.Errorf("failed to initialize git-annex: %w", err)
	}
	fmt.Printf("✅ git-annex initialized\n")

	// Configure annex defaults
	applyAnnexDefaults(exohubConfig)

	// Set origin remote (prefer SSH)
	remoteURL := repo.SSHURL
	if remoteURL == "" {
		remoteURL = repo.CloneURL
	}

	fmt.Printf("\n🔗 Setting remote origin to %s\n", remoteURL)
	cmd := command("git", "remote", "add", "origin", remoteURL)
	if err := cmd.Run(); err != nil {
		if err := runCommand([]string{"git", "remote", "set-url", "origin", remoteURL}); err != nil {
			fmt.Printf("⚠️  Could not set origin: %v\n", err)
		}
	}

	// If repo was just created, fetch and checkout the remote default branch
	pulledFromOrigin := false
	if created {
		if err := syncFromOriginDefaultBranch(true); err != nil {
			fmt.Printf("⚠️  Could not sync from origin: %v\n", err)
		} else {
			pulledFromOrigin = true
		}
		if pulledFromOrigin {
			if refreshed, err := LoadRemotesConfig(); err == nil && refreshed != nil {
				remotesConfig = refreshed
			} else if err != nil {
				fmt.Printf("⚠️  Could not reload .exohub/remotes after sync: %v\n", err)
			}
			// Reload exohub config in case template provided one
			if refreshedCfg, err := LoadExohubConfig(); err == nil && refreshedCfg != nil {
				exohubConfig = refreshedCfg
				applyAnnexDefaults(exohubConfig)
			}
		}
	}

	// Ensure there's an initial commit on main branch
	if err := ensureInitialCommit(pulledFromOrigin); err != nil {
		return fmt.Errorf("failed to create initial commit: %w", err)
	}

	// Configure git-annex remotes if specified
	if len(remotesConfig.Remotes) > 0 {
		fmt.Printf("\n==> Configuring %d git-annex remote(s)\n", len(remotesConfig.Remotes))

		_ = runCommand([]string{"git", "config", "annex.sshcaching", "true"})

		for _, remote := range remotesConfig.Remotes {
			fmt.Printf("  - %s (type: %s)\n", remote.Name, remote.Type)

			if remoteExists(remote.Name) {
				if !remoteTypeMatches(remote) {
					fmt.Printf("    ⚠️  Type mismatch - manual intervention required\n")
					continue
				}

				if needsReconfigure(remote) {
					fmt.Printf("    🔄 Configuration differs, reconfiguring...\n")
					if err := reconfigureRemote(remote); err != nil {
						fmt.Printf("    ❌ Failed to reconfigure: %v\n", err)
					} else {
						fmt.Printf("    ✅ Reconfigured\n")
					}
					continue
				}

				if remote.Type == "export" || remote.Type == "drive" {
					if err := ensureExportTrackingBranch(remote); err != nil {
						fmt.Printf("    ⚠️  Could not set tracking branch: %v\n", err)
					}
				}
				fmt.Printf("    ✅ Already configured\n")
				continue
			}

			if err := createRemoteFromConfig(remote); err != nil {
				fmt.Printf("    ❌ Failed: %v\n", err)
			} else {
				fmt.Printf("    ✅ Configured\n")
			}
		}
	}

	if err := createExamplePresetsIfNeeded(); err != nil {
		fmt.Printf("⚠️  Could not create example presets: %v\n", err)
	}

	if err := createDefaultBundleManifestIfNeeded(); err != nil {
		fmt.Printf("⚠️  Could not create bundle manifest: %v\n", err)
	}

	fmt.Printf("\n🎉 Repository initialization complete!\n")
	fmt.Printf("   SSH URL:   %s\n", CommandStyle.Render(remoteURL))
	fmt.Printf("   Web URL:   %s\n", CommandStyle.Render(repo.HTTPURL))

	return nil
}

func syncFromOriginDefaultBranch(force bool) error {
	// Avoid overwriting existing local work unless forced
	if !force && command("git", "rev-parse", "HEAD").Run() == nil {
		return nil
	}
	if !force {
		statusOut, err := command("git", "status", "--porcelain").Output()
		if err == nil && len(strings.TrimSpace(string(statusOut))) > 0 {
			return fmt.Errorf("working tree not clean; skipping origin sync")
		}
	}

	branch, err := getOriginDefaultBranch()
	if err != nil || branch == "" {
		return nil
	}

	if err := runCommand([]string{"git", "fetch", "origin", branch}); err != nil {
		return err
	}
	if force {
		_ = removeUntrackedGeneratedFiles()
	}
	return runCommand([]string{"git", "checkout", "-B", branch, "origin/" + branch})
}

func getOriginDefaultBranch() (string, error) {
	out, err := command("git", "ls-remote", "--symref", "origin", "HEAD").Output()
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "ref: ") && strings.Contains(line, "refs/heads/") {
			parts := strings.Split(line, "\t")
			if len(parts) > 0 {
				ref := strings.TrimPrefix(parts[0], "ref: ")
				return strings.TrimPrefix(ref, "refs/heads/"), nil
			}
		}
	}
	return "", nil
}

func removeUntrackedGeneratedFiles() error {
	statusOut, err := command("git", "status", "--porcelain").Output()
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(statusOut), "\n") {
		if !strings.HasPrefix(line, "?? ") {
			continue
		}
		path := strings.TrimSpace(strings.TrimPrefix(line, "?? "))
		if path == "" {
			continue
		}
		if path == "README.md" || strings.HasPrefix(path, ".exohub/") {
			_ = os.RemoveAll(path)
		}
	}
	return nil
}

// handleRemoteValidation checks if remotes are properly configured
// detectAndRenameRemotes checks if any remotes in the config have UUIDs that exist
// under different names in git-annex, and renames them to match the config
// Returns a set of remote names that were successfully renamed (to skip validation)
func detectAndRenameRemotes(remotesConfig *RemotesConfig) (map[string]bool, error) {
	// Build map of UUID -> current remote name in git-annex
	uuidToName, err := buildUUIDToNameMap()
	if err != nil {
		return nil, err
	}

	// Track successfully renamed remotes
	renamedRemotes := make(map[string]bool)

	// Check each remote in config for potential renames
	for _, remote := range remotesConfig.Remotes {
		// Only process remotes with UUIDs
		if remote.UUID == "" {
			continue
		}

		// Check if this UUID exists in git-annex
		currentName, exists := uuidToName[remote.UUID]
		if !exists {
			// UUID not found in git-annex - remote doesn't exist yet, skip
			continue
		}

		// Check if the current name matches the desired name
		if currentName == remote.Name {
			// Names match - no rename needed
			continue
		}

		// Names differ - perform rename
		fmt.Printf("🔄 Detected remote rename: %s → %s (UUID: %s)\n",
			WarningStyle.Render(currentName),
			SuccessStyle.Render(remote.Name),
			remote.UUID)

		// Ask for confirmation unless --yes flag is set
		if !flagYes {
			fmt.Println()
			fmt.Println("⚠️  Renaming a remote modifies git-annex metadata.")
			fmt.Println("   If the operation fails, your repository may be left in an inconsistent state.")
			fmt.Println()
			proceed, err := askYesNoTUI("Proceed with rename?", false)
			if err != nil {
				return nil, fmt.Errorf("failed to get confirmation: %w", err)
			}
			if !proceed {
				fmt.Printf("⏭️  Skipped renaming %s\n", currentName)
				continue
			}
		}

		fmt.Printf("🔄 Renaming remote %s → %s...\n", currentName, remote.Name)
		if err := runCommand([]string{"git", "annex", "renameremote", currentName, remote.Name}); err != nil {
			// Log error but continue with other remotes
			fmt.Printf("❌ Failed to rename remote: %v\n", err)
			fmt.Printf("   You may need to run: git annex renameremote %s %s\n", currentName, remote.Name)
			continue
		}

		// Clean up old git config entries to avoid duplicate UUID issues
		// git annex renameremote creates new config but doesn't remove old entries
		if err := command("git", "config", "--remove-section", "remote."+currentName).Run(); err != nil {
			// Not fatal if this fails - remote is already renamed in git-annex
			fmt.Printf("⚠️  Could not remove old git config for %s (remote still functional)\n", currentName)
		}

		// Enable the remote with its new name to populate git config
		// git annex renameremote only updates git-annex metadata, not local git config
		if err := runCommand([]string{"git", "annex", "enableremote", remote.Name}); err != nil {
			fmt.Printf("⚠️  Could not enable remote %s locally: %v\n", remote.Name, err)
			fmt.Printf("   Run: git annex enableremote %s\n", remote.Name)
		}

		// Mark as renamed so we skip validation check later
		renamedRemotes[remote.Name] = true
		fmt.Printf("✅ Remote renamed successfully\n")
	}

	return renamedRemotes, nil
}

// filterRemotesByName returns a new RemotesConfig containing only the remote
// matching the given name. If name is empty the original config is returned
// unchanged.
func filterRemotesByName(remotesConfig *RemotesConfig, name string) *RemotesConfig {
	if name == "" {
		return remotesConfig
	}
	for _, r := range remotesConfig.Remotes {
		if r.Name == name {
			return &RemotesConfig{Remotes: []RemoteConfig{r}}
		}
	}
	// Return empty config — handleRemoteValidation will print a clear message
	return &RemotesConfig{Remotes: nil}
}

func handleRemoteValidation(exohubConfig *ExohubConfig, remotesConfig *RemotesConfig, remoteFilter string) error {
	// Display clone preset information if available
	if err := displayClonePresetInfo(); err != nil {
		// Don't fail if we can't read preset info, just log warning
		fmt.Printf("⚠️  %v\n\n", err)
	}

	// Apply --remote filter before any processing
	if remoteFilter != "" {
		origLen := len(remotesConfig.Remotes)
		remotesConfig = filterRemotesByName(remotesConfig, remoteFilter)
		if len(remotesConfig.Remotes) == 0 {
			if origLen == 0 {
				return fmt.Errorf("no remotes defined in .exohub/remotes")
			}
			return fmt.Errorf("remote %q not found in .exohub/remotes", remoteFilter)
		}
		fmt.Printf("==> Configuring remote %q (filtered by --remote)\n", remoteFilter)
	} else {
		if len(remotesConfig.Remotes) == 0 {
			fmt.Println("No remotes defined in .exohub/remotes. Nothing to validate.")
			return nil
		}
		fmt.Printf("==> Validating %d remote(s) from .exohub/remotes\n", len(remotesConfig.Remotes))
	}

	// First validate all remote configurations
	var invalidConfigs []string
	for _, remote := range remotesConfig.Remotes {
		if err := remote.Validate(); err != nil {
			invalidConfigs = append(invalidConfigs, fmt.Sprintf("  - %s: %s", ErrorStyle.Render(remote.Name), err.Error()))
		}
	}

	if len(invalidConfigs) > 0 {
		fmt.Println("\n❌ Invalid remote configurations in .exohub/remotes:")
		for _, msg := range invalidConfigs {
			fmt.Println(msg)
		}
		return fmt.Errorf("fix configuration errors in .exohub/remotes")
	}

	// Then ensure git-annex is initialized (also fetches git-annex branch from origin first)
	if err := ensureAnnexInitialized(); err != nil {
		return fmt.Errorf("git-annex not initialized: %w", err)
	}

	// Configure annex defaults (drift-safe for already-initialized repos)
	applyAnnexDefaults(exohubConfig)

	// Detect and handle remote renames before validation
	renamedRemotes, err := detectAndRenameRemotes(remotesConfig)
	if err != nil {
		return fmt.Errorf("failed to process remote renames: %w", err)
	}

	var missingRemotes []RemoteConfig
	var needsReconfiguration []RemoteConfig
	var typeChanged []RemoteConfig

	for _, remote := range remotesConfig.Remotes {
		// Skip existence check for remotes we just renamed
		if renamedRemotes[remote.Name] {
			fmt.Printf("✅ Remote %s is correctly configured (renamed)\n", SuccessStyle.Render(remote.Name))
			continue
		}

		locallyConfigured := remoteExists(remote.Name)
		existsInAnnex := remoteExistsInAnnex(remote.Name)

		if !locallyConfigured && !existsInAnnex {
			fmt.Printf("❌ Remote %s not found\n", ErrorStyle.Render(remote.Name))
			missingRemotes = append(missingRemotes, remote)
			continue
		}

		// If remote exists in git-annex but not locally configured, treat as needs reconfiguration
		if !locallyConfigured && existsInAnnex {
			fmt.Printf("⚠️  Remote %s exists in git-annex but not configured locally\n", WarningStyle.Render(remote.Name))
			needsReconfiguration = append(needsReconfiguration, remote)
			continue
		}

		// Check if type changed (can't be reconfigured, needs deletion + recreation)
		if !remoteTypeMatches(remote) {
			fmt.Printf("⚠️  Remote %s type changed in .exohub/remotes (cannot be reconfigured)\n", WarningStyle.Render(remote.Name))
			typeChanged = append(typeChanged, remote)
			continue
		}

		// Check if configuration matches
		if needsReconfigure(remote) {
			fmt.Printf("⚠️  Remote %s configuration differs from .exohub/remotes\n", WarningStyle.Render(remote.Name))
			needsReconfiguration = append(needsReconfiguration, remote)
		} else {
			fmt.Printf("✅ Remote %s is correctly configured\n", SuccessStyle.Render(remote.Name))
			// Capture UUID even for correctly configured remotes (if not already stored)
			if remote.UUID == "" {
				if err := captureAndStoreUUID(remote.Name); err != nil {
					fmt.Printf("⚠️  Could not capture UUID: %v\n", err)
				}
			}
		}

		// Warn if git-annex has chunk config but .exohub/remotes doesn't
		if remote.Type == "annex" && remote.Chunk == "" {
			if chunkVal := remoteGetConfigValue(remote.Name, remote.UUID, "chunk"); chunkVal != "" {
				fmt.Printf("   ⚠️  git-annex has chunk=%s for %s but .exohub/remotes is missing chunk: field\n", chunkVal, WarningStyle.Render(remote.Name))
				fmt.Printf("   Add 'chunk: %s' to .exohub/remotes for correct bundle generation\n", chunkVal)
			}
		}
	}

	// Sync grants per-remote so each remote's _grants.json is co-located with its own prefix
	for _, r := range remotesConfig.Remotes {
		if r.Grants && r.S3URL != "" {
			if err := syncGrantsForRemote(r.S3URL); err != nil {
				fmt.Printf("⚠️ %v\n", err)
				return err
			}
		}
	}

	// Report remotes with type changes
	if len(typeChanged) > 0 {
		fmt.Println("\n⚠️  Remotes with type changes detected:")
		for _, remote := range typeChanged {
			fmt.Printf("  - %s: Cannot change type of existing remote\n", ErrorStyle.Render(remote.Name))
		}
		fmt.Println("\nTo fix, manually remove the old remote(s) and re-run 'exo init':")
		fmt.Println("(Note: This only removes metadata. Your data on S3/rsync remains safe,")
		fmt.Println(" but you'll need to manually recreate the remote to reconnect.)")
		for _, remote := range typeChanged {
			fmt.Printf("\n# Remove %s:\n", remote.Name)
			fmt.Println(CommandStyle.Render(fmt.Sprintf("git annex dead %s", remote.Name)))
			fmt.Println(CommandStyle.Render("git annex forget --drop-dead --force"))
			fmt.Println(CommandStyle.Render(fmt.Sprintf("git config --remove-section remote.%s", remote.Name)))
		}
		fmt.Println()
	}

	// Check for orphaned remotes (exist in git-annex but not in .exohub/remotes)
	// Skip this check when --remote filter is active to avoid false positives
	orphanedRemotes := []string(nil)
	if remoteFilter == "" {
		orphanedRemotes = detectOrphanedRemotes(remotesConfig)
	}
	if len(orphanedRemotes) > 0 {
		fmt.Println("\n💡 Remotes exist in git-annex but not in .exohub/remotes:")
		for _, name := range orphanedRemotes {
			fmt.Printf("  - %s\n", WarningStyle.Render(name))
		}
		fmt.Println("\nThese remotes are not managed by exo init.")
		fmt.Println("To remove them (only removes metadata, data remains safe but requires manual recreation to reconnect):")
		for _, name := range orphanedRemotes {
			fmt.Printf("\n# Remove %s:\n", name)
			fmt.Println(CommandStyle.Render(fmt.Sprintf("git annex dead %s", name)))
			fmt.Println(CommandStyle.Render("git annex forget --drop-dead --force"))
			fmt.Println(CommandStyle.Render(fmt.Sprintf("git config --remove-section remote.%s", name)))
		}
		fmt.Println()
	}

	// Reconfigure remotes that need it
	for _, remote := range needsReconfiguration {
		fmt.Printf("\n==> Reconfiguring remote %s\n", WarningStyle.Render(remote.Name))
		if err := reconfigureRemote(remote); errors.Is(err, errRemoteSkipped) {
			// Already printed skip message
		} else if err != nil {
			fmt.Printf("❌ Failed to reconfigure %s: %v\n", ErrorStyle.Render(remote.Name), err)
		} else {
			fmt.Printf("✅ Remote %s reconfigured successfully\n", SuccessStyle.Render(remote.Name))
			// Capture UUID after reconfiguration
			if err := captureAndStoreUUID(remote.Name); err != nil {
				fmt.Printf("⚠️  Could not capture UUID: %v\n", err)
			}
		}
	}

	// Create missing remotes
	if len(missingRemotes) > 0 {
		fmt.Printf("\n==> Creating %d missing remote(s)\n", len(missingRemotes))
		for _, remote := range missingRemotes {
			if err := createRemoteFromConfig(remote); errors.Is(err, errRemoteSkipped) {
				// Already printed skip message
			} else if err != nil {
				fmt.Printf("❌ Failed to create %s: %v\n", ErrorStyle.Render(remote.Name), err)
			} else {
				fmt.Printf("✅ Remote %s created successfully\n", SuccessStyle.Render(remote.Name))
			}
		}
	}

	// Create example presets file if it doesn't exist
	if err := createExamplePresetsIfNeeded(); err != nil {
		fmt.Printf("⚠️  Could not create example presets: %v\n", err)
	}

	// Create default bundle manifest if it doesn't exist
	if err := createDefaultBundleManifestIfNeeded(); err != nil {
		fmt.Printf("⚠️  Could not create bundle manifest: %v\n", err)
	}

	return nil
}

// createRemoteFromConfig creates a remote from its configuration
func createRemoteFromConfig(remote RemoteConfig) error {
	// Check binary availability
	if err := checkRemoteBinaries(remote.Type); err != nil {
		return err
	}

	switch remote.Type {
	case "annex":
		return createAnnexRemoteFromConfig(remote)
	case "export":
		return createExportRemoteFromConfig(remote)
	case "import":
		return createImportRemoteFromConfig(remote)
	case "exospace":
		return createExospaceRemoteFromConfig(remote)
	case "artifactdb":
		return createArtifactDBRemoteFromConfig(remote)
	case "drive":
		return createDriveRemoteFromConfig(remote)
	default:
		return fmt.Errorf("unsupported remote type: %s", remote.Type)
	}
}

// enableOrInitRemote tries to enable an existing remote first, then falls back to initremote.
// If a UUID is provided (from .exohub/remotes), it will NEVER create a new remote - it will
// only try to enable the existing one by UUID. This prevents creating duplicate remotes.
func enableOrInitRemote(name, uuid string, initArgs []string) error {
	// Build enableremote args
	// If UUID is provided, use it instead of name to ensure we enable the correct remote
	var enableArgs []string
	if uuid != "" {
		// Use UUID to enable - this ensures we get the exact remote we want
		enableArgs = []string{"git", "annex", "enableremote", uuid, "name=" + name}
	} else {
		enableArgs = []string{"git", "annex", "enableremote", name}
	}

	// Extract config parameters from initArgs (skip "git", "annex", "initremote", name)
	for i := 4; i < len(initArgs); i++ {
		arg := initArgs[i]
		// Skip encryption=none, type=, and externaltype= as they're only for initremote
		if arg != "encryption=none" && !strings.HasPrefix(arg, "type=") && !strings.HasPrefix(arg, "externaltype=") {
			enableArgs = append(enableArgs, arg)
		}
	}

	// Try enableremote first (silently)
	cmd := command(enableArgs[0], enableArgs[1:]...)
	if err := cmd.Run(); err == nil {
		fmt.Printf("Enabled existing remote %s\n", name)
		return nil
	}

	// If UUID was specified, we should NOT create a new remote - that would be wrong
	if uuid != "" {
		return fmt.Errorf("remote %s with UUID %s not found in git-annex; cannot enable", name, uuid)
	}

	// If enableremote failed and no UUID specified, the remote doesn't exist, so use initremote
	fmt.Printf("Initializing new remote %s\n", name)
	return runCommand(initArgs)
}

// captureAndStoreUUID gets the UUID of a remote and updates the .exohub/remotes config
func captureAndStoreUUID(remoteName string) error {
	uuid := getRemoteUUID(remoteName)
	if uuid == "" {
		// UUID not found, but this is not critical
		return nil
	}

	// Load current remotes config
	remotesConfig, err := LoadRemotesConfig()
	if err != nil {
		return err
	}

	// Update the UUID for this remote
	for i := range remotesConfig.Remotes {
		if remotesConfig.Remotes[i].Name == remoteName {
			if remotesConfig.Remotes[i].UUID != uuid {
				remotesConfig.Remotes[i].UUID = uuid
				// Save the updated config
				if err := SaveRemotesConfig(remotesConfig); err != nil {
					fmt.Printf("⚠️  Could not save UUID to .exohub/remotes: %v\n", err)
				} else {
					fmt.Printf("📝 Saved UUID %s to .exohub/remotes\n", uuid)
				}
			}
			break
		}
	}

	return nil
}

func createAnnexRemoteFromConfig(remote RemoteConfig) error {
	chunk := remote.Chunk
	if chunk == "" {
		chunk = "1GiB"
	}

	fmt.Printf("Setting up %s with chunk=%s via s5cmd external remote\n", remote.Name, chunk)
	initArgs := []string{
		"git", "annex", "initremote", remote.Name,
		"type=external", "externaltype=s5cmd", "encryption=none",
		"s3url=" + remote.S3URL, "chunk=" + chunk,
	}
	if remote.Grants {
		initArgs = append(initArgs, "grants=true")
	}

	if err := enableOrInitRemote(remote.Name, remote.UUID, initArgs); err != nil {
		return err
	}

	// Capture and store UUID
	if err := captureAndStoreUUID(remote.Name); err != nil {
		fmt.Printf("⚠️  Could not capture UUID: %v\n", err)
	}

	// Set preferred content if include/exclude patterns are specified
	if err := setPreferredContentFromPatterns(remote.Name, remote.Include, remote.Exclude); err != nil {
		return err
	}

	return nil
}

func createExportRemoteFromConfig(remote RemoteConfig) error {
	if remote.Grants {
		// Export remotes require write access. Skip if user only has READ grants.
		if !hasGrantsWriteAccess(remote.S3URL) {
			fmt.Printf("Skipping export remote '%s' (read-only access)\n", remote.Name)
			return errRemoteSkipped
		}
	}

	fmt.Printf("Setting up %s as exporttree via s5cmd external remote\n", remote.Name)
	initArgs := []string{
		"git", "annex", "initremote", remote.Name,
		"type=external", "externaltype=s5cmd", "encryption=none",
		"s3url=" + remote.S3URL, "exporttree=yes",
	}
	if remote.Grants {
		initArgs = append(initArgs, "grants=true")
	}

	if err := enableOrInitRemote(remote.Name, remote.UUID, initArgs); err != nil {
		return err
	}

	// Enable remote again to ensure settings are applied (for both new and existing remotes)
	enableArgs := []string{"git", "annex", "enableremote", remote.Name, "s3url=" + remote.S3URL, "exporttree=yes"}
	if remote.Grants {
		enableArgs = append(enableArgs, "grants=true")
	}
	_ = runCommand(enableArgs)

	// Capture and store UUID
	if err := captureAndStoreUUID(remote.Name); err != nil {
		fmt.Printf("⚠️  Could not capture UUID: %v\n", err)
	}

	// Set tracking branch (required for export remotes)
	trackingBranch := remote.TrackingBranch
	if trackingBranch == "" {
		// Default to current branch or main
		out, err := command("git", "branch", "--show-current").Output()
		if err == nil && len(out) > 0 {
			trackingBranch = strings.TrimSpace(string(out))
		} else {
			trackingBranch = "main"
		}
	}
	_ = runCommand([]string{"git", "config", fmt.Sprintf("remote.%s.annex-tracking-branch", remote.Name), trackingBranch})
	fmt.Printf("Set remote.%s.annex-tracking-branch to '%s'\n", remote.Name, trackingBranch)

	ensureRemoteConfigS3URL(remote.Name, remote.S3URL)

	// Set preferred content if include/exclude patterns are specified
	if err := setPreferredContentFromPatterns(remote.Name, remote.Include, remote.Exclude); err != nil {
		return err
	}

	return nil
}

func createArtifactDBRemoteFromConfig(remote RemoteConfig) error {
	if remote.Grants {
		// Export remotes require write access. Skip if user only has READ grants.
		if !hasGrantsWriteAccess(remote.S3URL) {
			fmt.Printf("Skipping export remote '%s' (read-only access)\n", remote.Name)
			return errRemoteSkipped
		}
	}

	externalType := fmt.Sprintf("%s-%s", remote.Type, remote.Mode)
	fmt.Printf("Setting up %s as exporttree via %s external remote\n", remote.Name, externalType)
	initArgs := []string{
		"git", "annex", "initremote", remote.Name,
		"type=external", "externaltype=" + externalType, "encryption=none",
		"s3url=" + remote.S3URL, "exporttree=yes",
	}
	if remote.Grants {
		initArgs = append(initArgs, "grants=true")
	}
	if remote.InstanceURL != "" {
		initArgs = append(initArgs, "instance_url="+remote.InstanceURL)
	}

	if err := enableOrInitRemote(remote.Name, remote.UUID, initArgs); err != nil {
		return err
	}

	// Capture and store UUID
	if err := captureAndStoreUUID(remote.Name); err != nil {
		fmt.Printf("⚠️  Could not capture UUID: %v\n", err)
	}

	// Set tracking branch
	trackingBranch := remote.TrackingBranch
	if trackingBranch == "" {
		out, err := command("git", "branch", "--show-current").Output()
		if err == nil && len(out) > 0 {
			trackingBranch = strings.TrimSpace(string(out))
		} else {
			trackingBranch = "main"
		}
	}
	_ = runCommand([]string{"git", "config", fmt.Sprintf("remote.%s.annex-tracking-branch", remote.Name), trackingBranch})
	fmt.Printf("Set remote.%s.annex-tracking-branch to '%s'\n", remote.Name, trackingBranch)

	ensureRemoteConfigS3URL(remote.Name, remote.S3URL)

	// Set preferred content: only export .exohub/bundles/ and .artifactdb/
	wanted := "include=.exohub/bundles/* or include=.artifactdb/*"
	_ = runCommand([]string{"git", "annex", "wanted", remote.Name, wanted})
	fmt.Printf("Set preferred content for '%s': %s\n", remote.Name, wanted)

	return nil
}

func reconfigureArtifactDBRemote(remote RemoteConfig) error {
	if remote.Grants {
		if !hasGrantsWriteAccess(remote.S3URL) {
			fmt.Printf("Skipping export remote '%s' (read-only access)\n", remote.Name)
			return errRemoteSkipped
		}
	}

	enableArgs := []string{"git", "annex", "enableremote", remote.Name, "s3url=" + remote.S3URL, "exporttree=yes"}
	if remote.Grants {
		enableArgs = append(enableArgs, "grants=true")
	}
	if remote.InstanceURL != "" {
		enableArgs = append(enableArgs, "instance_url="+remote.InstanceURL)
	}
	_ = runCommand(enableArgs)

	ensureRemoteConfigS3URL(remote.Name, remote.S3URL)

	// Ensure preferred content is set
	wanted := "include=.exohub/bundles/* or include=.artifactdb/*"
	_ = runCommand([]string{"git", "annex", "wanted", remote.Name, wanted})

	return nil
}

func createImportRemoteFromConfig(remote RemoteConfig) error {
	// Ensure AWS credentials are available
	if err := ensureAWSCredentials(); err != nil {
		return fmt.Errorf("AWS credentials not available: %w", err)
	}

	datacenter := remote.Datacenter
	if datacenter == "" {
		datacenter = "us-west-2"
	}

	fmt.Printf("Setting up %s as S3 import remote (bucket: %s, prefix: %s)\n", remote.Name, remote.Bucket, remote.Prefix)

	protocol := "https"
	if remote.Protocol != "" {
		protocol = remote.Protocol
	}

	// Build base args for both versioning attempts
	baseArgs := []string{
		"git", "annex", "initremote", remote.Name,
		"type=S3",
		"bucket=" + remote.Bucket,
		"encryption=none",
		"protocol=" + protocol,
		"importtree=yes",
		"datacenter=" + datacenter,
	}
	if remote.Prefix != "" {
		baseArgs = append(baseArgs, "fileprefix="+remote.Prefix)
	}
	if remote.Host != "" {
		baseArgs = append(baseArgs, "host="+remote.Host)
	}
	if remote.Port != "" {
		baseArgs = append(baseArgs, "port="+remote.Port)
	}

	// Try enableremote first - use UUID if available
	var enableArgs []string
	if remote.UUID != "" {
		// Use UUID to enable - this ensures we get the exact remote we want
		enableArgs = []string{"git", "annex", "enableremote", remote.UUID, "name=" + remote.Name, "importtree=yes"}
	} else {
		enableArgs = []string{"git", "annex", "enableremote", remote.Name, "importtree=yes"}
	}
	cmd := command(enableArgs[0], enableArgs[1:]...)
	if err := cmd.Run(); err == nil {
		fmt.Printf("Enabled existing remote %s\n", remote.Name)
		// Capture and store UUID
		if err := captureAndStoreUUID(remote.Name); err != nil {
			fmt.Printf("⚠️  Could not capture UUID: %v\n", err)
		}
		// Set tracking branch if specified
		if remote.TrackingBranch != "" {
			_ = runCommand([]string{"git", "config", fmt.Sprintf("remote.%s.annex-tracking-branch", remote.Name), remote.TrackingBranch})
			fmt.Printf("Set remote.%s.annex-tracking-branch to '%s'\n", remote.Name, remote.TrackingBranch)
		}
		// Set preferred content if include/exclude patterns are specified
		if err := setPreferredContentFromPatterns(remote.Name, remote.Include, remote.Exclude); err != nil {
			return err
		}
		return nil
	}

	// If UUID was specified, we should NOT create a new remote - that would be wrong
	if remote.UUID != "" {
		return fmt.Errorf("remote %s with UUID %s not found in git-annex; cannot enable", remote.Name, remote.UUID)
	}

	// Remote doesn't exist, initialize with versioning=yes first
	fmt.Printf("Initializing new remote %s\n", remote.Name)
	args := append(baseArgs, "versioning=yes")

	// Run command and capture stderr to check for versioning errors
	cmd = command(args[0], args[1:]...)
	cmd.Stdout = os.Stdout
	var stderrBuf strings.Builder
	cmd.Stderr = io.MultiWriter(os.Stderr, &stderrBuf)
	cmd.Stdin = os.Stdin
	err := cmd.Run()

	if err != nil && (strings.Contains(stderrBuf.String(), "403") ||
		strings.Contains(stderrBuf.String(), "Forbidden") ||
		strings.Contains(stderrBuf.String(), "GetBucketVersioning") ||
		strings.Contains(stderrBuf.String(), "PutBucketVersioning")) {
		// Retry without versioning if we get 403 (permission denied) or versioning errors
		fmt.Println("⚠️  Versioning not accessible (403), retrying without versioning...")
		if err = runCommand(baseArgs); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	// Capture and store UUID
	if err := captureAndStoreUUID(remote.Name); err != nil {
		fmt.Printf("⚠️  Could not capture UUID: %v\n", err)
	}

	// Set tracking branch if specified
	if remote.TrackingBranch != "" {
		_ = runCommand([]string{"git", "config", fmt.Sprintf("remote.%s.annex-tracking-branch", remote.Name), remote.TrackingBranch})
		fmt.Printf("Set remote.%s.annex-tracking-branch to '%s'\n", remote.Name, remote.TrackingBranch)
	}

	// Set preferred content if include/exclude patterns are specified
	if err := setPreferredContentFromPatterns(remote.Name, remote.Include, remote.Exclude); err != nil {
		return err
	}

	return nil
}

func createExospaceRemoteFromConfig(remote RemoteConfig) error {
	fmt.Printf("Setting up %s as rsync special remote\n", remote.Name)
	initArgs := []string{
		"git", "annex", "initremote", remote.Name,
		"type=rsync", "rsyncurl=" + remote.RsyncURL,
		"encryption=none",
	}

	if err := enableOrInitRemote(remote.Name, remote.UUID, initArgs); err != nil {
		return err
	}

	// Capture and store UUID
	if err := captureAndStoreUUID(remote.Name); err != nil {
		fmt.Printf("⚠️  Could not capture UUID: %v\n", err)
	}

	// Set rsync options if permissions or rsync_options are specified
	rsyncOpts := getExpectedRsyncOptions(remote)
	if rsyncOpts != "" {
		if err := setRsyncOptions(remote.Name, rsyncOpts); err != nil {
			return fmt.Errorf("failed to set rsync options: %w", err)
		}
		fmt.Printf("Set remote.%s.annex-rsync-options to '%s'\n", remote.Name, rsyncOpts)
	}

	// Set preferred content if include/exclude patterns are specified
	if err := setPreferredContentFromPatterns(remote.Name, remote.Include, remote.Exclude); err != nil {
		return err
	}

	return nil
}

// driveTokenPath returns the path where the OAuth token for a drive remote is stored
func driveTokenPath(name string) string {
	exoDir, err := configdir.ExoConfigDir()
	if err != nil {
		exoDir = filepath.Join(os.Getenv("HOME"), ".config", "exo")
	}
	return filepath.Join(exoDir, "drive-tokens", name+".json")
}

func createDriveRemoteFromConfig(remote RemoteConfig) error {
	fmt.Printf("Setting up %s as exporttree via drive external remote\n", remote.Name)
	initArgs := []string{
		"git", "annex", "initremote", remote.Name,
		"type=external", "externaltype=drive", "encryption=none",
		"exporttree=yes", "drive_path=" + remote.DrivePath,
	}
	if len(remote.Exclude) > 0 {
		initArgs = append(initArgs, "exclude="+strings.Join(remote.Exclude, ","))
	}

	if err := enableOrInitRemote(remote.Name, remote.UUID, initArgs); err != nil {
		return err
	}

	// Capture and store UUID
	if err := captureAndStoreUUID(remote.Name); err != nil {
		fmt.Printf("⚠️  Could not capture UUID: %v\n", err)
	}

	// Set tracking branch (required for export remotes)
	trackingBranch := remote.TrackingBranch
	if trackingBranch == "" {
		// Default to current branch or main
		out, err := command("git", "branch", "--show-current").Output()
		if err == nil && len(out) > 0 {
			trackingBranch = strings.TrimSpace(string(out))
		} else {
			trackingBranch = "main"
		}
	}
	_ = runCommand([]string{"git", "config", fmt.Sprintf("remote.%s.annex-tracking-branch", remote.Name), trackingBranch})
	fmt.Printf("Set remote.%s.annex-tracking-branch to '%s'\n", remote.Name, trackingBranch)

	// Set preferred content if include/exclude patterns are specified
	if err := setPreferredContentFromPatterns(remote.Name, remote.Include, remote.Exclude); err != nil {
		return err
	}

	return nil
}

// buildPreferredContentExpression builds a preferred content expression from include/exclude patterns
func buildPreferredContentExpression(include, exclude []string) string {
	if len(include) == 0 && len(exclude) == 0 {
		return ""
	}

	var parts []string

	// Add include patterns (OR'd together)
	if len(include) > 0 {
		var includeParts []string
		for _, pattern := range include {
			// Normalize directory patterns: "foo/" -> "foo/**"
			if strings.HasSuffix(pattern, "/") {
				pattern = pattern + "**"
			}
			includeParts = append(includeParts, fmt.Sprintf("include=%s", pattern))
		}
		if len(includeParts) == 1 {
			parts = append(parts, includeParts[0])
		} else {
			parts = append(parts, "("+strings.Join(includeParts, " or ")+")")
		}
	}

	// Add exclude patterns (exclude already means "not wanted" in git-annex)
	if len(exclude) > 0 {
		for _, pattern := range exclude {
			// Normalize directory patterns: "foo/" -> "foo/**"
			if strings.HasSuffix(pattern, "/") {
				pattern = pattern + "**"
			}
			parts = append(parts, fmt.Sprintf("exclude=%s", pattern))
		}
	}

	// Combine with AND
	return strings.Join(parts, " and ")
}

// setPreferredContentFromPatterns builds a preferred content expression from include/exclude patterns
// and sets it on the remote using git annex wanted
func setPreferredContentFromPatterns(remoteName string, include, exclude []string) error {
	expr := buildPreferredContentExpression(include, exclude)
	if expr == "" {
		// Clear the wanted rules by setting to "anything" so all content syncs
		fmt.Printf("Clearing preferred content for '%s' (setting to anything)\n", remoteName)
		return runCommand([]string{"git", "annex", "wanted", remoteName, "anything"})
	}

	fmt.Printf("Setting preferred content for '%s': %s\n", remoteName, expr)
	return runCommand([]string{"git", "annex", "wanted", remoteName, expr})
}

// needsPreferredContentUpdate checks if the preferred content needs to be updated
func needsPreferredContentUpdate(remoteName string, include, exclude []string) bool {
	expectedExpr := buildPreferredContentExpression(include, exclude)

	// Get current preferred content
	output, err := command("git", "annex", "wanted", remoteName).Output()
	if err != nil {
		return expectedExpr != ""
	}

	currentExpr := strings.TrimSpace(string(output))

	// If expected is empty but current is "anything", no update needed
	if expectedExpr == "" && currentExpr == "anything" {
		return false
	}

	// If expected is empty, we need to clear it (unless already "anything")
	if expectedExpr == "" {
		return currentExpr != "anything"
	}

	// Otherwise check if they match
	return currentExpr != expectedExpr
}

// needsReconfigure checks if a remote's configuration differs from .exohub/remotes
// NOTE: This assumes the remote type already matches (checked separately)
func needsReconfigure(remote RemoteConfig) bool {
	switch remote.Type {
	case "annex", "export":
		currentS3URL := getRemoteConfig(remote.Name, "s3url")
		if currentS3URL == "" {
			currentS3URL = getRemoteInfoS3URL(remote.Name)
		}
		if currentS3URL != remote.S3URL {
			return true
		}
		// Check if grants setting changed
		currentGrants := remoteHasConfigValue(remote.Name, remote.UUID, "grants=true")
		if currentGrants != remote.Grants {
			return true
		}
		if remote.Type == "export" {
			currentTB := getGitConfig(fmt.Sprintf("remote.%s.annex-tracking-branch", remote.Name))
			// Check if tracking branch is missing or different
			if currentTB == "" {
				return true // Tracking branch is required for export remotes
			}
			if remote.TrackingBranch != "" && currentTB != remote.TrackingBranch {
				return true // Explicitly specified tracking branch differs
			}
		}
	case "exospace":
		// For rsync remotes, check remote.<name>.annex-rsyncurl
		currentRsyncURL := getGitConfig(fmt.Sprintf("remote.%s.annex-rsyncurl", remote.Name))
		if currentRsyncURL != remote.RsyncURL {
			return true
		}
		// Check if rsync options need updating (permissions or rsync_options fields)
		currentRsyncOpts := getGitConfig(fmt.Sprintf("remote.%s.annex-rsync-options", remote.Name))
		expectedRsyncOpts := getExpectedRsyncOptions(remote)
		if currentRsyncOpts != expectedRsyncOpts {
			return true
		}
	case "import":
		// For import remotes, check tracking branch
		currentTB := getGitConfig(fmt.Sprintf("remote.%s.annex-tracking-branch", remote.Name))
		// Check if tracking branch is missing or different
		if currentTB == "" {
			return true // Tracking branch is beneficial for import remotes
		}
		if remote.TrackingBranch != "" && currentTB != remote.TrackingBranch {
			return true // Explicitly specified tracking branch differs
		}
		// Check bucket/prefix drift from remote.log
		if remote.Bucket != "" {
			currentBucket := remoteGetConfigValue(remote.Name, remote.UUID, "bucket")
			if currentBucket != "" && currentBucket != remote.Bucket {
				return true
			}
		}
		if remote.Prefix != "" {
			currentPrefix := remoteGetConfigValue(remote.Name, remote.UUID, "fileprefix")
			if currentPrefix != "" && currentPrefix != remote.Prefix {
				return true
			}
		}
		if remote.Datacenter != "" {
			currentDC := remoteGetConfigValue(remote.Name, remote.UUID, "datacenter")
			if currentDC != "" && currentDC != remote.Datacenter {
				return true
			}
		}
		if remote.Host != "" {
			currentHost := remoteGetConfigValue(remote.Name, remote.UUID, "host")
			if currentHost != "" && currentHost != remote.Host {
				return true
			}
		}
		if remote.Port != "" {
			currentPort := remoteGetConfigValue(remote.Name, remote.UUID, "port")
			if currentPort != "" && currentPort != remote.Port {
				return true
			}
		}
		if remote.Protocol != "" {
			currentProtocol := remoteGetConfigValue(remote.Name, remote.UUID, "protocol")
			if currentProtocol != "" && currentProtocol != remote.Protocol {
				return true
			}
		}
	case "artifactdb":
		// Check s3url from git config (avoid git annex info which starts the remote binary)
		currentS3URL := getRemoteConfig(remote.Name, "s3url")
		if currentS3URL != remote.S3URL {
			return true
		}
		currentGrants := remoteHasConfigValue(remote.Name, remote.UUID, "grants=true")
		if currentGrants != remote.Grants {
			return true
		}
		// Check hardcoded preferred content for artifactdb remotes
		expectedWanted := "include=.exohub/bundles/* or include=.artifactdb/*"
		wantedOut, _ := command("git", "annex", "wanted", remote.Name).Output()
		if strings.TrimSpace(string(wantedOut)) != expectedWanted {
			return true
		}
	case "drive":
		// Check drive_path from git-annex remote.log
		currentDrivePath := remoteGetConfigValue(remote.Name, remote.UUID, "drive_path")
		if currentDrivePath != "" && currentDrivePath != remote.DrivePath {
			return true
		}
		// Check tracking branch
		currentTB := getGitConfig(fmt.Sprintf("remote.%s.annex-tracking-branch", remote.Name))
		if currentTB == "" {
			return true
		}
		if remote.TrackingBranch != "" && currentTB != remote.TrackingBranch {
			return true
		}
	}

	// Check if preferred content needs updating (for annex, import, export, exospace, drive)
	if remote.Type == "annex" || remote.Type == "import" || remote.Type == "export" || remote.Type == "exospace" || remote.Type == "drive" {
		if needsPreferredContentUpdate(remote.Name, remote.Include, remote.Exclude) {
			return true
		}
	}

	return false
}

// remoteTypeMatches checks if the actual remote type matches the expected type
func remoteTypeMatches(remote RemoteConfig) bool {
	switch remote.Type {
	case "export":
		// Export remotes should have exporttree=yes in git-annex remote.log
		return remoteHasExportTree(remote.Name, remote.UUID)
	case "import":
		// Import remotes should have importtree=yes in git-annex remote.log
		return remoteHasImportTree(remote.Name, remote.UUID)
	case "annex":
		// Annex remotes should NOT have exporttree=yes or importtree=yes
		return !remoteHasExportTree(remote.Name, remote.UUID) && !remoteHasImportTree(remote.Name, remote.UUID)
	case "exospace":
		// Exospace uses rsync type, check git-annex remote.log for type=rsync
		return remoteHasGitAnnexType(remote.Name, remote.UUID, "rsync")
	case "artifactdb":
		// ArtifactDB remotes use exporttree=yes with external type
		return remoteHasExportTree(remote.Name, remote.UUID)
	case "drive":
		// Drive remotes use exporttree=yes with external type
		return remoteHasExportTree(remote.Name, remote.UUID)
	}
	return true
}

// remoteHasExportTree checks if a remote has exporttree=yes in git-annex remote.log
// Matches by UUID if provided, otherwise by name (for backwards compatibility)
func remoteHasExportTree(name string, uuid string) bool {
	// Check git-annex remote.log for exporttree=yes
	output, err := command("git", "show", "git-annex:remote.log").Output()
	if err != nil {
		return false
	}

	// Match by UUID first (handles renamed remotes), fall back to name
	for _, line := range strings.Split(string(output), "\n") {
		// Check if this line matches our remote
		matchesRemote := false
		if uuid != "" && strings.HasPrefix(line, uuid+" ") {
			matchesRemote = true
		} else if strings.Contains(line, fmt.Sprintf("name=%s", name)) {
			matchesRemote = true
		}

		if matchesRemote && strings.Contains(line, "exporttree=yes") {
			return true
		}
	}
	return false
}

// remoteHasImportTree checks if a remote has importtree=yes in git-annex remote.log
// Matches by UUID if provided, otherwise by name (for backwards compatibility)
func remoteHasImportTree(name string, uuid string) bool {
	// Check git-annex remote.log for importtree=yes
	output, err := command("git", "show", "git-annex:remote.log").Output()
	if err != nil {
		return false
	}

	// Match by UUID first (handles renamed remotes), fall back to name
	for _, line := range strings.Split(string(output), "\n") {
		// Check if this line matches our remote
		matchesRemote := false
		if uuid != "" && strings.HasPrefix(line, uuid+" ") {
			matchesRemote = true
		} else if strings.Contains(line, fmt.Sprintf("name=%s", name)) {
			matchesRemote = true
		}

		if matchesRemote && strings.Contains(line, "importtree=yes") {
			return true
		}
	}
	return false
}

// remoteHasGitAnnexType checks if a remote has a specific git-annex type in remote.log
// Matches by UUID if provided, otherwise by name (for backwards compatibility)
func remoteHasGitAnnexType(name string, uuid string, expectedType string) bool {
	// Check git-annex remote.log for type=<expectedType>
	output, err := command("git", "show", "git-annex:remote.log").Output()
	if err != nil {
		return false
	}

	// Match by UUID first (handles renamed remotes), fall back to name
	// Format: "<uuid> key=value key=value ... timestamp=<time>s"
	expectedTypeStr := fmt.Sprintf("type=%s", expectedType)
	for _, line := range strings.Split(string(output), "\n") {
		// Check if this line matches our remote
		matchesRemote := false
		if uuid != "" && strings.HasPrefix(line, uuid+" ") {
			matchesRemote = true
		} else if strings.Contains(line, fmt.Sprintf("name=%s", name)) {
			matchesRemote = true
		}

		if matchesRemote && strings.Contains(line, expectedTypeStr) {
			return true
		}
	}
	return false
}

// remoteHasConfigValue checks if a remote has a specific key=value in git-annex remote.log
func remoteHasConfigValue(name, uuid, keyValue string) bool {
	output, err := command("git", "show", "git-annex:remote.log").Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(output), "\n") {
		matchesRemote := false
		if uuid != "" && strings.HasPrefix(line, uuid+" ") {
			matchesRemote = true
		} else if strings.Contains(line, fmt.Sprintf("name=%s", name)) {
			matchesRemote = true
		}
		if matchesRemote && strings.Contains(line, keyValue) {
			return true
		}
	}
	return false
}

// decodeGitAnnexValue decodes git-annex's URL-style encoding in remote.log values.
// git-annex encodes special characters as &NN; where NN is the decimal ASCII code.
// For example, spaces become &32; and ampersands become &38;
func decodeGitAnnexValue(s string) string {
	var result strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '&' {
			end := strings.IndexByte(s[i:], ';')
			if end > 1 {
				code := s[i+1 : i+end]
				var n int
				if _, err := fmt.Sscanf(code, "%d", &n); err == nil && n > 0 && n < 256 {
					result.WriteByte(byte(n))
					i += end + 1
					continue
				}
			}
		}
		result.WriteByte(s[i])
		i++
	}
	return result.String()
}

// remoteGetConfigValue extracts the value for a given key from git-annex remote.log.
// Returns empty string if not found.
func remoteGetConfigValue(name, uuid, key string) string {
	output, err := command("git", "show", "git-annex:remote.log").Output()
	if err != nil {
		return ""
	}
	prefix := key + "="
	for _, line := range strings.Split(string(output), "\n") {
		matchesRemote := false
		if uuid != "" && strings.HasPrefix(line, uuid+" ") {
			matchesRemote = true
		} else if strings.Contains(line, fmt.Sprintf("name=%s", name)) {
			matchesRemote = true
		}
		if !matchesRemote {
			continue
		}
		for _, field := range strings.Fields(line) {
			if strings.HasPrefix(field, prefix) {
				return decodeGitAnnexValue(strings.TrimPrefix(field, prefix))
			}
		}
	}
	return ""
}

// reconfigureRemote updates a remote's configuration
func reconfigureRemote(remote RemoteConfig) error {
	switch remote.Type {
	case "annex":
		return reconfigureAnnexRemote(remote)
	case "export":
		return reconfigureExportRemote(remote)
	case "import":
		return reconfigureImportRemote(remote)
	case "exospace":
		return reconfigureExospaceRemote(remote)
	case "artifactdb":
		return reconfigureArtifactDBRemote(remote)
	case "drive":
		return reconfigureDriveRemote(remote)
	}
	return fmt.Errorf("unsupported remote type: %s", remote.Type)
}

func reconfigureAnnexRemote(remote RemoteConfig) error {

	args := []string{"git", "annex", "enableremote", remote.Name}
	if remote.S3URL != "" {
		args = append(args, "s3url="+remote.S3URL)
	}
	if remote.Chunk != "" {
		args = append(args, "chunk="+remote.Chunk)
	}
	if remote.Grants {
		args = append(args, "grants=true")
	} else {
		args = append(args, "grants=false")
	}

	if err := runCommand(args); err != nil {
		return err
	}

	// Persist s3url in annex-remote config
	ensureRemoteConfigS3URL(remote.Name, remote.S3URL)

	// Set preferred content if include/exclude patterns are specified
	if err := setPreferredContentFromPatterns(remote.Name, remote.Include, remote.Exclude); err != nil {
		return err
	}

	return nil
}

func reconfigureExportRemote(remote RemoteConfig) error {
	if remote.Grants {
		if !hasGrantsWriteAccess(remote.S3URL) {
			fmt.Printf("Skipping export remote '%s' (read-only access)\n", remote.Name)
			return errRemoteSkipped
		}
	}

	args := []string{"git", "annex", "enableremote", remote.Name}
	if remote.S3URL != "" {
		args = append(args, "s3url="+remote.S3URL)
	}
	args = append(args, "exporttree=yes")
	if remote.Grants {
		args = append(args, "grants=true")
	} else {
		args = append(args, "grants=false")
	}

	if err := runCommand(args); err != nil {
		return err
	}

	// Update tracking branch (with fallback to current branch or main)
	trackingBranch := remote.TrackingBranch
	if trackingBranch == "" {
		// Default to current branch or main
		out, err := command("git", "branch", "--show-current").Output()
		if err == nil && len(out) > 0 {
			trackingBranch = strings.TrimSpace(string(out))
		} else {
			trackingBranch = "main"
		}
	}
	_ = runCommand([]string{"git", "config", fmt.Sprintf("remote.%s.annex-tracking-branch", remote.Name), trackingBranch})
	fmt.Printf("Set remote.%s.annex-tracking-branch to '%s'\n", remote.Name, trackingBranch)

	// Persist s3url in annex-remote config
	ensureRemoteConfigS3URL(remote.Name, remote.S3URL)

	// Set preferred content if include/exclude patterns are specified
	if err := setPreferredContentFromPatterns(remote.Name, remote.Include, remote.Exclude); err != nil {
		return err
	}

	return nil
}

func reconfigureImportRemote(remote RemoteConfig) error {
	args := []string{"git", "annex", "enableremote", remote.Name}
	if remote.S3URL != "" {
		args = append(args, "s3url="+remote.S3URL)
	}
	if remote.Bucket != "" {
		args = append(args, "bucket="+remote.Bucket)
	}
	if remote.Prefix != "" {
		args = append(args, "fileprefix="+remote.Prefix)
	}
	if remote.Datacenter != "" {
		args = append(args, "datacenter="+remote.Datacenter)
	}
	if remote.Host != "" {
		args = append(args, "host="+remote.Host)
	}
	if remote.Port != "" {
		args = append(args, "port="+remote.Port)
	}
	if remote.Protocol != "" {
		args = append(args, "protocol="+remote.Protocol)
	}
	args = append(args, "importtree=yes")

	if err := runCommand(args); err != nil {
		return err
	}

	// Update tracking branch (with fallback to current branch or main)
	trackingBranch := remote.TrackingBranch
	if trackingBranch == "" {
		// Default to current branch or main
		out, err := command("git", "branch", "--show-current").Output()
		if err == nil && len(out) > 0 {
			trackingBranch = strings.TrimSpace(string(out))
		} else {
			trackingBranch = "main"
		}
	}
	_ = runCommand([]string{"git", "config", fmt.Sprintf("remote.%s.annex-tracking-branch", remote.Name), trackingBranch})
	fmt.Printf("Set remote.%s.annex-tracking-branch to '%s'\n", remote.Name, trackingBranch)

	// Persist s3url in annex-remote config
	ensureRemoteConfigS3URL(remote.Name, remote.S3URL)

	// Set preferred content if include/exclude patterns are specified
	if err := setPreferredContentFromPatterns(remote.Name, remote.Include, remote.Exclude); err != nil {
		return err
	}

	return nil
}

func reconfigureExospaceRemote(remote RemoteConfig) error {
	args := []string{"git", "annex", "enableremote", remote.Name}
	if remote.RsyncURL != "" {
		args = append(args, "rsyncurl="+remote.RsyncURL)
	}

	if err := runCommand(args); err != nil {
		return err
	}

	// Set rsync options if permissions or rsync_options are specified
	rsyncOpts := getExpectedRsyncOptions(remote)
	if err := setRsyncOptions(remote.Name, rsyncOpts); err != nil {
		return fmt.Errorf("failed to set rsync options: %w", err)
	}
	if rsyncOpts != "" {
		fmt.Printf("Set remote.%s.annex-rsync-options to '%s'\n", remote.Name, rsyncOpts)
	}

	// Set preferred content if include/exclude patterns are specified
	if err := setPreferredContentFromPatterns(remote.Name, remote.Include, remote.Exclude); err != nil {
		return err
	}

	// Hint about auth if no token exists yet
	if _, err := os.Stat(driveTokenPath(remote.Name)); os.IsNotExist(err) {
		fmt.Printf("⚠️  Drive remote '%s' needs authorization. Run: exo auth %s\n", remote.Name, remote.Name)
	}

	return nil
}

func reconfigureDriveRemote(remote RemoteConfig) error {
	args := []string{"git", "annex", "enableremote", remote.Name}
	if remote.DrivePath != "" {
		args = append(args, "drive_path="+remote.DrivePath)
	}
	if len(remote.Exclude) > 0 {
		args = append(args, "exclude="+strings.Join(remote.Exclude, ","))
	}

	if err := runCommand(args); err != nil {
		return err
	}

	// Update tracking branch (with fallback to current branch or main)
	trackingBranch := remote.TrackingBranch
	if trackingBranch == "" {
		out, err := command("git", "branch", "--show-current").Output()
		if err == nil && len(out) > 0 {
			trackingBranch = strings.TrimSpace(string(out))
		} else {
			trackingBranch = "main"
		}
	}
	_ = runCommand([]string{"git", "config", fmt.Sprintf("remote.%s.annex-tracking-branch", remote.Name), trackingBranch})
	fmt.Printf("Set remote.%s.annex-tracking-branch to '%s'\n", remote.Name, trackingBranch)

	// Set preferred content if include/exclude patterns are specified
	if err := setPreferredContentFromPatterns(remote.Name, remote.Include, remote.Exclude); err != nil {
		return err
	}

	return nil
}

// rsyncOptionsFromPermissions converts a permission preset to rsync --chmod flags.
// Returns empty string if preset is empty or unrecognized.
func rsyncOptionsFromPermissions(preset string) string {
	switch preset {
	case "public":
		return "--chmod=ugo=rwX,Do+t,Fo-w"
	case "group":
		return "--chmod=ug=rwX,o=rX,Do+t,Fo-w"
	case "private":
		return "--chmod=u=rwX,go="
	default:
		return ""
	}
}

// getExpectedRsyncOptions returns the expected rsync options for an exospace remote
// based on either the permissions preset or custom rsync_options field.
func getExpectedRsyncOptions(remote RemoteConfig) string {
	if remote.RsyncOptions != "" {
		return remote.RsyncOptions
	}
	return rsyncOptionsFromPermissions(remote.Permissions)
}

// setRsyncOptions sets the rsync options for an exospace remote via git config.
// If options is empty, it removes the config entry.
func setRsyncOptions(remoteName, options string) error {
	configKey := fmt.Sprintf("remote.%s.annex-rsync-options", remoteName)
	if options == "" {
		// Remove the config entry
		_ = command("git", "config", "--unset", configKey).Run()
		return nil
	}
	return runCommand([]string{"git", "config", configKey, options})
}

// getGitConfig reads a git config value
func getGitConfig(key string) string {
	output, err := command("git", "config", "--get", key).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

// detectOrphanedRemotes finds remotes that exist in git-annex but not in .exohub/remotes
func detectOrphanedRemotes(remotesConfig *RemotesConfig) []string {
	// Get all git-annex special remotes
	output, err := command("git", "config", "--get-regexp", "^remote\\..+\\.annex-uuid$").Output()
	if err != nil {
		return nil
	}

	var orphaned []string
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		// Extract remote name from "remote.<name>.annex-uuid"
		parts := strings.Split(line, " ")
		if len(parts) < 1 {
			continue
		}
		key := parts[0]
		// Parse remote.<name>.annex-uuid
		if !strings.HasPrefix(key, "remote.") || !strings.HasSuffix(key, ".annex-uuid") {
			continue
		}
		name := strings.TrimPrefix(key, "remote.")
		name = strings.TrimSuffix(name, ".annex-uuid")

		// Check if this remote is in .exohub/remotes
		found := false
		for _, remote := range remotesConfig.Remotes {
			if remote.Name == name {
				found = true
				break
			}
		}

		if !found {
			orphaned = append(orphaned, name)
		}
	}

	return orphaned
}

func preflight() error {
	if err := ensureGitRepo(); err != nil {
		return err
	}
	if err := ensureOrigin(); err != nil {
		return err
	}
	if err := ensureAnnexInitialized(); err != nil {
		return err
	}
	return nil
}

func newInitSubcommand(name string, action func() error, aliases ...string) *cobra.Command {
	return &cobra.Command{
		Use:     name,
		Aliases: aliases,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return fmt.Errorf("unexpected argument: %s", args[0])
			}
			if err := preflight(); err != nil {
				return err
			}
			return action()
		},
	}
}

func runSequence(steps ...func() error) error {
	for _, step := range steps {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}

func ensureGitRepo() error {
	if !isDir(".git") {
		fmt.Printf("Initializing git repository in %s\n", mustCwd())
		return runCommand([]string{"git", "init"})
	}
	return nil
}

func ensureInitialCommit(skipReadme bool) error {
	// Check if there are any commits
	if err := command("git", "rev-parse", "HEAD").Run(); err == nil {
		// HEAD exists, so there's already a commit
		return nil
	}

	// Check if main/master branch exists
	if err := command("git", "show-ref", "--verify", "--quiet", "refs/heads/main").Run(); err == nil {
		// main branch exists
		return nil
	}
	if err := command("git", "show-ref", "--verify", "--quiet", "refs/heads/master").Run(); err == nil {
		// master branch exists
		return nil
	}

	// No commits and no branches, create initial commit
	fmt.Printf("📝 Creating initial commit on main branch...\n")

	defaultBranch := "main"
	if originBranch, err := getOriginDefaultBranch(); err == nil && originBranch != "" {
		defaultBranch = originBranch
	}

	// Set default branch to match origin when possible
	if err := runCommand([]string{"git", "config", "--local", "init.defaultBranch", defaultBranch}); err != nil {
		fmt.Printf("⚠️  Could not set default branch: %v\n", err)
	}

	// Create README.md only when we didn't pull from origin
	if !skipReadme {
		readmePath := "README.md"
		if _, err := os.Stat(readmePath); os.IsNotExist(err) {
			repoName := filepath.Base(mustCwd())
			readme := fmt.Sprintf("# %s\n\nCreated with exo CLI\n", repoName)
			if err := os.WriteFile(readmePath, []byte(readme), 0644); err != nil {
				return fmt.Errorf("failed to create README.md: %w", err)
			}
		}
		// Stage README.md only if we created it
		if err := runCommand([]string{"git", "add", "README.md"}); err != nil {
			return fmt.Errorf("failed to stage README.md: %w", err)
		}
	}
	if err := runCommand([]string{"git", "add", ".exohub/"}); err != nil {
		// .exohub might not exist yet, that's ok
		fmt.Printf("⚠️  Could not stage .exohub/: %v\n", err)
	}

	// Build commit message with context and remotes info
	commitMsg := buildInitialCommitMessage()

	// Create initial commit with message from stdin
	cmd := command("git", "commit", "-F", "-")
	cmd.Stdin = strings.NewReader(commitMsg)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to create initial commit: %w", err)
	}

	// Ensure we're on main branch
	if err := runCommand([]string{"git", "branch", "-M", defaultBranch}); err != nil {
		fmt.Printf("⚠️  Could not rename branch to main: %v\n", err)
	}

	return nil
}

func buildInitialCommitMessage() string {
	msg := "feat: initial commit created with `exo`\n\n"

	// Add context file content
	contextPath := filepath.Join(".exohub", "context")
	if contextData, err := os.ReadFile(contextPath); err == nil {
		msg += "## .exohub/context\n\n"
		msg += "```yaml\n"
		msg += string(contextData)
		msg += "```\n\n"
	}

	// Add remotes file content
	remotesPath := filepath.Join(".exohub", "remotes")
	if remotesData, err := os.ReadFile(remotesPath); err == nil {
		msg += "## .exohub/remotes\n\n"
		msg += "```yaml\n"
		msg += string(remotesData)
		msg += "```\n"
	}

	return msg
}

func ensureOrigin() error {
	if err := command("git", "remote", "get-url", "origin").Run(); err == nil {
		return nil
	}

	// Try to suggest a URL from context
	repoName := getRepoName()
	var url string
	var err error

	globalCtx, ctxErr := contextcmd.GetCurrentContext()
	if ctxErr == nil && globalCtx.Host != "" && globalCtx.Org != "" {
		suggestedURL := fmt.Sprintf("%s/%s/%s", strings.TrimSuffix(globalCtx.Host, "/"), globalCtx.Org, repoName)
		url, err = askWithDefault("Enter git URL for 'origin'", suggestedURL)
	} else {
		url, err = askNonEmpty("Enter git URL for 'origin':", "")
	}
	if err != nil {
		return err
	}
	if err := runCommand([]string{"git", "remote", "add", "origin", url}); err != nil {
		return err
	}
	fmt.Printf("Set origin to %s\n", url)
	return nil
}

func getRepoName() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "repo"
	}
	parts := strings.Split(cwd, string(os.PathSeparator))
	if len(parts) == 0 {
		return "repo"
	}
	return parts[len(parts)-1]
}

func ensureAnnexInitialized() error {
	// Check if already initialized
	if err := command("git", "config", "--get", "annex.uuid").Run(); err == nil {
		return nil
	}

	// Before initializing, try to fetch git-annex branch from origin if it exists.
	// This must happen BEFORE git annex init, so the branch exists locally and
	// git annex init will recognize existing remotes in remote.log.
	// If we fetch after init, the branches diverge and require a merge.
	_ = fetchGitAnnexBranch()

	// Tell git-annex to ignore origin before init, so it won't probe it
	// for git-annex-shell (which produces a confusing warning on regular
	// git forges like Gitea/GitHub/GitLab).
	if command("git", "remote", "get-url", "origin").Run() == nil {
		_ = command("git", "config", "remote.origin.annex-ignore", "true").Run()
	}

	// Initialize git-annex
	return runCommand([]string{"git", "annex", "init"})
}

// applyAnnexSetting configures an annex setting.
// When shared is true, uses "git annex config" (stored on git-annex branch, propagates to clones).
// When shared is false, uses "git config" (local .git/config only).
// If val is nil, uses defaultVal. Skips shared settings already set to the target value.
func applyAnnexSetting(key string, val *bool, defaultVal bool, shared bool) {
	target := defaultVal
	if val != nil {
		target = *val
	}
	fullKey := "annex." + key
	if shared {
		out, err := command("git", "annex", "config", "--get", fullKey).Output()
		current := strings.TrimSpace(string(out))
		targetStr := fmt.Sprintf("%t", target)
		if err == nil && current == targetStr {
			return
		}
		_ = runCommand([]string{"git", "annex", "config", "--set", fullKey, targetStr})
	} else {
		if target {
			_ = command("git", "config", fullKey, "true").Run()
		} else {
			_ = command("git", "config", "--unset", fullKey).Run()
		}
	}
}

// applyAnnexDefaults applies all annex settings from ExohubConfig with their defaults.
func applyAnnexDefaults(cfg *ExohubConfig) {
	var addUnlocked, thin *bool
	if cfg != nil && cfg.Annex != nil {
		addUnlocked = cfg.Annex.AddUnlocked
		thin = cfg.Annex.Thin
	}
	applyAnnexSetting("addunlocked", addUnlocked, true, true)
	applyAnnexSetting("thin", thin, false, false)
}

// fetchGitAnnexBranch fetches the git-annex branch from origin if it exists.
// This is needed for fresh clones where the git-annex branch hasn't been fetched yet,
// so that remoteExistsInAnnex can find existing remotes in remote.log.
// IMPORTANT: This must be called BEFORE git annex init, while no local git-annex
// branch exists yet. This allows the fetch to create the branch directly.
func fetchGitAnnexBranch() error {
	// Check if origin remote exists
	if err := command("git", "remote", "get-url", "origin").Run(); err != nil {
		// No origin remote, nothing to fetch
		return nil
	}

	// Check if local git-annex branch already exists - if so, skip fetch
	// (git annex init will handle merging if needed)
	if err := command("git", "show-ref", "--verify", "--quiet", "refs/heads/git-annex").Run(); err == nil {
		// Local branch exists, don't try to fetch over it
		return nil
	}

	// Try to fetch git-annex branch from origin directly into local branch
	// This works because no local git-annex branch exists yet
	cmd := command("git", "fetch", "origin", "git-annex:git-annex")
	_ = cmd.Run() // Ignore errors - origin might not have git-annex branch
	return nil
}

func remoteExists(name string) bool {
	// Check if remote has an annex-uuid in git config
	// This is more reliable than 'git annex info' especially after renames,
	// since git config is updated immediately but remote.log may lag
	uuid := getRemoteUUID(name)
	return uuid != ""
}

// remoteExistsInAnnex checks if a remote exists in git-annex metadata (remote.log)
// even if it's not configured in local git config. This handles the case where
// a special remote exists in the git-annex branch but hasn't been enabled locally.
func remoteExistsInAnnex(name string) bool {
	// Check git-annex remote.log for this remote name
	output, err := command("git", "show", "git-annex:remote.log").Output()
	if err != nil {
		return false
	}

	// Look for name=<remoteName> in remote.log
	// remote.log format: "uuid timestamp name=value key=value ..."
	for _, line := range strings.Split(string(output), "\n") {
		if strings.Contains(line, fmt.Sprintf("name=%s ", name)) || strings.HasSuffix(line, fmt.Sprintf("name=%s", name)) {
			return true
		}
	}
	return false
}

func getRemoteUUID(name string) string {
	output, err := command("git", "config", "--get", fmt.Sprintf("remote.%s.annex-uuid", name)).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

// buildUUIDToNameMap builds a map of UUID -> remote name for all git-annex remotes
func buildUUIDToNameMap() (map[string]string, error) {
	output, err := command("git", "config", "--get-regexp", "remote\\..*\\.annex-uuid").Output()
	if err != nil {
		// No remotes configured is not an error
		return make(map[string]string), nil
	}

	uuidMap := make(map[string]string)
	lines := strings.Split(string(output), "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Parse: remote.<name>.annex-uuid <uuid>
		parts := strings.Fields(line)
		if len(parts) != 2 {
			continue
		}

		configKey := parts[0]
		uuid := parts[1]

		// Extract remote name from remote.<name>.annex-uuid
		if !strings.HasPrefix(configKey, "remote.") || !strings.HasSuffix(configKey, ".annex-uuid") {
			continue
		}

		// Remove "remote." prefix and ".annex-uuid" suffix
		remoteName := strings.TrimPrefix(configKey, "remote.")
		remoteName = strings.TrimSuffix(remoteName, ".annex-uuid")

		uuidMap[uuid] = remoteName
	}

	return uuidMap, nil
}

func getRemoteConfig(name, key string) string {
	uuid := getRemoteUUID(name)
	if uuid == "" {
		return ""
	}
	output, err := command("git", "config", "--get", fmt.Sprintf("annex-remote.%s.config.%s", uuid, key)).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func getRemoteInfoS3URL(name string) string {
	args := []string{"git", "annex", "info", "--fast", name}
	output, err := command(args[0], args[1:]...).Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "s3url: ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "s3url: "))
		}
	}
	return ""
}

func ensureRemoteConfigS3URL(name, s3url string) {
	if s3url == "" {
		return
	}
	uuid := getRemoteUUID(name)
	if uuid == "" {
		return
	}
	saved := getRemoteConfig(name, "s3url")
	if saved == s3url {
		return
	}
	fmt.Println("Persisting s3url under annex-remote." + uuid + ".config.s3url")
	_ = runCommand([]string{"git", "config", fmt.Sprintf("annex-remote.%s.config.s3url", uuid), s3url})
	_ = runCommand([]string{"git", "annex", "enableremote", name, "s3url=" + s3url})
}

func createS3Annex() error {
	name := "s3-annex"
	if remoteExists(name) {
		fmt.Printf("Special remote '%s' already exists; skipping.\n", name)
		return nil
	}
	ok, err := askYesNoTUI("Create '"+name+"' special remote?", true)
	if err != nil || !ok {
		return err
	}
	s3url, err := askNonEmpty("Enter s3url for "+name+" (e.g., s3://bucket/prefix):", "^s3://.+$")
	if err != nil {
		return err
	}
	fmt.Println("Initializing " + name + " with chunk=1GiB via s5cmd external remote")
	return runCommand([]string{
		"git", "annex", "initremote", name,
		"type=external", "externaltype=s5cmd", "encryption=none",
		"s3url=" + s3url, "chunk=1GiB",
	})
}

func createS3Export() error {
	name := "s3-export"
	if remoteExists(name) {
		fmt.Printf("Special remote '%s' already exists; skipping.\n", name)
		existing := getRemoteConfig(name, "s3url")
		infoS3 := getRemoteInfoS3URL(name)
		if existing != "" {
			fmt.Printf("Remote '%s' already has s3url configured: %s\n", name, existing)
			fmt.Println("Re-enabling remote to refresh settings")
			_ = runCommand([]string{"git", "annex", "enableremote", name, "exporttree=yes"})
			return nil
		}
		if infoS3 != "" {
			fmt.Printf("Remote '%s' reports s3url: %s\n", name, infoS3)
			uuid := getRemoteUUID(name)
			if uuid != "" {
				_ = runCommand([]string{"git", "config", fmt.Sprintf("annex-remote.%s.config.s3url", uuid), infoS3})
			}
			_ = runCommand([]string{"git", "annex", "enableremote", name, "exporttree=yes"})
			return nil
		}
		s3url, err := askNonEmpty("Enter s3url for "+name+" (e.g., s3://bucket/prefix):", "^s3://.+$")
		if err != nil {
			return err
		}
		fmt.Println("Updating " + name + " with s3url and enabling exporttree")
		return runCommand([]string{"git", "annex", "enableremote", name, "s3url=" + s3url, "exporttree=yes"})
	}

	ok, err := askYesNoTUI("Create '"+name+"' special remote?", true)
	if err != nil || !ok {
		return err
	}
	s3url, err := askNonEmpty("Enter s3url for "+name+" (e.g., s3://bucket/prefix):", "^s3://.+$")
	if err != nil {
		return err
	}
	fmt.Println("Initializing " + name + " as exporttree via s5cmd external remote")
	if err := runCommand([]string{
		"git", "annex", "initremote", name,
		"type=external", "externaltype=s5cmd", "encryption=none",
		"s3url=" + s3url, "exporttree=yes",
	}); err != nil {
		return err
	}
	_ = runCommand([]string{"git", "annex", "enableremote", name, "s3url=" + s3url, "exporttree=yes"})

	tbDefault := defaultTrackingBranch()
	tb, err := askWithDefault("Enter tracking branch or tag for "+name+" (e.g., main or refs/tags/v1):", tbDefault)
	if err != nil {
		return err
	}
	if strings.TrimSpace(tb) != "" {
		_ = runCommand([]string{"git", "config", fmt.Sprintf("remote.%s.annex-tracking-branch", name), tb})
		fmt.Printf("Set remote.%s.annex-tracking-branch to '%s'\n", name, tb)
	}
	ensureRemoteConfigS3URL(name, s3url)
	return nil
}

func defaultTrackingBranch() string {
	if b := currentBranch(); b != "" {
		return b
	}
	if hasBranch("main") {
		return "main"
	}
	if hasBranch("master") {
		return "master"
	}
	return "main"
}

func currentBranch() string {
	out, err := command("git", "symbolic-ref", "--quiet", "--short", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func hasBranch(name string) bool {
	return command("git", "show-ref", "--verify", "--quiet", "refs/heads/"+name).Run() == nil
}

func createExospace() error {
	name := "exospace"
	if remoteExists(name) {
		fmt.Printf("Special remote '%s' already exists; skipping.\n", name)
		return nil
	}
	ok, err := askYesNoTUI("Create '"+name+"' special remote?", true)
	if err != nil || !ok {
		return err
	}
	rsyncURL, err := askNonEmpty("Enter rsyncurl for "+name+" (e.g., rsync://host/path or /mnt/dir):", "")
	if err != nil {
		return err
	}
	fmt.Println("Initializing " + name + " as rsync special remote")
	return runCommand([]string{
		"git", "annex", "initremote", name,
		"type=rsync", "rsyncurl=" + rsyncURL,
		"encryption=none",
	})
}

func createGoogleDrive() error {
	name := "google-drive"
	if remoteExists(name) {
		fmt.Printf("Special remote '%s' already exists; skipping.\n", name)
		return nil
	}
	ok, err := askYesNoTUI("Create '"+name+"' special remote?", true)
	if err != nil || !ok {
		return err
	}
	fmt.Println("Initializing " + name + " as gdrive special remote")
	return runCommand([]string{
		"git", "annex", "initremote", name,
		"type=gdrive", "encryption=none",
	})
}

func askNonEmpty(prompt, rx string) (string, error) {
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Printf("%s ", prompt)
		resp, err := reader.ReadString('\n')
		if err != nil {
			return "", err
		}
		resp = strings.TrimSpace(resp)
		if resp == "" {
			fmt.Fprintln(os.Stderr, "A non-empty value is required.")
			continue
		}
		if rx != "" {
			matched, _ := regexpMatch(rx, resp)
			if !matched {
				fmt.Fprintln(os.Stderr, "Provide a valid value.")
				continue
			}
		}
		return resp, nil
	}
}

func askWithDefault(prompt, def string) (string, error) {
	reader := bufio.NewReader(os.Stdin)
	fmt.Printf("%s [%s] ", prompt, def)
	resp, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	resp = strings.TrimSpace(resp)
	if resp == "" {
		return def, nil
	}
	return resp, nil
}

func regexpMatch(rx, value string) (bool, error) {
	re, err := regexp.Compile(rx)
	if err != nil {
		return false, err
	}
	return re.MatchString(value), nil
}

func runCommand(args []string) error {
	cmd := command(args[0], args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func mustCwd() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return cwd
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// markUUIDasDead marks a remote UUID as dead and removes its configuration
func markUUIDasDead(uuid string, dryRun bool) error {
	fmt.Println("🔍 Looking up UUID " + uuid + "...")

	// Validate UUID format
	if !isValidUUID(uuid) {
		return fmt.Errorf("invalid UUID format: %s", uuid)
	}

	// Find remote name for this UUID
	remoteName, err := getRemoteNameByUUID(uuid)
	if err != nil {
		return fmt.Errorf("UUID %s not found: %w\nRun 'exo info' to see all remotes and their UUIDs", uuid, err)
	}

	fmt.Printf("   Remote name: %s\n\n", SuccessStyle.Render(remoteName))

	// Check for duplicates - if this remote name has multiple UUIDs, reject --dead
	if hasDuplicateRemoteName(remoteName) {
		return fmt.Errorf("❌ Cannot use --dead with duplicate remote names\n\n"+
			"Multiple UUIDs exist for remote '%s'.\n"+
			"Use --destroy instead to completely remove this specific UUID from history:\n\n"+
			"  exo init --destroy %s\n\n"+
			"Run 'exo info' to see all UUIDs for this remote.", remoteName, uuid)
	}

	// Show what will be done
	fmt.Println("⚠️  This will:")
	fmt.Printf("  - Mark remote '%s' (%s) as dead\n", remoteName, uuid)
	fmt.Println("  - Remove dead remotes from git-annex metadata")
	fmt.Printf("  - Remove git config for remote.%s\n\n", remoteName)

	if dryRun {
		fmt.Println(WarningStyle.Render("DRY RUN - No changes will be made"))
		return nil
	}

	// Confirm using Bubble Tea
	if !flagYes {
		ok, err := askYesNoTUI("Continue?", false)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("aborted")
		}
	}

	fmt.Println()

	// Step 1: Mark as dead
	fmt.Printf("🔄 Marking %s as dead...\n", remoteName)
	if err := runCommand([]string{"git", "annex", "dead", remoteName}); err != nil {
		return fmt.Errorf("failed to mark as dead: %w", err)
	}
	fmt.Println("✓ Marked as dead")

	// Step 2: Forget dead remotes
	fmt.Println("🔄 Forgetting dead remotes from git-annex...")
	if err := runCommand([]string{"git", "annex", "forget", "--drop-dead", "--force"}); err != nil {
		return fmt.Errorf("failed to forget dead remotes: %w", err)
	}
	fmt.Println("✓ Forgot dead remotes")

	// Step 3: Remove git config section
	fmt.Println("🔄 Removing git config...")
	if err := command("git", "config", "--remove-section", "remote."+remoteName).Run(); err != nil {
		// Not an error if section doesn't exist
		fmt.Println("⚠️  Git config section not found (may have been already removed)")
	} else {
		fmt.Println("✓ Removed git config section")
	}

	fmt.Printf("\n%s\n\n", SuccessStyle.Render("✅ Cleanup complete"))
	fmt.Println("To reconnect to the correct remote, run: exo init")

	return nil
}

// destroyUUID completely removes a UUID from git-annex history (DESTRUCTIVE)
func destroyUUID(uuid string, dryRun bool, confirmUUID string) error {
	// Show different header for dry-run vs actual operation
	if dryRun {
		fmt.Println(WarningStyle.Render("🔍 DRY RUN: Preview UUID Destruction"))
		fmt.Println()
		fmt.Printf("Analyzing UUID %s\n\n", WarningStyle.Render(uuid))
	} else {
		fmt.Println(ErrorStyle.Render("⚠️  WARNING: DESTRUCTIVE OPERATION ⚠️"))
		fmt.Println()
		fmt.Printf("You are about to DESTROY UUID %s\n\n", ErrorStyle.Render(uuid))
	}

	// Validate UUID format
	if !isValidUUID(uuid) {
		return fmt.Errorf("invalid UUID format: %s", uuid)
	}

	// Find remote name
	remoteName, err := getRemoteNameByUUID(uuid)
	if err != nil {
		fmt.Printf("⚠️  UUID not found in local config, checking git-annex branch...\n")
		remoteName, err = getRemoteNameFromGitAnnex(uuid)
		if err != nil {
			return fmt.Errorf("UUID %s not found in repository", uuid)
		}
	}
	fmt.Printf("Remote name: %s\n\n", WarningStyle.Render(remoteName))

	// Show warnings (different wording for dry-run)
	if dryRun {
		fmt.Println("This operation would:")
		fmt.Println("  • Remove ALL references from git-annex branch")
		fmt.Println("  • Remove from remote.log, uuid.log, trust.log")
		fmt.Println("  • Remove from ALL location tracking logs")
		fmt.Println("  • Rewrite git-annex branch history")
		fmt.Println()
		fmt.Println("Note: This does NOT delete actual data on S3/rsync, but you would lose")
		fmt.Println("tracking information about which keys were stored on this UUID.")
		fmt.Println()
	} else {
		fmt.Println("This will:")
		fmt.Println("  " + ErrorStyle.Render("✗") + " Remove ALL references from git-annex branch")
		fmt.Println("  " + ErrorStyle.Render("✗") + " Remove from remote.log, uuid.log, trust.log")
		fmt.Println("  " + ErrorStyle.Render("✗") + " Remove from ALL location tracking logs")
		fmt.Println("  " + ErrorStyle.Render("✗") + " Rewrite git-annex branch history")
		fmt.Println("  " + ErrorStyle.Render("✗") + " Cannot be undone without restoring from backup")
		fmt.Println()
		fmt.Println("This does NOT delete actual data on S3/rsync, but you will lose")
		fmt.Println("tracking information about which keys were stored on this UUID.")
		fmt.Println()
	}

	// Check if UUID has tracked content
	keyCount, err := countKeysOnUUID(uuid)
	if err == nil && keyCount > 0 {
		if dryRun {
			fmt.Printf("📊 This UUID has %s keys tracked in location logs\n", WarningStyle.Render(fmt.Sprintf("%d", keyCount)))
			fmt.Println("    Consider migrating content before destroying this UUID.")
			fmt.Println()
		} else {
			fmt.Printf("⚠️  WARNING: This UUID has %s keys tracked in location logs\n", WarningStyle.Render(fmt.Sprintf("%d", keyCount)))
			fmt.Println("Consider migrating content before destroying this UUID.")
			fmt.Println()
		}
	}

	// Create backup branch name
	backupBranch := fmt.Sprintf("backup/git-annex-%s", time.Now().Format("20060102-150405"))
	if dryRun {
		fmt.Printf("A backup would be created at: %s\n\n", SuccessStyle.Render(backupBranch))
	} else {
		fmt.Printf("A backup will be created at: %s\n\n", SuccessStyle.Render(backupBranch))
	}

	if dryRun {
		fmt.Println(WarningStyle.Render("DRY RUN - No changes will be made"))
		fmt.Println("\nWould execute:")
		fmt.Printf("  1. Create backup: git branch %s git-annex\n", backupBranch)
		fmt.Println("  2. Remove UUID from git-annex branch:")

		// Show what would be filtered in the git-annex branch
		fmt.Println("     - Filtering remote.log, uuid.log, trust.log, group.log")

		// Count location logs that would be affected
		locationLogCount, err := previewLocationLogFiltering(uuid)
		if err != nil {
			fmt.Printf("     ⚠️  Warning: could not preview location logs: %v\n", err)
		} else if locationLogCount > 0 {
			fmt.Printf("     - Would filter %s location log files (remove UUID references)\n",
				WarningStyle.Render(fmt.Sprintf("%d", locationLogCount)))
		} else {
			fmt.Println("     - No location logs contain this UUID")
		}

		fmt.Printf("  3. Remove git config: git config --remove-section remote.%s\n", remoteName)
		return nil
	}

	// Confirmation for interactive mode using Bubble Tea
	if !flagYes {
		ok, err := askYesNoTUI("Have you migrated all content away from this UUID?", false)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("aborted - please migrate content first")
		}
		fmt.Println()
	}

	// Require UUID confirmation using Bubble Tea
	confirmedUUID := confirmUUID
	if confirmedUUID == "" && !flagYes {
		input, err := askUUIDConfirmationTUI("To confirm, type the UUID exactly as shown above:", uuid)
		if err != nil {
			return err
		}
		confirmedUUID = input
	}

	if confirmedUUID != uuid {
		return fmt.Errorf("UUID confirmation does not match. Aborted for safety")
	}

	fmt.Println()

	// Step 1: Create backup
	fmt.Println("🔄 Creating backup branch...")
	if err := runCommand([]string{"git", "branch", backupBranch, "git-annex"}); err != nil {
		return fmt.Errorf("failed to create backup: %w", err)
	}
	fmt.Printf("✓ Backup created: %s\n", backupBranch)

	// Step 2: Destroy from git-annex branch
	// Note: We don't need to mark as dead or forget first - we're surgically
	// removing the UUID from all log files, which is more complete than the
	// dead + forget workflow
	fmt.Println("\n🔄 Destroying UUID from git-annex branch...")
	if err := destroyUUIDFromGitAnnexBranch(uuid, remoteName); err != nil {
		return fmt.Errorf("failed to destroy from git-annex: %w", err)
	}
	fmt.Println("✓ UUID destroyed from git-annex")

	// Step 3: Remove git config
	fmt.Println("\n🔄 Removing git config...")
	if err := command("git", "config", "--remove-section", "remote."+remoteName).Run(); err != nil {
		fmt.Println("⚠️  Git config section not found (may have been already removed)")
	} else {
		fmt.Println("✓ Removed git config")
	}

	fmt.Printf("\n%s\n\n", SuccessStyle.Render("✅ UUID completely destroyed"))

	// Verification
	fmt.Println("Verification:")
	verifyCmd := command("git", "show", "git-annex:remote.log")
	output, _ := verifyCmd.Output()
	if strings.Contains(string(output), uuid) {
		fmt.Printf("⚠️  Warning: UUID still found in remote.log (may need manual cleanup)\n")
	} else {
		fmt.Printf("✓ UUID not found in git-annex logs\n")
	}

	fmt.Printf("\nBackup available at: %s\n", SuccessStyle.Render(backupBranch))
	fmt.Printf("To restore: git branch -f git-annex %s\n", backupBranch)

	return nil
}

// destroyUUIDFromGitAnnexBranch removes all references to a UUID from git-annex branch
func destroyUUIDFromGitAnnexBranch(uuid, remoteName string) error {
	// Create temporary worktree for git-annex branch
	tmpDir := filepath.Join(os.TempDir(), "git-annex-destroy-"+uuid)

	// Clean up any existing worktree for this UUID
	_ = command("git", "worktree", "remove", tmpDir, "--force").Run()

	// Also clean up any other git-annex worktrees (in case of interrupted operations)
	// Git only allows one worktree per branch, so we need to remove any existing git-annex worktree
	listOutput, _ := command("git", "worktree", "list", "--porcelain").Output()
	for _, line := range strings.Split(string(listOutput), "\n") {
		if strings.HasPrefix(line, "worktree ") {
			worktreePath := strings.TrimPrefix(line, "worktree ")
			// Check if this worktree is for git-annex branch
			if strings.Contains(worktreePath, "git-annex-destroy-") {
				fmt.Printf("  - Removing leftover worktree: %s\n", worktreePath)
				// Try to remove normally first
				if err := command("git", "worktree", "remove", worktreePath, "--force").Run(); err != nil {
					// If that fails, the worktree might be corrupted - try deleting the directory
					// and then prune the worktree metadata
					fmt.Printf("  - Worktree stuck, cleaning up manually...\n")
					_ = os.RemoveAll(worktreePath)
					_ = command("git", "worktree", "prune").Run()
				}
			}
		}
	}

	fmt.Println("  - Creating temporary worktree")
	if err := runCommand([]string{"git", "worktree", "add", tmpDir, "git-annex"}); err != nil {
		return fmt.Errorf("failed to create worktree: %w", err)
	}

	defer func() {
		fmt.Println("  - Cleaning up worktree")
		// After updating the git-annex branch, the worktree might be "locked"
		// Try normal removal first, but if it fails, force cleanup
		if err := command("git", "worktree", "remove", tmpDir, "--force").Run(); err != nil {
			// Worktree locked or corrupted - remove directory and prune metadata
			_ = os.RemoveAll(tmpDir)
			_ = command("git", "worktree", "prune").Run()
		}
	}()

	// Save current directory
	oldDir, err := os.Getwd()
	if err != nil {
		return err
	}

	// Change to worktree
	if err := os.Chdir(tmpDir); err != nil {
		return fmt.Errorf("failed to change to worktree: %w", err)
	}
	defer os.Chdir(oldDir)

	// Filter main log files
	logFiles := []string{
		"remote.log",
		"uuid.log",
		"trust.log",
		"group.log",
	}

	for _, file := range logFiles {
		fmt.Printf("  - Filtering %s\n", file)
		if err := filterUUIDFromLog(file, uuid); err != nil {
			fmt.Printf("    ⚠️  Warning: %v\n", err)
		}
	}

	// Filter location logs (only those containing the UUID)
	fmt.Println("  - Filtering location logs")

	// First, find all location logs that contain this UUID using git grep
	cmd := command("git", "grep", "-l", uuid, "--", "*.log")
	output, err := cmd.Output()

	filesToFilter := []string{}
	if err != nil {
		// Exit code 1 means no matches found
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			fmt.Println("    (no location logs contain this UUID)")
		} else {
			return fmt.Errorf("failed to search for UUID in logs: %w", err)
		}
	} else {
		// Parse the list of files
		scanner := bufio.NewScanner(bytes.NewReader(output))
		for scanner.Scan() {
			filename := scanner.Text()
			// Skip main log files (already processed above)
			base := filepath.Base(filename)
			if base != "remote.log" && base != "uuid.log" &&
			   base != "trust.log" && base != "group.log" {
				filesToFilter = append(filesToFilter, filename)
			}
		}
		if err := scanner.Err(); err != nil {
			return fmt.Errorf("failed to parse git grep output: %w", err)
		}
	}

	// Now filter only the files that actually contain the UUID
	modifiedCount := 0
	for _, file := range filesToFilter {
		modified, err := filterUUIDFromLogWithReport(file, uuid)
		if err != nil {
			fmt.Printf("    ⚠️  Warning: failed to filter %s: %v\n", file, err)
			continue
		}
		if modified {
			modifiedCount++
		}
	}

	if len(filesToFilter) > 0 {
		fmt.Printf("    (filtered %s location logs, removed UUID from %s)\n",
			WarningStyle.Render(fmt.Sprintf("%d", len(filesToFilter))),
			WarningStyle.Render(fmt.Sprintf("%d", modifiedCount)))
	}

	// Commit changes in the worktree
	fmt.Println("  - Committing changes")
	if err := runCommand([]string{"git", "add", "."}); err != nil {
		return fmt.Errorf("failed to stage changes: %w", err)
	}

	commitMsg := fmt.Sprintf("exo: destroy UUID %s [%s] - remove all references", uuid, remoteName)
	if err := runCommand([]string{"git", "commit", "-m", commitMsg}); err != nil {
		return fmt.Errorf("failed to commit: %w", err)
	}

	// Get the commit SHA that we just created
	commitOutput, err := command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		return fmt.Errorf("failed to get commit SHA: %w", err)
	}
	commitSHA := strings.TrimSpace(string(commitOutput))

	// Return to original directory before updating the main git-annex branch
	if err := os.Chdir(oldDir); err != nil {
		return fmt.Errorf("failed to return to original directory: %w", err)
	}

	// Update the main git-annex branch to point to our new commit
	fmt.Println("  - Updating git-annex branch")
	if err := runCommand([]string{"git", "update-ref", "refs/heads/git-annex", commitSHA}); err != nil {
		return fmt.Errorf("failed to update git-annex branch: %w", err)
	}

	return nil
}

// filterUUIDFromLog removes all lines containing a UUID from a log file
func filterUUIDFromLog(filepath, uuid string) error {
	_, err := filterUUIDFromLogWithReport(filepath, uuid)
	return err
}

// filterUUIDFromLogWithReport removes all lines containing a UUID from a log file
// and reports whether any lines were actually removed
func filterUUIDFromLogWithReport(filepath, uuid string) (bool, error) {
	content, err := os.ReadFile(filepath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil // File doesn't exist, skip
		}
		return false, err
	}

	// Filter out any lines containing the UUID
	var filtered []string
	linesRemoved := 0
	scanner := bufio.NewScanner(bytes.NewReader(content))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.Contains(line, uuid) {
			filtered = append(filtered, line)
		} else {
			linesRemoved++
		}
	}

	if err := scanner.Err(); err != nil {
		return false, err
	}

	// Only write if something changed
	if linesRemoved == 0 {
		return false, nil
	}

	// Write filtered content back
	newContent := strings.Join(filtered, "\n")
	if len(newContent) > 0 {
		newContent += "\n" // Ensure trailing newline
	}

	return true, os.WriteFile(filepath, []byte(newContent), 0644)
}

// previewLocationLogFiltering counts how many location log files would be affected
// by UUID filtering without actually modifying anything
func previewLocationLogFiltering(uuid string) (int, error) {
	// Use git grep to find all location logs containing this UUID
	cmd := command("git", "grep", "-l", uuid, "git-annex", "--", "*.log")
	output, err := cmd.Output()

	if err != nil {
		// Exit code 1 means no matches found
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return 0, nil
		}
		return 0, err
	}

	// Count the files, excluding main log files (remote.log, uuid.log, etc.)
	count := 0
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		filename := scanner.Text()
		// Strip git-annex: prefix if present
		filename = strings.TrimPrefix(filename, "git-annex:")
		// Skip main log files - we only care about location logs
		base := filepath.Base(filename)
		if base != "remote.log" && base != "uuid.log" &&
		   base != "trust.log" && base != "group.log" {
			count++
		}
	}

	return count, scanner.Err()
}

// getRemoteNameByUUID finds the remote name for a given UUID
func getRemoteNameByUUID(uuid string) (string, error) {
	// First check local git config
	output, err := command("git", "config", "--get-regexp", "^remote\\..*\\.annex-uuid$").Output()
	if err == nil {
		for _, line := range strings.Split(string(output), "\n") {
			parts := strings.Fields(line)
			if len(parts) == 2 && parts[1] == uuid {
				// Extract name from "remote.<name>.annex-uuid"
				key := parts[0]
				name := strings.TrimPrefix(key, "remote.")
				name = strings.TrimSuffix(name, ".annex-uuid")
				return name, nil
			}
		}
	}

	// Not found in local config, check git-annex branch
	return getRemoteNameFromGitAnnex(uuid)
}

// getRemoteNameFromGitAnnex finds remote name from git-annex branch logs
func getRemoteNameFromGitAnnex(uuid string) (string, error) {
	// Try remote.log first (for configured remotes)
	output, err := command("git", "show", "git-annex:remote.log").Output()
	if err == nil {
		// Parse remote.log format: "uuid timestamp name=value ..."
		for _, line := range strings.Split(string(output), "\n") {
			if strings.HasPrefix(line, uuid+" ") {
				// Extract name from parameters
				parts := strings.Fields(line)
				for _, part := range parts {
					if strings.HasPrefix(part, "name=") {
						return strings.TrimPrefix(part, "name="), nil
					}
				}
			}
		}
	}

	// Fallback to uuid.log (for repository descriptions)
	output, err = command("git", "show", "git-annex:uuid.log").Output()
	if err != nil {
		return "", fmt.Errorf("failed to read git-annex logs: %w", err)
	}

	// Parse uuid.log format: "uuid timestamp description"
	for _, line := range strings.Split(string(output), "\n") {
		if strings.HasPrefix(line, uuid+" ") {
			parts := strings.Fields(line)
			if len(parts) >= 3 {
				// Description is everything after timestamp
				description := strings.Join(parts[2:], " ")
				return description, nil
			}
		}
	}

	return "", fmt.Errorf("UUID not found in git-annex branch")
}

// isValidUUID checks if a string is a valid UUID format
func hasDuplicateRemoteName(remoteName string) bool {
	// Count how many UUIDs are associated with this remote name
	// Check both git config and git-annex branch

	uuids := make(map[string]bool)

	// Check git config
	output, err := command("git", "config", "--get", "remote."+remoteName+".annex-uuid").Output()
	if err == nil {
		uuid := strings.TrimSpace(string(output))
		if uuid != "" && uuid != "00000000-0000-0000-0000-000000000001" { // Ignore web UUID
			uuids[uuid] = true
		}
	}

	// Check git-annex branch remote.log for all UUIDs with this name
	output, err = command("git", "show", "git-annex:remote.log").Output()
	if err == nil {
		for _, line := range strings.Split(string(output), "\n") {
			if strings.Contains(line, "name="+remoteName) {
				parts := strings.Fields(line)
				if len(parts) >= 1 {
					uuid := parts[0]
					if uuid != "" && uuid != "00000000-0000-0000-0000-000000000001" {
						uuids[uuid] = true
					}
				}
			}
		}
	}

	// Check uuid.log for repository descriptions matching this name
	output, err = command("git", "show", "git-annex:uuid.log").Output()
	if err == nil {
		for _, line := range strings.Split(string(output), "\n") {
			parts := strings.Fields(line)
			if len(parts) >= 3 {
				uuid := parts[0]
				desc := strings.Join(parts[2:], " ")
				if strings.Contains(desc, remoteName) && uuid != "" && uuid != "00000000-0000-0000-0000-000000000001" {
					uuids[uuid] = true
				}
			}
		}
	}

	return len(uuids) > 1
}

func isValidUUID(uuid string) bool {
	// Standard UUID format: 8-4-4-4-12 hexadecimal digits
	matched, _ := regexp.MatchString(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`, uuid)
	return matched
}

// countKeysOnUUID counts how many keys are tracked on a specific UUID by scanning location logs
func countKeysOnUUID(uuid string) (int, error) {
	// Use git grep to find all location logs containing this UUID
	// Location logs are in the format: xxx/yyy/KEYHASH.log
	cmd := command("git", "grep", "-l", uuid, "git-annex", "--", "*.log")
	output, err := cmd.Output()
	if err != nil {
		// Exit code 1 means no matches found, which is fine
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return 0, nil
		}
		return 0, err
	}

	// Count the files, excluding main log files (remote.log, uuid.log, etc.)
	count := 0
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		filename := scanner.Text()
		// Strip git-annex: prefix if present (format: git-annex:path/to/file.log)
		filename = strings.TrimPrefix(filename, "git-annex:")
		// Skip main log files - we only care about location logs (key logs)
		base := filepath.Base(filename)
		if base != "remote.log" && base != "uuid.log" &&
		   base != "trust.log" && base != "group.log" {
			count++
		}
	}

	return count, scanner.Err()
}

// ensureExportTrackingBranch ensures an export or drive remote has tracking branch configured
func ensureExportTrackingBranch(remote RemoteConfig) error {
	// Only applicable to export and drive remotes
	if remote.Type != "export" && remote.Type != "drive" {
		return nil
	}

	configKey := fmt.Sprintf("remote.%s.annex-tracking-branch", remote.Name)

	// Check current value
	out, _ := command("git", "config", "--get", configKey).Output()
	currentTB := strings.TrimSpace(string(out))

	// If an explicit tracking branch is configured and differs from the current value, update it
	if remote.TrackingBranch != "" && currentTB != remote.TrackingBranch {
		fmt.Printf("Updating remote.%s.annex-tracking-branch: %s → %s\n", remote.Name, currentTB, remote.TrackingBranch)
		return runCommand([]string{"git", "config", configKey, remote.TrackingBranch})
	}

	// Already set to the correct value
	if currentTB != "" {
		return nil
	}

	// Not set yet — determine tracking branch from current branch or fall back to main
	trackingBranch := remote.TrackingBranch
	if trackingBranch == "" {
		bout, err := command("git", "branch", "--show-current").Output()
		if err == nil && len(bout) > 0 {
			trackingBranch = strings.TrimSpace(string(bout))
		} else {
			trackingBranch = "main"
		}
	}

	// Set tracking branch
	return runCommand([]string{"git", "config", configKey, trackingBranch})
}
