//go:build artifactdb

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// newFakeS5cmd writes a fake s5cmd to dir/s5cmd that mirrors every `sync`
// and `cp` destination (an s3://bucket/key URL) under uploadRoot (stripping
// the s3:// scheme), and serves `cat <url>` from whatever was written there
// — so tests can inspect exactly which objects a publish wrote, in which
// order (via the returned log file), without a real S3. A destination
// containing failOn (when non-empty) makes the command fail without
// writing anything, to simulate an upload failure mid-sync. A genuinely
// missing key fails `cat` with a NoSuchKey-style stderr message (so
// checkTagImmutability's not-found detection has something realistic to
// match); see newFakeS5cmdDenyCat for simulating a non-not-found read
// failure (e.g. AccessDenied).
func newFakeS5cmd(t *testing.T, uploadRoot, failOn string) string {
	return newFakeS5cmdDenyCat(t, uploadRoot, failOn, "")
}

// newFakeS5cmdDenyCat is newFakeS5cmd plus: a `cat <url>` whose url contains
// denyCatOn (when non-empty) fails with an AccessDenied-style stderr
// message instead of the usual NoSuchKey — for testing that
// checkTagImmutability treats a non-not-found read failure as a refusal,
// not as "no record".
func newFakeS5cmdDenyCat(t *testing.T, uploadRoot, failOn, denyCatOn string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "s5cmd")
	script := fmt.Sprintf(`#!/bin/bash
set -e
UPLOAD_ROOT=%q
FAIL_ON=%q
DENY_CAT_ON=%q
LOG=%q

cmd="$1"
case "$cmd" in
  sync)
    # Record the full invocation (full argv after "sync") BEFORE parsing
    # it, so a test can assert on exactly what flags s5cmd itself received
    # (e.g. that no --exclude was ever passed) regardless of how this fake
    # interprets them.
    echo "sync-argv ${*:2}" >> "$LOG"
    shift
    excludes=()
    pos=()
    while [ $# -gt 0 ]; do
      case "$1" in
        --exclude) excludes+=("$2"); shift 2 ;;
        *) pos+=("$1"); shift ;;
      esac
    done
    src="${pos[0]}"
    dst="${pos[1]}"
    src="${src%%/}"
    destpath=$(printf '%%s' "$dst" | sed 's#^s3://##')
    mkdir -p "$UPLOAD_ROOT/$destpath"
    find "$src" -type f | while read -r f; do
      rel=${f#"$src"/}
      skip=0
      for pat in "${excludes[@]}"; do
        case "$rel" in
          $pat) skip=1 ;;
        esac
      done
      if [ "$skip" -eq 1 ]; then
        continue
      fi
      if [ -n "$FAIL_ON" ]; then
        case "$dst$rel" in
          *"$FAIL_ON"*) exit 1 ;;
        esac
      fi
      mkdir -p "$UPLOAD_ROOT/$destpath/$(dirname "$rel")"
      cp "$f" "$UPLOAD_ROOT/$destpath/$rel"
      echo "sync $dst$rel" >> "$LOG"
      echo "$rel"
    done
    ;;
  cp)
    src="$2"
    dst="$3"
    if [ -n "$FAIL_ON" ]; then
      case "$dst" in
        *"$FAIL_ON"*) exit 1 ;;
      esac
    fi
    destpath=$(printf '%%s' "$dst" | sed 's#^s3://##')
    mkdir -p "$(dirname "$UPLOAD_ROOT/$destpath")"
    cp "$src" "$UPLOAD_ROOT/$destpath"
    echo "cp $dst" >> "$LOG"
    ;;
  cat)
    src="$2"
    if [ -n "$DENY_CAT_ON" ]; then
      case "$src" in
        *"$DENY_CAT_ON"*) echo "ERROR \"cat $src\": AccessDenied: Access Denied" >&2; exit 1 ;;
      esac
    fi
    srcpath=$(printf '%%s' "$src" | sed 's#^s3://##')
    if [ -f "$UPLOAD_ROOT/$srcpath" ]; then
      cat "$UPLOAD_ROOT/$srcpath"
    else
      echo "ERROR \"cat $src\": NoSuchKey: The specified key does not exist." >&2
      exit 1
    fi
    ;;
  rm)
    shift
    # Record the invocation (full argv after "rm") BEFORE attempting the
    # delete, success or failure — so a test can assert "no rm was ever
    # issued" and isn't fooled by a guard-less call that merely fails for
    # an unrelated reason (e.g. the target not existing).
    echo "rm $*" >> "$LOG"
    src=""
    for a in "$@"; do
      case "$a" in
        --raw) ;;
        *) src="$a" ;;
      esac
    done
    srcpath=$(printf '%%s' "$src" | sed 's#^s3://##')
    if [ -f "$UPLOAD_ROOT/$srcpath" ]; then
      rm "$UPLOAD_ROOT/$srcpath"
    else
      echo "ERROR \"rm $src\": NoSuchKey: The specified key does not exist." >&2
      exit 1
    fi
    ;;
  *)
    exit 1
    ;;
esac
`, uploadRoot, failOn, denyCatOn, filepath.Join(uploadRoot, "..log"))
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// uploadedLog returns the ordered list of writes recorded by newFakeS5cmd's
// LOG file ("sync <dst>" / "cp <dst>" lines), or nil if nothing was written.
func uploadedLog(t *testing.T, uploadRoot string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(uploadRoot, "..log"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	return lines
}

// trimTrailingPublishingCleanup drops the trailing "rm --raw .../..publishing"
// log entry — the best-effort cleanup a branch publish issues strictly
// AFTER the new ..complete marker is written — so a test can assert what
// the last object WRITTEN was without caring whether that benign cleanup
// delete happened to succeed and get logged after it.
func trimTrailingPublishingCleanup(log []string) []string {
	for len(log) > 0 && strings.HasPrefix(log[len(log)-1], "rm ") && strings.Contains(log[len(log)-1], "..publishing") {
		log = log[:len(log)-1]
	}
	return log
}

// uploadedExists reports whether the given s3:// URL was written under
// uploadRoot by newFakeS5cmd.
func uploadedExists(uploadRoot, s3url string) bool {
	key := strings.TrimPrefix(s3url, "s3://")
	_, err := os.Stat(filepath.Join(uploadRoot, key))
	return err == nil
}

// newInstanceStorageServer serves GET / with the given meta_folder for
// bucket (empty metaFolder means the entry declares none).
func newInstanceStorageServer(t *testing.T, bucket, metaFolder string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		s3 := map[string]interface{}{"bucket": bucket}
		if metaFolder != "" {
			s3["meta_folder"] = metaFolder
		}
		resp := map[string]interface{}{
			"storage": map[string]interface{}{
				"clients": []map[string]interface{}{
					{"s3": s3},
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
}

// newPublishAPIServer serves POST /publish with a successful single-target
// result and records whether it was called and with what body.
func newPublishAPIServer(t *testing.T, gotBody *[]byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/publish" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		body := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(body)
		*gotBody = body
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","results":[{"url":"https://adb.example.com/v1/b","status":"ok"}]}`))
	}))
}

func writeArtifactdbDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestDoPublishInstance_VersionRootWhenNoMetaFolder(t *testing.T) {
	uploadRoot := t.TempDir()
	t.Setenv("S5CMD_BIN", newFakeS5cmd(t, uploadRoot, ""))

	instance := newInstanceStorageServer(t, "bucket", "")
	defer instance.Close()

	var gotBody []byte
	api := newPublishAPIServer(t, &gotBody)
	defer api.Close()

	artifactdbDir := writeArtifactdbDir(t, map[string]string{
		"dataset.json": `{"a":1}`,
		"sub/a.json":   `{"b":2}`,
	})

	result, err := doPublishInstance(instancePublishArgs{
		s3url:         "s3://bucket/DS1",
		artifactdbDir: artifactdbDir,
		instanceURL:   instance.URL,
		atlasURL:      "https://atlas.example.com/v1/a",
		projectID:     "DS1",
		refName:       "1",
		apiURL:        api.URL,
	})
	if err != nil {
		t.Fatalf("doPublishInstance() error = %v", err)
	}
	if !result.Published {
		t.Errorf("Published = false, want true")
	}
	if result.S3Location != "s3://bucket/DS1/1/" {
		t.Errorf("S3Location = %q, want %q", result.S3Location, "s3://bucket/DS1/1/")
	}

	if !uploadedExists(uploadRoot, "s3://bucket/DS1/1/dataset.json") {
		t.Error("dataset.json not uploaded to the version root")
	}
	if !uploadedExists(uploadRoot, "s3://bucket/DS1/1/sub/a.json") {
		t.Error("sub/a.json not uploaded to the version root")
	}
	if uploadedExists(uploadRoot, "s3://bucket/DS1/1/.artifactdb/dataset.json") {
		t.Error(".artifactdb/dataset.json should not exist (no meta_folder)")
	}
	if !uploadedExists(uploadRoot, "s3://bucket/DS1/1/..complete") {
		t.Error("..complete marker not written")
	}
}

func TestDoPublishInstance_MetaFolderDestination(t *testing.T) {
	uploadRoot := t.TempDir()
	t.Setenv("S5CMD_BIN", newFakeS5cmd(t, uploadRoot, ""))

	instance := newInstanceStorageServer(t, "bucket", ".artifactdb")
	defer instance.Close()

	var gotBody []byte
	api := newPublishAPIServer(t, &gotBody)
	defer api.Close()

	artifactdbDir := writeArtifactdbDir(t, map[string]string{"dataset.json": `{}`})

	result, err := doPublishInstance(instancePublishArgs{
		s3url:         "s3://bucket/DS1",
		artifactdbDir: artifactdbDir,
		instanceURL:   instance.URL,
		projectID:     "DS1",
		refName:       "1",
		apiURL:        api.URL,
	})
	if err != nil {
		t.Fatalf("doPublishInstance() error = %v", err)
	}
	if result.S3Location != "s3://bucket/DS1/1/.artifactdb/" {
		t.Errorf("S3Location = %q, want %q", result.S3Location, "s3://bucket/DS1/1/.artifactdb/")
	}
	if !uploadedExists(uploadRoot, "s3://bucket/DS1/1/.artifactdb/dataset.json") {
		t.Error("dataset.json not uploaded under .artifactdb/")
	}
	if !uploadedExists(uploadRoot, "s3://bucket/DS1/1/..complete") {
		t.Error("..complete marker not written at the version root")
	}
}

func TestDoPublishInstance_MarkerWrittenLast(t *testing.T) {
	uploadRoot := t.TempDir()
	t.Setenv("S5CMD_BIN", newFakeS5cmd(t, uploadRoot, ""))

	instance := newInstanceStorageServer(t, "bucket", "")
	defer instance.Close()

	var gotBody []byte
	api := newPublishAPIServer(t, &gotBody)
	defer api.Close()

	artifactdbDir := writeArtifactdbDir(t, map[string]string{"dataset.json": `{}`})

	if _, err := doPublishInstance(instancePublishArgs{
		s3url:         "s3://bucket/DS1",
		artifactdbDir: artifactdbDir,
		instanceURL:   instance.URL,
		projectID:     "DS1",
		refName:       "1",
		apiURL:        api.URL,
	}); err != nil {
		t.Fatalf("doPublishInstance() error = %v", err)
	}

	log := trimTrailingPublishingCleanup(uploadedLog(t, uploadRoot))
	if len(log) == 0 {
		t.Fatal("expected uploads, got none")
	}
	last := log[len(log)-1]
	if !strings.HasPrefix(last, "cp ") || !strings.Contains(last, "..complete") {
		t.Errorf("last write = %q, want it to be the ..complete marker", last)
	}
}

func TestDoPublishInstance_NoMarkerOnFailedUpload(t *testing.T) {
	uploadRoot := t.TempDir()
	t.Setenv("S5CMD_BIN", newFakeS5cmd(t, uploadRoot, "DS1/1/dataset.json"))

	instance := newInstanceStorageServer(t, "bucket", "")
	defer instance.Close()

	apiCalled := false
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiCalled = true
	}))
	defer api.Close()

	artifactdbDir := writeArtifactdbDir(t, map[string]string{"dataset.json": `{}`})

	_, err := doPublishInstance(instancePublishArgs{
		s3url:         "s3://bucket/DS1",
		artifactdbDir: artifactdbDir,
		instanceURL:   instance.URL,
		projectID:     "DS1",
		refName:       "1",
		apiURL:        api.URL,
	})
	if err == nil {
		t.Fatal("expected an error for a failed upload, got nil")
	}
	if uploadedExists(uploadRoot, "s3://bucket/DS1/1/..complete") {
		t.Error("..complete marker should not exist after a failed upload")
	}
	if apiCalled {
		t.Error("the ExoHub API should not be called when the upload failed")
	}
}

