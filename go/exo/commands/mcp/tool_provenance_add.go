package mcp

import (
	"context"
	"path/filepath"
	"strings"

	gomcp "github.com/mark3labs/mcp-go/mcp"
)

func handleProvenanceAdd(ctx context.Context, request gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
	repoDir := getStringParam(request, "repo_dir")
	if repoDir == "" {
		return resultError("repo_dir is required")
	}
	if err := requireGitRepo(repoDir); err != nil {
		return resultError("%v", err)
	}

	generated := getStringArrayParam(request, "generated")
	if len(generated) == 0 {
		return resultError("generated is required")
	}

	repoAbs, err := filepath.Abs(repoDir)
	if err != nil {
		return resultError("failed to resolve repo path: %v", err)
	}

	args := []string{"provenance", "add", "--json"}
	for _, g := range generated {
		args = append(args, "--generated", g)
	}
	for _, u := range getStringArrayParam(request, "used") {
		args = append(args, "--used", u)
	}
	for _, d := range getStringArrayParam(request, "derived_from") {
		args = append(args, "--derived-from", d)
	}
	if getBoolParam(request, "no_derived_from", false) {
		args = append(args, "--no-derived-from")
	}
	for _, r := range getStringArrayParam(request, "revision_of") {
		args = append(args, "--revision-of", r)
	}
	if plan := getStringParam(request, "plan"); plan != "" {
		args = append(args, "--plan", plan)
	}

	// Default agent to this MCP client as a software agent, unless the
	// caller gave one explicitly. exo provenance add already defaults
	// --on-behalf-of to the logged-in user when --agent is software:<name>
	// and --on-behalf-of is omitted, so there is nothing more to resolve
	// here.
	agent := getStringParam(request, "agent")
	if agent == "" {
		agent = defaultSoftwareAgent(ctx)
	}
	if agent != "" {
		args = append(args, "--agent", agent)
	}
	if onBehalfOf := getStringParam(request, "on_behalf_of"); onBehalfOf != "" {
		args = append(args, "--on-behalf-of", onBehalfOf)
	}
	if model := getStringParam(request, "model"); model != "" {
		args = append(args, "--model", model)
	}

	if started := getStringParam(request, "started"); started != "" {
		args = append(args, "--started", started)
	}
	if ended := getStringParam(request, "ended"); ended != "" {
		args = append(args, "--ended", ended)
	}
	if assertion := getStringParam(request, "assertion"); assertion != "" {
		args = append(args, "--assertion", assertion)
	}
	if evidence := getStringParam(request, "evidence"); evidence != "" {
		args = append(args, "--evidence", evidence)
	}
	for _, s := range getStringArrayParam(request, "supersedes") {
		args = append(args, "--supersedes", s)
	}
	if getBoolParam(request, "skip_existing", false) {
		args = append(args, "--skip-existing")
	}
	if getBoolParam(request, "dry_run", false) {
		args = append(args, "--dry-run")
	}

	out, err := runInDir(repoAbs, append([]string{"exo"}, args...)...)
	if err != nil {
		return resultError("exo provenance add failed: %s", strings.TrimSpace(out))
	}
	return jsonOrText(out)
}
