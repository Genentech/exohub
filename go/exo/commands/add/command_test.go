package add

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
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
			locals, urls, flags, _, _, _, _, _, err := splitArgs(tt.args)
			if err != nil {
				t.Fatal(err)
			}
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

// TestClassifyFilesRespectsGitignore reproduces issue #108: a directory
// expansion must skip .gitignore'd paths uniformly across every MIME route
// (git for text/JSON, annex for binary, largeText for oversized text), not
// just some of them.
func TestClassifyFilesRespectsGitignore(t *testing.T) {
	dir := initGitRepo(t)
	orig, _ := os.Getwd()
	defer os.Chdir(orig)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	mustWrite := func(rel string, content []byte) {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, content, 0644); err != nil {
			t.Fatal(err)
		}
	}

	mustWrite(".gitignore", []byte("ignored/\n"))
	mustWrite("ignored/out.json", []byte(`{"x":1}`))        // git route (JSON)
	mustWrite("ignored/blob.bin", []byte{0x00, 0x01, 0x02}) // annex route (binary)
	mustWrite("ignored/small.txt", []byte("hello\n"))       // git route (small text)
	big := bytes.Repeat([]byte("a"), largeTextFileThreshold+1)
	mustWrite("ignored/big.txt", big) // largeText route
	mustWrite("README.md", []byte("# keep\n"))

	git, annex, largeText := classifyFiles([]string{"."})

	var all []string
	all = append(all, git...)
	all = append(all, annex...)
	all = append(all, largeText...)
	for _, f := range all {
		if strings.HasPrefix(filepath.ToSlash(f), "ignored/") {
			t.Errorf("ignored path %q was classified; should have been skipped (git=%v annex=%v largeText=%v)", f, git, annex, largeText)
		}
	}

	found := false
	for _, f := range git {
		if f == "README.md" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected README.md to still be classified, got git=%v", git)
	}
}

// TestClassifyFilesExcludesDotGit ensures the git-aware enumerator never
// walks into .git, so git/git-annex internals are never MIME-sniffed or
// passed as explicit add arguments.
func TestClassifyFilesExcludesDotGit(t *testing.T) {
	dir := initGitRepo(t)
	orig, _ := os.Getwd()
	defer os.Chdir(orig)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}

	git, annex, largeText := classifyFiles([]string{"."})
	var all []string
	all = append(all, git...)
	all = append(all, annex...)
	all = append(all, largeText...)
	for _, f := range all {
		if strings.HasPrefix(filepath.ToSlash(f), ".git/") || f == ".git" {
			t.Fatalf(".git contents were classified: %q (all=%v)", f, all)
		}
	}
}

// TestCollectFilesRespectsGitignore covers the --force migration path
// (migrateToAnnex/migrateToGit), which shares the same enumerator.
func TestCollectFilesRespectsGitignore(t *testing.T) {
	dir := initGitRepo(t)
	orig, _ := os.Getwd()
	defer os.Chdir(orig)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("ignored/\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "ignored"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ignored", "x.bin"), []byte{0x00}, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}

	files := collectFiles([]string{"."})
	for _, f := range files {
		if strings.HasPrefix(filepath.ToSlash(f), "ignored/") {
			t.Errorf("collectFiles returned ignored path %q (all=%v)", f, files)
		}
	}
	found := false
	for _, f := range files {
		if f == "keep.txt" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected keep.txt in collected files, got %v", files)
	}
}

// TestListGitAwareFilesFallsBackOutsideRepo ensures a directory outside any
// git working tree still gets walked (no silent empty result) when the
// ignore-aware git invocation can't run.
func TestListGitAwareFilesFallsBackOutsideRepo(t *testing.T) {
	dir := t.TempDir() // not a git repo, and (via t.TempDir) outside this repo's worktree
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "b.txt"), []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}

	got := listGitAwareFiles(dir)
	if len(got) != 2 {
		t.Fatalf("expected 2 files via fallback walk, got %d: %v", len(got), got)
	}
}

