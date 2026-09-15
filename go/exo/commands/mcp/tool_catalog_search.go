//go:build artifactdb

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	gomcp "github.com/mark3labs/mcp-go/mcp"
)

var catalogSearchTool = gomcp.NewTool("catalog_search",
	gomcp.WithDescription(
		"Search the ExoHub catalog for datasets and artifacts. "+
			"Supports full-text search and field-specific queries using dotfield notation "+
			"(e.g. _extra.project_id:\"myproject\" or path:*.parquet). "+
			"Use catalog_schemas to discover available schema names for the schema filter. "+
			"Requires the catalog URL to be configured (EXOHUB_CATALOG_URL env, exo context, or default). "+
			"Requires authentication (run 'login' first).",
	),
	gomcp.WithString("q", gomcp.Required(), gomcp.Description(
		"Search query. Use * for all results. "+
			"Supports dotfield notation for field-specific queries (e.g. _extra.project_id:\"myproject\", path:*.parquet). "+
			"Multiple terms can be joined with AND/OR.",
	)),
	gomcp.WithString("schema", gomcp.Description("Filter by schema name (e.g. exohub-artifact/v1). Adds _extra.$schema filter to the query.")),
	gomcp.WithString("project", gomcp.Description("Filter by project ID. Adds _extra.project_id filter to the query.")),
	gomcp.WithBoolean("latest", gomcp.Description("Only return latest versions (default: true)")),
	gomcp.WithString("fields", gomcp.Description("Comma-separated response fields (default: _extra,path)")),
	gomcp.WithString("sort", gomcp.Description("Sort order (default: -_extra.uploaded). Prefix with - for descending.")),
	gomcp.WithNumber("size", gomcp.Description("Number of results per page (default: 10, max: 100)")),
	gomcp.WithReadOnlyHintAnnotation(true),
)

func handleCatalogSearch(ctx context.Context, request gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
	q := getStringParam(request, "q")
	if q == "" {
		return resultError("parameter 'q' is required")
	}

	schema := getStringParam(request, "schema")
	project := getStringParam(request, "project")
	fields := getStringParam(request, "fields")
	sortParam := getStringParam(request, "sort")
	size := getNumberParam(request, "size")
	latest := getBoolParam(request, "latest", true)

	if fields == "" {
		fields = "_extra,path"
	}
	if sortParam == "" {
		sortParam = "-_extra.uploaded"
	}
	if size <= 0 {
		size = 10
	}
	if size > 100 {
		size = 100
	}

	var filters []string
	if schema != "" {
		filters = append(filters, fmt.Sprintf("_extra.$schema:\"%s\"", schema))
	}
	if project != "" {
		filters = append(filters, fmt.Sprintf("_extra.project_id:\"%s\"", project))
	}

	queryParts := append(filters, q)
	fullQuery := strings.Join(queryParts, " AND ")

	catalogURL, err := resolveCatalogURL()
	if err != nil {
		return resultError("failed to resolve catalog URL: %v", err)
	}

	params := url.Values{}
	params.Set("q", fullQuery)
	params.Set("fields", fields)
	params.Set("sort", sortParam)
	params.Set("size", strconv.Itoa(size))
	params.Set("latest", strconv.FormatBool(latest))

	searchURL := catalogURL + "/search?" + params.Encode()

	body, statusCode, err := catalogGet(searchURL)
	if err != nil {
		return resultError("search request failed: %v", err)
	}
	if statusCode >= 400 {
		return resultError("search failed: %s", catalogErrorReason(body, statusCode))
	}

	var resp catalogSearchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return resultError("failed to parse search response: %v", err)
	}

	result := map[string]any{
		"total":   resp.Total,
		"count":   resp.Count,
		"results": resp.Results,
	}

	return resultJSON(result)
}

func getNumberParam(request gomcp.CallToolRequest, key string) int {
	args := request.GetArguments()
	v, ok := args[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	}
	return 0
}

func getBoolParam(request gomcp.CallToolRequest, key string, defaultVal bool) bool {
	args := request.GetArguments()
	v, ok := args[key]
	if !ok {
		return defaultVal
	}
	if b, ok := v.(bool); ok {
		return b
	}
	return defaultVal
}
