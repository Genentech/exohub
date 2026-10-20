package mcp

import (
	"context"
	"testing"
)

func TestHandleProvenanceShowRequiresRepoDir(t *testing.T) {
	result, err := handleProvenanceShow(context.Background(), mustRequest(map[string]any{"path": "out.csv"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected error result, got %+v", result)
	}
}

func TestHandleProvenanceShowRequiresPath(t *testing.T) {
	dir := fakeGitRepo(t)
	result, err := handleProvenanceShow(context.Background(), mustRequest(map[string]any{"repo_dir": dir}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected error result for missing path, got %+v", result)
	}
}

func TestHandleProvenanceShowBuildsArgs(t *testing.T) {
	dir := fakeGitRepo(t)
	calls := withFakeCommand(t, `echo '{"@graph":[]}'`)

	result, err := handleProvenanceShow(context.Background(), mustRequest(map[string]any{
		"repo_dir": dir,
		"path":     "out.csv",
		"ref":      "main",
		"depth":    float64(2),
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %+v", result)
	}

	call := lastExoCall(t, calls)
	want := []string{"exo", "provenance", "show", "out.csv", "--json", "--ref", "main", "--depth", "2"}
	if len(call) != len(want) {
		t.Fatalf("exo provenance show args = %v, want %v", call, want)
	}
	for i := range want {
		if call[i] != want[i] {
			t.Fatalf("exo provenance show args = %v, want %v", call, want)
		}
	}
}

func TestHandleProvenanceShowNonJSONFailureIsError(t *testing.T) {
	dir := fakeGitRepo(t)
	withFakeCommand(t, `echo "boom"; exit 1`)

	result, err := handleProvenanceShow(context.Background(), mustRequest(map[string]any{
		"repo_dir": dir,
		"path":     "out.csv",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected error result for command failure, got %+v", result)
	}
}
