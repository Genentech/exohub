package publish

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	initcmd "github.com/Genentech/exohub/go/exo/commands/init"
	"github.com/Genentech/exohub/go/exo/commandutil"
)

func TestClassifyRemotes(t *testing.T) {
	tests := []struct {
		name        string
		config      *initcmd.RemotesConfig
		wantContent []string
		wantCatalog []string
	}{
		{
			name:   "nil config",
			config: nil,
		},
		{
			name:   "empty config",
			config: &initcmd.RemotesConfig{},
		},
		{
			name: "only content remotes",
			config: &initcmd.RemotesConfig{
				Remotes: []initcmd.RemoteConfig{
					{Name: "s3", Type: "annex"},
					{Name: "tree", Type: "export"},
				},
			},
			wantContent: []string{"s3", "tree"},
		},
		{
			name: "only catalog remotes",
			config: &initcmd.RemotesConfig{
				Remotes: []initcmd.RemoteConfig{
					{Name: "atlas", Type: "artifactdb"},
				},
			},
			wantCatalog: []string{"atlas"},
		},
		{
			name: "mixed remotes",
			config: &initcmd.RemotesConfig{
				Remotes: []initcmd.RemoteConfig{
					{Name: "s3", Type: "annex"},
					{Name: "tree", Type: "export"},
					{Name: "atlas", Type: "artifactdb"},
					{Name: "cache", Type: "exospace"},
					{Name: "importer", Type: "import"},
				},
			},
			wantContent: []string{"s3", "tree", "cache", "importer"},
			wantCatalog: []string{"atlas"},
		},
		{
			name: "custom catalog type",
			config: &initcmd.RemotesConfig{
				Remotes: []initcmd.RemoteConfig{
					{Name: "s3", Type: "annex"},
					{Name: "custom", Type: "mydb"},
				},
			},
			wantContent: []string{"s3"},
			wantCatalog: []string{"custom"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content, catalog := classifyRemotes(tt.config)
			if !sliceEqual(content, tt.wantContent) {
				t.Errorf("content remotes = %v, want %v", content, tt.wantContent)
			}
			if !sliceEqual(catalog, tt.wantCatalog) {
				t.Errorf("catalog remotes = %v, want %v", catalog, tt.wantCatalog)
			}
		})
	}
}

func TestIsStandardRemoteType(t *testing.T) {
	standards := []string{"annex", "export", "import", "exospace"}
	for _, s := range standards {
		if !isStandardRemoteType(s) {
			t.Errorf("expected %q to be standard", s)
		}
	}

	nonStandards := []string{"artifactdb", "mydb", "", "catalog"}
	for _, s := range nonStandards {
		if isStandardRemoteType(s) {
			t.Errorf("expected %q to NOT be standard", s)
		}
	}
}

// setupPublishEnv creates a temporary directory with a .git dir, installs a
// fake "exo" binary on PATH, and overrides the package-level command variable
// to record invocations. It returns a pointer to the recorded call list and a
// cleanup function.
func setupPublishEnv(t *testing.T, exoExitCode int) (calls *[]string, cleanup func()) {
	t.Helper()

	// Create a temp dir that acts as the repo root and cd into it.
	repoDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	// Install a fake "exo" on PATH.
	binDir := t.TempDir()
	exoScript := filepath.Join(binDir, "exo")
	script := "#!/bin/sh\nexit " + itoa(exoExitCode) + "\n"
	if err := os.WriteFile(exoScript, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake exo: %v", err)
	}
	oldPath := os.Getenv("PATH")
	os.Setenv("PATH", binDir+":"+oldPath)

	// Override command to record calls and delegate to a real sh -c exit 0.
	recorded := &[]string{}
	oldCommand := command
	command = func(name string, args ...string) *exec.Cmd {
		*recorded = append(*recorded, name+" "+strings.Join(args, " "))
		return exec.Command("sh", "-c", "exit "+itoa(exoExitCode))
	}

	// Simulate a configured git remote so the git-remote precondition passes.
	oldResolveCloneURL := resolveCloneURL
	resolveCloneURL = func() (string, error) { return "https://example.com/repo.git", nil }

	cleanup = func() {
		command = oldCommand
		resolveCloneURL = oldResolveCloneURL
		os.Setenv("PATH", oldPath)
		os.Chdir(oldDir) //nolint:errcheck
	}
	return recorded, cleanup
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}

func hasBroadcast(calls []string) bool {
	for _, c := range calls {
		if strings.Contains(c, "broadcast") {
			return true
		}
	}
	return false
}

