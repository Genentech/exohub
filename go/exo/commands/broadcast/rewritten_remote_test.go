package broadcast

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/Genentech/exohub/go/exo/commandutil"
)

// broadcastTestEnv runs git in dir with an isolated identity/config, failing
// the test on error.
func broadcastTestRun(t *testing.T, dir string, args ...string) string {
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

func chdirBroadcast(t *testing.T, dir string) {
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

// setupRewrittenRemoteFixture creates a bare "origin" and a local clone with
// one commit pushed to main, then force-pushes an unrelated orphan history
// to origin's main from a third, independent clone -- simulating the
// data-repository rewrite in exo-cli#178. The returned localDir has a stale
// refs/remotes/origin/main.
func setupRewrittenRemoteFixture(t *testing.T) (localDir string) {
	t.Helper()
	if _, err := exec.LookPath("git-annex"); err != nil {
		t.Skip("git-annex not in PATH")
	}

	originDir := t.TempDir()
	localDir = t.TempDir()
	otherDir := t.TempDir()

	broadcastTestRun(t, originDir, "git", "init", "--bare", "-b", "main")

	broadcastTestRun(t, localDir, "git", "clone", originDir, ".")
	broadcastTestRun(t, localDir, "git", "checkout", "-b", "main")
	broadcastTestRun(t, localDir, "git", "commit", "--allow-empty", "-m", "initial")
	broadcastTestRun(t, localDir, "git", "push", "origin", "main")
	broadcastTestRun(t, localDir, "git", "annex", "init", "testrepo")

	broadcastTestRun(t, otherDir, "git", "init", "-b", "main")
	broadcastTestRun(t, otherDir, "git", "commit", "--allow-empty", "-m", "rewritten history")
	broadcastTestRun(t, otherDir, "git", "push", "--force", originDir, "main")

	return localDir
}

func TestBroadcastFailsWithHintOnRewrittenRemoteMain_AllRemotes(t *testing.T) {
	localDir := setupRewrittenRemoteFixture(t)
	chdirBroadcast(t, localDir)

	cmd := NewCommand()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs(nil)

	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected broadcast to fail on a rewritten remote main")
	}
	if !strings.Contains(err.Error(), "rewrote main") || !strings.Contains(err.Error(), "--reset-synced origin") {
		t.Fatalf("expected a clear rewritten-remote hint, got: %v", err)
	}

	if _, statErr := os.Stat(localDir + "/.git/MERGE_HEAD"); statErr == nil {
		t.Fatalf("expected no merge left in progress after the diagnosed failure")
	}
}

func TestBroadcastFailsWithHintOnRewrittenRemoteMain_WithFlag(t *testing.T) {
	localDir := setupRewrittenRemoteFixture(t)
	chdirBroadcast(t, localDir)

	cmd := NewCommand()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"--with", "origin"})

	// In this fixture `origin` is a plain git remote, so `git annex
	// enableremote origin` fails before any sync (pre-existing behaviour,
	// previously masked by the up-front rewrite check). The rewrite hint is
	// now only added after a FAILED sync; here we only require a failure and
	// no merge left in progress.
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected broadcast --with origin to fail on a rewritten remote main")
	}
	if _, statErr := os.Stat(localDir + "/.git/MERGE_HEAD"); statErr == nil {
		t.Fatalf("expected no merge left in progress")
	}
}

func TestBroadcastSucceedsWithoutRewrite(t *testing.T) {
	if _, err := exec.LookPath("git-annex"); err != nil {
		t.Skip("git-annex not in PATH")
	}

	originDir := t.TempDir()
	localDir := t.TempDir()

	broadcastTestRun(t, originDir, "git", "init", "--bare", "-b", "main")
	broadcastTestRun(t, localDir, "git", "clone", originDir, ".")
	broadcastTestRun(t, localDir, "git", "checkout", "-b", "main")
	broadcastTestRun(t, localDir, "git", "commit", "--allow-empty", "-m", "initial")
	broadcastTestRun(t, localDir, "git", "push", "origin", "main")
	broadcastTestRun(t, localDir, "git", "annex", "init", "testrepo")

	chdirBroadcast(t, localDir)

	cmd := NewCommand()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs(nil)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("expected broadcast to succeed without a rewrite, got: %v", err)
	}
}

