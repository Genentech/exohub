package sync

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	stdsync "sync"

	"github.com/Genentech/exohub/go/exo/commandutil"
)

// Exit codes for `exo sync --check`, stable for automation (obol and friends)
// to branch on without parsing --json. See the sync reference docs.
const (
	exitCheckClean   = 0
	exitCheckFixable = 3
	exitCheckLost    = 4
)

// checkItem is one non-ok (fixable or lost) *(path, key)* entry in a
// completeness report, grouped across every ref that references it. Field
// names and shape are part of the documented, tested --json contract.
//
// Items are grouped by (path, key) rather than emitted once per (ref, path)
// occurrence: a key's classification (state, remotes_missing, seen_in) only
// depends on the key's location log, never on which ref references it, so
// one item with a `refs` list is both smaller and more useful to automation
// than N near-identical items that differ only in `ref`. For a versioned
// dataset where most files are unchanged across many tags, this makes the
// report O(unique paths), not O(refs x paths).
type checkItem struct {
	Path           string   `json:"path"`
	Key            string   `json:"key"`
	State          string   `json:"state"` // "fixable" or "lost"
	Refs           []string `json:"refs"`
	RemotesMissing []string `json:"remotes_missing,omitempty"`
	SeenIn         []string `json:"seen_in,omitempty"`
	// Verified is true when this item's classification came from an actual
	// presence check against the remote (--verify), not just the location
	// log: a remote can move its underlying storage (e.g. an S3 prefix)
	// without a UUID change, leaving the log claiming content is present
	// when it never made it to the new location (exo-cli#166).
	Verified bool `json:"verified,omitempty"`
}

type checkSummary struct {
	// Fixable and Lost count grouped items (unique (path, key) pairs), not
	// raw (ref, path) occurrences.
	Fixable int `json:"fixable"`
	Lost    int `json:"lost"`
}

// checkReport is the stable --json schema for `exo sync --check`.
type checkReport struct {
	RefsChecked []string     `json:"refs_checked"`
	Items       []checkItem  `json:"items"`
	Summary     checkSummary `json:"summary"`
}

// exitCode returns the automation-facing exit code for this report: the
// worst classification found takes precedence (lost > fixable > clean).
func (r checkReport) exitCode() int {
	if r.Summary.Lost > 0 {
		return exitCheckLost
	}
	if r.Summary.Fixable > 0 {
		return exitCheckFixable
	}
	return exitCheckClean
}

// refKeyEntry is one annexed (ref, path) pair resolved to its content key.
type refKeyEntry struct {
	Ref  string
	Path string
	Key  string
}

// annexLocation is one entry from `git annex whereis --batch-keys --json`.
type annexLocation struct {
	UUID        string
	Here        bool
	Description string
}

// completenessEngine holds the configured-remote set a check is evaluated
// against: remote name <-> git-annex UUID, in both directions.
type completenessEngine struct {
	uuidToName map[string]string
	nameToUUID map[string]string
}

// newCompletenessEngine builds the engine for the given remote names. When
// names is empty, it discovers every configured git-annex remote.
func newCompletenessEngine(names []string) (*completenessEngine, error) {
	if len(names) == 0 {
		discovered, err := listAnnexRemotes()
		if err != nil {
			// listAnnexRemotes shells out to `git config --get-regexp`, which
			// exits 1 (not a real error) when no annex remotes are configured
			// at all. Treat that as "zero remotes" rather than a hard failure.
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) {
				return nil, fmt.Errorf("discovering git-annex remotes: %w", err)
			}
			discovered = nil
		}
		names = discovered
	}
	engine := &completenessEngine{
		uuidToName: make(map[string]string, len(names)),
		nameToUUID: make(map[string]string, len(names)),
	}
	for _, name := range names {
		out, err := runCommandOutput([]string{"git", "config", "--get", fmt.Sprintf("remote.%s.annex-uuid", name)})
		uuid := strings.TrimSpace(out)
		if err != nil || uuid == "" {
			continue
		}
		engine.uuidToName[uuid] = name
		engine.nameToUUID[name] = uuid
	}
	return engine, nil
}

