package gitprovider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExtractNamespaceFromURL(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "full URL with group and subgroup",
			input:    "https://gitlab.com/exohub/data-repositories/cancerdb",
			expected: "exohub/data-repositories/cancerdb",
		},
		{
			name:     "full URL with single group",
			input:    "https://gitlab.example.com/mygroup",
			expected: "mygroup",
		},
		{
			name:     "path only (no URL)",
			input:    "group/subgroup",
			expected: "group/subgroup",
		},
		{
			name:     "simple group name",
			input:    "mygroup",
			expected: "mygroup",
		},
		{
			name:     "URL with trailing slash",
			input:    "https://gitlab.com/group/subgroup/",
			expected: "group/subgroup",
		},
		{
			name:     "URL with leading slash",
			input:    "https://gitlab.com/group/subgroup",
			expected: "group/subgroup",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractNamespaceFromURL(tt.input)
			if result != tt.expected {
				t.Errorf("extractNamespaceFromURL(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestParentNamespace(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"a/b/c", "a/b"},
		{"a/b", "a"},
		{"a", ""},
		{"", ""},
		{"exohub/data-repositories/cancerdb", "exohub/data-repositories"},
	}
	for _, tt := range tests {
		got := parentNamespace(tt.input)
		if got != tt.expected {
			t.Errorf("parentNamespace(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestNormalizeGitLabHost(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "empty host defaults to gitlab.com",
			input:    "",
			expected: "https://gitlab.com/api/v4",
		},
		{
			name:     "gitlab.com without protocol",
			input:    "gitlab.com",
			expected: "https://gitlab.com/api/v4",
		},
		{
			name:     "gitlab.com with https",
			input:    "https://gitlab.com",
			expected: "https://gitlab.com/api/v4",
		},
		{
			name:     "self-hosted without protocol",
			input:    "gitlab.example.com",
			expected: "https://gitlab.example.com/api/v4",
		},
		{
			name:     "self-hosted with https",
			input:    "https://gitlab.example.com",
			expected: "https://gitlab.example.com/api/v4",
		},
		{
			name:     "self-hosted with http",
			input:    "http://gitlab.local",
			expected: "http://gitlab.local/api/v4",
		},
		{
			name:     "already has /api/v4",
			input:    "https://gitlab.com/api/v4",
			expected: "https://gitlab.com/api/v4",
		},
		{
			name:     "trailing slash removed",
			input:    "https://gitlab.com/",
			expected: "https://gitlab.com/api/v4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeGitLabHost(tt.input)
			if result != tt.expected {
				t.Errorf("normalizeGitLabHost(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestGitLabProvider_CreateRepository(t *testing.T) {
	tests := []struct {
		name           string
		opts           CreateRepoOptions
		setupServer    func() *httptest.Server
		expectError    bool
		errorContains  string
		validateResult func(*testing.T, *Repository)
	}{
		{
			name: "successful creation",
			opts: CreateRepoOptions{
				Org:         "mygroup",
				Name:        "test-repo",
				Description: "Test repository",
				Private:     false,
			},
			setupServer: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/api/v4/namespaces/mygroup" {
						w.WriteHeader(http.StatusOK)
						json.NewEncoder(w).Encode(gitlabNamespace{
							ID:   123,
							Name: "mygroup",
							Path: "mygroup",
						})
						return
					}
					if r.URL.Path == "/api/v4/projects" && r.Method == "POST" {
						w.WriteHeader(http.StatusCreated)
						json.NewEncoder(w).Encode(gitlabProject{
							ID:            456,
							Name:          "test-repo",
							Path:          "test-repo",
							HTTPURLToRepo: "https://gitlab.com/mygroup/test-repo.git",
							SSHURLToRepo:  "git@gitlab.com:mygroup/test-repo.git",
							WebURL:        "https://gitlab.com/mygroup/test-repo",
						})
						return
					}
					w.WriteHeader(http.StatusNotFound)
				}))
			},
			expectError: false,
			validateResult: func(t *testing.T, repo *Repository) {
				if repo.Name != "test-repo" {
					t.Errorf("expected name 'test-repo', got %q", repo.Name)
				}
				if !strings.Contains(repo.CloneURL, "test-repo.git") {
					t.Errorf("expected clone URL to contain 'test-repo.git', got %q", repo.CloneURL)
				}
			},
		},
		{
			name: "private repository",
			opts: CreateRepoOptions{
				Org:     "mygroup",
				Name:    "private-repo",
				Private: true,
			},
			setupServer: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/api/v4/namespaces/mygroup" {
						w.WriteHeader(http.StatusOK)
						json.NewEncoder(w).Encode(gitlabNamespace{ID: 123})
						return
					}
					if r.URL.Path == "/api/v4/projects" && r.Method == "POST" {
						var req gitlabCreateProjectRequest
						json.NewDecoder(r.Body).Decode(&req)
						if req.Visibility != "private" {
							t.Errorf("expected visibility 'private', got %q", req.Visibility)
						}
						w.WriteHeader(http.StatusCreated)
						json.NewEncoder(w).Encode(gitlabProject{
							Name:          "private-repo",
							HTTPURLToRepo: "https://gitlab.com/mygroup/private-repo.git",
						})
						return
					}
					w.WriteHeader(http.StatusNotFound)
				}))
			},
			expectError: false,
		},
		{
			name: "project already exists",
			opts: CreateRepoOptions{
				Org:  "mygroup",
				Name: "existing-repo",
			},
			setupServer: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/api/v4/namespaces/mygroup" {
						w.WriteHeader(http.StatusOK)
						json.NewEncoder(w).Encode(gitlabNamespace{ID: 123})
						return
					}
					if r.URL.Path == "/api/v4/projects" {
						w.WriteHeader(http.StatusBadRequest)
						json.NewEncoder(w).Encode(map[string]interface{}{
							"message": map[string][]string{
								"name": {"has already been taken"},
							},
						})
						return
					}
					w.WriteHeader(http.StatusNotFound)
				}))
			},
			expectError:   true,
			errorContains: "already exists",
		},
		{
			name: "authentication failed",
			opts: CreateRepoOptions{
				Org:  "mygroup",
				Name: "test-repo",
			},
			setupServer: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/api/v4/namespaces/mygroup" {
						w.WriteHeader(http.StatusOK)
						json.NewEncoder(w).Encode(gitlabNamespace{ID: 123})
						return
					}
					w.WriteHeader(http.StatusUnauthorized)
					json.NewEncoder(w).Encode(map[string]string{
						"message": "401 Unauthorized",
					})
				}))
			},
			expectError:   true,
			errorContains: "authentication failed",
		},
		{
			name: "namespace not found",
			opts: CreateRepoOptions{
				Org:  "nonexistent",
				Name: "test-repo",
			},
			setupServer: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusNotFound)
					json.NewEncoder(w).Encode(map[string]string{
						"message": "404 Namespace Not Found",
					})
				}))
			},
			expectError:   true,
			errorContains: "namespace not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := tt.setupServer()
			defer server.Close()

			provider := NewGitLabProvider(server.URL, "test-token")
			repo, err := provider.CreateRepository(context.Background(), tt.opts)

			if tt.expectError {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errorContains != "" && !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tt.errorContains)) {
					t.Errorf("expected error to contain %q, got %q", tt.errorContains, err.Error())
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if tt.validateResult != nil {
					tt.validateResult(t, repo)
				}
			}
		})
	}
}

