//go:build artifactdb

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"

	gomcp "github.com/mark3labs/mcp-go/mcp"
)

var catalogLineageTool = gomcp.NewTool("catalog_lineage",
	gomcp.WithDescription(
		"Show an artifact's recorded lineage from the ExoHub catalog (its \"lineage\" field, written by "+
			"'exo bundle' from provenance records: what generated it, its inputs, and the agents involved) "+
			"plus its direct consumers — other catalog artifacts whose lineage.inputs reference this artifact. "+
			"Complements 'provenance_show', which reads an artifact's lineage from the git repository directly "+
			"rather than from what has been published to the catalog. "+
			"Use 'catalog_search' (schema exohub-artifact/v1 or exohub-provenance/v1) to find artifact ids first. "+
			"Requires authentication (run 'login' first). "+
			"Requires the catalog URL to be configured (EXOHUB_CATALOG_URL env, exo context, or default).",
	),
	gomcp.WithString("id", gomcp.Required(), gomcp.Description(
		"Artifact ID to look up (e.g. \"myproject:file.txt@v1\"), as returned by catalog_search in _extra.id.",
	)),
	gomcp.WithNumber("consumers_size", gomcp.Description("Max number of direct consumers to return (default: 10, max: 100)")),
	gomcp.WithReadOnlyHintAnnotation(true),
)

func handleCatalogLineage(_ context.Context, request gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
	id := getStringParam(request, "id")
	if id == "" {
		return resultError("parameter 'id' is required")
	}

	size := getNumberParam(request, "consumers_size")
	if size <= 0 {
		size = 10
	}
	if size > 100 {
		size = 100
	}

	catalogURL, err := resolveCatalogURL()
	if err != nil {
		return resultError("failed to resolve catalog URL: %v", err)
	}

	metaURL := catalogURL + "/files/" + url.PathEscape(id) + "/metadata"
	body, statusCode, err := catalogGet(metaURL)
	if err != nil {
		return resultError("lineage lookup failed: %v", err)
	}
	if statusCode >= 400 {
		return resultError("lineage lookup failed: %s", catalogErrorReason(body, statusCode))
	}

	var doc struct {
		Lineage json.RawMessage `json:"lineage"`
		Extra   struct {
			ID string `json:"id"`
		} `json:"_extra"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return resultError("failed to parse artifact metadata: %v", err)
	}

	contentID := doc.Extra.ID
	if contentID == "" {
		contentID = id
	}

	params := url.Values{}
	params.Set("q", fmt.Sprintf(`lineage.inputs.id:"%s"`, contentID))
	params.Set("fields", "_extra,path,lineage")
	params.Set("size", strconv.Itoa(size))

	consumersBody, consumersStatus, err := catalogGet(catalogURL + "/search?" + params.Encode())
	if err != nil {
		return resultError("consumer search failed: %v", err)
	}
	if consumersStatus >= 400 {
		return resultError("consumer search failed: %s", catalogErrorReason(consumersBody, consumersStatus))
	}

	var consumers catalogSearchResponse
	if err := json.Unmarshal(consumersBody, &consumers); err != nil {
		return resultError("failed to parse consumer search response: %v", err)
	}

	return resultJSON(map[string]any{
		"id":              id,
		"lineage":         doc.Lineage,
		"consumers":       consumers.Results,
		"total_consumers": consumers.Total,
	})
}
