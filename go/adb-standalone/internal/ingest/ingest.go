// Package ingest implements synchronous bundle ingestion into SurrealDB.
// Versions come from git (the caller-supplied version/ref_name field); no sequences.
package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"path/filepath"
	"sort"
	"strings"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"

	"github.com/Genentech/exohub/go/adb-standalone/internal/config"
	"github.com/Genentech/exohub/go/adb-standalone/internal/embed"
	"github.com/Genentech/exohub/go/adb-standalone/internal/seed"
)

// Request holds the parsed POST /project/ingest body.
type Request struct {
	Schema     string `json:"$schema"`
	ProjectID  string `json:"project_id"`
	Version    string `json:"version"`
	S3Location string `json:"s3_location"`
	S3URL      string `json:"s3url"`
}

// Result summarises a completed ingest.
type Result struct {
	JobID           string `json:"job_id"`
	Ingested        int    `json:"ingested"`
	Errors          int    `json:"errors"`
	EmbeddingErrors int    `json:"embedding_errors,omitempty"`
}

// IngestBundle reads the bundle using the provided reader, computes _extra for
// each metadata document, upserts into SurrealDB, then recomputes the latest
// flag for the project. Returns a synthetic job ID (always complete).
// embedder may be nil; when non-nil, each document is embedded and stored in
// the embedding field for semantic search.
func IngestBundle(ctx context.Context, db *surrealdb.DB, cfg *config.Config, req Request, reader BundleReader, embedder embed.Embedder) (Result, error) {
	if req.ProjectID == "" {
		return Result{}, fmt.Errorf("project_id is required")
	}
	if req.Version == "" {
		return Result{}, fmt.Errorf("version is required")
	}
	if reader == nil {
		return Result{}, fmt.Errorf("bundle reader is required")
	}

	permissions := bundlePermissions(cfg)
	tenant := defaultTenant(cfg)
	now := time.Now().UTC().Format(time.RFC3339Nano)

	var res Result
	res.JobID = syntheticJobID(req.ProjectID, req.Version)

	paths, err := reader.ListPaths()
	if err != nil {
		return Result{}, fmt.Errorf("list bundle paths: %w", err)
	}

	for _, relPath := range paths {
		rc, err := reader.Open(relPath)
		if err != nil {
			res.Errors++
			continue
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			res.Errors++
			continue
		}

		var doc seed.Document
		if err := json.Unmarshal(data, &doc); err != nil {
			res.Errors++
			continue
		}

		extra := computeExtra(doc, req.ProjectID, req.Version, relPath, cfg, permissions, tenant, now)
		doc["_extra"] = extra
		searchText := seed.BuildSearchText(doc)
		doc["search_text"] = searchText
		id := extra["id"].(string)

		if embedder != nil {
			vec, embedErr := embedder.Embed(searchText)
			if embedErr != nil {
				log.Printf("WARNING: embed failed for doc %q: %v — stored without embedding", id, embedErr)
				res.EmbeddingErrors++
			} else {
				doc["embedding"] = vec
			}
		}
		rid := models.NewRecordID("document", id)
		if _, err := surrealdb.Upsert[seed.Document](ctx, db, rid, doc); err != nil {
			res.Errors++
			continue
		}
		res.Ingested++
	}

	if err := recomputeLatest(ctx, db, req.ProjectID); err != nil {
		return res, fmt.Errorf("recompute latest: %w", err)
	}

	return res, nil
}

// computeExtra builds the _extra map for a single metadata document.
// relPath is the metadata file path relative to the bundle root.
// For artifact files (files/*.json), the ADB id path uses doc["path"] (the
// actual file path without files/ prefix or .json suffix).
func computeExtra(
	doc seed.Document,
	projectID, version, relPath string,
	cfg *config.Config,
	permissions map[string]any,
	tenant map[string]any,
	now string,
) map[string]any {
	metapath := relPath

	// The ADB id path: for artifact files under files/, doc["path"] is the
	// bare file path (e.g. "releases/foo.dmg"); for bundle/commit docs it equals relPath.
	idPath := metapath
	if p, ok := doc["path"].(string); ok && p != "" {
		idPath = p
	}

	adbID := fmt.Sprintf("%s:%s@%s", projectID, idPath, version)

	gprn := fmt.Sprintf("gprn:%s:%s::%s:%s",
		cfg.GPRN.Service,
		cfg.GPRN.Environment,
		cfg.GPRN.Placeholder,
		adbID,
	)

	schema := ""
	if s, ok := doc["$schema"].(string); ok {
		schema = s
	}

	location := map[string]any{
		"type": "s3",
		"s3": map[string]any{
			"bucket": cfg.Storage.S3.Bucket,
		},
	}

	extra := map[string]any{
		"$schema":       schema,
		"type":          schemaToType(schema),
		"id":            adbID,
		"gprn":          gprn,
		"project_id":    projectID,
		"version":       version,
		"latest":        false,
		"permissions":   permissions,
		"location":      location,
		"tenant":        tenant,
		"metapath":      metapath,
		"meta_indexed":  now,
		"meta_uploaded": now,
	}

	if existingExtra, ok := doc["_extra"].(map[string]any); ok {
		if fs, exists := existingExtra["file_size"]; exists {
			extra["file_size"] = fs
		}
	}

	return extra
}

