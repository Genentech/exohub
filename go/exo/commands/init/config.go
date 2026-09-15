package init

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/Genentech/exohub/go/exo/commands/clone"
)

// AnnexConfig holds git-annex settings configurable via .exohub/config
type AnnexConfig struct {
	Thin        *bool `yaml:"thin,omitempty"`
	AddUnlocked *bool `yaml:"addunlocked,omitempty"`
}

// ExohubConfig represents .exohub/config file for shared repository settings
type ExohubConfig struct {
	Annex *AnnexConfig `yaml:"annex,omitempty"`
}

// LoadExohubConfig reads .exohub/config from the current directory.
// Returns nil, nil if the file doesn't exist.
func LoadExohubConfig() (*ExohubConfig, error) {
	path := filepath.Join(".exohub", "config")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read .exohub/config: %w", err)
	}

	var cfg ExohubConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse .exohub/config: %w", err)
	}

	return &cfg, nil
}

// SaveExohubConfig writes .exohub/config to the current directory.
func SaveExohubConfig(cfg *ExohubConfig) error {
	if err := os.MkdirAll(".exohub", 0755); err != nil {
		return fmt.Errorf("failed to create .exohub directory: %w", err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	path := filepath.Join(".exohub", "config")
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write .exohub/config: %w", err)
	}

	return nil
}

// ParseRepoURL extracts host, org, and repo name from a git URL.
// Supports HTTPS (https://host/org/repo.git), SSH SCP (git@host:org/repo.git),
// and SSH URL (ssh://git@host/org/repo.git) formats.
func ParseRepoURL(rawURL string) (host, org, repo string, err error) {
	// SSH SCP format: git@host:org/repo.git
	if strings.HasPrefix(rawURL, "git@") {
		withoutPrefix := strings.TrimPrefix(rawURL, "git@")
		parts := strings.SplitN(withoutPrefix, ":", 2)
		if len(parts) != 2 || parts[1] == "" {
			return "", "", "", fmt.Errorf("invalid SSH URL format: %s", rawURL)
		}
		host = "https://" + sshHostToHTTPS(parts[0])
		path := strings.TrimSuffix(parts[1], ".git")
		pathParts := strings.Split(path, "/")
		if len(pathParts) < 2 {
			return "", "", "", fmt.Errorf("could not extract org/repo from URL: %s", rawURL)
		}
		repo = pathParts[len(pathParts)-1]
		org = strings.Join(pathParts[:len(pathParts)-1], "/")
		return host, org, repo, nil
	}

	// SSH URL format: ssh://git@host/org/repo.git
	if strings.HasPrefix(rawURL, "ssh://") {
		parsed, parseErr := url.Parse(rawURL)
		if parseErr != nil {
			return "", "", "", fmt.Errorf("invalid URL: %w", parseErr)
		}
		host = "https://" + sshHostToHTTPS(parsed.Hostname())
		path := strings.TrimPrefix(parsed.Path, "/")
		path = strings.TrimSuffix(path, ".git")
		pathParts := strings.Split(path, "/")
		if len(pathParts) < 2 {
			return "", "", "", fmt.Errorf("could not extract org/repo from URL: %s", rawURL)
		}
		repo = pathParts[len(pathParts)-1]
		org = strings.Join(pathParts[:len(pathParts)-1], "/")
		return host, org, repo, nil
	}

	// HTTPS format: https://host/org/repo.git
	parsed, parseErr := url.Parse(rawURL)
	if parseErr != nil {
		return "", "", "", fmt.Errorf("invalid URL: %w", parseErr)
	}
	if parsed.Host == "" {
		return "", "", "", fmt.Errorf("invalid URL (no host): %s", rawURL)
	}
	host = parsed.Scheme + "://" + parsed.Host
	path := strings.TrimPrefix(parsed.Path, "/")
	path = strings.TrimSuffix(path, ".git")
	if path == "" {
		return "", "", "", fmt.Errorf("could not extract org/repo from URL: %s", rawURL)
	}
	pathParts := strings.Split(path, "/")
	if len(pathParts) < 2 {
		return "", "", "", fmt.Errorf("could not extract org/repo from URL: %s (need at least org/repo)", rawURL)
	}
	repo = pathParts[len(pathParts)-1]
	org = strings.Join(pathParts[:len(pathParts)-1], "/")
	return host, org, repo, nil
}

// sshHostToHTTPS converts an SSH hostname to its HTTPS equivalent.
// e.g. "ssh.gitlab.com" → "gitlab.com"
func sshHostToHTTPS(hostname string) string {
	if strings.HasPrefix(hostname, "ssh.") {
		return strings.TrimPrefix(hostname, "ssh.")
	}
	return hostname
}

// RemoteConfig represents a single remote configuration
type RemoteConfig struct {
	Name           string     `yaml:"name"`
	Type           string     `yaml:"type"`           // annex, export, import, exospace, artifactdb, drive
	UUID           string     `yaml:"uuid,omitempty"` // git-annex UUID (tracked for documentation)
	S3URL          string     `yaml:"s3url,omitempty"`
	Bucket         string     `yaml:"bucket,omitempty"`     // For S3 type remotes
	Prefix         string     `yaml:"prefix,omitempty"`     // For S3 type remotes (fileprefix)
	Datacenter     string     `yaml:"datacenter,omitempty"` // For S3 type remotes (AWS region)
	RsyncURL       string     `yaml:"rsyncurl,omitempty"`
	TrackingBranch string     `yaml:"tracking_branch,omitempty"`
	Chunk          string     `yaml:"chunk,omitempty"`
	Include        stringList `yaml:"include,omitempty"`       // Glob patterns for filtering (import/annex only)
	Exclude        stringList `yaml:"exclude,omitempty"`       // Glob patterns for exclusion (import/annex only)
	ImportDir      string     `yaml:"import_dir,omitempty"`    // Destination directory for imported files (import only)
	Grants         bool       `yaml:"grants,omitempty"`        // Use S3 Access Grants for credentials (annex/export only)
	Permissions    string     `yaml:"permissions,omitempty"`   // Permission preset for exospace: public, group, private
	RsyncOptions   string     `yaml:"rsync_options,omitempty"` // Custom rsync options for exospace (advanced)
	Host           string     `yaml:"host,omitempty"`          // Custom S3 host for import remotes (e.g., localhost:9000 for MinIO)
	Port           string     `yaml:"port,omitempty"`          // Custom S3 port for import remotes
	Protocol       string     `yaml:"protocol,omitempty"`      // S3 protocol for import remotes: http or https (default: https)
	InstanceURL    string     `yaml:"instance_url,omitempty"`  // ArtifactDB instance URL (artifactdb remotes only)
	PublishOn      string     `yaml:"publish_on,omitempty"`    // When to publish: "tag" (only on tags) or "always" (default)
	ProjectID      string     `yaml:"project_id,omitempty"`    // Custom project ID override (defaults to repo name)
	Mode           string     `yaml:"mode,omitempty"`          // Required for catalog remotes: "export" or "import"
	DrivePath      string     `yaml:"drive_path,omitempty"`    // Drive folder path (drive remotes only)
}

// stringList is a YAML type that accepts both single strings and string lists
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

// Validate checks if the remote configuration is valid
func (r *RemoteConfig) Validate() error {
	if r.Name == "" {
		return fmt.Errorf("remote name is required")
	}

	switch r.Type {
	case "annex":
		if r.S3URL == "" {
			return fmt.Errorf("s3url is required for annex remotes")
		}
		if r.TrackingBranch != "" {
			return fmt.Errorf("tracking_branch is not allowed for annex remotes (only for export/import)")
		}
		if r.RsyncURL != "" {
			return fmt.Errorf("rsyncurl is not allowed for annex remotes (only for exospace)")
		}
		if r.Permissions != "" {
			return fmt.Errorf("permissions is only allowed for exospace remotes")
		}
		if r.RsyncOptions != "" {
			return fmt.Errorf("rsync_options is only allowed for exospace remotes")
		}
		if r.Host != "" || r.Port != "" || r.Protocol != "" {
			return fmt.Errorf("host/port/protocol are only allowed for import remotes")
		}
	case "export":
		if r.S3URL == "" {
			return fmt.Errorf("s3url is required for export remotes")
		}
		if r.Chunk != "" {
			return fmt.Errorf("chunk is not allowed for export remotes (only for annex)")
		}
		if r.RsyncURL != "" {
			return fmt.Errorf("rsyncurl is not allowed for export remotes (only for exospace)")
		}
		if r.Permissions != "" {
			return fmt.Errorf("permissions is only allowed for exospace remotes")
		}
		if r.RsyncOptions != "" {
			return fmt.Errorf("rsync_options is only allowed for exospace remotes")
		}
		if r.Host != "" || r.Port != "" || r.Protocol != "" {
			return fmt.Errorf("host/port/protocol are only allowed for import remotes")
		}
	case "import":
		if r.Bucket == "" && r.S3URL == "" {
			return fmt.Errorf("bucket or s3url is required for import remotes")
		}
		if r.Chunk != "" {
			return fmt.Errorf("chunk is not allowed for import remotes (only for annex)")
		}
		if r.RsyncURL != "" {
			return fmt.Errorf("rsyncurl is not allowed for import remotes (only for exospace)")
		}
		if r.Permissions != "" {
			return fmt.Errorf("permissions is only allowed for exospace remotes")
		}
		if r.RsyncOptions != "" {
			return fmt.Errorf("rsync_options is only allowed for exospace remotes")
		}
		if r.Protocol != "" && r.Protocol != "http" && r.Protocol != "https" {
			return fmt.Errorf("invalid protocol '%s' (must be: http or https)", r.Protocol)
		}
	case "exospace":
		if r.RsyncURL == "" {
			return fmt.Errorf("rsyncurl is required for exospace remotes")
		}
		if r.S3URL != "" {
			return fmt.Errorf("s3url is not allowed for exospace remotes (only for annex/export/import)")
		}
		if r.Chunk != "" {
			return fmt.Errorf("chunk is not allowed for exospace remotes (only for annex)")
		}
		if r.TrackingBranch != "" {
			return fmt.Errorf("tracking_branch is not allowed for exospace remotes (only for export/import)")
		}
		if r.Grants {
			return fmt.Errorf("grants is not allowed for exospace remotes (only for annex/export/import)")
		}
		// Validate permissions field
		if r.Permissions != "" && r.Permissions != "public" && r.Permissions != "group" && r.Permissions != "private" {
			return fmt.Errorf("invalid permissions value '%s' (must be: public, group, or private)", r.Permissions)
		}
		// Validate mutual exclusivity of permissions and rsync_options
		if r.Permissions != "" && r.RsyncOptions != "" {
			return fmt.Errorf("permissions and rsync_options are mutually exclusive for exospace remotes")
		}
		if r.Host != "" || r.Port != "" || r.Protocol != "" {
			return fmt.Errorf("host/port/protocol are only allowed for import remotes")
		}
		// Include/exclude patterns are allowed for exospace (regular annex remote using rsync)
	case "drive":
		if r.DrivePath == "" {
			return fmt.Errorf("drive_path is required for drive remotes")
		}
		if r.S3URL != "" {
			return fmt.Errorf("s3url is not allowed for drive remotes")
		}
		if r.RsyncURL != "" {
			return fmt.Errorf("rsyncurl is not allowed for drive remotes")
		}
		if r.Chunk != "" {
			return fmt.Errorf("chunk is not allowed for drive remotes")
		}
		if r.Grants {
			return fmt.Errorf("grants is not allowed for drive remotes")
		}
		if r.Permissions != "" {
			return fmt.Errorf("permissions is not allowed for drive remotes")
		}
		if r.RsyncOptions != "" {
			return fmt.Errorf("rsync_options is not allowed for drive remotes")
		}
		if r.Host != "" || r.Port != "" || r.Protocol != "" {
			return fmt.Errorf("host/port/protocol are not allowed for drive remotes")
		}
	default:
		// Catalog remote — requires mode field to build binary name: git-annex-remote-<type>-<mode>
		if r.Mode == "" {
			return fmt.Errorf("mode is required for catalog remote type '%s' (e.g. mode: export)", r.Type)
		}
		if r.Mode != "export" && r.Mode != "import" {
			return fmt.Errorf("invalid mode '%s' for remote type '%s' (must be: export or import)", r.Mode, r.Type)
		}
		if r.S3URL == "" {
			return fmt.Errorf("s3url is required for %s remotes", r.Type)
		}
	}

	return nil
}

// RemotesConfig represents .exohub/remotes file
type RemotesConfig struct {
	Remotes []RemoteConfig `yaml:"remotes"`
}

// normalizeHostURL ensures the host URL uses HTTPS protocol
func normalizeHostURL(host string) string {
	host = strings.TrimPrefix(host, "http://")
	host = strings.TrimPrefix(host, "https://")
	return "https://" + host
}

// LoadRemotesConfig reads .exohub/remotes from the current directory
func LoadRemotesConfig() (*RemotesConfig, error) {
	path := filepath.Join(".exohub", "remotes")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &RemotesConfig{Remotes: []RemoteConfig{}}, nil
		}
		return nil, fmt.Errorf("failed to read .exohub/remotes: %w", err)
	}

	var config RemotesConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse .exohub/remotes: %w", err)
	}

	return &config, nil
}

