package add

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	initcmd "github.com/Genentech/exohub/go/exo/commands/init"
	"github.com/Genentech/exohub/go/exo/internal/download"
	"github.com/Genentech/exohub/go/exo/internal/exohub"
)

// maxExistingPointerSize bounds how large an existing regular file can be
// before materializeAnnexFile stops considering it a possible unlocked
// git-annex pointer file worth reading to compare keys (same rationale as
// provenance.maxPointerFileSize): a real data file this large is never a
// pointer file, so there's no point reading it just to refuse overwriting
// it.
const maxExistingPointerSize = 4096

// materializeFile writes one resolved file at targetPath (design D3): a
// git-tracked file is copied with the same blob content (task 3.2); an
// annexed file gets a pointer for the same key, made retrievable without
// copying its content up front when possible (task 3.1: Z1 default, Z3
// copy fallback).
func materializeFile(res *exohub.Resolution, f exohub.ResolvedFile, targetPath string) error {
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return fmt.Errorf("create directory for %s: %w", targetPath, err)
	}
	if f.Kind == exohub.ContentGit {
		return materializeGitFile(res, f, targetPath)
	}
	return materializeAnnexFile(res, f, targetPath)
}

// materializeGitFile implements task 3.2: the file's blob content is read
// from the source (local clone, or a lazily fetched mirror) and written
// into the working tree, then staged exactly like a plain `git add`.
func materializeGitFile(res *exohub.Resolution, f exohub.ResolvedFile, targetPath string) error {
	content, err := exohub.ReadGitFile(res, f.Path)
	if err != nil {
		return fmt.Errorf("read %s from source: %w", f.Path, err)
	}
	if existing, readErr := os.ReadFile(targetPath); readErr == nil {
		if bytes.Equal(existing, content) {
			return nil // already materialized with identical content: no-op
		}
		return fmt.Errorf("refusing to overwrite existing file %s with different content (remove it or choose a different --to)", targetPath)
	} else if !os.IsNotExist(readErr) {
		return fmt.Errorf("check existing %s: %w", targetPath, readErr)
	}
	if err := os.WriteFile(targetPath, content, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", targetPath, err)
	}
	return execCommand("git", "add", "--", targetPath)
}

// materializeAnnexFile implements task 3.1: a pointer for the same key is
// always created (`git annex fromkey --force`), then Z1 (a read-only remote
// on the source's own annex storage) is attempted so the content stays
// retrievable without being copied; if Z1 isn't available or the content
// isn't actually reachable through it, Z3 (materializeByCopy) copies the
// content once instead.
func materializeAnnexFile(res *exohub.Resolution, f exohub.ResolvedFile, targetPath string) error {
	existingKey, exists, err := existingAnnexKey(targetPath)
	if err != nil {
		return err
	}
	if exists {
		if existingKey == f.Key {
			return nil // already materialized with the same key: no-op
		}
		return fmt.Errorf("refusing to overwrite existing file %s (remove it or choose a different --to)", targetPath)
	}

	// --force is needed here because f.Key's content is not present
	// locally yet (git-annex normally insists on that for `fromkey`), not
	// as licence to clobber a pre-existing file: the check above already
	// ruled that out.
	if err := execCommand("git", "annex", "fromkey", "--force", f.Key, targetPath); err != nil {
		return fmt.Errorf("git annex fromkey %s: %w", f.Key, err)
	}

	if remoteName, ok := ensureZ1Remote(f); ok {
		if err := exec.Command("git", "annex", "fsck", "--fast", "--from", remoteName, "--", targetPath).Run(); err == nil {
			return nil
		}
		fmt.Fprintf(os.Stderr, "note: %s is not retrievable from %s, copying its content instead\n", targetPath, remoteName)
	}

	return materializeByCopy(res, f, targetPath)
}

