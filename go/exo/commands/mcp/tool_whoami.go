package mcp

import (
	"context"

	gomcp "github.com/mark3labs/mcp-go/mcp"

	"github.com/Genentech/exohub/go/exo/commands/whoami"
)

func handleWhoami(_ context.Context, request gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
	includeAWS := false
	if v, ok := request.GetArguments()["aws"]; ok {
		if b, ok := v.(bool); ok {
			includeAWS = b
		}
	}

	result, err := whoami.Run(includeAWS)
	if err != nil {
		return resultError("%v", err)
	}
	return resultJSON(result)
}
