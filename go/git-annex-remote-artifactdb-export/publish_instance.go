//go:build artifactdb

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// validateInstancePrefix enforces the spec "Instance prefix guard": a
// role: instance remote's s3url must be exactly s3://<bucket>/<projectID>
// (one path segment, equal to the remote's effective project id, not
// starting with "_"). Mirrors ValidateInstancePrefix in
// go/exo/commands/init/config.go — the catalog binary is a separate Go
// module (its own go.mod), so this is a deliberate duplicate; keep both in
// sync.
func validateInstancePrefix(s3url, projectID string) error {
	const prefix = "s3://"
	if !strings.HasPrefix(s3url, prefix) {
		return fmt.Errorf("invalid s3url %q (must start with s3://)", s3url)
	}
	rest := strings.TrimPrefix(s3url, prefix)
	parts := strings.SplitN(rest, "/", 2)
	bucket := parts[0]
	if bucket == "" {
		return fmt.Errorf("invalid s3url %q (missing bucket)", s3url)
	}
	want := fmt.Sprintf("s3://%s/%s", bucket, projectID)
	if s3url != want {
		return fmt.Errorf("s3url %q is not allowed for an instance remote (expected %s)", s3url, want)
	}
	if strings.HasPrefix(projectID, "_") {
		return fmt.Errorf("s3url %q is not allowed for an instance remote (project id %q must not start with '_')", s3url, projectID)
	}
	return nil
}

// annexPointerPrefix is the start of git-annex's unlocked-file pointer
// content, as git stores it in the ref (and as git archive extracts it): a
// small regular file whose entire content is a path to the annexed object
// ("/annex/objects/<key>..."), rather than the symlink used for a locked
// annexed file. See looksLikeAnnexPointer.
const annexPointerPrefix = "/annex/objects/"

// annexPointerMaxSize bounds the file size looksLikeAnnexPointer bothers to
// read: a pointer file is a short path, so anything larger is read
// (metadata) content, not a pointer, and is skipped without reading it.
const annexPointerMaxSize = 4096

// looksLikeAnnexPointer reports whether the regular file at path is an
// unlocked git-annex pointer file (content starting with
// annexPointerPrefix), rather than real metadata content.
func looksLikeAnnexPointer(path string, size int64) (bool, error) {
	if size <= 0 || size > annexPointerMaxSize {
		return false, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	return strings.HasPrefix(strings.TrimSpace(string(data)), annexPointerPrefix), nil
}

// validateArtifactdbTree refuses, before any upload, a .artifactdb/ tree
// containing an annexed entry — a symlink (git-annex's working-tree
// representation of a locked annexed file) or a small regular file whose
// content is an annex pointer (an unlocked annexed file, see
// looksLikeAnnexPointer) — or any name starting with ".." (reserved for
// internal files such as the completion marker).
func validateArtifactdbTree(dir string) error {
	return filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if path == dir {
			return nil
		}
		name := info.Name()
		if strings.HasPrefix(name, "..") {
			return fmt.Errorf("refusing to publish %q: names starting with '..' are reserved", path)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to publish %q: annexed (symlink) files are not allowed in .artifactdb/", path)
		}
		if info.Mode().IsRegular() {
			isPointer, err := looksLikeAnnexPointer(path, info.Size())
			if err != nil {
				return err
			}
			if isPointer {
				return fmt.Errorf("refusing to publish %q: an unlocked annexed file (annex pointer) is not allowed in .artifactdb/", path)
			}
		}
		return nil
	})
}

// storageInfo is the shape of GET <instance url>/ relevant to resolving the
// tenant's metadata folder: storage.clients[].s3.{bucket,meta_folder}.
type storageInfo struct {
	Storage struct {
		Clients []struct {
			S3 *struct {
				Bucket     string `json:"bucket"`
				MetaFolder string `json:"meta_folder"`
			} `json:"s3"`
		} `json:"clients"`
	} `json:"storage"`
}

