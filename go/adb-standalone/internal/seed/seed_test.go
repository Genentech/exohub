package seed

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"

	"github.com/Genentech/exohub/go/adb-standalone/internal/config"
	"github.com/Genentech/exohub/go/adb-standalone/internal/db"
)

var surrealTestURL string

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

func requireDB(t *testing.T, database string) (*db.DB, *surrealdb.DB) {
	t.Helper()
	if surrealTestURL == "" {
		t.Skip("surreal binary not found — skipping SurrealDB integration test")
	}

	cfg := &config.SurrealConfig{
		URL:      surrealTestURL,
		NS:       "test",
		DB:       database,
		Username: "root",
		Password: "root",
	}
	d, err := db.Connect(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := d.InitSchema(context.Background(), "", 0); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	t.Cleanup(func() { _ = d.Close(context.Background()) })
	return d, d.Inner()
}

func makeNDJSON(n int) string {
	var sb strings.Builder
	for i := 0; i < n; i++ {
		doc := map[string]any{
			"path": fmt.Sprintf("file%d.txt", i),
			"size": i * 100,
			"_extra": map[string]any{
				"id":         fmt.Sprintf("test-project:file%d.txt@v1.0.0", i),
				"project_id": "test-project",
				"version":    "v1.0.0",
				"type":       "exohub artifact",
				"$schema":    "exohub-artifact/v1.json",
				"latest":     true,
				"permissions": map[string]any{
					"read_access":  "public",
					"write_access": "owners",
				},
			},
		}
		b, _ := json.Marshal(doc)
		sb.Write(b)
		sb.WriteByte('\n')
	}
	return sb.String()
}

func TestSeedCount(t *testing.T) {
	const n = 20
	_, inner := requireDB(t, "test_seed_count")

	ndjson := makeNDJSON(n)
	res, err := Seed(context.Background(), inner, strings.NewReader(ndjson), os.Stderr, nil)
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if res.Errors != 0 {
		t.Fatalf("Seed reported %d errors", res.Errors)
	}
	if res.Loaded != n {
		t.Fatalf("Seed loaded %d, want %d", res.Loaded, n)
	}

	// Verify via SELECT count()
	type CountResult struct {
		C int `json:"count"`
	}
	results, err := surrealdb.Query[[]CountResult](
		context.Background(), inner,
		"SELECT count() AS count FROM document GROUP ALL",
		nil,
	)
	if err != nil {
		t.Fatalf("count query: %v", err)
	}
	if len(*results) == 0 || len((*results)[0].Result) == 0 {
		t.Fatal("count query returned no results")
	}
	got := (*results)[0].Result[0].C
	if got != n {
		t.Fatalf("SELECT count() = %d, want %d", got, n)
	}
}

func TestSeedIdempotent(t *testing.T) {
	const n = 5
	_, inner := requireDB(t, "test_seed_idempotent")

	ndjson := makeNDJSON(n)

	for i := 0; i < 2; i++ {
		res, err := Seed(context.Background(), inner, strings.NewReader(ndjson), nil, nil)
		if err != nil {
			t.Fatalf("Seed (run %d): %v", i+1, err)
		}
		if res.Errors != 0 {
			t.Fatalf("run %d: %d errors", i+1, res.Errors)
		}
	}

	type CountResult struct {
		C int `json:"count"`
	}
	results, err := surrealdb.Query[[]CountResult](
		context.Background(), inner,
		"SELECT count() AS count FROM document GROUP ALL",
		nil,
	)
	if err != nil {
		t.Fatalf("count query: %v", err)
	}
	if len(*results) == 0 || len((*results)[0].Result) == 0 {
		t.Fatal("count query returned no results")
	}
	got := (*results)[0].Result[0].C
	if got != n {
		t.Fatalf("SELECT count() = %d after 2 seeds, want %d (upsert must be idempotent)", got, n)
	}
}

func TestBuildSearchText(t *testing.T) {
	doc := Document{
		"path": "bundle.json",
		"ref":  "v1.2.3",
		"size": float64(999), // numeric — should be excluded
		"_extra": map[string]any{
			"id":         "proj:bundle.json@v1.2.3",
			"type":       "exohub bundle",
			"project_id": "proj",
			"$schema":    "exohub-bundle/v1.json",
		},
	}

	text := BuildSearchText(doc)

	for _, want := range []string{"bundle.json", "v1.2.3", "exohub bundle", "proj", "exohub-bundle/v1.json"} {
		if !strings.Contains(text, want) {
			t.Errorf("search_text %q missing %q", text, want)
		}
	}

	// Verify determinism: two calls must return identical strings.
	text2 := BuildSearchText(doc)
	if text != text2 {
		t.Errorf("BuildSearchText is non-deterministic: %q != %q", text, text2)
	}
}
