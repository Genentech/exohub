package provenance

import (
	"fmt"
	"testing"
	"time"
)

func derivationRecord(generatedID, activityID string, usedIDs []string, derivedFromIDs []string) string {
	usedJSON := "[]"
	if len(usedIDs) > 0 {
		usedJSON = fmt.Sprintf("%q", usedIDs[0])
		if len(usedIDs) > 1 {
			usedJSON = jsonStringArray(usedIDs)
		}
	}
	derivedJSON := jsonStringArray(derivedFromIDs)
	return minimalRecordJSON(NewRecordID(), "recorded", "urn:exohub:agent:person:alice",
		fmt.Sprintf(`
    {"@id": %q, "@type": "Activity", "used": %s},
    {"@id": %q, "@type": "Entity", "wasGeneratedBy": %q, "wasDerivedFrom": %s}
    `, activityID, usedJSON, generatedID, activityID, derivedJSON))
}

// collectionRecord builds a minimal record covering a folder Collection
// node at dirPath (with its recorded exo:path, design "A folder can be one
// artifact"), the shape hasCollectionAtPath/folderHistoryHint require to
// consider an older tree id at all.
func collectionRecord(treeID, dirPath, prop, value string) string {
	return minimalRecordJSON(NewRecordID(), "recorded", "urn:exohub:agent:person:alice",
		fmt.Sprintf(`{"@id": %q, "@type": "Collection", "exo:path": %q, %q: %q}`, treeID, dirPath+"/", prop, value))
}

// usedRecord builds a record where activityID merely "used" usedID as an
// input: usedID gets no covering property of its own (design D14), the
// shape review item 2 ("'own provenance' should mean covered, not 'appears
// in the graph'") needs to distinguish from a generated/derived entity.
func usedRecord(activityID, usedID string) string {
	return minimalRecordJSON(NewRecordID(), "recorded", "urn:exohub:agent:person:alice",
		fmt.Sprintf(`
    {"@id": %q, "@type": "Activity", "used": %q},
    {"@id": %q, "@type": "Entity"}
    `, activityID, usedID, usedID))
}

func jsonStringArray(ss []string) string {
	out := "["
	for i, s := range ss {
		if i > 0 {
			out += ", "
		}
		out += fmt.Sprintf("%q", s)
	}
	return out + "]"
}

func TestLineage_MultiStepDerivation(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "a.csv", "a")
	writeRepoFile(t, root, "b.csv", "b")
	writeRepoFile(t, root, "c.parquet", "c")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	reader := NewWorktreeReader(root)
	aID, _ := reader.ResolveContentID("a.csv")
	bID, _ := reader.ResolveContentID("b.csv")
	cID, _ := reader.ResolveContentID("c.parquet")

	writeRepoFile(t, root, RecordDir+"/r1.jsonld", derivationRecord(bID, NewActivityID(), []string{aID}, []string{aID}))
	writeRepoFile(t, root, RecordDir+"/r2.jsonld", derivationRecord(cID, NewActivityID(), []string{bID}, []string{bID}))
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "records")

	lineage, err := BuildLineage(NewRefReader(root, "HEAD"), "c.parquet", 0)
	if err != nil {
		t.Fatalf("BuildLineage() error = %v", err)
	}
	if lineage.Subject.ID != cID || lineage.Subject.Source != LineageSourceOwn {
		t.Fatalf("Subject = %+v, want own %s", lineage.Subject, cID)
	}
	visited := map[string]bool{}
	for _, id := range lineage.Visited {
		visited[id] = true
	}
	if !visited[cID] || !visited[bID] || !visited[aID] {
		t.Fatalf("Visited = %v, want it to include a, b and c", lineage.Visited)
	}
	if len(lineage.Cycles) != 0 {
		t.Errorf("Cycles = %v, want none", lineage.Cycles)
	}
}

