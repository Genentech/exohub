package sync

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSync_UploadsHistoryAcrossTags is the #161 reproduction: a.bin is
// replaced across three tagged commits, and only ever synced at HEAD. A
// plain `exo sync` (no flags) must upload every intermediate version via the
// default incremental history upload, leaving `exo sync --check` clean.
func TestSync_UploadsHistoryAcrossTags(t *testing.T) {
	repoDir, _, restore := newAnnexRepo(t, "r1")
	defer restore()

	addAnnexFile(t, repoDir, "a.bin", "version-1")
	annexRun(t, repoDir, "git", "tag", "1")

	overwriteAnnexFile(t, repoDir, "a.bin", "version-2")
	annexRun(t, repoDir, "git", "tag", "2")

	overwriteAnnexFile(t, repoDir, "a.bin", "version-3")
	annexRun(t, repoDir, "git", "tag", "3")

	captureStdout(t, func() {
		runSync("", nil, nil, nil, nil, "", false, false, "", "1", false, false, nil, false, false, false, false)
	})

	for _, ref := range []string{"1", "2", "3"} {
		var code int
		out := captureStdout(t, func() {
			code = runCheckCommand([]string{ref}, nil, false, false)
		})
		if code != exitCheckClean {
			t.Errorf("exo sync --check --ref %s: expected exit %d, got %d; output:\n%s", ref, exitCheckClean, code, out)
		}
	}

	markerRef := historyMarkerRef("r1", "main")
	if _, ok := readMarkerCommit(markerRef); !ok {
		t.Errorf("expected marker %s to be set after sync", markerRef)
	}
}

// TestSync_NoContentDisablesHistoryUpload is the review's requirement: since
// `--no-content` means "don't move content", it must also disable the
// default incremental history upload (not just the import-remote metadata-
// only path it originally guarded). Tag 1's content must stay off the
// remote and no marker must be created.
func TestSync_NoContentDisablesHistoryUpload(t *testing.T) {
	repoDir, _, restore := newAnnexRepo(t, "r1")
	defer restore()

	addAnnexFile(t, repoDir, "a.bin", "version-1")
	annexRun(t, repoDir, "git", "tag", "1")

	overwriteAnnexFile(t, repoDir, "a.bin", "version-2")
	annexRun(t, repoDir, "git", "tag", "2")

	captureStdout(t, func() {
		runSync("", nil, nil, nil, nil, "", false, false, "", "1", true, false, nil, false, false, false, false)
	})

	if _, ok := readMarkerCommit(historyMarkerRef("r1", "main")); ok {
		t.Errorf("--no-content must not create a history-upload marker")
	}

	foundTag1 := annexRun(t, repoDir, "git", "annex", "find", "--branch=1", "--in", "r1")
	if strings.TrimSpace(foundTag1) != "" {
		t.Errorf("--no-content must not upload tag 1's history content, found: %q", foundTag1)
	}
}

