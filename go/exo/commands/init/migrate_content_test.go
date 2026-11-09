package init

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// fakeS3 is a minimal in-memory S3-compatible HTTP server covering just
// enough of the API (ListObjectsV2, HeadObject, GetObject, PutObject,
// CopyObject) to exercise --migrate-content without a real S3 endpoint.
type fakeS3 struct {
	mu        sync.Mutex
	objects   map[string][]byte // "bucket/key" -> content
	dropWrite map[string]bool   // "bucket/key" -> silently discard writes (simulates a corrupted upload)
}

func newFakeS3() *fakeS3 {
	return &fakeS3{objects: make(map[string][]byte), dropWrite: make(map[string]bool)}
}

func (f *fakeS3) put(bucket, key string, data []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.dropWrite[bucket+"/"+key] {
		return
	}
	f.objects[bucket+"/"+key] = append([]byte(nil), data...)
}

func (f *fakeS3) seed(bucket, key string, data []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects[bucket+"/"+key] = append([]byte(nil), data...)
}

func (f *fakeS3) get(bucket, key string) ([]byte, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	data, ok := f.objects[bucket+"/"+key]
	return data, ok
}

func (f *fakeS3) markDropWrite(bucket, key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dropWrite[bucket+"/"+key] = true
}

func (f *fakeS3) server() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(f.handle))
}

func (f *fakeS3) handle(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	parts := strings.SplitN(path, "/", 2)
	bucket := parts[0]

	if r.Method == http.MethodGet && r.URL.Query().Get("list-type") == "2" {
		f.listObjectsV2(w, bucket, r.URL.Query().Get("prefix"))
		return
	}

	if len(parts) < 2 || parts[1] == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	key, err := url.PathUnescape(parts[1])
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodHead:
		data, ok := f.get(bucket, key)
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.Header().Set("ETag", fmt.Sprintf(`"%x"`, data))
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		data, ok := f.get(bucket, key)
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.Header().Set("ETag", fmt.Sprintf(`"%x"`, data))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	case http.MethodPut:
		if src := r.Header.Get("X-Amz-Copy-Source"); src != "" {
			f.copyObject(w, bucket, key, src)
			return
		}
		data, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		f.put(bucket, key, data)
		w.Header().Set("ETag", fmt.Sprintf(`"%x"`, data))
		w.WriteHeader(http.StatusOK)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (f *fakeS3) copyObject(w http.ResponseWriter, destBucket, destKey, source string) {
	source = strings.TrimPrefix(source, "/")
	srcParts := strings.SplitN(source, "/", 2)
	if len(srcParts) != 2 {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	srcBucket := srcParts[0]
	srcKey, err := url.PathUnescape(srcParts[1])
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	data, ok := f.get(srcBucket, srcKey)
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprint(w, `<Error><Code>NoSuchKey</Code><Message>not found</Message></Error>`)
		return
	}
	f.put(destBucket, destKey, data)
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, `<CopyObjectResult><ETag>"%x"</ETag><LastModified>%s</LastModified></CopyObjectResult>`,
		data, time.Now().UTC().Format("2006-01-02T15:04:05.000Z"))
}

func (f *fakeS3) listObjectsV2(w http.ResponseWriter, bucket, prefix string) {
	f.mu.Lock()
	var keys []string
	for k := range f.objects {
		bp := strings.SplitN(k, "/", 2)
		if len(bp) != 2 || bp[0] != bucket {
			continue
		}
		if prefix != "" && !strings.HasPrefix(bp[1], prefix) {
			continue
		}
		keys = append(keys, bp[1])
	}
	f.mu.Unlock()

	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	sb.WriteString(`<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">`)
	fmt.Fprintf(&sb, `<Name>%s</Name><Prefix>%s</Prefix><KeyCount>%d</KeyCount><MaxKeys>1000</MaxKeys><IsTruncated>false</IsTruncated>`,
		bucket, prefix, len(keys))
	for _, k := range keys {
		data, _ := f.get(bucket, k)
		fmt.Fprintf(&sb, `<Contents><Key>%s</Key><LastModified>%s</LastModified><ETag>"%x"</ETag><Size>%d</Size><StorageClass>STANDARD</StorageClass></Contents>`,
			k, time.Now().UTC().Format("2006-01-02T15:04:05.000Z"), data, len(data))
	}
	sb.WriteString(`</ListBucketResult>`)
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, sb.String())
}

