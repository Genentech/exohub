package mcp

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"syscall"

	gomcp "github.com/mark3labs/mcp-go/mcp"
)

type syncResult struct {
	Status  string   `json:"status"`
	Remotes []string `json:"remotes,omitempty"`
	Output  string   `json:"output,omitempty"`
	Message string   `json:"message,omitempty"`
}

func handleSync(_ context.Context, request gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
	repoDir := getStringParam(request, "repo_dir")
	if repoDir == "" {
		return resultError("repo_dir is required")
	}
	if err := requireGitRepo(repoDir); err != nil {
		return resultError("%v", err)
	}

	if err := requireAnnexInit(repoDir); err != nil {
		return resultError("%v", err)
	}

	repoAbs, err := filepath.Abs(repoDir)
	if err != nil {
		return resultError("failed to resolve repo path: %v", err)
	}

	remotes := getStringArrayParam(request, "with")

	// Ensure remotes are initialized via exo init before syncing
	if _, err := runInDir(repoAbs, "exo", "init", "--yes"); err != nil {
		// Non-fatal: remotes may already be configured
	}

	// Build the sync command
	args := []string{"annex", "sync", "--content"}
	args = append(args, remotes...)

	exePath, err := exec.LookPath("git")
	if err != nil {
		return resultError("git not found: %v", err)
	}

	cmd := exec.Command(exePath, args...)
	cmd.Dir = repoAbs
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return resultError("failed to start sync: %v", err)
	}

	backgroundProcs.Track("sync:"+repoAbs, cmd.Process)
	go func() {
		cmd.Wait()
		backgroundProcs.Remove("sync:" + repoAbs)
	}()

	msg := "sync started in background — use 'heartbeat_record' and 'heartbeat_show' to monitor progress"
	return resultJSON(syncResult{
		Status:  "started",
		Remotes: remotes,
		Message: msg,
	})
}

// syncStatus checks if a background sync is still running for the given repo.
func handleSyncStatus(_ context.Context, request gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
	repoDir := getStringParam(request, "repo_dir")
	if repoDir == "" {
		return resultError("repo_dir is required")
	}

	repoAbs, err := filepath.Abs(repoDir)
	if err != nil {
		return resultError("failed to resolve repo path: %v", err)
	}

	running := backgroundProcs.IsRunning("sync:" + repoAbs)

	status := "not_running"
	if running {
		status = "running"
	}

	return resultJSON(syncResult{
		Status:  status,
		Message: fmt.Sprintf("sync is %s for %s", status, repoAbs),
	})
}
