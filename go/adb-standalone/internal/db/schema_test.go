package db

import (
	"context"
	"net"
	"os"
	"os/exec"
	"testing"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"
)

// surrealTestURL is set in TestMain when a surreal process is started.
var surrealTestURL string

// TestMain starts an in-memory SurrealDB if a surreal binary is available,
// otherwise all integration tests are skipped.
func TestMain(m *testing.M) {
	bin := os.Getenv("SURREAL_BIN")
	if bin == "" {
		if path, err := exec.LookPath("surreal"); err == nil {
			bin = path
		}
	}

	if bin == "" {
		os.Exit(m.Run())
	}

	// Acquire an ephemeral port to avoid collisions.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic("failed to find free port: " + err.Error())
	}
	addr := ln.Addr().String()
	ln.Close()

	surrealTestURL = "ws://" + addr

	cmd := exec.Command(bin,
		"start", "memory",
		"--bind", addr,
		"--user", "root",
		"--pass", "root",
		"--log", "none",
	)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		panic("failed to start surreal: " + err.Error())
	}
	defer func() { _ = cmd.Process.Kill() }()

	time.Sleep(800 * time.Millisecond)

	// Verify the process is still alive after the sleep.
	if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
		panic("surreal process exited unexpectedly; check port availability or binary")
	}

	os.Exit(m.Run())
}

func requireSurreal(t *testing.T, database string) *DB {
	t.Helper()
	if surrealTestURL == "" {
		t.Skip("surreal binary not found — skipping SurrealDB integration test")
	}

	cfg := mustSurrealConfig(surrealTestURL, "test", database, "root", "root")
	d, err := Connect(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = d.Close(context.Background()) })
	return d
}

func TestInitSchemaIdempotent(t *testing.T) {
	d := requireSurreal(t, "test_schema_idempotent")
	ctx := context.Background()

	if err := d.InitSchema(ctx, "", 0); err != nil {
		t.Fatalf("InitSchema (1st): %v", err)
	}
	if err := d.InitSchema(ctx, "", 0); err != nil {
		t.Fatalf("InitSchema (2nd, idempotency): %v", err)
	}
}

func TestInitSchemaWithVector(t *testing.T) {
	d := requireSurreal(t, "test_schema_vector")
	ctx := context.Background()

	if err := d.InitSchema(ctx, "/some/model.onnx", 256); err != nil {
		t.Fatalf("InitSchema with vector: %v", err)
	}
	if err := d.InitSchema(ctx, "/some/model.onnx", 256); err != nil {
		t.Fatalf("InitSchema with vector (2nd): %v", err)
	}
}

func TestInitSchemaEntityRelationshipTables(t *testing.T) {
	d := requireSurreal(t, "test_schema_entity_rel")
	ctx := context.Background()

	if err := d.InitSchema(ctx, "", 0); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}

	// Insert an entity and a relationship to verify the tables exist and are writable.
	_, err := surrealdb.Query[any](ctx, d.Inner(), `
		INSERT INTO entity (entity_id, name, type, summary, source_docs) VALUES ("e1", "BRCA1", "gene", "tumor suppressor", []);
	`, nil)
	if err != nil {
		t.Fatalf("insert entity: %v", err)
	}
	_, err = surrealdb.Query[any](ctx, d.Inner(), `
		INSERT INTO relationship (rel_id, subject, predicate, object) VALUES ("r1", "BRCA1", "associated_with", "breast cancer");
	`, nil)
	if err != nil {
		t.Fatalf("insert relationship: %v", err)
	}
}
