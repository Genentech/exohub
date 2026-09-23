package standalone

import (
	"bufio"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const versitygwVersion = "v1.0.10"
const surrealVersion = "v3.2.4"

// binarySpec describes how to fetch a stack binary from GitHub releases.
type binarySpec struct {
	repo        string
	version     string // may be overridden at call time (e.g. CLI version for adb-standalone)
	assetName   func(version, goos, goarch string) (string, error)
	innerBinary string
	// checksumAsset returns the checksums file asset name, or "" if none.
	checksumAsset func(version string) string
}

var binarySpecs = map[string]binarySpec{
	"versitygw": {
		repo:    "versity/versitygw",
		version: versitygwVersion,
		assetName: func(version, goos, goarch string) (string, error) {
			assetOS := map[string]string{"linux": "Linux", "darwin": "Darwin"}[goos]
			assetArch := map[string]string{"amd64": "x86_64", "arm64": "arm64"}[goarch]
			if assetOS == "" || assetArch == "" {
				return "", fmt.Errorf("unsupported platform %s/%s — install versitygw manually", goos, goarch)
			}
			return fmt.Sprintf("versitygw_%s_%s_%s.tar.gz", version[1:], assetOS, assetArch), nil
		},
		innerBinary:   "versitygw",
		checksumAsset: func(version string) string { return "" },
	},
	"surreal": {
		repo:    "surrealdb/surrealdb",
		version: surrealVersion,
		assetName: func(version, goos, goarch string) (string, error) {
			assetOS := map[string]string{"linux": "linux", "darwin": "darwin"}[goos]
			assetArch := map[string]string{"amd64": "amd64", "arm64": "arm64"}[goarch]
			if assetOS == "" || assetArch == "" {
				return "", fmt.Errorf("unsupported platform %s/%s — install surreal manually", goos, goarch)
			}
			return fmt.Sprintf("surreal-%s.%s-%s.tgz", version, assetOS, assetArch), nil
		},
		innerBinary:   "surreal",
		checksumAsset: func(version string) string { return "" },
	},
	"adb-standalone": {
		repo: "Genentech/exohub",
		// version is set at download time to the CLI's own build version.
		assetName: func(version, goos, goarch string) (string, error) {
			assetOS := map[string]string{"linux": "linux", "darwin": "darwin"}[goos]
			assetArch := map[string]string{"amd64": "amd64", "arm64": "arm64"}[goarch]
			if assetOS == "" || assetArch == "" {
				return "", fmt.Errorf("unsupported platform %s/%s — install adb-standalone manually", goos, goarch)
			}
			return fmt.Sprintf("adb-standalone_%s_%s_%s.tar.gz", version, assetOS, assetArch), nil
		},
		innerBinary: "adb-standalone",
		checksumAsset: func(version string) string {
			return fmt.Sprintf("adb-standalone_%s_checksums.txt", version)
		},
	},
}

// isBinaryMissing reports whether name resolves neither from PATH nor from dataDir/bin/.
// If it is found, it returns the resolved path.
func isBinaryMissing(name, dataDir string) (found string, missing bool) {
	if p, err := exec.LookPath(name); err == nil {
		return p, false
	}
	localPath := filepath.Join(dataDir, "bin", name)
	if _, err := os.Stat(localPath); err == nil {
		return localPath, false
	}
	return "", true
}

// resolveBinary returns the path for name: PATH → dataDir/bin/ → already-cached.
// It does NOT download; call downloadBinary for that.
func resolveBinary(name, dataDir string) (string, error) {
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	localPath := filepath.Join(dataDir, "bin", name)
	if _, err := os.Stat(localPath); err == nil {
		return localPath, nil
	}
	return "", fmt.Errorf("%q not found in PATH or %s", name, filepath.Join(dataDir, "bin"))
}

// downloadBinary downloads name into dataDir/bin/ using its binarySpec.
// version overrides the spec's default (used for adb-standalone which must match CLI version).
func downloadBinary(name, dataDir, versionOverride string) (string, error) {
	spec, ok := binarySpecs[name]
	if !ok {
		return "", fmt.Errorf("no download spec for %q", name)
	}
	ver := spec.version
	if versionOverride != "" {
		ver = versionOverride
	}
	if ver == "" {
		return "", fmt.Errorf("cannot download %q: version unknown (pass --version or upgrade exo)", name)
	}

	goos := runtime.GOOS
	goarch := runtime.GOARCH
	assetName, err := spec.assetName(ver, goos, goarch)
	if err != nil {
		return "", err
	}

	releaseURL := fmt.Sprintf("https://github.com/%s/releases/download/v%s/%s", spec.repo, strings.TrimPrefix(ver, "v"), assetName)
	// versitygw tags don't have leading v stripped
	if name == "versitygw" || name == "surreal" {
		releaseURL = fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", spec.repo, ver, assetName)
	}

	destPath := filepath.Join(dataDir, "bin", name)

	fmt.Fprintf(os.Stderr, "downloading %s %s…\n", name, ver)

	// Verify checksum if a checksums file is provided.
	checksumAsset := spec.checksumAsset(ver)
	if checksumAsset != "" {
		checksumURL := fmt.Sprintf("https://github.com/%s/releases/download/v%s/%s", spec.repo, strings.TrimPrefix(ver, "v"), checksumAsset)
		if err := downloadVerifyAndExtract(releaseURL, checksumURL, assetName, spec.innerBinary, destPath, name); err != nil {
			return "", err
		}
	} else {
		if err := downloadAndExtract(releaseURL, spec.innerBinary, destPath, name); err != nil {
			return "", err
		}
	}

	fmt.Fprintf(os.Stderr, "%s installed to %s\n", name, destPath)
	return destPath, nil
}

// downloadVerifyAndExtract fetches the tarball and verifies its SHA-256 against a checksums file.
func downloadVerifyAndExtract(tarURL, checksumURL, assetName, binaryName, destPath, logLabel string) error {
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return fmt.Errorf("create bin dir: %w", err)
	}

	// Fetch checksums file.
	csResp, err := http.Get(checksumURL) //nolint:noctx
	if err != nil {
		return fmt.Errorf("fetch checksums for %s: %w", logLabel, err)
	}
	defer csResp.Body.Close()
	if csResp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch checksums for %s: HTTP %d from %s", logLabel, csResp.StatusCode, checksumURL)
	}
	expectedHash, err := findChecksumForAsset(csResp.Body, assetName)
	if err != nil {
		return fmt.Errorf("parse checksums for %s: %w", logLabel, err)
	}

	// Download tarball into a temp file, computing SHA-256 as we go.
	tmpFile, err := os.CreateTemp(filepath.Dir(destPath), logLabel+"-download-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	resp, err := http.Get(tarURL) //nolint:noctx
	if err != nil {
		return fmt.Errorf("download %s: %w", logLabel, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d from %s", logLabel, resp.StatusCode, tarURL)
	}

	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmpFile, h), resp.Body); err != nil {
		return fmt.Errorf("write download: %w", err)
	}
	tmpFile.Close()

	got := fmt.Sprintf("%x", h.Sum(nil))
	if got != expectedHash {
		return fmt.Errorf("%s checksum mismatch: got %s want %s", logLabel, got, expectedHash)
	}

	if err := extractFromTarGz(tmpFile.Name(), binaryName, destPath); err != nil {
		return fmt.Errorf("extract %s: %w", logLabel, err)
	}
	return os.Chmod(destPath, 0o755)
}

