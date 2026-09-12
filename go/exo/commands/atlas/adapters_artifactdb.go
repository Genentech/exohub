//go:build artifactdb

package atlas

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/spf13/viper"

	"github.com/Genentech/exohub/go/exo/internal/tui"

	"github.com/Genentech/exohub/go/exo/commands/login"
	"github.com/Genentech/exohub/go/exo/internal/download"
)

// --- HTTP auth helper ---

// setAuthHeader loads the JWT token and sets the Authorization header.
// If the token is expired and a refresh token is available, it tries a silent refresh.
func setAuthHeader(req *http.Request) error {
	tokenFile, err := login.GetTokenFile()
	if err != nil {
		return nil // No credentials available, proceed without auth
	}
	token, err := login.LoadToken(tokenFile)
	if err != nil {
		return nil // No token, proceed without auth
	}

	// Try to refresh if expired
	if !token.Valid() && token.RefreshToken != "" {
		if os.Getenv("EXO_SERVE_MODE") == "1" {
			// In serve mode: refresh JWT only, no AWS creds, no file writes
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

// doRequest performs an authenticated HTTP request.
func doRequest(method, requestURL string, body io.Reader) ([]byte, *http.Response, error) {
	req, err := http.NewRequest(method, requestURL, body)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	_ = setAuthHeader(req)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp, err
	}

	return respBody, resp, nil
}

// --- clientAdapter implements tui.Client ---

type clientAdapter struct {
	baseURL string

	// Schema cache
	schemaMu     sync.RWMutex
	schemaCache  []string
	schemaLoaded bool
}

func newClientAdapter(baseURL string) *clientAdapter {
	return &clientAdapter{baseURL: baseURL}
}

func (c *clientAdapter) Search(qs string) (chan tui.SearchResponse, chan error) {
	searchURL := c.baseURL + "/search?" + qs
	return c.searchOrScroll(searchURL)
}

func (c *clientAdapter) Scroll(scrollID string) (chan tui.SearchResponse, chan error) {
	// scrollID is a path like /search?scroll=xxx
	scrollURL := c.baseURL + scrollID
	return c.searchOrScroll(scrollURL)
}

func (c *clientAdapter) searchOrScroll(requestURL string) (chan tui.SearchResponse, chan error) {
	ch := make(chan tui.SearchResponse)
	errCh := make(chan error, 1)

	go func() {
		defer close(ch)
		defer close(errCh)

		body, resp, err := doRequest("GET", requestURL, nil)
		if err != nil {
			ch <- tui.SearchResponse{Error: err.Error()}
			return
		}

		if resp.StatusCode >= 400 {
			// Try to parse ArtifactDB error response
			var errorResp struct {
				Status string `json:"status"`
				Reason string `json:"reason"`
			}
			if json.Unmarshal(body, &errorResp) == nil && errorResp.Reason != "" {
				ch <- tui.SearchResponse{Error: errorResp.Reason}
			} else {
				ch <- tui.SearchResponse{Error: fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))}
			}
			return
		}

		var search tui.SearchResponse
		if err := json.Unmarshal(body, &search); err != nil {
			ch <- tui.SearchResponse{Error: fmt.Sprintf("failed to parse response: %v", err)}
			return
		}

		ch <- search
	}()

	errCh <- nil
	return ch, errCh
}

func (c *clientAdapter) GetFileMetadata(fileID string, tenant string, raw bool, followLink bool) (map[string]interface{}, error) {
	encodedID := url.PathEscape(fileID)
	metadataURL := c.baseURL + "/files/" + encodedID + "/metadata"
	if tenant != "" {
		metadataURL += "?tenant=" + url.QueryEscape(tenant)
	}

	body, resp, err := doRequest("GET", metadataURL, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d fetching metadata for %s", resp.StatusCode, fileID)
	}

	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, err
	}
	return data, nil
}

func (c *clientAdapter) GetSchemas() []string {
	c.schemaMu.RLock()
	if c.schemaLoaded && len(c.schemaCache) > 0 {
		defer c.schemaMu.RUnlock()
		return c.schemaCache
	}
	c.schemaMu.RUnlock()

	schemas, err := c.fetchSchemas()
	if err != nil {
		c.schemaMu.Lock()
		c.schemaCache = []string{"*"}
		c.schemaLoaded = true
		c.schemaMu.Unlock()
		return []string{"*"}
	}

	c.schemaMu.Lock()
	c.schemaCache = schemas
	c.schemaLoaded = true
	c.schemaMu.Unlock()

	return schemas
}

