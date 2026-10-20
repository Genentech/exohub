package mcp

import (
	"context"
	"path/filepath"

	gomcp "github.com/mark3labs/mcp-go/mcp"
)

func handleDoctor(_ context.Context, request gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
	args := []string{"doctor", "--json"}

	if repoDir := getStringParam(request, "repo_dir"); repoDir != "" {
		repoAbs, err := filepath.Abs(repoDir)
		if err != nil {
			return resultError("failed to resolve repo path: %v", err)
		}
		args = append(args, "--repo", repoAbs)
	}
	if remote := getStringParam(request, "remote"); remote != "" {
		args = append(args, "--remote", remote)
	}
	if getBoolParam(request, "write_test", false) {
		args = append(args, "--write-test")
	}

	// 'exo doctor' exits non-zero when any check fails — that's a findings
	// report, not a tool failure, so trust well-formed JSON regardless of
	// exit code.
	return runExoJSONTolerant(".", args...)
}
