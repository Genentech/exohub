package provenance

import (
	"sort"
	"strings"
	"testing"
)

// newExternalProjectRepo creates a throwaway git repository standing in
// for another ExoHub project, publishes "main" (so a pinned
// "exohub:<project>:...@main" reference resolves it, exo-cli#152 review),
// points DiscoverLocalClone(project) at it for the duration of the test
// (design D5: "members" resolves exactly like "exo add exohub:..."), and
// returns its root plus the helper to run further git commands in it.
func newExternalProjectRepo(t *testing.T, project string) (root string, run func(args ...string) string) {
	t.Helper()
	// No reachable catalog: resolution must use the local clone env var
	// below, the same way go/exo/commands/add/exohub_test.go isolates
	// these tests from a real catalog endpoint.
	t.Setenv("EXOHUB_CATALOG_URL", "http://127.0.0.1:0")
	root, run = stateTestRepo(t, false)
	writeRepoFile(t, root, "fastq/sample1.fastq.gz", "fastq-1")
	writeRepoFile(t, root, "fastq/sample2.fastq.gz", "fastq-2")
	writeCollectionManifest(t, root, "raw-fastqs", `{"paths": ["fastq/sample1.fastq.gz", "fastq/sample2.fastq.gz"]}`)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")
	run("git", "branch", "-M", "main")
	run("git", "update-ref", "refs/remotes/origin/main", "refs/heads/main")
	envVar := "EXOHUB_CLONE_" + strings.ToUpper(strings.ReplaceAll(project, "-", "_"))
	t.Setenv(envVar, root)
	return root, run
}

func writeCollectionManifest(t *testing.T, root, name, body string) {
	t.Helper()
	writeRepoFile(t, root, CollectionManifestPath(name), body)
}

// writeCollectionLockFixture writes name's lock file (design D5 amendment
// "a lock file per collection with external members"), the way
// "exo provenance add --generated/--revision-of collection:<name>" would
// stage it, so an offline test can resolve "members" entries
// (refreshLock=false) without reaching the external project at all.
func writeCollectionLockFixture(t *testing.T, root, name string, lock *CollectionLock) {
	t.Helper()
	data, err := MarshalCollectionLock(lock)
	if err != nil {
		t.Fatalf("MarshalCollectionLock() error = %v", err)
	}
	writeRepoFile(t, root, CollectionLockPath(name), string(data))
}

func TestValidCollectionName(t *testing.T) {
	valid := []string{"a", "a1", "cohort-a-bams", "a-b-c9"}
	invalid := []string{"", "A", "-a", "cohort_a", "cohort a", "Cohort-A"}
	for _, name := range valid {
		if !ValidCollectionName(name) {
			t.Errorf("ValidCollectionName(%q) = false, want true", name)
		}
	}
	for _, name := range invalid {
		if ValidCollectionName(name) {
			t.Errorf("ValidCollectionName(%q) = true, want false", name)
		}
	}
}

func TestLoadCollectionManifest_InvalidName(t *testing.T) {
	root, _ := stateTestRepo(t, false)
	if _, err := LoadCollectionManifest(NewWorktreeReader(root), "Not-Valid"); err == nil {
		t.Fatal("expected an error for an invalid collection name")
	}
}

func TestLoadCollectionManifest_Missing(t *testing.T) {
	root, _ := stateTestRepo(t, false)
	if _, err := LoadCollectionManifest(NewWorktreeReader(root), "cohort-a"); err == nil {
		t.Fatal("expected an error for a missing manifest")
	}
}

func TestLoadCollectionManifest_RequiresPathsOrGlobs(t *testing.T) {
	root, _ := stateTestRepo(t, false)
	writeCollectionManifest(t, root, "cohort-a", `{}`)
	if _, err := LoadCollectionManifest(NewWorktreeReader(root), "cohort-a"); err == nil {
		t.Fatal(`expected an error when "paths", "globs" and "members" are all empty`)
	}
}

// TestLoadCollectionManifest_MembersOnly covers design D5 of
// add-exohub-asset-import: a manifest with only "members" (no "paths" or
// "globs") is a valid manifest shape, not rejected by the "at least one
// of" check.
func TestLoadCollectionManifest_MembersOnly(t *testing.T) {
	root, _ := stateTestRepo(t, false)
	writeCollectionManifest(t, root, "cohort-a", `{"members": ["exohub:other-project:data/file.bam@main"]}`)
	m, err := LoadCollectionManifest(NewWorktreeReader(root), "cohort-a")
	if err != nil {
		t.Fatalf("LoadCollectionManifest() error = %v", err)
	}
	if len(m.Members) != 1 || m.Members[0] != "exohub:other-project:data/file.bam@main" {
		t.Errorf("Members = %v", m.Members)
	}
}

