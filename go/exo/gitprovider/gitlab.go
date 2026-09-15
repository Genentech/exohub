package gitprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// GitLabProvider implements GitProvider for GitLab
type GitLabProvider struct {
	host   string
	token  string
	client *http.Client
}

// NewGitLabProvider creates a new GitLab provider
func NewGitLabProvider(host, token string) *GitLabProvider {
	// Normalize GitLab host to API endpoint
	host = normalizeGitLabHost(host)

	return &GitLabProvider{
		host:  strings.TrimSuffix(host, "/"),
		token: token,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// normalizeGitLabHost converts gitlab.com to gitlab.com/api/v4 for API calls
func normalizeGitLabHost(host string) string {
	// Handle empty host - default to GitLab.com API
	if host == "" {
		return "https://gitlab.com/api/v4"
	}

	// Remove trailing slashes
	host = strings.TrimSuffix(host, "/")

	// If already has /api/v4, return as is
	if strings.HasSuffix(host, "/api/v4") {
		return host
	}

	// Add scheme if missing
	if !strings.HasPrefix(host, "http://") && !strings.HasPrefix(host, "https://") {
		host = "https://" + host
	}

	// Append /api/v4
	return host + "/api/v4"
}

// gitlabCreateProjectRequest represents the GitLab API request for creating a project
type gitlabCreateProjectRequest struct {
	Name                        string `json:"name"`
	Description                 string `json:"description,omitempty"`
	NamespaceID                 int    `json:"namespace_id,omitempty"`
	Path                        string `json:"path,omitempty"`
	Visibility                  string `json:"visibility"`
	UseCustomTemplate           bool   `json:"use_custom_template,omitempty"`
	TemplateProjectID           int    `json:"template_project_id,omitempty"`
	GroupWithProjectTemplatesID int    `json:"group_with_project_templates_id,omitempty"`
	InitializeWithReadme        bool   `json:"initialize_with_readme,omitempty"`
}

// gitlabProject represents a GitLab project response
type gitlabProject struct {
	ID                int    `json:"id"`
	Name              string `json:"name"`
	Path              string `json:"path"`
	Description       string `json:"description"`
	Template          bool   `json:"template"`
	HTTPURLToRepo     string `json:"http_url_to_repo"`
	SSHURLToRepo      string `json:"ssh_url_to_repo"`
	WebURL            string `json:"web_url"`
	PathWithNamespace string `json:"path_with_namespace"`
}

// gitlabNamespace represents a GitLab namespace/group
type gitlabNamespace struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	FullPath string `json:"full_path"`
}

// CreateRepository creates a new project in GitLab
func (g *GitLabProvider) CreateRepository(ctx context.Context, opts CreateRepoOptions) (*Repository, error) {
	if opts.Template != nil {
		return g.createRepositoryFromTemplate(ctx, opts)
	}

	// Get namespace ID if org is specified
	var namespaceID int
	if opts.Org != "" {
		nsID, err := g.getNamespaceID(ctx, opts.Org)
		if err != nil {
			return nil, &ProviderError{Provider: ProviderGitLab, Op: "resolve namespace", Err: err}
		}
		namespaceID = nsID
	}

	url := fmt.Sprintf("%s/projects", g.host)

	visibility := "public"
	if opts.Private {
		visibility = "private"
	}

	reqBody := gitlabCreateProjectRequest{
		Name:        opts.Name,
		Description: opts.Description,
		NamespaceID: namespaceID,
		Visibility:  visibility,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, &ProviderError{Provider: ProviderGitLab, Op: "marshal request", Err: err}
	}

	// Debug logging
	if os.Getenv("EXOHUB_CLI_DEBUG") == "1" {
		fmt.Fprintf(os.Stderr, "DEBUG: POST %s\n", url)
		fmt.Fprintf(os.Stderr, "DEBUG: Request body: %s\n", string(body))
		fmt.Fprintf(os.Stderr, "DEBUG: Token length: %d\n", len(g.token))
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, &ProviderError{Provider: ProviderGitLab, Op: "create request", Err: err}
	}

	req.Header.Set("Authorization", "Bearer "+g.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, &ProviderError{Provider: ProviderGitLab, Op: "create project", Err: fmt.Errorf("%w: %v", ErrNetwork, err)}
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
		var project gitlabProject
		if err := json.Unmarshal(respBody, &project); err != nil {
			return nil, &ProviderError{Provider: ProviderGitLab, Op: "parse response", Err: err}
		}
		return &Repository{
			Name:     project.Name,
			CloneURL: project.HTTPURLToRepo,
			HTTPURL:  project.WebURL,
			SSHURL:   project.SSHURLToRepo,
		}, nil

	case http.StatusBadRequest:
		// Check if it's an "already exists" error
		var gitlabErr struct {
			Message map[string][]string `json:"message"`
		}
		if err := json.Unmarshal(respBody, &gitlabErr); err == nil {
			if nameErrs, ok := gitlabErr.Message["name"]; ok {
				for _, msg := range nameErrs {
					if strings.Contains(strings.ToLower(msg), "has already been taken") {
						return nil, &ProviderError{Provider: ProviderGitLab, Op: "create project", Err: ErrAlreadyExists}
					}
				}
			}
			if pathErrs, ok := gitlabErr.Message["path"]; ok {
				for _, msg := range pathErrs {
					if strings.Contains(strings.ToLower(msg), "has already been taken") {
						return nil, &ProviderError{Provider: ProviderGitLab, Op: "create project", Err: ErrAlreadyExists}
					}
				}
			}
		}
		return nil, &ProviderError{
			Provider: ProviderGitLab,
			Op:       "create project",
			Err:      fmt.Errorf("validation failed: status %d, response: %s", resp.StatusCode, string(respBody)),
		}

	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, &ProviderError{
			Provider: ProviderGitLab,
			Op:       "create project",
			Err:      fmt.Errorf("%w: status %d, response: %s", ErrAuthFailed, resp.StatusCode, string(respBody)),
		}

	default:
		return nil, &ProviderError{
			Provider: ProviderGitLab,
			Op:       "create project",
			Err:      fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(respBody)),
		}
	}
}

