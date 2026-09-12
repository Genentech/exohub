package manifest

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Genentech/exohub/go/exo/commandutil"
)

func TestNormalizeManifest(t *testing.T) {
	input := map[string]any{
		"name":         "sample",
		"url":          "https://example.test/repo.git",
		"ref":          "main",
		"repo-dir":     "/tmp/repo",
		"remote-type":  "annex",
		"with-remotes": []string{"origin"},
		"paths":        []string{"data/*"},
		"queue":        "exohub-sync-aws",
	}

	normalized := commandutil.NormalizeManifest(input)

	if got := normalized["repo-dir"]; got != "/tmp/repo" {
		t.Fatalf("repo-dir mismatch: %#v", got)
	}
	if got := normalized["remote-type"]; got != "annex" {
		t.Fatalf("remote-type mismatch: %#v", got)
	}
	if got := normalized["queue"]; got != "exohub-sync-aws" {
		t.Fatalf("queue mismatch: %#v", got)
	}
	if got := normalized["with-remotes"]; !equalAnySlice(got, []any{"origin"}) {
		t.Fatalf("with-remotes mismatch: %#v", got)
	}
	if got := normalized["paths"]; !equalAnySlice(got, []any{"data/*"}) {
		t.Fatalf("paths mismatch: %#v", got)
	}
}

func TestInferManifestTypeFromActivities(t *testing.T) {
	normalized := map[string]any{
		"activities":  map[string]any{"mirror-plan": map[string]any{"manifest": "m.yaml"}},
		"remote-type": "annex",
	}
	got, err := commandutil.InferManifestType(normalized)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "workflow" {
		t.Fatalf("expected workflow, got %s", got)
	}
}

func TestInferManifestTypeMissingRemoteType(t *testing.T) {
	normalized := map[string]any{
		"name": "sample",
	}
	if _, err := commandutil.InferManifestType(normalized); err == nil {
		t.Fatalf("expected error for missing remote-type")
	}
}

func TestValidateManifestAnnexRequiredFields(t *testing.T) {
	normalized := map[string]any{
		"name":        "sample",
		"url":         "https://example.test/repo.git",
		"ref":         "main",
		"remote-type": "annex",
	}
	errors := commandutil.ValidateManifest("annex", normalized)
	// repo-dir is optional; validation should pass with just name, url, ref, and remote-type
	if len(errors) != 0 {
		t.Fatalf("expected no errors, got: %v", errors)
	}
}

func TestValidateManifestExportRequiresTo(t *testing.T) {
	normalized := map[string]any{
		"name":        "sample",
		"url":         "https://example.test/repo.git",
		"ref":         "main",
		"repo-dir":    "/tmp/repo",
		"remote-type": "export",
	}
	errors := commandutil.ValidateManifest("export", normalized)
	if !containsError(errors, "to is required") {
		t.Fatalf("expected to error, got: %v", errors)
	}
}

func TestValidateManifestWorkflowExportNeedsFromTo(t *testing.T) {
	normalized := map[string]any{
		"name":        "sample",
		"url":         "https://example.test/repo.git",
		"ref":         "main",
		"remote-type": "export",
		"to":          "dest",
	}
	errors := commandutil.ValidateManifest("workflow", normalized)
	if !containsError(errors, "from and to are required when remote-type is export") {
		t.Fatalf("expected from/to error, got: %v", errors)
	}
}

func TestValidateManifestQueueType(t *testing.T) {
	normalized := map[string]any{
		"name":        "sample",
		"url":         "https://example.test/repo.git",
		"ref":         "main",
		"repo-dir":    "/tmp/repo",
		"remote-type": "annex",
		"queue":       5,
	}
	errors := commandutil.ValidateManifest("annex", normalized)
	if !containsError(errors, "queue must be a string") {
		t.Fatalf("expected queue error, got: %v", errors)
	}
}

func TestValidateManifestRemoteTypeMulti(t *testing.T) {
	normalized := map[string]any{
		"name":        "sample",
		"url":         "https://example.test/repo.git",
		"ref":         "main",
		"repo-dir":    "/tmp/repo",
		"remote-type": []any{"annex", "export"},
	}
	errors := commandutil.ValidateManifest("annex", normalized)
	if !containsError(errors, "remote-type must be a single value ('annex' or 'export')") {
		t.Fatalf("expected remote-type single value error, got: %v", errors)
	}
}

