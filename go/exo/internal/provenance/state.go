package provenance

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	initcmd "github.com/Genentech/exohub/go/exo/commands/init"
)

// Mode selects which checked state a Reader reads from (design D15
// "Checked states"):
//
//	Mode        Records              Content ids / file set   .exohub/config
//	Worktree    worktree files       index                    worktree
//	Staged      index                index                    index
//	Ref         tree of the ref      tree of the ref          tree of the ref
type Mode int

const (
	// ModeWorktree is the default `exo provenance validate` state: records
	// come from worktree files (including untracked ones), content
	// identifiers and the tracked file set come from the index.
	ModeWorktree Mode = iota
	// ModeStaged is `exo provenance validate --staged`: everything is read
	// from the index.
	ModeStaged
	// ModeRef is `exo provenance validate --ref`, `exo provenance show
	// --ref` and `exo bundle`: everything is read from a ref's tree.
	ModeRef
)

// Reader reads provenance records, resolves content identifiers, and loads
// .exohub/config from one checked state (design D15).
type Reader struct {
	mode Mode
	ref  string
	root string
}

// NewWorktreeReader returns a Reader for the default validate state: records
// from worktree files, content ids and config from the index/worktree. root
// is the repository root; git plumbing commands run with it as the working
// directory, so paths passed to Reader methods must be repo-root-relative.
func NewWorktreeReader(root string) *Reader {
	return &Reader{mode: ModeWorktree, root: root}
}

// NewStagedReader returns a Reader for `--staged`: everything from the
// index.
func NewStagedReader(root string) *Reader {
	return &Reader{mode: ModeStaged, root: root}
}

// NewRefReader returns a Reader for `--ref <ref>`: everything from ref's
// tree.
func NewRefReader(root, ref string) *Reader {
	return &Reader{mode: ModeRef, ref: ref, root: root}
}

// Mode returns the Reader's checked state.
func (r *Reader) Mode() Mode { return r.mode }

// RecordFile is one record file found by ListRecords.
type RecordFile struct {
	// Path is repo-relative, for example
	// ".exohub/provenance/20261014T101200Z-normalize-7f3a2c.jsonld".
	Path string
	Data []byte
	// Annexed reports whether the record file itself is backed by
	// git-annex (a symlink to, or an unlocked pointer file for, an annex
	// object) rather than ordinary git content, at the Reader's state.
	// Callers (for example `exo provenance validate`, design D15) can use
	// this to report an annexed record without re-querying git.
	Annexed bool
}

// ListRecords returns every ".exohub/provenance/*.jsonld" file at the
// Reader's state (design spec "Provenance record storage": other files in
// the directory, for example a README, are ignored), together with their
// contents.
//
// ModeStaged and ModeRef list and read every record in a constant number
// of git invocations regardless of how many records there are (review:
// "Reader.ListRecords -> readRecordFile runs one git ls-files/ls-tree plus
// one git cat-file per record" — on a 20,000 record repository this took
// over two minutes; see listRecordsBatch). ModeWorktree already reads
// worktree files directly with no git invocation per record.
func (r *Reader) ListRecords() ([]RecordFile, error) {
	switch r.mode {
	case ModeWorktree:
		return r.listRecordsWorktree()
	case ModeStaged, ModeRef:
		return r.listRecordsBatch()
	}
	return nil, fmt.Errorf("unknown mode %d", r.mode)
}

// listRecordsWorktree lists and reads every record file directly from the
// worktree directory (ModeWorktree): no git invocation is needed since the
// Reader's state is the worktree itself.
func (r *Reader) listRecordsWorktree() ([]RecordFile, error) {
	entries, err := os.ReadDir(filepath.Join(r.root, RecordDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", RecordDir, err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonld") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)

	out := make([]RecordFile, 0, len(names))
	for _, name := range names {
		path := RecordDir + "/" + name
		full := filepath.Join(r.root, path)
		info, err := os.Lstat(full)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(full)
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", path, err)
			}
			_, annexed := annexKeyFromSymlinkTarget(target)
			data, err := os.ReadFile(full)
			if err != nil {
				return nil, fmt.Errorf("read content linked from %s: %w", path, err)
			}
			out = append(out, RecordFile{Path: path, Data: data, Annexed: annexed})
			continue
		}
		data, err := os.ReadFile(full)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		_, annexed := annexKeyFromPointerContent(data)
		out = append(out, RecordFile{Path: path, Data: data, Annexed: annexed})
	}
	return out, nil
}

