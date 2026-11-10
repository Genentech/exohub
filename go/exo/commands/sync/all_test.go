package sync

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSyncAll_RespectsStandardWantedViaGroup is the re-review's regression
// test: a remote with `wanted standard` is not "no preferred content" --
// `standard` is itself a real preferred-content expression that git-annex
// resolves from the remote's group. A remote in group `manual` (which wants
// nothing by default) must receive nothing from `--all`, and the `--dry-run`
// preview must agree with the real run exactly (both ask git-annex itself,
// not the Go-side include=/exclude= approximation).
func TestSyncAll_RespectsStandardWantedViaGroup(t *testing.T) {
	repoDir, _, restore := newAnnexRepo(t, "r1")
	defer restore()

	annexRun(t, repoDir, "git", "annex", "group", "r1", "manual")
	annexRun(t, repoDir, "git", "annex", "wanted", "r1", "standard")

	addAnnexFile(t, repoDir, "a.bin", "content")

	// Dry-run preview must show nothing would be copied.
	out := captureStdout(t, func() {
		code := runAllCommand(nil, true, true, "1")
		if code != 0 {
			t.Fatalf("expected exit 0 for dry-run, got %d", code)
		}
	})
	var dryReport struct {
		Remotes []struct {
			WouldCopy []allCopyItem `json:"would_copy"`
		} `json:"remotes"`
	}
	if err := json.Unmarshal([]byte(out), &dryReport); err != nil {
		t.Fatalf("parsing --json output: %v\n%s", err, out)
	}
	if len(dryReport.Remotes) != 1 || len(dryReport.Remotes[0].WouldCopy) != 0 {
		t.Fatalf("expected would_copy to be empty for a manual-group remote with wanted=standard, got: %+v", dryReport.Remotes)
	}

	// The real run must likewise copy nothing: this is the bug the re-review
	// caught (the old `currentWanted != "standard"` guard skipped
	// --want-get-by here, so the real copy ignored the group's routing).
	code := runAllCommand(nil, false, false, "1")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	found := annexRun(t, repoDir, "git", "annex", "find", "--in", "r1", "--json")
	if strings.TrimSpace(found) != "" {
		t.Fatalf("expected nothing copied to the manual-group remote, but found: %s", found)
	}
}

// TestSyncAll_UploadsOldTagRespectingPreferredContent is the scope review's
// regression test: a remote with filename-based preferred content (`wanted`)
// plus a file replaced across two tags. `exo sync --all` must upload the old
// tag's content and must skip a file the remote doesn't want.
func TestSyncAll_UploadsOldTagRespectingPreferredContent(t *testing.T) {
	repoDir, _, restore := newAnnexRepo(t, "r1")
	defer restore()

	annexRun(t, repoDir, "git", "annex", "wanted", "r1", "include=*.bin")

	addAnnexFile(t, repoDir, "a.bin", "version-1")
	annexRun(t, repoDir, "git", "tag", "v1")

	addAnnexFile(t, repoDir, "notes.txt", "not wanted by r1")

	if err := os.Remove(repoDir + "/a.bin"); err != nil {
		t.Fatalf("remove a.bin: %v", err)
	}
	if err := os.WriteFile(repoDir+"/a.bin", []byte("version-2"), 0o644); err != nil {
		t.Fatalf("overwrite a.bin: %v", err)
	}
	annexRun(t, repoDir, "git", "annex", "add", "a.bin")
	annexRun(t, repoDir, "git", "commit", "-m", "update a.bin")
	annexRun(t, repoDir, "git", "tag", "v2")

	// Nothing has been copied to r1 yet.
	code := runAllCommand(nil, false, false, "1")
	if code != 0 {
		t.Fatalf("expected exit 0 (notes.txt is correctly unwanted, not lost), got %d", code)
	}

	// v1's a.bin content must now be on r1.
	engine, err := newCompletenessEngine(nil)
	if err != nil {
		t.Fatalf("newCompletenessEngine: %v", err)
	}
	v1Report, err := runCompletenessCheck([]string{"v1"}, engine)
	if err != nil {
		t.Fatalf("runCompletenessCheck(v1): %v", err)
	}
	if len(v1Report.Items) != 0 {
		t.Fatalf("expected v1 to be clean after --all, got: %+v", v1Report.Items)
	}

	// notes.txt must never have been copied: r1's wanted expression excludes it.
	v2Report, err := runCompletenessCheck([]string{"v2"}, engine)
	if err != nil {
		t.Fatalf("runCompletenessCheck(v2): %v", err)
	}
	foundNotes := false
	for _, item := range v2Report.Items {
		if item.Path == "notes.txt" {
			foundNotes = true
			if item.State != "fixable" {
				t.Errorf("expected notes.txt to be fixable (present here, unwanted by r1), got state %q", item.State)
			}
		}
	}
	if !foundNotes {
		t.Fatalf("expected notes.txt to still be reported (unwanted by r1), got: %+v", v2Report.Items)
	}
}

