//go:build artifactdb

package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadExoTokenFromEnv(t *testing.T) {
	t.Setenv("EXO_TOKEN", `{"access_token":"env-token"}`)

	if got := loadExoToken(); got != "env-token" {
		t.Errorf("loadExoToken() = %q, want %q", got, "env-token")
	}
}

func TestLoadExoTokenFromEnvRawJWT(t *testing.T) {
	raw := "eyJhbGciOiJIUzI1NiJ9.payload.signature"
	t.Setenv("EXO_TOKEN", raw)

	if got := loadExoToken(); got != raw {
		t.Errorf("loadExoToken() = %q, want %q", got, raw)
	}
}

func TestLoadExoTokenFromTokenFile(t *testing.T) {
	tmpDir := t.TempDir()
	tokenFile := filepath.Join(tmpDir, "token.json")
	if err := os.WriteFile(tokenFile, []byte(`{"access_token":"file-token"}`), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Setenv("EXO_TOKEN", "")
	t.Setenv("EXO_TOKEN_FILE", tokenFile)

	if got := loadExoToken(); got != "file-token" {
		t.Errorf("loadExoToken() = %q, want %q", got, "file-token")
	}
}

func TestLoadExoTokenFromConfigDir(t *testing.T) {
	tmpDir := t.TempDir()
	tokenDir := filepath.Join(tmpDir, "exo", "credentials")
	if err := os.MkdirAll(tokenDir, 0700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	tokenFile := filepath.Join(tokenDir, "token.json")
	if err := os.WriteFile(tokenFile, []byte(`{"access_token":"configdir-token"}`), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Setenv("EXO_TOKEN", "")
	t.Setenv("EXO_TOKEN_FILE", "")
	t.Setenv("EXO_CONFIG_DIR", tmpDir)

	if got := loadExoToken(); got != "configdir-token" {
		t.Errorf("loadExoToken() = %q, want %q", got, "configdir-token")
	}
}

func TestLoadExoTokenDefaultPath(t *testing.T) {
	tmpDir := t.TempDir()
	tokenDir := filepath.Join(tmpDir, "exo", "credentials")
	if err := os.MkdirAll(tokenDir, 0700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	tokenFile := filepath.Join(tokenDir, "token.json")
	if err := os.WriteFile(tokenFile, []byte(`{"access_token":"default-token"}`), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Setenv("EXO_TOKEN", "")
	t.Setenv("EXO_TOKEN_FILE", "")
	t.Setenv("EXO_CONFIG_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("HOME", tmpDir)

	if got := loadExoToken(); got != "default-token" {
		t.Errorf("loadExoToken() = %q, want %q", got, "default-token")
	}
}

func TestLoadExoTokenMissing(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("EXO_TOKEN", "")
	t.Setenv("EXO_TOKEN_FILE", "")
	t.Setenv("EXO_CONFIG_DIR", tmpDir)

	if got := loadExoToken(); got != "" {
		t.Errorf("loadExoToken() = %q, want empty", got)
	}
}

func TestParseS3url(t *testing.T) {
	tests := []struct {
		input      string
		wantBucket string
		wantPrefix string
		wantErr    bool
	}{
		{"s3://bucket/prefix", "bucket", "prefix", false},
		{"s3://bucket/a/b/c", "bucket", "a/b/c", false},
		{"s3://bucket", "bucket", "", false},
		{"http://bucket/prefix", "", "", true},
		{"invalid", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			bucket, prefix, err := parseS3url(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error for %q", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if bucket != tt.wantBucket {
				t.Errorf("bucket = %q, want %q", bucket, tt.wantBucket)
			}
			if prefix != tt.wantPrefix {
				t.Errorf("prefix = %q, want %q", prefix, tt.wantPrefix)
			}
		})
	}
}

func TestS3ExportPath(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
		want   string
	}{
		{".exohub/bundles/v1.0/bundle.json", "path/_artifactdb", "path/_artifactdb/.exohub/bundles/v1.0/bundle.json"},
		{".exohub/bundles/v1.0/files/data.json", "prefix", "prefix/.exohub/bundles/v1.0/files/data.json"},
		{".artifactdb/meta.json", "prefix", "prefix/.artifactdb/meta.json"},
		{"file.json", "", "file.json"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := s3ExportPath(tt.name, tt.prefix)
			if got != tt.want {
				t.Errorf("s3ExportPath(%q, %q) = %q, want %q",
					tt.name, tt.prefix, got, tt.want)
			}
		})
	}
}

func TestHandleInitRemote(t *testing.T) {
	var output []string
	origWriteLine := writeLine
	writeLine = func(line string) {
		output = append(output, line)
	}
	defer func() { writeLine = origWriteLine }()

	config = map[string]string{
		"s3url":        "s3://bucket/_artifactdb",
		"instance_url": "https://catalog.example.com/v1/mydb",
		"atlas_url":    "https://atlas.example.com/v1/sandbox1",
		"grants":       "true",
	}

	handleInitRemote()

	found := map[string]bool{}
	for _, line := range output {
		if strings.HasPrefix(line, "CONFIG s3url=") {
			found["s3url"] = true
		}
		if strings.HasPrefix(line, "CONFIG instance_url=") {
			found["instance_url"] = true
		}
		if strings.HasPrefix(line, "CONFIG atlas_url=") {
			found["atlas_url"] = true
		}
		if strings.HasPrefix(line, "CONFIG grants=") {
			found["grants"] = true
		}
		if line == "INITREMOTE-SUCCESS" {
			found["success"] = true
		}
	}

	if !found["s3url"] || !found["instance_url"] || !found["atlas_url"] || !found["grants"] || !found["success"] {
		t.Errorf("handleInitRemote output missing expected lines: %v", output)
	}
}

func TestHandleInitRemoteNoAtlasURL(t *testing.T) {
	var output []string
	origWriteLine := writeLine
	writeLine = func(line string) {
		output = append(output, line)
	}
	defer func() { writeLine = origWriteLine }()

	config = map[string]string{
		"s3url": "s3://bucket/_artifactdb",
	}

	handleInitRemote()

	for _, line := range output {
		if strings.HasPrefix(line, "CONFIG atlas_url=") {
			t.Errorf("handleInitRemote emitted CONFIG atlas_url when none was set: %v", output)
		}
	}
}

func TestHandleExport(t *testing.T) {
	var output []string
	origWriteLine := writeLine
	writeLine = func(line string) {
		output = append(output, line)
	}
	defer func() { writeLine = origWriteLine }()

	exportName = ""
	handleExport(".exohub/bundles/v1.0/bundle.json")
	if exportName != ".exohub/bundles/v1.0/bundle.json" {
		t.Errorf("exportName = %q, want %q", exportName, ".exohub/bundles/v1.0/bundle.json")
	}
}

func TestHandleRemoveExportDirectoryWhenEmpty(t *testing.T) {
	var output []string
	origWriteLine := writeLine
	writeLine = func(line string) {
		output = append(output, line)
	}
	defer func() { writeLine = origWriteLine }()

	handleRemoveExportDirectoryWhenEmpty("some/dir")

	if len(output) != 1 || output[0] != "REMOVEEXPORTDIRECTORY-SUCCESS" {
		t.Errorf("expected REMOVEEXPORTDIRECTORY-SUCCESS, got %v", output)
	}
}

// makePermissionsDir creates a temp directory with a .exohub/permissions file
// containing content and returns the directory path.
func makePermissionsDir(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".exohub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".exohub", "permissions"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// captureUploadedPermissions runs uploadPermissions with a fake s5cmd that
// captures the JSON it would upload, and returns the parsed result.
func captureUploadedPermissions(t *testing.T, rootDir string) map[string]interface{} {
	t.Helper()
	out := filepath.Join(rootDir, "permissions.json")
	fakeS5 := filepath.Join(rootDir, "s5cmd")
	script := "#!/bin/sh\ncp \"$2\" " + out + "\n"
	if err := os.WriteFile(fakeS5, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := uploadPermissions(fakeS5, "s3://bucket/v1", rootDir); err != nil {
		t.Fatalf("uploadPermissions: %v", err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read captured permissions.json: %v", err)
	}
	var result map[string]interface{}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal permissions.json: %v", err)
	}
	return result
}

func TestUploadPermissions_QuotedValues(t *testing.T) {
	dir := makePermissionsDir(t, `owners:
  - "aboyounp"
read_access: "public"
write_access: "owners"
`)
	got := captureUploadedPermissions(t, dir)

	owners, ok := got["owners"].([]interface{})
	if !ok || len(owners) != 1 || owners[0] != "aboyounp" {
		t.Errorf("owners = %v, want [aboyounp]", got["owners"])
	}
	if got["read_access"] != "public" {
		t.Errorf("read_access = %q, want \"public\"", got["read_access"])
	}
	if got["write_access"] != "owners" {
		t.Errorf("write_access = %q, want \"owners\"", got["write_access"])
	}
	if _, hasViewers := got["viewers"]; hasViewers {
		t.Errorf("viewers should be absent when not set")
	}
}

func TestUploadPermissions_UnquotedValues(t *testing.T) {
	dir := makePermissionsDir(t, `owners:
  - aboyounp
read_access: public
write_access: owners
`)
	got := captureUploadedPermissions(t, dir)

	owners, ok := got["owners"].([]interface{})
	if !ok || len(owners) != 1 || owners[0] != "aboyounp" {
		t.Errorf("owners = %v, want [aboyounp]", got["owners"])
	}
	if got["read_access"] != "public" {
		t.Errorf("read_access = %q, want \"public\"", got["read_access"])
	}
}

func TestUploadPermissions_WithViewers(t *testing.T) {
	dir := makePermissionsDir(t, `owners:
  - "alice"
viewers:
  - "bob"
  - "carol"
read_access: "authenticated"
write_access: "owners"
`)
	got := captureUploadedPermissions(t, dir)

	viewers, ok := got["viewers"].([]interface{})
	if !ok {
		t.Fatalf("viewers missing or wrong type: %v", got["viewers"])
	}
	want := []interface{}{"bob", "carol"}
	if !reflect.DeepEqual(viewers, want) {
		t.Errorf("viewers = %v, want %v", viewers, want)
	}
}

func TestUploadPermissions_MissingFile(t *testing.T) {
	dir := t.TempDir() // no .exohub/permissions created
	if err := uploadPermissions("/nonexistent/s5cmd", "s3://bucket/v1", dir); err != nil {
		t.Errorf("expected nil error for missing permissions file, got %v", err)
	}
}

func TestUploadPermissions_MalformedYAML(t *testing.T) {
	dir := makePermissionsDir(t, "owners: [unclosed\n")
	err := uploadPermissions("/nonexistent/s5cmd", "s3://bucket/v1", dir)
	if err == nil {
		t.Error("expected error for malformed YAML, got nil")
	}
	if !strings.Contains(err.Error(), "parse .exohub/permissions") {
		t.Errorf("error message should mention 'parse .exohub/permissions', got: %v", err)
	}
}

func TestUploadPermissions_MissingRequiredFields(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr string
	}{
		{
			name:    "empty owners",
			content: "owners: []\nread_access: public\nwrite_access: owners\n",
			wantErr: "owners must be non-empty",
		},
		{
			name:    "missing read_access",
			content: "owners:\n  - alice\nwrite_access: owners\n",
			wantErr: "read_access must be set",
		},
		{
			name:    "missing write_access",
			content: "owners:\n  - alice\nread_access: public\n",
			wantErr: "write_access must be set",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := makePermissionsDir(t, tt.content)
			err := uploadPermissions("/nonexistent/s5cmd", "s3://bucket/v1", dir)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestPublishNotify_WithAtlasURL(t *testing.T) {
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(body)
		gotBody = body
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","results":[]}`))
	}))
	defer server.Close()

	_, err := publishNotify(server.URL, "myproject", "ssh://git@host/org/myproject.git", "abc123", "v1.0.0",
		"s3://bucket/prefix/v1.0.0/", "s3://bucket/prefix", "", "https://atlas.example.com/v1/sandbox1")
	if err != nil {
		t.Fatalf("publishNotify() error = %v", err)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(gotBody, &payload); err != nil {
		t.Fatalf("failed to parse request payload: %v", err)
	}
	if got, want := payload["atlas_url"], "https://atlas.example.com/v1/sandbox1"; got != want {
		t.Errorf("payload[atlas_url] = %v, want %v", got, want)
	}
}

func TestPublishNotify_WithoutAtlasURL(t *testing.T) {
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(body)
		gotBody = body
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","results":[]}`))
	}))
	defer server.Close()

	_, err := publishNotify(server.URL, "myproject", "ssh://git@host/org/myproject.git", "abc123", "v1.0.0",
		"s3://bucket/prefix/v1.0.0/", "s3://bucket/prefix", "", "")
	if err != nil {
		t.Fatalf("publishNotify() error = %v", err)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(gotBody, &payload); err != nil {
		t.Fatalf("failed to parse request payload: %v", err)
	}
	if _, present := payload["atlas_url"]; present {
		t.Errorf("payload should not contain atlas_url when unset, got: %s", string(gotBody))
	}
}

func TestPublishNotify_422WithAtlasURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"detail":"atlas_url not in allowlist"}`))
	}))
	defer server.Close()

	_, err := publishNotify(server.URL, "myproject", "ssh://git@host/org/myproject.git", "abc123", "v1.0.0",
		"s3://bucket/prefix/v1.0.0/", "s3://bucket/prefix", "", "https://atlas.example.com/v1/not-allowed")
	if err == nil {
		t.Fatal("expected error for 422 response, got nil")
	}
	if !strings.Contains(err.Error(), "API returned 422") {
		t.Errorf("error = %v, want it to mention 'API returned 422'", err)
	}
}

func TestPublishNotify_422ReturnsAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"detail":{"code":"atlas_url_not_allowlisted","message":"atlas_url not allowed"}}`))
	}))
	defer server.Close()

	_, err := publishNotify(server.URL, "myproject", "ssh://git@host/org/myproject.git", "abc123", "v1.0.0",
		"s3://bucket/prefix/v1.0.0/", "s3://bucket/prefix", "", "")

	var apiErr *apiError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *apiError", err)
	}
	if apiErr.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("apiErr.StatusCode = %d, want 422", apiErr.StatusCode)
	}
}