// SaveRemotesConfig writes .exohub/remotes to the current directory
func SaveRemotesConfig(config *RemotesConfig) error {
	if err := os.MkdirAll(".exohub", 0755); err != nil {
		return fmt.Errorf("failed to create .exohub directory: %w", err)
	}

	data, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal remotes config: %w", err)
	}

	path := filepath.Join(".exohub", "remotes")
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write .exohub/remotes: %w", err)
	}

	return nil
}

// AddRemote adds a remote to the configuration
func (rc *RemotesConfig) AddRemote(remote RemoteConfig) {
	// Check if remote already exists and update it
	for i, r := range rc.Remotes {
		if r.Name == remote.Name {
			rc.Remotes[i] = remote
			return
		}
	}
	// Otherwise append
	rc.Remotes = append(rc.Remotes, remote)
}

// IsGitRepo checks if current directory is a git repository
func IsGitRepo() bool {
	info, err := os.Stat(".git")
	return err == nil && info.IsDir()
}

// examplePresetsTemplate returns a template with example presets (commented out)
const examplePresetsTemplate = `# Example clone presets for exo clone command
#
# Presets allow users to clone with specific sparse checkout paths,
# git configuration, and clone depth settings.
#
# Usage:
#   exo clone <repo-url>                    # auto-discovers presets
#   exo clone --preset <name> <repo-url>    # use specific preset
#
# Note: .exohub/ directory is ALWAYS included automatically in every clone.
#       Do not list .exohub/ in sparse_paths.

# Uncomment and customize the examples below:

# presets:
#   # Minimal preset for CI/CD pipelines
#   - name: ci-minimal
#     description: Sparse checkout for CI/CD pipelines (config and scripts only)
#     depth: 1  # Shallow clone with depth 1
#     sparse_paths:
#       - Makefile
#       - scripts/
#       - .github/
#     git_config:
#       status.showUntrackedFiles: "no"
#
#   # Development preset with full repository
#   - name: development
#     description: Full repository for local development
#     depth: 0  # Full clone (0 means unlimited depth)
#     git_config:
#       core.untrackedCache: "true"
#       status.showUntrackedFiles: "normal"
#
#   # Data-only preset for data scientists
#   - name: data-only
#     description: Sparse checkout for data files only
#     depth: 1
#     sparse_paths:
#       - data/
#       - notebooks/
#     git_config:
#       status.showUntrackedFiles: "no"
#
#   # Documentation preset
#   - name: docs-only
#     description: Sparse checkout for documentation
#     depth: 1
#     sparse_paths:
#       - docs/
#       - README.md
#       - CONTRIBUTING.md
#
#   # Read-only preset for auditing/review
#   - name: readonly
#     description: Read-only configuration for auditing
#     depth: 1
#     sparse_paths:
#       - src/
#       - tests/
#     git_config:
#       core.fileMode: "false"
#       status.showUntrackedFiles: "no"
`

