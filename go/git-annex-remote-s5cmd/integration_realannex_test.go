package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildS5cmdBinaryForTest builds the current package into a temp directory as
// "git-annex-remote-s5cmd" and returns that directory, so it can be prepended
// to PATH for real git-annex to exec. Skips the test if git-annex itself is
// not on PATH (CI's integration job has it; see exo-cli#131 review).
func buildS5cmdBinaryForTest(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git-annex"); err != nil {
		t.Skip("git-annex not available")
	}

	pkgDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}

	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "git-annex-remote-s5cmd")
	cmd := exec.Command("go", "build", "-o", binPath, ".")
	cmd.Dir = pkgDir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("go build git-annex-remote-s5cmd: %v\n%s", err, out.String())
	}
	return binDir
}

// runGitAnnex runs a command (typically git/git-annex) in dir and returns its
// combined output, failing the test on error with that output attached.
func runGitAnnex(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// remoteLogLine returns the git-annex:remote.log line for a remote named
// name, or "" if not found.
func remoteLogLine(t *testing.T, dir, name string) string {
	t.Helper()
	cmd := exec.Command("git", "show", "git-annex:remote.log")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git show git-annex:remote.log: %v\n%s", err, out)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "name="+name+" ") || strings.HasSuffix(line, "name="+name) {
			return line
		}
	}
	return ""
}

// TestRealGitAnnexAcceptsGroupConfig is a regression test for exo-cli#131
// review blocker 1: LISTCONFIGS must declare "group", or real git-annex
// rejects it with "Unexpected parameters: group" on both initremote and
// enableremote. Verifies initremote with group=, enableremote changing the
// group, and enableremote clearing it to empty, each against real git-annex
// (not the protocol-level unit tests elsewhere in this package).
func TestRealGitAnnexAcceptsGroupConfig(t *testing.T) {
	binDir := buildS5cmdBinaryForTest(t)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	repoDir := t.TempDir()
	runGitAnnex(t, repoDir, "git", "init", "-q")
	runGitAnnex(t, repoDir, "git", "config", "user.email", "test@example.com")
	runGitAnnex(t, repoDir, "git", "config", "user.name", "Test")
	runGitAnnex(t, repoDir, "git", "annex", "init", "-q", "test")

	// initremote ... group=<g> must succeed (blocker 1).
	runGitAnnex(t, repoDir, "git", "annex", "initremote", "r1",
		"type=external", "externaltype=s5cmd", "encryption=none",
		"s3url=s3://test-bucket/prefix", "group=GRP-Y")

	if got := remoteLogLine(t, repoDir, "r1"); !strings.Contains(got, "group=GRP-Y") {
		t.Errorf("remote.log after initremote with group=GRP-Y = %q, want it to contain group=GRP-Y", got)
	}

	// enableremote ... group=<g> must succeed and change the recorded group.
	runGitAnnex(t, repoDir, "git", "annex", "enableremote", "r1", "group=GRP-X")
	if got := remoteLogLine(t, repoDir, "r1"); !strings.Contains(got, "group=GRP-X") {
		t.Errorf("remote.log after enableremote group=GRP-X = %q, want it to contain group=GRP-X", got)
	}

	// enableremote ... group= (empty) must succeed and clear it.
	runGitAnnex(t, repoDir, "git", "annex", "enableremote", "r1", "group=")
	if got := remoteLogLine(t, repoDir, "r1"); strings.Contains(got, "group=GRP-X") || strings.Contains(got, "group=GRP-Y") {
		t.Errorf("remote.log after enableremote group= (clear) = %q, want no stale group value", got)
	}
}

// TestRealGitAnnexAcceptsGroupOnEnableWithoutInit covers enableremote
// (without group=) immediately followed by enableremote with a group, on a
// freshly initremote'd remote that never had one.
func TestRealGitAnnexAcceptsGroupOnEnableWithoutInit(t *testing.T) {
	binDir := buildS5cmdBinaryForTest(t)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	repoDir := t.TempDir()
	runGitAnnex(t, repoDir, "git", "init", "-q")
	runGitAnnex(t, repoDir, "git", "config", "user.email", "test@example.com")
	runGitAnnex(t, repoDir, "git", "config", "user.name", "Test")
	runGitAnnex(t, repoDir, "git", "annex", "init", "-q", "test")

	runGitAnnex(t, repoDir, "git", "annex", "initremote", "r2",
		"type=external", "externaltype=s5cmd", "encryption=none",
		"s3url=s3://test-bucket/prefix2")

	runGitAnnex(t, repoDir, "git", "annex", "enableremote", "r2", "group=GRP-X")
	if got := remoteLogLine(t, repoDir, "r2"); !strings.Contains(got, "group=GRP-X") {
		t.Errorf("remote.log after enableremote group=GRP-X = %q, want it to contain group=GRP-X", got)
	}
}