// recomputeLatest sets _extra.latest=true on all documents at the highest
// semver for the given project and false on all others.
func recomputeLatest(ctx context.Context, db *surrealdb.DB, projectID string) error {
	sql := `SELECT _extra.version FROM document WHERE _extra.project_id = $pid GROUP BY _extra.version`
	type vRow struct {
		Extra struct {
			Version string `json:"version"`
		} `json:"_extra"`
	}
	res, err := surrealdb.Query[[]vRow](ctx, db, sql, map[string]any{"pid": projectID})
	if err != nil {
		return err
	}
	if res == nil || len(*res) == 0 {
		return nil
	}
	qr := (*res)[0]
	if qr.Error != nil {
		return qr.Error
	}

	versions := make([]string, 0, len(qr.Result))
	for _, r := range qr.Result {
		if r.Extra.Version != "" {
			versions = append(versions, r.Extra.Version)
		}
	}
	if len(versions) == 0 {
		return nil
	}

	sort.Slice(versions, func(i, j int) bool {
		return compareSemver(versions[i], versions[j]) > 0
	})
	latestVersion := versions[0]

	// Wrap both UPDATEs in a transaction to eliminate the window where all docs
	// have latest=false for concurrent readers.
	txSQL := `
BEGIN TRANSACTION;
UPDATE document SET _extra.latest = false WHERE _extra.project_id = $pid;
UPDATE document SET _extra.latest = true WHERE _extra.project_id = $pid AND _extra.version = $ver;
COMMIT TRANSACTION;
`
	if _, err := surrealdb.Query[any](ctx, db, txSQL, map[string]any{"pid": projectID, "ver": latestVersion}); err != nil {
		return fmt.Errorf("update latest: %w", err)
	}

	return nil
}

// bundlePermissions returns default permissions from config.
// When .exohub/permissions is available via the bundle reader, callers may
// override this; the local-FS reader handles this via the storage seam.
func bundlePermissions(cfg *config.Config) map[string]any {
	return map[string]any{
		"read_access":  cfg.Permissions.DefaultRead,
		"write_access": cfg.Permissions.DefaultWrite,
		"scope":        "project",
		"owners":       []string{},
	}
}

func defaultTenant(cfg *config.Config) map[string]any {
	if len(cfg.Tenants) > 0 {
		t := cfg.Tenants[0]
		return map[string]any{"path": t.Path, "alias": t.Alias}
	}
	return map[string]any{"path": "/adb", "alias": "adb"}
}

func syntheticJobID(projectID, version string) string {
	return fmt.Sprintf("%s@%s", projectID, version)
}

// schemaToType derives the human-readable document type from the schema URI.
// The schema title is encoded in the URI prefix before the first '/': e.g.
// "exohub-bundle/v1.json" → "exohub bundle" (hyphens replaced by spaces).
// This avoids a hardcoded switch that would need updating for every new schema.
func schemaToType(schema string) string {
	if schema == "" {
		return ""
	}
	name := schema
	if idx := strings.Index(schema, "/"); idx >= 0 {
		name = schema[:idx]
	}
	return strings.ReplaceAll(name, "-", " ")
}

// compareSemver compares two version strings of the form vX.Y.Z.
// Returns positive if a > b, negative if a < b, 0 if equal.
func compareSemver(a, b string) int {
	pa := parseSemver(a)
	pb := parseSemver(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			return pa[i] - pb[i]
		}
	}
	return 0
}

func parseSemver(v string) [3]int {
	v = strings.TrimPrefix(v, "v")
	parts := strings.SplitN(v, ".", 3)
	var out [3]int
	for i, p := range parts {
		if i >= 3 {
			break
		}
		n := 0
		for _, c := range p {
			if c >= '0' && c <= '9' {
				n = n*10 + int(c-'0')
			} else {
				break
			}
		}
		out[i] = n
	}
	return out
}

// ReaderForRequest creates a BundleReader appropriate for the given request.
// Returns a local-FS reader; NewLocalReader canonicalizes with EvalSymlinks
// and rejects path traversal. S3 bundle reading will reuse the aws-sdk-go-v2
// client from internal/storage/s3.go in a future slice.
func ReaderForRequest(req Request) (BundleReader, error) {
	if req.S3Location == "" {
		return nil, fmt.Errorf("s3_location is required")
	}
	// Resolve to absolute path so testdata relative refs work from any cwd.
	abs, err := filepath.Abs(req.S3Location)
	if err != nil {
		return nil, fmt.Errorf("resolve s3_location: %w", err)
	}
	return NewLocalReader(abs)
}
