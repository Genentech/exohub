package main

import (
	"fmt"
	"os"
	"testing"
	"time"
)

func resetState() {
	config = map[string]string{}
	keyToFile = map[string]string{}
	exportName = ""
	exportRef = ""
	exportRefSet = false
	currentLocation = ""
	expectedContent = map[string]string{}
}

func TestParseS3url(t *testing.T) {
	bucket, prefix, err := parseS3url("s3://my-bucket/path/to/dir")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bucket != "my-bucket" {
		t.Fatalf("bucket mismatch: %q", bucket)
	}
	if prefix != "path/to/dir" {
		t.Fatalf("prefix mismatch: %q", prefix)
	}

	if _, _, err := parseS3url("http://example.com"); err == nil {
		t.Fatal("expected error for non-s3 url")
	}
	if _, _, err := parseS3url("s3:///missing"); err == nil {
		t.Fatal("expected error for missing bucket")
	}
}

func TestS3Key(t *testing.T) {
	if got := s3Key("key", ""); got != "key" {
		t.Fatalf("unexpected key: %q", got)
	}
	if got := s3Key("key", "prefix"); got != "prefix/key" {
		t.Fatalf("unexpected key: %q", got)
	}
	if got := s3Key("key", "prefix/"); got != "prefix/key" {
		t.Fatalf("unexpected key: %q", got)
	}
}

func TestS3ExportKey(t *testing.T) {
	resetState()
	os.Setenv("GIT_ANNEX_EXPORT_REF", "refs/tags/v1")
	defer os.Unsetenv("GIT_ANNEX_EXPORT_REF")

	if got := s3ExportKey("name", ""); got != "refs/tags/v1/name" {
		t.Fatalf("unexpected export key: %q", got)
	}
	if got := s3ExportKey("name", "prefix"); got != "prefix/refs/tags/v1/name" {
		t.Fatalf("unexpected export key: %q", got)
	}

	os.Unsetenv("GIT_ANNEX_EXPORT_REF")
	resetState()
	if got := s3ExportKey("name", "prefix"); got != "prefix/name" {
		t.Fatalf("unexpected export key without ref: %q", got)
	}
}

func TestGetInfoDict(t *testing.T) {
	resetState()
	info := getInfoDict()
	if _, ok := info["s3url"]; ok {
		t.Fatal("expected no s3url when unset")
	}

	resetState()
	config["s3url"] = "s3://bucket/prefix"
	info = getInfoDict()
	if info["s3url"] != "s3://bucket/prefix" {
		t.Fatalf("expected config s3url, got %q", info["s3url"])
	}

	resetState()
	os.Setenv("ANNEX_S3URL", "s3://envbucket")
	defer os.Unsetenv("ANNEX_S3URL")
	info = getInfoDict()
	if info["s3url"] != "s3://envbucket" {
		t.Fatalf("expected env s3url, got %q", info["s3url"])
	}
}

func TestParseS5cmdJSON(t *testing.T) {
	jsonOutput := []byte(`{"key":"bucket/prefix/file1.txt","type":"file","size":1024,"etag":"abc123","last_modified":"2024-01-01T00:00:00Z","storage_class":"STANDARD"}
{"key":"bucket/prefix/file2.txt","type":"file","size":2048,"etag":"def456","last_modified":"2024-01-02T00:00:00Z","storage_class":"STANDARD"}
{"key":"bucket/prefix/dir/","type":"directory","size":0,"etag":"","last_modified":"2024-01-01T00:00:00Z","storage_class":""}
`)

	objects, err := parseS5cmdJSON(jsonOutput)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(objects) != 2 {
		t.Fatalf("expected 2 objects, got %d", len(objects))
	}

	if objects[0].Key != "bucket/prefix/file1.txt" {
		t.Fatalf("unexpected key: %q", objects[0].Key)
	}
	if objects[0].Size != 1024 {
		t.Fatalf("unexpected size: %d", objects[0].Size)
	}
	if objects[0].ETag != "abc123" {
		t.Fatalf("unexpected etag: %q", objects[0].ETag)
	}

	if objects[1].Key != "bucket/prefix/file2.txt" {
		t.Fatalf("unexpected key: %q", objects[1].Key)
	}
	if objects[1].Size != 2048 {
		t.Fatalf("unexpected size: %d", objects[1].Size)
	}
}

func TestFormatContentIdentifier(t *testing.T) {
	// Test with ETag
	cid := formatContentIdentifier("abc123", 1024, parseTime("2024-01-01T00:00:00Z"))
	if cid != "etag:abc123" {
		t.Fatalf("unexpected content identifier: %q", cid)
	}

	// Test with quoted ETag
	cid = formatContentIdentifier(`"def456"`, 2048, parseTime("2024-01-01T00:00:00Z"))
	if cid != "etag:def456" {
		t.Fatalf("unexpected content identifier: %q", cid)
	}

	// Test without ETag (fallback to size:mtime)
	ts := parseTime("2024-01-01T00:00:00Z")
	cid = formatContentIdentifier("", 1024, ts)
	expected := fmt.Sprintf("size:1024:%d", ts.Unix())
	if cid != expected {
		t.Fatalf("unexpected content identifier: %q, expected %q", cid, expected)
	}

	// Test with dash ETag (fallback)
	cid = formatContentIdentifier("-", 1024, ts)
	expected = fmt.Sprintf("size:1024:%d", ts.Unix())
	if cid != expected {
		t.Fatalf("unexpected content identifier: %q, expected %q", cid, expected)
	}
}

