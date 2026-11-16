//go:build artifactdb

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// builtCLI compiles the publish binary once (-tags artifactdb) into a
// temporary directory and returns its path, for tests that exercise the
// actual `publish` flag parsing and --role dispatch end to end (os.Exit
// paths that can't be called in-process).
var builtCLI = sync.OnceValues(func() (string, error) {
	dir, err := filepath.Abs(".")
	if err != nil {
		return "", err
	}
	tmpDir, err := os.MkdirTemp("", "artifactdb-export-cli-test-")
	if err != nil {
		return "", err
	}
	out := filepath.Join(tmpDir, "git-annex-remote-artifactdb-export")
	cmd := exec.Command("go", "build", "-tags", "artifactdb", "-o", out, ".")
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", &buildError{output: string(output), err: err}
	}
	return out, nil
})

type buildError struct {
	output string
	err    error
}

func (e *buildError) Error() string { return e.output + ": " + e.err.Error() }

func runCLI(t *testing.T, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	bin, err := builtCLI()
	if err != nil {
		t.Fatalf("building test binary: %v", err)
	}
	cmd := exec.Command(bin, args...)
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	runErr := cmd.Run()
	if runErr == nil {
		return outBuf.String(), errBuf.String(), 0
	}
	if exitErr, ok := runErr.(*exec.ExitError); ok {
		return outBuf.String(), errBuf.String(), exitErr.ExitCode()
	}
	t.Fatalf("running test binary: %v", runErr)
	return "", "", -1
}

func TestCLI_PublishRejectsInstanceURL(t *testing.T) {
	_, stderr, code := runCLI(t, "publish", "--s3url", "s3://bucket/prefix", "--bundle-dir", t.TempDir(), "--instance-url", "https://adb.example.com/v1/x")
	if code == 0 {
		t.Fatal("expected a non-zero exit code")
	}
	if !strings.Contains(stderr, "removed") {
		t.Errorf("stderr = %q, want it to mention 'removed'", stderr)
	}
}

func TestCLI_PublishAtlasRejectsArtifactdbDir(t *testing.T) {
	_, stderr, code := runCLI(t, "publish", "--s3url", "s3://bucket/prefix", "--bundle-dir", t.TempDir(), "--artifactdb-dir", t.TempDir())
	if code == 0 {
		t.Fatal("expected a non-zero exit code")
	}
	if !strings.Contains(stderr, "removed") {
		t.Errorf("stderr = %q, want it to mention 'removed'", stderr)
	}
}

func TestCLI_PublishInstanceRejectsBundleDir(t *testing.T) {
	_, stderr, code := runCLI(t, "publish", "--role", "instance", "--s3url", "s3://bucket/DS1", "--project-id", "DS1", "--ref-name", "1", "--url", "https://adb.example.com/v1/x", "--bundle-dir", t.TempDir())
	if code == 0 {
		t.Fatal("expected a non-zero exit code")
	}
	if !strings.Contains(stderr, "--bundle-dir") {
		t.Errorf("stderr = %q, want it to mention --bundle-dir", stderr)
	}
}

func TestCLI_PublishAtlasRejectsExclude(t *testing.T) {
	_, stderr, code := runCLI(t, "publish", "--s3url", "s3://bucket/prefix", "--bundle-dir", t.TempDir(), "--exclude", "dataset_merged.json*")
	if code == 0 {
		t.Fatal("expected a non-zero exit code")
	}
	if !strings.Contains(stderr, "--exclude") {
		t.Errorf("stderr = %q, want it to mention --exclude", stderr)
	}
}

func TestCLI_PublishInvalidRole(t *testing.T) {
	_, stderr, code := runCLI(t, "publish", "--role", "bogus", "--s3url", "s3://bucket/prefix")
	if code == 0 {
		t.Fatal("expected a non-zero exit code")
	}
	if !strings.Contains(stderr, "bogus") {
		t.Errorf("stderr = %q, want it to name the invalid role", stderr)
	}
}
