package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

// Metadata handles GET /v1/{tenant}/files/{fileID}/metadata
// fileID is URL-encoded: project:path@version
func Metadata(db *surrealdb.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rawID := chi.URLParam(r, "fileID")
		if rawID == "" {
			writeError(w, http.StatusBadRequest, "missing file id")
			return
		}

		fileID, err := url.PathUnescape(rawID)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid file id encoding: %v", err))
			return
		}

		ctx := r.Context()
		rid := models.NewRecordID("document", fileID)
		res, err := surrealdb.Select[map[string]any](ctx, db, rid)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("db error: %v", err))
			return
		}
		if res == nil {
			writeError(w, http.StatusNotFound, fmt.Sprintf("not found: %s", fileID))
			return
		}

		doc := *res
		delete(doc, "search_text")

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(doc)
	}
}
