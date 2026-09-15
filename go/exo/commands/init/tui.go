package init

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/huh"

	contextcmd "github.com/Genentech/exohub/go/exo/commands/context"
	"github.com/Genentech/exohub/go/exo/commandutil"
	"github.com/Genentech/exohub/go/exo/gitprovider"
)

// tuiContext holds host and org for TUI suggestions and prompts
type tuiContext struct {
	Host     string
	Org      string
	Provider string
}

// runInteractiveTUI launches the interactive TUI for remote creation
func runInteractiveTUI() error {
	ctx := &tuiContext{}
	// Try to get org from global context
	if globalCtx, err := contextcmd.GetCurrentContext(); err == nil {
		ctx.Host = globalCtx.Host
		ctx.Org = globalCtx.Org
	}

	// Step 1: Select remote type
	var remoteType string
	typeForm := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Select remote type").
				Options(
					huh.NewOption("📦  annex: Use S3 to store annexed files (keeps file versions, ensures integrity)", "annex"),
					huh.NewOption("🔴↑ export: Export file tree to S3 (for direct access on S3)", "export"),
					huh.NewOption("🟢↓ import: Import file tree from S3 (reference S3 keys without annexed files)", "import"),
					huh.NewOption("🤝  exospace: Use local directory for annexed files (shared local cache)", "exospace"),
					huh.NewOption("💎  artifactdb: ArtifactDB catalog / ExoHub Atlas integration", "artifactdb"),
					huh.NewOption("☁️  drive: Export file tree to Google Drive", "drive"),
				).
				Value(&remoteType),
		),
	).WithTheme(commandutil.ExoTheme()).WithKeyMap(commandutil.ExoKeyMap())

	if err := typeForm.Run(); err != nil {
		return err
	}

	// Step 2: Collect remote configuration based on type
	switch remoteType {
	case "annex":
		return createAnnexRemoteTUI(ctx)
	case "export":
		return createExportRemoteTUI(ctx)
	case "import":
		return createImportRemoteTUI(ctx)
	case "exospace":
		return createExospaceRemoteTUI(ctx)
	case "artifactdb":
		return createArtifactDBRemoteTUI(ctx)
	case "drive":
		return createDriveRemoteTUI(ctx)
	}

	return nil
}

func createAnnexRemoteTUI(ctx *tuiContext) error {
	name := "s3-annex"
	var s3url string
	chunk := "1GiB" // default

	// Suggest a bucket based on context
	suggestedBucket := fmt.Sprintf("s3://%s-data", strings.ReplaceAll(ctx.Org, "/", "-"))

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Remote name").
				Value(&name).
				Validate(func(s string) error {
					if s == "" {
						return fmt.Errorf("name is required")
					}
					if remoteExists(s) {
						return fmt.Errorf("remote '%s' already exists", s)
					}
					return nil
				}),
			huh.NewInput().
				Title("S3 URL").
				Placeholder(suggestedBucket+"/_annex").
				Description("Example: s3://bucket/prefix/_annex (chunk size defaults to 1GiB, configure in .exohub/remotes for custom values)").
				Value(&s3url).
				Validate(func(s string) error {
					if !strings.HasPrefix(s, "s3://") {
						return fmt.Errorf("must start with s3://")
					}
					return nil
				}),
		),
	).WithTheme(commandutil.ExoTheme()).WithKeyMap(commandutil.ExoKeyMap())

	if err := form.Run(); err != nil {
		return err
	}

	// Ask about grants
	useGrants, err := promptGrants()
	if err != nil {
		return err
	}

	// Ensure we're in a git repo
	if !IsGitRepo() {
		fmt.Println("Not a git repository. Initializing...")
		if err := ensureGitRepo(); err != nil {
			return err
		}
	}

	// Ensure git-annex is initialized
	if err := ensureAnnexInitialized(); err != nil {
		return err
	}

	// Check binary availability
	if err := checkRemoteBinaries("annex"); err != nil {
		return err
	}

	// Create the remote
	fmt.Printf("\nInitializing %s with chunk=%s via s5cmd external remote\n", name, chunk)
	initArgs := []string{
		"git", "annex", "initremote", name,
		"type=external", "externaltype=s5cmd", "encryption=none",
		"s3url=" + s3url, "chunk=" + chunk,
	}
	if useGrants {
		initArgs = append(initArgs, "grants=true")
	}
	if err := runCommand(initArgs); err != nil {
		return err
	}

	// Save to .exohub/remotes
	remotesConfig, err := LoadRemotesConfig()
	if err != nil {
		return err
	}

	// Get UUID for tracking
	uuid := getRemoteUUID(name)

	remotesConfig.AddRemote(RemoteConfig{
		Name:   name,
		Type:   "annex",
		UUID:   uuid,
		S3URL:  s3url,
		Chunk:  chunk,
		Grants: useGrants,
	})

	if err := SaveRemotesConfig(remotesConfig); err != nil {
		return err
	}

	// Set up grants permissions file if grants enabled
	if useGrants {
		if _, err := loadOrCreatePermissions(); err != nil {
			fmt.Printf("⚠️  Could not create permissions file: %v\n", err)
		}
	}

	fmt.Printf("✅ Remote %s created and saved to .exohub/remotes\n", SuccessStyle.Render(name))
	return nil
}