func TestLineage_RenameKeepsProvenance(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "old.csv", "same content")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	reader := NewWorktreeReader(root)
	contentID, err := reader.ResolveContentID("old.csv")
	if err != nil {
		t.Fatalf("ResolveContentID() error = %v", err)
	}
	writeRepoFile(t, root, RecordDir+"/r1.jsonld", entityRecord(contentID, "wasGeneratedBy", NewActivityID()))
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record")

	run("git", "mv", "old.csv", "new.csv")
	run("git", "commit", "-q", "-m", "rename")

	newID, err := NewRefReader(root, "HEAD").ResolveContentID("new.csv")
	if err != nil {
		t.Fatalf("ResolveContentID() error = %v", err)
	}
	if newID != contentID {
		t.Fatalf("content id changed across rename: %s != %s", newID, contentID)
	}

	lineage, err := BuildLineage(NewRefReader(root, "HEAD"), "new.csv", 0)
	if err != nil {
		t.Fatalf("BuildLineage() error = %v", err)
	}
	if lineage.Subject.ID != contentID || lineage.Subject.Source != LineageSourceOwn {
		t.Fatalf("Subject = %+v, want own %s", lineage.Subject, contentID)
	}
	if lineage.Graph.Nodes[contentID] == nil {
		t.Fatal("expected the renamed file's provenance record to still be reachable")
	}
	if !lineage.Covered {
		t.Error("Covered = false, want true (wasGeneratedBy covers the node)")
	}
	// Review: "print the requested path for the subject... for every other
	// repository entity, print its path at the ref" — the path index must
	// resolve the node's *current* path, not the stale recorded one.
	if path, ok := lineage.Paths.Path(contentID); !ok || path != "new.csv" {
		t.Errorf("Paths.Path(%s) = (%q, %v), want (\"new.csv\", true)", contentID, path, ok)
	}
}

func TestLineage_ModifiedContentHasNoProvenance(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "data.csv", "v1")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	reader := NewWorktreeReader(root)
	oldID, err := reader.ResolveContentID("data.csv")
	if err != nil {
		t.Fatalf("ResolveContentID() error = %v", err)
	}
	writeRepoFile(t, root, RecordDir+"/r1.jsonld", entityRecord(oldID, "wasGeneratedBy", NewActivityID()))
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record")

	writeRepoFile(t, root, "data.csv", "v2, different content")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "modify")

	lineage, err := BuildLineage(NewRefReader(root, "HEAD"), "data.csv", 0)
	if err != nil {
		t.Fatalf("BuildLineage() error = %v", err)
	}
	if lineage.Subject.Source != LineageSourceOwn {
		t.Fatalf("Subject = %+v, want own (no folder to fall back to)", lineage.Subject)
	}
	if lineage.Graph.Nodes[lineage.Subject.ID] != nil {
		t.Fatalf("expected no provenance for the new content id %s", lineage.Subject.ID)
	}
	if lineage.Covered {
		t.Error("Covered = true, want false (the new content id has no record at all)")
	}
}

func TestLineage_UsedOnlyFallsBackToCoveredFolder(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "data/expr.tdb/fragment1", "f1")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	reader := NewWorktreeReader(root)
	treeID, err := reader.ResolveContentID("data/expr.tdb/")
	if err != nil {
		t.Fatalf("ResolveContentID() error = %v", err)
	}
	fragID, err := reader.ResolveContentID("data/expr.tdb/fragment1")
	if err != nil {
		t.Fatalf("ResolveContentID() error = %v", err)
	}

	writeRepoFile(t, root, RecordDir+"/r1.jsonld", collectionRecord(treeID, "data/expr.tdb", "wasGeneratedBy", NewActivityID()))
	writeRepoFile(t, root, RecordDir+"/r2.jsonld", usedRecord(NewActivityID(), fragID))
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "records")

	lineage, err := BuildLineage(NewRefReader(root, "HEAD"), "data/expr.tdb/fragment1", 0)
	if err != nil {
		t.Fatalf("BuildLineage() error = %v", err)
	}
	// Review item 2: being used as an input does not cover fragID, so show
	// must fall back to the enclosing covered folder, exactly as it would
	// for an uncovered file with no records at all.
	if lineage.Subject.ID != treeID || lineage.Subject.Source != LineageSourceFolderFallback {
		t.Fatalf("Subject = %+v, want folder fallback to %s", lineage.Subject, treeID)
	}
}

