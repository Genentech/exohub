package upgrade

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDownloadUpgradeScript(t *testing.T) {
	previous := http.DefaultTransport
	http.DefaultTransport = roundTripper(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     http.StatusText(http.StatusOK),
			Body:       io.NopCloser(strings.NewReader("#!/bin/sh\necho ok\n")),
			Header:     make(http.Header),
		}, nil
	})
	defer func() { http.DefaultTransport = previous }()

	path, cleanup, err := downloadUpgradeScript("http://upgrade.test/script")
	if err != nil {
		t.Fatalf("downloadUpgradeScript: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("script missing: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read script: %v", err)
	}
	if string(data) == "" {
		t.Fatalf("script content empty")
	}
	cleanup()
	if _, err := os.Stat(path); err == nil {
		t.Fatalf("expected script removed after cleanup")
	}
}

func TestDownloadUpgradeScriptRejectsNon2xx(t *testing.T) {
	previous := http.DefaultTransport
	http.DefaultTransport = roundTripper(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Status:     http.StatusText(http.StatusBadRequest),
			Body:       io.NopCloser(strings.NewReader("bad")),
			Header:     make(http.Header),
		}, nil
	})
	defer func() { http.DefaultTransport = previous }()

	if _, _, err := downloadUpgradeScript("http://upgrade.test/bad"); err == nil {
		t.Fatalf("expected error for non-2xx")
	}
}

func TestCurrentBinaryDir(t *testing.T) {
	dir, err := currentBinaryDir()
	if err != nil {
		t.Fatalf("currentBinaryDir: %v", err)
	}
	if dir == "" {
		t.Fatalf("currentBinaryDir empty")
	}
	if _, err := os.Stat(filepath.Clean(dir)); err != nil {
		t.Fatalf("currentBinaryDir missing: %v", err)
	}
}

type roundTripper func(*http.Request) (*http.Response, error)

func (rt roundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	return rt(r)
}
