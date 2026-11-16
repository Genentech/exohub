package init

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/dustin/go-humanize"
	"gopkg.in/yaml.v3"

	"github.com/Genentech/exohub/go/exo/commands/clone"
)

// routingRuleKnownKeys lists the only keys allowed in an annex.routing
// rule. Unlike the rest of .exohub/config, routing rules are always
// strict (see design "Routing configuration validation"): an unknown key
// here is a configuration mistake worth failing loudly on, independent of
// whatever leniency the rest of the document has.
var routingRuleKnownKeys = map[string]bool{
	"path": true, "larger_than": true, "smaller_than": true, "content": true, "to": true,
}

// RoutingRule is one entry in annex.routing (see design doc D2). It must
// have a required destination (To) and at least one match key (Path,
// LargerThan, SmallerThan, Content); all match keys present must match for
// the rule to match.
type RoutingRule struct {
	Path        string `yaml:"path,omitempty"`
	LargerThan  string `yaml:"larger_than,omitempty"`
	SmallerThan string `yaml:"smaller_than,omitempty"`
	Content     string `yaml:"content,omitempty"`
	To          string `yaml:"to"`

	// unknownKeys records any mapping keys found while decoding this rule
	// that are not in routingRuleKnownKeys. Decoding itself never fails on
	// them (so a lenient LoadExohubConfig can still load the rest of the
	// document); Validate turns them into an error at validation time,
	// which design D9 scopes to NewRouter/add/explain/reroute/MCP/doctor,
	// not the shared config loader.
	unknownKeys []string
}

// UnmarshalYAML decodes a routing rule and records any keys outside
// routingRuleKnownKeys, without failing the decode itself.
func (r *RoutingRule) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("routing rule must be a mapping")
	}
	var raw struct {
		Path        string `yaml:"path,omitempty"`
		LargerThan  string `yaml:"larger_than,omitempty"`
		SmallerThan string `yaml:"smaller_than,omitempty"`
		Content     string `yaml:"content,omitempty"`
		To          string `yaml:"to"`
	}
	if err := node.Decode(&raw); err != nil {
		return err
	}
	r.Path = raw.Path
	r.LargerThan = raw.LargerThan
	r.SmallerThan = raw.SmallerThan
	r.Content = raw.Content
	r.To = raw.To

	r.unknownKeys = nil
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i].Value
		if !routingRuleKnownKeys[key] {
			r.unknownKeys = append(r.unknownKeys, key)
		}
	}
	return nil
}

// ParsedRoutingRule is a RoutingRule after Validate has parsed its sizes,
// so callers (the routing engine) don't need to parse larger_than and
// smaller_than a second time.
type ParsedRoutingRule struct {
	Rule        RoutingRule
	HasLarger   bool
	LargerThan  uint64
	HasSmaller  bool
	SmallerThan uint64
}

