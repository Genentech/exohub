package safe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProvisionSSHKeys(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tempDir)
	t.Setenv("HOME", tempDir)

	entries := []Entry{
		{Name: "gitlab.com", Type: TypeSSHKey, Value: "-----BEGIN OPENSSH PRIVATE KEY-----\nfake-key-1\n-----END OPENSSH PRIVATE KEY-----\n"},
		{Name: "github.com", Type: TypeSSHKey, Value: "-----BEGIN OPENSSH PRIVATE KEY-----\nfake-key-2\n-----END OPENSSH PRIVATE KEY-----\n"},
	}

	n, err := ProvisionSSHKeys(entries)
	if err != nil {
		t.Fatalf("ProvisionSSHKeys() error: %v", err)
	}
	if n != 2 {
		t.Errorf("ProvisionSSHKeys() returned %d, want 2", n)
	}

	sshDir := filepath.Join(tempDir, "exo", "ssh")

	// Verify key files exist with correct permissions
	for _, entry := range entries {
		keyPath := filepath.Join(sshDir, entry.Name)
		info, err := os.Stat(keyPath)
		if err != nil {
			t.Errorf("key file %s not found: %v", entry.Name, err)
			continue
		}
		if info.Mode().Perm() != 0600 {
			t.Errorf("key file %s has permissions %o, want 0600", entry.Name, info.Mode().Perm())
		}
		content, _ := os.ReadFile(keyPath)
		if string(content) != entry.Value {
			t.Errorf("key file %s content mismatch", entry.Name)
		}
	}

	// Verify SSH config was generated
	configPath := filepath.Join(sshDir, "config")
	configContent, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("SSH config not found: %v", err)
	}

	config := string(configContent)
	if !strings.Contains(config, "Host gitlab.com") {
		t.Error("SSH config missing Host gitlab.com")
	}
	if !strings.Contains(config, "Host github.com") {
		t.Error("SSH config missing Host github.com")
	}
	if !strings.Contains(config, "IdentityFile") {
		t.Error("SSH config missing IdentityFile")
	}
	if !strings.Contains(config, "IdentitiesOnly yes") {
		t.Error("SSH config missing IdentitiesOnly")
	}
}

func TestProvisionSSHKeysEmpty(t *testing.T) {
	n, err := ProvisionSSHKeys(nil)
	if err != nil {
		t.Fatalf("ProvisionSSHKeys(nil) error: %v", err)
	}
	if n != 0 {
		t.Errorf("ProvisionSSHKeys(nil) returned %d, want 0", n)
	}
}

func TestProvisionGitCredentials(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tempDir)
	t.Setenv("HOME", tempDir)

	entries := []Entry{
		{Name: "gitlab.com", Type: TypeToken, Value: "my-gitlab-token"},
		{Name: "github.com", Type: TypeToken, Value: "ghp_1234567890"},
	}

	n, err := ProvisionGitCredentials(entries)
	if err != nil {
		t.Fatalf("ProvisionGitCredentials() error: %v", err)
	}
	if n != 2 {
		t.Errorf("ProvisionGitCredentials() returned %d, want 2", n)
	}

	credsFile := filepath.Join(tempDir, "exo", "credentials", "git-credentials")
	content, err := os.ReadFile(credsFile)
	if err != nil {
		t.Fatalf("git-credentials file not found: %v", err)
	}

	creds := string(content)
	if !strings.Contains(creds, "https://token:my-gitlab-token@gitlab.com") {
		t.Error("git-credentials missing gitlab.com entry")
	}
	if !strings.Contains(creds, "https://token:ghp_1234567890@github.com") {
		t.Error("git-credentials missing github.com entry")
	}

	// Verify permissions
	info, _ := os.Stat(credsFile)
	if info.Mode().Perm() != 0600 {
		t.Errorf("git-credentials has permissions %o, want 0600", info.Mode().Perm())
	}
}

func TestProvisionGitCredentialsEmpty(t *testing.T) {
	n, err := ProvisionGitCredentials(nil)
	if err != nil {
		t.Fatalf("ProvisionGitCredentials(nil) error: %v", err)
	}
	if n != 0 {
		t.Errorf("ProvisionGitCredentials(nil) returned %d, want 0", n)
	}
}

func TestHasSSHKeys(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tempDir)
	t.Setenv("HOME", tempDir)

	// No keys yet
	if HasSSHKeys() {
		t.Error("HasSSHKeys() should be false before provisioning")
	}

	// Provision a key
	entries := []Entry{
		{Name: "github.com", Type: TypeSSHKey, Value: "fake-key"},
	}
	_, _ = ProvisionSSHKeys(entries)

	if !HasSSHKeys() {
		t.Error("HasSSHKeys() should be true after provisioning")
	}
}

