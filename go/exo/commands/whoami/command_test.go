package whoami

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func encodeBase64URL(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}

func makeTokenJSON(t *testing.T, claims map[string]any, refreshToken string, expiry time.Time) string {
	t.Helper()
	pb, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	jwt := "eyJhbGciOiJub25lIn0." + encodeBase64URL(pb) + ".sig"
	tok := map[string]any{
		"access_token": jwt,
	}
	if refreshToken != "" {
		tok["refresh_token"] = refreshToken
	}
	if !expiry.IsZero() {
		tok["expiry"] = expiry.Format(time.RFC3339)
	}
	tb, err := json.Marshal(tok)
	if err != nil {
		t.Fatalf("marshal token: %v", err)
	}
	return string(tb)
}

func executeCommand(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := NewCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	err := cmd.Execute()
	return buf.String(), err
}

func TestWhoami_NotLoggedIn(t *testing.T) {
	t.Setenv("EXO_TOKEN", "")
	t.Setenv("EXO_TOKEN_FILE", t.TempDir()+"/nonexistent-token.json")
	t.Setenv("EXO_CONFIG_DIR", t.TempDir())

	out, err := executeCommand(t)
	if err == nil {
		t.Fatal("expected error when not logged in")
	}
	if !strings.Contains(err.Error(), "not logged in") || !strings.Contains(err.Error(), "exo login") {
		t.Errorf("unexpected error message: %v", err)
	}
	if strings.Contains(out, "Identity") {
		t.Errorf("expected no whoami output when not logged in, got: %q", out)
	}
}

func TestWhoami_JSONShape(t *testing.T) {
	t.Setenv("EXO_CONFIG_DIR", t.TempDir())
	t.Setenv("EXO_TOKEN", makeTokenJSON(t, map[string]any{
		"preferred_username": "jdoe",
		"name":               "Jane Doe",
		"email":              "jdoe@example.com",
		"iss":                "https://idp.example.com",
		"department":         "Engineering",
		"cost_center":        "CC-100",
		"manager_username":   "mgr",
		"groups":             []string{"EXOHUB_USERS"},
	}, "a-refresh-token", time.Now().Add(42*time.Minute)))

	out, err := executeCommand(t, "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result Result
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("failed to decode JSON output: %v\noutput: %s", err, out)
	}

	if result.Identity.Username != "jdoe" {
		t.Errorf("Identity.Username = %q, want jdoe", result.Identity.Username)
	}
	if result.Identity.Name != "Jane Doe" || result.Identity.Email != "jdoe@example.com" {
		t.Errorf("unexpected identity: %+v", result.Identity)
	}
	if result.Identity.Issuer != "https://idp.example.com" {
		t.Errorf("Identity.Issuer = %q", result.Identity.Issuer)
	}
	if result.Identity.Provider == "" {
		t.Errorf("expected non-empty provider")
	}
	if !result.Session.CanRefresh {
		t.Errorf("expected CanRefresh=true")
	}
	if result.Session.ExpiresIn == "" || !strings.Contains(result.Session.ExpiresIn, "expires in") {
		t.Errorf("unexpected ExpiresIn: %q", result.Session.ExpiresIn)
	}
	if result.Organisation == nil {
		t.Fatal("expected organisation to be populated")
	}
	if result.Organisation.Department != "Engineering" || result.Organisation.CostCenter != "CC-100" || result.Organisation.ManagerUsername != "mgr" {
		t.Errorf("unexpected organisation: %+v", result.Organisation)
	}
	if len(result.Organisation.ExohubGroups) != 1 || result.Organisation.ExohubGroups[0] != "EXOHUB_USERS" {
		t.Errorf("unexpected groups: %v", result.Organisation.ExohubGroups)
	}
	if result.AWS.Profile == "" {
		t.Errorf("expected AWS profile to be set")
	}
	if len(result.Extra) != 0 {
		t.Errorf("expected no extra fields in OSS build, got: %v", result.Extra)
	}
}

