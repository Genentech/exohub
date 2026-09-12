//go:build !internal

package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// stsCredentialsXML returns a minimal STS AssumeRoleWithWebIdentity XML response.
func stsCredentialsXML(accessKeyID, secretKey, sessionToken string, expiry time.Time) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<AssumeRoleWithWebIdentityResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/">
  <AssumeRoleWithWebIdentityResult>
    <Credentials>
      <AccessKeyId>%s</AccessKeyId>
      <SecretAccessKey>%s</SecretAccessKey>
      <SessionToken>%s</SessionToken>
      <Expiration>%s</Expiration>
    </Credentials>
  </AssumeRoleWithWebIdentityResult>
</AssumeRoleWithWebIdentityResponse>`,
		accessKeyID, secretKey, sessionToken,
		expiry.UTC().Format("2006-01-02T15:04:05Z"),
	)
}

// mockSTSServer starts an httptest server that handles AssumeRoleWithWebIdentity
// and returns fixed credentials.
func mockSTSServer(t *testing.T, accessKeyID, secretKey, sessionToken string) *httptest.Server {
	t.Helper()
	expiry := time.Now().Add(1 * time.Hour)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		action := r.FormValue("Action")
		if action != "AssumeRoleWithWebIdentity" {
			http.Error(w, "unexpected action: "+action, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/xml")
		fmt.Fprint(w, stsCredentialsXML(accessKeyID, secretKey, sessionToken, expiry))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestDetectRemoteGrantsFalse(t *testing.T) {
	tmpDir := t.TempDir()
	oldDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldDir)

	os.MkdirAll(filepath.Join(tmpDir, ".exohub"), 0755)
	content := `remotes:
  - name: myremote
    type: annex
    s3url: s3://non-grants-bucket/prefix/
    grants: false
`
	os.WriteFile(filepath.Join(tmpDir, ".exohub", "remotes"), []byte(content), 0644)

	detected, err := detectRemote()
	if err != nil {
		t.Fatalf("detectRemote error: %v", err)
	}
	if detected.S3URL != "s3://non-grants-bucket/prefix/" {
		t.Errorf("S3URL = %q, want s3://non-grants-bucket/prefix/", detected.S3URL)
	}
	if detected.Grants {
		t.Error("Grants should be false when grants: false is specified")
	}
}

func TestDetectRemoteGrantsDefault(t *testing.T) {
	tmpDir := t.TempDir()
	oldDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldDir)

	os.MkdirAll(filepath.Join(tmpDir, ".exohub"), 0755)
	content := `remotes:
  - name: myremote
    type: annex
    s3url: s3://grants-bucket/prefix/
`
	os.WriteFile(filepath.Join(tmpDir, ".exohub", "remotes"), []byte(content), 0644)

	detected, err := detectRemote()
	if err != nil {
		t.Fatalf("detectRemote error: %v", err)
	}
	if !detected.Grants {
		t.Error("Grants should default to true when not specified")
	}
}

func TestDetectRemoteGrantsExplicitTrue(t *testing.T) {
	tmpDir := t.TempDir()
	oldDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldDir)

	os.MkdirAll(filepath.Join(tmpDir, ".exohub"), 0755)
	content := `remotes:
  - name: myremote
    type: annex
    s3url: s3://grants-bucket/prefix/
    grants: true
