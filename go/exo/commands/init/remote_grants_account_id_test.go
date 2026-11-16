package init

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// fakeGitCommand succeeds (no-op) for `git annex initremote`/`enableremote`
// (the calls under test) and fails fast with no output for everything else
// (e.g. getRemoteUUID's `git config --get`), so createAnnexRemoteInteractive
// and createExportRemoteInteractive see no existing remote/UUID.
func fakeGitCommand(name string, args ...string) *exec.Cmd {
	if name == "git" && len(args) >= 2 && args[0] == "annex" && (args[1] == "initremote" || args[1] == "enableremote" || args[1] == "wanted") {
		return exec.Command("true")
	}
	return exec.Command("false")
}

// withRemoteFlags sets the package-level remote.go flag vars used by the
// create*RemoteInteractive functions and restores them after the test.
func withRemoteFlags(t *testing.T, s3url, grants string, grantsAccountID string, trackingBranch string) {
	t.Helper()
	// loadOrCreatePermissions (triggered when grants is true) writes
	// .exohub/permissions relative to the cwd; run from a scratch dir so
	// these tests never touch the repo working tree.
	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldCwd) })

	oldS3URL, oldGrants, oldGrantsAccountID, oldTrackingBranch := remoteS3URL, remoteGrants, remoteGrantsAccountID, remoteTrackingBranch
	remoteS3URL = s3url
	remoteGrants = grants == "true"
	remoteGrantsAccountID = grantsAccountID
	remoteTrackingBranch = trackingBranch
	t.Cleanup(func() {
		remoteS3URL, remoteGrants, remoteGrantsAccountID, remoteTrackingBranch = oldS3URL, oldGrants, oldGrantsAccountID, oldTrackingBranch
	})
}

// TestCreateAnnexRemoteInteractive_PassesGrantsAccountID verifies that
// createAnnexRemoteInteractive passes grants_account_id=<value> to `git annex
// initremote` when --grants-account-id is set, and omits it otherwise.
func TestCreateAnnexRemoteInteractive_PassesGrantsAccountID(t *testing.T) {
	withRemoteFlags(t, "s3://bucket/_annex", "true", "111122223333", "")

	var calls []string
	withCommand(t, func(name string, args ...string) *exec.Cmd {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return fakeGitCommand(name, args...)
	})

	rc, err := createAnnexRemoteInteractive("backup")
	if err != nil {
		t.Fatalf("createAnnexRemoteInteractive: %v", err)
	}
	if rc.GrantsAccountID != "111122223333" {
		t.Errorf("rc.GrantsAccountID = %q, want %q", rc.GrantsAccountID, "111122223333")
	}

	found := false
	for _, c := range calls {
		if strings.Contains(c, "initremote") && strings.Contains(c, "grants_account_id=111122223333") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an initremote call with grants_account_id=111122223333, got: %v", calls)
	}
}

// TestCreateAnnexRemoteInteractive_NoAccountIDOmitsConfig verifies that when
// --grants-account-id is unset, no grants_account_id= config is passed.
func TestCreateAnnexRemoteInteractive_NoAccountIDOmitsConfig(t *testing.T) {
	withRemoteFlags(t, "s3://bucket/_annex", "true", "", "")

	var calls []string
	withCommand(t, func(name string, args ...string) *exec.Cmd {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return fakeGitCommand(name, args...)
	})

	if _, err := createAnnexRemoteInteractive("backup"); err != nil {
		t.Fatalf("createAnnexRemoteInteractive: %v", err)
	}

	for _, c := range calls {
		if strings.Contains(c, "grants_account_id=") {
			t.Errorf("expected no grants_account_id= in any call, got: %v", calls)
		}
	}
}

// TestCreateExportRemoteInteractive_PassesGrantsAccountID verifies that
// createExportRemoteInteractive passes grants_account_id=<value> to both
// `git annex initremote` and the follow-up `enableremote` call, so two
// remotes with two different accounts get distinct config.
func TestCreateExportRemoteInteractive_PassesGrantsAccountID(t *testing.T) {
	withRemoteFlags(t, "s3://bucket/_export", "true", "444455556666", "main")

	var calls []string
	withCommand(t, func(name string, args ...string) *exec.Cmd {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return fakeGitCommand(name, args...)
	})

	rc, err := createExportRemoteInteractive("catalog-export")
	if err != nil {
		t.Fatalf("createExportRemoteInteractive: %v", err)
	}
	if rc.GrantsAccountID != "444455556666" {
		t.Errorf("rc.GrantsAccountID = %q, want %q", rc.GrantsAccountID, "444455556666")
	}

	var initCall, enableCall string
	for _, c := range calls {
		if strings.Contains(c, "initremote") {
			initCall = c
		}
		if strings.Contains(c, "enableremote") {
			enableCall = c
		}
	}
	if !strings.Contains(initCall, "grants_account_id=444455556666") {
		t.Errorf("expected initremote call to carry grants_account_id=444455556666, got: %q", initCall)
	}
	if !strings.Contains(enableCall, "grants_account_id=444455556666") {
		t.Errorf("expected enableremote call to carry grants_account_id=444455556666, got: %q", enableCall)
	}
}
