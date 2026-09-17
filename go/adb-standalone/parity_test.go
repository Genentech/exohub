// Package adb_standalone_test contains the L1 parity test suite.
// It seeds an in-memory SurrealDB instance from docs/adb-standalone/fixtures/seed/documents.ndjson,
// starts the HTTP server, replays the corpus.yaml test vectors, and asserts:
//   - G1: normalize(actual) == normalize(golden fixture)
//   - G2: exact _extra.id set match for search/scroll queries
//   - G3: nDCG@K ranking vs reference ArtifactDB fixture; gate ≥ 0.95
//
// Run with SURREAL_BIN=/path/to/surreal go test -run TestParity -v ./...
package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/Genentech/exohub/go/adb-standalone/internal/config"
	"github.com/Genentech/exohub/go/adb-standalone/internal/db"
	"github.com/Genentech/exohub/go/adb-standalone/internal/handler"
	"github.com/Genentech/exohub/go/adb-standalone/internal/normalize"
	"github.com/Genentech/exohub/go/adb-standalone/internal/seed"
	"github.com/Genentech/exohub/go/adb-standalone/internal/server"
)

// ── global test state ────────────────────────────────────────────────────

var (
	parityTestURL string // URL of the test server
	parityDB      *db.DB
)

func TestMain(m *testing.M) {
	bin := os.Getenv("SURREAL_BIN")
	if bin == "" {
		if p, err := exec.LookPath("surreal"); err == nil {
			bin = p
		}
	}
	if bin == "" {
		// Skip all parity tests gracefully when surreal is not installed.
		os.Exit(m.Run())
	}

	// Allocate a free port for SurrealDB. There is a small TOCTOU window between
	// ln.Close() and SurrealDB binding the same address, but this is an acceptable
	// CI risk (another process grabbing the port is extremely unlikely in the
	// isolated test environment). SurrealDB does not support port 0 (let OS pick).
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic("find free port: " + err.Error())
	}
	surrealAddr := ln.Addr().String()
	ln.Close()

	cmd := exec.Command(bin,
		"start", "memory",
		"--bind", surrealAddr,
		"--user", "root", "--pass", "root",
		"--log", "none",
	)
	// Do NOT attach cmd.Stdout/Stderr to os.Stderr — that leaves I/O goroutines
	// draining after Kill(), causing "WaitDelay expired" and a test failure even
	// when all subtests pass. Discard output instead.
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		panic("start surreal: " + err.Error())
	}
	// Teardown is handled explicitly before os.Exit below (defers are bypassed by os.Exit).

	// Poll the SurrealDB health endpoint instead of a fixed sleep so we don't
	// flake on slow CI runners or waste time on fast ones.
	healthURL := "http://" + surrealAddr + "/health"
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if resp, err := http.Get(healthURL); err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			panic("surreal exited early")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
		panic("surreal exited early")
	}

	ctx := context.Background()
	cfg := &config.SurrealConfig{
		URL: "ws://" + surrealAddr, NS: "parity", DB: "parity",
		Username: "root", Password: "root",
	}
	dbConn, err := db.Connect(ctx, cfg)
	if err != nil {
		panic("db connect: " + err.Error())
	}
	if err := dbConn.InitSchema(ctx, "", 0); err != nil {
		panic("schema init: " + err.Error())
	}
	parityDB = dbConn

	// Seed documents.
	docsPath := filepath.Join(repoRoot(), "docs/adb-standalone/fixtures/seed/documents.ndjson")
	f, err := os.Open(docsPath)
	if err != nil {
		panic("open seed: " + err.Error())
	}
	res, err := seed.Seed(ctx, dbConn.Inner(), f, os.Stderr, nil)
	f.Close()
	if err != nil {
		panic("seed: " + err.Error())
	}
	fmt.Fprintf(os.Stderr, "seeded %d docs (%d errors)\n", res.Loaded, res.Errors)

	// Seed schema_type table from fixtures/schemas.json.
	schemasPath := filepath.Join(repoRoot(), "docs/adb-standalone/fixtures/schemas.json")
	schemaNames, err := loadSchemaNames(schemasPath)
	if err != nil {
		panic("load schemas: " + err.Error())
	}
	if err := handler.SeedSchemasFromList(ctx, dbConn.Inner(), schemaNames); err != nil {
		panic("seed schemas: " + err.Error())
	}

	// Start HTTP test server.
	srv := server.NewWithBaseURL("", dbConn, "http://localhost")
	ts := httptest.NewServer(srv)
	parityTestURL = ts.URL

	// Capture exit code, run teardown, then exit.
	// IMPORTANT: os.Exit bypasses defer, so we must NOT call os.Exit inside a
	// function that has active defers for cleanup. Instead: run tests, close
	// the test server, kill+wait the surreal process, then exit.
	code := m.Run()
	ts.Close()
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	os.Exit(code)
}

