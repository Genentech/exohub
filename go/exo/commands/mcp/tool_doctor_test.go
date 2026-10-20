package mcp

import (
	"context"
	"testing"
)

func TestHandleDoctorNoArgs(t *testing.T) {
	calls := withFakeCommand(t, `echo '[{"name":"env","status":"PASS"}]'`)

	result, err := handleDoctor(context.Background(), mustRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %+v", result)
	}

	call := lastExoCall(t, calls)
	want := []string{"exo", "doctor", "--json"}
	if len(call) != len(want) {
		t.Fatalf("exo doctor args = %v, want %v", call, want)
	}
}

func TestHandleDoctorWithOptions(t *testing.T) {
	dir := fakeGitRepo(t)
	calls := withFakeCommand(t, `echo '[{"name":"env","status":"FAIL"}]'`)

	result, err := handleDoctor(context.Background(), mustRequest(map[string]any{
		"repo_dir":   dir,
		"remote":     "s3-annex",
		"write_test": true,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Tolerant: non-zero exit with valid JSON output is still success.
	if result.IsError {
		t.Fatalf("unexpected error result for well-formed JSON findings: %+v", result)
	}

	call := lastExoCall(t, calls)
	found := map[string]bool{}
	for i, a := range call {
		if a == "--repo" || a == "--remote" || a == "--write-test" {
			found[a] = true
			_ = i
		}
	}
	for _, flag := range []string{"--repo", "--remote", "--write-test"} {
		if !found[flag] {
			t.Errorf("exo doctor args = %v, missing %s", call, flag)
		}
	}
}

func TestHandleDoctorNonJSONFailureIsError(t *testing.T) {
	withFakeCommand(t, `echo "boom" >&2; exit 1`)

	result, err := handleDoctor(context.Background(), mustRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected error result for non-JSON failure, got %+v", result)
	}
}
