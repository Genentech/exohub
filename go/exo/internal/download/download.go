package download

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"golang.org/x/sync/errgroup"

	"github.com/Genentech/exohub/go/exo/commands/login"
	"github.com/Genentech/exohub/go/exo/commandutil"
)

// ProgressFunc is called during downloads with progress updates.
type ProgressFunc func(filename string, downloaded, total int64)

// Strategy describes how to download a file based on its storage type.
type Strategy int

const (
	StrategyExport      Strategy = iota // Direct presigned URL from export remote
	StrategyAnnexSingle                 // Presigned URL from annex remote (single chunk)
	StrategyAnnexChunked                // Parallel chunk download from annex remote
)

// Location represents one storage location for an artifact.
type Location struct {
	S3URL      string      `json:"s3url"`
	S3Object   string      `json:"s3_object"`
	ExportPath string      `json:"export_path"`
	Remote     string      `json:"remote"`
	Type       string      `json:"type"` // "annex" or "export"
	Chunks     []ChunkInfo `json:"chunks"`
}

// ChunkInfo describes one chunk of a multi-chunk annex file.
type ChunkInfo struct {
	ChunkKey    string `json:"chunk_key,omitempty"`
	ChunkNumber int    `json:"chunk_number"`
	ChunkSize   int64  `json:"chunk_size"`
	S3Path      string `json:"s3_path,omitempty"`
	URL         string `json:"url,omitempty"` // presigned URL from /files/{id}?chunks=true
}

// Extra holds the _extra block from artifact metadata.
type Extra struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	Version   string `json:"version"`
	Schema    string `json:"$schema"`
	FileSize  int64  `json:"file_size"`
}

// FileMetadata is the parsed metadata response for an artifact.
type FileMetadata struct {
	Extra     Extra                  `json:"-"`
	Locations []Location             `json:"locations"`
	Path      string                 `json:"path"`
	Size      int64                  `json:"-"` // top-level "size" field
	Raw       map[string]interface{} `json:"-"`
}

// IsDownloadable returns true if the artifact can be downloaded.
// exohub-artifact: downloadable if it has locations.
// Other schemas: downloadable if _extra.file_size > 0.
func (m *FileMetadata) IsDownloadable() bool {
	if m.IsExohubArtifact() {
		return len(m.Locations) > 0
	}
	return m.Extra.FileSize > 0
}

// IsExohubArtifact returns true if the artifact uses the exohub-artifact schema.
func (m *FileMetadata) IsExohubArtifact() bool {
	return strings.HasPrefix(m.Extra.Schema, "exohub-artifact/")
}

// FileInfo is a summary of a file for listing purposes.
type FileInfo struct {
	ID       string
	Path     string
	Size     int64
	Schema   string
}

// ChunksResponse is the response from GET /files/{id}?chunks=true.
type ChunksResponse struct {
	Chunks   []ChunkInfo `json:"chunks"`
	Filename string      `json:"filename"`
	Size     int64       `json:"size"`
}

// SkipResult indicates why a file was skipped.
type SkipResult struct {
	Path   string
	Reason string
}

// Downloader downloads artifacts from an ArtifactDB catalog.
type Downloader struct {
	catalogURL string
	httpClient *http.Client
	Force      bool // Force re-download even if file exists with matching size
}