func TestDescribePublishError_StructuredCode(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []string // substrings expected in the output
	}{
		{
			name: "atlas_url_not_allowlisted",
			body: `{"detail":{"code":"atlas_url_not_allowlisted","message":"atlas_url not allowed","allowed":["https://atlas.example.com/v1/a","https://atlas.example.com/v1/b"]}}`,
			want: []string{"atlas_url not allowed", "the catalog remote's atlas_url", ".exohub/remotes", "https://atlas.example.com/v1/a"},
		},
		{
			name: "instance_url_not_allowlisted",
			body: `{"detail":{"code":"instance_url_not_allowlisted","message":"instance_url not allowed","allowed":["https://artifactdb.example.com/v1/a"]}}`,
			want: []string{"instance_url not allowed", "the catalog remote's instance_url", ".exohub/remotes", "https://artifactdb.example.com/v1/a"},
		},
		{
			name: "unknown code falls back to server message",
			body: `{"detail":{"code":"some_future_code","message":"a brand new refusal"}}`,
			want: []string{"a brand new refusal"},
		},
		{
			name: "old-server plain string detail",
			body: `{"detail":"atlas_url not in allowlist"}`,
			want: []string{"atlas_url not in allowlist"},
		},
		{
			name: "FastAPI validation-error list",
			body: `{"detail":[{"loc":["body","atlas_url"],"msg":"Input should be a valid URL"}]}`,
			want: []string{"body.atlas_url", "Input should be a valid URL"},
		},
		{
			name: "unparseable body falls back to raw text",
			body: `not json`,
			want: []string{"not json"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := describePublishError([]byte(tc.body))
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("describePublishError(%q) = %q, want substring %q", tc.body, got, want)
				}
			}
		})
	}
}

