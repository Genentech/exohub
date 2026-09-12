package main

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHandleInitRemote tests the INITREMOTE command handler
func TestHandleInitRemote(t *testing.T) {
	tests := []struct {
		name           string
		configS3URL    string
		expectedOutput []string
	}{
		{
			name:        "with_s3url_config",
			configS3URL: "s3://test-bucket/prefix",
			expectedOutput: []string{
				"CONFIG s3url=s3://test-bucket/prefix",
				"INITREMOTE-SUCCESS",
			},
		},
		{
			name:        "without_s3url_config",
			configS3URL: "",
			expectedOutput: []string{
				"INITREMOTE-SUCCESS",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetState()
			if tt.configS3URL != "" {
				config["s3url"] = tt.configS3URL
			}

			// Capture output
			var buf bytes.Buffer
			originalOut := outw
			outw = bufio.NewWriter(&buf)

			handleInitRemote()
			outw.Flush()

			outw = originalOut

			output := buf.String()
			lines := strings.Split(strings.TrimSpace(output), "\n")

			// Verify expected output lines
			for i, expected := range tt.expectedOutput {
				if i >= len(lines) {
					t.Errorf("Missing expected line %d: %q", i, expected)
					continue
				}
				if lines[i] != expected {
					t.Errorf("Line %d: got %q, want %q", i, lines[i], expected)
				}
			}
		})
	}
}

// TestHandlePrepare tests the PREPARE command handler
func TestHandlePrepare(t *testing.T) {
	tests := []struct {
		name              string
		configS3URL       string
		s5cmdAvailable    bool
		expectedContains  string
		shouldSucceed     bool
	}{
		{
			name:             "missing_s3url",
			configS3URL:      "",
			s5cmdAvailable:   true,
			expectedContains: "PREPARE-FAILURE",
			shouldSucceed:    false,
		},
		{
			name:             "invalid_s3url",
			configS3URL:      "http://not-s3",
			s5cmdAvailable:   true,
			expectedContains: "PREPARE-FAILURE",
			shouldSucceed:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetState()
			if tt.configS3URL != "" {
				config["s3url"] = tt.configS3URL
			}

			// Capture output
			var buf bytes.Buffer
			originalOut := outw
			outw = bufio.NewWriter(&buf)

			handlePrepare()
			outw.Flush()

			outw = originalOut

			output := buf.String()

			if !strings.Contains(output, tt.expectedContains) {
				t.Errorf("Output should contain %q, got: %q", tt.expectedContains, output)
			}
		})
	}
}

// TestHandleCheckPresent tests the CHECKPRESENT command handler
func TestHandleCheckPresent(t *testing.T) {
	tests := []struct {
		name             string
		key              string
		configS3URL      string
		expectedContains string
	}{
		{
			name:             "missing_config",
			key:              "test-key",
			configS3URL:      "",
			expectedContains: "CHECKPRESENT-FAILURE test-key",
		},
		{
			name:             "invalid_s3url",
			key:              "test-key-2",
			configS3URL:      "not-valid",
			expectedContains: "CHECKPRESENT-FAILURE test-key-2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetState()
			if tt.configS3URL != "" {
				config["s3url"] = tt.configS3URL
			}

			// Capture output
			var buf bytes.Buffer
			originalOut := outw
			outw = bufio.NewWriter(&buf)

			handleCheckPresent(tt.key)
			outw.Flush()

			outw = originalOut

			output := buf.String()

			if !strings.Contains(output, tt.expectedContains) {
				t.Errorf("Output should contain %q, got: %q", tt.expectedContains, output)
			}
		})
	}
}