// New creates a Downloader for the given catalog URL.
func New(catalogURL string) *Downloader {
	return &Downloader{
		catalogURL: strings.TrimRight(catalogURL, "/"),
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// shouldSkip checks if the file already exists and matches the expected size.
// Returns a SkipResult if the file should be skipped, nil otherwise.
func (d *Downloader) shouldSkip(outputPath string, expectedSize int64) *SkipResult {
	if d.Force {
		return nil
	}
	info, err := os.Stat(outputPath)
	if err != nil {
		return nil // file doesn't exist
	}
	if expectedSize > 0 && info.Size() == expectedSize {
		return &SkipResult{Path: outputPath, Reason: "already exists with matching size"}
	}
	return nil
}

// setAuthHeader loads the JWT token and sets the Authorization header.
func setAuthHeader(req *http.Request) error {
	tokenFile, err := login.GetTokenFile()
	if err != nil {
		return nil
	}
	token, err := login.LoadToken(tokenFile)
	if err != nil {
		return nil
	}

	if !token.Valid() && token.RefreshToken != "" {
		if os.Getenv("EXO_SERVE_MODE") == "1" {
			refreshed, _, refreshErr := login.RefreshTokenOnly(token.RefreshToken)
			if refreshErr == nil {
				token = refreshed
			}
		} else {
			refreshed, _, refreshErr := login.RefreshAuth(token.RefreshToken)
			if refreshErr == nil {
				_ = login.SaveToken(tokenFile, refreshed)
				token = refreshed
			}
		}
	}

	if token.AccessToken != "" {
		req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	}
	return nil
}

func debugf(format string, args ...interface{}) {
	if commandutil.IsDebug() {
		log.Debugf(format, args...)
	}
}

// doRequest performs an authenticated JSON request and returns the body.
func (d *Downloader) doRequest(method, requestURL string) ([]byte, *http.Response, error) {
	debugf("%s %s", method, requestURL)

	req, err := http.NewRequest(method, requestURL, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	_ = setAuthHeader(req)

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	debugf("%s %s -> %d", method, requestURL, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp, err
	}
	return body, resp, nil
}

// GetFileMetadata fetches /files/{id}/metadata and parses it.
func (d *Downloader) GetFileMetadata(id string) (*FileMetadata, error) {
	encodedID := url.PathEscape(id)
	metaURL := d.catalogURL + "/files/" + encodedID + "/metadata"

	body, resp, err := d.doRequest("GET", metaURL)
	if err != nil {
		return nil, fmt.Errorf("fetching metadata for %s: %w", id, err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d fetching metadata for %s: %s", resp.StatusCode, id, truncate(body, 200))
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parsing metadata for %s: %w", id, err)
	}

	meta := &FileMetadata{Raw: raw}

	// Parse _extra
	if extraRaw, ok := raw["_extra"]; ok {
		extraBytes, _ := json.Marshal(extraRaw)
		_ = json.Unmarshal(extraBytes, &meta.Extra)
	}

	// Parse locations
	if locsRaw, ok := raw["locations"]; ok {
		locsBytes, _ := json.Marshal(locsRaw)
		_ = json.Unmarshal(locsBytes, &meta.Locations)
	}

	// Parse path
	if p, ok := raw["path"].(string); ok {
		meta.Path = p
	}

	// Parse top-level size (file size in bytes)
	if s, ok := raw["size"].(float64); ok {
		meta.Size = int64(s)
	}

	return meta, nil
}

// SelectLocation picks the best download strategy from available locations.
// Preference: annex multi-chunk (2+) > export > annex single/one-chunk.
// Only prefer chunked annex over export when there are multiple chunks,
// since parallel downloads only help with 2+ chunks.
func SelectLocation(locations []Location) (Strategy, *Location) {
	var annexMultiChunk, export, annexSingle *Location

	for i := range locations {
		loc := &locations[i]
		switch {
		case loc.Type == "annex" && len(loc.Chunks) > 1:
			if annexMultiChunk == nil {
				annexMultiChunk = loc
			}
		case loc.Type == "export":
			if export == nil {
				export = loc
			}
		case loc.Type == "annex":
			if annexSingle == nil {
				annexSingle = loc
			}
		}
	}

	switch {
	case annexMultiChunk != nil:
		return StrategyAnnexChunked, annexMultiChunk
	case export != nil:
		return StrategyExport, export
	case annexSingle != nil:
		return StrategyAnnexSingle, annexSingle
	default:
		return StrategyExport, nil
	}
}

// DownloadFile downloads a single artifact to outputDir.
// Returns the output path, a *SkipResult if skipped (nil if downloaded), and an error.
func (d *Downloader) DownloadFile(id, outputDir string, progress ProgressFunc) (string, *SkipResult, error) {
	log.Debug("DownloadFile", "id", id, "outputDir", outputDir)
	meta, err := d.GetFileMetadata(id)
	if err != nil {
		return "", nil, err
	}

	if !meta.IsDownloadable() {
		if meta.IsExohubArtifact() {
			return "", nil, fmt.Errorf("%s: no download locations available", id)
		}
		return "", nil, fmt.Errorf("%s is not downloadable", id)
	}

	// Preserve folder structure from artifact path
	relPath := meta.Path
	if relPath == "" {
		relPath = filepath.Base(id)
	}
	outputPath := filepath.Join(outputDir, relPath)

	// Determine file size for skip check
	fileSize := meta.Size
	if fileSize == 0 {
		fileSize = meta.Extra.FileSize
	}

	// Check if file already exists with matching size
	if skip := d.shouldSkip(outputPath, fileSize); skip != nil {
		return outputPath, skip, nil
	}

	// exohub-artifact: use location-based strategy (supports chunked downloads)
	// Other schemas: always use direct download
	if meta.IsExohubArtifact() {
		if len(meta.Locations) == 0 {
			return "", nil, fmt.Errorf("%s has no download locations", id)
		}
		strategy, loc := SelectLocation(meta.Locations)
		if loc == nil {
			return "", nil, fmt.Errorf("%s has no usable download location", id)
		}
		log.Debug("DownloadFile strategy", "id", id, "strategy", strategy, "locType", loc.Type, "s3url", loc.S3URL, "s3object", loc.S3Object)
		switch strategy {
		case StrategyAnnexChunked:
			return outputPath, nil, d.downloadChunked(id, outputPath, fileSize, progress)
		default:
			return outputPath, nil, d.downloadDirect(id, outputPath, fileSize, progress)
		}
	}

	return outputPath, nil, d.downloadDirect(id, outputPath, fileSize, progress)
}

// GetPresignedURL resolves the presigned S3 URL for a file without downloading it.
// Used in serve mode to emit the URL to the browser for direct download.
func (d *Downloader) GetPresignedURL(id string) (presignedURL, filename string, err error) {
	meta, err := d.GetFileMetadata(id)
	if err != nil {
		return "", "", err
	}

	if !meta.IsDownloadable() {
		if meta.IsExohubArtifact() {
			return "", "", fmt.Errorf("%s: no download locations available", id)
		}
		return "", "", fmt.Errorf("%s is not downloadable", id)
	}

	fname := filepath.Base(meta.Path)
	if fname == "" || fname == "." {
		fname = filepath.Base(id)
	}

	encodedID := url.PathEscape(id)
	filesURL := d.catalogURL + "/files/" + encodedID

	req, err := http.NewRequest("GET", filesURL, nil)
	if err != nil {
		return "", "", err
	}
	_ = setAuthHeader(req)

	// Don't follow redirects — capture the presigned URL from the Location header
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Timeout: 30 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("resolving presigned URL for %s: %w", id, err)
	}
	resp.Body.Close()

	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		loc := resp.Header.Get("Location")
		log.Debug("presigned URL", "id", id, "location", loc)
		return loc, fname, nil
	}

	return "", "", fmt.Errorf("expected redirect for %s, got HTTP %d", id, resp.StatusCode)
}

// downloadDirect downloads via GET /files/{id} following the presigned redirect.
func (d *Downloader) downloadDirect(id, outputPath string, totalSize int64, progress ProgressFunc) error {
	encodedID := url.PathEscape(id)
	filesURL := d.catalogURL + "/files/" + encodedID

	debugf("GET %s (download)", filesURL)

	req, err := http.NewRequest("GET", filesURL, nil)
	if err != nil {
		return err
	}
	_ = setAuthHeader(req)

	// Use a client that follows redirects (default behavior) to get the file content
	client := &http.Client{Timeout: 0} // No timeout for large downloads
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("downloading %s: %w", id, err)
	}
	defer resp.Body.Close()

	finalURL := resp.Request.URL.String()
	log.Debug("download redirect", "filesURL", filesURL, "finalURL", finalURL, "status", resp.StatusCode)

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		log.Debug("download error", "body", truncate(body, 500))
		return fmt.Errorf("HTTP %d downloading %s: %s", resp.StatusCode, id, truncate(body, 200))
	}

	if totalSize == 0 && resp.ContentLength > 0 {
		totalSize = resp.ContentLength
	}

	if err := writeToFile(outputPath, resp.Body, totalSize, filepath.Base(outputPath), progress); err != nil {
		return err
	}

	// Preserve Last-Modified timestamp from S3
	if lastMod := resp.Header.Get("Last-Modified"); lastMod != "" {
		if t, err := time.Parse(time.RFC1123, lastMod); err == nil {
			_ = os.Chtimes(outputPath, t, t)
		}
	}

	return nil
}

