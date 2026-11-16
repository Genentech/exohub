package provenance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	lib "github.com/Genentech/exohub/go/exo/internal/provenance"
	"github.com/Genentech/exohub/go/exo/internal/provenance/importer"
)

func runImportCmd(t *testing.T, args ...string) (stdout string, err error) {
	t.Helper()
	cmd := newImportCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return buf.String(), err
}

// registerFakeFormat registers a test-only import format adapter that
// always returns run, under a name unique to the calling test (tasks
// 5.1-5.6's Nextflow adapter is a separate change, P6; the command shell
// itself is tested against a hand-built run model).
func registerFakeFormat(t *testing.T, run importer.Run) string {
	t.Helper()
	name := fmt.Sprintf("fake-%s", t.Name())
	RegisterFormat(name, func(store, runArg string) (importer.Run, error) {
		return run, nil
	})
	t.Cleanup(func() { delete(formatAdapters, name) })
	return name
}

func TestImport_UnsupportedFormat(t *testing.T) {
	root, _ := addTestRepo(t, false)
	chdir(t, root)
	noLoginToken(t)

	_, err := runImportCmd(t, "no-such-format")
	if err == nil || !strings.Contains(err.Error(), "unsupported import format") {
		t.Fatalf("err = %v, want unsupported import format", err)
	}
}

func TestImport_DryRunWritesNothing(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	withLoginToken(t, fakeJWT(`{"preferred_username":"alice","name":"Alice"}`))

	writeFile(t, root, "data/reads.fq", "reads")
	writeFile(t, root, "results/report.html", "report")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "data")

	format := registerFakeFormat(t, importer.Run{
		ID:   "lid://dryrun",
		Name: "dry_run",
		Tasks: []importer.Task{
			{
				Inputs:  []importer.File{{Path: "data/reads.fq"}},
				Outputs: []importer.File{{Path: "results/report.html"}},
			},
		},
	})

	out, err := runImportCmd(t, format, "--dry-run")
	if err != nil {
		t.Fatalf("import error = %v, output: %s", err, out)
	}
	if !strings.Contains(out, "\"exo:assertion\": \"imported\"") {
		t.Errorf("output does not contain the printed record: %s", out)
	}

	entries, err := os.ReadDir(filepath.Join(root, ".exohub/provenance"))
	if err == nil && len(entries) != 0 {
		t.Errorf("dry-run wrote %d record file(s), want 0", len(entries))
	}
}

func TestImport_WritesAndStages(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	withLoginToken(t, fakeJWT(`{"preferred_username":"alice","name":"Alice"}`))

	writeFile(t, root, "data/reads.fq", "reads")
	writeFile(t, root, "results/report.html", "report")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "data")

	format := registerFakeFormat(t, importer.Run{
		ID:   "lid://write1",
		Name: "happy_curie",
		Tasks: []importer.Task{
			{
				Inputs:  []importer.File{{Path: "data/reads.fq"}},
				Outputs: []importer.File{{Path: "results/report.html"}},
			},
		},
	})

	out, err := runImportCmd(t, format)
	if err != nil {
		t.Fatalf("import error = %v, output: %s", err, out)
	}
	if !strings.Contains(out, "Wrote and staged") {
		t.Errorf("output = %q, want a Wrote and staged line", out)
	}
	if !strings.Contains(out, "1 activity(ies)") {
		t.Errorf("output = %q, want a summary line", out)
	}

	entries, err := os.ReadDir(filepath.Join(root, ".exohub/provenance"))
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d record files, want 1", len(entries))
	}
	recordPath := ".exohub/provenance/" + entries[0].Name()
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
	if rec.Assertion != lib.AssertionImported {
		t.Errorf("Assertion = %q, want imported", rec.Assertion)
	}
	if rec.AssertedBy != lib.PersonAgentID("alice") {
		t.Errorf("AssertedBy = %q, want %q", rec.AssertedBy, lib.PersonAgentID("alice"))
	}
}

func TestImport_JSONOutput(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	withLoginToken(t, fakeJWT(`{"preferred_username":"alice","name":"Alice"}`))

	writeFile(t, root, "data/reads.fq", "reads")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "data")

	format := registerFakeFormat(t, importer.Run{
		ID:   "lid://json1",
		Name: "json_run",
		Tasks: []importer.Task{
			{Inputs: []importer.File{{Path: "data/reads.fq"}}},
		},
	})

	out, err := runImportCmd(t, format, "--json")
	if err != nil {
		t.Fatalf("import error = %v, output: %s", err, out)
	}
	var decoded struct {
		Path    string          `json:"path"`
		Record  json.RawMessage `json:"record"`
		Summary struct {
			Activities int `json:"Activities"`
		} `json:"summary"`
	}
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v, output: %s", err, out)
	}
	if decoded.Path == "" {
		t.Error("path is empty")
	}
}

