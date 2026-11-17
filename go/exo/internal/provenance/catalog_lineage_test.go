package provenance

import (
	"fmt"
	"strings"
	"testing"
)

func lineageFor(t *testing.T, result *CompileResult, path string) *ArtifactLineage {
	t.Helper()
	l, ok := result.Lineage[path]
	if !ok {
		t.Fatalf("no lineage entry for %q, got keys %v", path, lineageKeys(result.Lineage))
	}
	return l
}

func lineageKeys(m map[string]*ArtifactLineage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestBuildArtifactLineage_DerivedFileInputs(t *testing.T) {
	// results/de.csv is derived from normalized.csv, itself derived from
	// counts.csv: lineage.inputs lists the direct input (spec "Artifact
	// lineage in bundle documents": "the artifact document of
	// results/de.csv has lineage.inputs with normalized.csv"). Transitive
	// traversal is not part of lineage.inputs (design decision: drop
	// lineage.upstream, consumers and a future graph database handle
	// transitive traversal).
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "counts.csv", "counts\n")
	writeRepoFile(t, root, "normalized.csv", "normalized\n")
	writeRepoFile(t, root, "results/de.csv", "de\n")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")
	countsID := GitBlobEntityID(mustBlobID(t, run, "counts.csv"))
	normID := GitBlobEntityID(mustBlobID(t, run, "normalized.csv"))
	deID := GitBlobEntityID(mustBlobID(t, run, "results/de.csv"))

	recID := NewRecordID()
	graph := fmt.Sprintf(`
{"@id": %q, "@type": "Entity"},
{"@id": %q, "@type": "Entity", "wasDerivedFrom": %q},
{"@id": %q, "@type": "Entity", "wasDerivedFrom": %q}`,
		countsID, normID, countsID, deID, normID)
	rec := minimalRecordJSON(recID, "recorded", "urn:exohub:agent:person:alice", graph)
	writeRepoFile(t, root, RecordDir+"/r.jsonld", rec)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record")

	result, report, err := CompileBundle(NewRefReader(root, "HEAD"), []string{"results/de.csv", "normalized.csv", "counts.csv"})
	if err != nil {
		t.Fatalf("CompileBundle() error = %v", err)
	}
	if report.HasErrors() {
		t.Fatalf("HasErrors(): %+v", report.Findings)
	}

	de := lineageFor(t, result, "results/de.csv")
	if len(de.Inputs) != 1 || de.Inputs[0].ID != normID || de.Inputs[0].Path != "normalized.csv" {
		t.Errorf("de.Inputs = %+v, want [normalized.csv]", de.Inputs)
	}
	if !de.Covered {
		t.Error("de.Covered = false, want true (wasDerivedFrom is a covering statement)")
	}
}

func TestBuildArtifactLineage_FolderMemberInheritsAndFollowsRevision(t *testing.T) {
	// Matches spec "Lineage of folder collection members" scenario "TileDB
	// member inherits the folder lineage": v2 wasRevisionOf v1, v1 was
	// derived from counts.csv, and f2 has no record of its own.
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "counts.csv", "counts\n")
	writeRepoFile(t, root, "data/expr.tdb/f1.bin", "one\n")
	writeRepoFile(t, root, "data/expr.tdb/f2.bin", "two\n")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "v1")
	countsID := GitBlobEntityID(mustBlobID(t, run, "counts.csv"))
	tree1 := GitTreeEntityID(mustBlobID(t, run, "data/expr.tdb"))

	act1 := NewActivityID()
	rec1 := minimalRecordJSON(NewRecordID(), "recorded", "urn:exohub:agent:person:alice",
		fmt.Sprintf(`{"@id": %q, "@type": "Collection", "wasGeneratedBy": %q, "wasDerivedFrom": %q}, {"@id": %q, "@type": "Activity"}, {"@id": %q, "@type": "Entity"}`,
			tree1, act1, countsID, act1, countsID))
	writeRepoFile(t, root, RecordDir+"/v1.jsonld", rec1)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record v1")

	writeRepoFile(t, root, "data/expr.tdb/f1.bin", "one-updated\n")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "v2")
	tree2 := GitTreeEntityID(mustBlobID(t, run, "data/expr.tdb"))

	act2 := NewActivityID()
	rec2 := minimalRecordJSON(NewRecordID(), "recorded", "urn:exohub:agent:person:alice",
		fmt.Sprintf(`{"@id": %q, "@type": "Collection", "wasGeneratedBy": %q, "wasRevisionOf": %q}, {"@id": %q, "@type": "Activity"}`,
			tree2, act2, tree1, act2))
	writeRepoFile(t, root, RecordDir+"/v2.jsonld", rec2)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record v2")

	result, report, err := CompileBundle(NewRefReader(root, "HEAD"), []string{"data/expr.tdb/f1.bin", "data/expr.tdb/f2.bin", "counts.csv"})
	if err != nil {
		t.Fatalf("CompileBundle() error = %v", err)
	}
	if report.HasErrors() {
		t.Fatalf("HasErrors(): %+v", report.Findings)
	}

	for _, path := range []string{"data/expr.tdb/f1.bin", "data/expr.tdb/f2.bin"} {
		member := lineageFor(t, result, path)
		if !member.Covered {
			t.Errorf("%s: Covered = false, want true (inherited from folder)", path)
		}
		if member.Collection != nil {
			t.Errorf("%s: Collection = %+v, want nil (deprecated, no longer written)", path, member.Collection)
		}
		if len(member.Collections) != 1 || member.Collections[0].ID != tree2 || member.Collections[0].Kind != "folder" || member.Collections[0].Path != "data/expr.tdb/" || member.Collections[0].Name != "" {
			t.Errorf("%s: Collections = %+v, want [{%s, folder, data/expr.tdb/, \"\"}]", path, member.Collections, tree2)
		}
		if member.GeneratedBy == nil || member.GeneratedBy.ID != act2 {
			t.Errorf("%s: GeneratedBy = %+v, want activity %s", path, member.GeneratedBy, act2)
		}
		if len(member.RevisionOf) != 1 || member.RevisionOf[0] != tree1 {
			t.Errorf("%s: RevisionOf = %v, want [%s]", path, member.RevisionOf, tree1)
		}
	}
}

