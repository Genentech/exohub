//go:build exohubapi

package credhelper

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetCredsFromServer_Success(t *testing.T) {
	want := Credentials{
		Version:         1,
		AccessKeyID:     "ASIA123",
		SecretAccessKey: "secret",
		SessionToken:    "token",
		Expiration:      "2099-01-01T00:00:00Z",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/grants/credentials" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("expected Bearer header, got %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(want)
	}))
	defer srv.Close()

	t.Setenv("EXOHUB_API_URL", srv.URL)
	creds, err := GetCredsFromServer(context.Background(), "s3://bucket/prefix/", "READWRITE",
		func() (string, error) { return "test-token", nil },
		nil,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if creds.AccessKeyID != want.AccessKeyID {
		t.Errorf("AccessKeyID = %q, want %q", creds.AccessKeyID, want.AccessKeyID)
	}
	if creds.Version != 1 {
		t.Errorf("Version = %d, want 1", creds.Version)
	}
}

func TestGetCredsFromServer_401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"detail":"not authenticated"}`))
	}))
	defer srv.Close()

	t.Setenv("EXOHUB_API_URL", srv.URL)
	_, err := GetCredsFromServer(context.Background(), "s3://bucket/prefix/", "READ", nil, nil)
	if !errors.Is(err, ErrAuthRequired) {
		t.Errorf("expected ErrAuthRequired, got %v", err)
	}
}

func TestGetCredsFromServer_403(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"detail":"you are not authorized"}`))
	}))
	defer srv.Close()

	t.Setenv("EXOHUB_API_URL", srv.URL)
	_, err := GetCredsFromServer(context.Background(), "s3://bucket/prefix/", "READ", nil, nil)
	if !errors.Is(err, ErrAccessDenied) {
		t.Errorf("expected ErrAccessDenied, got %v", err)
	}
}

func TestGetCredsFromServer_Unreachable(t *testing.T) {
	t.Setenv("EXOHUB_API_URL", "http://127.0.0.1:1") // nothing listens here
	_, err := GetCredsFromServer(context.Background(), "s3://bucket/prefix/", "READ", nil, nil)
	if err == nil {
		t.Error("expected error for unreachable server")
	}
}
