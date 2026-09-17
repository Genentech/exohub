package handler

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	surrealdb "github.com/surrealdb/surrealdb.go"
)

type versionsResponse struct {
	ProjectID string           `json:"project_id"`
	Aggs      []map[string]any `json:"aggs"`
	Total     int              `json:"total"`
	Latest    map[string]any   `json:"latest"`
}

type versionRow struct {
	Extra struct {
		Version string `json:"version"`
	} `json:"_extra"`
}

// Versions handles GET /v1/{tenant}/projects/{projectID}/versions
func Versions(db *surrealdb.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID := chi.URLParam(r, "projectID")
		if projectID == "" {
			writeError(w, http.StatusBadRequest, "missing project id")
			return
		}

		ctx := r.Context()
		vars := map[string]any{"pid": projectID}

		// Collect distinct versions, ordered deterministically.
		// Note: no metapath filter here because every version in the exohub bundle
		// layout has exactly one bundle.json, so all doc types carry the same version
		// set. Verified against golden fixture (14 versions, all types agree).
		sql := `SELECT _extra.version FROM document WHERE _extra.project_id = $pid GROUP BY _extra.version ORDER BY _extra.version ASC`
		res, err := surrealdb.Query[[]versionRow](ctx, db, sql, vars)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("versions query error: %v", err))
			return
		}
		if res == nil || len(*res) == 0 || (*res)[0].Error != nil {
			writeError(w, http.StatusNotFound, fmt.Sprintf("project not found: %s", projectID))
			return
		}
		rows := (*res)[0].Result
		if len(rows) == 0 {
			writeError(w, http.StatusNotFound, fmt.Sprintf("project not found: %s", projectID))
			return
		}

		aggs := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			aggs = append(aggs, map[string]any{"_extra.version": row.Extra.Version})
		}

		// Determine latest version (where _extra.latest = true).
		latestSQL := `SELECT _extra.version FROM document WHERE _extra.project_id = $pid AND _extra.latest = true LIMIT 1`
		var latestMap map[string]any
		latestRes, err := surrealdb.Query[[]versionRow](ctx, db, latestSQL, vars)
		if err == nil && latestRes != nil && len(*latestRes) > 0 && (*latestRes)[0].Error == nil {
			lrows := (*latestRes)[0].Result
			if len(lrows) > 0 {
				latestMap = map[string]any{"_extra.version": lrows[0].Extra.Version}
			}
		}

		resp := versionsResponse{
			ProjectID: projectID,
			Aggs:      aggs,
			Total:     len(aggs),
			Latest:    latestMap,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}
