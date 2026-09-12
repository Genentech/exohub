//go:build artifactdb

package mcp

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	gomcp "github.com/mark3labs/mcp-go/mcp"
)

var catalogSchemasTool = gomcp.NewTool("catalog_schemas",
	gomcp.WithDescription(
		"List available document schemas from the ExoHub catalog. "+
			"Returns schema names that can be used to filter searches with catalog_search. "+
			"By default only returns exohub-* schemas; use the filter parameter to change the pattern. "+
			"Requires the catalog URL to be configured (EXOHUB_CATALOG_URL env, exo context, or default).",
	),
	gomcp.WithString("filter", gomcp.Description("Glob pattern to filter schema names (default: exohub-*)")),
	gomcp.WithReadOnlyHintAnnotation(true),
)

func handleCatalogSchemas(ctx context.Context, request gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
	filter := getStringParam(request, "filter")
	if filter == "" {
		filter = "exohub-*"
	}

	catalogURL, err := resolveCatalogURL()
	if err != nil {
		return resultError("failed to resolve catalog URL: %v", err)
	}

	body, statusCode, err := catalogGet(catalogURL + "/schemas")
	if err != nil {
		return resultError("failed to fetch schemas: %v", err)
	}
	if statusCode >= 400 {
		return resultError("failed to fetch schemas: %s", catalogErrorReason(body, statusCode))
	}

	var schemaResp struct {
		DocumentTypes []struct {
			Name string `json:"name"`
		} `json:"document_types"`
	}
	if err := json.Unmarshal(body, &schemaResp); err != nil {
		return resultError("failed to parse schemas response: %v", err)
	}

	seen := make(map[string]bool)
	var schemas []string
	for _, dt := range schemaResp.DocumentTypes {
		if dt.Name == "" || seen[dt.Name] {
			continue
		}
		if matchSchemaFilter(filter, dt.Name) {
			seen[dt.Name] = true
			schemas = append(schemas, dt.Name)
		}
	}
	sort.Strings(schemas)

	return resultJSON(schemas)
}

// matchSchemaFilter matches a schema name against a simple glob pattern.
// Supports * as a wildcard that matches any characters including /.
func matchSchemaFilter(pattern, name string) bool {
	if pattern == "*" {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(name, pattern[:len(pattern)-1])
	}
	return pattern == name
}
