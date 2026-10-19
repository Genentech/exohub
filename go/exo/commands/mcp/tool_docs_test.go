package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gomcp "github.com/mark3labs/mcp-go/mcp"
)

func TestResolveDocsURL_Table(t *testing.T) {
	tests := []struct {
		name    string
		base    string
		page    string
		want    string
		wantErr bool
	}{
		{
			name: "no page returns base",
			base: "https://genentech.github.io/exohub/llms.txt",
			page: "",
			want: "https://genentech.github.io/exohub/llms.txt",
		},
		{
			name: "relative page under base dir",
			base: "https://genentech.github.io/exohub/llms.txt",
			page: "user-guide/sync/index.md",
			want: "https://genentech.github.io/exohub/user-guide/sync/index.md",
		},
		{
			name: "absolute page under base dir",
			base: "https://genentech.github.io/exohub/llms.txt",
			page: "https://genentech.github.io/exohub/user-guide/sync/index.md",
			want: "https://genentech.github.io/exohub/user-guide/sync/index.md",
		},
		{
			name:    "dot-dot escapes base dir",
			base:    "https://genentech.github.io/exohub/llms.txt",
			page:    "../other-repo/secret.md",
			wantErr: true,
		},
		{
			name:    "absolute path escapes base dir",
			base:    "https://genentech.github.io/exohub/llms.txt",
			page:    "/another-project/index.md",
			wantErr: true,
		},
		{
			name:    "encoded dot-dot escapes base dir",
			base:    "https://genentech.github.io/exohub/llms.txt",
			page:    "%2e%2e/other-repo/x.md",
			wantErr: true,
		},
		{
			name:    "userinfo rejected",
			base:    "https://genentech.github.io/exohub/llms.txt",
			page:    "https://attacker@genentech.github.io/exohub/x.md",
			wantErr: true,
		},
		{
			name:    "off-base host rejected",
			base:    "https://genentech.github.io/exohub/llms.txt",
			page:    "https://evil.example/x",
			wantErr: true,
		},
		{
			name:    "off-base port rejected",
			base:    "https://genentech.github.io/exohub/llms.txt",
			page:    "https://genentech.github.io:8443/exohub/x.md",
			wantErr: true,
		},
		{
			name:    "off-base scheme rejected",
			base:    "https://genentech.github.io/exohub/llms.txt",
			page:    "http://genentech.github.io/exohub/x.md",
			wantErr: true,
		},
		{
			name:    "encoded slash rejected",
			base:    "https://genentech.github.io/exohub/llms.txt",
			page:    "a%2f..%2fb.md",
			wantErr: true,
		},
		{
			name:    "backslash rejected",
			base:    "https://genentech.github.io/exohub/llms.txt",
			page:    "a\\..\\b.md",
			wantErr: true,
		},
		{
			name:    "encoded backslash rejected",
			base:    "https://genentech.github.io/exohub/llms.txt",
			page:    "a%5c..%5cb.md",
			wantErr: true,
		},
		{
			name:    "query rejected",
			base:    "https://genentech.github.io/exohub/llms.txt",
			page:    "user-guide/sync/index.md?x=1",
			wantErr: true,
		},
		{
			name:    "fragment rejected",
			base:    "https://genentech.github.io/exohub/llms.txt",
			page:    "user-guide/sync/index.md#section",
			wantErr: true,
		},
		{
			name: "origin-only base: relative page allowed anywhere under root",
			base: "https://docs.example.com/llms.txt",
			page: "user-guide/sync/index.md",
			want: "https://docs.example.com/user-guide/sync/index.md",
		},
		{
			name:    "origin-only base: off-host still rejected",
			base:    "https://docs.example.com/llms.txt",
			page:    "https://evil.example/x",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveDocsURL(tc.base, tc.page)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("resolveDocsURL(%q, %q) = %q, want error", tc.base, tc.page, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveDocsURL(%q, %q) unexpected error: %v", tc.base, tc.page, err)
			}
			if got != tc.want {
				t.Errorf("resolveDocsURL(%q, %q) = %q, want %q", tc.base, tc.page, got, tc.want)
			}
		})
	}
}