// TestBroadcastSucceedsAfterResetSynced reproduces the exo-cli#178 MR !325
// review (blocker 1) end to end, with real conflicting file content: a bare
// origin; c1 commits + pushes "one", "two", then `git annex sync
// --no-content` (so origin's own synced/main is established, pointing at
// the OLD "two"); c1 then rewrites "two" with conflicting content and
// force-pushes ONLY main. `exo broadcast` must refuse; after `exo sync
// --reset-synced origin --yes` (simulated here via the same commandutil
// calls that command makes), `exo broadcast` must succeed cleanly -- no
// merge conflict, and origin's synced/main must no longer be stale.
func TestBroadcastSucceedsAfterResetSynced(t *testing.T) {
	if _, err := exec.LookPath("git-annex"); err != nil {
		t.Skip("git-annex not in PATH")
	}

	originDir := t.TempDir()
	c1Dir := t.TempDir()

	broadcastTestRun(t, originDir, "git", "init", "--bare", "-b", "main")

	broadcastTestRun(t, c1Dir, "git", "clone", originDir, ".")
	broadcastTestRun(t, c1Dir, "git", "checkout", "-b", "main")
	if err := os.WriteFile(c1Dir+"/file.txt", []byte("one\n"), 0o644); err != nil {
		t.Fatalf("write file.txt: %v", err)
	}
	broadcastTestRun(t, c1Dir, "git", "add", "file.txt")
	broadcastTestRun(t, c1Dir, "git", "commit", "-m", "one")
	broadcastTestRun(t, c1Dir, "git", "push", "origin", "main")
	if err := os.WriteFile(c1Dir+"/file.txt", []byte("two-original\n"), 0o644); err != nil {
		t.Fatalf("write file.txt: %v", err)
	}
	broadcastTestRun(t, c1Dir, "git", "add", "file.txt")
	broadcastTestRun(t, c1Dir, "git", "commit", "-m", "two")
	broadcastTestRun(t, c1Dir, "git", "push", "origin", "main")
	broadcastTestRun(t, c1Dir, "git", "annex", "init", "testrepo")
	broadcastTestRun(t, c1Dir, "git", "annex", "sync", "--no-content")

	// c1 rewrites "two" with conflicting content and force-pushes ONLY main;
	// origin's own synced/main (just established above) is untouched and
	// still holds the OLD "two".
	if err := os.WriteFile(c1Dir+"/file.txt", []byte("two-REWRITTEN-conflicting\n"), 0o644); err != nil {
		t.Fatalf("write file.txt: %v", err)
	}
	broadcastTestRun(t, c1Dir, "git", "add", "file.txt")
	broadcastTestRun(t, c1Dir, "git", "commit", "--amend", "-m", "two (rewritten)")
	broadcastTestRun(t, c1Dir, "git", "push", "--force", "origin", "HEAD:main")

	chdirBroadcast(t, c1Dir)

	cmd := NewCommand()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected broadcast to refuse on the rewritten remote main")
	} else if !strings.Contains(err.Error(), "rewrote main") {
		t.Fatalf("expected a clear rewritten-remote hint, got: %v", err)
	}

	// Simulate `exo sync --reset-synced origin --yes`: c1 is its own
	// force-pusher, so local main already matches the new remote main --
	// nothing is genuinely unpushed.
	before, err := commandutil.RemoteBranchRef("origin", "main")
	if err != nil {
		t.Fatalf("RemoteBranchRef: %v", err)
	}
	if err := commandutil.FetchRemote("origin"); err != nil {
		t.Fatalf("FetchRemote: %v", err)
	}
	report, err := commandutil.ListUnpushedCommits("origin", "main", before)
	if err != nil {
		t.Fatalf("ListUnpushedCommits: %v", err)
	}
	if report.HasUnpushed() || report.HasOnlyRewrittenUpstream() {
		t.Fatalf("expected the force-pusher's own main to already be safe to reset from, got %+v", report)
	}
	if err := commandutil.ResetSyncedRefs("origin", "main", before); err != nil {
		t.Fatalf("ResetSyncedRefs: %v", err)
	}

	// Per the maître's addendum review: verify the REMOTE side directly via
	// `git ls-remote`, not just this clone's own remote-tracking ref cache.
	wantRemoteSyncedTip, err := commandutil.RemoteBranchRef("origin", "main")
	if err != nil {
		t.Fatalf("RemoteBranchRef: %v", err)
	}
	lsRemoteOut := broadcastTestRun(t, c1Dir, "git", "ls-remote", "origin", "refs/heads/synced/main")
	if !strings.HasPrefix(lsRemoteOut, wantRemoteSyncedTip) {
		t.Fatalf("expected `git ls-remote origin refs/heads/synced/main` to report %s, got: %q", wantRemoteSyncedTip, lsRemoteOut)
	}

	// The core regression: broadcast must now succeed cleanly, with no merge
	// conflict -- resetting only the LOCAL synced/main is not enough (the
	// remote's own synced/main must also be force-updated, which
	// ResetSyncedRefs just did).
	cmd2 := NewCommand()
	cmd2.SilenceUsage = true
	cmd2.SilenceErrors = true
	cmd2.SetArgs(nil)
	if err := cmd2.Execute(); err != nil {
		out := broadcastTestRun(t, c1Dir, "git", "status", "--short")
		t.Fatalf("expected broadcast to succeed after the reset, got: %v\ngit status:\n%s", err, out)
	}

	// No leftover conflict markers/unmerged state, and the rewritten content won.
	statusOut := broadcastTestRun(t, c1Dir, "git", "status", "--short")
	if strings.Contains(statusOut, "UU") {
		t.Fatalf("expected no unmerged files after broadcast, got status:\n%s", statusOut)
	}
	content, err := os.ReadFile(c1Dir + "/file.txt")
	if err != nil {
		t.Fatalf("read file.txt: %v", err)
	}
	if strings.Contains(string(content), "<<<<<<<") {
		t.Fatalf("expected no conflict markers in file.txt, got:\n%s", content)
	}
}