// TestSyncAll_DryRunDoesNotUpload verifies --all --dry-run previews without
// copying anything.
func TestSyncAll_DryRunDoesNotUpload(t *testing.T) {
	repoDir, _, restore := newAnnexRepo(t, "r1")
	defer restore()

	addAnnexFile(t, repoDir, "a.bin", "content")

	var out string
	out = captureStdout(t, func() {
		code := runAllCommand(nil, true, true, "1")
		if code != 0 {
			t.Fatalf("expected exit 0 for dry-run, got %d", code)
		}
	})

	var report struct {
		DryRun  bool `json:"dry_run"`
		Remotes []struct {
			Remote    string `json:"remote"`
			WouldCopy []struct {
				Path string `json:"path"`
			} `json:"would_copy"`
			Copied []struct{} `json:"copied"`
		} `json:"remotes"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("parsing --json output: %v\n%s", err, out)
	}
	if !report.DryRun {
		t.Errorf("expected dry_run true")
	}
	if len(report.Remotes) != 1 || len(report.Remotes[0].WouldCopy) != 1 {
		t.Fatalf("expected one would_copy item, got: %+v", report.Remotes)
	}

	engine, err := newCompletenessEngine(nil)
	if err != nil {
		t.Fatalf("newCompletenessEngine: %v", err)
	}
	check, err := runCompletenessCheck([]string{"main"}, engine)
	if err != nil {
		t.Fatalf("runCompletenessCheck: %v", err)
	}
	if len(check.Items) != 1 || check.Items[0].State != "fixable" {
		t.Fatalf("expected a.bin to still be fixable after dry-run (nothing uploaded), got: %+v", check.Items)
	}
}

// TestSyncAll_ReportsStillLost verifies that content missing everywhere
// (not here, not on any remote) is reported as lost after --all.
func TestSyncAll_ReportsStillLost(t *testing.T) {
	repoDir, _, restore := newAnnexRepo(t, "r1")
	defer restore()

	addAnnexFile(t, repoDir, "a.bin", "version-1")
	annexRun(t, repoDir, "git", "tag", "v1")

	if err := os.Remove(repoDir + "/a.bin"); err != nil {
		t.Fatalf("remove a.bin: %v", err)
	}
	if err := os.WriteFile(repoDir+"/a.bin", []byte("version-2"), 0o644); err != nil {
		t.Fatalf("overwrite a.bin: %v", err)
	}
	annexRun(t, repoDir, "git", "annex", "add", "a.bin")
	annexRun(t, repoDir, "git", "commit", "-m", "update a.bin")

	out := annexRun(t, repoDir, "git", "annex", "find", "--branch=v1", "--json")
	var found struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal([]byte(out), &found); err != nil {
		t.Fatalf("parsing find output %q: %v", out, err)
	}
	annexRun(t, repoDir, "git", "annex", "drop", "--force", "--key", found.Key)

	var code int
	jsonOut := captureStdout(t, func() {
		code = runAllCommand(nil, false, true, "1")
	})
	if code != exitCheckLost {
		t.Fatalf("expected exit %d (lost content remains), got %d; output:\n%s", exitCheckLost, code, jsonOut)
	}

	var report struct {
		Lost []struct {
			Refs  []string `json:"refs"`
			State string   `json:"state"`
		} `json:"lost"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &report); err != nil {
		t.Fatalf("parsing --json output: %v\n%s", err, jsonOut)
	}
	if len(report.Lost) != 1 || len(report.Lost[0].Refs) != 1 || report.Lost[0].Refs[0] != "v1" {
		t.Fatalf("expected one lost item for v1, got: %+v", report.Lost)
	}
}

// TestSyncAll_ReuploadsAfterRemoteMove is the exo-cli#166 end-to-end
// regression: a remote's underlying storage moves without a UUID change
// (stood in for by wiping the directory special remote's backing directory
// and invalidating presence via `git annex fsck --from <remote> --fast`,
// the same mechanism exo-cli#166's fix applies after an `exo init --remote`
// s3url change). `exo sync --all` must re-upload the content, not trust a
// stale "already there" log entry for content the remote's storage move
// left behind.
func TestSyncAll_ReuploadsAfterRemoteMove(t *testing.T) {
	repoDir, remoteDir, restore := newAnnexRepo(t, "r1")
	defer restore()

	addAnnexFile(t, repoDir, "a.bin", "version-1")
	annexRun(t, repoDir, "git", "annex", "copy", "--to", "r1", "a.bin")

	// Simulate the remote's storage moving: wipe the backing directory
	// without touching git-annex's location log.
	chmodRWritable(remoteDir)
	if err := filepath.WalkDir(remoteDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		return os.Remove(path)
	}); err != nil {
		t.Fatalf("clearing remote storage: %v", err)
	}

	// Apply exo-cli#166's fix: invalidate stale presence after the move.
	// fsck exits non-zero when it fixes a stale location-log entry (that's
	// the expected, successful outcome here, not a test failure).
	_ = runCommand([]string{"git", "annex", "fsck", "--from", "r1", "--fast"})

	engine, err := newCompletenessEngine(nil)
	if err != nil {
		t.Fatalf("newCompletenessEngine: %v", err)
	}
	preReport, err := runCompletenessCheck([]string{"main"}, engine)
	if err != nil {
		t.Fatalf("runCompletenessCheck: %v", err)
	}
	if len(preReport.Items) != 1 || preReport.Items[0].State != "fixable" {
		t.Fatalf("expected a.bin to be fixable after invalidation, got: %+v", preReport.Items)
	}

	// --all must now re-upload the content to the (new) location.
	code := runAllCommand(nil, false, false, "1")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	postReport, err := runCompletenessCheck([]string{"main"}, engine)
	if err != nil {
		t.Fatalf("runCompletenessCheck: %v", err)
	}
	if len(postReport.Items) != 0 {
		t.Fatalf("expected a.bin to be clean after --all re-uploads it, got: %+v", postReport.Items)
	}
	if entries, err := os.ReadDir(remoteDir); err != nil || len(entries) == 0 {
		t.Fatalf("expected --all to re-upload content into remote storage, entries=%v err=%v", entries, err)
	}
}
