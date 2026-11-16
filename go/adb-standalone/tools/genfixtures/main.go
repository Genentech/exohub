// Command genfixtures regenerates the adb-standalone parity test fixtures
// (seed/documents.ndjson, fixtures/search_p1.json, fixtures/search_p2.json)
// from a small, deterministic, fully synthetic corpus — no captured data.
//
// Usage (from go/adb-standalone):
//
//	SURREAL_BIN=/path/to/surreal go run ./tools/genfixtures [--out <fixtures-dir>]
//
// It generates the synthetic documents in-process, starts a throwaway
// in-memory SurrealDB instance (the same way the parity test suite does),
// seeds it, serves /search through the real handler stack, and writes the
// raw responses (keys sorted, scroll token pinned) as the golden fixtures.
// Re-running it must reproduce byte-identical output (verified by
// genfixtures_test.go and `make verify-fixtures`).
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/Genentech/exohub/go/adb-standalone/internal/config"
	"github.com/Genentech/exohub/go/adb-standalone/internal/db"
	"github.com/Genentech/exohub/go/adb-standalone/internal/seed"
	"github.com/Genentech/exohub/go/adb-standalone/internal/server"
)

func main() {
	outDir := flag.String("out", defaultFixturesDir(), "fixtures output directory")
	flag.Parse()

	bin, err := resolveSurrealBin()
	if err != nil {
		fmt.Fprintf(os.Stderr, "genfixtures: %v\n", err)
		os.Exit(1)
	}
	if err := generateFixtures(context.Background(), bin, *outDir); err != nil {
		fmt.Fprintf(os.Stderr, "genfixtures: %v\n", err)
		os.Exit(1)
	}
}

// resolveSurrealBin finds the surreal binary via SURREAL_BIN or PATH.
func resolveSurrealBin() (string, error) {
	bin := os.Getenv("SURREAL_BIN")
	if bin == "" {
		if p, err := exec.LookPath("surreal"); err == nil {
			bin = p
		}
	}
	if bin == "" {
		return "", fmt.Errorf("surreal binary not found; set SURREAL_BIN or put surreal on PATH")
	}
	return bin, nil
}

// generateFixtures runs the full pipeline: build the synthetic corpus, write
// seed/documents.ndjson, seed an ephemeral SurrealDB, drive /search through
// the real handler stack, and write search_p1.json / search_p2.json.
// Exercised directly by genfixtures_test.go so the end-to-end path (not just
// buildCorpus) has test coverage, skipping loudly when bin is unusable.
func generateFixtures(ctx context.Context, bin, outDir string) error {
	docs := buildCorpus()

	if err := os.MkdirAll(filepath.Join(outDir, "seed"), 0o755); err != nil {
		return fmt.Errorf("mkdir seed dir: %w", err)
	}
	ndjsonPath := filepath.Join(outDir, "seed", "documents.ndjson")
	if err := writeNDJSON(ndjsonPath, docs); err != nil {
		return fmt.Errorf("write %s: %w", ndjsonPath, err)
	}
	fmt.Fprintf(os.Stderr, "wrote %d synthetic documents to %s\n", len(docs), ndjsonPath)

	dbConn, cleanup, err := startEphemeralDB(ctx, bin)
	if err != nil {
		return fmt.Errorf("start ephemeral db: %w", err)
	}
	defer cleanup()

	f, err := os.Open(ndjsonPath)
	if err != nil {
		return fmt.Errorf("reopen ndjson: %w", err)
	}
	res, err := seed.Seed(ctx, dbConn.Inner(), f, os.Stderr, nil)
	f.Close()
	if err != nil {
		return fmt.Errorf("seed: %w", err)
	}
	fmt.Fprintf(os.Stderr, "seeded %d docs (%d errors)\n", res.Loaded, res.Errors)

	srv := server.NewWithBaseURL("", dbConn, "http://localhost")
	ts := httptest.NewServer(srv)
	defer ts.Close()

	page1, next, err := fetchSearchPage(ts.URL, "exohub", "/search?q=*&size=2")
	if err != nil {
		return fmt.Errorf("fetch search page 1: %w", err)
	}
	if next == "" {
		return fmt.Errorf("search page 1 has no 'next' link — corpus too small for pagination")
	}
	page2, _, err := fetchSearchPage(ts.URL, "exohub", next)
	if err != nil {
		return fmt.Errorf("fetch search page 2: %w", err)
	}

	if err := writeCanonicalFixture(filepath.Join(outDir, "search_p1.json"), page1); err != nil {
		return err
	}
	if err := writeCanonicalFixture(filepath.Join(outDir, "search_p2.json"), page2); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote search_p1.json and search_p2.json to %s\n", outDir)

	return nil
}

func defaultFixturesDir() string {
	return filepath.Join("..", "..", "docs", "adb-standalone", "fixtures")
}

func writeNDJSON(path string, docs []document) error {
	var buf bytes.Buffer
	for _, d := range docs {
		b, err := json.Marshal(d)
		if err != nil {
			return err
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// deterministicScrollID replaces the server-assigned (random) scroll session
// token with a fixed placeholder so re-running the generator produces
// byte-identical fixtures. The parity test normalizer already treats any
// "/scroll/<token>" value as equivalent, so this has no effect on gate G1.
const deterministicScrollID = "/scroll/deterministic-test-scroll-id"

// writeCanonicalFixture writes body as pretty-printed JSON with keys sorted
// (matching the format of the existing committed fixtures), after pinning
// the scroll token to a deterministic placeholder.
func writeCanonicalFixture(path string, body []byte) error {
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return fmt.Errorf("unmarshal %s: %w", path, err)
	}
	if m, ok := v.(map[string]any); ok {
		if next, ok := m["next"].(string); ok && next != "" {
			m["next"] = deterministicScrollID
		}
	}
	pretty, err := json.MarshalIndent(v, "", "    ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	return os.WriteFile(path, pretty, 0o644)
}

func fetchSearchPage(baseURL, tenant, urlPath string) (body []byte, next string, err error) {
	resp, err := http.Get(baseURL + "/v1/" + tenant + urlPath)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	body, err = io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("GET %s: status %d: %s", urlPath, resp.StatusCode, body)
	}
	var page struct {
		Next string `json:"next"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, "", fmt.Errorf("unmarshal page: %w", err)
	}
	return body, page.Next, nil
}

// startEphemeralDB launches an in-memory SurrealDB instance on a free port
// and returns a connected, schema-initialized db.DB plus a cleanup func.
// Mirrors parity_test.go's TestMain setup so genfixtures and the parity
// suite stay in lockstep.
func startEphemeralDB(ctx context.Context, bin string) (*db.DB, func(), error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, nil, fmt.Errorf("find free port: %w", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	cmd := exec.Command(bin,
		"start", "memory",
		"--bind", addr,
		"--user", "root", "--pass", "root",
		"--log", "none",
	)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("start surreal: %w", err)
	}

	healthURL := "http://" + addr + "/health"
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if resp, err := http.Get(healthURL); err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			return nil, nil, fmt.Errorf("surreal exited early")
		}
		time.Sleep(50 * time.Millisecond)
	}

	cfg := &config.SurrealConfig{
		URL: "ws://" + addr, NS: "genfixtures", DB: "genfixtures",
		Username: "root", Password: "root",
	}
	dbConn, err := db.Connect(ctx, cfg)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, nil, fmt.Errorf("db connect: %w", err)
	}
	if err := dbConn.InitSchema(ctx, "", 0); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, nil, fmt.Errorf("schema init: %w", err)
	}

	cleanup := func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}
	return dbConn, cleanup, nil
}