// listRecordsBatch lists and reads every record file for ModeStaged and
// ModeRef in two git invocations total: one to list every entry directly
// under RecordDir with its mode and object id (`git ls-files --stage -z`
// over the index, or `git ls-tree -z <ref>` over the ref's tree; neither is
// recursive, so a subdirectory of RecordDir — not expected to hold records,
// design spec "Provenance record storage" — is not descended into, only
// listed as one tree entry and skipped), then one `git cat-file --batch` to
// read every matching blob's content (same two-call shape as
// Reader.AllContentIDs; review: "Same pattern as AllContentIDs in !256").
func (r *Reader) listRecordsBatch() ([]RecordFile, error) {
	var out []byte
	var err error
	switch r.mode {
	case ModeStaged:
		out, err = r.gitBytes("--literal-pathspecs", "ls-files", "--stage", "-z", "--", RecordDir+"/")
	case ModeRef:
		out, err = r.gitBytes("--literal-pathspecs", "ls-tree", "-z", r.ref, "--", RecordDir+"/")
	default:
		return nil, fmt.Errorf("unknown mode %d", r.mode)
	}
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", RecordDir, err)
	}

	type entry struct {
		path, mode, oid string
	}
	var entries []entry
	for _, rec := range splitNUL(string(out)) {
		tab := strings.IndexByte(rec, '\t')
		if tab < 0 {
			return nil, fmt.Errorf("unexpected output: %q", rec)
		}
		fields := strings.Fields(rec[:tab])
		path := rec[tab+1:]
		if !strings.HasSuffix(path, ".jsonld") {
			continue
		}
		if strings.Contains(strings.TrimPrefix(path, RecordDir+"/"), "/") {
			// ls-files --stage matches every index entry under the
			// RecordDir/ pathspec, including ones nested in a
			// subdirectory; only *.jsonld files directly under RecordDir
			// are records (design spec "Provenance record storage"), same
			// restriction ModeWorktree's os.ReadDir (non-recursive) and
			// ModeRef's non-recursive ls-tree already apply.
			continue
		}
		switch r.mode {
		case ModeStaged:
			// "<mode> <oid> <stage>"
			if len(fields) < 2 {
				return nil, fmt.Errorf("unexpected ls-files --stage output: %q", rec)
			}
			entries = append(entries, entry{path: path, mode: fields[0], oid: fields[1]})
		case ModeRef:
			// "<mode> <type> <oid>"
			if len(fields) < 3 {
				return nil, fmt.Errorf("unexpected ls-tree output: %q", rec)
			}
			if fields[1] != "blob" {
				continue // a subdirectory or submodule entry, not a record file.
			}
			entries = append(entries, entry{path: path, mode: fields[0], oid: fields[2]})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })

	oids := make([]string, len(entries))
	for i, e := range entries {
		oids[i] = e.oid
	}
	contents, err := batchCatFileContents(r, oids)
	if err != nil {
		return nil, fmt.Errorf("read %s records: %w", RecordDir, err)
	}

	out2 := make([]RecordFile, 0, len(entries))
	for _, e := range entries {
		content, ok := contents[e.oid]
		if !ok {
			return nil, fmt.Errorf("missing blob %s for %s", e.oid, e.path)
		}
		if e.mode == "120000" {
			_, annexed := annexKeyFromSymlinkTarget(string(content))
			out2 = append(out2, RecordFile{Path: e.path, Data: content, Annexed: annexed})
			continue
		}
		_, annexed := annexKeyFromPointerContent(content)
		out2 = append(out2, RecordFile{Path: e.path, Data: content, Annexed: annexed})
	}
	return out2, nil
}

