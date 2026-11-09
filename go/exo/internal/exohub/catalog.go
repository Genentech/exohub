package exohub

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	exocontext "github.com/Genentech/exohub/go/exo/commands/context"
	"github.com/Genentech/exohub/go/exo/internal/defaults"
	"github.com/Genentech/exohub/go/exo/internal/download"
	"github.com/Genentech/exohub/go/exo/internal/pathglob"
)

// ResolveCatalogURL returns the catalog URL to resolve exohub: references
// against: EXOHUB_CATALOG_URL, then the current exo context, then the
// built-in default (same precedence as the MCP catalog tools).
func ResolveCatalogURL() string {
	if envURL := os.Getenv("EXOHUB_CATALOG_URL"); envURL != "" {
		return strings.TrimRight(envURL, "/")
	}
	if ctx, err := exocontext.GetCurrentContext(); err == nil && ctx.CatalogURL != "" {
		return strings.TrimRight(ctx.CatalogURL, "/")
	}
	return defaults.CatalogURL()
}

// ContentKind identifies how a resolved file's content is stored in the
// source project.
type ContentKind int

const (
	// ContentGit is a plain git-tracked file: the source of truth is the
	// pinned commit's git blob, there is no annex key or location.
	ContentGit ContentKind = iota
	// ContentAnnex is a git-annex tracked file, identified by key/backend
	// and retrievable from one or more Locations.
	ContentAnnex
)

// Location is one storage location for an annexed file (design D2/D3): the
// annex S3 prefix and chunk layout, or an export path.
type Location struct {
	Type       string // "annex" or "export"
	Remote     string
	S3URL      string
	ExportPath string
	ChunkSize  int64 // 0 if the location is not chunked
	// ChunkKey is the first chunk's key, carrying the configured chunk-size
	// boundary ("-S<bytes>-C<n>-") that `git annex initremote chunk=...`
	// needs to match exactly (spike-zero-copy.md "Chunking must match
	// exactly"); it is distinct from ChunkSize, which is the chunk's actual
	// byte count and can be smaller than the configured boundary.
	ChunkKey  string
	Untrusted bool
}

// ResolvedFile is one file resolved from a catalog artifact document
// (design D2): everything `exo add exohub:...` needs to materialise it
// (task 3) and record its provenance locator (task 4).
type ResolvedFile struct {
	// Path is the file's path within the source project (catalog "path").
	Path string
	// Kind is how the content is stored in the source.
	Kind ContentKind
	// Key and Backend identify the annex content (Kind == ContentAnnex).
	Key     string
	Backend string
	Size    int64
	// Locations are the source's git-annex locations for the key
	// (Kind == ContentAnnex only).
	Locations []Location
	// GitOID is the git blob object id (Kind == ContentGit) resolved
	// directly from a local clone or a git remote; empty when the file came
	// from the catalog, which does not carry git blob ids.
	GitOID string
}

// Resolution is everything resolved for one `exohub:` reference: the
// pinned source (project, version, repository, commit) and the files it
// covers (one for an asset, several for a folder/project/collection).
type Resolution struct {
	// CatalogID is the readable catalog id used as the locator's `label`
	// (design D4), e.g. "myproject:data/file.csv@main" or "myproject@main".
	CatalogID string
	Project   string
	Version   string
	// RepoURL and Commit are the source's pinned git identity (design D2
	// D4): every file in Files came from this exact commit.
	RepoURL string
	Commit  string
	// TreeID is the git tree object id of ref's resolved root (design D5
	// "external pinned members in collection manifests"): the whole
	// project's root tree for a project reference, or the folder's own
	// tree for a folder reference. Empty for an asset or collection
	// reference, and for any project/folder reference resolved purely
	// from the catalog with no reachable local clone or git remote (the
	// catalog does not record tree ids).
	TreeID string
	Files  []ResolvedFile
	// LocalClonePath is the git directory (a caller-owned local clone, or a
	// temporary mirror fetched for this Resolution) to read ContentGit blob
	// content from (design D3 "Git file: copied into the working tree");
	// empty when only catalog metadata is available.
	LocalClonePath string
	// Note is an informational message the caller should surface to the
	// user (e.g. a local clone's branch pointing somewhere other than the
	// published commit actually used); empty when there is nothing to note.
	Note string

	cleanup func()
}

// Close releases any resources Resolve allocated for this Resolution (a
// temporary git mirror fetched from a git remote, see ReadGitFile and
// ResolveGitRemote). It is always safe to call.
func (res *Resolution) Close() error {
	if res.cleanup != nil {
		res.cleanup()
		res.cleanup = nil
	}
	return nil
}

