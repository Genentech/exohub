package gitprovider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestDetectProvider(t *testing.T) {
	tests := []struct {
		name         string
		headers      map[string]string
		expectedType ProviderType
		expectError  bool
	}{
		{
			name: "Gitea via cookie",
			headers: map[string]string{
				"Set-Cookie": "i_like_gitea=abc123; Path=/",
			},
			expectedType: ProviderGitea,
			expectError:  false,
		},
		{
			name: "GitLab via x-gitlab-meta header",
			headers: map[string]string{
				"X-Gitlab-Meta": `{"correlation_id":"abc"}`,
			},
			expectedType: ProviderGitLab,
			expectError:  false,
		},
		{
			name: "GitLab via cookie",
			headers: map[string]string{
				"Set-Cookie": "_gitlab_session=xyz789; Path=/",
			},
			expectedType: ProviderGitLab,
			expectError:  false,
		},
		{
			name: "GitHub via cookie",
			headers: map[string]string{
				"Set-Cookie": "_gh_sess=xyz789; Path=/",
			},
			expectedType: ProviderGitHub,
			expectError:  false,
		},
		{
			name:         "Unknown provider - all detection methods fail",
			headers:      map[string]string{},
			expectedType: "",
			expectError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test server
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// For API detection tests, return 404 if we want detection to fail
				if tt.expectError && (strings.Contains(r.URL.Path, "/api/") || strings.Contains(r.URL.Path, "/version")) {
					w.WriteHeader(http.StatusNotFound)
					return
				}

				for k, v := range tt.headers {
					w.Header().Set(k, v)
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()

			// Detect provider
			providerType, err := DetectProvider(context.Background(), server.URL, "")

			// Check results
			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if providerType != tt.expectedType {
					t.Errorf("Expected provider %s, got %s", tt.expectedType, providerType)
				}
			}
		})
	}
}

func TestDetectProviderWithExplicitType(t *testing.T) {
	// Set a test token for the test
	oldToken := os.Getenv("EXOHUB_GITEA_TOKEN")
	os.Setenv("EXOHUB_GITEA_TOKEN", "test-token")
	defer func() {
		if oldToken != "" {
			os.Setenv("EXOHUB_GITEA_TOKEN", oldToken)
		} else {
			os.Unsetenv("EXOHUB_GITEA_TOKEN")
		}
	}()

	// When provider is explicitly specified, it should be used
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// This would be called with explicit provider from context
	// Testing that NewProvider accepts explicit types
	provider, err := NewProvider(ProviderGitea, server.URL)
	if err != nil {
		t.Errorf("Unexpected error creating provider: %v", err)
	}
	if provider == nil {
		t.Error("Expected provider, got nil")
	}
}