// Validate checks a single annex.routing rule per design D9 and returns
// the rule with its sizes parsed. The caller is responsible for naming the
// rule's index in any returned error.
func (r RoutingRule) Validate() (ParsedRoutingRule, error) {
	if len(r.unknownKeys) > 0 {
		return ParsedRoutingRule{}, fmt.Errorf("unknown key %q", r.unknownKeys[0])
	}

	hasMatchKey := r.Path != "" || r.LargerThan != "" || r.SmallerThan != "" || r.Content != ""
	if !hasMatchKey {
		return ParsedRoutingRule{}, fmt.Errorf("rule has no match keys (path, larger_than, smaller_than, or content)")
	}
	if r.To == "" {
		return ParsedRoutingRule{}, fmt.Errorf("missing required key 'to'")
	}
	if r.To != "git" && r.To != "annex" {
		return ParsedRoutingRule{}, fmt.Errorf("invalid 'to' value %q (must be 'git' or 'annex')", r.To)
	}
	if r.Content != "" && r.Content != "text" && r.Content != "binary" {
		return ParsedRoutingRule{}, fmt.Errorf("invalid 'content' value %q (must be 'text' or 'binary')", r.Content)
	}
	if r.Path != "" && !doublestar.ValidatePattern(r.Path) {
		return ParsedRoutingRule{}, fmt.Errorf("invalid 'path' glob %q", r.Path)
	}

	parsed := ParsedRoutingRule{Rule: r}
	if r.LargerThan != "" {
		v, err := humanize.ParseBytes(r.LargerThan)
		if err != nil {
			return ParsedRoutingRule{}, fmt.Errorf("invalid 'larger_than' size %q: %w", r.LargerThan, err)
		}
		parsed.LargerThan, parsed.HasLarger = v, true
	}
	if r.SmallerThan != "" {
		v, err := humanize.ParseBytes(r.SmallerThan)
		if err != nil {
			return ParsedRoutingRule{}, fmt.Errorf("invalid 'smaller_than' size %q: %w", r.SmallerThan, err)
		}
		parsed.SmallerThan, parsed.HasSmaller = v, true
	}
	if parsed.HasLarger && parsed.HasSmaller && parsed.LargerThan >= parsed.SmallerThan {
		return ParsedRoutingRule{}, fmt.Errorf("'larger_than' (%s) must be less than 'smaller_than' (%s)", r.LargerThan, r.SmallerThan)
	}
	return parsed, nil
}

// AnnexConfig holds git-annex settings configurable via .exohub/config
type AnnexConfig struct {
	Thin        *bool         `yaml:"thin,omitempty"`
	AddUnlocked *bool         `yaml:"addunlocked,omitempty"`
	Routing     []RoutingRule `yaml:"routing,omitempty"`
}

// ValidateRouting validates every rule in Routing per design D9. Errors
// name the 1-based rule index and the offending key. It is not called by
// LoadExohubConfig: per design D9, routing is validated by NewRouter and
// by exo add/--explain/--reroute/the MCP add tool/exo doctor, not by the
// shared config loader used by exo init and profile refresh.
func (a *AnnexConfig) ValidateRouting() error {
	if a == nil {
		return nil
	}
	for i, rule := range a.Routing {
		if _, err := rule.Validate(); err != nil {
			return fmt.Errorf("annex.routing rule %d: %w", i+1, err)
		}
	}
	return nil
}

// ProvenanceConfig holds the provenance.require and provenance.collections
// settings from .exohub/config (design D14/D15). Both lists use the same
// glob syntax as annex.routing paths (pathglob.Match): require patterns
// match file paths, collections patterns end in "/" and match directory
// paths.
type ProvenanceConfig struct {
	Require     []string `yaml:"require,omitempty"`
	Collections []string `yaml:"collections,omitempty"`
}

// ExohubConfig represents .exohub/config file for shared repository settings
type ExohubConfig struct {
	Annex      *AnnexConfig      `yaml:"annex,omitempty"`
	Provenance *ProvenanceConfig `yaml:"provenance,omitempty"`
}

// LoadExohubConfig reads .exohub/config from the current directory.
// Returns nil, nil if the file doesn't exist.
func LoadExohubConfig() (*ExohubConfig, error) {
	return LoadExohubConfigAt(".")
}

// LoadExohubConfigAt reads shared configuration from the repository root.
func LoadExohubConfigAt(root string) (*ExohubConfig, error) {
	path := filepath.Join(root, ".exohub", "config")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read .exohub/config: %w", err)
	}
	return LoadExohubConfigFromBytes(data)
}

// LoadExohubConfigFromBytes parses .exohub/config content already read from
// some state (design D15 "Checked states"): the worktree, the index (`git
// show :.exohub/config`) or a ref tree (`git show <ref>:.exohub/config`).
// Unlike LoadExohubConfigAt, it never looks at the filesystem, so callers
// decide how "file does not exist at that state" is handled.
func LoadExohubConfigFromBytes(data []byte) (*ExohubConfig, error) {
	var cfg ExohubConfig
	if strings.TrimSpace(string(data)) != "" {
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("failed to parse .exohub/config: %w", err)
		}
	}

	return &cfg, nil
}

