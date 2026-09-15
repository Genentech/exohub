//go:build artifactdb

package mcp

func catalogTools() []toolEntry {
	return []toolEntry{
		{tool: catalogSearchTool, handler: handleCatalogSearch},
		{tool: catalogSchemasTool, handler: handleCatalogSchemas},
		{tool: catalogDownloadTool, handler: handleCatalogDownload},
		{tool: catalogSemanticSearchTool, handler: handleCatalogSemanticSearch},
		{tool: catalogEntitiesTool, handler: handleCatalogEntities},
	}
}