func TestBuildArtifactLineage_NamedCollectionMembership(t *testing.T) {
	// Matches design D8 "Lineage of collection membership": a current
	// member of a covered named collection gets a lineage.collections entry
	// of kind "named", and inherits the collection's generated_by when it
	// has no record of its own.
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "cohort_a/sample1.bam", "s1\n")
	writeRepoFile(t, root, "cohort_a/sample2.bam", "s2\n")
	writeCollectionManifest(t, root, "cohort-a-bams", `{"paths": ["cohort_a/sample1.bam", "cohort_a/sample2.bam"]}`)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	reader := NewRefReader(root, "HEAD")
	collID, members, _, _, err := ResolveCollection(reader, "cohort-a-bams", false)
	if err != nil {
		t.Fatalf("ResolveCollection() error = %v", err)
	}
	memberIDs := make([]string, len(members))
	for i, m := range members {
		memberIDs[i] = m.ID
	}

	act := NewActivityID()
	graph := fmt.Sprintf(`{"@id": %q, "@type": "Collection", "wasGeneratedBy": %q, "hadMember": [%q, %q]}, {"@id": %q, "@type": "Activity"}`,
		collID, act, memberIDs[0], memberIDs[1], act)
	rec := minimalRecordJSON(NewRecordID(), "recorded", "urn:exohub:agent:person:alice", graph)
	writeRepoFile(t, root, RecordDir+"/r.jsonld", rec)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record")

	result, report, err := CompileBundle(NewRefReader(root, "HEAD"), []string{"cohort_a/sample1.bam", "cohort_a/sample2.bam"})
	if err != nil {
		t.Fatalf("CompileBundle() error = %v", err)
	}
	if report.HasErrors() {
		t.Fatalf("HasErrors(): %+v", report.Findings)
	}

	for _, path := range []string{"cohort_a/sample1.bam", "cohort_a/sample2.bam"} {
		member := lineageFor(t, result, path)
		if !member.Covered {
			t.Errorf("%s: Covered = false, want true (inherited from named collection)", path)
		}
		if len(member.Collections) != 1 || member.Collections[0].ID != collID || member.Collections[0].Kind != "named" || member.Collections[0].Name != "cohort-a-bams" || member.Collections[0].Path != "" {
			t.Errorf("%s: Collections = %+v, want [{%s, named, \"\", cohort-a-bams}]", path, member.Collections, collID)
		}
		if member.GeneratedBy == nil || member.GeneratedBy.ID != act {
			t.Errorf("%s: GeneratedBy = %+v, want activity %s", path, member.GeneratedBy, act)
		}
	}
}

func TestBuildArtifactLineage_FolderCollectionsHasImportProjectVersion(t *testing.T) {
	// Matches design D4 of add-exohub-asset-import: a folder collection
	// imported with `exo add exohub:...` carries hadPrimarySource pointing
	// at the source node, whose label ("<project>@<version>") populates the
	// member's lineage.collections entry's project/version.
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "inputs/fastq-mini/a.fastq.gz", "a\n")
	writeRepoFile(t, root, "inputs/fastq-mini/b.fastq.gz", "b\n")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")
	treeID := GitTreeEntityID(mustBlobID(t, run, "inputs/fastq-mini"))
	sourceID := GitTreeEntityID("0123456789012345678901234567890123456789")

	act := NewActivityID()
	person := "urn:exohub:agent:person:alice"
	graph := fmt.Sprintf(`
{"@id": %q, "@type": "Entity", "label": "fastq-mini@main"},
{"@id": %q, "@type": "Collection", "wasDerivedFrom": %q, "hadPrimarySource": %q},
{"@id": %q, "@type": "Activity", "used": %q, "wasAssociatedWith": %q}`,
		sourceID, treeID, sourceID, sourceID, act, sourceID, person)
	rec := minimalRecordJSON(NewRecordID(), "recorded", person, graph)
	writeRepoFile(t, root, RecordDir+"/import.jsonld", rec)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record")

	result, report, err := CompileBundle(NewRefReader(root, "HEAD"), []string{"inputs/fastq-mini/a.fastq.gz", "inputs/fastq-mini/b.fastq.gz"})
	if err != nil {
		t.Fatalf("CompileBundle() error = %v", err)
	}
	if report.HasErrors() {
		t.Fatalf("HasErrors(): %+v", report.Findings)
	}

	for _, path := range []string{"inputs/fastq-mini/a.fastq.gz", "inputs/fastq-mini/b.fastq.gz"} {
		member := lineageFor(t, result, path)
		if len(member.Collections) != 1 {
			t.Fatalf("%s: Collections = %+v, want exactly one entry", path, member.Collections)
		}
		entry := member.Collections[0]
		if entry.Project != "fastq-mini" || entry.Version != "main" {
			t.Errorf("%s: Collections[0] = %+v, want Project=fastq-mini Version=main", path, entry)
		}
	}
}

