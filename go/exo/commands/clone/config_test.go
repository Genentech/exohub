package clone

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClonePresetValidate(t *testing.T) {
	tests := []struct {
		name    string
		preset  ClonePreset
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid minimal preset",
			preset: ClonePreset{
				Name:        "minimal",
				Description: "Minimal preset",
				SparsePaths: []string{"Makefile", "scripts/"},
				GitConfig:   map[string]string{"status.showUntrackedFiles": "no"},
				Depth:       1,
			},
			wantErr: false,
		},
		{
			name: "valid full clone preset",
			preset: ClonePreset{
				Name:        "development",
				Description: "Full development clone",
				Depth:       0,
				GitConfig:   map[string]string{"core.untrackedCache": "true"},
			},
			wantErr: false,
		},
		{
			name: "missing name",
			preset: ClonePreset{
				Description: "No name preset",
			},
			wantErr: true,
			errMsg:  "preset name is required",
		},
		{
			name: "name with whitespace",
			preset: ClonePreset{
				Name: "invalid name",
			},
			wantErr: true,
			errMsg:  "preset name cannot contain whitespace",
		},
		{
			name: "negative depth",
			preset: ClonePreset{
				Name:  "invalid-depth",
				Depth: -1,
			},
			wantErr: true,
			errMsg:  "preset depth cannot be negative",
		},
		{
			name: "sparse_paths includes .exohub/",
			preset: ClonePreset{
				Name:        "invalid-paths",
				SparsePaths: []string{".exohub/", "data/"},
			},
			wantErr: true,
			errMsg:  "sparse_paths should not include .exohub/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.preset.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("ClonePreset.Validate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && err != nil && tt.errMsg != "" {
				if !contains(err.Error(), tt.errMsg) {
					t.Errorf("ClonePreset.Validate() error message = %q, want to contain %q", err.Error(), tt.errMsg)
				}
			}
		})
	}
}

