package mcp

import (
	"context"
	"path/filepath"
	"strings"

	gomcp "github.com/mark3labs/mcp-go/mcp"
)

// supportedImportFormats lists the `exo provenance import <format>` adapters
// this MCP tool accepts. Kept in sync with the formats registered in
// go/exo/commands/provenance (currently just "nextflow"); exo itself
// re-validates format, but checking here means a bad value never reaches
// the command line as a bare positional.
var supportedImportFormats = map[string]bool{
	"nextflow": true,
}

func handleProvenanceImport(ctx context.Context, request gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
	repoDir := getStringParam(request, "repo_dir")
	if repoDir == "" {
		return resultError("repo_dir is required")
	}
	if err := requireGitRepo(repoDir); err != nil {
		return resultError("%v", err)
	}

	format := getStringParam(request, "format")
	if format == "" {
		return resultError("format is required")
	}
	if !supportedImportFormats[format] {
		return resultError("unsupported import format %q (supported: nextflow)", format)
	}

	repoAbs, err := filepath.Abs(repoDir)
	if err != nil {
		return resultError("failed to resolve repo path: %v", err)
	}

	// Flags first, then "--", then the positional format/run arguments: run
	// is agent-supplied free text, and without "--" a value starting with
	// "-" (e.g. "--store=/elsewhere") would be parsed by cobra as a flag of
	// 'exo provenance import' instead of a positional, letting tool input
	// reinterpret the command line.
	args := []string{"provenance", "import", "--json"}

	if store := getStringParam(request, "store"); store != "" {
		args = append(args, "--store", store)
	}
	if getBoolParam(request, "tasks", false) {
		args = append(args, "--tasks")
	}
	for _, s := range getStringArrayParam(request, "supersedes") {
		args = append(args, "--supersedes", s)
	}
	if getBoolParam(request, "keep_source", false) {
		args = append(args, "--keep-source")
	}
	if getBoolParam(request, "dry_run", false) {
		args = append(args, "--dry-run")
	}

	args = append(args, "--", format)
	if run := getStringParam(request, "run"); run != "" {
		args = append(args, run)
	}

	out, err := runInDir(repoAbs, append([]string{"exo"}, args...)...)
	if err != nil {
		return resultError("exo provenance import failed: %s", strings.TrimSpace(out))
	}
	return jsonOrText(out)
}