// TestBuildArtifactLineage_ExternalNamedCollectionMembership covers design
// D5 of add-exohub-asset-import: a lineage.collections entry for a named
// collection reached through another project's "members" entry carries
// that project's name and pinned version, alongside the same kind/name
// shape an ordinary same-project named collection gets (design D8).
func TestBuildArtifactLineage_ExternalNamedCollectionMembership(t *testing.T) {
	_, extRun := newExternalProjectRepo(t, "other-project")
	sample1ID := GitBlobEntityID(mustBlobID(t, extRun, "fastq/sample1.fastq.gz"))
	sample2ID := GitBlobEntityID(mustBlobID(t, extRun, "fastq/sample2.fastq.gz"))
	extDigest := CollectionID([]string{sample1ID, sample2ID})

	root, run := stateTestRepo(t, false)
	// Same content as the external project's fastq/sample1.fastq.gz, so its
	// git blob id (and hence content id) matches exactly as it would after
	// "exo add exohub:other-project:fastq/sample1.fastq.gz@main".
	writeRepoFile(t, root, "local/sample1.fastq.gz", "fastq-1")
	writeCollectionManifest(t, root, "imported", `{"members": ["exohub:other-project:collection:raw-fastqs@main"]}`)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	// Live-resolve and commit the lock (design D5 amendment: what
	// "exo provenance add" would do), then make the external project
	// unreachable so CompileBundle's offline resolution below must use it.
	_, _, _, lockUpdates, err := ResolveCollection(NewWorktreeReader(root), "imported", true)
	if err != nil {
		t.Fatalf("ResolveCollection(refreshLock) error = %v", err)
	}
	writeCollectionLockFixture(t, root, "imported", lockUpdates["imported"])
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "lock")
	t.Setenv("EXOHUB_CLONE_OTHER_PROJECT", "")

	act := NewActivityID()
	graph := fmt.Sprintf(`{"@id": %q, "@type": "Collection", "wasGeneratedBy": %q}, {"@id": %q, "@type": "Activity"}`,
		extDigest, act, act)
	rec := minimalRecordJSON(NewRecordID(), "recorded", "urn:exohub:agent:person:alice", graph)
	writeRepoFile(t, root, RecordDir+"/r.jsonld", rec)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record")

	result, report, err := CompileBundle(NewRefReader(root, "HEAD"), []string{"local/sample1.fastq.gz"})
	if err != nil {
		t.Fatalf("CompileBundle() error = %v", err)
	}
	if report.HasErrors() {
		t.Fatalf("HasErrors(): %+v", report.Findings)
	}

	member := lineageFor(t, result, "local/sample1.fastq.gz")
	if !member.Covered {
		t.Fatalf("Covered = false, want true (inherited from the external named collection)")
	}
	if len(member.Collections) != 1 {
		t.Fatalf("Collections = %+v, want exactly one entry", member.Collections)
	}
	entry := member.Collections[0]
	if entry.ID != extDigest || entry.Kind != "named" || entry.Name != "raw-fastqs" {
		t.Errorf("Collections[0] = %+v, want {%s, named, raw-fastqs}", entry, extDigest)
	}
	if entry.Project != "other-project" || entry.Version != "main" {
		t.Errorf("Collections[0].Project/Version = %q/%q, want other-project/main", entry.Project, entry.Version)
	}
}

func TestBuildProvenanceDocument_CollectionMemberCount(t *testing.T) {
	// Matches design D8 "Collection entity member counts": a Collection
	// entity's provenance.json entry carries member_count, not the member
	// list — a named collection's hadMember length, or a folder
	// collection's currently tracked file count.
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "data/expr.tdb/f1.bin", "one\n")
	writeRepoFile(t, root, "data/expr.tdb/f2.bin", "two\n")
	writeRepoFile(t, root, "cohort_a/sample1.bam", "s1\n")
	writeRepoFile(t, root, "cohort_a/sample2.bam", "s2\n")
	writeCollectionManifest(t, root, "cohort-a-bams", `{"paths": ["cohort_a/sample1.bam", "cohort_a/sample2.bam"]}`)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")
	treeID := GitTreeEntityID(mustBlobID(t, run, "data/expr.tdb"))

	reader := NewRefReader(root, "HEAD")
	collID, members, _, _, err := ResolveCollection(reader, "cohort-a-bams", false)
	if err != nil {
		t.Fatalf("ResolveCollection() error = %v", err)
	}
	memberIDs := make([]string, len(members))
	for i, m := range members {
		memberIDs[i] = m.ID
	}

	graph := fmt.Sprintf(`{"@id": %q, "@type": "Collection", "label": "experiment table"}, {"@id": %q, "@type": "Collection", "hadMember": [%q, %q]}`,
		treeID, collID, memberIDs[0], memberIDs[1])
	rec := minimalRecordJSON(NewRecordID(), "recorded", "urn:exohub:agent:person:alice", graph)
	writeRepoFile(t, root, RecordDir+"/r.jsonld", rec)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record")

	result, report, err := CompileBundle(NewRefReader(root, "HEAD"), []string{"data/expr.tdb/f1.bin", "data/expr.tdb/f2.bin", "cohort_a/sample1.bam", "cohort_a/sample2.bam"})
	if err != nil {
		t.Fatalf("CompileBundle() error = %v", err)
	}
	if report.HasErrors() {
		t.Fatalf("HasErrors(): %+v", report.Findings)
	}

	entityByID := map[string]ProvenanceEntity{}
	for _, e := range result.Catalog.Entities {
		entityByID[e.ID] = e
	}
	folder, ok := entityByID[treeID]
	if !ok {
		t.Fatalf("no entity for folder collection %s", treeID)
	}
	if folder.MemberCount == nil || *folder.MemberCount != 2 {
		t.Errorf("folder MemberCount = %v, want 2", folder.MemberCount)
	}
	named, ok := entityByID[collID]
	if !ok {
		t.Fatalf("no entity for named collection %s", collID)
	}
	if named.MemberCount == nil || *named.MemberCount != 2 {
		t.Errorf("named MemberCount = %v, want 2", named.MemberCount)
	}
}

