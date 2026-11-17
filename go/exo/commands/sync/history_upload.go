package sync

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	initcmd "github.com/Genentech/exohub/go/exo/commands/init"
	"github.com/Genentech/exohub/go/exo/commandutil"
)

// historyMarkerRef returns the strictly local ref that tracks the last
// commit whose content was fully uploaded to remote for branch. It lives
// under refs/exo/, never refs/heads/ or refs/tags/, so it is never listed by
// `git branch`/`git tag` and is never pushed or fetched by plain `git push`,
// `git annex sync`, or clones: those only transfer refs/heads/* and
// refs/tags/* (plus git-annex's own refs/synced/* and the git-annex branch).
func historyMarkerRef(remote, branch string) string {
	return fmt.Sprintf("refs/exo/content-synced/%s/%s", remote, branch)
}

// currentSyncBranch returns the current branch name, or false on detached
// HEAD (nothing to key a marker on).
func currentSyncBranch() (string, bool) {
	out, err := runCommandOutput([]string{"git", "rev-parse", "--abbrev-ref", "HEAD"})
	branch := strings.TrimSpace(out)
	if err != nil || branch == "" || branch == "HEAD" {
		return "", false
	}
	return branch, true
}

// readMarkerCommit resolves ref to a commit sha, or false if it doesn't
// exist yet (first run for this remote/branch pair).
func readMarkerCommit(ref string) (string, bool) {
	out, err := runCommandOutput([]string{"git", "rev-parse", "--verify", "--quiet", ref})
	sha := strings.TrimSpace(out)
	if err != nil || sha == "" {
		return "", false
	}
	return sha, true
}

func writeMarkerCommit(ref, commit string) error {
	return runCommand([]string{"git", "update-ref", ref, commit})
}

func isCommitAncestor(ancestor, commit string) bool {
	cmd := commandutil.Command("git", "merge-base", "--is-ancestor", ancestor, commit)
	return cmd.Run() == nil
}

func commitMergeBase(a, b string) (string, error) {
	out, err := runCommandOutput([]string{"git", "merge-base", a, b})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// upstreamCommit resolves @{upstream} for the current branch, or false if no
// upstream tracking branch is configured.
func upstreamCommit() (string, bool) {
	out, err := runCommandOutput([]string{"git", "rev-parse", "--verify", "--quiet", "@{upstream}"})
	sha := strings.TrimSpace(out)
	if err != nil || sha == "" {
		return "", false
	}
	return sha, true
}

// commitsSinceBaseline lists commits reachable from head, in chronological
// (oldest-first) order, excluding those reachable from baseline. An empty
// baseline means "from the beginning of history" (first upload).
func commitsSinceBaseline(baseline, head string) ([]string, error) {
	var args []string
	if baseline == "" {
		args = []string{"git", "rev-list", "--reverse", head}
	} else {
		args = []string{"git", "rev-list", "--reverse", baseline + ".." + head}
	}
	out, err := runCommandOutput(args)
	if err != nil {
		return nil, err
	}
	var commits []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			commits = append(commits, line)
		}
	}
	return commits, nil
}

// historyTreeEntry is one added/modified blob at a given commit, ready to
// feed `git update-index --index-info`.
type historyTreeEntry struct {
	mode string
	sha  string
	path string
}

// diffTreeAddedModified lists the blobs added, modified, or type-changed
// (annex symlink <-> pointer file) at commit, relative to its first parent
// (or the empty tree, for a root commit via --root). Deletions and renames
// carry no new content to upload, so they are excluded by --diff-filter.
func diffTreeAddedModified(commit string) ([]historyTreeEntry, error) {
	out, err := runCommandOutput([]string{
		"git", "diff-tree", "-r", "--root", "--no-commit-id", "--no-renames",
		"--diff-filter=AMT", "-z", commit,
	})
	if err != nil {
		return nil, err
	}
	parts := strings.Split(out, "\x00")
	var entries []historyTreeEntry
	for i := 0; i+1 < len(parts); i += 2 {
		meta := parts[i]
		path := parts[i+1]
		if !strings.HasPrefix(meta, ":") {
			continue
		}
		fields := strings.Fields(strings.TrimPrefix(meta, ":"))
		if len(fields) < 5 {
			continue
		}
		entries = append(entries, historyTreeEntry{mode: fields[1], sha: fields[3], path: path})
	}
	return entries, nil
}

// buildSyntheticTree builds a git tree object containing exactly entries,
// via a throwaway index file, so `git annex copy --branch=<tree>` can be
// pointed at just the blobs changed by one commit without ever checking it
// out. The caller must call the returned cleanup function.
func buildSyntheticTree(entries []historyTreeEntry) (tree string, cleanup func(), err error) {
	tmpDir, err := os.MkdirTemp("", "exo-sync-history-*")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() { os.RemoveAll(tmpDir) }
	indexPath := tmpDir + "/index"
	indexEnv := "GIT_INDEX_FILE=" + indexPath

	var stdin bytes.Buffer
	for _, e := range entries {
		fmt.Fprintf(&stdin, "%s blob %s\t%s\n", e.mode, e.sha, e.path)
	}

	addCmd := commandutil.Command("git", "update-index", "--add", "--index-info")
	addCmd.Env = append(os.Environ(), indexEnv)
	addCmd.Stdin = &stdin
	var stderr bytes.Buffer
	addCmd.Stderr = &stderr
	if err := addCmd.Run(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("git update-index --index-info: %w: %s", err, stderr.String())
	}

	writeCmd := commandutil.Command("git", "write-tree")
	writeCmd.Env = append(os.Environ(), indexEnv)
	out, err := writeCmd.Output()
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("git write-tree: %w", err)
	}
	return strings.TrimSpace(string(out)), cleanup, nil
}

