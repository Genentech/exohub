package tui

import "context"

// Client provides the API calls needed by the TUI.
type Client interface {
	// Search performs a search with the given query string and returns results
	// via channels. The response channel receives SearchResponse pages. The
	// error channel receives at most one error (or nil on success).
	Search(qs string) (chan SearchResponse, chan error)

	// Scroll fetches the next page of results using a scroll token.
	Scroll(scrollID string) (chan SearchResponse, chan error)

	// GetFileMetadata retrieves the full metadata for a document.
	// tenant is optional: when non-empty, it is passed as ?tenant= to set context
	// on multi-tenant instances.
	GetFileMetadata(fileID string, tenant string, raw bool, followLink bool) (map[string]interface{}, error)

	// GetSchemas returns cached schemas; call ClearSchemaCache first if needed.
	GetSchemas() []string

	// ClearSchemaCache forces re-fetching schemas on next GetSchemas call.
	ClearSchemaCache()

	// GetProjectVersions returns available versions for a project, sorted descending,
	// along with the latest version string (empty if unknown).
	GetProjectVersions(projectID string) (versions []string, latest string, err error)
}

// ContextProvider provides context and search state persistence.
type ContextProvider interface {
	// GetContext returns the current ArtifactDB context.
	GetContext() (*Context, error)

	// GetContextOrDie returns the current context or exits on failure.
	GetContextOrDie() *Context

	// LoadSearchParams loads persisted search parameters.
	LoadSearchParams() (PersistSearchParams, error)

	// SaveSearchParams persists search parameters.
	SaveSearchParams(params PersistSearchParams) error

	// MakeRequest performs an authenticated HTTP request.
	MakeRequest(method string, url string, payload interface{}, headers map[string]string) *ResponseWrapper
}

// DownloadProgress reports progress for a single file download.
type DownloadProgress struct {
	Filename   string
	Downloaded int64
	Total      int64
	Done       bool
	Skipped    bool
	Err        error
}

// FileInfo describes a file in a project for listing purposes.
type FileInfo struct {
	ID   string
	Path string
	Size int64
}

// Downloader handles file downloads from the catalog.
// This interface is optional — if not provided, the download feature is disabled.
type Downloader interface {
	// DownloadFiles downloads artifacts by ID to the output directory.
	// If force is true, re-download even if files already exist with matching size.
	// Progress is reported via the callback, which may be called from a goroutine.
	DownloadFiles(ids []string, outputDir string, force bool, progress func(DownloadProgress)) error

	// ListProjectFiles returns all artifact files in a project/version.
	ListProjectFiles(ctx context.Context, projectID, version string) ([]FileInfo, error)

	// ResolveCommitFiles returns artifact IDs for all files in a commit document.
	ResolveCommitFiles(commitID string) ([]FileInfo, error)

	// ResolveBundleFiles returns deduplicated artifact IDs and total size for all files across all commits in a bundle.
	ResolveBundleFiles(bundleID string) ([]FileInfo, int64, error)

	// ResolveBundleFilesWithProgress is like ResolveBundleFiles but supports cancellation and progress reporting.
	ResolveBundleFilesWithProgress(ctx context.Context, bundleID string, progress func(fetched int)) ([]FileInfo, int64, error)
}

// UIConfigProvider provides instance-specific UI configuration.
type UIConfigProvider interface {
	// InitConfig initializes the base configuration.
	InitConfig()

	// GetMainConfigFile returns the path to the main config file.
	GetMainConfigFile() string
}
