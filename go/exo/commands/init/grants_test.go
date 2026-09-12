package init

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadOrCreatePermissionsFromFile(t *testing.T) {
	dir := t.TempDir()
	exohubDir := filepath.Join(dir, ".exohub")
	if err := os.MkdirAll(exohubDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(exohubDir, "permissions"), []byte(`
owners:
  - alice
  - bob
viewers:
  - charlie
read_access: viewers
write_access: owners
`), 0644); err != nil {
		t.Fatal(err)
	}

	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(dir)

	perms, err := loadOrCreatePermissions()
	if err != nil {
		t.Fatal(err)
	}
	if len(perms.Owners) != 2 || perms.Owners[0] != "alice" || perms.Owners[1] != "bob" {
		t.Errorf("owners = %v, want [alice bob]", perms.Owners)
	}
	if len(perms.Viewers) != 1 || perms.Viewers[0] != "charlie" {
		t.Errorf("viewers = %v, want [charlie]", perms.Viewers)
	}
	if perms.ReadAccess != "viewers" {
		t.Errorf("read_access = %q, want %q", perms.ReadAccess, "viewers")
	}
	if perms.WriteAccess != "owners" {
		t.Errorf("write_access = %q, want %q", perms.WriteAccess, "owners")
	}
}

func TestLoadOrCreatePermissionsDefault(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(dir)

	perms, err := loadOrCreatePermissions()
	if err != nil {
		t.Fatal(err)
	}
	if len(perms.Owners) != 1 {
		t.Fatalf("expected 1 owner, got %d", len(perms.Owners))
	}
	if perms.ReadAccess != "viewers" {
		t.Errorf("read_access = %q, want %q", perms.ReadAccess, "viewers")
	}
	if perms.WriteAccess != "owners" {
		t.Errorf("write_access = %q, want %q", perms.WriteAccess, "owners")
	}

	// Verify file was written to disk with 2-space indentation
	path := filepath.Join(".exohub", "permissions")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected .exohub/permissions to be created on disk: %v", err)
	}
	content := string(data)
	// yaml.v3 with SetIndent(2): list items are "  - value" (2-space indent)
	if !strings.Contains(content, "  - ") {
		t.Errorf("expected 2-space indented list, got:\n%s", content)
	}
	if strings.Contains(content, "    - ") {
		t.Errorf("unexpected 4-space indentation in:\n%s", content)
	}
}

func TestLoadOrCreatePermissionsEmptyOwners(t *testing.T) {
	dir := t.TempDir()
	exohubDir := filepath.Join(dir, ".exohub")
	if err := os.MkdirAll(exohubDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(exohubDir, "permissions"), []byte(`
owners: []
read_access: viewers
write_access: owners
`), 0644); err != nil {
		t.Fatal(err)
	}

	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(dir)

	_, err := loadOrCreatePermissions()
	if err == nil {
		t.Fatal("expected error for empty owners")
	}
}

func TestSyncGrantsForRemote(t *testing.T) {
	flagYes = true
	defer func() { flagYes = false }()
	// Set up a mock API server that handles both verify and sync
	var received grantsSyncRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/grants/verify":
			// Return drift detected
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"in_sync":false,"drift":{"missing_grants":[{"grantee":"testuser"}],"extra_grants":[],"permission_mismatches":[]}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/grants/sync":
			if r.Header.Get("Authorization") == "" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			json.NewDecoder(r.Body).Decode(&received)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"synced"}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	// Set up temp dir with permissions file
	dir := t.TempDir()
	exohubDir := filepath.Join(dir, ".exohub")
	os.MkdirAll(exohubDir, 0755)
	os.WriteFile(filepath.Join(exohubDir, "permissions"), []byte(`
owners:
  - testuser
read_access: viewers
write_access: owners
`), 0644)

	// Set up a fake token file
	credsDir := filepath.Join(dir, "creds")
	os.MkdirAll(credsDir, 0700)
	tokenFile := filepath.Join(credsDir, "token.json")
	os.WriteFile(tokenFile, []byte(`{"access_token":"test-token-123","token_type":"Bearer"}`), 0600)

	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(dir)

	// Point to mock server
	t.Setenv("EXOHUB_API_URL", server.URL)
	// Override token file location via XDG
	t.Setenv("XDG_CONFIG_HOME", dir)
	os.MkdirAll(filepath.Join(dir, "exo", "credentials"), 0700)
	os.WriteFile(filepath.Join(dir, "exo", "credentials", "token.json"),
		[]byte(`{"access_token":"test-token-123","token_type":"Bearer"}`), 0600)

	err := syncGrantsForRemote("s3://test-bucket/test-prefix/")
	if err != nil {
		t.Fatalf("syncGrantsForRemote failed: %v", err)
	}
	if received.S3URL != "s3://test-bucket/test-prefix/" {
		t.Errorf("s3url = %q, want %q", received.S3URL, "s3://test-bucket/test-prefix/")
	}
	if len(received.Permissions.Owners) != 1 || received.Permissions.Owners[0] != "testuser" {
		t.Errorf("owners = %v, want [testuser]", received.Permissions.Owners)
	}
}

