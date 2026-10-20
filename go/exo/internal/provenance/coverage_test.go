package provenance

import (
	"fmt"
	"testing"
	"time"
)

func writeConfig(t *testing.T, root, yaml string) {
	t.Helper()
	writeRepoFile(t, root, ".exohub/config", yaml)
}

func entityRecord(entityID, prop, value string) string {
	return minimalRecordJSON(NewRecordID(), "recorded", "urn:exohub:agent:person:alice",
		fmt.Sprintf(`{"@id": %q, "@type": "Entity", %q: %q}`, entityID, prop, value))
}

func TestCoverage_DirectlyCoveredFile(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "results/out.txt", "data")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	contentID, err := NewWorktreeReader(root).ResolveContentID("results/out.txt")
	if err != nil {
		t.Fatalf("ResolveContentID() error = %v", err)
	}
	writeRepoFile(t, root, RecordDir+"/r1.jsonld", entityRecord(contentID, "wasGeneratedBy", NewActivityID()))
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record")

	report, err := Validate(NewWorktreeReader(root))
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if report.HasErrors() {
		t.Fatalf("HasErrors() = true: %+v", report.Findings)
	}
	if report.Coverage == nil || report.Coverage.Covered != 1 || report.Coverage.Uncovered != 0 {
		t.Errorf("Coverage = %+v, want 1 covered, 0 uncovered", report.Coverage)
	}
}

// TestCoverage_UsedOnlyFileIsUncovered covers the "used-only files are
// uncovered" done-when case: a file referenced only as an activity's
// "used" input, never generated/derived/attributed/revised, does not
// count as covered.
func TestCoverage_UsedOnlyFileIsUncovered(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "data/raw.csv", "a,b,c")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	contentID, err := NewWorktreeReader(root).ResolveContentID("data/raw.csv")
	if err != nil {
		t.Fatalf("ResolveContentID() error = %v", err)
	}
	activityID := NewActivityID()
	writeRepoFile(t, root, RecordDir+"/r1.jsonld", minimalRecordJSON(NewRecordID(), "recorded", "urn:exohub:agent:person:alice",
		fmt.Sprintf(`{"@id": %q, "@type": "Activity", "used": [%q]}`, activityID, contentID)))
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record")

	report, err := Validate(NewWorktreeReader(root))
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if report.HasErrors() {
		t.Fatalf("HasErrors() = true: %+v", report.Findings)
	}
	if report.Coverage == nil || report.Coverage.Covered != 0 || report.Coverage.Uncovered != 1 {
		t.Errorf("Coverage = %+v, want 0 covered, 1 uncovered", report.Coverage)
	}
}

// TestCoverage_FolderMemberChangeGivesNewEntity covers the "a member
// change gives a new folder entity" done-when case: a record covering a
// folder's tree id stops covering it once a member changes and the tree id
// changes.
func TestCoverage_FolderMemberChangeGivesNewEntity(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "data/expr.tdb/fragment1", "f1")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	treeID, err := NewWorktreeReader(root).ResolveContentID("data/expr.tdb/")
	if err != nil {
		t.Fatalf("ResolveContentID() error = %v", err)
	}
	writeRepoFile(t, root, RecordDir+"/r1.jsonld", entityRecord(treeID, "wasGeneratedBy", NewActivityID()))
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record")

	report, err := Validate(NewWorktreeReader(root))
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if report.Coverage == nil || report.Coverage.Covered != 1 || report.Coverage.Uncovered != 0 {
		t.Errorf("before member change: Coverage = %+v, want 1 covered, 0 uncovered", report.Coverage)
	}

	writeRepoFile(t, root, "data/expr.tdb/fragment2", "f2")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "new fragment")

	newTreeID, err := NewWorktreeReader(root).ResolveContentID("data/expr.tdb/")
	if err != nil {
		t.Fatalf("ResolveContentID() error = %v", err)
	}
	if newTreeID == treeID {
		t.Fatal("expected a new folder tree id after a member change")
	}

	report, err = Validate(NewWorktreeReader(root))
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if report.Coverage == nil || report.Coverage.Covered != 0 || report.Coverage.Uncovered != 2 {
		t.Errorf("after member change: Coverage = %+v, want 0 covered, 2 uncovered", report.Coverage)
	}
}

