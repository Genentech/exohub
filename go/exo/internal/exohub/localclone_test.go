package exohub

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestDiscoverLocalCloneEnvVar(t *testing.T) {
	repo := newTestRepo(t)
	repo.writeFile("README.md", "hello\n")
	repo.commit("initial")
	repo.markPublished("main")

	t.Setenv("EXOHUB_CLONE_MY_PROJECT", repo.Dir)
	if got := DiscoverLocalClone("my-project"); got != repo.Dir {
		t.Errorf("DiscoverLocalClone() = %q, want %q", got, repo.Dir)
	}
}

func TestDiscoverLocalCloneNone(t *testing.T) {
	t.Setenv("EXOHUB_CLONE_NO_SUCH_PROJECT_XYZ", "")
	if got := DiscoverLocalClone("no-such-project-xyz"); got != "" {
		t.Errorf("DiscoverLocalClone() = %q, want \"\"", got)
	}
}

func TestResolveLocalCloneAsset(t *testing.T) {
	repo := newTestRepo(t)
	repo.writeFile("README.md", "hello\n")
	key := "SHA256E-s10--abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234"
	repo.writeAnnexSymlink("data/file.bin", key)
	repo.commit("initial")
	repo.markPublished("main")

	ref, err := ParseReference("exohub:myproject:data/file.bin@main")
	if err != nil {
		t.Fatal(err)
	}
	res, err := ResolveLocalClone(repo.Dir, ref)
	if err != nil {
		t.Fatalf("ResolveLocalClone() error = %v", err)
	}
	if len(res.Files) != 1 {
		t.Fatalf("Files = %d, want 1", len(res.Files))
	}
	f := res.Files[0]
	if f.Kind != ContentAnnex {
		t.Errorf("Kind = %v, want ContentAnnex", f.Kind)
	}
	if f.Key != key {
		t.Errorf("Key = %q, want %q", f.Key, key)
	}
	if f.Backend != "SHA256E" {
		t.Errorf("Backend = %q", f.Backend)
	}
	if res.LocalClonePath != repo.Dir {
		t.Errorf("LocalClonePath = %q, want %q", res.LocalClonePath, repo.Dir)
	}
}

func TestResolveLocalCloneGitAsset(t *testing.T) {
	repo := newTestRepo(t)
	repo.writeFile("README.md", "hello\n")
	repo.commit("initial")
	repo.markPublished("main")

	ref, err := ParseReference("exohub:myproject:README.md@main")
	if err != nil {
		t.Fatal(err)
	}
	res, err := ResolveLocalClone(repo.Dir, ref)
	if err != nil {
		t.Fatalf("ResolveLocalClone() error = %v", err)
	}
	if res.Files[0].Kind != ContentGit {
		t.Errorf("Kind = %v, want ContentGit", res.Files[0].Kind)
	}
	if res.Files[0].GitOID == "" {
		t.Error("GitOID is empty, want a blob oid")
	}

	content, err := ReadGitFile(res, "README.md")
	if err != nil {
		t.Fatalf("ReadGitFile() error = %v", err)
	}
	if string(content) != "hello\n" {
		t.Errorf("ReadGitFile() = %q, want %q", content, "hello\n")
	}
}

func TestResolveLocalCloneFolder(t *testing.T) {
	repo := newTestRepo(t)
	repo.writeFile("data/a.txt", "a")
	repo.writeFile("data/b.txt", "b")
	repo.writeFile("other.txt", "o")
	repo.commit("initial")
	repo.markPublished("main")

	ref, err := ParseReference("exohub:myproject:data/@main")
	if err != nil {
		t.Fatal(err)
	}
	res, err := ResolveLocalClone(repo.Dir, ref)
	if err != nil {
		t.Fatalf("ResolveLocalClone() error = %v", err)
	}
	var paths []string
	for _, f := range res.Files {
		paths = append(paths, f.Path)
	}
	sort.Strings(paths)
	want := []string{"data/a.txt", "data/b.txt"}
	if len(paths) != len(want) || paths[0] != want[0] || paths[1] != want[1] {
		t.Errorf("paths = %v, want %v", paths, want)
	}
}

func TestResolveLocalCloneProject(t *testing.T) {
	repo := newTestRepo(t)
	repo.writeFile("data/a.txt", "a")
	repo.writeFile("README.md", "r")
	repo.commit("initial")
	repo.markPublished("main")

	ref, err := ParseReference("exohub:myproject@main")
	if err != nil {
		t.Fatal(err)
	}
	res, err := ResolveLocalClone(repo.Dir, ref)
	if err != nil {
		t.Fatalf("ResolveLocalClone() error = %v", err)
	}
	if len(res.Files) != 2 {
		t.Fatalf("Files = %d, want 2", len(res.Files))
	}
}

