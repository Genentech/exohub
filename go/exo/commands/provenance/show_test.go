package provenance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	lib "github.com/Genentech/exohub/go/exo/internal/provenance"
)

func runShowCmd(t *testing.T, args ...string) (stdout string, err error) {
	t.Helper()
	cmd := newShowCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return buf.String(), err
}

// runShowCmdSeparated runs `exo provenance show` with distinct stdout/stderr
// buffers (review: --json output must be on stdout, not cobra's
// OutOrStderr default, since scripts, agents and the pre-commit framework
// parse it).
func runShowCmdSeparated(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := newShowCommand()
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return outBuf.String(), errBuf.String(), err
}

func TestShow_MultiStepLineage(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	noLoginToken(t)

	writeFile(t, root, "a.csv", "a")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "a")

	writeFile(t, root, "b.csv", "b")
	run("git", "add", "b.csv")
	if _, err := runAddCmd(t, "--generated", "b.csv", "--used", "a.csv", "--agent", "person:alice"); err != nil {
		t.Fatalf("add b error = %v", err)
	}
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "b")

	writeFile(t, root, "c.csv", "c")
	run("git", "add", "c.csv")
	if _, err := runAddCmd(t, "--generated", "c.csv", "--used", "b.csv", "--agent", "person:alice"); err != nil {
		t.Fatalf("add c error = %v", err)
	}
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "c")

	out, err := runShowCmd(t, "c.csv")
	if err != nil {
		t.Fatalf("show error = %v, output: %s", err, out)
	}
	if !strings.Contains(out, "Lineage for") {
		t.Errorf("output = %q, want a lineage header", out)
	}
	if !strings.Contains(out, "person:alice") {
		t.Errorf("output = %q, want the person agent", out)
	}
	if !strings.Contains(out, "Commit history") {
		t.Errorf("output = %q, want a commit history section", out)
	}
}

func TestShow_RenameKeepsProvenance(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	noLoginToken(t)

	writeFile(t, root, "in.csv", "in")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "in")

	writeFile(t, root, "old.csv", "generated content")
	run("git", "add", "old.csv")
	if _, err := runAddCmd(t, "--generated", "old.csv", "--used", "in.csv", "--agent", "person:alice"); err != nil {
		t.Fatalf("add error = %v", err)
	}
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record")

	run("git", "mv", "old.csv", "new.csv")
	run("git", "commit", "-q", "-m", "rename")

	out, err := runShowCmd(t, "new.csv")
	if err != nil {
		t.Fatalf("show error = %v, output: %s", err, out)
	}
	if strings.Contains(out, "No provenance recorded") {
		t.Errorf("output = %q, want provenance to survive the rename", out)
	}
}

func TestShow_ModifiedContentHasNoProvenance(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	noLoginToken(t)

	writeFile(t, root, "in.csv", "in")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "in")

	writeFile(t, root, "out.csv", "v1")
	run("git", "add", "out.csv")
	if _, err := runAddCmd(t, "--generated", "out.csv", "--used", "in.csv", "--agent", "person:alice"); err != nil {
		t.Fatalf("add error = %v", err)
	}
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record")

	writeFile(t, root, "out.csv", "v2, changed")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "modify")

	out, err := runShowCmd(t, "out.csv")
	if err != nil {
		t.Fatalf("show error = %v, output: %s", err, out)
	}
	if !strings.Contains(out, "No provenance recorded") {
		t.Errorf("output = %q, want a no-provenance message for the new content", out)
	}
	if !strings.Contains(out, "Commit history") {
		t.Errorf("output = %q, want a commit history section", out)
	}
}

func TestShow_FolderMemberFallback(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	noLoginToken(t)

	writeFile(t, root, "data/expr.tdb/fragment1", "f1")
	run("git", "add", "data/expr.tdb/fragment1")
	if _, err := runAddCmd(t, "--generated", "data/expr.tdb/", "--agent", "person:alice"); err != nil {
		t.Fatalf("add error = %v", err)
	}
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record")

	out, err := runShowCmd(t, "data/expr.tdb/fragment1")
	if err != nil {
		t.Fatalf("show error = %v, output: %s", err, out)
	}
	if !strings.Contains(out, "enclosing folder collection data/expr.tdb/") {
		t.Errorf("output = %q, want it to report the folder fallback", out)
	}
}

