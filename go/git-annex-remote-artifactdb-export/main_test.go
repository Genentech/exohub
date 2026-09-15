//go:build artifactdb

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

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
		if strings.HasPrefix(line, "CONFIG grants=") {
			found["grants"] = true
		}
		if line == "INITREMOTE-SUCCESS" {
			found["success"] = true
		}
	}

	if !found["s3url"] || !found["instance_url"] || !found["grants"] || !found["success"] {
		t.Errorf("handleInitRemote output missing expected lines: %v", output)
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