// TestExecCommandBatched verifies large file lists are chunked across
// multiple invocations instead of exceeding argv limits in one call.
func TestExecCommandBatched(t *testing.T) {
	var calls [][]string
	origPath, err := exec.LookPath("true")
	if err != nil {
		t.Skip("'true' not in PATH")
	}
	_ = origPath

	// We can't easily intercept exec.Command inside execCommand without a
	// seam, so instead verify the chunk boundaries directly via the batching
	// arithmetic by calling execCommandBatched against a no-op command and
	// counting invocations through a side-effect file.
	tmp := t.TempDir()
	counterScript := filepath.Join(tmp, "count.sh")
	script := "#!/bin/sh\necho \"$#\" >> \"" + filepath.Join(tmp, "counts.txt") + "\"\n"
	if err := os.WriteFile(counterScript, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	files := make([]string, maxFilesPerBatch+1)
	for i := range files {
		files[i] = fmt.Sprintf("f%d", i)
	}
	if err := execCommandBatched(counterScript, nil, files); err != nil {
		t.Fatalf("execCommandBatched: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmp, "counts.txt"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Fields(strings.TrimSpace(string(data)))
	if len(lines) != 2 {
		t.Fatalf("expected 2 batched invocations, got %d (%v)", len(lines), lines)
	}
	if lines[0] != fmt.Sprintf("%d", maxFilesPerBatch) {
		t.Errorf("first batch size = %s, want %d", lines[0], maxFilesPerBatch)
	}
	if lines[1] != "1" {
		t.Errorf("second batch size = %s, want 1", lines[1])
	}
	_ = calls
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

// initGitAnnexRepo creates a temporary git+git-annex repo, chdirs the test
// process into it, and returns a restore func to chdir back. It skips the
// test when git-annex is not available in PATH.
func initGitAnnexRepo(t *testing.T) (dir string, restore func()) {
	t.Helper()
	if _, err := exec.LookPath("git-annex"); err != nil {
		t.Skip("git-annex not in PATH")
	}
	dir = t.TempDir()
	// git-annex stores object files read-only; make everything writable
	// before t.TempDir() cleanup tries to remove it.
	t.Cleanup(func() {
		filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			return os.Chmod(p, info.Mode()|0o700)
		})
	})
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "HOME="+dir, "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("command %v: %v\n%s", args, err, out)
		}
	}
	run("git", "init")
	run("git", "config", "user.email", "test@example.com")
	run("git", "config", "user.name", "Test")
	run("git", "annex", "init", "testrepo")

	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	return dir, func() { os.Chdir(orig) }
}

// TestAddDirSkipsIgnoredFilesConsistently is an end-to-end regression test
// for issue #108: `exo add <dir>` must skip .gitignore'd paths uniformly
// across both the git route (text/JSON) and the annex route (binary), exit
// 0, and never partially stage when an ignored path would otherwise have
// aborted the git-route invocation.
func TestAddDirSkipsIgnoredFilesConsistently(t *testing.T) {
	_, restore := initGitAnnexRepo(t)
	defer restore()

	write := func(rel string, content []byte) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(rel), 0755); err != nil && filepath.Dir(rel) != "." {
			t.Fatal(err)
		}
		if err := os.WriteFile(rel, content, 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Mirrors the issue's downstream motivation: exo publish writes
	// .exohub/bundles/*/bundle.json (JSON -> git route) after each commit;
	// .gitignore'ing it must not abort `exo add .`, and the unrelated
	// data.bin (binary -> annex route) must still get staged in the same run.
	write(".gitignore", []byte(".exohub/bundles\n"))
	write(".exohub/bundles/bundle.json", []byte(`{"a":1}`))
	write("data.bin", []byte{0x00, 0x01, 0x02, 0x03})
	write("README.md", []byte("keep\n"))

	cmd := NewCommand()
	cmd.SetArgs([]string{"."})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("exo add . returned error (should skip ignored paths and exit 0): %v", err)
	}

	out, err := exec.Command("git", "status", "--porcelain").Output()
	if err != nil {
		t.Fatal(err)
	}
	status := string(out)
	if strings.Contains(status, "bundle.json") {
		t.Errorf("ignored bundle.json was staged:\n%s", status)
	}
	if !strings.Contains(status, "data.bin") {
		t.Errorf("data.bin (non-ignored, annex route) was NOT staged (non-atomic failure regression):\n%s", status)
	}
	if !strings.Contains(status, "README.md") {
		t.Errorf("README.md was not staged:\n%s", status)
	}
}