func createExportRemoteTUI(ctx *tuiContext) error {
	name := "s3-export"
	var s3url, trackingBranch string

	// Suggest a bucket based on context
	suggestedBucket := fmt.Sprintf("s3://%s-data", strings.ReplaceAll(ctx.Org, "/", "-"))

	// Get current branch as default
	defaultBranch := defaultTrackingBranch()

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Remote name").
				Value(&name).
				Validate(func(s string) error {
					if s == "" {
						return fmt.Errorf("name is required")
					}
					if remoteExists(s) {
						return fmt.Errorf("remote '%s' already exists", s)
					}
					return nil
				}),
			huh.NewInput().
				Title("S3 URL").
				Placeholder(suggestedBucket+"/_export").
				Description("Example: s3://bucket/prefix/_export").
				Value(&s3url).
				Validate(func(s string) error {
					if !strings.HasPrefix(s, "s3://") {
						return fmt.Errorf("must start with s3://")
					}
					return nil
				}),
			huh.NewInput().
				Title("Tracking branch (optional)").
				Placeholder(defaultBranch).
				Description("Branch or tag to track (e.g., main, refs/tags/v1)").
				Value(&trackingBranch),
		),
	).WithTheme(commandutil.ExoTheme()).WithKeyMap(commandutil.ExoKeyMap())

	if err := form.Run(); err != nil {
		return err
	}

	if trackingBranch == "" {
		trackingBranch = defaultBranch
	}

	// Ask about grants
	useGrants, err := promptGrants()
	if err != nil {
		return err
	}

	// Ensure we're in a git repo
	if !IsGitRepo() {
		fmt.Println("Not a git repository. Initializing...")
		if err := ensureGitRepo(); err != nil {
			return err
		}
	}

	// Ensure git-annex is initialized
	if err := ensureAnnexInitialized(); err != nil {
		return err
	}

	// Check binary availability
	if err := checkRemoteBinaries("export"); err != nil {
		return err
	}

	// Create the remote
	fmt.Printf("\nInitializing %s as exporttree via s5cmd external remote\n", name)
	initArgs := []string{
		"git", "annex", "initremote", name,
		"type=external", "externaltype=s5cmd", "encryption=none",
		"s3url=" + s3url, "exporttree=yes",
	}
	if useGrants {
		initArgs = append(initArgs, "grants=true")
	}
	if err := runCommand(initArgs); err != nil {
		return err
	}

	// Enable remote again to ensure settings are applied
	_ = runCommand([]string{"git", "annex", "enableremote", name, "s3url=" + s3url, "exporttree=yes"})

	// Set tracking branch
	if trackingBranch != "" {
		_ = runCommand([]string{"git", "config", fmt.Sprintf("remote.%s.annex-tracking-branch", name), trackingBranch})
		fmt.Printf("Set remote.%s.annex-tracking-branch to '%s'\n", name, trackingBranch)
	}

	ensureRemoteConfigS3URL(name, s3url)

	// Save to .exohub/remotes
	remotesConfig, err := LoadRemotesConfig()
	if err != nil {
		return err
	}

	// Get UUID for tracking
	uuid := getRemoteUUID(name)

	remotesConfig.AddRemote(RemoteConfig{
		Name:           name,
		Type:           "export",
		UUID:           uuid,
		S3URL:          s3url,
		TrackingBranch: trackingBranch,
		Grants:         useGrants,
	})

	if err := SaveRemotesConfig(remotesConfig); err != nil {
		return err
	}

	// Set up grants permissions file if grants enabled
	if useGrants {
		if _, err := loadOrCreatePermissions(); err != nil {
			fmt.Printf("⚠️  Could not create permissions file: %v\n", err)
		}
	}

	fmt.Printf("✅ Remote %s created and saved to .exohub/remotes\n", SuccessStyle.Render(name))
	return nil
}