func (e *completenessEngine) remoteNamesSorted() []string {
	names := make([]string, 0, len(e.nameToUUID))
	for name := range e.nameToUUID {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// resolveCheckRefs validates explicit refs, or (when none are given) lists
// every local branch and tag.
func resolveCheckRefs(explicit []string) ([]string, error) {
	if len(explicit) > 0 {
		out := make([]string, 0, len(explicit))
		seen := make(map[string]bool, len(explicit))
		for _, ref := range explicit {
			ref = strings.TrimSpace(ref)
			if ref == "" || seen[ref] {
				continue
			}
			if _, err := runCommandOutput([]string{"git", "rev-parse", "--verify", ref + "^{commit}"}); err != nil {
				return nil, fmt.Errorf("ref not found: %s", ref)
			}
			seen[ref] = true
			out = append(out, ref)
		}
		return out, nil
	}

	out, err := runCommandOutput([]string{"git", "for-each-ref", "--format=%(refname:short)", "refs/heads", "refs/tags"})
	if err != nil {
		return nil, fmt.Errorf("listing branches and tags: %w", err)
	}
	var refs []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			refs = append(refs, line)
		}
	}
	return refs, nil
}

// annexKeyPattern matches the shape of a git-annex key, e.g.
// "SHA256E-s1234--<hex>.ext" or "WORM--example.bin". It is used to recognize
// annex symlink targets and unlocked pointer file contents without needing
// git-annex itself to tell us which tree entries are annexed.
var annexKeyPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(-[a-zA-Z][0-9]+)*--\S+$`)

// extractAnnexKey returns the annex key encoded in a symlink target or
// unlocked pointer file's content, if any.
func extractAnnexKey(content string) (string, bool) {
	content = strings.TrimSpace(content)
	if content == "" {
		return "", false
	}
	parts := strings.Split(content, "/")
	candidate := parts[len(parts)-1]
	if annexKeyPattern.MatchString(candidate) {
		return candidate, true
	}
	return "", false
}

// maxPointerFileSize bounds which regular-file blobs are worth fetching to
// check for an annex pointer file. Unlocked annex pointer files are tiny
// (just the key, or a short relative path into .git/annex/objects); ordinary
// git-tracked content (CSVs, notebooks, bundles) can be arbitrarily large, so
// never fetch a regular file's content above this size. Symlinks (which
// carry the annex key as their target) have no such check: the filesystem
// already bounds symlink target length well under this.
const maxPointerFileSize = 1024

type treeBlob struct {
	SHA  string
	Path string
}

// listCandidateBlobs lists tree entries reachable from treeish (a ref, a
// commit, or a tree SHA — callers pass a tree SHA so that refs sharing the
// same tree are only walked once, see collectAnnexEntriesForRefs) that
// *might* encode an annex key: symlinks (mode 120000, the target holds the
// key) and small regular files (mode 100644/100755, size bounded by
// maxPointerFileSize — unlocked annex pointer files). This is a pre-filter
// that runs before any blob content is fetched, so large non-annexed git
// blobs (big CSVs, notebooks, bundles committed directly to git) are never
// read into memory. Uses `-l` for sizes and `-z` so unusual (non-ASCII)
// paths are not C-quoted.
// listCandidateBlobsCalls counts invocations of listCandidateBlobs (and so,
// of the underlying `git ls-tree`). It exists only so tests can assert that
// N refs sharing a tree are walked once, not N times; production code never
// reads it.
var listCandidateBlobsCalls int

func listCandidateBlobs(treeish string) ([]treeBlob, error) {
	listCandidateBlobsCalls++
	out, err := runCommandOutput([]string{"git", "ls-tree", "-r", "-l", "-z", "--full-tree", treeish})
	if err != nil {
		return nil, fmt.Errorf("git ls-tree -r -l -z %s: %w", treeish, err)
	}
	var blobs []treeBlob
	for _, entry := range strings.Split(out, "\x00") {
		if entry == "" {
			continue
		}
		tabIdx := strings.Index(entry, "\t")
		if tabIdx < 0 {
			continue
		}
		meta := strings.Fields(entry[:tabIdx])
		// `ls-tree -l` fields: <mode> <type> <sha> <size>
		if len(meta) < 4 || meta[1] != "blob" {
			continue
		}
		mode, sha, sizeStr := meta[0], meta[2], meta[3]
		path := entry[tabIdx+1:]
		if mode == "120000" {
			blobs = append(blobs, treeBlob{SHA: sha, Path: path})
			continue
		}
		if mode != "100644" && mode != "100755" {
			continue
		}
		size, err := strconv.ParseInt(sizeStr, 10, 64)
		if err != nil || size <= 0 || size > maxPointerFileSize {
			continue
		}
		blobs = append(blobs, treeBlob{SHA: sha, Path: path})
	}
	return blobs, nil
}

// catFileBatchContents reads the full content of each blob sha via a single
// `git cat-file --batch` process, in order, without assuming the content is
// text (sizes are read explicitly so arbitrary binary content is handled).
func catFileBatchContents(shas []string) (map[string]string, error) {
	result := make(map[string]string, len(shas))
	if len(shas) == 0 {
		return result, nil
	}

	cmd := commandutil.Command("git", "cat-file", "--batch")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderrBuf strings.Builder
	cmd.Stderr = &stderrBuf
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	writeErrCh := make(chan error, 1)
	go func() {
		defer stdin.Close()
		w := bufio.NewWriter(stdin)
		for _, sha := range shas {
			if _, err := w.WriteString(sha + "\n"); err != nil {
				writeErrCh <- err
				return
			}
		}
		writeErrCh <- w.Flush()
	}()

	reader := bufio.NewReader(stdout)
	for _, sha := range shas {
		header, err := reader.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("git cat-file --batch: reading header for %s: %w", sha, err)
		}
		fields := strings.Fields(header)
		if len(fields) < 2 {
			continue
		}
		if fields[1] == "missing" {
			continue
		}
		if len(fields) < 3 {
			continue
		}
		size, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("git cat-file --batch: bad size for %s: %q", sha, header)
		}
		buf := make([]byte, size)
		if _, err := io.ReadFull(reader, buf); err != nil {
			return nil, fmt.Errorf("git cat-file --batch: reading content for %s: %w", sha, err)
		}
		if _, err := reader.ReadByte(); err != nil { // trailing newline after each object
			return nil, fmt.Errorf("git cat-file --batch: reading trailer for %s: %w", sha, err)
		}
		result[fields[0]] = string(buf)
	}

	if err := <-writeErrCh; err != nil {
		return nil, fmt.Errorf("git cat-file --batch: writing input: %w", err)
	}
	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("git cat-file --batch: %w (%s)", err, stderrBuf.String())
	}
	return result, nil
}

// resolveTreeSHA returns the tree object a ref (or any tree-ish) points at.
// `git ls-tree` accepts a tree SHA directly, so refs that happen to share a
// tree (most tags in a versioned dataset usually do, since a tag rarely
// changes anything else) can be walked exactly once.
func resolveTreeSHA(ref string) (string, error) {
	out, err := runCommandOutput([]string{"git", "rev-parse", "--verify", ref + "^{tree}"})
	if err != nil {
		return "", fmt.Errorf("git rev-parse %s^{tree}: %w", ref, err)
	}
	return strings.TrimSpace(out), nil
}

// collectAnnexEntriesForRefs resolves every annexed (ref, path, key) triple
// reachable from refs, using git plumbing only (no `git annex find
// --branch`, no checkout). Refs are first grouped by the tree SHA they
// point at (`resolveTreeSHA`): `ls-tree` and the candidate pre-filter run at
// most once per *distinct tree*, not once per ref, so N refs pointing at the
// same commit (common for tags in a versioned dataset — most of history
// doesn't change) collapse to a single walk. Candidate blobs collected from
// each distinct tree are then deduplicated by blob SHA across the *entire*
// ref set, and their content is fetched in a single `cat-file --batch`
// call: a blob shared across many distinct trees is still read at most once
// per run.
func collectAnnexEntriesForRefs(refs []string) ([]refKeyEntry, error) {
	type occurrence struct {
		Ref  string
		Path string
	}
	occurrencesBySHA := make(map[string][]occurrence)

	refsByTree := make(map[string][]string)
	var treeOrder []string
	for _, ref := range refs {
		treeSHA, err := resolveTreeSHA(ref)
		if err != nil {
			return nil, err
		}
		if _, seen := refsByTree[treeSHA]; !seen {
			treeOrder = append(treeOrder, treeSHA)
		}
		refsByTree[treeSHA] = append(refsByTree[treeSHA], ref)
	}

	for _, treeSHA := range treeOrder {
		blobs, err := listCandidateBlobs(treeSHA)
		if err != nil {
			return nil, err
		}
		refsForTree := refsByTree[treeSHA]
		for _, b := range blobs {
			for _, ref := range refsForTree {
				occurrencesBySHA[b.SHA] = append(occurrencesBySHA[b.SHA], occurrence{Ref: ref, Path: b.Path})
			}
		}
	}

	shas := make([]string, 0, len(occurrencesBySHA))
	for sha := range occurrencesBySHA {
		shas = append(shas, sha)
	}
	contents, err := catFileBatchContents(shas)
	if err != nil {
		return nil, fmt.Errorf("reading candidate blob contents: %w", err)
	}

	var entries []refKeyEntry
	for sha, occurrences := range occurrencesBySHA {
		content, ok := contents[sha]
		if !ok {
			continue
		}
		key, isKey := extractAnnexKey(content)
		if !isKey {
			continue
		}
		for _, occ := range occurrences {
			entries = append(entries, refKeyEntry{Ref: occ.Ref, Path: occ.Path, Key: key})
		}
	}
	return entries, nil
}

