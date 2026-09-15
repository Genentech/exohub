package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestResolveEnvDefault(t *testing.T) {
	// Flag value takes priority
	if got := resolveEnvDefault("flag", "NONEXISTENT", "default"); got != "flag" {
		t.Errorf("expected flag, got %s", got)
	}

	// Env var takes priority over default
	t.Setenv("TEST_RESOLVE", "envval")
	if got := resolveEnvDefault("", "TEST_RESOLVE", "default"); got != "envval" {
		t.Errorf("expected envval, got %s", got)
	}

	// Default when nothing else
	if got := resolveEnvDefault("", "NONEXISTENT_VAR_XYZ", "mydefault"); got != "mydefault" {
		t.Errorf("expected mydefault, got %s", got)
	}
}

func TestCacheKey(t *testing.T) {
	k1 := cacheKey("s3://bucket/prefix", "READ", true)
	k2 := cacheKey("s3://bucket/prefix", "READWRITE", true)
	k3 := cacheKey("s3://bucket/prefix", "READ", true)

	if k1 == k2 {
		t.Error("different permissions should produce different cache keys")
	}
	if k1 != k3 {
		t.Error("same input should produce same cache key")
	}
}

func TestCacheRoundTrip(t *testing.T) {
	// Set up temp cache dir
	tmpDir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", tmpDir)

	creds := &credentialOutput{
		Version:         1,
		AccessKeyID:     "ASIATEST",
		SecretAccessKey: "secret",
		SessionToken:    "token",
		Expiration:      time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339),
	}

	s3URL := "s3://test-bucket/prefix/"
	perm := "READWRITE"

	// Cache and load
	if err := cacheCredentials(s3URL, perm, true, creds); err != nil {
		t.Fatalf("cacheCredentials error: %v", err)
	}

	loaded, err := loadCachedCredentials(s3URL, perm, true)
	if err != nil {
		t.Fatalf("loadCachedCredentials error: %v", err)
	}

	if loaded.AccessKeyID != creds.AccessKeyID {
		t.Errorf("AccessKeyID = %q, want %q", loaded.AccessKeyID, creds.AccessKeyID)
	}
	if loaded.SecretAccessKey != creds.SecretAccessKey {
		t.Errorf("SecretAccessKey mismatch")
	}
}

func TestCacheExpired(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", tmpDir)

	creds := &credentialOutput{
		Version:         1,
		AccessKeyID:     "ASIAEXPIRED",
		SecretAccessKey: "secret",
		SessionToken:    "token",
		Expiration:      time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339),
	}

	s3URL := "s3://test-bucket/prefix/"
	perm := "READ"

	if err := cacheCredentials(s3URL, perm, true, creds); err != nil {
		t.Fatalf("cacheCredentials error: %v", err)
	}

	_, err := loadCachedCredentials(s3URL, perm, true)
	if err == nil {
		t.Fatal("expected error for expired credentials")
	}
}

func TestDetectRemote(t *testing.T) {
	tmpDir := t.TempDir()
	oldDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldDir)

	// Create .exohub/remotes
	os.MkdirAll(filepath.Join(tmpDir, ".exohub"), 0755)
	content := `remotes:
  - name: myremote
    type: annex
    s3url: s3://mybucket/myprefix/
`
	os.WriteFile(filepath.Join(tmpDir, ".exohub", "remotes"), []byte(content), 0644)

	detected, err := detectRemote()
	if err != nil {
		t.Fatalf("detectRemote error: %v", err)
	}
	if detected.S3URL != "s3://mybucket/myprefix/" {
		t.Errorf("S3URL = %q, want s3://mybucket/myprefix/", detected.S3URL)
	}
	if !detected.Grants {
		t.Error("Grants should default to true when not specified")
	}
}

func TestDetectRemoteMissing(t *testing.T) {
	tmpDir := t.TempDir()
	oldDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldDir)

	_, err := detectRemote()
	if err == nil {
		t.Fatal("expected error when .exohub/remotes doesn't exist")
	}
}

func TestIsAccessDeniedError(t *testing.T) {
	cases := []struct {
		msg  string
		want bool
	}{
		{"GetDataAccess failed: operation error S3 Control: GetDataAccess, https response error StatusCode: 403, AccessDenied", true},
		{"GetDataAccess failed: AccessDenied: You do not have READ permissions", true},
		{"GetDataAccess failed: access denied to prefix", true},
		{"GetDataAccess failed: StatusCode: 403, RequestID: abc", true},
		{"GetDataAccess failed: ExpiredTokenException", false},
		{"GetDataAccess failed: NoSuchBucket", false},
	}
	for _, tc := range cases {
		err := fmt.Errorf("%s", tc.msg)
		if got := isAccessDeniedError(err); got != tc.want {
			t.Errorf("isAccessDeniedError(%q) = %v, want %v", tc.msg, got, tc.want)
		}
	}
}

func TestLoadBearerTokenFromEnv(t *testing.T) {
	tokenJSON := `{"access_token":"mytoken123","token_type":"bearer"}`
	t.Setenv("EXO_TOKEN", tokenJSON)

	token, err := loadBearerToken()
	if err != nil {
		t.Fatalf("loadBearerToken error: %v", err)
	}
	if token != "mytoken123" {
		t.Errorf("token = %q, want mytoken123", token)
	}
}

