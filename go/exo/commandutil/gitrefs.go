package commandutil

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// RewrittenRemoteError is returned when a remote's branch was force-pushed
// (its history rewritten) since the last sync, making the local git-annex
// synced/<branch> branch and/or the previous remote-tracking ref stale. See
// exo-cli#178: merging a rewritten remote branch into a repo that still
// carries the old history produces a spurious conflict instead of a clear
// error.
type RewrittenRemoteError struct {
	Remote string
	Branch string
}

func (e *RewrittenRemoteError) Error() string {
	return fmt.Sprintf(
		"remote %s rewrote %s (force-push) since this clone last synced, which is why the sync failed: its synced/%s refs still hold the old history. Run `exo sync --reset-synced %s` (it previews first; add --yes to apply) after checking your local work is safe",
		e.Remote, e.Branch, e.Branch, e.Remote,
	)
}

// UnpushedCommitsError is returned when resetting synced/* refs for a remote
// would orphan local commits that were never pushed to that remote.
type UnpushedCommitsError struct {
	Remote  string
	Branch  string
	Commits []string
}

func (e *UnpushedCommitsError) Error() string {
	return fmt.Sprintf(
		"refusing to reset synced/%s for remote %s: local %s has commit(s) not on %s/%s:\n  %s",
		e.Branch, e.Remote, e.Branch, e.Remote, e.Branch, strings.Join(e.Commits, "\n  "),
	)
}

// StaleLocalBranchError is returned when local <branch> has no genuinely
// unpushed work but still sits on history remote rewrote away: resetting
// synced/* would itself be safe, but local <branch> should adopt the
// remote's new history first so it doesn't look rewritten again on the next
// sync.
type StaleLocalBranchError struct {
	Remote  string
	Branch  string
	Commits []string
}

func (e *StaleLocalBranchError) Error() string {
	return fmt.Sprintf(
		"local %s still has the history %s rewrote away (%d commit(s), none of them local work); "+
			"to adopt the new history run `git fetch %s && git reset --hard %s/%s`, then re-run `exo sync --reset-synced %s`",
		e.Branch, e.Remote, len(e.Commits), e.Remote, e.Remote, e.Branch, e.Remote,
	)
}