// findChecksumForAsset parses a SHA-256SUMS-style file and returns the hex hash for assetName.
func findChecksumForAsset(r io.Reader, assetName string) (string, error) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		// Format: "<hash>  <filename>" or "<hash> *<filename>"
		name := strings.TrimPrefix(fields[1], "*")
		if name == assetName || filepath.Base(name) == assetName {
			return fields[0], nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("no checksum entry for %q", assetName)
}

// downloadAndExtract fetches a tarball from url, extracts binaryName from it,
// writes it to destPath, and makes it executable.
func downloadAndExtract(url, binaryName, destPath, logLabel string) error {
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return fmt.Errorf("create bin dir: %w", err)
	}

	tmpFile, err := os.CreateTemp(filepath.Dir(destPath), logLabel+"-download-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	resp, err := http.Get(url) //nolint:noctx
	if err != nil {
		return fmt.Errorf("download %s: %w", logLabel, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d from %s", logLabel, resp.StatusCode, url)
	}
	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		return fmt.Errorf("write download: %w", err)
	}
	tmpFile.Close()

	if err := extractFromTarGz(tmpFile.Name(), binaryName, destPath); err != nil {
		return fmt.Errorf("extract %s: %w", logLabel, err)
	}
	return os.Chmod(destPath, 0o755)
}

// extractFromTarGz extracts a named file from a .tar.gz archive to destPath.
func extractFromTarGz(tarPath, fileName, destPath string) error {
	tmpDir, err := os.MkdirTemp("", "binary-extract-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	cmd := exec.Command("tar", "-xzf", tarPath, "-C", tmpDir)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("tar extract: %w", err)
	}

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
