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

// GiteaProvider implements GitProvider for Gitea
type GiteaProvider struct {
	host   string
	token  string
	client *http.Client
}

// NewGiteaProvider creates a new Gitea provider
func NewGiteaProvider(host, token string) *GiteaProvider {
	return &GiteaProvider{
		host:  strings.TrimSuffix(host, "/"),
		token: token,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// giteaCreateRepoRequest represents the Gitea API request for creating a repository
type giteaCreateRepoRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Private     bool   `json:"private"`
}

type giteaGenerateRepoRequest struct {
	Name        string `json:"name"`
	Owner       string `json:"owner"`
	Description string `json:"description,omitempty"`
	Private     bool   `json:"private"`
	GitContent  bool   `json:"git_content"`
	GitHooks    bool   `json:"git_hooks"`
	Labels      bool   `json:"labels"`
	Topics      bool   `json:"topics"`
	Webhooks    bool   `json:"webhooks"`
	Avatar      bool   `json:"avatar"`
}

// giteaRepository represents a Gitea repository response
type giteaRepository struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Template    bool   `json:"template"`
	CloneURL    string `json:"clone_url"`
	HTMLURL     string `json:"html_url"`
	SSHURL      string `json:"ssh_url"`
}

// CreateRepository creates a new repository in Gitea
func (g *GiteaProvider) CreateRepository(ctx context.Context, opts CreateRepoOptions) (*Repository, error) {
	if opts.Template != nil {
		return g.createRepositoryFromTemplate(ctx, opts)
	}

	url := fmt.Sprintf("%s/api/v1/org/%s/repos", g.host, opts.Org)

	reqBody := giteaCreateRepoRequest{
		Name:        opts.Name,
		Description: opts.Description,
		Private:     opts.Private,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, &ProviderError{Provider: ProviderGitea, Op: "marshal request", Err: err}
	}

	// Debug logging
	if os.Getenv("EXOHUB_CLI_DEBUG") == "1" {
		fmt.Fprintf(os.Stderr, "DEBUG: POST %s\n", url)
		fmt.Fprintf(os.Stderr, "DEBUG: Request body: %s\n", string(body))
		fmt.Fprintf(os.Stderr, "DEBUG: Token length: %d\n", len(g.token))
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, &ProviderError{Provider: ProviderGitea, Op: "create request", Err: err}
	}

	req.Header.Set("Authorization", "token "+g.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, &ProviderError{Provider: ProviderGitea, Op: "create repository", Err: fmt.Errorf("%w: %v", ErrNetwork, err)}
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
		var repo giteaRepository
		if err := json.Unmarshal(respBody, &repo); err != nil {
			return nil, &ProviderError{Provider: ProviderGitea, Op: "parse response", Err: err}
		}
		return &Repository{
			Name:     repo.Name,
			CloneURL: repo.CloneURL,
			HTTPURL:  repo.HTMLURL,
			SSHURL:   repo.SSHURL,
		}, nil

	case http.StatusConflict, http.StatusUnprocessableEntity:
		return nil, &ProviderError{Provider: ProviderGitea, Op: "create repository", Err: ErrAlreadyExists}

	case http.StatusUnauthorized, http.StatusForbidden:
		// Show more details about auth failure
		return nil, &ProviderError{
			Provider: ProviderGitea,
			Op:       "create repository",
			Err:      fmt.Errorf("%w: status %d, response: %s", ErrAuthFailed, resp.StatusCode, string(respBody)),
		}

	default:
		return nil, &ProviderError{
			Provider: ProviderGitea,
			Op:       "create repository",
			Err:      fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(respBody)),
		}
	}
}

// ListOrgTemplates lists org-level templates in Gitea.
func (g *GiteaProvider) ListOrgTemplates(ctx context.Context, org string) ([]RepoTemplate, error) {
	url := fmt.Sprintf("%s/api/v1/orgs/%s/repos?limit=100", g.host, org)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, &ProviderError{Provider: ProviderGitea, Op: "create request", Err: err}
	}

	req.Header.Set("Authorization", "token "+g.token)

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, &ProviderError{Provider: ProviderGitea, Op: "list templates", Err: fmt.Errorf("%w: %v", ErrNetwork, err)}
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	switch resp.StatusCode {
	case http.StatusOK:
		var repos []giteaRepository
		if err := json.Unmarshal(body, &repos); err != nil {
			return nil, &ProviderError{Provider: ProviderGitea, Op: "parse response", Err: err}
		}
		templates := make([]RepoTemplate, 0)
		for _, repo := range repos {
			if repo.Template {
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
		return nil, &ProviderError{Provider: ProviderGitea, Op: "list templates", Err: ErrAuthFailed}
	default:
		return nil, &ProviderError{
			Provider: ProviderGitea,
			Op:       "list templates",
			Err:      fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body)),
		}
	}
}

