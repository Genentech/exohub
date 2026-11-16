//go:build exohubapi

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestGetCredsFromServer(t *testing.T) {
	expiration := time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/grants/credentials" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"AccessKeyId":"ASIASERVER","SecretAccessKey":"serversecret","SessionToken":"servertoken","Expiration":%q}`, expiration)
	}))
	defer ts.Close()

	t.Setenv("EXOHUB_API_URL", ts.URL)
	noop := func(string, ...any) {}

	creds, err := getCredsFromServer("s3://bucket/prefix/", "READ", "", noop)
	if err != nil {
		t.Fatalf("getCredsFromServer error: %v", err)
	}
	if creds.AccessKeyID != "ASIASERVER" {
		t.Errorf("AccessKeyID = %q, want ASIASERVER", creds.AccessKeyID)
	}
	if creds.SecretAccessKey != "serversecret" {
		t.Errorf("SecretAccessKey mismatch")
	}
	if creds.Version != 1 {
		t.Errorf("Version = %d, want 1", creds.Version)
	}
}

func TestGetCredsFromServerSourceS3URL(t *testing.T) {
	var gotBody map[string]string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"AccessKeyId":"ASIA","SecretAccessKey":"s","SessionToken":"t","Expiration":"2099-01-01T00:00:00Z"}`)
	}))
	defer ts.Close()

	t.Setenv("EXOHUB_API_URL", ts.URL)
	noop := func(string, ...any) {}

	if _, err := getCredsFromServer("s3://instance-bucket/proj", "READ", "s3://atlas-bucket/prefix/", noop); err != nil {
		t.Fatalf("getCredsFromServer error: %v", err)
	}
	if gotBody["source_s3url"] != "s3://atlas-bucket/prefix/" {
		t.Errorf("source_s3url = %q, want s3://atlas-bucket/prefix/", gotBody["source_s3url"])
	}
}

func TestGetCredsFromServerNoSourceS3URLOmitted(t *testing.T) {
	var gotBody map[string]string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"AccessKeyId":"ASIA","SecretAccessKey":"s","SessionToken":"t","Expiration":"2099-01-01T00:00:00Z"}`)
	}))
	defer ts.Close()

	t.Setenv("EXOHUB_API_URL", ts.URL)
	noop := func(string, ...any) {}

	if _, err := getCredsFromServer("s3://atlas-bucket/prefix/", "READ", "", noop); err != nil {
		t.Fatalf("getCredsFromServer error: %v", err)
	}
	if _, ok := gotBody["source_s3url"]; ok {
		t.Errorf("source_s3url should be omitted, got %q", gotBody["source_s3url"])
	}
}

func TestGetCredsFromServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"detail":"access denied"}`)
	}))
	defer ts.Close()

	t.Setenv("EXOHUB_API_URL", ts.URL)
	noop := func(string, ...any) {}

	_, err := getCredsFromServer("s3://bucket/prefix/", "READ", "", noop)
	if err == nil {
		t.Fatal("expected error from server 403")
	}
	if !errors.Is(err, errAccessDenied) {
		t.Errorf("expected errAccessDenied, got: %v", err)
	}
	if !strings.Contains(err.Error(), "access denied") {
		t.Errorf("expected 'access denied' in error message, got: %v", err)
	}
}

func TestGetCredsFromServer403(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"detail":"you are not authorized to access this dataset"}`)
	}))
	defer ts.Close()

	t.Setenv("EXOHUB_API_URL", ts.URL)
	noop := func(string, ...any) {}

	_, err := getCredsFromServer("s3://bucket/prefix/", "READ", "", noop)
	if err == nil {
		t.Fatal("expected error from server 403")
	}
	if !errors.Is(err, errAccessDenied) {
		t.Errorf("expected errAccessDenied, got: %v", err)
	}
	if !strings.Contains(err.Error(), "you are not authorized to access this dataset") {
		t.Errorf("expected detail in error message, got: %v", err)
	}
}