// TestLoadCollectionManifest_MembersMustBePinned covers design D5
// "Offline validate checks format and pinning only": an unpinned member
// reference (no "@version") is a format error caught at load time, fully
// offline — exohub.ParseReference never performs network I/O.
func TestLoadCollectionManifest_MembersMustBePinned(t *testing.T) {
	root, _ := stateTestRepo(t, false)
	writeCollectionManifest(t, root, "cohort-a", `{"members": ["exohub:other-project:data/file.bam"]}`)
	_, err := LoadCollectionManifest(NewWorktreeReader(root), "cohort-a")
	if err == nil {
		t.Fatal("expected an error for an unpinned member reference")
	}
}

// TestLoadCollectionManifest_MembersMustParse covers the same offline
// format check for a member that isn't a well-formed exohub: reference at
// all.
func TestLoadCollectionManifest_MembersMustParse(t *testing.T) {
	root, _ := stateTestRepo(t, false)
	writeCollectionManifest(t, root, "cohort-a", `{"members": ["not-a-reference"]}`)
	_, err := LoadCollectionManifest(NewWorktreeReader(root), "cohort-a")
	if err == nil {
		t.Fatal("expected an error for a malformed member reference")
	}
}

func TestResolveCollection_Paths(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "cohort_a/sample1.bam", "s1")
	writeRepoFile(t, root, "cohort_a/sample2.bam", "s2")
	writeCollectionManifest(t, root, "cohort-a-bams", `{"paths": ["cohort_a/sample1.bam", "cohort_a/sample2.bam"]}`)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	reader := NewWorktreeReader(root)
	id, members, dropped, _, err := ResolveCollection(reader, "cohort-a-bams", false)
	if err != nil {
		t.Fatalf("ResolveCollection() error = %v", err)
	}
	if len(dropped) != 0 {
		t.Errorf("dropped = %v, want none", dropped)
	}
	if len(members) != 2 {
		t.Fatalf("members = %+v, want 2", members)
	}
	if !strings.HasPrefix(id, CollectionURNPrefix) {
		t.Errorf("id = %q, want prefix %q", id, CollectionURNPrefix)
	}

	memberIDs := make([]string, len(members))
	for i, m := range members {
		memberIDs[i] = m.ID
	}
	sort.Strings(memberIDs)
	if want := CollectionID(memberIDs); id != want {
		t.Errorf("id = %q, want %q", id, want)
	}
}

func TestResolveCollection_Globs(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "cohort_a/sample1.bam", "s1")
	writeRepoFile(t, root, "cohort_a/sample2.bam", "s2")
	writeRepoFile(t, root, "cohort_a/notes.txt", "notes")
	writeCollectionManifest(t, root, "cohort-a-bams", `{"globs": ["cohort_a/**/*.bam"]}`)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	reader := NewWorktreeReader(root)
	_, members, dropped, _, err := ResolveCollection(reader, "cohort-a-bams", false)
	if err != nil {
		t.Fatalf("ResolveCollection() error = %v", err)
	}
	if len(dropped) != 0 {
		t.Errorf("dropped = %v, want none", dropped)
	}
	if len(members) != 2 {
		t.Fatalf("members = %+v, want 2 (bam files only)", members)
	}
	for _, m := range members {
		if !strings.HasSuffix(m.Path, ".bam") {
			t.Errorf("member path = %q, want a .bam file", m.Path)
		}
	}
}

func TestResolveCollection_FolderMember(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "data/expr.tdb/fragment1", "f1")
	writeRepoFile(t, root, "samples/manifest.tsv", "a\tb\n")
	writeCollectionManifest(t, root, "mixed", `{"paths": ["data/expr.tdb/", "samples/manifest.tsv"]}`)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	reader := NewWorktreeReader(root)
	_, members, dropped, _, err := ResolveCollection(reader, "mixed", false)
	if err != nil {
		t.Fatalf("ResolveCollection() error = %v", err)
	}
	if len(dropped) != 0 {
		t.Errorf("dropped = %v, want none", dropped)
	}
	if len(members) != 2 {
		t.Fatalf("members = %+v, want 2", members)
	}
	var sawTree bool
	for _, m := range members {
		if strings.HasPrefix(m.ID, GitTreeURNPrefix) {
			sawTree = true
		}
	}
	if !sawTree {
		t.Errorf("members = %+v, want one folder (git tree) member", members)
	}
}

