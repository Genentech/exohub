package exohub

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Genentech/exohub/go/exo/internal/pathglob"
)

// DiscoverLocalClone looks for a local clone of project (design D2 "local
// clone" row, the fast offline path): the directory named by
// EXOHUB_CLONE_<PROJECT> (project upper-cased, with every character outside
// [A-Z0-9] replaced by "_"), then an adjacent "../<project>" directory, if
// either is a usable git working tree. It returns "" when neither is found.
func DiscoverLocalClone(project string) string {
	if v := os.Getenv("EXOHUB_CLONE_" + sanitizeEnvName(project)); v != "" && isGitWorkTree(v) {
		return v
	}
	if adjacent := filepath.Join("..", project); isGitWorkTree(adjacent) {
		return adjacent
	}
	return ""
}

func sanitizeEnvName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

func isGitWorkTree(dir string) bool {
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return false
	}
	return exec.Command("git", "-C", dir, "rev-parse", "--is-inside-work-tree").Run() == nil
}

// fullOrShortSHAPattern matches a value that could be a git commit sha (not
// a branch/tag name), used by resolvePublishedCommit to decide whether it
// is safe to rev-parse ref.Version directly.
var fullOrShortSHAPattern = regexp.MustCompile(`^[0-9a-fA-F]{7,40}$`)

// resolvePublishedCommit resolves version against clonePath's *published*
// refs only (exo-cli#152 review: a local clone's own checked-out branch can
// have unpushed or unpublished commits, which must never be recorded as an
// import's source). It tries, in order: version's origin-tracking branch,
// a tag named version, then (only if version itself looks like a commit
// sha) the commit directly. If clonePath's own local branch of the same
// name points somewhere else, note reports that the published commit was
// used instead, so the caller can warn about it.
func resolvePublishedCommit(clonePath, version string) (commit, note string, err error) {
	if c, e := gitRevParseCommit(clonePath, "refs/remotes/origin/"+version); e == nil {
		commit = c
	} else if c, e := gitRevParseCommit(clonePath, "refs/tags/"+version); e == nil {
		commit = c
	} else if fullOrShortSHAPattern.MatchString(version) {
		if c, e := gitRevParseCommit(clonePath, version); e == nil {
			commit = c
		}
	}
	if commit == "" {
		return "", "", fmt.Errorf("not a published ref (refs/remotes/origin/%s or refs/tags/%s) or a commit sha", version, version)
	}
	if local, e := gitRevParseCommit(clonePath, "refs/heads/"+version); e == nil && local != commit {
		note = fmt.Sprintf("note: %s's local branch %q (%s) differs from the published commit %s; the published commit was used", clonePath, version, local, commit)
	}
	return commit, note, nil
}

// ResolveLocalClone resolves ref against a local clone at clonePath (design
// D2): the pinned version is resolved to its *published* commit (origin's
// tracking branch, a tag, or a literal commit sha; never the clone's own
// local branch tip, exo-cli#152 review) with no network access, and the
// commit's tree gives path -> key/blob.
func ResolveLocalClone(clonePath string, ref *Reference) (*Resolution, error) {
	commit, note, err := resolvePublishedCommit(clonePath, ref.Version)
	if err != nil {
		return nil, fmt.Errorf("%q is not a known published revision in %s: %w", ref.Version, clonePath, err)
	}

	files, err := resolveTreeFiles(clonePath, commit, ref)
	if err != nil {
		return nil, err
	}

	var treeID string
	if ref.IsProject() || ref.IsFolder() {
		treeID, err = gitTreeOID(clonePath, commit, ref.Path)
		if err != nil {
			return nil, err
		}
	}

	return &Resolution{
		CatalogID:      ref.CatalogID(),
		Project:        ref.Project,
		Version:        ref.Version,
		RepoURL:        gitRemoteURL(clonePath),
		Commit:         commit,
		TreeID:         treeID,
		LocalClonePath: clonePath,
		Files:          files,
		Note:           note,
	}, nil
}

// readLocalManifest reads and parses a named collection's manifest
// (.exohub/collections/<name>.json, add-explicit-collections D2) from dir's
// tree at commit.
func readLocalManifest(dir, commit, name string) (*catalogManifest, error) {
	data, err := gitCatFile(dir, commit+":.exohub/collections/"+name+".json")
	if err != nil {
		return nil, fmt.Errorf("collection %q: %w", name, err)
	}
	var m catalogManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("collection %q: parse manifest: %w", name, err)
	}
	return &m, nil
}

// expandLocalManifest expands a collection manifest's literal paths/folders
// and glob patterns against dir's tree at commit. Nested "collection:<name>"
// members (add-explicit-collections D7) are not supported here.
func expandLocalManifest(dir, commit string, m *catalogManifest) ([]string, error) {
	var all []string
	if len(m.Globs) > 0 {
		treePaths, err := gitLsTreePaths(dir, commit, "")
		if err != nil {
			return nil, err
		}
		for _, g := range m.Globs {
			for _, p := range treePaths {
				ok, err := pathglob.Match(g, p)
				if err != nil {
					return nil, fmt.Errorf("collection glob %q: %w", g, err)
				}
				if ok {
					all = append(all, p)
				}
			}
		}
	}
	for _, p := range m.Paths {
		if strings.HasPrefix(p, "collection:") {
			return nil, fmt.Errorf("nested collection member %q is not supported", p)
		}
		if strings.HasSuffix(p, "/") {
			sub, err := gitLsTreePaths(dir, commit, p)
			if err != nil {
				return nil, err
			}
			all = append(all, sub...)
			continue
		}
		all = append(all, p)
	}
	return dedupeSorted(all), nil
}
