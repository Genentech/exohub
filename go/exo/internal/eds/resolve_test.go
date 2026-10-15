package eds

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResolveDeployment_ByID_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/eds" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(IndexResponse{
			Deployments: []IndexEntry{
				{
					ID:     "huge",
					URL:    "https://exohub.example.com/eds/huge",
					Status: "active",
					Info: Info{
						DeploymentID: "huge",
						Scopes:       []string{"global"},
						Endpoints: Endpoints{
							Download:   "/eds/huge/api/datasets/download",
							Operations: "/eds/huge/api/operations/{id}",
						},
					},
				},
			},
		})
	}))
	defer srv.Close()

	dep, err := ResolveDeployment(srv.Client(), "huge", srv.URL)
	if err != nil {
		t.Fatalf("ResolveDeployment() error = %v", err)
	}
	if dep.URL != "https://exohub.example.com/eds/huge" {
		t.Errorf("dep.URL = %q", dep.URL)
	}
	if dep.Info.DeploymentID != "huge" {
		t.Errorf("dep.Info.DeploymentID = %q", dep.Info.DeploymentID)
	}
}

func TestResolveDeployment_ByID_Unknown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(IndexResponse{
			Deployments: []IndexEntry{
				{ID: "huge", Status: "active"},
				{ID: "my-eds", Status: "active"},
			},
		})
	}))
	defer srv.Close()

	_, err := ResolveDeployment(srv.Client(), "nope", srv.URL)
	if err == nil {
		t.Fatal("ResolveDeployment() expected error for unknown id")
	}
	if !strings.Contains(err.Error(), "my-eds") || !strings.Contains(err.Error(), "huge") {
		t.Errorf("error should list available ids, got: %v", err)
	}
}

func TestResolveDeployment_ByID_NotActive(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(IndexResponse{
			Deployments: []IndexEntry{
				{ID: "broken", Status: "error", Error: "deployment unreachable"},
			},
		})
	}))
	defer srv.Close()

	_, err := ResolveDeployment(srv.Client(), "broken", srv.URL)
	if err == nil {
		t.Fatal("ResolveDeployment() expected error for non-active entry")
	}
	if !strings.Contains(err.Error(), "deployment unreachable") {
		t.Errorf("error should surface entry.Error, got: %v", err)
	}
}

func TestResolveDeployment_ByID_IndexIDNotDeploymentID(t *testing.T) {
	// Multiple index ids can map to the same deployment_id; the index id
	// must be used only to find the entry.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(IndexResponse{
			Deployments: []IndexEntry{
				{ID: "my-eds-tst", URL: "https://tst.example.com", Status: "active",
					Info: Info{DeploymentID: "my-eds"}},
				{ID: "my-eds-dev", URL: "https://dev.example.com", Status: "active",
					Info: Info{DeploymentID: "my-eds"}},
			},
		})
	}))
	defer srv.Close()

	dep, err := ResolveDeployment(srv.Client(), "my-eds-tst", srv.URL)
	if err != nil {
		t.Fatalf("ResolveDeployment() error = %v", err)
	}
	if dep.URL != "https://tst.example.com" {
		t.Errorf("resolved wrong deployment: dep.URL = %q", dep.URL)
	}
	if dep.Info.DeploymentID != "my-eds" {
		t.Errorf("dep.Info.DeploymentID = %q, want my-eds", dep.Info.DeploymentID)
	}
}

func TestResolveDeployment_ByURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(Info{
			DeploymentID: "my-eds",
			Scopes:       []string{"user", "global"},
			Endpoints: Endpoints{
				Download: "/api/datasets/download",
			},
		})
	}))
	defer srv.Close()

	dep, err := ResolveDeployment(srv.Client(), srv.URL, "https://ignored.example.com")
	if err != nil {
		t.Fatalf("ResolveDeployment() error = %v", err)
	}
	if dep.Info.DeploymentID != "my-eds" {
		t.Errorf("dep.Info.DeploymentID = %q", dep.Info.DeploymentID)
	}
	if dep.Info.Endpoints.Download != "/api/datasets/download" {
		t.Errorf("dep.Info.Endpoints.Download = %q", dep.Info.Endpoints.Download)
	}
}