func TestDoPublishInstance_PrefixGuardRefusalWritesNothing(t *testing.T) {
	uploadRoot := t.TempDir()
	t.Setenv("S5CMD_BIN", newFakeS5cmd(t, uploadRoot, ""))

	instanceCalled := false
	instance := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		instanceCalled = true
	}))
	defer instance.Close()

	apiCalled := false
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiCalled = true
	}))
	defer api.Close()

	artifactdbDir := writeArtifactdbDir(t, map[string]string{"dataset.json": `{}`})

	tests := []struct {
		name  string
		s3url string
	}{
		{"reserved underscore project", "s3://bucket/_external"},
		{"foreign project id", "s3://bucket/OTHER"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := doPublishInstance(instancePublishArgs{
				s3url:         tt.s3url,
				artifactdbDir: artifactdbDir,
				instanceURL:   instance.URL,
				projectID:     "DS1",
				refName:       "1",
				apiURL:        api.URL,
			})
			if err == nil {
				t.Fatal("expected a prefix guard refusal, got nil")
			}
			if uploadedLog(t, uploadRoot) != nil {
				t.Error("prefix guard refusal should write nothing")
			}
			if instanceCalled || apiCalled {
				t.Error("prefix guard refusal should call neither the instance nor the API")
			}
		})
	}
}

func TestDoPublishInstance_ReservedNameInArtifactdbDirRefused(t *testing.T) {
	uploadRoot := t.TempDir()
	t.Setenv("S5CMD_BIN", newFakeS5cmd(t, uploadRoot, ""))

	instance := newInstanceStorageServer(t, "bucket", "")
	defer instance.Close()

	artifactdbDir := writeArtifactdbDir(t, map[string]string{
		"dataset.json": `{}`,
		"..complete":   `{}`,
	})

	_, err := doPublishInstance(instancePublishArgs{
		s3url:         "s3://bucket/DS1",
		artifactdbDir: artifactdbDir,
		instanceURL:   instance.URL,
		projectID:     "DS1",
		refName:       "1",
		apiURL:        "http://127.0.0.1:1",
	})
	if err == nil {
		t.Fatal("expected a refusal for a '..'-prefixed name, got nil")
	}
	if !strings.Contains(err.Error(), "..complete") {
		t.Errorf("error = %v, want it to name the offending path", err)
	}
	if uploadedLog(t, uploadRoot) != nil {
		t.Error("refusal should write nothing")
	}
}

func TestDoPublishInstance_PermissionsUploadedAtProjectLevelBeforeMarker(t *testing.T) {
	uploadRoot := t.TempDir()
	t.Setenv("S5CMD_BIN", newFakeS5cmd(t, uploadRoot, ""))

	instance := newInstanceStorageServer(t, "bucket", "")
	defer instance.Close()

	var gotBody []byte
	api := newPublishAPIServer(t, &gotBody)
	defer api.Close()

	artifactdbDir := writeArtifactdbDir(t, map[string]string{"dataset.json": `{}`})

	tmpDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmpDir, ".exohub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, ".exohub", "permissions"), []byte(
		"owners:\n  - alice\nread_access: viewers\nwrite_access: owners\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	origWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origWd)

	if _, err := doPublishInstance(instancePublishArgs{
		s3url:         "s3://bucket/DS1",
		artifactdbDir: artifactdbDir,
		instanceURL:   instance.URL,
		projectID:     "DS1",
		refName:       "2",
		apiURL:        api.URL,
	}); err != nil {
		t.Fatalf("doPublishInstance() error = %v", err)
	}

	if !uploadedExists(uploadRoot, "s3://bucket/DS1/..meta/permissions.json") {
		t.Error("permissions.json not uploaded to the project-level ..meta/ key")
	}

	raw, err := os.ReadFile(filepath.Join(uploadRoot, "bucket/DS1/..meta/permissions.json"))
	if err != nil {
		t.Fatal(err)
	}
	var perm map[string]interface{}
	if err := json.Unmarshal(raw, &perm); err != nil {
		t.Fatal(err)
	}
	if perm["scope"] != "project" {
		t.Errorf("scope = %v, want %q", perm["scope"], "project")
	}

	log := uploadedLog(t, uploadRoot)
	permIdx, markerIdx := -1, -1
	for i, line := range log {
		if strings.Contains(line, "..meta/permissions.json") {
			permIdx = i
		}
		if strings.Contains(line, "..complete") {
			markerIdx = i
		}
	}
	if permIdx == -1 || markerIdx == -1 || permIdx >= markerIdx {
		t.Errorf("permissions upload (index %d) should happen before the marker (index %d): %v", permIdx, markerIdx, log)
	}
}

func TestDoPublishInstance_NoArtifactdbDirSkipsWithoutIngest(t *testing.T) {
	uploadRoot := t.TempDir()
	t.Setenv("S5CMD_BIN", newFakeS5cmd(t, uploadRoot, ""))

	apiCalled := false
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiCalled = true
	}))
	defer api.Close()

	result, err := doPublishInstance(instancePublishArgs{
		s3url:       "s3://bucket/DS1",
		instanceURL: "http://127.0.0.1:1",
		projectID:   "DS1",
		refName:     "1",
		apiURL:      api.URL,
	})
	if err != nil {
		t.Fatalf("doPublishInstance() error = %v", err)
	}
	if result.Published {
		t.Error("Published = true, want false for a ref with no .artifactdb/")
	}
	if !result.Skipped || result.SkipReason != "no_artifactdb" {
		t.Errorf("Skipped = %v, SkipReason = %q, want skipped with reason %q", result.Skipped, result.SkipReason, "no_artifactdb")
	}
	if apiCalled {
		t.Error("the ExoHub API should not be called when there is nothing to publish")
	}
}

