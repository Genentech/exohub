package mcp

import (
	"context"
	"strings"

	gomcp "github.com/mark3labs/mcp-go/mcp"
)

type statusResult struct {
	Clean      bool     `json:"clean"`
	Untracked  []string `json:"untracked,omitempty"`
	Modified   []string `json:"modified,omitempty"`
	Staged     []string `json:"staged,omitempty"`
	Deleted    []string `json:"deleted,omitempty"`
	Renamed    []string `json:"renamed,omitempty"`
	Conflicted []string `json:"conflicted,omitempty"`
}

func handleStatus(_ context.Context, request gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
	repoDir := getStringParam(request, "repo_dir")
	if repoDir == "" {
		return resultError("repo_dir is required")
	}
	if err := requireGitRepo(repoDir); err != nil {
		return resultError("%v", err)
	}

	out, err := runInDir(repoDir, "git", "status", "--porcelain")
	if err != nil {
		return resultError("git status failed: %s", out)
	}

	result := statusResult{Clean: true}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if len(line) < 3 {
			continue
		}
		result.Clean = false
		code := line[:2]
		file := strings.TrimSpace(line[3:])

		switch {
		case code == "??":
			result.Untracked = append(result.Untracked, file)
		case code[0] == 'A' || code[1] == 'A':
			result.Staged = append(result.Staged, file)
		case code[0] == 'M' || code[1] == 'M':
			result.Modified = append(result.Modified, file)
		case code[0] == 'D' || code[1] == 'D':
			result.Deleted = append(result.Deleted, file)
		case code[0] == 'R' || code[1] == 'R':
			result.Renamed = append(result.Renamed, file)
		case code == "UU" || code == "AA" || code == "DD":
			result.Conflicted = append(result.Conflicted, file)
		default:
			// Other states go to modified as a catch-all
			result.Modified = append(result.Modified, file)
		}
	}

	return resultJSON(result)
}
