//go:build artifactdb

package mcp

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestHandleCatalogLineage(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/files/proj:out.csv@v1/metadata", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"_extra": {"id": "proj:out.csv@v1"},
			"lineage": {"covered": true, "inputs": [{"id": "proj:in.csv@v1", "path": "in.csv"}]}
		}`))
	})
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		if q == "" {
			t.Error("expected q parameter on consumer search")
		}
		resp := map[string]any{
			"results": []map[string]any{
				{"_extra": map[string]any{"id": "proj:downstream.csv@v1"}, "path": "downstream.csv"},
			},
			"count": 1,
			"total": 1,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	withTestCatalog(t, mux)

	result, err := callTool(handleCatalogLineage, map[string]any{"id": "proj:out.csv@v1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %+v", result)
	}
}

func TestHandleCatalogLineageRequiresID(t *testing.T) {
	result, err := callTool(handleCatalogLineage, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected error result for missing id, got %+v", result)
	}
}

func TestHandleCatalogLineageNotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/files/missing/metadata", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"reason":"not found"}`, http.StatusNotFound)
	})
	withTestCatalog(t, mux)

	result, err := callTool(handleCatalogLineage, map[string]any{"id": "missing"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected error result for 404, got %+v", result)
	}
}
