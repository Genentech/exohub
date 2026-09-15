//go:build artifactdb

package mcp

import (
	"context"
	"fmt"
	"strings"
	"sync"

	gomcp "github.com/mark3labs/mcp-go/mcp"

	"github.com/Genentech/exohub/go/exo/internal/download"
)

var catalogDownloadTool = gomcp.NewTool("catalog_download",
	gomcp.WithDescription(
		"Download data files (annexed/large files) from the ExoHub catalog. "+
			"This downloads the actual data content stored in the catalog, not files tracked by git. "+
			"Accepts a single artifact ID (e.g. \"myproject:file.txt@v1\"), "+
			"a project@version (e.g. \"myproject@v1\" to download all data files), "+
			"a bundle ID, or a commit ID. "+
			"Bundles and commits are auto-detected from their schema. "+
			"Requires authentication (run 'login' first). "+
			"Requires the catalog URL to be configured (EXOHUB_CATALOG_URL env, exo context, or default).",
	),
	gomcp.WithString("id", gomcp.Required(), gomcp.Description(
		"Artifact ID, project@version, bundle ID, or commit ID to download. "+
			"Examples: \"myproject:file.txt@v1\" (single file), \"myproject@v1\" (all project files), "+
			"\"myproject:bundle.json@v1\" (bundle), \"myproject:commit.json@v1\" (commit).",
	)),
	gomcp.WithString("output_dir", gomcp.Description("Output directory for downloaded files (default: current directory)")),
	gomcp.WithBoolean("force", gomcp.Description("Re-download even if file already exists with matching size (default: false)")),
)

func handleCatalogDownload(ctx context.Context, request gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
	id := getStringParam(request, "id")
	if id == "" {
		return resultError("parameter 'id' is required")
	}

	outputDir := getStringParam(request, "output_dir")
	if outputDir == "" {
		outputDir = "."
	}

	force := getBoolParam(request, "force", false)

	catalogURL, err := resolveCatalogURL()
	if err != nil {
		return resultError("failed to resolve catalog URL: %v", err)
	}

	dl := download.New(catalogURL)
	dl.Force = force

	var mu sync.Mutex
	var files []map[string]any

	progress := func(filename string, downloaded, total int64) {
		// no-op for MCP — progress is not streamed
	}

	if isProjectVersion(id) {
		parts := strings.SplitN(id, "@", 2)
		projectID := parts[0]
		version := parts[1]

		listing, err := dl.SearchProjectFiles(projectID, version)
		if err != nil {
			return resultError("failed to list project files: %v", err)
		}
		if len(listing) == 0 {
			return resultError("no artifact files found for %s@%s", projectID, version)
		}

		err = dl.DownloadProject(projectID, version, outputDir, func(filename string, downloaded, total int64) {
			if total > 0 && downloaded >= total {
				mu.Lock()
				files = append(files, map[string]any{"file": filename, "size": total})
				mu.Unlock()
			}
		})
		if err != nil {
			return resultError("download failed: %v", err)
		}

		return resultJSON(map[string]any{
			"status":     "ok",
			"type":       "project",
			"project":    parts[0],
			"version":    parts[1],
			"files":      files,
			"file_count": len(listing),
			"output_dir": outputDir,
		})
	}

	meta, metaErr := dl.GetFileMetadata(id)
	if metaErr == nil && strings.HasPrefix(meta.Extra.Schema, "exohub-bundle/") {
		err := dl.DownloadBundle(id, outputDir, progress)
		if err != nil {
			return resultError("bundle download failed: %v", err)
		}
		return resultJSON(map[string]any{
			"status":     "ok",
			"type":       "bundle",
			"id":         id,
			"output_dir": outputDir,
		})
	}

	if metaErr == nil && strings.HasPrefix(meta.Extra.Schema, "exohub-commit/") {
		err := dl.DownloadCommit(id, outputDir, progress)
		if err != nil {
			return resultError("commit download failed: %v", err)
		}
		return resultJSON(map[string]any{
			"status":     "ok",
			"type":       "commit",
			"id":         id,
			"output_dir": outputDir,
		})
	}

	outputPath, skip, err := dl.DownloadFile(id, outputDir, progress)
	if err != nil {
		return resultError("download failed: %v", err)
	}
	if skip != nil {
		return resultJSON(map[string]any{
			"status":  "skipped",
			"file":    outputPath,
			"reason":  skip.Reason,
			"message": fmt.Sprintf("Skipped %s: %s", outputPath, skip.Reason),
		})
	}
	return resultJSON(map[string]any{
		"status":     "ok",
		"type":       "file",
		"file":       outputPath,
		"output_dir": outputDir,
	})
}

func isProjectVersion(arg string) bool {
	atIdx := strings.Index(arg, "@")
	if atIdx < 0 {
		return false
	}
	return !strings.Contains(arg[:atIdx], ":")
}
