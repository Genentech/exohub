package safe

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Genentech/exohub/go/exo/configdir"
)

// SSHDir returns the path to the exo SSH directory.
// Respects EXO_CONFIG_DIR / --config-dir if set.
func SSHDir() (string, error) {
	exoDir, err := configdir.ExoConfigDir()
	if err != nil {
		return "", fmt.Errorf("failed to get config directory: %w", err)
	}
	return filepath.Join(exoDir, "ssh"), nil
}

// GitCredentialsFile returns the path to the exo git-credentials file.
// Respects EXO_CONFIG_DIR / --config-dir if set.
func GitCredentialsFile() (string, error) {
	exoDir, err := configdir.ExoConfigDir()
	if err != nil {
		return "", fmt.Errorf("failed to get config directory: %w", err)
	}
	return filepath.Join(exoDir, "credentials", "git-credentials"), nil
}

// SSHConfigPath returns the path to the generated SSH config.
func SSHConfigPath() (string, error) {
	sshDir, err := SSHDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(sshDir, "config"), nil
}

// ProvisionSSHKeys writes SSH key entries to ~/.config/exo/ssh/ and generates
// an SSH config file with per-host entries. Keys are expected to be named by
// provider hostname (e.g., "github.com", "gitlab.com").
// Returns the number of keys provisioned.
func ProvisionSSHKeys(entries []Entry) (int, error) {
	if len(entries) == 0 {
		return 0, nil
	}

	sshDir, err := SSHDir()
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		return 0, fmt.Errorf("failed to create SSH directory: %w", err)
	}

	var configLines []string
	count := 0

	for _, entry := range entries {
		keyPath := filepath.Join(sshDir, entry.Name)
		if err := os.WriteFile(keyPath, []byte(entry.Value), 0600); err != nil {
			return count, fmt.Errorf("failed to write SSH key %s: %w", entry.Name, err)
		}
		count++

		configLines = append(configLines,
			fmt.Sprintf("Host %s", entry.Name),
			fmt.Sprintf("    IdentityFile \"%s\"", keyPath),
			"    StrictHostKeyChecking no",
			"    IdentitiesOnly yes",
			"",
		)
	}

	configPath := filepath.Join(sshDir, "config")
	configContent := strings.Join(configLines, "\n") + "\n"
	if err := os.WriteFile(configPath, []byte(configContent), 0600); err != nil {
		return count, fmt.Errorf("failed to write SSH config: %w", err)
	}

	return count, nil
}

// ProvisionGitCredentials writes git tokens to the exo git-credentials file
// in the git credential-store format: https://<username>:<token>@<host>
// Each entry should be named by provider hostname.
func ProvisionGitCredentials(entries []Entry) (int, error) {
	if len(entries) == 0 {
		return 0, nil
	}

	credsFile, err := GitCredentialsFile()
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(credsFile), 0700); err != nil {
		return 0, fmt.Errorf("failed to create credentials directory: %w", err)
	}

	var lines []string
	for _, entry := range entries {
		// Format: https://token:<token>@<host>
		// Using "token" as username is a common convention for token-based auth
		lines = append(lines, fmt.Sprintf("https://token:%s@%s", entry.Value, entry.Name))
	}

	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(credsFile, []byte(content), 0600); err != nil {
		return 0, fmt.Errorf("failed to write git credentials: %w", err)
	}

	return len(entries), nil
}

// HasSSHKeys returns true if the exo SSH directory contains keys.
func HasSSHKeys() bool {
	configPath, err := SSHConfigPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(configPath)
	return err == nil
}

// GitSSHCommand returns the GIT_SSH_COMMAND value to use for git subprocesses,
// or empty string if no exo SSH config exists.
func GitSSHCommand() string {
	configPath, err := SSHConfigPath()
	if err != nil {
		return ""
	}
	if _, err := os.Stat(configPath); err != nil {
		return ""
	}
	return fmt.Sprintf("ssh -F %s", configPath)
}

// Cleanup removes all provisioned safe credentials from the local filesystem.
func Cleanup() error {
	sshDir, err := SSHDir()
	if err != nil {
		return err
	}
	if err := os.RemoveAll(sshDir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove SSH directory: %w", err)
	}

	credsFile, err := GitCredentialsFile()
	if err != nil {
		return err
	}
	if err := os.Remove(credsFile); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove git credentials: %w", err)
	}

	return nil
}