// withNoLocationLog stubs the package-level `command` var so
// locationLogKeys's `git annex find --in <remote>` never shells out for
// real: run from this repo's own working directory, an unstubbed call
// would treat "origin" as a real git remote and hang on a network probe
// instead of just reporting "no location log entries".
func withNoLocationLog(t *testing.T) {
	t.Helper()
	withCommand(t, func(name string, args ...string) *exec.Cmd {
		return exec.Command("sh", "-c", "exit 1")
	})
}

func newTestS3Client(t *testing.T, server *httptest.Server, accessKey string) *s3.Client {
	t.Helper()
	return s3.New(s3.Options{
		Region:       "us-east-1",
		UsePathStyle: true,
		BaseEndpoint: aws.String(server.URL),
		Credentials:  credentials.NewStaticCredentialsProvider(accessKey, "secret", ""),
	})
}

// --- planContentMigration / runContentMigration (mode passed explicitly) ---

func TestPlanContentMigration_SameBucketCopiesEverything(t *testing.T) {
	withNoLocationLog(t)
	fake := newFakeS3()
	fake.seed("bucket", "old/prefix/a.bin", []byte("hello"))
	fake.seed("bucket", "old/prefix/sub/b.bin", []byte("world"))
	server := fake.server()
	defer server.Close()
	client := newTestS3Client(t, server, "test")

	plan, err := planContentMigration(context.Background(), client, client, modeServerSide, RemoteConfig{Name: "origin"}, nil, "s3://bucket/old/prefix", "s3://bucket/new/prefix")
	if err != nil {
		t.Fatalf("planContentMigration: %v", err)
	}
	if len(plan.ToCopy) != 2 {
		t.Fatalf("expected 2 to-copy actions, got %+v", plan.ToCopy)
	}

	report := runContentMigration(context.Background(), client, client, plan, 4)
	if !report.ok() {
		t.Fatalf("expected success, got %+v", report)
	}
	if len(report.Copied) != 2 {
		t.Fatalf("expected 2 copied, got %+v", report.Copied)
	}

	if data, ok := fake.get("bucket", "new/prefix/a.bin"); !ok || string(data) != "hello" {
		t.Errorf("expected a.bin content preserved, got %q ok=%v", data, ok)
	}
}

func TestPlanContentMigration_ChunkedKeys(t *testing.T) {
	withNoLocationLog(t)
	fake := newFakeS3()
	chunkedKey := "SHA256E-s10--deadbeef-S5-C1"
	fake.seed("bucket", "old/prefix/"+chunkedKey, []byte("chunk-one"))
	fake.seed("bucket", "old/prefix/SHA256E-s10--deadbeef-S5-C2", []byte("chunk-two"))
	server := fake.server()
	defer server.Close()
	client := newTestS3Client(t, server, "test")

	plan, err := planContentMigration(context.Background(), client, client, modeServerSide, RemoteConfig{Name: "origin"}, nil, "s3://bucket/old/prefix", "s3://bucket/new/prefix")
	if err != nil {
		t.Fatalf("planContentMigration: %v", err)
	}
	report := runContentMigration(context.Background(), client, client, plan, 4)
	if !report.ok() {
		t.Fatalf("expected success, got %+v", report)
	}
	if data, ok := fake.get("bucket", "new/prefix/"+chunkedKey); !ok || string(data) != "chunk-one" {
		t.Errorf("expected chunked key migrated, got %q ok=%v", data, ok)
	}
}

func TestPlanContentMigration_ResumeSkipsExisting(t *testing.T) {
	withNoLocationLog(t)
	fake := newFakeS3()
	fake.seed("bucket", "old/prefix/a.bin", []byte("hello"))
	fake.seed("bucket", "old/prefix/b.bin", []byte("world"))
	fake.seed("bucket", "new/prefix/a.bin", []byte("hello")) // already migrated, same size/ETag
	server := fake.server()
	defer server.Close()
	client := newTestS3Client(t, server, "test")

	plan, err := planContentMigration(context.Background(), client, client, modeServerSide, RemoteConfig{Name: "origin"}, nil, "s3://bucket/old/prefix", "s3://bucket/new/prefix")
	if err != nil {
		t.Fatalf("planContentMigration: %v", err)
	}

	var copyKeys, skipKeys []string
	for _, a := range plan.ToCopy {
		copyKeys = append(copyKeys, a.Key)
	}
	for _, a := range plan.Skipped {
		skipKeys = append(skipKeys, a.Key)
	}
	if len(skipKeys) != 1 || skipKeys[0] != "a.bin" {
		t.Errorf("expected a.bin to be skipped (already at destination), got %+v", skipKeys)
	}
	if len(copyKeys) != 1 || copyKeys[0] != "b.bin" {
		t.Errorf("expected b.bin to be copied, got %+v", copyKeys)
	}

	report := runContentMigration(context.Background(), client, client, plan, 4)
	if !report.ok() {
		t.Fatalf("expected success, got %+v", report)
	}
	if len(report.AlreadyPresent) != 1 || report.AlreadyPresent[0] != "a.bin" {
		t.Errorf("expected a.bin reported as already present, got %+v", report.AlreadyPresent)
	}
	if len(report.Copied) != 1 || report.Copied[0] != "b.bin" {
		t.Errorf("expected b.bin reported as copied, got %+v", report.Copied)
	}
}

