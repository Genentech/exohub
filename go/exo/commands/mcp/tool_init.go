package mcp

import (
	"context"
	"path/filepath"

	gomcp "github.com/mark3labs/mcp-go/mcp"
)

type initResult struct {
	Status  string `json:"status"`
	Output  string `json:"output,omitempty"`
	Message string `json:"message,omitempty"`
}

func handleInit(_ context.Context, request gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
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

	out, err := runInDir(repoAbs, "exo", "init", "--yes")
	if err != nil {
		return resultError("exo init failed: %s", out)
	}

	return resultJSON(initResult{
		Status:  "ok",
		Output:  out,
		Message: "Repository initialized — remotes from .exohub/remotes are now configured",
	})
}
