package preferences

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"gopkg.in/yaml.v3"

	"github.com/Genentech/exohub/go/exo/configdir"
)

// Preferences stores user-level settings.
type Preferences struct {
	Theme       string `yaml:"theme,omitempty"`
	ThemeMode   string `yaml:"theme_mode,omitempty"` // "auto", "dark", or "light"
	TipsEnabled *bool  `yaml:"tips_enabled,omitempty"`
}

// configDir returns the OS-appropriate exo config directory.
func configDir() (string, error) {
	// If EXO_CONFIG_DIR is set (or --config-dir flag), use it.
	if configdir.Root() != "" {
		return configdir.ExoConfigDir()
	}

	switch runtime.GOOS {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to get home directory: %w", err)
		}
		return filepath.Join(home, "Library", "Application Support", "exo"), nil
	case "linux":
		if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
			return filepath.Join(xdg, "exo"), nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to get home directory: %w", err)
		}
		return filepath.Join(home, ".config", "exo"), nil
	default:
		return "", fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}
}

func filePath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "preferences.yaml"), nil
}

// Load reads the preferences file. Returns empty preferences (not an error)
// if the file does not exist.
func Load() (*Preferences, error) {
	path, err := filePath()
	if err != nil {
		return &Preferences{}, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Preferences{}, nil
		}
		return nil, fmt.Errorf("failed to read preferences: %w", err)
	}

	var p Preferences
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("failed to parse preferences: %w", err)
	}
	return &p, nil
}

// Save writes the preferences to disk, creating the directory if needed.
func Save(p *Preferences) error {
	path, err := filePath()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	data, err := yaml.Marshal(p)
	if err != nil {
		return fmt.Errorf("failed to marshal preferences: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write preferences: %w", err)
	}
	return nil
}

// GetTheme returns the stored theme name, or empty string if unset or on error.
func GetTheme() string {
	p, err := Load()
	if err != nil || p == nil {
		return ""
	}
	return p.Theme
}

// GetThemeMode returns the stored theme mode, or empty string if unset or on error.
func GetThemeMode() string {
	p, err := Load()
	if err != nil || p == nil {
		return ""
	}
	return p.ThemeMode
}
