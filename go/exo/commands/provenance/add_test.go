package provenance

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	lib "github.com/Genentech/exohub/go/exo/internal/provenance"
)

// addTestRepo creates a temp git repository and returns its root plus a
// helper to run commands in it.
func addTestRepo(t *testing.T, withAnnex bool) (root string, run func(args ...string) string) {
	t.Helper()
	root = t.TempDir()
	run = func(args ...string) string {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+root)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("command %v failed: %v\n%s", args, err, out)
		}
		return string(out)
	}
	run("git", "init", "-q")
	run("git", "config", "user.email", "test@test.com")
	run("git", "config", "user.name", "Test")
	if withAnnex {
		if _, err := exec.LookPath("git-annex"); err != nil {
			t.Skip("git-annex not available")
		}
		run("git", "annex", "init", "-q", "test-repo")
		t.Cleanup(func() {
			exec.Command("chmod", "-R", "u+w", root).Run()
		})
	}
	return root, run
}

func writeFile(t *testing.T, root, relPath, content string) {
	t.Helper()
	full := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func chdir(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		os.Chdir(prev)
	})
}

// noLoginToken isolates a test from any real exo login token on the host,
// and from the user's real config directory.
func noLoginToken(t *testing.T) {
	t.Helper()
	t.Setenv("EXO_TOKEN", "")
	t.Setenv("EXO_TOKEN_FILE", "")
	t.Setenv("EXO_CONFIG_DIR", t.TempDir())
}

func fakeJWT(payload string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	body := base64.RawURLEncoding.EncodeToString([]byte(payload))
	return header + "." + body + ".sig"
}

// withLoginToken sets EXO_TOKEN to an inline token so login.LoadToken picks
// it up without touching the filesystem.
func withLoginToken(t *testing.T, jwt string) {
	t.Helper()
	t.Setenv("EXO_CONFIG_DIR", t.TempDir())
	t.Setenv("EXO_TOKEN_FILE", "")
	t.Setenv("EXO_TOKEN", `{"access_token":"`+jwt+`"}`)
}

func runAddCmd(t *testing.T, args ...string) (stdout string, err error) {
	t.Helper()
	cmd := newAddCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return buf.String(), err
}

// runAddCmdSeparated runs `exo provenance add` with distinct stdout/stderr
// buffers, to check that normal and --json output land on stdout and only
// diagnostics on stderr (review: cobra's cmd.Println/Printf default to
// OutOrStderr, which broke scripts and agents piping --json output).
func runAddCmdSeparated(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := newAddCommand()
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return outBuf.String(), errBuf.String(), err
}

func TestAdd_RecordADerivation(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	noLoginToken(t)

	writeFile(t, root, "data/counts.csv", "a,b\n1,2\n")
	writeFile(t, root, "scripts/de.R", "# script\n")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "inputs")
	writeFile(t, root, "results/de.parquet", "fake-parquet")
	run("git", "add", "results/de.parquet")

	out, err := runAddCmd(t, "--generated", "results/de.parquet", "--used", "data/counts.csv", "--plan", "scripts/de.R", "--agent", "person:alice")
	if err != nil {
		t.Fatalf("add error = %v, output: %s", err, out)
	}

	files, err := os.ReadDir(filepath.Join(root, ".exohub/provenance"))
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("got %d record files, want 1", len(files))
	}
	recordPath := ".exohub/provenance/" + files[0].Name()

	staged := run("git", "ls-files", "--", recordPath)
	if strings.TrimSpace(staged) != recordPath {
		t.Errorf("record was not staged: ls-files = %q", staged)
	}

	data, err := os.ReadFile(filepath.Join(root, recordPath))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	rec, err := lib.Parse(data)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if rec.Assertion != lib.AssertionAsserted {
		t.Errorf("Assertion = %q, want asserted", rec.Assertion)
	}
	if rec.AssertedBy != lib.PersonAgentID("alice") {
		t.Errorf("AssertedBy = %q, want %q", rec.AssertedBy, lib.PersonAgentID("alice"))
	}
	if rec.GeneratedAtTime == "" {
		t.Error("GeneratedAtTime is empty")
	}

	byID := map[string]lib.Node{}
	for _, n := range rec.Graph {
		byID[n.ID()] = n
	}

	countsBlob := gitBlobID(t, run, "data/counts.csv")
	deBlob := gitBlobID(t, run, "results/de.parquet")
	scriptBlob := gitBlobID(t, run, "scripts/de.R")

	generated, ok := byID[lib.GitBlobEntityID(deBlob)]
	if !ok {
		t.Fatalf("generated entity %s missing from graph", deBlob)
	}
	if generated["exo:path"] != "results/de.parquet" {
		t.Errorf("generated exo:path = %v, want results/de.parquet", generated["exo:path"])
	}
	if generated["wasDerivedFrom"] != lib.GitBlobEntityID(countsBlob) {
		t.Errorf("generated wasDerivedFrom = %v, want %v", generated["wasDerivedFrom"], lib.GitBlobEntityID(countsBlob))
	}
	activityID, _ := generated["wasGeneratedBy"].(string)
	activity, ok := byID[activityID]
	if !ok {
		t.Fatalf("activity %s missing from graph", activityID)
	}
	if activity["used"] != lib.GitBlobEntityID(countsBlob) {
		t.Errorf("activity used = %v, want %v", activity["used"], lib.GitBlobEntityID(countsBlob))
	}
	if activity["hadPlan"] != lib.GitBlobEntityID(scriptBlob) {
		t.Errorf("activity hadPlan = %v, want %v", activity["hadPlan"], lib.GitBlobEntityID(scriptBlob))
	}
	if activity["wasAssociatedWith"] != lib.PersonAgentID("alice") {
		t.Errorf("activity wasAssociatedWith = %v, want %v", activity["wasAssociatedWith"], lib.PersonAgentID("alice"))
	}

	person, ok := byID[lib.PersonAgentID("alice")]
	if !ok {
		t.Fatal("person agent missing from graph")
	}
	if types := person.Types(); len(types) != 1 || types[0] != "Person" {
		t.Errorf("person @type = %v, want [Person]", types)
	}
}

func gitBlobID(t *testing.T, run func(args ...string) string, path string) string {
	t.Helper()
	out := run("git", "ls-files", "--stage", "--", path)
	fields := strings.Fields(out)
	if len(fields) < 2 {
		t.Fatalf("unexpected ls-files output for %s: %q", path, out)
	}
	return fields[1]
}

