package gitprovider

import (
	"context"
	"fmt"
)

// GitProvider defines the interface for git hosting provider operations
type GitProvider interface {
	// CreateRepository creates a new repository
	CreateRepository(ctx context.Context, opts CreateRepoOptions) (*Repository, error)

	// RepositoryExists checks if a repository exists
	RepositoryExists(ctx context.Context, org, name string) (bool, error)

	// GetRepository fetches repository details
	GetRepository(ctx context.Context, org, name string) (*Repository, error)

	// ListOrgTemplates lists repository templates available for an organization.
	// For GitHub/Gitea: returns repos with the template flag set.
	// For GitLab: returns projects from the org's "templates" subgroup.
	ListOrgTemplates(ctx context.Context, org string) ([]RepoTemplate, error)

	// ResolveTemplate resolves a template by name, returning the template if found.
	// For GitHub/Gitea: looks up the template in the org's template repos.
	// For GitLab: tries org/templates/<name>, then org/<name>.
	ResolveTemplate(ctx context.Context, org, name string) (*RepoTemplate, error)
}

// CreateRepoOptions contains options for creating a repository
type CreateRepoOptions struct {
	Org         string
	Name        string
	Description string
	Private     bool
	Template    *RepoTemplate
}

// Repository represents a git repository
type Repository struct {
	Name     string
	CloneURL string
	HTTPURL  string
	SSHURL   string
}

// RepoTemplate represents a repository template
type RepoTemplate struct {
	Owner       string
	Name        string
	Description string
}

// ProviderType represents the type of git provider
type ProviderType string

const (
	ProviderGitea  ProviderType = "gitea"
	ProviderGitHub ProviderType = "github"
	ProviderGitLab ProviderType = "gitlab"
)

// ProviderError represents an error from a git provider
type ProviderError struct {
	Provider ProviderType
	Op       string
	Err      error
}

func (e *ProviderError) Error() string {
	return fmt.Sprintf("%s %s: %v", e.Provider, e.Op, e.Err)
}

func (e *ProviderError) Unwrap() error {
	return e.Err
}

// Common error types
var (
	ErrAlreadyExists = fmt.Errorf("repository already exists")
	ErrAuthFailed    = fmt.Errorf("authentication failed")
	ErrNotFound      = fmt.Errorf("repository not found")
	ErrNetwork       = fmt.Errorf("network error")
)
