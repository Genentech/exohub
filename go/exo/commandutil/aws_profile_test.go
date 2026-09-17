package commandutil

import (
	"os/exec"
	"strings"
	"testing"
)

func TestExohubAWSProfileDefault(t *testing.T) {
	t.Setenv("EXOHUB_AWS_PROFILE", "")
	if got := exohubAWSProfile(); got != "exohub" {
		t.Errorf("exohubAWSProfile() = %q, want %q", got, "exohub")
	}
}

func TestExohubAWSProfileOverride(t *testing.T) {
	t.Setenv("EXOHUB_AWS_PROFILE", "custom-profile")
	if got := exohubAWSProfile(); got != "custom-profile" {
		t.Errorf("exohubAWSProfile() = %q, want %q", got, "custom-profile")
	}
}

func TestInjectAWSProfileEnv_SetsDefault(t *testing.T) {
	t.Setenv("EXOHUB_AWS_PROFILE", "")
	cmd := exec.Command("echo")
	cmd.Env = []string{"HOME=/tmp", "PATH=/usr/bin"}
	InjectAWSProfileEnv(cmd)

	found := false
	for _, e := range cmd.Env {
		if e == "AWS_PROFILE=exohub" {
			found = true
		}
	}
	if !found {
		t.Errorf("AWS_PROFILE=exohub not found in env: %v", cmd.Env)
	}
}

func TestInjectAWSProfileEnv_RespectsExistingAWSProfile(t *testing.T) {
	t.Setenv("EXOHUB_AWS_PROFILE", "")
	t.Setenv("AWS_PROFILE", "user-profile")
	cmd := exec.Command("echo")
	cmd.Env = []string{"HOME=/tmp", "AWS_PROFILE=user-profile", "PATH=/usr/bin"}
	InjectAWSProfileEnv(cmd)

	// The user's value must be preserved; exohub profile must not appear.
	for _, e := range cmd.Env {
		if e == "AWS_PROFILE=exohub" {
			t.Error("AWS_PROFILE=exohub should not have been injected when user already set AWS_PROFILE")
		}
	}
}

func TestInjectAWSProfileEnv_UsesOverride(t *testing.T) {
	t.Setenv("EXOHUB_AWS_PROFILE", "my-profile")
	cmd := exec.Command("echo")
	cmd.Env = []string{"HOME=/tmp"}
	InjectAWSProfileEnv(cmd)

	found := false
	for _, e := range cmd.Env {
		if e == "AWS_PROFILE=my-profile" {
			found = true
		}
	}
	if !found {
		t.Errorf("AWS_PROFILE=my-profile not found in env: %v", cmd.Env)
	}
}

func TestInjectAWSProfileEnv_NilEnv(t *testing.T) {
	t.Setenv("EXOHUB_AWS_PROFILE", "")
	cmd := exec.Command("echo")
	cmd.Env = nil // should initialize from os.Environ()
	InjectAWSProfileEnv(cmd)

	if cmd.Env == nil {
		t.Fatal("cmd.Env should not be nil after injection")
	}

	found := false
	for _, e := range cmd.Env {
		if strings.HasPrefix(e, "AWS_PROFILE=exohub") {
			found = true
		}
	}
	if !found {
		t.Errorf("AWS_PROFILE=exohub not found in env")
	}
}