// TestCoverage_DeclaredCollectionUnit covers the "declared units" done-when
// case: files under a declared provenance.collections folder are covered
// only through the folder.
func TestCoverage_DeclaredCollectionUnit(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeConfig(t, root, "provenance:\n  collections:\n    - \"**/*.tdb/\"\n")
	writeRepoFile(t, root, "data/expr.tdb/fragments/f1", "f1")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	treeID, err := NewWorktreeReader(root).ResolveContentID("data/expr.tdb/")
	if err != nil {
		t.Fatalf("ResolveContentID() error = %v", err)
	}
	writeRepoFile(t, root, RecordDir+"/r1.jsonld", entityRecord(treeID, "wasGeneratedBy", NewActivityID()))
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record")

	report, err := Validate(NewWorktreeReader(root))
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if report.HasErrors() {
		t.Fatalf("HasErrors() = true: %+v", report.Findings)
	}
	// data/expr.tdb/fragments/f1 is covered through the declared folder.
	if report.Coverage == nil || report.Coverage.Covered != 1 || report.Coverage.Uncovered != 0 {
		t.Errorf("Coverage = %+v, want 1 covered, 0 uncovered", report.Coverage)
	}

	// A direct record about the member itself does not count: it is
	// covered only through the folder.
	memberID, err := NewWorktreeReader(root).ResolveContentID("data/expr.tdb/fragments/f1")
	if err != nil {
		t.Fatalf("ResolveContentID() error = %v", err)
	}
	writeRepoFile(t, root, RecordDir+"/r2.jsonld", entityRecord(memberID, "wasGeneratedBy", NewActivityID()))
	run("rm", "-rf", RecordDir+"/r1.jsonld")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "member-only record")

	report, err = Validate(NewWorktreeReader(root))
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if report.Coverage == nil || report.Coverage.Covered != 0 || report.Coverage.Uncovered != 1 {
		t.Errorf("Coverage = %+v, want 0 covered, 1 uncovered (member-only record doesn't cover the declared folder)", report.Coverage)
	}
}

// TestCoverage_RequireWithoutAnyRecords covers the "require without any
// records" done-when case.
func TestCoverage_RequireWithoutAnyRecords(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeConfig(t, root, "provenance:\n  require:\n    - \"results/**\"\n")
	writeRepoFile(t, root, "results/out.txt", "data")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	report, err := Validate(NewWorktreeReader(root))
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if !report.HasErrors() {
		t.Fatal("HasErrors() = false, want true")
	}
	if !hasFinding(report, "required-provenance", "results/out.txt") {
		t.Errorf("findings = %+v, want a required-provenance finding for results/out.txt", report.Findings)
	}
}

// TestCoverage_StagedOlderUncoveredFilesDoNotBlock covers "older uncovered
// files do not block --staged": adding a require rule does not fail
// --staged for pre-existing uncovered files, only full validate does.
func TestCoverage_StagedOlderUncoveredFilesDoNotBlock(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "results/old.txt", "old")
	writeRepoFile(t, root, "README.md", "hi")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	writeConfig(t, root, "provenance:\n  require:\n    - \"results/**\"\n")
	writeRepoFile(t, root, "README.md", "updated")
	run("git", "add", ".")

	staged, err := Validate(NewStagedReader(root))
	if err != nil {
		t.Fatalf("Validate(staged) error = %v", err)
	}
	if staged.HasErrors() {
		t.Errorf("staged HasErrors() = true, want false: %+v", staged.Findings)
	}

	worktree, err := Validate(NewWorktreeReader(root))
	if err != nil {
		t.Fatalf("Validate(worktree) error = %v", err)
	}
	if !worktree.HasErrors() || !hasFinding(worktree, "required-provenance", "results/old.txt") {
		t.Errorf("worktree findings = %+v, want a required-provenance finding for results/old.txt", worktree.Findings)
	}
}

// TestCoverage_StagedRenameDoesNotEscapeRequire covers the review fix for
// stagedAddedOrModified: staging an uncovered file's move into a
// provenance.require path must still be reported, rather than escaping
// the check as a rename (review: "moving an uncovered file into results/**
// shows as R and escapes the staged require check").
func TestCoverage_StagedRenameDoesNotEscapeRequire(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeConfig(t, root, "provenance:\n  require:\n    - \"results/**\"\n")
	writeRepoFile(t, root, "scratch/out.txt", "some fairly long content so git does not treat this as a trivial rename")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	run("mkdir", "-p", "results")
	run("git", "mv", "scratch/out.txt", "results/out.txt")
	run("git", "add", "-A")

	report, err := Validate(NewStagedReader(root))
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if !report.HasErrors() || !hasFinding(report, "required-provenance", "results/out.txt") {
		t.Errorf("findings = %+v, want a required-provenance finding for results/out.txt (a staged rename into a required path must not escape the check)", report.Findings)
	}
}

