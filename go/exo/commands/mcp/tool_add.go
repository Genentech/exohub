package mcp

import (
	"context"
	"net/url"
	"os"
	"strings"

	addcmd "github.com/Genentech/exohub/go/exo/commands/add"
	"github.com/Genentech/exohub/go/exo/internal/exohub"
	gomcp "github.com/mark3labs/mcp-go/mcp"
)

type addResult struct {
	GitFiles       []string          `json:"git_files,omitempty"`
	AnnexFiles     []string          `json:"annex_files,omitempty"`
	AddURLFiles    []string          `json:"addurl_files,omitempty"`
	ExohubRefs     []string          `json:"exohub_refs,omitempty"`
	Reasons        map[string]string `json:"reasons,omitempty"`
	SkippedDeleted []string          `json:"skipped_deleted,omitempty"`
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

	var exohubRefs, otherPaths []string
	for _, p := range paths {
		if exohub.IsReference(p) {
			exohubRefs = append(exohubRefs, p)
		} else {
			otherPaths = append(otherPaths, p)
		}
	}
	if len(exohubRefs) > 0 {
		if len(otherPaths) > 0 {
			return resultError("exohub: references cannot be combined with local paths or URLs in the same call")
		}
		return handleExohubAdd(repoDir, request, exohubRefs)
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

	// Categorize paths for the response based on what we know about the inputs
	var urlPaths, localPaths []string
	for _, p := range paths {
		if mcpIsURL(p) {
			urlPaths = append(urlPaths, p)
		} else {
			localPaths = append(localPaths, p)
		}
	}

	var plan []addcmd.PlannedFile
	if mode == "auto" && len(localPaths) > 0 {
		plan, err = addcmd.PlanAutoAdd(repoDir, localPaths, "")
		if err != nil {
			return resultError("cannot plan exo add: %v", err)
		}
	}
	out, err := runInDir(repoDir, args...)
	if err != nil {
		return resultError("exo add failed: %s", out)
	}

	result := addResult{AddURLFiles: urlPaths}
	switch mode {
	case "annex":
		result.AnnexFiles = localPaths
	case "git":
		result.GitFiles = localPaths
	default:
		applyAutoPlan(&result, plan)
	}

	return resultJSON(result)
}

// handleExohubAdd runs `exo add exohub:...` for refs, a path category
// mcpIsURL/PlanAutoAdd cannot classify (they plan local filesystem routing,
// not an exohub: reference's remote resolution): refs are passed straight
// through to the CLI, which already supports them (task 5.3 of
// openspec/changes/add-exohub-asset-import).
func handleExohubAdd(repoDir string, request gomcp.CallToolRequest, refs []string) (*gomcp.CallToolResult, error) {
	exoPath, err := os.Executable()
	if err != nil {
		return resultError("cannot find exo binary: %v", err)
	}

	args := exohubAddArgs(exoPath, refs, getStringParam(request, "to"), getStringParam(request, "repo"), getBoolParam(request, "verify", false))
	out, err := runInDir(repoDir, args...)
	if err != nil {
		return resultError("exo add failed: %s", out)
	}
	return resultJSON(addResult{ExohubRefs: refs})
}

// exohubAddArgs builds the `exo add` argv for refs, forwarding the
// `to`/`repo`/`verify` MCP parameters as their CLI flag equivalents.
func exohubAddArgs(exoPath string, refs []string, to, repo string, verify bool) []string {
	args := []string{exoPath, "add"}
	args = append(args, refs...)
	if to != "" {
		args = append(args, "--to", to)
	}
	if repo != "" {
		args = append(args, "--repo", repo)
	}
	if verify {
		args = append(args, "--verify")
	}
	return args
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

func applyAutoPlan(result *addResult, plan []addcmd.PlannedFile) {
	result.Reasons = make(map[string]string, len(plan))
	for _, file := range plan {
		// A tracked file removed from disk (issue #143) is not routed to
		// either backend: its deletion is left unstaged, so it must not be
		// reported as added to git or annex.
		if file.DeletedFromDisk {
			result.SkippedDeleted = append(result.SkippedDeleted, file.Path)
			continue
		}
		if file.Backend == addcmd.Annex {
			result.AnnexFiles = append(result.AnnexFiles, file.Path)
		} else {
			result.GitFiles = append(result.GitFiles, file.Path)
		}
		result.Reasons[file.Path] = file.Reason
	}
}