func TestAdd_FolderCollection(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	noLoginToken(t)

	writeFile(t, root, "data/expr.tdb/a.frag", "a")
	writeFile(t, root, "data/expr.tdb/b.frag", "b")
	writeFile(t, root, "data/counts.csv", "x")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	out, err := runAddCmd(t, "--generated", "data/expr.tdb/", "--used", "data/counts.csv", "--agent", "person:alice")
	if err != nil {
		t.Fatalf("add error = %v, output: %s", err, out)
	}

	treeID := strings.TrimSpace(run("git", "write-tree", "--prefix=data/expr.tdb/"))
	wantID := lib.GitTreeEntityID(treeID)

	rec := readOnlyRecord(t, root)
	byID := map[string]lib.Node{}
	for _, n := range rec.Graph {
		byID[n.ID()] = n
	}
	node, ok := byID[wantID]
	if !ok {
		t.Fatalf("folder entity %s missing from graph", wantID)
	}
	if types := node.Types(); len(types) != 1 || types[0] != "Collection" {
		t.Errorf("folder @type = %v, want [Collection]", types)
	}
	if node["exo:path"] != "data/expr.tdb/" {
		t.Errorf("folder exo:path = %v, want data/expr.tdb/", node["exo:path"])
	}
}

// activityNode returns rec's single Activity node.
func activityNode(t *testing.T, rec *lib.Record) lib.Node {
	t.Helper()
	for _, n := range rec.Graph {
		types := n.Types()
		if len(types) == 1 && types[0] == "Activity" {
			return n
		}
	}
	t.Fatal("no Activity node in graph")
	return nil
}

// wantAssociatedWith asserts that activity's wasAssociatedWith references
// exactly ids, regardless of array order (Serialize sorts multi-valued
// properties for a canonical form, so the software agent and the person it
// acted for do not appear in a fixed relative order in the written record).
func wantAssociatedWith(t *testing.T, activity lib.Node, ids ...string) {
	t.Helper()
	if len(ids) == 1 {
		if activity["wasAssociatedWith"] != ids[0] {
			t.Errorf("activity wasAssociatedWith = %v, want %v", activity["wasAssociatedWith"], ids[0])
		}
		return
	}
	got, ok := activity["wasAssociatedWith"].([]any)
	if !ok || len(got) != len(ids) {
		t.Fatalf("activity wasAssociatedWith = %v, want a %d-element array of %v", activity["wasAssociatedWith"], len(ids), ids)
	}
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	for _, v := range got {
		if !want[v.(string)] {
			t.Errorf("unexpected wasAssociatedWith entry %v, want one of %v", v, ids)
		}
	}
}

func readOnlyRecord(t *testing.T, root string) *lib.Record {
	t.Helper()
	files, err := os.ReadDir(filepath.Join(root, ".exohub/provenance"))
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("got %d record files, want 1", len(files))
	}
	data, err := os.ReadFile(filepath.Join(root, ".exohub/provenance", files[0].Name()))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	rec, err := lib.Parse(data)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	return rec
}

func TestAdd_UntrackedInputRejected(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	noLoginToken(t)

	writeFile(t, root, "data/counts.csv", "x")
	writeFile(t, root, "results/de.parquet", "out")
	run("git", "add", "data/counts.csv", "results/de.parquet")
	run("git", "commit", "-q", "-m", "init")
	writeFile(t, root, "data/new.csv", "y")

	_, err := runAddCmd(t, "--generated", "results/de.parquet", "--used", "data/new.csv", "--agent", "person:alice")
	if err == nil {
		t.Fatal("expected an error for an untracked --used path")
	}
	if !strings.Contains(err.Error(), "exo add") {
		t.Errorf("error = %v, want a hint to run exo add", err)
	}

	if _, statErr := os.Stat(filepath.Join(root, ".exohub/provenance")); !os.IsNotExist(statErr) {
		t.Errorf(".exohub/provenance was created, want nothing written")
	}
}

func TestAdd_ModifiedWorktreeRejected(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	noLoginToken(t)

	writeFile(t, root, "data/counts.csv", "x")
	writeFile(t, root, "results/de.parquet", "out")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")
	writeFile(t, root, "data/counts.csv", "changed")

	_, err := runAddCmd(t, "--generated", "results/de.parquet", "--used", "data/counts.csv", "--agent", "person:alice")
	if err == nil {
		t.Fatal("expected an error for a path modified relative to the index")
	}
	if !strings.Contains(err.Error(), "exo add") {
		t.Errorf("error = %v, want a hint to run exo add", err)
	}
}

func TestAdd_FolderWithUntrackedMemberRejected(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	noLoginToken(t)

	writeFile(t, root, "data/expr.tdb/a.frag", "a")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")
	writeFile(t, root, "data/expr.tdb/b.frag", "b")

	_, err := runAddCmd(t, "--generated", "data/expr.tdb/", "--agent", "person:alice")
	if err == nil {
		t.Fatal("expected an error for a folder with an untracked member")
	}
}

// TestAdd_JSONGoesToStdoutOnly is a regression test for review item 1 on
// !257: cobra's cmd.Println/Printf default to OutOrStderr, so a caller
// piping `exo provenance add --json` output (stdout only, discarding
// stderr) previously got nothing.
func TestAdd_JSONGoesToStdoutOnly(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	noLoginToken(t)

	writeFile(t, root, "results/de.parquet", "out")
	run("git", "add", "results/de.parquet")

	stdout, stderr, err := runAddCmdSeparated(t, "--generated", "results/de.parquet", "--agent", "person:alice", "--json")
	if err != nil {
		t.Fatalf("add error = %v, stderr: %s", err, stderr)
	}
	if strings.TrimSpace(stderr) != "" {
		t.Errorf("stderr = %q, want empty (no diagnostics on success)", stderr)
	}
	var asJSON map[string]any
	if err := json.Unmarshal([]byte(stdout), &asJSON); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\nstdout: %s", err, stdout)
	}
	if _, ok := asJSON["path"]; !ok {
		t.Errorf("stdout JSON = %+v, want a path field", asJSON)
	}
}

