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

// newCapturingEDSServer returns an httptest server that answers the EDS
// root and download endpoints, decoding every posted download body into
// *gotBody (last request wins).
func newCapturingEDSServer(t *testing.T, gotBody *map[string]any) *httptest.Server {
	t.Helper()
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
			_ = json.NewDecoder(r.Body).Decode(gotBody)
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"operation_id":  "op-1",
				"operation_url": "http://example.invalid/op-1",
			})
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

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

func TestRunEDSDownload_NoNewFlags_BodyHasOnlyFixedFields(t *testing.T) {
	withInlineToken(t)

	var gotBody map[string]any
	srv := newCapturingEDSServer(t, &gotBody)

	_ = captureStdout(t, func() {
		_ = runEDSDownload(edsOptions{
			eds:          srv.URL,
			arg:          "myproject@v1",
			noWait:       true,
			pollInterval: time.Second,
		})
	})

	want := map[string]any{"project_id": "myproject", "version": "v1"}
	if len(gotBody) != len(want) {
		t.Fatalf("body = %v, want exactly %v", gotBody, want)
	}
	for k, v := range want {
		if gotBody[k] != v {
			t.Errorf("body[%q] = %v, want %v", k, gotBody[k], v)
		}
	}
}

func TestRunEDSDownload_NoContent_SendsContentFalse(t *testing.T) {
	withInlineToken(t)

	var gotBody map[string]any
	srv := newCapturingEDSServer(t, &gotBody)

	out := captureStdout(t, func() {
		_ = runEDSDownload(edsOptions{
			eds:              srv.URL,
			arg:              "myproject@v1",
			noWait:           true,
			pollInterval:     time.Second,
			noContent:        true,
			noContentChanged: true,
		})
	})
	if gotBody["content"] != false {
		t.Errorf("content = %v, want false", gotBody["content"])
	}
	_ = out
}

func TestRunEDSDownload_Worktrees_TriState(t *testing.T) {
	withInlineToken(t)

	// --worktrees=false sends false.
	var gotFalse map[string]any
	srvFalse := newCapturingEDSServer(t, &gotFalse)
	_ = captureStdout(t, func() {
		_ = runEDSDownload(edsOptions{
			eds:              srvFalse.URL,
			arg:              "myproject@v1",
			noWait:           true,
			pollInterval:     time.Second,
			worktrees:        false,
			worktreesChanged: true,
		})
	})
	val, ok := gotFalse["worktrees"]
	if !ok || val != false {
		t.Errorf("worktrees = %v (present=%v), want false", val, ok)
	}

	// Unset --worktrees omits it.
	var gotUnset map[string]any
	srvUnset := newCapturingEDSServer(t, &gotUnset)
	_ = captureStdout(t, func() {
		_ = runEDSDownload(edsOptions{
			eds:          srvUnset.URL,
			arg:          "myproject@v1",
			noWait:       true,
			pollInterval: time.Second,
		})
	})
	if _, ok := gotUnset["worktrees"]; ok {
		t.Errorf("worktrees should be omitted when unset, got %v", gotUnset["worktrees"])
	}
}

func TestRunEDSDownload_Checkout_And_EdsForce(t *testing.T) {
	withInlineToken(t)

	var gotBody map[string]any
	srv := newCapturingEDSServer(t, &gotBody)

	_ = captureStdout(t, func() {
		_ = runEDSDownload(edsOptions{
			eds:             srv.URL,
			arg:             "myproject@v1",
			noWait:          true,
			pollInterval:    time.Second,
			checkout:        true,
			checkoutChanged: true,
			edsForce:        true,
			edsForceChanged: true,
		})
	})
	if gotBody["checkout"] != true {
		t.Errorf("checkout = %v, want true", gotBody["checkout"])
	}
	if gotBody["force"] != true {
		t.Errorf("force = %v, want true", gotBody["force"])
	}
}

func TestRunEDSDownload_EdsParams_InlineMerged(t *testing.T) {
	withInlineToken(t)

	var gotBody map[string]any
	srv := newCapturingEDSServer(t, &gotBody)

	_ = captureStdout(t, func() {
		_ = runEDSDownload(edsOptions{
			eds:              srv.URL,
			arg:              "myproject@v1",
			noWait:           true,
			pollInterval:     time.Second,
			edsParams:        `{"future_option": 7}`,
			edsParamsChanged: true,
		})
	})
	if gotBody["future_option"] != float64(7) {
		t.Errorf("future_option = %v, want 7", gotBody["future_option"])
	}
}

