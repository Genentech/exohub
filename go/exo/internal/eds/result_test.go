package eds

import (
	"encoding/json"
	"testing"
)

// TestClassify_LiveOperationURLFixture is shaped from a real response
// captured while polling operation_url against a live EDS (no token in it,
// since operation_url itself carries the auth); the username in repo_dir has
// been replaced with a placeholder. It pins the actual field names the
// server sends: result.repo_dir (not "destination"), result.bytes_total
// (not "annex_bytes_total"), and progress == null on completed operations.
func TestClassify_LiveOperationURLFixture(t *testing.T) {
	const fixture = `{"status": "completed", "progress": null, "environment": "tst", "project_id": "winery", "version": "v0.0.4",
 "result": {"status": "ok", "error": "", "scope": "user",
   "repo_dir": "/data/eds/users/alice/workspace/winery/v0.0.4",
   "files_synced": 1408, "bytes_synced": 188429675536,
   "files_total": 1414, "annex_files_total": 1410, "bytes_total": 188645144770,
   "verified": true, "missing_files": [], "files_added": 0, "commit_sha": ""}}`

	var op Operation
	if err := json.Unmarshal([]byte(fixture), &op); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}

	if op.Progress != nil {
		t.Errorf("Progress = %+v, want nil on a completed operation", op.Progress)
	}
	if !IsTerminal(op.Status) {
		t.Fatalf("op.Status = %q, want terminal", op.Status)
	}

	outcome := Classify(&op)
	if !outcome.Success {
		t.Fatalf("outcome.Success = false, want true; outcome = %+v", outcome)
	}

	const wantDestination = "/data/eds/users/alice/workspace/winery/v0.0.4"
	if outcome.Destination != wantDestination {
		t.Errorf("outcome.Destination = %q, want %q", outcome.Destination, wantDestination)
	}
	if outcome.Scope != "user" {
		t.Errorf("outcome.Scope = %q, want %q", outcome.Scope, "user")
	}

	if op.Result.BytesTotal != 188645144770 {
		t.Errorf("Result.BytesTotal = %d, want 188645144770", op.Result.BytesTotal)
	}
	if op.Result.AnnexFilesTotal != 1410 {
		t.Errorf("Result.AnnexFilesTotal = %d, want 1410", op.Result.AnnexFilesTotal)
	}
	if op.Result.FilesTotal != 1414 {
		t.Errorf("Result.FilesTotal = %d, want 1414", op.Result.FilesTotal)
	}
}