func TestAdd_DryRunWritesNothing(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	noLoginToken(t)

	writeFile(t, root, "data/counts.csv", "x")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")
	writeFile(t, root, "results/de.parquet", "out")
	run("git", "add", "results/de.parquet")

	out, err := runAddCmd(t, "--generated", "results/de.parquet", "--used", "data/counts.csv", "--agent", "person:alice", "--dry-run")
	if err != nil {
		t.Fatalf("add error = %v", err)
	}
	if strings.TrimSpace(out) == "" {
		t.Error("expected the record to be printed on --dry-run")
	}
	var asJSON map[string]any
	if err := json.Unmarshal([]byte(out), &asJSON); err != nil {
		t.Errorf("dry-run output is not valid JSON: %v\n%s", err, out)
	}

	if _, statErr := os.Stat(filepath.Join(root, ".exohub/provenance")); !os.IsNotExist(statErr) {
		t.Error(".exohub/provenance was created on --dry-run")
	}
	staged := run("git", "status", "--porcelain", "--", ".exohub")
	if strings.TrimSpace(staged) != "" {
		t.Errorf("something was staged under .exohub on --dry-run: %q", staged)
	}
}

func TestAdd_NoLoginTokenRequiresAgent(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	noLoginToken(t)

	writeFile(t, root, "data/counts.csv", "x")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")
	writeFile(t, root, "results/de.parquet", "out")
	run("git", "add", "results/de.parquet")

	_, err := runAddCmd(t, "--generated", "results/de.parquet", "--used", "data/counts.csv")
	if err == nil {
		t.Fatal("expected an error without a login token or --agent")
	}
	if !strings.Contains(err.Error(), "exo login") || !strings.Contains(err.Error(), "--agent") {
		t.Errorf("error = %v, want a hint about exo login or --agent", err)
	}
}

func TestAdd_LoginTokenIdentity(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	jwt := fakeJWT(`{"preferred_username":"bob","name":"Bob Smith"}`)
	withLoginToken(t, jwt)

	writeFile(t, root, "results/de.parquet", "out")
	run("git", "add", "results/de.parquet")

	_, err := runAddCmd(t, "--generated", "results/de.parquet")
	if err != nil {
		t.Fatalf("add error = %v", err)
	}

	rec := readOnlyRecord(t, root)
	if rec.AssertedBy != lib.PersonAgentID("bob") {
		t.Errorf("AssertedBy = %q, want %q", rec.AssertedBy, lib.PersonAgentID("bob"))
	}
	var person lib.Node
	for _, n := range rec.Graph {
		if n.ID() == lib.PersonAgentID("bob") {
			person = n
		}
	}
	if person == nil {
		t.Fatal("person agent missing from graph")
	}
	if person["label"] != "Bob Smith" {
		t.Errorf("label = %v, want Bob Smith", person["label"])
	}
}

func TestAdd_SoftwareAgentOnBehalfOf(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	noLoginToken(t)

	writeFile(t, root, "data/counts.csv", "x")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")
	writeFile(t, root, "results/de.parquet", "out")
	run("git", "add", "results/de.parquet")

	_, err := runAddCmd(t,
		"--generated", "results/de.parquet",
		"--used", "data/counts.csv",
		"--agent", "software:claude-code",
		"--model", "claude-sonnet",
		"--on-behalf-of", "person:alice",
		"--evidence", "scripts/de.R reads data/counts.csv and writes results/de.parquet",
	)
	if err != nil {
		t.Fatalf("add error = %v", err)
	}

	rec := readOnlyRecord(t, root)
	if rec.AssertedBy != lib.SoftwareAgentID("claude-code") {
		t.Errorf("AssertedBy = %q, want %q", rec.AssertedBy, lib.SoftwareAgentID("claude-code"))
	}
	if rec.Evidence == "" {
		t.Error("Evidence is empty")
	}

	byID := map[string]lib.Node{}
	for _, n := range rec.Graph {
		byID[n.ID()] = n
	}
	sw, ok := byID[lib.SoftwareAgentID("claude-code")]
	if !ok {
		t.Fatal("software agent missing from graph")
	}
	if sw["exo:model"] != "claude-sonnet" {
		t.Errorf("exo:model = %v, want claude-sonnet", sw["exo:model"])
	}
	if sw["actedOnBehalfOf"] != lib.PersonAgentID("alice") {
		t.Errorf("actedOnBehalfOf = %v, want %v", sw["actedOnBehalfOf"], lib.PersonAgentID("alice"))
	}
	person, ok := byID[lib.PersonAgentID("alice")]
	if !ok {
		t.Fatal("person agent missing from graph")
	}
	if types := person.Types(); len(types) != 1 || types[0] != "Person" {
		t.Errorf("person @type = %v, want [Person]", types)
	}

	wantAssociatedWith(t, activityNode(t, rec), lib.SoftwareAgentID("claude-code"), lib.PersonAgentID("alice"))
}

func TestAdd_NoDerivedFrom(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	noLoginToken(t)

	writeFile(t, root, "data/counts.csv", "x")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")
	writeFile(t, root, "results/de.parquet", "out")
	run("git", "add", "results/de.parquet")

	_, err := runAddCmd(t, "--generated", "results/de.parquet", "--used", "data/counts.csv", "--agent", "person:alice", "--no-derived-from")
	if err != nil {
		t.Fatalf("add error = %v", err)
	}

	rec := readOnlyRecord(t, root)
	for _, n := range rec.Graph {
		if _, has := n["wasDerivedFrom"]; has {
			t.Errorf("node %s has wasDerivedFrom with --no-derived-from", n.ID())
		}
	}
}

func TestAdd_AnnexedGeneratedFileStagedAsGitBlobRecord(t *testing.T) {
	root, run := addTestRepo(t, true)
	chdir(t, root)
	noLoginToken(t)

	writeFile(t, root, "data/counts.csv", "x")
	run("git", "annex", "add", "data/counts.csv")
	run("git", "commit", "-q", "-m", "init")

	annexOut := run("git", "annex", "lookupkey", "data/counts.csv")
	annexKey := strings.TrimSpace(annexOut)

	_, err := runAddCmd(t, "--generated", "data/counts.csv", "--agent", "person:alice")
	if err != nil {
		t.Fatalf("add error = %v", err)
	}

	rec := readOnlyRecord(t, root)
	byID := map[string]lib.Node{}
	for _, n := range rec.Graph {
		byID[n.ID()] = n
	}
	if _, ok := byID[lib.AnnexEntityID(annexKey)]; !ok {
		t.Fatalf("expected annex entity %s in graph, got %v", lib.AnnexEntityID(annexKey), byID)
	}

	files, err := os.ReadDir(filepath.Join(root, ".exohub/provenance"))
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	recordPath := ".exohub/provenance/" + files[0].Name()
	mode := strings.Fields(run("git", "ls-files", "--stage", "--", recordPath))[0]
	if mode != "100644" {
		t.Errorf("record file mode = %s, want 100644 (a plain git blob, not an annex symlink)", mode)
	}
}

