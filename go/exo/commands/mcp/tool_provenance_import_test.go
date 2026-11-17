package mcp

import (
	"context"
	"testing"
)

func TestHandleProvenanceImportRequiresRepoDir(t *testing.T) {
	result, err := handleProvenanceImport(context.Background(), mustRequest(map[string]any{
		"format": "nextflow",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected error result, got %+v", result)
	}
}

func TestHandleProvenanceImportRequiresFormat(t *testing.T) {
	dir := fakeGitRepo(t)
	result, err := handleProvenanceImport(context.Background(), mustRequest(map[string]any{"repo_dir": dir}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected error result for missing format, got %+v", result)
	}
}

func TestHandleProvenanceImportFullArgs(t *testing.T) {
	dir := fakeGitRepo(t)
	calls := withFakeCommand(t, `echo '{"path":".exohub/provenance/run.json"}'`)

	_, err := handleProvenanceImport(context.Background(), mustRequest(map[string]any{
		"repo_dir":    dir,
		"format":      "nextflow",
		"run":         "run-123",
		"store":       ".lineage",
		"tasks":       true,
		"supersedes":  []any{"urn:exohub:record:0191a000-0000-7000-8000-000000000000"},
		"keep_source": true,
		"dry_run":     true,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	call := lastExoCall(t, calls)
	if call[1] != "provenance" || call[2] != "import" {
		t.Fatalf("exo provenance import args = %v, want to start with [provenance import]", call)
	}
	dashIdx := indexOf(call, "--")
	if dashIdx == -1 {
		t.Fatalf("exo provenance import args = %v, missing \"--\" separator before positionals", call)
	}
	if dashIdx+1 >= len(call) || call[dashIdx+1] != "nextflow" {
		t.Errorf("exo provenance import args = %v, want format positional right after \"--\"", call)
	}
	if dashIdx+2 >= len(call) || call[dashIdx+2] != "run-123" {
		t.Errorf("exo provenance import args = %v, want run positional after format", call)
	}
	for _, pair := range [][2]string{
		{"--store", ".lineage"},
		{"--supersedes", "urn:exohub:record:0191a000-0000-7000-8000-000000000000"},
	} {
		if !argPairPresent(call, pair[0], pair[1]) {
			t.Errorf("exo provenance import args = %v, missing %s %s", call, pair[0], pair[1])
		}
	}
	for _, flag := range []string{"--json", "--tasks", "--keep-source", "--dry-run"} {
		idx := indexOf(call, flag)
		if idx == -1 {
			t.Errorf("exo provenance import args = %v, missing %s", call, flag)
		} else if idx > dashIdx {
			t.Errorf("exo provenance import args = %v, flag %s must come before \"--\"", call, flag)
		}
	}
}

func TestHandleProvenanceImportDefaultRun(t *testing.T) {
	dir := fakeGitRepo(t)
	calls := withFakeCommand(t, `echo '{"path":".exohub/provenance/run.json"}'`)

	_, err := handleProvenanceImport(context.Background(), mustRequest(map[string]any{
		"repo_dir": dir,
		"format":   "nextflow",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	call := lastExoCall(t, calls)
	want := []string{"exo", "provenance", "import", "--json", "--", "nextflow"}
	if len(call) != len(want) {
		t.Fatalf("exo provenance import args = %v, want %v", call, want)
	}
	for i, w := range want {
		if call[i] != w {
			t.Fatalf("exo provenance import args = %v, want %v", call, want)
		}
	}
}

func TestHandleProvenanceImportRejectsUnsupportedFormat(t *testing.T) {
	dir := fakeGitRepo(t)
	calls := withFakeCommand(t, `echo '{"path":".exohub/provenance/run.json"}'`)

	result, err := handleProvenanceImport(context.Background(), mustRequest(map[string]any{
		"repo_dir": dir,
		"format":   "rocrate",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected error result for unsupported format, got %+v", result)
	}
	if len(*calls) != 0 {
		t.Errorf("expected no exo invocation for an unsupported format, got %v", *calls)
	}
}

func TestHandleProvenanceImportFlagLikeInputsNeverParsedAsFlags(t *testing.T) {
	dir := fakeGitRepo(t)

	// A format that looks like a flag is rejected by the allow-list before
	// any exo invocation happens.
	result, err := handleProvenanceImport(context.Background(), mustRequest(map[string]any{
		"repo_dir": dir,
		"format":   "--store=/x",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected error result for flag-like format, got %+v", result)
	}

	// A flag-like run value lands strictly after "--", so cobra treats it
	// as a literal positional instead of re-parsing it as a flag.
	calls := withFakeCommand(t, `echo '{"path":".exohub/provenance/run.json"}'`)
	_, err = handleProvenanceImport(context.Background(), mustRequest(map[string]any{
		"repo_dir": dir,
		"format":   "nextflow",
		"run":      "--keep-source",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	call := lastExoCall(t, calls)
	dashIdx := indexOf(call, "--")
	if dashIdx == -1 || dashIdx+2 >= len(call) || call[dashIdx+2] != "--keep-source" {
		t.Fatalf("exo provenance import args = %v, want flag-like run literally after format", call)
	}
	// --keep-source was never requested, so it must not appear before "--".
	for i, a := range call {
		if i < dashIdx && a == "--keep-source" {
			t.Errorf("exo provenance import args = %v, flag-like run value was parsed as --keep-source", call)
		}
	}
}

func indexOf(call []string, s string) int {
	for i, a := range call {
		if a == s {
			return i
		}
	}
	return -1
}
