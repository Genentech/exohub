package publish

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Genentech/exohub/go/exo/commandutil"
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

	// Simulate a configured git remote so the git-remote precondition passes.
	oldResolveCloneURL := resolveCloneURL
	resolveCloneURL = func() (string, error) { return "https://example.com/repo.git", nil }

	cleanup = func() {
		command = oldCommand
		resolveCloneURL = oldResolveCloneURL
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

// TestNoGitRemoteAborts verifies that runPublish aborts before any step runs
// when the repo has a .git directory but no usable git remote.
func TestNoGitRemoteAborts(t *testing.T) {
	calls, cleanup := setupPublishEnv(t, 0)
	defer cleanup()

	// Override the default (configured-remote) stub installed by
	// setupPublishEnv to simulate no usable git remote.
	resolveCloneURL = func() (string, error) { return "", fmt.Errorf("no git remote configured") }

	err := runPublish(nil)
	if err == nil {
		t.Fatal("expected runPublish to return an error")
	}
	if !strings.Contains(err.Error(), "git remote") {
		t.Errorf("expected error to mention 'git remote', got: %v", err)
	}
	if len(*calls) != 0 {
		t.Errorf("expected no exo subcommands to run, got: %v", *calls)
	}
}

// TestLocalPathOnlyOriginAbortsBeforeAnyStep is an end-to-end check (using
// the real commandutil.ResolveCloneURL against an actual git repo, not a
// stub) that a repo whose only remote is a local path aborts before any
// step runs — regression test for #100 case 4.
func TestLocalPathOnlyOriginAbortsBeforeAnyStep(t *testing.T) {
	repoDir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = repoDir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+repoDir)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("command %v failed: %v\n%s", args, err, out)
		}
	}
	run("git", "init")
	run("git", "remote", "add", "origin", "/srv/git/repo.git")

	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(oldDir) //nolint:errcheck

	recorded := &[]string{}
	oldCommand := command
	command = func(name string, args ...string) *exec.Cmd {
		*recorded = append(*recorded, name+" "+strings.Join(args, " "))
		return exec.Command("true")
	}
	oldResolveCloneURL := resolveCloneURL
	resolveCloneURL = commandutil.ResolveCloneURL
	defer func() {
		command = oldCommand
		resolveCloneURL = oldResolveCloneURL
	}()

	if err := runPublish(nil); err == nil {
		t.Fatal("expected runPublish to error on a local-path-only origin")
	} else if !strings.Contains(err.Error(), "git remote") {
		t.Errorf("expected error to mention 'git remote', got: %v", err)
	}
	if len(*recorded) != 0 {
		t.Errorf("expected no exo subcommands to run, got: %v", *recorded)
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