// fetchInstanceMetaFolder reads GET <instanceURL>/ and returns the
// meta_folder declared by the storage.clients[].s3 entry whose bucket
// matches bucket (the instance remote's s3url bucket). Per the spec
// "Instance remotes publish only the ref's .artifactdb, into the tenant's
// metadata folder", exo never guesses: an unreachable instance, or no
// matching bucket, is a refusal before any upload. An empty return with a
// nil error means the entry exists but declares no meta_folder (the
// destination is then the version root).
//
// This request deliberately carries NO Authorization header: storage info
// is public (ArtifactDB instances answer GET <url>/ anonymously), and the
// instance's url comes from a repo's (possibly cloned/crafted)
// .exohub/remotes — sending the caller's ExoHub token here would leak it to
// an arbitrary host, exactly what routing the ingest through the ExoHub API
// (never a direct call) is meant to prevent. See publishInstanceNotify for
// the one request that does carry the token, to the ExoHub API itself.
func fetchInstanceMetaFolder(instanceURL, bucket string) (string, error) {
	endpoint := strings.TrimRight(instanceURL, "/") + "/"
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("reading instance storage configuration at %s: %w", endpoint, err)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("reading instance storage configuration at %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading instance storage configuration at %s: %w", endpoint, err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("instance %s returned %d reading storage configuration: %s", endpoint, resp.StatusCode, sanitizeMsg(string(body)))
	}
	var info storageInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return "", fmt.Errorf("parsing instance storage configuration from %s: %w", endpoint, err)
	}
	for _, c := range info.Storage.Clients {
		if c.S3 != nil && c.S3.Bucket == bucket {
			return c.S3.MetaFolder, nil
		}
	}
	return "", fmt.Errorf("instance %s has no storage client for bucket %q", endpoint, bucket)
}

// completionMarker is the small JSON document written as
// <s3url>/<ref_name>/..complete, the last object of a successful instance
// publish (spec "Completion marker written last").
type completionMarker struct {
	Version    string   `json:"version"`
	Commit     string   `json:"commit"`
	Objects    int      `json:"objects"`
	ExoVersion string   `json:"exo_version"`
	SourceRepo string   `json:"source_repo"`
	Files      []string `json:"files"`
}

