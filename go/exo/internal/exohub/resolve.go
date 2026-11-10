package exohub

import (
	"fmt"
	"strings"
)

// ResolveOptions configures Resolve (design D2).
type ResolveOptions struct {
	// CatalogURL overrides the catalog endpoint (default: ResolveCatalogURL()).
	CatalogURL string
	// LocalClone is an explicit path to a local clone of ref's project. If
	// empty, Resolve looks for one via DiscoverLocalClone.
	LocalClone string
	// RepoURL, if set, lets Resolve fall back to a direct git-remote
	// resolution (design D2: "works for projects not in the catalog when
	// given a repo URL") when neither a local clone nor the catalog can
	// resolve ref.
	RepoURL string
	// Verify re-resolves through the git remote and fails on any mismatch
	// with the chosen resolution (design D2 Q1, --verify).
	Verify bool
}

// Resolve resolves ref to pinned content (design D2): a local clone is
// tried first (fast, offline, resolved to its *published* commit only,
// never an unpushed local branch tip), but whenever the catalog is also
// reachable its pinned commit is authoritative (exo-cli#152 review: a
// stale/unpushed local clone must never be recorded as an import's
// source); in that case the local clone, if any, is kept only as a
// content cache (its path is reused so materialisation can read from it
// without a network round trip, but its own Commit/Files are discarded).
// A direct git-remote fetch (opts.RepoURL) is the last resort, for
// projects not yet in the catalog. When none succeeds, the returned error
// names every source tried.
func Resolve(ref *Reference, opts ResolveOptions) (*Resolution, error) {
	var tried []string

	clonePath := opts.LocalClone
	if clonePath == "" {
		clonePath = DiscoverLocalClone(ref.Project)
	}
	var localRes *Resolution
	if clonePath != "" {
		res, err := ResolveLocalClone(clonePath, ref)
		if err == nil {
			localRes = res
		} else {
			tried = append(tried, fmt.Sprintf("local clone %s: %v", clonePath, err))
		}
	}

	catalogURL := opts.CatalogURL
	if catalogURL == "" {
		catalogURL = ResolveCatalogURL()
	}
	if res, err := resolveCatalog(catalogURL, ref); err == nil {
		// ResolveLocalClone never sets a cleanup function (its
		// LocalClonePath is a caller-owned directory, not a temporary
		// mirror), so reusing its path here and discarding localRes is
		// always safe.
		if localRes != nil {
			res.LocalClonePath = localRes.LocalClonePath
			res.Note = localRes.Note
			res.TreeID = localRes.TreeID
		}
		return finishResolve(res, ref, opts)
	} else {
		tried = append(tried, fmt.Sprintf("catalog: %v", err))
	}

	if localRes != nil {
		return finishResolve(localRes, ref, opts)
	}

	if opts.RepoURL != "" {
		res, err := ResolveGitRemote(opts.RepoURL, ref.Version, ref)
		if err == nil {
			return res, nil
		}
		tried = append(tried, fmt.Sprintf("git remote %s: %v", opts.RepoURL, err))
	}

	return nil, fmt.Errorf("could not resolve %q: tried %s", ref.Raw, strings.Join(tried, "; "))
}

// resolveCatalog dispatches to the catalog resolver matching ref's kind.
func resolveCatalog(catalogURL string, ref *Reference) (*Resolution, error) {
	switch {
	case ref.IsCollection():
		return ResolveCatalogCollection(catalogURL, ref)
	case ref.IsAsset():
		return ResolveCatalogAsset(catalogURL, ref)
	default: // whole project or folder
		return ResolveCatalogProject(catalogURL, ref, ref.Path)
	}
}

// finishResolve applies opts.Verify (design D2 Q1): res is re-resolved
// through its own git remote and compared, failing hard on any mismatch
// (spec "stale catalog").
func finishResolve(res *Resolution, ref *Reference, opts ResolveOptions) (*Resolution, error) {
	if !opts.Verify {
		return res, nil
	}
	if res.RepoURL == "" || res.Commit == "" {
		res.Close()
		return nil, fmt.Errorf("--verify requires a repository URL and commit, which %q did not resolve", ref.Raw)
	}
	verified, err := ResolveGitRemote(res.RepoURL, res.Commit, ref)
	if err != nil {
		res.Close()
		return nil, fmt.Errorf("--verify: could not check %s against git: %w", res.RepoURL, err)
	}
	defer verified.Close()
	if mismatch := compareResolvedFiles(res.Files, verified.Files); mismatch != "" {
		res.Close()
		return nil, fmt.Errorf("--verify: %s", mismatch)
	}
	return res, nil
}

// compareResolvedFiles reports the first mismatch between a (the chosen
// resolution) and b (the git-remote resolution), or "" if they agree. Git
// files resolved from the catalog carry no blob id to compare (the catalog
// does not record one), so only their presence and kind are checked in that
// case; annex files are always compared by key.
func compareResolvedFiles(a, b []ResolvedFile) string {
	if len(a) != len(b) {
		return fmt.Sprintf("resolved %d file(s), git resolved %d", len(a), len(b))
	}
	byPath := make(map[string]ResolvedFile, len(b))
	for _, f := range b {
		byPath[f.Path] = f
	}
	for _, f := range a {
		g, ok := byPath[f.Path]
		if !ok {
			return fmt.Sprintf("%s: not found in git at the resolved commit", f.Path)
		}
		if f.Kind != g.Kind {
			return fmt.Sprintf("%s: resolved as %v, git resolves it as %v", f.Path, f.Kind, g.Kind)
		}
		if f.Kind == ContentAnnex && f.Key != g.Key {
			return fmt.Sprintf("%s: resolved key %s, git key %s (stale catalog)", f.Path, f.Key, g.Key)
		}
		if f.Kind == ContentGit && f.GitOID != "" && g.GitOID != "" && f.GitOID != g.GitOID {
			return fmt.Sprintf("%s: resolved blob %s, git blob %s (stale catalog)", f.Path, f.GitOID, g.GitOID)
		}
	}
	return ""
}
