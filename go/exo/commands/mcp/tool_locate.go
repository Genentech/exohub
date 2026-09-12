package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"strings"

	gomcp "github.com/mark3labs/mcp-go/mcp"
)

type fileLocation struct {
	File    string   `json:"file"`
	Remotes []string `json:"remotes"`
}

type locateResult struct {
	Files []fileLocation `json:"files"`
}

func handleLocate(_ context.Context, request gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
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

	args := []string{"git", "annex", "whereis", "--json"}
	paths := getStringArrayParam(request, "paths")
	args = append(args, paths...)

	out, err := runInDir(repoDir, args...)
	if err != nil && out == "" {
		return resultError("git annex whereis failed: %v", err)
	}

	result := locateResult{}
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry struct {
			File    string `json:"file"`
			Whereis []struct {
				Description string `json:"description"`
				Here        bool   `json:"here"`
			} `json:"whereis"`
			Untrusted []struct {
				Description string `json:"description"`
				Here        bool   `json:"here"`
			} `json:"untrusted"`
		}
		if json.Unmarshal([]byte(line), &entry) != nil {
			continue
		}
		if entry.File == "" {
			continue
		}
		loc := fileLocation{File: entry.File}
		for _, w := range append(entry.Whereis, entry.Untrusted...) {
			name := extractRemoteName(w.Description)
			if name != "" {
				loc.Remotes = append(loc.Remotes, name)
			} else if w.Here {
				loc.Remotes = append(loc.Remotes, "here")
			}
		}
		result.Files = append(result.Files, loc)
	}

	return resultJSON(result)
}

// extractRemoteName extracts the remote name from a git-annex description like "[remotename]" or "uuid -- description [remotename]".
func extractRemoteName(desc string) string {
	desc = strings.TrimSpace(desc)
	// Look for [name] pattern
	start := strings.LastIndex(desc, "[")
	end := strings.LastIndex(desc, "]")
	if start >= 0 && end > start {
		name := strings.TrimSpace(desc[start+1 : end])
		if name != "" && name != "here" {
			return name
		}
	}
	return ""
}