func createImportRemoteTUI(ctx *tuiContext) error {
	// Ensure AWS credentials are available
	if err := ensureAWSCredentials(); err != nil {
		fmt.Printf("⚠️  AWS credentials not available: %v\n", err)
		fmt.Println("Please set AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY or configure ~/.aws/credentials")
		return err
	}

	name := "s3-import"
	var s3url, datacenter, trackingBranch string

	// Suggest a bucket based on context
	suggestedBucket := fmt.Sprintf("s3://%s-data", strings.ReplaceAll(ctx.Org, "/", "-"))

	// Get current branch as default
	defaultBranch := defaultTrackingBranch()

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Remote name").
				Value(&name).
				Validate(func(s string) error {
					if s == "" {
						return fmt.Errorf("name is required")
					}
					if remoteExists(s) {
						return fmt.Errorf("remote '%s' already exists", s)
					}
					return nil
				}),
			huh.NewInput().
				Title("S3 URL").
				Placeholder(suggestedBucket+"/import").
				Description("Example: s3://bucket/prefix").
				Value(&s3url).
				Validate(func(s string) error {
					if !strings.HasPrefix(s, "s3://") {
						return fmt.Errorf("must start with s3://")
					}
					return nil
				}),
			huh.NewInput().
				Title("AWS Region").
				Placeholder("us-west-2").
				Description("AWS datacenter/region").
				Value(&datacenter),
			huh.NewInput().
				Title("Tracking branch (optional)").
				Placeholder(defaultBranch).
				Description("Branch or tag to track (e.g., main, refs/tags/v1)").
				Value(&trackingBranch),
		),
	).WithTheme(commandutil.ExoTheme()).WithKeyMap(commandutil.ExoKeyMap())

	if err := form.Run(); err != nil {
		return err
	}

	// Parse s3url into bucket and prefix
	bucket, prefix, err := parseS3URL(s3url)
	if err != nil {
		return err
	}

	if datacenter == "" {
		datacenter = "us-west-2"
	}
	if trackingBranch == "" {
		trackingBranch = defaultBranch
	}

	// Ensure we're in a git repo
	if !IsGitRepo() {
		fmt.Println("Not a git repository. Initializing...")
		if err := ensureGitRepo(); err != nil {
			return err
		}
	}

	// Ensure git-annex is initialized
	if err := ensureAnnexInitialized(); err != nil {
		return err
	}

	// Save to .exohub/remotes before attempting remote creation
	// This allows retry via `exo init` if the git-annex command fails
	remotesConfig, err := LoadRemotesConfig()
	if err != nil {
		return err
	}

	remotesConfig.AddRemote(RemoteConfig{
		Name:           name,
		Type:           "import",
		Bucket:         bucket,
		Prefix:         prefix,
		Datacenter:     datacenter,
		TrackingBranch: trackingBranch,
	})

	if err := SaveRemotesConfig(remotesConfig); err != nil {
		return err
	}

	// Create the remote
	fmt.Printf("\nInitializing %s as S3 import remote (bucket: %s, prefix: %s)\n", name, bucket, prefix)

	// Build base args for both versioning attempts (TUI always defaults to https)
	baseArgs := []string{
		"git", "annex", "initremote", name,
		"type=S3",
		"bucket=" + bucket,
		"encryption=none",
		"protocol=https",
		"importtree=yes",
		"datacenter=" + datacenter,
	}
	if prefix != "" {
		baseArgs = append(baseArgs, "fileprefix="+prefix)
	}

	// Try with versioning=yes first
	args := append(append([]string{}, baseArgs...), "versioning=yes")

	// Run command and capture stderr to check for versioning errors
	cmd := command(args[0], args[1:]...)
	cmd.Stdout = os.Stdout
	var stderrBuf strings.Builder
	cmd.Stderr = io.MultiWriter(os.Stderr, &stderrBuf)
	cmd.Stdin = os.Stdin
	err = cmd.Run()

	if err != nil && (strings.Contains(stderrBuf.String(), "403") ||
		strings.Contains(stderrBuf.String(), "Forbidden") ||
		strings.Contains(stderrBuf.String(), "GetBucketVersioning")) {
		// Retry without versioning if we get 403 (permission denied)
		fmt.Println("⚠️  Versioning not accessible (403), retrying without versioning...")
		if err = runCommand(baseArgs); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	// Set tracking branch
	if trackingBranch != "" {
		_ = runCommand([]string{"git", "config", fmt.Sprintf("remote.%s.annex-tracking-branch", name), trackingBranch})
		fmt.Printf("Set remote.%s.annex-tracking-branch to '%s'\n", name, trackingBranch)
	}

	// Update UUID in .exohub/remotes after successful creation
	remotesConfig2, err := LoadRemotesConfig()
	if err == nil {
		uuid := getRemoteUUID(name)
		for i := range remotesConfig2.Remotes {
			if remotesConfig2.Remotes[i].Name == name {
				remotesConfig2.Remotes[i].UUID = uuid
				_ = SaveRemotesConfig(remotesConfig2)
				break
			}
		}
	}

	fmt.Printf("✅ Remote %s created and saved to .exohub/remotes\n", SuccessStyle.Render(name))
	return nil
}