func splitLines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// ReadFile reads path (repo-relative) at the Reader's state: from the
// worktree for ModeWorktree, from the index for ModeStaged, and from the
// ref's tree for ModeRef.
func (r *Reader) ReadFile(path string) ([]byte, error) {
	switch r.mode {
	case ModeWorktree:
		data, err := os.ReadFile(filepath.Join(r.root, path))
		if err != nil {
			return nil, err
		}
		return data, nil
	case ModeStaged:
		return r.gitBytes("show", ":"+path)
	case ModeRef:
		return r.gitBytes("show", r.ref+":"+path)
	}
	return nil, fmt.Errorf("unknown mode %d", r.mode)
}

// LoadConfig loads .exohub/config at the Reader's state (design D15): the
// worktree for ModeWorktree, the index for ModeStaged, and the ref's tree
// for ModeRef. It returns nil, nil if no config file exists at that state.
func (r *Reader) LoadConfig() (*initcmd.ExohubConfig, error) {
	if r.mode == ModeWorktree {
		return initcmd.LoadExohubConfigAt(r.root)
	}
	data, err := r.ReadFile(".exohub/config")
	if err != nil {
		if isMissingAtRevision(err) {
			return nil, nil
		}
		return nil, err
	}
	return initcmd.LoadExohubConfigFromBytes(data)
}

