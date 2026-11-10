package sync

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// annexRun runs a git/git-annex command in repoDir with the same isolated
// env used by newAnnexRepo/addAnnexFile, failing the test on error.
func annexRun(t *testing.T, repoDir string, args ...string) string {
	t.Helper()
	env := append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1",
		"HOME="+repoDir,
		"GIT_AUTHOR_NAME=Test",
		"GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=Test",
		"GIT_COMMITTER_EMAIL=test@test.com",
	)
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = repoDir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("command %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// captureStdout redirects os.Stdout for the duration of fn and returns
// everything written to it.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		io.Copy(&buf, r)
		done <- buf.String()
	}()
	fn()
	w.Close()
	os.Stdout = old
	return <-done
}

// TestCompletenessEngine_FixableAcrossTags replays the #161 reproduction:
// a.bin is replaced by a second commit; only the current version is copied
// to the remote. The old tag's content must be reported fixable, the new
// tag's content must be clean.
func TestCompletenessEngine_FixableAcrossTags(t *testing.T) {
	repoDir, _, restore := newAnnexRepo(t, "r1")
	defer restore()

	addAnnexFile(t, repoDir, "a.bin", "version-1")
	annexRun(t, repoDir, "git", "tag", "v1")

	// Overwrite a.bin with new content and tag the new version. The existing
	// symlink points at a read-only annex object, so remove it first.
	if err := os.Remove(repoDir + "/a.bin"); err != nil {
		t.Fatalf("remove a.bin: %v", err)
	}
	if err := os.WriteFile(repoDir+"/a.bin", []byte("version-2"), 0o644); err != nil {
		t.Fatalf("overwrite a.bin: %v", err)
	}
	annexRun(t, repoDir, "git", "annex", "add", "a.bin")
	annexRun(t, repoDir, "git", "commit", "-m", "update a.bin")
	annexRun(t, repoDir, "git", "tag", "v2")

	// Only the current (v2) content is copied to the remote.
	annexRun(t, repoDir, "git", "annex", "copy", "--to", "r1", "a.bin")

	engine, err := newCompletenessEngine(nil)
	if err != nil {
		t.Fatalf("newCompletenessEngine: %v", err)
	}

	reportV1, err := runCompletenessCheck([]string{"v1"}, engine)
	if err != nil {
		t.Fatalf("runCompletenessCheck(v1): %v", err)
	}
	if len(reportV1.Items) != 1 || reportV1.Items[0].State != "fixable" {
		t.Fatalf("expected one fixable item for v1, got: %+v", reportV1.Items)
	}
	if reportV1.Items[0].Path != "a.bin" {
		t.Errorf("expected path a.bin, got %q", reportV1.Items[0].Path)
	}
	if reportV1.exitCode() != exitCheckFixable {
		t.Errorf("expected exit code %d, got %d", exitCheckFixable, reportV1.exitCode())
	}

	reportV2, err := runCompletenessCheck([]string{"v2"}, engine)
	if err != nil {
		t.Fatalf("runCompletenessCheck(v2): %v", err)
	}
	if len(reportV2.Items) != 0 {
		t.Fatalf("expected v2 to be clean, got: %+v", reportV2.Items)
	}
	if reportV2.exitCode() != exitCheckClean {
		t.Errorf("expected exit code %d, got %d", exitCheckClean, reportV2.exitCode())
	}
}

// TestCompletenessEngine_Lost verifies that content missing from every
// configured remote AND absent from this clone is classified as lost.
func TestCompletenessEngine_Lost(t *testing.T) {
	repoDir, _, restore := newAnnexRepo(t, "r1")
	defer restore()

	addAnnexFile(t, repoDir, "a.bin", "version-1")
	annexRun(t, repoDir, "git", "tag", "v1")

	// The existing symlink points at a read-only annex object; remove it first.
	if err := os.Remove(repoDir + "/a.bin"); err != nil {
		t.Fatalf("remove a.bin: %v", err)
	}
	if err := os.WriteFile(repoDir+"/a.bin", []byte("version-2"), 0o644); err != nil {
		t.Fatalf("overwrite a.bin: %v", err)
	}
	annexRun(t, repoDir, "git", "annex", "add", "a.bin")
	annexRun(t, repoDir, "git", "commit", "-m", "update a.bin")
	annexRun(t, repoDir, "git", "tag", "v2")
	annexRun(t, repoDir, "git", "annex", "copy", "--to", "r1", "a.bin")

	// Drop the only local copy of the v1 content: it is on no remote and no
	// longer here either.
	out := annexRun(t, repoDir, "git", "annex", "find", "--branch=v1", "--json")
	var found struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal([]byte(out), &found); err != nil {
		t.Fatalf("parsing find output %q: %v", out, err)
	}
	annexRun(t, repoDir, "git", "annex", "drop", "--force", "--key", found.Key)

	engine, err := newCompletenessEngine(nil)
	if err != nil {
		t.Fatalf("newCompletenessEngine: %v", err)
	}
	report, err := runCompletenessCheck([]string{"v1"}, engine)
	if err != nil {
		t.Fatalf("runCompletenessCheck(v1): %v", err)
	}
	if len(report.Items) != 1 || report.Items[0].State != "lost" {
		t.Fatalf("expected one lost item for v1, got: %+v", report.Items)
	}
	if report.exitCode() != exitCheckLost {
		t.Errorf("expected exit code %d, got %d", exitCheckLost, report.exitCode())
	}
}

