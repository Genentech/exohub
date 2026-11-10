package init

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestCreateReadOnlyAnnexRemoteCreatesAndReuses exercises the Z1
// materialisation helper (design D3,
// openspec/changes/add-exohub-asset-import/spike-zero-copy.md "Effort to
// productise"): the first call creates a read-only, grants-enabled remote
// and records it in .exohub/remotes; a second call for the same s3url and
// chunk reuses it instead of creating a second remote (so an `exo add
// exohub:...` importing many files from one source only pays the
// initremote cost once).
func TestCreateReadOnlyAnnexRemoteCreatesAndReuses(t *testing.T) {
	dir := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	created := false
	withCommand(t, func(name string, args ...string) *exec.Cmd {
		switch {
		case name == "git" && len(args) >= 3 && args[0] == "config" && args[1] == "--get" && args[2] == "annex.uuid":
			return exec.Command("sh", "-c", "exit 0") // annex already initialized
		case name == "which" && len(args) == 1 && args[0] == "s5cmd":
			return exec.Command("sh", "-c", "exit 0")
		case name == "git" && len(args) >= 3 && args[0] == "config" && args[1] == "--get" &&
			strings.HasPrefix(args[2], "remote.") && strings.HasSuffix(args[2], ".annex-uuid"):
			if created {
				return exec.Command("sh", "-c", "echo 00000000-0000-0000-0000-000000000001")
			}
			return exec.Command("sh", "-c", "exit 1")
		case name == "git" && len(args) >= 2 && args[0] == "annex" && args[1] == "initremote":
			created = true
			return exec.Command("sh", "-c", "exit 0")
		default:
			return exec.Command("sh", "-c", "exit 1")
		}
	})

	const s3url = "s3://example-bucket/project/_annex"
	const chunk = "1073741824"

	rc, wasCreated, err := CreateReadOnlyAnnexRemote("exohub-import-abc123", s3url, chunk)
	if err != nil {
		t.Fatalf("CreateReadOnlyAnnexRemote() error = %v", err)
	}
	if !wasCreated {
		t.Error("wasCreated = false, want true for the first call")
	}
	if !rc.Readonly {
		t.Error("rc.Readonly = false, want true (defense in depth, exo-cli#147)")
	}
	if !rc.Grants {
		t.Error("rc.Grants = false, want true (required for TRANSFER RETRIEVE, spike-zero-copy.md)")
	}
	if rc.UUID != "00000000-0000-0000-0000-000000000001" {
		t.Errorf("rc.UUID = %q", rc.UUID)
	}

	saved, err := LoadRemotesConfig()
	if err != nil {
		t.Fatalf("LoadRemotesConfig() error = %v", err)
	}
	if len(saved.Remotes) != 1 || saved.Remotes[0].Name != rc.Name {
		t.Errorf("saved remotes = %+v, want one entry named %q", saved.Remotes, rc.Name)
	}

	rc2, wasCreated2, err := CreateReadOnlyAnnexRemote("exohub-import-abc123", s3url, chunk)
	if err != nil {
		t.Fatalf("second CreateReadOnlyAnnexRemote() error = %v", err)
	}
	if wasCreated2 {
		t.Error("wasCreated2 = true, want false (should reuse the existing remote)")
	}
	if rc2.Name != rc.Name {
		t.Errorf("rc2.Name = %q, want %q", rc2.Name, rc.Name)
	}
}

// TestCreateReadOnlyAnnexRemoteReusesExistingEntryUnderDifferentName covers
// exo-cli#152 review item 1: CreateReadOnlyAnnexRemote can find a matching
// .exohub/remotes entry (same s3url+chunk) registered under a name the
// caller didn't ask for; the caller (ensureZ1Remote) must use the returned
// config's own Name, not the name it originally suggested.
func TestCreateReadOnlyAnnexRemoteReusesExistingEntryUnderDifferentName(t *testing.T) {
	dir := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	withCommand(t, func(name string, args ...string) *exec.Cmd {
		// No git/s5cmd call should happen at all: the entry is reused
		// purely from .exohub/remotes, without touching git-annex.
		return exec.Command("sh", "-c", "exit 1")
	})

	const s3url = "s3://example-bucket/project/_annex"
	const chunk = "1073741824"

	existing, err := LoadRemotesConfig()
	if err != nil {
		t.Fatal(err)
	}
	existing.AddRemote(RemoteConfig{
		Name: "a-previously-created-name", Type: "annex", S3URL: s3url, Chunk: chunk,
		Readonly: true, Grants: true, UUID: "00000000-0000-0000-0000-000000000002",
	})
	if err := SaveRemotesConfig(existing); err != nil {
		t.Fatal(err)
	}

	rc, wasCreated, err := CreateReadOnlyAnnexRemote("exohub-import-some-other-name", s3url, chunk)
	if err != nil {
		t.Fatalf("CreateReadOnlyAnnexRemote() error = %v", err)
	}
	if wasCreated {
		t.Error("wasCreated = true, want false (should reuse the existing entry)")
	}
	if rc.Name != "a-previously-created-name" {
		t.Errorf("rc.Name = %q, want the existing entry's own name %q", rc.Name, "a-previously-created-name")
	}
}

// TestCreateReadOnlyAnnexRemoteRefusesWritableMatch covers the other half
// of exo-cli#152 review item 1: an existing .exohub/remotes entry with the
// same s3url+chunk but readonly: false (e.g. the project's own writable
// backup/push remote on that storage) must never be silently reused as a
// Z1 read-only import source.
func TestCreateReadOnlyAnnexRemoteRefusesWritableMatch(t *testing.T) {
	dir := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	withCommand(t, func(name string, args ...string) *exec.Cmd {
		return exec.Command("sh", "-c", "exit 1")
	})

	const s3url = "s3://example-bucket/project/_annex"
	const chunk = "1073741824"

	existing, err := LoadRemotesConfig()
	if err != nil {
		t.Fatal(err)
	}
	existing.AddRemote(RemoteConfig{
		Name: "my-writable-backup", Type: "annex", S3URL: s3url, Chunk: chunk,
		Readonly: false,
	})
	if err := SaveRemotesConfig(existing); err != nil {
		t.Fatal(err)
	}

	if _, _, err := CreateReadOnlyAnnexRemote("exohub-import-xyz", s3url, chunk); err == nil {
		t.Fatal("expected an error refusing to reuse a writable remote as a read-only import source")
	}
}

func TestCreateReadOnlyAnnexRemoteRequiresS5cmd(t *testing.T) {
	dir := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	withCommand(t, func(name string, args ...string) *exec.Cmd {
		return exec.Command("sh", "-c", "exit 1") // nothing is available, including "which"
	})

	if _, _, err := CreateReadOnlyAnnexRemote("exohub-import-abc123", "s3://bucket/prefix", "1GiB"); err == nil {
		t.Fatal("expected an error when s5cmd is not available")
	}
}