func TestAdd_FromSubdirectory(t *testing.T) {
	root, run := addTestRepo(t, false)
	noLoginToken(t)

	writeFile(t, root, "data/counts.csv", "x")
	writeFile(t, root, "scripts/de.R", "# script")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")
	writeFile(t, root, "results/de.parquet", "out")
	run("git", "add", "results/de.parquet")

	// Run from inside data/, using paths relative to that subdirectory.
	chdir(t, filepath.Join(root, "data"))

	_, err := runAddCmd(t,
		"--generated", "../results/de.parquet",
		"--used", "counts.csv",
		"--plan", "../scripts/de.R",
		"--agent", "person:alice",
	)
	if err != nil {
		t.Fatalf("add error = %v", err)
	}

	rec := readOnlyRecord(t, root)
	byID := map[string]lib.Node{}
	for _, n := range rec.Graph {
		byID[n.ID()] = n
	}

	countsBlob := gitBlobID(t, run, "data/counts.csv")
	deBlob := gitBlobID(t, run, "results/de.parquet")
	scriptBlob := gitBlobID(t, run, "scripts/de.R")

	generated, ok := byID[lib.GitBlobEntityID(deBlob)]
	if !ok {
		t.Fatalf("generated entity missing from graph: %v", byID)
	}
	if generated["exo:path"] != "results/de.parquet" {
		t.Errorf("generated exo:path = %v, want repo-root-relative results/de.parquet", generated["exo:path"])
	}

	used, ok := byID[lib.GitBlobEntityID(countsBlob)]
	if !ok {
		t.Fatalf("used entity missing from graph: %v", byID)
	}
	if used["exo:path"] != "data/counts.csv" {
		t.Errorf("used exo:path = %v, want repo-root-relative data/counts.csv", used["exo:path"])
	}

	planNode, ok := byID[lib.GitBlobEntityID(scriptBlob)]
	if !ok {
		t.Fatalf("plan entity missing from graph: %v", byID)
	}
	if planNode["exo:path"] != "scripts/de.R" {
		t.Errorf("plan exo:path = %v, want repo-root-relative scripts/de.R", planNode["exo:path"])
	}
}

func TestAdd_PathOutsideRepoRejected(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	noLoginToken(t)

	writeFile(t, root, "data/counts.csv", "x")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	_, err := runAddCmd(t, "--generated", "../outside.csv", "--agent", "person:alice")
	if err == nil {
		t.Fatal("expected an error for a path outside the repository")
	}
	if !strings.Contains(err.Error(), "outside the repository") {
		t.Errorf("error = %v, want a mention of being outside the repository", err)
	}
}

func TestAdd_IgnoredInputGetsAddHint(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	noLoginToken(t)

	writeFile(t, root, ".gitignore", "ignored.csv\n")
	writeFile(t, root, "results/de.parquet", "out")
	run("git", "add", ".gitignore", "results/de.parquet")
	run("git", "commit", "-q", "-m", "init")
	writeFile(t, root, "ignored.csv", "x")

	_, err := runAddCmd(t, "--generated", "results/de.parquet", "--used", "ignored.csv", "--agent", "person:alice")
	if err == nil {
		t.Fatal("expected an error for a gitignored --used path")
	}
	if !strings.Contains(err.Error(), "exo add") {
		t.Errorf("error = %v, want a hint to run exo add", err)
	}
}

func TestAdd_LiteralPathspecsInWorktreeCheck(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	noLoginToken(t)

	// A path containing a pathspec magic character ("[") must not be
	// interpreted as a glob by checkWorktreeClean: without
	// --literal-pathspecs, "data/[x].csv" could match unrelated files.
	writeFile(t, root, "data/[x].csv", "a")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")
	writeFile(t, root, "results/de.parquet", "out")
	run("git", "add", "results/de.parquet")

	_, err := runAddCmd(t, "--generated", "results/de.parquet", "--used", "data/[x].csv", "--agent", "person:alice")
	if err != nil {
		t.Fatalf("add error = %v", err)
	}
}

func TestAdd_InvalidStartedEndedRejected(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	noLoginToken(t)

	writeFile(t, root, "results/de.parquet", "out")
	run("git", "add", "results/de.parquet")

	if _, err := runAddCmd(t, "--generated", "results/de.parquet", "--agent", "person:alice", "--started", "not-a-time"); err == nil {
		t.Error("expected an error for an invalid --started")
	}
	if _, err := runAddCmd(t, "--generated", "results/de.parquet", "--agent", "person:alice", "--ended", "not-a-time"); err == nil {
		t.Error("expected an error for an invalid --ended")
	}
	if _, err := runAddCmd(t, "--generated", "results/de.parquet", "--agent", "person:alice", "--started", "2026-01-01T00:00:00Z", "--ended", "2026-01-01T00:05:00Z"); err != nil {
		t.Errorf("add error = %v", err)
	}
}

func TestAdd_InvalidSupersedesRejected(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	noLoginToken(t)

	writeFile(t, root, "results/de.parquet", "out")
	run("git", "add", "results/de.parquet")

	if _, err := runAddCmd(t, "--generated", "results/de.parquet", "--agent", "person:alice", "--supersedes", "not-a-record-id"); err == nil {
		t.Error("expected an error for an invalid --supersedes")
	}

	validID := lib.NewRecordID()
	_, err := runAddCmd(t, "--generated", "results/de.parquet", "--agent", "person:alice", "--supersedes", validID)
	if err != nil {
		t.Fatalf("add error = %v", err)
	}
	rec := readOnlyRecord(t, root)
	if len(rec.Supersedes) != 1 || rec.Supersedes[0] != validID {
		t.Errorf("Supersedes = %v, want [%s]", rec.Supersedes, validID)
	}
}

