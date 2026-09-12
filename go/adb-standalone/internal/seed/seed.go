package seed

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"sort"
	"strings"

	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"

	"github.com/Genentech/exohub/go/adb-standalone/internal/embed"
)

// Document is a loosely-typed ADB document as stored in SurrealDB.
type Document map[string]any

// Result holds the outcome of a Seed call.
type Result struct {
	Loaded          int
	Errors          int
	EmbeddingErrors int
}

// Seed reads NDJSON documents from r and upserts them into the document table.
// Each line must be a JSON object with an "_extra.id" field.
// Progress is written to w (may be nil).
// embedder may be nil; when non-nil, documents are embedded for semantic search.
func Seed(ctx context.Context, db *surrealdb.DB, r io.Reader, progress io.Writer, embedder embed.Embedder) (Result, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 4*1024*1024), 4*1024*1024) // 4 MB per line

	var res Result
	lineNo := 0

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		lineNo++
		if line == "" {
			continue
		}

		var doc Document
		if err := json.Unmarshal([]byte(line), &doc); err != nil {
			res.Errors++
			logf(progress, "line %d: json parse error: %v\n", lineNo, err)
			continue
		}

		id, err := extractID(doc)
		if err != nil {
			res.Errors++
			logf(progress, "line %d: %v\n", lineNo, err)
			continue
		}

		searchText := BuildSearchText(doc)
		doc["search_text"] = searchText

		if embedder != nil {
			if vec, embedErr := embedder.Embed(searchText); embedErr != nil {
				log.Printf("WARNING: seed embed failed for doc %q: %v — stored without embedding", id, embedErr)
				res.EmbeddingErrors++
			} else {
				doc["embedding"] = vec
			}
		}

		rid := models.NewRecordID("document", id)
		if _, err := surrealdb.Upsert[Document](ctx, db, rid, doc); err != nil {
			res.Errors++
			logf(progress, "line %d: upsert %q: %v\n", lineNo, id, err)
			continue
		}

		res.Loaded++
		if progress != nil && res.Loaded%100 == 0 {
			logf(progress, "  … %d documents loaded\n", res.Loaded)
		}
	}

	if err := scanner.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return res, fmt.Errorf("reading input: line %d exceeds scanner buffer limit (4 MB); document may be too large", lineNo)
		}
		return res, fmt.Errorf("reading input: %w", err)
	}

	return res, nil
}

// extractID retrieves _extra.id from a document map.
func extractID(doc Document) (string, error) {
	extra, ok := doc["_extra"].(map[string]any)
	if !ok {
		return "", fmt.Errorf("missing or invalid _extra field")
	}
	id, ok := extra["id"].(string)
	if !ok || id == "" {
		return "", fmt.Errorf("missing or empty _extra.id")
	}
	return id, nil
}

// BuildSearchText concatenates all string-valued top-level fields (excluding
// "_extra") and adds _extra.type, _extra.project_id, and _extra.$schema for
// full-text coverage. Keys are sorted for deterministic output.
func BuildSearchText(doc Document) string {
	// Collect and sort top-level keys for deterministic ordering.
	keys := make([]string, 0, len(doc))
	for k := range doc {
		if k != "_extra" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	var parts []string
	for _, k := range keys {
		if s, ok := doc[k].(string); ok && s != "" {
			parts = append(parts, s)
		}
	}

	if extra, ok := doc["_extra"].(map[string]any); ok {
		for _, field := range []string{"type", "project_id", "$schema"} {
			if s, ok := extra[field].(string); ok && s != "" {
				parts = append(parts, s)
			}
		}
	}

	return strings.Join(parts, " ")
}

func logf(w io.Writer, format string, args ...any) {
	if w != nil {
		fmt.Fprintf(w, format, args...)
	}
}
