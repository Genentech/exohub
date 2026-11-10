package init

import (
	"os/exec"
	"path/filepath"
	"testing"
)

var noRemotesSpec = &ProfileSpec{Name: "my-profile"}

func TestResolveFolderExplicitOverrideWins(t *testing.T) {
	chdirTemp(t)

	withCommand(t, func(name string, args ...string) *exec.Cmd {
		return exec.Command("printf", "https://git.example.com/org/should-not-be-used.git")
	})

	folder, source := resolveFolder("explicit-name", noRemotesSpec, "jdoe", nil, nil, "/some/dir/whatever")
	if folder != "explicit-name" || source != folderSourceOverride {
		t.Errorf("resolveFolder = (%q, %q), want (explicit-name, %q)", folder, source, folderSourceOverride)
	}
}

func TestResolveFolderFromProfileLock(t *testing.T) {
	chdirTemp(t)

	withCommand(t, func(name string, args ...string) *exec.Cmd {
		return exec.Command("printf", "https://git.example.com/org/should-not-be-used.git")
	})

	if err := SaveProfileLock(&ProfileLock{Profile: "my-profile", Folder: "DS000000001"}); err != nil {
		t.Fatalf("SaveProfileLock: %v", err)
	}

	folder, source := resolveFolder("", noRemotesSpec, "jdoe", nil, nil, "/some/renamed-scratch-dir")
	if folder != "DS000000001" || source != folderSourceLock {
		t.Errorf("resolveFolder = (%q, %q), want (DS000000001, %q)", folder, source, folderSourceLock)
	}
}

func TestResolveFolderFromExistingRemoteBeforeGitOrigin(t *testing.T) {
	chdirTemp(t)

	withCommand(t, func(name string, args ...string) *exec.Cmd {
		if name == "git" && len(args) >= 2 && args[0] == "remote" && args[1] == "get-url" {
			return exec.Command("printf", "git@git.example.com:example-org/datasets/should-not-be-used.git")
		}
		return exec.Command("sh", "-c", "exit 1")
	})

	spec := &ProfileSpec{
		Name: "my-profile",
		Templates: map[string]string{
			"remotes": "remotes:\n  - name: exohub-annex\n    type: annex\n    s3url: s3://bucket/{{.Folder}}/_annex\n",
		},
	}
	existing := &RemotesConfig{Remotes: []RemoteConfig{
		{Name: "exohub-annex", Type: "annex", S3URL: "s3://bucket/DS000000001/_annex"},
	}}

	folder, source := resolveFolder("", spec, "jdoe", map[string]interface{}{}, existing, "/home/user/ds-migrate")
	if folder != "DS000000001" || source != folderSourceExistingRemote {
		t.Errorf("resolveFolder = (%q, %q), want (DS000000001, %q) — existing remote prefix must win over git origin", folder, source, folderSourceExistingRemote)
	}
}

func TestResolveFolderFromGitOrigin(t *testing.T) {
	chdirTemp(t)

	withCommand(t, func(name string, args ...string) *exec.Cmd {
		if name == "git" && len(args) >= 2 && args[0] == "remote" && args[1] == "get-url" {
			return exec.Command("printf", "git@git.example.com:example-org/datasets/DS000000001.git")
		}
		return exec.Command("sh", "-c", "exit 1")
	})

	folder, source := resolveFolder("", noRemotesSpec, "jdoe", nil, nil, "/home/user/ds-migrate")
	if folder != "DS000000001" || source != folderSourceGitOrigin {
		t.Errorf("resolveFolder = (%q, %q), want (DS000000001, %q)", folder, source, folderSourceGitOrigin)
	}
}

func TestResolveFolderFallsBackToDirectoryWithoutOrigin(t *testing.T) {
	chdirTemp(t)

	withCommand(t, func(name string, args ...string) *exec.Cmd {
		return exec.Command("sh", "-c", "exit 1")
	})

	folder, source := resolveFolder("", noRemotesSpec, "jdoe", nil, nil, filepath.Join("home", "user", "my-scratch-dir"))
	if folder != "my-scratch-dir" || source != folderSourceDirectory {
		t.Errorf("resolveFolder = (%q, %q), want (my-scratch-dir, %q)", folder, source, folderSourceDirectory)
	}
}

