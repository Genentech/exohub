package provenance

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/Genentech/exohub/go/exo/internal/exohub"
	"github.com/Genentech/exohub/go/exo/internal/pathglob"
)

// CollectionsDir is the directory holding named collection manifests
// (design D2 of add-explicit-collections).
const CollectionsDir = ".exohub/collections"

// collectionNamePattern matches a valid named collection name (design D2):
// "[a-z0-9][a-z0-9-]*", the same style as other exo slugs.
var collectionNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// ValidCollectionName reports whether name matches the required pattern
// (design D2).
func ValidCollectionName(name string) bool {
	return collectionNamePattern.MatchString(name)
}

// CollectionManifest is one .exohub/collections/<name>.json file (design
// D2, D5): paths is a literal list (plain file paths, a folder path ending
// in "/", or "collection:<other-name>" for a nested named collection);
// globs uses the shared pathglob syntax and matches tracked file paths,
// not folders; members is a list of pinned ExoHub references (design D5
// of add-exohub-asset-import) naming a file, folder or whole project
// version of another project, or a collection defined in another project
// -- everything "exo add exohub:..." accepts, always pinned to an exact
// `@<version>`. An unpinned reference is a format error.
type CollectionManifest struct {
	Paths   []string `json:"paths,omitempty"`
	Globs   []string `json:"globs,omitempty"`
	Members []string `json:"members,omitempty"`
}

// CollectionManifestPath returns the repo-relative path of name's manifest
// file (design D2).
func CollectionManifestPath(name string) string {
	return CollectionsDir + "/" + name + ".json"
}

// LoadCollectionManifest loads and validates name's manifest at reader's
// checked state (design D2, D15): the worktree for ModeWorktree, the index
// for ModeStaged, and the ref's tree for ModeRef.
func LoadCollectionManifest(reader *Reader, name string) (*CollectionManifest, error) {
	if !ValidCollectionName(name) {
		return nil, fmt.Errorf("invalid collection name %q: must match [a-z0-9][a-z0-9-]*", name)
	}
	path := CollectionManifestPath(name)
	data, err := reader.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) || isMissingAtRevision(err) {
			return nil, fmt.Errorf("no collection manifest %s", path)
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var m CollectionManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if len(m.Paths) == 0 && len(m.Globs) == 0 && len(m.Members) == 0 {
		return nil, fmt.Errorf(`%s: at least one of "paths"/"globs"/"members" must be non-empty`, path)
	}
	for _, raw := range m.Members {
		if _, err := exohub.ParseReference(raw); err != nil {
			return nil, fmt.Errorf("%s: invalid member %q: %w", path, raw, err)
		}
	}
	return &m, nil
}

// CollectionMember is one member of a named collection's resolved member
// set (design D1, D3): Path is the repo-relative path (a file, or a folder
// with a trailing "/") it resolved from, or "" for a member that is itself
// a nested named collection (design D7).
type CollectionMember struct {
	Path string
	ID   string
}

// resolvedCollection is the result of resolving one named collection's
// manifest (design D1, D3, D6, D7, D5).
type resolvedCollection struct {
	// members is the collection's digest input list (design D1): one entry
	// per resolved "paths"/"globs"/"members" member, or (Path == "") one
	// pseudo-entry carrying a nested or external named collection's own
	// current id.
	members []CollectionMember
	// dropped is every "paths" entry (including a nested collection's own)
	// that no longer exists at the checked state (design D3), sorted.
	dropped []string
	// coveredIDs flattens every leaf content id and folder tree id this
	// collection covers for coverage purposes (design D6, D7): it recurses
	// into a nested named collection's own coveredIDs rather than stopping
	// at the nested collection's id, so a file nested two collections deep
	// is recognized as covered once the outermost collection's record is
	// covered, even though it is not itself one of members' digest inputs.
	coveredIDs map[string]bool
	// external is every distinct external collection reached while
	// resolving "members" (design D5), directly or through a nested local
	// collection, for BuildArtifactLineage to give the project/version it
	// was defined in once some record covers its digest (catalog_lineage.go).
	external []ExternalCollectionInfo
	// lockUpdates collects a refreshed CollectionLock for every collection
	// (this one, or a nested "collection:<name>" reached through paths)
	// whose "members" were live-resolved because refreshLock was true,
	// keyed by that collection's own name, for the caller
	// (exo provenance add --generated collection:<name>) to write and stage
	// (design D5 amendment "a lock file per collection with external
	// members"). Empty when refreshLock is false or no collection visited
	// has any "members".
	lockUpdates map[string]*CollectionLock
}