func TestPlanContentMigration_RefusesOverlappingPrefixes(t *testing.T) {
	fake := newFakeS3()
	server := fake.server()
	defer server.Close()
	client := newTestS3Client(t, server, "test")

	cases := []struct{ old, new string }{
		{"s3://bucket/ds/", "s3://bucket/ds/v2/"}, // new nested inside old
		{"s3://bucket/ds/v2/", "s3://bucket/ds/"}, // old nested inside new
	}
	for _, tc := range cases {
		_, err := planContentMigration(context.Background(), client, client, modeServerSide, RemoteConfig{Name: "origin"}, nil, tc.old, tc.new)
		if err == nil {
			t.Errorf("planContentMigration(%s, %s): expected overlap error, got nil", tc.old, tc.new)
		}
	}
}

func TestPlanContentMigration_ExcludesOtherRemotePrefix(t *testing.T) {
	withNoLocationLog(t)
	fake := newFakeS3()
	fake.seed("bucket", "ds/a.bin", []byte("mine"))
	fake.seed("bucket", "ds/sibling/b.bin", []byte("not mine"))
	server := fake.server()
	defer server.Close()
	client := newTestS3Client(t, server, "test")

	remotesConfig := &RemotesConfig{Remotes: []RemoteConfig{
		{Name: "origin", S3URL: "s3://bucket/ds/"},
		{Name: "sibling", S3URL: "s3://bucket/ds/sibling/"},
	}}

	plan, err := planContentMigration(context.Background(), client, client, modeServerSide, RemoteConfig{Name: "origin"}, remotesConfig, "s3://bucket/ds/", "s3://bucket/new-ds/")
	if err != nil {
		t.Fatalf("planContentMigration: %v", err)
	}
	for _, a := range plan.ToCopy {
		if strings.HasPrefix(a.Key, "sibling/") {
			t.Errorf("expected sibling/* excluded from this remote's migration, got %+v", a)
		}
	}
	if len(plan.Excluded) != 1 {
		t.Errorf("expected 1 excluded object, got %+v", plan.Excluded)
	}
}

func TestPlanContentMigration_FlagsLostKeys(t *testing.T) {
	fake := newFakeS3()
	fake.seed("bucket", "old/prefix/present-key", []byte("here"))
	server := fake.server()
	defer server.Close()
	client := newTestS3Client(t, server, "test")

	withCommand(t, func(name string, args ...string) *exec.Cmd {
		if name == "git" && len(args) >= 2 && args[0] == "annex" && args[1] == "find" {
			return exec.Command("printf", "present-key\\nmissing-key\\n")
		}
		return exec.Command("sh", "-c", "exit 0")
	})

	plan, err := planContentMigration(context.Background(), client, client, modeServerSide, RemoteConfig{Name: "origin"}, nil, "s3://bucket/old/prefix", "s3://bucket/new/prefix")
	if err != nil {
		t.Fatalf("planContentMigration: %v", err)
	}
	if len(plan.Lost) != 1 || plan.Lost[0] != "missing-key" {
		t.Errorf("expected missing-key flagged as lost, got %+v", plan.Lost)
	}
}

func TestRunContentMigration_StreamFallback(t *testing.T) {
	withNoLocationLog(t)
	fake := newFakeS3()
	fake.seed("bucket", "old/prefix/a.bin", []byte("stream-me"))
	server := fake.server()
	defer server.Close()
	// Two distinct clients (as migrateRemoteContent would build for differing
	// credentials) pointed at the same fake server -- stream mode performs a
	// real GetObject (src) + PutObject (dest) instead of CopyObject.
	srcClient := newTestS3Client(t, server, "reader")
	destClient := newTestS3Client(t, server, "writer")

	plan, err := planContentMigration(context.Background(), srcClient, destClient, modeStream, RemoteConfig{Name: "origin"}, nil, "s3://bucket/old/prefix", "s3://bucket/new/prefix")
	if err != nil {
		t.Fatalf("planContentMigration: %v", err)
	}
	report := runContentMigration(context.Background(), srcClient, destClient, plan, 4)
	if !report.ok() {
		t.Fatalf("expected success, got %+v", report)
	}
	if data, ok := fake.get("bucket", "new/prefix/a.bin"); !ok || string(data) != "stream-me" {
		t.Errorf("expected a.bin migrated via stream fallback, got %q ok=%v", data, ok)
	}
}