// TestAddExplicitIgnoredPathStillErrors ensures a path named directly on the
// command line (not discovered via directory expansion) keeps today's error
// behavior when it is .gitignore'd, matching plain `git add <ignored-file>`.
// Only paths exo discovers itself while expanding a directory should be
// silently skipped.
func TestAddExplicitIgnoredPathStillErrors(t *testing.T) {
	_, restore := initGitAnnexRepo(t)
	defer restore()

	if err := os.MkdirAll("ignored", 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(".gitignore", []byte("ignored/\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("ignored/small.txt", []byte("hello\n"), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := NewCommand()
	cmd.SetArgs([]string{"ignored/small.txt"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected exo add <explicit ignored path> to fail, matching git add's behavior")
	}
}

// TestCollectFilesExcludesDeletedTrackedFiles covers a maître review finding:
// `git ls-files -c` reports index entries regardless of whether they still
// exist in the working tree, so a tracked file removed with a plain `rm`
// (deletion not yet staged) must not be returned by the enumerator, or
// --force migrations will try to operate on a path that doesn't exist.
func TestCollectFilesExcludesDeletedTrackedFiles(t *testing.T) {
	dir := initGitRepo(t)
	orig, _ := os.Getwd()
	defer os.Chdir(orig)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	stageFileAt(t, dir, "data/keep.txt", []byte("keep\n"), "100644")
	stageFileAt(t, dir, "data/gone.txt", []byte("gone\n"), "100644")
	commit := exec.Command("git", "commit", "-q", "-m", "init")
	if out, err := commit.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}

	// Remove gone.txt from the working tree without staging the deletion.
	if err := os.Remove(filepath.Join(dir, "data", "gone.txt")); err != nil {
		t.Fatal(err)
	}

	files := collectFiles([]string{"data"})
	for _, f := range files {
		if f == "data/gone.txt" {
			t.Errorf("collectFiles returned a tracked-but-deleted path %q: %v", f, files)
		}
	}
	found := false
	for _, f := range files {
		if f == "data/keep.txt" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected data/keep.txt in collected files, got %v", files)
	}
}

// TestAddForceAnnexMigrationSkipsDeletedTrackedFiles is an end-to-end
// regression test for the maître's finding: `exo add --annex --force <dir>`
// must not fail, and must not stage the deletion of, a tracked file that was
// removed from the working tree (but not yet `git rm`'d) alongside files
// that are actually being migrated.
func TestAddForceAnnexMigrationSkipsDeletedTrackedFiles(t *testing.T) {
	_, restore := initGitAnnexRepo(t)
	defer restore()

	if err := os.MkdirAll("data", 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("data/keep.txt", []byte("keep\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("data/gone.txt", []byte("gone\n"), 0644); err != nil {
		t.Fatal(err)
	}
	addCmd := exec.Command("git", "add", "data")
	if out, err := addCmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	commitCmd := exec.Command("git", "commit", "-q", "-m", "init")
	if out, err := commitCmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}

	// Remove gone.txt from the working tree without staging the deletion.
	if err := os.Remove("data/gone.txt"); err != nil {
		t.Fatal(err)
	}

	cmd := NewCommand()
	cmd.SetArgs([]string{"--annex", "--force", "data"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("exo add --annex --force data returned error: %v", err)
	}

	out, err := exec.Command("git", "status", "--porcelain").Output()
	if err != nil {
		t.Fatal(err)
	}
	status := string(out)
	if !strings.Contains(status, " D data/gone.txt") {
		t.Errorf("expected gone.txt's deletion to remain unstaged (\" D data/gone.txt\"), got status:\n%s", status)
	}
	if strings.Contains(status, "D  data/gone.txt") {
		t.Errorf("gone.txt's deletion must not be staged by the migration, got status:\n%s", status)
	}
}

// TestAddForceGitMigrationSkipsDeletedAnnexedFiles mirrors the above for the
// opposite migration direction (annex -> git): `exo add --git --force <dir>`
// must not fail, and must not stage the deletion of, an annexed file whose
// symlink was removed from the working tree.
func TestAddForceGitMigrationSkipsDeletedAnnexedFiles(t *testing.T) {
	_, restore := initGitAnnexRepo(t)
	defer restore()

	if err := os.MkdirAll("data", 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("data/keep.bin", []byte{0x00, 0x01}, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("data/gone.bin", []byte{0x02, 0x03}, 0644); err != nil {
		t.Fatal(err)
	}
	annexAddCmd := exec.Command("git", "annex", "add", "data")
	if out, err := annexAddCmd.CombinedOutput(); err != nil {
		t.Fatalf("git annex add: %v\n%s", err, out)
	}
	commitCmd := exec.Command("git", "commit", "-q", "-m", "init")
	if out, err := commitCmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}

	// Remove gone.bin's symlink from the working tree without staging the deletion.
	if err := os.Remove("data/gone.bin"); err != nil {
		t.Fatal(err)
	}

	cmd := NewCommand()
	cmd.SetArgs([]string{"--git", "--force", "data"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("exo add --git --force data returned error: %v", err)
	}

	out, err := exec.Command("git", "status", "--porcelain").Output()
	if err != nil {
		t.Fatal(err)
	}
	status := string(out)
	if !strings.Contains(status, " D data/gone.bin") {
		t.Errorf("expected gone.bin's deletion to remain unstaged (\" D data/gone.bin\"), got status:\n%s", status)
	}
	if strings.Contains(status, "D  data/gone.bin") {
		t.Errorf("gone.bin's deletion must not be staged by the migration, got status:\n%s", status)
	}
}

func runExplainCLI(t *testing.T, root string, args ...string) string {
	t.Helper()
	out, _ := runExplainCLIFull(t, root, args...)
	return out
}

// runExplainCLIFull is runExplainCLI but also returns stderr, for
// assertions on the shadow-rule warnings design D9 requires --explain to
// print there (so stdout's --json output stays a clean array).
func runExplainCLIFull(t *testing.T, root string, args ...string) (stdout, stderr string) {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(cwd); err != nil {
			t.Fatal(err)
		}
	}()
	cmd := NewCommand()
	cmd.SetArgs(args)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	return outBuf.String(), errBuf.String()
}

func TestAddExplainTable(t *testing.T) {
	root := routingRepo(t)
	routingWrite(t, root, ".exohub/config", "annex:\n  routing:\n    - path: '*.csv'\n      larger_than: 1KB\n      to: annex\n")
	routingWrite(t, root, "data/a.csv", strings.Repeat("x", 2000))
	routingWrite(t, root, "notes.txt", "hello\n")

	out := runExplainCLI(t, root, "--explain", "data/a.csv", "notes.txt")
	if !strings.Contains(out, "PATH") || !strings.Contains(out, "SIZE") || !strings.Contains(out, "DEST") || !strings.Contains(out, "WHY") {
		t.Fatalf("missing table header: %q", out)
	}
	if !strings.Contains(out, "data/a.csv") || !strings.Contains(out, "rule 1:") || !strings.Contains(out, "annex") {
		t.Fatalf("missing routed entry: %q", out)
	}
	if !strings.Contains(out, "notes.txt") || !strings.Contains(out, "default: text") {
		t.Fatalf("missing fallback entry: %q", out)
	}
}

func TestAddExplainJSON(t *testing.T) {
	root := routingRepo(t)
	routingWrite(t, root, ".exohub/config", "annex:\n  routing:\n    - path: '*.csv'\n      larger_than: 1KB\n      to: annex\n")
	routingWrite(t, root, "data/a.csv", strings.Repeat("x", 2000))

	out := runExplainCLI(t, root, "--explain", "--json", "data/a.csv")
	var entries []explainEntry
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %+v", entries)
	}
	entry := entries[0]
	if entry.Path != "data/a.csv" || entry.Destination != "annex" || entry.Size != 2000 || !strings.HasPrefix(entry.Reason, "rule 1:") {
		t.Fatalf("entry = %+v", entry)
	}
}

func TestAddExplainTrackedShowsRerouteHint(t *testing.T) {
	root := routingRepo(t)
	routingWrite(t, root, "data/old.csv", "original\n")
	routingGit(t, root, "add", "data/old.csv")
	routingWrite(t, root, ".exohub/config", "annex:\n  routing:\n    - path: 'data/*.csv'\n      to: annex\n")

	out := runExplainCLI(t, root, "--explain", "data/old.csv")
	if !strings.Contains(out, "tracked (git)") || !strings.Contains(out, "--reroute would move to annex") {
		t.Fatalf("missing reroute hint: %q", out)
	}
}

// TestAddExplainWarnsAboutShadowedRulesOnStderr is a should-fix item from
// the maître's review of MR !247: design D9 says a shadowed rule "is
// reported as a warning by exo doctor / --explain", but --explain only
// wired this into exo doctor. The warning must land on stderr (both in
// table and --json mode) so stdout's --json array stays clean.
func TestAddExplainWarnsAboutShadowedRulesOnStderr(t *testing.T) {
	root := routingRepo(t)
	routingWrite(t, root, ".exohub/config", "annex:\n  routing:\n    - path: '**/*.csv'\n      to: git\n    - path: 'data/*.csv'\n      to: annex\n")
	routingWrite(t, root, "data/a.csv", "hello\n")

	_, stderr := runExplainCLIFull(t, root, "--explain", "data/a.csv")
	if !strings.Contains(stderr, "rule 2") || !strings.Contains(stderr, "can never match") {
		t.Fatalf("expected a shadow warning on stderr, got %q", stderr)
	}

	stdout, stderrJSON := runExplainCLIFull(t, root, "--explain", "--json", "data/a.csv")
	if !strings.Contains(stderrJSON, "rule 2") {
		t.Fatalf("expected a shadow warning on stderr in --json mode, got %q", stderrJSON)
	}
	var entries []explainEntry
	if err := json.Unmarshal([]byte(stdout), &entries); err != nil {
		t.Fatalf("--json stdout must stay a clean array even with a shadow warning: %v\n%s", err, stdout)
	}
}

// TestAddJSONWithoutExplainErrors is a minor item from the maître's review
// of MR !247: --json without --explain must not be silently swallowed.
func TestAddJSONWithoutExplainErrors(t *testing.T) {
	root := routingRepo(t)
	routingWrite(t, root, "a.txt", "hello\n")

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}

	cmd := NewCommand()
	cmd.SetArgs([]string{"--json", "a.txt"})
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	err = cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--json requires --explain") {
		t.Fatalf("expected a '--json requires --explain' error, got %v", err)
	}
}

func TestAddExplainZeroSideEffects(t *testing.T) {
	root := routingRepo(t)
	routingWrite(t, root, ".exohub/config", "annex:\n  routing:\n    - path: '*.csv'\n      to: annex\n")
	routingWrite(t, root, "data/a.csv", "hello\n")

	before := routingGit(t, root, "status", "--porcelain")
	if err := routingCLI(t, root, "--explain", "--json", "."); err != nil {
		t.Fatal(err)
	}
	after := routingGit(t, root, "status", "--porcelain")
	if before != after {
		t.Fatalf("working tree/index changed:\nbefore=%q\nafter=%q", before, after)
	}
}

// TestAddPlainModeSkipsDroppedAnnexContent is an end-to-end regression test
// for the maître's blocking finding on MR !247: both plain `exo add` and
// `exo add --explain` must succeed in a repo that has a locked annex file
// whose content was dropped (a dangling symlink, the normal state after a
// clone without `get`), and plain `exo add .` must still stage an unrelated
// new file.
func TestAddPlainModeSkipsDroppedAnnexContent(t *testing.T) {
	dir, restore := initGitAnnexRepo(t)
	defer restore()

	if err := os.WriteFile(filepath.Join(dir, "big.bin"), []byte(strings.Repeat("x", 2000)), 0644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "annex", "add", "big.bin").CombinedOutput(); err != nil {
		t.Fatalf("git annex add: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "commit", "-q", "-m", "init").CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
	// Drop the content: big.bin is now a dangling symlink, same as after a
	// clone without `get`.
	if out, err := exec.Command("git", "annex", "drop", "--force", "big.bin").CombinedOutput(); err != nil {
		t.Fatalf("git annex drop --force: %v\n%s", err, out)
	}

	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("new\n"), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := NewCommand()
	cmd.SetArgs([]string{"."})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("exo add . with dropped annex content returned error: %v", err)
	}
	out, err := exec.Command("git", "status", "--porcelain").Output()
	if err != nil {
		t.Fatal(err)
	}
	status := string(out)
	if !strings.Contains(status, "A  new.txt") {
		t.Fatalf("expected new.txt to be staged, got status:\n%s", status)
	}
	if strings.Contains(status, "big.bin") {
		t.Fatalf("big.bin must be untouched by plain add, got status:\n%s", status)
	}

	explainOut := runExplainCLI(t, dir, "--explain", ".")
	if !strings.Contains(explainOut, "big.bin") || !strings.Contains(explainOut, "tracked (annex)") {
		t.Fatalf("missing dropped tracked entry: %q", explainOut)
	}
	if strings.Contains(explainOut, "--reroute") {
		t.Fatalf("expected no reroute hint for a dropped, locked annex file: %q", explainOut)
	}
}

func TestRoutingHelp(t *testing.T) {
	cmd := NewCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{
		".exohub/**", ".gitignore", ".gitattributes", "first match wins",
		"new (untracked)", "current backend", "5 MiB", "--explain", "--json",
		"--reroute", "--yes", "--large-text-threshold", "EXOHUB_LARGE_TEXT_THRESHOLD",
		"Overrides affect only the fallback", "--annex --force", "git annex addurl",
	} {
		if !strings.Contains(out.String(), text) {
			t.Errorf("help missing %q", text)
		}
	}
	if strings.Contains(out.String(), "JSON, XML") {
		t.Error("help still advertises obsolete JSON/XML exemptions")
	}
}
