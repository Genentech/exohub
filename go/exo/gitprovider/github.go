package gitprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// GitHubProvider implements GitProvider for GitHub
type GitHubProvider struct {
	host   string
	token  string
	client *http.Client
}

// NewGitHubProvider creates a new GitHub provider
func NewGitHubProvider(host, token string) *GitHubProvider {
	// Normalize GitHub host to API endpoint
	host = normalizeGitHubHost(host)

	return &GitHubProvider{
		host:  strings.TrimSuffix(host, "/"),
		token: token,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// normalizeGitHubHost converts github.com to api.github.com for API calls
func normalizeGitHubHost(host string) string {
	// Handle empty host - default to GitHub.com API
	if host == "" {
		return "https://api.github.com"
	}

	// Remove trailing slashes
	host = strings.TrimSuffix(host, "/")

	// Convert github.com to api.github.com
	host = strings.Replace(host, "://github.com", "://api.github.com", 1)
	host = strings.Replace(host, "://www.github.com", "://api.github.com", 1)

	// If just "github.com" without protocol
	if host == "github.com" || host == "www.github.com" {
		return "https://api.github.com"
	}

	return host
}

// githubCreateRepoRequest represents the GitHub API request for creating a repository
type githubCreateRepoRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Private     bool   `json:"private"`
}

type githubGenerateRepoRequest struct {
	Name        string `json:"name"`
	Owner       string `json:"owner"`
	Description string `json:"description,omitempty"`
	Private     bool   `json:"private"`
}

// githubRepository represents a GitHub repository response
type githubRepository struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	IsTemplate  bool   `json:"is_template"`
	CloneURL    string `json:"clone_url"`
	HTMLURL     string `json:"html_url"`
	SSHURL      string `json:"ssh_url"`
}

// CreateRepository creates a new repository in GitHub
func (g *GitHubProvider) CreateRepository(ctx context.Context, opts CreateRepoOptions) (*Repository, error) {
	if opts.Template != nil {
		return g.createRepositoryFromTemplate(ctx, opts)
	}

	// Detect if opts.Org is an organization or a user
	isOrg, err := g.isOrganization(ctx, opts.Org)
	if err != nil {
		return nil, &ProviderError{Provider: ProviderGitHub, Op: "detect org/user", Err: err}
	}

	var url string
	if isOrg {
		url = fmt.Sprintf("%s/orgs/%s/repos", g.host, opts.Org)
	} else {
		url = fmt.Sprintf("%s/user/repos", g.host)
	}

	reqBody := githubCreateRepoRequest{
		Name:        opts.Name,
		Description: opts.Description,
		Private:     opts.Visibility != "public",
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, &ProviderError{Provider: ProviderGitHub, Op: "marshal request", Err: err}
	}

	// Debug logging
	if os.Getenv("EXOHUB_CLI_DEBUG") == "1" {
		fmt.Fprintf(os.Stderr, "DEBUG: isOrg=%v\n", isOrg)
		fmt.Fprintf(os.Stderr, "DEBUG: POST %s\n", url)
		fmt.Fprintf(os.Stderr, "DEBUG: Request body: %s\n", string(body))
		fmt.Fprintf(os.Stderr, "DEBUG: Token length: %d\n", len(g.token))
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, &ProviderError{Provider: ProviderGitHub, Op: "create request", Err: err}
	}

	req.Header.Set("Authorization", "token "+g.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, &ProviderError{Provider: ProviderGitHub, Op: "create repository", Err: fmt.Errorf("%w: %v", ErrNetwork, err)}
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	// Debug logging
	if os.Getenv("EXOHUB_CLI_DEBUG") == "1" {
		fmt.Fprintf(os.Stderr, "DEBUG: Response status: %d\n", resp.StatusCode)
		fmt.Fprintf(os.Stderr, "DEBUG: Response body: %s\n", string(respBody))
	}

	switch resp.StatusCode {
	case http.StatusCreated:
		var repo githubRepository
		if err := json.Unmarshal(respBody, &repo); err != nil {
			return nil, &ProviderError{Provider: ProviderGitHub, Op: "parse response", Err: err}
		}
		return &Repository{
			Name:     repo.Name,
			CloneURL: repo.CloneURL,
			HTTPURL:  repo.HTMLURL,
			SSHURL:   repo.SSHURL,
		}, nil

	case http.StatusConflict: // Typically indicates resource already exists
		return nil, &ProviderError{Provider: ProviderGitHub, Op: "create repository", Err: ErrAlreadyExists}

	case http.StatusUnprocessableEntity: // Can indicate various validation errors, including already exists
		// Try to parse the response to see if it's explicitly an "already exists" error
		var githubErr struct {
			Message string `json:"message"`
			Errors  []struct {
				Code string `json:"code"`
				// Other fields like Field, Resource, etc.
			} `json:"errors"`
		}
		if err := json.Unmarshal(respBody, &githubErr); err == nil {
			// Check for specific GitHub error codes that mean "already exists"
			for _, e := range githubErr.Errors {
				if e.Code == "already_exists" || strings.Contains(strings.ToLower(githubErr.Message), "name already exists") {
					return nil, &ProviderError{Provider: ProviderGitHub, Op: "create repository", Err: ErrAlreadyExists}
				}
			}
		}
		// If not an explicit "already exists" error, return the full 422 message
		return nil, &ProviderError{
			Provider: ProviderGitHub,
			Op:       "create repository",
			Err:      fmt.Errorf("%w: status %d, response: %s", errors.New("validation failed"), resp.StatusCode, string(respBody)),
		}

	case http.StatusUnauthorized, http.StatusForbidden:
		// Show more details about auth failure
		return nil, &ProviderError{
			Provider: ProviderGitHub,
			Op:       "create repository",
			Err:      fmt.Errorf("%w: status %d, response: %s", ErrAuthFailed, resp.StatusCode, string(respBody)),
		}

	default:
		return nil, &ProviderError{
			Provider: ProviderGitHub,
			Op:       "create repository",
			Err:      fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(respBody)),
		}
	}
}

