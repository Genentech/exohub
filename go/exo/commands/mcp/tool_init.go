package mcp

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"

	gomcp "github.com/mark3labs/mcp-go/mcp"
)

type initResult struct {
	Status  string `json:"status"`
	Output  string `json:"output,omitempty"`
	Message string `json:"message,omitempty"`
}

func handleInit(_ context.Context, request gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
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

	args := []string{"init", "--yes"}

	profile := getStringParam(request, "profile")
	if profile != "" {
		args = append(args, "--profile", profile)
	}
	if name := getStringParam(request, "name"); name != "" {
		args = append(args, "--name", name)
	}
	if remote := getStringParam(request, "remote"); remote != "" {
		args = append(args, "--remote", remote)
	}
	dryRun := getBoolParam(request, "dry_run", false)
	asJSON := getBoolParam(request, "json", false)
	if dryRun {
		args = append(args, "--dry-run")
	}
	if asJSON {
		args = append(args, "--json")
	}

	if rawAnswers, ok := request.GetArguments()["answers"]; ok {
		answers, ok := rawAnswers.(map[string]any)
		if !ok {
			return resultError("answers must be an object")
		}
		keys := make([]string, 0, len(answers))
		for k := range answers {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			args = append(args, "--set", fmt.Sprintf("%s=%s", k, formatAnswerValue(answers[k])))
		}
	}

	// --dry-run --json reports remote/profile drift and exits non-zero when
	// drift is found (not a tool failure) — handle it like 'provenance
	// validate'/'doctor': trust well-formed JSON output even on a non-zero exit.
	if dryRun && asJSON {
		return runExoJSONTolerant(repoAbs, args...)
	}

	out, err := runInDir(repoAbs, append([]string{"exo"}, args...)...)
	if err != nil {
		return resultError("exo init failed: %s", out)
	}

	return resultJSON(initResult{
		Status:  "ok",
		Output:  out,
		Message: "Repository initialized — remotes from .exohub/remotes are now configured",
	})
}

// formatAnswerValue renders a JSON-decoded answer value (bool, string, or
// number) as the plain string exo init's --set key=value expects.
func formatAnswerValue(v any) string {
	switch val := v.(type) {
	case bool:
		return strconv.FormatBool(val)
	case string:
		return val
	case float64:
		return strconv.FormatFloat(val, 'f', -1, 64)
	default:
		return fmt.Sprintf("%v", val)
	}
}
