package init

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFilterUUIDFromLogWithReport(t *testing.T) {
	tests := []struct {
		name           string
		content        string
		uuid           string
		expectModified bool
		expectContent  string
		expectError    bool
	}{
		{
			name: "removes lines with UUID",
			content: `1234567890 key1 here
abcd-1234-5678-uuid some data
9876543210 key2 there`,
			uuid:           "abcd-1234-5678-uuid",
			expectModified: true,
			expectContent: `1234567890 key1 here
9876543210 key2 there
`,
		},
		{
			name: "no modification when UUID not present",
			content: `1234567890 key1 here
9876543210 key2 there`,
			uuid:           "abcd-1234-5678-uuid",
			expectModified: false,
			expectContent: `1234567890 key1 here
9876543210 key2 there`,
		},
		{
			name: "removes multiple lines with UUID",
			content: `line1 abcd-1234-5678-uuid
line2 normal
line3 abcd-1234-5678-uuid again
line4 normal`,
			uuid:           "abcd-1234-5678-uuid",
			expectModified: true,
			expectContent: `line2 normal
line4 normal
`,
		},
		{
			name:           "empty file remains empty",
			content:        "",
			uuid:           "abcd-1234-5678-uuid",
			expectModified: false,
			expectContent:  "",
		},
		{
			name: "removes all lines leaves empty file",
			content: `uuid1 abcd-1234-5678-uuid data
uuid2 abcd-1234-5678-uuid more`,
			uuid:           "abcd-1234-5678-uuid",
			expectModified: true,
			expectContent:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temp file
			tmpDir := t.TempDir()
			testFile := filepath.Join(tmpDir, "test.log")

			// Write initial content
			if err := os.WriteFile(testFile, []byte(tt.content), 0644); err != nil {
				t.Fatalf("failed to write test file: %v", err)
			}

			// Run the function
			modified, err := filterUUIDFromLogWithReport(testFile, tt.uuid)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if modified != tt.expectModified {
				t.Errorf("expected modified=%v, got %v", tt.expectModified, modified)
			}

			// Check file content
			result, err := os.ReadFile(testFile)
			if err != nil {
				t.Fatalf("failed to read result file: %v", err)
			}

			if string(result) != tt.expectContent {
				t.Errorf("content mismatch:\nexpected: %q\ngot: %q", tt.expectContent, string(result))
			}
		})
	}
}

func TestFilterUUIDFromLogNonExistentFile(t *testing.T) {
	tmpDir := t.TempDir()
	nonExistent := filepath.Join(tmpDir, "does-not-exist.log")

	// Should return false, nil for non-existent files
	modified, err := filterUUIDFromLogWithReport(nonExistent, "some-uuid")
	if err != nil {
		t.Errorf("expected no error for non-existent file, got: %v", err)
	}
	if modified {
		t.Errorf("expected modified=false for non-existent file")
	}
}