// routingConfigExample is entirely inactive until the owner opts in.
const routingConfigExample = `
# Routing rules for exo add (new files only).
# The first match wins (top-down), opposite to .gitignore / .gitattributes.
# Uncomment to opt in; if annex already exists above, put routing under it
# instead of adding a second annex key. Sizes use >= for larger_than.
# annex:
#   routing:
#     - path: "**/*.md"
#       to: git
#     - path: "**/*.csv"
#       larger_than: 1KB
#       to: annex
#     - path: "**/*.json"
#       larger_than: 1MB
#       to: annex
`

// ensureConfigTemplate creates a config only when none exists.
func ensureConfigTemplate() error {
	if err := os.MkdirAll(".exohub", 0755); err != nil {
		return fmt.Errorf("failed to create .exohub: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(".exohub", "config"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if os.IsExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to create .exohub/config: %w", err)
	}
	_, writeErr := f.WriteString(routingConfigExample)
	closeErr := f.Close()
	if writeErr != nil {
		return fmt.Errorf("failed to write .exohub/config: %w", writeErr)
	}
	return closeErr
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
	if _, err := os.Stat(path); os.IsNotExist(err) {
		data = append(data, routingConfigExample...)
	} else if err != nil {
		return fmt.Errorf("failed to stat .exohub/config: %w", err)
	}
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
	Name            string     `yaml:"name"`
	Type            string     `yaml:"type"`           // annex, export, import, exospace, artifactdb, drive
	UUID            string     `yaml:"uuid,omitempty"` // git-annex UUID (tracked for documentation)
	S3URL           string     `yaml:"s3url,omitempty"`
	Bucket          string     `yaml:"bucket,omitempty"`     // For S3 type remotes
	Prefix          string     `yaml:"prefix,omitempty"`     // For S3 type remotes (fileprefix)
	Datacenter      string     `yaml:"datacenter,omitempty"` // For S3 type remotes (AWS region)
	RsyncURL        string     `yaml:"rsyncurl,omitempty"`
	TrackingBranch  string     `yaml:"tracking_branch,omitempty"`
	Chunk           string     `yaml:"chunk,omitempty"`
	Include         stringList `yaml:"include,omitempty"`           // Glob patterns for filtering (import/annex only)
	Exclude         stringList `yaml:"exclude,omitempty"`           // Glob patterns for exclusion (import/annex only)
	ImportDir       string     `yaml:"import_dir,omitempty"`        // Destination directory for imported files (import only)
	Grants          bool       `yaml:"grants,omitempty"`            // Use S3 Access Grants for credentials (annex/export only)
	Permissions     string     `yaml:"permissions,omitempty"`       // Permission preset for exospace: public, group, private
	RsyncOptions    string     `yaml:"rsync_options,omitempty"`     // Custom rsync options for exospace (advanced)
	Host            string     `yaml:"host,omitempty"`              // Custom S3 host for import remotes (e.g., localhost:9000 for MinIO)
	Port            string     `yaml:"port,omitempty"`              // Custom S3 port for import remotes
	Protocol        string     `yaml:"protocol,omitempty"`          // S3 protocol for import remotes: http or https (default: https)
	Role            string     `yaml:"role,omitempty"`              // artifactdb remotes only: "atlas" (default) or "instance"
	URL             string     `yaml:"url,omitempty"`               // artifactdb remotes only: the Atlas this remote's bundle publishes to (role: atlas) or the ArtifactDB instance endpoint fed by .artifactdb/ (role: instance)
	GrantsAccountID string     `yaml:"grants_account_id,omitempty"` // Access Grants account id to request credentials from for this remote (falls back to the build default when unset)
	DataFrom        string     `yaml:"data_from,omitempty"`         // role: instance only — names the role: atlas remote that holds the data this instance delegates to
	PublishOn       string     `yaml:"publish_on,omitempty"`        // When to publish: "tag" (only on tags) or "always" (default)
	ProjectID       string     `yaml:"project_id,omitempty"`        // Custom project ID override (defaults to repo name)
	Mode            string     `yaml:"mode,omitempty"`              // Required for catalog remotes: "export" or "import" (artifactdb role: instance takes no mode)
	DrivePath       string     `yaml:"drive_path,omitempty"`        // Drive folder path (drive remotes only)
	Readonly        bool       `yaml:"readonly,omitempty"`          // Never write to this remote (get-only); set for remotes importing another project's storage (exo-cli#147)

	// Credentials is an opaque provider->setting map (e.g. `credentials:
	// {<provider>: {group: ...}}`) for remote-scoped credential settings that
	// only some builds interpret. Shared code never inspects provider or key
	// names; unknown providers are ignored by exo and flagged by exo doctor
	// (see checks_repo.go). A build that interprets a provider registers its
	// validation and wiring via validateRemoteCredentials/remoteCredentialGroup
	// below (see config_internal.go for the internal build).
	Credentials map[string]map[string]string `yaml:"credentials,omitempty"`

	// legacyKeys records the raw value of any `atlas_url`/`instance_url` key
	// found while decoding this remote, which no longer exist on this struct
	// (replaced by role/url). Decoding never fails on them so the rest of
	// .exohub/remotes can still be inspected; LoadRemotesConfig/
	// LoadRemotesConfigFromRef and Validate turn a non-empty legacyKeys into
	// a refusal before any command acts (see legacyKeyError).
	legacyKeys map[string]string
}

