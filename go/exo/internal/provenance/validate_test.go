package provenance

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestValidAgentName(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
	}{
		{"alice", true},
		{"claude-code", true},
		{"", false},
		{"has space", false},
		{"has/slash", false},
		{"tab\ttab", false},
	} {
		if got := ValidAgentName(tc.name); got != tc.want {
			t.Errorf("ValidAgentName(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func minimalRecordJSON(id, assertion, assertedBy, graph string) string {
	return fmt.Sprintf(`{
  "@context": %q,
  "@id": %q,
  "exo:assertion": %q,
  "exo:assertedBy": %q,
  "generatedAtTime": "2026-10-14T10:00:00Z",
  "@graph": [%s]
}`, ContextIRI, id, assertion, assertedBy, graph)
}

func hasFinding(report *Report, code, path string) bool {
	for _, f := range report.Findings {
		if f.Code == code && (path == "" || f.Path == path) {
			return true
		}
	}
	return false
}

func findingsOfSeverity(report *Report, sev Severity) []Finding {
	var out []Finding
	for _, f := range report.Findings {
		if f.Severity == sev {
			out = append(out, f)
		}
	}
	return out
}

func TestValidate_NoRecords(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "README.md", "hi")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	report, err := Validate(NewWorktreeReader(root))
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if report.RecordCount != 0 {
		t.Errorf("RecordCount = %d, want 0", report.RecordCount)
	}
	if report.HasErrors() {
		t.Errorf("HasErrors() = true, want false: %+v", report.Findings)
	}
}

func TestValidate_InvalidJSON(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, RecordDir+"/bad.jsonld", "not json")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	report, err := Validate(NewWorktreeReader(root))
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if !hasFinding(report, "invalid-record", RecordDir+"/bad.jsonld") {
		t.Errorf("findings = %+v, want invalid-record", report.Findings)
	}
	if !report.HasErrors() {
		t.Error("HasErrors() = false, want true")
	}
}

func TestValidate_MalformedRecordID(t *testing.T) {
	root, run := stateTestRepo(t, false)
	// A well-formed-looking id (36 hex/dash characters, matching the
	// schema's loose @id pattern) whose UUID version is not 7, so it
	// reaches the IsUUIDv7 check rather than being rejected by
	// ValidateSchema first.
	rec := minimalRecordJSON("urn:exohub:record:123e4567-e89b-42d3-a456-426614174000", "recorded", "urn:exohub:agent:person:alice",
		`{"@id": "urn:exohub:git:blob:4b825dc642cb6eb9a060e54bf8d69288fbee4904", "@type": "Entity", "exo:path": "a.txt"}`)
	writeRepoFile(t, root, RecordDir+"/a.jsonld", rec)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	report, err := Validate(NewWorktreeReader(root))
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if !hasFinding(report, "malformed-record-id", RecordDir+"/a.jsonld") {
		t.Errorf("findings = %+v, want malformed-record-id", report.Findings)
	}
}

func TestValidate_AnnexedRecordFile(t *testing.T) {
	root, run := stateTestRepo(t, true)
	run("git", "config", "annex.dotfiles", "true")
	writeRepoFile(t, root, RecordDir+"/annexed.jsonld", "{}")
	run("git", "annex", "add", RecordDir+"/annexed.jsonld")
	run("git", "commit", "-q", "-m", "init")

	report, err := Validate(NewWorktreeReader(root))
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if !hasFinding(report, "annexed-record", RecordDir+"/annexed.jsonld") {
		t.Errorf("findings = %+v, want annexed-record", report.Findings)
	}
}

func TestValidate_DuplicateRecordID(t *testing.T) {
	root, run := stateTestRepo(t, false)
	id := NewRecordID()
	graph := `{"@id": "urn:exohub:git:blob:4b825dc642cb6eb9a060e54bf8d69288fbee4904", "@type": "Entity", "exo:path": "a.txt"}`
	writeRepoFile(t, root, RecordDir+"/a.jsonld", minimalRecordJSON(id, "recorded", "urn:exohub:agent:person:alice", graph))
	writeRepoFile(t, root, RecordDir+"/b.jsonld", minimalRecordJSON(id, "recorded", "urn:exohub:agent:person:alice", graph))
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	report, err := Validate(NewWorktreeReader(root))
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if !hasFinding(report, "duplicate-record-id", RecordDir+"/a.jsonld") || !hasFinding(report, "duplicate-record-id", RecordDir+"/b.jsonld") {
		t.Errorf("findings = %+v, want duplicate-record-id on both files", report.Findings)
	}
}

