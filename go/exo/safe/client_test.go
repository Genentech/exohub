package safe

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientStoreSizeLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	client := &Client{
		apiBase:    server.URL,
		httpClient: server.Client(),
	}

	// Exactly at limit should work
	value := strings.Repeat("a", MaxSecretSize)
	err := client.Store("fake-token", "test", TypeToken, value)
	if err != nil {
		t.Errorf("Store at exactly %d bytes should succeed: %v", MaxSecretSize, err)
	}

	// Over limit should fail
	value = strings.Repeat("a", MaxSecretSize+1)
	err = client.Store("fake-token", "test", TypeToken, value)
	if err == nil {
		t.Error("Store over size limit should fail")
	}
	if !strings.Contains(err.Error(), "limit") {
		t.Errorf("error should mention limit: %v", err)
	}
}

func TestClientStoreSuccess(t *testing.T) {
	var receivedBody map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("expected auth header, got %s", r.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(r.Body).Decode(&receivedBody); err != nil {
			t.Errorf("failed to decode body: %v", err)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	client := &Client{apiBase: server.URL, httpClient: server.Client()}
	err := client.Store("test-token", "github.com", TypeSSHKey, "key-content")
	if err != nil {
		t.Fatalf("Store() error: %v", err)
	}

	if receivedBody["value"] != "key-content" {
		t.Errorf("expected value 'key-content', got %q", receivedBody["value"])
	}
	if receivedBody["name"] != "github.com" {
		t.Errorf("expected name 'github.com', got %q", receivedBody["name"])
	}
}

func TestClientList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		json.NewEncoder(w).Encode([]ListEntry{
			{Name: "github.com", Type: TypeSSHKey},
			{Name: "gitlab.com", Type: TypeToken},
		})
	}))
	defer server.Close()

	client := &Client{apiBase: server.URL, httpClient: server.Client()}
	entries, err := client.List("test-token")
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Name != "github.com" || entries[0].Type != TypeSSHKey {
		t.Errorf("first entry mismatch: %+v", entries[0])
	}
	if entries[1].Name != "gitlab.com" || entries[1].Type != TypeToken {
		t.Errorf("second entry mismatch: %+v", entries[1])
	}
}

func TestClientGetAll(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]Entry{
			{Name: "github.com", Type: TypeSSHKey, Value: "ssh-key-content"},
			{Name: "gitlab.com", Type: TypeToken, Value: "gitlab-token"},
		})
	}))
	defer server.Close()

	client := &Client{apiBase: server.URL, httpClient: server.Client()}
	entries, err := client.GetAll("test-token")
	if err != nil {
		t.Fatalf("GetAll() error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Type != TypeSSHKey {
		t.Errorf("expected type %q, got %q", TypeSSHKey, entries[0].Type)
	}
	if entries[1].Type != TypeToken {
		t.Errorf("expected type %q, got %q", TypeToken, entries[1].Type)
	}
}

func TestClientDelete(t *testing.T) {
	deleted := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("expected DELETE, got %s", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/safe/ssh-key/github.com") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		deleted = true
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := &Client{apiBase: server.URL, httpClient: server.Client()}
	err := client.Delete("test-token", "ssh-key", "github.com")
	if err != nil {
		t.Fatalf("Delete() error: %v", err)
	}
	if !deleted {
		t.Error("Delete handler was not called")
	}
}

func TestClientAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("access denied"))
	}))
	defer server.Close()

	client := &Client{apiBase: server.URL, httpClient: server.Client()}

	err := client.Store("bad-token", "test", TypeToken, "value")
	if err == nil {
		t.Error("expected error on 403")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("error should contain status code: %v", err)
	}
}
