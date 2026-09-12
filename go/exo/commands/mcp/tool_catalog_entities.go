//go:build artifactdb

package mcp

import (
	"context"
	"encoding/json"
	"net/url"

	gomcp "github.com/mark3labs/mcp-go/mcp"
)

var catalogEntitiesTool = gomcp.NewTool("catalog_entities",
	gomcp.WithDescription(
		"Retrieve entity details and related relationships from the ExoHub knowledge graph. "+
			"Entities are extracted from catalog documents (genes, diseases, pathways, datasets, etc.). "+
			"Use this to explore how a specific entity connects to other entities across the catalog. "+
			"For discovery, combine with catalog_semantic_search to find relevant entities first. "+
			"Requires authentication (run 'login' first). "+
			"Requires the catalog URL to be configured (EXOHUB_CATALOG_URL env, exo context, or default).",
	),
	gomcp.WithString("name", gomcp.Required(), gomcp.Description(
		"Entity name to look up (e.g. \"BRCA1\", \"Alzheimer's disease\", \"RNAseq\").",
	)),
	gomcp.WithBoolean("include_relationships", gomcp.Description(
		"Whether to include related relationships where this entity appears as subject or object (default: true).",
	)),
	gomcp.WithReadOnlyHintAnnotation(true),
)

func handleCatalogEntities(ctx context.Context, request gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
	name := getStringParam(request, "name")
	if name == "" {
		return resultError("parameter 'name' is required")
	}

	includeRelationships := getBoolParam(request, "include_relationships", true)

	catalogURL, err := resolveCatalogURL()
	if err != nil {
		return resultError("failed to resolve catalog URL: %v", err)
	}

	params := url.Values{}
	params.Set("name", name)
	if !includeRelationships {
		params.Set("include_relationships", "false")
	}

	entitiesURL := catalogURL + "/entities?" + params.Encode()

	body, statusCode, err := catalogGet(entitiesURL)
	if err != nil {
		return resultError("entities request failed: %v", err)
	}
	if statusCode >= 400 {
		return resultError("entities lookup failed: %s", catalogErrorReason(body, statusCode))
	}

	var result map[string]json.RawMessage
	if err := json.Unmarshal(body, &result); err != nil {
		return resultError("failed to parse entities response: %v", err)
	}

	return resultJSON(result)
}