func TestValidate_UnknownSupersedes(t *testing.T) {
	root, run := stateTestRepo(t, false)
	id := NewRecordID()
	rec := fmt.Sprintf(`{
  "@context": %q,
  "@id": %q,
  "exo:assertion": "asserted",
  "exo:assertedBy": "urn:exohub:agent:person:alice",
  "generatedAtTime": "2026-10-14T10:00:00Z",
  "exo:supersedes": [%q],
  "@graph": [{"@id": "urn:exohub:git:blob:4b825dc642cb6eb9a060e54bf8d69288fbee4904", "@type": "Entity", "exo:path": "a.txt"}]
}`, ContextIRI, id, NewRecordID())
	writeRepoFile(t, root, RecordDir+"/a.jsonld", rec)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	report, err := Validate(NewWorktreeReader(root))
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if !hasFinding(report, "unknown-supersedes", RecordDir+"/a.jsonld") {
		t.Errorf("findings = %+v, want unknown-supersedes", report.Findings)
	}
}

func TestValidate_ConflictingProperty(t *testing.T) {
	root, run := stateTestRepo(t, false)
	activityID := NewActivityID()
	graphA := fmt.Sprintf(`{"@id": %q, "@type": "Activity", "startedAtTime": "2026-10-14T09:00:00Z"}`, activityID)
	graphB := fmt.Sprintf(`{"@id": %q, "@type": "Activity", "startedAtTime": "2026-10-14T10:00:00Z"}`, activityID)
	writeRepoFile(t, root, RecordDir+"/a.jsonld", minimalRecordJSON(NewRecordID(), "recorded", "urn:exohub:agent:person:alice", graphA))
	writeRepoFile(t, root, RecordDir+"/b.jsonld", minimalRecordJSON(NewRecordID(), "recorded", "urn:exohub:agent:person:alice", graphB))
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	report, err := Validate(NewWorktreeReader(root))
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if !hasFinding(report, "conflicting-property", "") {
		t.Errorf("findings = %+v, want conflicting-property", report.Findings)
	}
}

func TestValidate_ConflictingProperty_SupersededExcluded(t *testing.T) {
	root, run := stateTestRepo(t, false)
	activityID := NewActivityID()
	oldID := NewRecordID()
	graphOld := fmt.Sprintf(`{"@id": %q, "@type": "Activity", "startedAtTime": "2026-10-14T09:00:00Z"}`, activityID)
	graphNew := fmt.Sprintf(`{"@id": %q, "@type": "Activity", "startedAtTime": "2026-10-14T10:00:00Z"}`, activityID)
	writeRepoFile(t, root, RecordDir+"/old.jsonld", minimalRecordJSON(oldID, "recorded", "urn:exohub:agent:person:alice", graphOld))

	newRec := fmt.Sprintf(`{
  "@context": %q,
  "@id": %q,
  "exo:assertion": "recorded",
  "exo:assertedBy": "urn:exohub:agent:person:alice",
  "generatedAtTime": "2026-10-14T10:00:00Z",
  "exo:supersedes": [%q],
  "@graph": [%s]
}`, ContextIRI, NewRecordID(), oldID, graphNew)
	writeRepoFile(t, root, RecordDir+"/new.jsonld", newRec)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	report, err := Validate(NewWorktreeReader(root))
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if hasFinding(report, "conflicting-property", "") {
		t.Errorf("findings = %+v, want no conflicting-property once the old record is superseded", report.Findings)
	}
}