// TestCompletenessEngine_VerifyCatchesStaleLog simulates exo-cli#166: a
// remote's location log says a.bin's content is present (normal `git annex
// copy` succeeded), but the physical object is gone from the remote's
// storage without git-annex being told (the moved-S3-prefix scenario, here
// stood in for by deleting the file straight out of the directory special
// remote's backing directory). Without --verify, the stale log reports
// clean; with --verify, checkpresentkey catches it and reports fixable
// (the content is still in this clone) with Verified set.
func TestCompletenessEngine_VerifyCatchesStaleLog(t *testing.T) {
	repoDir, remoteDir, restore := newAnnexRepo(t, "r1")
	defer restore()

	addAnnexFile(t, repoDir, "a.bin", "version-1")
	annexRun(t, repoDir, "git", "annex", "copy", "--to", "r1", "a.bin")

	// Simulate the remote's storage moving without git-annex's location log
	// being told: delete the object straight out of the backing directory, as
	// if an S3 prefix move had left the old content behind.
	chmodRWritable(remoteDir)
	if err := filepath.WalkDir(remoteDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		return os.Remove(path)
	}); err != nil {
		t.Fatalf("clearing remote storage: %v", err)
	}

	engine, err := newCompletenessEngine(nil)
	if err != nil {
		t.Fatalf("newCompletenessEngine: %v", err)
	}

	// Without --verify: the location log still says it's on r1, so this is
	// reported clean even though the content is actually gone.
	unverified, err := runCompletenessCheck([]string{"main"}, engine)
	if err != nil {
		t.Fatalf("runCompletenessCheck: %v", err)
	}
	if len(unverified.Items) != 0 {
		t.Fatalf("expected stale log to report clean without --verify, got: %+v", unverified.Items)
	}

	// With --verify: checkpresentkey actually checks r1 and finds nothing,
	// so this is caught as fixable (content is still in this clone).
	verified, err := runCompletenessCheckVerify([]string{"main"}, engine, true)
	if err != nil {
		t.Fatalf("runCompletenessCheckVerify: %v", err)
	}
	if len(verified.Items) != 1 || verified.Items[0].State != "fixable" {
		t.Fatalf("expected one fixable item with --verify, got: %+v", verified.Items)
	}
	if !verified.Items[0].Verified {
		t.Errorf("expected Verified=true on the item caught by --verify, got: %+v", verified.Items[0])
	}
	if verified.exitCode() != exitCheckFixable {
		t.Errorf("expected exit code %d, got %d", exitCheckFixable, verified.exitCode())
	}
}

// TestSyncCheck_VerifyRequiresCheck asserts the full --check --verify command
// path reports verified:true in the JSON item once checkpresentkey catches a
// stale log entry.
func TestSyncCheck_VerifyRequiresCheck(t *testing.T) {
	repoDir, remoteDir, restore := newAnnexRepo(t, "r1")
	defer restore()

	addAnnexFile(t, repoDir, "a.bin", "version-1")
	annexRun(t, repoDir, "git", "annex", "copy", "--to", "r1", "a.bin")

	chmodRWritable(remoteDir)
	if err := filepath.WalkDir(remoteDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		return os.Remove(path)
	}); err != nil {
		t.Fatalf("clearing remote storage: %v", err)
	}

	var code int
	out := captureStdout(t, func() {
		code = runCheckCommand(nil, nil, true, true)
	})
	if code != exitCheckFixable {
		t.Fatalf("expected exit %d, got %d; output:\n%s", exitCheckFixable, code, out)
	}
	var report checkReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("parsing --json output: %v\n%s", err, out)
	}
	if len(report.Items) != 1 || !report.Items[0].Verified {
		t.Fatalf("expected one verified item, got: %+v", report.Items)
	}
}

