//go:build artifactdb

package mcp

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	gomcp "github.com/mark3labs/mcp-go/mcp"
)

var catalogSemanticSearchTool = gomcp.NewTool("catalog_semantic_search",
	gomcp.WithDescription(
		"Perform a semantic (natural language) search over the ExoHub catalog using vector similarity. "+
			"Unlike catalog_search which does full-text keyword matching, this tool embeds the query "+
			"and retrieves semantically related chunks, entities, and relationships from the catalog. "+
			"Use this to answer questions like 'datasets related to neurodegeneration' or 'find BRCA1 studies'. "+
			"Requires authentication (run 'login' first). "+
			"Requires the catalog URL to be configured (EXOHUB_CATALOG_URL env, exo context, or default).",
	),
	gomcp.WithString("q", gomcp.Required(), gomcp.Description(
		"Natural language query (e.g. \"datasets related to cell proliferation\", \"BRCA1 gene expression studies\"). "+
			"The query is embedded and used for vector similarity search.",
	)),
	gomcp.WithArray("types", gomcp.Description(
		"Result types to include. Allowed values: chunks, entities, relationships. "+
			"Defaults to all types when omitted.",
	), gomcp.WithStringItems()),
	gomcp.WithString("project", gomcp.Description("Filter results by project ID.")),
	gomcp.WithNumber("limit", gomcp.Description("Maximum number of results per type (default: 10, max: 100)")),
	gomcp.WithReadOnlyHintAnnotation(true),
)

func handleCatalogSemanticSearch(ctx context.Context, request gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
	q := getStringParam(request, "q")
	if q == "" {
		return resultError("parameter 'q' is required")
	}

	types := getStringArrayParam(request, "types")
	project := getStringParam(request, "project")
	limit := getNumberParam(request, "limit")

	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}

	catalogURL, err := resolveCatalogURL()
	if err != nil {
		return resultError("failed to resolve catalog URL: %v", err)
	}

	params := url.Values{}
	params.Set("q", q)
	params.Set("limit", strconv.Itoa(limit))
	if len(types) > 0 {
		params.Set("types", strings.Join(types, ","))
	}
	if project != "" {
		params.Set("project_id", project)
	}

	searchURL := catalogURL + "/semantic-search?" + params.Encode()

	body, statusCode, err := catalogGet(searchURL)
	if err != nil {
		return resultError("semantic search request failed: %v", err)
	}
	if statusCode >= 400 {
		return resultError("semantic search failed: %s", catalogErrorReason(body, statusCode))
	}

	var result map[string]json.RawMessage
	if err := json.Unmarshal(body, &result); err != nil {
		return resultError("failed to parse semantic search response: %v", err)
	}

	return resultJSON(result)
}