func TestWithRewriteDiagnosis(t *testing.T) {
	originDir := t.TempDir()
	localDir := t.TempDir()
	otherDir := t.TempDir()

	broadcastTestRun(t, originDir, "git", "init", "--bare", "-b", "main")
	broadcastTestRun(t, localDir, "git", "clone", originDir, ".")
	broadcastTestRun(t, localDir, "git", "checkout", "-b", "main")
	broadcastTestRun(t, localDir, "git", "commit", "--allow-empty", "-m", "initial")
	broadcastTestRun(t, localDir, "git", "push", "origin", "main")
	chdirBroadcast(t, localDir)

	syncErr := errors.New("git annex sync failed")
	before := snapshotRemoteTips([]string{"origin", "not-a-remote"})
	if _, ok := before["not-a-remote"]; ok {
		t.Fatalf("expected special/unknown remote names to be skipped by the snapshot")
	}
	if err := withRewriteDiagnosis(syncErr, before); err != syncErr {
		t.Fatalf("expected the original error unchanged without a rewrite, got: %v", err)
	}

	broadcastTestRun(t, otherDir, "git", "init", "-b", "main")
	broadcastTestRun(t, otherDir, "git", "commit", "--allow-empty", "-m", "rewritten history")
	broadcastTestRun(t, otherDir, "git", "push", "--force", originDir, "main")

	err := withRewriteDiagnosis(syncErr, before)
	if err == nil || !strings.Contains(err.Error(), "git annex sync failed") || !strings.Contains(err.Error(), "rewrote main") {
		t.Fatalf("expected the sync error plus the rewrite hint, got: %v", err)
	}
}

// Regression (urgent fix): a brand-new repo whose local main (and therefore
// git-annex's local synced/main) is ahead of origin/main with unpushed
// commits must broadcast normally, never be reported as a rewritten remote.
func TestBroadcastSucceedsWithUnpushedLocalCommits(t *testing.T) {
	if _, err := exec.LookPath("git-annex"); err != nil {
		t.Skip("git-annex not installed")
	}
	originDir := t.TempDir()
	localDir := t.TempDir()
	broadcastTestRun(t, originDir, "git", "init", "--bare", "-b", "main")
	broadcastTestRun(t, originDir, "git", "annex", "init", "origin")
	broadcastTestRun(t, localDir, "git", "init", "-b", "main")
	broadcastTestRun(t, localDir, "git", "remote", "add", "origin", originDir)
	broadcastTestRun(t, localDir, "git", "annex", "init", "local")
	broadcastTestRun(t, localDir, "git", "commit", "--allow-empty", "-m", "initial")
	broadcastTestRun(t, localDir, "git", "annex", "sync", "--no-content")
	for _, m := range []string{"c1", "c2", "c3"} {
		broadcastTestRun(t, localDir, "git", "commit", "--allow-empty", "-m", m)
	}
	broadcastTestRun(t, localDir, "git", "annex", "sync", "--no-content", "--no-push")
	chdirBroadcast(t, localDir)

	cmd := NewCommand()
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("expected broadcast of a repo with unpushed commits to succeed, got: %v", err)
	}
	main := strings.TrimSpace(broadcastTestRun(t, localDir, "git", "rev-parse", "main"))
	remote := strings.TrimSpace(broadcastTestRun(t, localDir, "git", "rev-parse", "origin/main"))
	if main != remote {
		t.Fatalf("expected origin/main (%s) to equal main (%s) after broadcast", remote, main)
	}
}