func TestWhoami_OpaqueAccessToken(t *testing.T) {
	t.Setenv("EXO_CONFIG_DIR", t.TempDir())
	// Simulate the OSS OIDC provider storing a real (non-JWT, opaque) OAuth
	// access token rather than an ID token: DecodeJWTClaims fails, so
	// identity/organisation must be empty with an explicit note, exit 0.
	tok := map[string]any{
		"access_token":  "opaque-access-token-xyz",
		"refresh_token": "a-refresh-token",
		"expiry":        time.Now().Add(time.Hour).Format(time.RFC3339),
	}
	tb, err := json.Marshal(tok)
	if err != nil {
		t.Fatalf("marshal token: %v", err)
	}
	t.Setenv("EXO_TOKEN", string(tb))

	out, err := executeCommand(t, "--json")
	if err != nil {
		t.Fatalf("unexpected error for opaque access token: %v", err)
	}
	var result Result
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("failed to decode JSON: %v", err)
	}
	if result.Identity.Username != "" || result.Identity.Name != "" || result.Identity.Email != "" {
		t.Errorf("expected empty identity claims for opaque token, got %+v", result.Identity)
	}
	if result.Identity.Note == "" {
		t.Errorf("expected a note explaining identity claims are unavailable, got empty")
	}
	if result.Organisation != nil {
		t.Errorf("expected no organisation info for opaque token, got %+v", result.Organisation)
	}
	// Session info (expiry, refreshability) is still derived from the OAuth
	// token itself, independent of claim decoding.
	if result.Session.ExpiresAt == "" {
		t.Errorf("expected session expiry to still be reported for opaque token")
	}
	if !result.Session.CanRefresh {
		t.Errorf("expected CanRefresh=true (refresh token present)")
	}

	humanOut, err := executeCommand(t)
	if err != nil {
		t.Fatalf("unexpected error for opaque access token (human output): %v", err)
	}
	if !strings.Contains(humanOut, "not a decodable ID token") {
		t.Errorf("expected human output to explain unavailable identity, got: %s", humanOut)
	}
}

func TestWhoami_ExpiredSession(t *testing.T) {
	t.Setenv("EXO_CONFIG_DIR", t.TempDir())
	t.Setenv("EXO_TOKEN", makeTokenJSON(t, map[string]any{
		"preferred_username": "jdoe",
	}, "", time.Now().Add(-3*time.Hour)))

	out, err := executeCommand(t, "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result Result
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("failed to decode JSON: %v", err)
	}
	if !strings.Contains(result.Session.ExpiresIn, "expired") {
		t.Errorf("expected expired message, got %q", result.Session.ExpiresIn)
	}
	if result.Session.CanRefresh {
		t.Errorf("expected CanRefresh=false (no refresh token)")
	}
}

func TestWhoami_NoSecretsInOutput(t *testing.T) {
	t.Setenv("EXO_CONFIG_DIR", t.TempDir())
	const refreshSecret = "super-secret-refresh-token-value"
	tokenJSON := makeTokenJSON(t, map[string]any{
		"preferred_username": "jdoe",
	}, refreshSecret, time.Now().Add(time.Hour))
	t.Setenv("EXO_TOKEN", tokenJSON)

	var accessToken struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal([]byte(tokenJSON), &accessToken); err != nil {
		t.Fatalf("failed to extract access token: %v", err)
	}

	for _, args := range [][]string{{}, {"--json"}} {
		out, err := executeCommand(t, args...)
		if err != nil {
			t.Fatalf("unexpected error for args %v: %v", args, err)
		}
		if strings.Contains(out, refreshSecret) {
			t.Errorf("output leaked refresh token for args %v: %s", args, out)
		}
		if strings.Contains(out, accessToken.AccessToken) {
			t.Errorf("output leaked raw access token for args %v: %s", args, out)
		}
	}
}

func TestWhoami_HumanOutput(t *testing.T) {
	t.Setenv("EXO_CONFIG_DIR", t.TempDir())
	t.Setenv("EXO_TOKEN", makeTokenJSON(t, map[string]any{
		"preferred_username": "jdoe",
	}, "", time.Now().Add(time.Hour)))

	out, err := executeCommand(t)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Identity") || !strings.Contains(out, "jdoe") {
		t.Errorf("unexpected human output: %s", out)
	}
	if !strings.Contains(out, "Session") {
		t.Errorf("expected Session section, got: %s", out)
	}
}
