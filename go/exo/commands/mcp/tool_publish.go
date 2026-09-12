package mcp

import (
	"context"
	"fmt"
	"path/filepath"

	gomcp "github.com/mark3labs/mcp-go/mcp"
)

type publishResult struct {
	Status  string `json:"status"`
	Output  string `json:"output,omitempty"`
	Message string `json:"message"`
}

func handlePublish(_ context.Context, request gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
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

	syncWith := getStringArrayParam(request, "sync")

	args := []string{"exo", "publish"}
	for _, r := range syncWith {
		args = append(args, "--sync", r)
	}

	out, err := runInDir(repoAbs, args...)
	if err != nil {
		return resultError("publish failed: %v\n%s", err, out)
	}

	return resultJSON(publishResult{
		Status:  "completed",
		Output:  out,
		Message: fmt.Sprintf("publish completed for %s", repoAbs),
	})
}