func TestLineage_UsedOnlyWithoutFolderReportsUsedBy(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "input.csv", "in")
	writeRepoFile(t, root, "output.csv", "out")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	reader := NewWorktreeReader(root)
	inID, err := reader.ResolveContentID("input.csv")
	if err != nil {
		t.Fatalf("ResolveContentID() error = %v", err)
	}

	activityID := NewActivityID()
	writeRepoFile(t, root, RecordDir+"/r1.jsonld", usedRecord(activityID, inID))
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record")

	lineage, err := BuildLineage(NewRefReader(root, "HEAD"), "input.csv", 0)
	if err != nil {
		t.Fatalf("BuildLineage() error = %v", err)
	}
	// There is no covered enclosing folder, so the subject stays input.csv's
	// own id, but it is still not Covered; review item 2: "When there is
	// none, still print what is known, for example 'used by activity X'".
	if lineage.Subject.ID != inID || lineage.Subject.Source != LineageSourceOwn {
		t.Fatalf("Subject = %+v, want own %s (no folder to fall back to)", lineage.Subject, inID)
	}
	if lineage.Covered {
		t.Error("Covered = true, want false (used-only is not covered, design D14)")
	}
	if len(lineage.UsedByActivities) != 1 || lineage.UsedByActivities[0] != activityID {
		t.Errorf("UsedByActivities = %v, want [%s]", lineage.UsedByActivities, activityID)
	}
}

func TestLineage_FolderMemberFallback(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "data/expr.tdb/fragment1", "f1")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	reader := NewWorktreeReader(root)
	treeID, err := reader.ResolveContentID("data/expr.tdb/")
	if err != nil {
		t.Fatalf("ResolveContentID() error = %v", err)
	}
	writeRepoFile(t, root, RecordDir+"/r1.jsonld", entityRecord(treeID, "wasGeneratedBy", NewActivityID()))
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record")

	lineage, err := BuildLineage(NewRefReader(root, "HEAD"), "data/expr.tdb/fragment1", 0)
	if err != nil {
		t.Fatalf("BuildLineage() error = %v", err)
	}
	if lineage.Subject.ID != treeID || lineage.Subject.Source != LineageSourceFolderFallback {
		t.Fatalf("Subject = %+v, want folder fallback to %s", lineage.Subject, treeID)
	}
	if lineage.Subject.FolderPath != "data/expr.tdb" {
		t.Errorf("FolderPath = %q, want data/expr.tdb", lineage.Subject.FolderPath)
	}
}

func TestLineage_DeclaredCollectionFallback(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeConfig(t, root, "provenance:\n  collections:\n    - \"**/*.tdb/\"\n")
	writeRepoFile(t, root, "data/expr.tdb/fragments/f1", "f1")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	reader := NewWorktreeReader(root)
	treeID, err := reader.ResolveContentID("data/expr.tdb/")
	if err != nil {
		t.Fatalf("ResolveContentID() error = %v", err)
	}
	writeRepoFile(t, root, RecordDir+"/r1.jsonld", entityRecord(treeID, "wasGeneratedBy", NewActivityID()))
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record")

	// Also record the member directly: declared collections are covered
	// only through the folder, so show must still fall back to it.
	memberID, err := NewRefReader(root, "HEAD").ResolveContentID("data/expr.tdb/fragments/f1")
	if err != nil {
		t.Fatalf("ResolveContentID() error = %v", err)
	}
	writeRepoFile(t, root, RecordDir+"/r2.jsonld", entityRecord(memberID, "wasGeneratedBy", NewActivityID()))
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "member record")

	lineage, err := BuildLineage(NewRefReader(root, "HEAD"), "data/expr.tdb/fragments/f1", 0)
	if err != nil {
		t.Fatalf("BuildLineage() error = %v", err)
	}
	if lineage.Subject.ID != treeID || lineage.Subject.Source != LineageSourceDeclaredCollection {
		t.Fatalf("Subject = %+v, want declared collection fallback to %s", lineage.Subject, treeID)
	}
}

func TestLineage_Cycle(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "a.csv", "a")
	writeRepoFile(t, root, "b.csv", "b")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	reader := NewWorktreeReader(root)
	aID, _ := reader.ResolveContentID("a.csv")
	bID, _ := reader.ResolveContentID("b.csv")

	writeRepoFile(t, root, RecordDir+"/r1.jsonld", entityRecord(aID, "wasDerivedFrom", bID))
	writeRepoFile(t, root, RecordDir+"/r2.jsonld", entityRecord(bID, "wasDerivedFrom", aID))
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "records")

	lineage, err := BuildLineage(NewRefReader(root, "HEAD"), "a.csv", 0)
	if err != nil {
		t.Fatalf("BuildLineage() error = %v", err)
	}
	if len(lineage.Cycles) == 0 {
		t.Fatal("expected a cycle to be detected")
	}
	count := map[string]int{}
	for _, id := range lineage.Visited {
		count[id]++
	}
	for id, n := range count {
		if n != 1 {
			t.Errorf("node %s visited %d times, want each node visited once", id, n)
		}
	}
}