func TestBuildProvenanceDocument_FolderMemberCountOnlyCurrentRevision(t *testing.T) {
	// Matches the collections maître's review of !289: member_count for a
	// folder Collection only applies to the node whose id is the ref's
	// current tree id for its exo:path (treeIDs[dir]). An older revision of
	// the same folder, kept in scope only as a wasRevisionOf upstream
	// reference, is a different node id and must not get the current
	// folder's count.
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "data/expr.tdb/f1.bin", "one\n")
	writeRepoFile(t, root, "data/expr.tdb/f2.bin", "two\n")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "v1")
	tree1 := GitTreeEntityID(mustBlobID(t, run, "data/expr.tdb"))

	act1 := NewActivityID()
	rec1 := minimalRecordJSON(NewRecordID(), "recorded", "urn:exohub:agent:person:alice",
		fmt.Sprintf(`{"@id": %q, "@type": "Collection", "wasGeneratedBy": %q}, {"@id": %q, "@type": "Activity"}`,
			tree1, act1, act1))
	writeRepoFile(t, root, RecordDir+"/v1.jsonld", rec1)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record v1")

	writeRepoFile(t, root, "data/expr.tdb/f1.bin", "one-updated\n")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "v2")
	tree2 := GitTreeEntityID(mustBlobID(t, run, "data/expr.tdb"))

	act2 := NewActivityID()
	rec2 := minimalRecordJSON(NewRecordID(), "recorded", "urn:exohub:agent:person:alice",
		fmt.Sprintf(`{"@id": %q, "@type": "Collection", "wasGeneratedBy": %q, "wasRevisionOf": %q}, {"@id": %q, "@type": "Activity"}`,
			tree2, act2, tree1, act2))
	writeRepoFile(t, root, RecordDir+"/v2.jsonld", rec2)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record v2")

	result, report, err := CompileBundle(NewRefReader(root, "HEAD"), []string{"data/expr.tdb/f1.bin", "data/expr.tdb/f2.bin"})
	if err != nil {
		t.Fatalf("CompileBundle() error = %v", err)
	}
	if report.HasErrors() {
		t.Fatalf("HasErrors(): %+v", report.Findings)
	}

	entityByID := map[string]ProvenanceEntity{}
	for _, e := range result.Catalog.Entities {
		entityByID[e.ID] = e
	}
	current, ok := entityByID[tree2]
	if !ok {
		t.Fatalf("no entity for current folder collection %s", tree2)
	}
	if current.MemberCount == nil || *current.MemberCount != 2 {
		t.Errorf("current folder MemberCount = %v, want 2", current.MemberCount)
	}
	old, ok := entityByID[tree1]
	if !ok {
		t.Fatalf("no entity for older folder collection revision %s", tree1)
	}
	if old.MemberCount != nil {
		t.Errorf("older revision MemberCount = %v, want nil (not the current tree at its path)", *old.MemberCount)
	}
}

func TestBuildArtifactLineage_UsedOnlyInputNotCovered(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "raw/input.csv", "input\n")
	writeRepoFile(t, root, "results/out.csv", "out\n")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")
	inputID := GitBlobEntityID(mustBlobID(t, run, "raw/input.csv"))
	outID := GitBlobEntityID(mustBlobID(t, run, "results/out.csv"))
	actID := NewActivityID()

	rec := minimalRecordJSON(NewRecordID(), "recorded", "urn:exohub:agent:person:alice",
		fmt.Sprintf(`{"@id": %q, "@type": "Entity"}, {"@id": %q, "@type": "Activity", "used": %q}, {"@id": %q, "@type": "Entity", "wasGeneratedBy": %q}`,
			inputID, actID, inputID, outID, actID))
	writeRepoFile(t, root, RecordDir+"/r.jsonld", rec)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record")

	result, report, err := CompileBundle(NewRefReader(root, "HEAD"), []string{"raw/input.csv", "results/out.csv"})
	if err != nil {
		t.Fatalf("CompileBundle() error = %v", err)
	}
	if report.HasErrors() {
		t.Fatalf("HasErrors(): %+v", report.Findings)
	}

	input := lineageFor(t, result, "raw/input.csv")
	if input.Covered {
		t.Error("input.Covered = true, want false (only used as an input)")
	}

	out := lineageFor(t, result, "results/out.csv")
	if !out.Covered {
		t.Error("out.Covered = false, want true")
	}
	if len(out.Inputs) != 1 || out.Inputs[0].ID != inputID {
		t.Errorf("out.Inputs = %+v, want [%s]", out.Inputs, inputID)
	}
}

func TestBuildArtifactLineage_UnrelatedFileHasNoLineageKey(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "a.txt", "a\n")
	writeRepoFile(t, root, "unrelated.txt", "unrelated\n")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")
	aID := GitBlobEntityID(mustBlobID(t, run, "a.txt"))

	rec := minimalRecordJSON(NewRecordID(), "recorded", "urn:exohub:agent:person:alice",
		fmt.Sprintf(`{"@id": %q, "@type": "Entity", "wasAttributedTo": "urn:exohub:agent:person:alice"}`, aID))
	writeRepoFile(t, root, RecordDir+"/r.jsonld", rec)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record")

	result, report, err := CompileBundle(NewRefReader(root, "HEAD"), []string{"a.txt", "unrelated.txt"})
	if err != nil {
		t.Fatalf("CompileBundle() error = %v", err)
	}
	if report.HasErrors() {
		t.Fatalf("HasErrors(): %+v", report.Findings)
	}
	if _, ok := result.Lineage["unrelated.txt"]; ok {
		t.Error("unrelated.txt has a lineage entry, want none")
	}
	lineageFor(t, result, "a.txt")
}

func TestBuildArtifactLineage_SupersededRecordExcludedFromRecords(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "a.txt", "hello\n")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")
	entityID := GitBlobEntityID(mustBlobID(t, run, "a.txt"))

	id1 := NewRecordID()
	id2 := NewRecordID()
	rec1 := minimalRecordJSON(id1, "recorded", "urn:exohub:agent:person:alice",
		fmt.Sprintf(`{"@id": %q, "@type": "Entity", "wasAttributedTo": "urn:exohub:agent:person:alice"}`, entityID))
	rec2 := fmt.Sprintf(`{
  "@context": %q,
  "@id": %q,
  "exo:assertion": "recorded",
  "exo:assertedBy": "urn:exohub:agent:person:alice",
  "generatedAtTime": "2026-10-14T11:00:00Z",
  "exo:supersedes": [%q],
  "@graph": [{"@id": %q, "@type": "Entity", "wasAttributedTo": "urn:exohub:agent:person:alice", "label": "corrected"}]
}`, ContextIRI, id2, id1, entityID)
	writeRepoFile(t, root, RecordDir+"/1.jsonld", rec1)
	writeRepoFile(t, root, RecordDir+"/2.jsonld", rec2)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "records")

	result, report, err := CompileBundle(NewRefReader(root, "HEAD"), []string{"a.txt"})
	if err != nil {
		t.Fatalf("CompileBundle() error = %v", err)
	}
	if report.HasErrors() {
		t.Fatalf("HasErrors(): %+v", report.Findings)
	}

	a := lineageFor(t, result, "a.txt")
	if len(a.Records) != 1 || a.Records[0].ID != id2 {
		t.Errorf("a.Records = %+v, want only the correcting record %s", a.Records, id2)
	}
}