// TestBroadcastRunsWhenCatalogSkipped verifies that broadcast is called even
// when no catalog remotes are configured (step 3 is skipped).
func TestBroadcastRunsWhenCatalogSkipped(t *testing.T) {
	calls, cleanup := setupPublishEnv(t, 0)
	defer cleanup()

	// No remotes configured → catalog step is skipped.
	if err := runPublish(nil, nil, false); err != nil {
		t.Fatalf("runPublish: %v", err)
	}
	if !hasBroadcast(*calls) {
		t.Errorf("expected broadcast call, got: %v", *calls)
	}
}

// TestBroadcastRunsAfterCatalogSync verifies that broadcast runs after a
// successful catalog sync (all four steps complete).
func TestBroadcastRunsAfterCatalogSync(t *testing.T) {
	calls, cleanup := setupPublishEnv(t, 0)
	defer cleanup()

	// Use --sync to feed a content remote so step 1 runs too.
	if err := runPublish([]string{"s3"}, nil, false); err != nil {
		t.Fatalf("runPublish: %v", err)
	}
	if !hasBroadcast(*calls) {
		t.Errorf("expected broadcast call, got: %v", *calls)
	}
}

// TestBroadcastNotCalledOnEarlierFailure verifies that broadcast is NOT called
// when an earlier step (bundle) fails.
func TestBroadcastNotCalledOnEarlierFailure(t *testing.T) {
	calls, cleanup := setupPublishEnv(t, 1) // non-zero exit → every exo call fails
	defer cleanup()

	err := runPublish(nil, nil, false)
	if err == nil {
		t.Fatal("expected runPublish to return an error")
	}
	if hasBroadcast(*calls) {
		t.Errorf("broadcast should not be called after earlier failure, got: %v", *calls)
	}
}

// TestNoGitRemoteAborts verifies that runPublish aborts before any step runs
// when the repo has a .git directory but no usable git remote.
func TestNoGitRemoteAborts(t *testing.T) {
	calls, cleanup := setupPublishEnv(t, 0)
	defer cleanup()

	// Override the default (configured-remote) stub installed by
	// setupPublishEnv to simulate no usable git remote.
	resolveCloneURL = func() (string, error) { return "", fmt.Errorf("no git remote configured") }

	err := runPublish(nil, nil, false)
	if err == nil {
		t.Fatal("expected runPublish to return an error")
	}
	if !strings.Contains(err.Error(), "git remote") {
		t.Errorf("expected error to mention 'git remote', got: %v", err)
	}
	if len(*calls) != 0 {
		t.Errorf("expected no exo subcommands to run, got: %v", *calls)
	}
}

// TestLocalPathOnlyOriginAbortsBeforeAnyStep is an end-to-end check (using
// the real commandutil.ResolveCloneURL against an actual git repo, not a
// stub) that a repo whose only remote is a local path aborts before any
// step runs — regression test for #100 case 4.
func TestLocalPathOnlyOriginAbortsBeforeAnyStep(t *testing.T) {
	repoDir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = repoDir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+repoDir)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("command %v failed: %v\n%s", args, err, out)
		}
	}
	run("git", "init")
	run("git", "remote", "add", "origin", "/srv/git/repo.git")

	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(oldDir) //nolint:errcheck

	recorded := &[]string{}
	oldCommand := command
	command = func(name string, args ...string) *exec.Cmd {
		*recorded = append(*recorded, name+" "+strings.Join(args, " "))
		return exec.Command("true")
	}
	oldResolveCloneURL := resolveCloneURL
	resolveCloneURL = commandutil.ResolveCloneURL
	defer func() {
		command = oldCommand
		resolveCloneURL = oldResolveCloneURL
	}()

	if err := runPublish(nil, nil, false); err == nil {
		t.Fatal("expected runPublish to error on a local-path-only origin")
	} else if !strings.Contains(err.Error(), "git remote") {
		t.Errorf("expected error to mention 'git remote', got: %v", err)
	}
	if len(*recorded) != 0 {
		t.Errorf("expected no exo subcommands to run, got: %v", *recorded)
	}
}

// extractArg returns the value immediately following flag in a recorded
// call string (e.g. extractArg("exo bundle --ref 1 --output /tmp/x --yes",
// "--output") == "/tmp/x"), used to find the temp bundle dir a call used.
func extractArg(call, flag string) (string, bool) {
	fields := strings.Fields(call)
	for i, f := range fields {
		if f == flag && i+1 < len(fields) {
			return fields[i+1], true
		}
	}
	return "", false
}