func TestResolveCollection_MissingPathIsDropped(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "cohort_a/sample1.bam", "s1")
	writeCollectionManifest(t, root, "cohort-a-bams", `{"paths": ["cohort_a/sample1.bam", "cohort_a/does-not-exist.bam"]}`)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	reader := NewWorktreeReader(root)
	_, members, dropped, _, err := ResolveCollection(reader, "cohort-a-bams", false)
	if err != nil {
		t.Fatalf("ResolveCollection() error = %v", err)
	}
	if len(members) != 1 {
		t.Fatalf("members = %+v, want 1", members)
	}
	if len(dropped) != 1 || dropped[0] != "cohort_a/does-not-exist.bam" {
		t.Errorf("dropped = %v, want [cohort_a/does-not-exist.bam]", dropped)
	}
}

func TestResolveCollection_NestedCollection(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "cohort_a/sample1.bam", "s1")
	writeRepoFile(t, root, "release_subset/extra.vcf.gz", "v1")
	writeCollectionManifest(t, root, "cohort-a-bams", `{"paths": ["cohort_a/sample1.bam"]}`)
	writeCollectionManifest(t, root, "release", `{"paths": ["collection:cohort-a-bams", "release_subset/extra.vcf.gz"]}`)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	reader := NewWorktreeReader(root)
	innerID, _, _, _, err := ResolveCollection(reader, "cohort-a-bams", false)
	if err != nil {
		t.Fatalf("ResolveCollection(inner) error = %v", err)
	}

	outerID, members, dropped, _, err := ResolveCollection(reader, "release", false)
	if err != nil {
		t.Fatalf("ResolveCollection(outer) error = %v", err)
	}
	if len(dropped) != 0 {
		t.Errorf("dropped = %v, want none", dropped)
	}
	if len(members) != 2 {
		t.Fatalf("members = %+v, want 2", members)
	}
	var sawInner bool
	for _, m := range members {
		if m.ID == innerID {
			sawInner = true
			if m.Path != "" {
				t.Errorf("nested collection member Path = %q, want empty", m.Path)
			}
		}
	}
	if !sawInner {
		t.Errorf("members = %+v, want the nested collection's id %q among them", members, innerID)
	}

	// Changing the inner collection's membership changes the outer
	// collection's id too (design D7: "a change inside the nested
	// collection changes the outer collection's id too").
	writeRepoFile(t, root, "cohort_a/sample2.bam", "s2")
	writeCollectionManifest(t, root, "cohort-a-bams", `{"paths": ["cohort_a/sample1.bam", "cohort_a/sample2.bam"]}`)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "add sample2")

	newOuterID, _, _, _, err := ResolveCollection(reader, "release", false)
	if err != nil {
		t.Fatalf("ResolveCollection(outer, after change) error = %v", err)
	}
	if newOuterID == outerID {
		t.Error("expected a new outer collection id after the nested collection's membership changed")
	}
}

func TestResolveCollection_CycleDetected(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeCollectionManifest(t, root, "a", `{"paths": ["collection:b"]}`)
	writeCollectionManifest(t, root, "b", `{"paths": ["collection:a"]}`)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	reader := NewWorktreeReader(root)
	if _, _, _, _, err := ResolveCollection(reader, "a", false); err == nil {
		t.Fatal("expected a cycle error")
	} else if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("error = %v, want it to mention a cycle", err)
	}
}

func TestResolveCollection_SelfReferenceIsACycle(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeCollectionManifest(t, root, "a", `{"paths": ["collection:a"]}`)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	reader := NewWorktreeReader(root)
	if _, _, _, _, err := ResolveCollection(reader, "a", false); err == nil {
		t.Fatal("expected a cycle error")
	}
}