func TestRunContentMigration_VerifyCatchesMissingAfterCopy(t *testing.T) {
	withNoLocationLog(t)
	fake := newFakeS3()
	fake.seed("bucket", "old/prefix/a.bin", []byte("hello"))
	fake.markDropWrite("bucket", "new/prefix/a.bin") // simulates a copy that reports success but never lands
	server := fake.server()
	defer server.Close()
	client := newTestS3Client(t, server, "test")

	plan, err := planContentMigration(context.Background(), client, client, modeServerSide, RemoteConfig{Name: "origin"}, nil, "s3://bucket/old/prefix", "s3://bucket/new/prefix")
	if err != nil {
		t.Fatalf("planContentMigration: %v", err)
	}
	report := runContentMigration(context.Background(), client, client, plan, 4)
	if report.ok() {
		t.Fatalf("expected verification to catch the missing object, got ok report: %+v", report)
	}
	if len(report.MissingAfter) != 1 || report.MissingAfter[0] != "a.bin" {
		t.Errorf("expected a.bin reported missing after copy, got %+v", report.MissingAfter)
	}
}

// --- migrateRemoteContent (full orchestration: credentials + dry-run) ---

func withStubbedCredentials(t *testing.T, fn func(s3url string, grants bool, group, permission string) (aws.Credentials, error)) {
	t.Helper()
	old := credentialFetcher
	credentialFetcher = fn
	t.Cleanup(func() { credentialFetcher = old })
}

func withTestS3Endpoint(t *testing.T, server *httptest.Server) {
	t.Helper()
	old := s3TestBaseEndpoint
	s3TestBaseEndpoint = server.URL
	t.Cleanup(func() { s3TestBaseEndpoint = old })
}

func TestMigrateRemoteContent_DryRunChangesNothing(t *testing.T) {
	withNoLocationLog(t)
	fake := newFakeS3()
	fake.seed("bucket", "old/prefix/a.bin", []byte("hello"))
	server := fake.server()
	defer server.Close()
	withTestS3Endpoint(t, server)
	withStubbedCredentials(t, func(s3url string, grants bool, group, permission string) (aws.Credentials, error) {
		return aws.Credentials{AccessKeyID: "same", SecretAccessKey: "same"}, nil
	})

	remote := RemoteConfig{Name: "origin", Type: "annex"}
	if err := migrateRemoteContent(remote, "s3://bucket/old/prefix", "s3://bucket/new/prefix", true, false); err != nil {
		t.Fatalf("migrateRemoteContent (dry-run): %v", err)
	}
	if _, ok := fake.get("bucket", "new/prefix/a.bin"); ok {
		t.Fatalf("--dry-run must not copy anything, but new/prefix/a.bin exists")
	}
}

func TestMigrateRemoteContent_SameIdentityUsesServerSideCopy(t *testing.T) {
	withNoLocationLog(t)
	fake := newFakeS3()
	fake.seed("bucket", "old/prefix/a.bin", []byte("hello"))
	server := fake.server()
	defer server.Close()
	withTestS3Endpoint(t, server)
	withStubbedCredentials(t, func(s3url string, grants bool, group, permission string) (aws.Credentials, error) {
		return aws.Credentials{AccessKeyID: "same", SecretAccessKey: "same"}, nil
	})

	remote := RemoteConfig{Name: "origin", Type: "annex"}
	if err := migrateRemoteContent(remote, "s3://bucket/old/prefix", "s3://bucket/new/prefix", false, false); err != nil {
		t.Fatalf("migrateRemoteContent: %v", err)
	}
	if data, ok := fake.get("bucket", "new/prefix/a.bin"); !ok || string(data) != "hello" {
		t.Fatalf("expected a.bin migrated, got %q ok=%v", data, ok)
	}
}