// parentNamespace returns the parent of a namespace path.
// "a/b/c" → "a/b", "a/b" → "a", "a" → ""
func parentNamespace(path string) string {
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[:i]
	}
	return ""
}

// ListOrgTemplates lists template projects from the "exohub-templates"
// subgroup in GitLab. It walks up the group hierarchy looking for an
// "exohub-templates" subgroup (e.g. for org "a/b/c", tries
// "a/b/c/exohub-templates", then "a/b/exohub-templates", then
// "a/exohub-templates").
//
// This convention is used because GitLab does not expose a per-project
// "template" boolean via its API.
func (g *GitLabProvider) ListOrgTemplates(ctx context.Context, org string) ([]RepoTemplate, error) {
	org = extractNamespaceFromURL(org)

	current := org
	for current != "" {
		templates, err := g.listTemplatesSubgroup(ctx, current)
		if err != nil {
			return nil, err
		}
		if len(templates) > 0 {
			return templates, nil
		}
		current = parentNamespace(current)
	}
	return nil, nil
}

// listTemplatesSubgroup lists all projects in org/exohub-templates.
// Returns nil, nil if the exohub-templates subgroup does not exist.
func (g *GitLabProvider) listTemplatesSubgroup(ctx context.Context, org string) ([]RepoTemplate, error) {
	templatesGroup := org + "/exohub-templates"
	namespaceID, err := g.getNamespaceID(ctx, templatesGroup)
	if err != nil {
		// templates subgroup doesn't exist — not an error
		if strings.Contains(err.Error(), "not found") {
			return nil, nil
		}
		return nil, &ProviderError{Provider: ProviderGitLab, Op: "resolve namespace", Err: err}
	}

	reqURL := fmt.Sprintf("%s/groups/%d/projects?per_page=100", g.host, namespaceID)

	if os.Getenv("EXOHUB_CLI_DEBUG") == "1" {
		fmt.Fprintf(os.Stderr, "DEBUG: GET %s (templates subgroup of %q)\n", reqURL, org)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, &ProviderError{Provider: ProviderGitLab, Op: "create request", Err: err}
	}

	req.Header.Set("Authorization", "Bearer "+g.token)

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, &ProviderError{Provider: ProviderGitLab, Op: "list templates", Err: fmt.Errorf("%w: %v", ErrNetwork, err)}
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	switch resp.StatusCode {
	case http.StatusOK:
		var projects []gitlabProject
		if err := json.Unmarshal(body, &projects); err != nil {
			return nil, &ProviderError{Provider: ProviderGitLab, Op: "parse response", Err: err}
		}
		if os.Getenv("EXOHUB_CLI_DEBUG") == "1" {
			fmt.Fprintf(os.Stderr, "DEBUG: Found %d template projects in %q\n", len(projects), templatesGroup)
		}
		templates := make([]RepoTemplate, 0, len(projects))
		for _, project := range projects {
			owner := templatesGroup
			if project.PathWithNamespace != "" {
				owner = parentNamespace(project.PathWithNamespace)
			}
			templates = append(templates, RepoTemplate{
				Owner:       owner,
				Name:        project.Path,
				Description: project.Description,
			})
		}
		return templates, nil
	case http.StatusNotFound:
		return nil, nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, &ProviderError{Provider: ProviderGitLab, Op: "list templates", Err: ErrAuthFailed}
	default:
		return nil, &ProviderError{
			Provider: ProviderGitLab,
			Op:       "list templates",
			Err:      fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body)),
		}
	}
}

