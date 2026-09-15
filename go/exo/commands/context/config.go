package context

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"gopkg.in/yaml.v3"

	"github.com/Genentech/exohub/go/exo/configdir"
	"github.com/Genentech/exohub/go/exo/internal/defaults"
)

// Context represents a git host configuration
type Context struct {
	Name        string `yaml:"name"`
	Host        string `yaml:"host"`
	Org         string `yaml:"org"`
	Provider    string `yaml:"provider,omitempty"`
	Template    string `yaml:"template,omitempty"`
	Repo        string `yaml:"repo,omitempty"`
	Description string `yaml:"description,omitempty"`
	CatalogURL  string `yaml:"catalog_url,omitempty"`
}

// DefaultHost returns the default git host URL.
func DefaultHost() string { return defaults.GitHost() }

const (
	// DefaultOrg is the default organization
	DefaultOrg = "sandbox"
)

var (
	// OverrideContext is the context name set via --context flag
	OverrideContext string
)

// GetConfigDir returns the OS-appropriate config directory
func GetConfigDir() (string, error) {
	// If EXO_CONFIG_DIR is set (or --config-dir flag), root contexts there.
	if configdir.Root() != "" {
		exoDir, err := configdir.ExoConfigDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(exoDir, "contexts"), nil
	}

	var baseDir string

	switch runtime.GOOS {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to get home directory: %w", err)
		}
		baseDir = filepath.Join(home, "Library", "Application Support", "exo")
	case "linux":
		if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
			baseDir = filepath.Join(xdg, "exo")
		} else {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("failed to get home directory: %w", err)
			}
			baseDir = filepath.Join(home, ".config", "exo")
		}
	default:
		return "", fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}

	contextsDir := filepath.Join(baseDir, "contexts")
	return contextsDir, nil
}

// EnsureConfigDir creates the config directory if it doesn't exist
func EnsureConfigDir() (string, error) {
	dir, err := GetConfigDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("failed to create config directory: %w", err)
	}
	return dir, nil
}

// LoadContext reads a context from a YAML file
func LoadContext(name string) (*Context, error) {
	dir, err := GetConfigDir()
	if err != nil {
		return nil, err
	}

	path := filepath.Join(dir, name+".yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("context %s not found", ErrorStyle.Render(name))
		}
		return nil, fmt.Errorf("failed to read context: %w", err)
	}

	var ctx Context
	if err := yaml.Unmarshal(data, &ctx); err != nil {
		return nil, fmt.Errorf("failed to parse context: %w", err)
	}

	return &ctx, nil
}

// SaveContext writes a context to a YAML file
func SaveContext(ctx *Context) error {
	dir, err := EnsureConfigDir()
	if err != nil {
		return err
	}

	if err := ValidateContext(ctx); err != nil {
		return err
	}

	path := filepath.Join(dir, ctx.Name+".yaml")
	data, err := yaml.Marshal(ctx)
	if err != nil {
		return fmt.Errorf("failed to marshal context: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write context: %w", err)
	}

	return nil
}

// DeleteContext removes a context file
func DeleteContext(name string) error {
	dir, err := GetConfigDir()
	if err != nil {
		return err
	}

	path := filepath.Join(dir, name+".yaml")
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("context %s not found", ErrorStyle.Render(name))
		}
		return fmt.Errorf("failed to delete context: %w", err)
	}

	return nil
}

// ContextExists checks if a context file exists
func ContextExists(name string) (bool, error) {
	dir, err := GetConfigDir()
	if err != nil {
		return false, err
	}

	path := filepath.Join(dir, name+".yaml")
	_, err = os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

// ListContexts returns all available context names
func ListContexts() ([]string, error) {
	dir, err := GetConfigDir()
	if err != nil {
		return nil, err
	}

	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return []string{}, nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to read contexts directory: %w", err)
	}

	var names []string
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".yaml" {
			continue
		}
		if entry.Name() == ".selected" {
			continue
		}
		name := entry.Name()[:len(entry.Name())-5] // Remove .yaml extension
		names = append(names, name)
	}

	return names, nil
}

// DefaultContext is the built-in default context.
// Internal-build fields are set by context_internal.go (//go:build internal).
var DefaultContext = Context{
	Name: "default",
	Host: defaults.GitHost(),
}

// ResolveContextName returns the effective context name from --context flag
// or EXOHUB_CONTEXT env var. Returns empty string if neither is set.
func ResolveContextName() string {
	if OverrideContext != "" {
		return OverrideContext
	}
	return os.Getenv("EXOHUB_CONTEXT")
}

// GetCurrentContext returns the context specified by --context flag,
// EXOHUB_CONTEXT env var, or an error if neither is set.
// The --context flag takes priority over the env var.
// If the name is "default" and no user-created context exists, the built-in default is returned.
func GetCurrentContext() (*Context, error) {
	name := ResolveContextName()
	if name == "" {
		return nil, fmt.Errorf("no context specified (use --context flag or EXOHUB_CONTEXT env var)")
	}

	// Try to load user-created context first
	ctx, err := LoadContext(name)
	if err == nil {
		return ctx, nil
	}

	// If the name is "default", return the built-in default context
	if name == "default" {
		def := DefaultContext
		return &def, nil
	}

	return nil, fmt.Errorf("context %s not found", ErrorStyle.Render(name))
}

// ValidateContext checks if a context has valid fields
func ValidateContext(ctx *Context) error {
	if ctx.Name == "" {
		return fmt.Errorf("context name is required")
	}
	if ctx.Host == "" {
		return fmt.Errorf("host is required")
	}
	if ctx.Org == "" {
		return fmt.Errorf("org is required")
	}
	return nil
}
