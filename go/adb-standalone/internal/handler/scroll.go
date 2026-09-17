package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

// Scroll handles GET /v1/{tenant}/scroll/{scrollID}
//
// Concurrency contract: scroll sessions are single-consumer. Two concurrent
// requests on the same session ID will both attempt the CAS cursor update;
// one will win and advance, the other will receive 409 Conflict.
//
// Cursor advance is performed BEFORE sending the response so that a crash
// mid-response does not cause the same page to be silently re-served.
func Scroll(db *surrealdb.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessionID := chi.URLParam(r, "scrollID")
		if sessionID == "" {
			writeError(w, http.StatusBadRequest, "missing scroll id")
			return
		}

		ctx := r.Context()

		rid := models.NewRecordID("scroll_session", sessionID)
		raw, err := surrealdb.Select[map[string]any](ctx, db, rid)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("scroll session db error: %v", err))
			return
		}
		if raw == nil {
			writeError(w, http.StatusNotFound, fmt.Sprintf("scroll session not found: %s", sessionID))
			return
		}

		// Re-serialise to parse into typed struct.
		b, err := json.Marshal(*raw)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "scroll session parse error")
			return
		}
		var session scrollSessionRecord
		if err := json.Unmarshal(b, &session); err != nil {
			writeError(w, http.StatusInternalServerError, "scroll session parse error")
			return
		}

		if time.Now().UTC().After(session.ExpiresAt) {
			writeError(w, http.StatusNotFound, "scroll session expired")
			return
		}

		q, _ := session.Query["q"].(string)
		size := defaultSearchSize
		if s, ok := session.Query["size"].(float64); ok {
			size = int(s)
		}

		oldCursor := session.Cursor
		newCursor := oldCursor + size

		// ── Atomic CAS cursor advance ────────────────────────────────────────
		// Update cursor only if it still equals the value we read (oldCursor).
		// This prevents two concurrent requests from returning the same page:
		// the losing request gets a 409 Conflict.
		casSQL := `UPDATE $rid SET cursor = $new WHERE cursor = $old`
		casVars := map[string]any{
			"rid": rid,
			"old": oldCursor,
			"new": newCursor,
		}
		casRes, err := surrealdb.Query[[]map[string]any](ctx, db, casSQL, casVars)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("scroll session: cursor update error: %v", err))
			return
		}
		// SurrealDB UPDATE WHERE returns an empty array when the WHERE clause
		// doesn't match. Two causes:
		// (a) concurrent request already advanced the cursor → 409
		// (b) session was deleted between our SELECT and this UPDATE → 404
		// Re-fetch to distinguish them.
		if casRes == nil || len(*casRes) == 0 || len((*casRes)[0].Result) == 0 {
			refetch, rerr := surrealdb.Select[map[string]any](ctx, db, rid)
			if rerr != nil || refetch == nil {
				writeError(w, http.StatusNotFound, fmt.Sprintf("scroll session not found: %s", sessionID))
			} else {
				writeError(w, http.StatusConflict, "scroll session: concurrent access detected; retry")
			}
			return
		}

		// Cursor is committed — now fetch results for the page we own.
		results, err := runSearch(ctx, db, q, oldCursor, size)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("search error: %v", err))
			return
		}

		var next *string
		if newCursor < session.Total {
			s := "/scroll/" + sessionID
			next = &s
		}

		resp := searchResponse{
			Results: results,
			Count:   len(results),
			Total:   session.Total,
			Next:    next,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}