func createExospaceRemoteTUI(ctx *tuiContext) error {
	name := "exospace"
	var rsyncURL string

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Remote name").
				Value(&name).
				Validate(func(s string) error {
					if s == "" {
						return fmt.Errorf("name is required")
					}
					if remoteExists(s) {
						return fmt.Errorf("remote '%s' already exists", s)
					}
					return nil
				}),
			huh.NewInput().
				Title("Rsync URL").
				Placeholder("rsync://host/path or /mnt/dir").
				Description("Local path or rsync URL").
				Value(&rsyncURL).
				Validate(func(s string) error {
					if s == "" {
						return fmt.Errorf("rsync URL is required")
					}
					return nil
				}),
		),
	).WithTheme(commandutil.ExoTheme()).WithKeyMap(commandutil.ExoKeyMap())

	if err := form.Run(); err != nil {
		return err
	}

	// Ensure we're in a git repo
	if !IsGitRepo() {
		fmt.Println("Not a git repository. Initializing...")
		if err := ensureGitRepo(); err != nil {
			return err
		}
	}

	// Ensure git-annex is initialized
	if err := ensureAnnexInitialized(); err != nil {
		return err
	}

	// Check binary availability
	if err := checkRemoteBinaries("exospace"); err != nil {
		return err
	}

	// Create the remote
	fmt.Printf("\nInitializing %s as rsync special remote\n", name)
	if err := runCommand([]string{
		"git", "annex", "initremote", name,
		"type=rsync", "rsyncurl=" + rsyncURL,
		"encryption=none",
	}); err != nil {
		return err
	}

	// Save to .exohub/remotes
	remotesConfig, err := LoadRemotesConfig()
	if err != nil {
		return err
	}

	// Get UUID for tracking
	uuid := getRemoteUUID(name)

	remotesConfig.AddRemote(RemoteConfig{
		Name:     name,
		Type:     "exospace",
		UUID:     uuid,
		RsyncURL: rsyncURL,
	})

	if err := SaveRemotesConfig(remotesConfig); err != nil {
		return err
	}

	fmt.Printf("✅ Remote %s created and saved to .exohub/remotes\n", SuccessStyle.Render(name))
	return nil
}