// copyHistoryBranch uploads every annexed key in tree to remote, routing
// aware: --want-get-by is passed whenever the remote has any preferred
// content expression configured (including the literal "standard", which
// git-annex resolves from the remote's group), matching the convention
// already established by `exo sync --all` (go/exo/commands/sync/all.go).
func copyHistoryBranch(tree, remote, jobs string, hasPreferredContent bool) error {
	args := []string{"git", "annex", "copy", "--branch=" + tree, "--to", remote, "--jobs", jobs}
	if hasPreferredContent {
		args = append(args, "--want-get-by", remote)
	}
	return runCommand(args)
}

// historyUploadBaseline resolves the starting point for the commit range a
// remote's incremental history upload (or preview) should process: the
// local marker if it is still an ancestor of head, the merge-base of marker
// and head otherwise (rebase/force-push), the upstream tip when no marker
// exists yet, or "" (full history) when neither exists. usedUpstream
// reports whether the upstream fallback was used, so callers that actually
// upload can print a one-time hint; a read-only preview ignores it.
func historyUploadBaseline(markerRef, head string) (baseline string, usedUpstream bool, err error) {
	if markerCommit, ok := readMarkerCommit(markerRef); ok {
		if isCommitAncestor(markerCommit, head) {
			return markerCommit, false, nil
		}
		mb, err := commitMergeBase(markerCommit, head)
		if err != nil {
			return "", false, fmt.Errorf("resolving merge-base for %s: %w", markerRef, err)
		}
		return mb, false, nil
	}
	if up, ok := upstreamCommit(); ok {
		return up, true, nil
	}
	return "", false, nil
}

// uploadHistorySinceMarker uploads the content of every commit between the
// last marker (or an appropriate baseline) and HEAD to remote, advancing
// the local refs/exo/content-synced/<remote>/<branch> marker one commit at a
// time so a failure partway through leaves already-uploaded commits marked
// and only the remainder is retried on the next sync.
func uploadHistorySinceMarker(remote, branch, jobs string) error {
	head, err := runCommandOutput([]string{"git", "rev-parse", "HEAD"})
	if err != nil {
		return fmt.Errorf("resolving HEAD: %w", err)
	}
	head = strings.TrimSpace(head)
	if head == "" {
		return nil
	}

	markerRef := historyMarkerRef(remote, branch)
	baseline, usedUpstream, err := historyUploadBaseline(markerRef, head)
	if err != nil {
		return err
	}
	if usedUpstream {
		fmt.Printf("No history-upload marker yet for remote '%s' branch '%s'; starting from the upstream tip. Run 'exo sync --check' to confirm older history is already uploaded.\n", remote, branch)
	}

	if baseline == head {
		return writeMarkerCommit(markerRef, head)
	}

	commits, err := commitsSinceBaseline(baseline, head)
	if err != nil {
		return fmt.Errorf("listing commits for history upload to '%s': %w", remote, err)
	}
	if len(commits) == 0 {
		return writeMarkerCommit(markerRef, head)
	}

	currentWanted, _ := runCommandOutput([]string{"git", "annex", "wanted", remote})
	hasPreferredContent := strings.TrimSpace(currentWanted) != ""

	for _, commit := range commits {
		entries, err := diffTreeAddedModified(commit)
		if err != nil {
			return fmt.Errorf("diffing commit %s for history upload: %w", commit, err)
		}
		if len(entries) > 0 {
			tree, cleanup, err := buildSyntheticTree(entries)
			if err != nil {
				return fmt.Errorf("building synthetic tree for commit %s: %w", commit, err)
			}
			fmt.Printf("  uploading history at %s to '%s' (%d file(s))\n", commit[:12], remote, len(entries))
			copyErr := copyHistoryBranch(tree, remote, jobs, hasPreferredContent)
			cleanup()
			if copyErr != nil {
				return fmt.Errorf("uploading history at %s to '%s': %w", commit[:12], remote, copyErr)
			}
		}
		if err := writeMarkerCommit(markerRef, commit); err != nil {
			return fmt.Errorf("advancing history marker for '%s': %w", remote, err)
		}
	}
	return nil
}