func TestResolveLocalCloneCollection(t *testing.T) {
	repo := newTestRepo(t)
	repo.writeFile("data/a.txt", "a")
	repo.writeFile("data/b.txt", "b")
	repo.writeFile("README.md", "r")
	repo.writeFile(".exohub/collections/qc.json", `{"paths":["README.md"],"globs":["data/*.txt"]}`)
	repo.commit("initial")
	repo.markPublished("main")

	ref, err := ParseReference("exohub:myproject:collection:qc@main")
	if err != nil {
		t.Fatal(err)
	}
	res, err := ResolveLocalClone(repo.Dir, ref)
	if err != nil {
		t.Fatalf("ResolveLocalClone() error = %v", err)
	}
	var paths []string
	for _, f := range res.Files {
		paths = append(paths, f.Path)
	}
	sort.Strings(paths)
	want := []string{"README.md", "data/a.txt", "data/b.txt"}
	if len(paths) != len(want) {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Errorf("paths[%d] = %q, want %q", i, paths[i], want[i])
		}
	}
}

func TestResolveLocalCloneUnknownRevision(t *testing.T) {
	repo := newTestRepo(t)
	repo.writeFile("README.md", "hello\n")
	repo.commit("initial")
	repo.markPublished("main")

	ref, err := ParseReference("exohub:myproject:README.md@no-such-branch")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveLocalClone(repo.Dir, ref); err == nil {
		t.Fatal("expected an error for an unknown revision")
	}
}

func TestIsGitWorkTree(t *testing.T) {
	repo := newTestRepo(t)
	if !isGitWorkTree(repo.Dir) {
		t.Errorf("isGitWorkTree(%q) = false, want true", repo.Dir)
	}
	if isGitWorkTree(filepath.Join(os.TempDir(), "exo-not-a-repo-xyz")) {
		t.Error("isGitWorkTree() = true for a non-existent directory, want false")
	}
}

// TestResolveLocalCloneIgnoresUnpublishedLocalCommit is the regression test
// for exo-cli#152 review item 5: a clone's local branch can have commits
// that were never pushed (refs/remotes/origin/<branch> is older), and
// those must never be resolved or recorded as an import's source.
func TestResolveLocalCloneIgnoresUnpublishedLocalCommit(t *testing.T) {
	repo := newTestRepo(t)
	repo.writeFile("README.md", "published\n")
	published := repo.commit("published")
	repo.markPublished("main")

	// An unpushed local commit: refs/remotes/origin/main still points at
	// the published commit above.
	repo.writeFile("README.md", "unpublished, not yet pushed\n")
	repo.commit("local only")

	ref, err := ParseReference("exohub:myproject:README.md@main")
	if err != nil {
		t.Fatal(err)
	}
	res, err := ResolveLocalClone(repo.Dir, ref)
	if err != nil {
		t.Fatalf("ResolveLocalClone() error = %v", err)
	}
	if res.Commit != published {
		t.Errorf("Commit = %q, want the published commit %q, not the local branch tip", res.Commit, published)
	}
	if res.Note == "" {
		t.Error("Note is empty, want a note that the local branch differs from the published commit")
	}
	content, err := ReadGitFile(res, "README.md")
	if err != nil {
		t.Fatalf("ReadGitFile() error = %v", err)
	}
	if string(content) != "published\n" {
		t.Errorf("content = %q, want the published content, not the unpushed local commit's", content)
	}
}

// TestResolveLocalCloneRequiresPublishedRef covers the case where version
// isn't a published ref at all (no origin-tracking branch, no tag, not a
// commit sha): ResolveLocalClone must refuse rather than silently falling
// back to a local-only branch.
func TestResolveLocalCloneRequiresPublishedRef(t *testing.T) {
	repo := newTestRepo(t)
	repo.writeFile("README.md", "hello\n")
	repo.commit("initial") // no markPublished: refs/remotes/origin/main does not exist

	ref, err := ParseReference("exohub:myproject:README.md@main")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveLocalClone(repo.Dir, ref); err == nil {
		t.Fatal("expected an error when the version has no published ref (origin-tracking branch, tag, or commit sha)")
	}
}

// TestResolveLocalCloneAcceptsTagAndSHA covers the other two published
// forms version can take: a tag, and a literal commit sha.
func TestResolveLocalCloneAcceptsTagAndSHA(t *testing.T) {
	repo := newTestRepo(t)
	repo.writeFile("README.md", "hello\n")
	commit := repo.commit("initial")
	repo.run("tag", "v1")

	tagRef, err := ParseReference("exohub:myproject:README.md@v1")
	if err != nil {
		t.Fatal(err)
	}
	if res, err := ResolveLocalClone(repo.Dir, tagRef); err != nil {
		t.Errorf("ResolveLocalClone() with a tag error = %v", err)
	} else if res.Commit != commit {
		t.Errorf("Commit = %q, want %q", res.Commit, commit)
	}

	shaRef, err := ParseReference("exohub:myproject:README.md@" + commit)
	if err != nil {
		t.Fatal(err)
	}
	if res, err := ResolveLocalClone(repo.Dir, shaRef); err != nil {
		t.Errorf("ResolveLocalClone() with a commit sha error = %v", err)
	} else if res.Commit != commit {
		t.Errorf("Commit = %q, want %q", res.Commit, commit)
	}
}