// downloadChunked fetches chunk URLs via /files/{id}?chunks=true and downloads in parallel.
func (d *Downloader) downloadChunked(id, outputPath string, totalSize int64, progress ProgressFunc) error {
	encodedID := url.PathEscape(id)
	chunksURL := d.catalogURL + "/files/" + encodedID + "?chunks=true"

	body, resp, err := d.doRequest("GET", chunksURL)
	if err != nil {
		return fmt.Errorf("fetching chunks for %s: %w", id, err)
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d fetching chunks for %s: %s", resp.StatusCode, id, truncate(body, 200))
	}

	var chunksResp ChunksResponse
	if err := json.Unmarshal(body, &chunksResp); err != nil {
		return fmt.Errorf("parsing chunks response for %s: %w", id, err)
	}

	if len(chunksResp.Chunks) == 0 {
		return fmt.Errorf("no chunks returned for %s", id)
	}

	for i, c := range chunksResp.Chunks {
		log.Debug("chunk", "id", id, "chunk", i, "key", c.ChunkKey, "s3path", c.S3Path, "url_prefix", truncate([]byte(c.URL), 120))
	}

	if totalSize == 0 {
		totalSize = chunksResp.Size
	}

	return downloadAndReassembleChunks(chunksResp.Chunks, outputPath, totalSize, filepath.Base(outputPath), progress)
}

