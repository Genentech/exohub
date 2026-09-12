package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	surrealdb "github.com/surrealdb/surrealdb.go"

	"github.com/Genentech/exohub/go/adb-standalone/internal/embed"
)

const defaultSemanticLimit = 10
const maxSemanticLimit = 100

type semanticSearchResponse struct {
	Chunks        []map[string]any `json:"chunks"`
	Entities      []map[string]any `json:"entities"`
	Relationships []map[string]any `json:"relationships"`
}

// SemanticSearch handles GET /v1/{tenant}/semantic-search.
// Returns 404 when semantic search is disabled (embedder == nil), matching
// the reference ArtifactDB service behaviour when nuros is not configured.
func SemanticSearch(db *surrealdb.DB, embedder embed.Embedder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if embedder == nil {
			writeError(w, http.StatusNotFound, "semantic search not enabled; set semantic.model_path to enable")
			return
		}

		q := r.URL.Query().Get("q")
		if q == "" {
			writeError(w, http.StatusBadRequest, "parameter 'q' is required")
			return
		}

		limit := defaultSemanticLimit
		if ls := r.URL.Query().Get("limit"); ls != "" {
			n, err := strconv.Atoi(ls)
			if err != nil || n <= 0 {
				writeError(w, http.StatusBadRequest, "invalid limit parameter; must be a positive integer")
				return
			}
			limit = n
		}
		if limit > maxSemanticLimit {
			limit = maxSemanticLimit
		}

		projectID := r.URL.Query().Get("project_id")

		vec, err := embedder.Embed(q)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("embedding error: %v", err))
			return
		}

		ctx := r.Context()
		results, err := runVectorSearch(ctx, db, vec, projectID, limit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("vector search error: %v", err))
			return
		}

		resp := semanticSearchResponse{
			Chunks:        results,
			Entities:      []map[string]any{},
			Relationships: []map[string]any{},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

func runVectorSearch(ctx context.Context, db *surrealdb.DB, vec []float32, projectID string, limit int) ([]map[string]any, error) {
	// Use the SurrealDB KNN operator (<|K|>) which hits the MTREE doc_vec index
	// rather than a full table scan via ORDER BY vector::distance::cosine(...).
	var sql string
	var vars map[string]any

	if projectID != "" {
		// Fetch more candidates (10×) then filter by project_id, because the
		// SurrealDB v3 KNN operator does not support combined WHERE conditions
		// alongside the <|K|> shorthand in the same filter position.
		candidates := limit * 10
		sql = `SELECT * FROM document WHERE embedding <|$k|> $vec AND _extra.project_id = $pid`
		vars = map[string]any{"vec": vec, "k": candidates, "pid": projectID}
	} else {
		sql = `SELECT * FROM document WHERE embedding <|$k|> $vec`
		vars = map[string]any{"vec": vec, "k": limit}
	}

	res, err := surrealdb.Query[[]map[string]any](ctx, db, sql, vars)
	if err != nil {
		return nil, err
	}
	if res == nil || len(*res) == 0 {
		return []map[string]any{}, nil
	}
	qr := (*res)[0]
	if qr.Error != nil {
		return nil, qr.Error
	}
	rows := qr.Result
	if rows == nil {
		return []map[string]any{}, nil
	}
	// Trim to requested limit after project filter.
	if projectID != "" && len(rows) > limit {
		rows = rows[:limit]
	}
	for _, row := range rows {
		delete(row, "search_text")
		delete(row, "embedding")
	}
	return rows, nil
}