type whereisKeyResult struct {
	Key       string
	Locations []annexLocation
}

// whereisBatch queries the location of every key in one batched
// `git annex whereis --batch-keys --json` process.
func whereisBatch(keys []string) (map[string][]annexLocation, error) {
	result := make(map[string][]annexLocation, len(keys))
	if len(keys) == 0 {
		return result, nil
	}

	cmd := commandutil.Command("git", "annex", "whereis", "--batch-keys", "--json")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderrBuf strings.Builder
	cmd.Stderr = &stderrBuf
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	writeErrCh := make(chan error, 1)
	go func() {
		defer stdin.Close()
		w := bufio.NewWriter(stdin)
		for _, key := range keys {
			if _, err := w.WriteString(key + "\n"); err != nil {
				writeErrCh <- err
				return
			}
		}
		writeErrCh <- w.Flush()
	}()

	type whereisEntryJSON struct {
		Here        bool   `json:"here"`
		UUID        string `json:"uuid"`
		Description string `json:"description"`
	}
	type whereisJSON struct {
		Key     string             `json:"key"`
		Whereis []whereisEntryJSON `json:"whereis"`
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var wj whereisJSON
		if err := json.Unmarshal([]byte(line), &wj); err != nil {
			continue
		}
		locations := make([]annexLocation, 0, len(wj.Whereis))
		for _, w := range wj.Whereis {
			locations = append(locations, annexLocation{UUID: w.UUID, Here: w.Here, Description: w.Description})
		}
		result[wj.Key] = locations
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("git annex whereis --batch-keys: reading output: %w", err)
	}
	if err := <-writeErrCh; err != nil {
		return nil, fmt.Errorf("git annex whereis --batch-keys: writing input: %w", err)
	}
	// git-annex exits non-zero from `whereis --batch-keys` when any key in the
	// batch has zero copies, even though it already printed valid JSON for
	// every key (including that one, with an empty "whereis" list). That's
	// exactly the "lost" case this engine needs to detect, so a non-zero exit
	// here is not itself an error as long as we parsed a result per key below.
	_ = cmd.Wait()
	if len(result) != len(keys) {
		missing := 0
		for _, k := range keys {
			if _, ok := result[k]; !ok {
				missing++
			}
		}
		if missing > 0 {
			return result, fmt.Errorf("git annex whereis --batch-keys: no response for %d of %d keys (%s)", missing, len(keys), stderrBuf.String())
		}
	}
	return result, nil
}

