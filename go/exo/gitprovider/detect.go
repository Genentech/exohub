package gitprovider

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// DetectProvider detects the git provider type from host and explicit provider config
func DetectProvider(ctx context.Context, host string, explicitProvider string) (ProviderType, error) {
	// 1. Check explicit provider configuration
	if explicitProvider != "" {
		switch strings.ToLower(explicitProvider) {
		case "gitea":
			return ProviderGitea, nil
		case "github":
			return ProviderGitHub, nil
		case "gitlab":
			return ProviderGitLab, nil
		default:
			return "", fmt.Errorf("unknown provider type: %s (supported: gitea, github, gitlab)", explicitProvider)
		}
	}

	// 2. Try header-based detection
	if provider, err := detectFromHeaders(ctx, host); err == nil && provider != "" {
		return provider, nil
	}

	// 3. Try API endpoint fallback
	if provider, err := detectFromAPI(ctx, host); err == nil && provider != "" {
		return provider, nil
	}

	// 4. Detection failed
	return "", fmt.Errorf("could not detect provider for %s (supported: gitea, github, gitlab)", host)
}

// detectFromHeaders attempts to detect provider from HTTP headers
func detectFromHeaders(ctx context.Context, host string) (ProviderType, error) {
	client := &http.Client{
		Timeout: 2 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse // Don't follow redirects
		},
	}

	req, err := http.NewRequestWithContext(ctx, "HEAD", host, nil)
	if err != nil {
		return "", err
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	// Check for Gitea signature (very distinctive!)
	if cookies := resp.Header.Get("Set-Cookie"); strings.Contains(cookies, "i_like_gitea=") {
		return ProviderGitea, nil
	}

	// Check for GitLab signatures
	if resp.Header.Get("X-Gitlab-Meta") != "" {
		return ProviderGitLab, nil
	}
	if cookies := resp.Header.Get("Set-Cookie"); strings.Contains(cookies, "_gitlab_session=") {
		return ProviderGitLab, nil
	}

	// Check for GitHub signatures
	if cookies := resp.Header.Get("Set-Cookie"); strings.Contains(cookies, "_gh_sess=") {
		return ProviderGitHub, nil
	}
	for key := range resp.Header {
		if strings.HasPrefix(strings.ToLower(key), "x-github-") {
			return ProviderGitHub, nil
		}
	}

	return "", fmt.Errorf("no distinctive headers found")
}

// detectFromAPI attempts to detect provider from API endpoints
func detectFromAPI(ctx context.Context, host string) (ProviderType, error) {
	client := &http.Client{
		Timeout: 2 * time.Second,
	}

	// Try Gitea API
	if isGiteaAPI(ctx, client, host) {
		return ProviderGitea, nil
	}

	// Try GitLab API
	if isGitLabAPI(ctx, client, host) {
		return ProviderGitLab, nil
	}

	// Try GitHub API
	if isGitHubAPI(ctx, client, host) {
		return ProviderGitHub, nil
	}

	return "", fmt.Errorf("no matching API endpoints found")
}

func isGiteaAPI(ctx context.Context, client *http.Client, host string) bool {
	url := strings.TrimSuffix(host, "/") + "/api/v1/version"
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return false
	}

	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}

func isGitLabAPI(ctx context.Context, client *http.Client, host string) bool {
	url := strings.TrimSuffix(host, "/") + "/api/v4/version"
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return false
	}

	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}

func isGitHubAPI(ctx context.Context, client *http.Client, host string) bool {
	url := strings.TrimSuffix(host, "/") + "/api"
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return false
	}

	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	// Check for GitHub-specific headers
	for key := range resp.Header {
		if strings.HasPrefix(strings.ToLower(key), "x-github-") {
			return true
		}
	}

	return false
}

// NewProvider creates a new provider instance with authentication
func NewProvider(providerType ProviderType, host string) (GitProvider, error) {
	token, err := getAuthToken(providerType)
	if err != nil {
		return nil, err
	}

	switch providerType {
	case ProviderGitea:
		return NewGiteaProvider(host, token), nil
	case ProviderGitHub:
		return NewGitHubProvider(host, token), nil
	case ProviderGitLab:
		return NewGitLabProvider(host, token), nil
	default:
		return nil, fmt.Errorf("unsupported provider: %s", providerType)
	}
}

// getAuthToken retrieves authentication token from environment or config
func getAuthToken(providerType ProviderType) (string, error) {
	// 1. Check provider-specific environment variable
	var providerSpecificVar string
	switch providerType {
	case ProviderGitea:
		providerSpecificVar = "EXOHUB_GITEA_TOKEN"
	case ProviderGitHub:
		providerSpecificVar = "EXOHUB_GITHUB_TOKEN"
	case ProviderGitLab:
		providerSpecificVar = "EXOHUB_GITLAB_TOKEN"
	}

	if token := os.Getenv(providerSpecificVar); token != "" {
		return token, nil
	}

	// 2. Check generic environment variable
	if token := os.Getenv("EXOHUB_GIT_TOKEN"); token != "" {
		return token, nil
	}

	// 3. TODO: Check config file ~/.config/exo/tokens.yaml

	// No token found
	return "", fmt.Errorf("authentication token not found: set %s or EXOHUB_GIT_TOKEN environment variable", providerSpecificVar)
}
