package main

import (
	"os"
	"testing"
	"time"
)

// Additional helper function tests to increase coverage

// TestSanitizeMsg tests error message sanitization
func TestSanitizeMsg(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "simple message",
			input: "simple error message",
			want:  "simple error message",
		},
		{
			name:  "newlines replaced with spaces",
			input: "line1\nline2\nline3",
			want:  "line1 line2 line3",
		},
		{
			name:  "carriage returns replaced",
			input: "has\rcarriage\rreturns",
			want:  "has carriage returns",
		},
		{
			name:  "leading and trailing spaces trimmed",
			input: "  spaces around  ",
			want:  "spaces around",
		},
		{
			name:  "mixed line endings",
			input: "mixed\r\nnewlines\nand\rreturns",
			want:  "mixed  newlines and returns",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "only whitespace",
			input: "   \n\r   ",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeMsg(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeMsg(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestGetExportRef tests export reference retrieval
func TestGetExportRef(t *testing.T) {
	tests := []struct {
		name    string
		envVar  string
		want    string
	}{
		{
			name:   "with export ref env",
			envVar: "refs/heads/main",
			want:   "refs/heads/main",
		},
		{
			name:   "with tag ref",
			envVar: "refs/tags/v1.0.0",
			want:   "refs/tags/v1.0.0",
		},
		{
			name:   "without env var",
			envVar: "",
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetState()

			// Unset the env var to ensure clean state
			os.Unsetenv("GIT_ANNEX_EXPORT_REF")

			if tt.envVar != "" {
				os.Setenv("GIT_ANNEX_EXPORT_REF", tt.envVar)
				defer os.Unsetenv("GIT_ANNEX_EXPORT_REF")
			}

			got := getExportRef()

			if got != tt.want {
				t.Errorf("getExportRef() = %q, want %q", got, tt.want)
			}

			// getExportRef() always sets exportRefSet to true (lazy initialization pattern)
			if !exportRefSet {
				t.Error("exportRefSet should be true after getExportRef() is called")
			}

			if exportRef != tt.want {
				t.Errorf("exportRef = %q, want %q", exportRef, tt.want)
			}
		})
	}
}

// TestResetLocationState tests location state cleanup
func TestResetLocationState(t *testing.T) {
	// Setup state with multiple locations
	currentLocation = "active/location"
	expectedContent["active/location"] = "etag:abc123"
	expectedContent["other/location"] = "etag:def456"
	expectedContent["third/location"] = "etag:ghi789"

	// Reset
	resetLocationState()

	// Verify current location cleared
	if currentLocation != "" {
		t.Errorf("expected currentLocation to be cleared, got %q", currentLocation)
	}

	// Verify only current location removed from map
	if _, exists := expectedContent["active/location"]; exists {
		t.Error("expected active location to be removed from expectedContent")
	}

	// Verify other locations preserved
	if _, exists := expectedContent["other/location"]; !exists {
		t.Error("expected other location to remain in expectedContent")
	}
	if _, exists := expectedContent["third/location"]; !exists {
		t.Error("expected third location to remain in expectedContent")
	}

	// Verify map size
	if len(expectedContent) != 2 {
		t.Errorf("expected 2 entries in expectedContent, got %d", len(expectedContent))
	}
}

// TestHandleLocation tests location tracking
func TestHandleLocation(t *testing.T) {
	resetState()

	locations := []string{"file1.txt", "dir/file2.txt", "deep/nested/file3.txt"}

	for _, loc := range locations {
		handleLocation(loc)

		if currentLocation != loc {
			t.Errorf("after handleLocation(%q), currentLocation = %q, want %q", loc, currentLocation, loc)
		}
	}
}

// TestHandleExpected tests expected content tracking
func TestHandleExpected(t *testing.T) {
	resetState()

	// Set location first
	currentLocation = "test/file.txt"

	// Test setting expected content
	cid := "etag:abc123def"
	handleExpected(cid)

	if expectedContent[currentLocation] != cid {
		t.Errorf("expectedContent[%q] = %q, want %q", currentLocation, expectedContent[currentLocation], cid)
	}

	// Test updating expected content for same location
	newCid := "etag:xyz789"
	handleExpected(newCid)

	if expectedContent[currentLocation] != newCid {
		t.Errorf("expectedContent[%q] = %q, want %q", currentLocation, expectedContent[currentLocation], newCid)
	}
}

// TestHandleNothingExpected tests NOTHINGEXPECTED handling
func TestHandleNothingExpected(t *testing.T) {
	resetState()

	currentLocation = "test/file.txt"

	handleNothingExpected()

	// NOTHINGEXPECTED should set empty string
	if expectedContent[currentLocation] != "" {
		t.Errorf("expectedContent[%q] = %q, want empty string", currentLocation, expectedContent[currentLocation])
	}

	// Verify it's actually in the map (not just missing)
	if _, exists := expectedContent[currentLocation]; !exists {
		t.Error("expected location to exist in expectedContent with empty value")
	}
}

// TestFormatContentIdentifierEdgeCases tests content identifier edge cases
func TestFormatContentIdentifierEdgeCases(t *testing.T) {
	tests := []struct {
		name  string
		etag  string
		size  int64
		mtime time.Time
		want  string
	}{
		{
			name:  "zero size with etag",
			etag:  "abc",
			size:  0,
			mtime: time.Unix(1000, 0),
			want:  "etag:abc",
		},
		{
			name:  "zero size no etag",
			etag:  "",
			size:  0,
			mtime: time.Unix(1000, 0),
			want:  "size:0:1000",
		},
		{
			name:  "very large size",
			etag:  "",
			size:  9999999999,
			mtime: time.Unix(2000, 0),
			want:  "size:9999999999:2000",
		},
		{
			name:  "etag with special chars",
			etag:  "abc-123_xyz",
			size:  100,
			mtime: time.Unix(1500, 0),
			want:  "etag:abc-123_xyz",
		},
		{
			name:  "quoted etag with spaces",
			etag:  `"etag with spaces"`,
			size:  200,
			mtime: time.Unix(1500, 0),
			want:  "etag:etag with spaces",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatContentIdentifier(tt.etag, tt.size, tt.mtime)
			if got != tt.want {
				t.Errorf("formatContentIdentifier() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestParseS5cmdJSONEdgeCases tests JSON parsing edge cases
func TestParseS5cmdJSONEdgeCases(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantCount   int
		wantErr     bool
		description string
	}{
		{
			name:        "empty input",
			input:       "",
			wantCount:   0,
			wantErr:     false,
			description: "empty input should return empty slice",
		},
		{
			name:        "whitespace only",
			input:       "   \n\n   ",
			wantCount:   0,
			wantErr:     false,
			description: "whitespace should be ignored",
		},
		{
			name:        "single file",
			input:       `{"key":"bucket/file.txt","type":"file","size":100,"etag":"abc","last_modified":"2024-01-01T00:00:00Z","storage_class":"STANDARD"}`,
			wantCount:   1,
			wantErr:     false,
			description: "single file should parse correctly",
		},
		{
			name: "mixed files and directories",
			input: `{"key":"bucket/file1.txt","type":"file","size":100,"etag":"abc","last_modified":"2024-01-01T00:00:00Z","storage_class":"STANDARD"}
{"key":"bucket/dir/","type":"directory","size":0,"etag":"","last_modified":"2024-01-01T00:00:00Z","storage_class":""}
{"key":"bucket/file2.txt","type":"file","size":200,"etag":"def","last_modified":"2024-01-01T00:00:00Z","storage_class":"STANDARD"}`,
			wantCount:   2,
			wantErr:     false,
			description: "should filter out directories",
		},
		{
			name:        "invalid JSON",
			input:       `not valid json`,
			wantCount:   0,
			wantErr:     false,
			description: "invalid JSON should return empty slice (parseS5cmdJSON doesn't return errors)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects, err := parseS5cmdJSON([]byte(tt.input))

			if (err != nil) != tt.wantErr {
				t.Errorf("%s: wantErr=%v, got err=%v", tt.description, tt.wantErr, err)
			}

			if !tt.wantErr && len(objects) != tt.wantCount {
				t.Errorf("%s: got %d objects, want %d", tt.description, len(objects), tt.wantCount)
			}
		})
	}
}

// TestEnsureCfgEnvironmentPrecedence tests configuration precedence
func TestEnsureCfgEnvironmentPrecedence(t *testing.T) {
	tests := []struct {
		name        string
		setup       func()
		cleanup     func()
		wantBucket  string
		wantPrefix  string
		wantErr     bool
		description string
	}{
		{
			name: "config takes precedence over env",
			setup: func() {
				config["s3url"] = "s3://config-bucket/config-prefix"
				os.Setenv("ANNEX_S3URL", "s3://env-bucket/env-prefix")
			},
			cleanup: func() {
				os.Unsetenv("ANNEX_S3URL")
			},
			wantBucket:  "config-bucket",
			wantPrefix:  "config-prefix",
			wantErr:     false,
			description: "config should take precedence over environment",
		},
		{
			name: "ANNEX_S3URL over S3URL",
			setup: func() {
				os.Setenv("ANNEX_S3URL", "s3://annex-bucket")
				os.Setenv("S3URL", "s3://s3-bucket")
			},
			cleanup: func() {
				os.Unsetenv("ANNEX_S3URL")
				os.Unsetenv("S3URL")
			},
			wantBucket:  "annex-bucket",
			wantPrefix:  "",
			wantErr:     false,
			description: "ANNEX_S3URL should take precedence over S3URL",
		},
		{
			name: "S3URL as fallback",
			setup: func() {
				os.Setenv("S3URL", "s3://fallback-bucket/path")
			},
			cleanup: func() {
				os.Unsetenv("S3URL")
			},
			wantBucket:  "fallback-bucket",
			wantPrefix:  "path",
			wantErr:     false,
			description: "S3URL should be used as fallback",
		},
		{
			name: "no configuration",
			setup: func() {
				// No setup
			},
			cleanup:     func() {},
			wantErr:     true,
			description: "should error when no s3url is configured",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetState()
			tt.setup()
			defer tt.cleanup()

			bucket, prefix, err := ensureCfg()

			if (err != nil) != tt.wantErr {
				t.Errorf("%s: wantErr=%v, got err=%v", tt.description, tt.wantErr, err)
				return
			}

			if !tt.wantErr {
				if bucket != tt.wantBucket {
					t.Errorf("%s: bucket=%q, want %q", tt.description, bucket, tt.wantBucket)
				}
				if prefix != tt.wantPrefix {
					t.Errorf("%s: prefix=%q, want %q", tt.description, prefix, tt.wantPrefix)
				}
			}
		})
	}
}

// TestGetS5cmdVersion tests version string extraction
func TestGetS5cmdVersion(t *testing.T) {
	// This test verifies getS5cmdVersion() logic by testing firstLineSafe helper
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
		{
			input:    "s5cmd version v2.2.2",
			expected: "s5cmd version v2.2.2",
		},
	}

	for _, tt := range tests {
		result := firstLineSafe(tt.input)
		if result != tt.expected {
			t.Errorf("firstLineSafe(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}
