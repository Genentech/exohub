package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"syscall"

	gomcp "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

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

// getNumberParam extracts an integer-valued number parameter from the request.
func getNumberParam(request gomcp.CallToolRequest, key string) int {
	args := request.GetArguments()
	v, ok := args[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	}
	return 0
}

// getBoolParam extracts a bool parameter from the request, falling back to
// defaultVal when absent or not a bool.
func getBoolParam(request gomcp.CallToolRequest, key string, defaultVal bool) bool {
	args := request.GetArguments()
	v, ok := args[key]
	if !ok {
		return defaultVal
	}
	if b, ok := v.(bool); ok {
		return b
	}
	return defaultVal
}

// agentNameDisallowed matches any run of characters that are not valid in
// an exo provenance agent name (person:<name> / software:<name> — see
// internal/provenance's validAgentName: non-empty, no whitespace or "/").
// normalizeAgentName is stricter than that minimum, collapsing to a safe
// lowercase [a-z0-9._-] form so MCP client names like "Visual Studio Code"
// or "Claude Desktop" become valid agent names instead of being rejected
// by 'exo provenance validate' after the fact.
var agentNameDisallowed = regexp.MustCompile(`[^a-z0-9._-]+`)

// normalizeAgentName lowercases name and replaces every run of characters
// outside [a-z0-9._-] with a single "-", trimming leading/trailing "-".
// e.g. "Claude Code" -> "claude-code", "Visual Studio Code" ->
// "visual-studio-code". Returns "" if name has no normalizable content.
func normalizeAgentName(name string) string {
	lowered := strings.ToLower(strings.TrimSpace(name))
	normalized := agentNameDisallowed.ReplaceAllString(lowered, "-")
	return strings.Trim(normalized, "-")
}

// defaultSoftwareAgent returns "software:<normalized client name>" for the
// MCP client driving this tool call (design decision: an AI agent calling
// exo tools over MCP is a software agent, not the logged-in human), or ""
// if the transport exposes no client info (e.g. a bare context.Context in
// tests) or the client name has no normalizable content. The client name
// is normalized (lowercased, non [a-z0-9._-] runs collapsed to "-") so
// names like "Visual Studio Code" or "Claude Desktop" are valid exo
// provenance agent names instead of being rejected by 'exo provenance
// validate' after the fact.
func defaultSoftwareAgent(ctx context.Context) string {
	session := server.ClientSessionFromContext(ctx)
	if session == nil {
		return ""
	}
	withInfo, ok := session.(server.SessionWithClientInfo)
	if !ok {
		return ""
	}
	name := normalizeAgentName(withInfo.GetClientInfo().Name)
	if name == "" {
		return ""
	}
	return "software:" + name
}

// runExoJSON runs an exo subcommand and returns its stdout as the tool
// result, trusting well-formed JSON output; it fails the tool call if the
// command errors.
func runExoJSON(dir string, args ...string) (*gomcp.CallToolResult, error) {
	out, err := runInDir(dir, append([]string{"exo"}, args...)...)
	if err != nil {
		return resultError("exo %s failed: %s", strings.Join(args, " "), strings.TrimSpace(out))
	}
	return jsonOrText(out)
}

// runExoJSONTolerant runs an exo subcommand, trusting a well-formed JSON
// result even when the command exits non-zero. Some exo subcommands (e.g.
// 'provenance validate', 'doctor') use a non-zero exit code to report
// findings, not to signal that the tool call itself failed.
func runExoJSONTolerant(dir string, args ...string) (*gomcp.CallToolResult, error) {
	out, err := runInDir(dir, append([]string{"exo"}, args...)...)
	trimmed := strings.TrimSpace(out)
	if json.Valid([]byte(trimmed)) {
		return gomcp.NewToolResultText(trimmed), nil
	}
	if err != nil {
		return resultError("exo %s failed: %s", strings.Join(args, " "), trimmed)
	}
	return resultJSON(map[string]any{"status": "ok", "output": trimmed})
}

// jsonOrText wraps out as the tool result: verbatim if it is already valid
// JSON (e.g. from a --json flag), or as a {"status":"ok","output":...} text
// envelope otherwise.
func jsonOrText(out string) (*gomcp.CallToolResult, error) {
	trimmed := strings.TrimSpace(out)
	if json.Valid([]byte(trimmed)) {
		return gomcp.NewToolResultText(trimmed), nil
	}
	return resultJSON(map[string]any{"status": "ok", "output": trimmed})
}
