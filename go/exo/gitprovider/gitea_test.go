package gitprovider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGiteaCreateRepository(t *testing.T) {
	tests := []struct {
		name           string
		opts           CreateRepoOptions
		responseStatus int
		responseBody   giteaRepository
		expectError    bool
		errorType      error
	}{
		{
			name: "Success",
			opts: CreateRepoOptions{
				Org:         "testorg",
				Name:        "testrepo",
				Description: "Test description",
				Visibility:  "private",
			},
			responseStatus: http.StatusCreated,
			responseBody: giteaRepository{
				Name:     "testrepo",
				CloneURL: "https://git.example.com/testorg/testrepo.git",
				HTMLURL:  "https://git.example.com/testorg/testrepo",
				SSHURL:   "git@git.example.com:testorg/testrepo.git",
			},
			expectError: false,
		},
		{
			name: "Repository already exists",
			opts: CreateRepoOptions{
				Org:  "testorg",
				Name: "existing",
			},
			responseStatus: http.StatusConflict,
			expectError:    true,
			errorType:      ErrAlreadyExists,
		},
		{
			name: "Authentication failed",
			opts: CreateRepoOptions{
				Org:  "testorg",
				Name: "testrepo",
			},
			responseStatus: http.StatusUnauthorized,
			expectError:    true,
			errorType:      ErrAuthFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Verify request method and path
				if r.Method != "POST" {
					t.Errorf("Expected POST, got %s", r.Method)
				}
				if r.URL.Path != "/api/v1/org/"+tt.opts.Org+"/repos" {
					t.Errorf("Expected path /api/v1/org/%s/repos, got %s", tt.opts.Org, r.URL.Path)
				}

				// Verify auth header
				auth := r.Header.Get("Authorization")
				if auth != "token test-token" {
					t.Errorf("Expected auth token, got %s", auth)
				}

				w.WriteHeader(tt.responseStatus)
				if tt.responseStatus == http.StatusCreated {
					json.NewEncoder(w).Encode(tt.responseBody)
				}
			}))
			defer server.Close()

			provider := NewGiteaProvider(server.URL, "test-token")
			repo, err := provider.CreateRepository(context.Background(), tt.opts)

			if tt.expectError {
				if err == nil {
					t.Error("Expected error, got nil")
				}
				// Check error type if specified
				if tt.errorType != nil {
					provErr, ok := err.(*ProviderError)
					if !ok {
						t.Errorf("Expected ProviderError, got %T", err)
					} else if provErr.Err != tt.errorType && !contains(provErr.Err.Error(), tt.errorType.Error()) {
						t.Errorf("Expected error containing %v, got %v", tt.errorType, provErr.Err)
					}
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if repo == nil {
					t.Fatal("Expected repository, got nil")
				}
				if repo.Name != tt.responseBody.Name {
					t.Errorf("Expected name %s, got %s", tt.responseBody.Name, repo.Name)
				}
				if repo.SSHURL != tt.responseBody.SSHURL {
					t.Errorf("Expected SSH URL %s, got %s", tt.responseBody.SSHURL, repo.SSHURL)
				}
			}
		})
	}
}

func TestGiteaRepositoryExists(t *testing.T) {
	tests := []struct {
		name           string
		org            string
		repoName       string
		responseStatus int
		expectedExists bool
		expectError    bool
	}{
		{
			name:           "Repository exists",
			org:            "testorg",
			repoName:       "testrepo",
			responseStatus: http.StatusOK,
			expectedExists: true,
			expectError:    false,
		},
		{
			name:           "Repository does not exist",
			org:            "testorg",
			repoName:       "nonexistent",
			responseStatus: http.StatusNotFound,
			expectedExists: false,
			expectError:    false,
		},
		{
			name:           "Authentication failed",
			org:            "testorg",
			repoName:       "testrepo",
			responseStatus: http.StatusUnauthorized,
			expectedExists: false,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Errorf("Expected GET, got %s", r.Method)
				}
				w.WriteHeader(tt.responseStatus)
				if tt.responseStatus == http.StatusOK {
					json.NewEncoder(w).Encode(giteaRepository{
						Name:     tt.repoName,
						CloneURL: "https://git.example.com/" + tt.org + "/" + tt.repoName + ".git",
						HTMLURL:  "https://git.example.com/" + tt.org + "/" + tt.repoName,
						SSHURL:   "git@git.example.com:" + tt.org + "/" + tt.repoName + ".git",
					})
				}
			}))
			defer server.Close()

			provider := NewGiteaProvider(server.URL, "test-token")
			exists, err := provider.RepositoryExists(context.Background(), tt.org, tt.repoName)

			if tt.expectError {
				if err == nil {
					t.Error("Expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if exists != tt.expectedExists {
					t.Errorf("Expected exists=%v, got %v", tt.expectedExists, exists)
				}
			}
		})
	}
}

