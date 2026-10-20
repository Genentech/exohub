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
		if member.Collection == nil || member.Collection.ID != tree2 || member.Collection.Path != "data/expr.tdb/" {
			t.Errorf("%s: Collection = %+v, want {%s, data/expr.tdb/}", path, member.Collection, tree2)
		}
		if member.GeneratedBy == nil || member.GeneratedBy.ID != act2 {
			t.Errorf("%s: GeneratedBy = %+v, want activity %s", path, member.GeneratedBy, act2)
		}
		if len(member.RevisionOf) != 1 || member.RevisionOf[0] != tree1 {
			t.Errorf("%s: RevisionOf = %v, want [%s]", path, member.RevisionOf, tree1)
		}
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
