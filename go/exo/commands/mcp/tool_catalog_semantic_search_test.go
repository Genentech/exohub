//go:build artifactdb

package mcp

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"

	gomcp "github.com/mark3labs/mcp-go/mcp"
)

func TestHandleCatalogSemanticSearch_Basic(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/semantic-search", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		if q == "" {
			t.Error("expected q parameter")
		}
		resp := map[string]any{
			"chunks":        []any{map[string]any{"chunk_id": "c1", "text": "rnaseq data", "score": 0.92}},
			"entities":      []any{},
			"relationships": []any{},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	withTestCatalog(t, mux)

	result, err := callTool(handleCatalogSemanticSearch, map[string]any{"q": "rnaseq datasets"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error")
	}

	content := result.Content[0].(gomcp.TextContent)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(content.Text), &parsed); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	if _, ok := parsed["chunks"]; !ok {
		t.Error("expected chunks key in response")
	}
}

func TestHandleCatalogSemanticSearch_MissingQ(t *testing.T) {
	os.Setenv("EXOHUB_CATALOG_URL", "http://unused")
	defer os.Unsetenv("EXOHUB_CATALOG_URL")

	result, err := callTool(handleCatalogSemanticSearch, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result for missing q parameter")
	}
}

func TestHandleCatalogSemanticSearch_WithTypesFilter(t *testing.T) {
	var capturedTypes string
	mux := http.NewServeMux()
	mux.HandleFunc("/semantic-search", func(w http.ResponseWriter, r *http.Request) {
		capturedTypes = r.URL.Query().Get("types")
		resp := map[string]any{"entities": []any{}}
		json.NewEncoder(w).Encode(resp)
	})
	withTestCatalog(t, mux)

	_, err := callTool(handleCatalogSemanticSearch, map[string]any{
		"q":     "BRCA1",
		"types": []any{"entities"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedTypes != "entities" {
		t.Errorf("expected types=entities, got %q", capturedTypes)
	}
}

func TestHandleCatalogSemanticSearch_WithProjectFilter(t *testing.T) {
	var capturedProjectID string
	mux := http.NewServeMux()
	mux.HandleFunc("/semantic-search", func(w http.ResponseWriter, r *http.Request) {
		capturedProjectID = r.URL.Query().Get("project_id")
		resp := map[string]any{"chunks": []any{}}
		json.NewEncoder(w).Encode(resp)
	})
	withTestCatalog(t, mux)

	_, err := callTool(handleCatalogSemanticSearch, map[string]any{
		"q":       "gene expression",
		"project": "DS000020193",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedProjectID != "DS000020193" {
		t.Errorf("expected project_id=DS000020193, got %q", capturedProjectID)
	}
}

func TestHandleCatalogSemanticSearch_DefaultLimit(t *testing.T) {
	var capturedLimit string
	mux := http.NewServeMux()
	mux.HandleFunc("/semantic-search", func(w http.ResponseWriter, r *http.Request) {
		capturedLimit = r.URL.Query().Get("limit")
		resp := map[string]any{"chunks": []any{}}
		json.NewEncoder(w).Encode(resp)
	})
	withTestCatalog(t, mux)

	_, _ = callTool(handleCatalogSemanticSearch, map[string]any{"q": "*"})

	if capturedLimit != "10" {
		t.Errorf("expected default limit=10, got %q", capturedLimit)
	}
}

func TestHandleCatalogSemanticSearch_ServerError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/semantic-search", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
		json.NewEncoder(w).Encode(map[string]string{"reason": "service unavailable"})
	})
	withTestCatalog(t, mux)

	result, err := callTool(handleCatalogSemanticSearch, map[string]any{"q": "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result for server error")
	}
}