// TestPublishRefOrchestration verifies that --ref, per ref: runs the
// content-availability check ('exo sync --check --ref <ref> --verify'),
// builds a bundle into a fresh temporary directory ('exo bundle --ref <ref>
// --output <tmp> --yes'), and syncs catalog remotes from that temp dir
// ('exo sync --bundle-dir <tmp> --with ...'), in that order, with routing
// coming from the currently loaded .exohub/remotes rather than the ref
// itself — then removes the temp dir.
func TestPublishRefOrchestration(t *testing.T) {
	calls, cleanup := setupPublishEnv(t, 0)
	defer cleanup()

	remotesConfig := &initcmd.RemotesConfig{
		Remotes: []initcmd.RemoteConfig{
			{Name: "atlas", Type: "artifactdb", S3URL: "s3://bucket/new-prefix", AtlasURL: "https://atlas.example/sandbox1"},
		},
	}
	if err := initcmd.SaveRemotesConfig(remotesConfig); err != nil {
		t.Fatalf("SaveRemotesConfig: %v", err)
	}

	if err := runPublish(nil, []string{"1", "2"}, false); err != nil {
		t.Fatalf("runPublish: %v", err)
	}

	var bundleDirs []string
	for _, ref := range []string{"1", "2"} {
		checkIdx, bundleIdx, syncIdx := -1, -1, -1
		var dir string
		for i, c := range *calls {
			if strings.Contains(c, "sync --check --ref "+ref+" --verify") {
				checkIdx = i
			}
			if strings.Contains(c, "bundle --ref "+ref+" --output") {
				bundleIdx = i
				d, ok := extractArg(c, "--output")
				if !ok {
					t.Fatalf("could not find --output in bundle call: %q", c)
				}
				dir = d
			}
			if dir != "" && strings.Contains(c, "sync --bundle-dir "+dir+" --with atlas") {
				syncIdx = i
			}
		}
		if checkIdx < 0 || bundleIdx < 0 || syncIdx < 0 {
			t.Fatalf("missing expected calls for ref %q: check=%d bundle=%d sync=%d, calls=%v", ref, checkIdx, bundleIdx, syncIdx, *calls)
		}
		if !(checkIdx < bundleIdx && bundleIdx < syncIdx) {
			t.Errorf("expected check < bundle < sync ordering for ref %q, got check=%d bundle=%d sync=%d", ref, checkIdx, bundleIdx, syncIdx)
		}
		bundleDirs = append(bundleDirs, dir)
	}

	if !hasBroadcast(*calls) {
		t.Errorf("expected broadcast call, got: %v", *calls)
	}
	// No ref-less 'exo bundle --yes' should run once --ref is used.
	for _, c := range *calls {
		if strings.HasSuffix(c, "bundle --yes") {
			t.Errorf("did not expect a plain 'bundle --yes' call when --ref is used, got: %v", *calls)
		}
	}
	if len(bundleDirs) == 2 && bundleDirs[0] == bundleDirs[1] {
		t.Errorf("expected a distinct temp dir per ref, got the same for both: %v", bundleDirs)
	}
	for _, dir := range bundleDirs {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("expected temp bundle dir %q to be removed after publish, stat err = %v", dir, err)
		}
	}
}

// TestPublishRefForce verifies --force publishes even when the
// content-availability check fails, and that the check still runs (it is
// only its failure that is ignored).
func TestPublishRefForce(t *testing.T) {
	checkExitCode := 3 // fixable
	calls, cleanup := setupPublishEnvWithCheckExit(t, checkExitCode)
	defer cleanup()

	remotesConfig := &initcmd.RemotesConfig{
		Remotes: []initcmd.RemoteConfig{{Name: "atlas", Type: "artifactdb", S3URL: "s3://bucket/new-prefix"}},
	}
	if err := initcmd.SaveRemotesConfig(remotesConfig); err != nil {
		t.Fatalf("SaveRemotesConfig: %v", err)
	}

	if err := runPublish(nil, []string{"1"}, false); err == nil {
		t.Fatal("expected runPublish to refuse without --force when content is fixable/lost")
	} else if !strings.Contains(err.Error(), "annexed content") {
		t.Errorf("expected error to mention annexed content, got: %v", err)
	}
	for _, c := range *calls {
		if strings.Contains(c, "bundle --ref") {
			t.Errorf("did not expect a bundle call before the failed content check, got: %v", *calls)
		}
	}

	*calls = nil
	if err := runPublish(nil, []string{"1"}, true); err != nil {
		t.Fatalf("runPublish with --force: %v", err)
	}
	foundCheck, foundBundle := false, false
	for _, c := range *calls {
		if strings.Contains(c, "sync --check --ref 1 --verify") {
			foundCheck = true
		}
		if strings.Contains(c, "bundle --ref 1 --output") {
			foundBundle = true
		}
	}
	if foundCheck {
		t.Errorf("expected --force to skip the content-availability check entirely, got: %v", *calls)
	}
	if !foundBundle {
		t.Errorf("expected --force to still publish the ref, got: %v", *calls)
	}
}

