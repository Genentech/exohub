package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	gomcp "github.com/mark3labs/mcp-go/mcp"
)

func mustRequest(args map[string]any) gomcp.CallToolRequest {
	if args == nil {
		args = map[string]any{}
	}
	var req gomcp.CallToolRequest
	req.Params.Arguments = args
	return req
}

func fakeGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func lastExoCall(t *testing.T, calls *[][]string) []string {
	t.Helper()
	for i := len(*calls) - 1; i >= 0; i-- {
		if (*calls)[i][0] == "exo" {
			return (*calls)[i]
		}
	}
	t.Fatalf("no 'exo' invocation recorded, calls=%v", *calls)
	return nil
}

func TestHandleInitRequiresRepoDir(t *testing.T) {
	result, err := handleInit(context.Background(), mustRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected error result, got %+v", result)
	}
}

func TestHandleInitRequiresGitRepo(t *testing.T) {
	result, err := handleInit(context.Background(), mustRequest(map[string]any{"repo_dir": t.TempDir()}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected error result for non-git directory, got %+v", result)
	}
}

func TestHandleInitPlainInvocation(t *testing.T) {
	dir := fakeGitRepo(t)
	calls := withFakeCommand(t, "exit 0")

	result, err := handleInit(context.Background(), mustRequest(map[string]any{"repo_dir": dir}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %+v", result)
	}

	call := lastExoCall(t, calls)
	want := []string{"exo", "init", "--yes"}
	if len(call) != len(want) {
		t.Fatalf("exo init args = %v, want %v", call, want)
	}
	for i := range want {
		if call[i] != want[i] {
			t.Fatalf("exo init args = %v, want %v", call, want)
		}
	}
}

func TestHandleInitWithProfileAndAnswers(t *testing.T) {
	dir := fakeGitRepo(t)
	calls := withFakeCommand(t, "exit 0")

	_, err := handleInit(context.Background(), mustRequest(map[string]any{
		"repo_dir": dir,
		"profile":  "sandbox",
		"name":     "my-repo",
		"answers": map[string]any{
			"read_access": "authenticated",
			"create_repo": true,
		},
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	call := lastExoCall(t, calls)
	argsJSON, _ := json.Marshal(call)
	contains := func(a, b string) bool {
		for i := 0; i+1 <= len(call); i++ {
			if call[i] == a && i+1 < len(call) && call[i+1] == b {
				return true
			}
		}
		return false
	}
	if !contains("--profile", "sandbox") {
		t.Errorf("missing --profile sandbox in %s", argsJSON)
	}
	if !contains("--name", "my-repo") {
		t.Errorf("missing --name my-repo in %s", argsJSON)
	}
	if !contains("--set", "create_repo=true") {
		t.Errorf("missing --set create_repo=true in %s", argsJSON)
	}
	if !contains("--set", "read_access=authenticated") {
		t.Errorf("missing --set read_access=authenticated in %s", argsJSON)
	}
}

func TestHandleInitAnswersMustBeObject(t *testing.T) {
	dir := fakeGitRepo(t)
	withFakeCommand(t, "exit 0")

	result, err := handleInit(context.Background(), mustRequest(map[string]any{
		"repo_dir": dir,
		"answers":  "not-an-object",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected error result for non-object answers, got %+v", result)
	}
}

func TestFormatAnswerValue(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{true, "true"},
		{false, "false"},
		{"hello", "hello"},
		{float64(42), "42"},
	}
	for _, c := range cases {
		if got := formatAnswerValue(c.in); got != c.want {
			t.Errorf("formatAnswerValue(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}