func TestResolveCollection_Deduplicated(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "cohort_a/sample1.bam", "s1")
	writeCollectionManifest(t, root, "cohort-a", `{"paths": ["cohort_a/sample1.bam"], "globs": ["cohort_a/**/*.bam"]}`)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	reader := NewWorktreeReader(root)
	_, members, _, _, err := ResolveCollection(reader, "cohort-a", false)
	if err != nil {
		t.Fatalf("ResolveCollection() error = %v", err)
	}
	if len(members) != 1 {
		t.Fatalf("members = %+v, want 1 (same file listed in paths and matched by globs)", members)
	}
}

func TestListCollectionNames(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeCollectionManifest(t, root, "cohort-a-bams", `{"paths": ["cohort_a/sample1.bam"]}`)
	writeCollectionManifest(t, root, "release-2026", `{"paths": ["cohort_a/sample1.bam"]}`)
	writeRepoFile(t, root, "cohort_a/sample1.bam", "s1")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	names, err := ListCollectionNames(NewWorktreeReader(root))
	if err != nil {
		t.Fatalf("ListCollectionNames() error = %v", err)
	}
	if want := []string{"cohort-a-bams", "release-2026"}; !sort.StringsAreSorted(names) || len(names) != len(want) || names[0] != want[0] || names[1] != want[1] {
		t.Errorf("ListCollectionNames() = %v, want %v", names, want)
	}
}

func TestListCollectionNames_NoCollections(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "data/raw.csv", "a")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	names, err := ListCollectionNames(NewWorktreeReader(root))
	if err != nil {
		t.Fatalf("ListCollectionNames() error = %v", err)
	}
	if len(names) != 0 {
		t.Errorf("ListCollectionNames() = %v, want none", names)
	}
}

// TestResolveCollectionCoverage_NestedFlattensMembers covers coverage
// through a nested named collection (design D6, D7): the outer collection's
// coveredIDs set includes the inner collection's leaf members directly,
// not just the inner collection's own id, so a file nested two collections
// deep is recognized once the outermost collection's record is covered.
func TestResolveCollectionCoverage_NestedFlattensMembers(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "cohort_a/sample1.bam", "s1")
	writeRepoFile(t, root, "release_subset/extra.vcf.gz", "v1")
	writeCollectionManifest(t, root, "cohort-a-bams", `{"paths": ["cohort_a/sample1.bam"]}`)
	writeCollectionManifest(t, root, "release", `{"paths": ["collection:cohort-a-bams", "release_subset/extra.vcf.gz"]}`)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	reader := NewWorktreeReader(root)
	sampleID, err := reader.ResolveContentID("cohort_a/sample1.bam")
	if err != nil {
		t.Fatalf("ResolveContentID() error = %v", err)
	}
	extraID, err := reader.ResolveContentID("release_subset/extra.vcf.gz")
	if err != nil {
		t.Fatalf("ResolveContentID() error = %v", err)
	}

	id, coveredIDs, _, err := ResolveCollectionCoverage(reader, "release")
	if err != nil {
		t.Fatalf("ResolveCollectionCoverage() error = %v", err)
	}
	if !strings.HasPrefix(id, CollectionURNPrefix) {
		t.Errorf("id = %q, want prefix %q", id, CollectionURNPrefix)
	}
	if !coveredIDs[sampleID] {
		t.Errorf("coveredIDs = %v, want the nested collection's leaf member %q", coveredIDs, sampleID)
	}
	if !coveredIDs[extraID] {
		t.Errorf("coveredIDs = %v, want the direct member %q", coveredIDs, extraID)
	}
}

