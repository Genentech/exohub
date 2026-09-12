package commandutil

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Genentech/exohub/go/exo/configdir"
)

const debugEnv = "EXOHUB_CLI_DEBUG"

func Command(name string, args ...string) *exec.Cmd {
	if os.Getenv(debugEnv) == "1" {
		quoted := make([]string, 0, len(args)+1)
		quoted = append(quoted, shellQuote(name))
		for _, arg := range args {
			quoted = append(quoted, shellQuote(arg))
		}
		fmt.Fprintf(os.Stderr, "DEBUG: %s\n", strings.Join(quoted, " "))
	}
	cmd := exec.Command(name, args...)
	if name == "git" {
		InjectGitSSHEnv(cmd)
	}
	InjectAWSProfileEnv(cmd)
	return cmd
}

// InjectGitSSHEnv sets GIT_SSH_COMMAND on the given exec.Cmd if exo-managed
// SSH keys exist and the env var is not already set by the user.
func InjectGitSSHEnv(cmd *exec.Cmd) {
	// Don't override if already set in the process environment
	if os.Getenv("GIT_SSH_COMMAND") != "" {
		return
	}
	sshCmd := gitSSHCommand()
	if sshCmd == "" {
		return
	}
	if cmd.Env == nil {
		cmd.Env = os.Environ()
	}
	cmd.Env = append(cmd.Env, "GIT_SSH_COMMAND="+sshCmd)
}

// gitSSHCommand returns the GIT_SSH_COMMAND value pointing to the exo SSH
// config, or empty string if no exo SSH config exists.
func gitSSHCommand() string {
	exoDir, err := configdir.ExoConfigDir()
	if err != nil {
		return ""
	}
	configPath := filepath.Join(exoDir, "ssh", "config")
	if _, err := os.Stat(configPath); err != nil {
		return ""
	}
	return fmt.Sprintf("ssh -F '%s'", configPath)
}

// InjectAWSProfileEnv sets AWS_PROFILE (and, when EXO_CONFIG_DIR is active,
// AWS_SHARED_CREDENTIALS_FILE) on the given exec.Cmd so that AWS SDK calls
// (including git-annex S3 operations) use the credentials written by exo login.
//
// Injection is skipped when the user has already supplied any AWS identity via
// the environment (AWS_PROFILE, AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, or
// AWS_SESSION_TOKEN), or when EXO_NO_AWS_PROFILE_INJECT=1 is set.
func InjectAWSProfileEnv(cmd *exec.Cmd) {
	// Respect user-supplied AWS identity — mirror InjectGitSSHEnv convention.
	if os.Getenv("EXO_NO_AWS_PROFILE_INJECT") == "1" {
		return
	}
	if os.Getenv("AWS_PROFILE") != "" ||
		os.Getenv("AWS_ACCESS_KEY_ID") != "" ||
		os.Getenv("AWS_SECRET_ACCESS_KEY") != "" ||
		os.Getenv("AWS_SESSION_TOKEN") != "" {
		return
	}

	profile := exohubAWSProfile()

	if cmd.Env == nil {
		cmd.Env = os.Environ()
	}

	// Append AWS_PROFILE; deduplicate so tools reading the first entry are correct.
	found := false
	for i, e := range cmd.Env {
		if strings.HasPrefix(e, "AWS_PROFILE=") {
			cmd.Env[i] = "AWS_PROFILE=" + profile
			found = true
			break
		}
	}
	if !found {
		cmd.Env = append(cmd.Env, "AWS_PROFILE="+profile)
	}

	// When a config root is active, also propagate AWS_SHARED_CREDENTIALS_FILE
	// so that git-annex and external remote helpers use the isolated creds file.
	// Skip if the user has already set it.
	if os.Getenv("AWS_SHARED_CREDENTIALS_FILE") == "" {
		if credsFile, err := configdir.AWSCredentialsFile(); err == nil && configdir.Root() != "" {
			setOrReplace(cmd, "AWS_SHARED_CREDENTIALS_FILE", credsFile)
		}
	}
}

func setOrReplace(cmd *exec.Cmd, key, value string) {
	prefix := key + "="
	for i, e := range cmd.Env {
		if strings.HasPrefix(e, prefix) {
			cmd.Env[i] = prefix + value
			return
		}
	}
	cmd.Env = append(cmd.Env, prefix+value)
}

func exohubAWSProfile() string {
	if p := os.Getenv("EXOHUB_AWS_PROFILE"); p != "" {
		return p
	}
	return "exohub"
}

func shellQuote(val string) string {
	if val == "" {
		return "''"
	}
	val = strings.ReplaceAll(val, "\n", "\\n")
	if strings.ContainsAny(val, " \t\n\"'\\$") {
		return "'" + strings.ReplaceAll(val, "'", "'\"'\"'") + "'"
	}
	return val
}

// IsDebug returns true if EXOHUB_CLI_DEBUG=1
func IsDebug() bool {
	return os.Getenv(debugEnv) == "1"
}

// DebugHTTP logs an HTTP request method and URL when debug mode is enabled
func DebugHTTP(method, url string) {
	if IsDebug() {
		fmt.Fprintf(os.Stderr, "DEBUG HTTP: %s %s\n", method, url)
	}
}
