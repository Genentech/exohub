package provenance

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runInstallHookCmd(t *testing.T) (stdout string, err error) {
	t.Helper()
	cmd := newInstallHookCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	err = cmd.Execute()
	return buf.String(), err
}

func TestInstallHook_CreatesHook(t *testing.T) {
	root, _ := addTestRepo(t, false)
	chdir(t, root)

	if _, err := runInstallHookCmd(t); err != nil {
		t.Fatalf("install-hook error = %v", err)
	}

	hookPath := filepath.Join(root, ".git", "hooks", "pre-commit")
	data, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatalf("read hook: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, hookBeginMarker) || !strings.Contains(content, hookEndMarker) {
		t.Errorf("hook content = %q, want the marked block", content)
	}
	if !strings.Contains(content, "exo provenance validate --staged") {
		t.Errorf("hook content = %q, want it to run validate --staged", content)
	}
	if !strings.HasPrefix(content, "#!") {
		t.Errorf("hook content = %q, want a shebang", content)
	}

	info, err := os.Stat(hookPath)
	if err != nil {
		t.Fatalf("stat hook: %v", err)
	}
	if info.Mode()&0o100 == 0 {
		t.Errorf("hook mode = %v, want executable", info.Mode())
	}
}

// TestInstallHook_Idempotent covers the "hook installation keeps existing
// hooks" done-when case: running install-hook twice keeps any existing
// hook content and the marked block appears exactly once.
func TestInstallHook_Idempotent(t *testing.T) {
	root, _ := addTestRepo(t, false)
	chdir(t, root)

	hookPath := filepath.Join(root, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(hookPath, []byte("#!/bin/sh\necho existing-hook\n"), 0o755); err != nil {
		t.Fatalf("write existing hook: %v", err)
	}

	if _, err := runInstallHookCmd(t); err != nil {
		t.Fatalf("install-hook (1st) error = %v", err)
	}
	first, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatalf("read hook: %v", err)
	}
	if !strings.Contains(string(first), "echo existing-hook") {
		t.Errorf("hook content = %q, want existing content kept", string(first))
	}

	out, err := runInstallHookCmd(t)
	if err != nil {
		t.Fatalf("install-hook (2nd) error = %v", err)
	}
	if !strings.Contains(out, "already up to date") {
		t.Errorf("2nd run output = %q, want it to report no change", out)
	}

	second, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatalf("read hook: %v", err)
	}
	if string(first) != string(second) {
		t.Errorf("hook content changed on 2nd run:\n1st: %q\n2nd: %q", string(first), string(second))
	}
	if n := strings.Count(string(second), hookBeginMarker); n != 1 {
		t.Errorf("marked block appears %d times, want exactly once", n)
	}
	if !strings.Contains(string(second), "echo existing-hook") {
		t.Errorf("hook content = %q, want existing content kept", string(second))
	}
}

// TestInstallHook_ExistingHookEndingInExec covers the review fix: a hook
// ending in `exec ...` (a common pattern that replaces the current process
// and never returns) must still run the provenance check, so the block
// must be inserted before it, not appended after.
func TestInstallHook_ExistingHookEndingInExec(t *testing.T) {
	root, _ := addTestRepo(t, false)
	chdir(t, root)

	hookPath := filepath.Join(root, ".git", "hooks", "pre-commit")
	existing := "#!/bin/sh\necho pre-existing\nexec other-hook \"$@\"\n"
	if err := os.WriteFile(hookPath, []byte(existing), 0o755); err != nil {
		t.Fatalf("write existing hook: %v", err)
	}

	if _, err := runInstallHookCmd(t); err != nil {
		t.Fatalf("install-hook error = %v", err)
	}

	data, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatalf("read hook: %v", err)
	}
	content := string(data)
	blockIdx := strings.Index(content, hookBeginMarker)
	execIdx := strings.Index(content, "exec other-hook")
	if blockIdx < 0 || execIdx < 0 {
		t.Fatalf("hook content = %q, want both the marked block and the exec line", content)
	}
	if blockIdx > execIdx {
		t.Errorf("hook content = %q, want the marked block before the exec line (an appended block after exec never runs)", content)
	}
	if !strings.Contains(content, "echo pre-existing") {
		t.Errorf("hook content = %q, want existing content kept", content)
	}
}

// TestInstallHook_CreatesMissingHooksDirectory covers the review fix: if
// core.hooksPath points to a directory that does not exist on disk yet,
// install-hook must create it rather than failing.
func TestInstallHook_CreatesMissingHooksDirectory(t *testing.T) {
	root, run := addTestRepo(t, false)
	chdir(t, root)
	run("git", "config", "core.hooksPath", "custom-hooks")

	if _, err := runInstallHookCmd(t); err != nil {
		t.Fatalf("install-hook error = %v", err)
	}

	hookPath := filepath.Join(root, "custom-hooks", "pre-commit")
	data, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatalf("read hook at %s: %v", hookPath, err)
	}
	if !strings.Contains(string(data), hookBeginMarker) {
		t.Errorf("hook content = %q, want the marked block", string(data))
	}
}

func TestInstallHookBlock_AppendsToEmptyFile(t *testing.T) {
	updated, changed := installHookBlock("")
	if !changed {
		t.Error("changed = false, want true")
	}
	if !strings.HasPrefix(updated, "#!/bin/sh\n") {
		t.Errorf("updated = %q, want a shebang", updated)
	}
	if strings.Count(updated, hookBeginMarker) != 1 {
		t.Errorf("updated = %q, want exactly one marked block", updated)
	}
}

func TestInstallHookBlock_ReplacesExistingBlockInPlace(t *testing.T) {
	existing, _ := installHookBlock("#!/bin/sh\necho before\n")
	again, changed := installHookBlock(existing)
	if changed {
		t.Errorf("changed = true, want false (no-op on identical content)")
	}
	if again != existing {
		t.Errorf("again = %q, want unchanged %q", again, existing)
	}
}

func TestInstallHookBlock_InsertsAfterShebangBeforeExistingContent(t *testing.T) {
	updated, changed := installHookBlock("#!/bin/sh\nexec other-hook\n")
	if !changed {
		t.Error("changed = false, want true")
	}
	if !strings.HasPrefix(updated, "#!/bin/sh\n"+hookBeginMarker) {
		t.Errorf("updated = %q, want the marked block immediately after the shebang", updated)
	}
	if strings.Index(updated, hookBeginMarker) > strings.Index(updated, "exec other-hook") {
		t.Errorf("updated = %q, want the marked block before the exec line", updated)
	}
}