// TestResolveCollection_ExternalAsset covers design D5 of
// add-exohub-asset-import, amended per !294 review to use a lock file: a
// "members" entry naming a single file in another project live-resolves
// (refreshLock=true, what "exo provenance add --generated collection:..."
// does) to that file's git blob id, the same id exo.add.ResolveFile would
// give a locally materialized copy with identical content, and records
// that id in the returned lock update; once the lock is committed,
// resolving again with refreshLock=false reads it back without touching
// the external project at all.
func TestResolveCollection_ExternalAsset(t *testing.T) {
	extRoot, extRun := newExternalProjectRepo(t, "other-project")
	assetID := GitBlobEntityID(mustBlobID(t, extRun, "fastq/sample1.fastq.gz"))
	_ = extRoot
	const memberRef = "exohub:other-project:fastq/sample1.fastq.gz@main"

	root, run := stateTestRepo(t, false)
	writeCollectionManifest(t, root, "imported", `{"members": ["`+memberRef+`"]}`)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	reader := NewWorktreeReader(root)
	wantID := CollectionID([]string{assetID})

	// refreshLock=false with no lock file yet is a clear, actionable error
	// (design D5 amendment review point 1: offline paths never resolve a
	// "members" entry themselves).
	if _, _, _, _, err := ResolveCollection(reader, "imported", false); err == nil {
		t.Fatal("expected a \"no lock entry\" error before the lock exists")
	} else if !strings.Contains(err.Error(), "no lock entry") || !strings.Contains(err.Error(), "exo provenance add --generated collection:imported") {
		t.Errorf("error = %v, want it to name the write command", err)
	}

	id, members, dropped, lockUpdates, err := ResolveCollection(reader, "imported", true)
	if err != nil {
		t.Fatalf("ResolveCollection(refreshLock) error = %v", err)
	}
	if len(dropped) != 0 {
		t.Errorf("dropped = %v, want none", dropped)
	}
	if len(members) != 1 || members[0].ID != assetID {
		t.Fatalf("members = %+v, want one member with ID %q", members, assetID)
	}
	if id != wantID {
		t.Errorf("id = %q, want %q", id, wantID)
	}
	lock, ok := lockUpdates["imported"]
	if !ok {
		t.Fatalf("lockUpdates = %v, want an entry for \"imported\"", lockUpdates)
	}
	entry, ok := lock.Members[memberRef]
	if !ok || entry.ID != assetID || entry.Commit == "" {
		t.Fatalf("lock entry = %+v, ok=%v, want non-empty Commit and ID %q", entry, ok, assetID)
	}

	// Commit the lock and make the external project unreachable: offline
	// resolution must still succeed, reading the committed lock only.
	writeCollectionLockFixture(t, root, "imported", lock)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "lock")
	t.Setenv("EXOHUB_CLONE_OTHER_PROJECT", "")

	id2, members2, dropped2, _, err := ResolveCollection(reader, "imported", false)
	if err != nil {
		t.Fatalf("ResolveCollection(offline, locked) error = %v", err)
	}
	if id2 != wantID || len(dropped2) != 0 || len(members2) != 1 || members2[0].ID != assetID {
		t.Fatalf("offline resolve = id %q members %+v dropped %v, want id %q one member %q none dropped", id2, members2, dropped2, wantID, assetID)
	}
}

// TestResolveCollection_ExternalCollection covers a "members" entry naming
// a collection defined in another project (design D5, amended per !294
// review): refreshLock=true resolves it to that collection's own digest
// (computed the same way the source project would compute it), flattens
// its leaf content ids into coveredIDs so an imported file matching one
// of those ids is recognized as covered, and records CoveredIDs in the
// lock entry so a later offline resolution can reconstruct the same
// digest and coverage without re-resolving.
func TestResolveCollection_ExternalCollection(t *testing.T) {
	_, extRun := newExternalProjectRepo(t, "other-project")
	sample1ID := GitBlobEntityID(mustBlobID(t, extRun, "fastq/sample1.fastq.gz"))
	sample2ID := GitBlobEntityID(mustBlobID(t, extRun, "fastq/sample2.fastq.gz"))
	wantDigest := CollectionID([]string{sample1ID, sample2ID})
	const memberRef = "exohub:other-project:collection:raw-fastqs@main"

	root, run := stateTestRepo(t, false)
	writeCollectionManifest(t, root, "imported", `{"members": ["`+memberRef+`"]}`)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	reader := NewWorktreeReader(root)
	_, _, _, lockUpdates, err := ResolveCollection(reader, "imported", true)
	if err != nil {
		t.Fatalf("ResolveCollection(refreshLock) error = %v", err)
	}
	writeCollectionLockFixture(t, root, "imported", lockUpdates["imported"])
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "lock")
	t.Setenv("EXOHUB_CLONE_OTHER_PROJECT", "")

	id, coveredIDs, external, err := ResolveCollectionCoverage(reader, "imported")
	if err != nil {
		t.Fatalf("ResolveCollectionCoverage() error = %v", err)
	}
	wantID := CollectionID([]string{wantDigest})
	if id != wantID {
		t.Errorf("id = %q, want %q", id, wantID)
	}
	if !coveredIDs[sample1ID] || !coveredIDs[sample2ID] {
		t.Errorf("coveredIDs = %v, want both leaf members covered", coveredIDs)
	}
	if len(external) != 1 {
		t.Fatalf("external = %+v, want exactly one entry", external)
	}
	e := external[0]
	if e.Name != "raw-fastqs" || e.Project != "other-project" || e.Version != "main" || e.ID != wantDigest {
		t.Errorf("external[0] = %+v", e)
	}
	if !e.CoveredIDs[sample1ID] || !e.CoveredIDs[sample2ID] {
		t.Errorf("external[0].CoveredIDs = %v", e.CoveredIDs)
	}
}

