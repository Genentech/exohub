package provenance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	lib "github.com/Genentech/exohub/go/exo/internal/provenance"
)

// writeCollectionManifestFile writes a named collection manifest for use in
// `exo provenance add` tests (design D2).
func writeCollectionManifestFile(t *testing.T, root, name, body string) {
	t.Helper()
	writeFile(t, root, lib.CollectionManifestPath(name), body)
}

func TestAdd_NamedCollectionGenerated(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	noLoginToken(t)

	writeFile(t, root, "cohort_a/sample1.bam", "s1")
	writeFile(t, root, "cohort_a/sample2.bam", "s2")
	writeCollectionManifestFile(t, root, "cohort-a-bams", `{"paths": ["cohort_a/sample1.bam", "cohort_a/sample2.bam"]}`)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	reader := lib.NewWorktreeReader(root)
	wantID, _, _, _, err := lib.ResolveCollection(reader, "cohort-a-bams", false)
	if err != nil {
		t.Fatalf("ResolveCollection() error = %v", err)
	}

	out, err := runAddCmd(t, "--generated", "collection:cohort-a-bams", "--agent", "person:alice")
	if err != nil {
		t.Fatalf("add error = %v, output: %s", err, out)
	}

	rec := readOnlyRecord(t, root)
	node := generatedNode(t, rec, wantID)
	if types := node.Types(); len(types) != 1 || types[0] != "Collection" {
		t.Errorf("@type = %v, want [Collection]", types)
	}
	if node["label"] != "collection:cohort-a-bams" {
		t.Errorf("label = %v, want collection:cohort-a-bams", node["label"])
	}
	hadMember, ok := node["hadMember"].([]any)
	if !ok || len(hadMember) != 2 {
		t.Fatalf("hadMember = %v, want a 2-element array", node["hadMember"])
	}
	bam1 := gitBlobID(t, run, "cohort_a/sample1.bam")
	bam2 := gitBlobID(t, run, "cohort_a/sample2.bam")
	want := map[string]bool{lib.GitBlobEntityID(bam1): true, lib.GitBlobEntityID(bam2): true}
	for _, v := range hadMember {
		if !want[v.(string)] {
			t.Errorf("unexpected hadMember entry %v", v)
		}
	}
}

func TestAdd_NamedCollectionMissingManifestRejected(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	noLoginToken(t)

	writeFile(t, root, "data/counts.csv", "x")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	_, err := runAddCmd(t, "--generated", "collection:does-not-exist", "--agent", "person:alice")
	if err == nil {
		t.Fatal("expected an error for a missing collection manifest")
	}
}

func TestAdd_NamedCollectionMissingMemberRejected(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	noLoginToken(t)

	writeFile(t, root, "cohort_a/sample1.bam", "s1")
	writeCollectionManifestFile(t, root, "cohort-a-bams", `{"paths": ["cohort_a/sample1.bam", "cohort_a/missing.bam"]}`)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	_, err := runAddCmd(t, "--generated", "collection:cohort-a-bams", "--agent", "person:alice")
	if err == nil {
		t.Fatal("expected an error for a collection manifest member that does not exist")
	}
	if !strings.Contains(err.Error(), "cohort_a/missing.bam") {
		t.Errorf("error = %v, want it to name the missing member", err)
	}
}

func TestAdd_NamedCollectionModifiedMemberRejected(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	noLoginToken(t)

	writeFile(t, root, "cohort_a/sample1.bam", "s1")
	writeCollectionManifestFile(t, root, "cohort-a-bams", `{"paths": ["cohort_a/sample1.bam"]}`)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")
	writeFile(t, root, "cohort_a/sample1.bam", "changed")

	_, err := runAddCmd(t, "--generated", "collection:cohort-a-bams", "--agent", "person:alice")
	if err == nil {
		t.Fatal("expected an error for a collection member modified relative to the index")
	}
	if !strings.Contains(err.Error(), "exo add") {
		t.Errorf("error = %v, want a hint to run exo add", err)
	}
}

func TestAdd_NamedCollectionDryRun(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	noLoginToken(t)

	writeFile(t, root, "cohort_a/sample1.bam", "s1")
	writeCollectionManifestFile(t, root, "cohort-a-bams", `{"paths": ["cohort_a/sample1.bam"]}`)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	out, err := runAddCmd(t, "--generated", "collection:cohort-a-bams", "--agent", "person:alice", "--dry-run")
	if err != nil {
		t.Fatalf("add error = %v", err)
	}
	if !strings.Contains(out, "hadMember") {
		t.Errorf("dry-run output = %q, want it to mention hadMember", out)
	}
}