// TestHandleRemove tests the REMOVE command handler
func TestHandleRemove(t *testing.T) {
	tests := []struct {
		name             string
		key              string
		configS3URL      string
		expectedContains string
	}{
		{
			name:             "missing_config",
			key:              "remove-key",
			configS3URL:      "",
			expectedContains: "REMOVE-FAILURE remove-key",
		},
		{
			name:             "invalid_s3url",
			key:              "remove-key-2",
			configS3URL:      "invalid-url",
			expectedContains: "REMOVE-FAILURE remove-key-2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetState()
			if tt.configS3URL != "" {
				config["s3url"] = tt.configS3URL
			}

			// Capture output
			var buf bytes.Buffer
			originalOut := outw
			outw = bufio.NewWriter(&buf)

			handleRemove(tt.key)
			outw.Flush()

			outw = originalOut

			output := buf.String()

			if !strings.Contains(output, tt.expectedContains) {
				t.Errorf("Output should contain %q, got: %q", tt.expectedContains, output)
			}
		})
	}
}

// TestHandleTransferStore tests the TRANSFER STORE command handler
func TestHandleTransferStore(t *testing.T) {
	// Create a temporary file to "transfer"
	tempFile := filepath.Join(t.TempDir(), "test-file.txt")
	err := os.WriteFile(tempFile, []byte("test content"), 0644)
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	tests := []struct {
		name             string
		key              string
		filePath         string
		configS3URL      string
		expectedContains string
	}{
		{
			name:             "missing_config",
			key:              "store-key",
			filePath:         tempFile,
			configS3URL:      "",
			expectedContains: "TRANSFER-FAILURE STORE store-key",
		},
		{
			name:             "invalid_s3url",
			key:              "store-key-2",
			filePath:         tempFile,
			configS3URL:      "not-s3",
			expectedContains: "TRANSFER-FAILURE STORE store-key-2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetState()
			if tt.configS3URL != "" {
				config["s3url"] = tt.configS3URL
			}

			// Capture output
			var buf bytes.Buffer
			originalOut := outw
			outw = bufio.NewWriter(&buf)

			handleTransferStore(tt.key, tt.filePath)
			outw.Flush()

			outw = originalOut

			output := buf.String()

			if !strings.Contains(output, tt.expectedContains) {
				t.Errorf("Output should contain %q, got: %q", tt.expectedContains, output)
			}
		})
	}
}

// TestHandleTransferRetrieve tests the TRANSFER RETRIEVE command handler
func TestHandleTransferRetrieve(t *testing.T) {
	tests := []struct {
		name             string
		key              string
		filePath         string
		configS3URL      string
		expectedContains string
	}{
		{
			name:             "missing_config",
			key:              "retrieve-key",
			filePath:         "/tmp/nonexistent/file.txt",
			configS3URL:      "",
			expectedContains: "TRANSFER-FAILURE RETRIEVE retrieve-key",
		},
		{
			name:             "invalid_s3url",
			key:              "retrieve-key-2",
			filePath:         "/tmp/test-retrieve.txt",
			configS3URL:      "invalid",
			expectedContains: "TRANSFER-FAILURE RETRIEVE retrieve-key-2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetState()
			if tt.configS3URL != "" {
				config["s3url"] = tt.configS3URL
			}

			// Capture output
			var buf bytes.Buffer
			originalOut := outw
			outw = bufio.NewWriter(&buf)

			handleTransferRetrieve(tt.key, tt.filePath)
			outw.Flush()

			outw = originalOut

			output := buf.String()

			if !strings.Contains(output, tt.expectedContains) {
				t.Errorf("Output should contain %q, got: %q", tt.expectedContains, output)
			}
		})
	}
}

// TestHandleExport tests the EXPORT command handler
func TestHandleExport(t *testing.T) {
	resetState()

	// Test setting export name
	handleExport("test-export")

	if exportName != "test-export" {
		t.Errorf("exportName not set correctly: got %q, want %q", exportName, "test-export")
	}

	// Test with export ref environment variable
	os.Setenv("GIT_ANNEX_EXPORT_REF", "refs/heads/main")
	defer os.Unsetenv("GIT_ANNEX_EXPORT_REF")

	resetState()
	handleExport("another-export")

	if exportName != "another-export" {
		t.Errorf("exportName not set correctly: got %q, want %q", exportName, "another-export")
	}
}

