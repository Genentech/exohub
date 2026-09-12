package gitprovider

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// VerifyToken checks that a token is valid by first detecting the provider,
// then making an authenticated API call to the provider's user endpoint.
// Returns the detected provider type on success.
func VerifyToken(ctx context.Context, host, token string) (string, error) {
	baseURL := "https://" + strings.TrimPrefix(strings.TrimPrefix(host, "https://"), "http://")
	baseURL = strings.TrimSuffix(baseURL, "/")

	// Detect provider first
	provider, err := DetectProvider(ctx, baseURL, "")
	if err != nil {
		return "", fmt.Errorf("could not detect provider for %s: %w", host, err)
	}

	// Verify token against the detected provider's user endpoint
	var userURL, authHeader string
	switch provider {
	case ProviderGitea:
		userURL = baseURL + "/api/v1/user"
		authHeader = "token " + token
	case ProviderGitLab:
		userURL = baseURL + "/api/v4/user"
		authHeader = "Bearer " + token
	case ProviderGitHub:
		// GitHub API lives at api.github.com, not github.com
		apiHost := strings.Replace(baseURL, "://github.com", "://api.github.com", 1)
		userURL = apiHost + "/user"
		authHeader = "Bearer " + token
	default:
		return "", fmt.Errorf("unsupported provider %s for %s", provider, host)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", userURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", authHeader)

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("could not reach %s: %w", host, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	detail := strings.TrimSpace(string(body))

	switch resp.StatusCode {
	case http.StatusOK:
		// For GitHub, /user returns 200 even without auth (public info).
		// Authenticated requests get rate limit 5000; unauthenticated get 60.
		if provider == ProviderGitHub {
			rateLimit := resp.Header.Get("X-Ratelimit-Limit")
			if rateLimit == "60" || rateLimit == "" {
				return "", fmt.Errorf("token rejected by %s (%s): not authenticated (rate limit: %s)", host, provider, rateLimit)
			}
		}
		return string(provider), nil
	case http.StatusUnauthorized, http.StatusForbidden:
		msg := fmt.Sprintf("token rejected by %s (%s): %d", host, provider, resp.StatusCode)
		if detail != "" {
			msg += " — " + detail
		}
		return "", fmt.Errorf("%s", msg)
	default:
		msg := fmt.Sprintf("unexpected response from %s (%s): %d", host, provider, resp.StatusCode)
		if detail != "" {
			msg += " — " + detail
		}
		return "", fmt.Errorf("%s", msg)
	}
}