func TestGitSSHCommand(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tempDir)
	t.Setenv("HOME", tempDir)

	// No keys
	if cmd := GitSSHCommand(); cmd != "" {
		t.Errorf("GitSSHCommand() should be empty, got %q", cmd)
	}

	// Provision
	entries := []Entry{{Name: "github.com", Type: TypeSSHKey, Value: "fake-key"}}
	_, _ = ProvisionSSHKeys(entries)

	cmd := GitSSHCommand()
	if cmd == "" {
		t.Fatal("GitSSHCommand() should not be empty after provisioning")
	}
	if !strings.HasPrefix(cmd, "ssh -F ") {
		t.Errorf("GitSSHCommand() = %q, want prefix 'ssh -F '", cmd)
	}
}

func TestCleanup(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tempDir)
	t.Setenv("HOME", tempDir)

	// Provision SSH keys and git credentials
	sshEntries := []Entry{{Name: "github.com", Type: TypeSSHKey, Value: "fake-key"}}
	_, _ = ProvisionSSHKeys(sshEntries)

	tokenEntries := []Entry{{Name: "github.com", Type: TypeToken, Value: "ghp_token"}}
	_, _ = ProvisionGitCredentials(tokenEntries)

	// Verify they exist
	if !HasSSHKeys() {
		t.Fatal("SSH keys should exist before cleanup")
	}

	// Cleanup
	if err := Cleanup(); err != nil {
		t.Fatalf("Cleanup() error: %v", err)
	}

	// Verify cleaned up
	if HasSSHKeys() {
		t.Error("SSH keys should not exist after cleanup")
	}

	credsFile, _ := GitCredentialsFile()
	if _, err := os.Stat(credsFile); !os.IsNotExist(err) {
		t.Error("git-credentials should not exist after cleanup")
	}
}

func TestCleanupIdempotent(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tempDir)
	t.Setenv("HOME", tempDir)

	// Cleanup with nothing to clean
	if err := Cleanup(); err != nil {
		t.Errorf("Cleanup() on empty state should not error: %v", err)
	}
}

func TestMaxSecretSize(t *testing.T) {
	if MaxSecretSize != 4096 {
		t.Errorf("MaxSecretSize = %d, want 4096", MaxSecretSize)
	}
}

func TestEntryTypes(t *testing.T) {
	if TypeSSHKey != "ssh-key" {
		t.Errorf("TypeSSHKey = %q, want 'ssh-key'", TypeSSHKey)
	}
	if TypeToken != "token" {
		t.Errorf("TypeToken = %q, want 'token'", TypeToken)
	}
}

func TestSSHDirRespectsConfigDir(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("EXO_CONFIG_DIR", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", "") // ensure system default is not used

	got, err := SSHDir()
	if err != nil {
		t.Fatalf("SSHDir() error: %v", err)
	}

	want := filepath.Join(tmpDir, "exo", "ssh")
	if got != want {
		t.Errorf("SSHDir() = %q, want %q", got, want)
	}
}

func TestGitCredentialsFileRespectsConfigDir(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("EXO_CONFIG_DIR", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", "")

	got, err := GitCredentialsFile()
	if err != nil {
		t.Fatalf("GitCredentialsFile() error: %v", err)
	}

	want := filepath.Join(tmpDir, "exo", "credentials", "git-credentials")
	if got != want {
		t.Errorf("GitCredentialsFile() = %q, want %q", got, want)
	}
}

func TestProvisionSSHKeysRespectsConfigDir(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("EXO_CONFIG_DIR", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", "")

	entries := []Entry{
		{Name: "github.com", Type: TypeSSHKey, Value: "-----BEGIN OPENSSH PRIVATE KEY-----\nfakekey\n-----END OPENSSH PRIVATE KEY-----\n"},
	}

	n, err := ProvisionSSHKeys(entries)
	if err != nil {
		t.Fatalf("ProvisionSSHKeys() error: %v", err)
	}
	if n != 1 {
		t.Errorf("ProvisionSSHKeys() = %d, want 1", n)
	}

	keyPath := filepath.Join(tmpDir, "exo", "ssh", "github.com")
	if _, err := os.Stat(keyPath); err != nil {
		t.Errorf("SSH key not found at %s: %v", keyPath, err)
	}
}

func TestProvisionGitCredentialsRespectsConfigDir(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("EXO_CONFIG_DIR", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", "")

	entries := []Entry{
		{Name: "gitlab.com", Type: TypeToken, Value: "mytoken"},
	}

	n, err := ProvisionGitCredentials(entries)
	if err != nil {
		t.Fatalf("ProvisionGitCredentials() error: %v", err)
	}
	if n != 1 {
		t.Errorf("ProvisionGitCredentials() = %d, want 1", n)
	}

	credsPath := filepath.Join(tmpDir, "exo", "credentials", "git-credentials")
	data, err := os.ReadFile(credsPath)
	if err != nil {
		t.Fatalf("git-credentials not found: %v", err)
	}
	if !strings.Contains(string(data), "mytoken") {
		t.Errorf("git-credentials does not contain expected token")
	}
}