// TestResolveCollection_ExternalCollectionNestedAndFolder covers design D5
// amendment review point 3 (exo-cli#154): a "members" entry naming a
// collection in another project whose own manifest nests another
// collection and includes a folder member gets a different id than a flat
// digest over its resolved files -- a flat recompute would silently
// disagree with what the source project itself recorded. The locked id
// must equal the source's own ResolveCollection id instead.
func TestResolveCollection_ExternalCollectionNestedAndFolder(t *testing.T) {
	extRoot, extRun := newExternalProjectRepo(t, "other-project")
	writeRepoFile(t, extRoot, "extra/note.txt", "extra-1")
	writeCollectionManifest(t, extRoot, "cohort-a", `{"paths": ["collection:raw-fastqs", "extra/"]}`)
	extRun("git", "add", ".")
	extRun("git", "commit", "-q", "-m", "add cohort-a")
	extRun("git", "update-ref", "refs/remotes/origin/main", "refs/heads/main")

	// What the source project itself would compute for cohort-a: a nested
	// collection member contributes raw-fastqs' own digest as one member,
	// and the folder member contributes its tree id as one member -- not
	// every individual file flattened together.
	sourceReader := NewRefReader(extRoot, "HEAD")
	wantID, _, _, _, err := ResolveCollection(sourceReader, "cohort-a", false)
	if err != nil {
		t.Fatalf("source ResolveCollection() error = %v", err)
	}
	sample1ID := GitBlobEntityID(mustBlobID(t, extRun, "fastq/sample1.fastq.gz"))
	sample2ID := GitBlobEntityID(mustBlobID(t, extRun, "fastq/sample2.fastq.gz"))
	extraTreeID := GitTreeEntityID(strings.TrimSpace(extRun("git", "rev-parse", "HEAD:extra")))
	flatDigest := CollectionID([]string{sample1ID, sample2ID, GitBlobEntityID(mustBlobID(t, extRun, "extra/note.txt"))})
	if wantID == flatDigest {
		t.Fatal("test setup error: the source's own id must differ from a flat recompute over its resolved files")
	}

	const memberRef = "exohub:other-project:collection:cohort-a@main"
	root, run := stateTestRepo(t, false)
	writeCollectionManifest(t, root, "imported", `{"members": ["`+memberRef+`"]}`)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	reader := NewWorktreeReader(root)
	_, _, _, lockUpdates, err := ResolveCollection(reader, "imported", true)
	if err != nil {
		t.Fatalf("ResolveCollection(refreshLock) error = %v", err)
	}
	entry, ok := lockUpdates["imported"].Members[memberRef]
	if !ok {
		t.Fatalf("lockUpdates[imported].Members = %v, want an entry for %q", lockUpdates["imported"].Members, memberRef)
	}
	if entry.ID != wantID {
		t.Errorf("locked id = %q, want the source's own id %q (not the flat recompute %q)", entry.ID, wantID, flatDigest)
	}
	covered := map[string]bool{}
	for _, id := range entry.CoveredIDs {
		covered[id] = true
	}
	if !covered[sample1ID] || !covered[sample2ID] || !covered[extraTreeID] {
		t.Errorf("CoveredIDs = %v, want %q, %q and the folder tree id %q", entry.CoveredIDs, sample1ID, sample2ID, extraTreeID)
	}

	// Offline (no network, no local clone): resolving the committed lock
	// reads the same source id back.
	writeCollectionLockFixture(t, root, "imported", lockUpdates["imported"])
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "lock")
	t.Setenv("EXOHUB_CLONE_OTHER_PROJECT", "")

	offlineID, _, external, err := ResolveCollectionCoverage(reader, "imported")
	if err != nil {
		t.Fatalf("ResolveCollectionCoverage(offline) error = %v", err)
	}
	wantCoverageID := CollectionID([]string{wantID})
	if offlineID != wantCoverageID {
		t.Errorf("offline id = %q, want %q", offlineID, wantCoverageID)
	}
	if len(external) != 1 || external[0].ID != wantID {
		t.Errorf("external = %+v, want one entry with ID %q", external, wantID)
	}
}

