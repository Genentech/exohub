package copy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseBool(t *testing.T) {
	cases := map[string]bool{
		"true":  true,
		"yes":   true,
		"1":     true,
		"false": false,
		"no":    false,
		"0":     false,
		"junk":  false,
	}
	for input, expect := range cases {
		if got := parseBool(input); got != expect {
			t.Fatalf("parseBool(%q): got %v want %v", input, got, expect)
		}
	}
}

func TestParseManifest(t *testing.T) {
	tempDir := t.TempDir()
	manifestPath := filepath.Join(tempDir, "manifest.yaml")
	data := "from: here\nto: there\nref: main\nauto: true\n"
	if err := os.WriteFile(manifestPath, []byte(data), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	parsed, err := parseManifest(manifestPath)
	if err != nil {
		t.Fatalf("parseManifest: %v", err)
	}
	if parsed.From != "here" || parsed.To != "there" || parsed.Ref != "main" {
		t.Fatalf("unexpected manifest: %#v", parsed)
	}
}

func TestParseManifestMissingFile(t *testing.T) {
	if _, err := parseManifest("/not/a/real/path.yaml"); err == nil {
		t.Fatalf("expected error for missing manifest")
	}
}