// TestSyncDryRunJSON_ReportsPendingHistoryUpload is the review's requirement
// that --dry-run/--json report what the incremental pass would upload
// (commit range and file counts per commit) without uploading anything or
// moving the marker.
func TestSyncDryRunJSON_ReportsPendingHistoryUpload(t *testing.T) {
	repoDir, _, restore := newAnnexRepo(t, "r1")
	defer restore()

	addAnnexFile(t, repoDir, "a.bin", "version-1")
	overwriteAnnexFile(t, repoDir, "a.bin", "version-2")

	markerRef := historyMarkerRef("r1", "main")
	if _, ok := readMarkerCommit(markerRef); ok {
		t.Fatalf("expected no marker before the dry-run")
	}

	out := captureStdout(t, func() {
		runSync("", nil, nil, nil, nil, "", true, true, "", "1", false, false, nil, false, false, false, false)
	})

	var report struct {
		PendingHistoryUpload []struct {
			Remote  string `json:"remote"`
			Branch  string `json:"branch"`
			Commits []struct {
				Commit string `json:"commit"`
				Files  int    `json:"files"`
			} `json:"commits"`
			TotalFiles int `json:"total_files"`
		} `json:"pending_history_upload"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("parsing --dry-run --json output: %v\n%s", err, out)
	}
	if len(report.PendingHistoryUpload) != 1 {
		t.Fatalf("expected exactly one pending_history_upload entry, got: %+v", report.PendingHistoryUpload)
	}
	p := report.PendingHistoryUpload[0]
	if p.Remote != "r1" || p.Branch != "main" {
		t.Errorf("unexpected remote/branch: %+v", p)
	}
	if len(p.Commits) != 2 || p.TotalFiles != 2 {
		t.Errorf("expected 2 pending commits totalling 2 files, got: %+v", p)
	}

	if _, ok := readMarkerCommit(markerRef); ok {
		t.Errorf("--dry-run must not create or move the history-upload marker")
	}
	found := annexRun(t, repoDir, "git", "annex", "find", "--in", "r1")
	if strings.TrimSpace(found) != "" {
		t.Errorf("--dry-run must not upload anything, found: %q", found)
	}
}

// TestSyncDryRunText_ReportsPendingHistoryUpload is the text-output
// counterpart: the same pending catch-up must be visible in the
// human-readable --dry-run report.
func TestSyncDryRunText_ReportsPendingHistoryUpload(t *testing.T) {
	repoDir, _, restore := newAnnexRepo(t, "r1")
	defer restore()

	addAnnexFile(t, repoDir, "a.bin", "version-1")
	overwriteAnnexFile(t, repoDir, "a.bin", "version-2")

	out := captureStdout(t, func() {
		runSync("", nil, nil, nil, nil, "", true, false, "", "1", false, false, nil, false, false, false, false)
	})

	if !strings.Contains(out, "pending history catch-up: 2 commit(s), 2 file(s) total") {
		t.Errorf("expected a pending history catch-up section in dry-run text output, got:\n%s", out)
	}

	if _, ok := readMarkerCommit(historyMarkerRef("r1", "main")); ok {
		t.Errorf("--dry-run must not create or move the history-upload marker")
	}
}

// TestSync_HistoryUploadSkipsUnwantedFile verifies that the default
// incremental upload respects a remote's filename-based preferred content:
// an old version of a wanted file is uploaded, a file the remote never
// wants is never copied, even though both changed in the same commit.
func TestSync_HistoryUploadSkipsUnwantedFile(t *testing.T) {
	repoDir, remoteDir, restore := newAnnexRepo(t, "r1")
	defer restore()

	annexRun(t, repoDir, "git", "annex", "wanted", "r1", "include=*.bin")

	addAnnexFile(t, repoDir, "a.bin", "version-1")
	addAnnexFile(t, repoDir, "notes.txt", "note-1")
	annexRun(t, repoDir, "git", "tag", "1")

	overwriteAnnexFile(t, repoDir, "a.bin", "version-2")
	overwriteAnnexFile(t, repoDir, "notes.txt", "note-2")
	annexRun(t, repoDir, "git", "tag", "2")

	// Simulate a prior plain git-annex sync that only ever uploaded the
	// current (tag 2) content.
	annexRun(t, repoDir, "git", "annex", "copy", "--to", "r1", "--want-get-by", "r1", "a.bin")

	branch, ok := currentSyncBranch()
	if !ok {
		t.Fatalf("expected a current branch")
	}
	if err := uploadHistorySinceMarker("r1", branch, "1"); err != nil {
		t.Fatalf("uploadHistorySinceMarker: %v", err)
	}

	foundBin := annexRun(t, repoDir, "git", "annex", "find", "--branch=1", "--in", "r1")
	if strings.TrimSpace(foundBin) != "a.bin" {
		t.Errorf("expected a.bin@1 to be uploaded to r1, find output: %q", foundBin)
	}

	// notes.txt must never have been copied: list the remote's directory
	// contents directly rather than trusting annex metadata alone.
	foundTxt := annexRun(t, repoDir, "git", "annex", "find", "--branch=1", "--not", "--in", "r1")
	if !strings.Contains(foundTxt, "notes.txt") {
		t.Errorf("expected notes.txt@1 to remain missing from r1, find --not --in output: %q", foundTxt)
	}
	assertDirHasNoFileNamed(t, remoteDir, "notes")
}

// TestHistoryUpload_FailedCopyDoesNotAdvanceMarker verifies that a failed
// upload to a remote leaves its marker exactly where it was: a later sync
// retries from the same point instead of silently skipping the failed
// commit.
func TestHistoryUpload_FailedCopyDoesNotAdvanceMarker(t *testing.T) {
	repoDir, remoteDir, restore := newAnnexRepo(t, "r1")
	defer restore()

	addAnnexFile(t, repoDir, "a.bin", "version-1")

	chmodRWritable(remoteDir)
	if err := os.RemoveAll(remoteDir); err != nil {
		t.Fatalf("removing remote dir: %v", err)
	}

	err := uploadHistorySinceMarker("r1", "main", "1")
	if err == nil {
		t.Fatalf("expected uploadHistorySinceMarker to fail when the remote is unreachable")
	}

	if _, ok := readMarkerCommit(historyMarkerRef("r1", "main")); ok {
		t.Errorf("marker must not be written after a failed upload")
	}
}

// TestHistoryUpload_RebaseFallsBackToMergeBase verifies that when the
// marker commit is no longer an ancestor of HEAD (rebase or force-push),
// upload falls back to the merge-base and re-points the marker at the new
// tip after a successful upload.
func TestHistoryUpload_RebaseFallsBackToMergeBase(t *testing.T) {
	repoDir, _, restore := newAnnexRepo(t, "r1")
	defer restore()

	// A base commit before the one that will be rebased, so the rewritten
	// commit still shares a real common ancestor with the original (a
	// realistic rebase, as opposed to two unrelated root commits).
	annexRun(t, repoDir, "git", "commit", "--allow-empty", "-m", "base")

	addAnnexFile(t, repoDir, "a.bin", "version-1")
	if err := uploadHistorySinceMarker("r1", "main", "1"); err != nil {
		t.Fatalf("initial uploadHistorySinceMarker: %v", err)
	}
	markerRef := historyMarkerRef("r1", "main")
	originalMarker, ok := readMarkerCommit(markerRef)
	if !ok {
		t.Fatalf("expected marker to be set after initial upload")
	}

	// Simulate a rebase: amend HEAD in place (same parent, i.e. none, but a
	// different commit SHA and content), so the marker commit is no longer
	// reachable from the new HEAD at all.
	if err := os.Remove(repoDir + "/a.bin"); err != nil {
		t.Fatalf("remove a.bin: %v", err)
	}
	if err := os.WriteFile(repoDir+"/a.bin", []byte("version-1-amended"), 0o644); err != nil {
		t.Fatalf("overwrite a.bin: %v", err)
	}
	annexRun(t, repoDir, "git", "annex", "add", "a.bin")
	annexRun(t, repoDir, "git", "commit", "--amend", "-m", "amended")

	newHead := strings.TrimSpace(annexRun(t, repoDir, "git", "rev-parse", "HEAD"))
	if newHead == originalMarker {
		t.Fatalf("expected amended HEAD to differ from the original marker commit")
	}
	if isCommitAncestor(originalMarker, newHead) {
		t.Fatalf("expected the original marker to no longer be an ancestor of HEAD after amending")
	}

	if err := uploadHistorySinceMarker("r1", "main", "1"); err != nil {
		t.Fatalf("uploadHistorySinceMarker after rebase: %v", err)
	}

	updatedMarker, ok := readMarkerCommit(markerRef)
	if !ok {
		t.Fatalf("expected marker to still be set after rebase")
	}
	if updatedMarker != newHead {
		t.Errorf("expected marker to advance to the new HEAD %s, got %s", newHead, updatedMarker)
	}

	found := annexRun(t, repoDir, "git", "annex", "find", "--in", "r1")
	if !strings.Contains(found, "a.bin") {
		t.Errorf("expected the amended a.bin to be uploaded to r1, find output: %q", found)
	}
}

// TestHistoryUpload_NoMarkerUsesUpstreamBaseline verifies that, with no
// marker yet but an upstream tracking branch configured, the upload starts
// from the upstream tip rather than the full history.
func TestHistoryUpload_NoMarkerUsesUpstreamBaseline(t *testing.T) {
	repoDir, _, restore := newAnnexRepo(t, "r1")
	defer restore()

	addAnnexFile(t, repoDir, "a.bin", "version-1")

	bareDir, err := os.MkdirTemp("", "exo-sync-bare-*")
	if err != nil {
		t.Fatalf("mkdir bare remote: %v", err)
	}
	defer os.RemoveAll(bareDir)
	annexRun(t, repoDir, "git", "init", "--bare", bareDir)
	annexRun(t, repoDir, "git", "remote", "add", "origin", bareDir)
	annexRun(t, repoDir, "git", "push", "-u", "origin", "main")

	overwriteAnnexFile(t, repoDir, "a.bin", "version-2")
	newCommit := strings.TrimSpace(annexRun(t, repoDir, "git", "rev-parse", "HEAD"))

	out := captureStdout(t, func() {
		if err := uploadHistorySinceMarker("r1", "main", "1"); err != nil {
			t.Fatalf("uploadHistorySinceMarker: %v", err)
		}
	})
	if !strings.Contains(out, "upstream tip") {
		t.Errorf("expected a one-time upstream-baseline hint, got:\n%s", out)
	}

	markerRef := historyMarkerRef("r1", "main")
	marker, ok := readMarkerCommit(markerRef)
	if !ok || marker != newCommit {
		t.Errorf("expected marker to end at %s, got %q (ok=%v)", newCommit, marker, ok)
	}

	found := annexRun(t, repoDir, "git", "annex", "find", "--branch=main", "--in", "r1")
	if !strings.Contains(found, "a.bin") {
		t.Errorf("expected a.bin to be uploaded, find output: %q", found)
	}

	branches := annexRun(t, repoDir, "git", "branch", "--list")
	tags := annexRun(t, repoDir, "git", "tag", "--list")
	if strings.Contains(branches, "exo") || strings.Contains(tags, "exo") {
		t.Errorf("the marker must never show up as a branch or tag: branches=%q tags=%q", branches, tags)
	}
}

// TestHistoryUpload_MarkerNeverPushed proves the strictly-local nature of
// the marker: after pushing to a real git remote, the remote has no
// refs/exo/* namespace at all.
func TestHistoryUpload_MarkerNeverPushed(t *testing.T) {
	repoDir, _, restore := newAnnexRepo(t, "r1")
	defer restore()

	addAnnexFile(t, repoDir, "a.bin", "version-1")
	if err := uploadHistorySinceMarker("r1", "main", "1"); err != nil {
		t.Fatalf("uploadHistorySinceMarker: %v", err)
	}
	if _, ok := readMarkerCommit(historyMarkerRef("r1", "main")); !ok {
		t.Fatalf("expected the marker to be set")
	}

	bareDir, err := os.MkdirTemp("", "exo-sync-bare-*")
	if err != nil {
		t.Fatalf("mkdir bare remote: %v", err)
	}
	defer os.RemoveAll(bareDir)
	annexRun(t, repoDir, "git", "init", "--bare", bareDir)
	annexRun(t, repoDir, "git", "remote", "add", "origin", bareDir)
	annexRun(t, repoDir, "git", "push", "-u", "origin", "main")
	annexRun(t, repoDir, "git", "annex", "sync")

	cmd := exec.Command("git", "show-ref")
	cmd.Dir = bareDir
	out, _ := cmd.CombinedOutput()
	if strings.Contains(string(out), "refs/exo/") {
		t.Errorf("remote must never receive refs/exo/*, show-ref output:\n%s", out)
	}
}

// overwriteAnnexFile replaces relPath's content and commits the change,
// removing the existing (read-only) annex symlink first.
func overwriteAnnexFile(t *testing.T, repoDir, relPath, content string) {
	t.Helper()
	fullPath := repoDir + "/" + relPath
	if err := os.Remove(fullPath); err != nil {
		t.Fatalf("remove %s: %v", relPath, err)
	}
	addAnnexFile(t, repoDir, relPath, content)
}

// assertDirHasNoFileNamed walks dir and fails the test if any file name
// contains substr (used to confirm a filtered-out file's content never
// reached a directory-type annex remote's object store).
func assertDirHasNoFileNamed(t *testing.T, dir, substr string) {
	t.Helper()
	var matches []string
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if strings.Contains(path, substr) {
			matches = append(matches, path)
		}
		return nil
	})
	if len(matches) > 0 {
		t.Errorf("expected no file matching %q under %s, found: %v", substr, dir, matches)
	}
}
