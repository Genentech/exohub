package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"

	gomcp "github.com/mark3labs/mcp-go/mcp"
)

func handleInfo(_ context.Context, request gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
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

	out, err := runInDir(repoAbs, "exo", "info", "--json")
	if err != nil {
		return resultError("exo info failed: %s", out)
	}

	// Parse the JSON output and return it directly
	var info json.RawMessage
	if json.Unmarshal([]byte(out), &info) == nil {
		return resultJSON(info)
	}

	// Fallback: return as plain text
	return resultJSON(map[string]string{
		"status": "ok",
		"output": out,
	})
}
