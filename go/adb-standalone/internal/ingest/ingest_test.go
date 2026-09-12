package ingest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Genentech/exohub/go/adb-standalone/internal/config"
)

func TestCompareSemver(t *testing.T) {
	cases := []struct {
		a, b string
		want int // sign only
	}{
		{"v2.9.2", "v2.9.1", 1},
		{"v2.9.1", "v2.9.2", -1},
		{"v2.9.2", "v2.9.2", 0},
		{"v2.10.0", "v2.9.9", 1},
		{"v1.0.0", "v2.0.0", -1},
		{"v2.2.0", "v2.3.0", -1},
	}
	for _, tc := range cases {
		got := compareSemver(tc.a, tc.b)
		if signum(got) != signum(tc.want) {
			t.Errorf("compareSemver(%q, %q) = %d, want sign %d", tc.a, tc.b, got, signum(tc.want))
		}
	}
}

func signum(n int) int {
	if n < 0 {
		return -1
	}
	if n > 0 {
		return 1
	}
	return 0
}

func TestSchemaToType(t *testing.T) {
	cases := []struct{ schema, want string }{
		{"exohub-bundle/v1.json", "exohub bundle"},
		{"exohub-commit/v1.json", "exohub commit"},
		{"exohub-artifact/v1.json", "exohub artifact"},
		{"unknown/v1.json", "unknown"},
		{"my-custom-schema/v2.json", "my custom schema"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := schemaToType(tc.schema); got != tc.want {
			t.Errorf("schemaToType(%q) = %q, want %q", tc.schema, got, tc.want)
		}
	}
}

func TestSyntheticJobID(t *testing.T) {
	if got := syntheticJobID("my-project", "v1.2.3"); got != "my-project@v1.2.3" {
		t.Errorf("got %q", got)
	}
}

func TestComputeExtra_Bundle(t *testing.T) {
	cfg := minimalConfig()
	doc := map[string]any{
		"$schema":  "exohub-bundle/v1.json",
		"ref_name": "v2.2.0",
		"path":     "bundle.json",
	}
	extra := computeExtra(doc, "my-project", "v2.2.0", "bundle.json", cfg, testPerms(), testTenant(), "2026-01-01T00:00:00Z")

	check := func(key, want string) {
		t.Helper()
		if got, _ := extra[key].(string); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	check("id", "my-project:bundle.json@v2.2.0")
	check("gprn", "gprn:adb:local::artifact:my-project:bundle.json@v2.2.0")
	check("metapath", "bundle.json")
	check("type", "exohub bundle")
	check("version", "v2.2.0")
	if extra["latest"] != false {
		t.Errorf("latest should be false initially, got %v", extra["latest"])
	}
}

func TestComputeExtra_Artifact(t *testing.T) {
	cfg := minimalConfig()
	// Artifact files: doc["path"] is the bare file path (no files/ prefix, no .json).
	// relPath is the metadata file path (with files/ prefix and .json suffix).
	doc := map[string]any{
		"$schema": "exohub-artifact/v1.json",
		"path":    "releases/foo.dmg",
	}
	relPath := "files/releases/foo.dmg.json"
	extra := computeExtra(doc, "my-project", "v1.0.0", relPath, cfg, testPerms(), testTenant(), "2026-01-01T00:00:00Z")

	check := func(key, want string) {
		t.Helper()
		if got, _ := extra[key].(string); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	check("id", "my-project:releases/foo.dmg@v1.0.0")
	check("metapath", "files/releases/foo.dmg.json")
	check("type", "exohub artifact")
}

func TestReaderForRequest_MissingLocation(t *testing.T) {
	req := Request{ProjectID: "x", Version: "v1.0.0", S3Location: ""}
	_, err := ReaderForRequest(req)
	if err == nil {
		t.Error("expected error for empty s3_location")
	}
}

func TestReaderForRequest_LocalDir(t *testing.T) {
	tmp := t.TempDir()
	_ = os.WriteFile(filepath.Join(tmp, "bundle.json"), []byte("{}"), 0o644)
	req := Request{ProjectID: "x", Version: "v1.0.0", S3Location: tmp}
	r, err := ReaderForRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	paths, err := r.ListPaths()
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || paths[0] != "bundle.json" {
		t.Errorf("paths = %v", paths)
	}
}

func TestReaderForRequest_NonexistentDir(t *testing.T) {
	req := Request{ProjectID: "x", Version: "v1.0.0", S3Location: "/nonexistent/path/that/does/not/exist"}
	_, err := ReaderForRequest(req)
	if err == nil {
		t.Error("expected error for nonexistent s3_location")
	}
}

// ── helpers ───────────────────────────────────────────────────────────────

func minimalConfig() *config.Config {
	return &config.Config{
		GPRN: config.GPRNConfig{
			Service:     "adb",
			Environment: "local",
			Placeholder: "artifact",
		},
		Permissions: config.PermissionsConfig{
			DefaultRead:  "public",
			DefaultWrite: "owners",
		},
		Storage: config.StorageConfig{
			S3: config.S3Config{Bucket: "test-bucket"},
		},
		Tenants: []config.TenantConfig{{Path: "/exohub", Alias: "exohub"}},
	}
}

func testPerms() map[string]any {
	return map[string]any{
		"read_access":  "public",
		"write_access": "owners",
		"scope":        "project",
		"owners":       []string{},
	}
}

func testTenant() map[string]any {
	return map[string]any{"path": "/exohub", "alias": "exohub"}
}