func TestLocationTracking(t *testing.T) {
	resetState()
	currentLocation = ""
	expectedContent = map[string]string{}

	handleLocation("file1.txt")
	if currentLocation != "file1.txt" {
		t.Fatalf("expected currentLocation to be 'file1.txt', got %q", currentLocation)
	}

	handleExpected("etag:abc123")
	if expectedContent["file1.txt"] != "etag:abc123" {
		t.Fatalf("expected content identifier for file1.txt to be 'etag:abc123', got %q", expectedContent["file1.txt"])
	}

	resetLocationState()
	if currentLocation != "" {
		t.Fatalf("expected currentLocation to be empty after reset, got %q", currentLocation)
	}
	if _, exists := expectedContent["file1.txt"]; exists {
		t.Fatalf("expected expectedContent to be cleared for file1.txt")
	}
}

func TestNothingExpected(t *testing.T) {
	resetState()
	currentLocation = ""
	expectedContent = map[string]string{}

	handleLocation("file2.txt")
	handleNothingExpected()

	if expectedContent["file2.txt"] != "" {
		t.Fatalf("expected empty content identifier for file2.txt, got %q", expectedContent["file2.txt"])
	}
}

// Helper function to parse time for tests
func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

// makeFakeCredHelper writes a shell script that outputs JSON credentials with
// the given expiration to a temp dir and returns its path.
func makeFakeCredHelper(t *testing.T, expiration string) string {
	t.Helper()
	dir := t.TempDir()
	path := dir + "/fake-cred-helper"
	script := "#!/bin/sh\necho '{\"AccessKeyId\":\"FAKEKEY\",\"SecretAccessKey\":\"FAKESECRET\",\"SessionToken\":\"FAKETOKEN\",\"Expiration\":\"" + expiration + "\"}'\n"
	os.WriteFile(path, []byte(script), 0755)
	return path
}

func TestFetchBaseCredentialsSetsExpiration(t *testing.T) {
	resetState()
	exp := time.Now().Add(45 * time.Minute).UTC().Truncate(time.Second)
	expStr := exp.Format(time.RFC3339)

	origBin := credHelperBin
	defer func() { credHelperBin = origBin }()
	credHelperBin = makeFakeCredHelper(t, expStr)

	baseCredsExpiration = time.Time{}
	err := fetchBaseCredentials("s3://test-bucket/")
	if err != nil {
		t.Fatalf("fetchBaseCredentials error: %v", err)
	}
	if baseCredsExpiration.IsZero() {
		t.Error("baseCredsExpiration should be set after fetchBaseCredentials")
	}
	if !baseCredsExpiration.Equal(exp) {
		t.Errorf("baseCredsExpiration = %v, want %v", baseCredsExpiration, exp)
	}
}

func TestRefreshCredentialsIfNeeded_BaseCredsExpiring(t *testing.T) {
	resetState()
	exp := time.Now().Add(1 * time.Hour).UTC().Truncate(time.Second)
	expStr := exp.Format(time.RFC3339)

	origBin := credHelperBin
	defer func() { credHelperBin = origBin }()
	credHelperBin = makeFakeCredHelper(t, expStr)

	config["s3url"] = "s3://test-bucket/prefix/"
	config["grants"] = "false"
	// Pre-set expiration within 5-min window.
	baseCredsExpiration = time.Now().Add(2 * time.Minute)

	refreshCredentialsIfNeeded()

	// After refresh, baseCredsExpiration should be updated to the new expiry.
	if baseCredsExpiration.IsZero() || !baseCredsExpiration.Equal(exp) {
		t.Errorf("baseCredsExpiration = %v, want %v (proactive refresh did not fire or did not update)", baseCredsExpiration, exp)
	}
}

func TestRefreshCredentialsIfNeeded_BaseCredsNotExpiring(t *testing.T) {
	resetState()
	origBin := credHelperBin
	defer func() { credHelperBin = origBin }()
	// Use a helper that would succeed but should never be called.
	credHelperBin = "/bin/false"

	config["s3url"] = "s3://test-bucket/prefix/"
	config["grants"] = "false"
	notExpiring := time.Now().Add(30 * time.Minute)
	baseCredsExpiration = notExpiring

	refreshCredentialsIfNeeded()

	// baseCredsExpiration should be unchanged because proactive refresh should not fire.
	if !baseCredsExpiration.Equal(notExpiring) {
		t.Errorf("baseCredsExpiration changed unexpectedly: %v", baseCredsExpiration)
	}
}

func TestRefreshCredentialsIfNeeded_GrantsTrue_NotExpiring(t *testing.T) {
	resetState()
	origBin := credHelperBin
	defer func() { credHelperBin = origBin }()
	credHelperBin = "/bin/false"

	config["s3url"] = "s3://test-bucket/prefix/"
	config["grants"] = "true"
	notExpiring := time.Now().Add(30 * time.Minute)
	grantsExpiration = notExpiring

	// Should not call the credential helper (it would fail with /bin/false).
	refreshCredentialsIfNeeded()

	if !grantsExpiration.Equal(notExpiring) {
		t.Errorf("grantsExpiration changed unexpectedly: %v", grantsExpiration)
	}
}