func TestSyncGrantsForRemoteInSync(t *testing.T) {
	flagYes = true
	defer func() { flagYes = false }()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/grants/verify" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"in_sync":true,"drift":{"missing_grants":[],"extra_grants":[],"permission_mismatches":[]}}`))
			return
		}
		t.Errorf("unexpected call to %s %s (should have skipped sync)", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	dir := t.TempDir()
	exohubDir := filepath.Join(dir, ".exohub")
	os.MkdirAll(exohubDir, 0755)
	os.WriteFile(filepath.Join(exohubDir, "permissions"), []byte(`
owners:
  - testuser
read_access: viewers
write_access: owners
`), 0644)

	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(dir)

	t.Setenv("EXOHUB_API_URL", server.URL)
	os.MkdirAll(filepath.Join(dir, "exo", "credentials"), 0700)
	os.WriteFile(filepath.Join(dir, "exo", "credentials", "token.json"),
		[]byte(`{"access_token":"test-token","token_type":"Bearer"}`), 0600)
	t.Setenv("XDG_CONFIG_HOME", dir)

	err := syncGrantsForRemote("s3://bucket/prefix/")
	if err != nil {
		t.Fatalf("expected no error when in sync, got: %v", err)
	}
}

func TestSyncGrantsForRemote403(t *testing.T) {
	flagYes = true
	defer func() { flagYes = false }()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/grants/verify" {
			// Return drift so sync is attempted
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"in_sync":false,"drift":{"missing_grants":[{}],"extra_grants":[],"permission_mismatches":[]}}`))
			return
		}
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"detail":"not an owner"}`))
	}))
	defer server.Close()

	dir := t.TempDir()
	exohubDir := filepath.Join(dir, ".exohub")
	os.MkdirAll(exohubDir, 0755)
	os.WriteFile(filepath.Join(exohubDir, "permissions"), []byte(`
owners:
  - someone
read_access: viewers
write_access: owners
`), 0644)

	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(dir)

	t.Setenv("EXOHUB_API_URL", server.URL)
	// Set up token
	os.MkdirAll(filepath.Join(dir, "exo", "credentials"), 0700)
	os.WriteFile(filepath.Join(dir, "exo", "credentials", "token.json"),
		[]byte(`{"access_token":"test-token","token_type":"Bearer"}`), 0600)
	t.Setenv("XDG_CONFIG_HOME", dir)

	err := syncGrantsForRemote("s3://bucket/prefix/")
	if err == nil {
		t.Fatal("expected error for 403 response")
	}
	if !contains(err.Error(), "Permission denied") {
		t.Errorf("error = %q, want to contain 'Permission denied'", err.Error())
	}
}

func TestSyncGrantsForRemote404NonFatal(t *testing.T) {
	flagYes = true
	defer func() { flagYes = false }()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`404 page not found`))
	}))
	defer server.Close()

	dir := t.TempDir()
	exohubDir := filepath.Join(dir, ".exohub")
	os.MkdirAll(exohubDir, 0755)
	os.WriteFile(filepath.Join(exohubDir, "permissions"), []byte(`
owners:
  - testuser
read_access: viewers
write_access: owners
`), 0644)

	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(dir)

	t.Setenv("EXOHUB_API_URL", server.URL)
	os.MkdirAll(filepath.Join(dir, "exo", "credentials"), 0700)
	os.WriteFile(filepath.Join(dir, "exo", "credentials", "token.json"),
		[]byte(`{"access_token":"test-token","token_type":"Bearer"}`), 0600)
	t.Setenv("XDG_CONFIG_HOME", dir)

	err := syncGrantsForRemote("s3://bucket/prefix/")
	if err != nil {
		t.Fatalf("expected no error for 404 (non-fatal), got: %v", err)
	}
}