func TestDoPublishInstance_NeverCallsInstanceDirectlyForIngest(t *testing.T) {
	uploadRoot := t.TempDir()
	t.Setenv("S5CMD_BIN", newFakeS5cmd(t, uploadRoot, ""))

	instanceIngestCalled := false
	instance := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"storage":{"clients":[{"s3":{"bucket":"bucket"}}]}}`))
			return
		}
		instanceIngestCalled = true
		w.WriteHeader(http.StatusNotFound)
	}))
	defer instance.Close()

	var gotBody []byte
	api := newPublishAPIServer(t, &gotBody)
	defer api.Close()

	artifactdbDir := writeArtifactdbDir(t, map[string]string{"dataset.json": `{}`})

	if _, err := doPublishInstance(instancePublishArgs{
		s3url:         "s3://bucket/DS1",
		artifactdbDir: artifactdbDir,
		instanceURL:   instance.URL,
		projectID:     "DS1",
		refName:       "1",
		apiURL:        api.URL,
	}); err != nil {
		t.Fatalf("doPublishInstance() error = %v", err)
	}
	if instanceIngestCalled {
		t.Error("exo must never call the instance directly for ingest")
	}
}

func TestDoPublishInstance_BranchRepublishDeletesRemovedFile(t *testing.T) {
	uploadRoot := t.TempDir()
	t.Setenv("S5CMD_BIN", newFakeS5cmd(t, uploadRoot, ""))

	instance := newInstanceStorageServer(t, "bucket", "")
	defer instance.Close()

	var gotBody []byte
	api := newPublishAPIServer(t, &gotBody)
	defer api.Close()

	args := instancePublishArgs{
		s3url:       "s3://bucket/DS1",
		instanceURL: instance.URL,
		projectID:   "DS1",
		refName:     "main",
		refKind:     "branch",
		apiURL:      api.URL,
	}

	firstDir := writeArtifactdbDir(t, map[string]string{
		"dataset.json": `{"a":1}`,
		"sub/a.json":   `{"b":2}`,
	})
	args.artifactdbDir = firstDir
	if _, err := doPublishInstance(args); err != nil {
		t.Fatalf("first doPublishInstance() error = %v", err)
	}
	if !uploadedExists(uploadRoot, "s3://bucket/DS1/main/sub/a.json") {
		t.Fatal("sub/a.json not uploaded by the first publish")
	}

	// Re-publish with sub/a.json removed.
	secondDir := writeArtifactdbDir(t, map[string]string{"dataset.json": `{"a":1}`})
	args.artifactdbDir = secondDir
	if _, err := doPublishInstance(args); err != nil {
		t.Fatalf("second doPublishInstance() error = %v", err)
	}

	if uploadedExists(uploadRoot, "s3://bucket/DS1/main/sub/a.json") {
		t.Error("sub/a.json should have been deleted by the branch re-publish")
	}
	if !uploadedExists(uploadRoot, "s3://bucket/DS1/main/dataset.json") {
		t.Error("dataset.json should still exist")
	}
	if !uploadedExists(uploadRoot, "s3://bucket/DS1/main/..complete") {
		t.Error("..complete marker should exist after the re-publish")
	}
}

// TestDoPublishInstance_ExcludeGlobNotUploaded verifies that a file matching
// a --exclude glob is never uploaded, and that it's absent from the
// "..complete" marker's Files/Objects (exo-cli#202).
func TestDoPublishInstance_ExcludeGlobNotUploaded(t *testing.T) {
	uploadRoot := t.TempDir()
	t.Setenv("S5CMD_BIN", newFakeS5cmd(t, uploadRoot, ""))

	instance := newInstanceStorageServer(t, "bucket", "")
	defer instance.Close()

	var gotBody []byte
	api := newPublishAPIServer(t, &gotBody)
	defer api.Close()

	artifactdbDir := writeArtifactdbDir(t, map[string]string{
		"dataset.json":        `{"a":1}`,
		"dataset_merged.json": `{"b":2}`,
	})

	result, err := doPublishInstance(instancePublishArgs{
		s3url:         "s3://bucket/DS1",
		artifactdbDir: artifactdbDir,
		instanceURL:   instance.URL,
		projectID:     "DS1",
		refName:       "1",
		apiURL:        api.URL,
		excludes:      []string{"dataset_merged.json*"},
	})
	if err != nil {
		t.Fatalf("doPublishInstance() error = %v", err)
	}
	if !result.Published {
		t.Errorf("Published = false, want true")
	}

	if !uploadedExists(uploadRoot, "s3://bucket/DS1/1/dataset.json") {
		t.Error("dataset.json should have been uploaded")
	}
	if uploadedExists(uploadRoot, "s3://bucket/DS1/1/dataset_merged.json") {
		t.Error("dataset_merged.json matches the exclude glob and should not have been uploaded")
	}

	marker, err := readCompletionMarker(newFakeS5cmd(t, uploadRoot, ""), "s3://bucket/DS1/1/..complete")
	if err != nil {
		t.Fatalf("readCompletionMarker() error = %v", err)
	}
	if marker == nil {
		t.Fatal("marker is nil")
	}
	if marker.Objects != 1 {
		t.Errorf("marker.Objects = %d, want 1", marker.Objects)
	}
	for _, f := range marker.Files {
		if f == "dataset_merged.json" {
			t.Errorf("marker.Files should not list the excluded file, got %v", marker.Files)
		}
	}
	if len(marker.Files) != 1 || marker.Files[0] != "dataset.json" {
		t.Errorf("marker.Files = %v, want [dataset.json]", marker.Files)
	}
}

// TestDoPublishInstance_ExcludeGlobSegmentLocalNotRecursive verifies the
// exact matchAnnexGlob semantics the reviewer called out on exo-cli#202: a
// single "*" stays within one path segment, so "meta/*" matches
// "meta/x.json" but not "meta/a/b.json" (a deeper path under meta/) — that
// deeper file is uploaded and listed, while "meta/x.json" is excluded. Also
// asserts that no --exclude argument ever reaches the s5cmd binary itself
// (exo-cli#202 review item 2): exclusion must be enforced by staging the
// kept files, never by delegating to s5cmd's own glob dialect.
func TestDoPublishInstance_ExcludeGlobSegmentLocalNotRecursive(t *testing.T) {
	uploadRoot := t.TempDir()
	t.Setenv("S5CMD_BIN", newFakeS5cmd(t, uploadRoot, ""))

	instance := newInstanceStorageServer(t, "bucket", "")
	defer instance.Close()

	var gotBody []byte
	api := newPublishAPIServer(t, &gotBody)
	defer api.Close()

	artifactdbDir := writeArtifactdbDir(t, map[string]string{
		"meta/x.json":   `{"a":1}`,
		"meta/a/b.json": `{"b":2}`,
	})

	result, err := doPublishInstance(instancePublishArgs{
		s3url:         "s3://bucket/DS1",
		artifactdbDir: artifactdbDir,
		instanceURL:   instance.URL,
		projectID:     "DS1",
		refName:       "1",
		apiURL:        api.URL,
		excludes:      []string{"meta/*"},
	})
	if err != nil {
		t.Fatalf("doPublishInstance() error = %v", err)
	}
	if !result.Published {
		t.Errorf("Published = false, want true")
	}

	if uploadedExists(uploadRoot, "s3://bucket/DS1/1/meta/x.json") {
		t.Error("meta/x.json matches meta/* and should not have been uploaded")
	}
	if !uploadedExists(uploadRoot, "s3://bucket/DS1/1/meta/a/b.json") {
		t.Error("meta/a/b.json does not match meta/* (a single * stays within one segment) and should have been uploaded")
	}

	marker, err := readCompletionMarker(newFakeS5cmd(t, uploadRoot, ""), "s3://bucket/DS1/1/..complete")
	if err != nil {
		t.Fatalf("readCompletionMarker() error = %v", err)
	}
	if marker == nil {
		t.Fatal("marker is nil")
	}
	if len(marker.Files) != 1 || marker.Files[0] != "meta/a/b.json" {
		t.Errorf("marker.Files = %v, want [meta/a/b.json]", marker.Files)
	}

	log := uploadedLog(t, uploadRoot)
	for _, line := range log {
		if strings.HasPrefix(line, "sync-argv") && strings.Contains(line, "--exclude") {
			t.Errorf("s5cmd must never receive --exclude (exclusion is enforced by staging), got: %q", line)
		}
	}
}

// TestDoPublishInstance_ExcludeGlobNoOpWithoutExclude is the mutation-check
// counterpart of TestDoPublishInstance_ExcludeGlobNotUploaded: passing no
// --exclude at all leaves the upload and marker byte-identical to today's
// behavior.
func TestDoPublishInstance_ExcludeGlobNoOpWithoutExclude(t *testing.T) {
	uploadRoot := t.TempDir()
	t.Setenv("S5CMD_BIN", newFakeS5cmd(t, uploadRoot, ""))

	instance := newInstanceStorageServer(t, "bucket", "")
	defer instance.Close()

	var gotBody []byte
	api := newPublishAPIServer(t, &gotBody)
	defer api.Close()

	artifactdbDir := writeArtifactdbDir(t, map[string]string{
		"dataset.json":        `{"a":1}`,
		"dataset_merged.json": `{"b":2}`,
	})

	result, err := doPublishInstance(instancePublishArgs{
		s3url:         "s3://bucket/DS1",
		artifactdbDir: artifactdbDir,
		instanceURL:   instance.URL,
		projectID:     "DS1",
		refName:       "1",
		apiURL:        api.URL,
	})
	if err != nil {
		t.Fatalf("doPublishInstance() error = %v", err)
	}
	if !result.Published {
		t.Errorf("Published = false, want true")
	}
	if !uploadedExists(uploadRoot, "s3://bucket/DS1/1/dataset.json") {
		t.Error("dataset.json should have been uploaded")
	}
	if !uploadedExists(uploadRoot, "s3://bucket/DS1/1/dataset_merged.json") {
		t.Error("dataset_merged.json should have been uploaded (no exclude configured)")
	}
}

// TestDoPublishInstance_ExcludeGlobSelfHealsPreviouslyPublishedFile verifies
// the self-heal: a branch re-publish that newly adds an exclude glob
// matching an already-published file deletes that stale key from the
// version prefix (and nothing outside it), before "..complete" is
// rewritten.
func TestDoPublishInstance_ExcludeGlobSelfHealsPreviouslyPublishedFile(t *testing.T) {
	uploadRoot := t.TempDir()
	t.Setenv("S5CMD_BIN", newFakeS5cmd(t, uploadRoot, ""))

	instance := newInstanceStorageServer(t, "bucket", "")
	defer instance.Close()

	var gotBody []byte
	api := newPublishAPIServer(t, &gotBody)
	defer api.Close()

	args := instancePublishArgs{
		s3url:       "s3://bucket/DS1",
		instanceURL: instance.URL,
		projectID:   "DS1",
		refName:     "main",
		refKind:     "branch",
		apiURL:      api.URL,
	}

	args.artifactdbDir = writeArtifactdbDir(t, map[string]string{
		"dataset.json":        `{"a":1}`,
		"dataset_merged.json": `{"b":2}`,
	})
	if _, err := doPublishInstance(args); err != nil {
		t.Fatalf("first doPublishInstance() error = %v", err)
	}
	if !uploadedExists(uploadRoot, "s3://bucket/DS1/main/dataset_merged.json") {
		t.Fatal("dataset_merged.json not uploaded by the first publish")
	}

	// A sibling object outside the version prefix, which the self-heal
	// must never touch.
	outsidePath := filepath.Join(uploadRoot, "bucket", "DS1", "other", "dataset_merged.json")
	if err := os.MkdirAll(filepath.Dir(outsidePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outsidePath, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Re-publish the same files, now excluding dataset_merged.json.
	args.excludes = []string{"dataset_merged.json*"}
	if _, err := doPublishInstance(args); err != nil {
		t.Fatalf("second doPublishInstance() error = %v", err)
	}

	if uploadedExists(uploadRoot, "s3://bucket/DS1/main/dataset_merged.json") {
		t.Error("dataset_merged.json should have been self-healed (deleted) from the version prefix")
	}
	if !uploadedExists(uploadRoot, "s3://bucket/DS1/main/dataset.json") {
		t.Error("dataset.json should still exist")
	}
	if _, err := os.Stat(outsidePath); err != nil {
		t.Errorf("object outside the version prefix should not have been touched: %v", err)
	}
	if !uploadedExists(uploadRoot, "s3://bucket/DS1/main/..complete") {
		t.Error("..complete marker should exist after the re-publish")
	}

	log := uploadedLog(t, uploadRoot)
	found := false
	for _, line := range log {
		if line == "rm --raw s3://bucket/DS1/main/dataset_merged.json" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an exact rm --raw of the excluded key, got log: %v", log)
	}
}

// TestDoPublishInstance_ExcludeGlobSelfHealsOnTagRepublish verifies that
// the exclude self-heal also runs for a tag re-publish of the same commit
// (which passes tag immutability), unlike the rename/deletion stale-cleanup
// which is gated to branches.
func TestDoPublishInstance_ExcludeGlobSelfHealsOnTagRepublish(t *testing.T) {
	uploadRoot := t.TempDir()
	t.Setenv("S5CMD_BIN", newFakeS5cmd(t, uploadRoot, ""))

	instance := newInstanceStorageServer(t, "bucket", "")
	defer instance.Close()

	var gotBody []byte
	api := newPublishAPIServer(t, &gotBody)
	defer api.Close()

	args := instancePublishArgs{
		s3url:       "s3://bucket/DS1",
		instanceURL: instance.URL,
		projectID:   "DS1",
		refName:     "v1",
		refKind:     "tag",
		commit:      "abc123",
		apiURL:      api.URL,
	}

	args.artifactdbDir = writeArtifactdbDir(t, map[string]string{
		"dataset.json":        `{"a":1}`,
		"dataset_merged.json": `{"b":2}`,
	})
	if _, err := doPublishInstance(args); err != nil {
		t.Fatalf("first doPublishInstance() error = %v", err)
	}
	if !uploadedExists(uploadRoot, "s3://bucket/DS1/v1/dataset_merged.json") {
		t.Fatal("dataset_merged.json not uploaded by the first publish")
	}

	// Re-publish the same tag at the same commit, now excluding
	// dataset_merged.json — passes tag immutability (same commit).
	args.excludes = []string{"dataset_merged.json*"}
	if _, err := doPublishInstance(args); err != nil {
		t.Fatalf("second doPublishInstance() (tag re-publish) error = %v", err)
	}

	if uploadedExists(uploadRoot, "s3://bucket/DS1/v1/dataset_merged.json") {
		t.Error("dataset_merged.json should have been self-healed on the tag re-publish")
	}
	if !uploadedExists(uploadRoot, "s3://bucket/DS1/v1/dataset.json") {
		t.Error("dataset.json should still exist")
	}
}

// TestDoPublishInstance_BranchRepublishDeletesWildcardNamedFileWithRaw
// verifies that a stale metadata file whose name contains shell/s5cmd
// glob characters (*, ?, [) is deleted with --raw and that the delete
// targets exactly that key — s5cmd rm expands wildcards unless --raw is
// passed, so without it this delete could match unrelated objects under
// dst instead of just the one stale key.
func TestDoPublishInstance_BranchRepublishDeletesWildcardNamedFileWithRaw(t *testing.T) {
	uploadRoot := t.TempDir()
	t.Setenv("S5CMD_BIN", newFakeS5cmd(t, uploadRoot, ""))

	instance := newInstanceStorageServer(t, "bucket", "")
	defer instance.Close()

	var gotBody []byte
	api := newPublishAPIServer(t, &gotBody)
	defer api.Close()

	args := instancePublishArgs{
		s3url:       "s3://bucket/DS1",
		instanceURL: instance.URL,
		projectID:   "DS1",
		refName:     "main",
		refKind:     "branch",
		apiURL:      api.URL,
	}

	const weirdName = "run[1]*?.json"
	args.artifactdbDir = writeArtifactdbDir(t, map[string]string{
		"dataset.json": `{"a":1}`,
		weirdName:      `{"b":2}`,
	})
	if _, err := doPublishInstance(args); err != nil {
		t.Fatalf("first doPublishInstance() error = %v", err)
	}
	if !uploadedExists(uploadRoot, "s3://bucket/DS1/main/"+weirdName) {
		t.Fatalf("%s not uploaded by the first publish", weirdName)
	}

	// A decoy object whose name would be matched by a wildcard expansion of
	// "run[1]*?.json" (e.g. "run1X.json" matches run[1]*?.json un-raw), so a
	// missing --raw would delete it too.
	decoyPath := filepath.Join(uploadRoot, "bucket", "DS1", "main", "run1X.json")
	if err := os.WriteFile(decoyPath, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Re-publish with the wildcard-named file removed.
	args.artifactdbDir = writeArtifactdbDir(t, map[string]string{"dataset.json": `{"a":1}`})
	if _, err := doPublishInstance(args); err != nil {
		t.Fatalf("second doPublishInstance() error = %v", err)
	}

	if uploadedExists(uploadRoot, "s3://bucket/DS1/main/"+weirdName) {
		t.Errorf("%s should have been deleted by the branch re-publish", weirdName)
	}
	if _, err := os.Stat(decoyPath); err != nil {
		t.Errorf("decoy object run1X.json should not have been touched: %v", err)
	}

	want := "rm --raw s3://bucket/DS1/main/" + weirdName
	found := false
	for _, line := range uploadedLog(t, uploadRoot) {
		if strings.HasPrefix(line, "rm ") && strings.Contains(line, weirdName) {
			if line != want {
				t.Errorf("rm argv = %q, want exactly %q", line, want)
			}
			found = true
		}
	}
	if !found {
		t.Errorf("expected an rm for %q, got log: %v", weirdName, uploadedLog(t, uploadRoot))
	}
}

func TestDoPublishInstance_BranchRepublishNeverDeletesReservedOrUnlistedObjects(t *testing.T) {
	uploadRoot := t.TempDir()
	t.Setenv("S5CMD_BIN", newFakeS5cmd(t, uploadRoot, ""))

	instance := newInstanceStorageServer(t, "bucket", "")
	defer instance.Close()

	var gotBody []byte
	api := newPublishAPIServer(t, &gotBody)
	defer api.Close()

	args := instancePublishArgs{
		s3url:       "s3://bucket/DS1",
		instanceURL: instance.URL,
		projectID:   "DS1",
		refName:     "main",
		refKind:     "branch",
		apiURL:      api.URL,
	}

	args.artifactdbDir = writeArtifactdbDir(t, map[string]string{
		"dataset.json": `{"a":1}`,
		"sub/a.json":   `{"b":2}`,
	})
	if _, err := doPublishInstance(args); err != nil {
		t.Fatalf("first doPublishInstance() error = %v", err)
	}

	// An object the instance side wrote, which exo never uploaded and must
	// never delete, in the no-meta_folder layout where dst is the version
	// root itself (the same prefix as ..complete and dataset.json).
	if err := os.MkdirAll(filepath.Join(uploadRoot, "bucket", "DS1", "main"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(uploadRoot, "bucket", "DS1", "main", "..project-source.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Re-publish with sub/a.json removed.
	args.artifactdbDir = writeArtifactdbDir(t, map[string]string{"dataset.json": `{"a":1}`})
	if _, err := doPublishInstance(args); err != nil {
		t.Fatalf("second doPublishInstance() error = %v", err)
	}

	if !uploadedExists(uploadRoot, "s3://bucket/DS1/main/..project-source.json") {
		t.Error("..project-source.json (not uploaded by exo) must never be deleted")
	}
	if !uploadedExists(uploadRoot, "s3://bucket/DS1/main/..complete") {
		t.Error("..complete marker should exist after the re-publish")
	}

	log := uploadedLog(t, uploadRoot)
	rmCount := 0
	for _, line := range log {
		if strings.HasPrefix(line, "rm ") {
			rmCount++
			if strings.Contains(line, "..project-source.json") {
				t.Errorf("unlisted object deleted: %q", line)
			}
		}
	}
	// Four deletions expected across both publishes: each publish's own
	// ..publishing cleanup (2), plus the second publish's previous ..complete
	// pre-delete and its removed sub/a.json (stale-cleanup) — never
	// ..project-source.json.
	if rmCount != 4 {
		t.Errorf("rm count = %d, want 4 (2x ..publishing cleanup + previous marker + removed file): %v", rmCount, log)
	}
}

func TestDoPublishInstance_MarkerComesLastOnBranchRepublish(t *testing.T) {
	uploadRoot := t.TempDir()
	t.Setenv("S5CMD_BIN", newFakeS5cmd(t, uploadRoot, ""))

	instance := newInstanceStorageServer(t, "bucket", "")
	defer instance.Close()

	var gotBody []byte
	api := newPublishAPIServer(t, &gotBody)
	defer api.Close()

	args := instancePublishArgs{
		s3url:       "s3://bucket/DS1",
		instanceURL: instance.URL,
		projectID:   "DS1",
		refName:     "main",
		refKind:     "branch",
		apiURL:      api.URL,
	}

	args.artifactdbDir = writeArtifactdbDir(t, map[string]string{
		"dataset.json": `{"a":1}`,
		"sub/a.json":   `{"b":2}`,
	})
	if _, err := doPublishInstance(args); err != nil {
		t.Fatalf("first doPublishInstance() error = %v", err)
	}

	args.artifactdbDir = writeArtifactdbDir(t, map[string]string{"dataset.json": `{"a":1}`})
	if _, err := doPublishInstance(args); err != nil {
		t.Fatalf("second doPublishInstance() error = %v", err)
	}

	fullLog := uploadedLog(t, uploadRoot)
	log := trimTrailingPublishingCleanup(fullLog)
	if len(log) == 0 {
		t.Fatal("expected uploads, got none")
	}
	last := log[len(log)-1]
	if !strings.HasPrefix(last, "cp ") || !strings.Contains(last, "..complete") {
		t.Errorf("last write = %q, want it to be the ..complete marker", last)
	}
	// The ..complete delete (pre-sync) must come before any "cp .../..complete"
	// write, and no rm of ..complete after that write.
	markerDeleteIdx, markerWriteIdx := -1, -1
	for i, line := range log {
		if strings.Contains(line, "..complete") {
			if strings.HasPrefix(line, "rm ") {
				markerDeleteIdx = i
			} else {
				markerWriteIdx = i
			}
		}
	}
	if markerDeleteIdx == -1 || markerWriteIdx == -1 || markerDeleteIdx >= markerWriteIdx {
		t.Errorf("expected the previous marker's delete (index %d) before the new marker's write (index %d): %v", markerDeleteIdx, markerWriteIdx, log)
	}
	// ..publishing must be written before the previous ..complete is
	// deleted, and removed only after the new ..complete is written.
	publishingWriteIdx, publishingDeleteIdx := -1, -1
	for i, line := range fullLog {
		if strings.Contains(line, "..publishing") {
			if strings.HasPrefix(line, "rm ") {
				publishingDeleteIdx = i
			} else {
				publishingWriteIdx = i
			}
		}
	}
	if publishingWriteIdx == -1 || publishingWriteIdx >= markerDeleteIdx {
		t.Errorf("expected ..publishing written (index %d) before the previous marker's delete (index %d): %v", publishingWriteIdx, markerDeleteIdx, fullLog)
	}
	if publishingDeleteIdx == -1 || publishingDeleteIdx <= markerWriteIdx {
		t.Errorf("expected ..publishing deleted (index %d) after the new marker's write (index %d): %v", publishingDeleteIdx, markerWriteIdx, fullLog)
	}
}

func TestDoPublishInstance_LegacyMarkerWithoutFilesDeletesNothing(t *testing.T) {
	uploadRoot := t.TempDir()
	t.Setenv("S5CMD_BIN", newFakeS5cmd(t, uploadRoot, ""))

	instance := newInstanceStorageServer(t, "bucket", "")
	defer instance.Close()

	var gotBody []byte
	api := newPublishAPIServer(t, &gotBody)
	defer api.Close()

	// Simulate an older exo's marker: no "files" key at all.
	markerDir := filepath.Join(uploadRoot, "bucket", "DS1", "main")
	if err := os.MkdirAll(markerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(markerDir, "dataset.json"), []byte(`{"a":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(markerDir, "sub_a.json"), []byte(`{"b":2}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(markerDir, "..complete"), []byte(`{"version":"main","commit":"abc","objects":2}`), 0o644); err != nil {
		t.Fatal(err)
	}

	args := instancePublishArgs{
		s3url:         "s3://bucket/DS1",
		artifactdbDir: writeArtifactdbDir(t, map[string]string{"dataset.json": `{"a":1}`}),
		instanceURL:   instance.URL,
		projectID:     "DS1",
		refName:       "main",
		refKind:       "branch",
		apiURL:        api.URL,
	}
	if _, err := doPublishInstance(args); err != nil {
		t.Fatalf("doPublishInstance() error = %v", err)
	}

	// sub_a.json isn't one of the new files, but since the previous marker
	// had no "files" list, nothing should have been deleted (other than the
	// previous ..complete, which is always deleted pre-sync).
	if !uploadedExists(uploadRoot, "s3://bucket/DS1/main/sub_a.json") {
		t.Error("sub_a.json should not have been deleted: the previous marker predates per-file tracking")
	}
}

// TestDoPublishInstance_PublishingFallbackRefusesDifferentRepoAfterFailedSync
// covers the crash window: a branch re-publish deletes the previous
// ..complete BEFORE syncing, so a sync failure between that delete and the
// new ..complete's write leaves ..complete absent. Without the
// ..publishing fallback, the source-repo guard would then see "no record"
// and let a publish from ANY repository through; with it, the guard still
// refuses a different repository.
func TestDoPublishInstance_PublishingFallbackRefusesDifferentRepoAfterFailedSync(t *testing.T) {
	uploadRoot := t.TempDir()

	instance := newInstanceStorageServer(t, "bucket", "")
	defer instance.Close()

	var gotBody []byte
	api := newPublishAPIServer(t, &gotBody)
	defer api.Close()

	args := instancePublishArgs{
		s3url:       "s3://bucket/DS1",
		instanceURL: instance.URL,
		projectID:   "DS1",
		refName:     "main",
		refKind:     "branch",
		repoURL:     "https://example.com/group/repo-a.git",
		apiURL:      api.URL,
	}

	// First publish: succeeds, from repo-a.
	t.Setenv("S5CMD_BIN", newFakeS5cmd(t, uploadRoot, ""))
	args.artifactdbDir = writeArtifactdbDir(t, map[string]string{"a.json": `{}`})
	if _, err := doPublishInstance(args); err != nil {
		t.Fatalf("first doPublishInstance() error = %v", err)
	}

	// Second publish, same repo: the sync itself fails. ..complete was
	// already deleted (pre-sync) by the time this happens.
	t.Setenv("S5CMD_BIN", newFakeS5cmd(t, uploadRoot, "c.json"))
	args.artifactdbDir = writeArtifactdbDir(t, map[string]string{"a.json": `{}`, "c.json": `{}`})
	if _, err := doPublishInstance(args); err == nil {
		t.Fatal("expected the second publish's sync to fail")
	}
	if uploadedExists(uploadRoot, "s3://bucket/DS1/main/..complete") {
		t.Fatal("..complete should have been deleted before the failed sync")
	}
	if !uploadedExists(uploadRoot, "s3://bucket/DS1/main/..publishing") {
		t.Fatal("..publishing should exist after the failed sync")
	}

	// Third publish, from a DIFFERENT repository: refused, even though
	// ..complete doesn't exist, because ..publishing still carries repo-a.
	t.Setenv("S5CMD_BIN", newFakeS5cmd(t, uploadRoot, ""))
	args.repoURL = "https://example.com/group/repo-b.git"
	args.artifactdbDir = writeArtifactdbDir(t, map[string]string{"a.json": `{}`})
	_, err := doPublishInstance(args)
	if err == nil {
		t.Fatal("expected a refusal for a different source repository, got nil")
	}
	if !strings.Contains(err.Error(), "repo-a") || !strings.Contains(err.Error(), "repo-b") {
		t.Errorf("error = %v, want it to name both repositories", err)
	}
}

// TestDoPublishInstance_PublishingFallbackCleansStaleFilesFromBothTrees
// covers the other half of the same crash window: after ..complete is
// deleted and the sync fails, the PREVIOUS marker's files list would
// otherwise be lost, so a stale file from either the original tree or the
// failed attempt's tree would never be cleaned up. The ..publishing
// fallback (a union of both) fixes that, and a stale key that was only
// ever recorded (never actually uploaded, because the sync that would
// have created it failed) must not turn a successful cleanup into a
// failure.
func TestDoPublishInstance_PublishingFallbackCleansStaleFilesFromBothTrees(t *testing.T) {
	uploadRoot := t.TempDir()

	instance := newInstanceStorageServer(t, "bucket", "")
	defer instance.Close()

	var gotBody []byte
	api := newPublishAPIServer(t, &gotBody)
	defer api.Close()

	args := instancePublishArgs{
		s3url:       "s3://bucket/DS1",
		instanceURL: instance.URL,
		projectID:   "DS1",
		refName:     "main",
		refKind:     "branch",
		apiURL:      api.URL,
	}

	// First publish: a.json and b.json, succeeds.
	t.Setenv("S5CMD_BIN", newFakeS5cmd(t, uploadRoot, ""))
	args.artifactdbDir = writeArtifactdbDir(t, map[string]string{"a.json": `{}`, "b.json": `{}`})
	if _, err := doPublishInstance(args); err != nil {
		t.Fatalf("first doPublishInstance() error = %v", err)
	}
	if !uploadedExists(uploadRoot, "s3://bucket/DS1/main/b.json") {
		t.Fatal("b.json not uploaded by the first publish")
	}

	// Second publish: drops b.json, adds c.json, but the sync fails before
	// c.json is ever actually written — yet ..publishing (written before
	// the sync, from the local tree) still records it.
	t.Setenv("S5CMD_BIN", newFakeS5cmd(t, uploadRoot, "c.json"))
	args.artifactdbDir = writeArtifactdbDir(t, map[string]string{"a.json": `{}`, "c.json": `{}`})
	if _, err := doPublishInstance(args); err == nil {
		t.Fatal("expected the second publish's sync to fail")
	}
	if uploadedExists(uploadRoot, "s3://bucket/DS1/main/c.json") {
		t.Fatal("c.json should never have actually been uploaded (the sync failed first)")
	}

	// Third publish: back to just a.json, succeeds. Must clean up b.json
	// (from the first tree) AND c.json (from the second, failed tree's
	// ..publishing record, even though it was never actually uploaded).
	t.Setenv("S5CMD_BIN", newFakeS5cmd(t, uploadRoot, ""))
	args.artifactdbDir = writeArtifactdbDir(t, map[string]string{"a.json": `{}`})
	if _, err := doPublishInstance(args); err != nil {
		t.Fatalf("third doPublishInstance() error = %v", err)
	}

	if !uploadedExists(uploadRoot, "s3://bucket/DS1/main/a.json") {
		t.Error("a.json should still exist")
	}
	if uploadedExists(uploadRoot, "s3://bucket/DS1/main/b.json") {
		t.Error("b.json (stale from the first tree) should have been deleted")
	}
	if uploadedExists(uploadRoot, "s3://bucket/DS1/main/c.json") {
		t.Error("c.json (from the failed attempt's tree) should not exist")
	}
	if !uploadedExists(uploadRoot, "s3://bucket/DS1/main/..complete") {
		t.Error("..complete should exist after the successful third publish")
	}
	if uploadedExists(uploadRoot, "s3://bucket/DS1/main/..publishing") {
		t.Error("..publishing should have been removed after the successful publish")
	}
}

func TestDoPublishInstance_TagRepublishNeverDeletes(t *testing.T) {
	uploadRoot := t.TempDir()
	t.Setenv("S5CMD_BIN", newFakeS5cmd(t, uploadRoot, ""))

	instance := newInstanceStorageServer(t, "bucket", "")
	defer instance.Close()

	var gotBody []byte
	api := newPublishAPIServer(t, &gotBody)
	defer api.Close()

	args := instancePublishArgs{
		s3url:       "s3://bucket/DS1",
		instanceURL: instance.URL,
		projectID:   "DS1",
		refName:     "1",
		refKind:     "tag",
		commit:      "abc",
		apiURL:      api.URL,
	}

	args.artifactdbDir = writeArtifactdbDir(t, map[string]string{
		"dataset.json": `{"a":1}`,
		"sub/a.json":   `{"b":2}`,
	})
	if _, err := doPublishInstance(args); err != nil {
		t.Fatalf("first doPublishInstance() error = %v", err)
	}

	// Idempotent retry with the same commit: a tag never deletes anything,
	// even if the local .artifactdb/ tree (for whatever reason) differs.
	args.artifactdbDir = writeArtifactdbDir(t, map[string]string{"dataset.json": `{"a":1}`})
	if _, err := doPublishInstance(args); err != nil {
		t.Fatalf("second doPublishInstance() error = %v", err)
	}

	if !uploadedExists(uploadRoot, "s3://bucket/DS1/1/sub/a.json") {
		t.Error("sub/a.json should not have been deleted: tag re-publish never deletes")
	}

	log := uploadedLog(t, uploadRoot)
	for _, line := range log {
		if strings.HasPrefix(line, "rm ") {
			t.Errorf("tag re-publish must never issue a delete, got %q", line)
		}
	}
}

func TestDeleteStaleInstanceObjects_GuardRejectsEscapingOrReservedKeys(t *testing.T) {
	dst := "s3://bucket/DS1/main/"
	tests := []struct {
		name string
		key  string
	}{
		{"empty key", ""},
		{"reserved dotdot-prefixed key", "..complete"},
		{"reserved dotdot-prefixed key (project-source)", "..project-source.json"},
		{"path-traversal segment", "sub/../..complete"},
		{"leading path-traversal", "../outside.json"},
		{"leading slash (absolute-looking key)", "/outside.json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// A fresh uploadRoot/s5/log per subtest: sharing one across
			// subtests would let an earlier subtest's (mutation-induced) rm
			// attempt poison a later subtest's "no rm issued" check.
			uploadRoot := t.TempDir()
			s5 := newFakeS5cmd(t, uploadRoot, "")
			err := deleteStaleInstanceObjects(s5, dst, []string{tt.key}, nil)
			// The message must name this a guard refusal ("refusing to
			// delete"), not merely any error: an s5cmd rm failure for an
			// unrelated reason (e.g. the fake backend finding dst itself
			// not a regular file) would also return a non-nil error without
			// the guard ever having run — a weaker assertion here would miss
			// a guard that silently lets the rm through to s5cmd.
			if err == nil || !strings.Contains(err.Error(), "refusing to delete") {
				t.Fatalf("deleteStaleInstanceObjects(%q) = %v, want a guard refusal error", tt.key, err)
			}
			// The fake s5cmd records every rm invocation (success or
			// failure) BEFORE attempting the delete, so this is a direct
			// check that runS5cmd("rm", ...) was never even called — not
			// just that the overall call returned an error.
			if uploadedLog(t, uploadRoot) != nil {
				t.Errorf("guard rejection for %q should never issue an rm", tt.key)
			}
		})
	}
}

func TestCheckTagImmutability_MovedTagRefused(t *testing.T) {
	uploadRoot := t.TempDir()
	s5 := newFakeS5cmd(t, uploadRoot, "")
	t.Setenv("S5CMD_BIN", s5)

	markerDir := filepath.Join(uploadRoot, "bucket", "DS1", "1")
	if err := os.MkdirAll(markerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(markerDir, "..complete"), []byte(`{"version":"1","commit":"abc"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	err := checkTagImmutability(s5, "tag", "1", "def", "s3://bucket/DS1/1/..complete", extractMarkerCommit)
	if err == nil {
		t.Fatal("expected a refusal for a moved tag, got nil")
	}
	if !strings.Contains(err.Error(), "abc") || !strings.Contains(err.Error(), "def") {
		t.Errorf("error = %v, want it to name both commits", err)
	}
}

func TestCheckTagImmutability_SameTagRetryAllowed(t *testing.T) {
	uploadRoot := t.TempDir()
	s5 := newFakeS5cmd(t, uploadRoot, "")

	markerDir := filepath.Join(uploadRoot, "bucket", "DS1", "1")
	if err := os.MkdirAll(markerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(markerDir, "..complete"), []byte(`{"version":"1","commit":"abc"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := checkTagImmutability(s5, "tag", "1", "abc", "s3://bucket/DS1/1/..complete", extractMarkerCommit); err != nil {
		t.Errorf("checkTagImmutability() = %v, want nil for an idempotent retry", err)
	}
}

func TestCheckTagImmutability_BranchNeverChecks(t *testing.T) {
	uploadRoot := t.TempDir()
	s5 := newFakeS5cmd(t, uploadRoot, "")

	markerDir := filepath.Join(uploadRoot, "bucket", "DS1", "main")
	if err := os.MkdirAll(markerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(markerDir, "..complete"), []byte(`{"version":"main","commit":"abc"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := checkTagImmutability(s5, "branch", "main", "def", "s3://bucket/DS1/main/..complete", extractMarkerCommit); err != nil {
		t.Errorf("checkTagImmutability() = %v, want nil for a branch", err)
	}
}

func TestCheckTagImmutability_IncompleteEarlierAttemptProceeds(t *testing.T) {
	uploadRoot := t.TempDir()
	s5 := newFakeS5cmd(t, uploadRoot, "")
	// No marker was ever written (earlier attempt failed before completing).

	if err := checkTagImmutability(s5, "tag", "1", "def", "s3://bucket/DS1/1/..complete", extractMarkerCommit); err != nil {
		t.Errorf("checkTagImmutability() = %v, want nil when no record exists", err)
	}
}

func TestCheckSourceRepoGuard_InstanceMarkerDifferentRepoRefused(t *testing.T) {
	uploadRoot := t.TempDir()
	s5 := newFakeS5cmd(t, uploadRoot, "")

	markerDir := filepath.Join(uploadRoot, "bucket", "DS1", "1")
	if err := os.MkdirAll(markerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(markerDir, "..complete"), []byte(`{"version":"1","commit":"abc","source_repo":"code.example.com/group/ds1"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	err := checkSourceRepoGuard(s5, "https://code.example.com/other/ds1.git", "s3://bucket/DS1/1/..complete", extractMarkerSourceRepo, false)
	if err == nil {
		t.Fatal("expected a refusal for a different source repository, got nil")
	}
	if !strings.Contains(err.Error(), "code.example.com/group/ds1") || !strings.Contains(err.Error(), "code.example.com/other/ds1") {
		t.Errorf("error = %v, want it to name both (normalised) repositories", err)
	}
}

func TestCheckSourceRepoGuard_DifferentRepoRefusalNeverLeaksCredentials(t *testing.T) {
	uploadRoot := t.TempDir()
	s5 := newFakeS5cmd(t, uploadRoot, "")

	markerDir := filepath.Join(uploadRoot, "bucket", "DS1", "1")
	if err := os.MkdirAll(markerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Both the recorded source_repo and the current repo URL carry
	// credentials (e.g. a CI token), as would happen with a clone URL like
	// https://gitlab-ci-token:<token>@host/group/repo.git.
	if err := os.WriteFile(filepath.Join(markerDir, "..complete"), []byte(`{"version":"1","commit":"abc","source_repo":"https://gitlab-ci-token:prev-secret-token@code.example.com/group/ds1.git"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	err := checkSourceRepoGuard(s5, "https://gitlab-ci-token:current-secret-token@code.example.com/other/ds1.git", "s3://bucket/DS1/1/..complete", extractMarkerSourceRepo, false)
	if err == nil {
		t.Fatal("expected a refusal for a different source repository, got nil")
	}
	if strings.Contains(err.Error(), "prev-secret-token") || strings.Contains(err.Error(), "current-secret-token") || strings.Contains(err.Error(), "gitlab-ci-token") {
		t.Errorf("error = %v, must never contain credentials from either repo URL", err)
	}
	if !strings.Contains(err.Error(), "code.example.com/group/ds1") || !strings.Contains(err.Error(), "code.example.com/other/ds1") {
		t.Errorf("error = %v, want it to name both repositories by their normalised (credential-free) host/path", err)
	}
}

func TestCheckSourceRepoGuard_SameRepoAllowed(t *testing.T) {
	uploadRoot := t.TempDir()
	s5 := newFakeS5cmd(t, uploadRoot, "")

	markerDir := filepath.Join(uploadRoot, "bucket", "DS1", "1")
	if err := os.MkdirAll(markerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(markerDir, "..complete"), []byte(`{"version":"1","commit":"abc","source_repo":"code.example.com/group/ds1"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := checkSourceRepoGuard(s5, "HTTPS://Code.Example.com/group/ds1.git", "s3://bucket/DS1/1/..complete", extractMarkerSourceRepo, false); err != nil {
		t.Errorf("checkSourceRepoGuard() = %v, want nil for the same (normalised) source repository", err)
	}
}

func TestCheckSourceRepoGuard_ForceAllowsDifferentRepo(t *testing.T) {
	uploadRoot := t.TempDir()
	s5 := newFakeS5cmd(t, uploadRoot, "")

	markerDir := filepath.Join(uploadRoot, "bucket", "DS1", "1")
	if err := os.MkdirAll(markerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(markerDir, "..complete"), []byte(`{"version":"1","commit":"abc","source_repo":"code.example.com/group/ds1"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := checkSourceRepoGuard(s5, "https://code.example.com/other/ds1.git", "s3://bucket/DS1/1/..complete", extractMarkerSourceRepo, true); err != nil {
		t.Errorf("checkSourceRepoGuard() = %v, want nil with force", err)
	}
}

func TestCheckSourceRepoGuard_NoRecordAllowed(t *testing.T) {
	uploadRoot := t.TempDir()
	s5 := newFakeS5cmd(t, uploadRoot, "")
	// No marker was ever written (first publish, or an earlier failed attempt).

	if err := checkSourceRepoGuard(s5, "https://code.example.com/group/ds1.git", "s3://bucket/DS1/1/..complete", extractMarkerSourceRepo, false); err != nil {
		t.Errorf("checkSourceRepoGuard() = %v, want nil when no record exists", err)
	}
}

func TestCheckSourceRepoGuard_LegacyRecordWithoutSourceRepoAllowed(t *testing.T) {
	uploadRoot := t.TempDir()
	s5 := newFakeS5cmd(t, uploadRoot, "")

	markerDir := filepath.Join(uploadRoot, "bucket", "DS1", "1")
	if err := os.MkdirAll(markerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A legacy marker with no "source_repo" field.
	if err := os.WriteFile(filepath.Join(markerDir, "..complete"), []byte(`{"version":"1","commit":"abc"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := checkSourceRepoGuard(s5, "https://code.example.com/other/ds1.git", "s3://bucket/DS1/1/..complete", extractMarkerSourceRepo, false); err != nil {
		t.Errorf("checkSourceRepoGuard() = %v, want nil for a legacy record without a source repository", err)
	}
}

func TestCheckSourceRepoGuard_AtlasBundleJSONDifferentRepoRefused(t *testing.T) {
	uploadRoot := t.TempDir()
	s5 := newFakeS5cmd(t, uploadRoot, "")

	bundleDir := filepath.Join(uploadRoot, "bucket", "v1.0.0", ".exohub-bundle")
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "bundle.json"), []byte(`{"ref_name":"v1.0.0","ref":"abc","repo_url":"https://code.example.com/group/ds1.git"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	err := checkSourceRepoGuard(s5, "https://code.example.com/fork/ds1.git", "s3://bucket/v1.0.0/.exohub-bundle/bundle.json", extractBundleRepoURL, false)
	if err == nil {
		t.Fatal("expected a refusal for a fork publishing to the Atlas role, got nil")
	}
}

func TestCheckSourceRepoGuard_AccessDeniedRefusesRatherThanProceeds(t *testing.T) {
	uploadRoot := t.TempDir()
	s5 := newFakeS5cmdDenyCat(t, uploadRoot, "", "DS1/1/..complete")

	err := checkSourceRepoGuard(s5, "https://code.example.com/group/ds1.git", "s3://bucket/DS1/1/..complete", extractMarkerSourceRepo, false)
	if err == nil {
		t.Fatal("expected a refusal when the record read fails with AccessDenied, got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "accessdenied") {
		t.Errorf("error = %v, want it to surface the AccessDenied detail", err)
	}
}

func TestDoPublishInstance_ForkRefusedForTagAndBranch(t *testing.T) {
	for _, refKind := range []string{"tag", "branch"} {
		t.Run(refKind, func(t *testing.T) {
			uploadRoot := t.TempDir()
			t.Setenv("S5CMD_BIN", newFakeS5cmd(t, uploadRoot, ""))

			markerDir := filepath.Join(uploadRoot, "bucket", "DS1", "1")
			if err := os.MkdirAll(markerDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(markerDir, "..complete"), []byte(`{"version":"1","commit":"abc","source_repo":"code.example.com/group/ds1"}`), 0o644); err != nil {
				t.Fatal(err)
			}

			instance := newInstanceStorageServer(t, "bucket", "")
			defer instance.Close()

			apiCalled := false
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				apiCalled = true
			}))
			defer api.Close()

			artifactdbDir := writeArtifactdbDir(t, map[string]string{"dataset.json": `{}`})

			_, err := doPublishInstance(instancePublishArgs{
				s3url:         "s3://bucket/DS1",
				artifactdbDir: artifactdbDir,
				instanceURL:   instance.URL,
				projectID:     "DS1",
				refName:       "1",
				refKind:       refKind,
				commit:        "abc",
				repoURL:       "https://code.example.com/other/ds1.git",
				apiURL:        api.URL,
			})
			if err == nil {
				t.Fatal("expected a refusal for a fork, got nil")
			}
			if apiCalled {
				t.Error("the ExoHub API must not be called when the source-repo guard refuses")
			}
			if uploadedExists(uploadRoot, "s3://bucket/DS1/1/dataset.json") {
				t.Error("dataset.json must not be uploaded when the source-repo guard refuses")
			}
		})
	}
}

func TestDoPublishInstance_ForceAllowsForkAndSendsForceTrue(t *testing.T) {
	uploadRoot := t.TempDir()
	t.Setenv("S5CMD_BIN", newFakeS5cmd(t, uploadRoot, ""))

	markerDir := filepath.Join(uploadRoot, "bucket", "DS1", "1")
	if err := os.MkdirAll(markerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(markerDir, "..complete"), []byte(`{"version":"1","commit":"abc","source_repo":"code.example.com/group/ds1"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	instance := newInstanceStorageServer(t, "bucket", "")
	defer instance.Close()

	var gotBody []byte
	api := newPublishAPIServer(t, &gotBody)
	defer api.Close()

	artifactdbDir := writeArtifactdbDir(t, map[string]string{"dataset.json": `{}`})

	result, err := doPublishInstance(instancePublishArgs{
		s3url:         "s3://bucket/DS1",
		artifactdbDir: artifactdbDir,
		instanceURL:   instance.URL,
		projectID:     "DS1",
		refName:       "1",
		refKind:       "tag",
		commit:        "abc",
		repoURL:       "https://code.example.com/other/ds1.git",
		apiURL:        api.URL,
		force:         true,
	})
	if err != nil {
		t.Fatalf("doPublishInstance() error = %v", err)
	}
	if !result.Published {
		t.Error("Published = false, want true with --force")
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(gotBody, &payload); err != nil {
		t.Fatalf("failed to parse request payload: %v", err)
	}
	if got, want := payload["force"], true; got != want {
		t.Errorf("payload[force] = %v, want %v", got, want)
	}
}

func TestDoPublishInstance_ForceDoesNotBypassMovedTag(t *testing.T) {
	uploadRoot := t.TempDir()
	t.Setenv("S5CMD_BIN", newFakeS5cmd(t, uploadRoot, ""))

	markerDir := filepath.Join(uploadRoot, "bucket", "DS1", "1")
	if err := os.MkdirAll(markerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(markerDir, "..complete"), []byte(`{"version":"1","commit":"abc"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	instance := newInstanceStorageServer(t, "bucket", "")
	defer instance.Close()

	apiCalled := false
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiCalled = true
	}))
	defer api.Close()

	artifactdbDir := writeArtifactdbDir(t, map[string]string{"dataset.json": `{}`})

	_, err := doPublishInstance(instancePublishArgs{
		s3url:         "s3://bucket/DS1",
		artifactdbDir: artifactdbDir,
		instanceURL:   instance.URL,
		projectID:     "DS1",
		refName:       "1",
		refKind:       "tag",
		commit:        "def", // the tag now resolves to a different commit than the record ("abc")
		repoURL:       "https://code.example.com/group/ds1.git",
		apiURL:        api.URL,
		force:         true,
	})
	if err == nil {
		t.Fatal("expected --force to still refuse a moved tag, got nil")
	}
	if !strings.Contains(err.Error(), "abc") || !strings.Contains(err.Error(), "def") {
		t.Errorf("error = %v, want it to name both commits (tag immutability, not source-repo)", err)
	}
	if apiCalled {
		t.Error("the ExoHub API must not be called when tag immutability refuses")
	}
}

func TestNormalizeRepoURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"https with .git", "https://code.example.com/group/ds1.git", "code.example.com/group/ds1"},
		{"https without .git", "https://code.example.com/group/ds1", "code.example.com/group/ds1"},
		{"https with trailing slash", "https://code.example.com/group/ds1/", "code.example.com/group/ds1"},
		{"http scheme", "http://code.example.com/group/ds1.git", "code.example.com/group/ds1"},
		{"mixed-case host", "https://Code.Example.COM/group/ds1.git", "code.example.com/group/ds1"},
		{"credentials in url", "https://user:pass@code.example.com/group/ds1.git", "code.example.com/group/ds1"},
		{"scp-like ssh", "git@code.example.com:group/ds1.git", "code.example.com/group/ds1"},
		{"ssh scheme", "ssh://git@code.example.com/group/ds1.git", "code.example.com/group/ds1"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeRepoURL(tt.in); got != tt.want {
				t.Errorf("normalizeRepoURL(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestBuildInstancePublishPayload_Golden(t *testing.T) {
	target := buildInstanceIngestTarget(
		"https://adb.example.com/v1/b",
		"s3://bucket/DS1/3/",
		"https://atlas.example.com/v1/a",
		"DS1",
		"3",
		"code.example.com/group/ds1",
	)
	payload := buildInstancePublishPayload("DS1", "code.example.com/group/ds1", "abc123", "3", "s3://bucket/DS1", "s3://bucket/DS1/3/", false, target)

	got, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}

	const want = `{
		"project_id": "DS1",
		"repo_url": "code.example.com/group/ds1",
		"ref": "abc123",
		"ref_name": "3",
		"s3_location": "s3://bucket/DS1/3/",
		"s3url": "s3://bucket/DS1",
		"bundle": false,
		"targets": [
			{
				"url": "https://adb.example.com/v1/b",
				"type": "artifactdb",
				"s3_location": "s3://bucket/DS1/3/",
				"location": {
					"type": "exohub",
					"exohub": {
						"atlas_url": "https://atlas.example.com/v1/a",
						"project_id": "DS1",
						"version": "3",
						"source_repo": "code.example.com/group/ds1"
					}
				}
			}
		]
	}`

	var gotParsed, wantParsed interface{}
	if err := json.Unmarshal(got, &gotParsed); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &wantParsed); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotParsed, wantParsed) {
		t.Errorf("buildInstancePublishPayload() = %s, want %s", got, want)
	}
}

// matchAnnexGlobSharedVectors is the test-vector table shared verbatim with
// matchAnnexGlobSharedVectors in go/exo/commands/sync/command_test.go,
// which owns the original matchAnnexGlob that this module's matchAnnexGlob
// is a verbatim port of (exo-cli#202 review: the catalog binary's
// exclude-glob matcher must never drift from exo's, since the dry-run
// "excluded" preview uses exo's copy and the actual publish uses this
// one). Keep both copies byte-identical; add a row here AND there.
var matchAnnexGlobSharedVectors = []struct {
	pattern string
	path    string
	want    bool
}{
	{"**/x.json", "ax.json", true},
	{"meta/**", "meta", false},
	{"dataset_merged.json*", "dataset_merged.json.bak", true},
	{"dataset_merged.json*", "sub/dataset_merged.json.bak", true},
	{"meta/*", "meta/a/b", false},
	{"*.json", "a/b.json", true},
	// A single * or ? inside a ** pattern must still stay within one path
	// segment ([^/]*, not .*) — these catch a mutation that lets * cross /
	// in the ** branch.
	{"**/m*", "x/m/y", false},
	{"**/m*", "x/mine.json", true},
	{"a/**/b?.json", "a/x/y/b1.json", true},
	{"a/**/b?.json", "a/x/b1/z.json", false},
}

func TestMatchAnnexGlob_SharedVectors(t *testing.T) {
	for _, tt := range matchAnnexGlobSharedVectors {
		t.Run(tt.pattern+"|"+tt.path, func(t *testing.T) {
			got := matchAnnexGlob(tt.pattern, tt.path)
			if got != tt.want {
				t.Errorf("matchAnnexGlob(%q, %q) = %v, want %v", tt.pattern, tt.path, got, tt.want)
			}
		})
	}
}

func TestValidateInstancePrefix(t *testing.T) {
	tests := []struct {
		name      string
		s3url     string
		projectID string
		wantErr   bool
	}{
		{"exact match", "s3://bucket/DS1", "DS1", false},
		{"reserved underscore", "s3://bucket/_DS1", "_DS1", true},
		{"foreign project", "s3://bucket/OTHER", "DS1", true},
		{"extra path segment", "s3://bucket/DS1/extra", "DS1", true},
		{"not s3", "https://bucket/DS1", "DS1", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateInstancePrefix(tt.s3url, tt.projectID)
			if tt.wantErr && err == nil {
				t.Errorf("validateInstancePrefix(%q, %q) expected error, got nil", tt.s3url, tt.projectID)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("validateInstancePrefix(%q, %q) unexpected error: %v", tt.s3url, tt.projectID, err)
			}
		})
	}
}

func TestValidateArtifactdbTree_RefusesReservedName(t *testing.T) {
	dir := writeArtifactdbDir(t, map[string]string{
		"dataset.json": `{}`,
		"..complete":   `{}`,
	})
	err := validateArtifactdbTree(dir)
	if err == nil {
		t.Fatal("expected a refusal for a '..'-prefixed name, got nil")
	}
}

func TestValidateArtifactdbTree_AllowsNormalTree(t *testing.T) {
	dir := writeArtifactdbDir(t, map[string]string{
		"dataset.json": `{}`,
		"sub/a.json":   `{}`,
	})
	if err := validateArtifactdbTree(dir); err != nil {
		t.Errorf("validateArtifactdbTree() = %v, want nil", err)
	}
}

func TestFetchInstanceMetaFolder_NoMatchingBucketRefuses(t *testing.T) {
	instance := newInstanceStorageServer(t, "other-bucket", "")
	defer instance.Close()

	_, err := fetchInstanceMetaFolder(instance.URL, "bucket")
	if err == nil {
		t.Fatal("expected an error when no storage client matches the bucket, got nil")
	}
}

func TestFetchInstanceMetaFolder_Unreachable(t *testing.T) {
	_, err := fetchInstanceMetaFolder("http://127.0.0.1:1", "bucket")
	if err == nil {
		t.Fatal("expected an error for an unreachable instance, got nil")
	}
}

func TestFetchInstanceMetaFolder_NoAuthorizationHeader(t *testing.T) {
	t.Setenv("EXO_TOKEN", "super-secret-token")

	var gotAuth string
	var sawAuthHeaderKey bool
	instance := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, sawAuthHeaderKey = r.Header["Authorization"]
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"storage":{"clients":[{"s3":{"bucket":"bucket"}}]}}`))
	}))
	defer instance.Close()

	if _, err := fetchInstanceMetaFolder(instance.URL, "bucket"); err != nil {
		t.Fatalf("fetchInstanceMetaFolder() error = %v", err)
	}
	if sawAuthHeaderKey || gotAuth != "" {
		t.Errorf("instance received an Authorization header (%q) — the ExoHub token must never be sent to the instance URL directly", gotAuth)
	}
}

func TestDoPublishInstance_NeverSendsTokenToInstance(t *testing.T) {
	t.Setenv("EXO_TOKEN", "super-secret-token")

	uploadRoot := t.TempDir()
	t.Setenv("S5CMD_BIN", newFakeS5cmd(t, uploadRoot, ""))

	var instanceGotAuth string
	instance := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		instanceGotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"storage":{"clients":[{"s3":{"bucket":"bucket"}}]}}`))
	}))
	defer instance.Close()

	var apiGotAuth string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiGotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","results":[{"url":"x","status":"ok"}]}`))
	}))
	defer api.Close()

	artifactdbDir := writeArtifactdbDir(t, map[string]string{"dataset.json": `{}`})

	if _, err := doPublishInstance(instancePublishArgs{
		s3url:         "s3://bucket/DS1",
		artifactdbDir: artifactdbDir,
		instanceURL:   instance.URL,
		projectID:     "DS1",
		refName:       "1",
		apiURL:        api.URL,
	}); err != nil {
		t.Fatalf("doPublishInstance() error = %v", err)
	}

	if instanceGotAuth != "" {
		t.Errorf("the instance received Authorization %q, want none", instanceGotAuth)
	}
	if apiGotAuth != "Bearer super-secret-token" {
		t.Errorf("the ExoHub API received Authorization %q, want the bearer token", apiGotAuth)
	}
}

func TestCheckTagImmutability_AccessDeniedRefusesRatherThanProceeds(t *testing.T) {
	uploadRoot := t.TempDir()
	s5 := newFakeS5cmdDenyCat(t, uploadRoot, "", "DS1/1/..complete")

	err := checkTagImmutability(s5, "tag", "1", "def", "s3://bucket/DS1/1/..complete", extractMarkerCommit)
	if err == nil {
		t.Fatal("expected a refusal when the record read fails with AccessDenied, got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "accessdenied") {
		t.Errorf("error = %v, want it to surface the AccessDenied detail", err)
	}
}

func TestDoPublishInstance_AccessDeniedTagCheckWritesNothing(t *testing.T) {
	uploadRoot := t.TempDir()
	t.Setenv("S5CMD_BIN", newFakeS5cmdDenyCat(t, uploadRoot, "", "..complete"))

	instance := newInstanceStorageServer(t, "bucket", "")
	defer instance.Close()

	apiCalled := false
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiCalled = true
	}))
	defer api.Close()

	artifactdbDir := writeArtifactdbDir(t, map[string]string{"dataset.json": `{}`})

	_, err := doPublishInstance(instancePublishArgs{
		s3url:         "s3://bucket/DS1",
		artifactdbDir: artifactdbDir,
		instanceURL:   instance.URL,
		projectID:     "DS1",
		refName:       "1",
		refKind:       "tag",
		commit:        "def",
		apiURL:        api.URL,
	})
	if err == nil {
		t.Fatal("expected a refusal when the tag record read fails with AccessDenied, got nil")
	}
	if uploadedLog(t, uploadRoot) != nil {
		t.Error("an AccessDenied tag-record read should write nothing")
	}
	if apiCalled {
		t.Error("the ExoHub API should not be called when the tag check refuses")
	}
}

