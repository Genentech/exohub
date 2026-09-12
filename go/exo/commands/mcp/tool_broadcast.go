package mcp

import (
	"context"
	"fmt"

	gomcp "github.com/mark3labs/mcp-go/mcp"
)

type broadcastResult struct {
	Status  string   `json:"status"`
	Remotes []string `json:"remotes,omitempty"`
	Output  string   `json:"output,omitempty"`
}

func handleBroadcast(_ context.Context, request gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
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

	remotes := getStringArrayParam(request, "with")

	// Ensure remotes are initialized via exo init before broadcasting
	if _, err := runInDir(repoDir, "exo", "init", "--yes"); err != nil {
		// Non-fatal: remotes may already be configured
	}

	if len(remotes) == 0 {
		out, err := runInDir(repoDir, "git", "annex", "sync", "--no-content")
		if err != nil {
			return resultError("broadcast failed: %s", out)
		}
		return resultJSON(broadcastResult{Status: "ok", Output: out})
	}

	var allOutput string
	for _, remote := range remotes {
		out, err := runInDir(repoDir, "git", "annex", "sync", "--no-content", remote)
		if err != nil {
			return resultError("broadcast to remote %q failed: %s", remote, out)
		}
		allOutput += fmt.Sprintf("=== %s ===\n%s\n", remote, out)
	}

	return resultJSON(broadcastResult{Status: "ok", Remotes: remotes, Output: allOutput})
}
