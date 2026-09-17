package publish

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	initcmd "github.com/Genentech/exohub/go/exo/commands/init"
)

func TestClassifyRemotes(t *testing.T) {
	tests := []struct {
		name            string
		config          *initcmd.RemotesConfig
		wantContent     []string
		wantCatalog     []string
	}{
		{
			name:   "nil config",
			config: nil,
		},
		{
			name:   "empty config",
			config: &initcmd.RemotesConfig{},
		},
		{
			name: "only content remotes",
			config: &initcmd.RemotesConfig{
				Remotes: []initcmd.RemoteConfig{
					{Name: "s3", Type: "annex"},
					{Name: "tree", Type: "export"},
				},
			},
			wantContent: []string{"s3", "tree"},
		},
		{
			name: "only catalog remotes",
			config: &initcmd.RemotesConfig{
				Remotes: []initcmd.RemoteConfig{
					{Name: "atlas", Type: "artifactdb"},
				},
			},
			wantCatalog: []string{"atlas"},
		},
		{
			name: "mixed remotes",
			config: &initcmd.RemotesConfig{
				Remotes: []initcmd.RemoteConfig{
					{Name: "s3", Type: "annex"},
					{Name: "tree", Type: "export"},
					{Name: "atlas", Type: "artifactdb"},
					{Name: "cache", Type: "exospace"},
					{Name: "importer", Type: "import"},
				},
			},
			wantContent: []string{"s3", "tree", "cache", "importer"},
			wantCatalog: []string{"atlas"},
		},
		{
			name: "custom catalog type",
			config: &initcmd.RemotesConfig{
				Remotes: []initcmd.RemoteConfig{
					{Name: "s3", Type: "annex"},
					{Name: "custom", Type: "mydb"},
				},
			},
			wantContent: []string{"s3"},
			wantCatalog: []string{"custom"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content, catalog := classifyRemotes(tt.config)
			if !sliceEqual(content, tt.wantContent) {
				t.Errorf("content remotes = %v, want %v", content, tt.wantContent)
			}
			if !sliceEqual(catalog, tt.wantCatalog) {
				t.Errorf("catalog remotes = %v, want %v", catalog, tt.wantCatalog)
			}
		})
	}
}

func TestIsStandardRemoteType(t *testing.T) {
	standards := []string{"annex", "export", "import", "exospace"}
	for _, s := range standards {
		if !isStandardRemoteType(s) {
			t.Errorf("expected %q to be standard", s)
		}
	}

	nonStandards := []string{"artifactdb", "mydb", "", "catalog"}
	for _, s := range nonStandards {
		if isStandardRemoteType(s) {
			t.Errorf("expected %q to NOT be standard", s)
		}
	}
}

// setupPublishEnv creates a temporary directory with a .git dir, installs a
// fake "exo" binary on PATH, and overrides the package-level command variable
// to record invocations. It returns a pointer to the recorded call list and a
// cleanup function.
func setupPublishEnv(t *testing.T, exoExitCode int) (calls *[]string, cleanup func()) {
	t.Helper()

	// Create a temp dir that acts as the repo root and cd into it.
	repoDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	// Install a fake "exo" on PATH.
	binDir := t.TempDir()
	exoScript := filepath.Join(binDir, "exo")
	script := "#!/bin/sh\nexit " + itoa(exoExitCode) + "\n"
	if err := os.WriteFile(exoScript, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake exo: %v", err)
	}
	oldPath := os.Getenv("PATH")
	os.Setenv("PATH", binDir+":"+oldPath)

	// Override command to record calls and delegate to a real sh -c exit 0.
	recorded := &[]string{}
	oldCommand := command
	command = func(name string, args ...string) *exec.Cmd {
		*recorded = append(*recorded, name+" "+strings.Join(args, " "))
		return exec.Command("sh", "-c", "exit "+itoa(exoExitCode))
	}

	cleanup = func() {
		command = oldCommand
		os.Setenv("PATH", oldPath)
		os.Chdir(oldDir) //nolint:errcheck
	}
	return recorded, cleanup
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}

func hasBroadcast(calls []string) bool {
	for _, c := range calls {
		if strings.Contains(c, "broadcast") {
			return true
		}
	}
	return false
}

// TestBroadcastRunsWhenCatalogSkipped verifies that broadcast is called even
// when no catalog remotes are configured (step 3 is skipped).
func TestBroadcastRunsWhenCatalogSkipped(t *testing.T) {
	calls, cleanup := setupPublishEnv(t, 0)
	defer cleanup()

	// No remotes configured → catalog step is skipped.
	if err := runPublish(nil); err != nil {
		t.Fatalf("runPublish: %v", err)
	}
	if !hasBroadcast(*calls) {
		t.Errorf("expected broadcast call, got: %v", *calls)
	}
}

// TestBroadcastRunsAfterCatalogSync verifies that broadcast runs after a
// successful catalog sync (all four steps complete).
func TestBroadcastRunsAfterCatalogSync(t *testing.T) {
	calls, cleanup := setupPublishEnv(t, 0)
	defer cleanup()

	// Use --sync to feed a content remote so step 1 runs too.
	if err := runPublish([]string{"s3"}); err != nil {
		t.Fatalf("runPublish: %v", err)
	}
	if !hasBroadcast(*calls) {
		t.Errorf("expected broadcast call, got: %v", *calls)
	}
}

// TestBroadcastNotCalledOnEarlierFailure verifies that broadcast is NOT called
// when an earlier step (bundle) fails.
func TestBroadcastNotCalledOnEarlierFailure(t *testing.T) {
	calls, cleanup := setupPublishEnv(t, 1) // non-zero exit → every exo call fails
	defer cleanup()

	err := runPublish(nil)
	if err == nil {
		t.Fatal("expected runPublish to return an error")
	}
	if hasBroadcast(*calls) {
		t.Errorf("broadcast should not be called after earlier failure, got: %v", *calls)
	}
}

func sliceEqual(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