// fakeCredentialHelper writes a fake exo-credential-helper to a temp dir
// that prints a fixed credential JSON and records the --account-id it was
// called with (if any) to a log file, for asserting per-remote grants
// account wiring and read-after-credentials ordering.
func fakeCredentialHelper(t *testing.T) (path, logPath string) {
	t.Helper()
	dir := t.TempDir()
	path = filepath.Join(dir, "exo-credential-helper")
	logPath = filepath.Join(dir, "calls.log")
	script := fmt.Sprintf(`#!/bin/bash
echo "$@" >> %q
echo '{"AccessKeyId":"AKIAFAKE","SecretAccessKey":"fakesecret","SessionToken":"faketoken","Expiration":"2099-01-01T00:00:00Z"}'
`, logPath)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path, logPath
}

func TestDoPublishInstance_TagCheckRunsAfterCredentialsFetched(t *testing.T) {
	uploadRoot := t.TempDir()

	// The fake s5cmd records the AWS_ACCESS_KEY_ID env var it sees for
	// every invocation (including `cat`), one line per call, so the test
	// can assert credentials were already set by the time the
	// tag-immutability read runs.
	dir := t.TempDir()
	s5 := filepath.Join(dir, "s5cmd")
	envLog := filepath.Join(dir, "env.log")
	script := fmt.Sprintf(`#!/bin/bash
set -e
UPLOAD_ROOT=%q
LOG=%q

echo "$1 AWS_ACCESS_KEY_ID=$AWS_ACCESS_KEY_ID" >> %q

cmd="$1"
case "$cmd" in
  sync)
    src="$2"; dst="$3"; src="${src%%/}"
    destpath=$(printf '%%s' "$dst" | sed 's#^s3://##')
    mkdir -p "$UPLOAD_ROOT/$destpath"
    find "$src" -type f | while read -r f; do
      rel=${f#"$src"/}
      mkdir -p "$UPLOAD_ROOT/$destpath/$(dirname "$rel")"
      cp "$f" "$UPLOAD_ROOT/$destpath/$rel"
      echo "sync $dst$rel" >> "$LOG"
      echo "$rel"
    done
    ;;
  cp)
    src="$2"; dst="$3"
    destpath=$(printf '%%s' "$dst" | sed 's#^s3://##')
    mkdir -p "$(dirname "$UPLOAD_ROOT/$destpath")"
    cp "$src" "$UPLOAD_ROOT/$destpath"
    echo "cp $dst" >> "$LOG"
    ;;
  cat)
    src="$2"
    srcpath=$(printf '%%s' "$src" | sed 's#^s3://##')
    if [ -f "$UPLOAD_ROOT/$srcpath" ]; then
      cat "$UPLOAD_ROOT/$srcpath"
    else
      echo "NoSuchKey" >&2
      exit 1
    fi
    ;;
  *)
    exit 1
    ;;
esac
`, uploadRoot, filepath.Join(uploadRoot, "..log"), envLog)
	if err := os.WriteFile(s5, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("S5CMD_BIN", s5)

	credHelper, credLog := fakeCredentialHelper(t)
	origCredHelperBin := credHelperBin
	credHelperBin = credHelper
	defer func() { credHelperBin = origCredHelperBin }()

	instance := newInstanceStorageServer(t, "bucket", "")
	defer instance.Close()

	var gotBody []byte
	api := newPublishAPIServer(t, &gotBody)
	defer api.Close()

	artifactdbDir := writeArtifactdbDir(t, map[string]string{"dataset.json": `{}`})

	if _, err := doPublishInstance(instancePublishArgs{
		s3url:           "s3://bucket/DS1",
		artifactdbDir:   artifactdbDir,
		instanceURL:     instance.URL,
		projectID:       "DS1",
		refName:         "1",
		refKind:         "tag",
		commit:          "abc",
		apiURL:          api.URL,
		grants:          true,
		grantsAccountID: "111122223333",
	}); err != nil {
		t.Fatalf("doPublishInstance() error = %v", err)
	}

	credCalls, err := os.ReadFile(credLog)
	if err != nil {
		t.Fatalf("credential helper was not called: %v", err)
	}
	if !strings.Contains(string(credCalls), "--account-id 111122223333") {
		t.Errorf("credential helper calls = %q, want --account-id 111122223333", credCalls)
	}

	envCalls, err := os.ReadFile(envLog)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(envCalls)), "\n") {
		if strings.HasPrefix(line, "cat ") {
			if !strings.Contains(line, "AWS_ACCESS_KEY_ID=AKIAFAKE") {
				t.Errorf("s5cmd cat ran without the fetched credentials in the environment: %q", line)
			}
		}
	}
}

