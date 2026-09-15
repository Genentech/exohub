//go:build !internal

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// cognitoOAuthURL is the token endpoint for the OIDC provider's token endpoint.
// Overridable via EXO_COGNITO_OAUTH_URL.
// The built-in default is empty for the OSS build; supply via env or an internal overlay.
var cognitoOAuthURL = ""

// doRefreshIDToken exchanges a refresh_token for a fresh id_token using the
// configured OIDC token endpoint (EXO_COGNITO_OAUTH_URL or cognitoOAuthURL overlay).
// Returns the new id_token and (possibly rotated) refresh_token.
func doRefreshIDToken(ctx context.Context, refreshToken string, debugf func(string, ...any)) (idToken, newRefreshToken string, err error) {
	clientID := resolveEnvDefault("", "EXO_OIDC_CLIENT_ID", defaultOIDCClientID)
	oauthURL := resolveEnvDefault("", "EXO_COGNITO_OAUTH_URL", cognitoOAuthURL)

	if oauthURL == "" {
		return "", "", fmt.Errorf("no OIDC token endpoint configured: set EXO_COGNITO_OAUTH_URL")
	}

	data := url.Values{}
	data.Set("grant_type", "refresh_token")
	data.Set("client_id", clientID)
	data.Set("refresh_token", refreshToken)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, oauthURL,
		strings.NewReader(data.Encode()))
	if err != nil {
		return "", "", fmt.Errorf("failed to build refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("refresh request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("refresh request returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var tok struct {
		IDToken      string `json:"id_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		return "", "", fmt.Errorf("failed to parse refresh response: %w", err)
	}
	if tok.IDToken == "" {
		return "", "", fmt.Errorf("refresh response contained no id_token")
	}

	debugf("base-creds: id_token refreshed via Cognito token endpoint")
	return tok.IDToken, tok.RefreshToken, nil
}

// defaultOIDCClientID is the default OIDC client ID for the OSS build.
// Empty in the OSS base; set by the internal overlay (main_basecreds_refresh_internal.go).
var defaultOIDCClientID = ""
