package export

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestStringListUnmarshal(t *testing.T) {
	var parsed manifest
	data := "paths: [one, two]\n"
	if err := yaml.Unmarshal([]byte(data), &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(parsed.Paths) != 2 || parsed.Paths[0] != "one" || parsed.Paths[1] != "two" {
		t.Fatalf("unexpected paths: %#v", parsed.Paths)
	}
}

func TestStringListUnmarshalError(t *testing.T) {
	var parsed manifest
	data := "paths:\n  - one\n  - {bad: 1}\n"
	if err := yaml.Unmarshal([]byte(data), &parsed); err == nil {
		t.Fatalf("expected error for non-scalar list item")
	}
}

func TestStringValueUnmarshal(t *testing.T) {
	var parsed manifest
	data := "from: '  remote '\n"
	if err := yaml.Unmarshal([]byte(data), &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.From != "remote" {
		t.Fatalf("stringValue: %q", parsed.From)
	}
	if err := yaml.Unmarshal([]byte("from: [a]"), &parsed); err == nil {
		t.Fatalf("expected error for non-scalar from")
	}
}

func TestParseManifest(t *testing.T) {
	tempDir := t.TempDir()
	manifestPath := filepath.Join(tempDir, "manifest.yaml")
	data := "from: here\nto: there\nref: main\njobs: 4\n"
	if err := os.WriteFile(manifestPath, []byte(data), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	parsed, err := parseManifest(manifestPath)
	if err != nil {
		t.Fatalf("parseManifest: %v", err)
	}
	if parsed.From != "here" || parsed.To != "there" || parsed.Ref != "main" || parsed.Jobs != "4" {
		t.Fatalf("parsed manifest: %#v", parsed)
	}
}

func TestParseManifestWithRemotesAndPaths(t *testing.T) {
	tempDir := t.TempDir()
	manifestPath := filepath.Join(tempDir, "manifest.yaml")
	data := "with-remotes: [origin, backup]\npaths: [data, logs]\n"
	if err := os.WriteFile(manifestPath, []byte(data), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	parsed, err := parseManifest(manifestPath)
	if err != nil {
		t.Fatalf("parseManifest: %v", err)
	}
	if len(parsed.WithRemotes) != 2 || parsed.WithRemotes[0] != "origin" || parsed.WithRemotes[1] != "backup" {
		t.Fatalf("with-remotes: %#v", parsed.WithRemotes)
	}
	if len(parsed.Paths) != 2 || parsed.Paths[0] != "data" || parsed.Paths[1] != "logs" {
		t.Fatalf("paths: %#v", parsed.Paths)
	}
}

func TestExpandPaths(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "file.txt")
	if err := os.WriteFile(path, []byte("content"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	pattern := filepath.Join(tempDir, "*.txt")
	expanded := expandPaths([]string{pattern, filepath.Join(tempDir, "missing")})
	if len(expanded) != 2 {
		t.Fatalf("expanded len: %d", len(expanded))
	}
	if expanded[0] != path {
		t.Fatalf("expanded[0]: got %q", expanded[0])
	}
	if expanded[1] != filepath.Join(tempDir, "missing") {
		t.Fatalf("expanded[1]: got %q", expanded[1])
	}
}

func TestParseManifestMissingFile(t *testing.T) {
	if _, err := parseManifest("/not/a/real/path.yaml"); err == nil {
		t.Fatalf("expected error for missing manifest")
	}
}
