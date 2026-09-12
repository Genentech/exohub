package handler

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	surrealdb "github.com/surrealdb/surrealdb.go"
)

type entitiesResponse struct {
	Entity            map[string]any   `json:"entity"`
	Relationships     []map[string]any `json:"relationships"`
	RelationshipError string           `json:"relationship_error,omitempty"`
}

// Entities handles GET /v1/{tenant}/entities?name=<name>[&include_relationships=false].
func Entities(db *surrealdb.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		if name == "" {
			writeError(w, http.StatusBadRequest, "parameter 'name' is required")
			return
		}

		includeRel := true
		if v := r.URL.Query().Get("include_relationships"); v == "false" {
			includeRel = false
		}

		ctx := r.Context()

		// Look up entity by name.
		res, err := surrealdb.Query[[]map[string]any](ctx, db,
			`SELECT * FROM entity WHERE name = $name LIMIT 1`,
			map[string]any{"name": name},
		)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("db error: %v", err))
			return
		}

		var entity map[string]any
		if res != nil && len(*res) > 0 {
			qr := (*res)[0]
			if qr.Error == nil && len(qr.Result) > 0 {
				entity = qr.Result[0]
			}
		}

		if entity == nil {
			writeError(w, http.StatusNotFound, fmt.Sprintf("entity not found: %s", name))
			return
		}

		var relationships []map[string]any
		var relationshipErr string
		if includeRel {
			relRes, relQueryErr := surrealdb.Query[[]map[string]any](ctx, db,
				`SELECT * FROM relationship WHERE subject = $name OR object = $name`,
				map[string]any{"name": name},
			)
			if relQueryErr != nil {
				log.Printf("entities: relationship query for %q: %v", name, relQueryErr)
				relationshipErr = fmt.Sprintf("relationship lookup failed: %v", relQueryErr)
			} else if relRes != nil && len(*relRes) > 0 {
				qr := (*relRes)[0]
				if qr.Error != nil {
					log.Printf("entities: relationship query error for %q: %v", name, qr.Error)
					relationshipErr = fmt.Sprintf("relationship lookup failed: %v", qr.Error)
				} else if qr.Result != nil {
					relationships = qr.Result
				}
			}
		}

		if relationships == nil {
			relationships = []map[string]any{}
		}

		resp := entitiesResponse{Entity: entity, Relationships: relationships}
		if relationshipErr != "" {
			resp.RelationshipError = relationshipErr
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}
