package mcp

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	gomcp "github.com/mark3labs/mcp-go/mcp"

	"github.com/Genentech/exohub/go/exo/internal/defaults"
)

const (
	docsFetchTimeout = 10 * time.Second
	docsMaxBodyBytes = 2 * 1024 * 1024 // 2MB
	docsMaxRedirects = 5
)

func handleDocs(ctx context.Context, request gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
	base := defaults.DocsURL()
	if base == "" {
		return resultError("no docs URL is configured")
	}

	page := getStringParam(request, "page")
	target, err := resolveDocsURL(base, page)
	if err != nil {
		return resultError("%v", err)
	}

	body, err := fetchDocs(ctx, target, base)
	if err != nil {
		return resultJSON(map[string]string{
			"url":   target,
			"error": err.Error(),
		})
	}

	return gomcp.NewToolResultText(body), nil
}

// docsBaseDir returns the directory portion of a docs base URL's path: the
// path up to and including the last "/". For "/exohub/llms.txt" this is
// "/exohub/"; for "/llms.txt" (or no path) this is "/".
func docsBaseDir(base *url.URL) string {
	if idx := strings.LastIndex(base.Path, "/"); idx >= 0 {
		return base.Path[:idx+1]
	}
	return "/"
}

// validateDocsURL ensures resolved stays strictly under base: same scheme
// and host, no embedded userinfo, no query/fragment, and a cleaned path
// that falls within the base's directory (no "../" escapes, no absolute
// paths pointing elsewhere on the same host).
func validateDocsURL(resolved, base *url.URL) error {
	if resolved.Scheme != base.Scheme || resolved.Host != base.Host {
		return fmt.Errorf("URL is outside the configured docs base: %s", resolved)
	}
	if resolved.User != nil {
		return fmt.Errorf("URL must not contain userinfo: %s", resolved)
	}
	if resolved.RawQuery != "" || resolved.Fragment != "" {
		return fmt.Errorf("URL must not contain a query or fragment: %s", resolved)
	}

	baseDir := docsBaseDir(base)
	baseDirClean := strings.TrimSuffix(baseDir, "/")
	cleaned := path.Clean(resolved.Path)
	if cleaned != baseDirClean && !strings.HasPrefix(cleaned, baseDir) {
		return fmt.Errorf("URL path %q escapes the docs base directory %q", resolved.Path, baseDir)
	}

	return nil
}

// resolveDocsURL resolves page against the docs base URL and ensures the
// result stays strictly under that base (same scheme, host, and base
// directory), rejecting path traversal, encoded slashes/backslashes,
// embedded userinfo, and anything else that would escape the docs tree.
func resolveDocsURL(base, page string) (string, error) {
	baseURL, err := url.Parse(base)
	if err != nil {
		return "", err
	}

	if page == "" {
		return base, nil
	}

	lowerPage := strings.ToLower(page)
	if strings.ContainsAny(page, "\\") || strings.Contains(lowerPage, "%2f") || strings.Contains(lowerPage, "%5c") {
		return "", fmt.Errorf("page must not contain backslashes or encoded slashes: %s", page)
	}

	pageURL, err := url.Parse(page)
	if err != nil {
		return "", err
	}

	resolved := baseURL.ResolveReference(pageURL)
	if err := validateDocsURL(resolved, baseURL); err != nil {
		return "", err
	}

	// Normalize: use the cleaned, decoded path and drop any raw-encoding
	// artifacts, userinfo, query, and fragment so what we fetch is exactly
	// what we validated.
	resolved.Path = path.Clean(resolved.Path)
	resolved.RawPath = ""
	resolved.User = nil
	resolved.RawQuery = ""
	resolved.Fragment = ""

	return resolved.String(), nil
}

// fetchDocs fetches target with a timeout, a size cap (returning an error
// rather than silently truncating), a content-type check, and redirect
// validation that confines every hop to the docs base.
func fetchDocs(ctx context.Context, target, base string) (string, error) {
	baseURL, err := url.Parse(base)
	if err != nil {
		return "", err
	}

	reqCtx, cancel := context.WithTimeout(ctx, docsFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, target, nil)
	if err != nil {
		return "", err
	}

	client := &http.Client{
		Timeout: docsFetchTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= docsMaxRedirects {
				return fmt.Errorf("stopped after %d redirects", docsMaxRedirects)
			}
			if err := validateDocsURL(req.URL, baseURL); err != nil {
				return fmt.Errorf("redirect blocked: %w", err)
			}
			return nil
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", &docsFetchError{target, resp.StatusCode}
	}

	if ct := resp.Header.Get("Content-Type"); ct != "" {
		mediaType := strings.TrimSpace(strings.SplitN(ct, ";", 2)[0])
		if !strings.HasPrefix(strings.ToLower(mediaType), "text/") {
			return "", fmt.Errorf("unexpected content type %q fetching %s", ct, target)
		}
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, docsMaxBodyBytes+1))
	if err != nil {
		return "", err
	}
	if len(body) > docsMaxBodyBytes {
		return "", fmt.Errorf("docs content exceeds %d bytes, refusing to return a truncated document", docsMaxBodyBytes)
	}

	return strings.TrimSpace(string(body)), nil
}

type docsFetchError struct {
	url        string
	statusCode int
}

func (e *docsFetchError) Error() string {
	return fmt.Sprintf("unexpected status %d fetching %s", e.statusCode, e.url)
}