func TestGitLabProvider_GetRepository(t *testing.T) {
	tests := []struct {
		name          string
		org           string
		repoName      string
		setupServer   func() *httptest.Server
		expectError   bool
		errorContains string
	}{
		{
			name:     "successful get",
			org:      "mygroup",
			repoName: "test-repo",
			setupServer: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					// URL encoding happens in the URL construction, not in the path
					// so the path comes decoded as /api/v4/projects/mygroup/test-repo
					if strings.HasSuffix(r.URL.Path, "/projects/mygroup/test-repo") {
						w.WriteHeader(http.StatusOK)
						json.NewEncoder(w).Encode(gitlabProject{
							Name:          "test-repo",
							HTTPURLToRepo: "https://gitlab.com/mygroup/test-repo.git",
						})
						return
					}
					w.WriteHeader(http.StatusNotFound)
				}))
			},
			expectError: false,
		},
		{
			name:     "project not found",
			org:      "mygroup",
			repoName: "nonexistent",
			setupServer: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusNotFound)
				}))
			},
			expectError:   true,
			errorContains: "not found",
		},
		{
			name:     "nested namespace",
			org:      "mygroup/subgroup",
			repoName: "test-repo",
			setupServer: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if strings.HasSuffix(r.URL.Path, "/projects/mygroup/subgroup/test-repo") {
						w.WriteHeader(http.StatusOK)
						json.NewEncoder(w).Encode(gitlabProject{
							Name:          "test-repo",
							HTTPURLToRepo: "https://gitlab.com/mygroup/subgroup/test-repo.git",
						})
						return
					}
					w.WriteHeader(http.StatusNotFound)
				}))
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := tt.setupServer()
			defer server.Close()

			provider := NewGitLabProvider(server.URL, "test-token")
			repo, err := provider.GetRepository(context.Background(), tt.org, tt.repoName)

			if tt.expectError {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errorContains != "" && !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tt.errorContains)) {
					t.Errorf("expected error to contain %q, got %q", tt.errorContains, err.Error())
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if repo == nil {
					t.Fatal("expected repository, got nil")
				}
			}
		})
	}
}

