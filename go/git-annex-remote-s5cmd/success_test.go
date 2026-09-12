package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Phase 2 Success Path Tests: Protocol Handlers
// Target: Increase coverage from 25.9% to 70-80%

// ========================================
// Group A: Initialization Handlers
// ========================================

// TestHandleInitRemoteWithS3URL tests INITREMOTE with s3url configuration
func TestHandleInitRemoteWithS3URL(t *testing.T) {
	resetState()
	config["s3url"] = "s3://my-bucket/prefix"

	// Capture output
	var output []string
	originalWriteLine := writeLine
	writeLine = func(line string) {
		output = append(output, line)
	}
	defer func() { writeLine = originalWriteLine }()

	// Execute
	handleInitRemote()

	// Verify CONFIG line emitted
	foundConfig := false
	for _, line := range output {
		if line == "CONFIG s3url=s3://my-bucket/prefix" {
			foundConfig = true
			break
		}
	}
	if !foundConfig {
		t.Errorf("expected CONFIG line, got: %v", output)
	}

	// Verify success
	foundSuccess := false
	for _, line := range output {
		if line == "INITREMOTE-SUCCESS" {
			foundSuccess = true
			break
		}
	}
	if !foundSuccess {
		t.Errorf("expected INITREMOTE-SUCCESS, got: %v", output)
	}
}

// TestHandleInitRemoteWithoutS3URL tests INITREMOTE without s3url
func TestHandleInitRemoteWithoutS3URL(t *testing.T) {
	resetState()
	// No s3url in config

	var output []string
	originalWriteLine := writeLine
	writeLine = func(line string) {
		output = append(output, line)
	}
	defer func() { writeLine = originalWriteLine }()

	handleInitRemote()

	// Should still succeed
	foundSuccess := false
	for _, line := range output {
		if line == "INITREMOTE-SUCCESS" {
			foundSuccess = true
			break
		}
	}
	if !foundSuccess {
		t.Errorf("expected INITREMOTE-SUCCESS even without s3url, got: %v", output)
	}

	// Should NOT have CONFIG line
	for _, line := range output {
		if strings.HasPrefix(line, "CONFIG s3url=") {
			t.Errorf("unexpected CONFIG line without s3url: %s", line)
		}
	}
}

// TestHandlePrepareSuccess tests PREPARE with successful s5cmd check
func TestHandlePrepareSuccess(t *testing.T) {
	resetState()
	config["s3url"] = "s3://bucket/prefix"

	// Mock successful s5cmd version check
	callCount := 0
	originalRun := run
	run = func(args ...string) runResult {
		callCount++
		if len(args) >= 2 && args[0] == s5cmdBin {
			if args[1] == "version" {
				return runResult{code: 0, stdout: []byte("s5cmd version v2.2.2")}
			}
			if args[1] == "ls" {
				return runResult{code: 0, stdout: []byte("")}
			}
		}
		return runResult{code: 1}
	}
	defer func() { run = originalRun }()

	var output []string
	originalWriteLine := writeLine
	writeLine = func(line string) {
		output = append(output, line)
	}
	defer func() { writeLine = originalWriteLine }()

	handlePrepare()

	foundSuccess := false
	for _, line := range output {
		if line == "PREPARE-SUCCESS" {
			foundSuccess = true
			break
		}
	}
	if !foundSuccess {
		t.Errorf("expected PREPARE-SUCCESS, got: %v", output)
	}

	if callCount == 0 {
		t.Error("expected s5cmd to be called")
	}
}

// TestHandlePrepareMissingS5cmd tests PREPARE when s5cmd binary is missing
func TestHandlePrepareMissingS5cmd(t *testing.T) {
	resetState()
	config["s3url"] = "s3://bucket/prefix"

	originalRun := run
	run = func(args ...string) runResult {
		return runResult{code: 127, stderr: []byte("s5cmd: command not found")}
	}
	defer func() { run = originalRun }()

	var output []string
	originalWriteLine := writeLine
	writeLine = func(line string) {
		output = append(output, line)
	}
	defer func() { writeLine = originalWriteLine }()

	handlePrepare()

	foundFailure := false
	for _, line := range output {
		if strings.HasPrefix(line, "PREPARE-FAILURE") && strings.Contains(line, "s5cmd not found") {
			foundFailure = true
			break
		}
	}
	if !foundFailure {
		t.Errorf("expected PREPARE-FAILURE with s5cmd error, got: %v", output)
	}
}

// TestHandlePrepareMissingConfig tests PREPARE without s3url configured
func TestHandlePrepareMissingConfig(t *testing.T) {
	resetState()

	var output []string
	originalWriteLine := writeLine
	writeLine = func(line string) {
		output = append(output, line)
	}
	defer func() { writeLine = originalWriteLine }()

	handlePrepare()

	foundFailure := false
	for _, line := range output {
		if strings.HasPrefix(line, "PREPARE-FAILURE") && strings.Contains(line, "missing required config") {
			foundFailure = true
			break
		}
	}
	if !foundFailure {
		t.Errorf("expected PREPARE-FAILURE for missing config, got: %v", output)
	}
}

// ========================================
// Group B: Basic Annex Operations
// ========================================

