package handler

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Jobs handles GET /v1/{tenant}/jobs/{jobID}
// Since ingest is synchronous, all jobs are immediately complete.
func Jobs() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jobID := chi.URLParam(r, "jobID")
		if jobID == "" {
			writeError(w, http.StatusBadRequest, "missing job id")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"job_id": jobID,
			"status": "SUCCESS",
		})
	}
}
