package mcp

import (
	"context"
	"path/filepath"
	"strings"

	gomcp "github.com/mark3labs/mcp-go/mcp"
)

type cloneResult struct {
	Status    string `json:"status"`
	LocalPath string `json:"local_path"`
	Output    string `json:"output,omitempty"`
}

func handleClone(_ context.Context, request gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
	repoURL := getStringParam(request, "repo_url")
	if repoURL == "" {
		return resultError("repo_url is required")
	}

	directory := getStringParam(request, "directory")

	// Use git clone directly
	args := []string{"git", "clone", repoURL}
	if directory != "" {
		args = append(args, directory)
	}

	out, err := runInDir(".", args...)
	if err != nil {
		return resultError("clone failed: %s", out)
	}

	// Determine local path
	localPath := directory
	if localPath == "" {
		localPath = extractRepoName(repoURL)
	}
	absPath, _ := filepath.Abs(localPath)

	return resultJSON(cloneResult{
		Status:    "ok",
		LocalPath: absPath,
		Output:    string(out),
	})
}

// extractRepoName derives the repo directory name from a clone URL.
func extractRepoName(url string) string {
	// Remove trailing .git
	url = strings.TrimSuffix(url, ".git")
	// Remove trailing slash
	url = strings.TrimSuffix(url, "/")
	// Get last path component
	parts := strings.Split(url, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return url
}
