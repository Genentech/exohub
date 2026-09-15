package mcp

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	gomcp "github.com/mark3labs/mcp-go/mcp"
)

// deriveOrgFromOrigin extracts the org/user from the git remote origin URL.
// e.g. "git@github.com:org/repo.git" → "org"
func deriveOrgFromOrigin(repoDir string) string {
	out, err := runInDir(repoDir, "git", "config", "--get", "remote.origin.url")
	if err != nil {
		return ""
	}
	url := strings.TrimSpace(out)

	// SCP format: git@host:org/repo.git
	if strings.HasPrefix(url, "git@") {
		if idx := strings.Index(url, ":"); idx >= 0 {
			path := strings.TrimSuffix(url[idx+1:], ".git")
			parts := strings.Split(path, "/")
			if len(parts) >= 2 {
				return strings.Join(parts[:len(parts)-1], "/")
			}
		}
		return ""
	}

	// URL format: extract path between host and repo name
	// e.g. https://github.com/org/repo.git or ssh://git@host:port/org/repo.git
	path := url
	for _, prefix := range []string{"https://", "http://", "ssh://"} {
		path = strings.TrimPrefix(path, prefix)
	}
	// Remove user@host or host:port
	if idx := strings.Index(path, "/"); idx >= 0 {
		path = path[idx+1:]
	}
	path = strings.TrimSuffix(path, ".git")
	parts := strings.Split(path, "/")
	if len(parts) >= 2 {
		return strings.Join(parts[:len(parts)-1], "/")
	}
	return ""
}

type initRemoteResult struct {
	Status  string `json:"status"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Output  string `json:"output,omitempty"`
	Message string `json:"message,omitempty"`
}

func handleInitRemote(_ context.Context, request gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
	repoDir := getStringParam(request, "repo_dir")
	if repoDir == "" {
		return resultError("repo_dir is required")
	}
	if err := requireGitRepo(repoDir); err != nil {
		return resultError("%v", err)
	}

	name := getStringParam(request, "name")
	if name == "" {
		return resultError("name is required")
	}

	remoteType := getStringParam(request, "type")
	if remoteType == "" {
		return resultError("type is required")
	}

	repoAbs, err := filepath.Abs(repoDir)
	if err != nil {
		return resultError("failed to resolve repo path: %v", err)
	}

	// Build exo init remote command
	args := []string{"exo", "init", "remote", "--type", remoteType, "--name", name}

	switch remoteType {
	case "annex", "export", "artifactdb":
		s3url := getStringParam(request, "s3url")
		if s3url == "" {
			// Default to sandbox bucket: s3://exohub-sandbox-uat/sandbox/<org>/<repo>/_<type>
			repoName := filepath.Base(repoAbs)
			suffix := "_annex"
			if remoteType == "export" {
				suffix = "_export"
			} else if remoteType == "artifactdb" {
				suffix = "_catalog"
			}
			org := deriveOrgFromOrigin(repoAbs)
			if org != "" {
				s3url = fmt.Sprintf("s3://exohub-sandbox-uat/sandbox/%s/%s/%s", org, repoName, suffix)
			} else {
				s3url = fmt.Sprintf("s3://exohub-sandbox-uat/sandbox/%s/%s", repoName, suffix)
			}
		}
		args = append(args, "--s3url", s3url)
	case "exospace":
		rsyncurl := getStringParam(request, "rsyncurl")
		if rsyncurl == "" {
			return resultError("rsyncurl is required for exospace remotes")
		}
		args = append(args, "--rsyncurl", rsyncurl)
	default:
		return resultError("unsupported remote type %q: must be annex, export, artifactdb, or exospace", remoteType)
	}

	trackingBranch := getStringParam(request, "tracking_branch")
	if trackingBranch != "" {
		args = append(args, "--tracking-branch", trackingBranch)
	}

	// Grants defaults to true for S3-based remotes
	grants := true
	if v, ok := request.GetArguments()["grants"]; ok {
		if b, ok := v.(bool); ok {
			grants = b
		}
	}
	if grants && remoteType != "exospace" {
		args = append(args, "--grants")
	}

	out, err := runInDir(repoAbs, args...)
	if err != nil {
		return resultError("exo init remote failed: %s", out)
	}

	// Run exo init to ensure remote is fully configured
	initOut, _ := runInDir(repoAbs, "exo", "init", "--yes")
	out += "\n" + initOut

	return resultJSON(initRemoteResult{
		Status:  "ok",
		Name:    name,
		Type:    remoteType,
		Output:  out,
		Message: fmt.Sprintf("Remote '%s' (%s) created and saved to .exohub/remotes", name, remoteType),
	})
}
