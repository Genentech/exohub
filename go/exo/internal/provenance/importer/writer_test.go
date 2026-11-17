package importer

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	lib "github.com/Genentech/exohub/go/exo/internal/provenance"
)

func writeAndStageRecord(t *testing.T, root string, rec *lib.Record) string {
	t.Helper()
	data, err := lib.Serialize(rec)
	if err != nil {
		t.Fatalf("Serialize() error = %v", err)
	}
	path, err := lib.RecordPath(time.Now().UTC(), "import", rec.ID)
	if err != nil {
		t.Fatalf("RecordPath() error = %v", err)
	}
	full := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(full, data, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

func TestWriter_WritesAndValidates(t *testing.T) {
	root, run := importerTestRepo(t)
	writeTestFile(t, root, "data/reads.fq", "@read1\nACGT\n")
	writeTestFile(t, root, "results/report.html", "<html></html>")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "data")

	reader := lib.NewWorktreeReader(root)
	writer := NewWriter(NewMatcher(reader))

	runModel := Run{
		ID:      "lid://0d1f0000",
		Name:    "happy_curie",
		Started: time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC),
		Ended:   time.Date(2026, 11, 9, 10, 5, 0, 0, time.UTC),
		Engine:  Engine{Name: "nextflow", Version: "24.10.0"},
		Pipeline: Pipeline{
			Name:       "rnaseq",
			Checksum:   "sha256:abc123",
			Repository: "https://github.com/org/pipeline",
			Commit:     "4f2a9c1",
		},
		Params: []Value{{Name: "alpha", Value: 0.05}},
		Tasks: []Task{
			{
				Name:    "run",
				Inputs:  []File{{Path: "data/reads.fq", Role: "reads"}},
				Outputs: []File{{Path: "results/report.html", Role: "report"}},
				Environment: &Environment{
					Kind: "container",
					ID:   "docker://ghcr.io/org/tool@sha256:ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34",
				},
			},
		},
	}

	rec, summary, err := writer.Write(runModel, WriteOptions{
		AssertedBy:  lib.PersonAgentID("alice"),
		PersonID:    lib.PersonAgentID("alice"),
		PersonLabel: "Alice",
		GeneratedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	if rec.Assertion != lib.AssertionImported {
		t.Errorf("Assertion = %q, want imported", rec.Assertion)
	}
	if summary.Activities != 1 {
		t.Errorf("Activities = %d, want 1", summary.Activities)
	}
	if summary.MatchedInputs != 1 || summary.MatchedOutputs != 1 {
		t.Errorf("MatchedInputs/Outputs = %d/%d, want 1/1", summary.MatchedInputs, summary.MatchedOutputs)
	}

	nodes := byID(rec.Graph)

	var activity lib.Node
	for _, n := range nodes {
		if hasType(n, "Activity") {
			activity = n
		}
	}
	if activity == nil {
		t.Fatal("no Activity node in graph")
	}
	if activity["hadPrimarySource"] != runModel.ID {
		t.Errorf("hadPrimarySource = %v, want %v", activity["hadPrimarySource"], runModel.ID)
	}
	if activity["startedAtTime"] != "2026-11-09T10:00:00Z" {
		t.Errorf("startedAtTime = %v", activity["startedAtTime"])
	}

	engineID := lib.SoftwareAgentID("nextflow")
	engine, ok := nodes[engineID]
	if !ok {
		t.Fatalf("engine agent %s missing", engineID)
	}
	if engine["exo:version"] != "24.10.0" {
		t.Errorf("engine exo:version = %v, want 24.10.0", engine["exo:version"])
	}

	// Parameter value entity.
	valueID, err := lib.ValueEntityID("alpha", 0.05)
	if err != nil {
		t.Fatalf("ValueEntityID() error = %v", err)
	}
	if _, ok := nodes[valueID]; !ok {
		t.Errorf("parameter value entity %s missing", valueID)
	}

	// Plan entity carries the pipeline pointer.
	var plan lib.Node
	for _, n := range nodes {
		if n["exo:repository"] == runModel.Pipeline.Repository {
			plan = n
		}
	}
	if plan == nil {
		t.Fatal("no plan entity with exo:repository")
	}
	if plan["exo:commit"] != runModel.Pipeline.Commit {
		t.Errorf("plan exo:commit = %v, want %v", plan["exo:commit"], runModel.Pipeline.Commit)
	}
	if plan["exo:checksum"] != runModel.Pipeline.Checksum {
		t.Errorf("plan exo:checksum = %v, want %v", plan["exo:checksum"], runModel.Pipeline.Checksum)
	}

	writeAndStageRecord(t, root, rec)
	report, err := lib.Validate(reader)
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if report.HasErrors() {
		t.Errorf("Validate() found errors: %+v", report.Findings)
	}
}

func TestWriter_TasksProducesOneActivityPerTask(t *testing.T) {
	root, run := importerTestRepo(t)
	writeTestFile(t, root, "data/reads.fq", "reads")
	writeTestFile(t, root, "results/report.html", "report")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "data")

	reader := lib.NewWorktreeReader(root)
	writer := NewWriter(NewMatcher(reader))

	runModel := threeTaskRun()
	runModel.Tasks[0].Inputs = []File{{Path: "data/reads.fq"}}
	runModel.Tasks[2].Outputs = []File{{Path: "results/report.html"}}

	rec, summary, err := writer.Write(runModel, WriteOptions{
		AssertedBy:  lib.PersonAgentID("alice"),
		GeneratedAt: time.Now().UTC(),
		Tasks:       true,
	})
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if summary.Activities != 3 {
		t.Errorf("Activities = %d, want 3", summary.Activities)
	}

	nodes := byID(rec.Graph)
	var activities []lib.Node
	for _, n := range nodes {
		if hasType(n, "Activity") {
			activities = append(activities, n)
		}
	}
	if len(activities) != 3 {
		t.Fatalf("got %d Activity nodes, want 3", len(activities))
	}

	writeAndStageRecord(t, root, rec)
	report, err := lib.Validate(reader)
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if report.HasErrors() {
		t.Errorf("Validate() found errors: %+v", report.Findings)
	}
}

func TestWriter_SummaryRecordsVerification(t *testing.T) {
	root, run := importerTestRepo(t)
	checksummed := "@read1\nACGT\n"
	sizeOnly := "fake-parquet-content"
	writeTestFile(t, root, "data/reads.fq", checksummed)
	writeTestFile(t, root, "results/de.parquet", sizeOnly)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "data")

	reader := lib.NewWorktreeReader(root)
	writer := NewWriter(NewMatcher(reader))

	runModel := Run{
		ID:   "lid://verify1",
		Name: "verify_run",
		Tasks: []Task{
			{
				Inputs:  []File{{Path: "data/reads.fq", Size: int64(len(checksummed)), Checksum: Checksum{Algorithm: "sha256", Value: sha256Hex(checksummed)}}},
				Outputs: []File{{Path: "results/de.parquet", Size: int64(len(sizeOnly)), Checksum: Checksum{Algorithm: "nf-cache-v1", Value: "irrelevant"}}},
			},
		},
	}

	_, summary, err := writer.Write(runModel, WriteOptions{
		AssertedBy:  lib.PersonAgentID("alice"),
		GeneratedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	if len(summary.Matched) != 2 {
		t.Fatalf("got %d matched files, want 2", len(summary.Matched))
	}
	byPath := map[string]string{}
	for _, m := range summary.Matched {
		byPath[m.Path] = m.Verified
	}
	if byPath["data/reads.fq"] != "checksum" {
		t.Errorf("data/reads.fq verified = %q, want checksum", byPath["data/reads.fq"])
	}
	if byPath["results/de.parquet"] != "size" {
		t.Errorf("results/de.parquet verified = %q, want size", byPath["results/de.parquet"])
	}
}

func TestWriter_Supersedes(t *testing.T) {
	root, _ := importerTestRepo(t)
	reader := lib.NewWorktreeReader(root)
	writer := NewWriter(NewMatcher(reader))

	rec, _, err := writer.Write(Run{ID: "lid://x", Name: "r"}, WriteOptions{
		AssertedBy:  lib.PersonAgentID("alice"),
		GeneratedAt: time.Now().UTC(),
		Supersedes:  []string{lib.NewRecordID()},
	})
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if len(rec.Supersedes) != 1 {
		t.Errorf("Supersedes = %v, want 1 entry", rec.Supersedes)
	}
}
