package provenance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	lib "github.com/Genentech/exohub/go/exo/internal/provenance"
)

// writeLineageStore writes a minimal "lineage/v1beta1" Nextflow lineage
// store under root/.lineage (design D8), with one WorkflowRun ("rnaseq_demo")
// running one task ("align") that consumes readsContent (verified by
// checksum) and produces bamContent (verified by size only, since its
// checksum algorithm is the engine's own "nf-cache-v1", not sha256/md5 —
// review: "a fixture whose checksum algorithm isn't sha256/md5"), plus a
// non-file result and a container environment.
func writeLineageStore(t *testing.T, root string, readsContent, bamContent string) {
	t.Helper()
	writeLineageStoreAt(t, filepath.Join(root, ".lineage"), "run1", readsContent, bamContent)
}

// writeLineageStoreAt is writeLineageStore, writing the store at dir under
// run id lid://<runID> instead of assuming the default .lineage location
// and a fixed run id (used to exercise --store and multiple imports in the
// same repository).
func writeLineageStoreAt(t *testing.T, dir, runID, readsContent, bamContent string) {
	t.Helper()
	recordsDir := filepath.Join(dir, "records")
	if err := os.MkdirAll(recordsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	writeStoreJSON(t, filepath.Join(dir, "lineage.json"), map[string]any{
		"version": "lineage/v1beta1",
		"runs": []map[string]any{
			{"id": "lid://" + runID, "name": "rnaseq_demo", "startTime": "2026-11-09T10:00:00Z"},
		},
	})

	writeStoreJSON(t, filepath.Join(recordsDir, runID+".json"), map[string]any{
		"type":           "WorkflowRun",
		"runName":        "rnaseq_demo",
		"startTime":      "2026-11-09T10:00:00Z",
		"endTime":        "2026-11-09T10:05:00Z",
		"engine":         map[string]any{"name": "nextflow", "version": "24.10.0"},
		"repository":     "https://example.org/exo-pipelines/rnaseq.git",
		"commitId":       "4f2a9c1",
		"scriptChecksum": "sha256:9e9f0000",
		"params":         map[string]any{"alpha": 0.05},
		"tasks":          []string{"lid://task1"},
	})
	writeStoreJSON(t, filepath.Join(recordsDir, "task1.json"), map[string]any{
		"type":      "TaskRun",
		"name":      "align",
		"startTime": "2026-11-09T10:00:10Z",
		"endTime":   "2026-11-09T10:02:00Z",
		"container": "ghcr.io/example/tool@sha256:ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34",
		"inputs":    []string{"lid://in1"},
		"outputs":   []string{"lid://out1", "lid://val1"},
	})
	writeStoreJSON(t, filepath.Join(recordsDir, "in1.json"), map[string]any{
		"type":     "FileOutput",
		"path":     "data/reads.fq",
		"role":     "reads",
		"size":     len(readsContent),
		"checksum": map[string]any{"algorithm": "sha256", "value": sha256Hex(readsContent)},
	})
	writeStoreJSON(t, filepath.Join(recordsDir, "out1.json"), map[string]any{
		"type":     "FileOutput",
		"path":     "results/aligned.bam",
		"role":     "alignment",
		"size":     len(bamContent),
		"checksum": map[string]any{"algorithm": "nf-cache-v1", "value": "nf-own-hash-irrelevant-to-exo"},
	})
	writeStoreJSON(t, filepath.Join(recordsDir, "val1.json"), map[string]any{
		"type":  "TaskOutput",
		"name":  "read_count",
		"value": 1000,
	})
}

func writeStoreJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// TestNextflow_EndToEnd covers tasks.md 6.3: a fixture Nextflow lineage
// store plus a repo with matching files, imported with `exo provenance
// import nextflow`, validated clean, and compiled with artifact documents
// that carry roles, parameters and environment (and, per review, the
// size-only verification case for a non-sha256/md5 checksum algorithm).
func TestNextflow_EndToEnd(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	withLoginToken(t, fakeJWT(`{"preferred_username":"alice","name":"Alice"}`))

	readsContent := "@read1\nACGT\n"
	bamContent := "fake-bam-content"
	writeFile(t, root, "data/reads.fq", readsContent)
	writeFile(t, root, "results/aligned.bam", bamContent)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "data")

	writeLineageStore(t, root, readsContent, bamContent)

	importOut, err := runImportCmd(t, "nextflow")
	if err != nil {
		t.Fatalf("import error = %v, output: %s", err, importOut)
	}
	if !strings.Contains(importOut, "Wrote and staged") {
		t.Errorf("import output = %q, want a Wrote and staged line", importOut)
	}
	if !strings.Contains(importOut, "verified by checksum: 1, verified by size only: 1") {
		t.Errorf("import output = %q, want one checksum match and one size-only match", importOut)
	}
	if !strings.Contains(importOut, "results/aligned.bam") {
		t.Errorf("import output = %q, want the size-only file named", importOut)
	}

	validateOut, err := runValidateCmd(t)
	if err != nil {
		t.Fatalf("validate error = %v, output: %s", err, validateOut)
	}
	if strings.Contains(strings.ToLower(validateOut), "error") {
		t.Errorf("validate output = %q, want no errors", validateOut)
	}

	run("git", "add", ".exohub")
	run("git", "commit", "-q", "-m", "import")

	result, report, err := lib.CompileBundle(lib.NewRefReader(root, "HEAD"), []string{"data/reads.fq", "results/aligned.bam"})
	if err != nil {
		t.Fatalf("CompileBundle() error = %v", err)
	}
	if report.HasErrors() {
		t.Fatalf("CompileBundle() findings: %+v", report.Findings)
	}

	bam, ok := result.Lineage["results/aligned.bam"]
	if !ok {
		t.Fatalf("no lineage for results/aligned.bam, got keys %v", lineagePaths(result.Lineage))
	}
	if !bam.Covered {
		t.Error("results/aligned.bam Covered = false, want true")
	}
	if bam.GeneratedBy == nil {
		t.Fatal("results/aligned.bam GeneratedBy is nil")
	}
	if len(bam.GeneratedBy.Parameters) != 1 || bam.GeneratedBy.Parameters[0].Name != "alpha" {
		t.Errorf("GeneratedBy.Parameters = %+v, want [{alpha 0.05}]", bam.GeneratedBy.Parameters)
	}
	if len(bam.GeneratedBy.Environment) != 1 || bam.GeneratedBy.Environment[0].Kind != "container" {
		t.Errorf("GeneratedBy.Environment = %+v, want one container entry", bam.GeneratedBy.Environment)
	}
	if len(bam.Inputs) != 1 || bam.Inputs[0].Path != "data/reads.fq" || bam.Inputs[0].Role != "reads" {
		t.Errorf("Inputs = %+v, want [{data/reads.fq reads}]", bam.Inputs)
	}
}