func TestPublishJSONErrorFromBody_StructuredCode(t *testing.T) {
	body := []byte(`{"detail":{"code":"atlas_url_not_allowlisted","message":"atlas_url not allowed","allowed":["https://atlas.example.com/v1/a","https://atlas.example.com/v1/b"]}}`)
	got := publishJSONErrorFromBody(body)
	if got == nil {
		t.Fatal("publishJSONErrorFromBody() = nil")
	}
	if got.Code != "atlas_url_not_allowlisted" || got.Message != "atlas_url not allowed" {
		t.Errorf("got = %+v", got)
	}
	if !reflect.DeepEqual(got.Allowed, []string{"https://atlas.example.com/v1/a", "https://atlas.example.com/v1/b"}) {
		t.Errorf("got.Allowed = %v", got.Allowed)
	}
}

func TestPublishJSONErrorFromBody_InstanceURLNotAllowlisted(t *testing.T) {
	body := []byte(`{"detail":{"code":"instance_url_not_allowlisted","message":"instance_url not allowed","allowed":["https://artifactdb.example.com/v1/a"]}}`)
	got := publishJSONErrorFromBody(body)
	if got == nil {
		t.Fatal("publishJSONErrorFromBody() = nil")
	}
	if got.Code != "instance_url_not_allowlisted" || got.Message != "instance_url not allowed" {
		t.Errorf("got = %+v", got)
	}
	if !reflect.DeepEqual(got.Allowed, []string{"https://artifactdb.example.com/v1/a"}) {
		t.Errorf("got.Allowed = %v", got.Allowed)
	}
}

