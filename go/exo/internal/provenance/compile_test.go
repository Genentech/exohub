package provenance

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

func mustBlobID(t *testing.T, run func(args ...string) string, path string) string {
	t.Helper()
	return strings.TrimSpace(run("git", "rev-parse", "HEAD:"+path))
}

func TestCompileBundle_NoRecords(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "a.txt", "hello\n")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	result, report, err := CompileBundle(NewRefReader(root, "HEAD"), []string{"a.txt"})
	if err != nil {
		t.Fatalf("CompileBundle() error = %v", err)
	}
	if report.HasErrors() {
		t.Fatalf("HasErrors() = true, findings = %+v", report.Findings)
	}
	if result.Document != nil {
		t.Errorf("Document = %+v, want nil (no records)", result.Document)
	}
}

func TestCompileBundle_InvalidRecordFailsBeforeOutput(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "a.txt", "hello\n")
	writeRepoFile(t, root, RecordDir+"/bad.jsonld", "not json")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	result, report, err := CompileBundle(NewRefReader(root, "HEAD"), []string{"a.txt"})
	if err != nil {
		t.Fatalf("CompileBundle() error = %v", err)
	}
	if !report.HasErrors() {
		t.Fatal("HasErrors() = false, want true")
	}
	if result != nil {
		t.Errorf("result = %+v, want nil", result)
	}
}

