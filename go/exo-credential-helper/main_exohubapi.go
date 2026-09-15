//go:build exohubapi

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// errAuthRequired is returned by getCredsFromServer when the API responds with 401.
var errAuthRequired = fmt.Errorf("authentication required: run 'exo login' to authenticate")

// errAccessDenied is returned by getCredsFromServer when the API responds with 403.
var errAccessDenied = fmt.Errorf("access denied")

type apiErrorResponse struct {
	Detail string `json:"detail"`
}

// printCredsFromServerError prints a user-friendly error for getCredsFromServer failures.
func printCredsFromServerError(err error, s3URL string) {
	switch {
	case err == errAuthRequired:
		fmt.Fprintf(os.Stderr, "Error: authentication required — run 'exo login' to authenticate\n")
	case errors.Is(err, errAccessDenied):
		fmt.Fprintf(os.Stderr, "Error: access denied: you are not authorized for %s\n", s3URL)
		fmt.Fprintf(os.Stderr, "Hint: ask a dataset owner to add you to .exohub/permissions\n")
	default:
		fmt.Fprintf(os.Stderr, "Error: Failed to get credentials: %v\n", err)
	}
}

// getCredsFromServer calls POST /api/grants/credentials on the exohub API to
// provision a grant on-demand (used for public-dataset readers). The server
// reads _grants.json, checks read_access, and issues temporary credentials via
// the admin service account.
func getCredsFromServer(s3URL, permission string, debugf func(string, ...any)) (*credentialOutput, error) {
	apiBase := strings.TrimRight(resolveEnvDefault("", "EXOHUB_API_URL", builtinAPIBase), "/")
	if apiBase == "" {
		return nil, fmt.Errorf("EXOHUB_API_URL is not set; set it to your exohub server URL (e.g. https://your-exohub-server.example.com/api)")
	}
	endpoint := apiBase + "/grants/credentials"

	payload, err := json.Marshal(map[string]string{
		"s3url":      s3URL,
		"permission": permission,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	debugf("POST %s", endpoint)

	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("failed to build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// Attach Bearer token from 'exo login' token store.
	if token, err := loadBearerToken(); err == nil && token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	} else {
		debugf("Warning: no auth token available: %v", err)
	}

	// Attach username derived from the JWT (best-effort; server also has it).
	if username := usernameFromToken(); username != "" {
		req.Header.Set("X-Exohub-User", username)
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
		if err := json.Unmarshal(body, &apiErr); err == nil && apiErr.Detail != "" {
			detail = apiErr.Detail
		}
		switch {
		case resp.StatusCode == 401:
			return nil, errAuthRequired
		case resp.StatusCode == 403:
			return nil, fmt.Errorf("%w: %s", errAccessDenied, detail)
		case resp.StatusCode >= 500:
			return nil, fmt.Errorf("grants credentials API server error (%d): %s; try again later", resp.StatusCode, detail)
		default:
			return nil, fmt.Errorf("grants credentials API error (%d): %s", resp.StatusCode, detail)
		}
	}

	var creds credentialOutput
	if err := json.Unmarshal(body, &creds); err != nil {
		return nil, fmt.Errorf("failed to parse credentials response: %w", err)
	}
	creds.Version = 1
	return &creds, nil
}