func TestValidate_NonHashingBackendWarning(t *testing.T) {
	root, run := stateTestRepo(t, false)
	graph := `{"@id": "urn:exohub:annex:WORM-s10--abc.dat", "@type": "Entity", "exo:path": "a.dat"}`
	writeRepoFile(t, root, RecordDir+"/a.jsonld", minimalRecordJSON(NewRecordID(), "recorded", "urn:exohub:agent:person:alice", graph))
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	report, err := Validate(NewWorktreeReader(root))
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if !hasFinding(report, "non-hashing-backend", RecordDir+"/a.jsonld") {
		t.Errorf("findings = %+v, want non-hashing-backend", report.Findings)
	}
	for _, f := range findingsOfSeverity(report, SeverityError) {
		if f.Code == "non-hashing-backend" {
			t.Errorf("non-hashing-backend should be a warning, found as error: %+v", f)
		}
	}
}

func TestValidate_MissingContentGitBlob(t *testing.T) {
	root, run := stateTestRepo(t, false)
	graph := `{"@id": "urn:exohub:git:blob:0000000000000000000000000000000000000000", "@type": "Entity", "exo:path": "a.txt"}`
	writeRepoFile(t, root, RecordDir+"/a.jsonld", minimalRecordJSON(NewRecordID(), "recorded", "urn:exohub:agent:person:alice", graph))
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	report, err := Validate(NewWorktreeReader(root))
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if !hasFinding(report, "missing-content", RecordDir+"/a.jsonld") {
		t.Errorf("findings = %+v, want missing-content", report.Findings)
	}
}

func TestValidate_MissingEvidenceWarning(t *testing.T) {
	root, run := stateTestRepo(t, false)
	graph := `{"@id": "urn:exohub:git:blob:4b825dc642cb6eb9a060e54bf8d69288fbee4904", "@type": "Entity", "exo:path": "a.txt"}`
	writeRepoFile(t, root, RecordDir+"/a.jsonld", minimalRecordJSON(NewRecordID(), "asserted", "urn:exohub:agent:software:claude-code", graph))
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	report, err := Validate(NewWorktreeReader(root))
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if !hasFinding(report, "missing-evidence", RecordDir+"/a.jsonld") {
		t.Errorf("findings = %+v, want missing-evidence", report.Findings)
	}
}

func TestValidate_ImmutabilityModifiedRecord(t *testing.T) {
	root, run := stateTestRepo(t, false)
	graph := `{"@id": "urn:exohub:git:blob:4b825dc642cb6eb9a060e54bf8d69288fbee4904", "@type": "Entity", "exo:path": "a.txt"}`
	writeRepoFile(t, root, RecordDir+"/a.jsonld", minimalRecordJSON(NewRecordID(), "recorded", "urn:exohub:agent:person:alice", graph))
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	writeRepoFile(t, root, RecordDir+"/a.jsonld", minimalRecordJSON(NewRecordID(), "recorded", "urn:exohub:agent:person:alice", graph))
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "modify")

	report, err := Validate(NewWorktreeReader(root))
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if !hasFinding(report, "modified-record", RecordDir+"/a.jsonld") {
		t.Errorf("findings = %+v, want modified-record", report.Findings)
	}
}

func TestValidate_ImmutabilityDeletedRecord(t *testing.T) {
	root, run := stateTestRepo(t, false)
	graph := `{"@id": "urn:exohub:git:blob:4b825dc642cb6eb9a060e54bf8d69288fbee4904", "@type": "Entity", "exo:path": "a.txt"}`
	writeRepoFile(t, root, RecordDir+"/a.jsonld", minimalRecordJSON(NewRecordID(), "recorded", "urn:exohub:agent:person:alice", graph))
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	run("git", "rm", "-q", RecordDir+"/a.jsonld")
	run("git", "commit", "-q", "-m", "delete")

	report, err := Validate(NewWorktreeReader(root))
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if !hasFinding(report, "modified-record", RecordDir+"/a.jsonld") {
		t.Errorf("findings = %+v, want modified-record for the deletion", report.Findings)
	}
}

