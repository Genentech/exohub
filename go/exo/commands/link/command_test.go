package link

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateMode(t *testing.T) {
	valid := []string{"auto", "hardlink", "symlink", "copy"}
	for _, mode := range valid {
		if err := validateMode(mode); err != nil {
			t.Fatalf("validateMode(%q): %v", mode, err)
		}
	}
	if err := validateMode("nope"); err == nil {
		t.Fatalf("expected error for invalid mode")
	}
}

func TestCacheHelpers(t *testing.T) {
	if got := cacheSuffix(""); got != "" {
		t.Fatalf("cacheSuffix: %q", got)
	}
	if got := cacheSuffix("cache.txt"); got == "" {
		t.Fatalf("cacheSuffix expected non-empty")
	}
	if got := storeAbsOrUnset(""); got != "<unset>" {
		t.Fatalf("storeAbsOrUnset: %q", got)
	}
}

func TestBuildSearchPaths(t *testing.T) {
	repo := t.TempDir()
	data := filepath.Join(repo, "data")
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	opts := options{paths: []string{"data"}}
	paths := buildSearchPaths(opts, repo)
	if len(paths) != 1 {
		t.Fatalf("buildSearchPaths len: %d", len(paths))
	}
	if paths[0] != data {
		t.Fatalf("buildSearchPaths path: %q", paths[0])
	}

	opts = options{}
	paths = buildSearchPaths(opts, repo)
	if len(paths) != 1 || paths[0] != repo {
		t.Fatalf("buildSearchPaths default: %#v", paths)
	}
}
