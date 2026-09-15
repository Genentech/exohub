//go:build artifactdb

package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Genentech/exohub/go/exo/commands/context"
	"github.com/Genentech/exohub/go/exo/commands/login"
	"github.com/Genentech/exohub/go/exo/internal/defaults"
)

func resolveCatalogURL() (string, error) {
	if envURL := os.Getenv("EXOHUB_CATALOG_URL"); envURL != "" {
		return strings.TrimRight(envURL, "/"), nil
	}
	ctx, err := context.GetCurrentContext()
	if err == nil && ctx.CatalogURL != "" {
		return strings.TrimRight(ctx.CatalogURL, "/"), nil
	}
	return defaults.CatalogURL(), nil
}

func catalogSetAuthHeader(req *http.Request) {
	tokenFile, err := login.GetTokenFile()
	if err != nil {
		return
	}
	token, err := login.LoadToken(tokenFile)
	if err != nil {
		return
	}
	if !token.Valid() && token.RefreshToken != "" {
		refreshed, _, refreshErr := login.RefreshTokenOnly(token.RefreshToken)
		if refreshErr == nil {
			token = refreshed
		}
	}
	if token.AccessToken != "" {
		req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	}
}

func catalogGet(url string) ([]byte, int, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	catalogSetAuthHeader(req)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}

type catalogSearchResponse struct {
	Results []json.RawMessage `json:"results"`
	Count   int64             `json:"count"`
	Total   int64             `json:"total"`
	Next    string            `json:"next"`
}

func catalogErrorReason(body []byte, statusCode int) string {
	var errResp struct {
		Reason string `json:"reason"`
	}
	if json.Unmarshal(body, &errResp) == nil && errResp.Reason != "" {
		return errResp.Reason
	}
	return fmt.Sprintf("HTTP %d: %s", statusCode, strings.TrimSpace(string(body)))
}