// annexKeyPattern recognizes a git-annex key's basename: a backend name
// made of letters/digits, then "--", then the rest of the key (design D5).
// It matches both symlink targets and unlocked pointer file content, which
// share the same "<path>/<key>" shape. Matching this alone is not enough to
// call something an annex key (an ordinary git-tracked file can have a
// "X--Y"-shaped name); annexKeyFromSymlinkTarget and
// annexKeyFromPointerContent additionally require the annex objects path
// segment git-annex always uses.
var annexKeyPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*(-[A-Za-z0-9]+)*--\S+$`)

// annexObjectsMarker is the path segment every git-annex symlink target and
// unlocked pointer file contains, distinguishing genuine annex references
// from git-tracked content that merely looks like a key (for example a
// plain text file named "a--b", or a symlink to an unrelated target).
const annexObjectsMarker = "/annex/objects/"

// ResolveContentID resolves path (repo-relative) to its content identifier
// at the Reader's state (design D5, D13): an annex entity id for annexed
// files, a git blob id for git-tracked files, or a git tree id for a folder
// path ending in "/". Content identifiers always come from the index
// (ModeWorktree, ModeStaged) or the ref's tree (ModeRef), never the
// worktree (design D15).
//
// This resolves one path with up to two git plumbing calls, which is fine
// for single-path callers (add, show). A caller resolving many paths at
// once (for example provenance coverage) should use AllContentIDs instead,
// which makes the same classification decision (classifyContentID) in a
// constant number of calls regardless of how many paths it resolves
// (review: "coverage spawns several git processes per tracked file").
func (r *Reader) ResolveContentID(path string) (string, error) {
	if strings.HasSuffix(path, "/") {
		return r.resolveTreeID(strings.TrimSuffix(path, "/"))
	}
	mode, oid, err := r.statPath(path)
	if err != nil {
		return "", err
	}
	if mode == "040000" {
		return "", fmt.Errorf("%s is a directory; use a trailing \"/\" to reference it as a folder collection", path)
	}

	if mode == "120000" {
		target, err := r.gitBytes("cat-file", "-p", oid)
		if err != nil {
			return "", fmt.Errorf("read symlink %s for %s: %w", oid, path, err)
		}
		return classifyContentID(mode, oid, target, true), nil
	}

	// A regular file can only be an unlocked annex pointer file if it is
	// small and single-line; a large blob can never be one, so its full
	// content is never read (design D5: content ids must not require
	// streaming file data).
	sizeOut, err := r.git("cat-file", "-s", oid)
	if err != nil {
		return "", fmt.Errorf("stat blob %s for %s: %w", oid, path, err)
	}
	size, convErr := strconv.ParseInt(strings.TrimSpace(sizeOut), 10, 64)
	if convErr != nil {
		return "", fmt.Errorf("unexpected cat-file -s output for %s: %q", path, sizeOut)
	}
	if size > maxPointerFileSize {
		return classifyContentID(mode, oid, nil, false), nil
	}

	content, err := r.gitBytes("cat-file", "-p", oid)
	if err != nil {
		return "", fmt.Errorf("read blob %s for %s: %w", oid, path, err)
	}
	return classifyContentID(mode, oid, content, true), nil
}

// classifyContentID turns a git entry (mode, oid) into a content identifier
// (design D5), given its content when available: for a symlink (mode
// "120000"), content must be the link target; for a regular file, content
// may be omitted (hasContent=false) when the blob is larger than
// maxPointerFileSize, since a blob that large can never be an unlocked
// annex pointer file. ResolveContentID and AllContentIDs share this
// function so the single-path and batch resolution paths classify entries
// identically (review: "keep ResolveContentID for single paths, but have
// it share the classification code with the batch path").
func classifyContentID(mode, oid string, content []byte, hasContent bool) string {
	if mode == "120000" {
		if hasContent {
			if key, ok := annexKeyFromSymlinkTarget(string(content)); ok {
				return AnnexEntityID(key)
			}
		}
		return GitBlobEntityID(oid)
	}
	if hasContent {
		if key, ok := annexKeyFromPointerContent(content); ok {
			return AnnexEntityID(key)
		}
	}
	return GitBlobEntityID(oid)
}

// maxPointerFileSize bounds how large a regular-file blob can be before
// ResolveContentID stops considering it a possible unlocked annex pointer
// file and treats it as ordinary git-tracked content without reading it.
const maxPointerFileSize = 4096

// annexKeyFromSymlinkTarget extracts a git-annex key from a symlink's
// target (mode 120000): git-annex always links to a path containing
// "/annex/objects/", with the key as the final path component (for example
// "../.git/annex/objects/0v/fW/SHA256E-.../SHA256E-..."). A symlink to
// anything else (including one that happens to end in a "X--Y"-shaped
// name) is not an annex reference.
func annexKeyFromSymlinkTarget(target string) (string, bool) {
	return annexKeyFromReference(target)
}

// annexKeyFromPointerContent extracts a git-annex key from the content of
// an unlocked pointer file (a regular git blob git-annex writes in place
// of the real content): it is a single line starting with
// "/annex/objects/" followed by the key. A regular git-tracked file whose
// single-line content merely looks like a key (for example "a--b") is not
// a pointer file.
func annexKeyFromPointerContent(content []byte) (string, bool) {
	return annexKeyFromReference(string(content))
}

func annexKeyFromReference(text string) (string, bool) {
	text = strings.TrimSpace(text)
	if text == "" || strings.Contains(text, "\n") {
		return "", false
	}
	text = filepath.ToSlash(text)
	if !strings.Contains(text, annexObjectsMarker) {
		return "", false
	}
	base := filepath.Base(text)
	if annexKeyPattern.MatchString(base) {
		return base, true
	}
	return "", false
}

// statPath returns the git object mode ("100644", "100755", "120000" or
// "040000") and object id for path at the Reader's state. It requires an
// exact match on path: `git ls-files --stage -- <dir>` (no trailing slash)
// lists every blob under <dir>, not just an entry for <dir> itself, so a
// naive "take the first line" read silently resolves a directory path to
// one of its files. --literal-pathspecs additionally stops pathspec magic
// characters ("*", "?", "[") in path from being interpreted as a glob.
func (r *Reader) statPath(path string) (mode, oid string, err error) {
	switch r.mode {
	case ModeWorktree, ModeStaged:
		out, err := r.git("--literal-pathspecs", "ls-files", "--stage", "--", path)
		if err != nil {
			return "", "", err
		}
		entry, isDir, err := findExactLsFilesEntry(out, path)
		if err != nil {
			return "", "", err
		}
		if entry != nil {
			return entry.mode, entry.oid, nil
		}
		if isDir {
			return "040000", "", nil
		}
		return "", "", fmt.Errorf("not tracked in the index: %s", path)
	case ModeRef:
		out, err := r.git("--literal-pathspecs", "ls-tree", r.ref, "--", path)
		if err != nil {
			return "", "", err
		}
		line := strings.TrimSpace(out)
		if line == "" {
			return "", "", fmt.Errorf("not present at %s: %s", r.ref, path)
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 || parts[1] != path {
			return "", "", fmt.Errorf("unexpected ls-tree output for %s: %q", path, line)
		}
		fields := strings.Fields(parts[0])
		if len(fields) < 3 {
			return "", "", fmt.Errorf("unexpected ls-tree output for %s: %q", path, line)
		}
		return fields[0], fields[2], nil
	}
	return "", "", fmt.Errorf("unknown mode %d", r.mode)
}

type lsFilesEntry struct {
	mode, oid string
}

// findExactLsFilesEntry scans the output of
// `git ls-files --stage -- <path>` for an entry whose path equals path
// exactly. If none does but at least one entry's path starts with
// "<path>/", path names a directory (isDir is true): ls-files has no
// concept of an empty directory entry, so this is how a directory pathspec
// is told apart from a path that matches nothing at all.
func findExactLsFilesEntry(out, path string) (entry *lsFilesEntry, isDir bool, err error) {
	for _, line := range splitLines(out) {
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			return nil, false, fmt.Errorf("unexpected ls-files --stage output: %q", line)
		}
		fields := strings.Fields(parts[0])
		if len(fields) < 2 {
			return nil, false, fmt.Errorf("unexpected ls-files --stage output: %q", line)
		}
		name := parts[1]
		if name == path {
			return &lsFilesEntry{mode: fields[0], oid: fields[1]}, false, nil
		}
		if strings.HasPrefix(name, path+"/") {
			isDir = true
		}
	}
	return nil, isDir, nil
}

// resolveTreeID resolves a folder path to its git tree object id (design
// D13): `git write-tree --prefix` over the index for ModeWorktree/
// ModeStaged (both read content ids from the index), or `git rev-parse
// <ref>:<path>` for ModeRef.
func (r *Reader) resolveTreeID(path string) (string, error) {
	switch r.mode {
	case ModeWorktree, ModeStaged:
		out, err := r.git("write-tree", "--prefix="+path+"/")
		if err != nil {
			return "", fmt.Errorf("resolve folder %s/: %w", path, err)
		}
		return GitTreeEntityID(strings.TrimSpace(out)), nil
	case ModeRef:
		out, err := r.git("rev-parse", r.ref+":"+path)
		if err != nil {
			return "", fmt.Errorf("resolve folder %s/ at %s: %w", path, r.ref, err)
		}
		oid := strings.TrimSpace(out)
		// rev-parse <ref>:<path> succeeds for any object at path, not only a
		// tree; confirm it is actually a directory before handing back a
		// GitTreeEntityID for it.
		typ, err := r.git("cat-file", "-t", oid)
		if err != nil {
			return "", fmt.Errorf("stat %s/ at %s: %w", path, r.ref, err)
		}
		if strings.TrimSpace(typ) != "tree" {
			return "", fmt.Errorf("%s/ at %s is not a directory (git object type %s)", path, r.ref, strings.TrimSpace(typ))
		}
		return GitTreeEntityID(oid), nil
	}
	return "", fmt.Errorf("unknown mode %d", r.mode)
}