func TestHandleDocs_FetchesIndex(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("# llms.txt index\n- [intro](guide/intro.md)\n"))
	}))
	defer srv.Close()

	t.Setenv("EXOHUB_DOCS_URL", srv.URL+"/llms.txt")

	request := gomcp.CallToolRequest{}
	result, err := handleDocs(context.Background(), request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %+v", result)
	}
	content := result.Content[0].(gomcp.TextContent)
	if content.Text != "# llms.txt index\n- [intro](guide/intro.md)" {
		t.Errorf("content = %q", content.Text)
	}
}

func TestHandleDocs_FetchesPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/guide/intro.md" {
			w.Write([]byte("intro content"))
			return
		}
		w.Write([]byte("index"))
	}))
	defer srv.Close()

	t.Setenv("EXOHUB_DOCS_URL", srv.URL+"/llms.txt")

	request := gomcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{"page": "guide/intro.md"}
	result, err := handleDocs(context.Background(), request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %+v", result)
	}
	content := result.Content[0].(gomcp.TextContent)
	if content.Text != "intro content" {
		t.Errorf("content = %q", content.Text)
	}
}

func TestHandleDocs_RejectsOffBasePage(t *testing.T) {
	t.Setenv("EXOHUB_DOCS_URL", "https://docs.example.com/exohub/llms.txt")

	request := gomcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{"page": "https://evil.example.com/steal"}
	result, err := handleDocs(context.Background(), request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected error result, got %+v", result)
	}
}

func TestHandleDocs_RejectsPathTraversal(t *testing.T) {
	t.Setenv("EXOHUB_DOCS_URL", "https://docs.example.com/exohub/llms.txt")

	request := gomcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{"page": "../other-repo/secret.md"}
	result, err := handleDocs(context.Background(), request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected error result, got %+v", result)
	}
}

func TestHandleDocs_UnreachableSiteReturnsURLAndError(t *testing.T) {
	t.Setenv("EXOHUB_DOCS_URL", "http://127.0.0.1:1/llms.txt")

	request := gomcp.CallToolRequest{}
	result, err := handleDocs(context.Background(), request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected non-fatal result, got error result: %+v", result)
	}
	content := result.Content[0].(gomcp.TextContent)
	var parsed map[string]string
	if err := json.Unmarshal([]byte(content.Text), &parsed); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	if parsed["url"] != "http://127.0.0.1:1/llms.txt" {
		t.Errorf("url = %q, want %q", parsed["url"], "http://127.0.0.1:1/llms.txt")
	}
	if parsed["error"] == "" {
		t.Errorf("expected non-empty error message")
	}
}

func TestFetchDocs_RedirectUnderBaseAllowed(t *testing.T) {
	var mux http.ServeMux
	srv := httptest.NewServer(&mux)
	defer srv.Close()

	mux.HandleFunc("/exohub/llms.txt", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/exohub/llms-v2.txt", http.StatusFound)
	})
	mux.HandleFunc("/exohub/llms-v2.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("redirected index"))
	})

	base := srv.URL + "/exohub/llms.txt"
	body, err := fetchDocs(context.Background(), base, base)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if body != "redirected index" {
		t.Errorf("body = %q", body)
	}
}

func TestFetchDocs_RedirectOffBaseBlocked(t *testing.T) {
	var mux http.ServeMux
	srv := httptest.NewServer(&mux)
	defer srv.Close()

	var evilHit bool
	evilSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		evilHit = true
		w.Write([]byte("should not be fetched"))
	}))
	defer evilSrv.Close()

	mux.HandleFunc("/exohub/llms.txt", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, evilSrv.URL+"/steal", http.StatusFound)
	})

	base := srv.URL + "/exohub/llms.txt"
	_, err := fetchDocs(context.Background(), base, base)
	if err == nil {
		t.Fatal("expected error for off-base redirect, got nil")
	}
	if evilHit {
		t.Fatal("redirect target should never have been fetched")
	}
}

func TestFetchDocs_OversizedBodyReturnsError(t *testing.T) {
	big := strings.Repeat("x", docsMaxBodyBytes+1024)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(big))
	}))
	defer srv.Close()

	base := srv.URL + "/llms.txt"
	_, err := fetchDocs(context.Background(), base, base)
	if err == nil {
		t.Fatal("expected error for oversized body, got nil")
	}
}

func TestFetchDocs_NonTextContentTypeRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write([]byte("binary-ish"))
	}))
	defer srv.Close()

	base := srv.URL + "/llms.txt"
	_, err := fetchDocs(context.Background(), base, base)
	if err == nil {
		t.Fatal("expected error for non-text content type, got nil")
	}
}