func TestBuildArtifactLineage_SoftwareAgentInGeneratedByAgents(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "out.csv", "result\n")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")
	outID := GitBlobEntityID(mustBlobID(t, run, "out.csv"))
	actID := NewActivityID()
	botID := "urn:exohub:agent:software:bot"
	aliceID := "urn:exohub:agent:person:alice"

	rec := minimalRecordJSON(NewRecordID(), "recorded", botID,
		fmt.Sprintf(`{"@id": %q, "@type": "Entity", "wasGeneratedBy": %q},
{"@id": %q, "@type": "Activity", "wasAssociatedWith": [%q, %q]},
{"@id": %q, "@type": "SoftwareAgent", "exo:model": "gpt", "actedOnBehalfOf": %q},
{"@id": %q, "@type": "Person"}`,
			outID, actID, actID, botID, aliceID, botID, aliceID, aliceID))
	writeRepoFile(t, root, RecordDir+"/r.jsonld", rec)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record")

	result, report, err := CompileBundle(NewRefReader(root, "HEAD"), []string{"out.csv"})
	if err != nil {
		t.Fatalf("CompileBundle() error = %v", err)
	}
	if report.HasErrors() {
		t.Fatalf("HasErrors(): %+v", report.Findings)
	}

	out := lineageFor(t, result, "out.csv")
	if out.GeneratedBy == nil {
		t.Fatal("out.GeneratedBy = nil")
	}
	var bot *LineageAgent
	for i := range out.GeneratedBy.Agents {
		if out.GeneratedBy.Agents[i].ID == botID {
			bot = &out.GeneratedBy.Agents[i]
		}
	}
	if bot == nil {
		t.Fatalf("GeneratedBy.Agents = %+v, want the software agent %s", out.GeneratedBy.Agents, botID)
	}
	if bot.Type != "SoftwareAgent" || bot.Model != "gpt" {
		t.Errorf("bot = %+v, want Type=SoftwareAgent Model=gpt", bot)
	}
	if len(bot.ActedOnBehalfOf) != 1 || bot.ActedOnBehalfOf[0] != aliceID {
		t.Errorf("bot.ActedOnBehalfOf = %v, want [%s]", bot.ActedOnBehalfOf, aliceID)
	}
}

func TestBuildArtifactLineage_NoRecordsByteIdentical(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "a.txt", "hello\n")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	result, _, err := CompileBundle(NewRefReader(root, "HEAD"), []string{"a.txt"})
	if err != nil {
		t.Fatalf("CompileBundle() error = %v", err)
	}
	if result.Lineage != nil {
		t.Errorf("Lineage = %+v, want nil", result.Lineage)
	}
	if result.Catalog != nil {
		t.Errorf("Catalog = %+v, want nil", result.Catalog)
	}
}

func TestBuildProvenanceDocument_Fields(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "out.csv", "result\n")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")
	outID := GitBlobEntityID(mustBlobID(t, run, "out.csv"))
	actID := NewActivityID()

	rec := minimalRecordJSON(NewRecordID(), "recorded", "urn:exohub:agent:person:alice",
		fmt.Sprintf(`{"@id": %q, "@type": "Entity", "wasGeneratedBy": %q},
{"@id": %q, "@type": "Activity"},
{"@id": "urn:exohub:agent:person:alice", "@type": "Person"}`, outID, actID, actID))
	writeRepoFile(t, root, RecordDir+"/r.jsonld", rec)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record")

	headHash := run("git", "rev-parse", "HEAD")
	result, report, err := CompileBundle(NewRefReader(root, "HEAD"), []string{"out.csv"})
	if err != nil {
		t.Fatalf("CompileBundle() error = %v", err)
	}
	if report.HasErrors() {
		t.Fatalf("HasErrors(): %+v", report.Findings)
	}
	if result.Catalog == nil {
		t.Fatal("Catalog = nil")
	}
	if result.Catalog.Schema != ProvenanceSchemaID {
		t.Errorf("Schema = %q, want %q", result.Catalog.Schema, ProvenanceSchemaID)
	}
	if result.Catalog.GraphFile != "provenance.jsonld" {
		t.Errorf("GraphFile = %q, want provenance.jsonld", result.Catalog.GraphFile)
	}
	if result.Catalog.EntityCount != len(result.Document.Graph) {
		t.Errorf("EntityCount = %d, want %d", result.Catalog.EntityCount, len(result.Document.Graph))
	}
	if len(result.Catalog.Entities) != 1 || result.Catalog.Entities[0].ID != outID {
		t.Errorf("Entities = %+v, want [%s]", result.Catalog.Entities, outID)
	}
	if !result.Catalog.Entities[0].Covered {
		t.Error("Entities[0].Covered = false, want true")
	}
	if len(result.Catalog.Activities) != 1 || result.Catalog.Activities[0].ID != actID {
		t.Errorf("Activities = %+v, want [%s]", result.Catalog.Activities, actID)
	}
	if len(result.Catalog.Activities[0].Generated) != 1 || result.Catalog.Activities[0].Generated[0] != outID {
		t.Errorf("Activities[0].Generated = %v, want [%s]", result.Catalog.Activities[0].Generated, outID)
	}
	if len(result.Catalog.Agents) != 1 || result.Catalog.Agents[0].ID != "urn:exohub:agent:person:alice" {
		t.Errorf("Agents = %+v, want [urn:exohub:agent:person:alice]", result.Catalog.Agents)
	}
	_ = headHash
}

