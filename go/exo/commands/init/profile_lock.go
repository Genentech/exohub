package init

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ProfileLock records which profile (and prompt answers) a repo was
// initialized with, so a later `exo init --profile <name> --remote
// <remote-name>` refresh or `--dry-run --json` drift check can re-render
// the profile's templates without re-prompting the user (exo-cli#164). Folder
// and UnixID record the resolved environment-derived template variables so
// later runs never recompute (and thus never silently change) them based on
// the cwd or on which user happens to run the command (exo-cli#167).
type ProfileLock struct {
	Profile string                 `yaml:"profile"`
	Hash    string                 `yaml:"hash"`
	Folder  string                 `yaml:"folder,omitempty"`
	UnixID  string                 `yaml:"unix_id,omitempty"`
	Answers map[string]interface{} `yaml:"answers,omitempty"`
}

// ProfileSpecHash returns a stable content hash of a profile's vars,
// prompts, and templates. It is used as a cheap "version" to detect when a
// profile has changed since a repo was initialized or last refreshed; it is
// informational only and never gates the refresh/drift flows.
func ProfileSpecHash(spec *ProfileSpec) string {
	data, _ := json.Marshal(struct {
		Vars      map[string]string `json:"vars"`
		Prompts   []PromptSpec      `json:"prompts"`
		Templates map[string]string `json:"templates"`
	}{spec.Vars, spec.Prompts, spec.Templates})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func profileLockPath() string {
	return filepath.Join(".exohub", "profile.lock")
}

// LoadProfileLock reads .exohub/profile.lock from the current directory.
// Returns nil, nil if the file doesn't exist (e.g. repos initialized before
// this lock existed, or without --profile).
func LoadProfileLock() (*ProfileLock, error) {
	data, err := os.ReadFile(profileLockPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read .exohub/profile.lock: %w", err)
	}
	var lock ProfileLock
	if err := yaml.Unmarshal(data, &lock); err != nil {
		return nil, fmt.Errorf("failed to parse .exohub/profile.lock: %w", err)
	}
	return &lock, nil
}

// SaveProfileLock writes .exohub/profile.lock to the current directory.
func SaveProfileLock(lock *ProfileLock) error {
	if err := os.MkdirAll(".exohub", 0755); err != nil {
		return fmt.Errorf("failed to create .exohub directory: %w", err)
	}
	data, err := yaml.Marshal(lock)
	if err != nil {
		return fmt.Errorf("failed to marshal .exohub/profile.lock: %w", err)
	}
	if err := os.WriteFile(profileLockPath(), data, 0644); err != nil {
		return fmt.Errorf("failed to write .exohub/profile.lock: %w", err)
	}
	return nil
}