// TestValidate_ImmutabilityStagedModifiedRecord covers the gap review !255
// comment #1 identified: modifying a committed record and `git add`-ing it
// (but not committing) must be caught by `--staged` mode, not just the
// next commit's history walk.
func TestValidate_ImmutabilityStagedModifiedRecord(t *testing.T) {
	root, run := stateTestRepo(t, false)
	graph := `{"@id": "urn:exohub:git:blob:4b825dc642cb6eb9a060e54bf8d69288fbee4904", "@type": "Entity", "exo:path": "a.txt"}`
	writeRepoFile(t, root, RecordDir+"/a.jsonld", minimalRecordJSON(NewRecordID(), "recorded", "urn:exohub:agent:person:alice", graph))
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	writeRepoFile(t, root, RecordDir+"/a.jsonld", minimalRecordJSON(NewRecordID(), "recorded", "urn:exohub:agent:person:alice", graph))
	run("git", "add", ".")

	report, err := Validate(NewStagedReader(root))
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if !hasFinding(report, "modified-record", RecordDir+"/a.jsonld") {
		t.Errorf("findings = %+v, want modified-record for a staged, uncommitted modification", report.Findings)
	}
	if !report.HasErrors() {
		t.Error("HasErrors() = false, want true")
	}
}

// TestValidate_ImmutabilityWorktreeModifiedRecord covers the same gap for
// the default worktree mode, with the modification left unstaged.
func TestValidate_ImmutabilityWorktreeModifiedRecord(t *testing.T) {
	root, run := stateTestRepo(t, false)
	graph := `{"@id": "urn:exohub:git:blob:4b825dc642cb6eb9a060e54bf8d69288fbee4904", "@type": "Entity", "exo:path": "a.txt"}`
	writeRepoFile(t, root, RecordDir+"/a.jsonld", minimalRecordJSON(NewRecordID(), "recorded", "urn:exohub:agent:person:alice", graph))
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	writeRepoFile(t, root, RecordDir+"/a.jsonld", minimalRecordJSON(NewRecordID(), "recorded", "urn:exohub:agent:person:alice", graph))

	report, err := Validate(NewWorktreeReader(root))
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if !hasFinding(report, "modified-record", RecordDir+"/a.jsonld") {
		t.Errorf("findings = %+v, want modified-record for an unstaged worktree modification", report.Findings)
	}
}

// TestValidate_ImmutabilityWorktreeDeletedUncommitted covers an uncommitted
// deletion in the worktree.
func TestValidate_ImmutabilityWorktreeDeletedUncommitted(t *testing.T) {
	root, run := stateTestRepo(t, false)
	graph := `{"@id": "urn:exohub:git:blob:4b825dc642cb6eb9a060e54bf8d69288fbee4904", "@type": "Entity", "exo:path": "a.txt"}`
	writeRepoFile(t, root, RecordDir+"/a.jsonld", minimalRecordJSON(NewRecordID(), "recorded", "urn:exohub:agent:person:alice", graph))
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	if err := os.Remove(root + "/" + RecordDir + "/a.jsonld"); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}

	report, err := Validate(NewWorktreeReader(root))
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if !hasFinding(report, "modified-record", RecordDir+"/a.jsonld") {
		t.Errorf("findings = %+v, want modified-record for an uncommitted deletion", report.Findings)
	}
}

// TestValidate_ImmutabilityNewUncommittedRecordAllowed ensures a brand new,
// never-committed record staged or present in the worktree is not flagged:
// only a record that already existed at HEAD is immutable.
func TestValidate_ImmutabilityNewUncommittedRecordAllowed(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "README.md", "hi")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	graph := `{"@id": "urn:exohub:git:blob:4b825dc642cb6eb9a060e54bf8d69288fbee4904", "@type": "Entity", "exo:path": "a.txt"}`
	writeRepoFile(t, root, RecordDir+"/a.jsonld", minimalRecordJSON(NewRecordID(), "recorded", "urn:exohub:agent:person:alice", graph))
	run("git", "add", ".")

	report, err := Validate(NewStagedReader(root))
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if hasFinding(report, "modified-record", "") {
		t.Errorf("findings = %+v, a new staged record must not be flagged as modified", report.Findings)
	}
}

