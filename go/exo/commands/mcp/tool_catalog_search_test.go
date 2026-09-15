//go:build artifactdb

package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	gomcp "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func withTestCatalog(t *testing.T, handler http.Handler) string {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	t.Setenv("EXOHUB_CATALOG_URL", srv.URL)
	return srv.URL
}

func callTool(handler server.ToolHandlerFunc, args map[string]any) (*gomcp.CallToolResult, error) {
	reqJSON, _ := json.Marshal(map[string]any{
		"params": map[string]any{
			"name":      "test",
			"arguments": args,
		},
	})
	var req gomcp.CallToolRequest
	_ = json.Unmarshal(reqJSON, &req)
	return handler(context.Background(), req)
}

func TestHandleCatalogSearch_Basic(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		if q == "" {
			t.Error("expected q parameter")
		}
		resp := map[string]any{
			"results": []map[string]any{
				{"_extra": map[string]any{"id": "proj:file.txt@v1", "project_id": "proj"}, "path": "file.txt"},
			},
			"count": 1,
			"total": 1,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	withTestCatalog(t, mux)

	result, err := callTool(handleCatalogSearch, map[string]any{"q": "rnaseq"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := result.Content[0].(gomcp.TextContent)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(content.Text), &parsed); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	if parsed["total"].(float64) != 1 {
		t.Errorf("expected total=1, got %v", parsed["total"])
	}
}

func TestHandleCatalogSearch_WithSchemaFilter(t *testing.T) {
	var capturedQ string
	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		capturedQ = r.URL.Query().Get("q")
		resp := map[string]any{"results": []any{}, "count": 0, "total": 0}
		json.NewEncoder(w).Encode(resp)
	})
	withTestCatalog(t, mux)

	_, err := callTool(handleCatalogSearch, map[string]any{
		"q":      "*",
		"schema": "exohub-artifact/v1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := `_extra.$schema:"exohub-artifact/v1" AND *`
	if capturedQ != expected {
		t.Errorf("expected q=%q, got %q", expected, capturedQ)
	}
}

func TestHandleCatalogSearch_WithProjectFilter(t *testing.T) {
	var capturedQ string
	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		capturedQ = r.URL.Query().Get("q")
		resp := map[string]any{"results": []any{}, "count": 0, "total": 0}
		json.NewEncoder(w).Encode(resp)
	})
	withTestCatalog(t, mux)

	_, err := callTool(handleCatalogSearch, map[string]any{
		"q":       "*",
		"project": "DS000020193",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := `_extra.project_id:"DS000020193" AND *`
	if capturedQ != expected {
		t.Errorf("expected q=%q, got %q", expected, capturedQ)
	}
}

func TestHandleCatalogSearch_MissingQ(t *testing.T) {
	os.Setenv("EXOHUB_CATALOG_URL", "http://unused")
	defer os.Unsetenv("EXOHUB_CATALOG_URL")

	result, err := callTool(handleCatalogSearch, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result for missing q parameter")
	}
}

func TestHandleCatalogSearch_ServerError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]string{"reason": "internal error"})
	})
	withTestCatalog(t, mux)

	result, err := callTool(handleCatalogSearch, map[string]any{"q": "*"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result for server error")
	}
}

func TestHandleCatalogSearch_DefaultParams(t *testing.T) {
	var capturedParams map[string]string
	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		capturedParams = map[string]string{
			"fields": r.URL.Query().Get("fields"),
			"sort":   r.URL.Query().Get("sort"),
			"size":   r.URL.Query().Get("size"),
			"latest": r.URL.Query().Get("latest"),
		}
		resp := map[string]any{"results": []any{}, "count": 0, "total": 0}
		json.NewEncoder(w).Encode(resp)
	})
	withTestCatalog(t, mux)

	_, _ = callTool(handleCatalogSearch, map[string]any{"q": "*"})

	if capturedParams["fields"] != "_extra,path" {
		t.Errorf("expected fields=_extra,path, got %s", capturedParams["fields"])
	}
	if capturedParams["sort"] != "-_extra.uploaded" {
		t.Errorf("expected sort=-_extra.uploaded, got %s", capturedParams["sort"])
	}
	if capturedParams["size"] != "10" {
		t.Errorf("expected size=10, got %s", capturedParams["size"])
	}
	if capturedParams["latest"] != "true" {
		t.Errorf("expected latest=true, got %s", capturedParams["latest"])
	}
}
