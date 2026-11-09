package provenance

import (
	"fmt"
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
