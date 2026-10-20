package mcp

import (
	"context"
	"path/filepath"

	gomcp "github.com/mark3labs/mcp-go/mcp"
)

func handleProvenanceValidate(_ context.Context, request gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
	repoDir := getStringParam(request, "repo_dir")
	if repoDir == "" {
		return resultError("repo_dir is required")
	}
	if err := requireGitRepo(repoDir); err != nil {
		return resultError("%v", err)
	}

	repoAbs, err := filepath.Abs(repoDir)
	if err != nil {
		return resultError("failed to resolve repo path: %v", err)
	}

	args := []string{"provenance", "validate", "--json"}
	if getBoolParam(request, "staged", false) {
		args = append(args, "--staged")
	}
	if ref := getStringParam(request, "ref"); ref != "" {
		args = append(args, "--ref", ref)
	}

	// 'exo provenance validate' exits non-zero when it finds errors — that's
	// the findings report this tool returns, not a tool failure, so trust
	// well-formed JSON regardless of exit code.
	return runExoJSONTolerant(repoAbs, args...)
}