// uploadHistoryForRemotes runs the default incremental history upload for
// every writable, plain annex remote among candidates (the same filtering
// `exo sync --all` applies, via allTargetRemotes), for the current branch.
// Failures are reported but never abort the overall `exo sync`: the normal
// work-tree content sync already succeeded by the time this runs, and the
// incremental catch-up is a best-effort safety net, retried on the next
// sync from wherever its marker last advanced to.
func uploadHistoryForRemotes(candidates []string, remotesConfig *initcmd.RemotesConfig, jobs string) {
	branch, ok := currentSyncBranch()
	if !ok {
		return
	}
	targets, err := allTargetRemotes(candidates, remotesConfig)
	if err != nil || len(targets) == 0 {
		return
	}
	for _, remote := range targets {
		if err := uploadHistorySinceMarker(remote, branch, jobs); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: default history upload to '%s' failed: %v\n", remote, err)
		}
	}
}

// historyUploadPreviewCommit is one pending commit's change count in a
// --dry-run / --json preview of the default incremental history pass.
type historyUploadPreviewCommit struct {
	Commit string `json:"commit"`
	Files  int    `json:"files"`
}

// historyUploadPreviewRemote is one remote's pending incremental-history
// catch-up, as reported by `exo sync --dry-run` / `--json`. Computing it
// never uploads anything and never touches the remote's marker.
type historyUploadPreviewRemote struct {
	Remote     string                       `json:"remote"`
	Branch     string                       `json:"branch"`
	Baseline   string                       `json:"baseline,omitempty"`
	Commits    []historyUploadPreviewCommit `json:"commits"`
	TotalFiles int                          `json:"total_files"`
}

// previewHistoryUpload computes, for each writable plain annex remote among
// candidates and the current branch, the commit range and per-commit file
// counts the default incremental history upload would process, without
// uploading anything and without advancing any marker. Remotes with nothing
// pending are omitted. Returns nil (no error) on detached HEAD, where there
// is no branch to key a preview on.
func previewHistoryUpload(candidates []string, remotesConfig *initcmd.RemotesConfig) ([]historyUploadPreviewRemote, error) {
	branch, ok := currentSyncBranch()
	if !ok {
		return nil, nil
	}
	targets, err := allTargetRemotes(candidates, remotesConfig)
	if err != nil {
		return nil, err
	}

	var previews []historyUploadPreviewRemote
	for _, remote := range targets {
		preview, err := previewHistorySinceMarker(remote, branch)
		if err != nil {
			return nil, fmt.Errorf("previewing history upload to '%s': %w", remote, err)
		}
		if preview == nil {
			continue
		}
		previews = append(previews, *preview)
	}
	return previews, nil
}

// previewHistorySinceMarker mirrors uploadHistorySinceMarker's range
// resolution exactly (via historyUploadBaseline), but only counts pending
// changed entries per commit: no synthetic-tree build, no `git annex copy`,
// no marker write. Returns nil when there is nothing pending (already
// caught up).
func previewHistorySinceMarker(remote, branch string) (*historyUploadPreviewRemote, error) {
	head, err := runCommandOutput([]string{"git", "rev-parse", "HEAD"})
	if err != nil {
		return nil, fmt.Errorf("resolving HEAD: %w", err)
	}
	head = strings.TrimSpace(head)
	if head == "" {
		return nil, nil
	}

	markerRef := historyMarkerRef(remote, branch)
	baseline, _, err := historyUploadBaseline(markerRef, head)
	if err != nil {
		return nil, err
	}
	if baseline == head {
		return nil, nil
	}

	commits, err := commitsSinceBaseline(baseline, head)
	if err != nil {
		return nil, fmt.Errorf("listing commits for history preview to '%s': %w", remote, err)
	}
	if len(commits) == 0 {
		return nil, nil
	}

	preview := &historyUploadPreviewRemote{Remote: remote, Branch: branch, Baseline: baseline}
	for _, commit := range commits {
		entries, err := diffTreeAddedModified(commit)
		if err != nil {
			return nil, fmt.Errorf("diffing commit %s for history preview: %w", commit, err)
		}
		preview.Commits = append(preview.Commits, historyUploadPreviewCommit{Commit: commit, Files: len(entries)})
		preview.TotalFiles += len(entries)
	}
	return preview, nil
}

// buildHistoryUploadPreviewJSON renders previews as a JSON array for the
// `exo sync --dry-run --json` report's "pending_history_upload" field,
// mirroring buildCatalogTargetsJSON's convention: "" (field omitted
// entirely) when there is nothing pending.
func buildHistoryUploadPreviewJSON(previews []historyUploadPreviewRemote) string {
	if len(previews) == 0 {
		return ""
	}
	data, err := json.Marshal(previews)
	if err != nil {
		return ""
	}
	return string(data)
}

// printHistoryUploadPreviewText writes a human-readable "pending history
// catch-up" section for one remote's preview to the --dry-run text report.
func printHistoryUploadPreviewText(w io.Writer, preview *historyUploadPreviewRemote) {
	if preview == nil {
		return
	}
	fmt.Fprintf(w, "  pending history catch-up: %d commit(s), %d file(s) total\n", len(preview.Commits), preview.TotalFiles)
	for _, c := range preview.Commits {
		short := c.Commit
		if len(short) > 12 {
			short = short[:12]
		}
		fmt.Fprintf(w, "    %s: %d file(s)\n", short, c.Files)
	}
}
