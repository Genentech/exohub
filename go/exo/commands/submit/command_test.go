package submit

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSubmitManifestFromFile(t *testing.T) {
	manifest := "name: sample\nremote-type: annex\nurl: https://github.com/example/repo.git\nref: main\nrepo-dir: /tmp/repo\nqueue: exohub-sync-aws\n"
	path := writeTempFile(t, "manifest.yaml", manifest)

	client := newMockClient(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/api/manifest" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("queue"); got != "exohub-sync-aws" {
			t.Fatalf("unexpected queue: %s", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/x-yaml" {
			t.Fatalf("unexpected content type: %s", got)
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "queue: exohub-sync-aws") {
			t.Fatalf("manifest body missing queue")
		}
		return response(http.StatusOK, "{\"status\":\"started\"}"), nil
	})

	t.Setenv("EXOHUB_API_URL", "https://example.test/api")
	payload, err := submitManifest(path, "", client)
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}
	if payload != "{\"status\":\"started\"}" {
		t.Fatalf("unexpected payload: %s", payload)
	}
}

func TestSubmitManifestUsesQueueFlag(t *testing.T) {
	manifest := "name: sample\nremote-type: annex\nurl: https://github.com/example/repo.git\nref: main\nrepo-dir: /tmp/repo\n"
	path := writeTempFile(t, "manifest.yaml", manifest)

	client := newMockClient(func(r *http.Request) (*http.Response, error) {
		if got := r.URL.Query().Get("queue"); got != "exohub-sync-shpc" {
			t.Fatalf("unexpected queue: %s", got)
		}
		return response(http.StatusOK, ""), nil
	})

	t.Setenv("EXOHUB_API_URL", "https://example.test/api")
	if _, err := submitManifest(path, "exohub-sync-shpc", client); err != nil {
		t.Fatalf("submit failed: %v", err)
	}
}

func TestSubmitManifestRejectsMissingQueue(t *testing.T) {
	manifest := "name: sample\nremote-type: annex\nurl: https://github.com/example/repo.git\nref: main\nrepo-dir: /tmp/repo\n"
	path := writeTempFile(t, "manifest.yaml", manifest)

	if _, err := submitManifest(path, "", http.DefaultClient); err == nil {
		t.Fatalf("expected error for missing queue")
	}
}

func TestSubmitManifestRejectsInvalidQueue(t *testing.T) {
	manifest := "name: sample\nqueue: nope\n"
	path := writeTempFile(t, "manifest.yaml", manifest)

	if _, err := submitManifest(path, "", http.DefaultClient); err == nil {
		t.Fatalf("expected error for invalid queue")
	}
}

func TestSubmitManifestUsesJSONContentType(t *testing.T) {
	manifest := "{\"name\":\"sample\",\"remote-type\":\"annex\",\"url\":\"https://github.com/example/repo.git\",\"ref\":\"main\",\"repo-dir\":\"/tmp/repo\",\"queue\":\"exohub-sync-aws\"}"
	path := writeTempFile(t, "manifest.json", manifest)

	client := newMockClient(func(r *http.Request) (*http.Response, error) {
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("unexpected content type: %s", got)
		}
		return response(http.StatusOK, ""), nil
	})

	t.Setenv("EXOHUB_API_URL", "https://example.test/api")
	if _, err := submitManifest(path, "", client); err != nil {
		t.Fatalf("submit failed: %v", err)
	}
}

func writeTempFile(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	return path
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func newMockClient(fn roundTripFunc) *http.Client {
	return &http.Client{Transport: fn}
}

func response(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}