// ResolveTemplate resolves a template project by name in GitLab.
// It tries the following paths in order:
//  1. org/exohub-templates/<name> (conventional templates subgroup)
//  2. Walk up parent groups trying <parent>/exohub-templates/<name>
//  3. org/<name> (direct project in the org)
func (g *GitLabProvider) ResolveTemplate(ctx context.Context, org, name string) (*RepoTemplate, error) {
	org = extractNamespaceFromURL(org)

	// Try exohub-templates/ subgroup at each level of the hierarchy
	current := org
	for current != "" {
		templatePath := current + "/exohub-templates/" + name
		project, err := g.getProject(ctx, templatePath)
		if err == nil {
			return &RepoTemplate{
				Owner:       parentNamespace(project.PathWithNamespace),
				Name:        project.Path,
				Description: project.Description,
			}, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return nil, err
		}
		current = parentNamespace(current)
	}

	// Fall back: try direct project path org/<name>
	project, err := g.getProject(ctx, org+"/"+name)
	if err == nil {
		return &RepoTemplate{
			Owner:       parentNamespace(project.PathWithNamespace),
			Name:        project.Path,
			Description: project.Description,
		}, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	return nil, &ProviderError{Provider: ProviderGitLab, Op: "resolve template", Err: fmt.Errorf("template %q not found", name)}
}

// getProject fetches a project by its full path (e.g. "group/subgroup/project").
func (g *GitLabProvider) getProject(ctx context.Context, projectPath string) (*gitlabProject, error) {
	encodedPath := url.PathEscape(projectPath)
	reqURL := fmt.Sprintf("%s/projects/%s", g.host, encodedPath)

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+g.token)

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, &ProviderError{Provider: ProviderGitLab, Op: "get project", Err: fmt.Errorf("%w: %v", ErrNetwork, err)}
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	switch resp.StatusCode {
	case http.StatusOK:
		var project gitlabProject
		if err := json.Unmarshal(body, &project); err != nil {
			return nil, &ProviderError{Provider: ProviderGitLab, Op: "parse response", Err: err}
		}
		return &project, nil
	case http.StatusNotFound:
		return nil, ErrNotFound
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, ErrAuthFailed
	default:
		return nil, &ProviderError{
			Provider: ProviderGitLab,
			Op:       "get project",
			Err:      fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body)),
		}
	}
}

