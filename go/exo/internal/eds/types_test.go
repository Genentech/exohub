package eds

import (
	"encoding/json"
	"testing"
)

// These fixtures are the literal (host/path-neutralised) JSON payloads
// captured from a real EDS, quoted in issue #103. Field names here must
// match the EDS wire format exactly — do not add or rename fields to make
// a test pass; a previous spec did that and the mocks passed while the
// real output was wrong.

const runningOperationJSON = `{"status": "running",
 "progress": {
   "step": "syncing",
   "files_synced": 0, "bytes_synced": 0,
   "files_staged": 0, "bytes_staged": 0,
   "staging_dir": "/data/eds/users/alice/workspace/winery/.git-store/.git/annex/tmp",
   "files_added": 0,
   "ts": "2026-01-01T12:00:00.000000+00:00",
   "bytes_total": 188645144770, "files_total": 1414, "annex_files_total": 1410,
   "activities": [{"activity_type": "dataset_exo_get", "state": "started", "attempt": 1,
                   "last_failure": null, "last_heartbeat_time": "2026-01-01T12:00:00.000Z",
                   "step": "syncing", "files_synced": 0, "bytes_synced": 0}]}}`

const completedOperationJSON = `{"status": "completed", "progress": null,
 "result": {"status": "ok", "verified": true, "missing_files": [],
            "files_synced": 1, "bytes_synced": 60715200,
            "files_total": 1, "annex_files_total": 1, "bytes_total": 60715200,
            "files_added": 0, "scope": "user",
            "repo_dir": "/data/eds/users/alice/workspace/cvrm_exohub/main"}}`

func TestDecodeOperation_Running(t *testing.T) {
	var op Operation
	if err := json.Unmarshal([]byte(runningOperationJSON), &op); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if op.Status != "running" {
		t.Errorf("Status = %q", op.Status)
	}
	if op.Progress == nil {
		t.Fatal("Progress = nil")
	}
	p := op.Progress
	if p.Step != "syncing" {
		t.Errorf("Step = %q", p.Step)
	}
	if p.TS != "2026-01-01T12:00:00.000000+00:00" {
		t.Errorf("TS = %q", p.TS)
	}
	if p.FilesStaged != 0 || p.BytesStaged != 0 {
		t.Errorf("FilesStaged/BytesStaged = %d/%d", p.FilesStaged, p.BytesStaged)
	}
	if p.BytesTotal != 188645144770 || p.FilesTotal != 1414 || p.AnnexFilesTotal != 1410 {
		t.Errorf("totals = %d/%d/%d", p.BytesTotal, p.FilesTotal, p.AnnexFilesTotal)
	}
	if len(p.Activities) != 1 {
		t.Fatalf("Activities = %d entries, want 1", len(p.Activities))
	}
	a := p.Activities[0]
	if a.State != "started" {
		t.Errorf("Activity.State = %q", a.State)
	}
	if a.Attempt != 1 {
		t.Errorf("Activity.Attempt = %d", a.Attempt)
	}
	if a.LastFailure != nil {
		t.Errorf("Activity.LastFailure = %v, want nil", a.LastFailure)
	}
}

func TestDecodeOperation_Completed(t *testing.T) {
	var op Operation
	if err := json.Unmarshal([]byte(completedOperationJSON), &op); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if op.Status != StatusCompleted {
		t.Errorf("Status = %q", op.Status)
	}
	if op.Progress != nil {
		t.Errorf("Progress = %+v, want nil", op.Progress)
	}
	if op.Result == nil {
		t.Fatal("Result = nil")
	}
	r := op.Result
	if !r.Verified {
		t.Error("Verified = false, want true")
	}
	if len(r.MissingFiles) != 0 {
		t.Errorf("MissingFiles = %v", r.MissingFiles)
	}
	if r.FilesSynced != 1 || r.BytesSynced != 60715200 {
		t.Errorf("FilesSynced/BytesSynced = %d/%d", r.FilesSynced, r.BytesSynced)
	}
	if r.AnnexFilesTotal != 1 || r.BytesTotal != 60715200 {
		t.Errorf("AnnexFilesTotal/BytesTotal = %d/%d", r.AnnexFilesTotal, r.BytesTotal)
	}
	if r.Scope != "user" {
		t.Errorf("Scope = %q", r.Scope)
	}
	if r.RepoDir != "/data/eds/users/alice/workspace/cvrm_exohub/main" {
		t.Errorf("RepoDir = %q", r.RepoDir)
	}
}

func TestActivity_RetryWithLastFailure(t *testing.T) {
	failure := "heartbeat timeout"
	var op Operation
	body := `{"status": "running", "progress": {"step": "syncing",
	  "activities": [{"state": "started", "attempt": 2, "last_failure": "heartbeat timeout"}]}}`
	if err := json.Unmarshal([]byte(body), &op); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	a := op.Progress.Activities[0]
	if a.Attempt != 2 {
		t.Errorf("Attempt = %d", a.Attempt)
	}
	if a.LastFailure == nil || *a.LastFailure != failure {
		t.Errorf("LastFailure = %v, want %q", a.LastFailure, failure)
	}
}
