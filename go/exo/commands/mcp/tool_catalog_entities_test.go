//go:build artifactdb

package mcp

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"
)

func TestHandleCatalogEntities_Basic(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/entities", func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		if name == "" {
			t.Error("expected name parameter")
		}
		resp := map[string]any{
			"entity": map[string]any{
				"entity_id":   "e1",
				"name":        "BRCA1",
				"type":        "gene",
				"summary":     "BRCA1 is a tumor suppressor gene...",
				"source_docs": []string{"proj:doc.json@v1"},
			},
			"relationships": []map[string]any{
				{
					"rel_id":    "r1",
					"subject":   "BRCA1",
					"predicate": "associated_with",
					"object":    "breast cancer",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	withTestCatalog(t, mux)

	result, err := callTool(handleCatalogEntities, map[string]any{"name": "BRCA1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error")
	}
}

func TestHandleCatalogEntities_MissingName(t *testing.T) {
	os.Setenv("EXOHUB_CATALOG_URL", "http://unused")
	defer os.Unsetenv("EXOHUB_CATALOG_URL")

	result, err := callTool(handleCatalogEntities, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result for missing name parameter")
	}
}

func TestHandleCatalogEntities_WithoutRelationships(t *testing.T) {
	var capturedInclude string
	mux := http.NewServeMux()
	mux.HandleFunc("/entities", func(w http.ResponseWriter, r *http.Request) {
		capturedInclude = r.URL.Query().Get("include_relationships")
		resp := map[string]any{
			"entity": map[string]any{
				"entity_id": "e1",
				"name":      "BRCA1",
				"type":      "gene",
			},
		}
		json.NewEncoder(w).Encode(resp)
	})
	withTestCatalog(t, mux)

	_, err := callTool(handleCatalogEntities, map[string]any{
		"name":                  "BRCA1",
		"include_relationships": false,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedInclude != "false" {
		t.Errorf("expected include_relationships=false, got %q", capturedInclude)
	}
}

func TestHandleCatalogEntities_DefaultIncludeRelationships(t *testing.T) {
	var capturedInclude string
	mux := http.NewServeMux()
	mux.HandleFunc("/entities", func(w http.ResponseWriter, r *http.Request) {
		capturedInclude = r.URL.Query().Get("include_relationships")
		resp := map[string]any{"entity": map[string]any{"name": "BRCA1"}}
		json.NewEncoder(w).Encode(resp)
	})
	withTestCatalog(t, mux)

	_, _ = callTool(handleCatalogEntities, map[string]any{"name": "BRCA1"})

	// When include_relationships defaults to true, we do not set the param
	if capturedInclude != "" {
		t.Errorf("expected include_relationships param not set (default true), got %q", capturedInclude)
	}
}

func TestHandleCatalogEntities_ServerError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/entities", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		json.NewEncoder(w).Encode(map[string]string{"reason": "entity not found"})
	})
	withTestCatalog(t, mux)

	result, err := callTool(handleCatalogEntities, map[string]any{"name": "UNKNOWN"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result for server error")
	}
}

func TestHandleCatalogEntities_NamePassedToURL(t *testing.T) {
	var capturedName string
	mux := http.NewServeMux()
	mux.HandleFunc("/entities", func(w http.ResponseWriter, r *http.Request) {
		capturedName = r.URL.Query().Get("name")
		resp := map[string]any{"entity": map[string]any{"name": capturedName}}
		json.NewEncoder(w).Encode(resp)
	})
	withTestCatalog(t, mux)

	_, _ = callTool(handleCatalogEntities, map[string]any{"name": "Alzheimer's disease"})

	if capturedName != "Alzheimer's disease" {
		t.Errorf("expected name=Alzheimer's disease, got %q", capturedName)
	}
}