func createDriveRemoteTUI(ctx *tuiContext) error {
	name := "drive-export"
	var drivePath, trackingBranch string

	// Get current branch as default
	defaultBranch := defaultTrackingBranch()

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Remote name").
				Value(&name).
				Validate(func(s string) error {
					if s == "" {
						return fmt.Errorf("name is required")
					}
					if remoteExists(s) {
						return fmt.Errorf("remote '%s' already exists", s)
					}
					return nil
				}),
			huh.NewInput().
				Title("Drive path").
				Placeholder("/My Drive/datasets/my-project").
				Description("Google Drive folder path (will be created if it doesn't exist)").
				Value(&drivePath).
				Validate(func(s string) error {
					if s == "" {
						return fmt.Errorf("drive path is required")
					}
					return nil
				}),
			huh.NewInput().
				Title("Tracking branch (optional)").
				Placeholder(defaultBranch).
				Description("Branch or tag to track (e.g., main, refs/tags/v1)").
				Value(&trackingBranch),
		),
	).WithTheme(commandutil.ExoTheme()).WithKeyMap(commandutil.ExoKeyMap())

	if err := form.Run(); err != nil {
		return err
	}

	if trackingBranch == "" {
		trackingBranch = defaultBranch
	}

	// Ensure we're in a git repo
	if !IsGitRepo() {
		fmt.Println("Not a git repository. Initializing...")
		if err := ensureGitRepo(); err != nil {
			return err
		}
	}

	// Ensure git-annex is initialized
	if err := ensureAnnexInitialized(); err != nil {
		return err
	}

	// Check binary availability
	if err := checkRemoteBinaries("drive"); err != nil {
		return err
	}

	// Create the remote via createDriveRemoteFromConfig
	remote := RemoteConfig{
		Name:           name,
		Type:           "drive",
		DrivePath:      drivePath,
		TrackingBranch: trackingBranch,
	}

	fmt.Printf("\nInitializing %s as exporttree via drive external remote\n", name)
	if err := createDriveRemoteFromConfig(remote); err != nil {
		return err
	}

	// Save to .exohub/remotes
	remotesConfig, err := LoadRemotesConfig()
	if err != nil {
		return err
	}

	// Get UUID for tracking (captured by createDriveRemoteFromConfig)
	uuid := getRemoteUUID(name)
	remote.UUID = uuid

	remotesConfig.AddRemote(remote)

	if err := SaveRemotesConfig(remotesConfig); err != nil {
		return err
	}

	fmt.Printf("✅ Remote %s created and saved to .exohub/remotes\n", SuccessStyle.Render(name))
	return nil
}

// promptGrants asks the user whether to enable fine-grained permissions (S3 Access Grants).
// Defaults to true (use grants).
func promptGrants() (bool, error) {
	useGrants := true
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("Enable fine-grained permissions (S3 Access Grants)?").
				Description("Allows setting owners, viewers, and public access via .exohub/permissions").
				Affirmative("Yes").
				Negative("No").
				Value(&useGrants),
		),
	).WithTheme(commandutil.ExoTheme()).WithKeyMap(commandutil.ExoKeyMap())
	if err := form.Run(); err != nil {
		return false, err
	}
	return useGrants, nil
}

// providerOption is a selectable git provider entry shown in the init TUI.
type providerOption struct {
	name     string
	provider string
	host     string
}

// promptProviderConfig prompts for provider, host, and org configuration
func promptProviderConfig(localCtx *tuiContext) error {

	providers := defaultGitProviders()

	// Step 1: Determine provider if missing
	if localCtx.Provider == "" && localCtx.Host != "" {
		if detected, err := gitprovider.DetectProvider(context.Background(), localCtx.Host, ""); err == nil {
			localCtx.Provider = string(detected)
		}
	}

	// Step 1b: Select provider if still missing
	if localCtx.Provider == "" {
		var selectedIndex int
		providerOptions := make([]huh.Option[int], len(providers))
		for i, p := range providers {
			providerOptions[i] = huh.NewOption(p.name, i)
		}

		providerForm := huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[int]().
					Title("Select Git Provider").
					Options(providerOptions...).
					Value(&selectedIndex),
			),
		).WithTheme(commandutil.ExoTheme()).WithKeyMap(commandutil.ExoKeyMap())

		if err := providerForm.Run(); err != nil {
			return err
		}

		// Set provider and host based on selection
		selectedProvider := providers[selectedIndex]
		localCtx.Provider = selectedProvider.provider
		if localCtx.Host == "" {
			localCtx.Host = selectedProvider.host
		}
		fmt.Printf("✅ Selected provider: %s\n", SuccessStyle.Render(selectedProvider.name))
	}

	// Step 2: Prompt for organization if missing
	if localCtx.Org == "" {
		var org string
		hostLabel := localCtx.Host
		if hostLabel == "" {
			hostLabel = "the selected host"
		}

		orgForm := huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("Organization or Username").
					Description(fmt.Sprintf("Your repositories at %s", hostLabel)).
					Value(&org).
					Validate(func(s string) error {
						if s == "" {
							return fmt.Errorf("organization is required")
						}
						return nil
					}),
			),
		).WithTheme(commandutil.ExoTheme()).WithKeyMap(commandutil.ExoKeyMap())

		if err := orgForm.Run(); err != nil {
			return err
		}

		localCtx.Org = org
		fmt.Printf("✅ Organization: %s\n", SuccessStyle.Render(org))
	}

	return nil
}