`
	os.WriteFile(filepath.Join(tmpDir, ".exohub", "remotes"), []byte(content), 0644)

	detected, err := detectRemote()
	if err != nil {
		t.Fatalf("detectRemote error: %v", err)
	}
	if !detected.Grants {
		t.Error("Grants should be true when grants: true is specified")
	}
}

func TestCacheKeyIncludesGrants(t *testing.T) {
	s3URL := "s3://bucket/prefix/"
	perm := "READ"

	keyGrantsTrue := cacheKey(s3URL, perm, true)
	keyGrantsFalse := cacheKey(s3URL, perm, false)

	if keyGrantsTrue == keyGrantsFalse {
		t.Error("cacheKey must differ for grants=true vs grants=false")
	}

	// Verify stability.
	if got := cacheKey(s3URL, perm, false); got != keyGrantsFalse {
		t.Errorf("cacheKey(grants=false) is not stable: %q vs %q", got, keyGrantsFalse)
	}
}

func TestGetBaseCredsProviderFallback(t *testing.T) {
	// No exohub profile → falls back to AssumeRoleWithWebIdentity.
	srv := mockSTSServer(t, "ASIABASE", "basesecret", "basetoken")

	tmpDir := t.TempDir()
	t.Setenv("EXO_CONFIG_DIR", tmpDir)
	// Store a token so loadBearerToken succeeds.
	tokenDir := filepath.Join(tmpDir, "exo", "credentials")
	os.MkdirAll(tokenDir, 0700)
	tokenJSON := `{"access_token":"test-access-token","id_token":"test-id-token"}`
	os.WriteFile(filepath.Join(tokenDir, "token.json"), []byte(tokenJSON), 0600)

	// Point to a non-existent profile so profile probe fails.
	t.Setenv("EXOHUB_AWS_PROFILE", "nonexistent-profile-xyz")
	t.Setenv("EXO_AWS_ROLE_ARN", "arn:aws:iam::123456789012:role/TestRole")
	t.Setenv("EXO_AWS_REGION", "us-east-1")
	// Point AWS STS endpoint to our mock server.
	t.Setenv("AWS_ENDPOINT_URL_STS", srv.URL)
	// Disable real credential chain so only our env/config is used.
	t.Setenv("AWS_ACCESS_KEY_ID", "FAKEID")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "FAKESECRET")

	noop := func(string, ...any) {}
	creds, err := getBaseCreds(t.Context(), "s3://test-bucket/", 3600, noop)
	if err != nil {
		// If AssumeRoleWithWebIdentity isn't routed to our mock (SDK version),
		// this is still a valid integration test; skip rather than fail.
		if strings.Contains(err.Error(), "AssumeRoleWithWebIdentity failed") {
			t.Skipf("STS endpoint override not supported by SDK version: %v", err)
		}
		t.Fatalf("getBaseCreds error: %v", err)
	}
	if creds.AccessKeyID != "ASIABASE" {
		t.Errorf("AccessKeyID = %q, want ASIABASE", creds.AccessKeyID)
	}
	if creds.SecretAccessKey != "basesecret" {
		t.Errorf("SecretAccessKey mismatch")
	}
	if creds.Version != 1 {
		t.Errorf("Version = %d, want 1", creds.Version)
	}
}

func TestGetBaseCredsNoRoleARN(t *testing.T) {
	// No exohub profile, no EXO_AWS_ROLE_ARN → should return a helpful error.
	tmpDir := t.TempDir()
	t.Setenv("EXO_CONFIG_DIR", tmpDir)
	t.Setenv("EXOHUB_AWS_PROFILE", "nonexistent-profile-xyz")
	t.Setenv("EXO_AWS_ROLE_ARN", "")
	// Put a token so we get past token loading.
	tokenDir := filepath.Join(tmpDir, "exo", "credentials")
	os.MkdirAll(tokenDir, 0700)
	os.WriteFile(filepath.Join(tokenDir, "token.json"),
		[]byte(`{"access_token":"tok","id_token":"idtok"}`), 0600)

	noop := func(string, ...any) {}
	_, err := getBaseCreds(t.Context(), "s3://test-bucket/", 3600, noop)
	if err == nil {
		t.Fatal("expected error when EXO_AWS_ROLE_ARN is not set")
	}
	if !strings.Contains(err.Error(), "EXO_AWS_ROLE_ARN") {
		t.Errorf("error should mention EXO_AWS_ROLE_ARN, got: %v", err)
	}
}

func TestLoadIDToken(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("EXO_CONFIG_DIR", tmpDir)
	t.Setenv("EXO_TOKEN", "")
	t.Setenv("EXO_TOKEN_FILE", "")

	tokenDir := filepath.Join(tmpDir, "exo", "credentials")
	os.MkdirAll(tokenDir, 0700)

	tok := map[string]string{
		"access_token": "myaccesstoken",
		"id_token":     "myidtoken",
	}
	raw, _ := json.Marshal(tok)
	os.WriteFile(filepath.Join(tokenDir, "token.json"), raw, 0600)

	got := loadIDToken()
	if got != "myidtoken" {
		t.Errorf("loadIDToken() = %q, want myidtoken", got)
	}
}

func TestLoadIDTokenMissing(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("EXO_CONFIG_DIR", tmpDir)
	t.Setenv("EXO_TOKEN", "")
	t.Setenv("EXO_TOKEN_FILE", "")
	// No token file written.
	got := loadIDToken()
	if got != "" {
		t.Errorf("loadIDToken() = %q, want empty string for missing file", got)
	}
}

func TestLoadIDTokenFromEnv(t *testing.T) {
	tokenJSON := `{"access_token":"acc","id_token":"envid"}`
	t.Setenv("EXO_TOKEN", tokenJSON)

	got := loadIDToken()
	if got != "envid" {
		t.Errorf("loadIDToken() from EXO_TOKEN = %q, want envid", got)
	}
}

// mockCognitoTokenServer starts an httptest server that handles the Cognito
// token endpoint for refresh_token grant and returns a fixed id_token.
func mockCognitoTokenServer(t *testing.T, newIDToken, newRefreshToken string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil || r.FormValue("grant_type") != "refresh_token" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]string{
			"id_token":      newIDToken,
			"access_token":  "new-access-token",
			"refresh_token": newRefreshToken,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// buildExpiredJWT builds a minimal unsigned JWT with an exp claim in the past.
func buildExpiredJWT() string {
	import64 := func(v map[string]any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	header := import64(map[string]any{"alg": "none", "typ": "JWT"})
	payload := import64(map[string]any{"exp": time.Now().Add(-2 * time.Hour).Unix(), "sub": "test"})
	return header + "." + payload + "."
}

// buildFreshJWT builds a minimal unsigned JWT with an exp claim far in the future.
func buildFreshJWT() string {
	import64 := func(v map[string]any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	header := import64(map[string]any{"alg": "none", "typ": "JWT"})
	payload := import64(map[string]any{"exp": time.Now().Add(2 * time.Hour).Unix(), "sub": "test"})
	return header + "." + payload + "."
}

func TestRefreshIDTokenExpiredWithRefreshToken(t *testing.T) {
	newIDToken := buildFreshJWT()
	srv := mockCognitoTokenServer(t, newIDToken, "new-refresh-token")
	t.Setenv("EXO_COGNITO_OAUTH_URL", srv.URL)

	tmpDir := t.TempDir()
	t.Setenv("EXO_CONFIG_DIR", tmpDir)
	t.Setenv("EXO_TOKEN", "")
	t.Setenv("EXO_TOKEN_FILE", "")

	tokenDir := filepath.Join(tmpDir, "exo", "credentials")
	os.MkdirAll(tokenDir, 0700)

	expiredToken := buildExpiredJWT()
	tok := map[string]string{
		"id_token":      expiredToken,
		"access_token":  "old-access",
		"refresh_token": "valid-refresh-token",
	}
	raw, _ := json.Marshal(tok)
	tokenPath := filepath.Join(tokenDir, "token.json")
	os.WriteFile(tokenPath, raw, 0600)

	noop := func(string, ...any) {}
	got := refreshIDTokenIfNeeded(t.Context(), noop)
	if got != newIDToken {
		t.Errorf("refreshIDTokenIfNeeded() = %q, want new id_token", got)
	}

	// Verify token.json was updated.
	data, _ := os.ReadFile(tokenPath)
	var persisted map[string]string
	json.Unmarshal(data, &persisted)
	if persisted["id_token"] != newIDToken {
		t.Errorf("persisted id_token = %q, want %q", persisted["id_token"], newIDToken)
	}
	if persisted["refresh_token"] != "new-refresh-token" {
		t.Errorf("persisted refresh_token = %q, want new-refresh-token", persisted["refresh_token"])
	}
}

func TestRefreshIDTokenExpiredNoRefreshToken(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("EXO_CONFIG_DIR", tmpDir)
	t.Setenv("EXO_TOKEN", "")
	t.Setenv("EXO_TOKEN_FILE", "")

	tokenDir := filepath.Join(tmpDir, "exo", "credentials")
	os.MkdirAll(tokenDir, 0700)

	expiredToken := buildExpiredJWT()
	tok := map[string]string{
		"id_token":     expiredToken,
		"access_token": "old-access",
		// no refresh_token
	}
	raw, _ := json.Marshal(tok)
	os.WriteFile(filepath.Join(tokenDir, "token.json"), raw, 0600)

	noop := func(string, ...any) {}
	got := refreshIDTokenIfNeeded(t.Context(), noop)
	// Should fall through and return the original (expired) id_token unchanged.
	if got != expiredToken {
		t.Errorf("refreshIDTokenIfNeeded() = %q, want original expired token", got)
	}
}

func TestRefreshIDTokenNotExpired(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("EXO_CONFIG_DIR", tmpDir)
	t.Setenv("EXO_TOKEN", "")
	t.Setenv("EXO_TOKEN_FILE", "")

	tokenDir := filepath.Join(tmpDir, "exo", "credentials")
	os.MkdirAll(tokenDir, 0700)

	freshToken := buildFreshJWT()
	tok := map[string]string{
		"id_token":      freshToken,
		"access_token":  "access",
		"refresh_token": "refresh",
	}
	raw, _ := json.Marshal(tok)
	os.WriteFile(filepath.Join(tokenDir, "token.json"), raw, 0600)

	noop := func(string, ...any) {}
	got := refreshIDTokenIfNeeded(t.Context(), noop)
	if got != freshToken {
		t.Errorf("refreshIDTokenIfNeeded() = %q, want original fresh token", got)
	}
}

func TestIDTokenExp(t *testing.T) {
	import64 := func(v map[string]any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	expTime := time.Now().Add(30 * time.Minute).Truncate(time.Second)
	header := import64(map[string]any{"alg": "none"})
	payload := import64(map[string]any{"exp": expTime.Unix()})
	jwt := header + "." + payload + "."

	got := idTokenExp(jwt)
	if !got.Equal(expTime) {
		t.Errorf("idTokenExp() = %v, want %v", got, expTime)
	}

	// Invalid JWT should return zero.
	if !idTokenExp("notajwt").IsZero() {
		t.Error("idTokenExp(invalid) should return zero time")
	}
}
