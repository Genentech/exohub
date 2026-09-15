package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	gomcp "github.com/mark3labs/mcp-go/mcp"
)

type heartbeatRecordResult struct {
	Status   string `json:"status"`
	RepoDir  string `json:"repo_dir"`
	Interval int    `json:"interval"`
	Message  string `json:"message"`
}

func handleHeartbeatRecord(_ context.Context, request gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
	repoDir := getStringParam(request, "repo_dir")
	if repoDir == "" {
		return resultError("repo_dir is required")
	}
	if err := requireGitRepo(repoDir); err != nil {
		return resultError("%v", err)
	}

	repoAbs, err := filepath.Abs(repoDir)
	if err != nil {
		return resultError("failed to resolve repo path: %v", err)
	}

	interval := 2
	if v := getStringParam(request, "interval"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			interval = parsed
		}
	}

	paths := getStringArrayParam(request, "paths")
	if len(paths) == 0 {
		paths = []string{"."}
	}

	key := "heartbeat:" + repoAbs

	// Check if already recording for this repo
	if backgroundProcs.IsRunning(key) {
		return resultJSON(heartbeatRecordResult{
			Status:  "already_running",
			RepoDir: repoAbs,
			Message: "heartbeat record is already running for this repository",
		})
	}

	// Build command args
	args := []string{"heartbeat", "record", "--watch",
		"--repo", repoAbs,
		"--interval", strconv.Itoa(interval),
	}
	for _, p := range paths {
		args = append(args, "--path", p)
	}

	// Find exo binary
	exePath, err := os.Executable()
	if err != nil {
		return resultError("failed to find exo binary: %v", err)
	}

	cmd := exec.Command(exePath, args...)
	cmd.Dir = repoAbs
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return resultError("failed to start heartbeat record: %v", err)
	}

	backgroundProcs.Track(key, cmd.Process)
	go cmd.Wait()

	return resultJSON(heartbeatRecordResult{
		Status:   "started",
		RepoDir:  repoAbs,
		Interval: interval,
		Message:  fmt.Sprintf("heartbeat record started in background (every %ds), use heartbeat_show to check progress", interval),
	})
}

type heartbeatShowResult struct {
	Status  string `json:"status"`
	RepoDir string `json:"repo_dir"`
	Metrics any    `json:"metrics,omitempty"`
	Output  string `json:"output,omitempty"`
}

func handleHeartbeatShow(_ context.Context, request gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
	repoDir := getStringParam(request, "repo_dir")
	if repoDir == "" {
		return resultError("repo_dir is required")
	}
	if err := requireGitRepo(repoDir); err != nil {
		return resultError("%v", err)
	}

	repoAbs, err := filepath.Abs(repoDir)
	if err != nil {
		return resultError("failed to resolve repo path: %v", err)
	}

	// Try to read the latest metrics JSON directly
	metricsPath := filepath.Join(repoAbs, ".git", "exohub", "metrics", "latest.json")
	data, err := os.ReadFile(metricsPath)
	if err != nil {
		// Fall back to running the command
		out, cmdErr := runInDir(repoAbs, "exo", "heartbeat", "show", "--json")
		if cmdErr != nil {
			return resultError("no heartbeat metrics available — start 'heartbeat_record' first, then wait for metrics to be captured")
		}
		return resultJSON(heartbeatShowResult{
			Status:  "ok",
			RepoDir: repoAbs,
			Output:  strings.TrimSpace(out),
		})
	}

	// Parse and return structured metrics
	var metrics any
	if err := json.Unmarshal(data, &metrics); err != nil {
		return resultJSON(heartbeatShowResult{
			Status:  "ok",
			RepoDir: repoAbs,
			Output:  string(data),
		})
	}

	return resultJSON(heartbeatShowResult{
		Status:  "ok",
		RepoDir: repoAbs,
		Metrics: metrics,
	})
}