// ListOrgTemplates lists org-level templates in GitHub.
func (g *GitHubProvider) ListOrgTemplates(ctx context.Context, org string) ([]RepoTemplate, error) {
	url := fmt.Sprintf("%s/orgs/%s/repos?per_page=100&type=all", g.host, org)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, &ProviderError{Provider: ProviderGitHub, Op: "create request", Err: err}
	}

	req.Header.Set("Authorization", "token "+g.token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, &ProviderError{Provider: ProviderGitHub, Op: "list templates", Err: fmt.Errorf("%w: %v", ErrNetwork, err)}
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	switch resp.StatusCode {
	case http.StatusOK:
		var repos []githubRepository
		if err := json.Unmarshal(body, &repos); err != nil {
			return nil, &ProviderError{Provider: ProviderGitHub, Op: "parse response", Err: err}
		}
		templates := make([]RepoTemplate, 0)
		for _, repo := range repos {
			if repo.IsTemplate {
				templates = append(templates, RepoTemplate{
					Owner:       org,
					Name:        repo.Name,
					Description: repo.Description,
				})
			}
		}
		return templates, nil
	case http.StatusNotFound:
		return nil, nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, &ProviderError{Provider: ProviderGitHub, Op: "list templates", Err: ErrAuthFailed}
	default:
		return nil, &ProviderError{
			Provider: ProviderGitHub,
			Op:       "list templates",
			Err:      fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body)),
		}
	}
}

// ResolveTemplate resolves a template by name from the org's template repos.
func (g *GitHubProvider) ResolveTemplate(ctx context.Context, org, name string) (*RepoTemplate, error) {
	templates, err := g.ListOrgTemplates(ctx, org)
	if err != nil {
		return nil, err
	}
	for i := range templates {
		if templates[i].Name == name {
			return &templates[i], nil
		}
	}
	return nil, &ProviderError{Provider: ProviderGitHub, Op: "resolve template", Err: fmt.Errorf("template %q not found", name)}
}