// createExamplePresetsIfNeeded creates .exohub/presets with examples if it doesn't exist
func createExamplePresetsIfNeeded() error {
	presetsPath := filepath.Join(".exohub", "presets")

	// Check if .exohub directory exists
	if _, err := os.Stat(".exohub"); os.IsNotExist(err) {
		// .exohub doesn't exist, nothing to do
		return nil
	}

	// Check if presets file already exists
	if _, err := os.Stat(presetsPath); err == nil {
		// File already exists, don't overwrite
		return nil
	}

	// Create presets file with examples
	if err := os.WriteFile(presetsPath, []byte(examplePresetsTemplate), 0644); err != nil {
		return fmt.Errorf("failed to create .exohub/presets: %w", err)
	}

	fmt.Println("✓ Created .exohub/presets with example presets (uncomment to use)")
	return nil
}

// defaultBundleManifest is the default .exohub/bundle content created by exo init.
const defaultBundleManifest = `presets:
  - name: default
    scope: all
`

// createDefaultBundleManifestIfNeeded creates .exohub/bundle with a default preset if it doesn't exist.
func createDefaultBundleManifestIfNeeded() error {
	bundlePath := filepath.Join(".exohub", "bundle")

	// Check if .exohub directory exists
	if _, err := os.Stat(".exohub"); os.IsNotExist(err) {
		return nil
	}

	// Check if bundle file already exists
	if _, err := os.Stat(bundlePath); err == nil {
		return nil
	}

	if err := os.WriteFile(bundlePath, []byte(defaultBundleManifest), 0644); err != nil {
		return fmt.Errorf("failed to create .exohub/bundle: %w", err)
	}

	fmt.Println("✓ Created .exohub/bundle with default preset")
	return nil
}

