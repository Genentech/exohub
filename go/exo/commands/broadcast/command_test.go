package broadcast

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestManifestFieldsAndParse(t *testing.T) {
	tempDir := t.TempDir()
	manifestPath := filepath.Join(tempDir, "manifest.yaml")
	data := "repo-dir: " + tempDir + "\nwith: [one]\nwith_remotes: [two]\nwith-remotes: [three]\n"
	if err := os.WriteFile(manifestPath, []byte(data), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	parsed, err := parseManifest(manifestPath)
	if err != nil {
		t.Fatalf("parseManifest: %v", err)
	}
	if got := parsed.repoDir(); got != tempDir {
		t.Fatalf("repoDir: got %q", got)
	}
	remotes := parsed.remotes()
	if len(remotes) != 3 || remotes[0] != "one" || remotes[1] != "two" || remotes[2] != "three" {
		t.Fatalf("remotes: got %#v", remotes)
	}
}

func TestStringListUnmarshalScalarAndSequence(t *testing.T) {
	var parsed manifest
	data := "with: single\nwith_remotes:\n  - a\n  - b\n"
	if err := yaml.Unmarshal([]byte(data), &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	remotes := parsed.remotes()
	if len(remotes) != 3 || remotes[0] != "single" || remotes[1] != "a" || remotes[2] != "b" {
		t.Fatalf("unexpected remotes: %#v", remotes)
	}
}

func TestParseManifestRejectsMissingRepoDir(t *testing.T) {
	tempDir := t.TempDir()
	manifestPath := filepath.Join(tempDir, "manifest.yaml")
	data := "repo_dir: /not/a/real/path\n"
	if err := os.WriteFile(manifestPath, []byte(data), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if _, err := parseManifest(manifestPath); err == nil {
		t.Fatalf("expected error for missing repo dir")
	}
}

func TestEnsureAnnexInitRunsInit(t *testing.T) {
	var calls []string
	withCommand(t, func(name string, args ...string) *exec.Cmd {
		calls = append(calls, name+" "+strings.Join(args, " "))
		if name == "git" && len(args) >= 2 && args[0] == "config" && args[1] == "--get" {
			return exec.Command("sh", "-c", "exit 1")
		}
		return exec.Command("sh", "-c", "exit 0")
	})

	if err := ensureAnnexInit(&ui{enabled: false}); err != nil {
		t.Fatalf("ensureAnnexInit: %v", err)
	}
	found := false
	for _, call := range calls {
		if strings.Contains(call, "annex init") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected annex init call, got %#v", calls)
	}
}

func withCommand(t *testing.T, fn func(name string, args ...string) *exec.Cmd) {
	t.Helper()
	old := command
	command = fn
	t.Cleanup(func() { command = old })
}