// UnmarshalYAML decodes a remote and records any `atlas_url`/`instance_url`
// keys found on it (see legacyKeys) without failing the decode itself.
func (r *RemoteConfig) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("remote must be a mapping")
	}
	type remoteConfigAlias RemoteConfig
	var raw remoteConfigAlias
	if err := node.Decode(&raw); err != nil {
		return err
	}
	*r = RemoteConfig(raw)

	r.legacyKeys = nil
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i].Value
		if key == "atlas_url" || key == "instance_url" {
			if r.legacyKeys == nil {
				r.legacyKeys = make(map[string]string)
			}
			r.legacyKeys[key] = node.Content[i+1].Value
		}
	}
	return nil
}

// legacyRemoteKeysErr wraps the task 1.3 refusal text so callers (e.g.
// runInit's --profile migration path, exo-cli#197) can tell via errors.Is
// that a LoadRemotesConfig failure is specifically this refusal, not some
// other parse/structural error.
type legacyRemoteKeysErr struct{ msg string }

func (e *legacyRemoteKeysErr) Error() string { return e.msg }

// errLegacyRemoteKeys is the sentinel errors.Is target for
// legacyRemoteKeysErr; it carries no message of its own.
var errLegacyRemoteKeys = &legacyRemoteKeysErr{}

func (e *legacyRemoteKeysErr) Is(target error) bool { return target == errLegacyRemoteKeys }

// legacyKeyError returns the task 1.3 refusal for a remote carrying
// atlas_url/instance_url, or nil if it carries neither: exo no longer
// accepts those keys, and refuses before any upload or API call, showing
// the equivalent role-split configuration and profileHint (the exo command
// that rewrites .exohub/remotes). This is the generic, offline example
// (used by Validate(); legacyRemoteKeysError below tries a profile-
// rendered one first).
func (r RemoteConfig) legacyKeyError(profileHint string) error {
	if !r.hasLegacyKeys() {
		return nil
	}
	return &legacyRemoteKeysErr{msg: legacyKeyErrorMessage(r, profileHint, r.genericLegacyExample())}
}

