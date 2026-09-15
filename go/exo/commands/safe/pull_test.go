package safe

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/Genentech/exohub/go/exo/commands/login"
)

// makeTokenFile writes a token.json to dir and returns its path.
func makeTokenFile(t *testing.T, dir string, tok *oauth2.Token) string {
	t.Helper()
	path := filepath.Join(dir, "token.json")
	if err := login.SaveToken(path, tok); err != nil {
		t.Fatalf("saveToken: %v", err)
	}
	return path
}

// validToken returns an oauth2.Token that has not expired.
func validToken() *oauth2.Token {
	return &oauth2.Token{
		AccessToken: "test-access-token",
		TokenType:   "Bearer",
		Expiry:      time.Now().Add(1 * time.Hour),
	}
}

// expiredToken returns an oauth2.Token that is expired (no refresh token).
func expiredToken() *oauth2.Token {
	return &oauth2.Token{
		AccessToken: "expired-access-token",
		TokenType:   "Bearer",
		Expiry:      time.Now().Add(-1 * time.Hour),
	}
}

// safeServer starts an httptest server that serves the given safe entries JSON
// for GET /api/safe and returns its URL.
func safeServer(t *testing.T, body string, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestPullTokenFromEXO_TOKEN_FILE verifies that runPull uses the token at EXO_TOKEN_FILE.
func TestPullTokenFromEXO_TOKEN_FILE(t *testing.T) {
	tmpDir := t.TempDir()

	// Write a valid token file
	tokFile := makeTokenFile(t, tmpDir, validToken())
	t.Setenv("EXO_TOKEN_FILE", tokFile)
	t.Setenv("EXO_TOKEN", "") // clear inline env

	// Set up config dir for provisioning output
	configDir := t.TempDir()
	t.Setenv("EXO_CONFIG_DIR", configDir)

	// Safe server returns one SSH-key entry
	entries := `[{"name":"github.com","type":"ssh-key","value":"-----BEGIN OPENSSH PRIVATE KEY-----\nfakekey\n-----END OPENSSH PRIVATE KEY-----\n"}]`
	srv := safeServer(t, entries, http.StatusOK)
	t.Setenv("EXOHUB_API_URL", srv.URL+"/api")

	if err := runPull(false); err != nil {
		t.Fatalf("runPull() error: %v", err)
	}

	// Verify SSH key was provisioned
	sshDir := filepath.Join(configDir, "exo", "ssh")
	if _, err := os.Stat(filepath.Join(sshDir, "github.com")); err != nil {
		t.Errorf("SSH key not provisioned: %v", err)
	}
}

// TestPullTokenFromConfigDir verifies that runPull uses the token at EXO_CONFIG_DIR.
func TestPullTokenFromConfigDir(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("EXO_TOKEN_FILE", "") // clear override
	t.Setenv("EXO_TOKEN", "")

	// Write token into the EXO_CONFIG_DIR-derived path
	tokenDir := filepath.Join(tmpDir, "exo", "credentials")
	if err := os.MkdirAll(tokenDir, 0700); err != nil {
		t.Fatal(err)
	}
	makeTokenFile(t, tokenDir, validToken())
	// Rename to expected filename
	if err := os.Rename(
		filepath.Join(tokenDir, "token.json"),
		filepath.Join(tokenDir, "token.json"),
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EXO_CONFIG_DIR", tmpDir)

	entries := `[{"name":"gitlab.com","type":"token","value":"mytoken"}]`
	srv := safeServer(t, entries, http.StatusOK)
	t.Setenv("EXOHUB_API_URL", srv.URL+"/api")

	if err := runPull(false); err != nil {
		t.Fatalf("runPull() error: %v", err)
	}

	credsFile := filepath.Join(tmpDir, "exo", "credentials", "git-credentials")
	data, err := os.ReadFile(credsFile)
	if err != nil {
		t.Fatalf("git-credentials not found: %v", err)
	}
	if !strings.Contains(string(data), "mytoken") {
		t.Errorf("git-credentials does not contain expected token")
	}
}

// TestPullEmptySafe verifies that an empty safe returns a clear, actionable error.
func TestPullEmptySafe(t *testing.T) {
	tmpDir := t.TempDir()
	tokFile := makeTokenFile(t, tmpDir, validToken())
	t.Setenv("EXO_TOKEN_FILE", tokFile)
	t.Setenv("EXO_TOKEN", "")
	t.Setenv("EXO_CONFIG_DIR", tmpDir)

	srv := safeServer(t, `[]`, http.StatusOK)
	t.Setenv("EXOHUB_API_URL", srv.URL+"/api")

	err := runPull(false)
	if err == nil {
		t.Fatal("runPull() should error on empty safe")
	}
	if !strings.Contains(err.Error(), "exo safe store") {
		t.Errorf("error should guide to 'exo safe store', got: %v", err)
	}
}

// TestPullExpiredToken verifies that an expired token (no refresh) returns an error.
func TestPullExpiredToken(t *testing.T) {
	tmpDir := t.TempDir()
	tokFile := makeTokenFile(t, tmpDir, expiredToken())
	t.Setenv("EXO_TOKEN_FILE", tokFile)
	t.Setenv("EXO_TOKEN", "")
	t.Setenv("EXO_CONFIG_DIR", tmpDir)

	err := runPull(false)
	if err == nil {
		t.Fatal("runPull() should error on expired token")
	}
	// Should mention login
	if !strings.Contains(err.Error(), "login") {
		t.Errorf("error should mention 'login', got: %v", err)
	}
}

// TestPullJSONOutput verifies that --json produces valid JSON output.
func TestPullJSONOutput(t *testing.T) {
	tmpDir := t.TempDir()
	tokFile := makeTokenFile(t, tmpDir, validToken())
	t.Setenv("EXO_TOKEN_FILE", tokFile)
	t.Setenv("EXO_TOKEN", "")
	t.Setenv("EXO_CONFIG_DIR", tmpDir)

	entries := `[{"name":"github.com","type":"ssh-key","value":"fake"},{"name":"gitlab.com","type":"token","value":"tok"}]`
	srv := safeServer(t, entries, http.StatusOK)
	t.Setenv("EXOHUB_API_URL", srv.URL+"/api")

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := runPull(true)

	w.Close()
	os.Stdout = oldStdout

	if err != nil {
		t.Fatalf("runPull(json) error: %v", err)
	}

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	output := strings.TrimSpace(string(buf[:n]))

	var result pullResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, output)
	}
	if result.SSHKeys != 1 {
		t.Errorf("ssh_keys = %d, want 1", result.SSHKeys)
	}
	if result.GitCredentials != 1 {
		t.Errorf("git_credentials = %d, want 1", result.GitCredentials)
	}
}