func TestGitLabProvider_RepositoryExists(t *testing.T) {
	tests := []struct {
		name        string
		org         string
		repoName    string
		setupServer func() *httptest.Server
		expected    bool
	}{
		{
			name:     "repository exists",
			org:      "mygroup",
			repoName: "test-repo",
			setupServer: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusOK)
					json.NewEncoder(w).Encode(gitlabProject{Name: "test-repo"})
				}))
			},
			expected: true,
		},
		{
			name:     "repository does not exist",
			org:      "mygroup",
			repoName: "nonexistent",
			setupServer: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusNotFound)
				}))
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := tt.setupServer()
			defer server.Close()

			provider := NewGitLabProvider(server.URL, "test-token")
			exists, err := provider.RepositoryExists(context.Background(), tt.org, tt.repoName)

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if exists != tt.expected {
				t.Errorf("expected exists=%v, got %v", tt.expected, exists)
			}
		})
	}
}

func TestGitLabProvider_ListOrgTemplates(t *testing.T) {
	tests := []struct {
		name          string
		org           string
		setupServer   func() *httptest.Server
		expectError   bool
		expectedCount int
		expectedOwner string
		expectedName  string
	}{
		{
			name: "list templates from exohub-templates subgroup",
			org:  "mygroup",
			setupServer: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					path := r.URL.Path
					// mygroup/exohub-templates namespace exists
					if strings.HasSuffix(path, "/namespaces/mygroup/exohub-templates") {
						w.WriteHeader(http.StatusOK)
						json.NewEncoder(w).Encode(gitlabNamespace{ID: 50})
						return
					}
					// Projects in the exohub-templates subgroup
					if strings.Contains(path, "groups/50/projects") {
						w.WriteHeader(http.StatusOK)
						json.NewEncoder(w).Encode([]gitlabProject{
							{Name: "Template 1", Path: "template-1", Description: "Template 1", PathWithNamespace: "mygroup/exohub-templates/template-1"},
							{Name: "Template 2", Path: "template-2", Description: "Template 2", PathWithNamespace: "mygroup/exohub-templates/template-2"},
						})
						return
					}
					w.WriteHeader(http.StatusNotFound)
				}))
			},
			expectError:   false,
			expectedCount: 2,
		},
		{
			name: "no exohub-templates subgroup",
			org:  "mygroup",
			setupServer: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					// All namespace lookups return not found
					w.WriteHeader(http.StatusNotFound)
				}))
			},
			expectError:   false,
			expectedCount: 0,
		},
		{
			name: "find templates in parent group exohub-templates",
			org:  "exohub/data-repositories/cancerdb",
			setupServer: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					path := r.URL.Path
					// exohub/data-repositories/cancerdb/exohub-templates does not exist
					// exohub/data-repositories/exohub-templates exists
					if strings.HasSuffix(path, "/namespaces/exohub/data-repositories/exohub-templates") {
						w.WriteHeader(http.StatusOK)
						json.NewEncoder(w).Encode(gitlabNamespace{ID: 200})
						return
					}
					// Projects in parent's exohub-templates subgroup
					if strings.Contains(path, "groups/200/projects") {
						w.WriteHeader(http.StatusOK)
						json.NewEncoder(w).Encode([]gitlabProject{
							{Name: "CancerDB Template", Path: "cancerdb-template", Description: "Cancer DB template", PathWithNamespace: "exohub/data-repositories/exohub-templates/cancerdb-template"},
						})
						return
					}
					// Everything else not found (including cancerdb/exohub-templates)
					w.WriteHeader(http.StatusNotFound)
				}))
			},
			expectError:   false,
			expectedCount: 1,
			expectedOwner: "exohub/data-repositories/exohub-templates",
			expectedName:  "cancerdb-template",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := tt.setupServer()
			defer server.Close()

			provider := NewGitLabProvider(server.URL, "test-token")
			templates, err := provider.ListOrgTemplates(context.Background(), tt.org)

			if tt.expectError {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if len(templates) != tt.expectedCount {
					t.Errorf("expected %d templates, got %d", tt.expectedCount, len(templates))
				}
				if tt.expectedOwner != "" && len(templates) > 0 {
					if templates[0].Owner != tt.expectedOwner {
						t.Errorf("expected owner %q, got %q", tt.expectedOwner, templates[0].Owner)
					}
				}
				if tt.expectedName != "" && len(templates) > 0 {
					if templates[0].Name != tt.expectedName {
						t.Errorf("expected name %q, got %q", tt.expectedName, templates[0].Name)
					}
				}
			}
		})
	}
}