func TestGetCredsFromServer401(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"detail":"not authenticated"}`)
	}))
	defer ts.Close()

	t.Setenv("EXOHUB_API_URL", ts.URL)
	noop := func(string, ...any) {}

	_, err := getCredsFromServer("s3://bucket/prefix/", "READ", "", noop)
	if err == nil {
		t.Fatal("expected error from server 401")
	}
	if err != errAuthRequired {
		t.Errorf("expected errAuthRequired, got: %v", err)
	}
}

func TestGetCredsFromServer5xx(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, `{"detail":"upstream service unavailable"}`)
	}))
	defer ts.Close()

	t.Setenv("EXOHUB_API_URL", ts.URL)
	noop := func(string, ...any) {}

	_, err := getCredsFromServer("s3://bucket/prefix/", "READ", "", noop)
	if err == nil {
		t.Fatal("expected error from server 502")
	}
	if errors.Is(err, errAccessDenied) || err == errAuthRequired {
		t.Errorf("expected transient error, got sentinel: %v", err)
	}
	if !strings.Contains(err.Error(), "server error") {
		t.Errorf("expected 'server error' in message, got: %v", err)
	}
	if !strings.Contains(err.Error(), "try again later") {
		t.Errorf("expected 'try again later' in message, got: %v", err)
	}
	if !strings.Contains(err.Error(), "upstream service unavailable") {
		t.Errorf("expected detail in message, got: %v", err)
	}
}

func TestGetCredsFromServerDetailParsed(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		// Raw body contains extra noise; only 'detail' should be surfaced.
		fmt.Fprint(w, `{"detail":"clean detail message","trace":"long internal trace..."}`)
	}))
	defer ts.Close()

	t.Setenv("EXOHUB_API_URL", ts.URL)
	noop := func(string, ...any) {}

	_, err := getCredsFromServer("s3://bucket/prefix/", "READ", "", noop)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "clean detail message") {
		t.Errorf("expected JSON detail field to be surfaced, got: %v", err)
	}
	// The raw trace field should NOT appear in the error.
	if strings.Contains(err.Error(), "long internal trace") {
		t.Errorf("raw body noise should not appear in error, got: %v", err)
	}
}


// TestInstanceSourceRefusalHint_FirstClaim verifies the exact detail text
// from the API's authorize_instance_project refusal (exohub-workers!651)
// for a project with no recorded source yet is recognized — quoting the
// server's own wording so a future change to it breaks this test, not
// users.
func TestInstanceSourceRefusalHint_FirstClaim(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"detail":"instance project 'DS1' has no recorded source yet; a READWRITE request with write access on the source is required to claim it"}`)
	}))
	defer ts.Close()

	t.Setenv("EXOHUB_API_URL", ts.URL)
	noop := func(string, ...any) {}

	_, err := getCredsFromServer("s3://instance-bucket/DS1", "READ", "s3://atlas-bucket/DS1", noop)
	if err == nil {
		t.Fatal("expected error")
	}
	hint, ok := instanceSourceRefusalHint(err)
	if !ok {
		t.Fatalf("expected instanceSourceRefusalHint to recognize the first-claim refusal, got err: %v", err)
	}
	if !strings.Contains(hint, "claim") {
		t.Errorf("expected hint to mention claiming, got: %q", hint)
	}
}

// TestInstanceSourceRefusalHint_BoundToDifferentSource verifies the exact
// binding-mismatch detail text is recognized.
func TestInstanceSourceRefusalHint_BoundToDifferentSource(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"detail":"instance project 'DS1' is bound to a different source project"}`)
	}))
	defer ts.Close()

	t.Setenv("EXOHUB_API_URL", ts.URL)
	noop := func(string, ...any) {}

	_, err := getCredsFromServer("s3://instance-bucket/DS1", "READ", "s3://atlas-bucket/DS1", noop)
	if err == nil {
		t.Fatal("expected error")
	}
	hint, ok := instanceSourceRefusalHint(err)
	if !ok {
		t.Fatalf("expected instanceSourceRefusalHint to recognize the binding mismatch, got err: %v", err)
	}
	if !strings.Contains(hint, "data_from") {
		t.Errorf("expected hint to mention data_from, got: %q", hint)
	}
}

// TestInstanceSourceRefusalHint_ProjectMismatch verifies the exact
// source_s3url project-id mismatch detail text is recognized.
func TestInstanceSourceRefusalHint_ProjectMismatch(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"detail":"source_s3url project 'other-proj' does not match instance project_id 'DS1'"}`)
	}))
	defer ts.Close()

	t.Setenv("EXOHUB_API_URL", ts.URL)
	noop := func(string, ...any) {}

	_, err := getCredsFromServer("s3://instance-bucket/DS1", "READ", "s3://atlas-bucket/other-proj", noop)
	if err == nil {
		t.Fatal("expected error")
	}
	hint, ok := instanceSourceRefusalHint(err)
	if !ok {
		t.Fatalf("expected instanceSourceRefusalHint to recognize the project-id mismatch, got err: %v", err)
	}
	if !strings.Contains(hint, "project_id") {
		t.Errorf("expected hint to mention project_id, got: %q", hint)
	}
}

