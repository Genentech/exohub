//go:build artifactdb

package mcp

import (
	"encoding/json"
	"net/http"
	"testing"

	gomcp "github.com/mark3labs/mcp-go/mcp"
)

func TestHandleCatalogSchemas_DefaultFilter(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/schemas", func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"document_types": []map[string]any{
				{"name": "exohub-artifact/v1"},
				{"name": "exohub-bundle/v1"},
				{"name": "other-schema/v1"},
				{"name": "exohub-commit/v1"},
			},
		}
		json.NewEncoder(w).Encode(resp)
	})
	withTestCatalog(t, mux)

	result, err := callTool(handleCatalogSchemas, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := result.Content[0].(gomcp.TextContent)
	var schemas []string
	if err := json.Unmarshal([]byte(content.Text), &schemas); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	if len(schemas) != 3 {
		t.Fatalf("expected 3 schemas, got %d: %v", len(schemas), schemas)
	}
	expected := []string{"exohub-artifact/v1", "exohub-bundle/v1", "exohub-commit/v1"}
	for i, s := range expected {
		if schemas[i] != s {
			t.Errorf("schema[%d]: expected %q, got %q", i, s, schemas[i])
		}
	}
}

func TestHandleCatalogSchemas_CustomFilter(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/schemas", func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"document_types": []map[string]any{
				{"name": "exohub-artifact/v1"},
				{"name": "exohub-bundle/v1"},
				{"name": "other-schema/v1"},
			},
		}
		json.NewEncoder(w).Encode(resp)
	})
	withTestCatalog(t, mux)

	result, err := callTool(handleCatalogSchemas, map[string]any{"filter": "*"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := result.Content[0].(gomcp.TextContent)
	var schemas []string
	json.Unmarshal([]byte(content.Text), &schemas)

	if len(schemas) != 3 {
		t.Fatalf("expected 3 schemas with * filter, got %d: %v", len(schemas), schemas)
	}
}

func TestHandleCatalogSchemas_ServerError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/schemas", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte(`{"reason":"server error"}`))
	})
	withTestCatalog(t, mux)

	result, err := callTool(handleCatalogSchemas, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result for server error")
	}
}

func TestHandleCatalogSchemas_Deduplication(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/schemas", func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"document_types": []map[string]any{
				{"name": "exohub-artifact/v1"},
				{"name": "exohub-artifact/v1"},
				{"name": "exohub-artifact/v1"},
			},
		}
		json.NewEncoder(w).Encode(resp)
	})
	withTestCatalog(t, mux)

	result, err := callTool(handleCatalogSchemas, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := result.Content[0].(gomcp.TextContent)
	var schemas []string
	json.Unmarshal([]byte(content.Text), &schemas)

	if len(schemas) != 1 {
		t.Errorf("expected 1 deduplicated schema, got %d: %v", len(schemas), schemas)
	}
}
