package provenance

import (
	"fmt"
	"strings"
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

// TestCoverage_NamedCollection covers coverage through a named collection
// (add-explicit-collections design D6): a record about the collection's id
// covers every current member.
func TestCoverage_NamedCollection(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "cohort_a/sample1.bam", "s1")
	writeRepoFile(t, root, "cohort_a/sample2.bam", "s2")
	writeCollectionManifest(t, root, "cohort-a-bams", `{"paths": ["cohort_a/sample1.bam", "cohort_a/sample2.bam"]}`)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	id, _, _, _, err := ResolveCollection(NewWorktreeReader(root), "cohort-a-bams", false)
	if err != nil {
		t.Fatalf("ResolveCollection() error = %v", err)
	}
	writeRepoFile(t, root, RecordDir+"/r1.jsonld", entityRecord(id, "wasGeneratedBy", NewActivityID()))
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record")

	report, err := Validate(NewWorktreeReader(root))
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if report.HasErrors() {
		t.Fatalf("HasErrors() = true: %+v", report.Findings)
	}
	// cohort_a/sample1.bam, cohort_a/sample2.bam, and the manifest itself
	// (.exohub/collections/cohort-a-bams.json, a protected path) are
	// tracked: only the two bam files count toward coverage.
	if report.Coverage == nil || report.Coverage.Covered != 2 || report.Coverage.Uncovered != 0 {
		t.Errorf("Coverage = %+v, want 2 covered, 0 uncovered", report.Coverage)
	}
}

// TestCoverage_NamedCollectionStaleness covers staleness after a membership
// change: the named collection's id changes, so the old record no longer
// covers the new member set (design D6, "covered only while the current id
// matches").
func TestCoverage_NamedCollectionStaleness(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "cohort_a/sample1.bam", "s1")
	writeCollectionManifest(t, root, "cohort-a-bams", `{"paths": ["cohort_a/sample1.bam"]}`)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	id, _, _, _, err := ResolveCollection(NewWorktreeReader(root), "cohort-a-bams", false)
	if err != nil {
		t.Fatalf("ResolveCollection() error = %v", err)
	}
	writeRepoFile(t, root, RecordDir+"/r1.jsonld", entityRecord(id, "wasGeneratedBy", NewActivityID()))
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record")

	report, err := Validate(NewWorktreeReader(root))
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if report.Coverage == nil || report.Coverage.Covered != 1 || report.Coverage.Uncovered != 0 {
		t.Errorf("before membership change: Coverage = %+v, want 1 covered, 0 uncovered", report.Coverage)
	}

	writeRepoFile(t, root, "cohort_a/sample2.bam", "s2")
	writeCollectionManifest(t, root, "cohort-a-bams", `{"paths": ["cohort_a/sample1.bam", "cohort_a/sample2.bam"]}`)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "add sample2")

	report, err = Validate(NewWorktreeReader(root))
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if report.Coverage == nil || report.Coverage.Covered != 0 || report.Coverage.Uncovered != 2 {
		t.Errorf("after membership change: Coverage = %+v, want 0 covered, 2 uncovered", report.Coverage)
	}
}

// TestCoverage_OverlapFolderAndNamedCollection covers overlap between a
// folder collection and a named collection (design D7): a folder member of
// a named collection is still covered through the named collection even
// when the folder itself has no record of its own.
func TestCoverage_OverlapFolderAndNamedCollection(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "data/expr.tdb/fragment1", "f1")
	writeRepoFile(t, root, "samples/manifest.tsv", "a\tb\n")
	writeCollectionManifest(t, root, "mixed", `{"paths": ["data/expr.tdb/", "samples/manifest.tsv"]}`)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	id, _, _, _, err := ResolveCollection(NewWorktreeReader(root), "mixed", false)
	if err != nil {
		t.Fatalf("ResolveCollection() error = %v", err)
	}
	writeRepoFile(t, root, RecordDir+"/r1.jsonld", entityRecord(id, "wasGeneratedBy", NewActivityID()))
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record")

	report, err := Validate(NewWorktreeReader(root))
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if report.HasErrors() {
		t.Fatalf("HasErrors() = true: %+v", report.Findings)
	}
	// data/expr.tdb/fragment1 (through the folder member) and
	// samples/manifest.tsv are both covered.
	if report.Coverage == nil || report.Coverage.Covered != 2 || report.Coverage.Uncovered != 0 {
		t.Errorf("Coverage = %+v, want 2 covered, 0 uncovered", report.Coverage)
	}
}

// TestCoverage_OverlapTwoNamedCollections covers overlap between two named
// collections (design D7, "a file in several named collections: allowed.
// Coverage is a union"): a file covered only through the second of two
// collections it belongs to is still covered.
func TestCoverage_OverlapTwoNamedCollections(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "cohort_a/sample1.bam", "s1")
	writeCollectionManifest(t, root, "cohort-a", `{"paths": ["cohort_a/sample1.bam"]}`)
	writeCollectionManifest(t, root, "release-2026", `{"paths": ["cohort_a/sample1.bam"]}`)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	// Only the second collection gets a record.
	releaseID, _, _, _, err := ResolveCollection(NewWorktreeReader(root), "release-2026", false)
	if err != nil {
		t.Fatalf("ResolveCollection() error = %v", err)
	}
	writeRepoFile(t, root, RecordDir+"/r1.jsonld", entityRecord(releaseID, "wasGeneratedBy", NewActivityID()))
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

// TestCoverage_NamedCollectionCycleIsAnError covers the "cycle validation
// error" done-when case: a cyclic named collection declaration makes
// Validate fail rather than silently skip coverage for it.
func TestCoverage_NamedCollectionCycleIsAnError(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeConfig(t, root, "provenance:\n  require:\n    - \"**\"\n")
	writeCollectionManifest(t, root, "a", `{"paths": ["collection:b"]}`)
	writeCollectionManifest(t, root, "b", `{"paths": ["collection:a"]}`)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	if _, err := Validate(NewWorktreeReader(root)); err == nil {
		t.Fatal("expected Validate() to return an error for a collection cycle")
	} else if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("error = %v, want it to mention a cycle", err)
	}
}

// TestCoverage_RequireCoveredThroughNamedCollection covers
// provenance.require being satisfied through a named collection, unioned
// with direct and folder coverage (design D6).
func TestCoverage_RequireCoveredThroughNamedCollection(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeConfig(t, root, "provenance:\n  require:\n    - \"cohort_a/**\"\n")
	writeRepoFile(t, root, "cohort_a/sample1.bam", "s1")
	writeCollectionManifest(t, root, "cohort-a-bams", `{"paths": ["cohort_a/sample1.bam"]}`)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	id, _, _, _, err := ResolveCollection(NewWorktreeReader(root), "cohort-a-bams", false)
	if err != nil {
		t.Fatalf("ResolveCollection() error = %v", err)
	}
	writeRepoFile(t, root, RecordDir+"/r1.jsonld", entityRecord(id, "wasGeneratedBy", NewActivityID()))
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record")

	report, err := Validate(NewWorktreeReader(root))
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if report.HasErrors() {
		t.Fatalf("HasErrors() = true, want satisfied provenance.require: %+v", report.Findings)
	}
}