// TestValidate_ImmutabilityRenameCountsAsDeleteAndAdd covers review
// comment #1's `--no-renames` requirement: moving a committed record to a
// new file name must not be hidden from the diff-filter=AMD history walk
// as a rename; it must be detected as a delete of the original path.
func TestValidate_ImmutabilityRenameCountsAsDeleteAndAdd(t *testing.T) {
	root, run := stateTestRepo(t, false)
	run("git", "config", "diff.renames", "true")
	graph := `{"@id": "urn:exohub:git:blob:4b825dc642cb6eb9a060e54bf8d69288fbee4904", "@type": "Entity", "exo:path": "a.txt"}`
	rec := minimalRecordJSON(NewRecordID(), "recorded", "urn:exohub:agent:person:alice", graph)
	writeRepoFile(t, root, RecordDir+"/a.jsonld", rec)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	run("git", "mv", RecordDir+"/a.jsonld", RecordDir+"/b.jsonld")
	run("git", "commit", "-q", "-m", "rename")

	report, err := Validate(NewWorktreeReader(root))
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if !hasFinding(report, "modified-record", RecordDir+"/a.jsonld") {
		t.Errorf("findings = %+v, want modified-record for a.jsonld (renamed away, must count as delete+add)", report.Findings)
	}
}

func TestValidate_RefMode(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, RecordDir+"/bad.jsonld", "not json")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")
	run("git", "tag", "v1")

	writeRepoFile(t, root, RecordDir+"/bad.jsonld", "still not json but changed")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "second")

	report, err := Validate(NewRefReader(root, "v1"))
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if !hasFinding(report, "invalid-record", RecordDir+"/bad.jsonld") {
		t.Errorf("findings = %+v, want invalid-record at ref v1", report.Findings)
	}
	if hasFinding(report, "modified-record", "") {
		t.Errorf("findings = %+v, ref v1 should not see the later modification", report.Findings)
	}
}

func TestAnnexKeyBackend(t *testing.T) {
	cases := []struct {
		key        string
		wantHash   string
		nonHashing bool
	}{
		{"SHA256E-s2048--ab12.csv", "SHA256E", false},
		{"WORM-s10--abc.dat", "WORM", true},
		{"URL-s10--abc.dat", "URL", true},
		{"MD5E-s10--abc.dat", "MD5E", false},
	}
	for _, c := range cases {
		backend, nonHashing := annexKeyBackend(c.key)
		if backend != c.wantHash || nonHashing != c.nonHashing {
			t.Errorf("annexKeyBackend(%q) = (%q, %v), want (%q, %v)", c.key, backend, nonHashing, c.wantHash, c.nonHashing)
		}
	}
}

func TestCollectIdentifiers(t *testing.T) {
	rec, err := Parse([]byte(minimalRecordJSON(NewRecordID(), "recorded", "urn:exohub:agent:person:alice",
		`{"@id": "urn:exohub:git:blob:4b825dc642cb6eb9a060e54bf8d69288fbee4904", "@type": "Entity", "exo:path": "a.txt", "wasDerivedFrom": ["urn:exohub:annex:SHA256E-s1--aa.csv"]}`)))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	ids := collectIdentifiers(rec)
	if !strings.Contains(strings.Join(ids, ","), "urn:exohub:annex:SHA256E-s1--aa.csv") {
		t.Errorf("collectIdentifiers() = %v, want it to include the wasDerivedFrom id", ids)
	}
}

// TestValidate_SchemaRejectsUnknownEnvelopeProperty covers review comment
// #2: Validate must call ValidateSchema, which enforces the envelope's
// "additionalProperties": false, not just Parse's narrower envelope check.
func TestValidate_SchemaRejectsUnknownEnvelopeProperty(t *testing.T) {
	root, run := stateTestRepo(t, false)
	graph := `{"@id": "urn:exohub:git:blob:4b825dc642cb6eb9a060e54bf8d69288fbee4904", "@type": "Entity", "exo:path": "a.txt"}`
	rec := fmt.Sprintf(`{
  "@context": %q,
  "@id": %q,
  "exo:assertion": "recorded",
  "exo:assertedBy": "urn:exohub:agent:person:alice",
  "generatedAtTime": "2026-10-14T10:00:00Z",
  "notInTheSchema": "x",
  "@graph": [%s]
}`, ContextIRI, NewRecordID(), graph)
	writeRepoFile(t, root, RecordDir+"/a.jsonld", rec)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	report, err := Validate(NewWorktreeReader(root))
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if !hasFinding(report, "schema-violation", RecordDir+"/a.jsonld") {
		t.Errorf("findings = %+v, want schema-violation for an unknown envelope property", report.Findings)
	}
	if !report.HasErrors() {
		t.Error("HasErrors() = false, want true")
	}
}

