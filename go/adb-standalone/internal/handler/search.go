package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

const defaultSearchSize = 10
const scrollTTL = 24 * time.Hour

type searchResponse struct {
	Results []map[string]any `json:"results"`
	Count   int              `json:"count"`
	Total   int              `json:"total"`
	Next    *string          `json:"next"`
}

type scrollSessionRecord struct {
	Query     map[string]any `json:"query"`
	Cursor    int            `json:"cursor"`
	Total     int            `json:"total"`
	ExpiresAt time.Time      `json:"expires_at"`
}

// Search handles GET /v1/{tenant}/search?q=&fields=&size=
func Search(db *surrealdb.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		sizeStr := r.URL.Query().Get("size")
		size := defaultSearchSize
		if sizeStr != "" {
			n, err := strconv.Atoi(sizeStr)
			if err != nil || n < 0 {
				writeError(w, http.StatusBadRequest, "invalid size parameter")
				return
			}
			size = n
		}
		ctx := r.Context()

		if size == 0 {
			// Return the real total even when size=0 so callers can size-probe
			// the result set without fetching any documents.
			total, err := countSearch(ctx, db, q)
			if err != nil {
				writeError(w, http.StatusInternalServerError, fmt.Sprintf("count error: %v", err))
				return
			}
			resp := searchResponse{Results: []map[string]any{}, Count: 0, Total: total, Next: nil}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		// countSearch and runSearch are two separate round-trips. Under concurrent
		// ingest writes the total may not exactly match the result set (TOCTOU).
		// This is an accepted known limitation for a read-mostly search service;
		// atomic result+count in a single query would require a subquery or CTE
		// that SurrealDB does not yet support efficiently for FTS.
		total, err := countSearch(ctx, db, q)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("count error: %v", err))
			return
		}

		results, err := runSearch(ctx, db, q, 0, size)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("search error: %v", err))
			return
		}

		var next *string
		// Only create a scroll session when there are actual results to page through.
		// Without this guard a TOCTOU race (count > size but results == 0 due to
		// concurrent deletes) would issue a phantom next pointer.
		if total > size && len(results) > 0 {
			session := scrollSessionRecord{
				Query:     map[string]any{"q": q, "size": size},
				Cursor:    size,
				Total:     total,
				ExpiresAt: time.Now().UTC().Add(scrollTTL),
			}
			sid, err := createScrollSession(ctx, db, session)
			if err != nil {
				writeError(w, http.StatusInternalServerError, fmt.Sprintf("scroll session: %v", err))
				return
			}
			s := "/scroll/" + sid
			next = &s
		}

		resp := searchResponse{
			Results: results,
			Count:   len(results),
			Total:   total,
			Next:    next,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

type countRow struct {
	Count int `json:"count"`
}

func countSearch(ctx context.Context, db *surrealdb.DB, q string) (int, error) {
	var sql string
	var vars map[string]any
	if q == "" || q == "*" {
		sql = `SELECT count() FROM document WHERE _extra.permissions.read_access = 'public' GROUP ALL`
	} else {
		sql = `SELECT count() FROM document WHERE search_text @1@ $q AND _extra.permissions.read_access = 'public' GROUP ALL`
		vars = map[string]any{"q": q}
	}

	res, err := surrealdb.Query[[]countRow](ctx, db, sql, vars)
	if err != nil {
		return 0, err
	}
	if res == nil || len(*res) == 0 {
		return 0, nil
	}
	qr := (*res)[0]
	if qr.Error != nil {
		return 0, qr.Error
	}
	if len(qr.Result) == 0 {
		return 0, nil
	}
	return qr.Result[0].Count, nil
}

func runSearch(ctx context.Context, db *surrealdb.DB, q string, offset, limit int) ([]map[string]any, error) {
	var sql string
	var vars map[string]any
	if q == "" || q == "*" {
		sql = `SELECT * FROM document WHERE _extra.permissions.read_access = 'public' LIMIT $limit START $offset`
		vars = map[string]any{"limit": limit, "offset": offset}
	} else {
		sql = `SELECT *, search::score(1) AS _score FROM document WHERE search_text @1@ $q AND _extra.permissions.read_access = 'public' ORDER BY _score DESC LIMIT $limit START $offset`
		vars = map[string]any{"q": q, "limit": limit, "offset": offset}
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
	// Strip internal fields that should not appear in the response.
	for _, row := range rows {
		delete(row, "search_text")
		delete(row, "_score")
	}
	return rows, nil
}

func createScrollSession(ctx context.Context, db *surrealdb.DB, session scrollSessionRecord) (string, error) {
	// SurrealDB v3 Create on a table returns []T (array), not T.
	res, err := surrealdb.Create[[]map[string]any](ctx, db, models.Table("scroll_session"), session)
	if err != nil {
		return "", err
	}
	if res == nil || len(*res) == 0 {
		return "", fmt.Errorf("no result from scroll session create")
	}
	idVal, ok := (*res)[0]["id"]
	if !ok {
		return "", fmt.Errorf("scroll session missing id")
	}
	rid, ok := idVal.(models.RecordID)
	if !ok {
		return fmt.Sprintf("%v", idVal), nil
	}
	return fmt.Sprintf("%v", rid.ID), nil
}
