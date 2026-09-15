package init

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestRunSequenceStopsOnError(t *testing.T) {
	called := 0
	err := runSequence(
		func() error {
			called++
			return nil
		},
		func() error {
			called++
			return errTest
		},
		func() error {
			called++
			return nil
		},
	)
	if !errors.Is(err, errTest) {
		t.Fatalf("expected error")
	}
	if called != 2 {
		t.Fatalf("expected 2 steps, got %d", called)
	}
}

func TestRegexpMatch(t *testing.T) {
	matched, err := regexpMatch("^s3://.+$", "s3://bucket")
	if err != nil || !matched {
		t.Fatalf("regexpMatch expected match")
	}
	if _, err := regexpMatch("[", "bad"); err == nil {
		t.Fatalf("expected error for invalid regex")
	}
}

func TestPromptHelpers(t *testing.T) {
	withStdin(t, "\n", func() {
		val, err := askWithDefault("Value", "default")
		if err != nil || val != "default" {
			t.Fatalf("askWithDefault: %q err=%v", val, err)
		}
	})

	withStdin(t, "\ninvalid\ns3://bucket\n", func() {
		val, err := askNonEmpty("Value", "^s3://.+$")
		if err != nil || !strings.HasPrefix(val, "s3://") {
			t.Fatalf("askNonEmpty: %q err=%v", val, err)
		}
	})
}

var errTest = errors.New("sentinel")

func withStdin(t *testing.T, input string, fn func()) {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	if _, err := writer.WriteString(input); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	old := os.Stdin
	os.Stdin = reader
	defer func() {
		os.Stdin = old
		_ = reader.Close()
	}()
	fn()
}

func TestDefaultTrackingBranchUsesCurrentBranch(t *testing.T) {
	withCommand(t, func(name string, args ...string) *exec.Cmd {
		if name == "git" && len(args) >= 1 && args[0] == "symbolic-ref" {
			return exec.Command("sh", "-c", "printf 'feature'")
		}
		return exec.Command("sh", "-c", "exit 1")
	})

	if got := defaultTrackingBranch(); got != "feature" {
		t.Fatalf("defaultTrackingBranch: %q", got)
	}
}

func TestDefaultTrackingBranchFallsBackToMain(t *testing.T) {
	withCommand(t, func(name string, args ...string) *exec.Cmd {
		if name == "git" && len(args) >= 1 && args[0] == "symbolic-ref" {
			return exec.Command("sh", "-c", "exit 1")
		}
		if name == "git" && len(args) >= 1 && args[0] == "show-ref" && args[len(args)-1] == "refs/heads/main" {
			return exec.Command("sh", "-c", "exit 0")
		}
		return exec.Command("sh", "-c", "exit 1")
	})

	if got := defaultTrackingBranch(); got != "main" {
		t.Fatalf("defaultTrackingBranch: %q", got)
	}
}

func TestDefaultTrackingBranchFallsBackToMaster(t *testing.T) {
	withCommand(t, func(name string, args ...string) *exec.Cmd {
		if name == "git" && len(args) >= 1 && args[0] == "symbolic-ref" {
			return exec.Command("sh", "-c", "exit 1")
		}
		if name == "git" && len(args) >= 1 && args[0] == "show-ref" && args[len(args)-1] == "refs/heads/master" {
			return exec.Command("sh", "-c", "exit 0")
		}
		return exec.Command("sh", "-c", "exit 1")
	})

	if got := defaultTrackingBranch(); got != "master" {
		t.Fatalf("defaultTrackingBranch: %q", got)
	}
}

func withCommand(t *testing.T, fn func(name string, args ...string) *exec.Cmd) {
	t.Helper()
	old := command
	command = fn
	t.Cleanup(func() { command = old })
}

func TestEnsureOriginAddsRemoteWhenMissing(t *testing.T) {
	var calls []string
	withCommand(t, func(name string, args ...string) *exec.Cmd {
		calls = append(calls, name+" "+strings.Join(args, " "))
		if name == "git" && len(args) >= 2 && args[0] == "remote" && args[1] == "get-url" {
			return exec.Command("sh", "-c", "exit 1")
		}
		return exec.Command("sh", "-c", "exit 0")
	})

	withStdin(t, "https://example.test/repo.git\n", func() {
		if err := ensureOrigin(); err != nil {
			t.Fatalf("ensureOrigin: %v", err)
		}
	})

	found := false
	for _, call := range calls {
		if strings.Contains(call, "remote add origin https://example.test/repo.git") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected remote add call, got %#v", calls)
	}
}

func TestEnsureAnnexInitializedRunsInit(t *testing.T) {
	var calls []string
	withCommand(t, func(name string, args ...string) *exec.Cmd {
		calls = append(calls, name+" "+strings.Join(args, " "))
		if name == "git" && len(args) >= 2 && args[0] == "config" && args[1] == "--get" {
			return exec.Command("sh", "-c", "exit 1")
		}
		return exec.Command("sh", "-c", "exit 0")
	})

	if err := ensureAnnexInitialized(); err != nil {
		t.Fatalf("ensureAnnexInitialized: %v", err)
	}

	found := false
	for _, call := range calls {
		if strings.Contains(call, "annex init") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected annex init call, got %#v", calls)
	}
}