// genericLegacyExample renders the placeholder role-split YAML for a
// legacy remote, offline: real atlas_url/instance_url values but a
// placeholder instance bucket (the real one lives in the profile, not in
// anything a legacy remote's own fields carry).
func (r RemoteConfig) genericLegacyExample() string {
	var b strings.Builder
	if url, ok := r.legacyKeys["atlas_url"]; ok {
		fmt.Fprintf(&b, "  - name: %s\n    type: artifactdb\n    role: atlas\n    url: %s\n    s3url: %s\n", r.Name, url, r.S3URL)
	}
	if url, ok := r.legacyKeys["instance_url"]; ok {
		fmt.Fprintf(&b, "  - name: %s-instance\n    type: artifactdb\n    role: instance\n    url: %s\n    s3url: s3://<bucket>/<project_id>\n    data_from: %s\n", r.Name, url, r.Name)
	}
	return b.String()
}

// legacyKeyErrorMessage assembles the full refusal text for r, with
// example as the role-split YAML block (either genericLegacyExample's
// offline placeholder or legacyMigrationExampleFromProfile's real
// profile-rendered one).
func legacyKeyErrorMessage(r RemoteConfig, profileHint, example string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "remote %q: atlas_url/instance_url are no longer accepted in .exohub/remotes; exo refuses before any upload or API call.\n", r.Name)
	b.WriteString("Rewrite it with the equivalent role-split configuration:\n")
	b.WriteString(example)
	fmt.Fprintf(&b, "Rewrite .exohub/remotes with: %s\n", profileHint)
	return b.String()
}

// hasLegacyKeys reports whether r carries atlas_url or instance_url.
func (r RemoteConfig) hasLegacyKeys() bool { return len(r.legacyKeys) > 0 }