func TestShow_NoRecordsStillShowsCommits(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)

	writeFile(t, root, "plain.csv", "data")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	out, err := runShowCmd(t, "plain.csv")
	if err != nil {
		t.Fatalf("show error = %v, output: %s", err, out)
	}
	if !strings.Contains(out, "No provenance recorded") {
		t.Errorf("output = %q, want a no-provenance message", out)
	}
	if !strings.Contains(out, "committed by") {
		t.Errorf("output = %q, want the commit history", out)
	}
}

func TestShow_Cycle(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	noLoginToken(t)

	writeFile(t, root, "a.csv", "a")
	writeFile(t, root, "b.csv", "b")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	aBlob := gitBlobID(t, run, "a.csv")
	bBlob := gitBlobID(t, run, "b.csv")
	writeFile(t, root, ".exohub/provenance/r1.jsonld", `{
  "@context": "https://genentech.github.io/exohub/ns/provenance/v1.jsonld",
  "@id": "`+newRecordIDForTest()+`",
  "exo:assertion": "recorded",
  "exo:assertedBy": "urn:exohub:agent:person:alice",
  "generatedAtTime": "2026-10-14T10:00:00Z",
  "@graph": [
    {"@id": "urn:exohub:git:blob:`+aBlob+`", "@type": "Entity", "wasDerivedFrom": "urn:exohub:git:blob:`+bBlob+`"},
    {"@id": "urn:exohub:git:blob:`+bBlob+`", "@type": "Entity", "wasDerivedFrom": "urn:exohub:git:blob:`+aBlob+`"}
  ]
}`)
	run("git", "-c", "annex.largefiles=nothing", "add", ".")
	run("git", "commit", "-q", "-m", "cyclic record")

	out, err := runShowCmd(t, "a.csv")
	if err != nil {
		t.Fatalf("show error = %v, output: %s", err, out)
	}
	if !strings.Contains(out, "Cycle detected") {
		t.Errorf("output = %q, want a cycle to be reported", out)
	}
	if err != nil {
		t.Error("show on a cycle should exit zero")
	}
}

func TestShow_RenameShowsNewPath(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	noLoginToken(t)

	writeFile(t, root, "in.csv", "in")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "in")

	writeFile(t, root, "old.csv", "generated content")
	run("git", "add", "old.csv")
	if _, err := runAddCmd(t, "--generated", "old.csv", "--used", "in.csv", "--agent", "person:alice"); err != nil {
		t.Fatalf("add error = %v", err)
	}
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record")

	run("git", "mv", "old.csv", "new.csv")
	run("git", "commit", "-q", "-m", "rename")

	// Review item 1: "show <new path> to show the existing provenance with
	// the new path" — the record's recorded exo:path still reads "old.csv",
	// but the output must print the current path at the ref.
	out, err := runShowCmd(t, "new.csv")
	if err != nil {
		t.Fatalf("show error = %v, output: %s", err, out)
	}
	if !strings.Contains(out, "new.csv") {
		t.Errorf("output = %q, want it to show the renamed path new.csv", out)
	}
	if strings.Contains(out, "old.csv") {
		t.Errorf("output = %q, want it to not show the stale recorded path old.csv", out)
	}
	if !strings.Contains(out, "in.csv") {
		t.Errorf("output = %q, want the used input's current path too", out)
	}
}