// TestResolveCollection_ExternalProject covers a "members" entry naming a
// whole project version (design D5): refreshLock=true resolves it to that
// version's root git tree id, offline resolution reads the same id back
// from the lock.
func TestResolveCollection_ExternalProject(t *testing.T) {
	extRoot, extRun := newExternalProjectRepo(t, "other-project")
	treeID := strings.TrimSpace(extRun("git", "rev-parse", "HEAD^{tree}"))
	_ = extRoot

	root, run := stateTestRepo(t, false)
	writeCollectionManifest(t, root, "imported", `{"members": ["exohub:other-project@main"]}`)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	reader := NewWorktreeReader(root)
	wantID := GitTreeEntityID(treeID)
	_, members, _, lockUpdates, err := ResolveCollection(reader, "imported", true)
	if err != nil {
		t.Fatalf("ResolveCollection(refreshLock) error = %v", err)
	}
	if len(members) != 1 || members[0].ID != wantID {
		t.Fatalf("members = %+v, want one member with ID %q", members, wantID)
	}

	writeCollectionLockFixture(t, root, "imported", lockUpdates["imported"])
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "lock")
	t.Setenv("EXOHUB_CLONE_OTHER_PROJECT", "")

	_, members2, _, _, err := ResolveCollection(reader, "imported", false)
	if err != nil {
		t.Fatalf("ResolveCollection(offline, locked) error = %v", err)
	}
	if len(members2) != 1 || members2[0].ID != wantID {
		t.Fatalf("offline members = %+v, want one member with ID %q", members2, wantID)
	}
}

// TestResolveCollection_ExternalMemberUnresolvable covers that an
// unresolvable "members" entry (no local clone, no catalog, no git
// remote) is a clear resolution error, naming the collection, the same
// way any other unresolvable member is reported, when refreshLock=true
// (exo provenance add) tries to live-resolve it.
func TestResolveCollection_ExternalMemberUnresolvable(t *testing.T) {
	t.Setenv("EXOHUB_CATALOG_URL", "http://127.0.0.1:0")
	t.Setenv("EXOHUB_CLONE_NO_SUCH_PROJECT_XYZ", "")
	root, run := stateTestRepo(t, false)
	writeCollectionManifest(t, root, "imported", `{"members": ["exohub:no-such-project-xyz:file.txt@main"]}`)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	reader := NewWorktreeReader(root)
	if _, _, _, _, err := ResolveCollection(reader, "imported", true); err == nil {
		t.Fatal("expected an error resolving an unreachable external member")
	}
}

// TestResolveCollectionCoverage_MissingLockEntry covers design D5
// amendment review point 1 for the offline (ResolveCollectionCoverage)
// path too: a "members" reference with no lock entry at all (never
// locked) is an actionable error, not a silent network fallback.
func TestResolveCollectionCoverage_MissingLockEntry(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeCollectionManifest(t, root, "imported", `{"members": ["exohub:other-project:fastq/sample1.fastq.gz@main"]}`)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	reader := NewWorktreeReader(root)
	_, _, _, err := ResolveCollectionCoverage(reader, "imported")
	if err == nil {
		t.Fatal("expected a missing lock entry error")
	}
	if !strings.Contains(err.Error(), "no lock entry") || !strings.Contains(err.Error(), "exo provenance add --generated collection:imported") {
		t.Errorf("error = %v, want it to name the write command", err)
	}
}