// legacyRemoteKeysError scans remotes for any carrying atlas_url/
// instance_url and returns the first refusal found, or nil. The migration
// hint names the repo's recorded profile (.exohub/profile.lock) when
// present, falling back to a placeholder. When a profile lock exists, the
// refusal's example YAML is the profile's own rendered role-split remotes
// (real names, real instance bucket, real data_from) instead of the
// generic placeholder — best-effort, so a fetch/render failure (offline,
// server down) falls back to the generic example rather than failing the
// refusal itself (exo-cli#197; see legacyRemoteKeysExampleFromProfile).
func legacyRemoteKeysError(remotes []RemoteConfig) error {
	profileHint := "exo init --profile <profile> --remote <remote>"
	var profileExamples map[string]string
	if lock, err := LoadProfileLock(); err == nil && lock != nil && lock.Profile != "" {
		profileHint = fmt.Sprintf("exo init --profile %s --remote <remote>", lock.Profile)
		profileExamples = legacyRemoteKeysExamplesFromProfile(lock.Profile, remotes)
	}
	for _, r := range remotes {
		if !r.hasLegacyKeys() {
			continue
		}
		example, ok := profileExamples[r.Name]
		if !ok {
			example = r.genericLegacyExample()
		}
		return &legacyRemoteKeysErr{msg: legacyKeyErrorMessage(r, profileHint, example)}
	}
	return nil
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

// validateRemoteCredentials optionally validates r.Credentials against a
// provider's schema. Nil in the OSS build, where exo doctor instead just
// flags unrecognized providers generically (see checks_repo.go). A build
// that interprets a credentials provider registers its validation here
// (see config_internal.go for the internal build).
var validateRemoteCredentials func(r *RemoteConfig) error

// remoteCredentialGroup optionally resolves a remote's provider-specific
// group override, folded into the git-annex special remote config as
// group=<g> (see reconfigureAnnexRemote/reconfigureExportRemote in
// command.go). Nil in the OSS build, where no provider is known.
var remoteCredentialGroup func(r RemoteConfig) string

// Validate checks if the remote configuration is valid
func (r *RemoteConfig) Validate() error {
	if err := r.legacyKeyError("exo init --profile <profile> --remote <remote>"); err != nil {
		return err
	}
	return r.validateFields()
}

// validateFields runs every structural check Validate does except the
// legacy-key refusal, so the lenient profile-migration path (see
// LoadRemotesConfigAllowLegacy) still catches an invalid role/mode/url on a
// remote that also happens to carry atlas_url/instance_url.
func (r *RemoteConfig) validateFields() error {
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
	case "artifactdb":
		switch r.Role {
		case "", "atlas":
			if r.Mode == "" {
				return fmt.Errorf("mode is required for catalog remote type '%s' (e.g. mode: export)", r.Type)
			}
			if r.Mode != "export" && r.Mode != "import" {
				return fmt.Errorf("invalid mode '%s' for remote type '%s' (must be: export or import)", r.Mode, r.Type)
			}
			if r.S3URL == "" {
				return fmt.Errorf("s3url is required for %s remotes", r.Type)
			}
		case "instance":
			if r.Mode != "" {
				return fmt.Errorf("remote %q: instance remotes take no mode (got %q)", r.Name, r.Mode)
			}
			if r.URL == "" {
				return fmt.Errorf("remote %q: url is required for role: instance remotes", r.Name)
			}
			if r.S3URL == "" {
				return fmt.Errorf("remote %q: s3url is required for role: instance remotes", r.Name)
			}
		default:
			return fmt.Errorf("remote %q: invalid role %q (must be: atlas or instance)", r.Name, r.Role)
		}
		if r.URL != "" && !strings.HasPrefix(r.URL, "https://") {
			return fmt.Errorf("remote %q: url must be HTTPS, got %q", r.Name, r.URL)
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

	if validateRemoteCredentials != nil {
		if err := validateRemoteCredentials(r); err != nil {
			return err
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
	if err := legacyRemoteKeysError(config.Remotes); err != nil {
		return nil, err
	}
	if err := validateRemotesConfig(config.Remotes); err != nil {
		return nil, err
	}

	return &config, nil
}

// validateRemotesConfig runs Validate on every remote, so any exo command
// that loads .exohub/remotes (not just exo init) catches an invalid
// role/mode/url artifactdb config — or any other remote's invalid config —
// before acting (split-remotes-by-role spec: role values, role↔mode rules,
// instance's required url/s3url, HTTPS url, enforced on load).
func validateRemotesConfig(remotes []RemoteConfig) error {
	for _, r := range remotes {
		if err := r.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// validateRemotesConfigFields runs validateFields (every structural check
// except the legacy-key refusal) on every remote. Used by
// LoadRemotesConfigAllowLegacy so a remote that's both legacy AND otherwise
// malformed (e.g. an invalid role) still fails loudly instead of flowing
// into the migration path.
func validateRemotesConfigFields(remotes []RemoteConfig) error {
	for _, r := range remotes {
		if err := r.validateFields(); err != nil {
			return err
		}
	}
	return nil
}

// LoadRemotesConfigAllowLegacy reads .exohub/remotes from the current
// directory like LoadRemotesConfig, but never refuses on a remote carrying
// atlas_url/instance_url (exo-cli#197): used only by the `exo init
// --profile` migration path (runInit's profileRequested branch), which
// needs to inspect a legacy remote's existing fields (uuid in particular)
// in order to convert it into the profile's role-split shape. Every other
// caller keeps using LoadRemotesConfig, so the refusal still applies
// everywhere else (sync/publish/non-profile init). Every other structural
// check (role/mode/url rules) still runs, so an otherwise-invalid remote
// that also happens to be legacy still fails loudly.
func LoadRemotesConfigAllowLegacy() (*RemotesConfig, error) {
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
	if err := validateRemotesConfigFields(config.Remotes); err != nil {
		return nil, err
	}

	return &config, nil
}

// LoadRemotesConfigFromRef reads .exohub/remotes as it was committed at ref
// (via `git show <ref>:.exohub/remotes`), instead of whatever is checked out
// on disk right now. Used by the exo-cli#176 stale-routing guard to compare
// a detached-HEAD ref's own routing against the repo's current/default
// routing. A ref with no .exohub/remotes (predates its introduction) yields
// an empty config, same as LoadRemotesConfig's missing-file case.
func LoadRemotesConfigFromRef(ref string) (*RemotesConfig, error) {
	out, err := exec.Command("git", "show", ref+":.exohub/remotes").Output()
	if err != nil {
		return &RemotesConfig{Remotes: []RemoteConfig{}}, nil
	}

	var config RemotesConfig
	if err := yaml.Unmarshal(out, &config); err != nil {
		return nil, fmt.Errorf("failed to parse %s:.exohub/remotes: %w", ref, err)
	}
	if err := legacyRemoteKeysError(config.Remotes); err != nil {
		return nil, err
	}
	if err := validateRemotesConfig(config.Remotes); err != nil {
		return nil, err
	}
	return &config, nil
}

// LoadRemotesConfigFromRefLenient reads .exohub/remotes as it was committed
// at ref, like LoadRemotesConfigFromRef, but never refuses on a legacy
// atlas_url/instance_url remote or any other Validate failure (exo-cli#196):
// a ref made before the role-split migration necessarily carries legacy
// keys, and per the --ref design (exo-cli#176) routing comes from the
// CURRENT .exohub/remotes, not the ref's own copy — the ref's file is
// consulted only to check that every catalog remote it referenced still has
// a home in the current routing (by name or UUID, via FindRemoteConfig),
// which only needs Name/Type/UUID, never url/s3url/role. A ref with no
// .exohub/remotes (predates its introduction) yields an empty config, same
// as LoadRemotesConfigFromRef's missing-file case.
func LoadRemotesConfigFromRefLenient(ref string) (*RemotesConfig, error) {
	out, err := exec.Command("git", "show", ref+":.exohub/remotes").Output()
	if err != nil {
		return &RemotesConfig{Remotes: []RemoteConfig{}}, nil
	}

	var config RemotesConfig
	if err := yaml.Unmarshal(out, &config); err != nil {
		return nil, fmt.Errorf("failed to parse %s:.exohub/remotes: %w", ref, err)
	}
	return &config, nil
}

// ResolveDefaultRoutingRef returns the ref whose .exohub/remotes is treated
// as "current" routing by the exo-cli#176 stale-routing guard: the remote's
// default branch when known (via origin/HEAD), falling back to a local
// main/master branch, and finally HEAD itself when neither can be
// determined (e.g. a single-branch, remote-less repo).
func ResolveDefaultRoutingRef() string {
	if out, err := exec.Command("git", "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD").Output(); err == nil {
		if ref := strings.TrimSpace(string(out)); ref != "" {
			return ref
		}
	}
	for _, b := range []string{"main", "master"} {
		if exec.Command("git", "show-ref", "--verify", "--quiet", "refs/heads/"+b).Run() == nil {
			return b
		}
	}
	return "HEAD"
}

// RoutingFieldsDiffer reports whether two RemoteConfig values differ in any
// field that determines where content/metadata gets published: type,
// s3url, bucket/prefix/datacenter, role, url, mode, grants, project_id.
// Content-only fields (include/exclude, import_dir, chunk, ...) are
// deliberately ignored. A nil on only one side counts as a difference (the
// remote exists on one side's routing but not the other's).
func RoutingFieldsDiffer(a, b *RemoteConfig) bool {
	if (a == nil) != (b == nil) {
		return true
	}
	if a == nil {
		return false
	}
	return a.Type != b.Type ||
		a.S3URL != b.S3URL ||
		a.Bucket != b.Bucket ||
		a.Prefix != b.Prefix ||
		a.Datacenter != b.Datacenter ||
		a.Role != b.Role ||
		a.URL != b.URL ||
		a.Mode != b.Mode ||
		a.Grants != b.Grants ||
		a.ProjectID != b.ProjectID
}

// FindRemoteConfig finds the remote in config that corresponds to target,
// matched by git-annex UUID when both have one (so a remote renamed across a
// routing migration is still recognized as the same remote), falling back to
// an exact name match. Returns nil when no match is found in either way.
func FindRemoteConfig(target RemoteConfig, config *RemotesConfig) *RemoteConfig {
	if config == nil {
		return nil
	}
	if target.UUID != "" {
		for _, c := range config.Remotes {
			if c.UUID != "" && c.UUID == target.UUID {
				cc := c
				return &cc
			}
		}
	}
	for _, c := range config.Remotes {
		if c.Name == target.Name {
			cc := c
			return &cc
		}
	}
	return nil
}

// ResolveDelegationAtlas resolves a role: instance remote's delegation
// Atlas (spec "Delegation Atlas resolution"): the remote named by
// data_from when set (it must exist and be role: atlas), otherwise the
// single role: atlas remote among all. With zero or several role: atlas
// remotes and no data_from, it refuses, listing the candidates.
func ResolveDelegationAtlas(instance RemoteConfig, all []RemoteConfig) (*RemoteConfig, error) {
	if instance.DataFrom != "" {
		for _, r := range all {
			if r.Name == instance.DataFrom {
				if r.Type != "artifactdb" || (r.Role != "" && r.Role != "atlas") {
					return nil, fmt.Errorf("remote %q: data_from %q is not a role: atlas artifactdb remote", instance.Name, instance.DataFrom)
				}
				rr := r
				return &rr, nil
			}
		}
		return nil, fmt.Errorf("remote %q: data_from %q names no remote in .exohub/remotes", instance.Name, instance.DataFrom)
	}

	var atlases []RemoteConfig
	for _, r := range all {
		if r.Type == "artifactdb" && (r.Role == "" || r.Role == "atlas") {
			atlases = append(atlases, r)
		}
	}
	switch len(atlases) {
	case 0:
		return nil, fmt.Errorf("remote %q: no role: atlas remote found to delegate to (set data_from or add one)", instance.Name)
	case 1:
		return &atlases[0], nil
	default:
		names := make([]string, len(atlases))
		for i, r := range atlases {
			names[i] = r.Name
		}
		return nil, fmt.Errorf("remote %q: ambiguous delegation Atlas, candidates are %s (set data_from)", instance.Name, strings.Join(names, ", "))
	}
}

// ValidateInstancePrefix enforces the spec "Instance prefix guard": a
// role: instance remote's s3url must be exactly s3://<bucket>/<projectID>
// (one path segment, equal to the remote's effective project id, not
// starting with "_"). Shared by exo and the catalog binary so both refuse
// the same way before credentials are requested.
func ValidateInstancePrefix(s3url, projectID string) error {
	const prefix = "s3://"
	if !strings.HasPrefix(s3url, prefix) {
		return fmt.Errorf("invalid s3url %q (must start with s3://)", s3url)
	}
	rest := strings.TrimPrefix(s3url, prefix)
	parts := strings.SplitN(rest, "/", 2)
	bucket := parts[0]
	if bucket == "" {
		return fmt.Errorf("invalid s3url %q (missing bucket)", s3url)
	}
	want := fmt.Sprintf("s3://%s/%s", bucket, projectID)
	if s3url != want {
		return fmt.Errorf("s3url %q is not allowed for an instance remote (expected %s)", s3url, want)
	}
	if strings.HasPrefix(projectID, "_") {
		return fmt.Errorf("s3url %q is not allowed for an instance remote (project id %q must not start with '_')", s3url, projectID)
	}
	return nil
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