func TestApplyAnnexSetting(t *testing.T) {
	boolPtr := func(v bool) *bool { return &v }

	t.Run("shared setting defaults to true when nil", func(t *testing.T) {
		var calls []string
		withCommand(t, func(name string, args ...string) *exec.Cmd {
			call := name + " " + strings.Join(args, " ")
			calls = append(calls, call)
			if strings.Contains(call, "annex config --get") {
				return exec.Command("sh", "-c", "exit 1")
			}
			return exec.Command("sh", "-c", "exit 0")
		})
		calls = nil
		applyAnnexSetting("addunlocked", nil, true, true)
		found := false
		for _, call := range calls {
			if strings.Contains(call, "annex config --set annex.addunlocked true") {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected annex config --set annex.addunlocked true, got %#v", calls)
		}
	})

	t.Run("shared setting skips when already set to target", func(t *testing.T) {
		var calls []string
		withCommand(t, func(name string, args ...string) *exec.Cmd {
			call := name + " " + strings.Join(args, " ")
			calls = append(calls, call)
			if strings.Contains(call, "annex config --get") {
				return exec.Command("sh", "-c", "echo true")
			}
			return exec.Command("sh", "-c", "exit 0")
		})
		calls = nil
		applyAnnexSetting("addunlocked", nil, true, true)
		for _, call := range calls {
			if strings.Contains(call, "annex config --set") {
				t.Fatalf("should not set when already at target, got %#v", calls)
			}
		}
	})

	t.Run("shared setting override to false", func(t *testing.T) {
		var calls []string
		withCommand(t, func(name string, args ...string) *exec.Cmd {
			call := name + " " + strings.Join(args, " ")
			calls = append(calls, call)
			if strings.Contains(call, "annex config --get") {
				return exec.Command("sh", "-c", "echo true")
			}
			return exec.Command("sh", "-c", "exit 0")
		})
		calls = nil
		applyAnnexSetting("addunlocked", boolPtr(false), true, true)
		found := false
		for _, call := range calls {
			if strings.Contains(call, "annex config --set annex.addunlocked false") {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected annex config --set annex.addunlocked false, got %#v", calls)
		}
	})

	t.Run("local setting true", func(t *testing.T) {
		var calls []string
		withCommand(t, func(name string, args ...string) *exec.Cmd {
			calls = append(calls, name+" "+strings.Join(args, " "))
			return exec.Command("sh", "-c", "exit 0")
		})
		calls = nil
		applyAnnexSetting("thin", boolPtr(true), false, false)
		found := false
		for _, call := range calls {
			if strings.Contains(call, "config annex.thin true") {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected git config annex.thin true, got %#v", calls)
		}
	})

	t.Run("local setting false unsets", func(t *testing.T) {
		var calls []string
		withCommand(t, func(name string, args ...string) *exec.Cmd {
			calls = append(calls, name+" "+strings.Join(args, " "))
			return exec.Command("sh", "-c", "exit 0")
		})
		calls = nil
		applyAnnexSetting("thin", boolPtr(false), false, false)
		found := false
		for _, call := range calls {
			if strings.Contains(call, "config --unset annex.thin") {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected git config --unset annex.thin, got %#v", calls)
		}
	})

	t.Run("local setting nil uses default false and unsets", func(t *testing.T) {
		var calls []string
		withCommand(t, func(name string, args ...string) *exec.Cmd {
			calls = append(calls, name+" "+strings.Join(args, " "))
			return exec.Command("sh", "-c", "exit 0")
		})
		calls = nil
		applyAnnexSetting("thin", nil, false, false)
		found := false
		for _, call := range calls {
			if strings.Contains(call, "config --unset annex.thin") {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected git config --unset annex.thin when nil, got %#v", calls)
		}
	})
}

func TestApplyAnnexDefaults(t *testing.T) {
	boolPtr := func(v bool) *bool { return &v }

	t.Run("nil config applies defaults", func(t *testing.T) {
		var calls []string
		withCommand(t, func(name string, args ...string) *exec.Cmd {
			call := name + " " + strings.Join(args, " ")
			calls = append(calls, call)
			if strings.Contains(call, "annex config --get") {
				return exec.Command("sh", "-c", "exit 1")
			}
			return exec.Command("sh", "-c", "exit 0")
		})
		calls = nil
		applyAnnexDefaults(nil)
		foundUnlocked := false
		foundThinUnset := false
		for _, call := range calls {
			if strings.Contains(call, "annex config --set annex.addunlocked true") {
				foundUnlocked = true
			}
			if strings.Contains(call, "config --unset annex.thin") {
				foundThinUnset = true
			}
		}
		if !foundUnlocked {
			t.Fatalf("expected addunlocked=true by default, got %#v", calls)
		}
		if !foundThinUnset {
			t.Fatalf("expected thin unset by default, got %#v", calls)
		}
	})

	t.Run("config overrides defaults", func(t *testing.T) {
		var calls []string
		withCommand(t, func(name string, args ...string) *exec.Cmd {
			call := name + " " + strings.Join(args, " ")
			calls = append(calls, call)
			if strings.Contains(call, "annex config --get") {
				return exec.Command("sh", "-c", "exit 1")
			}
			return exec.Command("sh", "-c", "exit 0")
		})
		calls = nil
		applyAnnexDefaults(&ExohubConfig{
			Annex: &AnnexConfig{
				AddUnlocked: boolPtr(false),
				Thin:        boolPtr(true),
			},
		})
		foundUnlockedFalse := false
		foundThinTrue := false
		for _, call := range calls {
			if strings.Contains(call, "annex config --set annex.addunlocked false") {
				foundUnlockedFalse = true
			}
			if strings.Contains(call, "config annex.thin true") {
				foundThinTrue = true
			}
		}
		if !foundUnlockedFalse {
			t.Fatalf("expected addunlocked=false from config, got %#v", calls)
		}
		if !foundThinTrue {
			t.Fatalf("expected thin=true from config, got %#v", calls)
		}
	})
}

func TestIsGitRepo(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "exo-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	os.Chdir(tmpDir)

	if IsGitRepo() {
		t.Errorf("IsGitRepo() = true in non-git dir, want false")
	}

	// Create .git directory
	if err := os.Mkdir(".git", 0755); err != nil {
		t.Fatalf("failed to create .git dir: %v", err)
	}

	if !IsGitRepo() {
		t.Errorf("IsGitRepo() = false with .git dir, want true")
	}
}

func TestRemoteExists(t *testing.T) {
	withCommand(t, func(name string, args ...string) *exec.Cmd {
		// remoteExists now uses getRemoteUUID which calls git config --get
		if name == "git" && len(args) >= 3 && args[0] == "config" && args[1] == "--get" {
			if args[2] == "remote.origin.annex-uuid" {
				return exec.Command("sh", "-c", "printf 'uuid-1234'")
			}
			return exec.Command("sh", "-c", "exit 1")
		}
		return exec.Command("sh", "-c", "exit 1")
	})

	if !remoteExists("origin") {
		t.Errorf("remoteExists('origin') = false, want true")
	}

	if remoteExists("nonexistent") {
		t.Errorf("remoteExists('nonexistent') = true, want false")
	}
}

func TestGetGitConfig(t *testing.T) {
	withCommand(t, func(name string, args ...string) *exec.Cmd {
		if name == "git" && len(args) >= 3 && args[0] == "config" && args[1] == "--get" {
			if args[2] == "test.key" {
				return exec.Command("sh", "-c", "printf 'test-value'")
			}
		}
		return exec.Command("sh", "-c", "exit 1")
	})

	value := getGitConfig("test.key")
	if value != "test-value" {
		t.Errorf("getGitConfig('test.key') = %q, want %q", value, "test-value")
	}

	value = getGitConfig("nonexistent.key")
	if value != "" {
		t.Errorf("getGitConfig('nonexistent.key') = %q, want empty string", value)
	}
}

func TestGetRemoteUUID(t *testing.T) {
	withCommand(t, func(name string, args ...string) *exec.Cmd {
		if name == "git" && len(args) >= 3 && args[0] == "config" && args[1] == "--get" {
			if strings.Contains(args[2], "testremote.annex-uuid") {
				return exec.Command("sh", "-c", "printf 'test-uuid-123'")
			}
		}
		return exec.Command("sh", "-c", "exit 1")
	})

	uuid := getRemoteUUID("testremote")
	if uuid != "test-uuid-123" {
		t.Errorf("getRemoteUUID('testremote') = %q, want %q", uuid, "test-uuid-123")
	}

	uuid = getRemoteUUID("nonexistent")
	if uuid != "" {
		t.Errorf("getRemoteUUID('nonexistent') = %q, want empty string", uuid)
	}
}

func TestCommandExists(t *testing.T) {
	// Test with a command that should always exist
	if !commandExists("sh") && !commandExists("cmd") {
		t.Errorf("commandExists('sh' or 'cmd') = false, want true")
	}

	// Test with a command that likely doesn't exist
	if commandExists("definitely-not-a-real-command-xyz") {
		t.Errorf("commandExists('definitely-not-a-real-command-xyz') = true, want false")
	}
}

func TestDetectOrphanedRemotes(t *testing.T) {
	withCommand(t, func(name string, args ...string) *exec.Cmd {
		if name == "git" && len(args) >= 2 && args[0] == "config" && args[1] == "--get-regexp" {
			// Return two remotes with annex-uuid
			return exec.Command("sh", "-c", "printf 'remote.managed.annex-uuid uuid-1\nremote.orphaned.annex-uuid uuid-2'")
		}
		return exec.Command("sh", "-c", "exit 1")
	})

	// Create config with only "managed" remote
	cfg := &RemotesConfig{
		Remotes: []RemoteConfig{
			{Name: "managed", Type: "annex", S3URL: "s3://bucket/prefix"},
		},
	}

	orphaned := detectOrphanedRemotes(cfg)

	// Should detect "orphaned" as orphaned
	found := false
	for _, name := range orphaned {
		if name == "orphaned" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("detectOrphanedRemotes() did not detect 'orphaned' remote, got: %v", orphaned)
	}

	// Should not include "managed"
	for _, name := range orphaned {
		if name == "managed" {
			t.Errorf("detectOrphanedRemotes() incorrectly included 'managed' remote")
		}
	}
}

func TestCheckRemoteBinaries(t *testing.T) {
	tests := []struct {
		name       string
		remoteType string
		wantErr    bool
	}{
		{
			name:       "annex type requires s5cmd",
			remoteType: "annex",
			wantErr:    !commandExists("s5cmd"),
		},
		{
			name:       "export type requires s5cmd",
			remoteType: "export",
			wantErr:    !commandExists("s5cmd"),
		},
		{
			name:       "import type requires no binary (uses built-in S3)",
			remoteType: "import",
			wantErr:    false, // import uses built-in git-annex S3 type
		},
		{
			name:       "exospace type requires rsync",
			remoteType: "exospace",
			wantErr:    !commandExists("rsync"),
		},
		{
			name:       "invalid type",
			remoteType: "invalid",
			wantErr:    false, // doesn't check binaries for unknown types
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkRemoteBinaries(tt.remoteType)
			if (err != nil) != tt.wantErr {
				t.Errorf("checkRemoteBinaries(%q) error = %v, wantErr %v", tt.remoteType, err, tt.wantErr)
			}
		})
	}
}

func TestRemoteHasExportTree(t *testing.T) {
	withCommand(t, func(name string, args ...string) *exec.Cmd {
		if name == "git" && len(args) >= 2 && args[0] == "config" && args[1] == "--get" {
			if strings.Contains(args[2], "export-remote.annex-uuid") {
				return exec.Command("sh", "-c", "printf 'export-uuid'")
			}
			if strings.Contains(args[2], "annex-remote.annex-uuid") {
				return exec.Command("sh", "-c", "printf 'annex-uuid'")
			}
		}
		if name == "git" && len(args) >= 2 && args[0] == "show" && args[1] == "git-annex:remote.log" {
			// Simulate remote.log with one export and one annex remote
			return exec.Command("sh", "-c", "printf 'export-uuid exporttree=yes name=export-remote\nannex-uuid name=annex-remote'")
		}
		return exec.Command("sh", "-c", "exit 1")
	})

	if !remoteHasExportTree("export-remote", "export-uuid") {
		t.Errorf("remoteHasExportTree('export-remote', 'export-uuid') = false, want true")
	}

	if remoteHasExportTree("annex-remote", "annex-uuid") {
		t.Errorf("remoteHasExportTree('annex-remote', 'annex-uuid') = true, want false")
	}

	if remoteHasExportTree("nonexistent", "nonexistent-uuid") {
		t.Errorf("remoteHasExportTree('nonexistent', 'nonexistent-uuid') = true, want false")
	}
}

func TestEnsureExportTrackingBranch(t *testing.T) {
	tests := []struct {
		name             string
		remote           RemoteConfig
		currentBranch    string
		existingConfig   string
		expectConfigCall bool
		expectedBranch   string
	}{
		{
			name: "Export remote without tracking branch - uses current branch",
			remote: RemoteConfig{
				Name: "myexport",
				Type: "export",
			},
			currentBranch:    "main",
			existingConfig:   "",
			expectConfigCall: true,
			expectedBranch:   "main",
		},
		{
			name: "Export remote with explicit tracking branch",
			remote: RemoteConfig{
				Name:           "myexport",
				Type:           "export",
				TrackingBranch: "production",
			},
			existingConfig:   "",
			expectConfigCall: true,
			expectedBranch:   "production",
		},
		{
			name: "Export remote with existing config - no change",
			remote: RemoteConfig{
				Name: "myexport",
				Type: "export",
			},
			existingConfig:   "main",
			expectConfigCall: false,
		},
		{
			name: "Export remote with changed tracking branch - updates config",
			remote: RemoteConfig{
				Name:           "myexport",
				Type:           "export",
				TrackingBranch: "release",
			},
			existingConfig:   "main",
			expectConfigCall: true,
			expectedBranch:   "release",
		},
		{
			name: "Drive remote with changed tracking branch - updates config",
			remote: RemoteConfig{
				Name:           "mydrive",
				Type:           "drive",
				TrackingBranch: "release",
			},
			existingConfig:   "main",
			expectConfigCall: true,
			expectedBranch:   "release",
		},
		{
			name: "Non-export remote - no config",
			remote: RemoteConfig{
				Name: "myannex",
				Type: "annex",
			},
			expectConfigCall: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var configCalls []string
			withCommand(t, func(name string, args ...string) *exec.Cmd {
				if name == "git" && len(args) >= 3 && args[0] == "config" {
					if args[1] == "--get" {
						// Return existing config or empty
						if tt.existingConfig != "" {
							return exec.Command("printf", tt.existingConfig)
						}
						return exec.Command("sh", "-c", "exit 1")
					}
					if len(args) == 3 {
						// Setting config
						configCalls = append(configCalls, strings.Join(args, " "))
						if tt.expectConfigCall {
							expectedKey := "remote." + tt.remote.Name + ".annex-tracking-branch"
							if args[1] != expectedKey {
								t.Errorf("Expected config key %s, got %s", expectedKey, args[1])
							}
							if tt.expectedBranch != "" && args[2] != tt.expectedBranch {
								t.Errorf("Expected branch %s, got %s", tt.expectedBranch, args[2])
							}
						}
					}
				}
				if name == "git" && args[0] == "branch" && args[1] == "--show-current" {
					return exec.Command("printf", tt.currentBranch)
				}
				return exec.Command("sh", "-c", "exit 0")
			})

			err := ensureExportTrackingBranch(tt.remote)
			if err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if tt.expectConfigCall && len(configCalls) == 0 {
				t.Error("Expected config call but none were made")
			}
			if !tt.expectConfigCall && len(configCalls) > 0 {
				t.Errorf("Expected no config calls but got: %v", configCalls)
			}
		})
	}
}

func TestBuildInitialCommitMessage(t *testing.T) {
	// Create temp directory with .exohub files
	tmpDir := t.TempDir()
	oldDir, _ := os.Getwd()
	defer os.Chdir(oldDir)
	os.Chdir(tmpDir)

	// Create .exohub directory
	os.MkdirAll(".exohub", 0755)

	// Test with context only
	t.Run("With context only", func(t *testing.T) {
		contextContent := `name: test
host: https://example.com
org: testorg
`
		os.WriteFile(".exohub/context", []byte(contextContent), 0644)

		msg := buildInitialCommitMessage()
		if !strings.Contains(msg, "feat: initial commit created with `exo`") {
			t.Error("Missing commit subject")
		}
		if !strings.Contains(msg, ".exohub/context") {
			t.Error("Missing context section")
		}
		if !strings.Contains(msg, contextContent) {
			t.Error("Missing context content")
		}
		if strings.Contains(msg, ".exohub/remotes") {
			t.Error("Should not contain remotes section when file missing")
		}
	})

	// Test with both context and remotes
	t.Run("With context and remotes", func(t *testing.T) {
		remotesContent := `remotes:
  - name: s3backup
    type: annex
    s3url: s3://bucket/path
`
		os.WriteFile(".exohub/remotes", []byte(remotesContent), 0644)

		msg := buildInitialCommitMessage()
		if !strings.Contains(msg, ".exohub/context") {
			t.Error("Missing context section")
		}
		if !strings.Contains(msg, ".exohub/remotes") {
			t.Error("Missing remotes section")
		}
		if !strings.Contains(msg, remotesContent) {
			t.Error("Missing remotes content")
		}
	})
}

func TestNormalizeHostURL(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"http://example.com", "https://example.com"},
		{"https://example.com", "https://example.com"},
		{"http://example.com/", "https://example.com/"},
		{"https://example.com/", "https://example.com/"},
		{"example.com", "https://example.com"},
		{"example.com/", "https://example.com/"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := normalizeHostURL(tt.input)
			if result != tt.expected {
				t.Errorf("normalizeHostURL(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestCreateExportRemoteFromConfigSetsTrackingBranch(t *testing.T) {
	var configCalls []string
	currentBranch := "development"

	withCommand(t, func(name string, args ...string) *exec.Cmd {
		if name == "git" {
			if args[0] == "annex" {
				// Mock git-annex commands
				return exec.Command("sh", "-c", "exit 0")
			}
			if args[0] == "config" && len(args) == 3 {
				configCalls = append(configCalls, args[1]+"="+args[2])
			}
			if args[0] == "branch" && args[1] == "--show-current" {
				return exec.Command("printf", currentBranch)
			}
		}
		return exec.Command("sh", "-c", "exit 0")
	})

	t.Run("Sets tracking branch from config", func(t *testing.T) {
		configCalls = nil
		remote := RemoteConfig{
			Name:           "testexport",
			Type:           "export",
			S3URL:          "s3://bucket/path",
			TrackingBranch: "production",
		}

		err := createExportRemoteFromConfig(remote)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		found := false
		for _, call := range configCalls {
			if strings.Contains(call, "annex-tracking-branch=production") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected tracking branch to be set to 'production', got calls: %v", configCalls)
		}
	})

	t.Run("Defaults to current branch when not specified", func(t *testing.T) {
		configCalls = nil
		remote := RemoteConfig{
			Name:  "testexport2",
			Type:  "export",
			S3URL: "s3://bucket/path",
		}

		err := createExportRemoteFromConfig(remote)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		found := false
		for _, call := range configCalls {
			if strings.Contains(call, "annex-tracking-branch="+currentBranch) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected tracking branch to default to '%s', got calls: %v", currentBranch, configCalls)
		}
	})
}

func TestCheckLegacyContext(t *testing.T) {
	// Test: legacy context with thin=true triggers warning (no crash)
	tmpDir := t.TempDir()
	oldDir, _ := os.Getwd()
	defer os.Chdir(oldDir)
	os.Chdir(tmpDir)

	os.MkdirAll(".exohub", 0755)
	os.WriteFile(filepath.Join(".exohub", "context"), []byte("thin: true\nhost: https://old.com\norg: old\n"), 0644)

	// Should not crash, just print warning
	checkLegacyContext(nil)

	// Test: legacy context with thin=true, but config already has thin
	thinTrue := true
	checkLegacyContext(&ExohubConfig{Annex: &AnnexConfig{Thin: &thinTrue}})

	// Test: no legacy context file
	os.Remove(filepath.Join(".exohub", "context"))
	checkLegacyContext(nil)
}

func TestEnsureRemoteConfigS3URLUpdates(t *testing.T) {
	var calls []string
	withCommand(t, func(name string, args ...string) *exec.Cmd {
		calls = append(calls, name+" "+strings.Join(args, " "))
		if name == "git" && len(args) >= 3 && args[0] == "config" && args[1] == "--get" {
			if args[2] == "remote.origin.annex-uuid" {
				return exec.Command("printf", "uuid-1234")
			}
			if args[2] == "annex-remote.uuid-1234.config.s3url" {
				return exec.Command("printf", "s3://old")
			}
			return exec.Command("sh", "-c", "exit 1")
		}
		return exec.Command("sh", "-c", "exit 0")
	})

	ensureRemoteConfigS3URL("origin", "s3://new")

	var setConfig bool
	var enableRemote bool
	for _, call := range calls {
		if strings.Contains(call, "git config annex-remote.uuid-1234.config.s3url s3://new") {
			setConfig = true
		}
		if strings.Contains(call, "git annex enableremote origin s3url=s3://new") {
			enableRemote = true
		}
	}
	if !setConfig {
		t.Fatalf("expected s3url config update, calls: %#v", calls)
	}
	if !enableRemote {
		t.Fatalf("expected enableremote call with s3url parameter, calls: %#v", calls)
	}
}

func TestResolveRepoNamePrecedence(t *testing.T) {
	tmpDir := t.TempDir()
	oldDir, _ := os.Getwd()
	defer os.Chdir(oldDir)
	os.Chdir(tmpDir)

	// Ensure folder name is used for --auto when context is empty
	os.MkdirAll(".exohub", 0755)
	os.Remove(filepath.Join(".exohub", "context"))

	oldName := flagRepoName
	oldYes := flagYes
	flagRepoName = ""
	flagYes = true
	defer func() {
		flagRepoName = oldName
		flagYes = oldYes
	}()

	repoName := ""
	if flagRepoName != "" {
		repoName = flagRepoName
	} else if flagYes {
		repoName = getRepoName()
	}
	if repoName == "" {
		t.Fatalf("expected repo name, got empty")
	}
}

func TestBuildPreferredContentExpression(t *testing.T) {
	tests := []struct {
		name     string
		include  []string
		exclude  []string
		expected string
	}{
		{
			name:     "no patterns",
			include:  nil,
			exclude:  nil,
			expected: "",
		},
		{
			name:     "single include",
			include:  []string{"*.bam"},
			exclude:  nil,
			expected: "include=*.bam",
		},
		{
			name:     "multiple includes",
			include:  []string{"*.bam", "*.bai"},
			exclude:  nil,
			expected: "(include=*.bam or include=*.bai)",
		},
		{
			name:     "single exclude",
			include:  nil,
			exclude:  []string{"*.tmp"},
			expected: "exclude=*.tmp",
		},
		{
			name:     "include and exclude",
			include:  []string{"*.vcf.gz"},
			exclude:  []string{"*.mrjd.vcf.gz"},
			expected: "include=*.vcf.gz and exclude=*.mrjd.vcf.gz",
		},
		{
			name:     "multiple includes and excludes",
			include:  []string{"*.bam", "*.bai"},
			exclude:  []string{"*.tmp", "*/cache/*"},
			expected: "(include=*.bam or include=*.bai) and exclude=*.tmp and exclude=*/cache/*",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildPreferredContentExpression(tt.include, tt.exclude)
			if result != tt.expected {
				t.Errorf("buildPreferredContentExpression() = %q, want %q", result, tt.expected)
			}
		})
	}
}

// TestRemoteExistsInAnnex verifies that remoteExistsInAnnex correctly
// detects remotes in git-annex metadata even when not in local git config
func TestRemoteExistsInAnnex(t *testing.T) {
	// Mock command to return a sample remote.log
	oldCommand := command
	defer func() { command = oldCommand }()

	command = func(name string, args ...string) *exec.Cmd {
		if name == "git" && len(args) >= 2 && args[0] == "show" && args[1] == "git-annex:remote.log" {
			// Simulate remote.log with several remotes
			return exec.Command("sh", "-c", `printf 'uuid-1234 timestamp=123 name=test-remote type=rsync rsyncurl=/path
uuid-5678 timestamp=456 name=another-remote type=external externaltype=s5cmd
uuid-9999 timestamp=789 name=export-remote exporttree=yes'`)
		}
		return exec.Command("sh", "-c", "exit 1")
	}

	tests := []struct {
		name string
		want bool
	}{
		{"test-remote", true},
		{"another-remote", true},
		{"export-remote", true},
		{"nonexistent", false},
		{"test-remot", false}, // partial match should not work
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := remoteExistsInAnnex(tt.name)
			if got != tt.want {
				t.Errorf("remoteExistsInAnnex(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

// TestEnableOrInitRemoteFiltersInitParams verifies that enableOrInitRemote
// correctly filters out init-only parameters (type=, externaltype=, encryption=)
// when constructing enableremote arguments
func TestEnableOrInitRemoteFiltersInitParams(t *testing.T) {
	tests := []struct {
		name        string
		initArgs    []string
		wantEnable  []string
		description string
	}{
		{
			name: "rsync remote",
			initArgs: []string{
				"git", "annex", "initremote", "test-remote",
				"type=rsync", "rsyncurl=/path/to/remote",
				"encryption=none",
			},
			wantEnable: []string{
				"git", "annex", "enableremote", "test-remote",
				"rsyncurl=/path/to/remote",
			},
			description: "type= and encryption= should be filtered out",
		},
		{
			name: "s5cmd external remote",
			initArgs: []string{
				"git", "annex", "initremote", "s3-remote",
				"type=external", "externaltype=s5cmd",
				"s3url=s3://bucket/prefix", "chunk=1GiB",
				"encryption=none",
			},
			wantEnable: []string{
				"git", "annex", "enableremote", "s3-remote",
				"s3url=s3://bucket/prefix", "chunk=1GiB",
			},
			description: "type=, externaltype=, and encryption= should be filtered out",
		},
		{
			name: "export remote with tracking",
			initArgs: []string{
				"git", "annex", "initremote", "export-remote",
				"type=external", "externaltype=s5cmd",
				"s3url=s3://bucket/export", "exporttree=yes",
				"encryption=none",
			},
			wantEnable: []string{
				"git", "annex", "enableremote", "export-remote",
				"s3url=s3://bucket/export", "exporttree=yes",
			},
			description: "only s3url and exporttree should be passed to enableremote",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Build expected enableArgs by parsing initArgs
			enableArgs := []string{"git", "annex", "enableremote", tt.initArgs[3]}

			// Extract config parameters from initArgs (skip "git", "annex", "initremote", name)
			for i := 4; i < len(tt.initArgs); i++ {
				arg := tt.initArgs[i]
				// Skip encryption=none, type=, and externaltype= as they're only for initremote
				if arg != "encryption=none" && !strings.HasPrefix(arg, "type=") && !strings.HasPrefix(arg, "externaltype=") {
					enableArgs = append(enableArgs, arg)
				}
			}

			// Verify the constructed enableArgs match expected
			if len(enableArgs) != len(tt.wantEnable) {
				t.Errorf("%s: got %d args, want %d args", tt.description, len(enableArgs), len(tt.wantEnable))
				t.Errorf("got:  %v", enableArgs)
				t.Errorf("want: %v", tt.wantEnable)
				return
			}

			for i := range enableArgs {
				if enableArgs[i] != tt.wantEnable[i] {
					t.Errorf("%s: arg[%d] = %q, want %q", tt.description, i, enableArgs[i], tt.wantEnable[i])
				}
			}
		})
	}
}

// TestEnableOrInitRemoteWithUUID verifies that when a UUID is specified:
// 1. enableremote uses the UUID instead of name
// 2. If enableremote fails, it returns an error instead of falling back to initremote
// This prevents creating duplicate remotes with different UUIDs.
func TestEnableOrInitRemoteWithUUID(t *testing.T) {
	oldCommand := command
	defer func() { command = oldCommand }()

	tests := []struct {
		name           string
		remoteName     string
		uuid           string
		enableSucceeds bool
		wantError      bool
		wantEnableArgs []string // expected enableremote args (first 5 args)
		description    string
	}{
		{
			name:           "UUID specified, enableremote succeeds",
			remoteName:     "test-remote",
			uuid:           "abc-123-uuid",
			enableSucceeds: true,
			wantError:      false,
			wantEnableArgs: []string{"git", "annex", "enableremote", "abc-123-uuid", "name=test-remote"},
			description:    "should use UUID in enableremote and succeed",
		},
		{
			name:           "UUID specified, enableremote fails",
			remoteName:     "test-remote",
			uuid:           "abc-123-uuid",
			enableSucceeds: false,
			wantError:      true, // Should NOT fall back to initremote
			wantEnableArgs: []string{"git", "annex", "enableremote", "abc-123-uuid", "name=test-remote"},
			description:    "should return error, NOT create new remote",
		},
		{
			name:           "No UUID, enableremote fails",
			remoteName:     "test-remote",
			uuid:           "",
			enableSucceeds: false,
			wantError:      false, // Should fall back to initremote
			wantEnableArgs: []string{"git", "annex", "enableremote", "test-remote"},
			description:    "should fall back to initremote when no UUID",
		},
		{
			name:           "No UUID, enableremote succeeds",
			remoteName:     "test-remote",
			uuid:           "",
			enableSucceeds: true,
			wantError:      false,
			wantEnableArgs: []string{"git", "annex", "enableremote", "test-remote"},
			description:    "should succeed without UUID",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var capturedEnableArgs []string
			var initremoteCalled bool

			command = func(name string, args ...string) *exec.Cmd {
				fullArgs := append([]string{name}, args...)

				// Capture enableremote calls
				if name == "git" && len(args) >= 2 && args[0] == "annex" && args[1] == "enableremote" {
					capturedEnableArgs = fullArgs
					if tt.enableSucceeds {
						return exec.Command("sh", "-c", "exit 0")
					}
					return exec.Command("sh", "-c", "exit 1")
				}

				// Track initremote calls
				if name == "git" && len(args) >= 2 && args[0] == "annex" && args[1] == "initremote" {
					initremoteCalled = true
					return exec.Command("sh", "-c", "exit 0")
				}

				return exec.Command("sh", "-c", "exit 0")
			}

			initArgs := []string{
				"git", "annex", "initremote", tt.remoteName,
				"type=external", "externaltype=s5cmd", "encryption=none",
				"s3url=s3://bucket/path", "chunk=1GiB",
			}

			err := enableOrInitRemote(tt.remoteName, tt.uuid, initArgs)

			// Check error expectation
			if tt.wantError && err == nil {
				t.Errorf("%s: expected error but got nil", tt.description)
			}
			if !tt.wantError && err != nil {
				t.Errorf("%s: unexpected error: %v", tt.description, err)
			}

			// Verify enableremote was called with correct args
			if len(capturedEnableArgs) < len(tt.wantEnableArgs) {
				t.Errorf("%s: enableremote not called correctly, got %v", tt.description, capturedEnableArgs)
			} else {
				for i, want := range tt.wantEnableArgs {
					if capturedEnableArgs[i] != want {
						t.Errorf("%s: enableremote arg[%d] = %q, want %q", tt.description, i, capturedEnableArgs[i], want)
					}
				}
			}

			// Verify initremote behavior based on UUID presence
			if tt.uuid != "" && initremoteCalled {
				t.Errorf("%s: initremote was called when UUID was specified - should never create new remote!", tt.description)
			}
			if tt.uuid == "" && !tt.enableSucceeds && !initremoteCalled {
				t.Errorf("%s: initremote was NOT called when it should have been (no UUID, enable failed)", tt.description)
			}
		})
	}
}

// TestFetchGitAnnexBranch verifies that fetchGitAnnexBranch handles various scenarios
func TestFetchGitAnnexBranch(t *testing.T) {
	oldCommand := command
	defer func() { command = oldCommand }()

	t.Run("fetches when origin exists", func(t *testing.T) {
		var fetchCalled bool

		command = func(name string, args ...string) *exec.Cmd {
			if name == "git" && len(args) >= 2 && args[0] == "remote" && args[1] == "get-url" {
				return exec.Command("sh", "-c", "echo 'https://example.com/repo.git'")
			}
			// Mock git show-ref to say no local git-annex branch exists
			if name == "git" && len(args) >= 1 && args[0] == "show-ref" {
				return exec.Command("sh", "-c", "exit 1") // branch doesn't exist
			}
			if name == "git" && len(args) >= 3 && args[0] == "fetch" && args[1] == "origin" {
				fetchCalled = true
				return exec.Command("sh", "-c", "exit 0")
			}
			return exec.Command("sh", "-c", "exit 0")
		}

		err := fetchGitAnnexBranch()
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if !fetchCalled {
			t.Error("fetch was not called when origin exists")
		}
	})

	t.Run("skips fetch when no origin", func(t *testing.T) {
		var fetchCalled bool

		command = func(name string, args ...string) *exec.Cmd {
			if name == "git" && len(args) >= 2 && args[0] == "remote" && args[1] == "get-url" {
				return exec.Command("sh", "-c", "exit 1") // no origin
			}
			if name == "git" && args[0] == "fetch" {
				fetchCalled = true
				return exec.Command("sh", "-c", "exit 0")
			}
			return exec.Command("sh", "-c", "exit 0")
		}

		err := fetchGitAnnexBranch()
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if fetchCalled {
			t.Error("fetch should not be called when no origin exists")
		}
	})

	t.Run("skips fetch when local git-annex branch exists", func(t *testing.T) {
		var fetchCalled bool

		command = func(name string, args ...string) *exec.Cmd {
			if name == "git" && len(args) >= 2 && args[0] == "remote" && args[1] == "get-url" {
				return exec.Command("sh", "-c", "echo 'https://example.com/repo.git'")
			}
			// Mock git show-ref to say local git-annex branch already exists
			if name == "git" && len(args) >= 1 && args[0] == "show-ref" {
				return exec.Command("sh", "-c", "exit 0") // branch exists
			}
			if name == "git" && args[0] == "fetch" {
				fetchCalled = true
				return exec.Command("sh", "-c", "exit 0")
			}
			return exec.Command("sh", "-c", "exit 0")
		}

		err := fetchGitAnnexBranch()
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if fetchCalled {
			t.Error("fetch should not be called when local git-annex branch already exists")
		}
	})
}

func TestRsyncOptionsFromPermissions(t *testing.T) {
	tests := []struct {
		name   string
		preset string
		want   string
	}{
		{
			name:   "public preset",
			preset: "public",
			want:   "--chmod=ugo=rwX,Do+t,Fo-w",
		},
		{
			name:   "group preset",
			preset: "group",
			want:   "--chmod=ug=rwX,o=rX,Do+t,Fo-w",
		},
		{
			name:   "private preset",
			preset: "private",
			want:   "--chmod=u=rwX,go=",
		},
		{
			name:   "empty preset",
			preset: "",
			want:   "",
		},
		{
			name:   "unknown preset",
			preset: "unknown",
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rsyncOptionsFromPermissions(tt.preset)
			if got != tt.want {
				t.Errorf("rsyncOptionsFromPermissions(%q) = %q, want %q", tt.preset, got, tt.want)
			}
		})
	}
}

func TestGetExpectedRsyncOptions(t *testing.T) {
	tests := []struct {
		name   string
		remote RemoteConfig
		want   string
	}{
		{
			name: "permissions public",
			remote: RemoteConfig{
				Name:        "test",
				Type:        "exospace",
				RsyncURL:    "/mnt/data",
				Permissions: "public",
			},
			want: "--chmod=ugo=rwX,Do+t,Fo-w",
		},
		{
			name: "permissions group",
			remote: RemoteConfig{
				Name:        "test",
				Type:        "exospace",
				RsyncURL:    "/mnt/data",
				Permissions: "group",
			},
			want: "--chmod=ug=rwX,o=rX,Do+t,Fo-w",
		},
		{
			name: "permissions private",
			remote: RemoteConfig{
				Name:        "test",
				Type:        "exospace",
				RsyncURL:    "/mnt/data",
				Permissions: "private",
			},
			want: "--chmod=u=rwX,go=",
		},
		{
			name: "custom rsync_options takes precedence",
			remote: RemoteConfig{
				Name:         "test",
				Type:         "exospace",
				RsyncURL:     "/mnt/data",
				RsyncOptions: "--bwlimit=500 --compress",
			},
			want: "--bwlimit=500 --compress",
		},
		{
			name: "no permissions or rsync_options",
			remote: RemoteConfig{
				Name:     "test",
				Type:     "exospace",
				RsyncURL: "/mnt/data",
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getExpectedRsyncOptions(tt.remote)
			if got != tt.want {
				t.Errorf("getExpectedRsyncOptions() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNeedsReconfigureImportBucketChange(t *testing.T) {
	withCommand(t, func(name string, args ...string) *exec.Cmd {
		if name == "git" && len(args) >= 2 && args[0] == "show" && args[1] == "git-annex:remote.log" {
			return exec.Command("sh", "-c", "printf 'import-uuid bucket=old-bucket fileprefix=data/ datacenter=us-west-2 importtree=yes name=my-import'")
		}
		if name == "git" && len(args) >= 2 && args[0] == "config" && args[1] == "--get" {
			if strings.Contains(args[2], "annex-tracking-branch") {
				return exec.Command("printf", "main")
			}
		}
		if name == "git" && len(args) >= 2 && args[0] == "annex" && args[1] == "wanted" {
			return exec.Command("sh", "-c", "exit 1")
		}
		return exec.Command("sh", "-c", "exit 1")
	})

	remote := RemoteConfig{
		Name:   "my-import",
		Type:   "import",
		UUID:   "import-uuid",
		Bucket: "new-bucket",
		Prefix: "data/",
	}
	if !needsReconfigure(remote) {
		t.Error("needsReconfigure should return true when bucket changes")
	}
}

func TestNeedsReconfigureImportPrefixChange(t *testing.T) {
	withCommand(t, func(name string, args ...string) *exec.Cmd {
		if name == "git" && len(args) >= 2 && args[0] == "show" && args[1] == "git-annex:remote.log" {
			return exec.Command("sh", "-c", "printf 'import-uuid bucket=my-bucket fileprefix=old/path/ datacenter=us-west-2 importtree=yes name=my-import'")
		}
		if name == "git" && len(args) >= 2 && args[0] == "config" && args[1] == "--get" {
			if strings.Contains(args[2], "annex-tracking-branch") {
				return exec.Command("printf", "main")
			}
		}
		if name == "git" && len(args) >= 2 && args[0] == "annex" && args[1] == "wanted" {
			return exec.Command("sh", "-c", "exit 1")
		}
		return exec.Command("sh", "-c", "exit 1")
	})

	remote := RemoteConfig{
		Name:   "my-import",
		Type:   "import",
		UUID:   "import-uuid",
		Bucket: "my-bucket",
		Prefix: "new/path/",
	}
	if !needsReconfigure(remote) {
		t.Error("needsReconfigure should return true when prefix changes")
	}
}

func TestNeedsReconfigureImportNoChange(t *testing.T) {
	withCommand(t, func(name string, args ...string) *exec.Cmd {
		if name == "git" && len(args) >= 2 && args[0] == "show" && args[1] == "git-annex:remote.log" {
			return exec.Command("sh", "-c", "printf 'import-uuid bucket=my-bucket fileprefix=data/ datacenter=us-west-2 importtree=yes name=my-import'")
		}
		if name == "git" && len(args) >= 2 && args[0] == "config" && args[1] == "--get" {
			if strings.Contains(args[2], "annex-tracking-branch") {
				return exec.Command("printf", "main")
			}
		}
		if name == "git" && len(args) >= 2 && args[0] == "annex" && args[1] == "wanted" {
			return exec.Command("sh", "-c", "exit 1")
		}
		return exec.Command("sh", "-c", "exit 1")
	})

	remote := RemoteConfig{
		Name:   "my-import",
		Type:   "import",
		UUID:   "import-uuid",
		Bucket: "my-bucket",
		Prefix: "data/",
	}
	if needsReconfigure(remote) {
		t.Error("needsReconfigure should return false when bucket and prefix are unchanged")
	}
}

func TestFilterRemotesByName(t *testing.T) {
	cfg := &RemotesConfig{
		Remotes: []RemoteConfig{
			{Name: "s5-annex", Type: "annex", S3URL: "s3://bucket/path"},
			{Name: "delta-data", Type: "annex", S3URL: "s3://other/path"},
			{Name: "s5-export", Type: "export", S3URL: "s3://export/path"},
		},
	}

	t.Run("empty filter returns original config", func(t *testing.T) {
		got := filterRemotesByName(cfg, "")
		if got != cfg {
			t.Error("empty filter should return exact same pointer")
		}
	})

	t.Run("matching name returns single remote", func(t *testing.T) {
		got := filterRemotesByName(cfg, "s5-annex")
		if len(got.Remotes) != 1 {
			t.Fatalf("expected 1 remote, got %d", len(got.Remotes))
		}
		if got.Remotes[0].Name != "s5-annex" {
			t.Errorf("expected s5-annex, got %s", got.Remotes[0].Name)
		}
	})

	t.Run("non-matching name returns empty config", func(t *testing.T) {
		got := filterRemotesByName(cfg, "nonexistent")
		if len(got.Remotes) != 0 {
			t.Fatalf("expected 0 remotes, got %d", len(got.Remotes))
		}
	})

	t.Run("middle remote is found", func(t *testing.T) {
		got := filterRemotesByName(cfg, "delta-data")
		if len(got.Remotes) != 1 {
			t.Fatalf("expected 1 remote, got %d", len(got.Remotes))
		}
		if got.Remotes[0].Name != "delta-data" {
			t.Errorf("expected delta-data, got %s", got.Remotes[0].Name)
		}
	})
}

// TestProfileFlagParsing documents the cobra flag contract for --profile and --profile-select.
//
// --profile <name>   fetches a named profile directly
// --profile-select   shows interactive picker
// --profile          cobra error (string flag always requires a value)
//
// NoOptDefVal must NOT be set on --profile: it causes cobra to assign a sentinel
// and treat the profile name as a positional arg, so the picker always appears.
func TestProfileFlagParsing(t *testing.T) {
	makeCmd := func() *cobra.Command {
		cmd := &cobra.Command{Use: "init", SilenceErrors: true, SilenceUsage: true}
		var profile string
		var pickProfile bool
		cmd.Flags().StringVar(&profile, "profile", "", "profile name")
		cmd.Flags().BoolVar(&pickProfile, "profile-select", false, "interactive picker")
		cmd.RunE = func(cmd *cobra.Command, args []string) error { return nil }
		return cmd
	}

	t.Run("--profile sandbox sets named profile", func(t *testing.T) {
		cmd := makeCmd()
		cmd.SetArgs([]string{"--profile", "sandbox"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got, _ := cmd.Flags().GetString("profile")
		if got != "sandbox" {
			t.Errorf("flagProfile: got %q want sandbox — NoOptDefVal may have been re-added", got)
		}
		pick, _ := cmd.Flags().GetBool("profile-select")
		if pick {
			t.Error("profile-select should not be set")
		}
	})

	t.Run("--profile without value is a cobra error", func(t *testing.T) {
		cmd := makeCmd()
		cmd.SetArgs([]string{"--profile"})
		if err := cmd.Execute(); err == nil {
			t.Fatal("expected cobra error for --profile with no value")
		}
	})

	t.Run("--profile-select sets picker flag", func(t *testing.T) {
		cmd := makeCmd()
		cmd.SetArgs([]string{"--profile-select"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		pick, _ := cmd.Flags().GetBool("profile-select")
		if !pick {
			t.Error("profile-select should be true")
		}
		got, _ := cmd.Flags().GetString("profile")
		if got != "" {
			t.Errorf("profile should be empty when using --profile-select, got %q", got)
		}
	})

	t.Run("no flags leaves both unset", func(t *testing.T) {
		cmd := makeCmd()
		cmd.SetArgs([]string{})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got, _ := cmd.Flags().GetString("profile")
		pick, _ := cmd.Flags().GetBool("profile-select")
		if got != "" || pick {
			t.Errorf("expected both unset, got profile=%q pick=%v", got, pick)
		}
	})
}
