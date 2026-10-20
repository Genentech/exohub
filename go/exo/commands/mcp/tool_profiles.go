package mcp

import (
	"context"
	"encoding/json"

	gomcp "github.com/mark3labs/mcp-go/mcp"

	initcmd "github.com/Genentech/exohub/go/exo/commands/init"
)

// profilePromptResult is one entry of handleProfiles' "prompts" response,
// mirroring initcmd.PromptSpec with JSON field names matching exo init's
// --set key=value keys.
type profilePromptResult struct {
	Key      string          `json:"key"`
	Type     string          `json:"type"`
	Question string          `json:"question"`
	Choices  []string        `json:"choices,omitempty"`
	Default  json.RawMessage `json:"default,omitempty"`
}

func handleProfiles(_ context.Context, request gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
	name := getStringParam(request, "name")

	if name == "" {
		summaries, err := initcmd.ListProfiles()
		if err != nil {
			return resultError("failed to list profiles: %v", err)
		}
		return resultJSON(map[string]any{"profiles": summaries})
	}

	spec, err := initcmd.FetchProfile(name)
	if err != nil {
		return resultError("failed to fetch profile %q: %v", name, err)
	}

	prompts := make([]profilePromptResult, 0, len(spec.Prompts))
	for _, p := range spec.Prompts {
		prompts = append(prompts, profilePromptResult{
			Key:      p.Name,
			Type:     p.Kind,
			Question: p.Label,
			Choices:  p.Choices,
			Default:  p.Default,
		})
	}

	return resultJSON(map[string]any{
		"name":    spec.Name,
		"prompts": prompts,
	})
}