func TestPresetsConfigValidatePresets(t *testing.T) {
	tests := []struct {
		name    string
		config  PresetsConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid config with multiple presets",
			config: PresetsConfig{
				Presets: []ClonePreset{
					{Name: "minimal", Depth: 1, SparsePaths: []string{"Makefile"}},
					{Name: "full", Depth: 0},
				},
			},
			wantErr: false,
		},
		{
			// An empty preset list is not a validation error — it means
			// "no presets configured", equivalent to the file being absent.
			name:    "empty presets",
			config:  PresetsConfig{Presets: []ClonePreset{}},
			wantErr: false,
		},
		{
			name: "duplicate preset names",
			config: PresetsConfig{
				Presets: []ClonePreset{
					{Name: "minimal", Depth: 1},
					{Name: "minimal", Depth: 0},
				},
			},
			wantErr: true,
			errMsg:  "duplicate preset name: \"minimal\"",
		},
		{
			name: "invalid preset in config",
			config: PresetsConfig{
				Presets: []ClonePreset{
					{Name: "valid", Depth: 1},
					{Name: "", Depth: 1}, // Invalid: empty name
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.ValidatePresets()
			if (err != nil) != tt.wantErr {
				t.Errorf("PresetsConfig.ValidatePresets() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && err != nil && tt.errMsg != "" {
				if !contains(err.Error(), tt.errMsg) {
					t.Errorf("PresetsConfig.ValidatePresets() error message = %q, want to contain %q", err.Error(), tt.errMsg)
				}
			}
		})
	}
}

func TestLoadPresetsConfig(t *testing.T) {
	// Create temporary directory
	tmpDir := t.TempDir()

	tests := []struct {
		name       string
		setupFunc  func(string) error
		wantErr    bool
		wantNil    bool
		wantCount  int
		errContain string
	}{
		{
			name: "valid presets file",
			setupFunc: func(dir string) error {
				exohubDir := filepath.Join(dir, ".exohub")
				if err := os.MkdirAll(exohubDir, 0755); err != nil {
					return err
				}
				content := `presets:
  - name: minimal
    description: Minimal preset
    depth: 1
    sparse_paths:
      - Makefile
    git_config:
      status.showUntrackedFiles: "no"
  - name: full
    description: Full clone
    depth: 0
`
				return os.WriteFile(filepath.Join(exohubDir, "presets"), []byte(content), 0644)
			},
			wantErr:   false,
			wantNil:   false,
			wantCount: 2,
		},
		{
			name: "missing presets file",
			setupFunc: func(dir string) error {
				return os.MkdirAll(filepath.Join(dir, ".exohub"), 0755)
			},
			wantErr: false,
			wantNil: true,
		},
		{
			name: "invalid YAML",
			setupFunc: func(dir string) error {
				exohubDir := filepath.Join(dir, ".exohub")
				if err := os.MkdirAll(exohubDir, 0755); err != nil {
					return err
				}
				content := `presets:
  - name: minimal
    invalid yaml: [[[
`
				return os.WriteFile(filepath.Join(exohubDir, "presets"), []byte(content), 0644)
			},
			wantErr:    true,
			errContain: "failed to parse",
		},
		{
			name: "invalid preset config",
			setupFunc: func(dir string) error {
				exohubDir := filepath.Join(dir, ".exohub")
				if err := os.MkdirAll(exohubDir, 0755); err != nil {
					return err
				}
				content := `presets:
  - name: ""
    depth: 1
`
				return os.WriteFile(filepath.Join(exohubDir, "presets"), []byte(content), 0644)
			},
			wantErr:    true,
			errContain: "invalid presets configuration",
		},
		{
			// A file present but fully commented out is valid YAML that
			// produces an empty preset list.  It must succeed and return nil
			// (equivalent to no file) so clone falls through to full-clone.
			name: "fully commented-out presets file",
			setupFunc: func(dir string) error {
				exohubDir := filepath.Join(dir, ".exohub")
				if err := os.MkdirAll(exohubDir, 0755); err != nil {
					return err
				}
				content := `# presets:
#   - name: example
#     description: Example preset
#     depth: 1
`
				return os.WriteFile(filepath.Join(exohubDir, "presets"), []byte(content), 0644)
			},
			wantErr: false,
			wantNil: true, // same as file-absent
		},
		{
			// An explicit empty list (presets: []) must also succeed and
			// return nil so clone falls through to full-clone.
			name: "explicit empty presets list",
			setupFunc: func(dir string) error {
				exohubDir := filepath.Join(dir, ".exohub")
				if err := os.MkdirAll(exohubDir, 0755); err != nil {
					return err
				}
				content := "presets: []\n"
				return os.WriteFile(filepath.Join(exohubDir, "presets"), []byte(content), 0644)
			},
			wantErr: false,
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testDir := filepath.Join(tmpDir, tt.name)
			if err := os.MkdirAll(testDir, 0755); err != nil {
				t.Fatalf("Failed to create test dir: %v", err)
			}

			if tt.setupFunc != nil {
				if err := tt.setupFunc(testDir); err != nil {
					t.Fatalf("Setup failed: %v", err)
				}
			}

			config, err := LoadPresetsConfig(testDir)
			if (err != nil) != tt.wantErr {
				t.Errorf("LoadPresetsConfig() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && err != nil && tt.errContain != "" {
				if !contains(err.Error(), tt.errContain) {
					t.Errorf("LoadPresetsConfig() error = %q, want error containing %q", err.Error(), tt.errContain)
				}
			}

			if !tt.wantErr {
				if (config == nil) != tt.wantNil {
					t.Errorf("LoadPresetsConfig() config is nil = %v, want nil = %v", config == nil, tt.wantNil)
				}

				if !tt.wantNil && len(config.Presets) != tt.wantCount {
					t.Errorf("LoadPresetsConfig() preset count = %d, want %d", len(config.Presets), tt.wantCount)
				}
			}
		})
	}
}

func TestCloneMetadataSaveLoad(t *testing.T) {
	tmpDir := t.TempDir()
	gitDir := filepath.Join(tmpDir, ".git")
	if err := os.MkdirAll(gitDir, 0755); err != nil {
		t.Fatalf("Failed to create .git dir: %v", err)
	}

	metadata := &CloneMetadata{
		Preset:   "minimal",
		ClonedAt: "2026-02-26T10:00:00Z",
		RepoURL:  "https://example.com/repo.git",
		Commit:   "abc123def456",
		Branch:   "main",
	}

	// Test Save
	if err := SaveCloneMetadata(tmpDir, metadata); err != nil {
		t.Fatalf("SaveCloneMetadata() error = %v", err)
	}

	// Verify file exists
	metadataPath := filepath.Join(tmpDir, ".git", "exohub", "clone-preset")
	if _, err := os.Stat(metadataPath); os.IsNotExist(err) {
		t.Fatalf("Metadata file was not created")
	}

	// Test Load
	loaded, err := LoadCloneMetadata(tmpDir)
	if err != nil {
		t.Fatalf("LoadCloneMetadata() error = %v", err)
	}

	if loaded == nil {
		t.Fatalf("LoadCloneMetadata() returned nil")
	}

	// Verify fields
	if loaded.Preset != metadata.Preset {
		t.Errorf("Preset = %q, want %q", loaded.Preset, metadata.Preset)
	}
	if loaded.ClonedAt != metadata.ClonedAt {
		t.Errorf("ClonedAt = %q, want %q", loaded.ClonedAt, metadata.ClonedAt)
	}
	if loaded.RepoURL != metadata.RepoURL {
		t.Errorf("RepoURL = %q, want %q", loaded.RepoURL, metadata.RepoURL)
	}
	if loaded.Commit != metadata.Commit {
		t.Errorf("Commit = %q, want %q", loaded.Commit, metadata.Commit)
	}
	if loaded.Branch != metadata.Branch {
		t.Errorf("Branch = %q, want %q", loaded.Branch, metadata.Branch)
	}
}

func TestGetPresetByName(t *testing.T) {
	config := &PresetsConfig{
		Presets: []ClonePreset{
			{Name: "minimal", Depth: 1},
			{Name: "full", Depth: 0},
			{Name: "data-only", Depth: 1, SparsePaths: []string{"data/"}},
		},
	}

	tests := []struct {
		name       string
		searchName string
		wantFound  bool
		wantDepth  int
	}{
		{
			name:       "find existing preset",
			searchName: "minimal",
			wantFound:  true,
			wantDepth:  1,
		},
		{
			name:       "find another preset",
			searchName: "full",
			wantFound:  true,
			wantDepth:  0,
		},
		{
			name:       "preset not found",
			searchName: "nonexistent",
			wantFound:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			preset := config.GetPresetByName(tt.searchName)
			if (preset != nil) != tt.wantFound {
				t.Errorf("GetPresetByName(%q) found = %v, want %v", tt.searchName, preset != nil, tt.wantFound)
			}
			if tt.wantFound && preset.Depth != tt.wantDepth {
				t.Errorf("GetPresetByName(%q) depth = %d, want %d", tt.searchName, preset.Depth, tt.wantDepth)
			}
		})
	}
}

// Helper function
func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && (s == substr || len(s) >= len(substr) && (s[:len(substr)] == substr || s[len(s)-len(substr):] == substr || containsMiddle(s, substr)))
}

func containsMiddle(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
