package logging

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Genentech/exohub/go/exo/commandutil"
)

const (
	logsDir  = ".git/exohub/logs"
	debugEnv = "EXOHUB_CLI_DEBUG"
)

func ExecuteWithLogging(scope, remote string, cmd []string, paths []string, jobs string) error {
	return ExecuteWithLoggingPrefix(scope, remote, cmd, paths, jobs, "")
}

func ExecuteWithLoggingPrefix(scope, remote string, cmd []string, paths []string, jobs string, logPrefix string) error {
	return ExecuteWithLoggingPrefixRetry(scope, remote, cmd, paths, jobs, logPrefix, true)
}

// ExecuteWithLoggingPrefixRetry executes a command with logging and optional credential error retry
func ExecuteWithLoggingPrefixRetry(scope, remote string, cmd []string, paths []string, jobs string, logPrefix string, retryOnCredError bool) error {
	logfile, metafile := buildLogPaths(scope, remote, jobs, paths, false, logPrefix)

	EnsureLogsDir()
	writeMeta(metafile, scope, remote, cmd, paths, jobs, nil)

	err := runCommandLogged(cmd, logfile)
	if err == nil {
		return nil
	}

	// Check if we should retry on credential errors
	if !retryOnCredError {
		return err
	}

	// Read the log file to check for credential errors
	logContent, readErr := os.ReadFile(logfile)
	if readErr != nil {
		return err // Return original error if we can't read the log
	}

	// Check if the error is credential-related
	shouldRetry, loginErr := commandutil.HandleCredentialError(string(logContent))
	if loginErr != nil {
		fmt.Fprintf(os.Stderr, "Failed to refresh credentials: %v\n", loginErr)
		return err // Return original error
	}

	if !shouldRetry {
		return err // Not a credential error, return original error
	}

	// Retry the operation with fresh credentials
	fmt.Println("🔄 Retrying operation with refreshed credentials...")
	logfileRetry, metafileRetry := buildLogPaths(scope, remote, jobs, paths, false, logPrefix+"_retry")
	writeMeta(metafileRetry, scope, remote, cmd, paths, jobs, map[string]any{"retry_after_cred_refresh": true})
	return runCommandLogged(cmd, logfileRetry)
}

func MustCwd() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return cwd
}

func EnsureLogsDir() {
	_ = os.MkdirAll(logsDir, 0o755)
}

func RecordDryRunLog(scope, remote string, cmd []string, paths []string, jobs string, content string, extras map[string]any) error {
	return RecordDryRunLogPrefix(scope, remote, cmd, paths, jobs, content, extras, "")
}

func RecordDryRunLogPrefix(scope, remote string, cmd []string, paths []string, jobs string, content string, extras map[string]any, logPrefix string) error {
	logfile, metafile := buildLogPaths(scope, remote, jobs, paths, true, logPrefix)

	EnsureLogsDir()
	writeMeta(metafile, scope, remote, cmd, paths, jobs, extras)

	if content == "" {
		content = "\n"
	} else if !strings.HasSuffix(content, "\n") {
		content = content + "\n"
	}
	return os.WriteFile(logfile, []byte(content), 0o644)
}

func buildLogPaths(scope, remote, jobs string, paths []string, dryRun bool, logPrefix string) (string, string) {
	ts := time.Now().UTC().Format("20060102T150405Z")
	pid := os.Getpid()
	remote = strings.TrimSpace(remote)
	if remote == "" {
		remote = "none"
	}
	drySuffix := ""
	if dryRun {
		drySuffix = "_dry-run"
	}

	var base string
	if logPrefix != "" {
		// Use custom prefix for Temporal integration: prefix_scope_remote_dry-run_timestamp_pid_jobs
		base = fmt.Sprintf("%s_%s_%s%s_%s_pid%d_j%s", logPrefix, scope, remote, drySuffix, ts, pid, jobs)
	} else {
		// Original format: scope_remote_dry-run_timestamp_pid_jobs
		base = fmt.Sprintf("%s_%s%s_%s_pid%d_j%s", scope, remote, drySuffix, ts, pid, jobs)
	}

	if len(paths) > 0 {
		base = fmt.Sprintf("%s_paths%d", base, len(paths))
	}
	logfile := filepath.Join(logsDir, base+".log")
	metafile := filepath.Join(logsDir, base+".meta.json")
	return logfile, metafile
}

func runCommandLogged(args []string, logfile string) error {
	debugCmd(args...)
	pathEnv := os.Getenv("PATH")
	cmdString := strings.Join(QuoteArgs(args), " ")
	
	// Use stdbuf on Linux for unbuffered output, but it's not available on macOS
	// On macOS, commands are already unbuffered by default
	stdbufCmd := ""
	if commandExists("stdbuf") {
		stdbufCmd = "stdbuf -oL -eL "
	}
	
	shellCmd := fmt.Sprintf("set -o pipefail; PATH=%s %s%s 2>&1 | tee -a %s",
		ShellQuote(pathEnv),
		stdbufCmd,
		cmdString,
		ShellQuote(logfile),
	)
	cmd := commandutil.Command("bash", "-lc", shellCmd)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

// commandExists checks if a command is available in PATH
func commandExists(name string) bool {
	cmd := commandutil.Command("which", name)
	return cmd.Run() == nil
}

func writeMeta(path, scope, remote string, cmd []string, paths []string, jobs string, extras map[string]any) {
	jobCount := 0
	if jobs != "" {
		if n, err := strconv.Atoi(jobs); err == nil {
			jobCount = n
		}
	}
	meta := map[string]any{
		"ts":         time.Now().UTC().Format(time.RFC3339),
		"pid":        os.Getpid(),
		"scope":      scope,
		"remote":     remote,
		"jobs":       jobCount,
		"annex_uuid": getConfig("annex.uuid"),
		"git": map[string]string{
			"head_sha": getGit("rev-parse", "HEAD"),
			"head_ref": getGit("rev-parse", "--abbrev-ref", "HEAD"),
		},
		"cwd":   MustCwd(),
		"cmd":   cmd,
		"paths": paths,
	}
	if extras != nil {
		for k, v := range extras {
			meta[k] = v
		}
	}
	data, _ := json.MarshalIndent(meta, "", "  ")
	_ = os.WriteFile(path, append(data, '\n'), 0o644)
}

func getGit(args ...string) string {
	out, err := runCommandOutput(append([]string{"git"}, args...))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func getConfig(key string) string {
	out, err := runCommandOutput([]string{"git", "config", "--get", key})
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func runCommandOutput(args []string) (string, error) {
	cmd := commandutil.Command(args[0], args[1:]...)
	out, err := cmd.Output()
	return string(out), err
}

func QuoteArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for _, arg := range args {
		out = append(out, ShellQuote(arg))
	}
	return out
}

func ShellQuote(val string) string {
	if val == "" {
		return "''"
	}
	if strings.ContainsAny(val, " \t\n\"'\\$") {
		return "'" + strings.ReplaceAll(val, "'", "'\"'\"'") + "'"
	}
	return val
}

func debugCmd(args ...string) {
	if os.Getenv(debugEnv) != "1" {
		return
	}
	var quoted []string
	for _, arg := range args {
		quoted = append(quoted, ShellQuote(arg))
	}
	fmt.Fprintf(os.Stderr, "DEBUG: %s\n", strings.Join(quoted, " "))
}