// FileCatalogID returns the catalog identifier for one of res's resolved
// files (design D1/D4's `<project>:<path>@<version>` form), used by the Z3
// copy fallback to download it by id.
func (res *Resolution) FileCatalogID(f ResolvedFile) string {
	return fmt.Sprintf("%s:%s@%s", res.Project, f.Path, res.Version)
}

// catalogArtifact is the subset of an exohub-artifact/v1 document
// (schemas/exohub/exohub-artifact) ResolveCatalog needs: repo_url, the
// pinned commit, the content key/backend, and locations. download.
// FileMetadata already parses "locations" and "_extra"; repo_url, ref,
// key and backend are read directly off FileMetadata.Raw since they are
// not part of that struct.
type catalogArtifact struct {
	RepoURL string `json:"repo_url"`
	Ref     string `json:"ref"`
	Key     string `json:"key"`
	Backend string `json:"backend"`
}

func parseCatalogArtifact(raw map[string]interface{}) catalogArtifact {
	var a catalogArtifact
	if v, ok := raw["repo_url"].(string); ok {
		a.RepoURL = v
	}
	if v, ok := raw["ref"].(string); ok {
		a.Ref = v
	}
	if v, ok := raw["key"].(string); ok {
		a.Key = v
	}
	if v, ok := raw["backend"].(string); ok {
		a.Backend = v
	}
	return a
}

func toResolvedFile(path string, meta *download.FileMetadata) ResolvedFile {
	a := parseCatalogArtifact(meta.Raw)
	if a.Key == "" {
		return ResolvedFile{Path: path, Kind: ContentGit, Size: meta.Size}
	}

	locations := make([]Location, 0, len(meta.Locations))
	for _, l := range meta.Locations {
		loc := Location{Type: l.Type, Remote: l.Remote, S3URL: l.S3URL, ExportPath: l.ExportPath, Untrusted: l.Type == "export"}
		if len(l.Chunks) > 0 {
			loc.ChunkSize = l.Chunks[0].ChunkSize
			loc.ChunkKey = l.Chunks[0].ChunkKey
		}
		locations = append(locations, loc)
	}

	return ResolvedFile{
		Path:      path,
		Kind:      ContentAnnex,
		Key:       a.Key,
		Backend:   a.Backend,
		Size:      meta.Size,
		Locations: locations,
	}
}

// ResolveCatalogAsset resolves a single-asset or folder-entry exohub:
// reference against the catalog (design D2 "catalog" row): a search for
// `<project>:<path>@<version>` gives repo_url, the pinned commit, and the
// content key/backend/locations.
func ResolveCatalogAsset(catalogURL string, ref *Reference) (*Resolution, error) {
	if ref.Path == "" || ref.IsFolder() {
		return nil, fmt.Errorf("ResolveCatalogAsset requires a single-file reference, got %q", ref.Raw)
	}
	dl := download.New(catalogURL)
	catalogID := ref.CatalogID()
	meta, err := dl.GetFileMetadata(catalogID)
	if err != nil {
		return nil, catalogResolveError(ref, err)
	}
	a := parseCatalogArtifact(meta.Raw)
	if a.RepoURL == "" || a.Ref == "" {
		return nil, fmt.Errorf("catalog entry %q has no repo_url/ref: stale or non-exohub-artifact document", catalogID)
	}

	file := toResolvedFile(ref.Path, meta)
	return &Resolution{
		CatalogID: catalogID,
		Project:   ref.Project,
		Version:   ref.Version,
		RepoURL:   a.RepoURL,
		Commit:    a.Ref,
		Files:     []ResolvedFile{file},
	}, nil
}

// ResolveCatalogProject resolves a whole-project or folder exohub:
// reference against the catalog: every file of `<project>@<version>` whose
// path is under pathPrefix (empty for a whole-project import).
func ResolveCatalogProject(catalogURL string, ref *Reference, pathPrefix string) (*Resolution, error) {
	dl := download.New(catalogURL)
	infos, err := dl.SearchProjectFiles(ref.Project, ref.Version)
	if err != nil {
		return nil, catalogResolveError(ref, err)
	}
	if len(infos) == 0 {
		return nil, fmt.Errorf("no files found in the catalog for %s@%s", ref.Project, ref.Version)
	}

	res := &Resolution{CatalogID: ref.CatalogID(), Project: ref.Project, Version: ref.Version}
	for _, info := range infos {
		if pathPrefix != "" && !strings.HasPrefix(info.Path, pathPrefix) {
			continue
		}
		meta, err := dl.GetFileMetadata(info.ID)
		if err != nil {
			return nil, catalogResolveError(ref, err)
		}
		a := parseCatalogArtifact(meta.Raw)
		if res.RepoURL == "" {
			res.RepoURL, res.Commit = a.RepoURL, a.Ref
		} else if a.RepoURL != "" && (a.RepoURL != res.RepoURL || a.Ref != res.Commit) {
			return nil, fmt.Errorf("catalog returned inconsistent repo_url/ref across files of %s@%s: stale catalog", ref.Project, ref.Version)
		}
		res.Files = append(res.Files, toResolvedFile(info.Path, meta))
	}
	if len(res.Files) == 0 {
		return nil, fmt.Errorf("no files found under %q in %s@%s", pathPrefix, ref.Project, ref.Version)
	}
	return res, nil
}

