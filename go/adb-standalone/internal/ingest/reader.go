// reader.go — bundle-reading seam for the ingest pipeline.
//
// BundleReader is a separate concern from storage.Adapter (which handles
// file-serving presigned URLs). The local-FS implementation is used for
// L4 parity tests and dev mode; a future S3 implementation can reuse the
// aws-sdk-go-v2 client from internal/storage/s3.go without changing ingest logic.
package ingest

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// BundleReader lists and opens metadata files from a bundle location.
type BundleReader interface {
	// ListPaths returns relative paths of all JSON metadata files in the bundle.
	ListPaths() ([]string, error)
	// Open returns a reader for the file at relPath (relative to bundle root).
	Open(relPath string) (io.ReadCloser, error)
}

// NewLocalReader returns a BundleReader backed by a local filesystem directory.
// location is canonicalized with filepath.EvalSymlinks to reject symlink-traversal
// attacks before any FS operation. Returns an error if the path cannot be resolved.
func NewLocalReader(location string) (BundleReader, error) {
	real, err := filepath.EvalSymlinks(location)
	if err != nil {
		return nil, fmt.Errorf("bundle location %q: %w", location, err)
	}
	real = filepath.Clean(real)
	return &localReader{root: real}, nil
}

type localReader struct {
	// root is the canonicalized (EvalSymlinks + Clean) bundle directory.
	root string
}

// safePath resolves relPath under root and verifies the result stays within
// root (prevents path traversal via "../" sequences or symlinks).
func (r *localReader) safePath(relPath string) (string, error) {
	abs := filepath.Join(r.root, relPath)
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", relPath, err)
	}
	real = filepath.Clean(real)
	if !strings.HasPrefix(real, r.root+string(filepath.Separator)) && real != r.root {
		return "", fmt.Errorf("path %q escapes bundle root", relPath)
	}
	return real, nil
}

func (r *localReader) ListPaths() ([]string, error) {
	var paths []string

	// bundle.json at root.
	if _, err := os.Stat(filepath.Join(r.root, "bundle.json")); err == nil {
		paths = append(paths, "bundle.json")
	}

	// commits/*.json
	commitsDir := filepath.Join(r.root, "commits")
	entries, err := os.ReadDir(commitsDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read commits dir: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			paths = append(paths, filepath.Join("commits", e.Name()))
		}
	}

	// files/**/*.json — verify each resolved path stays within root.
	filesDir := filepath.Join(r.root, "files")
	if _, err := os.Stat(filesDir); err == nil {
		if err := filepath.WalkDir(filesDir, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if !strings.HasSuffix(d.Name(), ".json") {
				return nil
			}
			real, err := filepath.EvalSymlinks(p)
			if err != nil {
				return fmt.Errorf("resolve %s: %w", p, err)
			}
			real = filepath.Clean(real)
			if !strings.HasPrefix(real, r.root+string(filepath.Separator)) {
				return fmt.Errorf("file %q escapes bundle root", p)
			}
			rel, _ := filepath.Rel(r.root, real)
			paths = append(paths, rel)
			return nil
		}); err != nil {
			return nil, err
		}
	}

	return paths, nil
}

func (r *localReader) Open(relPath string) (io.ReadCloser, error) {
	safe, err := r.safePath(relPath)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(safe)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", relPath, err)
	}
	return f, nil
}
