package provenance

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestBuildLineageDocument_IncludesContextAndGraph(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "a.csv", "a")
	writeRepoFile(t, root, "b.csv", "b")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	reader := NewWorktreeReader(root)
	aID, _ := reader.ResolveContentID("a.csv")
	bID, _ := reader.ResolveContentID("b.csv")

	writeRepoFile(t, root, RecordDir+"/r1.jsonld", entityRecord(bID, "wasDerivedFrom", aID))
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record")

	lineage, err := BuildLineage(NewRefReader(root, "HEAD"), "b.csv", 0)
	if err != nil {
		t.Fatalf("BuildLineage() error = %v", err)
	}
	doc, err := BuildLineageDocument(lineage)
	if err != nil {
		t.Fatalf("BuildLineageDocument() error = %v", err)
	}
	if doc.Context == nil {
		t.Error("Context is nil, want the embedded context object")
	}
	// Review item 2 on !257: embedding the whole context document (itself
	// shaped {"@context": {...}}) as the value of this document's own
	// "@context" key double-wraps it; ContextObject() must return the inner
	// term table instead.
	if ctxMap, ok := doc.Context.(map[string]any); ok {
		if _, nested := ctxMap["@context"]; nested {
			t.Error("Context contains a nested @context key, want the inner term table only")
		}
	}
	if doc.Subject != bID {
		t.Errorf("Subject = %q, want %q", doc.Subject, bID)
	}
	if len(doc.Graph) == 0 {
		t.Fatal("Graph is empty, want the subject node")
	}
	found := false
	for _, n := range doc.Graph {
		if n.ID() == bID {
			found = true
			fromRecord, ok := n["exo:fromRecord"]
			if !ok {
				t.Error("subject node missing exo:fromRecord")
			}
			_ = fromRecord
		}
	}
	if !found {
		t.Errorf("Graph = %+v, want a node for %s", doc.Graph, bID)
	}

	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var roundtrip map[string]any
	if err := json.Unmarshal(data, &roundtrip); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if _, ok := roundtrip["@context"]; !ok {
		t.Error("marshaled document missing @context")
	}
	if _, ok := roundtrip["@graph"]; !ok {
		t.Error("marshaled document missing @graph")
	}
}

func TestBuildLineageDocument_SetsPathFromRef(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "old.csv", "same content")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	contentID, err := NewWorktreeReader(root).ResolveContentID("old.csv")
	if err != nil {
		t.Fatalf("ResolveContentID() error = %v", err)
	}
	writeRepoFile(t, root, RecordDir+"/r1.jsonld", entityRecord(contentID, "wasGeneratedBy", NewActivityID()))
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record")

	run("git", "mv", "old.csv", "new.csv")
	run("git", "commit", "-q", "-m", "rename")

	lineage, err := BuildLineage(NewRefReader(root, "HEAD"), "new.csv", 0)
	if err != nil {
		t.Fatalf("BuildLineage() error = %v", err)
	}
	doc, err := BuildLineageDocument(lineage)
	if err != nil {
		t.Fatalf("BuildLineageDocument() error = %v", err)
	}

	// Review item 1: "in --json, set exo:path to the path at the ref, the
	// same rule #121 uses for compilation (D8 step 6, D15)" — the record was
	// never updated, so without the override this would still read
	// "old.csv".
	var found bool
	for _, n := range doc.Graph {
		if n.ID() != contentID {
			continue
		}
		found = true
		if path, _ := n["exo:path"].(string); path != "new.csv" {
			t.Errorf("exo:path = %q, want %q (the path at the ref, not the recorded one)", path, "new.csv")
		}
	}
	if !found {
		t.Fatalf("Graph = %+v, want a node for %s", doc.Graph, contentID)
	}
}

