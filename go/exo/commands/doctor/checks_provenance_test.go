package doctor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	lib "github.com/Genentech/exohub/go/exo/internal/provenance"
)

func provenanceTestRepo(t *testing.T) (root string, run func(args ...string) string) {
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
	return root, run
}

func writeProvenanceFile(t *testing.T, root, relPath, content string) {
	t.Helper()
	full := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRunProvenanceChecksNoRecords(t *testing.T) {
	root, run := provenanceTestRepo(t)
	writeProvenanceFile(t, root, "README.md", "hi")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	results := runProvenanceChecks(root)
	if len(results) != 1 || results[0].Status != StatusSkip {
		t.Fatalf("results = %+v, want one SKIP", results)
	}
}

func TestRunProvenanceChecksOutsideRepo(t *testing.T) {
	dir := t.TempDir()
	results := runProvenanceChecks(dir)
	if len(results) != 1 || results[0].Status != StatusSkip {
		t.Fatalf("results = %+v, want one SKIP", results)
	}
}

func TestRunProvenanceChecksInvalidRecordFails(t *testing.T) {
	root, run := provenanceTestRepo(t)
	writeProvenanceFile(t, root, lib.RecordDir+"/bad.jsonld", "not json")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	results := runProvenanceChecks(root)
	var sawFail bool
	for _, r := range results {
		if r.Status == StatusFail {
			sawFail = true
		}
	}
	if !sawFail {
		t.Fatalf("results = %+v, want a FAIL", results)
	}
}

func TestRunProvenanceChecksWarningOnly(t *testing.T) {
	root, run := provenanceTestRepo(t)
	rec := fmt.Sprintf(`{
  "@context": %q,
  "@id": %q,
  "exo:assertion": "asserted",
  "exo:assertedBy": "urn:exohub:agent:software:claude-code",
  "generatedAtTime": "2026-10-14T10:00:00Z",
  "@graph": [{"@id": "urn:exohub:git:blob:4b825dc642cb6eb9a060e54bf8d69288fbee4904", "@type": "Entity", "exo:path": "a.txt"}]
}`, lib.ContextIRI, lib.NewRecordID())
	writeProvenanceFile(t, root, lib.RecordDir+"/a.jsonld", rec)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	results := runProvenanceChecks(root)
	var sawWarn, sawFail bool
	for _, r := range results {
		switch r.Status {
		case StatusWarn:
			sawWarn = true
		case StatusFail:
			sawFail = true
		}
	}
	if !sawWarn {
		t.Fatalf("results = %+v, want a WARN (missing evidence)", results)
	}
	if sawFail {
		t.Fatalf("results = %+v, want no FAIL", results)
	}
}

// TestRunProvenanceChecksDeletedRecordHistoryNotSkipped covers review
// comment #7: a repository with zero current records (all deleted) but
// with an immutability violation in its history must still FAIL, not be
// hidden behind the "no records, SKIP" shortcut.
func TestRunProvenanceChecksDeletedRecordHistoryNotSkipped(t *testing.T) {
	root, run := provenanceTestRepo(t)
	rec := fmt.Sprintf(`{
  "@context": %q,
  "@id": %q,
  "exo:assertion": "recorded",
  "exo:assertedBy": "urn:exohub:agent:person:alice",
  "generatedAtTime": "2026-10-14T10:00:00Z",
  "@graph": [{"@id": "urn:exohub:git:blob:4b825dc642cb6eb9a060e54bf8d69288fbee4904", "@type": "Entity", "exo:path": "a.txt"}]
}`, lib.ContextIRI, lib.NewRecordID())
	writeProvenanceFile(t, root, lib.RecordDir+"/a.jsonld", rec)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")
	run("git", "rm", "-q", lib.RecordDir+"/a.jsonld")
	run("git", "commit", "-q", "-m", "delete")

	results := runProvenanceChecks(root)
	var sawFail bool
	for _, r := range results {
		if r.Status == StatusSkip {
			t.Fatalf("results = %+v, must not SKIP: the deletion is an immutability violation", results)
		}
		if r.Status == StatusFail {
			sawFail = true
		}
	}
	if !sawFail {
		t.Fatalf("results = %+v, want a FAIL for the deleted committed record", results)
	}
}

func TestRunProvenanceChecksRequireRulesWithoutRecords(t *testing.T) {
	root, run := provenanceTestRepo(t)
	writeProvenanceFile(t, root, ".exohub/config", "provenance:\n  require:\n    - \"results/**\"\n")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	results := runProvenanceChecks(root)
	if len(results) == 0 {
		t.Fatal("results is empty")
	}
	if results[0].Status == StatusSkip {
		t.Fatalf("results = %+v, want not SKIP when provenance.require rules exist", results)
	}
}

// TestRunProvenanceChecksCoverageSummaryFeedsDoctor covers the "the summary
// is always reported, and also feeds the doctor check" requirement: the
// coverage counts flow into the doctor results as an additional PASS line.
func TestRunProvenanceChecksCoverageSummaryFeedsDoctor(t *testing.T) {
	root, run := provenanceTestRepo(t)
	writeProvenanceFile(t, root, "results/out.txt", "data")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	contentID, err := lib.NewWorktreeReader(root).ResolveContentID("results/out.txt")
	if err != nil {
		t.Fatalf("ResolveContentID() error = %v", err)
	}
	rec := fmt.Sprintf(`{
  "@context": %q,
  "@id": %q,
  "exo:assertion": "recorded",
  "exo:assertedBy": "urn:exohub:agent:person:alice",
  "generatedAtTime": "2026-10-14T10:00:00Z",
  "@graph": [{"@id": %q, "@type": "Entity", "wasGeneratedBy": %q}]
}`, lib.ContextIRI, lib.NewRecordID(), contentID, lib.NewActivityID())
	writeProvenanceFile(t, root, lib.RecordDir+"/a.jsonld", rec)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "record")

	results := runProvenanceChecks(root)
	var sawCoverage bool
	for _, r := range results {
		if strings.Contains(r.Summary, "Coverage: 1 covered, 0 uncovered") {
			sawCoverage = true
		}
	}
	if !sawCoverage {
		t.Fatalf("results = %+v, want a coverage summary result", results)
	}
}