func TestImport_SummaryReportsVerification(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	withLoginToken(t, fakeJWT(`{"preferred_username":"alice","name":"Alice"}`))

	checksummed := "reads"
	sizeOnly := "fake-parquet-content"
	writeFile(t, root, "data/reads.fq", checksummed)
	writeFile(t, root, "results/de.parquet", sizeOnly)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "data")

	format := registerFakeFormat(t, importer.Run{
		ID:   "lid://verify1",
		Name: "verify_run",
		Tasks: []importer.Task{
			{
				Inputs: []importer.File{{Path: "data/reads.fq"}},
				Outputs: []importer.File{{
					Path:     "results/de.parquet",
					Size:     int64(len(sizeOnly)),
					Checksum: importer.Checksum{Algorithm: "nf-cache-v1", Value: "irrelevant"},
				}},
			},
		},
	})

	out, err := runImportCmd(t, format)
	if err != nil {
		t.Fatalf("import error = %v, output: %s", err, out)
	}
	if !strings.Contains(out, "verified by checksum: 0, verified by size only: 1, unmatched: 0, external: 0") {
		t.Errorf("output = %q, want a verification breakdown line", out)
	}
	if !strings.Contains(out, "results/de.parquet") {
		t.Errorf("output = %q, want the size-only file named", out)
	}

	format2 := registerFakeFormat(t, importer.Run{
		ID:   "lid://verify2",
		Name: "verify_run2",
		Tasks: []importer.Task{
			{
				Outputs: []importer.File{{
					Path:     "results/de.parquet",
					Size:     int64(len(sizeOnly)),
					Checksum: importer.Checksum{Algorithm: "nf-cache-v1", Value: "irrelevant"},
				}},
			},
		},
	})
	out, err = runImportCmd(t, format2, "--json")
	if err != nil {
		t.Fatalf("import error = %v, output: %s", err, out)
	}
	var decoded struct {
		Summary struct {
			Matched []struct {
				Path     string `json:"path"`
				Verified string `json:"verified"`
			} `json:"Matched"`
		} `json:"summary"`
	}
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v, output: %s", err, out)
	}
	if len(decoded.Summary.Matched) != 1 {
		t.Fatalf("got %d matched files, want 1: %s", len(decoded.Summary.Matched), out)
	}
	if decoded.Summary.Matched[0].Verified != "size" {
		t.Errorf("verified = %q, want size", decoded.Summary.Matched[0].Verified)
	}
}

func TestImport_ReimportWritesNothing(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	withLoginToken(t, fakeJWT(`{"preferred_username":"alice","name":"Alice"}`))

	writeFile(t, root, "data/reads.fq", "reads")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "data")

	format := registerFakeFormat(t, importer.Run{
		ID:   "lid://0d1f0000",
		Name: "happy_curie",
		Tasks: []importer.Task{
			{Inputs: []importer.File{{Path: "data/reads.fq"}}},
		},
	})

	if _, err := runImportCmd(t, format); err != nil {
		t.Fatalf("first import error = %v", err)
	}
	entriesAfterFirst, _ := os.ReadDir(filepath.Join(root, ".exohub/provenance"))

	out, err := runImportCmd(t, format)
	if err != nil {
		t.Fatalf("second import error = %v, output: %s", err, out)
	}
	if !strings.Contains(out, "already imported") {
		t.Errorf("output = %q, want an already-imported message", out)
	}

	entriesAfterSecond, err := os.ReadDir(filepath.Join(root, ".exohub/provenance"))
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	if len(entriesAfterSecond) != len(entriesAfterFirst) {
		t.Errorf("got %d record files after the second import, want %d (unchanged)", len(entriesAfterSecond), len(entriesAfterFirst))
	}
}

func TestImport_KeepSourceStagesGitBlobs(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	withLoginToken(t, fakeJWT(`{"preferred_username":"alice","name":"Alice"}`))

	writeFile(t, root, "data/reads.fq", "reads")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "data")

	format := registerFakeFormat(t, importer.Run{
		ID:   "lid://keepsource1",
		Name: "kept_run",
		Tasks: []importer.Task{
			{Inputs: []importer.File{{Path: "data/reads.fq"}}},
		},
		SourceDocuments: []importer.SourceDocument{
			{Name: "lineage.json", Content: []byte(`{"id":"kept_run"}`)},
		},
	})

	out, err := runImportCmd(t, format, "--keep-source")
	if err != nil {
		t.Fatalf("import error = %v, output: %s", err, out)
	}

	sourceFiles := run("git", "ls-files", "--", ".exohub/provenance/sources/")
	if strings.TrimSpace(sourceFiles) == "" {
		t.Fatal("no source document was staged under .exohub/provenance/sources/")
	}
	if !strings.HasSuffix(strings.TrimSpace(sourceFiles), "lineage.json") {
		t.Errorf("staged source file = %q, want it to end in lineage.json", sourceFiles)
	}

	entries, err := os.ReadDir(filepath.Join(root, ".exohub/provenance"))
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	var recordPath string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".jsonld") {
			recordPath = ".exohub/provenance/" + e.Name()
		}
	}
	data, err := os.ReadFile(filepath.Join(root, recordPath))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	rec, err := lib.Parse(data)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if !strings.Contains(rec.Evidence, "lineage.json") {
		t.Errorf("Evidence = %q, want it to mention lineage.json", rec.Evidence)
	}
}
