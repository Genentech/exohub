package handler

import (
	"encoding/json"
	"fmt"
	"net/http"

	surrealdb "github.com/surrealdb/surrealdb.go"

	"github.com/Genentech/exohub/go/adb-standalone/internal/config"
	"github.com/Genentech/exohub/go/adb-standalone/internal/embed"
	"github.com/Genentech/exohub/go/adb-standalone/internal/ingest"
)

// Ingest handles POST /v1/{tenant}/project/ingest.
// embedder may be nil; when non-nil, documents are embedded for semantic search.
func Ingest(db *surrealdb.DB, cfg *config.Config, embedder embed.Embedder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req ingest.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %v", err))
			return
		}

		if req.ProjectID == "" {
			writeError(w, http.StatusBadRequest, "project_id is required")
			return
		}
		if req.Version == "" {
			writeError(w, http.StatusBadRequest, "version is required")
			return
		}
		if req.S3Location == "" {
			writeError(w, http.StatusBadRequest, "s3_location is required")
			return
		}

		reader, err := ingest.ReaderForRequest(req)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("bundle reader: %v", err))
			return
		}

		ctx := r.Context()
		result, err := ingest.IngestBundle(ctx, db, cfg, req, reader, embedder)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("ingest error: %v", err))
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"job_id":           result.JobID,
			"status":           "SUCCESS",
			"ingested":         result.Ingested,
			"errors":           result.Errors,
			"embedding_errors": result.EmbeddingErrors,
		})
	}
}
