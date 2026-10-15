//go:build artifactdb

package download

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w

	fn()

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	r.Close()
	return buf.String()
}

func withInlineToken(t *testing.T) {
	t.Helper()
	t.Setenv("EXO_TOKEN", `{"access_token":"test-token","token_type":"Bearer"}`)
	t.Setenv("EXO_TOKEN_FILE", filepath.Join(t.TempDir(), "token.json"))
}

func TestRunEDSDownload_RejectsOutputDir(t *testing.T) {
	err := runEDSDownload(edsOptions{
		eds:              "huge",
		arg:              "myproject@v1",
		outputDirChanged: true,
	})
	if err == nil || !strings.Contains(err.Error(), "-o/--output-dir") {
		t.Errorf("runEDSDownload() error = %v, want output-dir rejection", err)
	}
}

func TestRunEDSDownload_RejectsForce(t *testing.T) {
	err := runEDSDownload(edsOptions{
		eds:   "huge",
		arg:   "myproject@v1",
		force: true,
	})
	if err == nil || !strings.Contains(err.Error(), "-f/--force") {
		t.Errorf("runEDSDownload() error = %v, want force rejection", err)
	}
}

func TestRunEDSDownload_RejectsArtifactID(t *testing.T) {
	err := runEDSDownload(edsOptions{
		eds:          "huge",
		arg:          "myproject:file.txt@v1",
		pollInterval: time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "artifact id") {
		t.Errorf("runEDSDownload() error = %v, want artifact-id rejection", err)
	}
}

func TestRunEDSDownload_RejectsNonPositivePollInterval(t *testing.T) {
	err := runEDSDownload(edsOptions{
		eds:          "huge",
		arg:          "myproject@v1",
		pollInterval: 0,
	})
	if err == nil {
		t.Error("runEDSDownload() expected error for zero poll interval")
	}
}

func TestRunEDSDownload_NoWait_PrintsOperationInfoAndSkipsPolling(t *testing.T) {
	withInlineToken(t)

	var sawPoll bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"deployment_id": "huge",
				"endpoints": map[string]string{
					"download":   "/api/datasets/download",
					"operations": "/api/operations/{id}",
				},
			})
		case r.URL.Path == "/api/datasets/download" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"operation_id":  "op-42",
				"operation_url": "http://example.invalid/op-42",
			})
		default:
			sawPoll = true
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "completed"})
		}
	}))
	defer srv.Close()

	var runErr error
	out := captureStdout(t, func() {
		runErr = runEDSDownload(edsOptions{
			eds:          srv.URL,
			arg:          "myproject@v1",
			noWait:       true,
			maxWait:      0,
			pollInterval: time.Second,
		})
	})

	if runErr != nil {
		t.Fatalf("runEDSDownload() error = %v", runErr)
	}
	if sawPoll {
		t.Error("--no-wait must not poll the operation")
	}
	if !strings.Contains(out, "op-42") {
		t.Errorf("output missing operation_id, got: %q", out)
	}
	if !strings.Contains(out, "operation_url") {
		t.Errorf("output missing operation_url label, got: %q", out)
	}
}

func TestRunEDSDownload_ScopeSentOnlyWhenSet(t *testing.T) {
	withInlineToken(t)

	var gotScope any
	var sawScope bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"deployment_id": "huge",
				"endpoints": map[string]string{
					"download": "/api/datasets/download",
				},
			})
		case r.URL.Path == "/api/datasets/download" && r.Method == http.MethodPost:
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			gotScope, sawScope = body["scope"], body["scope"] != nil
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"operation_id":  "op-1",
				"operation_url": "http://example.invalid/op-1",
			})
		}
	}))
	defer srv.Close()

	_ = captureStdout(t, func() {
		_ = runEDSDownload(edsOptions{
			eds:          srv.URL,
			arg:          "myproject@v1",
			noWait:       true,
			pollInterval: time.Second,
		})
	})
	if sawScope {
		t.Errorf("scope should be omitted when not set, got %v", gotScope)
	}

	_ = captureStdout(t, func() {
		_ = runEDSDownload(edsOptions{
			eds:          srv.URL,
			arg:          "myproject@v1",
			scope:        "global",
			noWait:       true,
			pollInterval: time.Second,
		})
	})
	if gotScope != "global" {
		t.Errorf("scope = %v, want global", gotScope)
	}
}