func (g *GitLabProvider) createRepositoryFromTemplate(ctx context.Context, opts CreateRepoOptions) (*Repository, error) {
	template := opts.Template

	// Get template project ID
	templateProjectID, err := g.getProjectID(ctx, template.Owner, template.Name)
	if err != nil {
		return nil, &ProviderError{Provider: ProviderGitLab, Op: "resolve template", Err: err}
	}

	// Get namespace ID if org is specified
	var namespaceID int
	if opts.Org != "" {
		nsID, err := g.getNamespaceID(ctx, opts.Org)
		if err != nil {
			return nil, &ProviderError{Provider: ProviderGitLab, Op: "resolve namespace", Err: err}
		}
		namespaceID = nsID
	}

	// Resolve the group that contains the template project
	var templateGroupID int
	if template.Owner != "" {
		tgID, err := g.getNamespaceID(ctx, template.Owner)
		if err != nil {
			return nil, &ProviderError{Provider: ProviderGitLab, Op: "resolve template group", Err: err}
		}
		templateGroupID = tgID
	}

	url := fmt.Sprintf("%s/projects", g.host)

	visibility := "public"
	if opts.Private {
		visibility = "private"
	}

	reqBody := gitlabCreateProjectRequest{
		Name:                        opts.Name,
		Description:                 opts.Description,
		NamespaceID:                 namespaceID,
		Visibility:                  visibility,
		UseCustomTemplate:           true,
		TemplateProjectID:           templateProjectID,
		GroupWithProjectTemplatesID: templateGroupID,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, &ProviderError{Provider: ProviderGitLab, Op: "marshal request", Err: err}
	}

	if os.Getenv("EXOHUB_CLI_DEBUG") == "1" {
		fmt.Fprintf(os.Stderr, "DEBUG: POST %s\n", url)
		fmt.Fprintf(os.Stderr, "DEBUG: Request body: %s\n", string(body))
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, &ProviderError{Provider: ProviderGitLab, Op: "create request", Err: err}
	}

	req.Header.Set("Authorization", "Bearer "+g.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, &ProviderError{Provider: ProviderGitLab, Op: "create from template", Err: fmt.Errorf("%w: %v", ErrNetwork, err)}
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	switch resp.StatusCode {
	case http.StatusCreated:
		var project gitlabProject
		if err := json.Unmarshal(respBody, &project); err != nil {
			return nil, &ProviderError{Provider: ProviderGitLab, Op: "parse response", Err: err}
		}
		return &Repository{
			Name:     project.Name,
			CloneURL: project.HTTPURLToRepo,
			HTTPURL:  project.WebURL,
			SSHURL:   project.SSHURLToRepo,
		}, nil
	case http.StatusBadRequest:
		lower := strings.ToLower(string(respBody))
		if strings.Contains(lower, "has already been taken") {
			return nil, &ProviderError{Provider: ProviderGitLab, Op: "create from template", Err: ErrAlreadyExists}
		}
		return nil, &ProviderError{
			Provider: ProviderGitLab,
			Op:       "create from template",
			Err:      fmt.Errorf("validation failed: status %d, response: %s", resp.StatusCode, string(respBody)),
		}
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, &ProviderError{Provider: ProviderGitLab, Op: "create from template", Err: ErrAuthFailed}
	default:
		return nil, &ProviderError{
			Provider: ProviderGitLab,
			Op:       "create from template",
			Err:      fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(respBody)),
		}
	}
}

// RepositoryExists checks if a project exists in GitLab
func (g *GitLabProvider) RepositoryExists(ctx context.Context, org, name string) (bool, error) {
	_, err := g.GetRepository(ctx, org, name)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// GetRepository fetches project details from GitLab
func (g *GitLabProvider) GetRepository(ctx context.Context, org, name string) (*Repository, error) {
	// Handle case where org might be a full URL (legacy .exohub/context files)
	org = extractNamespaceFromURL(org)

	// URL-encode the project path (namespace/project-name)
	projectPath := fmt.Sprintf("%s/%s", org, name)
	encodedPath := url.PathEscape(projectPath)

	url := fmt.Sprintf("%s/projects/%s", g.host, encodedPath)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, &ProviderError{Provider: ProviderGitLab, Op: "create request", Err: err}
	}

	req.Header.Set("Authorization", "Bearer "+g.token)

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, &ProviderError{Provider: ProviderGitLab, Op: "get project", Err: fmt.Errorf("%w: %v", ErrNetwork, err)}
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	switch resp.StatusCode {
	case http.StatusOK:
		var project gitlabProject
		if err := json.Unmarshal(body, &project); err != nil {
			return nil, &ProviderError{Provider: ProviderGitLab, Op: "parse response", Err: err}
		}
		return &Repository{
			Name:     project.Name,
			CloneURL: project.HTTPURLToRepo,
			HTTPURL:  project.WebURL,
			SSHURL:   project.SSHURLToRepo,
		}, nil
	case http.StatusNotFound:
		return nil, &ProviderError{Provider: ProviderGitLab, Op: "get project", Err: ErrNotFound}
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, &ProviderError{Provider: ProviderGitLab, Op: "get project", Err: ErrAuthFailed}
	default:
		return nil, &ProviderError{
			Provider: ProviderGitLab,
			Op:       "get project",
			Err:      fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body)),
		}
	}
}

