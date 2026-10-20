package mcp

import (
	"context"
	"testing"
)

func TestHandleProvenanceValidateRequiresRepoDir(t *testing.T) {
	result, err := handleProvenanceValidate(context.Background(), mustRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected error result, got %+v", result)
	}
}

func TestHandleProvenanceValidateBuildsArgs(t *testing.T) {
	dir := fakeGitRepo(t)
	calls := withFakeCommand(t, `echo '{"errors":[],"warnings":[]}'`)

	result, err := handleProvenanceValidate(context.Background(), mustRequest(map[string]any{
		"repo_dir": dir,
		"staged":   true,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %+v", result)
	}

	call := lastExoCall(t, calls)
	want := []string{"exo", "provenance", "validate", "--json", "--staged"}
	if len(call) != len(want) {
		t.Fatalf("exo provenance validate args = %v, want %v", call, want)
	}
}

func TestHandleProvenanceValidateToleratesNonZeroExitWithFindings(t *testing.T) {
	dir := fakeGitRepo(t)
	withFakeCommand(t, `echo '{"errors":[{"record":"r1","message":"bad"}]}'; exit 1`)

	result, err := handleProvenanceValidate(context.Background(), mustRequest(map[string]any{"repo_dir": dir}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected findings to be returned as a successful result, got error: %+v", result)
	}
}

func TestHandleProvenanceValidateNonJSONFailureIsError(t *testing.T) {
	dir := fakeGitRepo(t)
	withFakeCommand(t, `echo "boom" >&2; exit 2`)

	result, err := handleProvenanceValidate(context.Background(), mustRequest(map[string]any{"repo_dir": dir}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected error result for non-JSON failure, got %+v", result)
	}
}
