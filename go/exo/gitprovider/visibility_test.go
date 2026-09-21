package gitprovider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestGitLabCreateRepositoryVisibility pins the visibility sent to the GitLab
// API. GitLab is the only provider with an "internal" level, and an unset
// value must default to internal rather than private or public.
func TestGitLabCreateRepositoryVisibility(t *testing.T) {
	tests := []struct {
		name       string
		visibility string
		want       string
	}{
		{name: "unset defaults to internal", visibility: "", want: "internal"},
		{name: "internal is passed through", visibility: "internal", want: "internal"},
		{name: "private is passed through", visibility: "private", want: "private"},
		{name: "public is passed through", visibility: "public", want: "public"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v4/namespaces/mygroup" {
					w.WriteHeader(http.StatusOK)
					json.NewEncoder(w).Encode(gitlabNamespace{ID: 123})
					return
				}
				if r.URL.Path == "/api/v4/projects" && r.Method == http.MethodPost {
					var req gitlabCreateProjectRequest
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Fatalf("decode request: %v", err)
					}
					got = req.Visibility
					w.WriteHeader(http.StatusCreated)
					json.NewEncoder(w).Encode(gitlabProject{
						Name:          "test-repo",
						HTTPURLToRepo: "https://gitlab.example.com/mygroup/test-repo.git",
					})
					return
				}
				w.WriteHeader(http.StatusNotFound)
			}))
			defer server.Close()

			provider := NewGitLabProvider(server.URL, "test-token")
			_, err := provider.CreateRepository(context.Background(), CreateRepoOptions{
				Org:        "mygroup",
				Name:       "test-repo",
				Visibility: tt.visibility,
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("visibility = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestGitHubCreateRepositoryVisibility checks that GitHub, which has no
// "internal" level, collapses it to private rather than leaking it as public.
func TestGitHubCreateRepositoryVisibility(t *testing.T) {
	tests := []struct {
		name        string
		visibility  string
		wantPrivate bool
	}{
		{name: "unset is private", visibility: "", wantPrivate: true},
		{name: "internal falls back to private", visibility: "internal", wantPrivate: true},
		{name: "private stays private", visibility: "private", wantPrivate: true},
		{name: "public stays public", visibility: "public", wantPrivate: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got githubCreateRepoRequest
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// CreateRepository first resolves whether the account is an org.
				if r.URL.Path == "/orgs/testorg" && r.Method == http.MethodGet {
					w.WriteHeader(http.StatusOK)
					w.Write([]byte(`{"login":"testorg"}`))
					return
				}
				if r.URL.Path == "/orgs/testorg/repos" && r.Method == http.MethodPost {
					if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
						t.Fatalf("decode request: %v", err)
					}
					w.WriteHeader(http.StatusCreated)
					json.NewEncoder(w).Encode(githubRepository{Name: "test-repo"})
					return
				}
				w.WriteHeader(http.StatusNotFound)
			}))
			defer server.Close()

			provider := NewGitHubProvider(server.URL, "test-token")
			_, err := provider.CreateRepository(context.Background(), CreateRepoOptions{
				Org:        "testorg",
				Name:       "test-repo",
				Visibility: tt.visibility,
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Private != tt.wantPrivate {
				t.Errorf("private = %v, want %v", got.Private, tt.wantPrivate)
			}
		})
	}
}

// TestGiteaCreateRepositoryVisibility checks the same collapse for Gitea.
func TestGiteaCreateRepositoryVisibility(t *testing.T) {
	tests := []struct {
		name        string
		visibility  string
		wantPrivate bool
	}{
		{name: "unset is private", visibility: "", wantPrivate: true},
		{name: "internal falls back to private", visibility: "internal", wantPrivate: true},
		{name: "private stays private", visibility: "private", wantPrivate: true},
		{name: "public stays public", visibility: "public", wantPrivate: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got giteaCreateRepoRequest
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Fatalf("decode request: %v", err)
				}
				w.WriteHeader(http.StatusCreated)
				json.NewEncoder(w).Encode(map[string]string{
					"name":      "test-repo",
					"clone_url": "https://gitea.example.com/testorg/test-repo.git",
				})
			}))
			defer server.Close()

			provider := NewGiteaProvider(server.URL, "test-token")
			_, err := provider.CreateRepository(context.Background(), CreateRepoOptions{
				Org:        "testorg",
				Name:       "test-repo",
				Visibility: tt.visibility,
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Private != tt.wantPrivate {
				t.Errorf("private = %v, want %v", got.Private, tt.wantPrivate)
			}
		})
	}
}