func TestSyncGrantsForRemoteNonOwnerPublicReadAccess(t *testing.T) {
	// Non-owners on a dataset with read_access: public must NOT trigger a grants
	// sync — the owner already provisioned the public grant.
	apiCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiCalled = true
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	dir := t.TempDir()
	exohubDir := filepath.Join(dir, ".exohub")
	os.MkdirAll(exohubDir, 0755)
	// Current user is NOT in owners; read_access is public.
	os.WriteFile(filepath.Join(exohubDir, "permissions"), []byte(`
owners:
  - someoneelse
read_access: public
write_access: owners
`), 0644)

	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(dir)

	t.Setenv("EXOHUB_API_URL", server.URL)
	// Provide a token so currentUsername() returns a known non-owner value.
	os.MkdirAll(filepath.Join(dir, "exo", "credentials"), 0700)
	header := "eyJhbGciOiJSUzI1NiJ9" // base64url({"alg":"RS256"})
	payload := "eyJwcmVmZXJyZWRfdXNlcm5hbWUiOiJub25vd25lciJ9" // base64url({"preferred_username":"nonowner"})
	sig := "ZmFrZXNpZw" // base64url(fakesig)
	fakeJWT := header + "." + payload + "." + sig
	os.WriteFile(filepath.Join(dir, "exo", "credentials", "token.json"),
		[]byte(`{"access_token":"`+fakeJWT+`","token_type":"Bearer"}`), 0600)
	t.Setenv("XDG_CONFIG_HOME", dir)

	err := syncGrantsForRemote("s3://bucket/prefix/")
	if err != nil {
		t.Fatalf("expected nil for non-owner with read_access:public, got: %v", err)
	}
	if apiCalled {
		t.Error("grants API must not be called when non-owner reads a public dataset")
	}
}

func TestUsernameFromToken(t *testing.T) {
	// Build a fake JWT with preferred_username claim
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"preferred_username":"lelongs","sub":"12345"}`))
	sig := base64.RawURLEncoding.EncodeToString([]byte(`fake-signature`))
	fakeJWT := header + "." + payload + "." + sig

	// Set up a temp token file
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "exo", "credentials"), 0700)
	tokenData := `{"access_token":"` + fakeJWT + `","token_type":"Bearer"}`
	os.WriteFile(filepath.Join(dir, "exo", "credentials", "token.json"), []byte(tokenData), 0600)
	t.Setenv("XDG_CONFIG_HOME", dir)

	got := usernameFromToken()
	if got != "lelongs" {
		t.Errorf("usernameFromToken() = %q, want %q", got, "lelongs")
	}
}

func TestUsernameFromTokenFallback(t *testing.T) {
	// No token file available — should return empty string
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	// Don't create the token file

	got := usernameFromToken()
	if got != "" {
		t.Errorf("usernameFromToken() = %q, want empty", got)
	}
}

func TestCurrentUsernamePrefersJWT(t *testing.T) {
	// Build a fake JWT with preferred_username
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"preferred_username":"jwtuser"}`))
	sig := base64.RawURLEncoding.EncodeToString([]byte(`sig`))
	fakeJWT := header + "." + payload + "." + sig

	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "exo", "credentials"), 0700)
	tokenData := `{"access_token":"` + fakeJWT + `","token_type":"Bearer"}`
	os.WriteFile(filepath.Join(dir, "exo", "credentials", "token.json"), []byte(tokenData), 0600)
	t.Setenv("XDG_CONFIG_HOME", dir)

	got := currentUsername()
	if got != "jwtuser" {
		t.Errorf("currentUsername() = %q, want %q", got, "jwtuser")
	}
}

// captureStdout redirects os.Stdout to a pipe and returns the captured output
// after calling f.
func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	old := os.Stdout
	os.Stdout = w
	f()
	w.Close()
	os.Stdout = old
	var buf strings.Builder
	io.Copy(&buf, r)
	r.Close()
	return buf.String()
}