func TestAdd_ModelRequiresSoftwareAgent(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	noLoginToken(t)

	writeFile(t, root, "results/de.parquet", "out")
	run("git", "add", "results/de.parquet")

	if _, err := runAddCmd(t, "--generated", "results/de.parquet", "--model", "claude-sonnet"); err == nil {
		t.Error("expected an error for --model without an explicit software agent")
	}
	if _, err := runAddCmd(t, "--generated", "results/de.parquet", "--agent", "person:alice", "--model", "claude-sonnet"); err == nil {
		t.Error("expected an error for --model with a person agent")
	}
}

func TestAdd_SoftwareAgentDefaultsOnBehalfOfToLoginIdentity(t *testing.T) {
	root, run := addTestRepo(t, false)
	jwt := fakeJWT(`{"preferred_username":"bob","name":"Bob Smith"}`)
	withLoginToken(t, jwt)
	chdir(t, root)

	writeFile(t, root, "results/de.parquet", "out")
	run("git", "add", "results/de.parquet")

	_, err := runAddCmd(t, "--generated", "results/de.parquet", "--agent", "software:claude-code")
	if err != nil {
		t.Fatalf("add error = %v", err)
	}

	rec := readOnlyRecord(t, root)
	byID := map[string]lib.Node{}
	for _, n := range rec.Graph {
		byID[n.ID()] = n
	}
	sw, ok := byID[lib.SoftwareAgentID("claude-code")]
	if !ok {
		t.Fatal("software agent missing from graph")
	}
	if sw["actedOnBehalfOf"] != lib.PersonAgentID("bob") {
		t.Errorf("actedOnBehalfOf = %v, want %v (defaulted from the login token)", sw["actedOnBehalfOf"], lib.PersonAgentID("bob"))
	}
	person, ok := byID[lib.PersonAgentID("bob")]
	if !ok {
		t.Fatal("person agent missing from graph")
	}
	if person["label"] != "Bob Smith" {
		t.Errorf("label = %v, want Bob Smith", person["label"])
	}

	wantAssociatedWith(t, activityNode(t, rec), lib.SoftwareAgentID("claude-code"), lib.PersonAgentID("bob"))
}

func TestAdd_SoftwareAgentNoTokenNoOnBehalfOf(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	noLoginToken(t)

	writeFile(t, root, "results/de.parquet", "out")
	run("git", "add", "results/de.parquet")

	_, err := runAddCmd(t, "--generated", "results/de.parquet", "--agent", "software:claude-code")
	if err != nil {
		t.Fatalf("add error = %v", err)
	}

	rec := readOnlyRecord(t, root)
	for _, n := range rec.Graph {
		if n.Types() != nil && len(n.Types()) == 1 && n.Types()[0] == "Person" {
			t.Errorf("unexpected person node %v with no token and no --on-behalf-of", n)
		}
		if _, has := n["actedOnBehalfOf"]; has {
			t.Errorf("node %s has actedOnBehalfOf with no token and no --on-behalf-of", n.ID())
		}
	}

	wantAssociatedWith(t, activityNode(t, rec), lib.SoftwareAgentID("claude-code"))
}

// symlinkedRepo creates a repo under root (via addTestRepo) plus a symlink
// to root in a separate temp directory, returning the symlink path. This
// reproduces a symlinked home/data directory, as found on shared clusters:
// `git rev-parse --show-toplevel` resolves to the real root, while
// os.Getwd() (and a user-supplied absolute path) can go through the
// symlink.
func symlinkedRepo(t *testing.T) (root, link string, run func(args ...string) string) {
	t.Helper()
	root, run = addTestRepo(t, false)
	link = filepath.Join(t.TempDir(), "repo-link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}
	return root, link, run
}

func TestAdd_FromSymlinkedCwd(t *testing.T) {
	root, link, run := symlinkedRepo(t)
	noLoginToken(t)

	writeFile(t, root, "data/x.csv", "x")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	// cd through the symlink, then use a path relative to that cwd.
	chdir(t, filepath.Join(link, "data"))

	out, err := runAddCmd(t, "--generated", "x.csv", "--agent", "person:alice", "--dry-run")
	if err != nil {
		t.Fatalf("add error = %v", err)
	}
	rec := parseDryRunRecord(t, out)
	assertHasPath(t, rec, "data/x.csv")
}

func TestAdd_AbsolutePathThroughSymlink(t *testing.T) {
	root, link, run := symlinkedRepo(t)
	noLoginToken(t)

	writeFile(t, root, "data/x.csv", "x")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")
	chdir(t, root)

	// An absolute path that goes through the symlink, not the real root.
	absThroughLink := filepath.Join(link, "data", "x.csv")

	out, err := runAddCmd(t, "--generated", absThroughLink, "--agent", "person:alice", "--dry-run")
	if err != nil {
		t.Fatalf("add error = %v", err)
	}
	rec := parseDryRunRecord(t, out)
	assertHasPath(t, rec, "data/x.csv")
}

func parseDryRunRecord(t *testing.T, out string) *lib.Record {
	t.Helper()
	rec, err := lib.Parse([]byte(out))
	if err != nil {
		t.Fatalf("Parse() error = %v\noutput: %s", err, out)
	}
	return rec
}

func assertHasPath(t *testing.T, rec *lib.Record, wantPath string) {
	t.Helper()
	for _, n := range rec.Graph {
		if n["exo:path"] == wantPath {
			return
		}
	}
	t.Errorf("no graph node has exo:path %q", wantPath)
}

func generatedNode(t *testing.T, rec *lib.Record, id string) lib.Node {
	t.Helper()
	for _, n := range rec.Graph {
		if n.ID() == id {
			return n
		}
	}
	t.Fatalf("node %s missing from graph", id)
	return nil
}

func TestAdd_RevisionOfIRI(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	noLoginToken(t)

	writeFile(t, root, "data/expr.tdb/a.frag", "a")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	const oldIRI = "urn:exohub:git:tree:0000000000000000000000000000000000000000"
	_, err := runAddCmd(t, "--generated", "data/expr.tdb/", "--revision-of", oldIRI, "--agent", "person:alice")
	if err != nil {
		t.Fatalf("add error = %v", err)
	}

	rec := readOnlyRecord(t, root)
	treeID := strings.TrimSpace(run("git", "write-tree", "--prefix=data/expr.tdb/"))
	generated := generatedNode(t, rec, lib.GitTreeEntityID(treeID))
	if generated["wasRevisionOf"] != oldIRI {
		t.Errorf("wasRevisionOf = %v, want %v", generated["wasRevisionOf"], oldIRI)
	}
	if _, has := generated["wasDerivedFrom"]; has {
		t.Error("generated entity has wasDerivedFrom from a --revision-of target, want none")
	}
}

func TestAdd_RevisionOfRefFolder(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	noLoginToken(t)

	writeFile(t, root, "data/expr.tdb/a.frag", "a")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "v1")
	run("git", "tag", "v1")
	oldTreeID := strings.TrimSpace(run("git", "write-tree", "--prefix=data/expr.tdb/"))

	writeFile(t, root, "data/expr.tdb/b.frag", "b")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "v2")

	_, err := runAddCmd(t, "--generated", "data/expr.tdb/", "--revision-of", "v1:data/expr.tdb/", "--agent", "person:alice")
	if err != nil {
		t.Fatalf("add error = %v", err)
	}

	rec := readOnlyRecord(t, root)
	newTreeID := strings.TrimSpace(run("git", "write-tree", "--prefix=data/expr.tdb/"))
	generated := generatedNode(t, rec, lib.GitTreeEntityID(newTreeID))
	if generated["wasRevisionOf"] != lib.GitTreeEntityID(oldTreeID) {
		t.Errorf("wasRevisionOf = %v, want %v", generated["wasRevisionOf"], lib.GitTreeEntityID(oldTreeID))
	}
	old := generatedNode(t, rec, lib.GitTreeEntityID(oldTreeID))
	if old["exo:path"] != "data/expr.tdb/" {
		t.Errorf("old entity exo:path = %v, want data/expr.tdb/", old["exo:path"])
	}
}

