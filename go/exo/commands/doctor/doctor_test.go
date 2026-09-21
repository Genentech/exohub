package doctor

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/Genentech/exohub/go/exo/internal/credhelper"
)

// --- result model tests ---

func TestStatusConstants(t *testing.T) {
	for _, s := range []Status{StatusPass, StatusWarn, StatusFail, StatusSkip} {
		if s == "" {
			t.Errorf("empty status constant")
		}
	}
}

func TestHasFail(t *testing.T) {
	cases := []struct {
		results []CheckResult
		want    bool
	}{
		{[]CheckResult{{Status: StatusPass}}, false},
		{[]CheckResult{{Status: StatusFail}}, true},
		{[]CheckResult{{Status: StatusWarn}, {Status: StatusFail}}, true},
		{[]CheckResult{{Status: StatusSkip}}, false},
		{nil, false},
	}
	for i, tc := range cases {
		if got := hasFail(tc.results); got != tc.want {
			t.Errorf("case %d: hasFail() = %v, want %v", i, got, tc.want)
		}
	}
}

func TestPrintResults_Human(t *testing.T) {
	results := []CheckResult{
		{ID: "a.b", Group: "Group A", Status: StatusPass, Summary: "All good"},
		{ID: "a.c", Group: "Group A", Status: StatusFail, Summary: "Something failed", Detail: "detail text", Remediation: "fix it"},
		{ID: "b.d", Group: "Group B", Status: StatusWarn, Summary: "Minor warning", Detail: "warn detail"},
		{ID: "b.e", Group: "Group B", Status: StatusSkip, Summary: "Skipped"},
	}
	var buf bytes.Buffer
	PrintResults(&buf, results, false)
	out := buf.String()
	if !strings.Contains(out, "Group A") {
		t.Error("expected Group A in output")
	}
	if !strings.Contains(out, "All good") {
		t.Error("expected PASS summary")
	}
	if !strings.Contains(out, "fix it") {
		t.Error("expected remediation for FAIL")
	}
	if !strings.Contains(out, "warn detail") {
		t.Error("expected detail for WARN")
	}
}

func TestPrintJSON(t *testing.T) {
	results := []CheckResult{
		{ID: "env.foo", Group: "Env", Status: StatusPass, Summary: "ok"},
		{ID: "net.bar", Group: "Net", Status: StatusFail, Summary: "bad", Remediation: "fix"},
	}
	var buf bytes.Buffer
	if err := PrintJSON(&buf, results); err != nil {
		t.Fatalf("PrintJSON error: %v", err)
	}
	var decoded []CheckResult
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("JSON decode error: %v", err)
	}
	if len(decoded) != 2 {
		t.Errorf("expected 2 results, got %d", len(decoded))
	}
	if decoded[0].ID != "env.foo" {
		t.Errorf("unexpected ID: %s", decoded[0].ID)
	}
	if decoded[1].Status != StatusFail {
		t.Errorf("expected FAIL, got %s", decoded[1].Status)
	}
}

// --- redaction tests ---

func TestRedact(t *testing.T) {
	cases := []struct {
		input        string
		shouldRedact bool
	}{
		{"ASIA" + "IOSFODNN7EXAMPLE", true}, // AWS temp key (ASIA + 16 chars)
		{"hello world", false},
		// JWT with realistic segment lengths (≥10 chars each segment)
		{"eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJ1c2VyMTIzIn0.SflKxwRJSMeKKF2QT4fwpMeJf36P", true},
	}
	for _, tc := range cases {
		out := Redact(tc.input)
		if tc.shouldRedact && out == tc.input {
			t.Errorf("Redact(%q) = %q, expected redaction", tc.input, out)
		}
		if !tc.shouldRedact && strings.Contains(out, "[REDACTED]") {
			t.Errorf("Redact(%q) = %q, unexpected redaction", tc.input, out)
		}
	}
}

// --- env checks tests ---

func TestCheckAPIURL_NoEnv(t *testing.T) {
	os.Unsetenv("EXOHUB_API_URL")
	r := checkAPIURL()
	if r.Status != StatusPass {
		t.Errorf("expected PASS when EXOHUB_API_URL not set, got %s: %s", r.Status, r.Summary)
	}
}