func TestCountKeysOnUUID(t *testing.T) {
	// This test requires a git repository with git-annex branch
	// Skip if git is not available
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	tests := []struct {
		name          string
		setupRepo     func(t *testing.T, repoDir string) string // returns UUID to search for
		expectedCount int
		expectError   bool
	}{
		{
			name: "counts location logs with UUID",
			setupRepo: func(t *testing.T, repoDir string) string {
				// Initialize git repo
				runCmd(t, repoDir, "git", "init")
				runCmd(t, repoDir, "git", "config", "user.email", "test@example.com")
				runCmd(t, repoDir, "git", "config", "user.name", "Test User")

				// Create orphan git-annex branch
				runCmd(t, repoDir, "git", "checkout", "--orphan", "git-annex")

				// Create some test location logs with a UUID
				testUUID := "abcd1234-5678-90ef-ghij-klmnopqrstuv"

				// Create directory structure for location logs
				logDir1 := filepath.Join(repoDir, "abc", "def")
				if err := os.MkdirAll(logDir1, 0755); err != nil {
					t.Fatal(err)
				}

				// Create location logs with the UUID
				log1 := filepath.Join(logDir1, "KEY1.log")
				if err := os.WriteFile(log1, []byte("1234567890 "+testUUID+" 1\n"), 0644); err != nil {
					t.Fatal(err)
				}

				log2 := filepath.Join(logDir1, "KEY2.log")
				if err := os.WriteFile(log2, []byte("1234567890 "+testUUID+" 1\n"), 0644); err != nil {
					t.Fatal(err)
				}

				// Create a location log WITHOUT the UUID (should not be counted)
				log3 := filepath.Join(logDir1, "KEY3.log")
				if err := os.WriteFile(log3, []byte("1234567890 other-uuid 1\n"), 0644); err != nil {
					t.Fatal(err)
				}

				// Create main log files (should be excluded from count)
				if err := os.WriteFile(filepath.Join(repoDir, "remote.log"), []byte("remote data "+testUUID+"\n"), 0644); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(repoDir, "uuid.log"), []byte("uuid data\n"), 0644); err != nil {
					t.Fatal(err)
				}

				// Commit the git-annex branch
				runCmd(t, repoDir, "git", "add", ".")
				runCmd(t, repoDir, "git", "commit", "-m", "test git-annex data")

				return testUUID
			},
			expectedCount: 2, // KEY1.log and KEY2.log
			expectError:   false,
		},
		{
			name: "returns zero for UUID not in any location logs",
			setupRepo: func(t *testing.T, repoDir string) string {
				// Initialize git repo
				runCmd(t, repoDir, "git", "init")
				runCmd(t, repoDir, "git", "config", "user.email", "test@example.com")
				runCmd(t, repoDir, "git", "config", "user.name", "Test User")

				// Create orphan git-annex branch
				runCmd(t, repoDir, "git", "checkout", "--orphan", "git-annex")

				// Create a location log with different UUID
				logDir := filepath.Join(repoDir, "abc", "def")
				if err := os.MkdirAll(logDir, 0755); err != nil {
					t.Fatal(err)
				}

				log1 := filepath.Join(logDir, "KEY1.log")
				if err := os.WriteFile(log1, []byte("1234567890 other-uuid 1\n"), 0644); err != nil {
					t.Fatal(err)
				}

				// Commit
				runCmd(t, repoDir, "git", "add", ".")
				runCmd(t, repoDir, "git", "commit", "-m", "test data")

				// Return a UUID that doesn't exist in any logs
				return "ffffffff-ffff-ffff-ffff-ffffffffffff"
			},
			expectedCount: 0,
			expectError:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temp directory for test repo
			tmpDir := t.TempDir()

			// Setup the test repository
			uuid := tt.setupRepo(t, tmpDir)

			// Change to temp directory
			oldDir, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			defer os.Chdir(oldDir)

			if err := os.Chdir(tmpDir); err != nil {
				t.Fatal(err)
			}

			// Run the function
			count, err := countKeysOnUUID(uuid)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if count != tt.expectedCount {
				t.Errorf("expected count=%d, got %d", tt.expectedCount, count)
			}
		})
	}
}

func TestCountKeysOnUUIDNoGitAnnexBranch(t *testing.T) {
	// Skip if git is not available
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	tmpDir := t.TempDir()

	// Initialize git repo without git-annex branch
	runCmd(t, tmpDir, "git", "init")
	runCmd(t, tmpDir, "git", "config", "user.email", "test@example.com")
	runCmd(t, tmpDir, "git", "config", "user.name", "Test User")

	// Create a commit on main branch
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}
	runCmd(t, tmpDir, "git", "add", ".")
	runCmd(t, tmpDir, "git", "commit", "-m", "initial")

	// Change to temp directory
	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldDir)

	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}

	// Should return 0 and an error (or handle gracefully)
	count, err := countKeysOnUUID("some-uuid")

	// Either error or count should be 0
	if err == nil && count != 0 {
		t.Errorf("expected error or count=0 when git-annex branch doesn't exist, got count=%d", count)
	}
}

// Helper function to run git commands in tests
func runCmd(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("Command failed: %s %v", name, args)
		t.Logf("Output: %s", output)
		t.Fatalf("command failed: %v", err)
	}
}