// existingAnnexKey reports the git-annex key already materialized at
// targetPath, if any (exo-cli#152 review: never silently overwrite a file
// that is already there, whether or not it happens to carry the same
// content/key). exists is true whenever something is already at
// targetPath; key is only meaningful when it could be identified as an
// annex pointer (a symlink or unlocked pointer file naming a key).
func existingAnnexKey(targetPath string) (key string, exists bool, err error) {
	info, err := os.Lstat(targetPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("check existing %s: %w", targetPath, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(targetPath)
		if err != nil {
			return "", true, fmt.Errorf("read existing symlink %s: %w", targetPath, err)
		}
		if k, ok := exohub.AnnexKeyFromReference(target); ok {
			return k, true, nil
		}
		return "", true, fmt.Errorf("refusing to overwrite existing file %s (not a recognized git-annex pointer; remove it or choose a different --to)", targetPath)
	}
	if info.Size() > maxExistingPointerSize {
		return "", true, fmt.Errorf("refusing to overwrite existing file %s (remove it or choose a different --to)", targetPath)
	}
	content, err := os.ReadFile(targetPath)
	if err != nil {
		return "", true, fmt.Errorf("read existing %s: %w", targetPath, err)
	}
	if k, ok := exohub.AnnexKeyFromReference(string(content)); ok {
		return k, true, nil
	}
	return "", true, fmt.Errorf("refusing to overwrite existing file %s (not a recognized git-annex pointer; remove it or choose a different --to)", targetPath)
}

// z1RemoteNamePrefix identifies the read-only annex remotes `exo add
// exohub:...` creates for Z1 materialisation (design D3), so repeated
// imports from the same source storage reuse one remote instead of
// creating one per file.
const z1RemoteNamePrefix = "exohub-import-"

// z1RemoteName derives a stable remote name for s3url, so every file
// sharing the same source storage reuses the same read-only remote.
func z1RemoteName(s3url string) string {
	sum := sha256.Sum256([]byte(s3url))
	return z1RemoteNamePrefix + hex.EncodeToString(sum[:])[:12]
}

var chunkKeySizePattern = regexp.MustCompile(`-S(\d+)-C\d+`)

// chunkSizeArg derives the chunk= value for `git annex initremote` from
// loc's chunk key (spike-zero-copy.md "Chunking must match exactly"): the
// "S<bytes>" component of a chunk key is the configured chunk boundary,
// distinct from ChunkSize (the actual per-chunk byte count, which can be
// smaller than the boundary for the last/only chunk). Falling back to
// ChunkSize when the key pattern is unavailable can register the wrong
// chunk size; per the spike, that fails safely (CHECKPRESENT-FAILURE)
// rather than corrupting data, so ensureZ1Remote's fsck probe still
// protects against it.
func chunkSizeArg(loc exohub.Location) string {
	if m := chunkKeySizePattern.FindStringSubmatch(loc.ChunkKey); m != nil {
		return m[1]
	}
	if loc.ChunkSize > 0 {
		return strconv.FormatInt(loc.ChunkSize, 10)
	}
	return ""
}

// ensureZ1Remote implements Z1 materialisation (design D3,
// spike-zero-copy.md "Effort to productise"): a read-only s5cmd external
// remote on the source's annex S3 prefix, reused across files that share
// the same s3url. It returns ok=false when f has no usable annex location
// or the remote could not be created (no s5cmd, no grants, etc.), in which
// case the caller falls back to Z3 (materializeByCopy).
func ensureZ1Remote(f exohub.ResolvedFile) (name string, ok bool) {
	for _, loc := range f.Locations {
		if loc.Type != "annex" || loc.S3URL == "" {
			continue
		}
		chunk := chunkSizeArg(loc)
		if chunk == "" {
			continue
		}
		// CreateReadOnlyAnnexRemote can return an *existing* .exohub/remotes
		// entry under a different name than the one we suggest (it is keyed
		// by s3url+chunk, not by name), so the remote actually usable for
		// fsck below is rc.Name, never the locally derived remoteName.
		rc, _, err := initcmd.CreateReadOnlyAnnexRemote(z1RemoteName(loc.S3URL), loc.S3URL, chunk)
		if err != nil {
			fmt.Fprintf(os.Stderr, "note: could not set up a read-only remote for %s: %v\n", loc.S3URL, err)
			continue
		}
		return rc.Name, true
	}
	return "", false
}

// materializeByCopy implements the Z3 fallback (design D3): the content is
// retrieved from a source clone directly if one is available (no network
// at all), otherwise downloaded through the catalog, and injected into the
// importer's own annex store under the same key via `git annex reinject`,
// which also verifies the checksum matches.
func materializeByCopy(res *exohub.Resolution, f exohub.ResolvedFile, targetPath string) error {
	if res.LocalClonePath != "" {
		out, err := exec.Command("git", "-C", res.LocalClonePath, "annex", "contentlocation", f.Key).Output()
		if err == nil {
			if local := strings.TrimSpace(string(out)); local != "" {
				// `git -C <dir> annex contentlocation` prints a path
				// relative to <dir>, not to our own working directory
				// (where `git annex reinject` below actually runs), so
				// it must be anchored to LocalClonePath first.
				if !filepath.IsAbs(local) {
					local = filepath.Join(res.LocalClonePath, local)
				}
				// `git annex reinject` moves (or deletes after copying) its
				// SRC argument, and the source clone's own annex object
				// directories are read-only, so SRC must be a disposable
				// copy, never the source clone's own object file.
				copyPath, err := copyToTempFile(local)
				if err != nil {
					return fmt.Errorf("copy %s from local clone: %w", f.Path, err)
				}
				defer os.RemoveAll(filepath.Dir(copyPath))
				return execCommand("git", "annex", "reinject", copyPath, targetPath)
			}
		}
	}

	dl := download.New(exohub.ResolveCatalogURL())
	tmpDir, err := os.MkdirTemp("", "exo-add-exohub-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	outPath, _, err := dl.DownloadFile(res.FileCatalogID(f), tmpDir, nil)
	if err != nil {
		return fmt.Errorf("no source storage available and the catalog copy failed: %w", err)
	}
	return execCommand("git", "annex", "reinject", outPath, targetPath)
}

// copyToTempFile copies src into a new temporary file (its own disposable
// directory, so the caller can remove it as a whole after `git annex
// reinject` has consumed it) and returns the copy's path.
func copyToTempFile(src string) (string, error) {
	tmpDir, err := os.MkdirTemp("", "exo-add-exohub-local-*")
	if err != nil {
		return "", err
	}
	in, err := os.Open(src)
	if err != nil {
		os.RemoveAll(tmpDir)
		return "", err
	}
	defer in.Close()
	dst := filepath.Join(tmpDir, filepath.Base(src))
	out, err := os.Create(dst)
	if err != nil {
		os.RemoveAll(tmpDir)
		return "", err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.RemoveAll(tmpDir)
		return "", err
	}
	if err := out.Close(); err != nil {
		os.RemoveAll(tmpDir)
		return "", err
	}
	return dst, nil
}