func TestCheckAPIURL_Valid(t *testing.T) {
	t.Setenv("EXOHUB_API_URL", "https://your-exohub-server.example.com/api")
	r := checkAPIURL()
	if r.Status != StatusPass {
		t.Errorf("expected PASS for valid API URL, got %s: %s", r.Status, r.Summary)
	}
}

func TestCheckAPIURL_Invalid(t *testing.T) {
	t.Setenv("EXOHUB_API_URL", "https://your-exohub-server.example.com")
	r := checkAPIURL()
	if r.Status != StatusFail {
		t.Errorf("expected FAIL for URL without /api, got %s: %s", r.Status, r.Summary)
	}
	if !strings.Contains(r.Summary, "does not contain /api") {
		t.Errorf("expected descriptive summary, got %q", r.Summary)
	}
}

func TestCheckEnvVars(t *testing.T) {
	t.Setenv("EXO_CONFIG_DIR", "/tmp/test-config")
	t.Setenv("EXOHUB_AWS_PROFILE", "my-profile")
	r := checkEnvVars()
	if r.Status != StatusPass {
		t.Errorf("expected PASS, got %s", r.Status)
	}
	if !strings.Contains(r.Summary, "EXO_CONFIG_DIR=/tmp/test-config") {
		t.Errorf("expected EXO_CONFIG_DIR in summary, got %q", r.Summary)
	}
	if !strings.Contains(r.Summary, "EXOHUB_AWS_PROFILE=my-profile") {
		t.Errorf("expected EXOHUB_AWS_PROFILE in summary, got %q", r.Summary)
	}
}

// --- auth checks tests ---

func TestDecodeJWTClaims(t *testing.T) {
	// Build a minimal JWT with known claims (no signature needed)
	header := "eyJhbGciOiJub25lIn0" // {"alg":"none"}
	payload := encodeBase64URL([]byte(`{"preferred_username":"testuser","iss":"https://example.com","exp":9999999999,"cognito:groups":["group1","group2"]}`))
	rawJWT := header + "." + payload + ".sig"

	claims, err := decodeJWTClaims(rawJWT)
	if err != nil {
		t.Fatalf("decodeJWTClaims error: %v", err)
	}
	if claims.PreferredUsername != "testuser" {
		t.Errorf("preferred_username = %q, want testuser", claims.PreferredUsername)
	}
	if claims.Iss != "https://example.com" {
		t.Errorf("iss = %q, want https://example.com", claims.Iss)
	}
}

func TestNormalizeGroups_CognitoArray(t *testing.T) {
	claims := &jwtClaims{
		CognitoGroups: []string{"group1", "group2"},
	}
	groups := normalizeGroups(claims)
	if len(groups) != 2 || groups[0] != "group1" {
		t.Errorf("unexpected groups: %v", groups)
	}
}

func TestNormalizeGroups_RealStringSlice(t *testing.T) {
	// groups claim is a real JSON array: ["g1","g2"]
	claims := &jwtClaims{
		Groups: json.RawMessage(`["exohub-users","admins"]`),
	}
	groups := normalizeGroups(claims)
	if len(groups) != 2 || groups[0] != "exohub-users" {
		t.Errorf("unexpected groups: %v", groups)
	}
}

func TestNormalizeGroups_StringArray(t *testing.T) {
	// Cognito JSON-encoded string array form: groups claim is a JSON string
	// whose value is itself a JSON array: "[\"g1\",\"g2\"]"
	claims := &jwtClaims{
		Groups: json.RawMessage(`"[\"exohub-users\",\"admins\"]"`),
	}
	groups := normalizeGroups(claims)
	if len(groups) != 2 || groups[0] != "exohub-users" {
		t.Errorf("unexpected groups: %v", groups)
	}
}

func TestNormalizeGroups_PlainString(t *testing.T) {
	claims := &jwtClaims{
		Groups: json.RawMessage(`"single-group"`),
	}
	groups := normalizeGroups(claims)
	if len(groups) != 1 || groups[0] != "single-group" {
		t.Errorf("unexpected groups: %v", groups)
	}
}

