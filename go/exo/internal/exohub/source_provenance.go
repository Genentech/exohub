package exohub

import (
	"fmt"
	"strings"
)

// recordDir mirrors provenance.RecordDir. internal/provenance imports
// commands/add, which imports this package, so this package cannot import
// internal/provenance without an import cycle; the record directory name
// is a stable constant shared across packages regardless.
const recordDir = ".exohub/provenance"

// FillGitOIDs resolves GitOID for every ContentGit file in res.Files that
// doesn't already have one (design D2: the catalog does not carry git blob
// ids), fetching a git mirror of res's pinned commit if res.LocalClonePath
// isn't already available.
func FillGitOIDs(res *Resolution) error {
	needsGit := false
	for _, f := range res.Files {
		if f.Kind == ContentGit && f.GitOID == "" {
			needsGit = true
			break
		}
	}
	if !needsGit {
		return nil
	}
	if err := EnsureLocalClone(res); err != nil {
		return err
	}
	for i, f := range res.Files {
		if f.Kind == ContentGit && f.GitOID == "" {
			entry, err := gitLsTreeEntry(res.LocalClonePath, res.Commit, f.Path)
			if err != nil {
				return fmt.Errorf("resolve git blob id for %s: %w", f.Path, err)
			}
			res.Files[i].GitOID = entry.oid
		}
	}
	return nil
}

// RootTreeID returns the git tree object id of path at res's pinned commit
// ("" for the whole project's root tree): the source entity id for a
// project or folder import (design D4), fetching a git mirror of the
// pinned commit if one isn't already available (the catalog does not carry
// tree ids).
func RootTreeID(res *Resolution, path string) (string, error) {
	if err := EnsureLocalClone(res); err != nil {
		return "", err
	}
	object := res.Commit + "^{tree}"
	if path != "" {
		object = res.Commit + ":" + strings.TrimSuffix(path, "/")
	}
	out, err := gitRun(res.LocalClonePath, "rev-parse", "--verify", object)
	if err != nil {
		return "", fmt.Errorf("resolve tree id for %q: %w", path, err)
	}
	return strings.TrimSpace(out), nil
}

// SourceRecordFiles returns the repo-relative path and raw content of every
// record file under .exohub/provenance/ at res's pinned commit (design D4
// Q3): the caller parses and filters these to the records that directly
// cover the imported assets, since this package does not understand the
// provenance record format (see the package-level comment on recordDir).
func SourceRecordFiles(res *Resolution) (map[string][]byte, error) {
	if err := EnsureLocalClone(res); err != nil {
		return nil, err
	}
	paths, err := gitLsTreePaths(res.LocalClonePath, res.Commit, recordDir+"/")
	if err != nil {
		return nil, err
	}
	out := make(map[string][]byte, len(paths))
	for _, p := range paths {
		data, err := gitCatFile(res.LocalClonePath, res.Commit+":"+p)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", p, err)
		}
		out[p] = data
	}
	return out, nil
}