// TestCoverage_LargeRepoIsFast covers the review's performance fix:
// coverage must resolve content identifiers and folder tree ids in a
// constant number of git invocations, not one or more per tracked file.
// Before the fix, validating a 5,000-file repository with one record took
// about 26s (review measurement); this asserts a generous bound that the
// old per-file implementation could not meet.
func TestCoverage_LargeRepoIsFast(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large-repo timing test in -short mode")
	}
	root, run := stateTestRepo(t, false)
	writeConfig(t, root, "provenance:\n  require:\n    - \"data/**\"\n")

	const fileCount = 3000
	for i := 0; i < fileCount; i++ {
		writeRepoFile(t, root, fmt.Sprintf("data/file%d.txt", i), fmt.Sprintf("content-%d", i))
	}
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	start := time.Now()
	report, err := Validate(NewWorktreeReader(root))
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if report.Coverage == nil || report.Coverage.Uncovered != fileCount {
		t.Fatalf("Coverage = %+v, want %d uncovered", report.Coverage, fileCount)
	}
	// Generous bound: a per-file implementation (2-3 git processes per
	// file) takes tens of seconds at this file count; the batch
	// implementation (a constant number of git processes total) takes well
	// under a second even on a loaded CI machine.
	const bound = 10 * time.Second
	if elapsed > bound {
		t.Errorf("Validate() took %s for %d files, want under %s", elapsed, fileCount, bound)
	}
	t.Logf("Validate() on %d files took %s", fileCount, elapsed)
}

// TestCoverage_AnnexedFileIsCovered exercises the batch content-id path
// (Reader.AllContentIDs) against a real git-annex symlink, so the
// performance fix does not change which files annex coverage recognizes
// (review: "Annex keys: ... apply the same /annex/objects/ rules as
// ResolveContentID").
func TestCoverage_AnnexedFileIsCovered(t *testing.T) {
	root, run := stateTestRepo(t, true)
	writeRepoFile(t, root, "data/big.bin", "x")
	run("git", "annex", "add", "data/big.bin")
	run("git", "commit", "-q", "-m", "init")

	contentID, err := NewWorktreeReader(root).ResolveContentID("data/big.bin")
	if err != nil {
		t.Fatalf("ResolveContentID() error = %v", err)
	}
	writeRepoFile(t, root, RecordDir+"/r1.jsonld", entityRecord(contentID, "wasGeneratedBy", NewActivityID()))
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record")

	report, err := Validate(NewWorktreeReader(root))
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if report.HasErrors() {
		t.Fatalf("HasErrors() = true: %+v", report.Findings)
	}
	if report.Coverage == nil || report.Coverage.Covered != 1 || report.Coverage.Uncovered != 0 {
		t.Errorf("Coverage = %+v, want 1 covered, 0 uncovered", report.Coverage)
	}
}

// TestCoverage_StagedRecordAndDataTogether covers "staged checks, including
// records and data in the same commit": a new output and a record covering
// it, both staged but uncommitted, satisfy provenance.require.
func TestCoverage_StagedRecordAndDataTogether(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeConfig(t, root, "provenance:\n  require:\n    - \"results/**\"\n")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	writeRepoFile(t, root, "results/de.parquet", "data")
	run("git", "add", "results/de.parquet")

	contentID, err := NewStagedReader(root).ResolveContentID("results/de.parquet")
	if err != nil {
		t.Fatalf("ResolveContentID() error = %v", err)
	}
	writeRepoFile(t, root, RecordDir+"/r1.jsonld", entityRecord(contentID, "wasGeneratedBy", NewActivityID()))
	run("git", "add", RecordDir+"/r1.jsonld")

	report, err := Validate(NewStagedReader(root))
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if report.HasErrors() {
		t.Errorf("HasErrors() = true, want false: %+v", report.Findings)
	}
}
