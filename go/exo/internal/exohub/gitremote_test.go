package exohub

import "testing"

func TestResolveGitRemote(t *testing.T) {
	repo := newTestRepo(t)
	repo.writeFile("README.md", "hello\n")
	key := "SHA256E-s10--abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234"
	repo.writeAnnexSymlink("data/file.bin", key)
	commit := repo.commit("initial")

	ref, err := ParseReference("exohub:myproject:data/file.bin@main")
	if err != nil {
		t.Fatal(err)
	}
	res, err := ResolveGitRemote(repo.Dir, "main", ref)
	if err != nil {
		t.Fatalf("ResolveGitRemote() error = %v", err)
	}
	defer res.Close()

	if res.Commit != commit {
		t.Errorf("Commit = %q, want %q", res.Commit, commit)
	}
	if res.RepoURL != repo.Dir {
		t.Errorf("RepoURL = %q, want %q", res.RepoURL, repo.Dir)
	}
	if len(res.Files) != 1 || res.Files[0].Key != key {
		t.Fatalf("Files = %+v", res.Files)
	}
	if res.LocalClonePath == "" {
		t.Error("LocalClonePath is empty, want the fetched mirror's path")
	}
}

func TestResolveGitRemoteUnknownRev(t *testing.T) {
	repo := newTestRepo(t)
	repo.writeFile("README.md", "hello\n")
	repo.commit("initial")

	ref, err := ParseReference("exohub:myproject:README.md@main")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveGitRemote(repo.Dir, "no-such-branch", ref); err == nil {
		t.Fatal("expected an error for an unknown revision")
	}
}

func TestReadGitFileLazyFetch(t *testing.T) {
	repo := newTestRepo(t)
	repo.writeFile("README.md", "hello world\n")
	commit := repo.commit("initial")

	// A Resolution with no LocalClonePath, as catalog resolution produces,
	// should still be able to read a ContentGit file's content by fetching
	// it lazily from RepoURL (design D3).
	res := &Resolution{RepoURL: repo.Dir, Commit: commit}
	content, err := ReadGitFile(res, "README.md")
	if err != nil {
		t.Fatalf("ReadGitFile() error = %v", err)
	}
	if string(content) != "hello world\n" {
		t.Errorf("ReadGitFile() = %q, want %q", content, "hello world\n")
	}
	if res.LocalClonePath == "" {
		t.Error("ReadGitFile did not cache a LocalClonePath for subsequent calls")
	}
	if err := res.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}
