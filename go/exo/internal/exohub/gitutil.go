package exohub

import (
	"bytes"
	"fmt"
	"os/exec"
	stdpath "path"
	"regexp"
	"strconv"
	"strings"
)

// maxPointerFileSize bounds how large a regular-file blob can be before it
// stops being considered a possible unlocked git-annex pointer file (same
// bound and rationale as go/exo/internal/provenance.maxPointerFileSize): a
// blob this large can never be a pointer file, so its content never needs
// to be read just to classify it.
const maxPointerFileSize = 4096

// annexObjectsMarker is the path segment every git-annex symlink target and
// unlocked pointer file contains (same check as
// go/exo/internal/provenance.annexObjectsMarker).
const annexObjectsMarker = "/annex/objects/"

// annexKeyPattern recognizes a git-annex key's basename: a backend name,
// then "--", then the rest of the key.
var annexKeyPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*(-[A-Za-z0-9]+)*--\S+$`)

// annexKeyFromReference extracts a git-annex key from a symlink target or
// unlocked pointer file's content, requiring the annex objects path segment
// so a git-tracked file that merely looks like a key is not misclassified.
func annexKeyFromReference(text string) (string, bool) {
	text = strings.TrimSpace(text)
	if !strings.Contains(text, annexObjectsMarker) {
		return "", false
	}
	base := stdpath.Base(text)
	if annexKeyPattern.MatchString(base) {
		return base, true
	}
	return "", false
}

// AnnexKeyFromReference is the exported form of annexKeyFromReference, for
// commands that need to classify an existing on-disk symlink target or
// unlocked pointer file's content from outside this package (e.g. before
// deciding whether it is safe to overwrite during `exo add exohub:...`
// materialisation, exo-cli#152 review).
func AnnexKeyFromReference(text string) (string, bool) {
	return annexKeyFromReference(text)
}

// backendFromKey returns the backend name prefix of a git-annex key: the
// leading letters/digits up to (not including) the first "-", e.g.
// "SHA256E" from "SHA256E-s2239317--<hash>" or from a backend with no size
// field, "WORM--<name>". No git-annex backend name contains a "-".
func backendFromKey(key string) string {
	if i := strings.IndexByte(key, '-'); i >= 0 {
		return key[:i]
	}
	return ""
}

// gitRun runs a git plumbing command, in dir if non-empty, returning
// stdout. Resolution against a local clone or a fetched git mirror (design
// D2) needs direct git plumbing access, not any higher-level abstraction.
func gitRun(dir string, args ...string) (string, error) {
	var cmdArgs []string
	if dir != "" {
		cmdArgs = append([]string{"-C", dir}, args...)
	} else {
		cmdArgs = args
	}
	cmd := exec.Command("git", cmdArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return stdout.String(), nil
}

// gitRevParseCommit resolves rev to a commit sha in dir.
func gitRevParseCommit(dir, rev string) (string, error) {
	out, err := gitRun(dir, "rev-parse", "--verify", rev+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// gitTreeOID returns the git tree object id of path (or the whole commit's
// root tree when path is "") at commit in dir (design D5 "external pinned
// members": the identity of a whole-project or folder reference, mirroring
// go/exo/internal/provenance.Reader.resolveTreeID's ModeRef case).
func gitTreeOID(dir, commit, path string) (string, error) {
	path = strings.TrimSuffix(path, "/")
	out, err := gitRun(dir, "rev-parse", "--verify", commit+":"+path)
	if err != nil {
		return "", fmt.Errorf("resolve tree id of %q at %s: %w", path, commit, err)
	}
	oid := strings.TrimSpace(out)
	typ, err := gitRun(dir, "cat-file", "-t", oid)
	if err != nil {
		return "", fmt.Errorf("stat %q at %s: %w", path, commit, err)
	}
	if strings.TrimSpace(typ) != "tree" {
		return "", fmt.Errorf("%q at %s is not a directory (git object type %s)", path, commit, strings.TrimSpace(typ))
	}
	return oid, nil
}

// gitRemoteURL returns dir's "origin" remote URL, or "" if there is none.
func gitRemoteURL(dir string) string {
	out, err := gitRun(dir, "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// gitCatFile reads one git object's content ("<commit>:<path>" or a raw
// object id).
func gitCatFile(dir, object string) ([]byte, error) {
	out, err := gitRun(dir, "cat-file", "-p", object)
	return []byte(out), err
}

// treeEntry is one `git ls-tree` line for a single path.
type treeEntry struct {
	mode, kind, oid string
}

// gitLsTreeEntry returns the single tree entry for path at commit.
func gitLsTreeEntry(dir, commit, path string) (treeEntry, error) {
	out, err := gitRun(dir, "ls-tree", commit, "--", path)
	if err != nil {
		return treeEntry{}, err
	}
	line := strings.TrimSpace(out)
	if line == "" {
		return treeEntry{}, fmt.Errorf("%q not found at %s", path, commit)
	}
	tab := strings.IndexByte(line, '\t')
	if tab < 0 {
		return treeEntry{}, fmt.Errorf("unexpected ls-tree output: %q", line)
	}
	fields := strings.Fields(line[:tab])
	if len(fields) < 3 {
		return treeEntry{}, fmt.Errorf("unexpected ls-tree output: %q", line)
	}
	return treeEntry{mode: fields[0], kind: fields[1], oid: fields[2]}, nil
}

// gitLsTreePaths lists every blob path under prefix at commit ("" lists
// the whole tree).
func gitLsTreePaths(dir, commit, prefix string) ([]string, error) {
	args := []string{"ls-tree", "-r", "--name-only", commit}
	if prefix != "" {
		args = append(args, "--", prefix)
	}
	out, err := gitRun(dir, args...)
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(out)
	if trimmed == "" {
		return nil, nil
	}
	return strings.Split(trimmed, "\n"), nil
}

// gitResolveFile classifies path at commit (design D2 "read the tree to get
// keys/blobs"): an annexed file (symlink or unlocked pointer file naming a
// git-annex key) or a plain git-tracked blob.
func gitResolveFile(dir, commit, path string) (ResolvedFile, error) {
	entry, err := gitLsTreeEntry(dir, commit, path)
	if err != nil {
		return ResolvedFile{}, err
	}
	if entry.kind != "blob" {
		return ResolvedFile{}, fmt.Errorf("%q is not a file at %s", path, commit)
	}

	if entry.mode == "120000" {
		target, err := gitCatFile(dir, entry.oid)
		if err != nil {
			return ResolvedFile{}, fmt.Errorf("read symlink for %s: %w", path, err)
		}
		if key, ok := annexKeyFromReference(string(target)); ok {
			return ResolvedFile{Path: path, Kind: ContentAnnex, Key: key, Backend: backendFromKey(key)}, nil
		}
		return ResolvedFile{Path: path, Kind: ContentGit, GitOID: entry.oid}, nil
	}

	sizeOut, err := gitRun(dir, "cat-file", "-s", entry.oid)
	if err != nil {
		return ResolvedFile{}, fmt.Errorf("stat blob for %s: %w", path, err)
	}
	size, _ := strconv.ParseInt(strings.TrimSpace(sizeOut), 10, 64)
	if size <= maxPointerFileSize {
		content, err := gitCatFile(dir, entry.oid)
		if err != nil {
			return ResolvedFile{}, fmt.Errorf("read blob for %s: %w", path, err)
		}
		if key, ok := annexKeyFromReference(string(content)); ok {
			return ResolvedFile{Path: path, Kind: ContentAnnex, Key: key, Backend: backendFromKey(key), Size: size}, nil
		}
	}
	return ResolvedFile{Path: path, Kind: ContentGit, GitOID: entry.oid, Size: size}, nil
}

// gitResolveFiles resolves every path in paths at commit.
func gitResolveFiles(dir, commit string, paths []string) ([]ResolvedFile, error) {
	files := make([]ResolvedFile, 0, len(paths))
	for _, p := range paths {
		f, err := gitResolveFile(dir, commit, p)
		if err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, nil
}

// resolveTreeFiles resolves ref's files directly from a readable git
// repository (a working tree or a bare mirror) at commit: shared by
// ResolveLocalClone and ResolveGitRemote.
func resolveTreeFiles(dir, commit string, ref *Reference) ([]ResolvedFile, error) {
	switch {
	case ref.IsCollection():
		manifest, err := readLocalManifest(dir, commit, ref.CollectionName)
		if err != nil {
			return nil, err
		}
		paths, err := expandLocalManifest(dir, commit, manifest)
		if err != nil {
			return nil, err
		}
		if len(paths) == 0 {
			return nil, fmt.Errorf("collection %q has no members at %s", ref.CollectionName, commit)
		}
		return gitResolveFiles(dir, commit, paths)
	case ref.IsAsset():
		f, err := gitResolveFile(dir, commit, ref.Path)
		if err != nil {
			return nil, err
		}
		return []ResolvedFile{f}, nil
	default: // whole project or folder
		paths, err := gitLsTreePaths(dir, commit, ref.Path)
		if err != nil {
			return nil, err
		}
		if len(paths) == 0 {
			return nil, fmt.Errorf("no files found under %q at %s", ref.Path, commit)
		}
		return gitResolveFiles(dir, commit, paths)
	}
}
