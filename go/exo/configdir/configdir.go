package configdir

import (
	"os"
	"path/filepath"
)

// FlagValue is set by the --config-dir persistent flag in main.go.
// When set, it takes precedence over EXO_CONFIG_DIR.
var FlagValue string

// Root returns the effective config root directory.
// Precedence: --config-dir flag > EXO_CONFIG_DIR env > "" (use system defaults).
func Root() string {
	if FlagValue != "" {
		return FlagValue
	}
	return os.Getenv("EXO_CONFIG_DIR")
}

// ExoConfigDir returns the exo config directory.
// If a config root is set: <root>/exo
// Otherwise: os.UserConfigDir()/exo
func ExoConfigDir() (string, error) {
	if root := Root(); root != "" {
		return filepath.Join(root, "exo"), nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "exo"), nil
}

// TokenFile returns the path to the exo token file.
// If a config root is set: <root>/exo/credentials/token.json
// Otherwise: os.UserConfigDir()/exo/credentials/token.json
// EXO_TOKEN_FILE always takes the highest precedence.
func TokenFile() (string, error) {
	if override := os.Getenv("EXO_TOKEN_FILE"); override != "" {
		return override, nil
	}
	dir, err := ExoConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "credentials", "token.json"), nil
}

// AWSCredentialsFile returns the path to the AWS shared credentials file.
// If a config root is set: <root>/aws/credentials
// Otherwise: AWS_SHARED_CREDENTIALS_FILE env → ~/.aws/credentials
func AWSCredentialsFile() (string, error) {
	if root := Root(); root != "" {
		return filepath.Join(root, "aws", "credentials"), nil
	}
	if v := os.Getenv("AWS_SHARED_CREDENTIALS_FILE"); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".aws", "credentials"), nil
}

// CredHelperCacheDir returns the cache directory for exo-credential-helper.
// If a config root is set: <root>/cache/exo-credential-helper
// Otherwise: os.UserCacheDir()/exo-credential-helper
func CredHelperCacheDir() (string, error) {
	if root := Root(); root != "" {
		p := filepath.Join(root, "cache", "exo-credential-helper")
		return p, os.MkdirAll(p, 0700)
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	p := filepath.Join(dir, "exo-credential-helper")
	return p, os.MkdirAll(p, 0700)
}

// PurgeCredHelperCache removes all cached S3 credentials (*.json) and lock
// files (*.lock) from the exo-credential-helper cache directory. It is a
// no-op when the cache directory does not exist.
func PurgeCredHelperCache() error {
	dir, err := CredHelperCacheDir()
	if err != nil {
		return err
	}
	for _, pattern := range []string{"*.json", "*.lock"} {
		matches, err := filepath.Glob(filepath.Join(dir, pattern))
		if err != nil {
			return err
		}
		for _, f := range matches {
			if err := os.Remove(f); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}
