package exohub

import "testing"

func TestResolvePrefersLocalClone(t *testing.T) {
	repo := newTestRepo(t)
	repo.writeFile("README.md", "from local clone\n")
	repo.commit("initial")
	repo.markPublished("main")

	ref, err := ParseReference("exohub:myproject:README.md@main")
	if err != nil {
		t.Fatal(err)
	}
	// No catalog URL is given at all: if Resolve tried the catalog it would
	// fail to connect, so success here proves the local clone was used.
	res, err := Resolve(ref, ResolveOptions{LocalClone: repo.Dir, CatalogURL: "http://127.0.0.1:0"})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	defer res.Close()
	if res.LocalClonePath != repo.Dir {
		t.Errorf("LocalClonePath = %q, want %q (local clone should win)", res.LocalClonePath, repo.Dir)
	}
}

func TestResolveCatalogCommitWinsOverLocalClone(t *testing.T) {
	repo := newTestRepo(t)
	key := "SHA256E-s10--abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234"
	repo.writeAnnexSymlink("data/file.bin", key)
	commit := repo.commit("initial")
	repo.markPublished("main")

	const id = "myproject:data/file.bin@main"
	doc := artifactDoc(id, "data/file.bin", repo.Dir, commit, key, "SHA256E", []map[string]any{
		{"type": "annex", "remote": "s3-archive", "s3url": "s3://example-bucket/project/_annex",
			"chunks": []map[string]any{{"chunk_number": 1, "chunk_size": 10}}},
	})
	srv := newCatalogServer(t, map[string]map[string]any{id: doc})
	defer srv.Close()

	ref, err := ParseReference("exohub:myproject:data/file.bin@main")
	if err != nil {
		t.Fatal(err)
	}
	// Both a local clone and a reachable catalog can resolve this reference
	// (exo-cli#152 review item 5): the catalog's resolution must win (its
	// Commit/Files/Locations are what gets recorded), with the local clone
	// kept only as a content cache.
	res, err := Resolve(ref, ResolveOptions{LocalClone: repo.Dir, CatalogURL: srv.URL})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	defer res.Close()
	if len(res.Files) != 1 || len(res.Files[0].Locations) == 0 {
		t.Fatalf("Files = %+v, want the catalog's resolution (with Locations)", res.Files)
	}
	if res.LocalClonePath != repo.Dir {
		t.Errorf("LocalClonePath = %q, want %q (local clone kept as a content cache)", res.LocalClonePath, repo.Dir)
	}
}

func TestResolveFallsBackToCatalog(t *testing.T) {
	const id = "myproject:fastq/sample.fastq.gz@main"
	doc := artifactDoc(id, "fastq/sample.fastq.gz",
		"git@git.example.com:group/myproject.git", "deadbeef",
		"SHA256E-s10--abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234",
		"SHA256E",
		[]map[string]any{
			{"type": "annex", "remote": "s3-archive", "s3url": "s3://example-bucket/project/_annex",
				"chunks": []map[string]any{{"chunk_number": 1, "chunk_size": 10}}},
		})
	srv := newCatalogServer(t, map[string]map[string]any{id: doc})
	defer srv.Close()

	ref, err := ParseReference("exohub:myproject:fastq/sample.fastq.gz@main")
	if err != nil {
		t.Fatal(err)
	}
	// No local clone configured (env var unset, no adjacent directory for
	// this made-up project name), so Resolve must fall through to the
	// catalog.
	t.Setenv("EXOHUB_CLONE_MYPROJECT", "")
	res, err := Resolve(ref, ResolveOptions{CatalogURL: srv.URL})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	defer res.Close()
	if res.Commit != "deadbeef" {
		t.Errorf("Commit = %q, want %q", res.Commit, "deadbeef")
	}
}

func TestResolveFallsBackToGitRemote(t *testing.T) {
	repo := newTestRepo(t)
	repo.writeFile("README.md", "from git remote\n")
	repo.commit("initial")

	ref, err := ParseReference("exohub:myproject:README.md@main")
	if err != nil {
		t.Fatal(err)
	}
	// Catalog unreachable, no local clone: only the RepoURL fallback can
	// resolve this reference.
	res, err := Resolve(ref, ResolveOptions{CatalogURL: "http://127.0.0.1:0", RepoURL: repo.Dir})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	defer res.Close()
	if res.RepoURL != repo.Dir {
		t.Errorf("RepoURL = %q, want %q", res.RepoURL, repo.Dir)
	}
}

func TestResolveNoSourceAvailable(t *testing.T) {
	ref, err := ParseReference("exohub:myproject:README.md@main")
	if err != nil {
		t.Fatal(err)
	}
	_, err = Resolve(ref, ResolveOptions{CatalogURL: "http://127.0.0.1:0"})
	if err == nil {
		t.Fatal("expected an error when no source can resolve the reference")
	}
}

func TestResolveVerifyMismatch(t *testing.T) {
	repo := newTestRepo(t)
	repo.writeFile("README.md", "hello\n")
	key := "SHA256E-s10--abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234"
	repo.writeAnnexSymlink("data/file.bin", key)
	commit := repo.commit("initial")

	// The catalog's key is deliberately stale (does not match what the git
	// remote actually has at the same commit): --verify must catch this.
	staleKey := "SHA256E-s10--staaaaalestaaaaalestaaaaalestaaaaalestaaaaalestaaaaalestaaale"
	const id = "myproject:data/file.bin@main"
	doc := artifactDoc(id, "data/file.bin", repo.Dir, commit, staleKey, "SHA256E", []map[string]any{
		{"type": "annex", "remote": "s3-archive", "s3url": "s3://example-bucket/project/_annex",
			"chunks": []map[string]any{{"chunk_number": 1, "chunk_size": 10}}},
	})
	srv := newCatalogServer(t, map[string]map[string]any{id: doc})
	defer srv.Close()

	ref, err := ParseReference("exohub:myproject:data/file.bin@main")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("EXOHUB_CLONE_MYPROJECT", "")
	_, err = Resolve(ref, ResolveOptions{CatalogURL: srv.URL, Verify: true})
	if err == nil {
		t.Fatal("expected a --verify error for a stale catalog key")
	}
}

func TestResolveVerifyMatch(t *testing.T) {
	repo := newTestRepo(t)
	key := "SHA256E-s10--abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234"
	repo.writeAnnexSymlink("data/file.bin", key)
	commit := repo.commit("initial")

	const id = "myproject:data/file.bin@main"
	doc := artifactDoc(id, "data/file.bin", repo.Dir, commit, key, "SHA256E", []map[string]any{
		{"type": "annex", "remote": "s3-archive", "s3url": "s3://example-bucket/project/_annex",
			"chunks": []map[string]any{{"chunk_number": 1, "chunk_size": 10}}},
	})
	srv := newCatalogServer(t, map[string]map[string]any{id: doc})
	defer srv.Close()

	ref, err := ParseReference("exohub:myproject:data/file.bin@main")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("EXOHUB_CLONE_MYPROJECT", "")
	res, err := Resolve(ref, ResolveOptions{CatalogURL: srv.URL, Verify: true})
	if err != nil {
		t.Fatalf("Resolve() with matching --verify error = %v", err)
	}
	res.Close()
}
