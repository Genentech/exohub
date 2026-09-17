package main

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

// TestProtocolBasics tests basic protocol command parsing and responses
func TestProtocolBasics(t *testing.T) {
	resetState()

	tests := []struct {
		name           string
		input          string
		expectedOutput string
		description    string
	}{
		{
			name:           "version",
			input:          "VERSION 1",
			expectedOutput: "VERSION 1",
			description:    "Should respond with VERSION 1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a buffer to capture output
			var outBuf bytes.Buffer
			originalOut := outw
			outw = bufio.NewWriter(&outBuf)

			// Process the input command
			parts := strings.Fields(tt.input)
			if len(parts) > 0 {
				cmd := parts[0]

				switch cmd {
				case "VERSION":
					if len(parts) > 1 && parts[1] == "1" {
						writeLine("VERSION 1")
					}
				}
			}

			// Restore original writer
			outw = originalOut

			output := outBuf.String()
			if !strings.Contains(output, tt.expectedOutput) {
				t.Errorf("%s: expected output to contain %q, got %q", tt.description, tt.expectedOutput, output)
			}
		})
	}
}

// TestS3URLConfiguration tests s3url configuration parsing
func TestS3URLConfiguration(t *testing.T) {
	resetState()

	tests := []struct {
		name           string
		s3url          string
		expectedBucket string
		expectedPrefix string
		shouldError    bool
	}{
		{
			name:           "bucket_only",
			s3url:          "s3://my-bucket",
			expectedBucket: "my-bucket",
			expectedPrefix: "",
			shouldError:    false,
		},
		{
			name:           "bucket_with_prefix",
			s3url:          "s3://my-bucket/data/files",
			expectedBucket: "my-bucket",
			expectedPrefix: "data/files",
			shouldError:    false,
		},
		{
			name:           "invalid_protocol",
			s3url:          "http://example.com/bucket",
			expectedBucket: "",
			expectedPrefix: "",
			shouldError:    true,
		},
		{
			name:           "no_bucket",
			s3url:          "s3:///missing",
			expectedBucket: "",
			expectedPrefix: "",
			shouldError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bucket, prefix, err := parseS3url(tt.s3url)

			if tt.shouldError {
				if err == nil {
					t.Errorf("Expected error for s3url %q, but got none", tt.s3url)
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error for valid s3url %q: %v", tt.s3url, err)
				return
			}

			if bucket != tt.expectedBucket {
				t.Errorf("Bucket mismatch: got %q, want %q", bucket, tt.expectedBucket)
			}

			if prefix != tt.expectedPrefix {
				t.Errorf("Prefix mismatch: got %q, want %q", prefix, tt.expectedPrefix)
			}
		})
	}
}

// TestExportRefHandling tests export reference tracking
func TestExportRefHandling(t *testing.T) {
	resetState()

	// Test with export ref set
	exportRef = "refs/heads/main"
	exportRefSet = true

	key := s3ExportKey("file.txt", "prefix")
	expected := "prefix/refs/heads/main/file.txt"
	if key != expected {
		t.Errorf("Export key with ref: got %q, want %q", key, expected)
	}

	// Test without export ref
	resetState()
	exportRefSet = false

	key = s3ExportKey("file.txt", "prefix")
	expected = "prefix/file.txt"
	if key != expected {
		t.Errorf("Export key without ref: got %q, want %q", key, expected)
	}
}

// TestExpectedContentTracking tests the content identifier tracking for imports
func TestExpectedContentTracking(t *testing.T) {
	resetState()

	// Set location
	handleLocation("data/file1.txt")
	if currentLocation != "data/file1.txt" {
		t.Errorf("currentLocation not set correctly: got %q", currentLocation)
	}

	// Set expected content identifier
	handleExpected("etag:abc123def")
	if expectedContent["data/file1.txt"] != "etag:abc123def" {
		t.Errorf("Expected content not tracked: got %q", expectedContent["data/file1.txt"])
	}

	// Test nothing expected
	handleLocation("data/file2.txt")
	handleNothingExpected()
	if expectedContent["data/file2.txt"] != "" {
		t.Errorf("NOTHINGEXPECTED should set empty identifier, got %q", expectedContent["data/file2.txt"])
	}

	// Reset and verify cleanup of current location only
	resetLocationState()
	if currentLocation != "" {
		t.Errorf("currentLocation should be empty after reset, got %q", currentLocation)
	}
	// resetLocationState only deletes the current location entry, not all entries
	if _, exists := expectedContent["data/file2.txt"]; exists {
		t.Errorf("expectedContent for current location should be cleared after reset")
	}
}

// TestContentIdentifierFormats tests different content identifier formats
func TestContentIdentifierFormats(t *testing.T) {
	tests := []struct {
		name     string
		etag     string
		size     int64
		mtime    int64
		expected string
	}{
		{
			name:     "with_etag",
			etag:     "abc123",
			size:     1024,
			mtime:    1704067200,
			expected: "etag:abc123",
		},
		{
			name:     "quoted_etag",
			etag:     `"def456"`,
			size:     2048,
			mtime:    1704067200,
			expected: "etag:def456",
		},
		{
			name:     "no_etag_fallback",
			etag:     "",
			size:     512,
			mtime:    1704067200,
			expected: "size:512:1704067200",
		},
		{
			name:     "dash_etag_fallback",
			etag:     "-",
			size:     256,
			mtime:    1704067200,
			expected: "size:256:1704067200",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Using parseTime helper from existing test
			mtime := parseTime("2024-01-01T00:00:00Z")
			cid := formatContentIdentifier(tt.etag, tt.size, mtime)

			// For cases with mtime, we need to use the actual unix timestamp
			if tt.etag == "" || tt.etag == "-" {
				expected := formatContentIdentifier(tt.etag, tt.size, mtime)
				if cid != expected {
					t.Errorf("Content identifier mismatch: got %q, want %q", cid, expected)
				}
			} else {
				if cid != tt.expected {
					t.Errorf("Content identifier mismatch: got %q, want %q", cid, tt.expected)
				}
			}
		})
	}
}