func TestShow_AgentLabelShown(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	jwt := fakeJWT(`{"preferred_username":"bob","name":"Bob Smith"}`)
	withLoginToken(t, jwt)

	writeFile(t, root, "out.csv", "generated content")
	run("git", "add", "out.csv")
	if _, err := runAddCmd(t, "--generated", "out.csv"); err != nil {
		t.Fatalf("add error = %v", err)
	}
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record")

	out, err := runShowCmd(t, "out.csv")
	if err != nil {
		t.Fatalf("show error = %v, output: %s", err, out)
	}
	// Review item 1: "print agent labels next to agent ids".
	if !strings.Contains(out, "Bob Smith (urn:exohub:agent:person:bob)") {
		t.Errorf("output = %q, want the person agent's label next to its id", out)
	}
}

func TestShow_SoftwareAgentListsBothAgents(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	noLoginToken(t)

	writeFile(t, root, "out.csv", "generated content")
	run("git", "add", "out.csv")
	if _, err := runAddCmd(t,
		"--generated", "out.csv",
		"--agent", "software:claude-code",
		"--model", "claude-sonnet",
		"--on-behalf-of", "person:alice",
		"--evidence", "...",
	); err != nil {
		t.Fatalf("add error = %v", err)
	}
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record")

	out, err := runShowCmd(t, "out.csv")
	if err != nil {
		t.Fatalf("show error = %v, output: %s", err, out)
	}
	// The Activity is wasAssociatedWith both the software agent and the
	// person it acted for (design D6, D11), not the person alone.
	if !strings.Contains(out, lib.SoftwareAgentID("claude-code")) {
		t.Errorf("output = %q, want the software agent id", out)
	}
	if !strings.Contains(out, lib.PersonAgentID("alice")) {
		t.Errorf("output = %q, want the person agent id", out)
	}
	if !strings.Contains(out, "wasAssociatedWith") {
		t.Errorf("output = %q, want a wasAssociatedWith line", out)
	}
}

func TestShow_UsedOnlyFallsBackToCoveredFolder(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	noLoginToken(t)

	writeFile(t, root, "data/expr.tdb/fragment1", "f1")
	run("git", "add", "data/expr.tdb/fragment1")
	if _, err := runAddCmd(t, "--generated", "data/expr.tdb/", "--agent", "person:alice"); err != nil {
		t.Fatalf("add folder error = %v", err)
	}
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "folder record")

	writeFile(t, root, "downstream.csv", "out")
	run("git", "add", "downstream.csv")
	// Review item 2: fragment1 is merely "used" here, which must not count
	// as its own provenance (design D14) — show must still fall back to the
	// enclosing covered folder rather than reporting fragment1 itself as
	// "own" lineage.
	if _, err := runAddCmd(t, "--generated", "downstream.csv", "--used", "data/expr.tdb/fragment1", "--agent", "person:alice"); err != nil {
		t.Fatalf("add downstream error = %v", err)
	}
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "downstream record")

	out, err := runShowCmd(t, "data/expr.tdb/fragment1")
	if err != nil {
		t.Fatalf("show error = %v, output: %s", err, out)
	}
	if !strings.Contains(out, "enclosing folder collection data/expr.tdb/") {
		t.Errorf("output = %q, want it to report the folder fallback (being used is not being covered)", out)
	}
}

func TestShow_UsedOnlyReportsUsedByActivity(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	noLoginToken(t)

	writeFile(t, root, "input.csv", "in")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "input")

	writeFile(t, root, "output.csv", "out")
	run("git", "add", "output.csv")
	if _, err := runAddCmd(t, "--generated", "output.csv", "--used", "input.csv", "--agent", "person:alice"); err != nil {
		t.Fatalf("add error = %v", err)
	}
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record")

	out, err := runShowCmd(t, "input.csv")
	if err != nil {
		t.Fatalf("show error = %v, output: %s", err, out)
	}
	// Review item 2: "When there is none, still print what is known, for
	// example 'used by activity X', together with 'no provenance recorded'".
	if !strings.Contains(out, "No provenance recorded") {
		t.Errorf("output = %q, want a no-provenance message (used-only is not covered)", out)
	}
	if !strings.Contains(out, "used as an input by") {
		t.Errorf("output = %q, want it to mention the activity that used input.csv", out)
	}
}