func TestLineage_NoRecords(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "data.csv", "v1")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	lineage, err := BuildLineage(NewRefReader(root, "HEAD"), "data.csv", 0)
	if err != nil {
		t.Fatalf("BuildLineage() error = %v", err)
	}
	if lineage.Graph.Nodes[lineage.Subject.ID] != nil {
		t.Fatalf("expected no provenance node for %s", lineage.Subject.ID)
	}
	if len(lineage.Visited) != 1 || lineage.Visited[0] != lineage.Subject.ID {
		t.Errorf("Visited = %v, want just the subject", lineage.Visited)
	}
	if lineage.Covered {
		t.Error("Covered = true, want false (no records exist at all)")
	}
	if len(lineage.UsedByActivities) != 0 {
		t.Errorf("UsedByActivities = %v, want none", lineage.UsedByActivities)
	}
}

// TestLineage_AssertingAgentWithNoNodeAppears is a regression test for
// review item 4 on !257: a record's envelope exo:assertedBy agent does not
// need a node of its own in that record's @graph (design D2 only requires
// it to be a valid agent id), so Walk must still surface it — synthesized
// as a plain Agent node when no record ever defines one.
func TestLineage_AssertingAgentWithNoNodeAppears(t *testing.T) {
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

	var found bool
	for _, id := range lineage.Visited {
		if id == botID {
			found = true
		}
	}
	if !found {
		t.Fatalf("Visited = %v, want it to include the asserting agent %s even though no @graph node defines it", lineage.Visited, botID)
	}
	node := lineage.Graph.Nodes[botID]
	if node == nil {
		t.Fatal("expected a synthesized node for the asserting agent")
	}
	types := node.Types()
	if len(types) != 1 || types[0] != "Agent" {
		t.Errorf("Types() = %v, want [Agent] for a synthesized asserter node", types)
	}
}

// TestLineage_AssertingAgentActedOnBehalfOfChainIncluded is a regression
// test for review item 4 on !257: once the asserting agent is found, its
// actedOnBehalfOf chain must also be walked and included.
func TestLineage_AssertingAgentActedOnBehalfOfChainIncluded(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "out.csv", "out")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	contentID, err := NewWorktreeReader(root).ResolveContentID("out.csv")
	if err != nil {
		t.Fatalf("ResolveContentID() error = %v", err)
	}
	botID := SoftwareAgentID("bot")
	personID := PersonAgentID("alice")
	graph := fmt.Sprintf(`
    {"@id": %q, "@type": "Entity", "wasGeneratedBy": %q},
    {"@id": %q, "@type": "SoftwareAgent", "actedOnBehalfOf": %q}
    `, contentID, NewActivityID(), botID, personID)
	writeRepoFile(t, root, RecordDir+"/r1.jsonld", minimalRecordJSON(NewRecordID(), "recorded", botID, graph))
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record")

	lineage, err := BuildLineage(NewRefReader(root, "HEAD"), "out.csv", 0)
	if err != nil {
		t.Fatalf("BuildLineage() error = %v", err)
	}

	var foundBot, foundPerson bool
	for _, id := range lineage.Visited {
		if id == botID {
			foundBot = true
		}
		if id == personID {
			foundPerson = true
		}
	}
	if !foundBot {
		t.Errorf("Visited = %v, want the asserting software agent %s", lineage.Visited, botID)
	}
	if !foundPerson {
		t.Errorf("Visited = %v, want the actedOnBehalfOf chain to reach %s", lineage.Visited, personID)
	}
}

func TestLineage_FolderHistoryHintSkippedWithoutCollectionRecord(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "data/expr.tdb/fragment1", "f1")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	// No provenance records exist, so the folder was never once covered.
	// Review item 3: "only compute a hint for a directory when the merged
	// graph has a Collection node whose recorded exo:path equals dir + '/'
	// (otherwise no older covered tree can exist)" — folderHistoryHint must
	// return nil without walking any commit history.
	lineage, err := BuildLineage(NewRefReader(root, "HEAD"), "data/expr.tdb/fragment1", 0)
	if err != nil {
		t.Fatalf("BuildLineage() error = %v", err)
	}
	if lineage.Hint != nil {
		t.Errorf("Hint = %+v, want nil (no Collection record was ever written)", lineage.Hint)
	}
}