// TestInstanceSourceRefusalHint_SourceS3URLRequired verifies the exact 400
// "source_s3url is required" detail text (an old client that predates
// source_s3url support) is recognized.
func TestInstanceSourceRefusalHint_SourceS3URLRequired(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"detail":"source_s3url is required for instance project credential requests"}`)
	}))
	defer ts.Close()

	t.Setenv("EXOHUB_API_URL", ts.URL)
	noop := func(string, ...any) {}

	_, err := getCredsFromServer("s3://instance-bucket/DS1", "READ", "", noop)
	if err == nil {
		t.Fatal("expected error")
	}
	hint, ok := instanceSourceRefusalHint(err)
	if !ok {
		t.Fatalf("expected instanceSourceRefusalHint to recognize the source_s3url-required refusal, got err: %v", err)
	}
	if !strings.Contains(hint, "upgrade") {
		t.Errorf("expected hint to mention upgrading, got: %q", hint)
	}
}

func TestFallbackToServerOnMissingProfile(t *testing.T) {
	// Simulate a Model 2 worker: no local AWS profile, but token.json is present.
	tmpDir := t.TempDir()
	t.Setenv("EXO_CONFIG_DIR", tmpDir)

	// Write a token so getCredsFromServer can attach Authorization header.
	tokenDir := filepath.Join(tmpDir, "exo", "credentials")
	os.MkdirAll(tokenDir, 0700)
	os.WriteFile(filepath.Join(tokenDir, "token.json"), []byte(`{"access_token":"model2-jwt"}`), 0600)

	expiration := time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/grants/credentials" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"AccessKeyId":"ASIAMODEL2","SecretAccessKey":"model2secret","SessionToken":"model2token","Expiration":%q}`, expiration)
	}))
	defer ts.Close()
	t.Setenv("EXOHUB_API_URL", ts.URL)

	noop := func(string, ...any) {}

	// Part 1: isMissingCredentialsError must classify the fast-path failure.
	fastPathErr := fmt.Errorf("failed to load AWS config: failed to get shared config profile, exohub")
	if !isMissingCredentialsError(fastPathErr) {
		t.Fatal("expected isMissingCredentialsError to return true for missing-profile error")
	}

	// Part 2: getCredsFromServer must return Model 2 credentials.
	creds, err := getCredsFromServer("s3://bucket/prefix/", "READ", "", noop)
	if err != nil {
		t.Fatalf("getCredsFromServer error: %v", err)
	}
	if creds.AccessKeyID != "ASIAMODEL2" {
		t.Errorf("AccessKeyID = %q, want ASIAMODEL2", creds.AccessKeyID)
	}
	if creds.Version != 1 {
		t.Errorf("Version = %d, want 1", creds.Version)
	}
}

// TestCacheLockConcurrency verifies that acquireCacheLock serialises concurrent
// callers so that only one of them fetches credentials while the rest read from
// the cache written by the winner.
func TestCacheLockConcurrency(t *testing.T) {
	const concurrency = 8

	var fetchCount atomic.Int32
	expiration := time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/grants/credentials" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		fetchCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"AccessKeyId":"ASIACONCURRENT","SecretAccessKey":"secret","SessionToken":"token","Expiration":%q}`, expiration)
	}))
	defer ts.Close()

	tmpDir := t.TempDir()
	t.Setenv("EXO_CONFIG_DIR", tmpDir)
	t.Setenv("EXOHUB_API_URL", ts.URL)

	s3URL := "s3://concurrent-bucket/prefix/"
	perm := "READ"
	noop := func(string, ...any) {}

	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			// Each goroutine mirrors the lock-then-check-then-fetch-then-cache
			// sequence from main().
			lf, err := acquireCacheLock(s3URL, perm, true)
			if err != nil {
				t.Errorf("acquireCacheLock error: %v", err)
				return
			}
			defer func() {
				_ = syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)
				lf.Close()
			}()

			// Double-check: a sibling may have written the cache while we waited.
			if cached, err := loadCachedCredentials(s3URL, perm, true); err == nil && cached != nil {
				return
			}

			// Fetch from server and write cache.
			creds, err := getCredsFromServer(s3URL, perm, "", noop)
			if err != nil {
				t.Errorf("getCredsFromServer error: %v", err)
				return
			}
			if err := cacheCredentials(s3URL, perm, true, creds); err != nil {
				t.Errorf("cacheCredentials error: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := fetchCount.Load(); got != 1 {
		t.Errorf("server called %d times, want 1", got)
	}
}