// pkClassification is one key's classification against the location log:
// which configured remotes (if any) it's logged present on, whether it's
// present in this clone, and where else (if anywhere unconfigured) it's
// been seen.
type pkClassification struct {
	onRemotes map[string]bool
	hereLocal bool
	seenIn    []string
}

// maxVerifyRemoteConcurrency bounds how many `git annex checkpresentkey
// --batch <remote>` processes run concurrently for `--check --verify`, one
// per remote the location log claims has at least one checked key present.
const maxVerifyRemoteConcurrency = 8

// checkPresentBatch verifies, for one remote, whether each key's content is
// actually retrievable there right now (not just logged present), via a
// single `git annex checkpresentkey --batch <remote>` process: keys are fed
// one per line on stdin, and git-annex replies "1" or "0" per line, in
// order.
func checkPresentBatch(remote string, keys []string) (map[string]bool, error) {
	result := make(map[string]bool, len(keys))
	if len(keys) == 0 {
		return result, nil
	}

	cmd := commandutil.Command("git", "annex", "checkpresentkey", "--batch", remote)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderrBuf strings.Builder
	cmd.Stderr = &stderrBuf
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	writeErrCh := make(chan error, 1)
	go func() {
		defer stdin.Close()
		w := bufio.NewWriter(stdin)
		for _, key := range keys {
			if _, err := w.WriteString(key + "\n"); err != nil {
				writeErrCh <- err
				return
			}
		}
		writeErrCh <- w.Flush()
	}()

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 4*1024), 64*1024)
	i := 0
	for i < len(keys) && scanner.Scan() {
		result[keys[i]] = strings.TrimSpace(scanner.Text()) == "1"
		i++
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("git annex checkpresentkey --batch %s: reading output: %w", remote, err)
	}
	if err := <-writeErrCh; err != nil {
		return nil, fmt.Errorf("git annex checkpresentkey --batch %s: writing input: %w", remote, err)
	}
	// git-annex exits non-zero from checkpresentkey when a key is verified
	// not present; that's a normal, expected result here, not an error, as
	// long as every key in the batch got a line of output (checked below).
	_ = cmd.Wait()
	if i != len(keys) {
		return result, fmt.Errorf("git annex checkpresentkey --batch %s: no response for %d of %d keys (%s)", remote, len(keys)-i, len(keys), stderrBuf.String())
	}
	return result, nil
}