func TestAdd_RevisionOfRefFile(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	noLoginToken(t)

	writeFile(t, root, "data/counts.csv", "v1")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "v1")
	run("git", "tag", "v1")
	oldBlob := gitBlobID(t, run, "data/counts.csv")

	writeFile(t, root, "data/counts.csv", "v2")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "v2")

	_, err := runAddCmd(t, "--generated", "data/counts.csv", "--revision-of", "v1:data/counts.csv", "--agent", "person:alice")
	if err != nil {
		t.Fatalf("add error = %v", err)
	}

	rec := readOnlyRecord(t, root)
	newBlob := gitBlobID(t, run, "data/counts.csv")
	generated := generatedNode(t, rec, lib.GitBlobEntityID(newBlob))
	if generated["wasRevisionOf"] != lib.GitBlobEntityID(oldBlob) {
		t.Errorf("wasRevisionOf = %v, want %v", generated["wasRevisionOf"], lib.GitBlobEntityID(oldBlob))
	}
}

func TestAdd_RevisionOfSamePathRejected(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	noLoginToken(t)

	writeFile(t, root, "data/expr.tdb/a.frag", "a")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	_, err := runAddCmd(t, "--generated", "data/expr.tdb/", "--revision-of", "data/expr.tdb/", "--agent", "person:alice")
	if err == nil {
		t.Fatal("expected an error for --revision-of equal to the --generated path")
	}
	if !strings.Contains(err.Error(), "cannot be a revision of itself") {
		t.Errorf("error = %v, want a self-revision message", err)
	}
	if !strings.Contains(err.Error(), "<ref>:") {
		t.Errorf("error = %v, want a hint about <ref>:<path>", err)
	}
}

func TestAdd_MultipleRevisionOf(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	noLoginToken(t)

	writeFile(t, root, "data/a.csv", "a")
	writeFile(t, root, "data/b.csv", "b")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")
	writeFile(t, root, "results/merged.csv", "merged")
	run("git", "add", "results/merged.csv")

	aBlob := gitBlobID(t, run, "data/a.csv")
	bBlob := gitBlobID(t, run, "data/b.csv")

	_, err := runAddCmd(t, "--generated", "results/merged.csv", "--revision-of", "data/a.csv", "--revision-of", "data/b.csv", "--agent", "person:alice")
	if err != nil {
		t.Fatalf("add error = %v", err)
	}

	rec := readOnlyRecord(t, root)
	mergedBlob := gitBlobID(t, run, "results/merged.csv")
	generated := generatedNode(t, rec, lib.GitBlobEntityID(mergedBlob))
	revisionOf, ok := generated["wasRevisionOf"].([]any)
	if !ok || len(revisionOf) != 2 {
		t.Fatalf("wasRevisionOf = %v, want a 2-element array", generated["wasRevisionOf"])
	}
	want := map[string]bool{lib.GitBlobEntityID(aBlob): true, lib.GitBlobEntityID(bBlob): true}
	for _, id := range revisionOf {
		if !want[id.(string)] {
			t.Errorf("unexpected wasRevisionOf entry %v", id)
		}
	}
}

func TestAdd_RevisionOfDryRunAndValidate(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	noLoginToken(t)

	writeFile(t, root, "data/expr.tdb/a.frag", "a")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "v1")
	run("git", "tag", "v1")

	writeFile(t, root, "data/expr.tdb/b.frag", "b")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "v2")

	out, err := runAddCmd(t, "--generated", "data/expr.tdb/", "--revision-of", "v1:data/expr.tdb/", "--agent", "person:alice", "--dry-run")
	if err != nil {
		t.Fatalf("add error = %v", err)
	}
	if !strings.Contains(out, "wasRevisionOf") {
		t.Errorf("dry-run output = %s, want wasRevisionOf", out)
	}
	if _, statErr := os.Stat(filepath.Join(root, ".exohub/provenance")); !os.IsNotExist(statErr) {
		t.Error(".exohub/provenance was created on --dry-run")
	}

	_, err = runAddCmd(t, "--generated", "data/expr.tdb/", "--revision-of", "v1:data/expr.tdb/", "--agent", "person:alice")
	if err != nil {
		t.Fatalf("add error = %v", err)
	}
	vout, verr := runValidateCmd(t)
	if verr != nil {
		t.Fatalf("exo provenance validate error = %v, output: %s", verr, vout)
	}
}