func TestMigrateRemoteContent_DifferingCredentialsStream(t *testing.T) {
	withNoLocationLog(t)
	fake := newFakeS3()
	fake.seed("bucket", "old/prefix/a.bin", []byte("hello"))
	server := fake.server()
	defer server.Close()
	withTestS3Endpoint(t, server)
	withStubbedCredentials(t, func(s3url string, grants bool, group, permission string) (aws.Credentials, error) {
		if strings.Contains(s3url, "old") {
			return aws.Credentials{AccessKeyID: "reader", SecretAccessKey: "x"}, nil
		}
		return aws.Credentials{AccessKeyID: "writer", SecretAccessKey: "y"}, nil
	})

	remote := RemoteConfig{Name: "origin", Type: "annex", Grants: true}
	if err := migrateRemoteContent(remote, "s3://bucket/old/prefix", "s3://bucket/new/prefix", false, false); err != nil {
		t.Fatalf("migrateRemoteContent: %v", err)
	}
	if data, ok := fake.get("bucket", "new/prefix/a.bin"); !ok || string(data) != "hello" {
		t.Fatalf("expected a.bin migrated via stream fallback, got %q ok=%v", data, ok)
	}
}

func TestMigrateRemoteContent_FailsNonZeroWhenMissingAfterCopy(t *testing.T) {
	withNoLocationLog(t)
	fake := newFakeS3()
	fake.seed("bucket", "old/prefix/a.bin", []byte("hello"))
	fake.markDropWrite("bucket", "new/prefix/a.bin")
	server := fake.server()
	defer server.Close()
	withTestS3Endpoint(t, server)
	withStubbedCredentials(t, func(s3url string, grants bool, group, permission string) (aws.Credentials, error) {
		return aws.Credentials{AccessKeyID: "same", SecretAccessKey: "same"}, nil
	})

	remote := RemoteConfig{Name: "origin", Type: "annex"}
	err := migrateRemoteContent(remote, "s3://bucket/old/prefix", "s3://bucket/new/prefix", false, false)
	if err == nil {
		t.Fatal("expected migrateRemoteContent to fail when an object is missing after copy")
	}
}

// --- ensureRemoteConfigS3URL: a failed migration must leave s3url unchanged ---

func TestEnsureRemoteConfigS3URL_FailedMigrationLeavesS3URLUnchanged(t *testing.T) {
	fake := newFakeS3()
	fake.seed("bucket", "old/prefix/a.bin", []byte("hello"))
	fake.markDropWrite("bucket", "new/prefix/a.bin")
	server := fake.server()
	defer server.Close()
	withTestS3Endpoint(t, server)
	withStubbedCredentials(t, func(s3url string, grants bool, group, permission string) (aws.Credentials, error) {
		return aws.Credentials{AccessKeyID: "same", SecretAccessKey: "same"}, nil
	})

	oldYes := flagYes
	oldMigrate := flagMigrateContent
	flagYes = true
	flagMigrateContent = true
	t.Cleanup(func() {
		flagYes = oldYes
		flagMigrateContent = oldMigrate
	})

	savedS3URL := "s3://bucket/old/prefix"
	withCommand(t, func(name string, args ...string) *exec.Cmd {
		if name == "git" && len(args) >= 3 && args[0] == "config" && args[1] == "--get" {
			if args[2] == "remote.origin.annex-uuid" {
				return exec.Command("printf", "uuid-1234")
			}
			if args[2] == "annex-remote.uuid-1234.config.s3url" {
				return exec.Command("printf", "%s", savedS3URL)
			}
			return exec.Command("sh", "-c", "exit 1")
		}
		if name == "git" && len(args) >= 2 && args[0] == "annex" && args[1] == "find" {
			return exec.Command("sh", "-c", "exit 1") // no location-log entries in this test
		}
		return exec.Command("sh", "-c", "exit 0")
	})

	ensureRemoteConfigS3URL(RemoteConfig{Name: "origin", S3URL: "s3://bucket/new/prefix"})

	if got := getRemoteConfig("origin", "s3url"); got != savedS3URL {
		t.Errorf("expected s3url to remain %q after a failed migration, got %q", savedS3URL, got)
	}
}

func TestCopySourcePath_EscapesSpecialCharacters(t *testing.T) {
	got := copySourcePath("my-bucket", "a dir/key with spaces.bin")
	want := "my-bucket/a%20dir/key%20with%20spaces.bin"
	if got != want {
		t.Errorf("copySourcePath = %q, want %q", got, want)
	}
}

func TestSameIdentity(t *testing.T) {
	a := aws.Credentials{AccessKeyID: "x", SecretAccessKey: "y"}
	b := aws.Credentials{AccessKeyID: "x", SecretAccessKey: "y"}
	c := aws.Credentials{AccessKeyID: "x", SecretAccessKey: "z"}
	if !sameIdentity(a, b) {
		t.Error("expected identical credentials to be sameIdentity")
	}
	if sameIdentity(a, c) {
		t.Error("expected differing secret keys to not be sameIdentity")
	}
}
