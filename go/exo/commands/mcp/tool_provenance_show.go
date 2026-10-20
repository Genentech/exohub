package mcp

import (
	"context"
	"path/filepath"
	"strconv"

	gomcp "github.com/mark3labs/mcp-go/mcp"
)

func handleProvenanceShow(_ context.Context, request gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
	repoDir := getStringParam(request, "repo_dir")
	if repoDir == "" {
		return resultError("repo_dir is required")
	}
	if err := requireGitRepo(repoDir); err != nil {
		return resultError("%v", err)
	}

	path := getStringParam(request, "path")
	if path == "" {
		return resultError("path is required")
	}

	repoAbs, err := filepath.Abs(repoDir)
	if err != nil {
		return resultError("failed to resolve repo path: %v", err)
	}

	args := []string{"provenance", "show", path, "--json"}
	if ref := getStringParam(request, "ref"); ref != "" {
		args = append(args, "--ref", ref)
	}
	if depth := getNumberParam(request, "depth"); depth > 0 {
		args = append(args, "--depth", strconv.Itoa(depth))
	}

	return runExoJSON(repoAbs, args...)
}
