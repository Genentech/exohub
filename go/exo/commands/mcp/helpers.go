package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"syscall"

	gomcp "github.com/mark3labs/mcp-go/mcp"

	"github.com/Genentech/exohub/go/exo/commandutil"
)

var command = commandutil.Command

// processTracker manages background processes for cleanup on server exit.
type processTracker struct {
	mu    sync.Mutex
	procs map[string]*os.Process
}

var backgroundProcs = &processTracker{procs: make(map[string]*os.Process)}

// Track registers a background process for cleanup.
func (pt *processTracker) Track(key string, proc *os.Process) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	pt.procs[key] = proc
}

// Remove unregisters a background process (e.g., after it exits).
func (pt *processTracker) Remove(key string) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	delete(pt.procs, key)
}

// IsRunning checks if a tracked process is still alive.
func (pt *processTracker) IsRunning(key string) bool {
	pt.mu.Lock()
	proc, ok := pt.procs[key]
	pt.mu.Unlock()
	if !ok {
		return false
	}
	// Signal 0 tests if process exists without sending a signal
	err := proc.Signal(syscall.Signal(0))
	if err != nil {
		pt.Remove(key)
		return false
	}
	return true
}

// KillAll terminates all tracked background processes and their process groups.
func (pt *processTracker) KillAll() {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	for key, proc := range pt.procs {
		_ = syscall.Kill(-proc.Pid, syscall.SIGTERM)
		delete(pt.procs, key)
	}
}

// requireGitRepo validates that the given directory is a git repository.
func requireGitRepo(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("directory not found: %s", dir)
	}
	if !info.IsDir() {
		return fmt.Errorf("not a directory: %s", dir)
	}
	gitDir := dir + "/.git"
	info, err = os.Stat(gitDir)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("not a git repository: %s", dir)
	}
	return nil
}

// requireAnnexInit validates that git-annex is initialized in the given repo.
func requireAnnexInit(dir string) error {
	cmd := command("git", "config", "--get", "annex.uuid")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git-annex not initialized in %s — run 'exo init' first", dir)
	}
	return nil
}

// runInDir runs a command in the given directory and returns combined output.
func runInDir(dir string, args ...string) (string, error) {
	cmd := command(args[0], args[1:]...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// runInDirSeparate runs a command in the given directory and returns stdout and stderr separately.
func runInDirSeparate(dir string, args ...string) (stdout, stderr string, err error) {
	cmd := command(args[0], args[1:]...)
	cmd.Dir = dir
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err = cmd.Run()
	return outBuf.String(), errBuf.String(), err
}

// resultJSON creates a successful MCP tool result from a JSON-serializable value.
func resultJSON(v any) (*gomcp.CallToolResult, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal result: %w", err)
	}
	return gomcp.NewToolResultText(string(data)), nil
}

// resultError creates an MCP error result.
func resultError(format string, args ...any) (*gomcp.CallToolResult, error) {
	return gomcp.NewToolResultError(fmt.Sprintf(format, args...)), nil
}

// getStringParam extracts a string parameter from the request.
func getStringParam(request gomcp.CallToolRequest, key string) string {
	args := request.GetArguments()
	if v, ok := args[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// getStringArrayParam extracts a string array parameter from the request.
func getStringArrayParam(request gomcp.CallToolRequest, key string) []string {
	args := request.GetArguments()
	v, ok := args[key]
	if !ok {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	var result []string
	for _, item := range arr {
		if s, ok := item.(string); ok {
			result = append(result, s)
		}
	}
	return result
}