// ResolveTemplate resolves a template by name from the org's template repos.
func (g *GiteaProvider) ResolveTemplate(ctx context.Context, org, name string) (*RepoTemplate, error) {
	templates, err := g.ListOrgTemplates(ctx, org)
	if err != nil {
		return nil, err
	}
	for i := range templates {
		if templates[i].Name == name {
			return &templates[i], nil
		}
	}
	return nil, &ProviderError{Provider: ProviderGitea, Op: "resolve template", Err: fmt.Errorf("template %q not found", name)}
}

func (g *GiteaProvider) createRepositoryFromTemplate(ctx context.Context, opts CreateRepoOptions) (*Repository, error) {
	template := opts.Template
	url := fmt.Sprintf("%s/api/v1/repos/%s/%s/generate", g.host, template.Owner, template.Name)

	reqBody := giteaGenerateRepoRequest{
		Name:        opts.Name,
		Owner:       opts.Org,
		Description: opts.Description,
		Private:     opts.Private,
		GitContent:  true,
		GitHooks:    true,
		Labels:      true,
		Topics:      true,
		Webhooks:    true,
		Avatar:      true,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, &ProviderError{Provider: ProviderGitea, Op: "marshal request", Err: err}
	}

	if os.Getenv("EXOHUB_CLI_DEBUG") == "1" {
		fmt.Fprintf(os.Stderr, "DEBUG: POST %s\n", url)
		fmt.Fprintf(os.Stderr, "DEBUG: Request body: %s\n", string(body))
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, &ProviderError{Provider: ProviderGitea, Op: "create request", Err: err}
	}

	req.Header.Set("Authorization", "token "+g.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, &ProviderError{Provider: ProviderGitea, Op: "create from template", Err: fmt.Errorf("%w: %v", ErrNetwork, err)}
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
		var repo giteaRepository
		if err := json.Unmarshal(respBody, &repo); err != nil {
			return nil, &ProviderError{Provider: ProviderGitea, Op: "parse response", Err: err}
		}
		return &Repository{
			Name:     repo.Name,
			CloneURL: repo.CloneURL,
			HTTPURL:  repo.HTMLURL,
			SSHURL:   repo.SSHURL,
		}, nil
	case http.StatusConflict, http.StatusUnprocessableEntity:
		lower := strings.ToLower(string(respBody))
		if strings.Contains(lower, "already exists") || strings.Contains(lower, "name has already been taken") {
			return nil, &ProviderError{Provider: ProviderGitea, Op: "create from template", Err: ErrAlreadyExists}
		}
		return nil, &ProviderError{
			Provider: ProviderGitea,
			Op:       "create from template",
			Err:      fmt.Errorf("validation failed: status %d, response: %s", resp.StatusCode, string(respBody)),
		}
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, &ProviderError{Provider: ProviderGitea, Op: "create from template", Err: ErrAuthFailed}
	default:
		return nil, &ProviderError{
			Provider: ProviderGitea,
			Op:       "create from template",
			Err:      fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(respBody)),
		}
	}
}

// RepositoryExists checks if a repository exists in Gitea
func (g *GiteaProvider) RepositoryExists(ctx context.Context, org, name string) (bool, error) {
	_, err := g.GetRepository(ctx, org, name)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// GetRepository fetches repository details from Gitea
func (g *GiteaProvider) GetRepository(ctx context.Context, org, name string) (*Repository, error) {
	url := fmt.Sprintf("%s/api/v1/repos/%s/%s", g.host, org, name)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, &ProviderError{Provider: ProviderGitea, Op: "create request", Err: err}
	}

	req.Header.Set("Authorization", "token "+g.token)

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, &ProviderError{Provider: ProviderGitea, Op: "get repository", Err: fmt.Errorf("%w: %v", ErrNetwork, err)}
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	switch resp.StatusCode {
	case http.StatusOK:
		var repo giteaRepository
		if err := json.Unmarshal(body, &repo); err != nil {
			return nil, &ProviderError{Provider: ProviderGitea, Op: "parse response", Err: err}
		}
		return &Repository{
			Name:     repo.Name,
			CloneURL: repo.CloneURL,
			HTTPURL:  repo.HTMLURL,
			SSHURL:   repo.SSHURL,
		}, nil
	case http.StatusNotFound:
		return nil, &ProviderError{Provider: ProviderGitea, Op: "get repository", Err: ErrNotFound}
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, &ProviderError{Provider: ProviderGitea, Op: "get repository", Err: ErrAuthFailed}
	default:
		return nil, &ProviderError{
			Provider: ProviderGitea,
			Op:       "get repository",
			Err:      fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body)),
		}
	}
}
