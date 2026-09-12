package auth

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/ini.v1"
)

func TestWriteAWSCredentials(t *testing.T) {
	tempDir := t.TempDir()
	credPath := filepath.Join(tempDir, "credentials")

	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", credPath)
	t.Setenv("EXOHUB_AWS_PROFILE", "test-profile")

	creds := AWSCredentials{
		AccessKeyID:     "TEST_ACCESS_KEY_ID_123",
		SecretAccessKey: "TEST_SECRET_ACCESS_KEY_456",
		SessionToken:    "test-session-token-12345",
	}

	if err := WriteAWSCredentials(creds); err != nil {
		t.Fatalf("WriteAWSCredentials() failed: %v", err)
	}

	info, err := os.Stat(credPath)
	if err != nil {
		t.Fatalf("credentials file not created: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("permissions: got %o, want 0600", info.Mode().Perm())
	}

	cfg, err := ini.Load(credPath)
	if err != nil {
		t.Fatalf("failed to parse credentials file: %v", err)
	}
	section := cfg.Section("test-profile")
	assertKey(t, section, "aws_access_key_id", creds.AccessKeyID)
	assertKey(t, section, "aws_secret_access_key", creds.SecretAccessKey)
	assertKey(t, section, "aws_session_token", creds.SessionToken)
}

func TestWriteAWSCredentialsDefaultProfile(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(tempDir, "credentials"))
	t.Setenv("EXOHUB_AWS_PROFILE", "")

	creds := AWSCredentials{AccessKeyID: "KEY", SecretAccessKey: "SECRET", SessionToken: "TOKEN"}
	if err := WriteAWSCredentials(creds); err != nil {
		t.Fatalf("WriteAWSCredentials() failed: %v", err)
	}

	cfg, err := ini.Load(filepath.Join(tempDir, "credentials"))
	if err != nil {
		t.Fatalf("failed to parse credentials file: %v", err)
	}
	assertKey(t, cfg.Section("exohub"), "aws_access_key_id", "KEY")
}

func TestWriteAWSCredentialsOverwrite(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(tempDir, "credentials"))
	t.Setenv("EXOHUB_AWS_PROFILE", "overwrite-test")

	first := AWSCredentials{AccessKeyID: "FIRST", SecretAccessKey: "S1", SessionToken: "T1"}
	second := AWSCredentials{AccessKeyID: "SECOND", SecretAccessKey: "S2", SessionToken: "T2"}

	if err := WriteAWSCredentials(first); err != nil {
		t.Fatalf("first write failed: %v", err)
	}
	if err := WriteAWSCredentials(second); err != nil {
		t.Fatalf("second write failed: %v", err)
	}

	cfg, _ := ini.Load(filepath.Join(tempDir, "credentials"))
	assertKey(t, cfg.Section("overwrite-test"), "aws_access_key_id", "SECOND")
}

func TestWriteAWSCredentialsMultipleProfiles(t *testing.T) {
	tempDir := t.TempDir()
	credPath := filepath.Join(tempDir, "credentials")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", credPath)

	t.Setenv("EXOHUB_AWS_PROFILE", "profile1")
	if err := WriteAWSCredentials(AWSCredentials{AccessKeyID: "P1KEY", SecretAccessKey: "S", SessionToken: "T"}); err != nil {
		t.Fatal(err)
	}

	t.Setenv("EXOHUB_AWS_PROFILE", "profile2")
	if err := WriteAWSCredentials(AWSCredentials{AccessKeyID: "P2KEY", SecretAccessKey: "S", SessionToken: "T"}); err != nil {
		t.Fatal(err)
	}

	cfg, _ := ini.Load(credPath)
	assertKey(t, cfg.Section("profile1"), "aws_access_key_id", "P1KEY")
	assertKey(t, cfg.Section("profile2"), "aws_access_key_id", "P2KEY")
}

func assertKey(t *testing.T, section *ini.Section, key, want string) {
	t.Helper()
	got := section.Key(key).String()
	if got != want {
		t.Errorf("key %q: got %q, want %q", key, got, want)
	}
}