// FileEntry is one tracked file's git mode and object id at a Reader's
// checked state.
type FileEntry struct {
	Mode string
	OID  string
}

// FileEntries resolves every tracked file (including protected paths; the
// caller filters those) to its git mode and object id, in a single git
// invocation regardless of how many files the repository has: `git
// ls-files --stage -z` for the index (ModeWorktree, ModeStaged both read
// content from the index), or `git ls-tree -r -z <ref>` for ModeRef
// (review: "coverage spawns several git processes per tracked file" — this
// replaces one `git ls-files --stage <path>` per file).
func (r *Reader) FileEntries() (map[string]FileEntry, error) {
	var out []byte
	var err error
	switch r.mode {
	case ModeWorktree, ModeStaged:
		out, err = r.gitBytes("ls-files", "--stage", "-z")
	case ModeRef:
		out, err = r.gitBytes("ls-tree", "-r", "-z", r.ref)
	default:
		return nil, fmt.Errorf("unknown mode %d", r.mode)
	}
	if err != nil {
		return nil, fmt.Errorf("list tracked files: %w", err)
	}

	entries := map[string]FileEntry{}
	for _, rec := range splitNUL(string(out)) {
		tab := strings.IndexByte(rec, '\t')
		if tab < 0 {
			return nil, fmt.Errorf("unexpected output: %q", rec)
		}
		fields := strings.Fields(rec[:tab])
		path := rec[tab+1:]
		switch r.mode {
		case ModeWorktree, ModeStaged:
			// "<mode> <oid> <stage>"
			if len(fields) < 2 {
				return nil, fmt.Errorf("unexpected ls-files --stage output: %q", rec)
			}
			entries[path] = FileEntry{Mode: fields[0], OID: fields[1]}
		case ModeRef:
			// "<mode> <type> <oid>"
			if len(fields) < 3 {
				return nil, fmt.Errorf("unexpected ls-tree output: %q", rec)
			}
			entries[path] = FileEntry{Mode: fields[0], OID: fields[2]}
		}
	}
	return entries, nil
}