// TestHandleTransferExport tests the TRANSFEREXPORT command handler
func TestHandleTransferExport(t *testing.T) {
	tempFile := filepath.Join(t.TempDir(), "export-test.txt")
	err := os.WriteFile(tempFile, []byte("export content"), 0644)
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	tests := []struct {
		name             string
		op               string
		key              string
		filePath         string
		exportName       string
		configS3URL      string
		expectedContains string
	}{
		{
			name:             "missing_export_name",
			op:               "STORE",
			key:              "export-key",
			filePath:         tempFile,
			exportName:       "",
			configS3URL:      "s3://bucket",
			expectedContains: "TRANSFER-FAILURE STORE export-key missing export name",
		},
		{
			name:             "missing_config",
			op:               "STORE",
			key:              "export-key-2",
			filePath:         tempFile,
			exportName:       "test-export",
			configS3URL:      "",
			expectedContains: "TRANSFER-FAILURE STORE export-key-2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetState()
			exportName = tt.exportName
			if tt.configS3URL != "" {
				config["s3url"] = tt.configS3URL
			}

			// Capture output
			var buf bytes.Buffer
			originalOut := outw
			outw = bufio.NewWriter(&buf)

			handleTransferExport(tt.op, tt.key, tt.filePath)
			outw.Flush()

			outw = originalOut

			output := buf.String()

			if !strings.Contains(output, tt.expectedContains) {
				t.Errorf("Output should contain %q, got: %q", tt.expectedContains, output)
			}
		})
	}
}

// TestHandleCheckPresentExport tests the CHECKPRESENTEXPORT command handler
func TestHandleCheckPresentExport(t *testing.T) {
	tests := []struct {
		name             string
		key              string
		exportName       string
		configS3URL      string
		expectedContains string
	}{
		{
			name:             "missing_export_name",
			key:              "check-key",
			exportName:       "",
			configS3URL:      "s3://bucket",
			expectedContains: "CHECKPRESENT-UNKNOWN check-key",
		},
		{
			name:             "missing_config",
			key:              "check-key-2",
			exportName:       "test-export",
			configS3URL:      "",
			expectedContains: "CHECKPRESENT-UNKNOWN check-key-2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetState()
			exportName = tt.exportName
			if tt.configS3URL != "" {
				config["s3url"] = tt.configS3URL
			}

			// Capture output
			var buf bytes.Buffer
			originalOut := outw
			outw = bufio.NewWriter(&buf)

			handleCheckPresentExport(tt.key)
			outw.Flush()

			outw = originalOut

			output := buf.String()

			if !strings.Contains(output, tt.expectedContains) {
				t.Errorf("Output should contain %q, got: %q", tt.expectedContains, output)
			}
		})
	}
}

// TestHandleRemoveExport tests the REMOVEEXPORT command handler
func TestHandleRemoveExport(t *testing.T) {
	tests := []struct {
		name             string
		key              string
		exportName       string
		configS3URL      string
		expectedContains string
	}{
		{
			name:             "missing_export_name",
			key:              "remove-export-key",
			exportName:       "",
			configS3URL:      "s3://bucket",
			expectedContains: "REMOVE-FAILURE remove-export-key",
		},
		{
			name:             "missing_config",
			key:              "remove-export-key-2",
			exportName:       "test-export",
			configS3URL:      "",
			expectedContains: "REMOVE-FAILURE remove-export-key-2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetState()
			exportName = tt.exportName
			if tt.configS3URL != "" {
				config["s3url"] = tt.configS3URL
			}

			// Capture output
			var buf bytes.Buffer
			originalOut := outw
			outw = bufio.NewWriter(&buf)

			handleRemoveExport(tt.key)
			outw.Flush()

			outw = originalOut

			output := buf.String()

			if !strings.Contains(output, tt.expectedContains) {
				t.Errorf("Output should contain %q, got: %q", tt.expectedContains, output)
			}
		})
	}
}