// writeCompletionMarker uploads marker as JSON to dst.
func writeCompletionMarker(s5cmd, dst string, marker completionMarker) error {
	body, err := json.Marshal(marker)
	if err != nil {
		return err
	}
	tmpFile, err := os.CreateTemp("", "complete-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmpFile.Name())
	if _, err := tmpFile.Write(body); err != nil {
		tmpFile.Close()
		return err
	}
	tmpFile.Close()

	r := runS5cmd(s5cmd, "cp", tmpFile.Name(), dst)
	if r.code != 0 {
		return fmt.Errorf("s5cmd cp failed: %s", sanitizeMsg(string(r.stderr)))
	}
	return nil
}

// readCompletionMarker reads and parses the ..complete marker at markerURL.
// A missing marker (first publish, or an earlier attempt that never
// completed) returns (nil, nil). Any other failure — access/credentials,
// network, or a record that doesn't parse — is returned as an error: unlike
// checkTagImmutability this is used to decide what to delete, so it must
// fail closed rather than silently behave as "no previous marker".
func readCompletionMarker(s5cmd, markerURL string) (*completionMarker, error) {
	r := runS5cmd(s5cmd, "cat", markerURL)
	if r.code != 0 {
		msg := sanitizeMsg(string(r.stderr))
		if isNotFoundS5cmdError(msg) {
			return nil, nil
		}
		return nil, fmt.Errorf("could not read the existing marker at %s: %s", markerURL, msg)
	}
	var marker completionMarker
	if err := json.Unmarshal(r.stdout, &marker); err != nil {
		return nil, fmt.Errorf("the existing marker at %s could not be parsed: %w", markerURL, err)
	}
	return &marker, nil
}

// listArtifactdbFiles walks dir and returns the slash-separated paths of
// its regular files, relative to dir — the exact set of object keys (under
// dst) that runS5cmdSync uploads for a publish. validateArtifactdbTree has
// already refused symlinks, annex pointers and ".."-prefixed names by the
// time this runs, so every entry seen here is a real metadata file.
func listArtifactdbFiles(dir string) ([]string, error) {
	var files []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

// stageKeptFiles stages a copy of srcDir containing only keptFiles (a
// subset of listArtifactdbFiles(srcDir)'s output) into a fresh temp
// directory, hardlinking each file (falling back to a byte copy when the
// temp dir is on a different filesystem) and returns its path plus a
// cleanup func. s5cmd sync has its own wildcard dialect and its own
// matching rules, which can disagree with matchAnnexGlob on what a given
// exclude: glob matches (exo-cli#202 review) — so the set it would
// actually upload under a bare `sync --exclude <glob>` can silently differ
// from the kept set our matcher computed, with no exclude pattern of ours
// ever reaching s5cmd to go wrong in the first place. Staging exactly the
// kept files and syncing that directory, with no --exclude at all, removes
// s5cmd's glob dialect from the decision entirely: what's uploaded is
// always exactly keptFiles.
func stageKeptFiles(srcDir string, keptFiles []string) (stagedDir string, cleanup func(), err error) {
	stagedDir, err = os.MkdirTemp("", "artifactdb-instance-publish-*")
	if err != nil {
		return "", nil, fmt.Errorf("creating staging dir: %w", err)
	}
	cleanup = func() { os.RemoveAll(stagedDir) }
	for _, rel := range keptFiles {
		srcPath := filepath.Join(srcDir, filepath.FromSlash(rel))
		dstPath := filepath.Join(stagedDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
			cleanup()
			return "", nil, fmt.Errorf("staging %s: %w", rel, err)
		}
		if linkErr := os.Link(srcPath, dstPath); linkErr != nil {
			if copyErr := copyFileContents(srcPath, dstPath); copyErr != nil {
				cleanup()
				return "", nil, fmt.Errorf("staging %s: %w", rel, copyErr)
			}
		}
	}
	return stagedDir, cleanup, nil
}

// copyFileContents copies src's contents to dst (created with 0o644),
// used by stageKeptFiles when hardlinking fails (e.g. src and dst are on
// different filesystems).
func copyFileContents(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// matchAnnexGlob matches a glob pattern against a file path: * and ? stay
// within one path segment, ** crosses segments, with a fallback match
// against the basename alone (so e.g. "dataset_merged.json*" matches at any
// depth). This is a verbatim port of matchAnnexGlob in
// go/exo/commands/sync/command.go — the exact matcher that drives exo's
// dry-run "excluded" preview for a remote's exclude: globs, ported here
// (like validateInstancePrefix documents doing) since this is a separate Go
// module. A prior version of this file used a different (bundle-style)
// glob matcher here, which disagreed with exo's on "**" edge cases (e.g.
// "**/x.json" vs "ax.json", or "meta/**" vs "meta"), letting the dry-run
// preview and the actual publish disagree on what gets excluded
// (exo-cli#202 review). Keep both copies byte-identical; see
// TestMatchAnnexGlob_SharedVectors for the shared test-vector table.
func matchAnnexGlob(pattern, filePath string) bool {
	if !strings.Contains(pattern, "**") {
		// No **: use filepath.Match against full path or basename.
		if m, _ := filepath.Match(pattern, filePath); m {
			return true
		}
		if m, _ := filepath.Match(pattern, filepath.Base(filePath)); m {
			return true
		}
		return false
	}
	// Convert git-annex glob to regex.
	// **/ matches zero or more path components (the slash is optional after last **).
	var regexBuf strings.Builder
	i := 0
	for i < len(pattern) {
		if pattern[i] == '*' && i+1 < len(pattern) && pattern[i+1] == '*' {
			// **
			regexBuf.WriteString(".*") // matches any chars including /
			i += 2
			if i < len(pattern) && pattern[i] == '/' {
				// **/  → the .* already allows empty match, so the / is optional
				regexBuf.WriteString("/?") // optional slash (** matches zero components)
				i++
			}
		} else if pattern[i] == '*' {
			regexBuf.WriteString("[^/]*") // * matches within one path segment
			i++
		} else if pattern[i] == '?' {
			regexBuf.WriteString("[^/]") // ? matches one char within segment
			i++
		} else {
			regexBuf.WriteString(regexp.QuoteMeta(string(pattern[i])))
			i++
		}
	}
	m, err := regexp.MatchString("^"+regexBuf.String()+"$", filePath)
	return err == nil && m
}

// matchesAnyExcludeGlob reports whether path matches any of globs.
func matchesAnyExcludeGlob(globs []string, path string) bool {
	for _, pattern := range globs {
		if matchAnnexGlob(pattern, path) {
			return true
		}
	}
	return false
}

// partitionExcludedFiles splits files (slash-separated paths relative to
// the .artifactdb/ root, as returned by listArtifactdbFiles) into those
// that match any of globs (excluded) and those that don't (kept). A nil or
// empty globs returns (files, nil) unchanged, so no-exclude callers never
// allocate.
func partitionExcludedFiles(files []string, globs []string) (kept, excluded []string) {
	if len(globs) == 0 {
		return files, nil
	}
	for _, f := range files {
		if matchesAnyExcludeGlob(globs, f) {
			excluded = append(excluded, f)
		} else {
			kept = append(kept, f)
		}
	}
	return kept, excluded
}

// deleteExcludedInstanceObjects deletes, under dst (the instance version
// prefix, or its tenant-meta-folder subpath), every key in excludedFiles —
// the local .artifactdb/ files that currently match an exclude glob —
// regardless of whether it appears in the previous completion marker's file
// list, so a previously published copy of a now-excluded file stops being
// crawled (self-heal, like exo-cli#177's). Applies to both branches and
// tags: unlike deleteStaleInstanceObjects (rename/deletion cleanup, gated
// to branches since a tag's commit never changes), an exclude glob can
// newly match an already-published tag on a re-publish of the same commit,
// which still passes tag immutability. Every candidate key is hard-guarded
// by isSafeStaleObjectKey before the delete is ever issued to s5cmd, so
// this never touches dst's "..complete", "..publishing" or "..meta"
// siblings, nor anything outside dst. A NoSuchKey failure (the object was
// never published, or a previous attempt already deleted it) is not an
// error.
func deleteExcludedInstanceObjects(s5cmd, dst string, excludedFiles []string) error {
	for _, key := range excludedFiles {
		if err := isSafeStaleObjectKey(key); err != nil {
			return err
		}
		url := dst + key
		r := runS5cmd(s5cmd, "rm", "--raw", url)
		if r.code != 0 {
			msg := sanitizeMsg(string(r.stderr))
			if !isNotFoundS5cmdError(msg) {
				return fmt.Errorf("s5cmd rm failed for excluded object %s: %s", url, msg)
			}
		}
	}
	return nil
}

// isSafeStaleObjectKey reports whether key is safe to delete under dst: not
// empty, not starting with ".." or "/", and containing no ".." path
// segment. Since the deleted URL is always dst+key by construction, this is
// what actually keeps every deletion confined to dst and off any reserved
// (".."-prefixed) object such as the completion marker, ..publishing, or
// the instance's own ..project-source.json — a further
// strings.HasPrefix(dst+key, dst) check would be a tautology (dst+key
// always starts with dst) and is deliberately not done.
func isSafeStaleObjectKey(key string) error {
	if key == "" || strings.HasPrefix(key, "..") || strings.HasPrefix(key, "/") {
		return fmt.Errorf("refusing to delete stale object with reserved key %q", key)
	}
	for _, segment := range strings.Split(key, "/") {
		if segment == ".." {
			return fmt.Errorf("refusing to delete stale object with path-traversal key %q", key)
		}
	}
	return nil
}

// deleteStaleInstanceObjects deletes, under dst, every key listed in
// prevFiles that is no longer in currentFiles — the stale metadata left
// behind by a branch re-publish after a file was deleted or renamed (spec
// item: branch re-publish removes stale metadata). Every candidate key is
// hard-guarded by isSafeStaleObjectKey before the delete is ever issued to
// s5cmd. The delete itself passes --raw: without it, s5cmd rm expands
// wildcard characters (*, ?, [...]) in the key, so a metadata file
// legitimately named e.g. "run[1].json" would otherwise make this issue a
// glob delete under dst instead of removing exactly that one key. A
// NoSuchKey failure (the object already absent — e.g. a previous attempt
// already deleted it, or it was only ever a crash-recovery union entry
// that was never actually uploaded, see the ..publishing fallback) is not
// an error: deleting an already-absent key is idempotent.
func deleteStaleInstanceObjects(s5cmd, dst string, prevFiles, currentFiles []string) error {
	current := make(map[string]bool, len(currentFiles))
	for _, f := range currentFiles {
		current[f] = true
	}
	for _, key := range prevFiles {
		if current[key] {
			continue
		}
		if err := isSafeStaleObjectKey(key); err != nil {
			return err
		}
		url := dst + key
		r := runS5cmd(s5cmd, "rm", "--raw", url)
		if r.code != 0 {
			msg := sanitizeMsg(string(r.stderr))
			if !isNotFoundS5cmdError(msg) {
				return fmt.Errorf("s5cmd rm failed for stale object %s: %s", url, msg)
			}
		}
	}
	return nil
}

// checkInstanceSourceRepoGuard enforces the source-repo guard (spec
// "Source-repo guard on every role") for a role: instance publish, with a
// crash-recovery fallback: when the completion marker is absent,
// checkSourceRepoGuard alone would see "no record" and let any
// repository through, even if a PREVIOUS branch publish had already
// recorded a source repository before dying between deleting the previous
// ..complete and writing the new one. Falling back to ..publishing (only
// ever written by a branch publish, see doPublishInstance) closes that
// window. Tags never pre-delete/write ..publishing, so for a tag this is
// exactly checkSourceRepoGuard against markerURL alone.
func checkInstanceSourceRepoGuard(s5cmd, refKind, currentRepoURL, markerURL, publishingURL string, force bool) error {
	prevRepo, found, err := readSourceRepoRecord(s5cmd, markerURL, extractMarkerSourceRepo)
	if err != nil {
		return err
	}
	if !found && refKind != "tag" {
		prevRepo, found, err = readSourceRepoRecord(s5cmd, publishingURL, extractMarkerSourceRepo)
		if err != nil {
			return err
		}
	}
	if !found {
		return nil
	}
	if normalizeRepoURL(prevRepo) == normalizeRepoURL(currentRepoURL) {
		return nil
	}
	if force {
		return nil
	}
	return fmt.Errorf("refusing to publish: this version was already published from %q, but this is %q — pass --force to publish anyway", normalizeRepoURL(prevRepo), normalizeRepoURL(currentRepoURL))
}

// unionFiles returns the sorted, de-duplicated union of a and b.
func unionFiles(a, b []string) []string {
	seen := make(map[string]bool, len(a)+len(b))
	var out []string
	for _, list := range [][]string{a, b} {
		for _, f := range list {
			if !seen[f] {
				seen[f] = true
				out = append(out, f)
			}
		}
	}
	sort.Strings(out)
	return out
}

// buildInstanceIngestTarget builds the single target object sent to
// <api>/publish for a role: instance publish: the project-source/v1 pointer
// (location.exohub) plus the instance's own url and s3_location. Isolated
// in one function and covered by a golden test (see
// TestBuildInstancePublishPayload_Golden) so a future schema change
// (artifactdb-xp#22) is a deliberate, visible diff.
func buildInstanceIngestTarget(instanceURL, s3Location, atlasURL, projectID, version, sourceRepo string) map[string]interface{} {
	return map[string]interface{}{
		"url":         instanceURL,
		"type":        "artifactdb",
		"s3_location": s3Location,
		"location": map[string]interface{}{
			"type": "exohub",
			"exohub": map[string]interface{}{
				"atlas_url":   atlasURL,
				"project_id":  projectID,
				"version":     version,
				"source_repo": sourceRepo,
			},
		},
	}
}

// buildInstancePublishPayload builds the full /publish request body for a
// role: instance publish: bundle: false (no Atlas bundle ingestion for this
// call) and exactly the one target from buildInstanceIngestTarget.
// repoURL and s3Location fill the API's required top-level PublishRequest
// fields (repo_url, s3_location): the normalized source repository (the same
// value as the target's location.exohub.source_repo) and the instance ingest
// location (the same as the target's s3_location, which takes precedence
// server-side when set). Without them the API rejects the request schema
// ("body.repo_url / body.s3_location: Field required").
func buildInstancePublishPayload(projectID, repoURL, commit, refName, s3url, s3Location string, force bool, target map[string]interface{}) map[string]interface{} {
	payload := map[string]interface{}{
		"project_id":  projectID,
		"repo_url":    repoURL,
		"ref":         commit,
		"ref_name":    refName,
		"s3_location": s3Location,
		"s3url":       s3url,
		"bundle":      false,
		"targets":     []map[string]interface{}{target},
	}
	// force is only added when true, so the payload stays byte-identical to
	// pre-source-repo-guard requests for a non-forced publish.
	if force {
		payload["force"] = true
	}
	return payload
}

// publishInstanceNotify POSTs payload to <apiBase>/publish with the caller's
// ExoHub token. It never contacts the instance directly — the ExoHub API
// forwards to <instance url>/project/ingest (spec "Instance ingest
// forwarded by the ExoHub API").
func publishInstanceNotify(apiBase string, payload map[string]interface{}) ([]byte, error) {
	endpoint := strings.TrimRight(apiBase, "/") + "/publish"

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	debugStderr("publish instance: POST %s", endpoint)
	debugStderr("publish instance: payload=%s", string(body))

	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token := loadExoToken(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return respBody, &apiError{StatusCode: resp.StatusCode, Body: respBody}
	}
	return respBody, nil
}

// instancePublishJSONError mirrors publishJSONError for the instance
// --json result's "error" object: {code, message, allowed}.
type instancePublishJSONError struct {
	Code    string   `json:"code"`
	Message string   `json:"message"`
	Allowed []string `json:"allowed,omitempty"`
}

// instancePublishJSONResult is the single-line JSON object --json prints to
// stdout after a `publish --role instance` attempt: {published, s3_location,
// skipped, skip_reason, error}, distinct from the atlas role's
// publishJSONResult shape. skipped/skip_reason distinguish a ref with no
// .artifactdb/ (published: false, skipped: true, skip_reason:
// "no_artifactdb") from both a successful publish and a failure, so
// automation doesn't count it as an ingest.
type instancePublishJSONResult struct {
	Published  bool                      `json:"published"`
	S3Location string                    `json:"s3_location"`
	Skipped    bool                      `json:"skipped"`
	SkipReason string                    `json:"skip_reason,omitempty"`
	Error      *instancePublishJSONError `json:"error,omitempty"`
}

func printInstancePublishJSON(result instancePublishJSONResult) {
	data, err := json.Marshal(result)
	if err != nil {
		return
	}
	fmt.Println(string(data))
}

// instanceJSONErrorFromBody mirrors publishJSONErrorFromBody for the
// instance role's error shape.
func instanceJSONErrorFromBody(body []byte) *instancePublishJSONError {
	if detail, ok := parseStructuredPublishError(body); ok {
		return &instancePublishJSONError{Code: detail.Code, Message: detail.Message, Allowed: detail.Allowed}
	}
	return &instancePublishJSONError{Message: describePublishError(body)}
}

// instancePublishArgs holds the --role instance flags.
type instancePublishArgs struct {
	s3url           string
	artifactdbDir   string
	instanceURL     string
	atlasURL        string
	sourceS3URL     string
	projectID       string
	refName         string
	apiURL          string
	grants          bool
	grantsAccountID string
	refKind         string
	commit          string
	repoURL         string
	force           bool
	outputJSON      bool
	excludes        []string
}

// doPublishInstance implements `publish --role instance`: prefix guard, tag
// immutability, grants credentials, resolve the tenant's metadata folder,
// upload .artifactdb/ + permissions, write the completion marker last, then
// forward the ingest through the ExoHub API. Every step is gated on the
// previous one succeeding; a refusal writes nothing. It returns the
// --json result and a nil error on success, or a non-nil error (with the
// result's Published left false) on any failure — so the caller controls
// process exit and stderr presentation (see runPublishInstance) while this
// function stays testable without os.Exit.
func doPublishInstance(a instancePublishArgs) (instancePublishJSONResult, error) {
	if a.s3url == "" || a.projectID == "" || a.refName == "" || a.instanceURL == "" {
		return instancePublishJSONResult{}, fmt.Errorf("--s3url, --project-id, --ref-name and --url are required for --role instance")
	}

	s5 := envOr("S5CMD_BIN", "s5cmd")

	if err := validateInstancePrefix(a.s3url, a.projectID); err != nil {
		return instancePublishJSONResult{}, err
	}

	versionRoot := strings.TrimRight(a.s3url, "/") + "/" + a.refName
	markerURL := versionRoot + "/..complete"
	publishingURL := versionRoot + "/..publishing"

	// Fetch credentials BEFORE the tag-immutability read below: that read
	// uses s5cmd and must run with credentials in place, or a
	// credentials/access failure would otherwise look just like "no
	// record" and the check would silently never apply.
	if a.grants {
		fmt.Fprintln(os.Stderr, "🔑  Fetching S3 credentials via grants")
		if err := fetchGrantsCredentialsForAccount(a.s3url, a.grantsAccountID, a.sourceS3URL); err != nil {
			return instancePublishJSONResult{}, err
		}
	}

	if err := checkTagImmutability(s5, a.refKind, a.refName, a.commit, markerURL, extractMarkerCommit); err != nil {
		return instancePublishJSONResult{}, err
	}

	// Falls back to ..publishing when ..complete is absent, so a crash
	// between the previous marker's pre-delete and the new marker's write
	// doesn't leave this guard with nothing to compare against (which would
	// let any repository through).
	if err := checkInstanceSourceRepoGuard(s5, a.refKind, a.repoURL, markerURL, publishingURL, a.force); err != nil {
		return instancePublishJSONResult{}, err
	}

	if a.artifactdbDir == "" {
		fmt.Fprintf(os.Stderr, "ℹ️  No .artifactdb/ for ref %q, skipping instance publish\n", a.refName)
		return instancePublishJSONResult{Skipped: true, SkipReason: "no_artifactdb"}, nil
	}

	if err := validateArtifactdbTree(a.artifactdbDir); err != nil {
		return instancePublishJSONResult{}, err
	}

	// Load (parse + validate) .exohub/permissions now, before any upload —
	// an invalid permissions file must stop the publish before anything is
	// written, not after the .artifactdb/ sync. Uploaded later, once the
	// destination is known.
	permDoc, err := loadPermissionsDocument(".")
	if err != nil {
		return instancePublishJSONResult{}, fmt.Errorf("permissions validation failed: %w", err)
	}

	bucket, _, err := parseS3url(a.s3url)
	if err != nil {
		return instancePublishJSONResult{}, err
	}

	metaFolder, err := fetchInstanceMetaFolder(a.instanceURL, bucket)
	if err != nil {
		return instancePublishJSONResult{}, err
	}

	dst := versionRoot + "/"
	if metaFolder != "" {
		dst = versionRoot + "/" + strings.Trim(metaFolder, "/") + "/"
	}
	s3Location := dst

	fmt.Fprintf(os.Stderr, "📊  Project: %s, Version: %s\n", orange(a.projectID), orange(a.refName))
	fmt.Fprintf(os.Stderr, "☁️  Target: %s\n", yellow(dst))

	// A branch is mutable; a tag never reaches here with a different commit
	// (checkTagImmutability refuses that above), so the stale-metadata
	// cleanup below, and the ..publishing crash-recovery marker, only ever
	// apply when refKind != "tag".
	isBranch := a.refKind != "tag"

	var prevMarker *completionMarker
	var publishingRecord *completionMarker
	if isBranch {
		prevMarker, err = readCompletionMarker(s5, markerURL)
		if err != nil {
			return instancePublishJSONResult{S3Location: s3Location}, err
		}
		if prevMarker == nil {
			// ..complete is absent: either this is the first publish ever, or
			// a previous attempt died after deleting it. ..publishing (fail-
			// closed read, like readCompletionMarker) tells the two apart.
			publishingRecord, err = readCompletionMarker(s5, publishingURL)
			if err != nil {
				return instancePublishJSONResult{S3Location: s3Location}, err
			}
		}

		// The local tree, listed BEFORE the pre-delete/sync below, so
		// ..publishing's snapshot reflects exactly what this attempt is
		// about to try to upload, independently of whether the sync itself
		// succeeds. Excluded files are filtered out: they are never uploaded,
		// so they must never appear in this snapshot either.
		localFilesBeforeSync, err := listArtifactdbFiles(a.artifactdbDir)
		if err != nil {
			return instancePublishJSONResult{S3Location: s3Location}, fmt.Errorf("listing local files failed: %w", err)
		}
		localFilesBeforeSync, _ = partitionExcludedFiles(localFilesBeforeSync, a.excludes)
		var priorFiles []string
		if prevMarker != nil {
			priorFiles = prevMarker.Files
		} else if publishingRecord != nil {
			priorFiles = publishingRecord.Files
		}
		publishing := completionMarker{
			Version:    a.refName,
			Commit:     a.commit,
			ExoVersion: appVersion,
			SourceRepo: normalizeRepoURL(a.repoURL),
			Files:      unionFiles(priorFiles, localFilesBeforeSync),
		}
		// Written BEFORE the previous ..complete is deleted: if this
		// process now dies, the NEXT attempt's checkInstanceSourceRepoGuard
		// and stale-object baseline both fall back to this record instead
		// of finding nothing.
		if err := writeCompletionMarker(s5, publishingURL, publishing); err != nil {
			return instancePublishJSONResult{S3Location: s3Location}, fmt.Errorf("writing ..publishing marker failed: %w", err)
		}

		if prevMarker != nil {
			// Delete the previous marker BEFORE uploading, so a half-updated
			// version is never left looking complete if this process dies
			// mid-sync.
			if r := runS5cmd(s5, "rm", "--raw", markerURL); r.code != 0 {
				return instancePublishJSONResult{S3Location: s3Location}, fmt.Errorf("deleting previous marker failed: %s", sanitizeMsg(string(r.stderr)))
			}
		}
	}

	// currentFiles (what gets uploaded, and what the "..complete" marker's
	// Files/Objects must reflect) and excludedFiles (self-healed below,
	// regardless of whether they were ever published before) are computed
	// with our own matcher BEFORE any s5cmd call: s5cmd has its own
	// wildcard dialect, matched by its own rules, which can disagree with
	// matchAnnexGlob on what a given exclude: glob matches (exo-cli#202
	// review) — so delegating the exclusion to `s5cmd sync --exclude` could
	// silently upload (or drop) a file our matcher disagreed with, leaving
	// "..complete" wrong either way. Instead, stageKeptFiles stages exactly
	// currentFiles into a fresh directory and that is what gets synced, with
	// no --exclude passed to s5cmd at all.
	allLocalFiles, err := listArtifactdbFiles(a.artifactdbDir)
	if err != nil {
		return instancePublishJSONResult{S3Location: s3Location}, fmt.Errorf("listing local files failed: %w", err)
	}
	currentFiles, excludedFiles := partitionExcludedFiles(allLocalFiles, a.excludes)

	uploadDir := a.artifactdbDir
	if len(excludedFiles) > 0 {
		stagedDir, stageCleanup, err := stageKeptFiles(a.artifactdbDir, currentFiles)
		if err != nil {
			return instancePublishJSONResult{S3Location: s3Location}, fmt.Errorf("staging kept files failed: %w", err)
		}
		defer stageCleanup()
		uploadDir = stagedDir
	}

	count, err := runS5cmdSync(s5, strings.TrimRight(uploadDir, "/")+"/", dst)
	if err != nil {
		return instancePublishJSONResult{S3Location: s3Location}, fmt.Errorf("artifactdb upload failed: %w", err)
	}
	fmt.Fprintf(os.Stderr, "\r🔄  Synced %d file(s)              \n", count)

	// Self-heal (exo-cli#202, like exo-cli#177's): delete any previously
	// published copy of a now-excluded file from the instance version
	// prefix, so it stops being crawled. Runs for both branches and tags —
	// a re-publish of an existing tag on the same commit passes tag
	// immutability above and must still clean this up — and before
	// "..complete" is (re)written.
	if err := deleteExcludedInstanceObjects(s5, dst, excludedFiles); err != nil {
		return instancePublishJSONResult{S3Location: s3Location}, fmt.Errorf("deleting excluded metadata failed: %w", err)
	}

	if isBranch {
		// The stale-cleanup baseline is the previous ..complete's files
		// when it exists, or ..publishing's (a safe superset, see above)
		// when it doesn't — recovering exactly the case a bare pre-delete
		// would otherwise lose. A record that exists but predates
		// per-file tracking (no "files" list) is reported and skipped
		// rather than guessed at.
		var baseline []string
		haveBaseline := false
		switch {
		case prevMarker != nil && prevMarker.Files != nil:
			baseline, haveBaseline = prevMarker.Files, true
		case prevMarker != nil:
			fmt.Fprintln(os.Stderr, "ℹ️  Previous marker predates per-file tracking; skipping stale-object cleanup")
		case publishingRecord != nil && publishingRecord.Files != nil:
			baseline, haveBaseline = publishingRecord.Files, true
		case publishingRecord != nil:
			fmt.Fprintln(os.Stderr, "ℹ️  Previous ..publishing marker predates per-file tracking; skipping stale-object cleanup")
		}
		if haveBaseline {
			// Excluded files were already deleted above (deleteExcludedInstanceObjects);
			// drop them from the baseline so they aren't deleted twice here.
			baseline, _ = partitionExcludedFiles(baseline, a.excludes)
			if err := deleteStaleInstanceObjects(s5, dst, baseline, currentFiles); err != nil {
				return instancePublishJSONResult{S3Location: s3Location}, fmt.Errorf("deleting stale metadata failed: %w", err)
			}
		}
	}

	permDst := strings.TrimRight(a.s3url, "/") + "/..meta/permissions.json"
	if err := uploadPermissionsDocument(s5, permDst, permDoc); err != nil {
		return instancePublishJSONResult{S3Location: s3Location}, fmt.Errorf("permissions upload failed: %w", err)
	}

	marker := completionMarker{Version: a.refName, Commit: a.commit, Objects: count, ExoVersion: appVersion, SourceRepo: normalizeRepoURL(a.repoURL), Files: currentFiles}
	if err := writeCompletionMarker(s5, markerURL, marker); err != nil {
		return instancePublishJSONResult{S3Location: s3Location}, fmt.Errorf("writing completion marker failed: %w", err)
	}
	fmt.Fprintf(os.Stderr, "✅  Marker written: %s\n", dim(markerURL))

	if isBranch {
		// Only after the new ..complete is safely in place. Best-effort: a
		// leftover ..publishing is inert once ..complete exists again (the
		// guard and baseline above only ever consult it when ..complete is
		// missing), so a failure here doesn't block the publish.
		if r := runS5cmd(s5, "rm", "--raw", publishingURL); r.code != 0 {
			msg := sanitizeMsg(string(r.stderr))
			if !isNotFoundS5cmdError(msg) {
				fmt.Fprintf(os.Stderr, "⚠️  Could not remove %s: %s\n", publishingURL, msg)
			}
		}
	}

	target := buildInstanceIngestTarget(a.instanceURL, s3Location, a.atlasURL, a.projectID, a.refName, normalizeRepoURL(a.repoURL))
	payload := buildInstancePublishPayload(a.projectID, normalizeRepoURL(a.repoURL), a.commit, a.refName, a.s3url, s3Location, a.force, target)

	respBody, err := publishInstanceNotify(a.apiURL, payload)
	if err != nil {
		var apiErr *apiError
		if errors.As(err, &apiErr) {
			return instancePublishJSONResult{S3Location: s3Location, Error: instanceJSONErrorFromBody(apiErr.Body)},
				fmt.Errorf("%s", describePublishError(apiErr.Body))
		}
		return instancePublishJSONResult{S3Location: s3Location}, fmt.Errorf("ExoHub API notification failed: %w", err)
	}

	var resp struct {
		Results []struct {
			URL    string `json:"url"`
			Status string `json:"status"`
			Detail string `json:"detail,omitempty"`
		} `json:"results"`
	}
	if json.Unmarshal(respBody, &resp) == nil {
		for _, r := range resp.Results {
			if r.Status != "ok" {
				return instancePublishJSONResult{S3Location: s3Location}, fmt.Errorf("instance ingest failed: %s %s", r.URL, r.Detail)
			}
		}
	}

	fmt.Fprintf(os.Stderr, "📡  Notified ExoHub API (project: %s, version: %s)\n", orange(a.projectID), orange(a.refName))
	return instancePublishJSONResult{Published: true, S3Location: s3Location}, nil
}

// runPublishInstance is the publish subcommand entry point for --role
// instance: run doPublishInstance, report the result, and exit non-zero on
// any error.
func runPublishInstance(a instancePublishArgs) {
	result, err := doPublishInstance(a)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌  Error: %v\n", err)
		result.Published = false
		if result.Error == nil {
			result.Error = &instancePublishJSONError{Message: err.Error()}
		}
		if a.outputJSON {
			printInstancePublishJSON(result)
		}
		os.Exit(1)
	}
	if a.outputJSON {
		printInstancePublishJSON(result)
	}
}