func TestRunEDSDownload_EdsParams_FileMerged(t *testing.T) {
	withInlineToken(t)

	var gotBody map[string]any
	srv := newCapturingEDSServer(t, &gotBody)

	path := filepath.Join(t.TempDir(), "opts.json")
	if err := os.WriteFile(path, []byte(`{"future_option": "x"}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_ = captureStdout(t, func() {
		_ = runEDSDownload(edsOptions{
			eds:              srv.URL,
			arg:              "myproject@v1",
			noWait:           true,
			pollInterval:     time.Second,
			edsParams:        "@" + path,
			edsParamsChanged: true,
		})
	})
	if gotBody["future_option"] != "x" {
		t.Errorf("future_option = %v, want x", gotBody["future_option"])
	}
}

func TestRunEDSDownload_EdsParams_NonObjectIsError(t *testing.T) {
	withInlineToken(t)
	var gotBody map[string]any
	srv := newCapturingEDSServer(t, &gotBody)

	var runErr error
	_ = captureStdout(t, func() {
		runErr = runEDSDownload(edsOptions{
			eds:              srv.URL,
			arg:              "myproject@v1",
			noWait:           true,
			pollInterval:     time.Second,
			edsParams:        `["not", "an", "object"]`,
			edsParamsChanged: true,
		})
	})
	if runErr == nil || !strings.Contains(runErr.Error(), "JSON object") {
		t.Errorf("runEDSDownload() error = %v, want JSON-object rejection", runErr)
	}
	if gotBody != nil {
		t.Errorf("no request should have been sent, got body: %v", gotBody)
	}
}

func TestRunEDSDownload_EdsParams_InvalidJSONIsError(t *testing.T) {
	withInlineToken(t)
	var gotBody map[string]any
	srv := newCapturingEDSServer(t, &gotBody)

	var runErr error
	_ = captureStdout(t, func() {
		runErr = runEDSDownload(edsOptions{
			eds:              srv.URL,
			arg:              "myproject@v1",
			noWait:           true,
			pollInterval:     time.Second,
			edsParams:        `{not valid json`,
			edsParamsChanged: true,
		})
	})
	if runErr == nil {
		t.Error("runEDSDownload() expected error for invalid JSON")
	}
	if gotBody != nil {
		t.Errorf("no request should have been sent, got body: %v", gotBody)
	}
}

func TestRunEDSDownload_EdsParams_ReservedKeyIsError(t *testing.T) {
	withInlineToken(t)
	var gotBody map[string]any
	srv := newCapturingEDSServer(t, &gotBody)

	var runErr error
	_ = captureStdout(t, func() {
		runErr = runEDSDownload(edsOptions{
			eds:              srv.URL,
			arg:              "myproject@v1",
			noWait:           true,
			pollInterval:     time.Second,
			edsParams:        `{"project_id": "other"}`,
			edsParamsChanged: true,
		})
	})
	if runErr == nil || !strings.Contains(runErr.Error(), "project_id") {
		t.Errorf("runEDSDownload() error = %v, want project_id rejection", runErr)
	}
	if gotBody != nil {
		t.Errorf("no request should have been sent, got body: %v", gotBody)
	}
}

func TestRunEDSDownload_EdsParams_ConflictWithTypedFlagIsError(t *testing.T) {
	withInlineToken(t)
	var gotBody map[string]any
	srv := newCapturingEDSServer(t, &gotBody)

	var runErr error
	_ = captureStdout(t, func() {
		runErr = runEDSDownload(edsOptions{
			eds:              srv.URL,
			arg:              "myproject@v1",
			noWait:           true,
			pollInterval:     time.Second,
			noContent:        true,
			noContentChanged: true,
			edsParams:        `{"content": true}`,
			edsParamsChanged: true,
		})
	})
	if runErr == nil || !strings.Contains(runErr.Error(), "content") {
		t.Errorf("runEDSDownload() error = %v, want content conflict rejection", runErr)
	}
	if gotBody != nil {
		t.Errorf("no request should have been sent, got body: %v", gotBody)
	}
}

func TestRunEDSDownload_EdsParams_ConflictWithScopeIsError(t *testing.T) {
	withInlineToken(t)
	var gotBody map[string]any
	srv := newCapturingEDSServer(t, &gotBody)

	var runErr error
	_ = captureStdout(t, func() {
		runErr = runEDSDownload(edsOptions{
			eds:              srv.URL,
			arg:              "myproject@v1",
			noWait:           true,
			pollInterval:     time.Second,
			scope:            "user",
			edsParams:        `{"scope": "global"}`,
			edsParamsChanged: true,
		})
	})
	if runErr == nil || !strings.Contains(runErr.Error(), "scope") {
		t.Errorf("runEDSDownload() error = %v, want scope conflict rejection", runErr)
	}
	if gotBody != nil {
		t.Errorf("no request should have been sent, got body: %v", gotBody)
	}
}

func TestRejectEDSOnlyFlagsWithoutEDS(t *testing.T) {
	for _, name := range edsOnlyFlags {
		cmd := NewCommand()
		if err := cmd.Flags().Set(name, "true"); err != nil {
			// --eds-params is a string flag.
			if err2 := cmd.Flags().Set(name, "{}"); err2 != nil {
				t.Fatalf("setting %s: %v / %v", name, err, err2)
			}
		}
		if err := rejectEDSOnlyFlagsWithoutEDS(cmd); err == nil {
			t.Errorf("rejectEDSOnlyFlagsWithoutEDS() with --%s set and no --eds: expected error", name)
		} else if !strings.Contains(err.Error(), name) {
			t.Errorf("error = %v, want mention of --%s", err, name)
		}
	}
}

func TestRunDownload_FFlagStillRejectedWithEDS(t *testing.T) {
	// -f/--force must still be rejected with --eds even though new
	// --eds-only flags exist now.
	err := runEDSDownload(edsOptions{
		eds:   "huge",
		arg:   "myproject@v1",
		force: true,
	})
	if err == nil || !strings.Contains(err.Error(), "-f/--force") {
		t.Errorf("runEDSDownload() error = %v, want force rejection", err)
	}
}