// TestValidate_SchemaRejectsMalformedTimestamp covers review comment #2's
// startedAtTime/endedAtTime/generatedAtTime RFC3339 requirement, at the
// node level (an Activity's startedAtTime).
func TestValidate_SchemaRejectsMalformedTimestamp(t *testing.T) {
	root, run := stateTestRepo(t, false)
	graph := `{"@id": "urn:exohub:activity:` + strings.TrimPrefix(NewActivityID(), ActivityURNPrefix) + `", "@type": "Activity", "startedAtTime": "not-a-timestamp"}`
	rec := minimalRecordJSON(NewRecordID(), "recorded", "urn:exohub:agent:person:alice", graph)
	writeRepoFile(t, root, RecordDir+"/a.jsonld", rec)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	report, err := Validate(NewWorktreeReader(root))
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if !hasFinding(report, "schema-violation", RecordDir+"/a.jsonld") {
		t.Errorf("findings = %+v, want schema-violation for a malformed node startedAtTime", report.Findings)
	}
}

// TestValidate_MalformedIdentifierIsError covers review comment #3: a
// malformed git:blob: identifier must be reported as an error
// (ClassIdentifier, malformed-identifier), not treated as a well-formed id
// with potentially-missing content (a warning).
func TestValidate_MalformedIdentifierIsError(t *testing.T) {
	root, run := stateTestRepo(t, false)
	graph := `{"@id": "urn:exohub:git:blob:not-hex", "@type": "Entity", "exo:path": "a.txt"}`
	writeRepoFile(t, root, RecordDir+"/a.jsonld", minimalRecordJSON(NewRecordID(), "recorded", "urn:exohub:agent:person:alice", graph))
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	report, err := Validate(NewWorktreeReader(root))
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	found := false
	for _, f := range report.Findings {
		if f.Code == "malformed-identifier" && f.Class == ClassIdentifier && f.Severity == SeverityError {
			found = true
		}
		if f.Code == "missing-content" {
			t.Errorf("findings = %+v, malformed id must not also produce missing-content", report.Findings)
		}
	}
	if !found {
		t.Errorf("findings = %+v, want an error-severity malformed-identifier finding", report.Findings)
	}
	if !report.HasErrors() {
		t.Error("HasErrors() = false, want true")
	}
}

// TestValidate_UnknownURNExohubKindIsError covers the "unknown urn:exohub:
// kind" branch of review comment #3.
func TestValidate_UnknownURNExohubKindIsError(t *testing.T) {
	root, run := stateTestRepo(t, false)
	graph := `{"@id": "urn:exohub:something-else:x", "@type": "Entity", "exo:path": "a.txt"}`
	writeRepoFile(t, root, RecordDir+"/a.jsonld", minimalRecordJSON(NewRecordID(), "recorded", "urn:exohub:agent:person:alice", graph))
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	report, err := Validate(NewWorktreeReader(root))
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if !hasFinding(report, "malformed-identifier", RecordDir+"/a.jsonld") {
		t.Errorf("findings = %+v, want malformed-identifier for an unrecognized urn:exohub: kind", report.Findings)
	}
}

// TestValidate_NonIRIValuedPropertyNotRequiredToBeAnIdentifier ensures a
// property the profile does not declare IRI-valued (here, an extension
// property holding a relative path) is left alone: the review's
// "other IRI-valued properties must be an absolute IRI" rule must not be
// misapplied to every node property.
func TestValidate_NonIRIValuedPropertyNotRequiredToBeAnIdentifier(t *testing.T) {
	root, run := stateTestRepo(t, false)
	graph := `{"@id": "urn:exohub:git:blob:4b825dc642cb6eb9a060e54bf8d69288fbee4904", "@type": "Entity", "exo:path": "results/de.parquet"}`
	writeRepoFile(t, root, RecordDir+"/a.jsonld", minimalRecordJSON(NewRecordID(), "recorded", "urn:exohub:agent:person:alice", graph))
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	report, err := Validate(NewWorktreeReader(root))
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if report.HasErrors() {
		t.Errorf("findings = %+v, exo:path is not IRI-valued and must not be flagged", report.Findings)
	}
}