// extractNamespaceFromURL extracts the namespace path from a full URL or returns the path as-is
// Handles cases like: "https://gitlab.com/group/subgroup/repo" -> "group/subgroup"
// or "group/subgroup" -> "group/subgroup"
func extractNamespaceFromURL(input string) string {
	// If it doesn't look like a URL, return as-is
	if !strings.Contains(input, "://") {
		return input
	}

	// Parse as URL
	parsedURL, err := url.Parse(input)
	if err != nil {
		// If parsing fails, return as-is
		return input
	}

	// Extract the path, trim leading/trailing slashes
	path := strings.Trim(parsedURL.Path, "/")

	// Remove the last segment (repo name) if present, keeping only the namespace
	// For "group/subgroup/repo", we want "group/subgroup"
	parts := strings.Split(path, "/")
	if len(parts) > 1 {
		// Keep all but the last part (assuming last part is repo name)
		// But we actually need to be careful here - we might want the full path
		// Let's just return the full path and let GitLab API handle it
		return path
	}

	return path
}

// getNamespaceID resolves a namespace path to its ID
func (g *GitLabProvider) getNamespaceID(ctx context.Context, namespacePath string) (int, error) {
	// Handle case where namespacePath might be a full URL (legacy .exohub/context files)
	// Extract just the path portion if it's a URL
	namespacePath = extractNamespaceFromURL(namespacePath)

	encodedPath := url.PathEscape(namespacePath)
	url := fmt.Sprintf("%s/namespaces/%s", g.host, encodedPath)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return 0, err
	}

	req.Header.Set("Authorization", "Bearer "+g.token)

	resp, err := g.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	switch resp.StatusCode {
	case http.StatusOK:
		var namespace gitlabNamespace
		if err := json.Unmarshal(body, &namespace); err != nil {
			return 0, fmt.Errorf("parse response: %w", err)
		}
		return namespace.ID, nil
	case http.StatusNotFound:
		return 0, fmt.Errorf("namespace not found: %s", namespacePath)
	case http.StatusUnauthorized, http.StatusForbidden:
		return 0, fmt.Errorf("authentication failed")
	default:
		return 0, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}
}

// getProjectID resolves a project path to its ID
func (g *GitLabProvider) getProjectID(ctx context.Context, namespace, projectName string) (int, error) {
	// Handle case where namespace might be a full URL
	namespace = extractNamespaceFromURL(namespace)

	projectPath := fmt.Sprintf("%s/%s", namespace, projectName)
	encodedPath := url.PathEscape(projectPath)
	url := fmt.Sprintf("%s/projects/%s", g.host, encodedPath)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return 0, err
	}

	req.Header.Set("Authorization", "Bearer "+g.token)

	resp, err := g.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	switch resp.StatusCode {
	case http.StatusOK:
		var project gitlabProject
		if err := json.Unmarshal(body, &project); err != nil {
			return 0, fmt.Errorf("parse response: %w", err)
		}
		return project.ID, nil
	case http.StatusNotFound:
		return 0, fmt.Errorf("project not found: %s", projectPath)
	case http.StatusUnauthorized, http.StatusForbidden:
		return 0, fmt.Errorf("authentication failed")
	default:
		return 0, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}
}