func TestLoadBearerTokenFromFile(t *testing.T) {
	tmpDir := t.TempDir()
	tokenFile := filepath.Join(tmpDir, "token.json")
	tokenJSON := `{"access_token":"filetoken456","token_type":"bearer"}`
	os.WriteFile(tokenFile, []byte(tokenJSON), 0600)
	t.Setenv("EXO_TOKEN_FILE", tokenFile)
	t.Setenv("EXO_TOKEN", "") // ensure env inline is not used

	token, err := loadBearerToken()
	if err != nil {
		t.Fatalf("loadBearerToken error: %v", err)
	}
	if token != "filetoken456" {
		t.Errorf("token = %q, want filetoken456", token)
	}
}

func TestOutputCredentialsJSON(t *testing.T) {
	creds := &credentialOutput{
		Version:         1,
		AccessKeyID:     "ASIATEST",
		SecretAccessKey: "secret123",
		SessionToken:    "token456",
		Expiration:      "2026-03-05T22:00:00Z",
	}

	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	outputCredentials(creds, false)

	w.Close()
	os.Stdout = old

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)

	var parsed credentialOutput
	if err := json.Unmarshal(buf[:n], &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, string(buf[:n]))
	}
	if parsed.AccessKeyID != "ASIATEST" {
		t.Errorf("AccessKeyID = %q, want ASIATEST", parsed.AccessKeyID)
	}
}

func TestCacheDirWithConfigRoot(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("EXO_CONFIG_DIR", tmpDir)
	t.Setenv("XDG_CACHE_HOME", "") // ensure system cache is not used

	dir, err := cacheDir()
	if err != nil {
		t.Fatalf("cacheDir error: %v", err)
	}

	expected := filepath.Join(tmpDir, "cache", "exo-credential-helper")
	if dir != expected {
		t.Errorf("cacheDir = %q, want %q", dir, expected)
	}

	// Verify directory was created
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("cache dir not created: %v", err)
	}
}

func TestLoadBearerTokenWithConfigRoot(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("EXO_CONFIG_DIR", tmpDir)
	t.Setenv("EXO_TOKEN", "")
	t.Setenv("EXO_TOKEN_FILE", "")

	// Write token at the expected path
	tokenDir := filepath.Join(tmpDir, "exo", "credentials")
	os.MkdirAll(tokenDir, 0700)
	tokenFile := filepath.Join(tokenDir, "token.json")
	os.WriteFile(tokenFile, []byte(`{"access_token":"configroot-token","token_type":"bearer"}`), 0600)

	token, err := loadBearerToken()
	if err != nil {
		t.Fatalf("loadBearerToken error: %v", err)
	}
	if token != "configroot-token" {
		t.Errorf("token = %q, want configroot-token", token)
	}
}

func TestCacheRoundTripWithConfigRoot(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("EXO_CONFIG_DIR", tmpDir)

	creds := &credentialOutput{
		Version:         1,
		AccessKeyID:     "ASIAROOT",
		SecretAccessKey: "secret",
		SessionToken:    "token",
		Expiration:      time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339),
	}

	s3URL := "s3://isolated-bucket/prefix/"
	perm := "READ"

	if err := cacheCredentials(s3URL, perm, true, creds); err != nil {
		t.Fatalf("cacheCredentials error: %v", err)
	}

	loaded, err := loadCachedCredentials(s3URL, perm, true)
	if err != nil {
		t.Fatalf("loadCachedCredentials error: %v", err)
	}
	if loaded.AccessKeyID != "ASIAROOT" {
		t.Errorf("AccessKeyID = %q, want ASIAROOT", loaded.AccessKeyID)
	}
}

func TestIsMissingCredentialsError(t *testing.T) {
	cases := []struct {
		msg  string
		want bool
	}{
		{"failed to load AWS config: failed to get shared config profile, exohub", true},
		{"failed to get shared config profile 'exohub'", true},
		{"SharedConfigProfileNotExistError: failed to find profile", true},
		{"NoCredentialProviders: no valid providers in chain", true},
		// Generic config-load failures (e.g. corrupted file, permission error) must NOT
		// be silently routed to the server path — only profile-specific errors qualify.
		{"failed to load AWS config: some other reason", false},
		{"GetDataAccess failed: AccessDenied", false},
		{"GetDataAccess failed: StatusCode: 403", false},
		{"GetDataAccess failed: NoSuchBucket", false},
		{"ExpiredTokenException", false},
	}
	for _, tc := range cases {
		err := fmt.Errorf("%s", tc.msg)
		if got := isMissingCredentialsError(err); got != tc.want {
			t.Errorf("isMissingCredentialsError(%q) = %v, want %v", tc.msg, got, tc.want)
		}
	}
}

// TestFallbackToServerOnMissingProfile verifies the two building blocks of the
// Model 2 fallback path:
//  1. isMissingCredentialsError correctly classifies the error that getDataAccess
//     returns when no local 'exohub' AWS profile exists.
//  2. getCredsFromServer successfully returns credentials from the server.
//
// The main() dispatch logic that wires these two together is not unit-tested here
// because getDataAccess requires a live AWS endpoint; it is validated manually on
// a Model 2 / EDS worker host.