func TestLineage_DepthLimitsWalk(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "a.csv", "a")
	writeRepoFile(t, root, "b.csv", "b")
	writeRepoFile(t, root, "c.parquet", "c")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	reader := NewWorktreeReader(root)
	aID, _ := reader.ResolveContentID("a.csv")
	bID, _ := reader.ResolveContentID("b.csv")
	cID, _ := reader.ResolveContentID("c.parquet")

	writeRepoFile(t, root, RecordDir+"/r1.jsonld", derivationRecord(bID, NewActivityID(), []string{aID}, []string{aID}))
	writeRepoFile(t, root, RecordDir+"/r2.jsonld", derivationRecord(cID, NewActivityID(), []string{bID}, []string{bID}))
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "records")

	lineage, err := BuildLineage(NewRefReader(root, "HEAD"), "c.parquet", 1)
	if err != nil {
		t.Fatalf("BuildLineage() error = %v", err)
	}
	if !lineage.DepthReached {
		t.Error("DepthReached = false, want true")
	}
	visited := map[string]bool{}
	for _, id := range lineage.Visited {
		visited[id] = true
	}
	if visited[aID] {
		t.Errorf("Visited = %v, want a.csv's id excluded beyond --depth 1", lineage.Visited)
	}
}

func TestLineage_FolderHistoryHintOnLockModeChange(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "data/expr.tdb/fragment1", "f1")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	oldTreeID, err := NewWorktreeReader(root).ResolveContentID("data/expr.tdb/")
	if err != nil {
		t.Fatalf("ResolveContentID() error = %v", err)
	}
	writeRepoFile(t, root, RecordDir+"/r1.jsonld", collectionRecord(oldTreeID, "data/expr.tdb", "wasGeneratedBy", NewActivityID()))
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record")

	// Simulate a tree id change unrelated to content, for example a
	// locked/unlocked mode switch (design D15 Risk): add then remove a
	// member so the folder's tree id moves away from oldTreeID and back,
	// via an intermediate commit, leaving an uncovered tip.
	writeRepoFile(t, root, "data/expr.tdb/fragment2", "f2")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "new fragment")

	lineage, err := BuildLineage(NewRefReader(root, "HEAD"), "data/expr.tdb/fragment1", 0)
	if err != nil {
		t.Fatalf("BuildLineage() error = %v", err)
	}
	if lineage.Hint == nil {
		t.Fatal("expected a folder history hint pointing at the older covered tree id")
	}
	if lineage.Hint.TreeID != oldTreeID {
		t.Errorf("Hint.TreeID = %q, want %q", lineage.Hint.TreeID, oldTreeID)
	}
	if lineage.Hint.FolderPath != "data/expr.tdb" {
		t.Errorf("Hint.FolderPath = %q, want data/expr.tdb", lineage.Hint.FolderPath)
	}
}

// TestLineage_UncoveredFileScalesWithRecordsNotHistory is a regression test
// for review item 3 ("folder-history hint is expensive on every uncovered
// file... it scales linearly with history, and 'no provenance' is the
// common case"): BuildLineage on an uncovered file in a repository with a
// long, unrelated commit history must stay fast, because hasCollectionAtPath
// skips folderHistoryHint's git log/cat-file calls entirely when no
// Collection record exists for any ancestor directory.
func TestLineage_UncoveredFileScalesWithRecordsNotHistory(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "data/expr.tdb/fragment1", "f1")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	// Build a long, unrelated history touching the same folder, the shape
	// that made the old per-commit `git rev-parse` loop slow.
	for i := 0; i < 150; i++ {
		writeRepoFile(t, root, fmt.Sprintf("data/expr.tdb/noise%d", i), "noise")
		run("git", "add", ".")
		run("git", "commit", "-q", "-m", fmt.Sprintf("noise %d", i))
	}

	start := time.Now()
	lineage, err := BuildLineage(NewRefReader(root, "HEAD"), "data/expr.tdb/fragment1", 0)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("BuildLineage() error = %v", err)
	}
	if lineage.Hint != nil {
		t.Errorf("Hint = %+v, want nil (no Collection record was ever written)", lineage.Hint)
	}
	// Generous bound: the old implementation spent one `git rev-parse`
	// process per commit here (150), the fix spends zero once
	// hasCollectionAtPath finds no Collection record.
	if elapsed > 2*time.Second {
		t.Errorf("BuildLineage() took %s for an uncovered file with no Collection record, want it independent of commit history", elapsed)
	}
}
