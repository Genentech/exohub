package commandutil

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// gitRefsTestRepo wires up a local "origin" bare remote plus a clone, both
// with isolated git config/identity, and returns run helpers for each.
func gitRefsTestRepo(t *testing.T) (cloneDir string, runClone func(args ...string) string, runOrigin func(args ...string) string) {
	t.Helper()
	originDir := t.TempDir()
	cloneDir = t.TempDir()

	runIn := func(dir string) func(args ...string) string {
		return func(args ...string) string {
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
	}

	runOrigin = runIn(originDir)
	runOrigin("git", "init", "--bare", "-b", "main")

	runClone = runIn(cloneDir)
	runClone("git", "clone", originDir, ".")
	runClone("git", "checkout", "-b", "main")
	runClone("git", "commit", "--allow-empty", "-m", "initial")
	runClone("git", "push", "origin", "main")

	return cloneDir, runClone, runOrigin
}

func TestDetectRewrittenRemote_NoRewrite(t *testing.T) {
	cloneDir, runClone, _ := gitRefsTestRepo(t)
	chdir(t, cloneDir)

	before, err := RemoteBranchRef("origin", "main")
	if err != nil {
		t.Fatalf("RemoteBranchRef: %v", err)
	}

	runClone("git", "commit", "--allow-empty", "-m", "second")
	runClone("git", "push", "origin", "main")

	if err := FetchRemote("origin"); err != nil {
		t.Fatalf("FetchRemote: %v", err)
	}
	rewritten, err := DetectRewrittenRemote("origin", "main", before)
	if err != nil {
		t.Fatalf("DetectRewrittenRemote: %v", err)
	}
	if rewritten {
		t.Fatalf("expected a fast-forward push to not be detected as rewritten")
	}
}

func TestDetectRewrittenRemote_FirstFetchIsNotRewritten(t *testing.T) {
	cloneDir, _, _ := gitRefsTestRepo(t)
	chdir(t, cloneDir)

	// No prior remote-tracking tip and no local synced/main: first-ever
	// comparison must not be flagged as a rewrite.
	rewritten, err := DetectRewrittenRemote("origin", "main", "")
	if err != nil {
		t.Fatalf("DetectRewrittenRemote: %v", err)
	}
	if rewritten {
		t.Fatalf("expected first fetch to not be detected as rewritten")
	}
}

func TestDetectRewrittenRemote_ForcePush(t *testing.T) {
	cloneDir, runClone, runOrigin := gitRefsTestRepo(t)
	chdir(t, cloneDir)

	before, err := RemoteBranchRef("origin", "main")
	if err != nil {
		t.Fatalf("RemoteBranchRef: %v", err)
	}

	// Simulate git-annex having created a local synced/main tracking what
	// was pushed.
	runClone("git", "branch", "synced/main", "main")

	// Force-push an unrelated history directly on the "remote" (origin).
	otherDir := t.TempDir()
	runOther := func(args ...string) string {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = otherDir
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_NOSYSTEM=1", "HOME="+otherDir,
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("command %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	runOther("git", "init", "-b", "main")
	runOther("git", "commit", "--allow-empty", "-m", "rewritten history")
	originURL := strings.TrimSpace(runClone("git", "remote", "get-url", "origin"))
	runOther("git", "push", "--force", originURL, "main")
	_ = runOrigin // origin is bare; pushed to directly above via its path

	if err := FetchRemote("origin"); err != nil {
		t.Fatalf("FetchRemote: %v", err)
	}
	rewritten, err := DetectRewrittenRemote("origin", "main", before)
	if err != nil {
		t.Fatalf("DetectRewrittenRemote: %v", err)
	}
	if !rewritten {
		t.Fatalf("expected force-pushed unrelated history to be detected as rewritten")
	}
}

func TestListUnpushedCommits(t *testing.T) {
	cloneDir, runClone, _ := gitRefsTestRepo(t)
	chdir(t, cloneDir)

	if err := FetchRemote("origin"); err != nil {
		t.Fatalf("FetchRemote: %v", err)
	}
	report, err := ListUnpushedCommits("origin", "main")
	if err != nil {
		t.Fatalf("ListUnpushedCommits: %v", err)
	}
	if report.HasUnpushed() || report.HasOnlyRewrittenUpstream() {
		t.Fatalf("expected no unpushed commits, got %+v", report)
	}

	runClone("git", "commit", "--allow-empty", "-m", "local only")
	report, err = ListUnpushedCommits("origin", "main")
	if err != nil {
		t.Fatalf("ListUnpushedCommits: %v", err)
	}
	if len(report.Unpushed) != 1 || !strings.Contains(report.Unpushed[0], "local only") {
		t.Fatalf("expected one unpushed commit mentioning 'local only', got %+v", report)
	}
	if len(report.RewrittenUpstream) != 0 {
		t.Fatalf("expected no rewritten-upstream commits, got %v", report.RewrittenUpstream)
	}
}

// TestListUnpushedCommits_ExcludesRewrittenUpstream reproduces the exo-cli#178
// MR !325 review (blocker 2): a clone that only ever saw the OLD history
// must not have that history reported as "unpushed" forever after another
// clone rewrites the remote's branch -- only genuinely local work should be.
func TestListUnpushedCommits_ExcludesRewrittenUpstream(t *testing.T) {
	cloneDir, runClone, _ := gitRefsTestRepo(t)
	chdir(t, cloneDir)

	// This clone fetched (via clone) the OLD history; capture it the same
	// way a caller would before re-fetching.
	before, err := RemoteBranchRef("origin", "main")
	if err != nil {
		t.Fatalf("RemoteBranchRef: %v", err)
	}

	// Another clone rewrites main (e.g. amends the tip commit) and force-pushes.
	rewriteRemoteMain(t, runClone, "origin")

	if err := FetchRemote("origin"); err != nil {
		t.Fatalf("FetchRemote: %v", err)
	}

	// Local main still holds the OLD, now-rewritten-away commit: it must be
	// classified as RewrittenUpstream, not Unpushed.
	report, err := ListUnpushedCommits("origin", "main", before)
	if err != nil {
		t.Fatalf("ListUnpushedCommits: %v", err)
	}
	if report.HasUnpushed() {
		t.Fatalf("expected no genuinely unpushed commits, got %v", report.Unpushed)
	}
	if !report.HasOnlyRewrittenUpstream() {
		t.Fatalf("expected the old commit to be classified as rewritten-upstream, got %+v", report)
	}

	// Add a genuinely local commit on top: it alone must be Unpushed, and the
	// old commit must still be excluded from it.
	runClone("git", "commit", "--allow-empty", "-m", "genuinely local")
	report, err = ListUnpushedCommits("origin", "main", before)
	if err != nil {
		t.Fatalf("ListUnpushedCommits: %v", err)
	}
	if len(report.Unpushed) != 1 || !strings.Contains(report.Unpushed[0], "genuinely local") {
		t.Fatalf("expected exactly one genuinely-local unpushed commit, got %+v", report)
	}
	if len(report.RewrittenUpstream) == 0 {
		t.Fatalf("expected the old commit to still be classified as rewritten-upstream, got %+v", report)
	}
}

// rewriteRemoteMain amends origin's current main tip (read via cloneRun, a
// run helper already pointed at a clone of origin) into a new commit and
// force-pushes it from a throwaway clone, simulating a catalog-service-style
// history rewrite that touches main only (not synced/main).
func rewriteRemoteMain(t *testing.T, cloneRun func(args ...string) string, remote string) {
	t.Helper()
	otherDir := t.TempDir()
	runOther := func(args ...string) string {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = otherDir
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_NOSYSTEM=1", "HOME="+otherDir,
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("command %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	originURL := strings.TrimSpace(cloneRun("git", "remote", "get-url", remote))
	runOther("git", "clone", originURL, ".")
	runOther("git", "commit", "--amend", "--allow-empty", "-m", "rewritten main")
	runOther("git", "push", "--force", "origin", "HEAD:main")
}

func TestResetSyncedRefs_RefusesWithUnpushedCommits(t *testing.T) {
	cloneDir, runClone, _ := gitRefsTestRepo(t)
	chdir(t, cloneDir)

	runClone("git", "branch", "synced/main", "main")
	runClone("git", "commit", "--allow-empty", "-m", "local only")

	if err := FetchRemote("origin"); err != nil {
		t.Fatalf("FetchRemote: %v", err)
	}
	err := ResetSyncedRefs("origin", "main")
	if err == nil {
		t.Fatalf("expected ResetSyncedRefs to refuse with an unpushed commit")
	}
	var unpushedErr *UnpushedCommitsError
	if !asUnpushedCommitsError(err, &unpushedErr) {
		t.Fatalf("expected *UnpushedCommitsError, got %T: %v", err, err)
	}
	if len(unpushedErr.Commits) != 1 {
		t.Fatalf("expected one listed commit, got %v", unpushedErr.Commits)
	}

	// synced/main must be untouched.
	tip, err := revParse("refs/heads/synced/main")
	if err != nil {
		t.Fatalf("revParse: %v", err)
	}
	originalTip, err := RemoteBranchRef("origin", "main")
	if err != nil {
		t.Fatalf("RemoteBranchRef: %v", err)
	}
	if tip != originalTip {
		t.Fatalf("expected synced/main to be untouched, got %s want %s", tip, originalTip)
	}
}

func TestResetSyncedRefs_ResetsToRemoteSyncedWhenSafe(t *testing.T) {
	cloneDir, runClone, runOrigin := gitRefsTestRepo(t)
	chdir(t, cloneDir)

	// Simulate a prior git-annex sync: a synced/main branch exists on both
	// sides, pointing at the old history.
	runClone("git", "branch", "synced/main", "main")
	runClone("git", "push", "origin", "synced/main")

	// Remote main (and its synced/main) get rewritten by a force-push from
	// elsewhere; local main is untouched (no unpushed commits relative to
	// the OLD remote main it already matches... but after a force push the
	// comparison base changes, so recreate the scenario explicitly).
	otherDir := t.TempDir()
	runOther := func(args ...string) string {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = otherDir
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_NOSYSTEM=1", "HOME="+otherDir,
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("command %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	originURL := strings.TrimSpace(runClone("git", "remote", "get-url", "origin"))
	runOther("git", "clone", originURL, ".")
	runOther("git", "checkout", "--orphan", "rewritten")
	runOther("git", "commit", "--allow-empty", "-m", "rewritten main")
	runOther("git", "branch", "-M", "rewritten", "main")
	runOther("git", "push", "--force", "origin", "main")
	runOther("git", "checkout", "-b", "synced/main")
	runOther("git", "push", "--force", "origin", "synced/main")
	_ = runOrigin

	if err := FetchRemote("origin"); err != nil {
		t.Fatalf("FetchRemote: %v", err)
	}

	// Realistic safe-reset precondition: the user has already confirmed their
	// local work is captured in the new history and aligned local main with
	// it (standard git practice after a rewrite), so no commits would be
	// orphaned by resetting synced/main.
	runClone("git", "reset", "--hard", "origin/main")

	localMainBefore, err := revParse("refs/heads/main")
	if err != nil {
		t.Fatalf("revParse main (before): %v", err)
	}

	if err := ResetSyncedRefs("origin", "main"); err != nil {
		t.Fatalf("ResetSyncedRefs: %v", err)
	}

	gotTip, err := revParse("refs/heads/synced/main")
	if err != nil {
		t.Fatalf("revParse: %v", err)
	}
	wantTip, err := RemoteBranchRef("origin", "synced/main")
	if err != nil {
		t.Fatalf("RemoteBranchRef: %v", err)
	}
	if gotTip == "" || gotTip != wantTip {
		t.Fatalf("expected local synced/main (%s) to match origin's synced/main (%s)", gotTip, wantTip)
	}

	// Local main must never be touched by a reset.
	localMainAfter, err := revParse("refs/heads/main")
	if err != nil {
		t.Fatalf("revParse main (after): %v", err)
	}
	if localMainAfter != localMainBefore {
		t.Fatalf("expected local main to be untouched by ResetSyncedRefs, got %s want %s", localMainAfter, localMainBefore)
	}
}

// TestResetSyncedRefs_PrefersRewrittenBranchOverStaleSyncedRemote reproduces
// the exo-cli#178 MR !325 review (blocker 1): a remote rewrite of <branch>
// alone (the catalog-service situation) leaves the remote's own synced/<branch>
// pointing at the OLD history. Resetting to it instead of the rewritten
// <branch> would re-trigger the rewrite guard forever.
func TestResetSyncedRefs_PrefersRewrittenBranchOverStaleSyncedRemote(t *testing.T) {
	cloneDir, runClone, _ := gitRefsTestRepo(t)
	chdir(t, cloneDir)

	// Simulate a prior git-annex sync: synced/main exists on both sides,
	// pointing at the OLD history.
	runClone("git", "branch", "synced/main", "main")
	runClone("git", "push", "origin", "synced/main")

	// Another clone rewrites (force-pushes) ONLY main; origin's synced/main
	// is untouched and still holds the OLD history.
	rewriteRemoteMain(t, runClone, "origin")

	// The operator (the force-pusher) aligns local main with the rewrite --
	// it's their own rewrite, so no unpushed work.
	if err := FetchRemote("origin"); err != nil {
		t.Fatalf("FetchRemote: %v", err)
	}
	runClone("git", "reset", "--hard", "origin/main")

	staleSyncedTipBefore, err := RemoteBranchRef("origin", "synced/main")
	if err != nil {
		t.Fatalf("RemoteBranchRef synced/main (before): %v", err)
	}

	if err := ResetSyncedRefs("origin", "main"); err != nil {
		t.Fatalf("ResetSyncedRefs: %v", err)
	}

	gotTip, err := revParse("refs/heads/synced/main")
	if err != nil {
		t.Fatalf("revParse: %v", err)
	}
	wantTip, err := RemoteBranchRef("origin", "main")
	if err != nil {
		t.Fatalf("RemoteBranchRef: %v", err)
	}

	if gotTip != wantTip {
		t.Fatalf("expected local synced/main (%s) to match the rewritten origin/main (%s)", gotTip, wantTip)
	}
	if gotTip == staleSyncedTipBefore {
		t.Fatalf("expected local synced/main to NOT be reset to origin's stale synced/main (%s)", staleSyncedTipBefore)
	}

	// And the ancestry that DetectRewrittenRemote relies on must now hold,
	// so a subsequent broadcast/sync does not loop forever.
	ok, err := isAncestor(gotTip, wantTip)
	if err != nil {
		t.Fatalf("isAncestor: %v", err)
	}
	if !ok {
		t.Fatalf("expected local synced/main to be an ancestor of (or equal to) the current origin/main")
	}

	// Blocker 1's full fix: the REMOTE's own synced/main must also have been
	// force-updated past its stale, pre-rewrite tip -- otherwise the very
	// next git-annex sync would pull that stale history back in and hit the
	// exact same conflict the rewrite guard exists to avoid.
	remoteSyncedTipAfter, err := RemoteBranchRef("origin", "synced/main")
	if err != nil {
		t.Fatalf("RemoteBranchRef synced/main (after): %v", err)
	}
	if remoteSyncedTipAfter != wantTip {
		t.Fatalf("expected origin's synced/main (%s) to be force-updated to match origin/main (%s), still stale at %s",
			remoteSyncedTipAfter, wantTip, staleSyncedTipBefore)
	}

	// Same check, but asking the remote directly via `git ls-remote`
	// (rather than this clone's own remote-tracking ref cache), per the
	// maître's addendum review.
	lsRemoteOut := runClone("git", "ls-remote", "origin", "refs/heads/synced/main")
	if !strings.HasPrefix(lsRemoteOut, wantTip) {
		t.Fatalf("expected `git ls-remote origin refs/heads/synced/main` to report %s, got: %q", wantTip, lsRemoteOut)
	}
}

// TestResetSyncedRefs_RefusesWithStaleLocalBranch reproduces the exo-cli#178
// MR !325 review (blocker 2): a clone that only ever saw the OLD, now
// rewritten-away history must get a plain "adopt the new history" refusal,
// not a misleading "you have unpushed commits" one -- and once it has a
// genuinely local commit on top, that commit must still block the reset.
func TestResetSyncedRefs_RefusesWithStaleLocalBranch(t *testing.T) {
	cloneDir, runClone, _ := gitRefsTestRepo(t)
	chdir(t, cloneDir)

	before, err := RemoteBranchRef("origin", "main")
	if err != nil {
		t.Fatalf("RemoteBranchRef: %v", err)
	}
	rewriteRemoteMain(t, runClone, "origin")
	if err := FetchRemote("origin"); err != nil {
		t.Fatalf("FetchRemote: %v", err)
	}

	err = ResetSyncedRefs("origin", "main", before)
	if err == nil {
		t.Fatalf("expected ResetSyncedRefs to refuse while local main is stale")
	}
	var staleErr *StaleLocalBranchError
	if !asStaleLocalBranchError(err, &staleErr) {
		t.Fatalf("expected *StaleLocalBranchError, got %T: %v", err, err)
	}
	if !strings.Contains(staleErr.Error(), "adopt the new history") {
		t.Fatalf("expected an adopt-the-new-history hint, got: %v", staleErr)
	}

	// synced/main must be untouched.
	if _, statErr := os.Stat(cloneDir + "/.git/refs/heads/synced"); statErr == nil {
		t.Fatalf("expected synced/main to not have been created by a refused reset")
	}

	// A genuinely local commit on top must still block, listed by itself.
	runClone("git", "commit", "--allow-empty", "-m", "genuinely local")
	err = ResetSyncedRefs("origin", "main", before)
	var unpushedErr *UnpushedCommitsError
	if !asUnpushedCommitsError(err, &unpushedErr) {
		t.Fatalf("expected *UnpushedCommitsError once local work exists, got %T: %v", err, err)
	}
	if len(unpushedErr.Commits) != 1 || !strings.Contains(unpushedErr.Commits[0], "genuinely local") {
		t.Fatalf("expected exactly the genuinely-local commit listed, got %v", unpushedErr.Commits)
	}
}

// asUnpushedCommitsError is a small helper so the test file doesn't need the
// errors package just for a single type assertion.
func asUnpushedCommitsError(err error, target **UnpushedCommitsError) bool {
	if e, ok := err.(*UnpushedCommitsError); ok {
		*target = e
		return true
	}
	return false
}

// asStaleLocalBranchError is a small helper so the test file doesn't need the
// errors package just for a single type assertion.
func asStaleLocalBranchError(err error, target **StaleLocalBranchError) bool {
	if e, ok := err.(*StaleLocalBranchError); ok {
		*target = e
		return true
	}
	return false
}

// Regression (urgent fix): git-annex's local synced/main follows LOCAL work,
// so with unpushed commits it is ahead of origin/main. That must never be
// read as the remote having rewritten main.
func TestDetectRewrittenRemote_LocalSyncedAheadIsNotRewrite(t *testing.T) {
	cloneDir, runClone, _ := gitRefsTestRepo(t)
	chdir(t, cloneDir)

	before, err := RemoteBranchRef("origin", "main")
	if err != nil {
		t.Fatalf("RemoteBranchRef: %v", err)
	}
	for _, m := range []string{"c1", "c2", "c3"} {
		runClone("git", "commit", "--allow-empty", "-m", m)
	}
	runClone("git", "branch", "-f", "synced/main", "main")

	if err := FetchRemote("origin"); err != nil {
		t.Fatalf("FetchRemote: %v", err)
	}
	rewritten, err := DetectRewrittenRemote("origin", "main", before)
	if err != nil {
		t.Fatalf("DetectRewrittenRemote: %v", err)
	}
	if rewritten {
		t.Fatalf("expected local synced/main ahead of origin/main (unpushed work) to NOT be a rewrite")
	}
}

// Regression: commits that only reached the remote's synced/main (pushed by
// this clone's own git-annex sync) are genuine local work, not history the
// remote rewrote away.
func TestListUnpushedCommits_RemoteSyncedOnlyCommitIsLocalWork(t *testing.T) {
	cloneDir, runClone, _ := gitRefsTestRepo(t)
	chdir(t, cloneDir)

	runClone("git", "commit", "--allow-empty", "-m", "local-only-on-synced")
	runClone("git", "push", "origin", "main:refs/heads/synced/main")
	if err := FetchRemote("origin"); err != nil {
		t.Fatalf("FetchRemote: %v", err)
	}
	report, err := ListUnpushedCommits("origin", "main")
	if err != nil {
		t.Fatalf("ListUnpushedCommits: %v", err)
	}
	if !report.HasUnpushed() {
		t.Fatalf("expected the commit present only on origin/synced/main to count as unpushed local work, got %+v", report)
	}
	if len(report.RewrittenUpstream) != 0 {
		t.Fatalf("expected no 'rewritten upstream' classification, got %v", report.RewrittenUpstream)
	}
}
