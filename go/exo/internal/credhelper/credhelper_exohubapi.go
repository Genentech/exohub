//go:build exohubapi

package credhelper

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Genentech/exohub/go/exo/internal/defaults"
)

type apiErrorResponse struct {
	Detail string `json:"detail"`
}

// GetCredsFromServer calls POST /api/grants/credentials (server fallback path).
// loadToken returns the bearer JWT; pass nil to skip authorization headers.
func GetCredsFromServer(ctx context.Context, s3URL, permission string, loadToken func() (string, error), debugf func(string, ...any)) (*Credentials, error) {
	if debugf == nil {
		debugf = func(string, ...any) {}
	}
	apiBase := strings.TrimRight(defaults.APIBase(), "/")
	endpoint := apiBase + "/grants/credentials"

	payload, err := json.Marshal(map[string]string{
		"s3url":      s3URL,
		"permission": permission,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}
	debugf("POST %s", endpoint)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("failed to build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	if loadToken != nil {
		if token, tokenErr := loadToken(); tokenErr == nil && token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		} else {
			debugf("Warning: no auth token available: %v", tokenErr)
		}
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("grants credentials API unreachable: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail := strings.TrimSpace(string(body))
		var apiErr apiErrorResponse
		if json.Unmarshal(body, &apiErr) == nil && apiErr.Detail != "" {
			detail = apiErr.Detail
		}
		switch {
		case resp.StatusCode == 401:
			return nil, ErrAuthRequired
		case resp.StatusCode == 403:
			return nil, fmt.Errorf("%w: %s", ErrAccessDenied, detail)
		case resp.StatusCode >= 500:
			return nil, fmt.Errorf("grants credentials API server error (%d): %s; try again later", resp.StatusCode, detail)
		default:
			return nil, fmt.Errorf("grants credentials API error (%d): %s", resp.StatusCode, detail)
		}
	}

	var creds Credentials
	if err := json.Unmarshal(body, &creds); err != nil {
		return nil, fmt.Errorf("failed to parse credentials response: %w", err)
	}
	creds.Version = 1
	return &creds, nil
}
