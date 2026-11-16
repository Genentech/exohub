package sync

import (
	"github.com/Genentech/exohub/go/exo/commandutil"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// rrRun runs git in dir with an isolated identity/config, failing the test
// on error.
func rrRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1",
		"HOME="+dir,
		"GIT_AUTHOR_NAME=Test",
		"GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=Test",
		"GIT_COMMITTER_EMAIL=test@test.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("command %v in %s: %v\n%s", args, dir, err, out)
	}
	return string(out)
}

func rrChdir(t *testing.T, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
	t.Cleanup(func() { os.Chdir(old) })
}

// rrFixture creates a bare "origin" and a local git-annex-initialized clone
// with one commit pushed to main.
func rrFixture(t *testing.T) (localDir, originDir string) {
	t.Helper()
	if _, err := exec.LookPath("git-annex"); err != nil {
		t.Skip("git-annex not in PATH")
	}
	originDir = t.TempDir()
	localDir = t.TempDir()

	rrRun(t, originDir, "git", "init", "--bare", "-b", "main")
	rrRun(t, localDir, "git", "clone", originDir, ".")
	rrRun(t, localDir, "git", "checkout", "-b", "main")
	rrRun(t, localDir, "git", "commit", "--allow-empty", "-m", "initial")
	rrRun(t, localDir, "git", "push", "origin", "main")
	rrRun(t, localDir, "git", "annex", "init", "testrepo")
	return localDir, originDir
}

// rrForcePushRewrite force-pushes an unrelated orphan history to origin's
// main from a third, independent clone.
func rrForcePushRewrite(t *testing.T, originDir string) {
	t.Helper()
	otherDir := t.TempDir()
	rrRun(t, otherDir, "git", "init", "-b", "main")
	rrRun(t, otherDir, "git", "commit", "--allow-empty", "-m", "rewritten history")
	rrRun(t, otherDir, "git", "push", "--force", originDir, "main")
}

// captureStderr redirects os.Stderr for the duration of fn and returns
// everything written to it.
func captureStderr(t *testing.T, fn func() int) (int, string) {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		buf := make([]byte, 0, 4096)
		tmp := make([]byte, 4096)
		for {
			n, readErr := r.Read(tmp)
			if n > 0 {
				buf = append(buf, tmp[:n]...)
			}
			if readErr != nil {
				break
			}
		}
		done <- string(buf)
	}()
	code := fn()
	os.Stderr = old
	w.Close()
	return code, <-done
}

func TestSnapshotAndDiagnose_NoFalsePositiveBeforeRewrite(t *testing.T) {
	localDir, _ := rrFixture(t)
	rrChdir(t, localDir)

	before := snapshotRemoteTips([]string{"origin", "", "not-a-remote"})
	if len(before) != 1 {
		t.Fatalf("expected only the plain git remote to be snapshotted, got %v", before)
	}
	if diag := commandutil.DiagnoseRewrittenRemote("origin", "main", before["origin"]); diag != nil {
		t.Fatalf("expected no rewrite diagnosis before any rewrite, got: %v", diag)
	}
	if diag := commandutil.DiagnoseRewrittenRemote("not-a-remote", "main", ""); diag != nil {
		t.Fatalf("expected an unknown/special remote to be skipped, got: %v", diag)
	}
}

func TestRunResetSyncedCommand_RefusesWithStaleLocalBranch(t *testing.T) {
	localDir, originDir := rrFixture(t)
	rrChdir(t, localDir)
	rrForcePushRewrite(t, originDir)

	// Local main still holds the pre-rewrite commit, which is reachable from
	// the remote's pre-rewrite history (captured before this command's own
	// fetch) but not from the rewritten origin/main -- it's stale, not local
	// work, so this must NOT be the generic "unpushed commits" refusal.
	code, stderr := captureStderr(t, func() int { return runResetSyncedCommand("", "origin", true) })
	if code == 0 {
		t.Fatalf("expected a non-zero exit refusing the reset")
	}
	if !strings.Contains(stderr, "adopt the new history") {
		t.Fatalf("expected the adopt-the-new-history hint, got: %q", stderr)
	}
	if strings.Contains(stderr, "has commit(s) not on") {
		t.Fatalf("expected no misleading 'unpushed commits' message, got: %q", stderr)
	}

	if _, err := os.Stat(localDir + "/.git/refs/heads/synced"); err == nil {
		t.Fatalf("expected synced/main to not have been created by a refused reset")
	}
}

// TestRunResetSyncedCommand_GenuineLocalCommitStillBlocks reproduces the
// exo-cli#178 MR !325 review (blocker 2): once a clone has a genuinely
// local commit on top of the stale pre-rewrite history, that commit alone
// must still block the reset -- the fix must not let genuine local work
// through just because some of the "ahead" commits turned out to be
// rewritten-upstream history.
func TestRunResetSyncedCommand_GenuineLocalCommitStillBlocks(t *testing.T) {
	localDir, originDir := rrFixture(t)
	rrChdir(t, localDir)
	rrForcePushRewrite(t, originDir)
	rrRun(t, localDir, "git", "commit", "--allow-empty", "-m", "genuinely local")

	code, stderr := captureStderr(t, func() int { return runResetSyncedCommand("", "origin", true) })
	if code == 0 {
		t.Fatalf("expected a non-zero exit refusing the reset")
	}
	if !strings.Contains(stderr, "genuinely local") {
		t.Fatalf("expected the genuinely-local commit to be named in the refusal, got: %q", stderr)
	}
	if _, err := os.Stat(localDir + "/.git/refs/heads/synced"); err == nil {
		t.Fatalf("expected synced/main to not have been created by a refused reset")
	}
}

func TestRunResetSyncedCommand_RequiresYesThenSucceeds(t *testing.T) {
	localDir, originDir := rrFixture(t)
	rrChdir(t, localDir)
	rrForcePushRewrite(t, originDir)

	// Simulate the operator confirming local work is safe and aligning
	// local main with the rewritten remote before resetting synced/*.
	rrRun(t, localDir, "git", "fetch", "origin")
	rrRun(t, localDir, "git", "reset", "--hard", "origin/main")

	if code := runResetSyncedCommand("", "origin", false); code == 0 {
		t.Fatalf("expected --reset-synced without --yes to only preview, not succeed")
	}
	if _, err := os.Stat(localDir + "/.git/refs/heads/synced"); err == nil {
		t.Fatalf("expected no synced/main to exist yet (preview must not mutate refs)")
	}

	if code := runResetSyncedCommand("", "origin", true); code != 0 {
		t.Fatalf("expected --reset-synced --yes to succeed once local main is safe, got exit %d", code)
	}
	if _, err := os.Stat(localDir + "/.git/refs/heads/synced/main"); err != nil {
		t.Fatalf("expected synced/main to be created by the reset: %v", err)
	}
}
