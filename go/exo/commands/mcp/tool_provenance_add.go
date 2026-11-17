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
	if planRepository := getStringParam(request, "plan_repository"); planRepository != "" {
		args = append(args, "--plan-repository", planRepository)
	}
	if planCommit := getStringParam(request, "plan_commit"); planCommit != "" {
		args = append(args, "--plan-commit", planCommit)
	}
	if planURL := getStringParam(request, "plan_url"); planURL != "" {
		args = append(args, "--plan-url", planURL)
	}
	for _, p := range getStringArrayParam(request, "param") {
		args = append(args, "--param", p)
	}
	for _, r := range getStringArrayParam(request, "role") {
		args = append(args, "--role", r)
	}
	for _, r := range getStringArrayParam(request, "output_role") {
		args = append(args, "--output-role", r)
	}
	for _, r := range getStringArrayParam(request, "result") {
		args = append(args, "--result", r)
	}
	for _, e := range getStringArrayParam(request, "environment") {
		args = append(args, "--environment", e)
	}
	if source := getStringParam(request, "source"); source != "" {
		args = append(args, "--source", source)
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