// TestBuildLineageDocument_RecordsShapeMatchesCompiledDocument is a
// regression test for review item 3 on !257: exo:records must be a list of
// {@id, exo:assertion, exo:assertedBy, generatedAtTime} entries, the same
// shape design D15 defines for #121's compiled provenance.jsonld, not a
// map keyed by Go field names.
func TestBuildLineageDocument_RecordsShapeMatchesCompiledDocument(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "a.csv", "a")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	aID, err := NewWorktreeReader(root).ResolveContentID("a.csv")
	if err != nil {
		t.Fatalf("ResolveContentID() error = %v", err)
	}
	recID := NewRecordID()
	writeRepoFile(t, root, RecordDir+"/r1.jsonld", minimalRecordJSON(recID, "recorded", "urn:exohub:agent:person:alice",
		fmt.Sprintf(`{"@id": %q, "@type": "Entity", "wasGeneratedBy": %q}`, aID, NewActivityID())))
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record")

	lineage, err := BuildLineage(NewRefReader(root, "HEAD"), "a.csv", 0)
	if err != nil {
		t.Fatalf("BuildLineage() error = %v", err)
	}
	doc, err := BuildLineageDocument(lineage)
	if err != nil {
		t.Fatalf("BuildLineageDocument() error = %v", err)
	}

	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var roundtrip map[string]any
	if err := json.Unmarshal(data, &roundtrip); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	entries, ok := roundtrip["exo:records"].([]any)
	if !ok {
		t.Fatalf("exo:records = %T %v, want a list", roundtrip["exo:records"], roundtrip["exo:records"])
	}
	if len(entries) != 1 {
		t.Fatalf("exo:records = %+v, want exactly one entry", entries)
	}
	entry, ok := entries[0].(map[string]any)
	if !ok {
		t.Fatalf("exo:records[0] = %T, want an object", entries[0])
	}
	for _, key := range []string{"@id", "exo:assertion", "exo:assertedBy", "generatedAtTime"} {
		if _, ok := entry[key]; !ok {
			t.Errorf("exo:records[0] = %+v, missing %q", entry, key)
		}
	}
	if entry["@id"] != recID {
		t.Errorf("exo:records[0][@id] = %v, want %q", entry["@id"], recID)
	}
	// The old shape was a map keyed by Go field names ("Path", "Assertion",
	// ...); make sure neither leaked through.
	if _, ok := entry["Path"]; ok {
		t.Error("exo:records entry still has the old Go field name \"Path\"")
	}
}

// TestBuildLineageDocument_IncludesAssertingAgentWithNoNode is a regression
// test for review item 4 on !257: a record's envelope exo:assertedBy agent
// must appear in --json's @graph even when no record ever defines a node
// for it.
func TestBuildLineageDocument_IncludesAssertingAgentWithNoNode(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "out.csv", "out")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	contentID, err := NewWorktreeReader(root).ResolveContentID("out.csv")
	if err != nil {
		t.Fatalf("ResolveContentID() error = %v", err)
	}
	botID := SoftwareAgentID("bot")
	writeRepoFile(t, root, RecordDir+"/r1.jsonld", minimalRecordJSON(NewRecordID(), "recorded", botID,
		fmt.Sprintf(`{"@id": %q, "@type": "Entity", "wasGeneratedBy": %q}`, contentID, NewActivityID())))
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record")

	lineage, err := BuildLineage(NewRefReader(root, "HEAD"), "out.csv", 0)
	if err != nil {
		t.Fatalf("BuildLineage() error = %v", err)
	}
	doc, err := BuildLineageDocument(lineage)
	if err != nil {
		t.Fatalf("BuildLineageDocument() error = %v", err)
	}

	var found bool
	for _, n := range doc.Graph {
		if n.ID() == botID {
			found = true
		}
	}
	if !found {
		t.Errorf("Graph = %+v, want a node for the asserting agent %s", doc.Graph, botID)
	}
}

func TestBuildLineageDocument_NoRecords(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "data.csv", "v1")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	lineage, err := BuildLineage(NewRefReader(root, "HEAD"), "data.csv", 0)
	if err != nil {
		t.Fatalf("BuildLineage() error = %v", err)
	}
	doc, err := BuildLineageDocument(lineage)
	if err != nil {
		t.Fatalf("BuildLineageDocument() error = %v", err)
	}
	if len(doc.Graph) != 0 {
		t.Errorf("Graph = %+v, want empty (no records)", doc.Graph)
	}
}
