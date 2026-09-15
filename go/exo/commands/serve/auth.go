package serve

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/oauth2"
)

// WriteConnectionTokenFile writes the OAuth token to a temporary file for a single PTY connection.
func WriteConnectionTokenFile(connID string, token *oauth2.Token) (string, error) {
	dir := filepath.Join(os.TempDir(), "exo-serve", connID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("failed to create connection token dir: %w", err)
	}

	tokenFile := filepath.Join(dir, "token.json")
	data, err := json.Marshal(token)
	if err != nil {
		return "", fmt.Errorf("failed to marshal token: %w", err)
	}

	if err := os.WriteFile(tokenFile, data, 0600); err != nil {
		return "", fmt.Errorf("failed to write token file: %w", err)
	}

	return tokenFile, nil
}

// CleanupConnectionFiles removes the temporary token file and parent directory for a connection.
func CleanupConnectionFiles(connID string) {
	dir := filepath.Join(os.TempDir(), "exo-serve", connID)
	os.RemoveAll(dir)
}

// connectionSearchDir returns the per-connection directory for search params isolation.
func connectionSearchDir(connID string) string {
	return filepath.Join(os.TempDir(), "exo-serve", connID, "search")
}