// catalogResolveError wraps a catalog lookup failure with the context
// `exo add exohub:...` needs to diagnose it (design "clear errors for
// stale catalog / no source / no permission"): an access-denied response
// gets the same remediation hint `exo doctor` already gives for a missing
// S3 Access Grant (spike-zero-copy.md "Grants required").
func catalogResolveError(ref *Reference, err error) error {
	msg := err.Error()
	if strings.Contains(msg, "HTTP 401") || strings.Contains(msg, "HTTP 403") {
		return fmt.Errorf("no permission to resolve %q from the catalog (run \"exo login\", or ask the project owner for access): %w", ref.Raw, err)
	}
	if strings.Contains(msg, "HTTP 404") {
		return fmt.Errorf("%q was not found in the catalog (stale catalog, or the project/path/version does not exist): %w", ref.Raw, err)
	}
	return fmt.Errorf("resolving %q from the catalog: %w", ref.Raw, err)
}

// catalogManifest is the subset of a named collection's manifest
// (.exohub/collections/<name>.json, add-explicit-collections D2)
// ResolveCatalogCollection needs: literal paths/folders and glob patterns.
// Nested "collection:<name>" members and external pinned members (design
// D5) are not resolved here; they are out of scope for this change.
type catalogManifest struct {
	Paths []string `json:"paths"`
	Globs []string `json:"globs"`
}

// ResolveCatalogCollection resolves a `:collection:<name>` exohub:
// reference against the catalog (design D2): the collection's manifest is
// downloaded from `<project>@<version>` and its "paths"/"globs" are
// expanded against that project's catalog file listing.
func ResolveCatalogCollection(catalogURL string, ref *Reference) (*Resolution, error) {
	if !ref.IsCollection() {
		return nil, fmt.Errorf("ResolveCatalogCollection requires a collection reference, got %q", ref.Raw)
	}
	dl := download.New(catalogURL)
	infos, err := dl.SearchProjectFiles(ref.Project, ref.Version)
	if err != nil {
		return nil, catalogResolveError(ref, err)
	}

	manifestPath := ".exohub/collections/" + ref.CollectionName + ".json"
	byPath := make(map[string]string, len(infos))
	var manifestID string
	for _, info := range infos {
		byPath[info.Path] = info.ID
		if info.Path == manifestPath {
			manifestID = info.ID
		}
	}
	if manifestID == "" {
		return nil, fmt.Errorf("no collection %q found in %s@%s (missing %s)", ref.CollectionName, ref.Project, ref.Version, manifestPath)
	}

	// The manifest is a plain git-tracked file, not an annexed artifact, so
	// it has no catalog download location (download.IsDownloadable requires
	// Locations for an exohub-artifact document); its repo_url/ref metadata
	// is still available, so its content is read with a one-off shallow git
	// fetch, like any other ContentGit file (design D3 "Git file").
	meta, err := dl.GetFileMetadata(manifestID)
	if err != nil {
		return nil, catalogResolveError(ref, err)
	}
	a := parseCatalogArtifact(meta.Raw)
	if a.RepoURL == "" || a.Ref == "" {
		return nil, fmt.Errorf("catalog entry for collection manifest %s has no repo_url/ref: stale or non-exohub-artifact document", manifestPath)
	}
	mirror, err := fetchShallowMirror(a.RepoURL, a.Ref)
	if err != nil {
		return nil, fmt.Errorf("fetch collection manifest %s: %w", manifestPath, err)
	}
	defer os.RemoveAll(mirror)
	data, err := gitCatFile(mirror, a.Ref+":"+manifestPath)
	if err != nil {
		return nil, fmt.Errorf("read collection manifest %s: %w", manifestPath, err)
	}
	var manifest catalogManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("parse collection manifest %s: %w", manifestPath, err)
	}

	paths, err := expandCatalogManifest(infos, &manifest)
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("collection %q in %s@%s has no members", ref.CollectionName, ref.Project, ref.Version)
	}

	res := &Resolution{CatalogID: ref.CatalogID(), Project: ref.Project, Version: ref.Version}
	for _, p := range paths {
		id, ok := byPath[p]
		if !ok {
			return nil, fmt.Errorf("collection %q member %q not found in %s@%s", ref.CollectionName, p, ref.Project, ref.Version)
		}
		meta, err := dl.GetFileMetadata(id)
		if err != nil {
			return nil, catalogResolveError(ref, err)
		}
		a := parseCatalogArtifact(meta.Raw)
		if res.RepoURL == "" {
			res.RepoURL, res.Commit = a.RepoURL, a.Ref
		} else if a.RepoURL != "" && (a.RepoURL != res.RepoURL || a.Ref != res.Commit) {
			return nil, fmt.Errorf("catalog returned inconsistent repo_url/ref for collection %q members: stale catalog", ref.CollectionName)
		}
		res.Files = append(res.Files, toResolvedFile(p, meta))
	}
	return res, nil
}

