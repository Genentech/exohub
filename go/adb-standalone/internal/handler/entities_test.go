package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Genentech/exohub/go/adb-standalone/internal/handler"
)

func TestEntities_MissingName(t *testing.T) {
	r := chi.NewRouter()
	r.Get("/entities", handler.Entities(nil))

	req := httptest.NewRequest(http.MethodGet, "/entities", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "error" {
		t.Errorf("expected status=error, got %v", body["status"])
	}
}

func TestEntities_MissingNameErrorEnvelope(t *testing.T) {
	r := chi.NewRouter()
	r.Get("/entities", handler.Entities(nil))

	req := httptest.NewRequest(http.MethodGet, "/entities", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["reason"] == "" {
		t.Error("expected non-empty reason in error envelope")
	}
}