func TestGiteaGetRepository(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(giteaRepository{
			Name:     "testrepo",
			CloneURL: "https://git.example.com/testorg/testrepo.git",
			HTMLURL:  "https://git.example.com/testorg/testrepo",
			SSHURL:   "ssh://git@git.example.com:2222/testorg/testrepo.git",
		})
	}))
	defer server.Close()

	provider := NewGiteaProvider(server.URL, "test-token")
	repo, err := provider.GetRepository(context.Background(), "testorg", "testrepo")

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if repo.SSHURL != "ssh://git@git.example.com:2222/testorg/testrepo.git" {
		t.Errorf("Expected custom SSH URL with port, got %s", repo.SSHURL)
	}
}

func TestGiteaListOrgTemplates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/orgs/testorg/repos" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode([]giteaRepository{
			{Name: "template-a", Description: "Template A", Template: true},
			{Name: "normal-repo", Description: "Normal", Template: false},
			{Name: "template-b", Description: "", Template: true},
		})
	}))
	defer server.Close()

	provider := NewGiteaProvider(server.URL, "test-token")
	templates, err := provider.ListOrgTemplates(context.Background(), "testorg")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(templates) != 2 {
		t.Fatalf("expected 2 templates, got %d", len(templates))
	}
	if templates[0].Name != "template-a" || templates[1].Name != "template-b" {
		t.Fatalf("unexpected templates: %+v", templates)
	}
}

func TestGiteaCreateRepositoryFromTemplate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/api/v1/repos/testorg/template-a/generate" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		var payload giteaGenerateRepoRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if payload.Owner != "destorg" || payload.Name != "newrepo" || !payload.GitContent || !payload.GitHooks || !payload.Labels || !payload.Topics || !payload.Webhooks || !payload.Avatar {
			t.Fatalf("unexpected payload: %+v", payload)
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(giteaRepository{
			Name:     "newrepo",
			CloneURL: "https://git.example.com/destorg/newrepo.git",
			HTMLURL:  "https://git.example.com/destorg/newrepo",
			SSHURL:   "git@git.example.com:destorg/newrepo.git",
		})
	}))
	defer server.Close()

	provider := NewGiteaProvider(server.URL, "test-token")
	repo, err := provider.CreateRepository(context.Background(), CreateRepoOptions{
		Org:        "destorg",
		Name:       "newrepo",
		Visibility: "private",
		Template:   &RepoTemplate{Owner: "testorg", Name: "template-a"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.Name != "newrepo" {
		t.Fatalf("unexpected repo: %+v", repo)
	}
}

func TestGiteaCreateRepositoryFromTemplateValidationError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write([]byte(`{"message":"template not found"}`))
	}))
	defer server.Close()

	provider := NewGiteaProvider(server.URL, "test-token")
	_, err := provider.CreateRepository(context.Background(), CreateRepoOptions{
		Org:        "destorg",
		Name:       "newrepo",
		Visibility: "private",
		Template:   &RepoTemplate{Owner: "testorg", Name: "template-a"},
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "validation failed") {
		t.Fatalf("expected validation failed error, got %v", err)
	}
}

func TestGiteaResolveTemplateFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode([]giteaRepository{
			{Name: "template-a", Description: "Template A", Template: true},
			{Name: "template-b", Description: "Template B", Template: true},
			{Name: "regular", Description: "Regular", Template: false},
		})
	}))
	defer server.Close()

	provider := NewGiteaProvider(server.URL, "test-token")
	tmpl, err := provider.ResolveTemplate(context.Background(), "testorg", "template-b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tmpl.Name != "template-b" {
		t.Errorf("expected name 'template-b', got %q", tmpl.Name)
	}
	if tmpl.Owner != "testorg" {
		t.Errorf("expected owner 'testorg', got %q", tmpl.Owner)
	}
}

func TestGiteaResolveTemplateNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode([]giteaRepository{
			{Name: "template-a", Template: true},
		})
	}))
	defer server.Close()

	provider := NewGiteaProvider(server.URL, "test-token")
	_, err := provider.ResolveTemplate(context.Background(), "testorg", "nonexistent")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && (s[:len(substr)] == substr || s[len(s)-len(substr):] == substr || containsMiddle(s, substr)))
}

func containsMiddle(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