func (c *clientAdapter) fetchSchemas() ([]string, error) {
	schemaURL := c.baseURL + "/schemas"

	body, resp, err := doRequest("GET", schemaURL, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d fetching schemas", resp.StatusCode)
	}

	var schemaResp struct {
		DocumentTypes []struct {
			Name string `json:"name"`
		} `json:"document_types"`
	}
	if err := json.Unmarshal(body, &schemaResp); err != nil {
		return nil, err
	}

	schemaMap := make(map[string]bool)
	for _, dt := range schemaResp.DocumentTypes {
		if dt.Name != "" {
			schemaMap[dt.Name] = true
		}
	}

	schemas := make([]string, 0, len(schemaMap))
	for s := range schemaMap {
		schemas = append(schemas, s)
	}
	sort.Strings(schemas)

	return schemas, nil
}

func (c *clientAdapter) ClearSchemaCache() {
	c.schemaMu.Lock()
	defer c.schemaMu.Unlock()
	c.schemaLoaded = false
	c.schemaCache = nil
}

func (c *clientAdapter) GetProjectVersions(projectID string) ([]string, string, error) {
	versionsURL := c.baseURL + "/projects/" + url.PathEscape(projectID) + "/versions"

	body, resp, err := doRequest("GET", versionsURL, nil)
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode >= 400 {
		return nil, "", fmt.Errorf("HTTP %d fetching versions for project %s", resp.StatusCode, projectID)
	}

	var versionsResp struct {
		Aggs []struct {
			Version string `json:"_extra.version"`
		} `json:"aggs"`
		Latest struct {
			Version string `json:"_extra.version"`
		} `json:"latest"`
	}
	if err := json.Unmarshal(body, &versionsResp); err != nil {
		return nil, "", err
	}

	versions := make([]string, 0, len(versionsResp.Aggs))
	for _, agg := range versionsResp.Aggs {
		if agg.Version != "" {
			versions = append(versions, agg.Version)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(versions)))

	latest := versionsResp.Latest.Version

	return versions, latest, nil
}

// --- contextAdapter implements tui.ContextProvider ---

type contextAdapter struct {
	catalogURL string
}

func newContextAdapter(catalogURL string) *contextAdapter {
	return &contextAdapter{catalogURL: catalogURL}
}

func (a *contextAdapter) GetContext() (*tui.Context, error) {
	return &tui.Context{
		Id:   "exo",
		Name: "exo-browse",
		Url:  a.catalogURL,
	}, nil
}

func (a *contextAdapter) GetContextOrDie() *tui.Context {
	ctx, _ := a.GetContext()
	return ctx
}

func (a *contextAdapter) LoadSearchParams() (tui.PersistSearchParams, error) {
	dir, err := getSearchParamsDir()
	if err != nil {
		return tui.PersistSearchParams{}, nil
	}

	file := filepath.Join(dir, "search_params.json")
	data, err := os.ReadFile(file)
	if err != nil {
		return tui.PersistSearchParams{}, nil // No saved params is fine
	}

	var params tui.PersistSearchParams
	if err := json.Unmarshal(data, &params); err != nil {
		return tui.PersistSearchParams{}, nil
	}
	return params, nil
}

func (a *contextAdapter) SaveSearchParams(params tui.PersistSearchParams) error {
	dir, err := getSearchParamsDir()
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(params, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "search_params.json"), data, 0644)
}

func (a *contextAdapter) MakeRequest(method string, requestURL string, payload interface{}, headers map[string]string) *tui.ResponseWrapper {
	var bodyReader io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return &tui.ResponseWrapper{Err: err}
		}
		bodyReader = strings.NewReader(string(data))
	}

	body, _, err := doRequest(method, requestURL, bodyReader)
	if err != nil {
		return &tui.ResponseWrapper{Err: err}
	}

	return &tui.ResponseWrapper{Body: body}
}

// --- downloaderAdapter implements tui.Downloader ---

type downloaderAdapter struct {
	catalogURL string
}

func newDownloaderAdapter(catalogURL string) *downloaderAdapter {
	return &downloaderAdapter{catalogURL: catalogURL}
}

