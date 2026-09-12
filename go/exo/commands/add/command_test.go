package add

import (
	"bufio"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestNewCommand(t *testing.T) {
	cmd := NewCommand()
	if cmd == nil {
		t.Fatal("NewCommand() returned nil")
	}
	if cmd.Use == "" {
		t.Error("Use should not be empty")
	}
	if cmd.Short == "" {
		t.Error("Short should not be empty")
	}
	if cmd.Long == "" {
		t.Error("Long should not be empty")
	}
	if cmd.Example == "" {
		t.Error("Example should not be empty")
	}
}

func TestExtractMode(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantMode  string
		wantForce bool
		wantRest  []string
	}{
		{"no mode flag", []string{"file1.csv", "file2.csv"}, "auto", false, []string{"file1.csv", "file2.csv"}},
		{"annex mode", []string{"--annex", "file.txt"}, "annex", false, []string{"file.txt"}},
		{"git mode", []string{"--git", "image.png"}, "git", false, []string{"image.png"}},
		{"mode flag in middle", []string{"-J", "4", "--annex", "file.txt"}, "annex", false, []string{"-J", "4", "file.txt"}},
		{"empty args", []string{}, "auto", false, nil},
		{"force with annex", []string{"--annex", "--force", "file.txt"}, "annex", true, []string{"file.txt"}},
		{"force with git", []string{"--git", "--force", "data.csv"}, "git", true, []string{"data.csv"}},
		{"force alone", []string{"--force", "file.txt"}, "auto", true, []string{"file.txt"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mode, force, rest := extractMode(tt.args)
			if mode != tt.wantMode {
				t.Errorf("mode = %q, want %q", mode, tt.wantMode)
			}
			if force != tt.wantForce {
				t.Errorf("force = %v, want %v", force, tt.wantForce)
			}
			if !reflect.DeepEqual(rest, tt.wantRest) {
				t.Errorf("remaining = %v, want %v", rest, tt.wantRest)
			}
		})
	}
}

