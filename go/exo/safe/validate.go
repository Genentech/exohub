package safe

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// ValidateSSHPrivateKey performs basic validation that the content looks like
// an SSH private key. It checks for PEM headers that indicate a private key.
// This is format validation only — it cannot verify the key works with any server.
func ValidateSSHPrivateKey(content []byte) error {
	s := string(content)

	// Check for common private key PEM headers
	privateKeyHeaders := []string{
		"-----BEGIN OPENSSH PRIVATE KEY-----",
		"-----BEGIN RSA PRIVATE KEY-----",
		"-----BEGIN EC PRIVATE KEY-----",
		"-----BEGIN DSA PRIVATE KEY-----",
		"-----BEGIN PRIVATE KEY-----",
		"-----BEGIN ENCRYPTED PRIVATE KEY-----",
	}

	for _, header := range privateKeyHeaders {
		if strings.Contains(s, header) {
			return nil
		}
	}

	// Check if it looks like a public key instead
	if strings.HasPrefix(strings.TrimSpace(s), "ssh-") ||
		strings.Contains(s, "-----BEGIN PUBLIC KEY-----") ||
		strings.Contains(s, "-----BEGIN SSH2 PUBLIC KEY-----") {
		return fmt.Errorf("file appears to be a public key, not a private key; store the private key instead")
	}

	// If it starts with "-----BEGIN" but doesn't match private key headers
	if strings.Contains(s, "-----BEGIN") {
		return fmt.Errorf("file contains a PEM block but it does not appear to be an SSH private key")
	}

	// Not a PEM file at all
	return fmt.Errorf("file does not contain an SSH private key")
}

// ValidateSSHKeyAgainstHost attempts ssh -i <keyFile> git@<host> to verify
// the key is accepted. Returns nil if accepted, an error with details if not.
// This is a best-effort check — it should warn but not block uploads.
func ValidateSSHKeyAgainstHost(keyFile, host string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Extract port from host if present (e.g. "exogit.example.com:30022")
	hostname := host
	args := []string{
		"-i", keyFile,
		"-T", // no pseudo-terminal
		"-o", "StrictHostKeyChecking=no",
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=3",
	}
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		hostname = host[:idx]
		port := host[idx+1:]
		args = append(args, "-p", port)
	}
	args = append(args, fmt.Sprintf("git@%s", hostname))

	cmd := exec.CommandContext(ctx, "ssh", args...)

	// ssh exits 1 for auth success on git hosts (they print "Hi user!" then disconnect)
	// and exit 255 for connection/auth failure
	output, err := cmd.CombinedOutput()
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if ok && exitErr.ExitCode() == 1 {
			// Exit code 1 typically means auth succeeded but shell was denied
			// (normal for git@host — "successfully authenticated, but GitHub does not provide shell access")
			return nil
		}
		// Timeout or connection refused
		if ok && exitErr.ExitCode() == 255 {
			outStr := strings.ToLower(string(output))
			if strings.Contains(outStr, "permission denied") {
				return fmt.Errorf("host %s rejected the SSH key", host)
			}
			if strings.Contains(outStr, "connection refused") || strings.Contains(outStr, "connection timed out") {
				return fmt.Errorf("could not connect to %s to verify the key", host)
			}
			return fmt.Errorf("SSH connection to %s failed: %s", host, strings.TrimSpace(string(output)))
		}
		return fmt.Errorf("SSH verification failed: %w", err)
	}

	return nil
}

// WarnSSHKeyAgainstHost runs host verification and returns a warning message
// if the key is not accepted, or empty string on success/connection issues.
func WarnSSHKeyAgainstHost(keyFile, host string) string {
	if err := ValidateSSHKeyAgainstHost(keyFile, host); err != nil {
		return fmt.Sprintf("Warning: %v", err)
	}
	return ""
}