func TestPublishJSONErrorFromBody_PlainStringFallsBackToMessageOnly(t *testing.T) {
	body := []byte(`{"detail":"atlas_url not in allowlist"}`)
	got := publishJSONErrorFromBody(body)
	if got == nil {
		t.Fatal("publishJSONErrorFromBody() = nil")
	}
	if got.Code != "" {
		t.Errorf("got.Code = %q, want empty for an old-server plain-string detail", got.Code)
	}
	if got.Message != "atlas_url not in allowlist" {
		t.Errorf("got.Message = %q", got.Message)
	}
	if len(got.Allowed) != 0 {
		t.Errorf("got.Allowed = %v, want empty", got.Allowed)
	}
}

// TestPublishJSONResult_SchemaStable locks the --json publish result's wire
// shape: {atlas_url_used, published, error}. atlas_url_used must serialize
// to null (not be absent) when nil, and the error object's code/message keys
// must always be present (not dropped by omitempty) when error is non-nil.
// Renaming or dropping any of these keys, or changing atlas_url_used from
// null to absent, should fail this test.
func TestPublishJSONResult_SchemaStable(t *testing.T) {
	success := publishJSONResult{Published: true}
	data, err := json.Marshal(success)
	if err != nil {
		t.Fatal(err)
	}
	var successFields map[string]interface{}
	if err := json.Unmarshal(data, &successFields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"atlas_url_used", "published"} {
		if _, ok := successFields[key]; !ok {
			t.Errorf("success result missing key %q: %s", key, data)
		}
	}
	if successFields["atlas_url_used"] != nil {
		t.Errorf("atlas_url_used = %v, want null when unset", successFields["atlas_url_used"])
	}
	if _, present := successFields["error"]; present {
		t.Errorf("success result should omit \"error\" entirely, got: %s", data)
	}

	failure := publishJSONResult{Published: false, Error: &publishJSONError{Code: "atlas_url_not_allowlisted", Message: "atlas_url not allowed"}}
	data, err = json.Marshal(failure)
	if err != nil {
		t.Fatal(err)
	}
	var failureFields map[string]interface{}
	if err := json.Unmarshal(data, &failureFields); err != nil {
		t.Fatal(err)
	}
	errObj, ok := failureFields["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("failure result missing \"error\" object: %s", data)
	}
	for _, key := range []string{"code", "message"} {
		if _, ok := errObj[key]; !ok {
			t.Errorf("error object missing key %q: %s", key, data)
		}
	}
	if _, present := errObj["allowed"]; present {
		t.Errorf("error.allowed should be omitted when empty, got: %s", data)
	}
}