// TestAdd_RevisionOfPlainPathContainingColon is a regression test: a plain
// path containing ":" (not a <ref>:<path> value, since nothing before the
// ":" resolves to a ref) must still resolve as an ordinary repository path.
func TestAdd_RevisionOfPlainPathContainingColon(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	noLoginToken(t)

	writeFile(t, root, "data/a:b.txt", "x")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")
	writeFile(t, root, "results/out.txt", "out")
	run("git", "add", "results/out.txt")

	_, err := runAddCmd(t, "--generated", "results/out.txt", "--revision-of", "data/a:b.txt", "--agent", "person:alice")
	if err != nil {
		t.Fatalf("add error = %v", err)
	}

	rec := readOnlyRecord(t, root)
	colonBlob := gitBlobID(t, run, "data/a:b.txt")
	outBlob := gitBlobID(t, run, "results/out.txt")
	generated := generatedNode(t, rec, lib.GitBlobEntityID(outBlob))
	if generated["wasRevisionOf"] != lib.GitBlobEntityID(colonBlob) {
		t.Errorf("wasRevisionOf = %v, want %v (data/a:b.txt resolved as a path)", generated["wasRevisionOf"], lib.GitBlobEntityID(colonBlob))
	}
}

// TestAdd_RevisionOfOnlyCoverage is a regression test for D14: a file whose
// only statement is wasRevisionOf still counts as covered.
func TestAdd_RevisionOfOnlyCoverage(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	noLoginToken(t)

	writeFile(t, root, "data/expr.tdb/a.frag", "a")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "v1")
	run("git", "tag", "v1")

	writeFile(t, root, "data/expr.tdb/b.frag", "b")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "v2")

	_, err := runAddCmd(t, "--generated", "data/expr.tdb/", "--revision-of", "v1:data/expr.tdb/", "--agent", "person:alice")
	if err != nil {
		t.Fatalf("add error = %v", err)
	}

	vout, verr := runValidateCmd(t, "--json")
	if verr != nil {
		t.Fatalf("exo provenance validate error = %v, output: %s", verr, vout)
	}
	if !strings.Contains(vout, `"covered"`) {
		t.Fatalf("validate --json output missing coverage summary: %s", vout)
	}
	var asJSON struct {
		Coverage struct {
			Covered   int `json:"covered"`
			Uncovered int `json:"uncovered"`
		} `json:"coverage"`
	}
	if err := json.Unmarshal([]byte(vout), &asJSON); err != nil {
		t.Fatalf("json.Unmarshal() error = %v\noutput: %s", err, vout)
	}
	if asJSON.Coverage.Uncovered != 0 {
		t.Errorf("Coverage.Uncovered = %d, want 0 (the folder is covered by wasRevisionOf alone)", asJSON.Coverage.Uncovered)
	}
}

// recordFileCount returns the number of record files under
// .exohub/provenance/ (ignoring any non-.jsonld entries).
func recordFileCount(t *testing.T, root string) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, ".exohub/provenance"))
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	n := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".jsonld") {
			n++
		}
	}
	return n
}

// writeManualRecord writes a record directly to .exohub/provenance/
// (bypassing `exo provenance add`, so it is untracked unless the caller
// stages it) stating wasGeneratedBy generatedID from a fresh activity, and
// returns its @id and repo-relative path.
func writeManualRecord(t *testing.T, root, generatedID string, supersedes ...string) (recordID, path string) {
	t.Helper()
	activityID := lib.NewActivityID()
	rec := &lib.Record{
		Context:         lib.ContextIRI,
		ID:              lib.NewRecordID(),
		Assertion:       lib.AssertionAsserted,
		AssertedBy:      lib.PersonAgentID("alice"),
		GeneratedAtTime: time.Now().UTC().Format(time.RFC3339),
		Supersedes:      supersedes,
		Graph: []lib.Node{
			{"@id": activityID, "@type": "Activity"},
			{"@id": generatedID, "@type": "Entity", "wasGeneratedBy": activityID},
		},
	}
	data, err := lib.Serialize(rec)
	if err != nil {
		t.Fatalf("Serialize() error = %v", err)
	}
	name, err := lib.RecordFileName(time.Now(), "manual", rec.ID)
	if err != nil {
		t.Fatalf("RecordFileName() error = %v", err)
	}
	path = lib.RecordDir + "/" + name
	writeFile(t, root, path, string(data))
	return rec.ID, path
}

// TestAdd_DuplicateGenerationRejected covers the issue: re-running
// `add --generated X` must not write a record that conflicts with an
// existing one (a new activity claiming wasGeneratedBy for the same
// entity).
func TestAdd_DuplicateGenerationRejected(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	noLoginToken(t)

	writeFile(t, root, "data/raw/x.gz", "x")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	if _, err := runAddCmd(t, "--generated", "data/raw/x.gz", "--agent", "person:alice"); err != nil {
		t.Fatalf("first add error = %v", err)
	}
	if n := recordFileCount(t, root); n != 1 {
		t.Fatalf("got %d record files after first add, want 1", n)
	}

	_, err := runAddCmd(t, "--generated", "data/raw/x.gz", "--agent", "person:alice")
	if err == nil {
		t.Fatal("expected an error re-running add for an already recorded file")
	}
	if !strings.Contains(err.Error(), "already has provenance") {
		t.Errorf("error = %v, want \"already has provenance\"", err)
	}
	if !strings.Contains(err.Error(), "--supersedes") || !strings.Contains(err.Error(), "--skip-existing") {
		t.Errorf("error = %v, want hints for --supersedes and --skip-existing", err)
	}
	if n := recordFileCount(t, root); n != 1 {
		t.Errorf("got %d record files after rejected add, want 1 (nothing written)", n)
	}
}

// TestAdd_DuplicateGenerationAllowedWithSupersedes covers the correction
// workflow: naming every conflicting record with --supersedes allows the
// new record to be written.
func TestAdd_DuplicateGenerationAllowedWithSupersedes(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	noLoginToken(t)

	writeFile(t, root, "data/raw/x.gz", "x")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	if _, err := runAddCmd(t, "--generated", "data/raw/x.gz", "--agent", "person:alice"); err != nil {
		t.Fatalf("first add error = %v", err)
	}
	firstRecord := readOnlyRecord(t, root)

	_, err := runAddCmd(t, "--generated", "data/raw/x.gz", "--agent", "person:alice", "--supersedes", firstRecord.ID)
	if err != nil {
		t.Fatalf("second add error = %v", err)
	}
	if n := recordFileCount(t, root); n != 2 {
		t.Errorf("got %d record files, want 2", n)
	}
}