// verifyKeyPresence actually checks, for every remote the location log
// claims has at least one of classified's keys present, whether that
// content is still really retrievable there (git-annex's checkpresentkey,
// not the log). Remotes are checked concurrently (bounded by
// maxVerifyRemoteConcurrency), each via a single batched process covering
// every key the log claims it has.
func verifyKeyPresence(classified map[string]pkClassification) (map[string]map[string]bool, error) {
	keysByRemote := make(map[string][]string)
	for key, c := range classified {
		for remote := range c.onRemotes {
			keysByRemote[remote] = append(keysByRemote[remote], key)
		}
	}
	remotes := make([]string, 0, len(keysByRemote))
	for remote := range keysByRemote {
		remotes = append(remotes, remote)
	}
	sort.Strings(remotes)

	type remoteResult struct {
		remote  string
		present map[string]bool
		err     error
	}
	results := make([]remoteResult, len(remotes))
	sem := make(chan struct{}, maxVerifyRemoteConcurrency)
	var wg stdsync.WaitGroup
	for i, remote := range remotes {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, remote string) {
			defer wg.Done()
			defer func() { <-sem }()
			present, err := checkPresentBatch(remote, keysByRemote[remote])
			results[i] = remoteResult{remote: remote, present: present, err: err}
		}(i, remote)
	}
	wg.Wait()

	verified := make(map[string]map[string]bool)
	for _, r := range results {
		if r.err != nil {
			return nil, r.err
		}
		for key, present := range r.present {
			if verified[key] == nil {
				verified[key] = make(map[string]bool)
			}
			verified[key][r.remote] = present
		}
	}
	return verified, nil
}

