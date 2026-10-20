package mcp

import (
	"context"
	"testing"
)

func TestHandleGetRequiresRepoDir(t *testing.T) {
	result, err := handleGet(context.Background(), mustRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected error result, got %+v", result)
	}
}

func TestHandleGetRequiresGitRepo(t *testing.T) {
	result, err := handleGet(context.Background(), mustRequest(map[string]any{"repo_dir": t.TempDir()}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected error result for non-git directory, got %+v", result)
	}
}

func TestHandleGetBuildsArgs(t *testing.T) {
	dir := fakeGitRepo(t)
	calls := withFakeCommand(t, "exit 0")

	result, err := handleGet(context.Background(), mustRequest(map[string]any{
		"repo_dir": dir,
		"paths":    []any{"a.txt", "b.txt"},
		"from":     "s3-annex",
		"all":      true,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %+v", result)
	}

	call := lastExoCall(t, calls)
	want := []string{"exo", "get", "--from", "s3-annex", "--all", "a.txt", "b.txt"}
	if len(call) != len(want) {
		t.Fatalf("exo get args = %v, want %v", call, want)
	}
	for i := range want {
		if call[i] != want[i] {
			t.Fatalf("exo get args = %v, want %v", call, want)
		}
	}
}