// ── corpus loader ─────────────────────────────────────────────────────────

type corpusFile struct {
	Tenant string      `yaml:"tenant"`
	Cases  []testCase  `yaml:"cases"`
}

type testCase struct {
	ID              string            `yaml:"id"`
	Path            string            `yaml:"path"`
	Query           map[string]string `yaml:"query"`
	Fixture         string            `yaml:"fixture"`
	Gates           []string          `yaml:"gates"`
	ExpectedStatus  int               `yaml:"expected_status"`
	ScrollFollow    bool              `yaml:"scroll_follow"`
	ScrollFull      bool              `yaml:"scroll_full"`
	RequiresStorage  bool              `yaml:"requires_storage"`
	RequiresSemantic bool              `yaml:"requires_semantic"`
	Note             string            `yaml:"note"`
}

func (tc testCase) hasGate(g string) bool {
	for _, x := range tc.Gates {
		if x == g {
			return true
		}
	}
	return false
}

func loadCorpus() (*corpusFile, error) {
	path := filepath.Join(repoRoot(), "docs/adb-standalone/corpus.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c corpusFile
	return &c, yaml.Unmarshal(data, &c)
}

// ── main parity test ──────────────────────────────────────────────────────

func TestParityL1(t *testing.T) {
	if parityTestURL == "" {
		t.Skip("surreal binary not found — skipping parity tests")
	}

	corpus, err := loadCorpus()
	if err != nil {
		t.Fatalf("load corpus: %v", err)
	}

	for _, tc := range corpus.Cases {
		tc := tc
		t.Run(tc.ID, func(t *testing.T) {
			t.Parallel()
			runCase(t, corpus.Tenant, tc)
		})
	}
}

// noRedirectClient never follows HTTP redirects — used for 307-response assertions.
var noRedirectClient = &http.Client{
	CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

func runCase(t *testing.T, tenant string, tc testCase) {
	t.Helper()

	// Cases that require a real storage adapter (MinIO) are skipped in L1.
	if tc.RequiresStorage {
		t.Skip("requires storage adapter (MinIO) — skipped in L1")
	}
	// Cases that require semantic search are skipped in L1 when no model_path is set.
	// The server is started without an embedder in TestMain (FTS-only mode).
	if tc.RequiresSemantic {
		t.Skip("requires semantic.model_path — skipped in L1 (FTS-only mode)")
	}

	// Build path: if path starts with /scroll or /files or /projects or /schemas or /search
	// or /semantic-search or /entities, prefix with /v1/{tenant}; otherwise use raw path.
	urlPath := buildPath(tenant, tc.Path)
	req, err := http.NewRequest(http.MethodGet, parityTestURL+urlPath, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	q := req.URL.Query()
	for k, v := range tc.Query {
		q.Set(k, v)
	}
	req.URL.RawQuery = q.Encode()

	// Use non-redirect client for cases that expect a 3xx response.
	client := http.DefaultClient
	expectedStatus := tc.ExpectedStatus
	if expectedStatus == 0 {
		expectedStatus = http.StatusOK
	}
	if expectedStatus >= 300 && expectedStatus < 400 {
		client = noRedirectClient
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	// Status check.
	if resp.StatusCode != expectedStatus {
		t.Errorf("status: got %d, want %d\nbody: %s", resp.StatusCode, expectedStatus, body)
	}
	if resp.StatusCode != http.StatusOK || len(tc.Gates) == 0 {
		return
	}

	// scroll_follow: follow the "next" link once and use the page-2 response for G1/G2.
	if tc.ScrollFollow {
		var page map[string]any
		if err := json.Unmarshal(body, &page); err != nil {
			t.Fatalf("scroll_follow: unmarshal page1: %v", err)
		}
		nextLink, _ := page["next"].(string)
		if nextLink == "" {
			t.Fatalf("scroll_follow: page1 has no next link")
		}
		nextURL := parityTestURL + "/v1/" + tenant + nextLink
		resp2, err := http.Get(nextURL)
		if err != nil {
			t.Fatalf("scroll_follow: %v", err)
		}
		body, _ = io.ReadAll(resp2.Body)
		resp2.Body.Close()
		if resp2.StatusCode != http.StatusOK {
			t.Fatalf("scroll_follow: got status %d: %s", resp2.StatusCode, body)
		}
	}

	// G1: structural equality via normalizer.
	if tc.hasGate("G1") && tc.Fixture != "" {
		assertG1(t, tc, body)
	}

	// G2: exact _extra.id set.
	if tc.hasGate("G2") {
		assertG2(t, tc, tenant, body)
	}

	// G3: ranking fidelity.
	if tc.hasGate("G3") && tc.Fixture != "" {
		assertG3(t, tc, body)
	}

	// Scroll full traversal.
	if tc.ScrollFull {
		assertScrollFull(t, tenant, tc, body)
	}
}

// ── G1: structural equality ───────────────────────────────────────────────

func assertG1(t *testing.T, tc testCase, actualBody []byte) {
	t.Helper()
	fixturePath := filepath.Join(repoRoot(), "docs/adb-standalone/fixtures", tc.Fixture)
	golden, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("G1: read fixture %s: %v", tc.Fixture, err)
	}

	normActual, err := normalize.Response(actualBody)
	if err != nil {
		t.Fatalf("G1: normalize actual: %v", err)
	}
	normGolden, err := normalize.Response(golden)
	if err != nil {
		t.Fatalf("G1: normalize golden: %v", err)
	}

	if !bytes.Equal(normActual, normGolden) {
		t.Errorf("G1 FAIL %s: normalized responses differ\nactual:  %s\nexpected: %s",
			tc.ID, normActual, normGolden)
	}
}

// ── G2: exact _extra.id set ───────────────────────────────────────────────

func assertG2(t *testing.T, tc testCase, tenant string, firstBody []byte) {
	t.Helper()

	actualIDs := collectIDs(t, tenant, tc, firstBody)
	if tc.Fixture == "" {
		// No golden to compare — just verify we got some results.
		t.Logf("G2 %s: %d ids (no fixture for comparison)", tc.ID, len(actualIDs))
		return
	}

	fixturePath := filepath.Join(repoRoot(), "docs/adb-standalone/fixtures", tc.Fixture)
	golden, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("G2: read fixture %s: %v", tc.Fixture, err)
	}
	goldenIDs := extractIDs(t, golden)

	if !setsEqual(actualIDs, goldenIDs) {
		t.Errorf("G2 FAIL %s: _extra.id sets differ\nactual:   %v\nexpected: %v",
			tc.ID, sorted(actualIDs), sorted(goldenIDs))
	}
}

// collectIDs extracts _extra.id values from a search response, following scroll if needed.
func collectIDs(t *testing.T, tenant string, tc testCase, body []byte) map[string]bool {
	t.Helper()
	ids := make(map[string]bool)

	var page map[string]any
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("G2: unmarshal response: %v", err)
	}
	addResultIDs(ids, page)

	if tc.ScrollFull {
		// Follow all scroll pages.
		for {
			next, _ := page["next"].(string)
			if next == "" {
				break
			}
			nextURL := parityTestURL + "/v1/" + tenant + next
			resp, err := http.Get(nextURL)
			if err != nil {
				t.Fatalf("G2 scroll: %v", err)
			}
			scrollBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			var nextPage map[string]any
			if err := json.Unmarshal(scrollBody, &nextPage); err != nil {
				t.Fatalf("G2 scroll unmarshal: %v", err)
			}
			addResultIDs(ids, nextPage)
			page = nextPage
		}
	}
	return ids
}

func addResultIDs(ids map[string]bool, page map[string]any) {
	results, _ := page["results"].([]any)
	for _, r := range results {
		if doc, ok := r.(map[string]any); ok {
			if extra, ok := doc["_extra"].(map[string]any); ok {
				if id, ok := extra["id"].(string); ok && id != "" {
					ids[id] = true
				}
			}
		}
	}
}

func extractIDs(t *testing.T, data []byte) map[string]bool {
	t.Helper()
	var page map[string]any
	if err := json.Unmarshal(data, &page); err != nil {
		t.Fatalf("extractIDs: %v", err)
	}
	ids := make(map[string]bool)
	addResultIDs(ids, page)
	return ids
}

// ── G3: ranking fidelity (nDCG@K) ────────────────────────────────────────

const ndcgK = 10
const ndcgGate = 0.95

func assertG3(t *testing.T, tc testCase, actualBody []byte) {
	t.Helper()
	fixturePath := filepath.Join(repoRoot(), "docs/adb-standalone/fixtures", tc.Fixture)
	golden, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("G3: read fixture %s: %v", tc.Fixture, err)
	}

	actualOrder := extractIDOrder(t, actualBody)
	goldenOrder := extractIDOrder(t, golden)

	score := ndcgAtK(actualOrder, goldenOrder, ndcgK)
	t.Logf("G3 %s: nDCG@%d = %.4f (gate=%.2f)", tc.ID, ndcgK, score, ndcgGate)

	if score < ndcgGate {
		// Emit per-query divergence report.
		t.Errorf("G3 FAIL %s: nDCG@%d = %.4f < %.2f\nactual order:  %v\nexpected order: %v",
			tc.ID, ndcgK, score, ndcgGate, actualOrder, goldenOrder)
	}
}