// AllContentIDs resolves the content identifier (classifyContentID) of
// every entry in entries, in two `git cat-file` processes regardless of
// how many entries there are (review: "coverage spawns several git
// processes per tracked file" — this replaces one `ResolveContentID` call,
// itself up to two git processes, per file): one `cat-file --batch-check`
// to size every regular-file blob, then one `cat-file --batch` to read the
// content of every symlink and every regular-file blob small enough to
// possibly be an unlocked annex pointer file (design D5: maxPointerFileSize
// bounds this without ever reading a large blob's content).
func (r *Reader) AllContentIDs(entries map[string]FileEntry) (map[string]string, error) {
	ids := make(map[string]string, len(entries))
	if len(entries) == 0 {
		return ids, nil
	}

	sizeOIDs := make([]string, 0, len(entries))
	seenSize := map[string]bool{}
	for _, e := range entries {
		if e.Mode == "120000" || seenSize[e.OID] {
			continue
		}
		seenSize[e.OID] = true
		sizeOIDs = append(sizeOIDs, e.OID)
	}
	sizes := batchObjectSizes(r, sizeOIDs)

	contentOIDs := make([]string, 0, len(entries))
	seenContent := map[string]bool{}
	for _, e := range entries {
		needsContent := e.Mode == "120000"
		if size, ok := sizes[e.OID]; ok && size <= maxPointerFileSize {
			needsContent = true
		}
		if needsContent && !seenContent[e.OID] {
			seenContent[e.OID] = true
			contentOIDs = append(contentOIDs, e.OID)
		}
	}
	contents, err := batchCatFileContents(r, contentOIDs)
	if err != nil {
		return nil, err
	}

	for path, e := range entries {
		content, hasContent := contents[e.OID]
		ids[path] = classifyContentID(e.Mode, e.OID, content, hasContent)
	}
	return ids, nil
}

// AllTreeIDs resolves the git tree object id of every directory at the
// Reader's checked state, in a single `git ls-tree -r -t -z <ref>` for
// ModeRef, or one `git write-tree` plus one `git ls-tree -r -t -z <tree>`
// for ModeWorktree/ModeStaged (both read content from the index) — rather
// than one `git write-tree --prefix=<dir>/` per directory (review:
// "coverage spawns several git processes per tracked file").
func (r *Reader) AllTreeIDs() (map[string]string, error) {
	var root string
	switch r.mode {
	case ModeWorktree, ModeStaged:
		out, err := r.git("write-tree")
		if err != nil {
			return nil, fmt.Errorf("write-tree: %w", err)
		}
		root = strings.TrimSpace(out)
	case ModeRef:
		root = r.ref
	default:
		return nil, fmt.Errorf("unknown mode %d", r.mode)
	}

	out, err := r.gitBytes("ls-tree", "-r", "-t", "-z", root)
	if err != nil {
		return nil, fmt.Errorf("list trees at %s: %w", root, err)
	}

	ids := map[string]string{}
	for _, rec := range splitNUL(string(out)) {
		tab := strings.IndexByte(rec, '\t')
		if tab < 0 {
			return nil, fmt.Errorf("unexpected ls-tree output: %q", rec)
		}
		fields := strings.Fields(rec[:tab])
		path := rec[tab+1:]
		if len(fields) < 3 || fields[1] != "tree" {
			continue
		}
		ids[path] = GitTreeEntityID(fields[2])
	}
	return ids, nil
}

