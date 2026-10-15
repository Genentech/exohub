package mcp

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	addcmd "github.com/Genentech/exohub/go/exo/commands/add"
	gomcp "github.com/mark3labs/mcp-go/mcp"
)

func TestAutoAddResultReasons(t *testing.T) {
	result := addResult{AddURLFiles: []string{"https://example.com/file"}}
	applyAutoPlan(&result, []addcmd.PlannedFile{
		{Path: "README.md", Backend: addcmd.Git, Reason: "rule 1: *.md"},
		{Path: "data/a.csv", Backend: addcmd.Annex, Reason: "rule 2: *.csv"},
		{Path: "old.csv", Backend: addcmd.Git, Reason: "tracked (git) — unchanged"},
	})
	if len(result.GitFiles) != 2 || len(result.AnnexFiles) != 1 || len(result.Reasons) != 3 {
		t.Fatalf("result = %+v", result)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["reasons"] == nil {
		t.Fatal("missing reasons")
	}
}

func TestMCPAutoAddInvalidConfigBeforeExecution(t *testing.T) {
	if _, err := exec.LookPath("git-annex"); err != nil {
		t.Skip("git-annex required")
	}
	root := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"annex", "init", "mcp-test"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v %s", args, err, out)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, ".exohub"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".exohub/config"), []byte("annex:\n  routing:\n    - path: '*.txt'\n      to: anex\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("text\n"), 0644); err != nil {
		t.Fatal(err)
	}
	request := gomcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{"repo_dir": root, "paths": []any{"a.txt"}}
	result, err := handleAdd(context.Background(), request)
	if err != nil || !result.IsError {
		t.Fatalf("result = %+v, err = %v", result, err)
	}
	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil || len(out) != 0 {
		t.Fatalf("index = %q, err = %v", out, err)
	}
}