func TestCompileBundle_SupersededRecordDropped(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "a.txt", "hello\n")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")
	entityID := GitBlobEntityID(mustBlobID(t, run, "a.txt"))

	id1 := NewRecordID()
	id2 := NewRecordID()
	rec1 := minimalRecordJSON(id1, "recorded", "urn:exohub:agent:person:alice",
		fmt.Sprintf(`{"@id": %q, "@type": "Entity", "label": "old"}`, entityID))
	rec2 := fmt.Sprintf(`{
  "@context": %q,
  "@id": %q,
  "exo:assertion": "recorded",
  "exo:assertedBy": "urn:exohub:agent:person:alice",
  "generatedAtTime": "2026-10-14T11:00:00Z",
  "exo:supersedes": [%q],
  "@graph": [{"@id": %q, "@type": "Entity", "label": "new"}]
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
		t.Fatalf("HasErrors() = true: %+v", report.Findings)
	}
	if result.Document == nil {
		t.Fatal("Document = nil, want non-nil")
	}
	if len(result.Document.Records) != 1 || result.Document.Records[0].ID != id2 {
		t.Errorf("Records = %+v, want only %s", result.Document.Records, id2)
	}
	if len(result.Document.Graph) != 1 {
		t.Fatalf("Graph = %+v, want 1 node", result.Document.Graph)
	}
	node := result.Document.Graph[0]
	if node["label"] != "new" {
		t.Errorf("label = %v, want %q", node["label"], "new")
	}
	fromRecord, _ := node["exo:fromRecord"].([]string)
	if len(fromRecord) != 1 || fromRecord[0] != id2 {
		t.Errorf("exo:fromRecord = %v, want [%s]", fromRecord, id2)
	}
	if node["exo:path"] != "a.txt" {
		t.Errorf("exo:path = %v, want \"a.txt\"", node["exo:path"])
	}
}

func TestCompileBundle_UpstreamNodeGetsPathAtRefOutsideSelection(t *testing.T) {
	// raw/in.csv is present at the ref but excluded from the bundle's
	// selection (for example a path-filtered bundle of only "results"), so
	// it must still show its ref path (design D8.5 step 6), not be stripped
	// of it just because it is upstream (review MR !258: "Upstream nodes
	// lose their path").
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "raw/in.csv", "input\n")
	writeRepoFile(t, root, "results/out.csv", "result\n")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")
	inID := GitBlobEntityID(mustBlobID(t, run, "raw/in.csv"))
	outID := GitBlobEntityID(mustBlobID(t, run, "results/out.csv"))

	recID := NewRecordID()
	graph := fmt.Sprintf(`{"@id": %q, "@type": "Entity", "wasDerivedFrom": %q}, {"@id": %q, "@type": "Entity"}`,
		outID, inID, inID)
	rec := minimalRecordJSON(recID, "recorded", "urn:exohub:agent:person:alice", graph)
	writeRepoFile(t, root, RecordDir+"/r.jsonld", rec)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record")

	// Only results/out.csv is selected: raw/in.csv is upstream and outside
	// the bundle's own path filter, but still present at the ref.
	result, report, err := CompileBundle(NewRefReader(root, "HEAD"), []string{"results/out.csv"})
	if err != nil {
		t.Fatalf("CompileBundle() error = %v", err)
	}
	if report.HasErrors() {
		t.Fatalf("HasErrors(): %+v", report.Findings)
	}
	var upstream Node
	for _, n := range result.Document.Graph {
		if n.ID() == inID {
			upstream = n
		}
	}
	if upstream == nil {
		t.Fatal("upstream node not kept")
	}
	if upstream["exo:path"] != "raw/in.csv" {
		t.Errorf("exo:path = %v, want \"raw/in.csv\"", upstream["exo:path"])
	}
}

func TestCompileBundle_KeepsUpstreamOutsideBundle(t *testing.T) {
	// The upstream entity's content is gone everywhere at the ref, so its
	// exo:path cannot be resolved there: the node keeps its recorded
	// exo:path, if the record gave it one (review MR !258: "keeps its
	// recorded exo:path, if any" — not stripped of exo:path altogether).
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "out.csv", "result\n")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")
	outID := GitBlobEntityID(mustBlobID(t, run, "out.csv"))
	// Looks like a real git blob id, but is not present in this repository
	// (design D8.5 "derived from content that is no longer present at the
	// ref").
	upstreamID := GitBlobEntityID("deadbeefdeadbeefdeadbeefdeadbeefdeadbeef")

	recID := NewRecordID()
	graph := fmt.Sprintf(`{"@id": %q, "@type": "Entity", "wasDerivedFrom": %q}, {"@id": %q, "@type": "Entity", "label": "gone", "exo:path": "raw/old-in.csv"}`,
		outID, upstreamID, upstreamID)
	rec := minimalRecordJSON(recID, "recorded", "urn:exohub:agent:person:alice", graph)
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
	if len(result.Document.Graph) != 2 {
		t.Fatalf("Graph = %+v, want 2 nodes (selected + upstream)", result.Document.Graph)
	}
	var upstream Node
	for _, n := range result.Document.Graph {
		if n.ID() == upstreamID {
			upstream = n
		}
	}
	if upstream == nil {
		t.Fatal("upstream node not kept")
	}
	if upstream["exo:path"] != "raw/old-in.csv" {
		t.Errorf("exo:path = %v, want the recorded \"raw/old-in.csv\" to be kept", upstream["exo:path"])
	}
	if upstream["label"] != "gone" {
		t.Errorf("upstream label = %v, want \"gone\"", upstream["label"])
	}
}

func TestCompileBundle_FolderCollectionInScope(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "data/expr.tdb/f1.bin", "one\n")
	writeRepoFile(t, root, "data/expr.tdb/f2.bin", "two\n")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")
	treeID := GitTreeEntityID(mustBlobID(t, run, "data/expr.tdb"))

	recID := NewRecordID()
	graph := fmt.Sprintf(`{"@id": %q, "@type": "Collection", "label": "experiment table"}`, treeID)
	rec := minimalRecordJSON(recID, "recorded", "urn:exohub:agent:person:alice", graph)
	writeRepoFile(t, root, RecordDir+"/r.jsonld", rec)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record")

	result, report, err := CompileBundle(NewRefReader(root, "HEAD"), []string{"data/expr.tdb/f1.bin", "data/expr.tdb/f2.bin"})
	if err != nil {
		t.Fatalf("CompileBundle() error = %v", err)
	}
	if report.HasErrors() {
		t.Fatalf("HasErrors(): %+v", report.Findings)
	}
	if len(result.Document.Graph) != 1 {
		t.Fatalf("Graph = %+v, want 1 node (the folder collection)", result.Document.Graph)
	}
	node := result.Document.Graph[0]
	if node.ID() != treeID {
		t.Errorf("node id = %s, want %s", node.ID(), treeID)
	}
	if node["exo:path"] != "data/expr.tdb/" {
		t.Errorf("exo:path = %v, want \"data/expr.tdb/\"", node["exo:path"])
	}
}

func TestCompileBundle_DeterministicOrdering(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "b.txt", "b\n")
	writeRepoFile(t, root, "a.txt", "a\n")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")
	aID := GitBlobEntityID(mustBlobID(t, run, "a.txt"))
	bID := GitBlobEntityID(mustBlobID(t, run, "b.txt"))

	recID := NewRecordID()
	graph := fmt.Sprintf(`{"@id": %q, "@type": "Entity"}, {"@id": %q, "@type": "Entity"}`, bID, aID)
	rec := minimalRecordJSON(recID, "recorded", "urn:exohub:agent:person:alice", graph)
	writeRepoFile(t, root, RecordDir+"/r.jsonld", rec)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record")

	result, report, err := CompileBundle(NewRefReader(root, "HEAD"), []string{"a.txt", "b.txt"})
	if err != nil {
		t.Fatalf("CompileBundle() error = %v", err)
	}
	if report.HasErrors() {
		t.Fatalf("HasErrors(): %+v", report.Findings)
	}
	if len(result.Document.Graph) != 2 {
		t.Fatalf("Graph = %+v, want 2 nodes", result.Document.Graph)
	}
	gotIDs := []string{result.Document.Graph[0].ID(), result.Document.Graph[1].ID()}
	wantIDs := []string{aID, bID}
	sort.Strings(wantIDs)
	if gotIDs[0] != wantIDs[0] || gotIDs[1] != wantIDs[1] {
		t.Errorf("Graph order = %v, want sorted %v", gotIDs, wantIDs)
	}

	data, err := result.Document.Serialize()
	if err != nil {
		t.Fatalf("Serialize() error = %v", err)
	}
	if !strings.HasSuffix(string(data), "\n") {
		t.Error("Serialize() output does not end with a newline")
	}
}

func TestCompileBundle_AssertingAgentAndOnBehalfOfChainIncluded(t *testing.T) {
	// The entity's own node never references the agent (only the record
	// envelope's exo:assertedBy does), which is how `exo provenance add`
	// records a simple run today. Attribution must still pull the agent
	// node, and its actedOnBehalfOf chain, into scope (review MR !258: "The
	// asserting agent is missing from @graph").
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "out.csv", "result\n")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")
	outID := GitBlobEntityID(mustBlobID(t, run, "out.csv"))
	botID := "urn:exohub:agent:software:bot"
	aliceID := "urn:exohub:agent:person:alice"

	recID := NewRecordID()
	graph := fmt.Sprintf(`{"@id": %q, "@type": "Entity"}, {"@id": %q, "@type": "SoftwareAgent", "exo:model": "gpt", "actedOnBehalfOf": %q}, {"@id": %q, "@type": "Person"}`,
		outID, botID, aliceID, aliceID)
	rec := minimalRecordJSON(recID, "recorded", botID, graph)
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
	if len(result.Document.Graph) != 3 {
		t.Fatalf("Graph = %+v, want 3 nodes (entity, asserting agent, on-behalf-of chain)", result.Document.Graph)
	}
	ids := map[string]bool{}
	for _, n := range result.Document.Graph {
		ids[n.ID()] = true
	}
	for _, want := range []string{outID, botID, aliceID} {
		if !ids[want] {
			t.Errorf("Graph missing %s, got ids %v", want, ids)
		}
	}
}

func TestCompileBundle_RecordsListOnlyContributingRecords(t *testing.T) {
	// A record about a file that is neither selected nor upstream-reachable
	// from a selected artifact must not appear in exo:records, and must not
	// count toward RecordCount (review MR !258: "exo:records must list only
	// contributing records").
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "a.txt", "a\n")
	writeRepoFile(t, root, "other.txt", "other\n")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")
	aID := GitBlobEntityID(mustBlobID(t, run, "a.txt"))
	otherID := GitBlobEntityID(mustBlobID(t, run, "other.txt"))

	recA := NewRecordID()
	graphA := fmt.Sprintf(`{"@id": %q, "@type": "Entity"}`, aID)
	recOther := NewRecordID()
	graphOther := fmt.Sprintf(`{"@id": %q, "@type": "Entity"}`, otherID)
	writeRepoFile(t, root, RecordDir+"/a.jsonld", minimalRecordJSON(recA, "recorded", "urn:exohub:agent:person:alice", graphA))
	writeRepoFile(t, root, RecordDir+"/other.jsonld", minimalRecordJSON(recOther, "recorded", "urn:exohub:agent:person:alice", graphOther))
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "records")

	// Only a.txt is selected: other.txt's record must not contribute.
	result, report, err := CompileBundle(NewRefReader(root, "HEAD"), []string{"a.txt"})
	if err != nil {
		t.Fatalf("CompileBundle() error = %v", err)
	}
	if report.HasErrors() {
		t.Fatalf("HasErrors(): %+v", report.Findings)
	}
	if len(result.Document.Graph) != 1 || result.Document.Graph[0].ID() != aID {
		t.Errorf("Graph = %+v, want only %s", result.Document.Graph, aID)
	}
	if len(result.Document.Records) != 1 || result.Document.Records[0].ID != recA {
		t.Errorf("Records = %+v, want only %s", result.Document.Records, recA)
	}
	if result.RecordCount != 1 {
		t.Errorf("RecordCount = %d, want 1", result.RecordCount)
	}
}

func TestCompileBundle_ContextIsNotNested(t *testing.T) {
	// ContextObject() currently returns the whole embedded context
	// document ({"@context": {...}}), which CompiledDocument then nests a
	// second time under its own "@context" key, producing invalid JSON-LD:
	// {"@context": {"@context": {...}}}. The fix lives in
	// internal/provenance/context.go, assigned to #122 (MR !257); this test
	// is expected to fail until !257 lands and this MR rebases onto it
	// (review MR !258, held pending that rebase).
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "a.txt", "hello\n")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")
	entityID := GitBlobEntityID(mustBlobID(t, run, "a.txt"))

	recID := NewRecordID()
	graph := fmt.Sprintf(`{"@id": %q, "@type": "Entity"}`, entityID)
	rec := minimalRecordJSON(recID, "recorded", "urn:exohub:agent:person:alice", graph)
	writeRepoFile(t, root, RecordDir+"/r.jsonld", rec)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record")

	result, report, err := CompileBundle(NewRefReader(root, "HEAD"), []string{"a.txt"})
	if err != nil {
		t.Fatalf("CompileBundle() error = %v", err)
	}
	if report.HasErrors() {
		t.Fatalf("HasErrors(): %+v", report.Findings)
	}

	ctx, ok := result.Document.Context.(map[string]any)
	if !ok {
		t.Fatalf("Context = %#v, want map[string]any", result.Document.Context)
	}
	if _, nested := ctx["@context"]; nested {
		t.Errorf("Context has a nested @context key, want a flat context object: %+v", ctx)
	}
	if _, ok := ctx["prov"]; !ok {
		t.Errorf("Context missing %q prefix: %+v", "prov", ctx)
	}
	if _, ok := ctx["exo"]; !ok {
		t.Errorf("Context missing %q prefix: %+v", "exo", ctx)
	}
}

func TestRequireViolations_RestrictedToSelection(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, ".exohub/config", "provenance:\n  require:\n    - \"results/**\"\n")
	writeRepoFile(t, root, "results/a.csv", "a\n")
	writeRepoFile(t, root, "results/b.csv", "b\n")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	// results/b.csv is also uncovered but outside the bundle's selection,
	// so it must not be reported (design D15 "Bundle scope").
	violations, err := RequireViolations(NewRefReader(root, "HEAD"), []string{"results/a.csv"})
	if err != nil {
		t.Fatalf("RequireViolations() error = %v", err)
	}
	if len(violations) != 1 || violations[0] != "results/a.csv" {
		t.Errorf("violations = %v, want [results/a.csv]", violations)
	}
}

func TestRequireViolations_CoveredFileNoViolation(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, ".exohub/config", "provenance:\n  require:\n    - \"results/**\"\n")
	writeRepoFile(t, root, "results/a.csv", "a\n")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")
	entityID := GitBlobEntityID(mustBlobID(t, run, "results/a.csv"))

	recID := NewRecordID()
	graph := fmt.Sprintf(`{"@id": %q, "@type": "Entity", "wasGeneratedBy": "urn:exohub:activity:%s"}`, entityID, NewActivityID()[len(ActivityURNPrefix):])
	rec := minimalRecordJSON(recID, "recorded", "urn:exohub:agent:person:alice", graph)
	writeRepoFile(t, root, RecordDir+"/r.jsonld", rec)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record")

	violations, err := RequireViolations(NewRefReader(root, "HEAD"), []string{"results/a.csv"})
	if err != nil {
		t.Fatalf("RequireViolations() error = %v", err)
	}
	if len(violations) != 0 {
		t.Errorf("violations = %v, want none", violations)
	}
}
