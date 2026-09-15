package login

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

// TestGetCredentialsDir verifies that the credentials directory is created correctly
func TestGetCredentialsDir(t *testing.T) {
	// Create a temporary config directory for testing
	tempDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tempDir) // Linux
	t.Setenv("HOME", tempDir)            // Fallback

	credsDir, err := GetCredentialsDir()
	if err != nil {
		t.Fatalf("GetCredentialsDir() failed: %v", err)
	}

	// Verify the directory exists
	info, err := os.Stat(credsDir)
	if err != nil {
		t.Fatalf("credentials directory does not exist: %v", err)
	}

	// Verify it's a directory
	if !info.IsDir() {
		t.Errorf("credentials path is not a directory")
	}

	// Verify permissions are restrictive (0700)
	mode := info.Mode().Perm()
	if mode != 0700 {
		t.Errorf("credentials directory has wrong permissions: got %o, want 0700", mode)
	}
}

// TestGetTokenFile verifies the token file path is constructed correctly
func TestGetTokenFile(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tempDir)
	t.Setenv("HOME", tempDir)

	tokenFile, err := GetTokenFile()
	if err != nil {
		t.Fatalf("GetTokenFile() failed: %v", err)
	}

	// Verify the path contains expected components
	if !filepath.IsAbs(tokenFile) {
		t.Errorf("token file path is not absolute: %s", tokenFile)
	}

	if filepath.Base(tokenFile) != "token.json" {
		t.Errorf("token file has wrong name: got %s, want token.json", filepath.Base(tokenFile))
	}

	// Verify parent directory exists (GetTokenFile should create it)
	dir := filepath.Dir(tokenFile)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Errorf("token file parent directory does not exist: %s", dir)
	}
}

// TestSaveAndLoadToken verifies token serialization and deserialization
func TestSaveAndLoadToken(t *testing.T) {
	tempDir := t.TempDir()
	tokenFile := filepath.Join(tempDir, "test_token.json")

	// Create a test token
	originalToken := &oauth2.Token{
		AccessToken:  "test-access-token",
		TokenType:    "Bearer",
		RefreshToken: "test-refresh-token",
		Expiry:       time.Now().Add(1 * time.Hour),
	}

	// Save the token
	err := SaveToken(tokenFile, originalToken)
	if err != nil {
		t.Fatalf("SaveToken() failed: %v", err)
	}

	// Verify file exists with correct permissions
	info, err := os.Stat(tokenFile)
	if err != nil {
		t.Fatalf("token file does not exist: %v", err)
	}

	mode := info.Mode().Perm()
	if mode != 0600 {
		t.Errorf("token file has wrong permissions: got %o, want 0600", mode)
	}

	// Load the token back
	loadedToken, err := LoadToken(tokenFile)
	if err != nil {
		t.Fatalf("LoadToken() failed: %v", err)
	}

	// Verify token contents
	if loadedToken.AccessToken != originalToken.AccessToken {
		t.Errorf("AccessToken mismatch: got %s, want %s", loadedToken.AccessToken, originalToken.AccessToken)
	}
	if loadedToken.TokenType != originalToken.TokenType {
		t.Errorf("TokenType mismatch: got %s, want %s", loadedToken.TokenType, originalToken.TokenType)
	}
	if loadedToken.RefreshToken != originalToken.RefreshToken {
		t.Errorf("RefreshToken mismatch: got %s, want %s", loadedToken.RefreshToken, originalToken.RefreshToken)
	}

	// Compare expiry times (allowing for small serialization differences)
	expiryDiff := loadedToken.Expiry.Sub(originalToken.Expiry)
	if expiryDiff < -1*time.Second || expiryDiff > 1*time.Second {
		t.Errorf("Expiry mismatch: got %v, want %v (diff: %v)", loadedToken.Expiry, originalToken.Expiry, expiryDiff)
	}
}

