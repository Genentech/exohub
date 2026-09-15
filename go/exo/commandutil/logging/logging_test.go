package logging

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecuteWithLoggingCreatesFiles(t *testing.T) {
	tempDir := t.TempDir()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	defer os.Chdir(orig)
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	if err := ExecuteWithLogging("test", "dest", []string{"printf", "hello"}, []string{"path/a"}, "2"); err != nil {
		t.Fatalf("ExecuteWithLogging: %v", err)
	}

	logs, err := filepath.Glob(".git/exohub/logs/*.log")
	if err != nil {
		t.Fatalf("listing logs: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("expected one log file, got %d", len(logs))
	}

	content, err := os.ReadFile(logs[0])
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !strings.Contains(string(content), "hello") {
		t.Fatalf("log missing expected output: %q", content)
	}

	metas, err := filepath.Glob(".git/exohub/logs/*.meta.json")
	if err != nil {
		t.Fatalf("listing metas: %v", err)
	}
	if len(metas) != 1 {
		t.Fatalf("expected one meta file, got %d", len(metas))
	}

	metaData, err := os.ReadFile(metas[0])
	if err != nil {
		t.Fatalf("read meta: %v", err)
	}

	var meta map[string]any
	if err := json.Unmarshal(metaData, &meta); err != nil {
		t.Fatalf("unmarshal meta: %v", err)
	}

	if got, _ := meta["scope"].(string); got != "test" {
		t.Fatalf("scope mismatch: %q", got)
	}
	if got, _ := meta["remote"].(string); got != "dest" {
		t.Fatalf("remote mismatch: %q", got)
	}
	paths, _ := meta["paths"].([]any)
	if len(paths) != 1 || paths[0] != "path/a" {
		t.Fatalf("paths content: %#v", paths)
	}

	cmd, _ := meta["cmd"].([]any)
	if len(cmd) == 0 || cmd[0] != "printf" {
		t.Fatalf("unexpected cmd: %#v", cmd)
	}
}

func TestRecordDryRunLogCreatesFiles(t *testing.T) {
	tempDir := t.TempDir()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	defer os.Chdir(orig)
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	content := "dry-run plan"
	extras := map[string]any{
		"dry_run":          true,
		"remote_mutations": false,
	}
	if err := RecordDryRunLog("sync", "dest", []string{"exo", "sync", "--dry-run"}, []string{"path/a"}, "2", content, extras); err != nil {
		t.Fatalf("RecordDryRunLog: %v", err)
	}

	logs, err := filepath.Glob(".git/exohub/logs/*.log")
	if err != nil {
		t.Fatalf("listing logs: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("expected one log file, got %d", len(logs))
	}
	if !strings.Contains(filepath.Base(logs[0]), "_dry-run_") {
		t.Fatalf("expected dry-run suffix in log filename, got %q", filepath.Base(logs[0]))
	}

	logContent, err := os.ReadFile(logs[0])
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !strings.Contains(string(logContent), content) {
		t.Fatalf("log missing expected output: %q", logContent)
	}

	metas, err := filepath.Glob(".git/exohub/logs/*.meta.json")
	if err != nil {
		t.Fatalf("listing metas: %v", err)
	}
	if len(metas) != 1 {
		t.Fatalf("expected one meta file, got %d", len(metas))
	}

	metaData, err := os.ReadFile(metas[0])
	if err != nil {
		t.Fatalf("read meta: %v", err)
	}

	var meta map[string]any
	if err := json.Unmarshal(metaData, &meta); err != nil {
		t.Fatalf("unmarshal meta: %v", err)
	}

	if got, _ := meta["dry_run"].(bool); !got {
		t.Fatalf("dry_run missing or false: %v", meta["dry_run"])
	}
	if got, _ := meta["remote_mutations"].(bool); got {
		t.Fatalf("remote_mutations expected false, got %v", got)
	}
}

func TestQuoteHelpers(t *testing.T) {
	if got := ShellQuote("foo bar"); got != "'foo bar'" {
		t.Fatalf("ShellQuote space: %q", got)
	}
	if got := ShellQuote("foo'bar"); got != "'foo'\"'\"'bar'" {
		t.Fatalf("ShellQuote quote: %q", got)
	}
	args := QuoteArgs([]string{"git", "annex sync", ""})
	if len(args) != 3 || args[1] != "'annex sync'" || args[2] != "''" {
		t.Fatalf("QuoteArgs: %#v", args)
	}
}