func TestIsTextFile(t *testing.T) {
	tmpDir := t.TempDir()

	textPath := filepath.Join(tmpDir, "text.txt")
	os.WriteFile(textPath, []byte("Hello, this is plain text content.\n"), 0644)

	binPath := filepath.Join(tmpDir, "image.png")
	os.WriteFile(binPath, []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), 0644)

	emptyPath := filepath.Join(tmpDir, "empty.txt")
	os.WriteFile(emptyPath, []byte{}, 0644)

	jsonPath := filepath.Join(tmpDir, "data.json")
	os.WriteFile(jsonPath, []byte(`{"key": "value"}`), 0644)

	dirPath := filepath.Join(tmpDir, "subdir")
	os.MkdirAll(dirPath, 0755)

	tests := []struct {
		name string
		path string
		want bool
	}{
		{"text file", textPath, true},
		{"binary file", binPath, false},
		{"empty file", emptyPath, true},
		{"json file", jsonPath, true},
		{"directory", dirPath, false},
		{"nonexistent file", filepath.Join(tmpDir, "nope.txt"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsTextFile(tt.path); got != tt.want {
				t.Errorf("IsTextFile(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestIsURL(t *testing.T) {
	tests := []struct {
		arg  string
		want bool
	}{
		{"https://example.com/data.csv", true},
		{"http://example.com/data.csv", true},
		{"ftp://mirror.example.com/dataset.tar.gz", true},
		{"magnet:?xt=urn:btih:abc123", true},
		{"local-file.csv", false},
		{"./relative/path.csv", false},
		{"/absolute/path.csv", false},
		{"", false},
		{"file://local.txt", false},
		{"ssh://host/path", false},
	}
	for _, tt := range tests {
		t.Run(tt.arg, func(t *testing.T) {
			if got := isURL(tt.arg); got != tt.want {
				t.Errorf("isURL(%q) = %v, want %v", tt.arg, got, tt.want)
			}
		})
	}
}

func TestFilenameFromURL(t *testing.T) {
	tests := []struct {
		rawURL string
		want   string
	}{
		{"https://server.com/db/files/archive.tgz", "archive.tgz"},
		{"https://example.com/data.csv", "data.csv"},
		{"ftp://mirror.example.com/path/to/dataset.tar.gz", "dataset.tar.gz"},
		{"https://example.com/", ""},
		{"https://example.com", ""},
		{"https://example.com/file?query=1&foo=bar", "file"},
		{"https://example.com/path/to/file.txt#fragment", "file.txt"},
		{"://invalid", ""},
	}
	for _, tt := range tests {
		t.Run(tt.rawURL, func(t *testing.T) {
			if got := filenameFromURL(tt.rawURL); got != tt.want {
				t.Errorf("filenameFromURL(%q) = %q, want %q", tt.rawURL, got, tt.want)
			}
		})
	}
}

func TestHasFileFlag(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{"no flags", []string{"some-url"}, false},
		{"--file present", []string{"--file", "output.csv"}, true},
		{"--file= present", []string{"--file=output.csv"}, true},
		{"other flags", []string{"-J", "4"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasFileFlag(tt.args); got != tt.want {
				t.Errorf("hasFileFlag(%v) = %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}

func TestSplitArgs(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		locals []string
		urls   []string
		flags  []string
	}{
		{"all local", []string{"file1.csv", "file2.csv"}, []string{"file1.csv", "file2.csv"}, nil, nil},
		{"all urls", []string{"https://example.com/data.csv", "ftp://mirror.com/file.tar.gz"}, nil, []string{"https://example.com/data.csv", "ftp://mirror.com/file.tar.gz"}, nil},
		{"mixed", []string{"local.csv", "https://example.com/remote.csv", "-J", "4"}, []string{"local.csv"}, []string{"https://example.com/remote.csv"}, []string{"-J", "4"}},
		{"flags only", []string{"-J", "4", "--file", "out.csv"}, nil, nil, []string{"-J", "4", "--file", "out.csv"}},
		{"empty", []string{}, nil, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			locals, urls, flags := splitArgs(tt.args)
			if !reflect.DeepEqual(locals, tt.locals) {
				t.Errorf("locals = %v, want %v", locals, tt.locals)
			}
			if !reflect.DeepEqual(urls, tt.urls) {
				t.Errorf("urls = %v, want %v", urls, tt.urls)
			}
			if !reflect.DeepEqual(flags, tt.flags) {
				t.Errorf("flags = %v, want %v", flags, tt.flags)
			}
		})
	}
}

func TestClassifyFiles(t *testing.T) {
	tmpDir := t.TempDir()

	// Create individual files
	textFile := filepath.Join(tmpDir, "readme.md")
	os.WriteFile(textFile, []byte("# Hello\nThis is markdown"), 0644)

	jsonFile := filepath.Join(tmpDir, "data.json")
	os.WriteFile(jsonFile, []byte(`{"key": "value"}`), 0644)

	binFile := filepath.Join(tmpDir, "image.png")
	os.WriteFile(binFile, []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), 0644)

	// Create a directory with mixed content
	subDir := filepath.Join(tmpDir, "mydir")
	os.MkdirAll(subDir, 0755)
	os.WriteFile(filepath.Join(subDir, "notes.txt"), []byte("some notes"), 0644)
	os.WriteFile(filepath.Join(subDir, "config.json"), []byte(`{"a":1}`), 0644)
	os.WriteFile(filepath.Join(subDir, "data.parquet"), []byte("\x89PNG\r\n\x1a\n"), 0644) // binary

	// Create nested directory
	nestedDir := filepath.Join(subDir, "nested")
	os.MkdirAll(nestedDir, 0755)
	os.WriteFile(filepath.Join(nestedDir, "deep.csv"), []byte("a,b,c\n1,2,3\n"), 0644)

	t.Run("individual files", func(t *testing.T) {
		git, annex, largeText := classifyFiles([]string{textFile, jsonFile, binFile})
		if len(git) != 2 {
			t.Errorf("expected 2 git files, got %d: %v", len(git), git)
		}
		if len(annex) != 1 {
			t.Errorf("expected 1 annex file, got %d: %v", len(annex), annex)
		}
		if len(largeText) != 0 {
			t.Errorf("expected 0 large text files, got %d: %v", len(largeText), largeText)
		}
	})

	t.Run("directory expands to individual files", func(t *testing.T) {
		git, annex, largeText := classifyFiles([]string{subDir})
		// notes.txt + config.json + deep.csv = 3 text, data.parquet = 1 binary
		if len(git) != 3 {
			t.Errorf("expected 3 git files, got %d: %v", len(git), git)
		}
		if len(annex) != 1 {
			t.Errorf("expected 1 annex file, got %d: %v", len(annex), annex)
		}
		if len(largeText) != 0 {
			t.Errorf("expected 0 large text files, got %d: %v", len(largeText), largeText)
		}
	})

	t.Run("mixed files and directories", func(t *testing.T) {
		git, annex, largeText := classifyFiles([]string{textFile, binFile, subDir})
		// textFile + 3 from subDir = 4 text, binFile + 1 from subDir = 2 binary
		if len(git) != 4 {
			t.Errorf("expected 4 git files, got %d: %v", len(git), git)
		}
		if len(annex) != 2 {
			t.Errorf("expected 2 annex files, got %d: %v", len(annex), annex)
		}
		if len(largeText) != 0 {
			t.Errorf("expected 0 large text files, got %d: %v", len(largeText), largeText)
		}
	})

	t.Run("empty directory", func(t *testing.T) {
		emptyDir := filepath.Join(tmpDir, "empty")
		os.MkdirAll(emptyDir, 0755)
		git, annex, largeText := classifyFiles([]string{emptyDir})
		if len(git) != 0 || len(annex) != 0 || len(largeText) != 0 {
			t.Errorf("expected 0 files, got git=%d annex=%d largeText=%d", len(git), len(annex), len(largeText))
		}
	})

	t.Run("nonexistent path goes to annex", func(t *testing.T) {
		git, annex, largeText := classifyFiles([]string{filepath.Join(tmpDir, "nope")})
		if len(git) != 0 {
			t.Errorf("expected 0 git files, got %d", len(git))
		}
		if len(annex) != 1 {
			t.Errorf("expected 1 annex file, got %d", len(annex))
		}
		if len(largeText) != 0 {
			t.Errorf("expected 0 large text files, got %d", len(largeText))
		}
	})

	t.Run("large text file flagged", func(t *testing.T) {
		largeCSV := filepath.Join(tmpDir, "big.csv")
		// Create a file larger than the threshold (5 MB)
		data := make([]byte, largeTextFileThreshold+1)
		for i := range data {
			data[i] = 'a'
		}
		os.WriteFile(largeCSV, data, 0644)

		git, annex, largeText := classifyFiles([]string{largeCSV})
		if len(git) != 0 {
			t.Errorf("expected 0 git files, got %d: %v", len(git), git)
		}
		if len(annex) != 0 {
			t.Errorf("expected 0 annex files, got %d: %v", len(annex), annex)
		}
		if len(largeText) != 1 {
			t.Errorf("expected 1 large text file, got %d: %v", len(largeText), largeText)
		}
	})

	t.Run("small text file under threshold stays in git", func(t *testing.T) {
		smallCSV := filepath.Join(tmpDir, "small.csv")
		os.WriteFile(smallCSV, []byte("a,b,c\n1,2,3\n"), 0644)

		git, annex, largeText := classifyFiles([]string{smallCSV})
		if len(git) != 1 {
			t.Errorf("expected 1 git file, got %d (annex=%d, largeText=%d)", len(git), len(annex), len(largeText))
		}
		if len(largeText) != 0 {
			t.Errorf("expected 0 large text files, got %d", len(largeText))
		}
	})
}

func TestBuildAddURLArgs(t *testing.T) {
	jobs := []string{"-J", "1"}

	t.Run("no --file: uses --preserve-filename and URL-parsed filename", func(t *testing.T) {
		got := buildAddURLArgs("https://example.com/data.csv", jobs, nil)
		want := []string{"annex", "addurl", "-J", "1", "--preserve-filename", "--file", "data.csv", "https://example.com/data.csv"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("no --file, URL with query and fragment: strips both", func(t *testing.T) {
		got := buildAddURLArgs("https://example.com/report.csv?token=abc#section", jobs, nil)
		want := []string{"annex", "addurl", "-J", "1", "--preserve-filename", "--file", "report.csv", "https://example.com/report.csv?token=abc#section"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("no --file, URL with no path component: omits --file fallback", func(t *testing.T) {
		got := buildAddURLArgs("https://example.com/", jobs, nil)
		want := []string{"annex", "addurl", "-J", "1", "--preserve-filename", "https://example.com/"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("--file flag overrides server filename", func(t *testing.T) {
		got := buildAddURLArgs("https://example.com/data.csv", jobs, []string{"--file", "raw/output.csv"})
		want := []string{"annex", "addurl", "-J", "1", "--file", "raw/output.csv", "https://example.com/data.csv"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("--file= form overrides server filename", func(t *testing.T) {
		got := buildAddURLArgs("https://example.com/data.csv", jobs, []string{"--file=subdir/data.csv"})
		want := []string{"annex", "addurl", "-J", "1", "--file=subdir/data.csv", "https://example.com/data.csv"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("no jobs args", func(t *testing.T) {
		got := buildAddURLArgs("https://example.com/archive.tar.gz", nil, nil)
		want := []string{"annex", "addurl", "--preserve-filename", "--file", "archive.tar.gz", "https://example.com/archive.tar.gz"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})
}

func TestJobsFlags(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		os.Unsetenv("EXOHUB_JOBS")
		got := jobsFlags(nil)
		if !reflect.DeepEqual(got, []string{"-J", "1"}) {
			t.Errorf("got %v, want [-J 1]", got)
		}
	})

	t.Run("env set", func(t *testing.T) {
		t.Setenv("EXOHUB_JOBS", "8")
		got := jobsFlags(nil)
		if !reflect.DeepEqual(got, []string{"-J", "8"}) {
			t.Errorf("got %v, want [-J 8]", got)
		}
	})

	t.Run("already has flag", func(t *testing.T) {
		got := jobsFlags([]string{"-J", "4"})
		if got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})
}

func TestHasJobsFlag(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{"empty", nil, false},
		{"short -J with value", []string{"-J", "4"}, true},
		{"long --jobs with value", []string{"--jobs", "4"}, true},
		{"--jobs= form", []string{"--jobs=4"}, true},
		{"-J concatenated", []string{"-J4"}, true},
		{"cpus value", []string{"-J", "cpus"}, true},
		{"other flags only", []string{"--annex", "--force"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasJobsFlag(tt.args); got != tt.want {
				t.Errorf("hasJobsFlag(%v) = %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}

func TestJobsFlagEnvDefaultPrecedence(t *testing.T) {
	// flag → env → default precedence for exo add
	// (jobsFlags is the single source of truth when no flag is in args)

	t.Run("no flag no env → default 1", func(t *testing.T) {
		os.Unsetenv("EXOHUB_JOBS")
		got := jobsFlags([]string{"data/"})
		if !reflect.DeepEqual(got, []string{"-J", "1"}) {
			t.Errorf("got %v, want [-J 1]", got)
		}
	})

	t.Run("no flag with env → env value", func(t *testing.T) {
		t.Setenv("EXOHUB_JOBS", "6")
		got := jobsFlags([]string{"data/"})
		if !reflect.DeepEqual(got, []string{"-J", "6"}) {
			t.Errorf("got %v, want [-J 6]", got)
		}
	})

	t.Run("-J flag overrides env", func(t *testing.T) {
		t.Setenv("EXOHUB_JOBS", "6")
		// When -J is in user flags, jobsFlags returns nil and
		// the user flag is used directly in the git-annex invocation.
		got := jobsFlags([]string{"-J", "3"})
		if got != nil {
			t.Errorf("got %v, want nil (user flag takes over)", got)
		}
	})

	t.Run("--jobs long form overrides env", func(t *testing.T) {
		t.Setenv("EXOHUB_JOBS", "6")
		got := jobsFlags([]string{"--jobs", "2"})
		if got != nil {
			t.Errorf("got %v, want nil (user flag takes over)", got)
		}
	})

	t.Run("-J cpus accepted", func(t *testing.T) {
		os.Unsetenv("EXOHUB_JOBS")
		got := jobsFlags([]string{"-J", "cpus"})
		if got != nil {
			t.Errorf("got %v, want nil (user flag takes over)", got)
		}
	})
}

// initGitRepo creates a temporary directory with a minimal git repo.
func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

// stageFileAt stages a file in the git index at the given mode using
// git hash-object + git update-index, so we can control the staged mode
// without running git annex.
func stageFileAt(t *testing.T, dir, rel string, content []byte, mode string) {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, content, 0644); err != nil {
		t.Fatal(err)
	}
	// hash-object the file content into the object store
	cmd := exec.Command("git", "hash-object", "-w", "--stdin")
	cmd.Dir = dir
	cmd.Stdin = bytes.NewReader(content)
	hashOut, err := cmd.Output()
	if err != nil {
		t.Fatalf("git hash-object: %v", err)
	}
	hash := strings.TrimSpace(string(hashOut))
	// stage it at the requested mode
	indexLine := mode + " " + hash + " 0\t" + rel
	upd := exec.Command("git", "update-index", "--index-info")
	upd.Dir = dir
	upd.Stdin = strings.NewReader(indexLine + "\n")
	if out, err := upd.CombinedOutput(); err != nil {
		t.Fatalf("git update-index --index-info: %v\n%s", err, out)
	}
}

// indexModeOf returns the mode string (e.g. "100644" or "100755") for the
// file at rel as currently recorded in the git index of dir.
func indexModeOf(t *testing.T, dir, rel string) string {
	t.Helper()
	cmd := exec.Command("git", "ls-files", "-s", "--", rel)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		return ""
	}
	sc := bufio.NewScanner(bytes.NewReader(out))
	if sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) > 0 {
			return fields[0]
		}
	}
	return ""
}

func TestReconcileExecBit(t *testing.T) {
	content := []byte("#!/bin/sh\necho hello\n")

	t.Run("executable file at 100644 gets upgraded to 100755", func(t *testing.T) {
		dir := initGitRepo(t)
		orig, _ := os.Getwd()
		defer os.Chdir(orig)
		if err := os.Chdir(dir); err != nil {
			t.Fatal(err)
		}
		stageFileAt(t, dir, "tool.sh", content, "100644")
		if err := os.Chmod(filepath.Join(dir, "tool.sh"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := reconcileExecBit([]string{"tool.sh"}); err != nil {
			t.Fatalf("reconcileExecBit: %v", err)
		}
		if got := indexModeOf(t, dir, "tool.sh"); got != "100755" {
			t.Errorf("index mode = %q, want 100755", got)
		}
	})

	t.Run("non-executable file at 100644 is left alone", func(t *testing.T) {
		dir := initGitRepo(t)
		orig, _ := os.Getwd()
		defer os.Chdir(orig)
		if err := os.Chdir(dir); err != nil {
			t.Fatal(err)
		}
		stageFileAt(t, dir, "data.bin", []byte("\x00\x01\x02"), "100644")
		if err := os.Chmod(filepath.Join(dir, "data.bin"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := reconcileExecBit([]string{"data.bin"}); err != nil {
			t.Fatalf("reconcileExecBit: %v", err)
		}
		if got := indexModeOf(t, dir, "data.bin"); got != "100644" {
			t.Errorf("index mode = %q, want 100644", got)
		}
	})

	t.Run("already staged at 100755 is left alone (idempotent)", func(t *testing.T) {
		dir := initGitRepo(t)
		orig, _ := os.Getwd()
		defer os.Chdir(orig)
		if err := os.Chdir(dir); err != nil {
			t.Fatal(err)
		}
		stageFileAt(t, dir, "already.sh", content, "100755")
		if err := os.Chmod(filepath.Join(dir, "already.sh"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := reconcileExecBit([]string{"already.sh"}); err != nil {
			t.Fatalf("reconcileExecBit: %v", err)
		}
		if got := indexModeOf(t, dir, "already.sh"); got != "100755" {
			t.Errorf("index mode = %q, want 100755", got)
		}
	})

	t.Run("symlink is skipped", func(t *testing.T) {
		dir := initGitRepo(t)
		orig, _ := os.Getwd()
		defer os.Chdir(orig)
		if err := os.Chdir(dir); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(dir, "target.sh")
		if err := os.WriteFile(target, content, 0755); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(dir, "link.sh")
		_ = os.Remove(link)
		if err := os.Symlink(target, link); err != nil {
			t.Skip("symlinks not supported:", err)
		}
		if err := reconcileExecBit([]string{"link.sh"}); err != nil {
			t.Fatalf("reconcileExecBit: %v", err)
		}
		if got := indexModeOf(t, dir, "link.sh"); got != "" {
			t.Errorf("unexpected index entry for symlink: %q", got)
		}
	})

	t.Run("empty list is a no-op", func(t *testing.T) {
		dir := initGitRepo(t)
		orig, _ := os.Getwd()
		defer os.Chdir(orig)
		if err := os.Chdir(dir); err != nil {
			t.Fatal(err)
		}
		if err := reconcileExecBit(nil); err != nil {
			t.Fatalf("reconcileExecBit(nil): %v", err)
		}
	})

	t.Run("unrelated staged file is not modified", func(t *testing.T) {
		dir := initGitRepo(t)
		orig, _ := os.Getwd()
		defer os.Chdir(orig)
		if err := os.Chdir(dir); err != nil {
			t.Fatal(err)
		}
		stageFileAt(t, dir, "other.txt", []byte("hello"), "100644")
		stageFileAt(t, dir, "run.sh", content, "100644")
		if err := os.Chmod(filepath.Join(dir, "run.sh"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(filepath.Join(dir, "other.txt"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := reconcileExecBit([]string{"run.sh"}); err != nil {
			t.Fatalf("reconcileExecBit: %v", err)
		}
		if got := indexModeOf(t, dir, "run.sh"); got != "100755" {
			t.Errorf("run.sh index mode = %q, want 100755", got)
		}
		if got := indexModeOf(t, dir, "other.txt"); got != "100644" {
			t.Errorf("other.txt index mode = %q, want 100644 (must be untouched)", got)
		}
	})

	t.Run("exec bit fixed when invoked from a subdirectory", func(t *testing.T) {
		dir := initGitRepo(t)
		orig, _ := os.Getwd()
		defer os.Chdir(orig)
		// Stage the file as repo-root-relative path "subdir/tool.sh" at 100644.
		stageFileAt(t, dir, "subdir/tool.sh", content, "100644")
		if err := os.Chmod(filepath.Join(dir, "subdir", "tool.sh"), 0755); err != nil {
			t.Fatal(err)
		}
		// Change into the subdirectory — this is the scenario that was broken.
		subdir := filepath.Join(dir, "subdir")
		if err := os.Chdir(subdir); err != nil {
			t.Fatal(err)
		}
		// Pass the cwd-relative name "tool.sh" exactly as exo add would.
		if err := reconcileExecBit([]string{"tool.sh"}); err != nil {
			t.Fatalf("reconcileExecBit: %v", err)
		}
		// Verify via the repo root that the index was updated.
		if got := indexModeOf(t, dir, "subdir/tool.sh"); got != "100755" {
			t.Errorf("index mode = %q, want 100755 (subdirectory case broken if 100644)", got)
		}
	})
}
