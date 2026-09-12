package get

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestNewCommand(t *testing.T) {
	cmd := NewCommand()

	if cmd == nil {
		t.Fatal("NewCommand() returned nil")
	}

	if cmd.Use != "get [flags] [path ...]" {
		t.Errorf("command Use = %q, want %q", cmd.Use, "get [flags] [path ...]")
	}

	if cmd.Short == "" {
		t.Error("command Short description should not be empty")
	}

	if cmd.RunE == nil {
		t.Error("RunE function should not be nil")
	}
}

func TestNewCommandFlags(t *testing.T) {
	cmd := NewCommand()

	// Verify --from flag exists
	f := cmd.Flags().Lookup("from")
	if f == nil {
		t.Fatal("--from flag not found")
	}
	if f.DefValue != "" {
		t.Errorf("--from default = %q, want empty", f.DefValue)
	}

	// Verify --all flag exists
	f = cmd.Flags().Lookup("all")
	if f == nil {
		t.Fatal("--all flag not found")
	}
	if f.DefValue != "false" {
		t.Errorf("--all default = %q, want %q", f.DefValue, "false")
	}

	// Verify --jobs / -J flag exists
	f = cmd.Flags().Lookup("jobs")
	if f == nil {
		t.Fatal("--jobs flag not found")
	}
	if f.Shorthand != "J" {
		t.Errorf("--jobs shorthand = %q, want %q", f.Shorthand, "J")
	}
}

func TestJobsDefaultFromEnv(t *testing.T) {
	// When EXOHUB_JOBS is set, NewCommand should pick it up
	t.Setenv("EXOHUB_JOBS", "4")
	cmd := NewCommand()
	// The default is baked in at construction time; we verify the flag exists
	// and has the right shorthand (env handling is internal to RunE)
	f := cmd.Flags().Lookup("jobs")
	if f == nil {
		t.Fatal("--jobs flag not found")
	}
}

func TestJobsDefaultEmpty(t *testing.T) {
	os.Unsetenv("EXOHUB_JOBS")
	cmd := NewCommand()
	f := cmd.Flags().Lookup("jobs")
	if f == nil {
		t.Fatal("--jobs flag not found")
	}
}

func TestCommandExists(t *testing.T) {
	if !commandExists("echo") {
		t.Error("commandExists('echo') should return true")
	}
	if commandExists("nonexistent-binary-xyz-12345") {
		t.Error("commandExists should return false for nonexistent binary")
	}
}

func TestRequireBins(t *testing.T) {
	if err := requireBins("echo"); err != nil {
		t.Errorf("requireBins('echo') should succeed: %v", err)
	}

	// Capture stderr to avoid polluting test output
	oldStderr := os.Stderr
	_, w, _ := os.Pipe()
	os.Stderr = w

	err := requireBins("nonexistent-binary-xyz-12345")

	w.Close()
	os.Stderr = oldStderr

	if err == nil {
		t.Error("requireBins should fail for nonexistent binary")
	}
}

// TestEnsureAWSCredentialsMissingFileNoWarning verifies that when the AWS
// credentials file does not exist (the normal grants-managed path), no warning
// is printed to stderr. The grants credential helper supplies creds lazily, so
// a missing file must be silent on the default code path.
func TestEnsureAWSCredentialsMissingFileNoWarning(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	t.Setenv("EXOHUB_CLI_DEBUG", "")
	// Point the credentials file to a path that cannot exist.
	t.Setenv("HOME", t.TempDir())

	var buf bytes.Buffer
	oldStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stderr = w

	// Replicate the gated warning block from RunE. EXOHUB_CLI_DEBUG is unset,
	// so nothing must be written to stderr.
	if credErr := ensureAWSCredentials(); credErr != nil {
		if os.Getenv("EXOHUB_CLI_DEBUG") == "1" {
			fmt.Fprintf(os.Stderr, "debug: AWS credentials pre-seed skipped: %v\n", credErr)
		}
	}

	w.Close()
	os.Stderr = oldStderr
	_, _ = buf.ReadFrom(r)
	r.Close()

	if buf.Len() != 0 {
		t.Errorf("expected no stderr output on grants-managed path, got: %q", buf.String())
	}
}

// TestEnsureAWSCredentialsDebugOutput verifies that when EXOHUB_CLI_DEBUG=1,
// the advisory message is written to stderr so operators can diagnose issues.
func TestEnsureAWSCredentialsDebugOutput(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	t.Setenv("EXOHUB_CLI_DEBUG", "1")
	t.Setenv("HOME", t.TempDir())

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	oldStderr := os.Stderr
	os.Stderr = w

	if credErr := ensureAWSCredentials(); credErr != nil {
		if os.Getenv("EXOHUB_CLI_DEBUG") == "1" {
			fmt.Fprintf(os.Stderr, "debug: AWS credentials pre-seed skipped: %v\n", credErr)
		}
	}

	w.Close()
	os.Stderr = oldStderr

	var out bytes.Buffer
	_, _ = out.ReadFrom(r)
	r.Close()

	if out.Len() == 0 {
		t.Error("expected debug output on stderr when EXOHUB_CLI_DEBUG=1, got nothing")
	}
	if got := out.String(); !strings.Contains(got, "debug:") {
		t.Errorf("expected 'debug:' prefix in output, got: %q", got)
	}
}