// setupPublishEnvWithCheckExit is like setupPublishEnv, but the fake 'exo'
// subprocess exits checkExitCode specifically for a 'sync --check --ref'
// invocation (simulating a fixable/lost completeness result) and 0 for
// everything else, so bundle/sync calls after a forced publish still look
// like a successful run.
func setupPublishEnvWithCheckExit(t *testing.T, checkExitCode int) (calls *[]string, cleanup func()) {
	t.Helper()
	recorded, baseCleanup := setupPublishEnv(t, 0)
	oldCommand := command
	command = func(name string, args ...string) *exec.Cmd {
		*recorded = append(*recorded, name+" "+strings.Join(args, " "))
		for _, a := range args {
			if a == "--check" {
				return exec.Command("sh", "-c", "exit "+itoa(checkExitCode))
			}
		}
		return exec.Command("sh", "-c", "exit 0")
	}
	return recorded, func() {
		command = oldCommand
		baseCleanup()
	}
}

// runGitForTest runs a real git command in dir with an isolated HOME/author
// env, failing the test on error. Used by the real-git tests below (mapping
// and .artifactdb extraction), which exercise actual git plumbing rather
// than the 'exo' subprocess stub.
func runGitForTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1", "HOME="+dir,
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("command %v failed: %v\n%s", args, err, out)
	}
}

// chdirForTest chdirs into dir for the duration of the test, restoring the
// previous working directory via t.Cleanup.
func chdirForTest(t *testing.T, dir string) {
	t.Helper()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(oldWd) }) //nolint:errcheck
}

// TestValidateRefRemoteMapping_RefusesOnMissingCatalogRemote verifies that a
// ref referencing a catalog remote absent from the current config (by both
// name and UUID) is refused with a message naming the missing remote.
func TestValidateRefRemoteMapping_RefusesOnMissingCatalogRemote(t *testing.T) {
	repoDir := t.TempDir()
	chdirForTest(t, repoDir)
	runGitForTest(t, repoDir, "git", "init", "-q")

	oldRemotes := &initcmd.RemotesConfig{Remotes: []initcmd.RemoteConfig{
		{Name: "old-atlas", Type: "artifactdb", S3URL: "s3://bucket/old"},
	}}
	if err := initcmd.SaveRemotesConfig(oldRemotes); err != nil {
		t.Fatal(err)
	}
	runGitForTest(t, repoDir, "git", "add", ".exohub/remotes")
	runGitForTest(t, repoDir, "git", "commit", "-q", "-m", "old routing")
	runGitForTest(t, repoDir, "git", "tag", "1")

	currentConfig := &initcmd.RemotesConfig{Remotes: []initcmd.RemoteConfig{
		{Name: "new-atlas", Type: "artifactdb", S3URL: "s3://bucket/new"},
	}}

	err := validateRefRemoteMapping("1", currentConfig)
	if err == nil {
		t.Fatal("expected refusal: ref's catalog remote not present in current config")
	}
	if !strings.Contains(err.Error(), "old-atlas") {
		t.Errorf("expected error to name the missing remote, got: %v", err)
	}
}

// TestValidateRefRemoteMapping_AllowsUUIDMatchAcrossRename verifies that a
// catalog remote renamed between the ref and the current config is still
// recognized (and so not refused) when both record the same git-annex UUID.
func TestValidateRefRemoteMapping_AllowsUUIDMatchAcrossRename(t *testing.T) {
	repoDir := t.TempDir()
	chdirForTest(t, repoDir)
	runGitForTest(t, repoDir, "git", "init", "-q")

	oldRemotes := &initcmd.RemotesConfig{Remotes: []initcmd.RemoteConfig{
		{Name: "atlas-old-name", Type: "artifactdb", UUID: "same-uuid", S3URL: "s3://bucket/old"},
	}}
	if err := initcmd.SaveRemotesConfig(oldRemotes); err != nil {
		t.Fatal(err)
	}
	runGitForTest(t, repoDir, "git", "add", ".exohub/remotes")
	runGitForTest(t, repoDir, "git", "commit", "-q", "-m", "old routing")
	runGitForTest(t, repoDir, "git", "tag", "1")

	currentConfig := &initcmd.RemotesConfig{Remotes: []initcmd.RemoteConfig{
		{Name: "atlas-new-name", Type: "artifactdb", UUID: "same-uuid", S3URL: "s3://bucket/new"},
	}}

	if err := validateRefRemoteMapping("1", currentConfig); err != nil {
		t.Errorf("expected UUID match across rename to be allowed, got: %v", err)
	}
}

