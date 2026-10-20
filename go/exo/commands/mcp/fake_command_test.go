package mcp

import (
	"os/exec"
	"sync"
	"testing"
)

// withFakeCommand replaces the package's command func so tool handlers run
// `sh -c script` instead of the real exo/git binaries, and records every
// argv they were about to invoke. Restores the original on test cleanup.
func withFakeCommand(t *testing.T, script string) *[][]string {
	t.Helper()
	var mu sync.Mutex
	var calls [][]string

	orig := command
	command = func(name string, args ...string) *exec.Cmd {
		mu.Lock()
		call := append([]string{name}, args...)
		calls = append(calls, call)
		mu.Unlock()
		return exec.Command("sh", "-c", script)
	}
	t.Cleanup(func() { command = orig })

	return &calls
}
