package db

import (
	"context"
	_ "embed"
	"fmt"

	surrealdb "github.com/surrealdb/surrealdb.go"

	"github.com/Genentech/exohub/go/adb-standalone/internal/config"
)

//go:embed schema.surql
var schemaSurQL string

// DB wraps a SurrealDB connection.
type DB struct {
	inner *surrealdb.DB
}

// Connect opens a connection to SurrealDB, authenticates, and selects the
// namespace/database specified in cfg.
func Connect(ctx context.Context, cfg *config.SurrealConfig) (*DB, error) {
	inner, err := surrealdb.FromEndpointURLString(ctx, cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("surrealdb connect %q: %w", cfg.URL, err)
	}

	if cfg.Username != "" {
		if _, err := inner.SignIn(ctx, surrealdb.Auth{
			Username: cfg.Username,
			Password: cfg.Password,
		}); err != nil {
			_ = inner.Close(ctx)
			return nil, fmt.Errorf("surrealdb signin: %w", err)
		}
	}

	if err := inner.Use(ctx, cfg.NS, cfg.DB); err != nil {
		_ = inner.Close(ctx)
		return nil, fmt.Errorf("surrealdb use %q/%q: %w", cfg.NS, cfg.DB, err)
	}

	return &DB{inner: inner}, nil
}

// Close shuts down the connection.
func (d *DB) Close(ctx context.Context) error {
	return d.inner.Close(ctx)
}

// Inner returns the underlying surrealdb.DB for use by other packages.
func (d *DB) Inner() *surrealdb.DB {
	return d.inner
}

// InitSchema applies the embedded SurrealQL schema idempotently.
// The full script is executed as a single multi-statement batch so that
// future additions (string literals, complex filter options) are safe.
// When semanticModelPath is non-empty, the vector index is also defined.
func (d *DB) InitSchema(ctx context.Context, semanticModelPath string, dimensions int) error {
	stmts := schemaSurQL

	if semanticModelPath != "" {
		stmts += fmt.Sprintf(`
DEFINE FIELD IF NOT EXISTS embedding ON document TYPE array<float>;
DEFINE INDEX IF NOT EXISTS doc_vec ON document FIELDS embedding MTREE DIMENSION %d DIST COSINE;
`, dimensions)
	}

	if _, err := surrealdb.Query[any](ctx, d.inner, stmts, nil); err != nil {
		return fmt.Errorf("schema init: %w", err)
	}
	return nil
}