// TestValidateRefRemoteMapping_IgnoresContentRemotes verifies that a content
// (non-catalog) remote missing from the current config is never refused —
// only catalog remotes need a current-routing home.
func TestValidateRefRemoteMapping_IgnoresContentRemotes(t *testing.T) {
	repoDir := t.TempDir()
	chdirForTest(t, repoDir)
	runGitForTest(t, repoDir, "git", "init", "-q")

	oldRemotes := &initcmd.RemotesConfig{Remotes: []initcmd.RemoteConfig{
		{Name: "old-s3", Type: "annex", S3URL: "s3://bucket/old"},
	}}
	if err := initcmd.SaveRemotesConfig(oldRemotes); err != nil {
		t.Fatal(err)
	}
	runGitForTest(t, repoDir, "git", "add", ".exohub/remotes")
	runGitForTest(t, repoDir, "git", "commit", "-q", "-m", "old routing")
	runGitForTest(t, repoDir, "git", "tag", "1")

	if err := validateRefRemoteMapping("1", &initcmd.RemotesConfig{}); err != nil {
		t.Errorf("expected a missing content remote to be ignored, got: %v", err)
	}
}

// TestExtractArtifactDBFromRef_ExtractsFiles verifies that a ref's
// .artifactdb/ tree is git-archived into destDir.
func TestExtractArtifactDBFromRef_ExtractsFiles(t *testing.T) {
	repoDir := t.TempDir()
	chdirForTest(t, repoDir)
	runGitForTest(t, repoDir, "git", "init", "-q")

	if err := os.MkdirAll(filepath.Join(repoDir, ".artifactdb"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, ".artifactdb", "foo.json"), []byte(`{"a":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitForTest(t, repoDir, "git", "add", ".artifactdb")
	runGitForTest(t, repoDir, "git", "commit", "-q", "-m", "add artifactdb")
	runGitForTest(t, repoDir, "git", "tag", "1")

	destDir := t.TempDir()
	if err := extractArtifactDBFromRef("1", destDir); err != nil {
		t.Fatalf("extractArtifactDBFromRef: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(destDir, ".artifactdb", "foo.json"))
	if err != nil {
		t.Fatalf("expected extracted file, got: %v", err)
	}
	if string(data) != `{"a":1}` {
		t.Errorf("extracted content = %q, want %q", data, `{"a":1}`)
	}
}

// TestExtractArtifactDBFromRef_NoArtifactDBIsNotError verifies that a ref
// with no .artifactdb/ is handled gracefully (nil error, nothing extracted).
func TestExtractArtifactDBFromRef_NoArtifactDBIsNotError(t *testing.T) {
	repoDir := t.TempDir()
	chdirForTest(t, repoDir)
	runGitForTest(t, repoDir, "git", "init", "-q")
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitForTest(t, repoDir, "git", "add", "README.md")
	runGitForTest(t, repoDir, "git", "commit", "-q", "-m", "no artifactdb")
	runGitForTest(t, repoDir, "git", "tag", "1")

	destDir := t.TempDir()
	if err := extractArtifactDBFromRef("1", destDir); err != nil {
		t.Fatalf("expected no error for a ref without .artifactdb/, got: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destDir, ".artifactdb")); !os.IsNotExist(err) {
		t.Errorf("expected nothing extracted, got stat err = %v", err)
	}
}

// TestVerifyRefContentAvailable_ExitCodes verifies the exit-code mapping of
// 'exo sync --check --ref <ref> --verify': 0 clean, 3/4 refuse, anything
// else is a generic failure.
func TestVerifyRefContentAvailable_ExitCodes(t *testing.T) {
	tests := []struct {
		name      string
		exitCode  int
		wantError bool
	}{
		{"clean", 0, false},
		{"fixable", 3, true},
		{"lost", 4, true},
		{"generic failure", 1, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, cleanup := setupPublishEnv(t, tt.exitCode)
			defer cleanup()
			err := verifyRefContentAvailable("1")
			if tt.wantError && err == nil {
				t.Errorf("exit %d: expected an error, got nil", tt.exitCode)
			}
			if !tt.wantError && err != nil {
				t.Errorf("exit %d: expected no error, got: %v", tt.exitCode, err)
			}
		})
	}
}

func sliceEqual(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
