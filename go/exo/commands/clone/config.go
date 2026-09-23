package clone

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ClonePreset represents a single clone preset configuration
type ClonePreset struct {
	Name        string            `yaml:"name"`
	Description string            `yaml:"description,omitempty"`
	SparsePaths []string          `yaml:"sparse_paths,omitempty"`
	GitConfig   map[string]string `yaml:"git_config,omitempty"`
	Depth       int               `yaml:"depth,omitempty"` // 0 = full, 1+ = shallow with depth
}

// PresetsConfig represents the .exohub/presets file structure
type PresetsConfig struct {
	Presets []ClonePreset `yaml:"presets"`
}

// CloneMetadata represents metadata stored in .git/exohub/clone-preset
type CloneMetadata struct {
	Preset    string `yaml:"preset"`
	ClonedAt  string `yaml:"cloned_at"`
	RepoURL   string `yaml:"repo_url"`
	Commit    string `yaml:"commit"`
	Branch    string `yaml:"branch"`
}

// Validate checks if the preset configuration is valid
func (p *ClonePreset) Validate() error {
	if p.Name == "" {
		return fmt.Errorf("preset name is required")
	}

	// Check for disallowed characters in preset name
	if containsWhitespace(p.Name) {
		return fmt.Errorf("preset name cannot contain whitespace: %q", p.Name)
	}

	// Depth validation
	if p.Depth < 0 {
		return fmt.Errorf("preset depth cannot be negative: %d", p.Depth)
	}

	// Validate sparse paths don't include .exohub/ (it's always included automatically)
	for _, path := range p.SparsePaths {
		if path == ".exohub/" || path == ".exohub" {
			return fmt.Errorf("sparse_paths should not include .exohub/ (it's always included automatically)")
		}
	}

	return nil
}

// containsWhitespace checks if a string contains any whitespace characters
func containsWhitespace(s string) bool {
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			return true
		}
	}
	return false
}

// ValidatePresets checks if the presets configuration is valid.
// An empty preset list is allowed — it means "no presets configured",
// which is equivalent to the .exohub/presets file being absent.
// Individual preset entries are still fully validated.
func (pc *PresetsConfig) ValidatePresets() error {
	// Check for duplicate preset names and validate each preset
	names := make(map[string]bool)
	for _, preset := range pc.Presets {
		if err := preset.Validate(); err != nil {
			return err
		}
		if names[preset.Name] {
			return fmt.Errorf("duplicate preset name: %q", preset.Name)
		}
		names[preset.Name] = true
	}

	return nil
}

// LoadPresetsConfig reads .exohub/presets from the specified directory.
// Returns nil, nil when:
//   - the file does not exist
//   - the file is present but defines no active presets (e.g. fully commented out)
//
// Both cases mean "no presets configured" and let the caller fall through to a
// full clone.  Individual preset entries that are present are still validated.
func LoadPresetsConfig(repoPath string) (*PresetsConfig, error) {
	path := filepath.Join(repoPath, ".exohub", "presets")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read .exohub/presets: %w", err)
	}

	var config PresetsConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse .exohub/presets: %w", err)
	}

	// Validate individual presets (duplicate names, bad fields, etc.)
	if err := config.ValidatePresets(); err != nil {
		return nil, fmt.Errorf("invalid presets configuration: %w", err)
	}

	// A file with no active presets is equivalent to no file at all.
	// Return nil so the caller's hasPresets check does the right thing.
	if len(config.Presets) == 0 {
		return nil, nil
	}

	return &config, nil
}

// LoadCloneMetadata reads .git/exohub/clone-preset from the specified directory
func LoadCloneMetadata(repoPath string) (*CloneMetadata, error) {
	path := filepath.Join(repoPath, ".git", "exohub", "clone-preset")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read .git/exohub/clone-preset: %w", err)
	}

	var metadata CloneMetadata
	if err := yaml.Unmarshal(data, &metadata); err != nil {
		return nil, fmt.Errorf("failed to parse .git/exohub/clone-preset: %w", err)
	}

	return &metadata, nil
}

// SaveCloneMetadata writes metadata to .git/exohub/clone-preset
func SaveCloneMetadata(repoPath string, metadata *CloneMetadata) error {
	dir := filepath.Join(repoPath, ".git", "exohub")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create .git/exohub directory: %w", err)
	}

	data, err := yaml.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal clone metadata: %w", err)
	}

	path := filepath.Join(dir, "clone-preset")
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write .git/exohub/clone-preset: %w", err)
	}

	return nil
}

// GetPresetByName finds a preset by name in the configuration
func (pc *PresetsConfig) GetPresetByName(name string) *ClonePreset {
	for i := range pc.Presets {
		if pc.Presets[i].Name == name {
			return &pc.Presets[i]
		}
	}
	return nil
}
