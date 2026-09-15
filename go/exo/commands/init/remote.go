package init

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Genentech/exohub/go/exo/configdir"
)

var (
	remoteType           string
	remoteName           string
	remoteS3URL          string
	remoteRsyncURL       string
	remoteTrackingBranch string
	remoteChunk          string
	remoteGrants         bool
)

func NewRemoteCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remote",
		Short: "Create and configure git-annex remotes",
		Long: `Create git-annex remotes with declarative configuration or interactive TUI.

Examples:
  exo init remote --type annex --name s3-backup --s3url s3://bucket/prefix
  exo init remote --type export --name s3-export --s3url s3://bucket/prefix
  exo init remote --type import --name s3-import --s3url s3://bucket/prefix
  exo init remote --type exospace --name shared-data --rsyncurl /mnt/shared
  exo init remote --type drive --name my-drive
  exo init remote  # Interactive TUI mode

Exospace remotes support permission presets via .exohub/remotes:
  permissions: public   - World-readable files (--chmod=ugo=rwX,Do+t,Fo-w)
  permissions: group    - Group writable, others read-only
  permissions: private  - Owner-only access

Or use rsync_options for custom rsync flags (mutually exclusive with permissions).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return fmt.Errorf("unexpected argument: %s", args[0])
			}

			// Interactive TUI mode if no flags provided
			if remoteType == "" && remoteName == "" {
				return runInteractiveRemoteSetup()
			}

			// Declarative mode with flags
			if remoteType == "" {
				return fmt.Errorf("--type is required when using declarative mode")
			}
			if remoteName == "" {
				return fmt.Errorf("--name is required when using declarative mode")
			}

			return runDeclarativeRemoteSetup(remoteType, remoteName)
		},
	}

	cmd.Flags().StringVar(&remoteType, "type", "", "Remote type (annex, export, import, exospace, drive)")
	cmd.Flags().StringVar(&remoteName, "name", "", "Remote name")
	cmd.Flags().StringVar(&remoteS3URL, "s3url", "", "S3 URL (for annex/export/import types)")
	cmd.Flags().StringVar(&remoteRsyncURL, "rsyncurl", "", "Rsync URL (for exospace type)")
	cmd.Flags().StringVar(&remoteTrackingBranch, "tracking-branch", "", "Tracking branch (for export/import types)")
	cmd.Flags().StringVar(&remoteChunk, "chunk", "1GiB", "Chunk size (for annex type)")
	cmd.Flags().BoolVar(&remoteGrants, "grants", false, "Enable fine-grained S3 Access Grants permissions")

	return cmd
}

func runDeclarativeRemoteSetup(rtype, rname string) error {
	// Validate type
	if rtype != "annex" && rtype != "export" && rtype != "import" && rtype != "exospace" && rtype != "artifactdb" && rtype != "drive" {
		return fmt.Errorf("unsupported remote type: %s (must be annex, export, import, exospace, artifactdb, or drive)", rtype)
	}

	// Check binary availability
	if err := checkRemoteBinaries(rtype); err != nil {
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

	// Check if remote already exists
	if remoteExists(rname) {
		fmt.Printf("Remote '%s' already exists. Skipping creation.\n", rname)
		return nil
	}

	// Create the remote based on type
	var remote RemoteConfig
	var err error

	switch rtype {
	case "annex":
		remote, err = createAnnexRemoteInteractive(rname)
	case "export":
		remote, err = createExportRemoteInteractive(rname)
	case "import":
		remote, err = createImportRemoteInteractive(rname)
	case "exospace":
		remote, err = createExospaceRemoteInteractive(rname)
	case "artifactdb":
		remote, err = createArtifactDBRemoteInteractive(rname)
	case "drive":
		remote, err = createDriveRemoteInteractive(rname)
	}

	if err != nil {
		return err
	}

	// For drive remotes, delegate setup to createDriveRemoteFromConfig
	if rtype == "drive" {
		if err := createDriveRemoteFromConfig(remote); err != nil {
			return err
		}
	}

	// Save to .exohub/remotes
	remotesConfig, err := LoadRemotesConfig()
	if err != nil {
		return err
	}

	remotesConfig.AddRemote(remote)
	if err := SaveRemotesConfig(remotesConfig); err != nil {
		return err
	}

	fmt.Printf("✅ Remote %s created and saved to .exohub/remotes\n", SuccessStyle.Render(rname))

	// Sync grants immediately so the user doesn't need to re-run exo init
	if remote.Grants && remote.S3URL != "" {
		if err := syncGrantsForRemote(remote.S3URL); err != nil {
			fmt.Printf("⚠️ %v\n", err)
		}
	}

	return nil
}

// checkRemoteBinaries verifies required binaries are available
func checkRemoteBinaries(rtype string) error {
	switch rtype {
	case "annex", "export":
		if !commandExists("s5cmd") {
			return fmt.Errorf("s5cmd binary not found. Install it first: https://github.com/peak/s5cmd")
		}
	case "import":
		// Import remotes use built-in git-annex S3 type, no external binary needed
		return nil
	case "exospace":
		if !commandExists("rsync") {
			return fmt.Errorf("rsync binary not found. Install it first (e.g., apt install rsync)")
		}
	case "artifactdb":
		if !commandExists("git-annex-remote-artifactdb-export") {
			return fmt.Errorf("git-annex-remote-artifactdb-export binary not found. Install it first")
		}
	case "drive":
		if !commandExists("git-annex-remote-drive") {
			return fmt.Errorf("git-annex-remote-drive binary not found. Install it first")
		}
	}
	return nil
}

// commandExists checks if a command is available in PATH
func commandExists(cmd string) bool {
	_, err := command("which", cmd).Output()
	return err == nil
}

// ensureAWSCredentials checks if AWS credentials are available and sets them from the credentials file if needed
func ensureAWSCredentials() error {
	// Check if credentials are already set
	if os.Getenv("AWS_ACCESS_KEY_ID") != "" && os.Getenv("AWS_SECRET_ACCESS_KEY") != "" {
		return nil // Already set, don't override
	}

	// Use exohub profile by default
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

func createAnnexRemoteInteractive(name string) (RemoteConfig, error) {
	s3url := remoteS3URL
	if s3url == "" {
		var err error
		s3url, err = askNonEmpty("Enter s3url for "+name+" (e.g., s3://bucket/prefix/_annex):", "^s3://.+$")
		if err != nil {
			return RemoteConfig{}, err
		}
	}

	chunk := remoteChunk
	if chunk == "" {
		chunk = "1GiB"
	}

	fmt.Printf("Initializing %s with chunk=%s via s5cmd external remote\n", name, chunk)
	initArgs := []string{
		"git", "annex", "initremote", name,
		"type=external", "externaltype=s5cmd", "encryption=none",
		"s3url=" + s3url, "chunk=" + chunk,
	}
	if remoteGrants {
		initArgs = append(initArgs, "grants=true")
	}
	if err := runCommand(initArgs); err != nil {
		return RemoteConfig{}, err
	}

	// Get UUID for tracking
	uuid := getRemoteUUID(name)

	rc := RemoteConfig{
		Name:   name,
		Type:   "annex",
		UUID:   uuid,
		S3URL:  s3url,
		Chunk:  chunk,
		Grants: remoteGrants,
	}

	if remoteGrants {
		if _, err := loadOrCreatePermissions(); err != nil {
			fmt.Printf("⚠️  Could not create permissions file: %v\n", err)
		}
	}

	return rc, nil
}

func createExportRemoteInteractive(name string) (RemoteConfig, error) {
	s3url := remoteS3URL
	if s3url == "" {
		var err error
		s3url, err = askNonEmpty("Enter s3url for "+name+" (e.g., s3://bucket/prefix/_export):", "^s3://.+$")
		if err != nil {
			return RemoteConfig{}, err
		}
	}

	fmt.Println("Initializing " + name + " as exporttree via s5cmd external remote")
	initArgs := []string{
		"git", "annex", "initremote", name,
		"type=external", "externaltype=s5cmd", "encryption=none",
		"s3url=" + s3url, "exporttree=yes",
	}
	if remoteGrants {
		initArgs = append(initArgs, "grants=true")
	}
	if err := runCommand(initArgs); err != nil {
		return RemoteConfig{}, err
	}

	// Enable remote again to ensure settings are applied
	_ = runCommand([]string{"git", "annex", "enableremote", name, "s3url=" + s3url, "exporttree=yes"})

	// Ask for tracking branch if not provided via flag
	trackingBranch := remoteTrackingBranch
	if trackingBranch == "" {
		tbDefault := defaultTrackingBranch()
		tb, err := askWithDefault("Enter tracking branch or tag for "+name+" (e.g., main or refs/tags/v1):", tbDefault)
		if err != nil {
			return RemoteConfig{}, err
		}
		trackingBranch = tb
	}

	if trackingBranch != "" {
		_ = runCommand([]string{"git", "config", fmt.Sprintf("remote.%s.annex-tracking-branch", name), trackingBranch})
		fmt.Printf("Set remote.%s.annex-tracking-branch to '%s'\n", name, trackingBranch)
	}

	ensureRemoteConfigS3URL(name, s3url)

	// Get UUID for tracking
	uuid := getRemoteUUID(name)

	rc := RemoteConfig{
		Name:           name,
		Type:           "export",
		UUID:           uuid,
		S3URL:          s3url,
		TrackingBranch: trackingBranch,
		Grants:         remoteGrants,
	}

	if remoteGrants {
		if _, err := loadOrCreatePermissions(); err != nil {
			fmt.Printf("⚠️  Could not create permissions file: %v\n", err)
		}
	}

	return rc, nil
}

// parseS3URL parses an s3:// URL into bucket and prefix components
func parseS3URL(s3url string) (bucket, prefix string, err error) {
	if !strings.HasPrefix(s3url, "s3://") {
		return "", "", fmt.Errorf("s3url must start with s3://")
	}

	// Remove s3:// prefix
	path := strings.TrimPrefix(s3url, "s3://")

	// Split into bucket and prefix
	parts := strings.SplitN(path, "/", 2)
	bucket = parts[0]
	if bucket == "" {
		return "", "", fmt.Errorf("bucket name cannot be empty")
	}

	if len(parts) > 1 {
		prefix = parts[1]
		// Normalize prefix: remove leading slash, ensure trailing slash
		prefix = strings.TrimPrefix(prefix, "/")
		if prefix != "" && !strings.HasSuffix(prefix, "/") {
			prefix += "/"
		}
	}

	return bucket, prefix, nil
}

func createImportRemoteInteractive(name string) (RemoteConfig, error) {
	// Ensure AWS credentials are available for git-annex S3 type
	if err := ensureAWSCredentials(); err != nil {
		return RemoteConfig{}, fmt.Errorf("AWS credentials not available: %w", err)
	}

	var bucket, prefix, datacenter string

	// Check if s3url flag was provided
	if remoteS3URL != "" {
		// Parse s3url into bucket and prefix
		var err error
		bucket, prefix, err = parseS3URL(remoteS3URL)
		if err != nil {
			return RemoteConfig{}, err
		}
	} else {
		// Ask for bucket
		var err error
		bucket, err = askNonEmpty("Enter S3 bucket name:", "")
		if err != nil {
			return RemoteConfig{}, err
		}

		// Ask for prefix (fileprefix in git-annex S3)
		prefix, err = askWithDefault("Enter prefix (path within bucket, optional):", "")
		if err != nil {
			return RemoteConfig{}, err
		}
		// Normalize prefix: remove leading slash if present, ensure trailing slash
		prefix = strings.TrimPrefix(prefix, "/")
		if prefix != "" && !strings.HasSuffix(prefix, "/") {
			prefix += "/"
		}
	}

	// Ask for datacenter
	datacenter, err := askWithDefault("Enter AWS region/datacenter:", "us-west-2")
	if err != nil {
		return RemoteConfig{}, err
	}

	// Ask for tracking branch
	trackingBranch := remoteTrackingBranch
	if trackingBranch == "" {
		tbDefault := defaultTrackingBranch()
		tb, err := askWithDefault("Enter tracking branch or tag for "+name+" (e.g., main or refs/tags/v1):", tbDefault)
		if err != nil {
			return RemoteConfig{}, err
		}
		trackingBranch = tb
	}

	fmt.Printf("Initializing %s as S3 import remote (bucket: %s, prefix: %s)\n", name, bucket, prefix)

	// Build RemoteConfig to save even if git-annex command fails
	remoteConfig := RemoteConfig{
		Name:           name,
		Type:           "import",
		Bucket:         bucket,
		Prefix:         prefix,
		Datacenter:     datacenter,
		TrackingBranch: trackingBranch,
	}

	// Build base args for both versioning attempts
	protocol := "https"
	if remoteConfig.Protocol != "" {
		protocol = remoteConfig.Protocol
	}
	baseArgs := []string{
		"git", "annex", "initremote", name,
		"type=S3",
		"bucket=" + bucket,
		"encryption=none",
		"protocol=" + protocol,
		"importtree=yes",
		"datacenter=" + datacenter,
	}
	if prefix != "" {
		baseArgs = append(baseArgs, "fileprefix="+prefix)
	}
	if remoteConfig.Host != "" {
		baseArgs = append(baseArgs, "host="+remoteConfig.Host)
	}
	if remoteConfig.Port != "" {
		baseArgs = append(baseArgs, "port="+remoteConfig.Port)
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
		strings.Contains(stderrBuf.String(), "GetBucketVersioning") ||
		strings.Contains(stderrBuf.String(), "PutBucketVersioning")) {
		// Retry without versioning if we get 403 (permission denied) or versioning errors
		fmt.Println("⚠️  Versioning not accessible (403), retrying without versioning...")
		if err = runCommand(baseArgs); err != nil {
			return remoteConfig, err
		}
	} else if err != nil {
		return remoteConfig, err
	}

	// Set tracking branch
	if trackingBranch != "" {
		_ = runCommand([]string{"git", "config", fmt.Sprintf("remote.%s.annex-tracking-branch", name), trackingBranch})
		fmt.Printf("Set remote.%s.annex-tracking-branch to '%s'\n", name, trackingBranch)
	}

	// Get UUID for tracking
	uuid := getRemoteUUID(name)
	remoteConfig.UUID = uuid

	return remoteConfig, nil
}

func createExospaceRemoteInteractive(name string) (RemoteConfig, error) {
	rsyncURL := remoteRsyncURL
	if rsyncURL == "" {
		var err error
		rsyncURL, err = askNonEmpty("Enter rsyncurl for "+name+" (e.g., rsync://host/path or /mnt/dir):", "")
		if err != nil {
			return RemoteConfig{}, err
		}
	}

	fmt.Println("Initializing " + name + " as rsync special remote")
	if err := runCommand([]string{
		"git", "annex", "initremote", name,
		"type=rsync", "rsyncurl=" + rsyncURL,
		"encryption=none",
	}); err != nil {
		return RemoteConfig{}, err
	}

	// Get UUID for tracking
	uuid := getRemoteUUID(name)

	return RemoteConfig{
		Name:     name,
		Type:     "exospace",
		UUID:     uuid,
		RsyncURL: rsyncURL,
	}, nil
}

func createArtifactDBRemoteInteractive(name string) (RemoteConfig, error) {
	s3url := remoteS3URL
	if s3url == "" {
		// Try to derive from first existing annex remote
		suggestion := ""
		if remotesConfig, err := LoadRemotesConfig(); err == nil {
			for _, r := range remotesConfig.Remotes {
				if r.Type == "annex" && r.S3URL != "" {
					suggestion = strings.TrimSuffix(r.S3URL, "/_annex") + "/_catalog"
					break
				}
			}
		}
		prompt := "Enter s3url for " + name
		if suggestion != "" {
			prompt += " (default: " + suggestion + ")"
		} else {
			prompt += " (e.g., s3://bucket/prefix/_catalog)"
		}
		prompt += ":"
		var err error
		s3url, err = askNonEmpty(prompt, "^s3://.+$")
		if err != nil {
			return RemoteConfig{}, err
		}
	}

	externalType := "artifactdb-export"
	fmt.Printf("Initializing %s as exporttree via %s external remote\n", name, externalType)
	initArgs := []string{
		"git", "annex", "initremote", name,
		"type=external", "externaltype=" + externalType, "encryption=none",
		"s3url=" + s3url, "exporttree=yes",
	}
	if remoteGrants {
		initArgs = append(initArgs, "grants=true")
	}
	if err := runCommand(initArgs); err != nil {
		return RemoteConfig{}, err
	}

	_ = runCommand([]string{"git", "annex", "enableremote", name, "s3url=" + s3url, "exporttree=yes"})

	ensureRemoteConfigS3URL(name, s3url)

	// Set preferred content
	wanted := "include=.exohub/bundles/* or include=.artifactdb/*"
	_ = runCommand([]string{"git", "annex", "wanted", name, wanted})
	fmt.Printf("Set preferred content for '%s': %s\n", name, wanted)

	uuid := getRemoteUUID(name)

	rc := RemoteConfig{
		Name:   name,
		Type:   "artifactdb",
		UUID:   uuid,
		S3URL:  s3url,
		Grants: remoteGrants,
		Mode:   "export",
	}

	if remoteGrants {
		if _, err := loadOrCreatePermissions(); err != nil {
			fmt.Printf("⚠️  Could not create permissions file: %v\n", err)
		}
	}

	return rc, nil
}

func createDriveRemoteInteractive(name string) (RemoteConfig, error) {
	// Ask for drive_path (required) -- the Drive folder path
	drivePath, err := askNonEmpty("Enter drive_path for "+name+" (e.g., /My Drive/datasets/gwasdb):", "")
	if err != nil {
		return RemoteConfig{}, err
	}

	// Ask for tracking branch
	trackingBranch := remoteTrackingBranch
	if trackingBranch == "" {
		tbDefault := defaultTrackingBranch()
		tb, err := askWithDefault("Enter tracking branch for "+name+" (e.g., main):", tbDefault)
		if err != nil {
			return RemoteConfig{}, err
		}
		trackingBranch = tb
	}

	return RemoteConfig{
		Name:           name,
		Type:           "drive",
		DrivePath:      drivePath,
		TrackingBranch: trackingBranch,
	}, nil
}

func runInteractiveRemoteSetup() error {
	return runInteractiveTUI()
}
