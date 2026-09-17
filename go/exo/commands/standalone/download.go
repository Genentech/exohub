package standalone

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

const versitygwVersion = "v1.0.10"
const versitygwRepo = "versity/versitygw"

// findOrDownloadBinary locates a binary in PATH or in dataDir/bin/, downloading
// it from GitHub releases if not found. Returns the resolved path.
func findOrDownloadBinary(name, dataDir string) (string, error) {
	// 1. Check PATH first.
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}

	// 2. Check dataDir/bin/<name>.
	localPath := filepath.Join(dataDir, "bin", name)
	if _, err := os.Stat(localPath); err == nil {
		return localPath, nil
	}

	// 3. Download from GitHub releases.
	return downloadGitHubRelease(name, localPath)
}

// downloadGitHubRelease downloads a pre-built binary from GitHub releases.
// Only versitygw is currently supported.
func downloadGitHubRelease(name, destPath string) (string, error) {
	if name != "versitygw" {
		return "", fmt.Errorf("%q not found in PATH and no download URL known for it — install it manually", name)
	}

	goos := runtime.GOOS
	goarch := runtime.GOARCH
	// GitHub release asset naming: versitygw_<version>_<OS>_<arch>
	assetOS := map[string]string{"linux": "Linux", "darwin": "Darwin"}[goos]
	assetArch := map[string]string{"amd64": "x86_64", "arm64": "arm64"}[goarch]
	if assetOS == "" || assetArch == "" {
		return "", fmt.Errorf("unsupported platform %s/%s — install versitygw manually", goos, goarch)
	}

	assetName := fmt.Sprintf("versitygw_%s_%s_%s.tar.gz", versitygwVersion[1:], assetOS, assetArch)
	url := fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", versitygwRepo, versitygwVersion, assetName)

	fmt.Fprintf(os.Stderr, "versitygw not found — downloading %s from GitHub releases…\n", versitygwVersion)

	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return "", fmt.Errorf("create bin dir: %w", err)
	}

	tmpFile, err := os.CreateTemp(filepath.Dir(destPath), "versitygw-download-*")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	resp, err := http.Get(url) //nolint:noctx
	if err != nil {
		return "", fmt.Errorf("download versitygw: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download versitygw: HTTP %d from %s", resp.StatusCode, url)
	}
	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		return "", fmt.Errorf("write download: %w", err)
	}
	tmpFile.Close()

	// Extract the binary from the tarball.
	if err := extractFromTarGz(tmpFile.Name(), "versitygw", destPath); err != nil {
		return "", fmt.Errorf("extract versitygw: %w", err)
	}
	if err := os.Chmod(destPath, 0o755); err != nil {
		return "", fmt.Errorf("chmod versitygw: %w", err)
	}

	fmt.Fprintf(os.Stderr, "versitygw downloaded to %s\n", destPath)
	return destPath, nil
}

// extractFromTarGz extracts a named file from a .tar.gz archive to destPath.
func extractFromTarGz(tarPath, fileName, destPath string) error {
	// Use the system tar command for simplicity and cross-platform compatibility.
	tmpDir, err := os.MkdirTemp("", "versitygw-extract-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	cmd := exec.Command("tar", "-xzf", tarPath, "-C", tmpDir)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("tar extract: %w", err)
	}

	// Find the binary inside the extracted tree.
	var found string
	_ = filepath.Walk(tmpDir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && info.Name() == fileName {
			found = path
		}
		return nil
	})
	if found == "" {
		return fmt.Errorf("%q not found in archive", fileName)
	}

	src, err := os.Open(found)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer dst.Close()

	_, err = io.Copy(dst, src)
	return err
}