func TestExplicitFolderOverrideFromSetFlag(t *testing.T) {
	oldFolder, oldSet := flagFolder, flagSet
	flagFolder = ""
	flagSet = []string{"Folder=my-dataset", "read_access=public"}
	t.Cleanup(func() { flagFolder, flagSet = oldFolder, oldSet })

	folder, rest := explicitFolderOverride()
	if folder != "my-dataset" {
		t.Errorf("folder = %q, want my-dataset", folder)
	}
	if len(rest) != 1 || rest[0] != "read_access=public" {
		t.Errorf("remaining sets = %v, want [read_access=public]", rest)
	}
}

func TestExplicitFolderOverrideFlagTakesPrecedenceOverSet(t *testing.T) {
	oldFolder, oldSet := flagFolder, flagSet
	flagFolder = "from-flag"
	flagSet = []string{"Folder=from-set"}
	t.Cleanup(func() { flagFolder, flagSet = oldFolder, oldSet })

	folder, rest := explicitFolderOverride()
	if folder != "from-flag" {
		t.Errorf("folder = %q, want from-flag", folder)
	}
	if len(rest) != 0 {
		t.Errorf("remaining sets = %v, want empty (Folder=... always stripped)", rest)
	}
}

func TestExplicitFolderOverrideNoneGiven(t *testing.T) {
	oldFolder, oldSet := flagFolder, flagSet
	flagFolder = ""
	flagSet = []string{"read_access=public"}
	t.Cleanup(func() { flagFolder, flagSet = oldFolder, oldSet })

	folder, rest := explicitFolderOverride()
	if folder != "" {
		t.Errorf("folder = %q, want empty", folder)
	}
	if len(rest) != 1 || rest[0] != "read_access=public" {
		t.Errorf("remaining sets = %v, want [read_access=public]", rest)
	}
}

func TestRecoverSentinelValue(t *testing.T) {
	tests := []struct {
		name     string
		template string
		actual   string
		sentinel string
		want     string
		wantOK   bool
	}{
		{"simple", "s3://bucket/SENT/_annex", "s3://bucket/DS1/_annex", "SENT", "DS1", true},
		{"no sentinel in template", "s3://bucket/_annex", "s3://bucket/_annex", "SENT", "", false},
		{"prefix mismatch", "s3://bucket/SENT/_annex", "s3://other/DS1/_annex", "SENT", "", false},
		{"empty recovered value", "s3://bucket/SENT/_annex", "s3://bucket//_annex", "SENT", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := recoverSentinelValue(tc.template, tc.actual, tc.sentinel)
			if ok != tc.wantOK || got != tc.want {
				t.Errorf("recoverSentinelValue(%q, %q, %q) = (%q, %v), want (%q, %v)", tc.template, tc.actual, tc.sentinel, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestResolveUnixIDFromLockMissing(t *testing.T) {
	chdirTemp(t)

	if _, ok := resolveUnixIDFromLock("my-profile"); ok {
		t.Error("expected no UnixID without a profile.lock")
	}
}

func TestResolveUnixIDFromLockPresent(t *testing.T) {
	chdirTemp(t)

	if err := SaveProfileLock(&ProfileLock{Profile: "my-profile", UnixID: "jdoe"}); err != nil {
		t.Fatalf("SaveProfileLock: %v", err)
	}

	id, ok := resolveUnixIDFromLock("my-profile")
	if !ok || id != "jdoe" {
		t.Errorf("resolveUnixIDFromLock = (%q, %v), want (jdoe, true)", id, ok)
	}

	if _, ok := resolveUnixIDFromLock("other-profile"); ok {
		t.Error("expected no UnixID for a different profile name")
	}
}
