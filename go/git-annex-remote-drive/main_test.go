package main

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// resetState resets all global state between tests.
func resetState() {
	config = map[string]string{}
	exportName = ""
	remoteName = ""
	drv = nil
	rootFolderID = ""
	folderCacheMu.Lock()
	folderCache = map[string]string{}
	folderCacheMu.Unlock()
}

// captureOutput redirects writeLine output to a buffer for testing.
func captureOutput(fn func()) string {
	var buf bytes.Buffer
	orig := outw
	outw = bufio.NewWriter(&buf)
	origWriteLine := writeLine
	writeLine = func(line string) {
		outw.WriteString(line)
		outw.WriteByte('\n')
		outw.Flush()
	}
	fn()
	outw = orig
	writeLine = origWriteLine
	return buf.String()
}

// ---- sanitizeMsg ----

func TestSanitizeMsg(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"simple message", "simple message"},
		{"line1\nline2", "line1 line2"},
		{"has\rreturns", "has returns"},
		{"  spaces  ", "spaces"},
		{"", ""},
	}
	for _, tt := range tests {
		got := sanitizeMsg(tt.input)
		if got != tt.want {
			t.Errorf("sanitizeMsg(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// ---- envOr ----

func TestEnvOr(t *testing.T) {
	t.Setenv("TEST_ENVVAR_PRESENT", "custom")
	if got := envOr("TEST_ENVVAR_PRESENT", "default"); got != "custom" {
		t.Errorf("expected 'custom', got %q", got)
	}
	if got := envOr("TEST_ENVVAR_ABSENT_XYZ", "default"); got != "default" {
		t.Errorf("expected 'default', got %q", got)
	}
}

// ---- Folder cache ----

func TestFolderCache(t *testing.T) {
	resetState()

	cacheFolderID("datasets/mydir", "fld123")
	if got := cachedFolderID("datasets/mydir"); got != "fld123" {
		t.Errorf("expected 'fld123', got %q", got)
	}
	if got := cachedFolderID("notexist"); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestFolderCacheConcurrency(t *testing.T) {
	resetState()
	done := make(chan struct{})
	for i := 0; i < 20; i++ {
		go func(n int) {
			key := strings.Repeat("a", n+1)
			cacheFolderID(key, "id")
			_ = cachedFolderID(key)
			done <- struct{}{}
		}(i)
	}
	for i := 0; i < 20; i++ {
		<-done
	}
}

// ---- exportParentAndFile ----

func TestExportParentAndFile_NoExportName(t *testing.T) {
	resetState()
	rootFolderID = "root123"
	exportName = ""

	_, _, err := exportParentAndFile()
	if err == nil {
		t.Fatal("expected error when exportName is empty")
	}
}

func TestExportParentAndFile_TopLevel(t *testing.T) {
	resetState()
	rootFolderID = "root123"
	exportName = "file.parquet"

	parentID, fileName, err := exportParentAndFile()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parentID != "root123" {
		t.Errorf("parentID = %q, want 'root123'", parentID)
	}
	if fileName != "file.parquet" {
		t.Errorf("fileName = %q, want 'file.parquet'", fileName)
	}
}

func TestExportParentAndFile_Nested(t *testing.T) {
	// Test that the filename extraction from a nested export path works.
	// We test the path splitting logic directly rather than calling
	// exportParentAndFile(), which would require a live Drive API.
	exportName = "gwasdb-studies/2024/results.parquet"
	fileName := filepath.Base(exportName)
	dir := filepath.Dir(exportName)
	if fileName != "results.parquet" {
		t.Errorf("fileName = %q, want 'results.parquet'", fileName)
	}
	if dir != "gwasdb-studies/2024" {
		t.Errorf("dir = %q, want 'gwasdb-studies/2024'", dir)
	}
}

// ---- INITREMOTE ----

func TestHandleInitRemote_MissingConfig(t *testing.T) {
	resetState()
	config["drive_path"] = ""
	// Override getConfigFromAnnex to avoid reading from stdin
	origGetConfig := getConfigFromAnnex
	defer func() { getConfigFromAnnex = origGetConfig }()
	getConfigFromAnnex = func(key string) string { return "" }

	out := captureOutput(handleInitRemote)
	if !strings.Contains(out, "INITREMOTE-FAILURE") {
		t.Errorf("expected INITREMOTE-FAILURE, got: %s", out)
	}
}

func TestHandleInitRemote_WithConfig(t *testing.T) {
	resetState()
	config["drive_path"] = "/My Drive/datasets/test"

	out := captureOutput(handleInitRemote)
	if !strings.Contains(out, "INITREMOTE-SUCCESS") {
		t.Errorf("expected INITREMOTE-SUCCESS, got: %s", out)
	}
}

// ---- EXPORT ----

func TestHandleExport(t *testing.T) {
	resetState()
	handleExport("gwasdb-studies/2024/results.parquet")
	if exportName != "gwasdb-studies/2024/results.parquet" {
		t.Errorf("exportName = %q, want 'gwasdb-studies/2024/results.parquet'", exportName)
	}
}

// ---- PKCE ----

func TestGeneratePKCE(t *testing.T) {
	verifier, challenge, err := generatePKCE()
	if err != nil {
		t.Fatalf("generatePKCE failed: %v", err)
	}
	if len(verifier) == 0 {
		t.Error("verifier should not be empty")
	}
	if len(challenge) == 0 {
		t.Error("challenge should not be empty")
	}
	if verifier == challenge {
		t.Error("verifier and challenge should differ")
	}
	// Two calls should produce different values
	v2, _, _ := generatePKCE()
	if verifier == v2 {
		t.Error("successive calls should produce different verifiers")
	}
}

// ---- Token storage ----

func TestTokenStorePath(t *testing.T) {
	home, _ := os.UserHomeDir()
	path := tokenStorePath("my-drive")
	expected := home + "/.config/exo/drive-tokens/my-drive.json"
	if path != expected {
		t.Errorf("tokenStorePath = %q, want %q", path, expected)
	}
}

func TestSaveLoadToken(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/test-token.json"
	tf := &tokenFile{
		AccessToken:  "acc123",
		RefreshToken: "ref456",
		TokenType:    "Bearer",
	}
	if err := saveToken(path, tf); err != nil {
		t.Fatalf("saveToken: %v", err)
	}
	// Verify permissions
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if fi.Mode()&0o077 != 0 {
		t.Errorf("token file should not be readable by group/other, mode=%o", fi.Mode())
	}

	loaded, err := loadStoredToken(path)
	if err != nil {
		t.Fatalf("loadStoredToken: %v", err)
	}
	if loaded.AccessToken != "acc123" {
		t.Errorf("AccessToken = %q, want 'acc123'", loaded.AccessToken)
	}
	if loaded.RefreshToken != "ref456" {
		t.Errorf("RefreshToken = %q, want 'ref456'", loaded.RefreshToken)
	}
}

func TestLoadStoredToken_Missing(t *testing.T) {
	_, err := loadStoredToken("/nonexistent/path/token.json")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

// ---- Client credentials ----

func TestClientCredentials_Defaults(t *testing.T) {
	os.Unsetenv("GOOGLE_CLIENT_ID")
	os.Unsetenv("GOOGLE_CLIENT_SECRET")

	clientID, clientSecret := clientCredentials()
	if clientID != builtinClientID {
		t.Errorf("expected builtinClientID, got %q", clientID)
	}
	if clientSecret != builtinClientSecret {
		t.Errorf("expected builtinClientSecret, got %q", clientSecret)
	}
}

func TestClientCredentials_EnvOverride(t *testing.T) {
	t.Setenv("GOOGLE_CLIENT_ID", "custom-id")
	t.Setenv("GOOGLE_CLIENT_SECRET", "custom-secret")

	clientID, clientSecret := clientCredentials()
	if clientID != "custom-id" {
		t.Errorf("expected 'custom-id', got %q", clientID)
	}
	if clientSecret != "custom-secret" {
		t.Errorf("expected 'custom-secret', got %q", clientSecret)
	}
}

// ---- Drive path parsing logic (offline) ----

// resolveRootDrivePathParts extracts path components from a drive_path string.
// It mirrors the logic in resolveRootDrivePath but without API calls.
func resolveRootDrivePathParts(drivePath string) []string {
	parts := strings.Split(strings.Trim(drivePath, "/"), "/")
	if len(parts) > 0 && strings.EqualFold(parts[0], "my drive") {
		parts = parts[1:]
	}
	var result []string
	for _, p := range parts {
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

func TestResolveRootDrivePathParts(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"/My Drive/datasets/exohub", []string{"datasets", "exohub"}},
		{"/My Drive/gwasdb", []string{"gwasdb"}},
		{"datasets/mydir", []string{"datasets", "mydir"}},
		{"/My Drive", nil},
		{"My Drive/reports/2024", []string{"reports", "2024"}},
		{"folderid123", []string{"folderid123"}},
	}
	for _, tt := range tests {
		got := resolveRootDrivePathParts(tt.input)
		if len(got) != len(tt.want) {
			t.Errorf("resolveRootDrivePathParts(%q) = %v, want %v", tt.input, got, tt.want)
			continue
		}
		for i, p := range got {
			if p != tt.want[i] {
				t.Errorf("resolveRootDrivePathParts(%q)[%d] = %q, want %q", tt.input, i, p, tt.want[i])
			}
		}
	}
}

// ---- resolvePath with cached folders ----

func TestResolvePath_Empty(t *testing.T) {
	resetState()
	rootFolderID = "root123"
	id, err := resolvePath("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "root123" {
		t.Errorf("expected 'root123', got %q", id)
	}
}

func TestResolvePath_Dot(t *testing.T) {
	resetState()
	rootFolderID = "root123"
	id, err := resolvePath(".")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "root123" {
		t.Errorf("expected 'root123', got %q", id)
	}
}

func TestResolvePath_AllCached(t *testing.T) {
	resetState()
	rootFolderID = "root123"
	// Pre-populate cache
	cacheFolderID("a", "fld-a")
	cacheFolderID("a/b", "fld-ab")
	cacheFolderID("a/b/c", "fld-abc")

	id, err := resolvePath("a/b/c")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "fld-abc" {
		t.Errorf("expected 'fld-abc', got %q", id)
	}
}

// ---- isTerminal ----

func TestIsTerminal(t *testing.T) {
	// In test environment, stderr is typically not a TTY; just ensure no panic
	_ = isTerminal()
}