// displayClonePresetInfo shows clone preset information and checks for drift
func displayClonePresetInfo() error {
	// Load clone metadata
	metadata, err := clone.LoadCloneMetadata(".")
	if err != nil {
		return err
	}

	// No metadata means this wasn't cloned with exo clone
	if metadata == nil {
		return nil
	}

	// Display basic preset info
	fmt.Println("==> Clone Preset Information")
	fmt.Printf("Preset: %s\n", metadata.Preset)

	// Parse and format the cloned_at timestamp
	clonedAt, err := time.Parse(time.RFC3339, metadata.ClonedAt)
	if err == nil {
		fmt.Printf("Cloned: %s\n", clonedAt.Format("2006-01-02"))
	} else {
		fmt.Printf("Cloned: %s\n", metadata.ClonedAt)
	}

	// Show commit SHA (short form) and branch
	shortCommit := metadata.Commit
	if len(shortCommit) > 7 {
		shortCommit = shortCommit[:7]
	}
	fmt.Printf("Preset commit: %s (%s)\n", shortCommit, metadata.Branch)

	// Check for drift
	if err := checkPresetDrift(metadata); err != nil {
		// Display the drift warning but don't fail
		fmt.Printf("\n%s\n", err.Error())
	}

	fmt.Println() // Add blank line after preset info
	return nil
}