// TestSaveTokenOverwrite verifies that SaveToken overwrites existing files
func TestSaveTokenOverwrite(t *testing.T) {
	tempDir := t.TempDir()
	tokenFile := filepath.Join(tempDir, "test_token.json")

	// Create and save first token
	token1 := &oauth2.Token{
		AccessToken: "first-token",
		TokenType:   "Bearer",
		Expiry:      time.Now().Add(1 * time.Hour),
	}
	err := SaveToken(tokenFile, token1)
	if err != nil {
		t.Fatalf("SaveToken() first save failed: %v", err)
	}

	// Overwrite with second token
	token2 := &oauth2.Token{
		AccessToken: "second-token",
		TokenType:   "Bearer",
		Expiry:      time.Now().Add(2 * time.Hour),
	}
	err = SaveToken(tokenFile, token2)
	if err != nil {
		t.Fatalf("SaveToken() overwrite failed: %v", err)
	}

	// Load and verify we have the second token
	loadedToken, err := LoadToken(tokenFile)
	if err != nil {
		t.Fatalf("LoadToken() failed: %v", err)
	}

	if loadedToken.AccessToken != "second-token" {
		t.Errorf("Expected second token, got %s", loadedToken.AccessToken)
	}
}

// TestLoadTokenNonExistent verifies error handling for missing token files
func TestLoadTokenNonExistent(t *testing.T) {
	tempDir := t.TempDir()
	tokenFile := filepath.Join(tempDir, "nonexistent_token.json")

	_, err := LoadToken(tokenFile)
	if err == nil {
		t.Error("LoadToken() should fail for non-existent file")
	}

	if !os.IsNotExist(err) {
		// The error is wrapped, so check the error message
		if err.Error() == "" {
			t.Errorf("LoadToken() returned unexpected error type: %v", err)
		}
	}
}

// TestLoadTokenInvalidJSON verifies error handling for corrupted token files
func TestLoadTokenInvalidJSON(t *testing.T) {
	tempDir := t.TempDir()
	tokenFile := filepath.Join(tempDir, "invalid_token.json")

	// Write invalid JSON
	err := os.WriteFile(tokenFile, []byte("not valid json"), 0600)
	if err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	_, err = LoadToken(tokenFile)
	if err == nil {
		t.Error("LoadToken() should fail for invalid JSON")
	}
}

// TestDeleteToken verifies token file deletion
func TestDeleteToken(t *testing.T) {
	tempDir := t.TempDir()
	tokenFile := filepath.Join(tempDir, "test_token.json")

	// Create a token file
	token := &oauth2.Token{
		AccessToken: "test-token",
		TokenType:   "Bearer",
		Expiry:      time.Now().Add(1 * time.Hour),
	}
	err := SaveToken(tokenFile, token)
	if err != nil {
		t.Fatalf("SaveToken() failed: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(tokenFile); os.IsNotExist(err) {
		t.Fatal("Token file was not created")
	}

	// Delete the token
	err = DeleteToken(tokenFile)
	if err != nil {
		t.Fatalf("DeleteToken() failed: %v", err)
	}

	// Verify file is gone
	if _, err := os.Stat(tokenFile); !os.IsNotExist(err) {
		t.Error("Token file still exists after deletion")
	}
}

// TestDeleteTokenNonExistent verifies that deleting a non-existent token doesn't error
func TestDeleteTokenNonExistent(t *testing.T) {
	tempDir := t.TempDir()
	tokenFile := filepath.Join(tempDir, "nonexistent_token.json")

	// Delete a non-existent file (should not error)
	err := DeleteToken(tokenFile)
	if err != nil {
		t.Errorf("DeleteToken() should not error for non-existent file: %v", err)
	}
}

// TestTokenValidation verifies oauth2.Token.Valid() behavior with saved tokens
func TestTokenValidation(t *testing.T) {
	tempDir := t.TempDir()

	tests := []struct {
		name        string
		token       *oauth2.Token
		wantValid   bool
		description string
	}{
		{
			name: "valid_future_expiry",
			token: &oauth2.Token{
				AccessToken: "valid-token",
				TokenType:   "Bearer",
				Expiry:      time.Now().Add(1 * time.Hour),
			},
			wantValid:   true,
			description: "Token with future expiry should be valid",
		},
		{
			name: "expired_token",
			token: &oauth2.Token{
				AccessToken: "expired-token",
				TokenType:   "Bearer",
				Expiry:      time.Now().Add(-1 * time.Hour),
			},
			wantValid:   false,
			description: "Token with past expiry should be invalid",
		},
		{
			name: "zero_expiry",
			token: &oauth2.Token{
				AccessToken: "no-expiry-token",
				TokenType:   "Bearer",
				Expiry:      time.Time{}, // Zero time
			},
			wantValid:   true,
			description: "Token with zero expiry should be valid (no expiration)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokenFile := filepath.Join(tempDir, tt.name+".json")

			// Save token
			err := SaveToken(tokenFile, tt.token)
			if err != nil {
				t.Fatalf("SaveToken() failed: %v", err)
			}

			// Load token
			loadedToken, err := LoadToken(tokenFile)
			if err != nil {
				t.Fatalf("LoadToken() failed: %v", err)
			}

			// Check validity
			if loadedToken.Valid() != tt.wantValid {
				t.Errorf("%s: Token.Valid() = %v, want %v", tt.description, loadedToken.Valid(), tt.wantValid)
			}
		})
	}
}

