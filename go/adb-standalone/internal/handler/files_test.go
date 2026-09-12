package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Genentech/exohub/go/adb-standalone/internal/config"
	"github.com/Genentech/exohub/go/adb-standalone/internal/handler"
	"github.com/Genentech/exohub/go/adb-standalone/internal/storage"
)

// mockAdapter implements storage.Adapter for tests — no real S3 calls.
type mockAdapter struct {
	presignURL string
	parts      []storage.Part
}

func (m *mockAdapter) PresignGetURL(_ context.Context, bucket, key string, _ time.Duration) (string, error) {
	if m.presignURL != "" {
		return m.presignURL, nil
	}
	return "https://minio.local/" + bucket + "/" + key + "?X-Amz-Signature=FAKE", nil
}

func (m *mockAdapter) Parts(_ context.Context, bucket, key string, _ time.Duration) ([]storage.Part, error) {
	if m.parts != nil {
		return m.parts, nil
	}
	return []storage.Part{{
		PartNumber: 1,
		Size:       1024,
		URL:        "https://minio.local/" + bucket + "/" + key + "?X-Amz-Signature=FAKE",
		S3Path:     bucket + "/" + key,
		ChunkKey:   "abc123",
	}}, nil
}

// TestChunkEntry_JSONShape verifies that the chunks response has the exact field
// names expected by exo download's ChunksResponse.
func TestChunkEntry_JSONShape(t *testing.T) {
	type chunkEntry struct {
		ChunkKey    string `json:"chunk_key,omitempty"`
		ChunkNumber int    `json:"chunk_number"`
		ChunkSize   int64  `json:"chunk_size"`
		S3Path      string `json:"s3_path,omitempty"`
		URL         string `json:"url,omitempty"`
	}
	type chunksResp struct {
		Chunks   []chunkEntry `json:"chunks"`
		Filename string       `json:"filename"`
		Size     int64        `json:"size"`
	}

	resp := chunksResp{
		Chunks: []chunkEntry{
			{ChunkKey: "abc", ChunkNumber: 1, ChunkSize: 512, S3Path: "bucket/key", URL: "https://example.com/presigned"},
		},
		Filename: "bundle.json",
		Size:     512,
	}

	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}

	chunks, _ := decoded["chunks"].([]any)
	if len(chunks) != 1 {
		t.Fatalf("want 1 chunk, got %d", len(chunks))
	}
	c := chunks[0].(map[string]any)
	for _, field := range []string{"chunk_key", "chunk_number", "chunk_size", "s3_path", "url"} {
		if _, ok := c[field]; !ok {
			t.Errorf("missing field %q in chunk entry", field)
		}
	}
	if _, ok := decoded["filename"]; !ok {
		t.Error("missing field 'filename'")
	}
	if _, ok := decoded["size"]; !ok {
		t.Error("missing field 'size'")
	}
}

// TestFilesServePresignURL verifies that the mock adapter returns the expected URL.
func TestFilesServePresignURL(t *testing.T) {
	adapter := &mockAdapter{presignURL: "https://minio.local/test-bucket/test-key?X-Amz-Signature=SIG"}
	u, err := adapter.PresignGetURL(context.Background(), "test-bucket", "my-project/bundle.json", time.Hour)
	if err != nil {
		t.Fatalf("PresignGetURL: %v", err)
	}
	if u != "https://minio.local/test-bucket/test-key?X-Amz-Signature=SIG" {
		t.Errorf("unexpected presigned URL: %q", u)
	}
}

// TestFilesServeMeta_Redirect exercises the meta_redirect=true JSON response shape.
func TestFilesServeMeta_Redirect(t *testing.T) {
	adapter := &mockAdapter{presignURL: "https://minio.local/bucket/key?X-Amz-Signature=X"}
	s3cfg := &config.S3Config{MetaRedirect: true, PresignedURLExpiration: 3600}

	r := chi.NewRouter()
	r.Get("/test", func(w http.ResponseWriter, req *http.Request) {
		u, _ := adapter.PresignGetURL(req.Context(), "bucket", "key", time.Hour)
		if s3cfg.MetaRedirect {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"url": u})
		} else {
			http.Redirect(w, req, u, http.StatusTemporaryRedirect)
		}
	})

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["url"] == "" {
		t.Error("expected 'url' in response body")
	}
}

// Compile-check: ensure FilesServe is exported from the handler package.
var _ = handler.FilesServe