func TestBuildArtifactLineage_RolesParametersEnvironment(t *testing.T) {
	// Spec "Roles, parameters and environment in the artifact document":
	// de.csv was generated by an activity that used counts.csv with role
	// "counts", the parameter alpha=0.05 and a container environment. The
	// parameter and environment entities share the activity's "used"
	// shortcut but must not show up in lineage.inputs (design D9).
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "counts.csv", "counts\n")
	writeRepoFile(t, root, "results/de.csv", "de\n")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")
	countsID := GitBlobEntityID(mustBlobID(t, run, "counts.csv"))
	deID := GitBlobEntityID(mustBlobID(t, run, "results/de.csv"))
	valueID, err := ValueEntityID("alpha", 0.05)
	if err != nil {
		t.Fatalf("ValueEntityID() error = %v", err)
	}
	envID := "docker://ghcr.io/org/de@sha256:" + strings.Repeat("ab", 32)
	actID := NewActivityID()
	usageCounts, usageAlpha, usageEnv := NewUsageID(), NewUsageID(), NewUsageID()

	graph := fmt.Sprintf(`
{"@id": %q, "@type": "Activity", "used": [%q, %q, %q], "qualifiedUsage": [%q, %q, %q]},
{"@id": %q, "@type": "Usage", "entity": %q, "hadRole": "counts"},
{"@id": %q, "@type": "Usage", "entity": %q, "hadRole": "alpha"},
{"@id": %q, "@type": "Usage", "entity": %q, "hadRole": "environment"},
{"@id": %q, "@type": "Entity"},
{"@id": %q, "@type": "Entity", "value": 0.05},
{"@id": %q, "@type": "Entity", "label": "tool"},
{"@id": %q, "@type": "Entity", "wasGeneratedBy": %q}`,
		actID, countsID, valueID, envID, usageCounts, usageAlpha, usageEnv,
		usageCounts, countsID,
		usageAlpha, valueID,
		usageEnv, envID,
		countsID,
		valueID,
		envID,
		deID, actID)
	rec := minVersionRecordJSON(NewRecordID(), "recorded", "urn:exohub:agent:person:alice", firstMinVersionRelease, graph)
	writeRepoFile(t, root, RecordDir+"/r.jsonld", rec)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record")

	result, report, err := CompileBundle(NewRefReader(root, "HEAD"), []string{"results/de.csv", "counts.csv"})
	if err != nil {
		t.Fatalf("CompileBundle() error = %v", err)
	}
	if report.HasErrors() {
		t.Fatalf("HasErrors(): %+v", report.Findings)
	}

	de := lineageFor(t, result, "results/de.csv")
	if len(de.Inputs) != 1 || de.Inputs[0].ID != countsID || de.Inputs[0].Role != "counts" {
		t.Errorf("de.Inputs = %+v, want [{ID: %s, Role: counts}]", de.Inputs, countsID)
	}
	if de.GeneratedBy == nil {
		t.Fatal("de.GeneratedBy = nil")
	}
	if len(de.GeneratedBy.Parameters) != 1 || de.GeneratedBy.Parameters[0] != (LineageValue{Name: "alpha", Value: 0.05}) {
		t.Errorf("de.GeneratedBy.Parameters = %+v, want [{alpha 0.05}]", de.GeneratedBy.Parameters)
	}
	if len(de.GeneratedBy.Environment) != 1 || de.GeneratedBy.Environment[0] != (LineageEnvironment{Kind: "container", ID: envID}) {
		t.Errorf("de.GeneratedBy.Environment = %+v, want [{container %s}]", de.GeneratedBy.Environment, envID)
	}
}

func TestBuildArtifactLineage_ResultValueEntity(t *testing.T) {
	// Spec "Parameter values" / design D3: a result is a value entity
	// generated by the activity with hadRole = the result's name; it shows
	// up in generated_by.results for every artifact the same activity
	// generates.
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "results/de.csv", "de\n")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")
	deID := GitBlobEntityID(mustBlobID(t, run, "results/de.csv"))
	resultID, err := ValueEntityID("score", 100)
	if err != nil {
		t.Fatalf("ValueEntityID() error = %v", err)
	}
	actID := NewActivityID()
	genID := NewGenerationID()

	graph := fmt.Sprintf(`
{"@id": %q, "@type": "Activity"},
{"@id": %q, "@type": "Entity", "wasGeneratedBy": %q},
{"@id": %q, "@type": "Entity", "wasGeneratedBy": %q, "qualifiedGeneration": %q, "value": 100},
{"@id": %q, "@type": "Generation", "activity": %q, "hadRole": "score"}`,
		actID,
		deID, actID,
		resultID, actID, genID,
		genID, actID)
	rec := minVersionRecordJSON(NewRecordID(), "recorded", "urn:exohub:agent:person:alice", firstMinVersionRelease, graph)
	writeRepoFile(t, root, RecordDir+"/r.jsonld", rec)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record")

	result, report, err := CompileBundle(NewRefReader(root, "HEAD"), []string{"results/de.csv"})
	if err != nil {
		t.Fatalf("CompileBundle() error = %v", err)
	}
	if report.HasErrors() {
		t.Fatalf("HasErrors(): %+v", report.Findings)
	}

	de := lineageFor(t, result, "results/de.csv")
	if de.GeneratedBy == nil {
		t.Fatal("de.GeneratedBy = nil")
	}
	wantValue := float64(100)
	if len(de.GeneratedBy.Results) != 1 || de.GeneratedBy.Results[0].Name != "score" || de.GeneratedBy.Results[0].Value != wantValue {
		t.Errorf("de.GeneratedBy.Results = %+v, want [{score 100}]", de.GeneratedBy.Results)
	}
}