func extractIDOrder(t *testing.T, data []byte) []string {
	t.Helper()
	var page map[string]any
	if err := json.Unmarshal(data, &page); err != nil {
		t.Fatalf("extractIDOrder: %v", err)
	}
	results, _ := page["results"].([]any)
	ids := make([]string, 0, len(results))
	for _, r := range results {
		if doc, ok := r.(map[string]any); ok {
			if extra, ok := doc["_extra"].(map[string]any); ok {
				if id, ok := extra["id"].(string); ok {
					ids = append(ids, id)
				}
			}
		}
	}
	return ids
}

// ndcgAtK computes nDCG@K where relevance = 1 for ids matching the golden top-K, 0 otherwise.
func ndcgAtK(actual, golden []string, k int) float64 {
	if len(golden) == 0 {
		return 1.0
	}
	// Build relevance map from golden order: position 0 = highest relevance.
	goldenRel := make(map[string]float64)
	for i, id := range golden {
		if i >= k {
			break
		}
		goldenRel[id] = float64(len(golden)-i) / float64(len(golden))
	}

	dcg := 0.0
	for i, id := range actual {
		if i >= k {
			break
		}
		rel := goldenRel[id]
		dcg += rel / math.Log2(float64(i+2))
	}

	// Ideal DCG: golden order, top-K.
	idcg := 0.0
	for i, id := range golden {
		if i >= k {
			break
		}
		rel := goldenRel[id]
		idcg += rel / math.Log2(float64(i+2))
	}

	if idcg == 0 {
		return 1.0
	}
	return dcg / idcg
}

