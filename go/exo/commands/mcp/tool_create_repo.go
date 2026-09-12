package mcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	gomcp "github.com/mark3labs/mcp-go/mcp"
)

type createRepoResult struct {
	Status    string   `json:"status"`
	RepoURL   string   `json:"repo_url,omitempty"`
	LocalPath string   `json:"local_path,omitempty"`
	Remotes   []string `json:"remotes,omitempty"`
	Output    string   `json:"output,omitempty"`
	Message   string   `json:"message,omitempty"`
}

func handleCreateRepo(_ context.Context, request gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
	repoURL := getStringParam(request, "repo_url")
	if repoURL == "" {
		return resultError("repo_url is required")
	}
	directory := getStringParam(request, "directory")

	if directory == "" {
		var err error
		directory, err = os.Getwd()
		if err != nil {
			return resultError("failed to get current directory: %v", err)
		}
	}

	absDir, err := filepath.Abs(directory)
	if err != nil {
		return resultError("failed to resolve directory: %v", err)
	}

	// Build exo init command — delegate all logic to exo init
	args := []string{"exo", "init", "--create-repo", "--yes", repoURL}

	cmd := command(args[0], args[1:]...)
	cmd.Dir = absDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return resultError("exo init failed: %s", string(out))
	}

	// Collect configured remotes
	var remotes []string
	remoteOut, _ := runInDir(absDir, "git", "remote")
	for _, r := range strings.Split(strings.TrimSpace(remoteOut), "\n") {
		r = strings.TrimSpace(r)
		if r != "" {
			remotes = append(remotes, r)
		}
	}

	message := "Repository initialized"
	if repoURL != "" {
		message = fmt.Sprintf("Repository initialized from %s", repoURL)
	}

	return resultJSON(createRepoResult{
		Status:    "ok",
		RepoURL:   repoURL,
		LocalPath: absDir,
		Remotes:   remotes,
		Output:    string(out),
		Message:   message,
	})
}