func TestPreviewLocationLogFiltering(t *testing.T) {
	// Skip if git is not available
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	tests := []struct {
		name          string
		setupRepo     func(t *testing.T, repoDir string) string // returns UUID to search for
		expectedCount int
		expectError   bool
	}{
		{
			name: "counts location logs that would be filtered",
			setupRepo: func(t *testing.T, repoDir string) string {
				// Initialize git repo
				runCmd(t, repoDir, "git", "init")
				runCmd(t, repoDir, "git", "config", "user.email", "test@example.com")
				runCmd(t, repoDir, "git", "config", "user.name", "Test User")

				// Create orphan git-annex branch
				runCmd(t, repoDir, "git", "checkout", "--orphan", "git-annex")

				testUUID := "test-uuid-1234-5678-90ab-cdef"

				// Create location logs with the UUID
				logDir := filepath.Join(repoDir, "abc", "def")
				if err := os.MkdirAll(logDir, 0755); err != nil {
					t.Fatal(err)
				}

				// 3 location logs with UUID
				for i := 1; i <= 3; i++ {
					log := filepath.Join(logDir, fmt.Sprintf("KEY%d.log", i))
					if err := os.WriteFile(log, []byte("1234567890 "+testUUID+" 1\n"), 0644); err != nil {
						t.Fatal(err)
					}
				}

				// 1 location log without UUID (should not be counted)
				log4 := filepath.Join(logDir, "KEY4.log")
				if err := os.WriteFile(log4, []byte("1234567890 other-uuid 1\n"), 0644); err != nil {
					t.Fatal(err)
				}

				// Main logs with UUID (should be excluded from count)
				if err := os.WriteFile(filepath.Join(repoDir, "remote.log"), []byte("remote "+testUUID+"\n"), 0644); err != nil {
					t.Fatal(err)
				}

				// Commit
				runCmd(t, repoDir, "git", "add", ".")
				runCmd(t, repoDir, "git", "commit", "-m", "test data")

				return testUUID
			},
			expectedCount: 3,
			expectError:   false,
		},
		{
			name: "returns zero when no location logs contain UUID",
			setupRepo: func(t *testing.T, repoDir string) string {
				// Initialize git repo
				runCmd(t, repoDir, "git", "init")
				runCmd(t, repoDir, "git", "config", "user.email", "test@example.com")
				runCmd(t, repoDir, "git", "config", "user.name", "Test User")

				// Create orphan git-annex branch
				runCmd(t, repoDir, "git", "checkout", "--orphan", "git-annex")

				// Create location log with different UUID
				logDir := filepath.Join(repoDir, "abc", "def")
				if err := os.MkdirAll(logDir, 0755); err != nil {
					t.Fatal(err)
				}

				log1 := filepath.Join(logDir, "KEY1.log")
				if err := os.WriteFile(log1, []byte("1234567890 other-uuid 1\n"), 0644); err != nil {
					t.Fatal(err)
				}

				// Commit
				runCmd(t, repoDir, "git", "add", ".")
				runCmd(t, repoDir, "git", "commit", "-m", "test data")

				return "uuid-not-in-logs"
			},
			expectedCount: 0,
			expectError:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			uuid := tt.setupRepo(t, tmpDir)

			// Change to temp directory
			oldDir, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			defer os.Chdir(oldDir)

			if err := os.Chdir(tmpDir); err != nil {
				t.Fatal(err)
			}

			// Run preview
			count, err := previewLocationLogFiltering(uuid)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if count != tt.expectedCount {
				t.Errorf("expected count=%d, got %d", tt.expectedCount, count)
			}
		})
	}
}

// Benchmark the filtering function
func BenchmarkFilterUUIDFromLogWithReport(b *testing.B) {
	// Create a temp file with 1000 lines
	tmpDir := b.TempDir()
	testFile := filepath.Join(tmpDir, "test.log")

	var content strings.Builder
	targetUUID := "abcd1234-5678-90ef-ghij-klmnopqrstuv"
	for i := 0; i < 1000; i++ {
		if i%100 == 0 {
			// Every 100th line contains the target UUID
			content.WriteString("1234567890 " + targetUUID + " 1\n")
		} else {
			content.WriteString("1234567890 other-uuid 1\n")
		}
	}

	if err := os.WriteFile(testFile, []byte(content.String()), 0644); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Rewrite the file for each iteration
		if err := os.WriteFile(testFile, []byte(content.String()), 0644); err != nil {
			b.Fatal(err)
		}

		_, err := filterUUIDFromLogWithReport(testFile, targetUUID)
		if err != nil {
			b.Fatal(err)
		}
	}
}
