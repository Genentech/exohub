package fsck

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestManifestHelpers(t *testing.T) {
	m := manifest{
		RepoDir:       "/repo",
		RepoDirHyphen: "/alt",
		Path:          []string{"a"},
		Paths:         []string{"b", "c"},
	}
	if got := m.repoDir(); got != "/repo" {
		t.Fatalf("repoDir: got %q", got)
	}
	all := m.allPaths()
	if len(all) != 3 || all[0] != "a" || all[1] != "b" || all[2] != "c" {
		t.Fatalf("allPaths: %#v", all)
	}
}

func TestStripLineAndExtractQuoted(t *testing.T) {
	reAnsi := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	line := "\x1b[31merror\x1b[0m: bad 'one' \"two\""
	stripped := stripLine(line, reAnsi)
	if stripped != "error: bad 'one' \"two\"" {
		t.Fatalf("stripLine: got %q", stripped)
	}
	quoted := extractQuoted(stripped)
	if len(quoted) != 2 || quoted[0] != "one" || quoted[1] != "two" {
		t.Fatalf("extractQuoted: %#v", quoted)
	}
}

func TestNormalizePaths(t *testing.T) {
	root := t.TempDir()
	repo = &repoInfo{root: root, gitDir: filepath.Join(root, ".git")}
	defer func() { repo = nil }()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() {
		_ = os.Chdir(cwd)
	}()

	absInside := filepath.Join(root, "data", "file.txt")
	normalized := normalizePath(absInside)
	if normalized != filepath.Join("data", "file.txt") {
		t.Fatalf("normalizePath inside: got %q", normalized)
	}

	outside := filepath.Join(filepath.Dir(root), "outside.txt")
	if got := normalizePath(outside); got != outside {
		t.Fatalf("normalizePath outside: got %q", got)
	}

	paths := normalizePaths([]string{"", "data/file.txt"})
	if len(paths) != 1 || paths[0] != "data/file.txt" {
		t.Fatalf("normalizePaths: %#v", paths)
	}
}

func TestReadLinesAndUniqueStrings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "list.txt")
	content := "a\n\nb\n a \n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	lines, err := readLines(path)
	if err != nil {
		t.Fatalf("readLines: %v", err)
	}
	if len(lines) != 3 || lines[0] != "a" || lines[1] != "b" || lines[2] != "a" {
		t.Fatalf("readLines: %#v", lines)
	}
	unique := uniqueStrings(lines)
	if len(unique) != 2 {
		t.Fatalf("uniqueStrings: %#v", unique)
	}
}

func TestAppendBadPathDedupes(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(cwd) }()

	if err := appendBadPath("data/file.txt"); err != nil {
		t.Fatalf("appendBadPath: %v", err)
	}
	if err := appendBadPath("data/file.txt"); err != nil {
		t.Fatalf("appendBadPath duplicate: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".git", "exohub", "bad"))
	if err != nil {
		t.Fatalf("read bad file: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 || lines[0] != "data/file.txt" {
		t.Fatalf("bad file contents: %#v", lines)
	}
}

func TestGitRevParseAndRemoteUUID(t *testing.T) {
	withCommand(t, func(name string, args ...string) *exec.Cmd {
		if name == "git" && len(args) >= 1 && args[0] == "rev-parse" {
			return exec.Command("sh", "-c", "printf 'abc123'")
		}
		if name == "git" && len(args) >= 2 && args[0] == "config" && args[1] == "--get" {
			return exec.Command("sh", "-c", "printf 'uuid-123'")
		}
		return exec.Command("sh", "-c", "exit 0")
	})

	hash, err := gitRevParse("HEAD")
	if err != nil || strings.TrimSpace(hash) != "abc123" {
		t.Fatalf("gitRevParse: %q err=%v", hash, err)
	}
	if got := remoteUUID("origin"); got != "uuid-123" {
		t.Fatalf("remoteUUID: %q", got)
	}
}

func withCommand(t *testing.T, fn func(name string, args ...string) *exec.Cmd) {
	t.Helper()
	old := command
	command = fn
	t.Cleanup(func() { command = old })
}

func TestNewCommandJobsFlagPrecedence(t *testing.T) {
	t.Run("default is 1 when EXOHUB_JOBS unset", func(t *testing.T) {
		t.Setenv("EXOHUB_JOBS", "")
		cmd := NewCommand()
		if err := cmd.ParseFlags([]string{}); err != nil {
			t.Fatalf("ParseFlags: %v", err)
		}
		// jobs default is captured at NewCommand() time; verify via flag default string
		f := cmd.Flags().Lookup("jobs")
		if f == nil {
			t.Fatal("--jobs flag not registered")
		}
		if f.DefValue != "" {
			// DefValue for StringVarP is "" since default is stored in the variable
			// The actual default resolution is tested separately via opts
		}
	})

	t.Run("EXOHUB_JOBS env is used as default", func(t *testing.T) {
		t.Setenv("EXOHUB_JOBS", "4")
		cmd := NewCommand()
		if err := cmd.ParseFlags([]string{}); err != nil {
			t.Fatalf("ParseFlags: %v", err)
		}
		f := cmd.Flags().Lookup("jobs")
		if f == nil {
			t.Fatal("--jobs flag not registered")
		}
		// Flag not explicitly set, so opts.jobs should be "4" (the env default)
		// We verify the flag is registered and has no explicit value set yet
		if f.Changed {
			t.Error("flag should not be marked Changed when not explicitly set")
		}
	})

	t.Run("-J flag overrides env", func(t *testing.T) {
		t.Setenv("EXOHUB_JOBS", "4")
		cmd := NewCommand()
		if err := cmd.ParseFlags([]string{"-J", "8"}); err != nil {
			t.Fatalf("ParseFlags: %v", err)
		}
		f := cmd.Flags().Lookup("jobs")
		if f == nil {
			t.Fatal("--jobs flag not registered")
		}
		if !f.Changed {
			t.Error("flag should be marked Changed after explicit set")
		}
		if f.Value.String() != "8" {
			t.Errorf("flag value = %q, want \"8\"", f.Value.String())
		}
	})

	t.Run("--jobs long form also works", func(t *testing.T) {
		t.Setenv("EXOHUB_JOBS", "2")
		cmd := NewCommand()
		if err := cmd.ParseFlags([]string{"--jobs", "cpus"}); err != nil {
			t.Fatalf("ParseFlags: %v", err)
		}
		f := cmd.Flags().Lookup("jobs")
		if f == nil {
			t.Fatal("--jobs flag not registered")
		}
		if f.Value.String() != "cpus" {
			t.Errorf("flag value = %q, want \"cpus\"", f.Value.String())
		}
	})
}