// batchObjectSizes resolves the size of every oid in oids using a single
// `git cat-file --batch-check` process (review: "Performance: ... batch
// the calls", same shape as batchGitObjectsExist in validate.go).
func batchObjectSizes(reader *Reader, oids []string) map[string]int64 {
	sizes := make(map[string]int64, len(oids))
	if len(oids) == 0 {
		return sizes
	}
	out := reader.gitStdinBytes(strings.Join(oids, "\n")+"\n", "cat-file", "--batch-check")
	for _, line := range splitLines(string(out)) {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[1] == "missing" {
			continue
		}
		size, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil {
			continue
		}
		sizes[fields[0]] = size
	}
	return sizes
}

// batchCatFileContents reads the content of every oid in oids using a
// single `git cat-file --batch` process, parsing git's
// "<oid> <type> <size>\n<content>\n" batch format. Content may contain
// arbitrary bytes, including newlines, so this tracks exact byte offsets
// rather than splitting the output on newlines.
func batchCatFileContents(reader *Reader, oids []string) (map[string][]byte, error) {
	contents := make(map[string][]byte, len(oids))
	if len(oids) == 0 {
		return contents, nil
	}
	out := reader.gitStdinBytes(strings.Join(oids, "\n")+"\n", "cat-file", "--batch")
	i := 0
	for i < len(out) {
		nl := bytes.IndexByte(out[i:], '\n')
		if nl < 0 {
			break
		}
		header := string(out[i : i+nl])
		i += nl + 1
		fields := strings.Fields(header)
		if len(fields) == 2 && fields[1] == "missing" {
			continue
		}
		if len(fields) < 3 {
			return nil, fmt.Errorf("unexpected cat-file --batch output: %q", header)
		}
		size, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("unexpected cat-file --batch output: %q", header)
		}
		if i+int(size) > len(out) {
			return nil, fmt.Errorf("truncated cat-file --batch output for %s", fields[0])
		}
		contents[fields[0]] = out[i : i+int(size)]
		i += int(size)
		if i < len(out) && out[i] == '\n' {
			i++
		}
	}
	return contents, nil
}

// splitNUL splits a NUL-terminated/-separated git output string (for
// example `git ls-files -z`) into its records, dropping a trailing empty
// record left by the final NUL.
func splitNUL(s string) []string {
	s = strings.TrimRight(s, "\x00")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\x00")
}

func (r *Reader) git(args ...string) (string, error) {
	data, err := r.gitBytes(args...)
	return string(data), err
}

func (r *Reader) gitBytes(args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = r.root
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return stdout.Bytes(), nil
}

// gitStdinBytes runs a git plumbing command that reads batch input from
// stdin (for example `cat-file --batch-check` or `annex whereis
// --batch-keys`), feeding it stdin and returning stdout regardless of exit
// status: some batch commands (`annex whereis --batch-keys`) exit non-zero
// when any input line reports a negative result, which is not a failure of
// the batch call itself.
func (r *Reader) gitStdinBytes(stdin string, args ...string) []byte {
	cmd := exec.Command("git", args...)
	cmd.Dir = r.root
	cmd.Stdin = strings.NewReader(stdin)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	_ = cmd.Run()
	return stdout.Bytes()
}

// isMissingAtRevision reports whether err looks like `git show` failing
// because the path does not exist at the requested revision.
func isMissingAtRevision(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "does not exist") ||
		strings.Contains(msg, "exists on disk, but not in") ||
		strings.Contains(msg, "unknown revision or path not in the working tree")
}