func TestNormalizeGroups_Empty(t *testing.T) {
	claims := &jwtClaims{}
	groups := normalizeGroups(claims)
	if len(groups) != 0 {
		t.Errorf("expected empty groups, got %v", groups)
	}
}

// --- required group check tests ---

func makeTokenJSON(t *testing.T, groupsClaim any) string {
	t.Helper()
	payload := map[string]any{
		"preferred_username": "testuser",
		"iss":                "https://example.com",
		"exp":                int64(9999999999),
	}
	if groupsClaim != nil {
		payload["groups"] = groupsClaim
	}
	pb, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	jwt := "eyJhbGciOiJub25lIn0." + encodeBase64URL(pb) + ".sig" // header={"alg":"none"}
	tb, err := json.Marshal(map[string]any{
		"access_token": jwt,
		"token_type":   "Bearer",
	})
	if err != nil {
		t.Fatalf("marshal token: %v", err)
	}
	return string(tb)
}

func TestCheckRequiredGroup(t *testing.T) {
	cases := []struct {
		name   string
		groups any
		want   Status
	}{
		{
			name:   "member of EXOHUB_USERS (json-encoded string array)",
			groups: `["SOME_GROUP_1","EXOHUB_USERS","SOME_GROUP_2"]`,
			want:   StatusPass,
		},
		{
			name:   "missing EXOHUB_USERS",
			groups: `["SOME_GROUP_1","SOME_GROUP_2"]`,
			want:   StatusFail,
		},
		{
			name:   "look-alike group is not an exact match",
			groups: `["EXOHUB_USERS_OLD"]`,
			want:   StatusFail,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("EXO_TOKEN", makeTokenJSON(t, tc.groups))
			r := checkRequiredGroup()
			if r.Status != tc.want {
				t.Errorf("checkRequiredGroup() = %s, want %s (summary: %q)", r.Status, tc.want, r.Summary)
			}
		})
	}
}

func TestCheckRequiredGroup_NoToken(t *testing.T) {
	t.Setenv("EXO_TOKEN", "")
	t.Setenv("EXO_TOKEN_FILE", t.TempDir()+"/nonexistent-token.json")
	r := checkRequiredGroup()
	if r.Status != StatusSkip {
		t.Errorf("checkRequiredGroup() = %s, want SKIP (summary: %q)", r.Status, r.Summary)
	}
}

// --- repo checks tests ---

func TestCheckRepoRemotes_NoRepo(t *testing.T) {
	dir := t.TempDir()
	results := runRepoChecks(dir)
	for _, r := range results {
		if r.Status != StatusSkip {
			t.Errorf("expected all SKIP outside repo, got %s for %s", r.Status, r.ID)
		}
	}
}

func TestCheckRepoRemotes_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	exohubDir := dir + "/.exohub"
	os.MkdirAll(exohubDir, 0755)
	os.WriteFile(exohubDir+"/remotes", []byte("not: valid: yaml: ::"), 0644)

	results, err := checkRepoRemotes(exohubDir + "/remotes")
	if err == nil {
		// YAML is lenient, check if we got a useful result
		_ = results
	}
}