func TestBuildArtifactLineage_PlanPointerAndSource(t *testing.T) {
	// Spec "Step code pointers" and design D6: the plan entity's
	// exo:checksum/exo:repository/exo:commit flatten onto
	// generated_by.plan, and the activity's hadPrimarySource flattens onto
	// generated_by.source.
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "out.csv", "out\n")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")
	outID := GitBlobEntityID(mustBlobID(t, run, "out.csv"))
	actID := NewActivityID()
	planID := NewActivityID() // any opaque plan id; not a content id
	sourceID := "lid://some-run-id"

	graph := fmt.Sprintf(`
{"@id": %q, "@type": "Entity", "wasGeneratedBy": %q},
{"@id": %q, "@type": "Activity", "hadPlan": %q, "hadPrimarySource": %q},
{"@id": %q, "@type": "Entity", "label": "pipeline.nf", "exo:checksum": "sha256:deadbeef", "exo:repository": "https://example.org/org/pipeline", "exo:commit": "4f2a9c1"}`,
		outID, actID,
		actID, planID, sourceID,
		planID)
	rec := minVersionRecordJSON(NewRecordID(), "recorded", "urn:exohub:agent:person:alice", firstMinVersionRelease, graph)
	writeRepoFile(t, root, RecordDir+"/r.jsonld", rec)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record")

	result, report, err := CompileBundle(NewRefReader(root, "HEAD"), []string{"out.csv"})
	if err != nil {
		t.Fatalf("CompileBundle() error = %v", err)
	}
	if report.HasErrors() {
		t.Fatalf("HasErrors(): %+v", report.Findings)
	}

	out := lineageFor(t, result, "out.csv")
	if out.GeneratedBy == nil {
		t.Fatal("out.GeneratedBy = nil")
	}
	if out.GeneratedBy.Source != sourceID {
		t.Errorf("out.GeneratedBy.Source = %q, want %q", out.GeneratedBy.Source, sourceID)
	}
	plan := out.GeneratedBy.Plan
	if plan == nil || plan.Checksum != "sha256:deadbeef" || plan.Repository != "https://example.org/org/pipeline" || plan.Commit != "4f2a9c1" {
		t.Errorf("out.GeneratedBy.Plan = %+v, want checksum/repository/commit set", plan)
	}
	if plan != nil && plan.URL != "" {
		t.Errorf("out.GeneratedBy.Plan.URL = %q, want empty when repository+commit are set", plan.URL)
	}
}

func TestBuildArtifactLineage_PlanURLWhenOnlyAddressable(t *testing.T) {
	// Design D5 option 4: an addressable URL with no repository/commit and
	// no content id becomes generated_by.plan.url.
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "out.csv", "out\n")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")
	outID := GitBlobEntityID(mustBlobID(t, run, "out.csv"))
	actID := NewActivityID()
	planID := "https://example.org/pipeline/main.nf"

	graph := fmt.Sprintf(`
{"@id": %q, "@type": "Entity", "wasGeneratedBy": %q},
{"@id": %q, "@type": "Activity", "hadPlan": %q},
{"@id": %q, "@type": "Entity", "label": "main.nf"}`,
		outID, actID,
		actID, planID,
		planID)
	rec := minimalRecordJSON(NewRecordID(), "recorded", "urn:exohub:agent:person:alice", graph)
	writeRepoFile(t, root, RecordDir+"/r.jsonld", rec)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record")

	result, report, err := CompileBundle(NewRefReader(root, "HEAD"), []string{"out.csv"})
	if err != nil {
		t.Fatalf("CompileBundle() error = %v", err)
	}
	if report.HasErrors() {
		t.Fatalf("HasErrors(): %+v", report.Findings)
	}

	out := lineageFor(t, result, "out.csv")
	if out.GeneratedBy == nil || out.GeneratedBy.Plan == nil {
		t.Fatal("out.GeneratedBy.Plan = nil")
	}
	if out.GeneratedBy.Plan.URL != planID {
		t.Errorf("out.GeneratedBy.Plan.URL = %q, want %q", out.GeneratedBy.Plan.URL, planID)
	}
}

func TestBuildArtifactLineage_RecordsWithoutNewTermsProduceUnchangedDocuments(t *testing.T) {
	// Spec "Records without new terms produce unchanged documents": a
	// record using only original v1 terms gets no role, parameters,
	// environment, results, plan pointer or source fields.
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "input.csv", "input\n")
	writeRepoFile(t, root, "out.csv", "out\n")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")
	inputID := GitBlobEntityID(mustBlobID(t, run, "input.csv"))
	outID := GitBlobEntityID(mustBlobID(t, run, "out.csv"))
	actID := NewActivityID()
	planID := GitBlobEntityID(mustBlobID(t, run, "input.csv")) // reuse a content id as the plan

	graph := fmt.Sprintf(`
{"@id": %q, "@type": "Entity"},
{"@id": %q, "@type": "Activity", "used": %q, "hadPlan": %q},
{"@id": %q, "@type": "Entity", "wasGeneratedBy": %q}`,
		inputID,
		actID, inputID, planID,
		outID, actID)
	rec := minimalRecordJSON(NewRecordID(), "recorded", "urn:exohub:agent:person:alice", graph)
	writeRepoFile(t, root, RecordDir+"/r.jsonld", rec)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record")

	result, report, err := CompileBundle(NewRefReader(root, "HEAD"), []string{"input.csv", "out.csv"})
	if err != nil {
		t.Fatalf("CompileBundle() error = %v", err)
	}
	if report.HasErrors() {
		t.Fatalf("HasErrors(): %+v", report.Findings)
	}

	out := lineageFor(t, result, "out.csv")
	if out.GeneratedBy == nil {
		t.Fatal("out.GeneratedBy = nil")
	}
	if out.GeneratedBy.Source != "" {
		t.Errorf("out.GeneratedBy.Source = %q, want empty", out.GeneratedBy.Source)
	}
	if len(out.GeneratedBy.Parameters) != 0 {
		t.Errorf("out.GeneratedBy.Parameters = %+v, want none", out.GeneratedBy.Parameters)
	}
	if len(out.GeneratedBy.Results) != 0 {
		t.Errorf("out.GeneratedBy.Results = %+v, want none", out.GeneratedBy.Results)
	}
	if len(out.GeneratedBy.Environment) != 0 {
		t.Errorf("out.GeneratedBy.Environment = %+v, want none", out.GeneratedBy.Environment)
	}
	if out.GeneratedBy.Plan == nil {
		t.Fatal("out.GeneratedBy.Plan = nil")
	}
	if out.GeneratedBy.Plan.Checksum != "" || out.GeneratedBy.Plan.Repository != "" || out.GeneratedBy.Plan.Commit != "" || out.GeneratedBy.Plan.URL != "" {
		t.Errorf("out.GeneratedBy.Plan = %+v, want no new pointer fields", out.GeneratedBy.Plan)
	}
	if len(out.Inputs) != 1 || out.Inputs[0].Role != "" {
		t.Errorf("out.Inputs = %+v, want role empty", out.Inputs)
	}
}