// promptRepositoryName prompts for a repository name with TUI
func promptRepositoryName(ctx *tuiContext) (string, error) {
	defaultName := getRepoName()

	// Pre-fill with default name so user can just press Enter
	repoName := defaultName

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Repository name").
				Description(fmt.Sprintf("Repository URL: %s/%s/[repo-name]", strings.TrimSuffix(ctx.Host, "/"), ctx.Org)).
				Value(&repoName).
				Validate(func(s string) error {
					if s == "" {
						return fmt.Errorf("repository name is required")
					}
					// Extract repo name if user pasted a full URL
					extractedName := extractRepoNameFromURL(s, ctx.Org)
					// Basic validation: alphanumeric, dash, underscore, dot
					for _, ch := range extractedName {
						if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') ||
							(ch >= '0' && ch <= '9') || ch == '-' || ch == '_' || ch == '.') {
							return fmt.Errorf("invalid characters (use letters, numbers, dash, underscore, dot)")
						}
					}
					return nil
				}).
				CharLimit(100),
		),
	).WithTheme(commandutil.ExoTheme()).WithKeyMap(commandutil.ExoKeyMap())

	if err := form.Run(); err != nil {
		return "", err
	}

	// Extract repo name if user pasted a full URL
	repoName = extractRepoNameFromURL(repoName, ctx.Org)

	// Use default if empty after form (shouldn't happen since we pre-fill)
	if repoName == "" {
		repoName = defaultName
	}

	// Show final URL
	fmt.Printf("\n📦 Will create repository: %s\n", SuccessStyle.Render(fmt.Sprintf("%s/%s/%s", strings.TrimSuffix(ctx.Host, "/"), ctx.Org, repoName)))
	return repoName, nil
}

func promptTemplateSelection(templates []gitprovider.RepoTemplate) (*gitprovider.RepoTemplate, error) {
	var selectedIndex int
	options := make([]huh.Option[int], 0, len(templates))
	for i, tmpl := range templates {
		desc := strings.TrimSpace(tmpl.Description)
		if desc == "" {
			desc = "no description"
		}
		label := fmt.Sprintf("%s - %s", tmpl.Name, desc)
		options = append(options, huh.NewOption(label, i))
	}

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[int]().
				Title("Select repository template").
				Options(options...).
				Value(&selectedIndex),
		),
	).WithTheme(commandutil.ExoTheme()).WithKeyMap(commandutil.ExoKeyMap())

	if err := form.Run(); err != nil {
		return nil, err
	}

	return &templates[selectedIndex], nil
}

// extractRepoNameFromURL extracts just the repository name from a full URL or path
// Examples:
//   - "https://github.com/lelongs_roche/test-DS000000021" -> "test-DS000000021"
//   - "github.com/lelongs_roche/test-DS000000021" -> "test-DS000000021"
//   - "lelongs_roche/test-DS000000021" -> "test-DS000000021"
//   - "test-DS000000021" -> "test-DS000000021"
func extractRepoNameFromURL(input, org string) string {
	// Remove protocol if present
	input = strings.TrimPrefix(input, "https://")
	input = strings.TrimPrefix(input, "http://")

	// Split by slashes
	parts := strings.Split(input, "/")

	// If we have multiple parts, take the last one (the repo name)
	if len(parts) > 1 {
		return parts[len(parts)-1]
	}

	// Otherwise, return as-is
	return input
}