// ExternalCollectionInfo is one externally pinned collection member (design
// D5): a "members" entry shaped
// "exohub:<project>:collection:<name>@<version>", resolved from name's
// lock file (or, when refreshLock is true, live via exohub.Resolve) to its
// digest and the content ids it covers. compile.go's resolveNamedCollections
// surfaces these alongside locally defined collections so
// BuildArtifactLineage can set the optional project/version on a
// lineage.collections entry once some local record covers the external
// collection's digest (coveringProperties already recognizes
// hadPrimarySource, exo-cli#147).
type ExternalCollectionInfo struct {
	Name       string
	Project    string
	Version    string
	ID         string
	CoveredIDs map[string]bool
}

// CollectionLockPath returns the repo-relative path of name's lock file
// (design D5 amendment "a lock file per collection with external
// members"): committed next to the manifest, it records what each
// "members" reference last resolved to.
func CollectionLockPath(name string) string {
	return CollectionsDir + "/" + name + ".lock.json"
}

// CollectionLock is name's lock file (design D5 amendment): every
// "members" reference is resolved and recorded only when
// "exo provenance add --generated collection:<name>" runs (the only
// command that reaches the network for a named collection); every other
// command — validate, show, coverage, provenance.require, doctor,
// --revision-of collection:<name>, and exo bundle without --verify — reads
// this file and never re-resolves a "members" reference itself, so an
// offline repository, or one whose source project's branch version has
// since moved, still gets a stable, reproducible result.
type CollectionLock struct {
	Members map[string]CollectionLockEntry `json:"members"`
}

// CollectionLockEntry is one "members" reference's last resolution (design
// D5 amendment): Commit is the exact commit exohub.Resolve pinned at lock
// time (informational: the collection's own identity never depends on it
// directly, only ID and CoveredIDs do, so a lock entry stays valid even if
// the source project's tag is later deleted). CoveredIDs is every content
// id this one reference resolved to (a single id for an asset; every file
// under a folder or whole project; every file an external collection's own
// members currently resolve to) — ID is derived from CoveredIDs the same
// way resolveExternalMemberLive computed it when the lock was written, so
// CollectionLockEntry carries both rather than recomputing ID from
// CoveredIDs on every read (recomputing would not work for an external
// collection: the source project's own id is read from this entry, not
// rebuilt from CoveredIDs the way a flat asset/folder/project id can be,
// design D5 amendment review point 3).
type CollectionLockEntry struct {
	Commit     string   `json:"commit"`
	ID         string   `json:"id"`
	CoveredIDs []string `json:"coveredIds,omitempty"`
}

// loadCollectionLock loads name's lock file at reader's checked state. A
// missing lock file is not an error here (each "members" reference's own
// lookup reports the more actionable "no lock entry for %q, run ..."
// error); it is returned as an empty lock.
func loadCollectionLock(reader *Reader, name string) (*CollectionLock, error) {
	data, err := reader.ReadFile(CollectionLockPath(name))
	if err != nil {
		if os.IsNotExist(err) || isMissingAtRevision(err) {
			return &CollectionLock{Members: map[string]CollectionLockEntry{}}, nil
		}
		return nil, fmt.Errorf("read %s: %w", CollectionLockPath(name), err)
	}
	var lock CollectionLock
	if err := json.Unmarshal(data, &lock); err != nil {
		return nil, fmt.Errorf("parse %s: %w", CollectionLockPath(name), err)
	}
	if lock.Members == nil {
		lock.Members = map[string]CollectionLockEntry{}
	}
	return &lock, nil
}

