package pull

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseManifest(t *testing.T) {
	tempDir := t.TempDir()
	manifestPath := filepath.Join(tempDir, "manifest.yaml")
	data := "name: repo\nurl: https://example.test/repo.git\nref: main\n"
	if err := os.WriteFile(manifestPath, []byte(data), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	manifest, err := parseManifest(manifestPath)
	if err != nil {
		t.Fatalf("parseManifest: %v", err)
	}
	if manifest.URL != "https://example.test/repo.git" || manifest.Ref != "main" {
		t.Fatalf("manifest: %#v", manifest)
	}
}

func TestParseManifestMissingFile(t *testing.T) {
	if _, err := parseManifest("/nope.yaml"); err == nil {
		t.Fatalf("expected error for missing manifest")
	}
}

func TestParseManifestInvalidYAML(t *testing.T) {
	tempDir := t.TempDir()
	manifestPath := filepath.Join(tempDir, "manifest.yaml")
	if err := os.WriteFile(manifestPath, []byte(":\n"), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if _, err := parseManifest(manifestPath); err == nil {
		t.Fatalf("expected error for invalid yaml")
	}
}

func TestIsDir(t *testing.T) {
	tempDir := t.TempDir()
	if !isDir(tempDir) {
		t.Fatalf("expected isDir true")
	}
	if isDir(filepath.Join(tempDir, "missing")) {
		t.Fatalf("expected isDir false")
	}
}

func TestVerifyRef(t *testing.T) {
	withCommand(t, func(name string, args ...string) *exec.Cmd {
		return exec.Command("sh", "-c", "exit 0")
	})
	if err := verifyRef("https://example.test/repo.git", "main"); err != nil {
		t.Fatalf("verifyRef: %v", err)
	}

	withCommand(t, func(name string, args ...string) *exec.Cmd {
		return exec.Command("sh", "-c", "exit 1")
	})
	if err := verifyRef("https://example.test/repo.git", "main"); err == nil {
		t.Fatalf("expected verifyRef error")
	}
}

func TestCheckoutRefChoosesBranch(t *testing.T) {
	var calls []string
	withCommand(t, func(name string, args ...string) *exec.Cmd {
		calls = append(calls, name+" "+strings.Join(args, " "))
		if name == "git" && len(args) >= 1 && args[0] == "show-ref" {
			return exec.Command("sh", "-c", "exit 0")
		}
		return exec.Command("sh", "-c", "exit 0")
	})
	if err := checkoutRef("feature", "hash"); err != nil {
		t.Fatalf("checkoutRef: %v", err)
	}
	found := false
	for _, call := range calls {
		if strings.Contains(call, "checkout feature") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected checkout feature, got %#v", calls)
	}
}

func TestCheckoutRefUsesHashWhenBranchMissing(t *testing.T) {
	var calls []string
	withCommand(t, func(name string, args ...string) *exec.Cmd {
		calls = append(calls, name+" "+strings.Join(args, " "))
		if name == "git" && len(args) >= 1 && args[0] == "show-ref" {
			return exec.Command("sh", "-c", "exit 1")
		}
		return exec.Command("sh", "-c", "exit 0")
	})
	if err := checkoutRef("feature", "hash"); err != nil {
		t.Fatalf("checkoutRef: %v", err)
	}
	found := false
	for _, call := range calls {
		if strings.Contains(call, "checkout hash") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected checkout hash, got %#v", calls)
	}
}

func withCommand(t *testing.T, fn func(name string, args ...string) *exec.Cmd) {
	t.Helper()
	old := command
	command = fn
	t.Cleanup(func() { command = old })
}
