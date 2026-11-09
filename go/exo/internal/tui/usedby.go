package tui

import (
	"fmt"
	"strings"

	"github.com/Genentech/exohub/go/exo/internal/provenance"
)

// isArtifactDocument reports whether md is an exohub-artifact or
// exohub-markdown document, the only document types that carry the
// backend/key/lineage fields a "used by" search needs.
func isArtifactDocument(md *markdown) bool {
	schema := md.Extra.Schema
	return strings.HasPrefix(schema, "exohub-artifact/") || strings.HasPrefix(schema, "exohub-markdown/")
}

// contentIDForDocument derives md's content id: a git blob id for backend
// "git", otherwise an annex key id (schemas/exohub/exohub-artifact/v1.json
// "key"/"backend").
func contentIDForDocument(md *markdown) (string, bool) {
	key, _ := md.Result.Metadata["key"].(string)
	if key == "" {
		return "", false
	}
	backend, _ := md.Result.Metadata["backend"].(string)
	if backend == "git" {
		return provenance.GitBlobEntityID(key), true
	}
	return provenance.AnnexEntityID(key), true
}

// collectionIDForDocument returns md's folder collection id
// (lineage.collection.id), if the document is a member of one.
func collectionIDForDocument(md *markdown) (string, bool) {
	lineage, ok := md.Result.Metadata["lineage"].(map[string]interface{})
	if !ok {
		return "", false
	}
	collection, ok := lineage["collection"].(map[string]interface{})
	if !ok {
		return "", false
	}
	id, ok := collection["id"].(string)
	if !ok || id == "" {
		return "", false
	}
	return id, true
}

// documentPath returns md's catalog path, falling back to its ArtifactDB id.
func documentPath(md *markdown) string {
	if path, ok := md.Result.Metadata["path"].(string); ok && path != "" {
		return path
	}
	return md.ArfifactDBID
}

// usedByQuery builds the catalog query for md's "used by" search (downstream
// lineage): other documents whose lineage.inputs reference md's content id,
// OR'd with its folder collection id when md is a member of one. It reports
// ok=false when md has no usable content id (e.g. missing "key").
func usedByQuery(md *markdown) (query string, path string, ok bool) {
	contentID, ok := contentIDForDocument(md)
	if !ok {
		return "", "", false
	}

	ids := []string{contentID}
	if collectionID, collOK := collectionIDForDocument(md); collOK {
		ids = append(ids, collectionID)
	}

	clauses := make([]string, len(ids))
	for i, id := range ids {
		clauses[i] = fmt.Sprintf(`lineage.inputs.id:"%s"`, id)
	}

	query = strings.Join(clauses, " OR ")
	if len(clauses) > 1 {
		// generateQuery joins this query with other filters using " AND ", which
		// binds tighter than "OR" ("filter AND a OR b" == "(filter AND a) OR b").
		// Parenthesize multi-clause queries so the OR stays scoped to this search.
		query = "(" + query + ")"
	}

	return query, documentPath(md), true
}