func TestAdd_NamedCollectionRevisionOf(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	noLoginToken(t)

	writeFile(t, root, "cohort_a/sample1.bam", "s1")
	writeCollectionManifestFile(t, root, "cohort-a-bams", `{"paths": ["cohort_a/sample1.bam"]}`)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "v1")
	run("git", "tag", "v1")

	oldReader := lib.NewRefReader(root, "v1")
	oldID, _, _, _, err := lib.ResolveCollection(oldReader, "cohort-a-bams", false)
	if err != nil {
		t.Fatalf("ResolveCollection(v1) error = %v", err)
	}

	writeFile(t, root, "cohort_a/sample2.bam", "s2")
	writeCollectionManifestFile(t, root, "cohort-a-bams", `{"paths": ["cohort_a/sample1.bam", "cohort_a/sample2.bam"]}`)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "v2")

	newReader := lib.NewWorktreeReader(root)
	newID, _, _, _, err := lib.ResolveCollection(newReader, "cohort-a-bams", false)
	if err != nil {
		t.Fatalf("ResolveCollection(new) error = %v", err)
	}
	if newID == oldID {
		t.Fatal("expected a new collection id after a membership change")
	}

	_, err = runAddCmd(t, "--generated", "collection:cohort-a-bams", "--revision-of", "v1:collection:cohort-a-bams", "--agent", "person:alice")
	if err != nil {
		t.Fatalf("add error = %v", err)
	}

	rec := readOnlyRecord(t, root)
	generated := generatedNode(t, rec, newID)
	if generated["wasRevisionOf"] != oldID {
		t.Errorf("wasRevisionOf = %v, want %v", generated["wasRevisionOf"], oldID)
	}
}

func TestAdd_NamedCollectionSelfRevisionRejected(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	noLoginToken(t)

	writeFile(t, root, "cohort_a/sample1.bam", "s1")
	writeCollectionManifestFile(t, root, "cohort-a-bams", `{"paths": ["cohort_a/sample1.bam"]}`)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	_, err := runAddCmd(t, "--generated", "collection:cohort-a-bams", "--revision-of", "collection:cohort-a-bams", "--agent", "person:alice")
	if err == nil {
		t.Fatal("expected an error for --revision-of equal to the --generated collection")
	}
}

func TestAdd_NamedCollectionDuplicateGenerationConflict(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	noLoginToken(t)

	writeFile(t, root, "cohort_a/sample1.bam", "s1")
	writeCollectionManifestFile(t, root, "cohort-a-bams", `{"paths": ["cohort_a/sample1.bam"]}`)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	if _, err := runAddCmd(t, "--generated", "collection:cohort-a-bams", "--agent", "person:alice"); err != nil {
		t.Fatalf("first add error = %v", err)
	}
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "first record")

	_, err := runAddCmd(t, "--generated", "collection:cohort-a-bams", "--agent", "person:alice")
	if err == nil {
		t.Fatal("expected a duplicate-generation conflict for re-recording the same collection id")
	}
	if !strings.Contains(err.Error(), "already has provenance") {
		t.Errorf("error = %v, want an already-has-provenance conflict", err)
	}
}

// externalProjectForAdd creates a throwaway git repository standing in for
// another ExoHub project, publishes "main", and points
// EXOHUB_CLONE_<PROJECT> at it for the duration of the test (design D5 of
// add-exohub-asset-import, amended per !294 review to use a lock file).
func externalProjectForAdd(t *testing.T, project string) (root string, run func(args ...string) string) {
	t.Helper()
	t.Setenv("EXOHUB_CATALOG_URL", "http://127.0.0.1:0")
	root, run = addTestRepo(t, false)
	writeFile(t, root, "data.txt", "external-data")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")
	run("git", "branch", "-M", "main")
	run("git", "update-ref", "refs/remotes/origin/main", "refs/heads/main")
	t.Setenv("EXOHUB_CLONE_"+strings.ToUpper(strings.ReplaceAll(project, "-", "_")), root)
	return root, run
}

// TestAdd_NamedCollectionGeneratedWithExternalMember covers design D5's
// amendment (!294 review, "a lock file per collection with external
// members"): "exo provenance add --generated collection:<name>" is the
// command that live-resolves a "members" entry and writes/stages its lock
// file, so the record's hadMember and the lock agree, and the lock is
// committed next to the manifest for every offline command to read.
func TestAdd_NamedCollectionGeneratedWithExternalMember(t *testing.T) {
	_, extRun := externalProjectForAdd(t, "other-project")
	extBlobID := lib.GitBlobEntityID(gitBlobID(t, extRun, "data.txt"))

	root, run := addTestRepo(t, false)
	chdir(t, root)
	noLoginToken(t)

	writeCollectionManifestFile(t, root, "imported", `{"members": ["exohub:other-project:data.txt@main"]}`)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	out, err := runAddCmd(t, "--generated", "collection:imported", "--agent", "person:alice")
	if err != nil {
		t.Fatalf("add error = %v, output: %s", err, out)
	}

	wantID := lib.CollectionID([]string{extBlobID})
	rec := readOnlyRecord(t, root)
	node := generatedNode(t, rec, wantID)
	if hadMember, ok := node["hadMember"].(string); !ok || hadMember != extBlobID {
		t.Fatalf("hadMember = %v, want %q", node["hadMember"], extBlobID)
	}

	lockPath := filepath.Join(root, lib.CollectionLockPath("imported"))
	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read lock file: %v", err)
	}
	if !strings.Contains(string(data), extBlobID) {
		t.Errorf("lock file = %s, want it to contain %q", data, extBlobID)
	}
	if out := run("git", "status", "--porcelain", lib.CollectionLockPath("imported")); out != "A  "+lib.CollectionLockPath("imported")+"\n" {
		t.Errorf("lock file not staged: git status --porcelain = %q", out)
	}

	// Offline (no network, no local clone): validate reads the committed
	// lock and does not fail.
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record and lock")
	t.Setenv("EXOHUB_CLONE_OTHER_PROJECT", "")
	offlineID, _, _, _, err := lib.ResolveCollection(lib.NewWorktreeReader(root), "imported", false)
	if err != nil {
		t.Fatalf("ResolveCollection(offline) error = %v", err)
	}
	if offlineID != wantID {
		t.Errorf("offline id = %q, want %q", offlineID, wantID)
	}
}