// TestAdd_SkipExistingAllCovered covers --skip-existing when every
// --generated entry is already covered: nothing is written and the
// command exits 0, so a generation loop can be re-run safely.
func TestAdd_SkipExistingAllCovered(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	noLoginToken(t)

	writeFile(t, root, "data/raw/x.gz", "x")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	if _, err := runAddCmd(t, "--generated", "data/raw/x.gz", "--agent", "person:alice"); err != nil {
		t.Fatalf("first add error = %v", err)
	}

	out, err := runAddCmd(t, "--generated", "data/raw/x.gz", "--agent", "person:alice", "--skip-existing")
	if err != nil {
		t.Fatalf("add --skip-existing error = %v, output: %s", err, out)
	}
	if n := recordFileCount(t, root); n != 1 {
		t.Errorf("got %d record files, want 1 (nothing written)", n)
	}
}

// TestAdd_SkipExistingPartialCoverage covers --skip-existing when only
// some --generated entries are already covered: a record is written for
// the rest.
func TestAdd_SkipExistingPartialCoverage(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	noLoginToken(t)

	writeFile(t, root, "data/raw/x.gz", "x")
	writeFile(t, root, "data/raw/y.gz", "y")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	if _, err := runAddCmd(t, "--generated", "data/raw/x.gz", "--agent", "person:alice"); err != nil {
		t.Fatalf("first add error = %v", err)
	}

	_, stderr, err := runAddCmdSeparated(t, "--generated", "data/raw/x.gz", "--generated", "data/raw/y.gz", "--agent", "person:alice", "--skip-existing")
	if err != nil {
		t.Fatalf("add --skip-existing error = %v, stderr: %s", err, stderr)
	}
	if !strings.Contains(stderr, "data/raw/x.gz") {
		t.Errorf("stderr = %q, want a note about skipping data/raw/x.gz", stderr)
	}
	if n := recordFileCount(t, root); n != 2 {
		t.Fatalf("got %d record files, want 2", n)
	}

	yOID := gitBlobID(t, run, "data/raw/y.gz")
	xOID := gitBlobID(t, run, "data/raw/x.gz")

	files, err := os.ReadDir(filepath.Join(root, ".exohub/provenance"))
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	var sawY bool
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(root, ".exohub/provenance", f.Name()))
		if err != nil {
			t.Fatalf("ReadFile() error = %v", err)
		}
		rec, err := lib.Parse(data)
		if err != nil {
			t.Fatalf("Parse() error = %v", err)
		}
		for _, n := range rec.Graph {
			if n.ID() == lib.GitBlobEntityID(yOID) {
				if _, has := n["wasGeneratedBy"]; has {
					sawY = true
				}
			}
			if n.ID() == lib.GitBlobEntityID(xOID) {
				if _, has := n["wasGeneratedBy"]; has && f.Name() != files[0].Name() {
					t.Errorf("new record unexpectedly also generates data/raw/x.gz")
				}
			}
		}
	}
	if !sawY {
		t.Error("no record generates data/raw/y.gz; --skip-existing should have kept the uncovered entry")
	}
}

// TestAdd_DuplicateGenerationUntrackedExistingRecordDetected covers the
// issue's requirement that an untracked existing record (hand-written, not
// yet staged) is still detected by the targeted `git grep --untracked`
// lookup.
func TestAdd_DuplicateGenerationUntrackedExistingRecordDetected(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	noLoginToken(t)

	writeFile(t, root, "data/raw/x.gz", "x")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	xOID := gitBlobID(t, run, "data/raw/x.gz")
	writeManualRecord(t, root, lib.GitBlobEntityID(xOID))

	// The manual record above was never `git add`-ed: confirm it really is
	// untracked before relying on it for the assertion below.
	status := run("git", "status", "--porcelain", "--", ".exohub/provenance")
	if !strings.Contains(status, "??") {
		t.Fatalf("expected the manual record to be untracked, git status: %q", status)
	}

	_, err := runAddCmd(t, "--generated", "data/raw/x.gz", "--agent", "person:alice")
	if err == nil {
		t.Fatal("expected an error: an untracked record already covers data/raw/x.gz")
	}
	if !strings.Contains(err.Error(), "already has provenance") {
		t.Errorf("error = %v, want \"already has provenance\"", err)
	}
	if n := recordFileCount(t, root); n != 1 {
		t.Errorf("got %d record files, want 1 (nothing written)", n)
	}
}

// TestAdd_DuplicateGenerationSupersededRecordDoesNotBlock covers the
// issue's requirement that a superseded record does not block a new
// generation claim.
func TestAdd_DuplicateGenerationSupersededRecordDoesNotBlock(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	noLoginToken(t)

	writeFile(t, root, "data/raw/x.gz", "x")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	xOID := gitBlobID(t, run, "data/raw/x.gz")
	oldID, _ := writeManualRecord(t, root, lib.GitBlobEntityID(xOID))
	// Supersede the old record directly (not through the id being
	// re-generated): the old claim no longer counts, so a fresh `add` for
	// data/raw/x.gz needs no --supersedes of its own.
	writeManualRecord(t, root, "urn:exohub:git:blob:0000000000000000000000000000000000000000", oldID)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "manual records")

	_, err := runAddCmd(t, "--generated", "data/raw/x.gz", "--agent", "person:alice")
	if err != nil {
		t.Fatalf("add error = %v, want success (the conflicting record is superseded)", err)
	}
	if n := recordFileCount(t, root); n != 3 {
		t.Errorf("got %d record files, want 3", n)
	}
}

// TestAdd_DuplicateGenerationDryRun covers the issue's requirement that
// --dry-run reports the same duplicate-generation outcome as a real run,
// writing nothing either way.
func TestAdd_DuplicateGenerationDryRun(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	noLoginToken(t)

	writeFile(t, root, "data/raw/x.gz", "x")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	if _, err := runAddCmd(t, "--generated", "data/raw/x.gz", "--agent", "person:alice"); err != nil {
		t.Fatalf("first add error = %v", err)
	}

	_, err := runAddCmd(t, "--generated", "data/raw/x.gz", "--agent", "person:alice", "--dry-run")
	if err == nil {
		t.Fatal("expected --dry-run to report the same duplicate-generation error")
	}
	if !strings.Contains(err.Error(), "already has provenance") {
		t.Errorf("error = %v, want \"already has provenance\"", err)
	}
	if n := recordFileCount(t, root); n != 1 {
		t.Errorf("got %d record files, want 1 (nothing written)", n)
	}
}