func TestVerifyGrantsRegistryDrift(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"in_sync":false,"drift":{"missing_grants":[],"extra_grants":[],"permission_mismatches":[],"registry_drift":[{"grantee":"alice","permission":"read_access: public -> authenticated"}]}}`))
	}))
	defer server.Close()

	perms := &permissionsConfig{Owners: []string{"alice"}, ReadAccess: "viewers", WriteAccess: "owners"}

	var inSync bool
	var verifyErr error
	out := captureStdout(t, func() {
		inSync, verifyErr = verifyGrants(server.URL, "s3://bucket/prefix/", perms)
	})

	if verifyErr != nil {
		t.Fatalf("unexpected error: %v", verifyErr)
	}
	if inSync {
		t.Error("expected inSync=false")
	}
	if !strings.Contains(out, "registry (_grants.json) differs") {
		t.Errorf("expected registry drift message, got: %q", out)
	}
	if !strings.Contains(out, "alice") {
		t.Errorf("expected grantee 'alice' in output, got: %q", out)
	}
	if !strings.Contains(out, "read_access: public -> authenticated") {
		t.Errorf("expected permission detail in output, got: %q", out)
	}
}

func TestVerifyGrantsMetadataDriftFallback(t *testing.T) {
	// Older API: emits metadata_drift (backward-compat alias) nested in drift, no registry_drift key.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"in_sync":false,"drift":{"missing_grants":[],"extra_grants":[],"permission_mismatches":[],"metadata_drift":[{"grantee":"bob","permission":"read_access: viewers -> public"}]}}`))
	}))
	defer server.Close()

	perms := &permissionsConfig{Owners: []string{"bob"}, ReadAccess: "viewers", WriteAccess: "owners"}

	var inSync bool
	var verifyErr error
	out := captureStdout(t, func() {
		inSync, verifyErr = verifyGrants(server.URL, "s3://bucket/prefix/", perms)
	})

	if verifyErr != nil {
		t.Fatalf("unexpected error: %v", verifyErr)
	}
	if inSync {
		t.Error("expected inSync=false")
	}
	if !strings.Contains(out, "registry (_grants.json) differs") {
		t.Errorf("expected registry drift message via metadata_drift fallback, got: %q", out)
	}
	if !strings.Contains(out, "bob") {
		t.Errorf("expected grantee 'bob' in output, got: %q", out)
	}
}

func TestVerifyGrantsNoRegistryDriftOldAPI(t *testing.T) {
	// Old API response with no registry_drift or metadata_drift fields — must not error
	// and must not print a registry drift message.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"in_sync":false,"drift":{"missing_grants":[{}],"extra_grants":[],"permission_mismatches":[]}}`))
	}))
	defer server.Close()

	perms := &permissionsConfig{Owners: []string{"carol"}, ReadAccess: "viewers", WriteAccess: "owners"}

	var inSync bool
	var verifyErr error
	out := captureStdout(t, func() {
		inSync, verifyErr = verifyGrants(server.URL, "s3://bucket/prefix/", perms)
	})

	if verifyErr != nil {
		t.Fatalf("unexpected error: %v", verifyErr)
	}
	if inSync {
		t.Error("expected inSync=false")
	}
	if strings.Contains(out, "registry (_grants.json) differs") {
		t.Errorf("unexpected registry drift message for old-API response, got: %q", out)
	}
	// The normal missing_grants line should still appear.
	if !strings.Contains(out, "missing grant") {
		t.Errorf("expected missing grant message, got: %q", out)
	}
}

// TestHasGrantsWriteAccess_PassesVerifyAndNoCache verifies that hasGrantsWriteAccess
// invokes the credential helper with both --verify and --no-cache flags.
func TestHasGrantsWriteAccess_PassesVerifyAndNoCache(t *testing.T) {
	// Create a fake credential helper script that records its arguments and exits 0.
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-cred-helper")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho \"$@\" > \""+dir+"/args.txt\"\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EXO_CREDENTIAL_HELPER_BIN", script)

	got := hasGrantsWriteAccess("s3://bucket/prefix/")
	if !got {
		t.Error("expected true when helper exits 0")
	}

	argsData, err := os.ReadFile(filepath.Join(dir, "args.txt"))
	if err != nil {
		t.Fatalf("helper was not called: %v", err)
	}
	args := strings.TrimSpace(string(argsData))
	if !strings.Contains(args, "--verify") {
		t.Errorf("expected --verify in helper args, got: %q", args)
	}
	if !strings.Contains(args, "--no-cache") {
		t.Errorf("expected --no-cache in helper args, got: %q", args)
	}
}

// TestHasGrantsWriteAccess_ReturnsFalseOnWriteProbeFailure verifies that
// hasGrantsWriteAccess returns false when the credential helper exits non-zero
// (as it will when --verify detects that the vended creds are read-only).
func TestHasGrantsWriteAccess_ReturnsFalseOnWriteProbeFailure(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-cred-helper")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EXO_CREDENTIAL_HELPER_BIN", script)

	got := hasGrantsWriteAccess("s3://bucket/prefix/")
	if got {
		t.Error("expected false when helper exits 1 (write probe failure)")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
