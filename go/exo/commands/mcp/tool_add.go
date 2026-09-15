package mcp

import (
	"context"
	"net/url"
	"os"
	"strings"

	gomcp "github.com/mark3labs/mcp-go/mcp"
)

type addResult struct {
	GitFiles    []string `json:"git_files,omitempty"`
	AnnexFiles  []string `json:"annex_files,omitempty"`
	AddURLFiles []string `json:"addurl_files,omitempty"`
}

func handleAdd(_ context.Context, request gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
	repoDir := getStringParam(request, "repo_dir")
	if repoDir == "" {
		return resultError("repo_dir is required")
	}
	if err := requireGitRepo(repoDir); err != nil {
		return resultError("%v", err)
	}

	paths := getStringArrayParam(request, "paths")
	if len(paths) == 0 {
		return resultError("paths is required")
	}

	mode := getStringParam(request, "mode")
	if mode == "" {
		mode = "auto"
	}

	// Annex and auto modes require git-annex to be initialized
	if mode != "git" {
		if err := requireAnnexInit(repoDir); err != nil {
			return resultError("%v", err)
		}
	}

	// Build exo add command
	exoPath, err := os.Executable()
	if err != nil {
		return resultError("cannot find exo binary: %v", err)
	}

	args := []string{exoPath, "add"}
	switch mode {
	case "annex":
		args = append(args, "--annex")
	case "git":
		args = append(args, "--git")
	case "auto":
		// default, no flag needed
	default:
		return resultError("invalid mode %q: must be auto, annex, or git", mode)
	}
	args = append(args, paths...)

	out, err := runInDir(repoDir, args...)
	if err != nil {
		return resultError("exo add failed: %s", out)
	}

	// Categorize paths for the response based on what we know about the inputs
	var urlPaths, localPaths []string
	for _, p := range paths {
		if mcpIsURL(p) {
			urlPaths = append(urlPaths, p)
		} else {
			localPaths = append(localPaths, p)
		}
	}

	result := addResult{AddURLFiles: urlPaths}
	switch mode {
	case "annex":
		result.AnnexFiles = localPaths
	case "git":
		result.GitFiles = localPaths
	default:
		// In auto mode we don't know the split without re-detecting;
		// report all local paths under annex_files for simplicity since
		// the actual routing is handled by exo add.
		result.AnnexFiles = localPaths
	}

	return resultJSON(result)
}

// mcpIsURL reports whether arg looks like a URL for git-annex addurl.
func mcpIsURL(arg string) bool {
	if strings.HasPrefix(arg, "magnet:") {
		return true
	}
	u, err := url.Parse(arg)
	if err != nil {
		return false
	}
	switch u.Scheme {
	case "http", "https", "ftp":
		return true
	}
	return false
}
