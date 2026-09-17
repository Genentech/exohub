package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Genentech/exohub/go/adb-standalone/internal/embed"
	"github.com/Genentech/exohub/go/adb-standalone/internal/handler"
)

// fixedEmbedder returns a unit vector of fixed length — used in tests where a
// real model is unavailable.
type fixedEmbedder struct {
	dims int
}

func (f *fixedEmbedder) Embed(_ string) ([]float32, error) {
	vec := make([]float32, f.dims)
	for i := range vec {
		vec[i] = 0.1
	}
	return vec, nil
}

func (f *fixedEmbedder) Close() error { return nil }

var _ embed.Embedder = (*fixedEmbedder)(nil)

func TestSemanticSearch_DisabledReturns404(t *testing.T) {
	r := chi.NewRouter()
	r.Get("/semantic-search", handler.SemanticSearch(nil, nil))

	req := httptest.NewRequest(http.MethodGet, "/semantic-search?q=test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["status"] != "error" {
		t.Errorf("expected status=error, got %v", body["status"])
	}
}

func TestSemanticSearch_MissingQ(t *testing.T) {
	r := chi.NewRouter()
	// Pass a non-nil embedder so the nil check is bypassed and we hit the q-validation.
	r.Get("/semantic-search", handler.SemanticSearch(nil, &fixedEmbedder{dims: 256}))

	req := httptest.NewRequest(http.MethodGet, "/semantic-search", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestSemanticSearch_InvalidLimit_NonNumeric(t *testing.T) {
	r := chi.NewRouter()
	r.Get("/semantic-search", handler.SemanticSearch(nil, &fixedEmbedder{dims: 256}))

	req := httptest.NewRequest(http.MethodGet, "/semantic-search?q=test&limit=notanumber", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestSemanticSearch_InvalidLimit_Zero(t *testing.T) {
	r := chi.NewRouter()
	r.Get("/semantic-search", handler.SemanticSearch(nil, &fixedEmbedder{dims: 256}))

	req := httptest.NewRequest(http.MethodGet, "/semantic-search?q=test&limit=0", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// limit=0 is indistinguishable from empty results; must be rejected with 400.
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for limit=0, got %d", w.Code)
	}
}

func TestSemanticSearch_InvalidLimit_Negative(t *testing.T) {
	r := chi.NewRouter()
	r.Get("/semantic-search", handler.SemanticSearch(nil, &fixedEmbedder{dims: 256}))

	req := httptest.NewRequest(http.MethodGet, "/semantic-search?q=test&limit=-5", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for limit=-5, got %d", w.Code)
	}
}

func TestSemanticSearch_DisabledReturnsErrorEnvelope(t *testing.T) {
	r := chi.NewRouter()
	r.Get("/semantic-search", handler.SemanticSearch(nil, nil))

	req := httptest.NewRequest(http.MethodGet, "/semantic-search?q=test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "error" {
		t.Errorf("status: got %v, want error", body["status"])
	}
	if body["reason"] == "" {
		t.Error("expected non-empty reason")
	}
}