func TestInjectAWSProfileEnv_NoDuplicates(t *testing.T) {
	t.Setenv("EXOHUB_AWS_PROFILE", "")
	cmd := exec.Command("echo")
	cmd.Env = []string{"AWS_PROFILE=old", "HOME=/tmp"}
	InjectAWSProfileEnv(cmd)

	count := 0
	for _, e := range cmd.Env {
		if strings.HasPrefix(e, "AWS_PROFILE=") {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 AWS_PROFILE entry, got %d in: %v", count, cmd.Env)
	}
}

func TestCommandInjectsAWSProfile(t *testing.T) {
	t.Setenv("EXOHUB_AWS_PROFILE", "")
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("EXO_NO_AWS_PROFILE_INJECT", "")
	cmd := Command("echo", "hello")

	found := false
	for _, e := range cmd.Env {
		if strings.HasPrefix(e, "AWS_PROFILE=exohub") {
			found = true
		}
	}
	if !found {
		t.Error("Command() should inject AWS_PROFILE=exohub when no user AWS vars are set")
	}
}

func TestInjectAWSProfileEnv_SkipsWhenAccessKeyIDSet(t *testing.T) {
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_ACCESS_KEY_ID", "test-access-key-id")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("EXO_NO_AWS_PROFILE_INJECT", "")
	cmd := exec.Command("echo")
	InjectAWSProfileEnv(cmd)

	for _, e := range cmd.Env {
		if strings.HasPrefix(e, "AWS_PROFILE=") {
			t.Errorf("AWS_PROFILE should not be injected when AWS_ACCESS_KEY_ID is set, got %s", e)
		}
	}
}

func TestInjectAWSProfileEnv_SkipsWhenSecretKeySet(t *testing.T) {
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "supersecret")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("EXO_NO_AWS_PROFILE_INJECT", "")
	cmd := exec.Command("echo")
	InjectAWSProfileEnv(cmd)

	for _, e := range cmd.Env {
		if strings.HasPrefix(e, "AWS_PROFILE=") {
			t.Errorf("AWS_PROFILE should not be injected when AWS_SECRET_ACCESS_KEY is set, got %s", e)
		}
	}
}

func TestInjectAWSProfileEnv_SkipsWhenSessionTokenSet(t *testing.T) {
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	t.Setenv("AWS_SESSION_TOKEN", "mytoken")
	t.Setenv("EXO_NO_AWS_PROFILE_INJECT", "")
	cmd := exec.Command("echo")
	InjectAWSProfileEnv(cmd)

	for _, e := range cmd.Env {
		if strings.HasPrefix(e, "AWS_PROFILE=") {
			t.Errorf("AWS_PROFILE should not be injected when AWS_SESSION_TOKEN is set, got %s", e)
		}
	}
}

func TestInjectAWSProfileEnv_SkipsWhenOptOutSet(t *testing.T) {
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("EXO_NO_AWS_PROFILE_INJECT", "1")
	cmd := exec.Command("echo")
	InjectAWSProfileEnv(cmd)

	for _, e := range cmd.Env {
		if strings.HasPrefix(e, "AWS_PROFILE=") {
			t.Errorf("AWS_PROFILE should not be injected when EXO_NO_AWS_PROFILE_INJECT=1, got %s", e)
		}
	}
}

func TestInjectAWSProfileEnv_SkipsCredentialsFileWhenUserSet(t *testing.T) {
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("EXO_NO_AWS_PROFILE_INJECT", "")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "/home/user/.aws/credentials")
	cmd := exec.Command("echo")
	cmd.Env = []string{"HOME=/tmp", "AWS_SHARED_CREDENTIALS_FILE=/home/user/.aws/credentials"}
	InjectAWSProfileEnv(cmd)

	for _, e := range cmd.Env {
		if strings.HasPrefix(e, "AWS_SHARED_CREDENTIALS_FILE=") && e != "AWS_SHARED_CREDENTIALS_FILE=/home/user/.aws/credentials" {
			t.Errorf("AWS_SHARED_CREDENTIALS_FILE should not be overridden, got %s", e)
		}
	}
}

func TestInjectAWSProfileEnv_InjectsWhenNoUserAWSVars(t *testing.T) {
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("EXO_NO_AWS_PROFILE_INJECT", "")
	t.Setenv("EXOHUB_AWS_PROFILE", "grants-true-profile")
	cmd := exec.Command("echo")
	cmd.Env = []string{"HOME=/tmp"}
	InjectAWSProfileEnv(cmd)

	found := false
	for _, e := range cmd.Env {
		if e == "AWS_PROFILE=grants-true-profile" {
			found = true
		}
	}
	if !found {
		t.Errorf("AWS_PROFILE=grants-true-profile should be injected when no user AWS vars are set, env: %v", cmd.Env)
	}
}