func (d *downloaderAdapter) DownloadFiles(ids []string, outputDir string, force bool, progress func(tui.DownloadProgress)) error {
	if os.Getenv("EXO_SERVE_MODE") == "1" {
		return d.emitBrowserDownloads(ids, progress)
	}

	dl := download.New(d.catalogURL)
	dl.Force = force
	for _, id := range ids {
		var lastReport time.Time
		_, skip, err := dl.DownloadFile(id, outputDir, func(filename string, downloaded, total int64) {
			if progress == nil {
				return
			}
			now := time.Now()
			if downloaded < total && now.Sub(lastReport) < 50*time.Millisecond {
				return
			}
			lastReport = now
			progress(tui.DownloadProgress{
				Filename:   filename,
				Downloaded: downloaded,
				Total:      total,
			})
		})
		if skip != nil && progress != nil {
			progress(tui.DownloadProgress{
				Filename: filepath.Base(id),
				Skipped:  true,
				Done:     true,
			})
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// emitBrowserDownloads resolves presigned URLs and emits them via a custom OSC
// escape sequence so the PTY bridge can forward them to the browser.
// Format: ESC ] 7 7 7 ; { "url": "...", "name": "..." } ST
func (d *downloaderAdapter) emitBrowserDownloads(ids []string, progress func(tui.DownloadProgress)) error {
	dl := download.New(d.catalogURL)
	for _, id := range ids {
		presignedURL, filename, err := dl.GetPresignedURL(id)
		if err != nil {
			return err
		}

		// Emit OSC 777 download escape sequence to stdout (the PTY)
		// OSC 777 is a custom/private-use escape — safe, won't conflict with standard sequences
		fmt.Fprintf(os.Stdout, "\x1b]777;download;%s;%s\x07", filename, presignedURL)

		if progress != nil {
			progress(tui.DownloadProgress{
				Filename: filename,
				Done:     true,
			})
		}
	}
	return nil
}

func (d *downloaderAdapter) ListProjectFiles(ctx context.Context, projectID, version string) ([]tui.FileInfo, error) {
	dl := download.New(d.catalogURL)
	files, err := dl.SearchProjectFilesWithProgress(ctx, projectID, version, nil)
	if err != nil {
		return nil, err
	}
	result := make([]tui.FileInfo, len(files))
	for i, f := range files {
		result[i] = tui.FileInfo{ID: f.ID, Path: f.Path, Size: f.Size}
	}
	return result, nil
}

func (d *downloaderAdapter) ResolveCommitFiles(commitID string) ([]tui.FileInfo, error) {
	dl := download.New(d.catalogURL)
	files, err := dl.ResolveCommitFiles(commitID)
	if err != nil {
		return nil, err
	}
	result := make([]tui.FileInfo, len(files))
	for i, f := range files {
		result[i] = tui.FileInfo{ID: f.ID, Path: f.Path, Size: f.Size}
	}
	return result, nil
}

func (d *downloaderAdapter) ResolveBundleFiles(bundleID string) ([]tui.FileInfo, int64, error) {
	return d.ResolveBundleFilesWithProgress(context.Background(), bundleID, nil)
}

func (d *downloaderAdapter) ResolveBundleFilesWithProgress(ctx context.Context, bundleID string, progress func(fetched int)) ([]tui.FileInfo, int64, error) {
	dl := download.New(d.catalogURL)
	bundle, err := dl.ResolveBundleFilesWithProgress(ctx, bundleID, progress)
	if err != nil {
		return nil, 0, err
	}
	result := make([]tui.FileInfo, len(bundle.Files))
	for i, f := range bundle.Files {
		result[i] = tui.FileInfo{ID: f.ID, Path: f.Path, Size: f.Size}
	}
	return result, bundle.TotalSize, nil
}

// --- uiConfigAdapter implements tui.UIConfigProvider ---

type uiConfigAdapter struct{}

func newUIConfigAdapter() *uiConfigAdapter {
	return &uiConfigAdapter{}
}

func (u *uiConfigAdapter) GetMainConfigFile() string {
	dir, err := getBrowseConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "config.yaml")
}

func (u *uiConfigAdapter) InitConfig() {
	// Add the contexts/exo/ui/ directory to viper search paths
	// so cli.yaml (downloaded by DownloadUIConfig) is found.
	dir, err := getBrowseConfigDir()
	if err != nil {
		return
	}
	uiDir := filepath.Join(dir, "contexts", "exo", "ui")
	viper.AddConfigPath(uiDir)
}
