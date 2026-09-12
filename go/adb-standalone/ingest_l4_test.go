// Package main_test — L4 ingest parity tests.
// Requires a running SurrealDB binary (set up by TestMain in parity_test.go).
// Ingests a fixture bundle from testdata/ingest_bundle/ into the shared DB,
// then asserts that resulting read endpoints return correct data.
package main_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/Genentech/exohub/go/adb-standalone/internal/config"
	"github.com/Genentech/exohub/go/adb-standalone/internal/normalize"
	"github.com/Genentech/exohub/go/adb-standalone/internal/server"
)

// TestIngestL4 ingests a minimal fixture bundle and asserts read endpoints work.
// It reuses the parityDB set up by TestMain in parity_test.go.
func TestIngestL4(t *testing.T) {
	if parityDB == nil {
		t.Skip("surreal binary not found — skipping L4 ingest tests")
	}

	cfg := ingestTestConfig()
	srv := server.NewWithStorage("", parityDB, "http://localhost", nil, nil, cfg)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	tenant := "exohub"
	bundleDir := filepath.Join(repoRoot(), "go/adb-standalone/testdata/ingest_bundle")

	// ── POST /project/ingest ──────────────────────────────────────────────

	body, _ := json.Marshal(map[string]any{
		"$schema":     "external-project/v1.json",
		"project_id":  "sample-app",
		"version":     "v2.2.0",
		"s3_location": bundleDir,
		"s3url":       "",
	})

	resp, err := http.Post(
		fmt.Sprintf("%s/v1/%s/project/ingest", ts.URL, tenant),
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatalf("ingest POST: %v", err)
	}
	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("ingest: got status %d, want 202\nbody: %s", resp.StatusCode, respBody)
	}

	var ingestResp map[string]any
	if err := json.Unmarshal(respBody, &ingestResp); err != nil {
		t.Fatalf("ingest response JSON: %v", err)
	}
	jobID, _ := ingestResp["job_id"].(string)
	if jobID == "" {
		t.Fatalf("ingest response missing job_id: %s", respBody)
	}
	if ingestResp["status"] != "SUCCESS" {
		t.Errorf("ingest status = %v, want SUCCESS", ingestResp["status"])
	}
	ingested, _ := ingestResp["ingested"].(float64)
	if ingested == 0 {
		t.Errorf("ingested = 0, expected > 0")
	}
	t.Logf("ingest: job_id=%s ingested=%.0f errors=%.0f", jobID, ingested, ingestResp["errors"])

	// ── GET /jobs/{jobID} ─────────────────────────────────────────────────

	jobResp, err := http.Get(fmt.Sprintf("%s/v1/%s/jobs/%s", ts.URL, tenant, url.PathEscape(jobID)))
	if err != nil {
		t.Fatalf("jobs GET: %v", err)
	}
	jobBody, _ := io.ReadAll(jobResp.Body)
	jobResp.Body.Close()

	if jobResp.StatusCode != http.StatusOK {
		t.Fatalf("jobs: got status %d\nbody: %s", jobResp.StatusCode, jobBody)
	}
	var jobObj map[string]any
	if err := json.Unmarshal(jobBody, &jobObj); err != nil {
		t.Fatalf("jobs response JSON: %v", err)
	}
	if jobObj["status"] != "SUCCESS" {
		t.Errorf("job status = %v, want SUCCESS", jobObj["status"])
	}

	// ── GET /files/{id}/metadata — bundle doc should be queryable ─────────

	fileID := url.PathEscape("sample-app:bundle.json@v2.2.0")
	metaResp, err := http.Get(fmt.Sprintf("%s/v1/%s/files/%s/metadata", ts.URL, tenant, fileID))
	if err != nil {
		t.Fatalf("metadata GET: %v", err)
	}
	metaBody, _ := io.ReadAll(metaResp.Body)
	metaResp.Body.Close()

	if metaResp.StatusCode != http.StatusOK {
		t.Fatalf("metadata: got status %d\nbody: %s", metaResp.StatusCode, metaBody)
	}
	var metaDoc map[string]any
	if err := json.Unmarshal(metaBody, &metaDoc); err != nil {
		t.Fatalf("metadata JSON: %v", err)
	}
	extra, _ := metaDoc["_extra"].(map[string]any)
	if extra == nil {
		t.Fatalf("metadata missing _extra")
	}
	if extra["id"] != "sample-app:bundle.json@v2.2.0" {
		t.Errorf("_extra.id = %v", extra["id"])
	}
	if extra["project_id"] != "sample-app" {
		t.Errorf("_extra.project_id = %v", extra["project_id"])
	}
	if extra["version"] != "v2.2.0" {
		t.Errorf("_extra.version = %v", extra["version"])
	}

	// G1: normalized metadata matches golden fixture from the reference ArtifactDB service.
	goldenPath := filepath.Join(repoRoot(), "docs/adb-standalone/fixtures/file_metadata.json")
	if golden, err := os.ReadFile(goldenPath); err == nil {
		normActual, err1 := normalize.Response(metaBody)
		normGolden, err2 := normalize.Response(golden)
		if err1 == nil && err2 == nil {
			if !bytes.Equal(normActual, normGolden) {
				t.Errorf("L4 G1 FAIL: metadata differs from golden\nactual:  %s\nexpected: %s", normActual, normGolden)
			} else {
				t.Log("L4 G1 PASS: metadata matches golden")
			}
		}
	}

	// ── GET /search — ingested docs must be queryable ─────────────────────

	searchResp, err := http.Get(fmt.Sprintf("%s/v1/%s/search?q=sample-app&size=50", ts.URL, tenant))
	if err != nil {
		t.Fatalf("search GET: %v", err)
	}
	searchBody, _ := io.ReadAll(searchResp.Body)
	searchResp.Body.Close()

	if searchResp.StatusCode != http.StatusOK {
		t.Fatalf("search: got status %d\nbody: %s", searchResp.StatusCode, searchBody)
	}
	var searchObj map[string]any
	if err := json.Unmarshal(searchBody, &searchObj); err != nil {
		t.Fatalf("search JSON: %v", err)
	}
	results, _ := searchObj["results"].([]any)
	if len(results) == 0 {
		t.Errorf("search returned 0 results after ingest")
	} else {
		t.Logf("L4 search: %d results", len(results))
	}

	// ── GET /projects/{id}/versions — latest must be correct ─────────────

	versResp, err := http.Get(fmt.Sprintf("%s/v1/%s/projects/sample-app/versions", ts.URL, tenant))
	if err != nil {
		t.Fatalf("versions GET: %v", err)
	}
	versBody, _ := io.ReadAll(versResp.Body)
	versResp.Body.Close()

	if versResp.StatusCode != http.StatusOK {
		t.Fatalf("versions: got status %d\nbody: %s", versResp.StatusCode, versBody)
	}
	var versObj map[string]any
	if err := json.Unmarshal(versBody, &versObj); err != nil {
		t.Fatalf("versions JSON: %v", err)
	}
	// sample-app has no pre-seeded data; after ingesting v2.2.0 it must be the only version and thus latest.
	latest, _ := versObj["latest"].(map[string]any)
	if latest != nil {
		latestVer := latest["_extra.version"]
		t.Logf("L4 latest version = %v", latestVer)
		if latestVer != "v2.2.0" {
			t.Errorf("latest = %v, want v2.2.0 (only version of sample-app)", latestVer)
		}
	}

	// ── POST /project/ingest — validation errors ──────────────────────────

	t.Run("missing_project_id", func(t *testing.T) {
		bad, _ := json.Marshal(map[string]any{"version": "v1.0.0", "s3_location": bundleDir})
		r, err := http.Post(fmt.Sprintf("%s/v1/%s/project/ingest", ts.URL, tenant), "application/json", bytes.NewReader(bad))
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != http.StatusBadRequest {
			t.Errorf("got %d, want 400", r.StatusCode)
		}
	})

	t.Run("missing_version", func(t *testing.T) {
		bad, _ := json.Marshal(map[string]any{"project_id": "x", "s3_location": bundleDir})
		r, err := http.Post(fmt.Sprintf("%s/v1/%s/project/ingest", ts.URL, tenant), "application/json", bytes.NewReader(bad))
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != http.StatusBadRequest {
			t.Errorf("got %d, want 400", r.StatusCode)
		}
	})

	t.Run("missing_s3_location", func(t *testing.T) {
		bad, _ := json.Marshal(map[string]any{"project_id": "x", "version": "v1.0.0"})
		r, err := http.Post(fmt.Sprintf("%s/v1/%s/project/ingest", ts.URL, tenant), "application/json", bytes.NewReader(bad))
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != http.StatusBadRequest {
			t.Errorf("got %d, want 400", r.StatusCode)
		}
	})
}

// ── helpers ───────────────────────────────────────────────────────────────

func ingestTestConfig() *config.Config {
	return &config.Config{
		GPRN: config.GPRNConfig{
			Service:     "adb-standalone",
			Environment: "local",
			Placeholder: "artifact",
		},
		Permissions: config.PermissionsConfig{
			DefaultRead:  "public",
			DefaultWrite: "owners",
		},
		Storage: config.StorageConfig{
			S3: config.S3Config{Bucket: "adb-standalone"},
		},
		Tenants: []config.TenantConfig{{Path: "/exohub", Alias: "exohub"}},
	}
}