// TestTokenFileFormat verifies the JSON format of saved tokens
func TestTokenFileFormat(t *testing.T) {
	tempDir := t.TempDir()
	tokenFile := filepath.Join(tempDir, "format_test.json")

	token := &oauth2.Token{
		AccessToken:  "test-access",
		TokenType:    "Bearer",
		RefreshToken: "test-refresh",
		Expiry:       time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC),
	}

	// Save token
	err := SaveToken(tokenFile, token)
	if err != nil {
		t.Fatalf("SaveToken() failed: %v", err)
	}

	// Read raw file contents
	data, err := os.ReadFile(tokenFile)
	if err != nil {
		t.Fatalf("Failed to read token file: %v", err)
	}

	// Parse JSON manually to verify structure
	var raw map[string]interface{}
	err = json.Unmarshal(data, &raw)
	if err != nil {
		t.Fatalf("Token file is not valid JSON: %v", err)
	}

	// Verify expected fields exist
	expectedFields := []string{"access_token", "token_type", "expiry"}
	for _, field := range expectedFields {
		if _, ok := raw[field]; !ok {
			t.Errorf("Token JSON missing field: %s", field)
		}
	}

	// Verify values
	if raw["access_token"] != "test-access" {
		t.Errorf("access_token mismatch: got %v", raw["access_token"])
	}
	if raw["token_type"] != "Bearer" {
		t.Errorf("token_type mismatch: got %v", raw["token_type"])
	}
}

// TestGetTokenFileWithConfigDir verifies that EXO_CONFIG_DIR changes the token file path.
func TestGetTokenFileWithConfigDir(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("EXO_CONFIG_DIR", tmpDir)
	t.Setenv("EXO_TOKEN_FILE", "") // ensure override is not active

	tokenFile, err := GetTokenFile()
	if err != nil {
		t.Fatalf("GetTokenFile() failed: %v", err)
	}

	expected := tmpDir + "/exo/credentials/token.json"
	if tokenFile != expected {
		t.Errorf("GetTokenFile() = %q, want %q", tokenFile, expected)
	}
}

// TestGetCredentialsDirWithConfigDir verifies that EXO_CONFIG_DIR changes the credentials dir.
func TestGetCredentialsDirWithConfigDir(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("EXO_CONFIG_DIR", tmpDir)

	credsDir, err := GetCredentialsDir()
	if err != nil {
		t.Fatalf("GetCredentialsDir() failed: %v", err)
	}

	expected := tmpDir + "/exo/credentials"
	if credsDir != expected {
		t.Errorf("GetCredentialsDir() = %q, want %q", credsDir, expected)
	}
}
