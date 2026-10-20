package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleProfilesList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/profiles" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"name":"sandbox","description":"Sandbox profile"}]`))
	}))
	defer srv.Close()
	t.Setenv("EXOHUB_API_URL", srv.URL+"/api")

	result, err := handleProfiles(context.Background(), mustRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %+v", result)
	}
}

func TestHandleProfilesShow(t *testing.T) {
	payload := `{
		"name": "sandbox",
		"vars": {},
		"prompts": [
			{"key": "read_access", "question": "Who reads?", "type": "choice", "default": "authenticated", "choices": ["public","authenticated"]}
		],
		"templates": {},
		"actions": []
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/profiles/sandbox" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(payload))
	}))
	defer srv.Close()
	t.Setenv("EXOHUB_API_URL", srv.URL+"/api")

	result, err := handleProfiles(context.Background(), mustRequest(map[string]any{"name": "sandbox"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %+v", result)
	}
}

func TestHandleProfilesShowNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()
	t.Setenv("EXOHUB_API_URL", srv.URL+"/api")

	result, err := handleProfiles(context.Background(), mustRequest(map[string]any{"name": "missing"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected error result for missing profile, got %+v", result)
	}
}