func TestDoPublishInstance_InvalidPermissionsStopsBeforeAnyUpload(t *testing.T) {
	uploadRoot := t.TempDir()
	t.Setenv("S5CMD_BIN", newFakeS5cmd(t, uploadRoot, ""))

	instance := newInstanceStorageServer(t, "bucket", "")
	defer instance.Close()

	artifactdbDir := writeArtifactdbDir(t, map[string]string{"dataset.json": `{}`})

	tmpDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmpDir, ".exohub"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Missing read_access/write_access — invalid.
	if err := os.WriteFile(filepath.Join(tmpDir, ".exohub", "permissions"), []byte("owners:\n  - alice\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	origWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origWd)

	_, err = doPublishInstance(instancePublishArgs{
		s3url:         "s3://bucket/DS1",
		artifactdbDir: artifactdbDir,
		instanceURL:   instance.URL,
		projectID:     "DS1",
		refName:       "1",
		apiURL:        "http://127.0.0.1:1",
	})
	if err == nil {
		t.Fatal("expected a refusal for invalid .exohub/permissions, got nil")
	}
	if uploadedLog(t, uploadRoot) != nil {
		t.Error("an invalid permissions file must stop the publish before any upload (sync/cp)")
	}
}

func TestValidateArtifactdbTree_RefusesUnlockedAnnexPointer(t *testing.T) {
	dir := writeArtifactdbDir(t, map[string]string{
		"dataset.json": `{}`,
		"big.json":     "/annex/objects/9f/3f/SHA256E-s30--abcdef1234567890/SHA256E-s30--abcdef1234567890",
	})
	err := validateArtifactdbTree(dir)
	if err == nil {
		t.Fatal("expected a refusal for an unlocked annex pointer file, got nil")
	}
	if !strings.Contains(err.Error(), "big.json") {
		t.Errorf("error = %v, want it to name the offending path", err)
	}
}

func TestDoPublishInstance_UnlockedAnnexPointerRefusedBeforeUpload(t *testing.T) {
	uploadRoot := t.TempDir()
	t.Setenv("S5CMD_BIN", newFakeS5cmd(t, uploadRoot, ""))

	instance := newInstanceStorageServer(t, "bucket", "")
	defer instance.Close()

	artifactdbDir := writeArtifactdbDir(t, map[string]string{
		"dataset.json": `{}`,
		"big.json":     "/annex/objects/9f/3f/SHA256E-s30--abcdef1234567890/SHA256E-s30--abcdef1234567890",
	})

	_, err := doPublishInstance(instancePublishArgs{
		s3url:         "s3://bucket/DS1",
		artifactdbDir: artifactdbDir,
		instanceURL:   instance.URL,
		projectID:     "DS1",
		refName:       "1",
		apiURL:        "http://127.0.0.1:1",
	})
	if err == nil {
		t.Fatal("expected a refusal for an unlocked annex pointer file, got nil")
	}
	if uploadedLog(t, uploadRoot) != nil {
		t.Error("refusal should write nothing")
	}
}

func TestCheckTagImmutability_UnparseableRecordRefuses(t *testing.T) {
	uploadRoot := t.TempDir()
	s5 := newFakeS5cmd(t, uploadRoot, "")

	markerDir := filepath.Join(uploadRoot, "bucket", "DS1", "1")
	if err := os.MkdirAll(markerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Exists, but not the expected {"commit": "..."} shape.
	if err := os.WriteFile(filepath.Join(markerDir, "..complete"), []byte(`not valid json`), 0o644); err != nil {
		t.Fatal(err)
	}

	err := checkTagImmutability(s5, "tag", "1", "def", "s3://bucket/DS1/1/..complete", extractMarkerCommit)
	if err == nil {
		t.Fatal("expected a refusal when the existing record can't be parsed, got nil")
	}
	if !strings.Contains(err.Error(), "s3://bucket/DS1/1/..complete") {
		t.Errorf("error = %v, want it to name the record URL", err)
	}
}

func TestCheckTagImmutability_UnparseableRecordBranchUnaffected(t *testing.T) {
	uploadRoot := t.TempDir()
	s5 := newFakeS5cmd(t, uploadRoot, "")

	markerDir := filepath.Join(uploadRoot, "bucket", "DS1", "main")
	if err := os.MkdirAll(markerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(markerDir, "..complete"), []byte(`not valid json`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := checkTagImmutability(s5, "branch", "main", "def", "s3://bucket/DS1/main/..complete", extractMarkerCommit); err != nil {
		t.Errorf("checkTagImmutability() = %v, want nil for a branch regardless of the record's content", err)
	}
}

// TestInstancePublishPayloadSatisfiesAPIRequiredFields is a contract test
// against the ExoHub API's PublishRequest schema: every field it declares
// without a default (project_id, repo_url, ref, ref_name, s3_location, s3url)
// must be present and non-empty in the instance payload, or the API rejects
// the request with "Field required" (seen on a real instance publish).
func TestInstancePublishPayloadSatisfiesAPIRequiredFields(t *testing.T) {
	target := buildInstanceIngestTarget("https://adb.example.com/v1/b", "s3://bucket/DS1/3/", "https://atlas.example.com/v1/a", "DS1", "3", "code.example.com/group/ds1")
	payload := buildInstancePublishPayload("DS1", "code.example.com/group/ds1", "abc123", "3", "s3://bucket/DS1", "s3://bucket/DS1/3/", false, target)
	for _, k := range []string{"project_id", "repo_url", "ref", "ref_name", "s3_location", "s3url"} {
		v, ok := payload[k].(string)
		if !ok || v == "" {
			t.Errorf("instance publish payload is missing the API-required field %q (got %#v)", k, payload[k])
		}
	}
	if payload["bundle"] != false {
		t.Errorf("instance publish must send bundle:false, got %#v", payload["bundle"])
	}
}