func TestGitLabProvider_CreateRepositoryFromTemplate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/namespaces/mygroup") {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(gitlabNamespace{ID: 123})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/projects/mygroup/template-repo") {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(gitlabProject{ID: 999, Name: "template-repo"})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/projects") && r.Method == "POST" {
			var req gitlabCreateProjectRequest
			json.NewDecoder(r.Body).Decode(&req)
			if !req.UseCustomTemplate || req.TemplateProjectID != 999 {
				t.Errorf("expected template creation with ID 999, got UseCustomTemplate=%v, TemplateProjectID=%d",
					req.UseCustomTemplate, req.TemplateProjectID)
			}
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(gitlabProject{
				Name:          req.Name,
				HTTPURLToRepo: "https://gitlab.com/mygroup/" + req.Name + ".git",
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	provider := NewGitLabProvider(server.URL, "test-token")
	opts := CreateRepoOptions{
		Org:  "mygroup",
		Name: "new-from-template",
		Template: &RepoTemplate{
			Owner: "mygroup",
			Name:  "template-repo",
		},
	}

	repo, err := provider.CreateRepository(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.Name != "new-from-template" {
		t.Errorf("expected name 'new-from-template', got %q", repo.Name)
	}
}

func TestGitLabProvider_CreateRepositoryFromTemplateWithGroupID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		// Resolve target namespace
		if strings.HasSuffix(path, "/namespaces/exohub/data-repositories") {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(gitlabNamespace{ID: 100})
			return
		}
		// Resolve template group namespace
		if strings.HasSuffix(path, "/namespaces/exohub/data-repositories/exohub-templates") {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(gitlabNamespace{ID: 200})
			return
		}
		// Resolve template project ID
		if strings.HasSuffix(path, "/projects/exohub/data-repositories/exohub-templates/cancerdb-template") {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(gitlabProject{ID: 555, Name: "CancerDB Template", Path: "cancerdb-template"})
			return
		}
		// Create project — verify group_with_project_templates_id is set
		if strings.HasSuffix(path, "/projects") && r.Method == "POST" {
			var req gitlabCreateProjectRequest
			json.NewDecoder(r.Body).Decode(&req)
			if !req.UseCustomTemplate {
				t.Errorf("expected UseCustomTemplate=true")
			}
			if req.TemplateProjectID != 555 {
				t.Errorf("expected TemplateProjectID=555, got %d", req.TemplateProjectID)
			}
			if req.GroupWithProjectTemplatesID != 200 {
				t.Errorf("expected GroupWithProjectTemplatesID=200, got %d", req.GroupWithProjectTemplatesID)
			}
			if req.NamespaceID != 100 {
				t.Errorf("expected NamespaceID=100, got %d", req.NamespaceID)
			}
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(gitlabProject{
				Name:          "test-repo",
				HTTPURLToRepo: "https://gitlab.com/exohub/data-repositories/test-repo.git",
				WebURL:        "https://gitlab.com/exohub/data-repositories/test-repo",
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	provider := NewGitLabProvider(server.URL, "test-token")
	repo, err := provider.CreateRepository(context.Background(), CreateRepoOptions{
		Org:  "exohub/data-repositories",
		Name: "test-repo",
		Template: &RepoTemplate{
			Owner: "exohub/data-repositories/exohub-templates",
			Name:  "cancerdb-template",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.Name != "test-repo" {
		t.Errorf("expected name 'test-repo', got %q", repo.Name)
	}
}

func TestGitLabProvider_ResolveTemplateAuthError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	provider := NewGitLabProvider(server.URL, "bad-token")
	_, err := provider.ResolveTemplate(context.Background(), "mygroup", "some-template")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestGitLabProvider_ListOrgTemplatesAuthError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		// Namespace resolves fine
		if strings.Contains(path, "/namespaces/") {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(gitlabNamespace{ID: 50})
			return
		}
		// But project listing returns auth error
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	provider := NewGitLabProvider(server.URL, "bad-token")
	_, err := provider.ListOrgTemplates(context.Background(), "mygroup")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestGitLabProvider_ListOrgTemplatesEmptySubgroup(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if strings.HasSuffix(path, "/namespaces/mygroup/exohub-templates") {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(gitlabNamespace{ID: 50})
			return
		}
		if strings.Contains(path, "groups/50/projects") {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode([]gitlabProject{})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	provider := NewGitLabProvider(server.URL, "test-token")
	templates, err := provider.ListOrgTemplates(context.Background(), "mygroup")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(templates) != 0 {
		t.Errorf("expected 0 templates, got %d", len(templates))
	}
}

func TestGitLabProvider_ResolveTemplate(t *testing.T) {
	tests := []struct {
		name          string
		org           string
		templateName  string
		setupServer   func() *httptest.Server
		expectError   bool
		expectedOwner string
		expectedName  string
	}{
		{
			name:         "resolve from exohub-templates subgroup",
			org:          "exohub/data-repositories",
			templateName: "cancerdb-template",
			setupServer: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					path := r.URL.Path
					// GET /projects/exohub/data-repositories/exohub-templates/cancerdb-template
					if strings.HasSuffix(path, "/projects/exohub/data-repositories/exohub-templates/cancerdb-template") {
						w.WriteHeader(http.StatusOK)
						json.NewEncoder(w).Encode(gitlabProject{
							ID:                1,
							Name:              "CancerDB Template",
							Path:              "cancerdb-template",
							Description:       "Cancer DB template",
							PathWithNamespace:  "exohub/data-repositories/exohub-templates/cancerdb-template",
						})
						return
					}
					w.WriteHeader(http.StatusNotFound)
				}))
			},
			expectError:   false,
			expectedOwner: "exohub/data-repositories/exohub-templates",
			expectedName:  "cancerdb-template",
		},
		{
			name:         "resolve from parent exohub-templates subgroup",
			org:          "exohub/data-repositories/cancerdb",
			templateName: "cancerdb-template",
			setupServer: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					path := r.URL.Path
					// cancerdb/exohub-templates/cancerdb-template does not exist
					// data-repositories/exohub-templates/cancerdb-template exists
					if strings.HasSuffix(path, "/projects/exohub/data-repositories/exohub-templates/cancerdb-template") {
						w.WriteHeader(http.StatusOK)
						json.NewEncoder(w).Encode(gitlabProject{
							ID:                1,
							Name:              "CancerDB Template",
							Path:              "cancerdb-template",
							PathWithNamespace:  "exohub/data-repositories/exohub-templates/cancerdb-template",
						})
						return
					}
					w.WriteHeader(http.StatusNotFound)
				}))
			},
			expectError:   false,
			expectedOwner: "exohub/data-repositories/exohub-templates",
			expectedName:  "cancerdb-template",
		},
		{
			name:         "resolve direct project fallback",
			org:          "mygroup",
			templateName: "my-template",
			setupServer: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					path := r.URL.Path
					// exohub-templates subgroup doesn't have it, but direct path works
					if strings.HasSuffix(path, "/projects/mygroup/my-template") {
						w.WriteHeader(http.StatusOK)
						json.NewEncoder(w).Encode(gitlabProject{
							ID:                2,
							Name:              "My Template",
							Path:              "my-template",
							PathWithNamespace:  "mygroup/my-template",
						})
						return
					}
					w.WriteHeader(http.StatusNotFound)
				}))
			},
			expectError:   false,
			expectedOwner: "mygroup",
			expectedName:  "my-template",
		},
		{
			name:         "template not found anywhere",
			org:          "mygroup",
			templateName: "nonexistent",
			setupServer: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusNotFound)
				}))
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := tt.setupServer()
			defer server.Close()

			provider := NewGitLabProvider(server.URL, "test-token")
			tmpl, err := provider.ResolveTemplate(context.Background(), tt.org, tt.templateName)

			if tt.expectError {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if tmpl.Owner != tt.expectedOwner {
					t.Errorf("expected owner %q, got %q", tt.expectedOwner, tmpl.Owner)
				}
				if tmpl.Name != tt.expectedName {
					t.Errorf("expected name %q, got %q", tt.expectedName, tmpl.Name)
				}
			}
		})
	}
}