func TestGetServiceInfo_WithDefaultAtlasURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"default_atlas_url":"https://atlas.example.com/v1/default"}`))
	}))
	defer server.Close()

	got, ok := getServiceInfo(server.URL)
	if !ok {
		t.Fatal("getServiceInfo() ok = false, want true")
	}
	if got != "https://atlas.example.com/v1/default" {
		t.Errorf("getServiceInfo() = %q, want %q", got, "https://atlas.example.com/v1/default")
	}
}

func TestGetServiceInfo_OlderServerWithoutField(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":"1.0.0"}`))
	}))
	defer server.Close()

	_, ok := getServiceInfo(server.URL)
	if ok {
		t.Error("getServiceInfo() ok = true, want false for a server without default_atlas_url")
	}
}

func TestGetServiceInfo_Unreachable(t *testing.T) {
	_, ok := getServiceInfo("http://127.0.0.1:1")
	if ok {
		t.Error("getServiceInfo() ok = true, want false for an unreachable server")
	}
}

func TestPublishNotify_SuccessSurfacesAtlasURLUsed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","atlas_url_used":"https://atlas.example.com/v1/sandbox1","results":[]}`))
	}))
	defer server.Close()

	respBody, err := publishNotify(server.URL, "myproject", "ssh://git@host/org/myproject.git", "abc123", "v1.0.0",
		"s3://bucket/prefix/v1.0.0/", "s3://bucket/prefix", "", "")
	if err != nil {
		t.Fatalf("publishNotify() error = %v", err)
	}

	var resp struct {
		AtlasURLUsed string `json:"atlas_url_used"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp.AtlasURLUsed != "https://atlas.example.com/v1/sandbox1" {
		t.Errorf("atlas_url_used = %q, want %q", resp.AtlasURLUsed, "https://atlas.example.com/v1/sandbox1")
	}
}