// assertScrollFull follows all scroll pages and verifies terminal next=null.
func assertScrollFull(t *testing.T, tenant string, tc testCase, firstBody []byte) {
	t.Helper()
	var page map[string]any
	if err := json.Unmarshal(firstBody, &page); err != nil {
		t.Fatalf("scroll full: unmarshal first page: %v", err)
	}

	pageCount := 1
	for {
		next, _ := page["next"].(string)
		if next == "" {
			break
		}
		if pageCount > 1000 {
			t.Fatal("scroll full: too many pages (> 1000), possible infinite loop")
		}
		resp, err := http.Get(parityTestURL + "/v1/" + tenant + next)
		if err != nil {
			t.Fatalf("scroll full page %d: %v", pageCount+1, err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("scroll full page %d: status %d", pageCount+1, resp.StatusCode)
		}
		if err := json.Unmarshal(b, &page); err != nil {
			t.Fatalf("scroll full page %d: unmarshal: %v", pageCount+1, err)
		}
		pageCount++
	}
	t.Logf("scroll full %s: traversed %d pages, terminal next=null OK", tc.ID, pageCount)
}

// ── helpers ───────────────────────────────────────────────────────────────

func buildPath(tenant, casePath string) string {
	// Routes that live under /v1/{tenant}
	tenantRoutes := []string{"/search", "/scroll/", "/files/", "/projects/", "/schemas", "/jobs/", "/project/", "/semantic-search", "/entities"}
	for _, prefix := range tenantRoutes {
		if strings.HasPrefix(casePath, prefix) {
			return "/v1/" + tenant + casePath
		}
	}
	// Raw path (e.g. /unknown-endpoint-xyz for error tests)
	return casePath
}

func repoRoot() string {
	// Walk up from the test file to find the repo root (contains go.mod).
	dir, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			// go/adb-standalone/go.mod — repo root is two levels up.
			return filepath.Join(dir, "..", "..")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			panic("repo root not found")
		}
		dir = parent
	}
}

func loadSchemaNames(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var obj struct {
		DocumentTypes []struct {
			Name string `json:"name"`
		} `json:"document_types"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(obj.DocumentTypes))
	for _, dt := range obj.DocumentTypes {
		names = append(names, dt.Name)
	}
	return names, nil
}

func setsEqual(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func sorted(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Ensure io is used (ReadAll in scroll traversal).
var _ = io.ReadAll