// MarshalCollectionLock serializes lock the same way every lock file on
// disk is formatted (design D5 amendment), for a caller writing a
// refreshed lock (exo provenance add) to stage it as a git blob.
func MarshalCollectionLock(lock *CollectionLock) ([]byte, error) {
	data, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// ResolveCollection resolves name's current member set and id (design D1,
// D3, D5): local "paths"/"globs" members are resolved from reader's
// checked state as before. refreshLock controls how "members" entries are
// resolved and is the online/offline switch design D5's amendment
// requires: true live-resolves every "members" entry via exohub.Resolve
// (network) and returns the refreshed lock(s) in lockUpdates for the
// caller to write and stage — only "exo provenance add --generated
// collection:<name>" passes true; false (every other caller, including a
// plain --revision-of collection:<name> at the current checked state and
// a historical "<ref>:collection:<name>") resolves every "members" entry
// from name's already-committed lock file only, never touching the
// network, and fails naming the write command when a reference has no
// lock entry yet.
//
// A "paths" entry that no longer exists at the checked state is dropped
// rather than treated as an error (returned in dropped, in no particular
// guaranteed order beyond being sorted); callers that must reject a
// dropped or modified member when writing a new record (design D3) do so
// themselves, since only they also need the worktree-vs-index check
// ResolveCollection does not perform. An unresolvable "members" entry
// (refreshLock true: unreachable; refreshLock false: no lock entry) is
// always an error, never dropped — unlike a "paths" entry there is no "no
// longer exists" case for a pinned external reference to fall into.
//
// Nested "collection:<name>" entries in paths are resolved recursively; a
// cycle through nested collections is reported as an error naming the
// cycle (design D3 "Cycles").
func ResolveCollection(reader *Reader, name string, refreshLock bool) (id string, members []CollectionMember, dropped []string, lockUpdates map[string]*CollectionLock, err error) {
	rc, err := resolveCollection(reader, name, map[string]bool{}, refreshLock)
	if err != nil {
		return "", nil, nil, nil, err
	}
	return collectionDigest(rc.members), rc.members, rc.dropped, rc.lockUpdates, nil
}

// ResolveCollectionCoverage resolves name's current id and flattened
// coverage set (design D6, D7, D5 amendment): always offline, reading any
// "members" entry from name's committed lock file (never resolving it over
// the network) — the only path coverage.go, lineage.go and compile.go's
// default (non---verify) bundle use. coveredIDs is every content id a
// tracked file's own content id, or an enclosing folder member's tree id,
// must match to be covered through name — directly, or through a nested
// named collection — when name's record is covered (coverage.go,
// lineage.go). external is every distinct external collection reached
// through name's "members", for compile.go's resolveNamedCollections.
func ResolveCollectionCoverage(reader *Reader, name string) (id string, coveredIDs map[string]bool, external []ExternalCollectionInfo, err error) {
	rc, err := resolveCollection(reader, name, map[string]bool{}, false)
	if err != nil {
		return "", nil, nil, err
	}
	return collectionDigest(rc.members), rc.coveredIDs, rc.external, nil
}

// ListCollectionNames returns the names of every valid named collection
// manifest tracked at reader's checked state
// (".exohub/collections/<name>.json", design D2), sorted: coverage.go and
// lineage.go use it to enumerate which named collections might cover a
// given path, since a manifest can declare any set of members, not
// necessarily something discoverable from the path itself the way a
// folder collection's enclosing directory is.
func ListCollectionNames(reader *Reader) ([]string, error) {
	entries, err := reader.FileEntries()
	if err != nil {
		return nil, err
	}
	prefix := CollectionsDir + "/"
	var names []string
	for path := range entries {
		rest, ok := strings.CutPrefix(path, prefix)
		if !ok {
			continue
		}
		name, ok := strings.CutSuffix(rest, ".json")
		if !ok || strings.Contains(name, "/") || !ValidCollectionName(name) {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

func collectionDigest(members []CollectionMember) string {
	ids := make([]string, len(members))
	for i, m := range members {
		ids[i] = m.ID
	}
	return CollectionID(ids)
}

func resolveCollection(reader *Reader, name string, visiting map[string]bool, refreshLock bool) (*resolvedCollection, error) {
	if visiting[name] {
		return nil, fmt.Errorf("collection cycle detected: collection:%s refers to itself (directly or through nested collections)", name)
	}
	visiting[name] = true
	defer delete(visiting, name)

	manifest, err := LoadCollectionManifest(reader, name)
	if err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	rc := &resolvedCollection{coveredIDs: map[string]bool{}}
	addMember := func(m CollectionMember) {
		if !seen[m.ID] {
			seen[m.ID] = true
			rc.members = append(rc.members, m)
		}
	}

	for _, p := range manifest.Paths {
		if nested, ok := strings.CutPrefix(p, "collection:"); ok {
			nestedRC, err := resolveCollection(reader, nested, visiting, refreshLock)
			if err != nil {
				return nil, fmt.Errorf("collection:%s: nested collection:%s: %w", name, nested, err)
			}
			rc.dropped = append(rc.dropped, nestedRC.dropped...)
			addMember(CollectionMember{ID: collectionDigest(nestedRC.members)})
			for id := range nestedRC.coveredIDs {
				rc.coveredIDs[id] = true
			}
			rc.external = append(rc.external, nestedRC.external...)
			for n, lock := range nestedRC.lockUpdates {
				if rc.lockUpdates == nil {
					rc.lockUpdates = map[string]*CollectionLock{}
				}
				rc.lockUpdates[n] = lock
			}
			continue
		}
		id, err := reader.ResolveContentID(p)
		if err != nil {
			if isMissingCollectionMember(err) {
				rc.dropped = append(rc.dropped, p)
				continue
			}
			return nil, fmt.Errorf("collection:%s: resolve member %s: %w", name, p, err)
		}
		addMember(CollectionMember{Path: p, ID: id})
		rc.coveredIDs[id] = true
	}

	if len(manifest.Globs) > 0 {
		entries, err := reader.FileEntries()
		if err != nil {
			return nil, fmt.Errorf("collection:%s: list tracked files: %w", name, err)
		}
		var matched []string
		for path := range entries {
			for _, pattern := range manifest.Globs {
				ok, err := pathglob.Match(pattern, path)
				if err != nil {
					return nil, fmt.Errorf("collection:%s glob %q: %w", name, pattern, err)
				}
				if ok {
					matched = append(matched, path)
					break
				}
			}
		}
		sort.Strings(matched)
		matchedEntries := make(map[string]FileEntry, len(matched))
		for _, p := range matched {
			matchedEntries[p] = entries[p]
		}
		ids, err := reader.AllContentIDs(matchedEntries)
		if err != nil {
			return nil, fmt.Errorf("collection:%s: resolve glob members: %w", name, err)
		}
		for _, p := range matched {
			addMember(CollectionMember{Path: p, ID: ids[p]})
			rc.coveredIDs[ids[p]] = true
		}
	}

	if len(manifest.Members) > 0 {
		var lock *CollectionLock
		if !refreshLock {
			lock, err = loadCollectionLock(reader, name)
			if err != nil {
				return nil, fmt.Errorf("collection:%s: %w", name, err)
			}
		} else {
			lock = &CollectionLock{Members: map[string]CollectionLockEntry{}}
		}
		for _, raw := range manifest.Members {
			var memberID string
			if refreshLock {
				var entry CollectionLockEntry
				memberID, entry, err = resolveExternalMemberLive(raw, rc)
				if err != nil {
					return nil, fmt.Errorf("collection:%s: member %q: %w", name, raw, err)
				}
				lock.Members[raw] = entry
			} else {
				memberID, err = resolveExternalMemberFromLock(name, raw, lock, rc)
				if err != nil {
					return nil, fmt.Errorf("collection:%s: member %q: %w", name, raw, err)
				}
			}
			addMember(CollectionMember{ID: memberID})
		}
		if refreshLock {
			if rc.lockUpdates == nil {
				rc.lockUpdates = map[string]*CollectionLock{}
			}
			rc.lockUpdates[name] = lock
		}
	}

	sort.Slice(rc.members, func(i, j int) bool { return rc.members[i].ID < rc.members[j].ID })
	sort.Strings(rc.dropped)
	return rc, nil
}

// resolveExternalMemberLive live-resolves one "members" entry over the
// network, exactly as "exo add exohub:..." resolves it (local clone ->
// catalog -> optional git remote, exohub.Resolve), for
// exo provenance add --generated collection:<name> to refresh its lock
// entry (design D5 amendment). It returns the same digest-input id
// resolveExternalMemberFromLock would read back from that entry, so a
// fresh lock immediately agrees with itself.
func resolveExternalMemberLive(raw string, rc *resolvedCollection) (memberID string, entry CollectionLockEntry, err error) {
	ref, err := exohub.ParseReference(raw)
	if err != nil {
		return "", CollectionLockEntry{}, err
	}

	// A collection member's own id is never recomputed flat from its
	// resolved files (design D5 amendment review point 3): that only
	// matches a source manifest that is a flat list of files, not one with
	// nested "collection:" entries or folder members. resolveExternalCollectionMember
	// locks the source project's own id instead.
	if ref.IsCollection() {
		return resolveExternalCollectionMember(ref, rc)
	}

	res, err := exohub.Resolve(ref, exohub.ResolveOptions{})
	if err != nil {
		return "", CollectionLockEntry{}, err
	}
	defer res.Close()

	ids := make([]string, 0, len(res.Files))
	for _, f := range res.Files {
		id, err := externalContentID(f)
		if err != nil {
			return "", CollectionLockEntry{}, err
		}
		ids = append(ids, id)
		rc.coveredIDs[id] = true
	}

	switch {
	case ref.IsAsset():
		if len(ids) == 0 {
			return "", CollectionLockEntry{}, fmt.Errorf("%s resolved no files", raw)
		}
		memberID = ids[0]
	default: // whole project or folder
		if res.TreeID == "" {
			return "", CollectionLockEntry{}, fmt.Errorf("resolving a whole project or folder reference's identity requires a reachable local clone or git remote for %s", ref.Project)
		}
		memberID = GitTreeEntityID(res.TreeID)
	}
	addExternalInfo(rc, ref, memberID, ids)
	return memberID, CollectionLockEntry{Commit: res.Commit, ID: memberID, CoveredIDs: ids}, nil
}

// resolveExternalCollectionMember locks ref's own collection id on the
// project it is defined in (design D5 amendment review point 3): never
// recomputed flat from ref's resolved files, since that only matches a
// flat source manifest. Preferred: a local clone or freshly fetched
// mirror of the source project lets this run the source's own offline
// ResolveCollectionCoverage over its own repository at the pinned commit
// (the same digest, with the source's own nested/folder members, the
// source project itself would compute), reading the source's own
// already-committed lock for any of its own external members rather than
// reaching the network again. Fallback: the catalog's own
// lineage.collections entry for the collection, read off whichever of the
// source project's documents already carries one. Otherwise it is a clear
// error -- never a flat recompute.
func resolveExternalCollectionMember(ref *exohub.Reference, rc *resolvedCollection) (memberID string, entry CollectionLockEntry, err error) {
	projectRef := &exohub.Reference{Raw: ref.Project + "@" + ref.Version, Project: ref.Project, Version: ref.Version}
	res, err := exohub.Resolve(projectRef, exohub.ResolveOptions{})
	if err != nil {
		return "", CollectionLockEntry{}, fmt.Errorf("collection:%s in %s@%s: resolve source project: %w", ref.CollectionName, ref.Project, ref.Version, err)
	}
	defer res.Close()

	if cloneErr := exohub.EnsureLocalClone(res); cloneErr == nil {
		reader := NewRefReader(res.LocalClonePath, res.Commit)
		id, coveredIDs, _, resolveErr := ResolveCollectionCoverage(reader, ref.CollectionName)
		if resolveErr != nil {
			return "", CollectionLockEntry{}, fmt.Errorf("collection:%s in %s@%s: %w", ref.CollectionName, ref.Project, ref.Version, resolveErr)
		}
		ids := make([]string, 0, len(coveredIDs))
		for id := range coveredIDs {
			ids = append(ids, id)
			rc.coveredIDs[id] = true
		}
		sort.Strings(ids)
		addExternalInfo(rc, ref, id, ids)
		return id, CollectionLockEntry{Commit: res.Commit, ID: id, CoveredIDs: ids}, nil
	}

	sourceID, files, ok, catalogErr := exohub.ResolveCollectionSourceID(exohub.ResolveCatalogURL(), ref)
	if catalogErr != nil {
		return "", CollectionLockEntry{}, fmt.Errorf("collection:%s in %s@%s: %w", ref.CollectionName, ref.Project, ref.Version, catalogErr)
	}
	if ok {
		ids := make([]string, 0, len(files))
		for _, f := range files {
			id, idErr := externalContentID(f)
			if idErr != nil {
				return "", CollectionLockEntry{}, idErr
			}
			ids = append(ids, id)
			rc.coveredIDs[id] = true
		}
		addExternalInfo(rc, ref, sourceID, ids)
		return sourceID, CollectionLockEntry{Commit: res.Commit, ID: sourceID, CoveredIDs: ids}, nil
	}

	return "", CollectionLockEntry{}, fmt.Errorf("collection:%s in %s@%s: locking its own id requires a reachable local clone or git remote for %s, or a catalog lineage.collections entry for it; neither was available",
		ref.CollectionName, ref.Project, ref.Version, ref.Project)
}

// resolveExternalMemberFromLock resolves one "members" entry from name's
// already-committed lock, fully offline (design D5 amendment: "offline
// validate uses the lock"). A reference with no lock entry is an error
// naming the command that writes one.
func resolveExternalMemberFromLock(name, raw string, lock *CollectionLock, rc *resolvedCollection) (string, error) {
	ref, err := exohub.ParseReference(raw)
	if err != nil {
		return "", err
	}
	entry, ok := lock.Members[raw]
	if !ok {
		return "", fmt.Errorf("no lock entry for %q in %s; run \"exo provenance add --generated collection:%s\" to resolve and lock it",
			raw, CollectionLockPath(name), name)
	}
	for _, id := range entry.CoveredIDs {
		rc.coveredIDs[id] = true
	}
	addExternalInfo(rc, ref, entry.ID, entry.CoveredIDs)
	return entry.ID, nil
}

// addExternalInfo records ref as an ExternalCollectionInfo on rc when it
// names a collection (design D5): the common bit shared by live and
// lock-based resolution.
func addExternalInfo(rc *resolvedCollection, ref *exohub.Reference, memberID string, coveredIDs []string) {
	if !ref.IsCollection() {
		return
	}
	covered := make(map[string]bool, len(coveredIDs))
	for _, id := range coveredIDs {
		covered[id] = true
	}
	rc.external = append(rc.external, ExternalCollectionInfo{
		Name:       ref.CollectionName,
		Project:    ref.Project,
		Version:    ref.Version,
		ID:         memberID,
		CoveredIDs: covered,
	})
}

// VerifyCollectionLock re-resolves every "members" entry name's manifest
// declares (and every nested local collection's, recursively) over the
// network and compares each against its current lock entry, for
// exo bundle --verify (design D5 amendment: "bundle ... re-resolves only
// with --verify, failing on a mismatch"). It reports the first mismatch or
// resolution failure as an error naming the reference; nil means every
// "members" entry's lock entry still matches what it resolves to now.
func VerifyCollectionLock(reader *Reader, name string) error {
	return verifyCollectionLock(reader, name, map[string]bool{})
}

func verifyCollectionLock(reader *Reader, name string, visiting map[string]bool) error {
	if visiting[name] {
		return fmt.Errorf("collection cycle detected: collection:%s refers to itself (directly or through nested collections)", name)
	}
	visiting[name] = true
	defer delete(visiting, name)

	manifest, err := LoadCollectionManifest(reader, name)
	if err != nil {
		return err
	}
	for _, p := range manifest.Paths {
		if nested, ok := strings.CutPrefix(p, "collection:"); ok {
			if err := verifyCollectionLock(reader, nested, visiting); err != nil {
				return err
			}
		}
	}
	if len(manifest.Members) == 0 {
		return nil
	}
	lock, err := loadCollectionLock(reader, name)
	if err != nil {
		return fmt.Errorf("collection:%s: %w", name, err)
	}
	for _, raw := range manifest.Members {
		locked, ok := lock.Members[raw]
		if !ok {
			return fmt.Errorf("collection:%s: no lock entry for %q in %s; run \"exo provenance add --generated collection:%s\" to resolve and lock it",
				name, raw, CollectionLockPath(name), name)
		}
		live, _, err := resolveExternalMemberLive(raw, &resolvedCollection{coveredIDs: map[string]bool{}})
		if err != nil {
			return fmt.Errorf("collection:%s: verify %q: %w", name, raw, err)
		}
		if live != locked.ID {
			return fmt.Errorf("collection:%s: %q now resolves to %s, but %s has %s; run \"exo provenance add --generated collection:%s\" to re-lock it",
				name, raw, live, CollectionLockPath(name), locked.ID, name)
		}
	}
	return nil
}

// externalContentID returns the content id of an externally resolved file
// (design D5): an annexed file's key, or a git-tracked file's blob id when
// one is known. A file resolved purely from the catalog carries no git
// blob id (the catalog does not record one), so that case is a clear
// error rather than a malformed identifier.
func externalContentID(f exohub.ResolvedFile) (string, error) {
	if f.Kind == exohub.ContentAnnex {
		return AnnexEntityID(f.Key), nil
	}
	if f.GitOID == "" {
		return "", fmt.Errorf("%s: resolving this git-tracked file's identity requires a reachable local clone or git remote (the catalog does not record git blob ids)", f.Path)
	}
	return GitBlobEntityID(f.GitOID), nil
}

// isMissingCollectionMember reports whether err from Reader.ResolveContentID
// means a declared collection member path does not exist at the checked
// state (design D3: "a paths entry that no longer exists at the checked
// state is dropped"), as opposed to some other resolution failure (for
// example a directory path missing its trailing "/").
func isMissingCollectionMember(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "not tracked in the index") ||
		strings.Contains(msg, "not present at") ||
		strings.Contains(msg, "not found")
}