// TestCheckSingleValuedConflicts_NormalizesIDRefForms covers review
// comment #4: a plain-string idRef and its {"@id": ...} object form for
// the same value are not a conflict.
func TestCheckSingleValuedConflicts_NormalizesIDRefForms(t *testing.T) {
	parsed := []parsedRecord{
		{path: "a.jsonld", rec: &Record{ID: "r1", Graph: []Node{{"@id": "n1", "wasGeneratedBy": "urn:exohub:agent:person:alice"}}}},
		{path: "b.jsonld", rec: &Record{ID: "r2", Graph: []Node{{"@id": "n1", "wasGeneratedBy": map[string]any{"@id": "urn:exohub:agent:person:alice"}}}}},
	}
	report := &Report{}
	checkSingleValuedConflicts(report, parsed, map[string]bool{})
	if len(report.Findings) != 0 {
		t.Errorf("findings = %+v, \"x\" and {\"@id\": \"x\"} must not conflict", report.Findings)
	}
}

// TestValidate_AnnexAvailableChecksAnnexUUID covers review comment #5: a
// git-annex-uninitialized repository must not produce a false
// missing-content warning for an annex: identifier, because
// `git annex whereis` cannot be trusted there.
func TestValidate_AnnexAvailableChecksAnnexUUID(t *testing.T) {
	root, run := stateTestRepo(t, false)
	if _, err := exec.LookPath("git-annex"); err == nil {
		// Confirm `git annex version` alone would have been misleading:
		// it succeeds even though this repository was never initialized.
		cmd := exec.Command("git", "annex", "version")
		cmd.Dir = root
		if err := cmd.Run(); err != nil {
			t.Skipf("git annex version unexpectedly failed: %v", err)
		}
	}
	graph := `{"@id": "urn:exohub:annex:SHA256E-s2--73cb3858a687a8494ca3323053016282f3dad39d42cf62ca4e79dda2aac7d9ac.txt", "@type": "Entity", "exo:path": "a.txt"}`
	writeRepoFile(t, root, RecordDir+"/a.jsonld", minimalRecordJSON(NewRecordID(), "recorded", "urn:exohub:agent:person:alice", graph))
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	if annexAvailable(NewWorktreeReader(root)) {
		t.Error("annexAvailable() = true for a repository without git annex init")
	}

	report, err := Validate(NewWorktreeReader(root))
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if hasFinding(report, "missing-content", "") {
		t.Errorf("findings = %+v, want no missing-content warning without git annex init", report.Findings)
	}
}

// TestBatchAnnexKeysKnown covers review comment #6's batched git-annex
// lookup: a known key and an unknown key in the same batch must each
// report correctly, and the batch call (which exits non-zero whenever any
// key has no known copies) must not be treated as a failure.
func TestBatchAnnexKeysKnown(t *testing.T) {
	root, run := stateTestRepo(t, true)
	writeRepoFile(t, root, "a.txt", "hello")
	run("git", "annex", "add", "a.txt")
	run("git", "commit", "-q", "-m", "init")
	keyOut := run("git", "annex", "lookupkey", "a.txt")
	knownKey := strings.TrimSpace(keyOut)

	reader := NewWorktreeReader(root)
	result := batchAnnexKeysKnown(reader, []string{knownKey, "SHA256E-s1--doesnotexist.dat"})
	if !result[knownKey] {
		t.Errorf("batchAnnexKeysKnown()[%q] = false, want true", knownKey)
	}
	if result["SHA256E-s1--doesnotexist.dat"] {
		t.Error("batchAnnexKeysKnown() reported an unknown key as known")
	}
}

// TestBatchGitObjectsExist covers review comment #6's batched git object
// lookup.
func TestBatchGitObjectsExist(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "a.txt", "hello")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")
	blobOID := strings.TrimSpace(run("git", "hash-object", "a.txt"))

	reader := NewWorktreeReader(root)
	missingOID := "0000000000000000000000000000000000000000"
	result := batchGitObjectsExist(reader, []string{blobOID, missingOID})
	if !result[blobOID] {
		t.Errorf("batchGitObjectsExist()[%q] = false, want true", blobOID)
	}
	if result[missingOID] {
		t.Error("batchGitObjectsExist() reported a never-written oid as existing")
	}
}