func TestBuildArtifactLineage_ObjectValuedParameterCanonicalJSON(t *testing.T) {
	// P1/!315 review clarification: an array or object parameter value is
	// emitted as its canonical JSON string in the catalog document (ES
	// rejects object values at index time), while the compiled
	// provenance.jsonld keeps the original typed value.
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "out.csv", "out\n")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")
	outID := GitBlobEntityID(mustBlobID(t, run, "out.csv"))
	actID := NewActivityID()
	usageID := NewUsageID()
	valueID, err := ValueEntityID("options", map[string]any{"a": float64(1)})
	if err != nil {
		t.Fatalf("ValueEntityID() error = %v", err)
	}

	graph := fmt.Sprintf(`
{"@id": %q, "@type": "Entity", "wasGeneratedBy": %q},
{"@id": %q, "@type": "Activity", "used": [%q], "qualifiedUsage": %q},
{"@id": %q, "@type": "Usage", "entity": %q, "hadRole": "options"},
{"@id": %q, "@type": "Entity", "value": {"a": 1}}`,
		outID, actID,
		actID, valueID, usageID,
		usageID, valueID,
		valueID)
	rec := minVersionRecordJSON(NewRecordID(), "recorded", "urn:exohub:agent:person:alice", firstMinVersionRelease, graph)
	writeRepoFile(t, root, RecordDir+"/r.jsonld", rec)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record")

	result, report, err := CompileBundle(NewRefReader(root, "HEAD"), []string{"out.csv"})
	if err != nil {
		t.Fatalf("CompileBundle() error = %v", err)
	}
	if report.HasErrors() {
		t.Fatalf("HasErrors(): %+v", report.Findings)
	}

	out := lineageFor(t, result, "out.csv")
	if out.GeneratedBy == nil || len(out.GeneratedBy.Parameters) != 1 {
		t.Fatalf("out.GeneratedBy.Parameters = %+v, want one entry", out.GeneratedBy)
	}
	got, ok := out.GeneratedBy.Parameters[0].Value.(string)
	if !ok {
		t.Fatalf("Parameters[0].Value = %#v (%T), want a JSON string", out.GeneratedBy.Parameters[0].Value, out.GeneratedBy.Parameters[0].Value)
	}
	if got != `{"a":1}` {
		t.Errorf("Parameters[0].Value = %q, want {\"a\":1}", got)
	}

	// The compiled provenance.jsonld keeps the original typed object.
	for _, n := range result.Document.Graph {
		if n.ID() == valueID {
			v, ok := n["value"].(map[string]any)
			if !ok {
				t.Fatalf("compiled graph value = %#v, want the original typed object", n["value"])
			}
			if v["a"] != float64(1) {
				t.Errorf("compiled graph value = %+v, want a=1", v)
			}
		}
	}
}

func TestBuildProvenanceDocument_ValueAndQualifiedRefs(t *testing.T) {
	// Tasks.md 4.2: provenance.json's flattened entities carry value, and
	// activities carry qualified inputs/outputs with roles.
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "out.csv", "out\n")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")
	outID := GitBlobEntityID(mustBlobID(t, run, "out.csv"))
	actID := NewActivityID()
	usageID := NewUsageID()
	valueID, err := ValueEntityID("alpha", 0.05)
	if err != nil {
		t.Fatalf("ValueEntityID() error = %v", err)
	}

	graph := fmt.Sprintf(`
{"@id": %q, "@type": "Entity", "wasGeneratedBy": %q},
{"@id": %q, "@type": "Activity", "used": [%q], "qualifiedUsage": %q},
{"@id": %q, "@type": "Usage", "entity": %q, "hadRole": "alpha"},
{"@id": %q, "@type": "Entity", "value": 0.05}`,
		outID, actID,
		actID, valueID, usageID,
		usageID, valueID,
		valueID)
	rec := minVersionRecordJSON(NewRecordID(), "recorded", "urn:exohub:agent:person:alice", firstMinVersionRelease, graph)
	writeRepoFile(t, root, RecordDir+"/r.jsonld", rec)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record")

	result, report, err := CompileBundle(NewRefReader(root, "HEAD"), []string{"out.csv"})
	if err != nil {
		t.Fatalf("CompileBundle() error = %v", err)
	}
	if report.HasErrors() {
		t.Fatalf("HasErrors(): %+v", report.Findings)
	}

	var valueEntity *ProvenanceEntity
	for i := range result.Catalog.Entities {
		if result.Catalog.Entities[i].ID == valueID {
			valueEntity = &result.Catalog.Entities[i]
		}
	}
	if valueEntity == nil {
		t.Fatalf("no provenance.json entity for %s, got %+v", valueID, result.Catalog.Entities)
	}
	if valueEntity.Value != float64(0.05) {
		t.Errorf("valueEntity.Value = %#v, want 0.05", valueEntity.Value)
	}

	var act *ProvenanceActivity
	for i := range result.Catalog.Activities {
		if result.Catalog.Activities[i].ID == actID {
			act = &result.Catalog.Activities[i]
		}
	}
	if act == nil {
		t.Fatalf("no provenance.json activity for %s", actID)
	}
	if len(act.Inputs) != 1 || act.Inputs[0] != (ProvenanceQualifiedRef{ID: valueID, Role: "alpha"}) {
		t.Errorf("act.Inputs = %+v, want [{%s alpha}]", act.Inputs, valueID)
	}
}