// ResolveCollectionSourceID looks up ref's own collection id and the files
// it covers from the catalog's lineage metadata (design D5 amendment
// review point 3's catalog-only fallback, used only when no local clone or
// git remote is reachable to run the source's own collection resolution
// directly): a project document carries a `lineage.collections` entry
// (`kind: "named"`, matching `name`) once some covering provenance record
// exists in the source project, naming that collection's own id -- the
// same id the source project's own resolution would compute. ok is false
// (with a nil error) when no document in the project carries a matching
// entry yet.
func ResolveCollectionSourceID(catalogURL string, ref *Reference) (id string, files []ResolvedFile, ok bool, err error) {
	if !ref.IsCollection() {
		return "", nil, false, fmt.Errorf("ResolveCollectionSourceID requires a collection reference, got %q", ref.Raw)
	}
	dl := download.New(catalogURL)
	infos, err := dl.SearchProjectFiles(ref.Project, ref.Version)
	if err != nil {
		return "", nil, false, catalogResolveError(ref, err)
	}
	for _, info := range infos {
		meta, mErr := dl.GetFileMetadata(info.ID)
		if mErr != nil {
			continue
		}
		entryID, found := lineageCollectionID(meta.Raw, ref.CollectionName)
		if !found {
			continue
		}
		id = entryID
		files = append(files, toResolvedFile(info.Path, meta))
	}
	return id, files, id != "", nil
}

// lineageCollectionID returns the `id` of raw's `lineage.collections`
// entry (a catalog document's parsed JSON) naming the collection `name`
// with `kind: "named"`, if any.
func lineageCollectionID(raw map[string]interface{}, name string) (string, bool) {
	lineage, _ := raw["lineage"].(map[string]interface{})
	if lineage == nil {
		return "", false
	}
	collections, _ := lineage["collections"].([]interface{})
	for _, c := range collections {
		entry, _ := c.(map[string]interface{})
		if entry == nil {
			continue
		}
		kind, _ := entry["kind"].(string)
		if kind != "named" {
			continue
		}
		if n, _ := entry["name"].(string); n != name {
			continue
		}
		if id, _ := entry["id"].(string); id != "" {
			return id, true
		}
	}
	return "", false
}

// expandCatalogManifest expands a collection manifest's literal
// paths/folders and glob patterns against infos, the project's full catalog
// file listing.
func expandCatalogManifest(infos []download.FileInfo, m *catalogManifest) ([]string, error) {
	var all []string
	for _, g := range m.Globs {
		for _, info := range infos {
			ok, err := pathglob.Match(g, info.Path)
			if err != nil {
				return nil, fmt.Errorf("collection glob %q: %w", g, err)
			}
			if ok {
				all = append(all, info.Path)
			}
		}
	}
	for _, p := range m.Paths {
		if strings.HasPrefix(p, "collection:") {
			return nil, fmt.Errorf("nested collection member %q is not supported", p)
		}
		if strings.HasSuffix(p, "/") {
			for _, info := range infos {
				if strings.HasPrefix(info.Path, p) {
					all = append(all, info.Path)
				}
			}
			continue
		}
		all = append(all, p)
	}
	return dedupeSorted(all), nil
}

// dedupeSorted returns paths deduplicated and sorted, so a path matched by
// both a glob and a literal entry (or by two overlapping globs) appears
// once, in a deterministic order.
func dedupeSorted(paths []string) []string {
	seen := make(map[string]bool, len(paths))
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}
