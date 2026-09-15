package login

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/oauth2"

	"github.com/Genentech/exohub/go/exo/configdir"
)

const credentialsDirname = "credentials"

// GetCredentialsDir returns the OS-specific credentials directory
// Linux: ~/.config/exo/credentials/
// macOS: ~/Library/Application Support/exo/credentials/
// Windows: %APPDATA%\exo\credentials\
func GetCredentialsDir() (string, error) {
	exoDir, err := configdir.ExoConfigDir()
	if err != nil {
		return "", fmt.Errorf("failed to get config directory: %w", err)
	}

	credsPath := filepath.Join(exoDir, credentialsDirname)
	err = os.MkdirAll(credsPath, 0700)
	if err != nil {
		return "", fmt.Errorf("failed to create credentials directory: %w", err)
	}

	return credsPath, nil
}

// GetTokenFile returns the path to the token file.
// Precedence: EXO_TOKEN_FILE > EXO_CONFIG_DIR-derived path > system default.
// The parent directory is created if it does not exist.
func GetTokenFile() (string, error) {
	tokenFile, err := configdir.TokenFile()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(tokenFile), 0700); err != nil {
		return "", fmt.Errorf("failed to create credentials directory: %w", err)
	}
	return tokenFile, nil
}

// SaveToken saves an OAuth2 token to a file with 0600 permissions
func SaveToken(file string, token *oauth2.Token) error {
	f, err := os.OpenFile(file, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to create token file: %w", err)
	}
	defer f.Close()

	return json.NewEncoder(f).Encode(token)
}

// LoadToken loads an OAuth2 token from a file, or from the EXO_TOKEN env var if set.
func LoadToken(file string) (*oauth2.Token, error) {
	// Check for inline token in environment (no file needed)
	if inline := os.Getenv("EXO_TOKEN"); inline != "" {
		var token oauth2.Token
		if err := json.Unmarshal([]byte(inline), &token); err != nil {
			return nil, fmt.Errorf("failed to parse EXO_TOKEN: %w", err)
		}
		return &token, nil
	}

	f, err := os.Open(file)
	if err != nil {
		return nil, fmt.Errorf("failed to open token file: %w", err)
	}
	defer f.Close()

	var token oauth2.Token
	err = json.NewDecoder(f).Decode(&token)
	if err != nil {
		return nil, fmt.Errorf("failed to decode token: %w", err)
	}

	return &token, nil
}

// DeleteToken deletes a token file
func DeleteToken(file string) error {
	err := os.Remove(file)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete token file: %w", err)
	}
	return nil
}