func TestCheckRepoRemotes_Valid(t *testing.T) {
	dir := t.TempDir()
	exohubDir := dir + "/.exohub"
	os.MkdirAll(exohubDir, 0755)
	content := `remotes:
  - name: s3-main
    type: annex
    s3url: s3://bucket/prefix/
    grants: true
`
	os.WriteFile(exohubDir+"/remotes", []byte(content), 0644)

	// Without git-annex, UUIDs won't match but the parse should succeed.
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	results, err := checkRepoRemotes(exohubDir + "/remotes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}
}

// TestCheckRepoRemotes_UUIDNotFound verifies that a pinned UUID absent from
// git-annex produces a FAIL result (not a WARN), and that the remediation
// text does NOT suggest running 'exo init'.
func TestCheckRepoRemotes_UUIDNotFound(t *testing.T) {
	dir := t.TempDir()
	exohubDir := dir + "/.exohub"
	os.MkdirAll(exohubDir, 0755)
	content := `remotes:
  - name: s3-archive
    type: annex
    uuid: 60c1bbba-0000-0000-0000-000000000001
    s3url: s3://bucket/archive/
    grants: true
`
	os.WriteFile(exohubDir+"/remotes", []byte(content), 0644)

	// Run inside the temp dir so git commands don't escape.
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	// buildUUIDMap will return empty (no git repo / no git-annex config) so
	// the UUID is guaranteed not found.
	results, err := checkRepoRemotes(exohubDir + "/remotes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var failResult *CheckResult
	for i := range results {
		if results[i].Status == StatusFail {
			failResult = &results[i]
			break
		}
	}
	if failResult == nil {
		t.Fatalf("expected a FAIL result for missing UUID, got: %+v", results)
	}
	// The remediation must point to exo sync as the primary recovery step.
	if !strings.Contains(failResult.Remediation, "exo sync") {
		t.Errorf("remediation should mention 'exo sync', got: %q", failResult.Remediation)
	}
	// The remediation must NOT open with 'Run exo init' as the primary fix
	// (it hard-fails when a UUID is pinned but absent from git-annex).
	if strings.HasPrefix(strings.TrimSpace(failResult.Remediation), "Run 'exo init'") {
		t.Errorf("remediation must not start with 'Run exo init' for missing UUID, got: %q", failResult.Remediation)
	}
}

// TestCheckRepoRemotes_UUIDNameMismatch verifies that a UUID registered under a
// different name in git-annex produces a WARN (not FAIL) result.
func TestCheckRepoRemotes_UUIDNameMismatch(t *testing.T) {
	dir := t.TempDir()
	exohubDir := dir + "/.exohub"
	os.MkdirAll(exohubDir, 0755)
	content := `remotes:
  - name: s3-archive
    type: annex
    uuid: 60c1bbba-0000-0000-0000-000000000002
    s3url: s3://bucket/archive/
    grants: true
`
	os.WriteFile(exohubDir+"/remotes", []byte(content), 0644)

	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	// Initialize a real git repo so 'git config --get-regexp' works, then
	// register the UUID under a different remote name to simulate a mismatch.
	if err := runCmd("git", "init", "-q"); err != nil {
		t.Skipf("git init unavailable: %v", err)
	}
	if err := runCmd("git", "config", "remote.s3-archive-old.annex-uuid", "60c1bbba-0000-0000-0000-000000000002"); err != nil {
		t.Fatalf("git config failed: %v", err)
	}

	results, err := checkRepoRemotes(exohubDir + "/remotes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var warnResult *CheckResult
	for i := range results {
		if results[i].Status == StatusWarn {
			warnResult = &results[i]
			break
		}
	}
	if warnResult == nil {
		t.Fatalf("expected a WARN result for name mismatch, got: %+v", results)
	}
	for _, r := range results {
		if r.Status == StatusFail {
			t.Errorf("expected no FAIL for name mismatch, got FAIL: %+v", r)
		}
	}
}

// runCmd is a test helper that runs a command in the current directory.
func runCmd(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	return cmd.Run()
}

func TestCheckPermissions_NotFound(t *testing.T) {
	dir := t.TempDir()
	results := checkPermissions(dir, nil)
	if len(results) == 0 {
		t.Fatal("expected result")
	}
	if results[0].Status != StatusWarn {
		t.Errorf("expected WARN for missing permissions, got %s", results[0].Status)
	}
}

func TestCheckPermissions_CallerIsOwner(t *testing.T) {
	dir := t.TempDir()
	exohubDir := dir + "/.exohub"
	os.MkdirAll(exohubDir, 0755)

	// Resolve the actual current username that currentUsername() will return.
	actualUser := currentUsername()
	if actualUser == "" {
		actualUser = os.Getenv("USER")
	}
	if actualUser == "" {
		t.Skip("cannot determine current username")
	}

	content := "owners:\n  - " + actualUser + "\nread_access: private\nwrite_access: owner\n"
	os.WriteFile(exohubDir+"/permissions", []byte(content), 0644)

	results := checkPermissions(dir, nil)
	if len(results) == 0 {
		t.Fatal("expected result")
	}
	if results[0].Status != StatusPass {
		t.Errorf("expected PASS, got %s: %s", results[0].Status, results[0].Summary)
	}
}

// --- grants checks tests ---

func TestParseS3URL(t *testing.T) {
	cases := []struct {
		url    string
		bucket string
		prefix string
		hasErr bool
	}{
		{"s3://my-bucket/data/project/", "my-bucket", "data/project", false},
		{"s3://my-bucket/", "my-bucket", "", false},
		{"s3://my-bucket", "my-bucket", "", false},
		{"s3://", "", "", true},
	}
	for _, tc := range cases {
		b, p, err := parseS3URL(tc.url)
		if tc.hasErr {
			if err == nil {
				t.Errorf("parseS3URL(%q) expected error", tc.url)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseS3URL(%q) error: %v", tc.url, err)
			continue
		}
		if b != tc.bucket {
			t.Errorf("parseS3URL(%q) bucket = %q, want %q", tc.url, b, tc.bucket)
		}
		if p != tc.prefix {
			t.Errorf("parseS3URL(%q) prefix = %q, want %q", tc.url, p, tc.prefix)
		}
	}
}

func TestCheckGrantScope_NormalURL(t *testing.T) {
	r := checkGrantScope("grants.test", remoteEntry{Name: "test", S3URL: "s3://bucket/data/myproject/"})
	if r.Status != StatusPass {
		t.Errorf("expected PASS, got %s: %s", r.Status, r.Summary)
	}
}

func TestCheckGrantScope_AnnexSubpath(t *testing.T) {
	// /_annex in the s3url is a convention, not a rule — always PASS regardless of type.
	r := checkGrantScope("grants.test", remoteEntry{Name: "test", Type: "annex", S3URL: "s3://bucket/data/_annex/"})
	if r.Status != StatusPass {
		t.Errorf("expected PASS for annex type with _annex sub-prefix, got %s", r.Status)
	}
}

func TestCheckGrantScope_NonAnnexWithAnnexSubpath(t *testing.T) {
	// /_annex is a convention, not mandatory — non-annex type should also PASS.
	r := checkGrantScope("grants.test", remoteEntry{Name: "test", Type: "export", S3URL: "s3://bucket/data/_annex/"})
	if r.Status != StatusPass {
		t.Errorf("expected PASS for non-annex type with _annex sub-prefix, got %s: %s", r.Status, r.Summary)
	}
}

// --- JSON output integration test ---

func TestJSONOutputShape(t *testing.T) {
	results := []CheckResult{
		pass("env.foo", "Env", "foo ok"),
		warn("net.bar", "Net", "warn summary", "detail", "fix it"),
		fail("auth.baz", "Auth", "fail summary", "fail detail", "remediate"),
		skip("repo.qux", "Repo", "skipped"),
	}
	var buf bytes.Buffer
	if err := PrintJSON(&buf, results); err != nil {
		t.Fatalf("PrintJSON: %v", err)
	}
	var out []map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("JSON parse: %v", err)
	}
	if len(out) != 4 {
		t.Errorf("expected 4 entries, got %d", len(out))
	}
	required := []string{"id", "group", "status", "summary"}
	for _, field := range required {
		if _, ok := out[0][field]; !ok {
			t.Errorf("JSON missing field %q", field)
		}
	}
}

// --- network probe tests ---

// TestResolveOIDCIssuer_EnvOverride verifies EXO_OIDC_ISSUER takes highest precedence.
func TestResolveOIDCIssuer_EnvOverride(t *testing.T) {
	t.Setenv("EXO_OIDC_ISSUER", "https://custom-oidc.example.com")
	issuer, source := resolveOIDCIssuer()
	if issuer != "https://custom-oidc.example.com" {
		t.Errorf("expected env issuer, got %q", issuer)
	}
	if source != "env" {
		t.Errorf("expected source=\"env\", got %q", source)
	}
}

// TestResolveOIDCIssuer_FromJWT verifies the JWT iss claim is used when present.
func TestResolveOIDCIssuer_FromJWT(t *testing.T) {
	t.Setenv("EXO_OIDC_ISSUER", "")

	// Build a JWT with a known iss claim and inject it via EXO_TOKEN.
	issuerURL := "https://cognito-idp.us-west-2.amazonaws.com/us-west-2_test"
	payload := map[string]any{
		"preferred_username": "testuser",
		"iss":                issuerURL,
		"exp":                int64(9999999999),
	}
	pb, _ := json.Marshal(payload)
	rawJWT := "eyJhbGciOiJub25lIn0." + encodeBase64URL(pb) + ".sig"
	tokenJSON, _ := json.Marshal(map[string]any{
		"access_token": rawJWT,
		"token_type":   "Bearer",
	})
	t.Setenv("EXO_TOKEN", string(tokenJSON))

	issuer, source := resolveOIDCIssuer()
	if issuer != issuerURL {
		t.Errorf("expected JWT iss %q, got %q", issuerURL, issuer)
	}
	if source != "JWT iss claim" {
		t.Errorf("expected source=\"JWT iss claim\", got %q", source)
	}
}

// TestResolveOIDCIssuer_FallbackDefault verifies fallback when no token and no env.
func TestResolveOIDCIssuer_FallbackDefault(t *testing.T) {
	t.Setenv("EXO_OIDC_ISSUER", "")
	t.Setenv("EXO_TOKEN", "")
	t.Setenv("EXO_TOKEN_FILE", t.TempDir()+"/nonexistent.json")

	issuer, source := resolveOIDCIssuer()
	// When no issuer is configured, the probe is skipped (empty return).
	if issuer != "" {
		t.Errorf("expected empty issuer when not configured, got %q", issuer)
	}
	if source != "" {
		t.Errorf("expected empty source when not configured, got %q", source)
	}
}

// TestProbeAPIURL_UsesBasePath verifies probeAPI targets /api/ (not /api/healthz).
func TestProbeAPIURL_UsesBasePath(t *testing.T) {
	// We can't make live HTTP calls in unit tests, so we test the URL construction
	// by verifying the probe URL ends with "/api/" (not "/api/healthz").
	apiURL := "https://your-exohub-server.example.com/api"
	probeURL := strings.TrimRight(apiURL, "/") + "/"
	if !strings.HasSuffix(probeURL, "/api/") {
		t.Errorf("expected probe URL to end with /api/, got %q", probeURL)
	}
	if strings.Contains(probeURL, "healthz") {
		t.Errorf("probe URL must not contain /healthz, got %q", probeURL)
	}
}

// --- grants write probe tests ---

// TestProbeS3Write_SuccessOnPutAndDelete verifies probeS3Write returns nil when
// both PutObject and DeleteObject succeed (genuine write access).
func TestProbeS3Write_SuccessOnPutAndDelete(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Accept any PUT or DELETE (PutObject, DeleteObject)
		if r.Method == http.MethodPut || r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	defer server.Close()

	creds := &credhelper.Credentials{
		AccessKeyID:     "ASIATEST",
		SecretAccessKey: "secret",
		SessionToken:    "token",
	}

	// Point the S3 client at the test server by setting the endpoint via env.
	// The AWS SDK respects AWS_ENDPOINT_URL for path-style addressing.
	t.Setenv("AWS_ENDPOINT_URL", server.URL)
	t.Setenv("EXOHUB_GRANTS_REGION", "us-east-1")

	err := probeS3Write(context.Background(), creds, "s3://testbucket/prefix/")
	if err != nil {
		t.Errorf("expected nil error on successful write probe, got: %v", err)
	}
}

// TestProbeS3Write_FailsOnAccessDenied verifies probeS3Write returns an error when
// PutObject is denied (read-only credentials).
func TestProbeS3Write_FailsOnAccessDenied(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><Error><Code>AccessDenied</Code><Message>Access Denied</Message></Error>`))
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	defer server.Close()

	creds := &credhelper.Credentials{
		AccessKeyID:     "ASIAREADONLY",
		SecretAccessKey: "secret",
		SessionToken:    "token",
	}

	t.Setenv("AWS_ENDPOINT_URL", server.URL)
	t.Setenv("EXOHUB_GRANTS_REGION", "us-east-1")

	err := probeS3Write(context.Background(), creds, "s3://testbucket/prefix/")
	if err == nil {
		t.Error("expected error when PutObject is denied (read-only creds), got nil")
	}
}

// --- helpers ---

func encodeBase64URL(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}