// SearchProjectFiles searches for all artifacts in a project/version and returns their info.
func (d *Downloader) SearchProjectFiles(projectID, version string) ([]FileInfo, error) {
	return d.SearchProjectFilesWithProgress(context.Background(), projectID, version, nil)
}

// SearchProjectFilesWithProgress is like SearchProjectFiles but supports cancellation and progress.
func (d *Downloader) SearchProjectFilesWithProgress(ctx context.Context, projectID, version string, progress func(fetched int)) ([]FileInfo, error) {
	query := fmt.Sprintf(`_extra.project_id:"%s" AND _extra.version:"%s"`, projectID, version)
	searchURL := d.catalogURL + "/search?q=" + url.QueryEscape(query) + "&fields=_extra,path,size"

	var files []FileInfo

	currentURL := searchURL
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		body, resp, err := d.doRequest("GET", currentURL)
		if err != nil {
			return nil, fmt.Errorf("searching project files: %w", err)
		}
		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("HTTP %d searching project files: %s", resp.StatusCode, truncate(body, 200))
		}

		var result struct {
			Results []struct {
				Extra struct {
					ID     string `json:"id"`
					Schema string `json:"$schema"`
				} `json:"_extra"`
				Path string `json:"path"`
				Size int64  `json:"size"`
			} `json:"results"`
			Next string `json:"next"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return nil, fmt.Errorf("parsing search response: %w", err)
		}

		for _, r := range result.Results {
			if !strings.HasPrefix(r.Extra.Schema, "exohub-artifact/") {
				continue
			}
			files = append(files, FileInfo{
				ID:     r.Extra.ID,
				Path:   r.Path,
				Size:   r.Size,
				Schema: r.Extra.Schema,
			})
		}

		if progress != nil {
			progress(len(files))
		}

		if result.Next == "" {
			break
		}
		currentURL = d.catalogURL + result.Next
	}

	return files, nil
}

// ResolveCommitFiles fetches commit metadata and returns artifact IDs for downloadable files.
// Uses annex_files (downloadable) if present, falls back to files for backward compatibility.
// Each file path is mapped to <project>:<path>@<version>.
func (d *Downloader) ResolveCommitFiles(commitID string) ([]FileInfo, error) {
	meta, err := d.GetFileMetadata(commitID)
	if err != nil {
		return nil, fmt.Errorf("fetching commit metadata: %w", err)
	}

	// Only annex_files are downloadable. git_files are not.
	filesSlice := extractStringSlice(meta.Raw, "annex_files")
	if len(filesSlice) == 0 {
		gitFiles := extractStringSlice(meta.Raw, "git_files")
		if len(gitFiles) > 0 {
			return nil, fmt.Errorf("commit %s has only git-tracked files (available via git clone)", commitID)
		}
		return nil, fmt.Errorf("commit %s has no downloadable files", commitID)
	}

	projectID := meta.Extra.ProjectID
	version := meta.Extra.Version

	var files []FileInfo
	for _, path := range filesSlice {
		artifactID := fmt.Sprintf("%s:%s@%s", projectID, path, version)
		files = append(files, FileInfo{
			ID:   artifactID,
			Path: path,
		})
	}

	return files, nil
}

// BundleResult holds the resolved files and total size from a bundle.
type BundleResult struct {
	Files     []FileInfo
	TotalSize int64
}

// ResolveBundleFiles fetches bundle metadata and uses project search to find
// all downloadable artifacts in the project/version. Much faster than traversing
// individual commits (O(1) paginated search vs O(N) commit fetches).
func (d *Downloader) ResolveBundleFiles(bundleID string) (*BundleResult, error) {
	return d.ResolveBundleFilesWithProgress(context.Background(), bundleID, nil)
}

// ResolveBundleFilesWithProgress is like ResolveBundleFiles but supports cancellation and progress.
func (d *Downloader) ResolveBundleFilesWithProgress(ctx context.Context, bundleID string, progress func(fetched int)) (*BundleResult, error) {
	meta, err := d.GetFileMetadata(bundleID)
	if err != nil {
		return nil, fmt.Errorf("fetching bundle metadata: %w", err)
	}

	projectID := meta.Extra.ProjectID
	version := meta.Extra.Version

	if projectID == "" || version == "" {
		return nil, fmt.Errorf("bundle %s missing project_id or version", bundleID)
	}

	// Get total_size from bundle metadata
	var totalSize int64
	if s, ok := meta.Raw["total_size"].(float64); ok {
		totalSize = int64(s)
	}

	files, err := d.SearchProjectFilesWithProgress(ctx, projectID, version, progress)
	if err != nil {
		return nil, fmt.Errorf("searching project files: %w", err)
	}

	if len(files) == 0 {
		return nil, fmt.Errorf("bundle %s has no downloadable files", bundleID)
	}

	return &BundleResult{Files: files, TotalSize: totalSize}, nil
}

// extractStringSlice extracts a []string from a map field, handling JSON's []interface{}.
func extractStringSlice(raw map[string]interface{}, key string) []string {
	v, ok := raw[key]
	if !ok {
		return nil
	}
	arr, ok := v.([]interface{})
	if !ok {
		return nil
	}
	var result []string
	for _, item := range arr {
		if s, ok := item.(string); ok {
			result = append(result, s)
		}
	}
	return result
}

const fileConcurrency = 4

// DownloadResult holds the outcome of a single file download.
type DownloadResult struct {
	Index int
	Path  string
	Skip  *SkipResult
	Err   error
}

// DownloadProject downloads all artifact files in a project/version in parallel.
func (d *Downloader) DownloadProject(projectID, version, outputDir string, progress ProgressFunc) error {
	files, err := d.SearchProjectFiles(projectID, version)
	if err != nil {
		return err
	}

	if len(files) == 0 {
		return fmt.Errorf("no artifact files found for %s@%s", projectID, version)
	}

	// Channel to receive results in completion order
	results := make(chan DownloadResult, len(files))

	// Print results as they arrive (in a separate goroutine)
	var printWg sync.WaitGroup
	var skipped int
	printWg.Add(1)
	go func() {
		defer printWg.Done()
		for r := range results {
			f := files[r.Index]
			if r.Skip != nil {
				skipped++
				fmt.Printf("[%d/%d] %s (skipped: %s)\n", r.Index+1, len(files), f.Path, r.Skip.Reason)
			}
		}
	}()

	// Download files in parallel
	g, ctx := errgroup.WithContext(context.Background())
	g.SetLimit(fileConcurrency)

	for i, f := range files {
		g.Go(func() error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			_, skip, dlErr := d.DownloadFile(f.ID, outputDir, progress)
			results <- DownloadResult{Index: i, Path: f.Path, Skip: skip, Err: dlErr}
			// Don't return errors — individual file failures shouldn't cancel the batch
			return nil
		})
	}

	_ = g.Wait()
	close(results)
	printWg.Wait()

	if skipped > 0 {
		fmt.Printf("%d of %d files skipped (already downloaded)\n", skipped, len(files))
	}

	return nil
}

// DownloadCommit resolves annex_files from a commit and downloads them all.
func (d *Downloader) DownloadCommit(commitID, outputDir string, progress ProgressFunc) error {
	files, err := d.ResolveCommitFiles(commitID)
	if err != nil {
		return err
	}
	return d.downloadFiles(files, outputDir, progress)
}

// DownloadBundle resolves all files across commits in a bundle and downloads them.
func (d *Downloader) DownloadBundle(bundleID, outputDir string, progress ProgressFunc) error {
	bundle, err := d.ResolveBundleFiles(bundleID)
	if err != nil {
		return err
	}
	return d.downloadFiles(bundle.Files, outputDir, progress)
}

// downloadFiles downloads a list of files in parallel.
func (d *Downloader) downloadFiles(files []FileInfo, outputDir string, progress ProgressFunc) error {
	if len(files) == 0 {
		return fmt.Errorf("no files to download")
	}

	results := make(chan DownloadResult, len(files))

	var printWg sync.WaitGroup
	var skipped int
	printWg.Add(1)
	go func() {
		defer printWg.Done()
		for r := range results {
			f := files[r.Index]
			if r.Skip != nil {
				skipped++
				fmt.Printf("[%d/%d] %s (skipped: %s)\n", r.Index+1, len(files), f.Path, r.Skip.Reason)
			}
		}
	}()

	g, ctx := errgroup.WithContext(context.Background())
	g.SetLimit(fileConcurrency)

	for i, f := range files {
		g.Go(func() error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			_, skip, dlErr := d.DownloadFile(f.ID, outputDir, progress)
			results <- DownloadResult{Index: i, Path: f.Path, Skip: skip, Err: dlErr}
			return nil
		})
	}

	_ = g.Wait()
	close(results)
	printWg.Wait()

	if skipped > 0 {
		fmt.Printf("%d of %d files skipped (already downloaded)\n", skipped, len(files))
	}

	return nil
}

// writeToFile streams body to a file with progress reporting.
func writeToFile(outputPath string, body io.Reader, totalSize int64, filename string, progress ProgressFunc) error {
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return err
	}

	f, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer f.Close()

	if progress == nil {
		_, err = io.Copy(f, body)
		return err
	}

	buf := make([]byte, 32*1024)
	var downloaded int64
	for {
		n, readErr := body.Read(buf)
		if n > 0 {
			if _, writeErr := f.Write(buf[:n]); writeErr != nil {
				return writeErr
			}
			downloaded += int64(n)
			progress(filename, downloaded, totalSize)
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return readErr
		}
	}

	return nil
}

func truncate(b []byte, max int) string {
	s := strings.TrimSpace(string(b))
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}