func TestShow_MaxCommitsCapsHistory(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)

	writeFile(t, root, "data.csv", "v0")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")
	for i := 0; i < 5; i++ {
		writeFile(t, root, "data.csv", fmt.Sprintf("v%d", i+1))
		run("git", "add", ".")
		run("git", "commit", "-q", "-m", fmt.Sprintf("update %d", i))
	}

	out, err := runShowCmd(t, "data.csv", "--max-commits", "2")
	if err != nil {
		t.Fatalf("show error = %v, output: %s", err, out)
	}
	if strings.Count(out, "committed by") != 2 {
		t.Errorf("output = %q, want exactly 2 commits shown", out)
	}
	if !strings.Contains(out, "4 more") {
		t.Errorf("output = %q, want a '... N more' notice", out)
	}

	unlimited, err := runShowCmd(t, "data.csv", "--max-commits", "0")
	if err != nil {
		t.Fatalf("show --max-commits 0 error = %v, output: %s", err, unlimited)
	}
	if strings.Count(unlimited, "committed by") != 6 {
		t.Errorf("output = %q, want all 6 commits shown", unlimited)
	}
}

func TestShow_NegativeMaxCommitsRejected(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)

	writeFile(t, root, "a.csv", "a")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	_, err := runShowCmd(t, "a.csv", "--max-commits", "-1")
	if err == nil {
		t.Fatal("show --max-commits -1 error = nil, want non-nil")
	}
}

func TestShow_JSONOutput(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	noLoginToken(t)

	writeFile(t, root, "in.csv", "in")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "in")

	writeFile(t, root, "out.csv", "generated content")
	run("git", "add", "out.csv")
	if _, err := runAddCmd(t, "--generated", "out.csv", "--used", "in.csv", "--agent", "person:alice"); err != nil {
		t.Fatalf("add error = %v", err)
	}
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record")

	out, err := runShowCmd(t, "out.csv", "--json")
	if err != nil {
		t.Fatalf("show --json error = %v, output: %s", err, out)
	}
	if !strings.Contains(out, `"@context"`) {
		t.Errorf("output = %q, want @context", out)
	}
	if !strings.Contains(out, `"@graph"`) {
		t.Errorf("output = %q, want @graph", out)
	}
}

// TestShow_JSONGoesToStdoutOnly is a regression test for review item 1 on
// !257: cobra's cmd.Println/Printf default to OutOrStderr, so a caller
// piping `exo provenance show --json` output (stdout only, discarding
// stderr) previously got nothing; it must also parse as a single JSON
// document, and the compiled-document-shaped exo:records list (review item
// 3) must be present.
func TestShow_JSONGoesToStdoutOnly(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	noLoginToken(t)

	writeFile(t, root, "in.csv", "in")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "in")

	writeFile(t, root, "out.csv", "generated content")
	run("git", "add", "out.csv")
	if _, err := runAddCmd(t, "--generated", "out.csv", "--used", "in.csv", "--agent", "person:alice"); err != nil {
		t.Fatalf("add error = %v", err)
	}
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record")

	stdout, stderr, err := runShowCmdSeparated(t, "out.csv", "--json")
	if err != nil {
		t.Fatalf("show --json error = %v, stderr: %s", err, stderr)
	}
	if strings.TrimSpace(stderr) != "" {
		t.Errorf("stderr = %q, want empty (no diagnostics on success)", stderr)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\nstdout: %s", err, stdout)
	}
	if _, ok := doc["exo:records"]; !ok {
		t.Errorf("stdout JSON = %+v, want an exo:records field", doc)
	}
}

func TestShow_NegativeDepthRejected(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)

	writeFile(t, root, "a.csv", "a")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	_, err := runShowCmd(t, "a.csv", "--depth", "-1")
	if err == nil {
		t.Fatal("show --depth -1 error = nil, want non-nil")
	}
}

// newRecordIDForTest returns a syntactically valid record id for a
// hand-written fixture record.
func newRecordIDForTest() string {
	return lib.NewRecordID()
}