// FetchRemote runs `git fetch <remote>` so its remote-tracking refs reflect
// what the remote currently holds.
func FetchRemote(remote string) error {
	out, err := Command("git", "fetch", remote).CombinedOutput()
	if err != nil {
		return fmt.Errorf("git fetch %s: %w\n%s", remote, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// RemoteBranchRef returns the commit SHA that refs/remotes/<remote>/<branch>
// currently points at, or "" if that ref does not exist (e.g. the remote
// has never been fetched, or has no such branch).
func RemoteBranchRef(remote, branch string) (string, error) {
	return revParse("refs/remotes/" + remote + "/" + branch)
}

// DetectRewrittenRemote reports whether remote's branch was rewritten
// (force-pushed): some tip previously OBSERVED on the remote's branch is no
// longer an ancestor of its current remote-tracking ref. The observed tips
// are beforeFetchTip (the value of refs/remotes/<remote>/<branch> captured
// before the last fetch, "" if unknown) and the previous values of that
// remote-tracking ref in its reflog (covers a `git fetch` the user ran
// themselves).
//
// Local branches — including git-annex's local synced/<branch>, which
// follows LOCAL work and is legitimately ahead of the remote whenever there
// are unpushed commits — are never used as evidence of what the remote once
// had (exo-cli: false "rewrote main" on a brand-new repo with unpushed
// commits). Neither is the remote's synced/<branch>, which this clone's own
// git-annex sync pushes and which can legitimately be ahead of the remote's
// main.
func DetectRewrittenRemote(remote, branch, beforeFetchTip string) (bool, error) {
	remoteRef := "refs/remotes/" + remote + "/" + branch
	afterFetchTip, err := revParse(remoteRef)
	if err != nil {
		return false, fmt.Errorf("resolving %s: %w", remoteRef, err)
	}
	if afterFetchTip == "" {
		return false, nil // remote has no such branch
	}
	for _, priorTip := range observedRemoteTips(remoteRef, beforeFetchTip) {
		ok, err := isAncestor(priorTip, afterFetchTip)
		if err != nil {
			return false, fmt.Errorf("checking ancestry toward %s: %w", remoteRef, err)
		}
		if !ok {
			return true, nil
		}
	}
	return false, nil
}

// observedRemoteTips returns the distinct, non-empty tips previously seen on
// remoteRef: beforeFetchTip plus the two previous reflog values of remoteRef.
func observedRemoteTips(remoteRef, beforeFetchTip string) []string {
	seen := map[string]bool{}
	var tips []string
	add := func(t string) {
		if t != "" && !seen[t] {
			seen[t] = true
			tips = append(tips, t)
		}
	}
	add(beforeFetchTip)
	for _, n := range []string{"1", "2"} {
		if t, err := revParse(remoteRef + "@{" + n + "}"); err == nil {
			add(t)
		}
	}
	return tips
}

// AbortMergeIfInProgress aborts a merge git-annex sync left in progress
// (MERGE_HEAD present), restoring the pre-sync state. It reports whether a
// merge was aborted. Callers only use it once a failed sync has been
// diagnosed as caused by a rewritten remote, so the half-done merge of old
// and new history is never left in the user's working tree.
func AbortMergeIfInProgress() (bool, error) {
	if err := Command("git", "rev-parse", "-q", "--verify", "MERGE_HEAD").Run(); err != nil {
		return false, nil // no merge in progress
	}
	if out, err := Command("git", "merge", "--abort").CombinedOutput(); err != nil {
		return false, fmt.Errorf("git merge --abort: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return true, nil
}

// DiagnoseRewrittenRemote is the post-failure diagnostic used by exo
// broadcast/sync (never a pre-check): after a git-annex sync with remote has
// FAILED, it fetches remote (best effort) and returns a
// *RewrittenRemoteError when the remote's branch was actually rewritten
// since beforeTip was observed, so the caller can add the
// `exo sync --reset-synced` hint to the error. It returns nil when the
// remote is not a plain git remote, or nothing points to a rewrite.
func DiagnoseRewrittenRemote(remote, branch, beforeTip string) error {
	if remote == "" {
		return nil
	}
	if err := Command("git", "config", "--get", "remote."+remote+".url").Run(); err != nil {
		return nil // not a plain git remote (e.g. a special remote)
	}
	_ = FetchRemote(remote) // best effort: git-annex sync usually fetched already
	rewritten, err := DetectRewrittenRemote(remote, branch, beforeTip)
	if err != nil || !rewritten {
		return nil
	}
	return &RewrittenRemoteError{Remote: remote, Branch: branch}
}

// UnpushedCommitsReport classifies commits on local <branch> that aren't on
// remote's current <branch> into genuine local work the remote has never
// seen (Unpushed) versus history the remote rewrote away, which local
// <branch> simply hasn't adopted yet (RewrittenUpstream).
type UnpushedCommitsReport struct {
	Unpushed          []string
	RewrittenUpstream []string
}

// HasUnpushed reports whether any commit is genuine, never-pushed local work.
func (r UnpushedCommitsReport) HasUnpushed() bool { return len(r.Unpushed) > 0 }

// HasOnlyRewrittenUpstream reports whether local <branch> is ahead of the
// remote's current <branch> solely because of history the remote rewrote
// away -- no genuine local work is at risk, but local <branch> itself is
// stale.
func (r UnpushedCommitsReport) HasOnlyRewrittenUpstream() bool {
	return len(r.Unpushed) == 0 && len(r.RewrittenUpstream) > 0
}

// ListUnpushedCommits classifies commits reachable from the local branch but
// not from refs/remotes/<remote>/<branch> (oldest-last, one-line-per-commit
// summaries as `git log --oneline` would print). A commit is RewrittenUpstream
// instead of Unpushed when it is also reachable from history the remote has
// since moved away from via a force-push: its stale refs/remotes/<remote>/synced/<branch>,
// the previous value of refs/remotes/<remote>/<branch> (via its reflog), or
// any priorTips the caller already captured (e.g. a snapshot of
// RemoteBranchRef taken before its most recent fetch). This keeps a clone
// that only ever saw the OLD history from being told it has "unpushed"
// commits forever after another clone rewrites the remote.
func ListUnpushedCommits(remote, branch string, priorTips ...string) (UnpushedCommitsReport, error) {
	remoteRef := "refs/remotes/" + remote + "/" + branch
	remoteTip, err := revParse(remoteRef)
	if err != nil {
		return UnpushedCommitsReport{}, fmt.Errorf("resolving %s: %w", remoteRef, err)
	}
	if remoteTip == "" {
		return UnpushedCommitsReport{}, nil
	}

	localRef := "refs/heads/" + branch

	all, err := gitLogOneline(localRef, remoteRef)
	if err != nil {
		return UnpushedCommitsReport{}, fmt.Errorf("git log %s --not %s: %w", localRef, remoteRef, err)
	}
	if len(all) == 0 {
		return UnpushedCommitsReport{}, nil
	}

	excludeRefs := []string{remoteRef}
	for _, tip := range priorTips {
		if tip != "" {
			excludeRefs = append(excludeRefs, tip)
		}
	}
	// Only tips OBSERVED on the remote's branch count as upstream history:
	// the remote's synced/<branch> is pushed by this clone's own git-annex
	// sync and may hold genuine local work, so it is NOT excluded here.
	for _, tip := range observedRemoteTips(remoteRef, "") {
		excludeRefs = append(excludeRefs, tip)
	}

	unpushed, err := gitLogOneline(localRef, excludeRefs...)
	if err != nil {
		return UnpushedCommitsReport{}, fmt.Errorf("git log %s --not %s: %w", localRef, strings.Join(excludeRefs, " "), err)
	}

	unpushedSet := make(map[string]bool, len(unpushed))
	for _, line := range unpushed {
		unpushedSet[commitHash(line)] = true
	}
	var rewritten []string
	for _, line := range all {
		if !unpushedSet[commitHash(line)] {
			rewritten = append(rewritten, line)
		}
	}

	return UnpushedCommitsReport{Unpushed: unpushed, RewrittenUpstream: rewritten}, nil
}

// commitHash extracts the abbreviated commit hash from a `git log --oneline` line.
func commitHash(logLine string) string {
	fields := strings.Fields(logLine)
	if len(fields) == 0 {
		return logLine
	}
	return fields[0]
}

// gitLogOneline returns `git log --oneline --no-decorate <positiveRef> [--not <negativeRefs...>]`,
// one line per commit (empty when there are none).
func gitLogOneline(positiveRef string, negativeRefs ...string) ([]string, error) {
	args := []string{"log", "--oneline", "--no-decorate", positiveRef}
	if len(negativeRefs) > 0 {
		args = append(args, "--not")
		args = append(args, negativeRefs...)
	}
	out, err := Command("git", args...).Output()
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines, nil
}

// ResetSyncedRefs points the local git-annex synced/<branch> branch at
// remote's current <branch> -- the rewritten history -- unless remote's own
// synced/<branch> is not stale relative to it (i.e. <branch> is an ancestor
// of synced/<branch>), in which case synced/<branch> is used instead (it may
// carry extra git-annex-branch-merge commits). A remote rewrite of <branch>
// alone otherwise leaves its synced/<branch> pointing at the OLD history,
// which would just re-trigger the rewrite guard on the next sync.
//
// When remote's own synced/<branch> is stale, ResetSyncedRefs also
// force-updates it (via --force-with-lease anchored to the stale tip it
// just observed, so a concurrent push isn't clobbered) to remote's current
// <branch>. This is necessary, not cosmetic: git-annex sync's merge step
// pulls and merges refs/remotes/<remote>/synced/<branch> directly from the
// remote, so leaving the remote's copy stale reproduces the exact same
// merge conflict the rewrite guard exists to avoid, on the very next sync.
//
// The caller must have already fetched remote; priorTips are extra
// candidate pre-rewrite tips to pass through to ListUnpushedCommits (e.g. a
// RemoteBranchRef snapshot taken before that fetch).
//
// It refuses (returning *UnpushedCommitsError) when local <branch> has
// genuinely unpushed commits the remote doesn't have, in either its current
// or pre-rewrite history, to avoid orphaning them; it refuses (returning
// *StaleLocalBranchError) when local <branch> has no genuine local work but
// still sits on the OLD, rewritten-away history, since it should adopt the
// new history first. Either way it never touches local <branch>, the
// remote's <branch>, or any ref other than synced/<branch> (local and
// remote).
func ResetSyncedRefs(remote, branch string, priorTips ...string) error {
	report, err := ListUnpushedCommits(remote, branch, priorTips...)
	if err != nil {
		return err
	}
	if report.HasUnpushed() {
		return &UnpushedCommitsError{Remote: remote, Branch: branch, Commits: report.Unpushed}
	}
	if report.HasOnlyRewrittenUpstream() {
		return &StaleLocalBranchError{Remote: remote, Branch: branch, Commits: report.RewrittenUpstream}
	}

	branchRef := "refs/remotes/" + remote + "/" + branch
	branchTip, err := revParse(branchRef)
	if err != nil {
		return fmt.Errorf("resolving %s: %w", branchRef, err)
	}
	if branchTip == "" {
		return fmt.Errorf("remote %s has no %s to reset from", remote, branch)
	}
	remoteSyncedRef := "refs/remotes/" + remote + "/synced/" + branch
	syncedTip, err := revParse(remoteSyncedRef)
	if err != nil {
		return fmt.Errorf("resolving %s: %w", remoteSyncedRef, err)
	}

	stale := false
	target := branchTip
	if syncedTip != "" {
		notStale, err := isAncestor(branchTip, syncedTip)
		if err != nil {
			return fmt.Errorf("checking staleness of %s: %w", remoteSyncedRef, err)
		}
		if notStale {
			target = syncedTip
		} else {
			stale = true
		}
	}

	localRef := "refs/heads/synced/" + branch
	if out, err := Command("git", "update-ref", localRef, target).CombinedOutput(); err != nil {
		return fmt.Errorf("git update-ref %s %s: %w\n%s", localRef, target, err, strings.TrimSpace(string(out)))
	}

	if stale {
		remoteRefName := "refs/heads/synced/" + branch
		lease := fmt.Sprintf("--force-with-lease=%s:%s", remoteRefName, syncedTip)
		refspec := branchRef + ":" + remoteRefName
		if out, err := Command("git", "push", lease, remote, refspec).CombinedOutput(); err != nil {
			return fmt.Errorf("git push %s %s %s: %w\n%s", lease, remote, refspec, err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// revParse resolves ref to its commit SHA, returning "" (no error) when ref
// does not exist.
func revParse(ref string) (string, error) {
	out, err := Command("git", "rev-parse", "--verify", "--quiet", ref).Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// isAncestor reports whether ancestor is an ancestor of (or equal to) descendant.
func isAncestor(ancestor, descendant string) (bool, error) {
	err := Command("git", "merge-base", "--is-ancestor", ancestor, descendant).Run()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}
