package mcp

import (
	"context"
	"path/filepath"

	gomcp "github.com/mark3labs/mcp-go/mcp"
)

type getResult struct {
	Status string `json:"status"`
	Output string `json:"output,omitempty"`
}

func handleGet(_ context.Context, request gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
	repoDir := getStringParam(request, "repo_dir")
	if repoDir == "" {
		return resultError("repo_dir is required")
	}
	if err := requireGitRepo(repoDir); err != nil {
		return resultError("%v", err)
	}
	if err := requireAnnexInit(repoDir); err != nil {
		return resultError("%v", err)
	}

	repoAbs, err := filepath.Abs(repoDir)
	if err != nil {
		return resultError("failed to resolve repo path: %v", err)
	}

	args := []string{"get"}
	if from := getStringParam(request, "from"); from != "" {
		args = append(args, "--from", from)
	}
	if getBoolParam(request, "all", false) {
		args = append(args, "--all")
	}
	args = append(args, getStringArrayParam(request, "paths")...)

	out, err := runInDir(repoAbs, append([]string{"exo"}, args...)...)
	if err != nil {
		return resultError("exo get failed: %s", out)
	}

	return resultJSON(getResult{Status: "ok", Output: out})
}