// checkPresetDrift checks if the preset has changed since clone time
func checkPresetDrift(metadata *clone.CloneMetadata) error {
	// Load current presets config
	presetsConfig, err := clone.LoadPresetsConfig(".")
	if err != nil {
		return err
	}

	// Check if presets file is missing
	if presetsConfig == nil {
		return fmt.Errorf("⚠️  Warning: Preset file .exohub/presets is missing\n    Cloned with preset: %s (commit %s)\n    \n    Either restore the file or re-clone with:\n      exo clone --preset %s %s <new-location>",
			metadata.Preset, metadata.Commit[:7], metadata.Preset, metadata.RepoURL)
	}

	// Check if the preset still exists
	currentPreset := presetsConfig.GetPresetByName(metadata.Preset)
	if currentPreset == nil && metadata.Preset != "full-clone" {
		availablePresets := ""
		for _, p := range presetsConfig.Presets {
			if availablePresets != "" {
				availablePresets += ", "
			}
			availablePresets += p.Name
		}
		return fmt.Errorf("⚠️  Warning: Preset '%s' is no longer defined\n    Cloned with commit: %s\n    \n    Available presets: %s\n    Consider re-cloning with a different preset",
			metadata.Preset, metadata.Commit[:7], availablePresets)
	}

	// Get the preset content at clone time
	cmd := exec.Command("git", "show", fmt.Sprintf("%s:.exohub/presets", metadata.Commit))
	cloneTimePresets, err := cmd.Output()
	if err != nil {
		// Commit might not exist anymore (shallow clone fetched more history)
		// This is not an error, just can't check drift
		return nil
	}

	// Get the current preset content
	cmd = exec.Command("git", "show", "HEAD:.exohub/presets")
	currentPresets, err := cmd.Output()
	if err != nil {
		return nil
	}

	// Compare the two
	if string(cloneTimePresets) != string(currentPresets) {
		shortCommit := metadata.Commit
		if len(shortCommit) > 7 {
			shortCommit = shortCommit[:7]
		}

		cmd := exec.Command("git", "rev-parse", "HEAD")
		currentCommit, err := cmd.Output()
		currentShort := ""
		if err == nil {
			current := strings.TrimSpace(string(currentCommit))
			if len(current) > 7 {
				currentShort = current[:7]
			} else {
				currentShort = current
			}
		}

		return fmt.Errorf("⚠️  Warning: Clone preset has changed since clone time\n\n    Cloned with commit:  %s\n    Current commit:      %s\n\n    To see what changed:\n      git show %s:.exohub/presets\n      git show %s:.exohub/presets\n      git diff %s:%s -- .exohub/presets\n\n    To apply changes manually:\n      git fetch --unshallow              (if depth changed)\n      git sparse-checkout add <path>     (for new paths)\n      git sparse-checkout set <paths>    (to reconfigure)\n      git config <key> <value>           (for new config)\n\n    Or, to apply the updated preset from scratch:\n      exo clone --preset %s %s <new-location>",
			shortCommit, currentShort,
			shortCommit, currentShort,
			shortCommit, currentShort,
			metadata.Preset, metadata.RepoURL)
	}

	return nil
}