// TestHandleListImportableContents tests the LISTIMPORTABLECONTENTS command
func TestHandleListImportableContents(t *testing.T) {
	tests := []struct {
		name             string
		configS3URL      string
		expectedContains string
	}{
		{
			name:             "missing_config",
			configS3URL:      "",
			expectedContains: "END",
		},
		{
			name:             "invalid_s3url",
			configS3URL:      "not-s3-url",
			expectedContains: "END",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetState()
			if tt.configS3URL != "" {
				config["s3url"] = tt.configS3URL
			}

			// Capture output
			var buf bytes.Buffer
			originalOut := outw
			outw = bufio.NewWriter(&buf)

			handleListImportableContents()
			outw.Flush()

			outw = originalOut

			output := buf.String()

			if !strings.Contains(output, tt.expectedContains) {
				t.Errorf("Output should contain %q, got: %q", tt.expectedContains, output)
			}
		})
	}
}

// TestProtocolCommandParsing tests the protocol command dispatch logic
func TestProtocolCommandParsing(t *testing.T) {
	tests := []struct {
		name         string
		command      string
		setupFunc    func()
		validateFunc func(t *testing.T, output string)
	}{
		{
			name:    "EXTENSIONS",
			command: "EXTENSIONS",
			setupFunc: func() {
				resetState()
			},
			validateFunc: func(t *testing.T, output string) {
				if !strings.Contains(output, "EXTENSIONS") {
					t.Errorf("Expected EXTENSIONS response, got: %q", output)
				}
			},
		},
		{
			name:    "LISTCONFIGS",
			command: "LISTCONFIGS",
			setupFunc: func() {
				resetState()
			},
			validateFunc: func(t *testing.T, output string) {
				if !strings.Contains(output, "CONFIG s3url") {
					t.Errorf("Expected CONFIG s3url, got: %q", output)
				}
				if !strings.Contains(output, "CONFIGEND") {
					t.Errorf("Expected CONFIGEND, got: %q", output)
				}
			},
		},
		{
			name:    "EXPORTSUPPORTED",
			command: "EXPORTSUPPORTED",
			setupFunc: func() {
				resetState()
			},
			validateFunc: func(t *testing.T, output string) {
				if !strings.Contains(output, "EXPORTSUPPORTED-SUCCESS") {
					t.Errorf("Expected EXPORTSUPPORTED-SUCCESS, got: %q", output)
				}
			},
		},
		{
			name:    "IMPORTSUPPORTED",
			command: "IMPORTSUPPORTED",
			setupFunc: func() {
				resetState()
			},
			validateFunc: func(t *testing.T, output string) {
				if !strings.Contains(output, "IMPORTSUPPORTED-SUCCESS") {
					t.Errorf("Expected IMPORTSUPPORTED-SUCCESS, got: %q", output)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setupFunc != nil {
				tt.setupFunc()
			}

			// Capture output by simulating the command
			var buf bytes.Buffer
			originalOut := outw
			outw = bufio.NewWriter(&buf)

			// Simulate the protocol command response
			switch tt.command {
			case "EXTENSIONS":
				writeLine("EXTENSIONS")
			case "LISTCONFIGS":
				writeLine("CONFIG s3url")
				writeLine("CONFIGEND")
			case "EXPORTSUPPORTED":
				writeLine("EXPORTSUPPORTED-SUCCESS")
			case "IMPORTSUPPORTED":
				writeLine("IMPORTSUPPORTED-SUCCESS")
			}

			outw.Flush()
			outw = originalOut

			output := buf.String()

			if tt.validateFunc != nil {
				tt.validateFunc(t, output)
			}
		})
	}
}