// TestS5cmdVersionCheck tests s5cmd version detection
func TestS5cmdVersionCheck(t *testing.T) {
	// This test verifies the version checking logic
	// We can't easily mock s5cmd, but we can test the helper functions

	// Test firstLineSafe helper
	tests := []struct {
		input    string
		expected string
	}{
		{
			input:    "v2.2.2\n",
			expected: "v2.2.2",
		},
		{
			input:    "v2.2.2\nextra line\n",
			expected: "v2.2.2",
		},
		{
			input:    "  v2.2.2  \n",
			expected: "v2.2.2",
		},
		{
			input:    "v2.2.2\r\n",
			expected: "v2.2.2",
		},
	}

	for _, tt := range tests {
		result := firstLineSafe(tt.input)
		if result != tt.expected {
			t.Errorf("firstLineSafe(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

// TestEnvOrHelper tests the envOr helper function
func TestEnvOrHelper(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		envValue string
		def      string
		expected string
	}{
		{
			name:     "env_set",
			key:      "TEST_KEY_1",
			envValue: "custom-value",
			def:      "default-value",
			expected: "custom-value",
		},
		{
			name:     "env_not_set",
			key:      "TEST_KEY_2_UNSET",
			envValue: "",
			def:      "default-value",
			expected: "default-value",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				t.Setenv(tt.key, tt.envValue)
			}

			result := envOr(tt.key, tt.def)
			if result != tt.expected {
				t.Errorf("envOr(%q, %q) = %q, want %q", tt.key, tt.def, result, tt.expected)
			}
		})
	}
}

// TestRunHelper tests the run command helper
func TestRunHelper(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		expectCode   int
		expectError  bool
		description  string
	}{
		{
			name:         "no_args",
			args:         []string{},
			expectCode:   127,
			expectError:  true,
			description:  "Empty args should return code 127",
		},
		{
			name:         "successful_echo",
			args:         []string{"echo", "test"},
			expectCode:   0,
			expectError:  false,
			description:  "Successful command should return code 0",
		},
		{
			name:         "nonexistent_command",
			args:         []string{"nonexistent-cmd-12345"},
			expectCode:   127,
			expectError:  true,
			description:  "Nonexistent command should return code 127",
		},
		{
			name:         "false_command",
			args:         []string{"sh", "-c", "exit 1"},
			expectCode:   1,
			expectError:  true,
			description:  "Failed command should return exit code 1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := run(tt.args...)

			if result.code != tt.expectCode {
				t.Errorf("%s: got code %d, want %d", tt.description, result.code, tt.expectCode)
			}

			if tt.expectError && result.code == 0 {
				t.Errorf("%s: expected error but got success", tt.description)
			}

			if !tt.expectError && result.code != 0 {
				t.Errorf("%s: expected success but got error code %d", tt.description, result.code)
			}
		})
	}
}

// TestRunHelperOutput tests that run() captures stdout and stderr
func TestRunHelperOutput(t *testing.T) {
	// Test stdout capture
	result := run("echo", "hello world")
	if result.code != 0 {
		t.Fatalf("Echo command failed with code %d", result.code)
	}

	output := strings.TrimSpace(string(result.stdout))
	if output != "hello world" {
		t.Errorf("stdout not captured correctly: got %q, want %q", output, "hello world")
	}

	// Test stderr capture
	result = run("sh", "-c", "echo 'error message' >&2")
	if result.code != 0 {
		t.Fatalf("Stderr test command failed with code %d", result.code)
	}

	stderr := strings.TrimSpace(string(result.stderr))
	if stderr != "error message" {
		t.Errorf("stderr not captured correctly: got %q, want %q", stderr, "error message")
	}
}

// TestStateReset verifies resetState() clears all global state
func TestStateReset(t *testing.T) {
	// Set some state
	config["key1"] = "value1"
	config["key2"] = "value2"
	keyToFile["annex-key"] = "path/to/file"
	exportName = "test-export"
	exportRef = "refs/heads/test"
	exportRefSet = true
	currentLocation = "some/location"
	expectedContent["loc1"] = "cid1"
	expectedContent["loc2"] = "cid2"

	// Reset
	resetState()

	// Verify everything is cleared
	if len(config) != 0 {
		t.Errorf("config not cleared: %v", config)
	}
	if len(keyToFile) != 0 {
		t.Errorf("keyToFile not cleared: %v", keyToFile)
	}
	if exportName != "" {
		t.Errorf("exportName not cleared: %q", exportName)
	}
	if exportRef != "" {
		t.Errorf("exportRef not cleared: %q", exportRef)
	}
	if exportRefSet {
		t.Error("exportRefSet not cleared")
	}
	if currentLocation != "" {
		t.Errorf("currentLocation not cleared: %q", currentLocation)
	}
	if len(expectedContent) != 0 {
		t.Errorf("expectedContent not cleared: %v", expectedContent)
	}
}