// TestHandleCheckPresentKeyExists tests CHECKPRESENT for existing key
func TestHandleCheckPresentKeyExists(t *testing.T) {
	resetState()
	config["s3url"] = "s3://bucket/prefix"
	key := "SHA256E-s1234--abc123.txt"

	var capturedArgs []string
	originalRun := run
	run = func(args ...string) runResult {
		capturedArgs = args
		if len(args) >= 2 && args[1] == "ls" {
			return runResult{code: 0, stdout: []byte("2024-01-01 00:00:00   1234 " + key)}
		}
		return runResult{code: 1}
	}
	defer func() { run = originalRun }()

	var output []string
	originalWriteLine := writeLine
	writeLine = func(line string) {
		output = append(output, line)
	}
	defer func() { writeLine = originalWriteLine }()

	handleCheckPresent(key)

	expectedResponse := "CHECKPRESENT-SUCCESS " + key
	found := false
	for _, line := range output {
		if line == expectedResponse {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected %q, got: %v", expectedResponse, output)
	}

	expectedPath := "s3://bucket/prefix/" + key
	if len(capturedArgs) < 3 || capturedArgs[2] != expectedPath {
		t.Errorf("expected ls path %q, got args: %v", expectedPath, capturedArgs)
	}
}

// TestHandleCheckPresentKeyMissing tests CHECKPRESENT for missing key
func TestHandleCheckPresentKeyMissing(t *testing.T) {
	resetState()
	config["s3url"] = "s3://bucket/prefix"
	key := "SHA256E-s1234--missing.txt"

	originalRun := run
	run = func(args ...string) runResult {
		if len(args) >= 2 && args[1] == "ls" {
			return runResult{code: 1, stderr: []byte("ERROR: not found")}
		}
		return runResult{code: 1}
	}
	defer func() { run = originalRun }()

	var output []string
	originalWriteLine := writeLine
	writeLine = func(line string) {
		output = append(output, line)
	}
	defer func() { writeLine = originalWriteLine }()

	handleCheckPresent(key)

	expectedResponse := "CHECKPRESENT-FAILURE " + key
	found := false
	for _, line := range output {
		if line == expectedResponse {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected %q, got: %v", expectedResponse, output)
	}
}

// TestHandleTransferStoreSuccess tests successful TRANSFER STORE
func TestHandleTransferStoreSuccess(t *testing.T) {
	resetState()
	config["s3url"] = "s3://bucket/prefix"
	key := "SHA256E-s100--test.txt"

	tmpfile, err := os.CreateTemp("", "transfer-*.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())
	tmpfile.WriteString("test file content for upload")
	tmpfile.Close()

	var capturedSrc, capturedDest string
	originalRun := run
	run = func(args ...string) runResult {
		if len(args) >= 4 && args[1] == "cp" {
			capturedSrc = args[2]
			capturedDest = args[3]
			return runResult{code: 0}
		}
		return runResult{code: 1}
	}
	defer func() { run = originalRun }()

	var output []string
	originalWriteLine := writeLine
	writeLine = func(line string) {
		output = append(output, line)
	}
	defer func() { writeLine = originalWriteLine }()

	handleTransferStore(key, tmpfile.Name())

	expectedResponse := "TRANSFER-SUCCESS STORE " + key
	found := false
	for _, line := range output {
		if line == expectedResponse {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected %q, got: %v", expectedResponse, output)
	}

	if capturedSrc != tmpfile.Name() {
		t.Errorf("expected source %q, got %q", tmpfile.Name(), capturedSrc)
	}
	expectedDest := "s3://bucket/prefix/" + key
	if capturedDest != expectedDest {
		t.Errorf("expected dest %q, got %q", expectedDest, capturedDest)
	}
}

// TestHandleTransferStoreFileNotFound tests TRANSFER STORE with missing file
func TestHandleTransferStoreFileNotFound(t *testing.T) {
	resetState()
	config["s3url"] = "s3://bucket/prefix"
	key := "SHA256E-s100--test.txt"
	nonExistentFile := "/tmp/nonexistent-file-12345.txt"

	var output []string
	originalWriteLine := writeLine
	writeLine = func(line string) {
		output = append(output, line)
	}
	defer func() { writeLine = originalWriteLine }()

	handleTransferStore(key, nonExistentFile)

	foundFailure := false
	for _, line := range output {
		if strings.HasPrefix(line, "TRANSFER-FAILURE STORE "+key) {
			foundFailure = true
			break
		}
	}
	if !foundFailure {
		t.Errorf("expected TRANSFER-FAILURE for missing file, got: %v", output)
	}
}

// TestHandleTransferStoreS3Failure tests TRANSFER STORE with S3 upload failure
func TestHandleTransferStoreS3Failure(t *testing.T) {
	resetState()
	config["s3url"] = "s3://bucket/prefix"
	key := "SHA256E-s100--test.txt"

	tmpfile, _ := os.CreateTemp("", "transfer-*.txt")
	defer os.Remove(tmpfile.Name())
	tmpfile.WriteString("content")
	tmpfile.Close()

	originalRun := run
	run = func(args ...string) runResult {
		if len(args) >= 2 && args[1] == "cp" {
			return runResult{code: 1, stderr: []byte("ERROR: AccessDenied: Access Denied")}
		}
		return runResult{code: 1}
	}
	defer func() { run = originalRun }()

	var output []string
	originalWriteLine := writeLine
	writeLine = func(line string) {
		output = append(output, line)
	}
	defer func() { writeLine = originalWriteLine }()

	handleTransferStore(key, tmpfile.Name())

	foundFailure := false
	for _, line := range output {
		if strings.HasPrefix(line, "TRANSFER-FAILURE STORE "+key) && strings.Contains(line, "AccessDenied") {
			foundFailure = true
			break
		}
	}
	if !foundFailure {
		t.Errorf("expected TRANSFER-FAILURE with error, got: %v", output)
	}
}

// TestHandleTransferRetrieveSuccess tests successful TRANSFER RETRIEVE
func TestHandleTransferRetrieveSuccess(t *testing.T) {
	resetState()
	config["s3url"] = "s3://bucket/prefix"
	key := "SHA256E-s100--download.txt"

	tmpdir, err := os.MkdirTemp("", "retrieve-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpdir)

	targetFile := filepath.Join(tmpdir, "subdir", "file.txt")
	expectedContent := "downloaded content from S3"

	originalRun := run
	run = func(args ...string) runResult {
		if len(args) >= 4 && args[1] == "cp" {
			os.MkdirAll(filepath.Dir(targetFile), 0755)
			os.WriteFile(targetFile, []byte(expectedContent), 0644)
			return runResult{code: 0}
		}
		return runResult{code: 1}
	}
	defer func() { run = originalRun }()

	var output []string
	originalWriteLine := writeLine
	writeLine = func(line string) {
		output = append(output, line)
	}
	defer func() { writeLine = originalWriteLine }()

	handleTransferRetrieve(key, targetFile)

	expectedResponse := "TRANSFER-SUCCESS RETRIEVE " + key
	found := false
	for _, line := range output {
		if line == expectedResponse {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected %q, got: %v", expectedResponse, output)
	}

	if _, err := os.Stat(targetFile); os.IsNotExist(err) {
		t.Errorf("expected file at %s to exist", targetFile)
	}

	content, _ := os.ReadFile(targetFile)
	if string(content) != expectedContent {
		t.Errorf("expected content %q, got %q", expectedContent, string(content))
	}
}

// TestHandleTransferRetrieveS3Failure tests TRANSFER RETRIEVE with S3 download failure
func TestHandleTransferRetrieveS3Failure(t *testing.T) {
	resetState()
	config["s3url"] = "s3://bucket/prefix"
	key := "SHA256E-s100--missing.txt"
	targetFile := "/tmp/download-test.txt"

	originalRun := run
	run = func(args ...string) runResult {
		if len(args) >= 2 && args[1] == "cp" {
			return runResult{code: 1, stderr: []byte("ERROR: NotFound: The specified key does not exist")}
		}
		return runResult{code: 1}
	}
	defer func() { run = originalRun }()

	var output []string
	originalWriteLine := writeLine
	writeLine = func(line string) {
		output = append(output, line)
	}
	defer func() { writeLine = originalWriteLine }()

	handleTransferRetrieve(key, targetFile)

	foundFailure := false
	for _, line := range output {
		if strings.HasPrefix(line, "TRANSFER-FAILURE RETRIEVE "+key) && strings.Contains(line, "NotFound") {
			foundFailure = true
			break
		}
	}
	if !foundFailure {
		t.Errorf("expected TRANSFER-FAILURE with error, got: %v", output)
	}
}

// TestHandleTransferRetrieveCreatesDirectories tests automatic directory creation
func TestHandleTransferRetrieveCreatesDirectories(t *testing.T) {
	resetState()
	config["s3url"] = "s3://bucket/prefix"
	key := "SHA256E-s100--test.txt"

	tmpdir, _ := os.MkdirTemp("", "retrieve-")
	defer os.RemoveAll(tmpdir)

	targetFile := filepath.Join(tmpdir, "a", "b", "c", "file.txt")

	originalRun := run
	run = func(args ...string) runResult {
		if len(args) >= 2 && args[1] == "cp" {
			parentDir := filepath.Dir(targetFile)
			if _, err := os.Stat(parentDir); os.IsNotExist(err) {
				t.Errorf("expected parent directory %s to be created before cp", parentDir)
			}
			os.WriteFile(targetFile, []byte("content"), 0644)
			return runResult{code: 0}
		}
		return runResult{code: 1}
	}
	defer func() { run = originalRun }()

	var output []string
	originalWriteLine := writeLine
	writeLine = func(line string) {
		output = append(output, line)
	}
	defer func() { writeLine = originalWriteLine }()

	handleTransferRetrieve(key, targetFile)

	if _, err := os.Stat(targetFile); os.IsNotExist(err) {
		t.Errorf("expected file and parent directories to be created")
	}
}

// TestHandleRemoveSuccess tests successful REMOVE operation
func TestHandleRemoveSuccess(t *testing.T) {
	resetState()
	config["s3url"] = "s3://bucket/prefix"
	key := "SHA256E-s100--delete.txt"

	var capturedPath string
	originalRun := run
	run = func(args ...string) runResult {
		if len(args) >= 3 && args[1] == "rm" {
			capturedPath = args[2]
			return runResult{code: 0}
		}
		return runResult{code: 1}
	}
	defer func() { run = originalRun }()

	var output []string
	originalWriteLine := writeLine
	writeLine = func(line string) {
		output = append(output, line)
	}
	defer func() { writeLine = originalWriteLine }()

	handleRemove(key)

	expectedResponse := "REMOVE-SUCCESS " + key
	found := false
	for _, line := range output {
		if line == expectedResponse {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected %q, got: %v", expectedResponse, output)
	}

	expectedPath := "s3://bucket/prefix/" + key
	if capturedPath != expectedPath {
		t.Errorf("expected path %q, got %q", expectedPath, capturedPath)
	}
}

// TestHandleRemoveS3Failure tests REMOVE with S3 failure
func TestHandleRemoveS3Failure(t *testing.T) {
	resetState()
	config["s3url"] = "s3://bucket/prefix"
	key := "SHA256E-s100--protected.txt"

	originalRun := run
	run = func(args ...string) runResult {
		if len(args) >= 2 && args[1] == "rm" {
			return runResult{code: 1, stderr: []byte("ERROR: AccessDenied: Cannot delete object")}
		}
		return runResult{code: 1}
	}
	defer func() { run = originalRun }()

	var output []string
	originalWriteLine := writeLine
	writeLine = func(line string) {
		output = append(output, line)
	}
	defer func() { writeLine = originalWriteLine }()

	handleRemove(key)

	foundFailure := false
	for _, line := range output {
		if strings.HasPrefix(line, "REMOVE-FAILURE "+key) && strings.Contains(line, "AccessDenied") {
			foundFailure = true
			break
		}
	}
	if !foundFailure {
		t.Errorf("expected REMOVE-FAILURE with error, got: %v", output)
	}
}

// ========================================
// Group C: Export Operations
// ========================================

// TestHandleExportSetName tests EXPORT command setting export name
func TestHandleExportSetName(t *testing.T) {
	resetState()
	os.Setenv("GIT_ANNEX_EXPORT_REF", "refs/heads/main")
	defer os.Unsetenv("GIT_ANNEX_EXPORT_REF")

	filename := "documents/report.pdf"
	handleExport(filename)

	if exportName != filename {
		t.Errorf("expected exportName=%q, got %q", filename, exportName)
	}

	if getExportRef() != "refs/heads/main" {
		t.Errorf("expected ref to be loaded")
	}
}

// TestHandleTransferExportStoreSuccess tests successful export STORE
func TestHandleTransferExportStoreSuccess(t *testing.T) {
	resetState()
	config["s3url"] = "s3://bucket/prefix"
	os.Setenv("GIT_ANNEX_EXPORT_REF", "refs/tags/v1.0")
	defer os.Unsetenv("GIT_ANNEX_EXPORT_REF")

	exportName = "data/results.csv"
	key := "SHA256E-s500--results.csv"

	tmpfile, _ := os.CreateTemp("", "export-*.csv")
	defer os.Remove(tmpfile.Name())
	tmpfile.WriteString("col1,col2\nval1,val2")
	tmpfile.Close()

	var capturedDest string
	originalRun := run
	run = func(args ...string) runResult {
		if len(args) >= 4 && args[1] == "cp" {
			capturedDest = args[3]
			return runResult{code: 0}
		}
		return runResult{code: 1}
	}
	defer func() { run = originalRun }()

	var output []string
	originalWriteLine := writeLine
	writeLine = func(line string) {
		output = append(output, line)
	}
	defer func() { writeLine = originalWriteLine }()

	handleTransferExport("STORE", key, tmpfile.Name())

	expected := "TRANSFER-SUCCESS STORE " + key
	found := false
	for _, line := range output {
		if strings.HasPrefix(line, expected) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected %q, got: %v", expected, output)
	}

	expectedDest := "s3://bucket/prefix/refs/tags/v1.0/data/results.csv"
	if capturedDest != expectedDest {
		t.Errorf("expected dest with ref %q, got %q", expectedDest, capturedDest)
	}
}

// TestHandleTransferExportRetrieveSuccess tests successful export RETRIEVE
func TestHandleTransferExportRetrieveSuccess(t *testing.T) {
	resetState()
	config["s3url"] = "s3://bucket/prefix"
	os.Setenv("GIT_ANNEX_EXPORT_REF", "refs/heads/main")
	defer os.Unsetenv("GIT_ANNEX_EXPORT_REF")

	exportName = "data/file.txt"
	key := "SHA256E-s100--file.txt"

	tmpdir, _ := os.MkdirTemp("", "export-retrieve-")
	defer os.RemoveAll(tmpdir)

	targetFile := filepath.Join(tmpdir, "retrieved.txt")
	expectedContent := "exported file content"

	originalRun := run
	run = func(args ...string) runResult {
		if len(args) >= 4 && args[1] == "cp" {
			os.WriteFile(targetFile, []byte(expectedContent), 0644)
			return runResult{code: 0}
		}
		return runResult{code: 1}
	}
	defer func() { run = originalRun }()

	var output []string
	originalWriteLine := writeLine
	writeLine = func(line string) {
		output = append(output, line)
	}
	defer func() { writeLine = originalWriteLine }()

	handleTransferExport("RETRIEVE", key, targetFile)

	expected := "TRANSFER-SUCCESS RETRIEVE " + key
	found := false
	for _, line := range output {
		if strings.HasPrefix(line, expected) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected %q, got: %v", expected, output)
	}

	if _, err := os.Stat(targetFile); os.IsNotExist(err) {
		t.Errorf("expected file to be created at %s", targetFile)
	}
}

// TestHandleTransferExportMissingName tests export transfer without export name
func TestHandleTransferExportMissingName(t *testing.T) {
	resetState()
	config["s3url"] = "s3://bucket/prefix"

	key := "SHA256E-s100--file.txt"
	tmpfile := "/tmp/test.txt"

	var output []string
	originalWriteLine := writeLine
	writeLine = func(line string) {
		output = append(output, line)
	}
	defer func() { writeLine = originalWriteLine }()

	handleTransferExport("STORE", key, tmpfile)

	foundFailure := false
	for _, line := range output {
		if strings.HasPrefix(line, "TRANSFER-FAILURE STORE "+key) {
			foundFailure = true
			break
		}
	}
	if !foundFailure {
		t.Errorf("expected TRANSFER-FAILURE for missing export name, got: %v", output)
	}
}

// TestHandleCheckPresentExportSuccess tests successful export presence check
func TestHandleCheckPresentExportSuccess(t *testing.T) {
	resetState()
	config["s3url"] = "s3://bucket/prefix"
	os.Setenv("GIT_ANNEX_EXPORT_REF", "refs/heads/main")
	defer os.Unsetenv("GIT_ANNEX_EXPORT_REF")

	exportName = "data/report.pdf"
	key := "SHA256E-s2000--report.pdf"

	originalRun := run
	run = func(args ...string) runResult {
		if len(args) >= 2 && args[1] == "ls" {
			return runResult{code: 0, stdout: []byte("2024-01-01 00:00:00   2000 report.pdf")}
		}
		return runResult{code: 1}
	}
	defer func() { run = originalRun }()

	var output []string
	originalWriteLine := writeLine
	writeLine = func(line string) {
		output = append(output, line)
	}
	defer func() { writeLine = originalWriteLine }()

	handleCheckPresentExport(key)

	expected := "CHECKPRESENT-SUCCESS " + key
	found := false
	for _, line := range output {
		if strings.HasPrefix(line, expected) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected %q, got: %v", expected, output)
	}
}

// TestHandleCheckPresentExportMissing tests export presence check for missing file
func TestHandleCheckPresentExportMissing(t *testing.T) {
	resetState()
	config["s3url"] = "s3://bucket/prefix"
	os.Setenv("GIT_ANNEX_EXPORT_REF", "refs/heads/main")
	defer os.Unsetenv("GIT_ANNEX_EXPORT_REF")

	exportName = "data/missing.txt"
	key := "SHA256E-s100--missing.txt"

	originalRun := run
	run = func(args ...string) runResult {
		if len(args) >= 2 && args[1] == "ls" {
			return runResult{code: 1, stderr: []byte("ERROR: not found")}
		}
		return runResult{code: 1}
	}
	defer func() { run = originalRun }()

	var output []string
	originalWriteLine := writeLine
	writeLine = func(line string) {
		output = append(output, line)
	}
	defer func() { writeLine = originalWriteLine }()

	handleCheckPresentExport(key)

	expected := "CHECKPRESENT-FAILURE " + key
	found := false
	for _, line := range output {
		if strings.HasPrefix(line, expected) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected %q, got: %v", expected, output)
	}
}

// TestHandleRemoveExportSuccess tests successful export removal
func TestHandleRemoveExportSuccess(t *testing.T) {
	resetState()
	config["s3url"] = "s3://bucket/prefix"
	os.Setenv("GIT_ANNEX_EXPORT_REF", "refs/tags/v2.0")
	defer os.Unsetenv("GIT_ANNEX_EXPORT_REF")

	exportName = "old/deprecated.txt"
	key := "SHA256E-s50--deprecated.txt"

	var capturedPath string
	originalRun := run
	run = func(args ...string) runResult {
		if len(args) >= 3 && args[1] == "rm" {
			capturedPath = args[2]
			return runResult{code: 0}
		}
		return runResult{code: 1}
	}
	defer func() { run = originalRun }()

	var output []string
	originalWriteLine := writeLine
	writeLine = func(line string) {
		output = append(output, line)
	}
	defer func() { writeLine = originalWriteLine }()

	handleRemoveExport(key)

	expected := "REMOVE-SUCCESS " + key
	found := false
	for _, line := range output {
		if strings.HasPrefix(line, expected) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected %q, got: %v", expected, output)
	}

	expectedPath := "s3://bucket/prefix/refs/tags/v2.0/old/deprecated.txt"
	if capturedPath != expectedPath {
		t.Errorf("expected path with ref %q, got %q", expectedPath, capturedPath)
	}
}

// TestHandleRemoveExportFailure tests export removal with S3 error
func TestHandleRemoveExportFailure(t *testing.T) {
	resetState()
	config["s3url"] = "s3://bucket/prefix"
	os.Setenv("GIT_ANNEX_EXPORT_REF", "refs/heads/main")
	defer os.Unsetenv("GIT_ANNEX_EXPORT_REF")

	exportName = "protected/file.txt"
	key := "SHA256E-s100--file.txt"

	originalRun := run
	run = func(args ...string) runResult {
		if len(args) >= 2 && args[1] == "rm" {
			return runResult{code: 1, stderr: []byte("ERROR: AccessDenied: Delete not permitted")}
		}
		return runResult{code: 1}
	}
	defer func() { run = originalRun }()

	var output []string
	originalWriteLine := writeLine
	writeLine = func(line string) {
		output = append(output, line)
	}
	defer func() { writeLine = originalWriteLine }()

	handleRemoveExport(key)

	foundFailure := false
	for _, line := range output {
		if strings.HasPrefix(line, "REMOVE-FAILURE "+key) && strings.Contains(line, "AccessDenied") {
			foundFailure = true
			break
		}
	}
	if !foundFailure {
		t.Errorf("expected REMOVE-FAILURE with error, got: %v", output)
	}
}

// ========================================
// Group D: Export Store (Legacy)
// ========================================

// TestHandleExportStoreSuccess tests legacy EXPORTSTORE success
func TestHandleExportStoreSuccess(t *testing.T) {
	resetState()
	config["s3url"] = "s3://bucket/prefix"
	os.Setenv("GIT_ANNEX_EXPORT_REF", "refs/heads/main")
	defer os.Unsetenv("GIT_ANNEX_EXPORT_REF")

	exportName = "legacy/file.dat"
	key := "SHA256E-s300--file.dat"

	tmpfile, _ := os.CreateTemp("", "exportstore-*.dat")
	defer os.Remove(tmpfile.Name())
	tmpfile.WriteString("legacy export data")
	tmpfile.Close()

	var capturedDest string
	originalRun := run
	run = func(args ...string) runResult {
		if len(args) >= 4 && args[1] == "cp" {
			capturedDest = args[3]
			return runResult{code: 0}
		}
		return runResult{code: 1}
	}
	defer func() { run = originalRun }()

	var output []string
	originalWriteLine := writeLine
	writeLine = func(line string) {
		output = append(output, line)
	}
	defer func() { writeLine = originalWriteLine }()

	handleExportStore(key, tmpfile.Name())

	expected := "EXPORTSTORE-SUCCESS " + key
	found := false
	for _, line := range output {
		if strings.HasPrefix(line, expected) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected %q, got: %v", expected, output)
	}

	expectedDest := "s3://bucket/prefix/SHA256E-s300--file.dat"
	if capturedDest != expectedDest {
		t.Errorf("expected dest %q, got %q", expectedDest, capturedDest)
	}
}

// TestHandleExportStoreFileNotFound tests legacy EXPORTSTORE with missing file
func TestHandleExportStoreFileNotFound(t *testing.T) {
	resetState()
	config["s3url"] = "s3://bucket/prefix"
	os.Setenv("GIT_ANNEX_EXPORT_REF", "refs/heads/main")
	defer os.Unsetenv("GIT_ANNEX_EXPORT_REF")

	exportName = "missing/file.txt"
	key := "SHA256E-s100--missing.txt"
	nonExistentFile := "/tmp/nonexistent-export-file.txt"

	var output []string
	originalWriteLine := writeLine
	writeLine = func(line string) {
		output = append(output, line)
	}
	defer func() { writeLine = originalWriteLine }()

	handleExportStore(key, nonExistentFile)

	foundFailure := false
	for _, line := range output {
		if strings.HasPrefix(line, "EXPORTSTORE-FAILURE "+key) {
			foundFailure = true
			break
		}
	}
	if !foundFailure {
		t.Errorf("expected STORE-FAILURE for missing file, got: %v", output)
	}
}

// ========================================
// Group E: Import Operations
// ========================================

// TestHandleListImportableContentsMultipleFiles tests listing with multiple objects
func TestHandleListImportableContentsMultipleFiles(t *testing.T) {
	resetState()
	config["s3url"] = "s3://bucket/prefix"

	originalRun := run
	run = func(args ...string) runResult {
		if len(args) >= 3 && args[1] == "ls" && args[2] == "--json" {
			jsonOutput := `{"key":"s3://bucket/prefix/file1.txt","type":"file","size":100,"etag":"abc123","last_modified":"2024-01-01T12:00:00Z","storage_class":"STANDARD"}
{"key":"s3://bucket/prefix/data/file2.csv","type":"file","size":500,"etag":"def456","last_modified":"2024-01-02T13:00:00Z","storage_class":"STANDARD"}
{"key":"s3://bucket/prefix/images/photo.jpg","type":"file","size":2048,"etag":"ghi789","last_modified":"2024-01-03T14:00:00Z","storage_class":"STANDARD"}`
			return runResult{code: 0, stdout: []byte(jsonOutput)}
		}
		return runResult{code: 1}
	}
	defer func() { run = originalRun }()

	var output []string
	originalWriteLine := writeLine
	writeLine = func(line string) {
		output = append(output, line)
	}
	defer func() { writeLine = originalWriteLine }()

	handleListImportableContents()

	foundFile1, foundFile2, foundFile3, foundEnd := false, false, false, false

	for _, line := range output {
		if strings.Contains(line, "file1.txt") && strings.Contains(line, "CONTENT") {
			foundFile1 = true
		}
		if strings.Contains(line, "file2.csv") && strings.Contains(line, "CONTENT") {
			foundFile2 = true
		}
		if strings.Contains(line, "photo.jpg") && strings.Contains(line, "CONTENT") {
			foundFile3 = true
		}
		if line == "END" {
			foundEnd = true
		}
	}

	if !foundFile1 {
		t.Errorf("expected CONTENT for file1.txt in output: %v", output)
	}
	if !foundFile2 {
		t.Errorf("expected CONTENT for data/file2.csv in output: %v", output)
	}
	if !foundFile3 {
		t.Errorf("expected CONTENT for images/photo.jpg in output: %v", output)
	}
	if !foundEnd {
		t.Errorf("expected END in output: %v", output)
	}
}

// TestHandleListImportableContentsNestedPaths tests import with nested directory structure
func TestHandleListImportableContentsNestedPaths(t *testing.T) {
	resetState()
	config["s3url"] = "s3://bucket/myprefix"

	originalRun := run
	run = func(args ...string) runResult {
		if len(args) >= 3 && args[1] == "ls" && args[2] == "--json" {
			jsonOutput := `{"key":"s3://bucket/myprefix/docs/reports/2024/jan.pdf","type":"file","size":1024,"etag":"report123","last_modified":"2024-01-15T10:00:00Z","storage_class":"STANDARD"}
{"key":"s3://bucket/myprefix/code/src/main.go","type":"file","size":256,"etag":"code456","last_modified":"2024-01-16T11:00:00Z","storage_class":"STANDARD"}`
			return runResult{code: 0, stdout: []byte(jsonOutput)}
		}
		return runResult{code: 1}
	}
	defer func() { run = originalRun }()

	var output []string
	originalWriteLine := writeLine
	writeLine = func(line string) {
		output = append(output, line)
	}
	defer func() { writeLine = originalWriteLine }()

	handleListImportableContents()

	foundReport, foundCode := false, false

	for _, line := range output {
		if strings.Contains(line, "jan.pdf") && strings.Contains(line, "CONTENT") {
			foundReport = true
		}
		if strings.Contains(line, "main.go") && strings.Contains(line, "CONTENT") {
			foundCode = true
		}
	}

	if !foundReport {
		t.Errorf("expected CONTENT for docs/reports/2024/jan.pdf in output: %v", output)
	}
	if !foundCode {
		t.Errorf("expected CONTENT for code/src/main.go in output: %v", output)
	}
}

// ========================================
// Group F: Conditional Operations
// ========================================

// TestHandleRetrieveExportExpectedSuccess tests conditional retrieve when content matches
func TestHandleRetrieveExportExpectedSuccess(t *testing.T) {
	resetState()
	config["s3url"] = "s3://bucket/prefix"
	os.Setenv("GIT_ANNEX_EXPORT_REF", "refs/heads/main")
	defer os.Unsetenv("GIT_ANNEX_EXPORT_REF")

	exportName = "data/result.txt"
	currentLocation = "data/result.txt"
	expectedContent["data/result.txt"] = "etag:expected123"

	tmpdir, _ := os.MkdirTemp("", "retrieve-expected-")
	defer os.RemoveAll(tmpdir)
	targetFile := filepath.Join(tmpdir, "result.txt")

	originalRun := run
	run = func(args ...string) runResult {
		if len(args) >= 4 && args[1] == "ls" && args[2] == "--json" {
			jsonOutput := `{"key":"s3://bucket/prefix/refs/heads/main/data/result.txt","type":"file","size":200,"etag":"expected123","last_modified":"2024-01-01T00:00:00Z","storage_class":"STANDARD"}`
			return runResult{code: 0, stdout: []byte(jsonOutput)}
		}
		if len(args) >= 4 && args[1] == "cp" {
			os.WriteFile(targetFile, []byte("file content"), 0644)
			return runResult{code: 0}
		}
		return runResult{code: 1}
	}
	defer func() { run = originalRun }()

	var output []string
	originalWriteLine := writeLine
	writeLine = func(line string) {
		output = append(output, line)
	}
	defer func() { writeLine = originalWriteLine }()

	handleRetrieveExportExpected(targetFile)

	expected := "RETRIEVE-SUCCESS"
	found := false
	for _, line := range output {
		if strings.Contains(line, expected) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected %q, got: %v", expected, output)
	}
}

// TestHandleRetrieveExportExpectedContentMismatch tests conditional retrieve with wrong content
func TestHandleRetrieveExportExpectedContentMismatch(t *testing.T) {
	resetState()
	config["s3url"] = "s3://bucket/prefix"
	os.Setenv("GIT_ANNEX_EXPORT_REF", "refs/heads/main")
	defer os.Unsetenv("GIT_ANNEX_EXPORT_REF")

	exportName = "data/changed.txt"
	currentLocation = "data/changed.txt"
	expectedContent["data/changed.txt"] = "etag:expected999"

	targetFile := "/tmp/changed.txt"

	originalRun := run
	run = func(args ...string) runResult {
		if len(args) >= 4 && args[1] == "ls" && args[2] == "--json" {
			jsonOutput := `{"key":"s3://bucket/prefix/refs/heads/main/data/changed.txt","type":"file","size":100,"etag":"actual888","last_modified":"2024-01-01T00:00:00Z","storage_class":"STANDARD"}`
			return runResult{code: 0, stdout: []byte(jsonOutput)}
		}
		return runResult{code: 1}
	}
	defer func() { run = originalRun }()

	var output []string
	originalWriteLine := writeLine
	writeLine = func(line string) {
		output = append(output, line)
	}
	defer func() { writeLine = originalWriteLine }()

	handleRetrieveExportExpected(targetFile)

	foundFailure := false
	for _, line := range output {
		if strings.Contains(line, "RETRIEVE-FAILURE") {
			foundFailure = true
			break
		}
	}
	if !foundFailure {
		t.Errorf("expected RETRIEVE-FAILURE for content mismatch, got: %v", output)
	}
}

// TestHandleRetrieveExportExpectedMissingLocation tests conditional retrieve without location
func TestHandleRetrieveExportExpectedMissingLocation(t *testing.T) {
	resetState()
	config["s3url"] = "s3://bucket/prefix"

	targetFile := "/tmp/file.txt"

	var output []string
	originalWriteLine := writeLine
	writeLine = func(line string) {
		output = append(output, line)
	}
	defer func() { writeLine = originalWriteLine }()

	handleRetrieveExportExpected(targetFile)

	foundFailure := false
	for _, line := range output {
		if strings.Contains(line, "RETRIEVE-FAILURE") {
			foundFailure = true
			break
		}
	}
	if !foundFailure {
		t.Errorf("expected RETRIEVE-FAILURE for missing location, got: %v", output)
	}
}

// TestHandleStoreExportExpectedSuccess tests conditional store when content matches
func TestHandleStoreExportExpectedSuccess(t *testing.T) {
	resetState()
	config["s3url"] = "s3://bucket/prefix"
	os.Setenv("GIT_ANNEX_EXPORT_REF", "refs/heads/main")
	defer os.Unsetenv("GIT_ANNEX_EXPORT_REF")

	exportName = "data/update.txt"
	currentLocation = "data/update.txt"
	expectedContent["data/update.txt"] = "etag:current456"

	key := "SHA256E-s150--update.txt"

	tmpfile, _ := os.CreateTemp("", "store-expected-*.txt")
	defer os.Remove(tmpfile.Name())
	tmpfile.WriteString("updated content")
	tmpfile.Close()

	originalRun := run
	run = func(args ...string) runResult {
		if len(args) >= 4 && args[1] == "ls" && args[2] == "--json" {
			jsonOutput := `{"key":"s3://bucket/prefix/refs/heads/main/data/update.txt","type":"file","size":150,"etag":"current456","last_modified":"2024-01-01T00:00:00Z","storage_class":"STANDARD"}`
			return runResult{code: 0, stdout: []byte(jsonOutput)}
		}
		if len(args) >= 4 && args[1] == "cp" {
			return runResult{code: 0}
		}
		return runResult{code: 1}
	}
	defer func() { run = originalRun }()

	var output []string
	originalWriteLine := writeLine
	writeLine = func(line string) {
		output = append(output, line)
	}
	defer func() { writeLine = originalWriteLine }()

	handleStoreExportExpected(key, tmpfile.Name())

	expected := "STORE-SUCCESS " + key
	found := false
	for _, line := range output {
		if strings.HasPrefix(line, expected) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected %q, got: %v", expected, output)
	}
}

// TestHandleStoreExportExpectedContentMismatch tests conditional store with wrong content
func TestHandleStoreExportExpectedContentMismatch(t *testing.T) {
	resetState()
	config["s3url"] = "s3://bucket/prefix"
	os.Setenv("GIT_ANNEX_EXPORT_REF", "refs/heads/main")
	defer os.Unsetenv("GIT_ANNEX_EXPORT_REF")

	exportName = "data/conflict.txt"
	currentLocation = "data/conflict.txt"
	expectedContent["data/conflict.txt"] = "etag:expected111"

	key := "SHA256E-s100--conflict.txt"

	tmpfile, _ := os.CreateTemp("", "store-mismatch-*.txt")
	defer os.Remove(tmpfile.Name())
	tmpfile.WriteString("new content")
	tmpfile.Close()

	originalRun := run
	run = func(args ...string) runResult {
		if len(args) >= 4 && args[1] == "ls" && args[2] == "--json" {
			jsonOutput := `{"key":"s3://bucket/prefix/refs/heads/main/data/conflict.txt","type":"file","size":100,"etag":"actual222","last_modified":"2024-01-01T00:00:00Z","storage_class":"STANDARD"}`
			return runResult{code: 0, stdout: []byte(jsonOutput)}
		}
		return runResult{code: 1}
	}
	defer func() { run = originalRun }()

	var output []string
	originalWriteLine := writeLine
	writeLine = func(line string) {
		output = append(output, line)
	}
	defer func() { writeLine = originalWriteLine }()

	handleStoreExportExpected(key, tmpfile.Name())

	foundFailure := false
	for _, line := range output {
		if strings.HasPrefix(line, "STORE-FAILURE "+key) {
			foundFailure = true
			break
		}
	}
	if !foundFailure {
		t.Errorf("expected STORE-FAILURE for content mismatch, got: %v", output)
	}
}

// TestHandleStoreExportExpectedNothingExpected tests conditional store with NOTHINGEXPECTED
func TestHandleStoreExportExpectedNothingExpected(t *testing.T) {
	resetState()
	config["s3url"] = "s3://bucket/prefix"
	os.Setenv("GIT_ANNEX_EXPORT_REF", "refs/heads/main")
	defer os.Unsetenv("GIT_ANNEX_EXPORT_REF")

	exportName = "data/new.txt"
	currentLocation = "data/new.txt"
	expectedContent["data/new.txt"] = ""

	key := "SHA256E-s50--new.txt"

	tmpfile, _ := os.CreateTemp("", "store-nothing-*.txt")
	defer os.Remove(tmpfile.Name())
	tmpfile.WriteString("initial content")
	tmpfile.Close()

	originalRun := run
	run = func(args ...string) runResult {
		if len(args) >= 4 && args[1] == "ls" && args[2] == "--json" {
			return runResult{code: 1, stderr: []byte("not found")}
		}
		if len(args) >= 4 && args[1] == "cp" {
			return runResult{code: 0}
		}
		return runResult{code: 1}
	}
	defer func() { run = originalRun }()

	var output []string
	originalWriteLine := writeLine
	writeLine = func(line string) {
		output = append(output, line)
	}
	defer func() { writeLine = originalWriteLine }()

	handleStoreExportExpected(key, tmpfile.Name())

	expected := "STORE-SUCCESS " + key
	found := false
	for _, line := range output {
		if strings.HasPrefix(line, expected) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected %q, got: %v", expected, output)
	}
}

// TestHandleCheckPresentExportExpectedSuccess tests conditional check with matching content
func TestHandleCheckPresentExportExpectedSuccess(t *testing.T) {
	resetState()
	config["s3url"] = "s3://bucket/prefix"
	os.Setenv("GIT_ANNEX_EXPORT_REF", "refs/heads/main")
	defer os.Unsetenv("GIT_ANNEX_EXPORT_REF")

	exportName = "data/verify.txt"
	currentLocation = "data/verify.txt"
	expectedContent["data/verify.txt"] = "etag:check789"

	key := "SHA256E-s300--verify.txt"

	originalRun := run
	run = func(args ...string) runResult {
		if len(args) >= 4 && args[1] == "ls" && args[2] == "--json" {
			jsonOutput := `{"key":"s3://bucket/prefix/refs/heads/main/data/verify.txt","type":"file","size":300,"etag":"check789","last_modified":"2024-01-01T00:00:00Z","storage_class":"STANDARD"}`
			return runResult{code: 0, stdout: []byte(jsonOutput)}
		}
		return runResult{code: 1}
	}
	defer func() { run = originalRun }()

	var output []string
	originalWriteLine := writeLine
	writeLine = func(line string) {
		output = append(output, line)
	}
	defer func() { writeLine = originalWriteLine }()

	handleCheckPresentExportExpected(key)

	expected := "CHECKPRESENT-SUCCESS " + key
	found := false
	for _, line := range output {
		if strings.HasPrefix(line, expected) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected %q, got: %v", expected, output)
	}
}

// TestHandleCheckPresentExportExpectedFailure tests conditional check with mismatched content
func TestHandleCheckPresentExportExpectedFailure(t *testing.T) {
	resetState()
	config["s3url"] = "s3://bucket/prefix"
	os.Setenv("GIT_ANNEX_EXPORT_REF", "refs/heads/main")
	defer os.Unsetenv("GIT_ANNEX_EXPORT_REF")

	exportName = "data/wrong.txt"
	currentLocation = "data/wrong.txt"
	expectedContent["data/wrong.txt"] = "etag:expected555"

	key := "SHA256E-s100--wrong.txt"

	originalRun := run
	run = func(args ...string) runResult {
		if len(args) >= 4 && args[1] == "ls" && args[2] == "--json" {
			jsonOutput := `{"key":"s3://bucket/prefix/refs/heads/main/data/wrong.txt","type":"file","size":100,"etag":"actual666","last_modified":"2024-01-01T00:00:00Z","storage_class":"STANDARD"}`
			return runResult{code: 0, stdout: []byte(jsonOutput)}
		}
		return runResult{code: 1}
	}
	defer func() { run = originalRun }()

	var output []string
	originalWriteLine := writeLine
	writeLine = func(line string) {
		output = append(output, line)
	}
	defer func() { writeLine = originalWriteLine }()

	handleCheckPresentExportExpected(key)

	expected := "CHECKPRESENT-FAILURE " + key
	found := false
	for _, line := range output {
		if strings.HasPrefix(line, expected) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected %q, got: %v", expected, output)
	}
}

// TestHandleRemoveExportExpectedSuccess tests conditional remove with matching content
func TestHandleRemoveExportExpectedSuccess(t *testing.T) {
	resetState()
	config["s3url"] = "s3://bucket/prefix"
	os.Setenv("GIT_ANNEX_EXPORT_REF", "refs/heads/main")
	defer os.Unsetenv("GIT_ANNEX_EXPORT_REF")

	exportName = "data/delete.txt"
	currentLocation = "data/delete.txt"
	expectedContent["data/delete.txt"] = "etag:todelete333"

	key := "SHA256E-s100--delete.txt"

	originalRun := run
	run = func(args ...string) runResult {
		if len(args) >= 4 && args[1] == "ls" && args[2] == "--json" {
			jsonOutput := `{"key":"s3://bucket/prefix/refs/heads/main/data/delete.txt","type":"file","size":100,"etag":"todelete333","last_modified":"2024-01-01T00:00:00Z","storage_class":"STANDARD"}`
			return runResult{code: 0, stdout: []byte(jsonOutput)}
		}
		if len(args) >= 3 && args[1] == "rm" {
			return runResult{code: 0}
		}
		return runResult{code: 1}
	}
	defer func() { run = originalRun }()

	var output []string
	originalWriteLine := writeLine
	writeLine = func(line string) {
		output = append(output, line)
	}
	defer func() { writeLine = originalWriteLine }()

	handleRemoveExportExpected(key)

	expected := "REMOVE-SUCCESS " + key
	found := false
	for _, line := range output {
		if strings.HasPrefix(line, expected) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected %q, got: %v", expected, output)
	}
}

// TestHandleRemoveExportExpectedMismatch tests conditional remove with wrong content
func TestHandleRemoveExportExpectedMismatch(t *testing.T) {
	resetState()
	config["s3url"] = "s3://bucket/prefix"
	os.Setenv("GIT_ANNEX_EXPORT_REF", "refs/heads/main")
	defer os.Unsetenv("GIT_ANNEX_EXPORT_REF")

	exportName = "data/keep.txt"
	currentLocation = "data/keep.txt"
	expectedContent["data/keep.txt"] = "etag:expected444"

	key := "SHA256E-s100--keep.txt"

	originalRun := run
	run = func(args ...string) runResult {
		if len(args) >= 4 && args[1] == "ls" && args[2] == "--json" {
			jsonOutput := `{"key":"s3://bucket/prefix/refs/heads/main/data/keep.txt","type":"file","size":100,"etag":"actual777","last_modified":"2024-01-01T00:00:00Z","storage_class":"STANDARD"}`
			return runResult{code: 0, stdout: []byte(jsonOutput)}
		}
		return runResult{code: 1}
	}
	defer func() { run = originalRun }()

	var output []string
	originalWriteLine := writeLine
	writeLine = func(line string) {
		output = append(output, line)
	}
	defer func() { writeLine = originalWriteLine }()

	handleRemoveExportExpected(key)

	foundFailure := false
	for _, line := range output {
		if strings.HasPrefix(line, "REMOVE-FAILURE "+key) {
			foundFailure = true
			break
		}
	}
	if !foundFailure {
		t.Errorf("expected REMOVE-FAILURE for content mismatch, got: %v", output)
	}
}
