package clone

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractRepoName(t *testing.T) {
	tests := []struct {
		name     string
		repoURL  string
		wantName string
	}{
		{
			name:     "HTTPS URL with .git",
			repoURL:  "https://github.com/user/repo.git",
			wantName: "repo",
		},
		{
			name:     "HTTPS URL without .git",
			repoURL:  "https://github.com/user/repo",
			wantName: "repo",
		},
		{
			name:     "SSH URL with .git",
			repoURL:  "git@github.com:user/repo.git",
			wantName: "repo",
		},
		{
			name:     "SSH URL with port",
			repoURL:  "ssh://git@exogit.example.com:30022/org/project.git",
			wantName: "project",
		},
		{
			name:     "URL with trailing slash",
			repoURL:  "https://github.com/user/repo/",
			wantName: "repo",
		},
		{
			name:     "nested organization",
			repoURL:  "https://gitlab.com/group/subgroup/repo.git",
			wantName: "repo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractRepoName(tt.repoURL)
			if got != tt.wantName {
				t.Errorf("extractRepoName(%q) = %q, want %q", tt.repoURL, got, tt.wantName)
			}
		})
	}
}

func TestCheckExohubExists(t *testing.T) {
	tmpDir := t.TempDir()

	tests := []struct {
		name      string
		setupFunc func(string) error
		want      bool
	}{
		{
			name: ".exohub directory exists",
			setupFunc: func(dir string) error {
				return createDir(dir, ".exohub")
			},
			want: true,
		},
		{
			name: ".exohub is a file not directory",
			setupFunc: func(dir string) error {
				return createFile(dir, ".exohub", "content")
			},
			want: false,
		},
		{
			name: ".exohub does not exist",
			setupFunc: func(dir string) error {
				return nil
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testDir := createTestDir(t, tmpDir, tt.name)
			if tt.setupFunc != nil {
				if err := tt.setupFunc(testDir); err != nil {
					t.Fatalf("Setup failed: %v", err)
				}
			}

			got := checkExohubExists(testDir)
			if got != tt.want {
				t.Errorf("checkExohubExists() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestContainsWhitespace(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{
			name:  "no whitespace",
			input: "minimal",
			want:  false,
		},
		{
			name:  "space in middle",
			input: "my preset",
			want:  true,
		},
		{
			name:  "tab character",
			input: "my\tpreset",
			want:  true,
		},
		{
			name:  "newline character",
			input: "my\npreset",
			want:  true,
		},
		{
			name:  "carriage return",
			input: "my\rpreset",
			want:  true,
		},
		{
			name:  "hyphen is not whitespace",
			input: "my-preset",
			want:  false,
		},
		{
			name:  "underscore is not whitespace",
			input: "my_preset",
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := containsWhitespace(tt.input)
			if got != tt.want {
				t.Errorf("containsWhitespace(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// Helper functions for tests

func createDir(baseDir, name string) error {
	return createDirWithMode(baseDir, name, 0755)
}

func createDirWithMode(baseDir, name string, mode int) error {
	path := filepath.Join(baseDir, name)
	return os.MkdirAll(path, os.FileMode(mode))
}

func createFile(baseDir, name, content string) error {
	path := filepath.Join(baseDir, name)
	return os.WriteFile(path, []byte(content), 0644)
}

func createTestDir(t *testing.T, tmpDir, name string) string {
	t.Helper()
	testDir := filepath.Join(tmpDir, sanitizeName(name))
	if err := os.MkdirAll(testDir, 0755); err != nil {
		t.Fatalf("Failed to create test dir: %v", err)
	}
	return testDir
}

func sanitizeName(name string) string {
	// Replace spaces and special chars with underscores for directory names
	replacer := strings.NewReplacer(" ", "_", "/", "_", ":", "_", ".", "_")
	return replacer.Replace(name)
}
