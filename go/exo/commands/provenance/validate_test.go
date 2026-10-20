package provenance

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func runValidateCmd(t *testing.T, args ...string) (stdout string, err error) {
	t.Helper()
	cmd := newValidateCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return buf.String(), err
}

// runValidateCmdSeparated runs `exo provenance validate` with distinct
// stdout/stderr buffers (review: --json output must be on stdout, not
// cobra's OutOrStderr default).
func runValidateCmdSeparated(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := newValidateCommand()
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return outBuf.String(), errBuf.String(), err
}

func TestValidate_NoRecords(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	writeFile(t, root, "README.md", "hi")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	out, err := runValidateCmd(t)
	if err != nil {
		t.Fatalf("validate error = %v, output: %s", err, out)
	}
	if !strings.Contains(out, "No provenance records found") {
		t.Errorf("output = %q, want a no-records message", out)
	}
}

func TestValidate_ReportsErrorAndExitsNonZero(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	writeFile(t, root, ".exohub/provenance/bad.jsonld", "not json")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	out, err := runValidateCmd(t)
	if err == nil {
		t.Fatalf("validate error = nil, want non-nil; output: %s", out)
	}
	if !strings.Contains(out, "bad.jsonld") {
		t.Errorf("output = %q, want it to name bad.jsonld", out)
	}
}

func TestValidate_JSONOutput(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	writeFile(t, root, ".exohub/provenance/bad.jsonld", "not json")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	out, err := runValidateCmd(t, "--json")
	if err == nil {
		t.Fatalf("validate error = nil, want non-nil; output: %s", out)
	}
	if !strings.Contains(out, `"code": "invalid-record"`) {
		t.Errorf("output = %q, want JSON with invalid-record finding", out)
	}
}

// TestValidate_JSONGoesToStdoutOnly is a regression test for review item 1
// on !257: `exo provenance validate --json 2>/dev/null` previously printed
// 0 lines because cobra's cmd.Println/Printf default to OutOrStderr.
func TestValidate_JSONGoesToStdoutOnly(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	writeFile(t, root, ".exohub/provenance/bad.jsonld", "not json")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	stdout, _, err := runValidateCmdSeparated(t, "--json")
	if err == nil {
		t.Fatalf("validate error = nil, want non-nil (bad.jsonld is invalid); stdout: %s", stdout)
	}
	var report map[string]any
	if decodeErr := json.Unmarshal([]byte(stdout), &report); decodeErr != nil {
		t.Fatalf("stdout is not valid JSON: %v\nstdout: %s", decodeErr, stdout)
	}
	if _, ok := report["findings"]; !ok {
		t.Errorf("stdout JSON = %+v, want a findings field", report)
	}
}

func TestValidate_Staged(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	writeFile(t, root, "README.md", "hi")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	// Staged state has no provenance records: committed worktree-only
	// garbage is not visible via --staged.
	writeFile(t, root, ".exohub/provenance/worktree-only.jsonld", "not json")

	out, err := runValidateCmd(t, "--staged")
	if err != nil {
		t.Fatalf("validate --staged error = %v, output: %s", err, out)
	}
	if !strings.Contains(out, "No provenance records found") {
		t.Errorf("output = %q, want a no-records message for --staged", out)
	}
}

func TestValidate_Ref(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	writeFile(t, root, "README.md", "hi")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")
	run("git", "tag", "v1")

	writeFile(t, root, ".exohub/provenance/bad.jsonld", "not json")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "second")

	out, err := runValidateCmd(t, "--ref", "v1")
	if err != nil {
		t.Fatalf("validate --ref v1 error = %v, output: %s", err, out)
	}
	if !strings.Contains(out, "No provenance records found") {
		t.Errorf("output = %q, want a no-records message at ref v1", out)
	}

	out, err = runValidateCmd(t, "--ref", "HEAD")
	if err == nil {
		t.Fatalf("validate --ref HEAD error = nil, want non-nil; output: %s", out)
	}
	if !strings.Contains(out, "bad.jsonld") {
		t.Errorf("output = %q, want it to name bad.jsonld at HEAD", out)
	}
}

func TestValidate_StagedAndRefMutuallyExclusive(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	run("git", "commit", "--allow-empty", "-q", "-m", "init")

	_, err := runValidateCmd(t, "--staged", "--ref", "HEAD")
	if err == nil {
		t.Fatal("validate --staged --ref error = nil, want non-nil")
	}
}

func TestValidate_PassesOnValidRecord(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	noLoginToken(t)

	writeFile(t, root, "data/counts.csv", "a,b\n1,2\n")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "inputs")
	writeFile(t, root, "results/de.parquet", "fake-parquet")
	run("git", "add", "results/de.parquet")

	addOut, err := runAddCmd(t, "--generated", "results/de.parquet", "--used", "data/counts.csv", "--agent", "person:alice")
	if err != nil {
		t.Fatalf("add error = %v, output: %s", err, addOut)
	}
	run("git", "commit", "-q", "-m", "record")

	out, err := runValidateCmd(t)
	if err != nil {
		t.Fatalf("validate error = %v, output: %s", err, out)
	}
	if !strings.Contains(out, "valid, no warnings") {
		t.Errorf("output = %q, want a valid/no-warnings summary", out)
	}
	if !strings.Contains(out, "Coverage: 1 covered, 1 uncovered") {
		t.Errorf("output = %q, want a coverage summary", out)
	}
}

func TestValidate_RequireViolationReportsError(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	writeFile(t, root, ".exohub/config", "provenance:\n  require:\n    - \"results/**\"\n")
	writeFile(t, root, "results/out.txt", "data")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	out, err := runValidateCmd(t)
	if err == nil {
		t.Fatalf("validate error = nil, want non-nil; output: %s", out)
	}
	if !strings.Contains(out, "results/out.txt") {
		t.Errorf("output = %q, want it to name results/out.txt", out)
	}
}