// TestResolveCollectionCoverage_StaleLockIgnoresLiveMoves covers design D5
// amendment review point 2 ("'Pinned' doesn't hold for branch versions"):
// once locked, a collection's id and coverage stay exactly what the lock
// says even after the external project's "main" moves, since
// ResolveCollectionCoverage never re-resolves "members" itself.
func TestResolveCollectionCoverage_StaleLockIgnoresLiveMoves(t *testing.T) {
	extRoot, extRun := newExternalProjectRepo(t, "other-project")
	const memberRef = "exohub:other-project:fastq/sample1.fastq.gz@main"

	root, run := stateTestRepo(t, false)
	writeCollectionManifest(t, root, "imported", `{"members": ["`+memberRef+`"]}`)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	reader := NewWorktreeReader(root)
	_, _, _, lockUpdates, err := ResolveCollection(reader, "imported", true)
	if err != nil {
		t.Fatalf("ResolveCollection(refreshLock) error = %v", err)
	}
	oldID := lockUpdates["imported"].Members[memberRef].ID
	writeCollectionLockFixture(t, root, "imported", lockUpdates["imported"])
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "lock")

	// "main" moves in the source project (and the publish ref resolution
	// actually reads, refs/remotes/origin/main -- not just refs/heads/main).
	writeRepoFile(t, extRoot, "fastq/sample1.fastq.gz", "fastq-1-changed")
	extRun("git", "add", ".")
	extRun("git", "commit", "-q", "-m", "update sample1")
	extRun("git", "update-ref", "refs/remotes/origin/main", "refs/heads/main")

	id, _, _, err := ResolveCollectionCoverage(reader, "imported")
	if err != nil {
		t.Fatalf("ResolveCollectionCoverage() error = %v", err)
	}
	wantID := CollectionID([]string{oldID})
	if id != wantID {
		t.Errorf("id = %q, want %q (the locked id, unaffected by main moving)", id, wantID)
	}

	// Re-locking (as "exo provenance add" would do) picks up the move: the
	// refreshed lock's id differs from the stale one above.
	_, _, _, lockUpdates2, err := ResolveCollection(reader, "imported", true)
	if err != nil {
		t.Fatalf("ResolveCollection(refreshLock) after move error = %v", err)
	}
	newID := lockUpdates2["imported"].Members[memberRef].ID
	if newID == oldID {
		t.Errorf("re-locked id = %q, want it to differ from the pre-move id %q", newID, oldID)
	}
}

// TestVerifyCollectionLock_Match covers exo bundle --verify's happy path
// (design D5 amendment): re-resolving a "members" reference over the
// network and finding it still matches the lock is not an error.
func TestVerifyCollectionLock_Match(t *testing.T) {
	_, extRun := newExternalProjectRepo(t, "other-project")
	_ = extRun
	const memberRef = "exohub:other-project:fastq/sample1.fastq.gz@main"

	root, run := stateTestRepo(t, false)
	writeCollectionManifest(t, root, "imported", `{"members": ["`+memberRef+`"]}`)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	reader := NewWorktreeReader(root)
	_, _, _, lockUpdates, err := ResolveCollection(reader, "imported", true)
	if err != nil {
		t.Fatalf("ResolveCollection(refreshLock) error = %v", err)
	}
	writeCollectionLockFixture(t, root, "imported", lockUpdates["imported"])
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "lock")

	if err := VerifyCollectionLock(reader, "imported"); err != nil {
		t.Errorf("VerifyCollectionLock() error = %v, want nil (lock still matches)", err)
	}
}

// TestVerifyCollectionLock_Mismatch covers exo bundle --verify catching a
// stale lock (design D5 amendment review point 2): once the external
// project's pinned branch moves, VerifyCollectionLock reports the mismatch
// instead of silently succeeding.
func TestVerifyCollectionLock_Mismatch(t *testing.T) {
	extRoot, extRun := newExternalProjectRepo(t, "other-project")
	const memberRef = "exohub:other-project:fastq/sample1.fastq.gz@main"

	root, run := stateTestRepo(t, false)
	writeCollectionManifest(t, root, "imported", `{"members": ["`+memberRef+`"]}`)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	reader := NewWorktreeReader(root)
	_, _, _, lockUpdates, err := ResolveCollection(reader, "imported", true)
	if err != nil {
		t.Fatalf("ResolveCollection(refreshLock) error = %v", err)
	}
	writeCollectionLockFixture(t, root, "imported", lockUpdates["imported"])
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "lock")

	writeRepoFile(t, extRoot, "fastq/sample1.fastq.gz", "fastq-1-changed")
	extRun("git", "add", ".")
	extRun("git", "commit", "-q", "-m", "update sample1")
	extRun("git", "update-ref", "refs/remotes/origin/main", "refs/heads/main")

	err = VerifyCollectionLock(reader, "imported")
	if err == nil {
		t.Fatal("expected a lock mismatch error")
	}
	if !strings.Contains(err.Error(), "now resolves to") {
		t.Errorf("error = %v, want it to describe the mismatch", err)
	}
}
