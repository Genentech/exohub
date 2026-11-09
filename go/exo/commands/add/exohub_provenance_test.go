package add

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/Genentech/exohub/go/exo/internal/exohub"
	lib "github.com/Genentech/exohub/go/exo/internal/provenance"
)

// fakeJWT builds an unsigned JWT with the given JSON payload, matching
// go/exo/commands/provenance/add_test.go's helper of the same name.
func fakeJWT(payload string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	body := base64.RawURLEncoding.EncodeToString([]byte(payload))
	return header + "." + body + ".sig"
}

// withLoginToken isolates a test from any real exo login token or config
// directory on the host, and sets EXO_TOKEN to an inline token so
// login.LoadToken picks it up without touching the filesystem.
func withLoginToken(t *testing.T, jwt string) {
	t.Helper()
	t.Setenv("EXO_CONFIG_DIR", t.TempDir())
	t.Setenv("EXO_TOKEN_FILE", "")
	t.Setenv("EXO_TOKEN", `{"access_token":"`+jwt+`"}`)
}

// withNoLoginToken isolates a test from any real exo login token or config
// directory on the host and leaves it with none: GetTokenFile resolves to
// a path under a fresh empty EXO_CONFIG_DIR that was never written, so
// LoadToken fails to open it, exactly like a host that never ran
// "exo login".
func withNoLoginToken(t *testing.T) {
	t.Helper()
	t.Setenv("EXO_CONFIG_DIR", t.TempDir())
	t.Setenv("EXO_TOKEN_FILE", "")
	t.Setenv("EXO_TOKEN", "")
}

// recordFiles returns the parsed records under root's .exohub/provenance/,
// keyed by their repo-relative path.
func recordFiles(t *testing.T, root string) map[string]*lib.Record {
	t.Helper()
	out := map[string]*lib.Record{}
	dir := filepath.Join(root, lib.RecordDir)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return out
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		rec, err := lib.Parse(data)
		if err != nil {
			t.Fatalf("parse %s: %v", e.Name(), err)
		}
		out[lib.RecordDir+"/"+e.Name()] = rec
	}
	return out
}

func nodeByID(rec *lib.Record, id string) (lib.Node, bool) {
	for _, n := range rec.Graph {
		if n.ID() == id {
			return n, true
		}
	}
	return lib.Node{}, false
}