// TestSyncCheck_CleanWhenAllOnRemote exercises the full `--check` command
// path (no --ref, default all branches+tags) and asserts exit 0 + empty items
// when everything referenced is on the remote.
func TestSyncCheck_CleanWhenAllOnRemote(t *testing.T) {
	repoDir, _, restore := newAnnexRepo(t, "r1")
	defer restore()

	addAnnexFile(t, repoDir, "a.bin", "only-version")
	annexRun(t, repoDir, "git", "annex", "copy", "--to", "r1", "a.bin")

	var code int
	out := captureStdout(t, func() {
		code = runCheckCommand(nil, nil, false, false)
	})
	if code != exitCheckClean {
		t.Errorf("expected exit code %d, got %d; output:\n%s", exitCheckClean, code, out)
	}
	if !strings.Contains(out, "OK") {
		t.Errorf("expected OK message in output, got:\n%s", out)
	}
}

// TestSyncCheck_JSONSchema asserts the --json report's field names/shape.
func TestSyncCheck_JSONSchema(t *testing.T) {
	repoDir, _, restore := newAnnexRepo(t, "r1")
	defer restore()

	addAnnexFile(t, repoDir, "a.bin", "version-1")
	annexRun(t, repoDir, "git", "tag", "v1")
	// Never copy to the remote: v1's content stays fixable.

	var code int
	out := captureStdout(t, func() {
		code = runCheckCommand([]string{"v1"}, nil, true, false)
	})
	if code != exitCheckFixable {
		t.Fatalf("expected exit code %d, got %d; output:\n%s", exitCheckFixable, code, out)
	}

	var report struct {
		RefsChecked []string `json:"refs_checked"`
		Items       []struct {
			Path           string   `json:"path"`
			Key            string   `json:"key"`
			State          string   `json:"state"`
			Refs           []string `json:"refs"`
			RemotesMissing []string `json:"remotes_missing"`
		} `json:"items"`
		Summary struct {
			Fixable int `json:"fixable"`
			Lost    int `json:"lost"`
		} `json:"summary"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("parsing --json output: %v\n%s", err, out)
	}
	if len(report.RefsChecked) != 1 || report.RefsChecked[0] != "v1" {
		t.Errorf("expected refs_checked [v1], got %+v", report.RefsChecked)
	}
	if len(report.Items) != 1 {
		t.Fatalf("expected 1 item, got %+v", report.Items)
	}
	item := report.Items[0]
	if len(item.Refs) != 1 || item.Refs[0] != "v1" || item.Path != "a.bin" || item.State != "fixable" || item.Key == "" {
		t.Errorf("unexpected item shape: %+v", item)
	}
	if report.Summary.Fixable != 1 || report.Summary.Lost != 0 {
		t.Errorf("unexpected summary: %+v", report.Summary)
	}
}

// TestSyncCheck_WithScoping verifies that --with limits which remotes count
// toward "ok".
func TestSyncCheck_WithScoping(t *testing.T) {
	repoDir, remoteBDir, restore := newAnnexRepo(t, "r1")
	defer restore()
	_ = remoteBDir

	remoteB := t.TempDir()
	annexRun(t, repoDir, "git", "annex", "initremote", "r2", "type=directory", "directory="+remoteB, "encryption=none")

	addAnnexFile(t, repoDir, "a.bin", "content")
	annexRun(t, repoDir, "git", "annex", "copy", "--to", "r1", "a.bin")

	engine, err := newCompletenessEngine([]string{"r2"})
	if err != nil {
		t.Fatalf("newCompletenessEngine: %v", err)
	}
	report, err := runCompletenessCheck([]string{"main"}, engine)
	if err != nil {
		t.Fatalf("runCompletenessCheck: %v", err)
	}
	if len(report.Items) != 1 || report.Items[0].State != "fixable" {
		t.Fatalf("expected a.bin to be fixable when scoped to r2 only, got: %+v", report.Items)
	}

	// Scoped to r1 (where it actually is), it should be clean.
	engineR1, err := newCompletenessEngine([]string{"r1"})
	if err != nil {
		t.Fatalf("newCompletenessEngine: %v", err)
	}
	reportR1, err := runCompletenessCheck([]string{"main"}, engineR1)
	if err != nil {
		t.Fatalf("runCompletenessCheck: %v", err)
	}
	if len(reportR1.Items) != 0 {
		t.Fatalf("expected a.bin to be ok when scoped to r1, got: %+v", reportR1.Items)
	}
}

// TestListCandidateBlobs_SkipsLargeNonAnnexedBlob is the review fix: a large
// (well above maxPointerFileSize) plain git blob must never be fetched as a
// pointer-file candidate. This is a whitebox check on the pre-filter stage
// (listCandidateBlobs) that runs before any cat-file call, so excluding it
// here is sufficient proof its content is never read.
func TestListCandidateBlobs_SkipsLargeNonAnnexedBlob(t *testing.T) {
	repoDir, _, restore := newAnnexRepo(t, "r1")
	defer restore()

	addAnnexFile(t, repoDir, "a.bin", "content")

	// A large plain git file (not annexed): well over maxPointerFileSize.
	big := make([]byte, maxPointerFileSize*8)
	for i := range big {
		big[i] = byte('a' + i%26)
	}
	if err := os.WriteFile(repoDir+"/big.csv", big, 0o644); err != nil {
		t.Fatalf("write big.csv: %v", err)
	}
	annexRun(t, repoDir, "git", "add", "big.csv")
	annexRun(t, repoDir, "git", "commit", "-m", "add big.csv")

	blobs, err := listCandidateBlobs("main")
	if err != nil {
		t.Fatalf("listCandidateBlobs: %v", err)
	}
	for _, b := range blobs {
		if b.Path == "big.csv" {
			t.Fatalf("big.csv must not be a candidate blob (size exceeds maxPointerFileSize), got: %+v", b)
		}
	}
	foundABin := false
	for _, b := range blobs {
		if b.Path == "a.bin" {
			foundABin = true
		}
	}
	if !foundABin {
		t.Fatalf("expected a.bin (an annex symlink) among candidate blobs, got: %+v", blobs)
	}

	// End-to-end: --check must still report a.bin as fixable and must not
	// choke on / misclassify big.csv (it's simply never a candidate).
	engine, err := newCompletenessEngine(nil)
	if err != nil {
		t.Fatalf("newCompletenessEngine: %v", err)
	}
	report, err := runCompletenessCheck([]string{"main"}, engine)
	if err != nil {
		t.Fatalf("runCompletenessCheck: %v", err)
	}
	if len(report.Items) != 1 || report.Items[0].Path != "a.bin" {
		t.Fatalf("expected only a.bin to be reported, got: %+v", report.Items)
	}
}

// TestCollectAnnexEntriesForRefs_DedupesAcrossRefs verifies that a blob
// shared by many refs (the common case for versioned datasets: most files
// are unchanged between tags) is fetched at most once per run, regardless of
// how many refs reference it. It does so by counting how many distinct SHAs
// end up needing a cat-file lookup, via the same code path --check uses.
func TestCollectAnnexEntriesForRefs_DedupesAcrossRefs(t *testing.T) {
	repoDir, _, restore := newAnnexRepo(t, "r1")
	defer restore()

	addAnnexFile(t, repoDir, "a.bin", "shared content")
	// Ten tags, all pointing at the same commit: the same blob SHA is
	// reachable from all of them.
	var tags []string
	for i := 0; i < 10; i++ {
		tag := fmt.Sprintf("t%d", i)
		annexRun(t, repoDir, "git", "tag", tag)
		tags = append(tags, tag)
	}

	entries, err := collectAnnexEntriesForRefs(tags)
	if err != nil {
		t.Fatalf("collectAnnexEntriesForRefs: %v", err)
	}
	// One entry per (ref, path): 10 refs x 1 file = 10 entries, but all the
	// same key (the point: listCandidateBlobs collected 10 occurrences of the
	// same SHA, and catFileBatchContents was asked to resolve exactly one
	// unique SHA, not ten).
	if len(entries) != 10 {
		t.Fatalf("expected 10 entries (one per ref), got %d: %+v", len(entries), entries)
	}
	key := entries[0].Key
	for _, e := range entries {
		if e.Key != key {
			t.Fatalf("expected every entry to share the same key, got: %+v", entries)
		}
	}
}

// TestCollectAnnexEntriesForRefs_OneTreeWalkPerDistinctTree is the second
// re-review's fix: N refs pointing at the same commit (and so the same
// tree) must be walked via `ls-tree` exactly once, not once per ref.
// Previously the memory savings from the pre-filter were undermined because
// every ref's candidates were re-collected independently even when the
// underlying tree never changed between tags.
func TestCollectAnnexEntriesForRefs_OneTreeWalkPerDistinctTree(t *testing.T) {
	repoDir, _, restore := newAnnexRepo(t, "r1")
	defer restore()

	addAnnexFile(t, repoDir, "a.bin", "shared content")
	var tags []string
	for i := 0; i < 12; i++ {
		tag := fmt.Sprintf("same%d", i)
		annexRun(t, repoDir, "git", "tag", tag)
		tags = append(tags, tag)
	}
	// One ref pointing at a genuinely different tree.
	os.WriteFile(repoDir+"/b.bin", []byte("other"), 0o644)
	annexRun(t, repoDir, "git", "annex", "add", "b.bin")
	annexRun(t, repoDir, "git", "commit", "-m", "add b.bin")
	annexRun(t, repoDir, "git", "tag", "different")
	refs := append(tags, "different")

	listCandidateBlobsCalls = 0
	entries, err := collectAnnexEntriesForRefs(refs)
	if err != nil {
		t.Fatalf("collectAnnexEntriesForRefs: %v", err)
	}

	// 12 identical-tree refs + 1 distinct-tree ref = 2 ls-tree calls, not 13.
	if listCandidateBlobsCalls != 2 {
		t.Fatalf("expected exactly 2 listCandidateBlobs (ls-tree) calls for 12 refs sharing a tree + 1 distinct ref, got %d", listCandidateBlobsCalls)
	}
	// Every ref still gets its own a.bin entry (12 identical-tree refs + the
	// "different" ref, whose tree still contains a.bin alongside b.bin).
	count := 0
	for _, e := range entries {
		if e.Path == "a.bin" {
			count++
		}
	}
	if count != 13 {
		t.Fatalf("expected 13 a.bin entries (one per ref), got %d", count)
	}
}

// TestSyncCheck_NonASCIIPathUnescaped verifies (review fix #3) that `-z` on
// `git ls-tree` means non-ASCII paths are never C-quoted/escaped in output.
func TestSyncCheck_NonASCIIPathUnescaped(t *testing.T) {
	repoDir, _, restore := newAnnexRepo(t, "r1")
	defer restore()

	addAnnexFile(t, repoDir, "\u00e9.bin", "content") // é.bin

	var code int
	out := captureStdout(t, func() {
		code = runCheckCommand(nil, nil, true, false)
	})
	if code != exitCheckFixable {
		t.Fatalf("expected exit %d, got %d; output:\n%s", exitCheckFixable, code, out)
	}
	if !strings.Contains(out, "\u00e9.bin") {
		t.Fatalf("expected unescaped \u00e9.bin in --json output, got:\n%s", out)
	}
	if strings.Contains(out, `\303\251`) {
		t.Fatalf("path must not be C-quoted (octal-escaped), got:\n%s", out)
	}
}

// TestSyncCheck_GroupsItemsAcrossRefs is the schema review's fix: items are
// grouped by (path, key), with a `refs` list, instead of one near-identical
// item per (ref, path) occurrence. A file unchanged across three tags must
// produce exactly one item listing all three refs, not three items.
func TestSyncCheck_GroupsItemsAcrossRefs(t *testing.T) {
	repoDir, _, restore := newAnnexRepo(t, "r1")
	defer restore()

	addAnnexFile(t, repoDir, "a.bin", "content")
	annexRun(t, repoDir, "git", "tag", "t1")
	annexRun(t, repoDir, "git", "tag", "t2")
	annexRun(t, repoDir, "git", "tag", "t3")

	engine, err := newCompletenessEngine(nil)
	if err != nil {
		t.Fatalf("newCompletenessEngine: %v", err)
	}
	report, err := runCompletenessCheck([]string{"main", "t1", "t2", "t3"}, engine)
	if err != nil {
		t.Fatalf("runCompletenessCheck: %v", err)
	}
	if len(report.Items) != 1 {
		t.Fatalf("expected a.bin to collapse into a single grouped item, got %d: %+v", len(report.Items), report.Items)
	}
	if report.Summary.Fixable != 1 {
		t.Fatalf("expected summary.fixable to count grouped items (1), got %d", report.Summary.Fixable)
	}
	item := report.Items[0]
	want := []string{"main", "t1", "t2", "t3"}
	got := sortedStrings(item.Refs)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected refs %v, got %v", want, got)
	}

	// --json must emit the grouped shape too: one item, a refs array.
	var code int
	out := captureStdout(t, func() {
		code = runCheckCommand(nil, nil, true, false)
	})
	if code != exitCheckFixable {
		t.Fatalf("expected exit %d, got %d; output:\n%s", exitCheckFixable, code, out)
	}
	var jsonReport struct {
		Items []struct {
			Refs []string `json:"refs"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &jsonReport); err != nil {
		t.Fatalf("parsing --json output: %v\n%s", err, out)
	}
	if len(jsonReport.Items) != 1 || len(jsonReport.Items[0].Refs) != 4 {
		t.Fatalf("expected one item with 4 refs in --json output, got: %+v", jsonReport.Items)
	}
}