// runCompletenessCheck is the shared engine: collect annexed entries for
// every ref, query their locations once (batched, deduplicated across refs),
// and classify each entry as ok / fixable / lost against engine's remote
// set, trusting the location log.
func runCompletenessCheck(refs []string, engine *completenessEngine) (checkReport, error) {
	return runCompletenessCheckVerify(refs, engine, false)
}

// runCompletenessCheckVerify is runCompletenessCheck, with an added
// `--verify` mode: for keys the location log says are present on a
// configured remote, it actually checks presence there (checkpresentkey)
// instead of trusting the log, and reclassifies anything the log got wrong
// as fixable or lost, flagged Verified. A remote's underlying storage can
// move (e.g. an S3 prefix) without a UUID change, leaving the log stale
// (exo-cli#166); --verify is how `exo sync --check` catches that.
func runCompletenessCheckVerify(refs []string, engine *completenessEngine, verify bool) (checkReport, error) {
	report := checkReport{RefsChecked: refs}

	allEntries, err := collectAnnexEntriesForRefs(refs)
	if err != nil {
		return report, err
	}

	// Group occurrences by (path, key) before classifying: a key's
	// classification never depends on which ref references it, so this
	// avoids both redundant classification work and redundant output for
	// the common case where many refs share most of their content.
	type pathKey struct {
		Path string
		Key  string
	}
	refsByPathKey := make(map[pathKey][]string)
	var order []pathKey
	keySet := make(map[string]bool)
	for _, e := range allEntries {
		pk := pathKey{Path: e.Path, Key: e.Key}
		if _, seen := refsByPathKey[pk]; !seen {
			order = append(order, pk)
		}
		refsByPathKey[pk] = append(refsByPathKey[pk], e.Ref)
		keySet[e.Key] = true
	}
	keys := make([]string, 0, len(keySet))
	for k := range keySet {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	locations, err := whereisBatch(keys)
	if err != nil {
		return report, fmt.Errorf("querying key locations: %w", err)
	}

	remoteNames := engine.remoteNamesSorted()

	classified := make(map[string]pkClassification, len(keys))
	for _, key := range keys {
		c := pkClassification{onRemotes: make(map[string]bool)}
		for _, loc := range locations[key] {
			if loc.Here {
				c.hereLocal = true
			}
			if name, ok := engine.uuidToName[loc.UUID]; ok {
				c.onRemotes[name] = true
			} else if !loc.Here {
				label := loc.Description
				if label == "" {
					label = loc.UUID
				}
				c.seenIn = append(c.seenIn, label)
			}
		}
		classified[key] = c
	}

	var verifiedPresent map[string]map[string]bool
	if verify {
		verifiedPresent, err = verifyKeyPresence(classified)
		if err != nil {
			return report, fmt.Errorf("verifying remote presence: %w", err)
		}
	}

	for _, pk := range order {
		c := classified[pk.Key]
		onRemotes := c.onRemotes
		verifiedItem := false
		if len(onRemotes) > 0 {
			if !verify {
				continue
			}
			confirmed := false
			for name := range onRemotes {
				if verifiedPresent[pk.Key][name] {
					confirmed = true
					break
				}
			}
			if confirmed {
				continue
			}
			// The log says this key is present on a configured remote, but
			// checkpresentkey disagrees with every one of them: treat it as
			// present nowhere, same as if the log had said so all along.
			onRemotes = map[string]bool{}
			verifiedItem = true
		}

		var missing []string
		for _, name := range remoteNames {
			if !onRemotes[name] {
				missing = append(missing, name)
			}
		}

		refs := uniqueStrings(refsByPathKey[pk])
		sort.Strings(refs)
		item := checkItem{Path: pk.Path, Key: pk.Key, Refs: refs, RemotesMissing: missing, Verified: verifiedItem}
		if c.hereLocal {
			item.State = "fixable"
			report.Summary.Fixable++
		} else {
			item.State = "lost"
			item.SeenIn = uniqueStrings(c.seenIn)
			report.Summary.Lost++
		}
		report.Items = append(report.Items, item)
	}

	sort.Slice(report.Items, func(i, j int) bool {
		if report.Items[i].Path != report.Items[j].Path {
			return report.Items[i].Path < report.Items[j].Path
		}
		return report.Items[i].Key < report.Items[j].Key
	})

	return report, nil
}

// printCheckReport writes the human-readable form of a completeness report.
// Every hint names only exo commands (operator rule: never git-annex).
func printCheckReport(w io.Writer, report checkReport) {
	refLabel := strings.Join(report.RefsChecked, ", ")
	if refLabel == "" {
		refLabel = "(none)"
	}
	fmt.Fprintf(w, "Checked %d ref(s) for annexed content missing from configured remotes: %s\n", len(report.RefsChecked), refLabel)

	if len(report.Items) == 0 {
		fmt.Fprintln(w, "  OK: every annexed file referenced by the checked refs is present on a configured remote")
		return
	}

	var fixable, lost []checkItem
	for _, item := range report.Items {
		if item.State == "fixable" {
			fixable = append(fixable, item)
		} else {
			lost = append(lost, item)
		}
	}

	if len(fixable) > 0 {
		fmt.Fprintf(w, "  fixable (present here, missing from every configured remote): %d\n", len(fixable))
		for _, item := range fixable {
			fmt.Fprintf(w, "    %s@%s (key %s): missing from %s\n", item.Path, strings.Join(item.Refs, ","), item.Key, joinOrNone(item.RemotesMissing))
		}
	}
	if len(lost) > 0 {
		fmt.Fprintf(w, "  lost (not here, missing from every configured remote): %d\n", len(lost))
		for _, item := range lost {
			line := fmt.Sprintf("    %s@%s (key %s): missing from %s", item.Path, strings.Join(item.Refs, ","), item.Key, joinOrNone(item.RemotesMissing))
			if len(item.SeenIn) > 0 {
				line += fmt.Sprintf("; seen in: %s", strings.Join(item.SeenIn, ", "))
			}
			fmt.Fprintln(w, line)
		}
	}

	fmt.Fprintln(w)
	if len(fixable) > 0 {
		fmt.Fprintln(w, "Fixable: this clone has the content above but no configured remote does; check out that ref and run exo sync to upload it.")
	}
	if len(lost) > 0 {
		fmt.Fprintln(w, "Lost: the content above is not available on any known remote or here; check other clones (see seen_in above).")
	}
}

func joinOrNone(items []string) string {
	if len(items) == 0 {
		return "(none configured)"
	}
	return strings.Join(items, ", ")
}

// runCheckCommand implements `exo sync --check`: resolve refs, run the
// completeness engine, print or emit the report, and return the exit code.
// With verify, keys the location log says are present on a configured
// remote are also checked for real (see runCompletenessCheckVerify).
func runCheckCommand(refs []string, withRemotes []string, outputJSON bool, verify bool) int {
	resolvedRefs, err := resolveCheckRefs(refs)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 1
	}
	if len(resolvedRefs) == 0 {
		fmt.Println("No branches or tags to check")
		return exitCheckClean
	}

	engine, err := newCompletenessEngine(withRemotes)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 1
	}
	if len(engine.nameToUUID) == 0 {
		fmt.Println("No git-annex remotes configured; nothing to check against")
		return exitCheckClean
	}

	report, err := runCompletenessCheckVerify(resolvedRefs, engine, verify)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 1
	}

	if outputJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if report.Items == nil {
			report.Items = []checkItem{}
		}
		if err := enc.Encode(report); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			return 1
		}
	} else {
		printCheckReport(os.Stdout, report)
	}

	return report.exitCode()
}