func TestRecordExohubImportAssetHasNoDerivedFrom(t *testing.T) {
	withLoginToken(t, fakeJWT(`{"preferred_username":"bob","name":"Bob Smith"}`))

	source := newTestGitRepo(t)
	if err := os.WriteFile(filepath.Join(source.Dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source.run("add", "-A")
	source.run("commit", "-q", "-m", "initial")
	source.markPublished("main")

	ref, err := exohub.ParseReference("exohub:myproject:README.md@main")
	if err != nil {
		t.Fatal(err)
	}
	res, err := exohub.ResolveLocalClone(source.Dir, ref)
	if err != nil {
		t.Fatalf("ResolveLocalClone() error = %v", err)
	}
	defer res.Close()

	importer := newTestGitRepo(t)
	withDir(t, importer.Dir)
	target := "README.md"
	if err := materializeFile(res, res.Files[0], target); err != nil {
		t.Fatalf("materializeFile() error = %v", err)
	}

	ctx, err := prepareExohubImportContext()
	if err != nil {
		t.Fatalf("prepareExohubImportContext() error = %v", err)
	}
	if err := recordExohubImport(ctx, ref, res, "", io.Discard); err != nil {
		t.Fatalf("recordExohubImport() error = %v", err)
	}

	records := recordFiles(t, importer.Dir)
	if len(records) != 1 {
		t.Fatalf("records = %v, want exactly one", records)
	}
	var rec *lib.Record
	for _, r := range records {
		rec = r
	}

	gitOID := func() string {
		out := source.run("rev-parse", "HEAD:README.md")
		return out[:len(out)-1]
	}()
	sourceID := lib.GitBlobEntityID(gitOID)

	srcNode, ok := nodeByID(rec, sourceID)
	if !ok {
		t.Fatalf("no source node %s in %+v", sourceID, rec.Graph)
	}
	if srcNode["label"] != "myproject@main" {
		t.Errorf("source label = %v, want myproject@main", srcNode["label"])
	}
	if srcNode["exo:path"] != "README.md" {
		t.Errorf("source exo:path = %v, want README.md", srcNode["exo:path"])
	}
	if srcNode["exo:commit"] != res.Commit {
		t.Errorf("source exo:commit = %v, want %s", srcNode["exo:commit"], res.Commit)
	}

	var activity lib.Node
	for _, n := range rec.Graph {
		for _, typ := range n.Types() {
			if typ == "Activity" {
				activity = n
			}
		}
	}
	if activity == nil {
		t.Fatalf("no Activity node in %+v", rec.Graph)
	}
	if activity["used"] != sourceID {
		t.Errorf("activity used = %v, want %s", activity["used"], sourceID)
	}
	if activity["wasAssociatedWith"] != lib.PersonAgentID("bob") {
		t.Errorf("activity wasAssociatedWith = %v, want %s", activity["wasAssociatedWith"], lib.PersonAgentID("bob"))
	}

	for _, n := range rec.Graph {
		if _, ok := n["wasDerivedFrom"]; ok {
			t.Errorf("unexpected wasDerivedFrom node for a single-asset import: %+v", n)
		}
		if _, ok := n["wasGeneratedBy"]; ok {
			t.Errorf("unexpected wasGeneratedBy node for imported content: %+v", n)
		}
	}
}

func TestRecordExohubImportProjectHasLocalDerivedFrom(t *testing.T) {
	withLoginToken(t, fakeJWT(`{"preferred_username":"bob","name":"Bob Smith"}`))

	source := newTestGitRepo(t)
	if err := os.WriteFile(filepath.Join(source.Dir, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source.Dir, "b.txt"), []byte("b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source.run("add", "-A")
	source.run("commit", "-q", "-m", "initial")
	source.markPublished("main")

	ref, err := exohub.ParseReference("exohub:myproject@main")
	if err != nil {
		t.Fatal(err)
	}
	res, err := exohub.ResolveLocalClone(source.Dir, ref)
	if err != nil {
		t.Fatalf("ResolveLocalClone() error = %v", err)
	}
	defer res.Close()

	importer := newTestGitRepo(t)
	withDir(t, importer.Dir)
	for _, f := range res.Files {
		target, err := exohubTargetPath(ref, f, "vendor")
		if err != nil {
			t.Fatal(err)
		}
		if err := materializeFile(res, f, target); err != nil {
			t.Fatalf("materializeFile() error = %v", err)
		}
	}

	ctx, err := prepareExohubImportContext()
	if err != nil {
		t.Fatalf("prepareExohubImportContext() error = %v", err)
	}
	if err := recordExohubImport(ctx, ref, res, "vendor", io.Discard); err != nil {
		t.Fatalf("recordExohubImport() error = %v", err)
	}

	records := recordFiles(t, importer.Dir)
	if len(records) != 1 {
		t.Fatalf("records = %v, want exactly one", records)
	}
	var rec *lib.Record
	for _, r := range records {
		rec = r
	}

	treeOID := func() string {
		out := source.run("rev-parse", "HEAD^{tree}")
		return out[:len(out)-1]
	}()
	sourceID := lib.GitTreeEntityID(treeOID)
	if _, ok := nodeByID(rec, sourceID); !ok {
		t.Fatalf("no source node %s in %+v", sourceID, rec.Graph)
	}

	targetID, err := lib.NewWorktreeReader(importer.Dir).ResolveContentID("vendor/")
	if err != nil {
		t.Fatalf("ResolveContentID(vendor/) error = %v", err)
	}
	targetNode, ok := nodeByID(rec, targetID)
	if !ok {
		t.Fatalf("no local target node %s in %+v", targetID, rec.Graph)
	}
	if targetNode["wasDerivedFrom"] != sourceID {
		t.Errorf("target wasDerivedFrom = %v, want %s", targetNode["wasDerivedFrom"], sourceID)
	}
	if targetNode["hadPrimarySource"] != sourceID {
		t.Errorf("target hadPrimarySource = %v, want %s", targetNode["hadPrimarySource"], sourceID)
	}
}

func TestCopySourceRecordsCoversImportedAsset(t *testing.T) {
	withLoginToken(t, fakeJWT(`{"preferred_username":"bob","name":"Bob Smith"}`))

	source := newTestGitRepo(t)
	if err := os.WriteFile(filepath.Join(source.Dir, "data.csv"), []byte("x,y\n1,2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source.run("add", "-A")
	source.run("commit", "-q", "-m", "initial")
	dataOID := func() string {
		out := source.run("rev-parse", "HEAD:data.csv")
		return out[:len(out)-1]
	}()
	dataID := lib.GitBlobEntityID(dataOID)

	act := lib.NewActivityID()
	person := "urn:exohub:agent:person:alice"
	srcRecord := fmt.Sprintf(`{
  "@context": %q,
  "@id": %q,
  "exo:assertion": "recorded",
  "exo:assertedBy": %q,
  "generatedAtTime": "2026-01-01T00:00:00Z",
  "@graph": [
    {"@id": %q, "@type": "Entity", "wasGeneratedBy": %q},
    {"@id": %q, "@type": "Activity"},
    {"@id": %q, "@type": "Person"}
  ]
}`, lib.ContextIRI, lib.NewRecordID(), person, dataID, act, act, person)
	if err := os.MkdirAll(filepath.Join(source.Dir, lib.RecordDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source.Dir, lib.RecordDir, "gen.jsonld"), []byte(srcRecord), 0o644); err != nil {
		t.Fatal(err)
	}
	source.run("add", "-A")
	source.run("commit", "-q", "-m", "record generation")
	source.markPublished("main")

	ref, err := exohub.ParseReference("exohub:myproject:data.csv@main")
	if err != nil {
		t.Fatal(err)
	}
	res, err := exohub.ResolveLocalClone(source.Dir, ref)
	if err != nil {
		t.Fatalf("ResolveLocalClone() error = %v", err)
	}
	defer res.Close()

	importer := newTestGitRepo(t)
	withDir(t, importer.Dir)
	if err := materializeFile(res, res.Files[0], "data.csv"); err != nil {
		t.Fatalf("materializeFile() error = %v", err)
	}
	ctx, err := prepareExohubImportContext()
	if err != nil {
		t.Fatalf("prepareExohubImportContext() error = %v", err)
	}
	if err := recordExohubImport(ctx, ref, res, "", io.Discard); err != nil {
		t.Fatalf("recordExohubImport() error = %v", err)
	}

	records := recordFiles(t, importer.Dir)
	var copied *lib.Record
	for path, r := range records {
		if path == lib.RecordDir+"/gen.jsonld" {
			copied = r
		}
	}
	if copied == nil {
		t.Fatalf("source's generating record was not copied, got %v", records)
	}
	if _, ok := nodeByID(copied, dataID); !ok {
		t.Errorf("copied record missing node %s: %+v", dataID, copied.Graph)
	}

	// Re-running must not duplicate or fail (dedupe by path/record id).
	root, err := exohubRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	_, coveredIDs, err := sourceEntityID(res, ref)
	if err != nil {
		t.Fatal(err)
	}
	if err := copySourceRecords(root, res, coveredIDs, io.Discard); err != nil {
		t.Fatalf("copySourceRecords() second call error = %v", err)
	}
	after := recordFiles(t, importer.Dir)
	if len(after) != len(records) {
		t.Errorf("record count changed after re-running copySourceRecords: before=%d after=%d", len(records), len(after))
	}
}

func TestCopySourceRecordsSkipsUnrelatedRecord(t *testing.T) {
	withLoginToken(t, fakeJWT(`{"preferred_username":"bob","name":"Bob Smith"}`))

	source := newTestGitRepo(t)
	if err := os.WriteFile(filepath.Join(source.Dir, "data.csv"), []byte("x,y\n1,2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source.Dir, "other.csv"), []byte("a,b\n3,4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source.run("add", "-A")
	source.run("commit", "-q", "-m", "initial")
	otherOID := func() string {
		out := source.run("rev-parse", "HEAD:other.csv")
		return out[:len(out)-1]
	}()
	otherID := lib.GitBlobEntityID(otherOID)

	act := lib.NewActivityID()
	person := "urn:exohub:agent:person:alice"
	unrelatedRecord := fmt.Sprintf(`{
  "@context": %q,
  "@id": %q,
  "exo:assertion": "recorded",
  "exo:assertedBy": %q,
  "generatedAtTime": "2026-01-01T00:00:00Z",
  "@graph": [
    {"@id": %q, "@type": "Entity", "wasGeneratedBy": %q},
    {"@id": %q, "@type": "Activity"},
    {"@id": %q, "@type": "Person"}
  ]
}`, lib.ContextIRI, lib.NewRecordID(), person, otherID, act, act, person)
	if err := os.MkdirAll(filepath.Join(source.Dir, lib.RecordDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source.Dir, lib.RecordDir, "unrelated.jsonld"), []byte(unrelatedRecord), 0o644); err != nil {
		t.Fatal(err)
	}
	source.run("add", "-A")
	source.run("commit", "-q", "-m", "unrelated record")
	source.markPublished("main")

	ref, err := exohub.ParseReference("exohub:myproject:data.csv@main")
	if err != nil {
		t.Fatal(err)
	}
	res, err := exohub.ResolveLocalClone(source.Dir, ref)
	if err != nil {
		t.Fatalf("ResolveLocalClone() error = %v", err)
	}
	defer res.Close()

	importer := newTestGitRepo(t)
	withDir(t, importer.Dir)
	if err := materializeFile(res, res.Files[0], "data.csv"); err != nil {
		t.Fatalf("materializeFile() error = %v", err)
	}
	ctx, err := prepareExohubImportContext()
	if err != nil {
		t.Fatalf("prepareExohubImportContext() error = %v", err)
	}
	if err := recordExohubImport(ctx, ref, res, "", io.Discard); err != nil {
		t.Fatalf("recordExohubImport() error = %v", err)
	}

	records := recordFiles(t, importer.Dir)
	for path := range records {
		if path == lib.RecordDir+"/unrelated.jsonld" {
			t.Errorf("unrelated record for other.csv was copied, want it skipped")
		}
	}
	if len(records) != 1 {
		t.Errorf("records = %v, want only the import record", records)
	}
}

// TestCopySourceRecordsSkipsSupersededRecord covers MR !295 review point 2:
// a source record superseded by a correction must never be copied on its
// own (the importer would then carry a stale generation claim); the
// superseding record is copied instead, since it directly covers the
// imported id too.
func TestCopySourceRecordsSkipsSupersededRecord(t *testing.T) {
	withLoginToken(t, fakeJWT(`{"preferred_username":"bob","name":"Bob Smith"}`))

	source := newTestGitRepo(t)
	if err := os.WriteFile(filepath.Join(source.Dir, "data.csv"), []byte("x,y\n1,2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source.run("add", "-A")
	source.run("commit", "-q", "-m", "initial")
	dataOID := func() string {
		out := source.run("rev-parse", "HEAD:data.csv")
		return out[:len(out)-1]
	}()
	dataID := lib.GitBlobEntityID(dataOID)

	person := "urn:exohub:agent:person:alice"
	oldAct := lib.NewActivityID()
	oldID := lib.NewRecordID()
	oldRecord := fmt.Sprintf(`{
  "@context": %q,
  "@id": %q,
  "exo:assertion": "recorded",
  "exo:assertedBy": %q,
  "generatedAtTime": "2026-01-01T00:00:00Z",
  "@graph": [
    {"@id": %q, "@type": "Entity", "wasGeneratedBy": %q},
    {"@id": %q, "@type": "Activity"},
    {"@id": %q, "@type": "Person"}
  ]
}`, lib.ContextIRI, oldID, person, dataID, oldAct, oldAct, person)
	if err := os.MkdirAll(filepath.Join(source.Dir, lib.RecordDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source.Dir, lib.RecordDir, "old.jsonld"), []byte(oldRecord), 0o644); err != nil {
		t.Fatal(err)
	}
	source.run("add", "-A")
	source.run("commit", "-q", "-m", "record generation")

	newAct := lib.NewActivityID()
	newRecord := fmt.Sprintf(`{
  "@context": %q,
  "@id": %q,
  "exo:assertion": "recorded",
  "exo:assertedBy": %q,
  "generatedAtTime": "2026-01-02T00:00:00Z",
  "exo:supersedes": [%q],
  "@graph": [
    {"@id": %q, "@type": "Entity", "wasGeneratedBy": %q},
    {"@id": %q, "@type": "Activity"},
    {"@id": %q, "@type": "Person"}
  ]
}`, lib.ContextIRI, lib.NewRecordID(), person, oldID, dataID, newAct, newAct, person)
	if err := os.WriteFile(filepath.Join(source.Dir, lib.RecordDir, "corrected.jsonld"), []byte(newRecord), 0o644); err != nil {
		t.Fatal(err)
	}
	source.run("add", "-A")
	source.run("commit", "-q", "-m", "correction")
	source.markPublished("main")

	ref, err := exohub.ParseReference("exohub:myproject:data.csv@main")
	if err != nil {
		t.Fatal(err)
	}
	res, err := exohub.ResolveLocalClone(source.Dir, ref)
	if err != nil {
		t.Fatalf("ResolveLocalClone() error = %v", err)
	}
	defer res.Close()

	importer := newTestGitRepo(t)
	withDir(t, importer.Dir)
	if err := materializeFile(res, res.Files[0], "data.csv"); err != nil {
		t.Fatalf("materializeFile() error = %v", err)
	}
	ctx, err := prepareExohubImportContext()
	if err != nil {
		t.Fatalf("prepareExohubImportContext() error = %v", err)
	}
	if err := recordExohubImport(ctx, ref, res, "", io.Discard); err != nil {
		t.Fatalf("recordExohubImport() error = %v", err)
	}

	records := recordFiles(t, importer.Dir)
	if _, ok := records[lib.RecordDir+"/old.jsonld"]; ok {
		t.Errorf("superseded record was copied, want it skipped: %v", records)
	}
	corrected, ok := records[lib.RecordDir+"/corrected.jsonld"]
	if !ok {
		t.Fatalf("superseding record was not copied, got %v", records)
	}
	if _, ok := nodeByID(corrected, dataID); !ok {
		t.Errorf("copied correction missing node %s: %+v", dataID, corrected.Graph)
	}
}

func TestValidSourceRecordPathRejectsTraversalAndNesting(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{lib.RecordDir + "/r.jsonld", true},
		{lib.RecordDir + "/../../etc/passwd", false},
		{lib.RecordDir + "/sub/r.jsonld", false},
		{lib.RecordDir + "/r.json", false},
		{lib.RecordDir + "/..jsonld", false},
		{"other/r.jsonld", false},
		{lib.RecordDir, false},
		{lib.RecordDir + "/", false},
	}
	for _, c := range cases {
		if got := validSourceRecordPath(c.path); got != c.want {
			t.Errorf("validSourceRecordPath(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

// TestCopySourceRecordsWarnsOnSkippedInvalidRecord covers MR !295 review
// point 4: a source record file that is skipped (invalid path or unparsable
// content) must produce a one-line warning explaining why, rather than
// silently vanishing.
func TestCopySourceRecordsWarnsOnSkippedInvalidRecord(t *testing.T) {
	withLoginToken(t, fakeJWT(`{"preferred_username":"bob","name":"Bob Smith"}`))

	source := newTestGitRepo(t)
	if err := os.WriteFile(filepath.Join(source.Dir, "data.csv"), []byte("x,y\n1,2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(source.Dir, lib.RecordDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source.Dir, lib.RecordDir, "broken.jsonld"), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	source.run("add", "-A")
	source.run("commit", "-q", "-m", "initial")
	source.markPublished("main")

	ref, err := exohub.ParseReference("exohub:myproject:data.csv@main")
	if err != nil {
		t.Fatal(err)
	}
	res, err := exohub.ResolveLocalClone(source.Dir, ref)
	if err != nil {
		t.Fatalf("ResolveLocalClone() error = %v", err)
	}
	defer res.Close()

	importer := newTestGitRepo(t)
	withDir(t, importer.Dir)
	if err := materializeFile(res, res.Files[0], "data.csv"); err != nil {
		t.Fatalf("materializeFile() error = %v", err)
	}
	ctx, err := prepareExohubImportContext()
	if err != nil {
		t.Fatalf("prepareExohubImportContext() error = %v", err)
	}
	var warn bytes.Buffer
	if err := recordExohubImport(ctx, ref, res, "", &warn); err != nil {
		t.Fatalf("recordExohubImport() error = %v", err)
	}
	if !strings.Contains(warn.String(), "broken.jsonld") {
		t.Errorf("warn output = %q, want it to mention the skipped broken.jsonld", warn.String())
	}
}

// TestAddExohubReferenceFailsBeforeMaterializingWithoutLogin covers MR !295
// review point 1: without a resolvable exo login token, addExohubReference
// must fail before writing or staging any file, not after.
func TestAddExohubReferenceFailsBeforeMaterializingWithoutLogin(t *testing.T) {
	withNoLoginToken(t)

	source := newTestGitRepo(t)
	if err := os.WriteFile(filepath.Join(source.Dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source.run("add", "-A")
	source.run("commit", "-q", "-m", "initial")
	source.markPublished("main")
	t.Setenv("EXOHUB_CLONE_MYPROJECT", source.Dir)

	ref, err := exohub.ParseReference("exohub:myproject:README.md@main")
	if err != nil {
		t.Fatal(err)
	}

	importer := newTestGitRepo(t)
	withDir(t, importer.Dir)

	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	if err := addExohubReference(cmd, ref, "", "", false); err == nil {
		t.Fatal("addExohubReference() error = nil, want an error without a login token")
	} else if !strings.Contains(err.Error(), "login") {
		t.Errorf("addExohubReference() error = %v, want it to mention login", err)
	}

	if _, err := os.Stat(filepath.Join(importer.Dir, "README.md")); !os.IsNotExist(err) {
		t.Errorf("README.md was materialised despite the failed identity check (err = %v)", err)
	}
	if _, err := os.Stat(filepath.Join(importer.Dir, lib.RecordDir)); !os.IsNotExist(err) {
		t.Errorf("%s was created despite the failed identity check (err = %v)", lib.RecordDir, err)
	}
}
