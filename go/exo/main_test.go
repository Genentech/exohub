package main

import (
	"os"
	"os/exec"
	"testing"
)

// TestExitCode verifies exit code extraction from errors
func TestExitCode(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		expected    int
		description string
	}{
		{
			name:        "nil_error",
			err:         nil,
			expected:    0,
			description: "nil error should return exit code 0",
		},
		{
			name:        "generic_error",
			err:         exec.ErrNotFound,
			expected:    1,
			description: "generic error should return exit code 1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldStderr := os.Stderr
			r, w, _ := os.Pipe()
			os.Stderr = w

			code := exitCode(tt.err)

			w.Close()
			os.Stderr = oldStderr

			buf := make([]byte, 1024)
			r.Read(buf)

			if code != tt.expected {
				t.Errorf("%s: exitCode() = %d, want %d", tt.description, code, tt.expected)
			}
		})
	}
}

// TestExitCodeWithExitError verifies exit code extraction from ExitError
func TestExitCodeWithExitError(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 5")
	err := cmd.Run()

	if err == nil {
		t.Fatal("Expected command to fail with exit code 5")
	}

	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("Expected *exec.ExitError, got %T", err)
	}

	code := exitCode(exitErr)
	if code != 5 {
		t.Errorf("exitCode() = %d, want 5", code)
	}
}

// TestExecCommand verifies the execCommand helper function
func TestExecCommand(t *testing.T) {
	err := execCommand("echo", "test")
	if err != nil {
		t.Errorf("execCommand('echo', 'test') failed: %v", err)
	}
}

// TestExecCommandWithFailure verifies error handling in execCommand
func TestExecCommandWithFailure(t *testing.T) {
	err := execCommand("nonexistent-command-12345")
	if err == nil {
		t.Error("execCommand with nonexistent command should return an error")
	}
}

// TestExecCommandWithNonZeroExit verifies exit code handling
func TestExecCommandWithNonZeroExit(t *testing.T) {
	err := execCommand("sh", "-c", "exit 42")

	if err == nil {
		t.Fatal("Expected command to return an error")
	}

	if exitErr, ok := err.(*exec.ExitError); ok {
		code := exitErr.ExitCode()
		if code != 42 {
			t.Errorf("Expected exit code 42, got %d", code)
		}
	} else {
		t.Errorf("Expected *exec.ExitError, got %T", err)
	}
}
