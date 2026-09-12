package unlock

import (
	"os"
	"testing"
)

func TestNewCommand(t *testing.T) {
	cmd := NewCommand()

	if cmd == nil {
		t.Fatal("NewCommand() returned nil")
	}

	if cmd.Use != "unlock [flags] [path ...]" {
		t.Errorf("command Use = %q, want %q", cmd.Use, "unlock [flags] [path ...]")
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

	// unlock should NOT have a --jobs flag (git-annex unlock doesn't support -J)
	f := cmd.Flags().Lookup("jobs")
	if f != nil {
		t.Error("unlock should not have a --jobs flag (git-annex unlock does not support -J)")
	}

	// unlock should NOT have a --force flag
	f = cmd.Flags().Lookup("force")
	if f != nil {
		t.Error("unlock should not have a --force flag")
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
