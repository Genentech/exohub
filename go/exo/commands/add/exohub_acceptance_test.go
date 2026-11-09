package add

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	lib "github.com/Genentech/exohub/go/exo/internal/provenance"
)

// TestAcceptance_ImportFastqFolderRecordsProvenance is the automated part
// of the task 5.4 acceptance scenario: importing a FASTQ folder from one
// project into another with `exo add exohub:...`, in place of the manual
// `exo provenance add` pass the two existing provenance datasets required
// before this change. It covers everything that can run as a hermetic
// test: reference resolution, materialisation, the automatic import
// record, and copying the source's own generation records. Live sandbox
// publish, `exo atlas u` on the source, and the non-owner Z1 negative-write
// test (task 3.1.1) need a real catalog, a real bucket and a second IAM
// identity; they are operator-only steps, documented in the MR description
// rather than run here.
func TestAcceptance_ImportFastqFolderRecordsProvenance(t *testing.T) {
	if _, err := exec.LookPath("git-annex"); err != nil {
		t.Skip("git-annex not in PATH")
	}
	withLoginToken(t, fakeJWT(`{"preferred_username":"alice","name":"Alice Doe"}`))

	source := newTestGitRepo(t)
	source.run("annex", "init", "-q", "fastq-mini")
	fastqFiles := []string{"fastq/sample1_R1.fastq.gz", "fastq/sample1_R2.fastq.gz"}
	for i, rel := range fastqFiles {
		full := filepath.Join(source.Dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(fmt.Sprintf("@read%d\nACGT\n+\nIIII\n", i)), 0o644); err != nil {
			t.Fatal(err)
		}
		source.run("annex", "add", rel)
	}
	source.run("commit", "-q", "-m", "add fastq-mini reads")

	// The source project already has provenance for how the reads were
	// generated: a folder collection record with wasGeneratedBy, which the
	// import must copy alongside the materialised files (design D4 Q3).
	keys := make([]string, len(fastqFiles))
	for i, rel := range fastqFiles {
		keys[i] = strings.TrimSpace(source.run("annex", "lookupkey", rel))
	}
	treeOID := strings.TrimSpace(source.run("rev-parse", "HEAD:fastq"))
	folderID := lib.GitTreeEntityID(treeOID)
	act := lib.NewActivityID()
	person := "urn:exohub:agent:person:carol"
	genRecord := fmt.Sprintf(`{
  "@context": %q,
  "@id": %q,
  "exo:assertion": "asserted",
  "exo:assertedBy": %q,
  "generatedAtTime": "2026-01-01T00:00:00Z",
  "@graph": [
    {"@id": %q, "@type": "Collection", "wasGeneratedBy": %q},
    {"@id": %q, "@type": "Activity"},
    {"@id": %q, "@type": "Person"}
  ]
}`, lib.ContextIRI, lib.NewRecordID(), person, folderID, act, act, person)
	if err := os.MkdirAll(filepath.Join(source.Dir, lib.RecordDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source.Dir, lib.RecordDir, "sequencing.jsonld"), []byte(genRecord), 0o644); err != nil {
		t.Fatal(err)
	}
	source.run("add", "-A")
	source.run("commit", "-q", "-m", "record fastq generation")
	source.markPublished("main")

	importer := newTestGitRepo(t)
	importer.run("annex", "init", "-q", "analysis-mini")
	withDir(t, importer.Dir)
	t.Setenv("EXOHUB_CLONE_FASTQ_MINI", source.Dir)

	cmd := NewCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"exohub:fastq-mini:fastq/@main", "--to", "inputs/fastq-mini"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("exo add exohub:fastq-mini:fastq/@main --to inputs/fastq-mini: %v\n%s", err, out.String())
	}

	for _, rel := range fastqFiles {
		base := filepath.Base(rel)
		full := filepath.Join(importer.Dir, "inputs", "fastq-mini", base)
		if _, err := os.Stat(full); err != nil {
			t.Fatalf("inputs/fastq-mini/%s missing: %v", base, err)
		}
	}
	for i, rel := range fastqFiles {
		base := filepath.Base(rel)
		gotKey := strings.TrimSpace(importer.run("annex", "lookupkey", filepath.Join("inputs", "fastq-mini", base)))
		if gotKey != keys[i] {
			t.Errorf("inputs/fastq-mini/%s annex key = %q, want %q (same content id as the source)", base, gotKey, keys[i])
		}
	}

	records := recordFiles(t, importer.Dir)
	var importRecord, copiedRecord *lib.Record
	for path, rec := range records {
		if path == lib.RecordDir+"/sequencing.jsonld" {
			copiedRecord = rec
			continue
		}
		importRecord = rec
	}
	if importRecord == nil {
		t.Fatalf("no automatic import record written, records = %v", records)
	}
	if copiedRecord == nil {
		t.Fatalf("source's fastq generation record was not copied, records = %v", records)
	}
	if _, ok := nodeByID(copiedRecord, folderID); !ok {
		t.Errorf("copied record missing the fastq folder's generation node %s: %+v", folderID, copiedRecord.Graph)
	}

	sourceNode, ok := nodeByID(importRecord, folderID)
	if !ok {
		t.Fatalf("import record missing source node %s: %+v", folderID, importRecord.Graph)
	}
	if sourceNode["label"] != "fastq-mini@main" {
		t.Errorf("source label = %v, want fastq-mini@main", sourceNode["label"])
	}

	targetID, err := lib.NewWorktreeReader(importer.Dir).ResolveContentID("inputs/fastq-mini/")
	if err != nil {
		t.Fatalf("ResolveContentID(inputs/fastq-mini/) error = %v", err)
	}
	targetNode, ok := nodeByID(importRecord, targetID)
	if !ok {
		t.Fatalf("import record missing local target node %s: %+v", targetID, importRecord.Graph)
	}
	if targetNode["hadPrimarySource"] != folderID {
		t.Errorf("target hadPrimarySource = %v, want %s (the source project and version are recoverable from this and the source node's label)", targetNode["hadPrimarySource"], folderID)
	}
}