func TestValidateWithSchema(t *testing.T) {
	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "schema.json")
	schema := `{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "required": ["name"],
  "properties": { "name": { "type": "string" } }
}`
	if err := os.WriteFile(schemaPath, []byte(schema), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}

	okManifest := map[string]any{"name": "ok"}
	data, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	errors, err := commandutil.ValidateWithSchemaBytes(data, okManifest)
	if len(errors) != 0 {
		t.Fatalf("expected no schema errors, got: %v", errors)
	}

	badManifest := map[string]any{}
	errors, err = commandutil.ValidateWithSchemaBytes(data, badManifest)
	if err != nil {
		t.Fatalf("unexpected schema error: %v", err)
	}
	if len(errors) == 0 {
		t.Fatalf("expected schema errors")
	}
}

func TestFetchSchemaFromAPI(t *testing.T) {
	schema := `{"type":"object","properties":{"name":{"type":"string"}}}`
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("network listener unavailable: %v", err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/schemas/annex-manifest.schema.json" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(schema))
	}))
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)

	t.Setenv("EXOHUB_API_URL", server.URL+"/api")
	data, err := commandutil.FetchSchema("annex")
	if err != nil {
		t.Fatalf("fetch schema failed: %v", err)
	}
	if strings.TrimSpace(string(data)) != schema {
		t.Fatalf("unexpected schema body: %s", string(data))
	}
}

func TestValidateSchemaMissingFile(t *testing.T) {
	normalized := map[string]any{"name": "sample"}
	_, err := commandutil.ValidateSchema("annex", "/nope/schema.json", normalized)
	if err == nil || !strings.Contains(err.Error(), "schema not found") {
		t.Fatalf("expected missing schema error, got: %v", err)
	}
}

func TestValidateSchemaWorkflowNeedsSchema(t *testing.T) {
	normalized := map[string]any{"name": "sample"}
	_, err := commandutil.ValidateSchema("workflow", "", normalized)
	if err == nil || !strings.Contains(err.Error(), "workflow schema is not available") {
		t.Fatalf("expected workflow schema error, got: %v", err)
	}
}

func TestSchemaNameForTypeUnsupported(t *testing.T) {
	if _, err := commandutil.SchemaNameForType("workflow"); err == nil {
		t.Fatalf("expected error for unsupported type")
	}
}

func TestBuildSchemaURL(t *testing.T) {
	got, err := commandutil.BuildSchemaURL("https://example.test/api", "annex-manifest.schema.json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "https://example.test/api/schemas/annex-manifest.schema.json" {
		t.Fatalf("unexpected schema url: %s", got)
	}
}

func TestValidateSchemaFetchesAndValidates(t *testing.T) {
	schema := `{"type":"object","required":["name"],"properties":{"name":{"type":"string"}}}`
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("network listener unavailable: %v", err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/schemas/annex-manifest.schema.json" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(schema))
	}))
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)

	t.Setenv("EXOHUB_API_URL", server.URL+"/api")

	okManifest := map[string]any{"name": "sample"}
	errors, err := commandutil.ValidateSchema("annex", "", okManifest)
	if err != nil {
		t.Fatalf("unexpected schema error: %v", err)
	}
	if len(errors) != 0 {
		t.Fatalf("expected no schema errors, got: %v", errors)
	}

	badManifest := map[string]any{}
	errors, err = commandutil.ValidateSchema("annex", "", badManifest)
	if err != nil {
		t.Fatalf("unexpected schema error: %v", err)
	}
	if !containsError(errors, "name") {
		t.Fatalf("expected schema error mentioning name, got: %v", errors)
	}
}

func containsError(errors []string, want string) bool {
	for _, err := range errors {
		if strings.Contains(err, want) {
			return true
		}
	}
	return false
}

func equalAnySlice(got any, expect []any) bool {
	actual, ok := got.([]any)
	if !ok {
		return false
	}
	if len(actual) != len(expect) {
		return false
	}
	for i := range actual {
		if actual[i] != expect[i] {
			return false
		}
	}
	return true
}
