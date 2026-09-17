package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

type schemasResponse struct {
	DocumentTypes []map[string]any `json:"document_types"`
}

// Schemas handles GET /v1/{tenant}/schemas
func Schemas(db *surrealdb.DB, baseURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenant := chi.URLParam(r, "tenant")
		ctx := r.Context()

		sql := `SELECT name FROM schema_type ORDER BY name ASC`
		type nameRow struct {
			Name string `json:"name"`
		}

		res, err := surrealdb.Query[[]nameRow](ctx, db, sql, nil)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("schemas query error: %v", err))
			return
		}

		var docTypes []map[string]any
		if res != nil && len(*res) > 0 && (*res)[0].Error == nil {
			for _, row := range (*res)[0].Result {
				docTypes = append(docTypes, map[string]any{
					"name": row.Name,
					"url":  fmt.Sprintf("%s/v1/%s/schemas/%s", baseURL, tenant, row.Name),
				})
			}
		}

		if docTypes == nil {
			docTypes = []map[string]any{}
		}

		resp := schemasResponse{DocumentTypes: docTypes}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// SeedSchemasFromList upserts schema_type records for the given names.
func SeedSchemasFromList(ctx context.Context, db *surrealdb.DB, names []string) error {
	for _, name := range names {
		rid := models.NewRecordID("schema_type", name)
		_, err := surrealdb.Upsert[map[string]any](ctx, db, rid, map[string]any{"name": name, "json": map[string]any{}})
		if err != nil {
			return fmt.Errorf("upsert schema_type %q: %w", name, err)
		}
	}
	return nil
}
