package gitprovider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewGitHubProvider(t *testing.T) {
	provider := NewGitHubProvider("", "test-token")
	if provider == nil {
		t.Fatal("NewGitHubProvider should not return nil")
	}
	if provider.host != "https://api.github.com" {
		t.Errorf("expected host to be https://api.github.com, got %s", provider.host)
	}
	if provider.token != "test-token" {
		t.Errorf("expected token to be test-token, got %s", provider.token)
	}
}

func TestNormalizeGitHubHost(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "empty host defaults to api.github.com",
			input:    "",
			expected: "https://api.github.com",
		},
		{
			name:     "github.com converts to api.github.com",
			input:    "https://github.com",
			expected: "https://api.github.com",
		},
		{
			name:     "www.github.com converts to api.github.com",
			input:    "https://www.github.com",
			expected: "https://api.github.com",
		},
		{
			name:     "github.com without protocol",
			input:    "github.com",
			expected: "https://api.github.com",
		},
		{
			name:     "api.github.com stays as is",
			input:    "https://api.github.com",
			expected: "https://api.github.com",
		},
		{
			name:     "custom GitHub Enterprise host stays as is",
			input:    "https://github.example.com",
			expected: "https://github.example.com",
		},
		{
			name:     "host with trailing slash",
			input:    "https://github.com/",
			expected: "https://api.github.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeGitHubHost(tt.input)
			if result != tt.expected {
				t.Errorf("normalizeGitHubHost(%q) = %q, expected %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestIsOrganization(t *testing.T) {
	tests := []struct {
		name          string
		accountName   string
		orgStatus     int
		userStatus    int
		expectedIsOrg bool
		expectError   bool
	}{
		{
			name:          "valid organization",
			accountName:   "myorg",
			orgStatus:     http.StatusOK,
			userStatus:    http.StatusNotFound,
			expectedIsOrg: true,
			expectError:   false,
		},
		{
			name:          "valid user",
			accountName:   "myuser",
			orgStatus:     http.StatusNotFound,
			userStatus:    http.StatusOK,
			expectedIsOrg: false,
			expectError:   false,
		},
		{
			name:          "neither org nor user",
			accountName:   "nonexistent",
			orgStatus:     http.StatusNotFound,
			userStatus:    http.StatusNotFound,
			expectedIsOrg: false,
			expectError:   true,
		},
		{
			name:          "org endpoint error",
			accountName:   "error",
			orgStatus:     http.StatusInternalServerError,
			userStatus:    http.StatusOK,
			expectedIsOrg: false,
			expectError:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a test server
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Check which endpoint is being called
				if r.URL.Path == "/orgs/"+tt.accountName {
					w.WriteHeader(tt.orgStatus)
					if tt.orgStatus == http.StatusOK {
						w.Write([]byte(`{"login":"` + tt.accountName + `"}`))
					}
				} else if r.URL.Path == "/users/"+tt.accountName {
					w.WriteHeader(tt.userStatus)
					if tt.userStatus == http.StatusOK {
						w.Write([]byte(`{"login":"` + tt.accountName + `"}`))
					}
				} else {
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()

			// Create provider with test server URL
			provider := NewGitHubProvider(server.URL, "test-token")

			// Test isOrganization
			isOrg, err := provider.isOrganization(context.Background(), tt.accountName)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if isOrg != tt.expectedIsOrg {
					t.Errorf("isOrganization(%q) = %v, expected %v", tt.accountName, isOrg, tt.expectedIsOrg)
				}
			}
		})
	}
}

func TestGitHubListOrgTemplates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/orgs/testorg/repos" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode([]githubRepository{
			{Name: "template-a", Description: "Template A", IsTemplate: true},
			{Name: "regular", Description: "Regular", IsTemplate: false},
			{Name: "template-b", Description: "", IsTemplate: true},
		})
	}))
	defer server.Close()

	provider := NewGitHubProvider(server.URL, "test-token")
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

func TestGitHubCreateRepositoryFromTemplate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/repos/testorg/template-a/generate" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		var payload githubGenerateRepoRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if payload.Owner != "destorg" || payload.Name != "newrepo" {
			t.Fatalf("unexpected payload: %+v", payload)
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(githubRepository{
			Name:     "newrepo",
			CloneURL: "https://git.example.com/destorg/newrepo.git",
			HTMLURL:  "https://git.example.com/destorg/newrepo",
			SSHURL:   "git@git.example.com:destorg/newrepo.git",
		})
	}))
	defer server.Close()

	provider := NewGitHubProvider(server.URL, "test-token")
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

func TestGitHubResolveTemplateFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode([]githubRepository{
			{Name: "template-a", Description: "Template A", IsTemplate: true},
			{Name: "template-b", Description: "Template B", IsTemplate: true},
			{Name: "regular", Description: "Regular", IsTemplate: false},
		})
	}))
	defer server.Close()

	provider := NewGitHubProvider(server.URL, "test-token")
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

func TestGitHubResolveTemplateNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode([]githubRepository{
			{Name: "template-a", IsTemplate: true},
		})
	}))
	defer server.Close()

	provider := NewGitHubProvider(server.URL, "test-token")
	_, err := provider.ResolveTemplate(context.Background(), "testorg", "nonexistent")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
