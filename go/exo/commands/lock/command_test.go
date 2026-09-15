package lock

import (
	"os"
	"testing"
)

func TestNewCommand(t *testing.T) {
	cmd := NewCommand()

	if cmd == nil {
		t.Fatal("NewCommand() returned nil")
	}

	if cmd.Use != "lock [flags] [path ...]" {
		t.Errorf("command Use = %q, want %q", cmd.Use, "lock [flags] [path ...]")
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

	f := cmd.Flags().Lookup("force")
	if f == nil {
		t.Fatal("--force flag not found")
	}
	if f.DefValue != "false" {
		t.Errorf("--force default = %q, want %q", f.DefValue, "false")
	}

	// lock should NOT have a --jobs flag (git-annex lock doesn't support -J)
	f = cmd.Flags().Lookup("jobs")
	if f != nil {
		t.Error("lock should not have a --jobs flag (git-annex lock does not support -J)")
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