func (g *GitHubProvider) createRepositoryFromTemplate(ctx context.Context, opts CreateRepoOptions) (*Repository, error) {
	template := opts.Template
	url := fmt.Sprintf("%s/repos/%s/%s/generate", g.host, template.Owner, template.Name)

	reqBody := githubGenerateRepoRequest{
		Name:        opts.Name,
		Owner:       opts.Org,
		Description: opts.Description,
		Private:     opts.Visibility != "public",
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, &ProviderError{Provider: ProviderGitHub, Op: "marshal request", Err: err}
	}

	if os.Getenv("EXOHUB_CLI_DEBUG") == "1" {
		fmt.Fprintf(os.Stderr, "DEBUG: POST %s\n", url)
		fmt.Fprintf(os.Stderr, "DEBUG: Request body: %s\n", string(body))
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, &ProviderError{Provider: ProviderGitHub, Op: "create request", Err: err}
	}

	req.Header.Set("Authorization", "token "+g.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, &ProviderError{Provider: ProviderGitHub, Op: "create from template", Err: fmt.Errorf("%w: %v", ErrNetwork, err)}
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	switch resp.StatusCode {
	case http.StatusCreated:
		var repo githubRepository
		if err := json.Unmarshal(respBody, &repo); err != nil {
			return nil, &ProviderError{Provider: ProviderGitHub, Op: "parse response", Err: err}
		}
		return &Repository{
			Name:     repo.Name,
			CloneURL: repo.CloneURL,
			HTTPURL:  repo.HTMLURL,
			SSHURL:   repo.SSHURL,
		}, nil
	case http.StatusConflict:
		return nil, &ProviderError{Provider: ProviderGitHub, Op: "create from template", Err: ErrAlreadyExists}
	case http.StatusUnprocessableEntity:
		return nil, &ProviderError{
			Provider: ProviderGitHub,
			Op:       "create from template",
			Err:      fmt.Errorf("%w: status %d, response: %s", errors.New("validation failed"), resp.StatusCode, string(respBody)),
		}
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, &ProviderError{
			Provider: ProviderGitHub,
			Op:       "create from template",
			Err:      fmt.Errorf("%w: status %d, response: %s", ErrAuthFailed, resp.StatusCode, string(respBody)),
		}
	default:
		return nil, &ProviderError{
			Provider: ProviderGitHub,
			Op:       "create from template",
			Err:      fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(respBody)),
		}
	}
}

// RepositoryExists checks if a repository exists in GitHub
func (g *GitHubProvider) RepositoryExists(ctx context.Context, org, name string) (bool, error) {
	_, err := g.GetRepository(ctx, org, name)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// GetRepository fetches repository details from GitHub
func (g *GitHubProvider) GetRepository(ctx context.Context, org, name string) (*Repository, error) {
	url := fmt.Sprintf("%s/repos/%s/%s", g.host, org, name)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, &ProviderError{Provider: ProviderGitHub, Op: "create request", Err: err}
	}

	req.Header.Set("Authorization", "token "+g.token)
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, &ProviderError{Provider: ProviderGitHub, Op: "get repository", Err: fmt.Errorf("%w: %v", ErrNetwork, err)}
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	switch resp.StatusCode {
	case http.StatusOK:
		var repo githubRepository
		if err := json.Unmarshal(body, &repo); err != nil {
			return nil, &ProviderError{Provider: ProviderGitHub, Op: "parse response", Err: err}
		}
		return &Repository{
			Name:     repo.Name,
			CloneURL: repo.CloneURL,
			HTTPURL:  repo.HTMLURL,
			SSHURL:   repo.SSHURL,
		}, nil
	case http.StatusNotFound:
		return nil, &ProviderError{Provider: ProviderGitHub, Op: "get repository", Err: ErrNotFound}
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, &ProviderError{Provider: ProviderGitHub, Op: "get repository", Err: ErrAuthFailed}
	default:
		return nil, &ProviderError{
			Provider: ProviderGitHub,
			Op:       "get repository",
			Err:      fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body)),
		}
	}
}

// isOrganization checks if the given name is an organization or a user
// Returns true if organization, false if user
func (g *GitHubProvider) isOrganization(ctx context.Context, name string) (bool, error) {
	// Try to fetch as an organization first
	orgURL := fmt.Sprintf("%s/orgs/%s", g.host, name)

	req, err := http.NewRequestWithContext(ctx, "GET", orgURL, nil)
	if err != nil {
		return false, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "token "+g.token)
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := g.client.Do(req)
	if err != nil {
		return false, fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		// Successfully fetched as org
		return true, nil
	}

	if resp.StatusCode == http.StatusNotFound {
		// Not an org, check if it's a user
		userURL := fmt.Sprintf("%s/users/%s", g.host, name)

		req, err = http.NewRequestWithContext(ctx, "GET", userURL, nil)
		if err != nil {
			return false, fmt.Errorf("create request: %w", err)
		}

		req.Header.Set("Authorization", "token "+g.token)
		req.Header.Set("Accept", "application/vnd.github.v3+json")

		resp, err = g.client.Do(req)
		if err != nil {
			return false, fmt.Errorf("network error: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			// It's a user
			return false, nil
		}

		if resp.StatusCode == http.StatusNotFound {
			return false, fmt.Errorf("neither organization nor user found: %s", name)
		}

		body, _ := io.ReadAll(resp.Body)
		return false, fmt.Errorf("unexpected status %d when checking user: %s", resp.StatusCode, string(body))
	}

	body, _ := io.ReadAll(resp.Body)
	return false, fmt.Errorf("unexpected status %d when checking org: %s", resp.StatusCode, string(body))
}
