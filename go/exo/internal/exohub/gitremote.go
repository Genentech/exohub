package exohub

import (
	"fmt"
	"os"
)

// ResolveGitRemote resolves ref directly against a git remote (design D2
// "git remote" row): it fetches only the pinned rev from repoURL into a
// temporary bare mirror (no checkout, no other history), then reads the
// tree the same way as a local clone.
func ResolveGitRemote(repoURL, rev string, ref *Reference) (*Resolution, error) {
	dir, err := fetchShallowMirror(repoURL, rev)
	if err != nil {
		return nil, err
	}

	commit, err := gitRevParseCommit(dir, "FETCH_HEAD_PINNED")
	if err != nil {
		os.RemoveAll(dir)
		return nil, fmt.Errorf("resolve %q at %s: %w", rev, repoURL, err)
	}

	files, err := resolveTreeFiles(dir, commit, ref)
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}

	var treeID string
	if ref.IsProject() || ref.IsFolder() {
		treeID, err = gitTreeOID(dir, commit, ref.Path)
		if err != nil {
			os.RemoveAll(dir)
			return nil, err
		}
	}

	res := &Resolution{
		CatalogID:      ref.CatalogID(),
		Project:        ref.Project,
		Version:        ref.Version,
		RepoURL:        repoURL,
		Commit:         commit,
		TreeID:         treeID,
		LocalClonePath: dir,
		Files:          files,
	}
	res.cleanup = func() { os.RemoveAll(dir) }
	return res, nil
}

// fetchShallowMirror fetches only rev from repoURL into a fresh temporary
// bare repository and returns its path (design D2: fetch only the pinned
// commit, no checkout, no other data). The caller is responsible for
// removing it (Resolution.Close, or directly on error before a Resolution
// exists).
func fetchShallowMirror(repoURL, rev string) (string, error) {
	dir, err := os.MkdirTemp("", "exo-exohub-git-*")
	if err != nil {
		return "", err
	}
	if _, err := gitRun("", "init", "--bare", "-q", dir); err != nil {
		os.RemoveAll(dir)
		return "", fmt.Errorf("init temporary git mirror: %w", err)
	}
	if _, err := gitRun(dir, "fetch", "--depth=1", "-q", repoURL, rev); err != nil {
		os.RemoveAll(dir)
		return "", fmt.Errorf("fetch %q from %s: %w", rev, repoURL, err)
	}
	if _, err := gitRun(dir, "update-ref", "FETCH_HEAD_PINNED", "FETCH_HEAD"); err != nil {
		os.RemoveAll(dir)
		return "", fmt.Errorf("resolve fetched commit: %w", err)
	}
	return dir, nil
}

// EnsureLocalClone makes sure res.LocalClonePath names a readable git
// repository containing res's pinned commit, fetching one (and caching it
// on res for later calls, released by res.Close) if res was resolved from
// the catalog alone.
func EnsureLocalClone(res *Resolution) error {
	if res.LocalClonePath != "" {
		return nil
	}
	if res.RepoURL == "" || res.Commit == "" {
		return fmt.Errorf("no repository available for %s@%s", res.Project, res.Version)
	}
	dir, err := fetchShallowMirror(res.RepoURL, res.Commit)
	if err != nil {
		return err
	}
	res.LocalClonePath = dir
	prev := res.cleanup
	res.cleanup = func() {
		if prev != nil {
			prev()
		}
		os.RemoveAll(dir)
	}
	return nil
}

// ReadGitFile returns the content of path at res's pinned commit (design D3
// "Git file: copied into the working tree"): read directly from
// res.LocalClonePath when one is already available, otherwise fetched once
// (and cached on res for later calls, released by res.Close) from
// res.RepoURL.
func ReadGitFile(res *Resolution, path string) ([]byte, error) {
	if err := EnsureLocalClone(res); err != nil {
		return nil, fmt.Errorf("no repository to read %q from: %w", path, err)
	}
	return gitCatFile(res.LocalClonePath, res.Commit+":"+path)
}
