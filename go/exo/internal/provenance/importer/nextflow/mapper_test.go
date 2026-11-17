package nextflow

import (
	"testing"
	"time"
)

func TestRead_MapsParamsContainerAndPipeline(t *testing.T) {
	dir := fixtureStore(t, supportedVersion,
		[]indexRun{{ID: "lid://run1", Name: "happy_curie", StartTime: time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)}},
		map[string]any{
			"lid://run1": map[string]any{
				"type":           "WorkflowRun",
				"runName":        "happy_curie",
				"startTime":      "2026-11-09T10:00:00Z",
				"endTime":        "2026-11-09T10:05:00Z",
				"engine":         map[string]any{"name": "nextflow", "version": "24.10.0"},
				"repository":     "https://example.org/exo-pipelines/rnaseq.git",
				"commitId":       "4f2a9c1",
				"scriptChecksum": "sha256:9e9f0000",
				"params":         map[string]any{"alpha": 0.05},
				"tasks":          []string{"lid://task1"},
			},
			"lid://task1": map[string]any{
				"type":      "TaskRun",
				"name":      "align",
				"startTime": "2026-11-09T10:00:10Z",
				"endTime":   "2026-11-09T10:02:00Z",
				"container": "ghcr.io/example/tool@sha256:ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34",
				"inputs":    []string{"lid://in1"},
				"outputs":   []string{"lid://out1", "lid://value1"},
			},
			"lid://in1": map[string]any{
				"type":     "FileOutput",
				"path":     "data/reads.fq",
				"role":     "reads",
				"size":     5,
				"checksum": map[string]any{"algorithm": "sha256", "value": "deadbeef"},
			},
			"lid://out1": map[string]any{
				"type":     "FileOutput",
				"path":     "results/aligned.bam",
				"role":     "alignment",
				"size":     10,
				"checksum": map[string]any{"algorithm": "nf-cache-v1", "value": "beadfeed"},
			},
			"lid://value1": map[string]any{
				"type":  "TaskOutput",
				"name":  "read_count",
				"value": float64(1000),
			},
		})

	run, err := Read(dir, "")
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}

	if run.Engine.Name != "nextflow" || run.Engine.Version != "24.10.0" {
		t.Errorf("Engine = %+v, want nextflow/24.10.0", run.Engine)
	}
	if run.Pipeline.Repository != "https://example.org/exo-pipelines/rnaseq.git" || run.Pipeline.Commit != "4f2a9c1" {
		t.Errorf("Pipeline = %+v, want the fixture's repository/commit", run.Pipeline)
	}
	if run.Pipeline.Checksum != "sha256:9e9f0000" {
		t.Errorf("Pipeline.Checksum = %q, want sha256:9e9f0000", run.Pipeline.Checksum)
	}
	if len(run.Params) != 1 || run.Params[0].Name != "alpha" || run.Params[0].Value != 0.05 {
		t.Errorf("Params = %+v, want [{alpha 0.05}]", run.Params)
	}

	if len(run.Tasks) != 1 {
		t.Fatalf("got %d tasks, want 1", len(run.Tasks))
	}
	task := run.Tasks[0]
	if task.Name != "align" {
		t.Errorf("Name = %q, want align", task.Name)
	}
	if task.Environment == nil || task.Environment.Kind != "container" {
		t.Fatalf("Environment = %+v, want a container environment", task.Environment)
	}
	if task.Environment.ID != "docker://ghcr.io/example/tool@sha256:ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34" {
		t.Errorf("Environment.ID = %q", task.Environment.ID)
	}

	if len(task.Inputs) != 1 || task.Inputs[0].Path != "data/reads.fq" || task.Inputs[0].Role != "reads" {
		t.Errorf("Inputs = %+v, want one reads input", task.Inputs)
	}
	if task.Inputs[0].Checksum.Algorithm != "sha256" || task.Inputs[0].Checksum.Value != "deadbeef" {
		t.Errorf("Inputs[0].Checksum = %+v", task.Inputs[0].Checksum)
	}

	if len(task.Outputs) != 1 || task.Outputs[0].Path != "results/aligned.bam" {
		t.Errorf("Outputs = %+v, want one aligned.bam output", task.Outputs)
	}
	if task.Outputs[0].Checksum.Algorithm != "nf-cache-v1" {
		t.Errorf("Outputs[0].Checksum.Algorithm = %q, want nf-cache-v1 (an algorithm exo cannot verify)", task.Outputs[0].Checksum.Algorithm)
	}
	if task.Outputs[0].ProducedByTask != "align" {
		t.Errorf("Outputs[0].ProducedByTask = %q, want align", task.Outputs[0].ProducedByTask)
	}

	if len(task.Results) != 1 || task.Results[0].Name != "read_count" || task.Results[0].Value != float64(1000) {
		t.Errorf("Results = %+v, want [{read_count 1000}]", task.Results)
	}
}

func TestRead_CondaEnvironment(t *testing.T) {
	dir := fixtureStore(t, supportedVersion,
		[]indexRun{{ID: "lid://run1", Name: "r"}},
		map[string]any{
			"lid://run1": map[string]any{
				"type":    "WorkflowRun",
				"runName": "r",
				"tasks":   []string{"lid://task1"},
			},
			"lid://task1": map[string]any{
				"type":  "TaskRun",
				"name":  "t",
				"conda": "bioconda::samtools=1.20",
			},
		})

	run, err := Read(dir, "")
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	env := run.Tasks[0].Environment
	if env == nil || env.Kind != "conda" || env.ID != "conda://bioconda::samtools=1.20" {
		t.Errorf("Environment = %+v, want a conda environment", env)
	}
}

func TestRead_ExternalInput(t *testing.T) {
	dir := fixtureStore(t, supportedVersion,
		[]indexRun{{ID: "lid://run1", Name: "r"}},
		map[string]any{
			"lid://run1": map[string]any{
				"type":    "WorkflowRun",
				"runName": "r",
				"tasks":   []string{"lid://task1"},
			},
			"lid://task1": map[string]any{
				"type":   "TaskRun",
				"name":   "t",
				"inputs": []string{"lid://ext1"},
			},
			"lid://ext1": map[string]any{
				"type": "FileOutput",
				"url":  "s3://example-bucket/refs/genome.fa",
				"role": "reference",
			},
		})

	run, err := Read(dir, "")
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	in := run.Tasks[0].Inputs[0]
	if in.URL != "s3://example-bucket/refs/genome.fa" || in.Path != "" {
		t.Errorf("Inputs[0] = %+v, want the external URL and no path", in)
	}
}

func TestRead_UnexpectedOutputRecordType(t *testing.T) {
	dir := fixtureStore(t, supportedVersion,
		[]indexRun{{ID: "lid://run1", Name: "r"}},
		map[string]any{
			"lid://run1": map[string]any{
				"type":    "WorkflowRun",
				"runName": "r",
				"tasks":   []string{"lid://task1"},
			},
			"lid://task1": map[string]any{
				"type":    "TaskRun",
				"name":    "t",
				"outputs": []string{"lid://run1"},
			},
		})

	_, err := Read(dir, "")
	if err == nil {
		t.Fatal("Read() error = nil, want an unexpected-record-type error")
	}
}