// TestNextflow_DocumentedFlags covers tasks.md 6.4's "verify the
// documented commands run against the fixture": the flags and
// invocations docs/provenance.md's Nextflow section describes (run
// selection by name, --store, --tasks, --keep-source) all work against a
// real lineage store fixture.
func TestNextflow_DocumentedFlags(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	withLoginToken(t, fakeJWT(`{"preferred_username":"alice","name":"Alice"}`))

	readsContent := "@read1\nACGT\n"
	bamContent := "fake-bam-content"
	writeFile(t, root, "data/reads.fq", readsContent)
	writeFile(t, root, "results/aligned.bam", bamContent)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "data")

	storeDir := filepath.Join(root, "custom-lineage-store")
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	writeLineageStoreAt(t, storeDir, "run1", readsContent, bamContent)

	// `exo provenance import nextflow happy_curie --store <dir>`
	out, err := runImportCmd(t, "nextflow", "rnaseq_demo", "--store", storeDir, "--keep-source")
	if err != nil {
		t.Fatalf("import error = %v, output: %s", err, out)
	}
	if !strings.Contains(out, "Wrote and staged") {
		t.Errorf("output = %q, want a Wrote and staged line", out)
	}

	sourceFiles := run("git", "ls-files", "--", ".exohub/provenance/sources/")
	if strings.TrimSpace(sourceFiles) == "" {
		t.Error("--keep-source staged no source documents")
	}

	// `exo provenance import nextflow --tasks` against a second run.
	writeFile(t, root, "results/counts.txt", "42\n")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "more data")

	storeDir2 := filepath.Join(root, ".lineage")
	writeLineageStoreAt(t, storeDir2, "run2", readsContent, bamContent)

	out, err = runImportCmd(t, "nextflow", "--tasks")
	if err != nil {
		t.Fatalf("import --tasks error = %v, output: %s", err, out)
	}
	if !strings.Contains(out, "1 activity(ies)") {
		t.Errorf("output = %q, want one activity (the fixture has a single task)", out)
	}
}

func lineagePaths(m map[string]*lib.ArtifactLineage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
