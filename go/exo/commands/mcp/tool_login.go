package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"

	gomcp "github.com/mark3labs/mcp-go/mcp"

	logincmd "github.com/Genentech/exohub/go/exo/commands/login"
)

type loginResult struct {
	Status   string `json:"status"`
	Username string `json:"username,omitempty"`
	Message  string `json:"message,omitempty"`
	AuthURL  string `json:"auth_url,omitempty"`
	UserCode string `json:"user_code,omitempty"`
}

// pendingLogin tracks a background device flow login process.
var pendingLogin struct {
	sync.Mutex
	proc   *os.Process
	result *loginResult // set when process completes
}

func handleLogin(_ context.Context, request gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
	force := false
	if v, ok := request.GetArguments()["force"]; ok {
		if b, ok := v.(bool); ok {
			force = b
		}
	}

	// First, check existing credentials (fast path — no device flow needed)
	if !force {
		tokenFile, err := logincmd.GetTokenFile()
		if err == nil {
			token, err := logincmd.LoadToken(tokenFile)
			if err == nil && token.Valid() {
				return resultJSON(loginResult{
					Status:  "ok",
					Message: "credentials are valid",
				})
			}
			// Try refresh
			if err == nil && token.RefreshToken != "" {
				refreshed, username, refreshErr := logincmd.RefreshAuth(token.RefreshToken)
				if refreshErr == nil {
					_ = logincmd.SaveToken(tokenFile, refreshed)
					return resultJSON(loginResult{
						Status:   "ok",
						Username: username,
						Message:  "token refreshed",
					})
				}
			}
		}
	}

	// Need device flow — start exo login --json in background,
	// read the first JSON line (auth URL), and return it immediately.
	args := []string{"login", "--json"}
	if force {
		args = append(args, "--force")
	}

	exePath, err := os.Executable()
	if err != nil {
		return resultError("failed to find exo binary: %v", err)
	}

	cmd := exec.Command(exePath, args...)
	cmd.Stderr = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return resultError("failed to create pipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		return resultError("failed to start login: %v", err)
	}

	// Read the first JSON line — this is the "waiting" message with the auth URL
	scanner := bufio.NewScanner(stdout)
	var firstLine loginResult
	if scanner.Scan() {
		if err := json.Unmarshal(scanner.Bytes(), &firstLine); err != nil {
			return resultError("failed to parse login output: %v", err)
		}
	} else {
		return resultError("login process produced no output")
	}

	// Track the process for cleanup and completion detection
	pendingLogin.Lock()
	pendingLogin.proc = cmd.Process
	pendingLogin.result = nil
	pendingLogin.Unlock()

	backgroundProcs.Track("login", cmd.Process)

	// Wait for completion in background, capture final result
	go func() {
		var finalResult loginResult
		if scanner.Scan() {
			_ = json.Unmarshal(scanner.Bytes(), &finalResult)
		}
		cmd.Wait()
		backgroundProcs.Remove("login")

		pendingLogin.Lock()
		if finalResult.Status != "" {
			pendingLogin.result = &finalResult
		} else {
			pendingLogin.result = &loginResult{Status: "failed", Message: "login process exited without result"}
		}
		pendingLogin.proc = nil
		pendingLogin.Unlock()
	}()

	// Return the auth URL immediately
	return resultJSON(loginResult{
		Status:   "waiting",
		AuthURL:  firstLine.AuthURL,
		UserCode: firstLine.UserCode,
		Message:  fmt.Sprintf("Show the user this URL to authenticate: %s (code: %s). Then call 'login_status' to check when authentication completes.", firstLine.AuthURL, firstLine.UserCode),
	})
}

func handleLoginStatus(_ context.Context, request gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
	// Check credentials on disk first — most reliable signal that login succeeded
	tokenFile, err := logincmd.GetTokenFile()
	if err == nil {
		token, err := logincmd.LoadToken(tokenFile)
		if err == nil && token.Valid() {
			// Clean up any pending login state
			pendingLogin.Lock()
			pendingLogin.result = nil
			pendingLogin.proc = nil
			pendingLogin.Unlock()
			return resultJSON(loginResult{Status: "ok", Message: "credentials are valid"})
		}
	}

	// Check background process state
	pendingLogin.Lock()
	result := pendingLogin.result
	proc := pendingLogin.proc
	pendingLogin.Unlock()

	if result != nil {
		pendingLogin.Lock()
		pendingLogin.result = nil
		pendingLogin.Unlock()
		return resultJSON(*result)
	}

	if proc != nil {
		return resultJSON(loginResult{
			Status:  "waiting",
			Message: "authentication is still in progress — waiting for user to visit the auth URL",
		})
	}

	// No pending login and no valid credentials
	if tokenFile != "" {
		token, err := logincmd.LoadToken(tokenFile)
		if err == nil && !token.Valid() {
			return resultJSON(loginResult{
				Status:  "expired",
				Message: fmt.Sprintf("credentials expired at %s, call login with force=true", token.Expiry.Format("2006-01-02 15:04:05")),
			})
		}
	}

	return resultJSON(loginResult{Status: "not_authenticated", Message: "no credentials found, call login to authenticate"})
}
