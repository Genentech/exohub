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

func TestInjectAWSProfileEnv_ReplacesExisting(t *testing.T) {
	t.Setenv("EXOHUB_AWS_PROFILE", "")
	cmd := exec.Command("echo")
	cmd.Env = []string{"HOME=/tmp", "AWS_PROFILE=default", "PATH=/usr/bin"}
	InjectAWSProfileEnv(cmd)

	for _, e := range cmd.Env {
		if e == "AWS_PROFILE=default" {
			t.Error("AWS_PROFILE=default should have been replaced")
		}
		if e == "AWS_PROFILE=exohub" {
			return // success
		}
	}
	t.Errorf("AWS_PROFILE=exohub not found in env: %v", cmd.Env)
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
	cmd := Command("echo", "hello")

	found := false
	for _, e := range cmd.Env {
		if strings.HasPrefix(e, "AWS_PROFILE=exohub") {
			found = true
		}
	}
	if !found {
		t.Error("Command() should inject AWS_PROFILE=exohub")
	}
}
