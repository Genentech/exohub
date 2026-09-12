//go:build integration
// +build integration

package main

// Integration tests for git-annex-remote-drive.
//
// These tests require real Google Drive credentials and a writable Drive folder.
// They are skipped by default; run with:
//
//	GOOGLE_CLIENT_ID=<id> GOOGLE_CLIENT_SECRET=<secret> \
//	DRIVE_TEST_PATH="/My Drive/exo-integration-test" \
//	go test -tags=integration -v ./...
//
// Prerequisites:
//   - Set GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET (or use defaults)
//   - Set DRIVE_TEST_PATH to a Drive folder path that exo can create
//   - Run once interactively (TTY) to complete the OAuth2 PKCE consent flow
//   - The test folder will be cleaned up after the test run

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIntegration_StoreRetrieveDelete(t *testing.T) {
	drivePath := os.Getenv("DRIVE_TEST_PATH")
	if drivePath == "" {
		t.Skip("DRIVE_TEST_PATH not set; skipping integration test")
	}

	// Initialize remote
	config["drive_path"] = drivePath
	remoteName = "integration-test"
	tokenPath := tokenStorePath(remoteName)

	tf, err := acquireToken(tokenPath)
	if err != nil {
		t.Fatalf("acquireToken: %v", err)
	}
	svc, err := newDriveService(tf)
	if err != nil {
		t.Fatalf("newDriveService: %v", err)
	}
	drv = svc

	folderID, err := resolveRootDrivePath(drivePath)
	if err != nil {
		t.Fatalf("resolveRootDrivePath: %v", err)
	}
	rootFolderID = folderID
	t.Logf("root folder ID: %s", rootFolderID)

	// Create a temp file to upload
	tmpDir := t.TempDir()
	localPath := filepath.Join(tmpDir, "test-file.parquet")
	content := []byte("hello from integration test at " + time.Now().String())
	if err := os.WriteFile(localPath, content, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// STORE
	exportName = "integration-subdir/test-file.parquet"
	t.Run("store", func(t *testing.T) {
		parentID, fileName, err := exportParentAndFile()
		if err != nil {
			t.Fatalf("exportParentAndFile: %v", err)
		}
		if err := uploadFile(fileName, parentID, localPath); err != nil {
			t.Fatalf("uploadFile: %v", err)
		}
		t.Logf("uploaded %s", exportName)
	})

	// CHECKPRESENT
	t.Run("checkpresent", func(t *testing.T) {
		parentID, fileName, err := exportParentAndFile()
		if err != nil {
			t.Fatalf("exportParentAndFile: %v", err)
		}
		fileID, err := findFile(fileName, parentID)
		if err != nil {
			t.Fatalf("findFile: %v", err)
		}
		if fileID == "" {
			t.Fatal("file not found in Drive after upload")
		}
		t.Logf("file found: %s", fileID)
	})

	// RETRIEVE
	t.Run("retrieve", func(t *testing.T) {
		destPath := filepath.Join(tmpDir, "retrieved.parquet")
		parentID, fileName, err := exportParentAndFile()
		if err != nil {
			t.Fatalf("exportParentAndFile: %v", err)
		}
		fileID, err := findFile(fileName, parentID)
		if err != nil || fileID == "" {
			t.Fatalf("findFile: %v / fileID empty", err)
		}
		if err := downloadFile(fileID, destPath); err != nil {
			t.Fatalf("downloadFile: %v", err)
		}
		got, _ := os.ReadFile(destPath)
		if string(got) != string(content) {
			t.Errorf("content mismatch: got %q, want %q", got, content)
		}
		t.Logf("retrieved and verified content")
	})

	// REMOVE
	t.Run("remove", func(t *testing.T) {
		parentID, fileName, err := exportParentAndFile()
		if err != nil {
			t.Fatalf("exportParentAndFile: %v", err)
		}
		fileID, err := findFile(fileName, parentID)
		if err != nil || fileID == "" {
			t.Fatalf("findFile: %v / fileID empty", err)
		}
		if err := deleteFile(fileID); err != nil {
			t.Fatalf("deleteFile: %v", err)
		}
		// Verify gone
		fileID2, err := findFile(fileName, parentID)
		if err != nil {
			t.Fatalf("post-delete findFile: %v", err)
		}
		if fileID2 != "" {
			t.Errorf("file still present after delete")
		}
		t.Logf("deleted and verified removal")
	})

	// REMOVEEXPORTDIRECTORYWHENEMPTY
	t.Run("remove_dir_when_empty", func(t *testing.T) {
		handleRemoveExportDirectoryWhenEmpty("integration-subdir")
		t.Logf("directory cleanup succeeded")
	})
}