// recordFileNames returns the record file names (not full paths) under
// .exohub/provenance/.
func recordFileNames(t *testing.T, root string) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, ".exohub/provenance"))
	if os.IsNotExist(err) {
		return map[string]bool{}
	}
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	names := map[string]bool{}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".jsonld") {
			names[e.Name()] = true
		}
	}
	return names
}

// newRecordSince reads the one record file under .exohub/provenance/ that
// is not in before (record file names are timestamp-prefixed to the
// second, design D1, so two records written by the same test within one
// second cannot be told apart by sorting their names; a before/after set
// difference can).
func newRecordSince(t *testing.T, root string, before map[string]bool) *lib.Record {
	t.Helper()
	after := recordFileNames(t, root)
	var added []string
	for name := range after {
		if !before[name] {
			added = append(added, name)
		}
	}
	if len(added) != 1 {
		t.Fatalf("got %d new record files since before, want 1: %v", len(added), added)
	}
	data, err := os.ReadFile(filepath.Join(root, ".exohub/provenance", added[0]))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	rec, err := lib.Parse(data)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	return rec
}

// TestAdd_NamedCollectionRevisionOfReadsCommittedLockNotLive covers
// exo-cli#154: "--revision-of collection:<name>" names the collection's
// *previous* state, so (design D5 amendment review point 2) it must read
// the external member's already-committed lock, never live-resolve and
// refresh it the way "--generated collection:<name>" does -- even when
// both flags name the very same collection in a single add invocation,
// where --generated's own live resolution refreshes that same lock file
// moments before --revision-of reads it.
func TestAdd_NamedCollectionRevisionOfReadsCommittedLockNotLive(t *testing.T) {
	extRoot, extRun := externalProjectForAdd(t, "other-project")
	oldBlobID := lib.GitBlobEntityID(gitBlobID(t, extRun, "data.txt"))
	oldID := lib.CollectionID([]string{oldBlobID})

	root, run := addTestRepo(t, false)
	chdir(t, root)
	noLoginToken(t)

	writeCollectionManifestFile(t, root, "imported", `{"members": ["exohub:other-project:data.txt@main"]}`)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	// Commit A: lock the collection at the external file's current content.
	if out, err := runAddCmd(t, "--generated", "collection:imported", "--agent", "person:alice"); err != nil {
		t.Fatalf("first add error = %v, output: %s", err, out)
	}
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record and lock at commit A")

	// The source project's "main" moves to commit B.
	writeFile(t, extRoot, "data.txt", "external-data-v2")
	extRun("git", "add", ".")
	extRun("git", "commit", "-q", "-m", "update data.txt")
	extRun("git", "update-ref", "refs/remotes/origin/main", "refs/heads/main")
	newBlobID := lib.GitBlobEntityID(gitBlobID(t, extRun, "data.txt"))
	newID := lib.CollectionID([]string{newBlobID})
	if newID == oldID {
		t.Fatal("test setup error: expected a different id after the source file changed")
	}

	// --generated collection:imported live-resolves commit B and refreshes
	// the lock; --revision-of collection:imported, in the very same
	// invocation, must still name commit A: the lock as committed before
	// this add ran, not the refresh --generated just computed.
	before := recordFileNames(t, root)
	out, err := runAddCmd(t, "--generated", "collection:imported", "--revision-of", "collection:imported", "--agent", "person:bob")
	if err != nil {
		t.Fatalf("second add error = %v, output: %s", err, out)
	}

	rec := newRecordSince(t, root, before)
	generated := generatedNode(t, rec, newID)
	if got, _ := generated["wasRevisionOf"].(string); got != oldID {
		t.Errorf("wasRevisionOf = %v, want %q (the lock as committed before this add refreshed it)", generated["wasRevisionOf"], oldID)
	}
	if hadMember, _ := generated["hadMember"].(string); hadMember != newBlobID {
		t.Errorf("hadMember = %v, want %q (the freshly locked commit B id)", generated["hadMember"], newBlobID)
	}

	lockPath := filepath.Join(root, lib.CollectionLockPath("imported"))
	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read lock file: %v", err)
	}
	if !strings.Contains(string(data), newBlobID) {
		t.Errorf("lock file = %s, want it to contain the refreshed id %q", data, newBlobID)
	}
	if strings.Contains(string(data), oldBlobID) {
		t.Errorf("lock file = %s, want it to no longer contain the superseded id %q", data, oldBlobID)
	}
}
