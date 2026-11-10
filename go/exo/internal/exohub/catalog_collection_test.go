package exohub

import "testing"

func TestResolveCatalogCollection(t *testing.T) {
	repo := newTestRepo(t)
	repo.writeFile(".exohub/collections/qc.json", `{"paths":["README.md"],"globs":["data/*.txt"]}`)
	commit := repo.commit("initial")

	manifestID := "myproject:.exohub/collections/qc.json@main"
	readmeID := "myproject:README.md@main"
	dataAID := "myproject:data/a.txt@main"
	dataBID := "myproject:data/b.txt@main"
	key := "SHA256E-s10--abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234"

	docs := map[string]map[string]any{
		manifestID: artifactDoc(manifestID, ".exohub/collections/qc.json", repo.Dir, commit, "", "", nil),
		readmeID:   artifactDoc(readmeID, "README.md", repo.Dir, commit, "", "", nil),
		dataAID: artifactDoc(dataAID, "data/a.txt", repo.Dir, commit, key, "SHA256E", []map[string]any{
			{"type": "annex", "remote": "s3-archive", "s3url": "s3://example-bucket/project/_annex",
				"chunks": []map[string]any{{"chunk_number": 1, "chunk_size": 10}}},
		}),
		dataBID: artifactDoc(dataBID, "data/b.txt", repo.Dir, commit, key, "SHA256E", []map[string]any{
			{"type": "annex", "remote": "s3-archive", "s3url": "s3://example-bucket/project/_annex",
				"chunks": []map[string]any{{"chunk_number": 1, "chunk_size": 10}}},
		}),
	}
	srv := newCatalogServer(t, docs)
	defer srv.Close()

	ref, err := ParseReference("exohub:myproject:collection:qc@main")
	if err != nil {
		t.Fatal(err)
	}
	res, err := ResolveCatalogCollection(srv.URL, ref)
	if err != nil {
		t.Fatalf("ResolveCatalogCollection() error = %v", err)
	}
	if res.RepoURL != repo.Dir || res.Commit != commit {
		t.Errorf("RepoURL/Commit = %q/%q, want %q/%q", res.RepoURL, res.Commit, repo.Dir, commit)
	}
	if len(res.Files) != 3 {
		t.Fatalf("Files = %d, want 3: %+v", len(res.Files), res.Files)
	}
	byPath := make(map[string]ResolvedFile, len(res.Files))
	for _, f := range res.Files {
		byPath[f.Path] = f
	}
	if f, ok := byPath["README.md"]; !ok || f.Kind != ContentGit {
		t.Errorf("README.md = %+v, want ContentGit", f)
	}
	if f, ok := byPath["data/a.txt"]; !ok || f.Kind != ContentAnnex || f.Key != key {
		t.Errorf("data/a.txt = %+v, want ContentAnnex key %q", f, key)
	}
	if _, ok := byPath["data/b.txt"]; !ok {
		t.Error("data/b.txt missing from collection members")
	}
}

func TestResolveCatalogCollectionMissingManifest(t *testing.T) {
	srv := newCatalogServer(t, map[string]map[string]any{})
	defer srv.Close()

	ref, err := ParseReference("exohub:myproject:collection:no-such@main")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveCatalogCollection(srv.URL, ref); err == nil {
		t.Fatal("expected an error for a missing collection manifest")
	}
}

// TestResolveCollectionSourceID covers design D5 amendment review point
// 3's catalog-only fallback (exo-cli#154), used when no local clone or
// git remote is reachable to run the source project's own collection
// resolution directly: it reads the id off a `lineage.collections` entry
// a document in the source project already carries, rather than
// recomputing a flat digest over the collection's resolved files (which
// would disagree with the source's own id for a nested or folder-shaped
// manifest).
func TestResolveCollectionSourceID(t *testing.T) {
	repo := newTestRepo(t)
	repo.writeFile("data/a.txt", "a")
	repo.writeFile("data/b.txt", "b")
	commit := repo.commit("initial")

	const wantID = "urn:exohub:git:collection:deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	dataAID := "myproject:data/a.txt@main"
	dataBID := "myproject:data/b.txt@main"
	docA := artifactDoc(dataAID, "data/a.txt", repo.Dir, commit, "", "", nil)
	docA["lineage"] = map[string]any{
		"collections": []map[string]any{
			{"kind": "named", "name": "qc", "id": wantID},
		},
	}
	docB := artifactDoc(dataBID, "data/b.txt", repo.Dir, commit, "", "", nil)
	docB["lineage"] = map[string]any{
		"collections": []map[string]any{
			{"kind": "named", "name": "qc", "id": wantID},
		},
	}
	docs := map[string]map[string]any{dataAID: docA, dataBID: docB}
	srv := newCatalogServer(t, docs)
	defer srv.Close()

	ref, err := ParseReference("exohub:myproject:collection:qc@main")
	if err != nil {
		t.Fatal(err)
	}
	id, files, ok, err := ResolveCollectionSourceID(srv.URL, ref)
	if err != nil {
		t.Fatalf("ResolveCollectionSourceID() error = %v", err)
	}
	if !ok || id != wantID {
		t.Errorf("id = %q, ok = %v, want %q, true", id, ok, wantID)
	}
	if len(files) != 2 {
		t.Errorf("files = %+v, want 2 (every document that carries the lineage entry)", files)
	}
}

// TestResolveCollectionSourceID_NoMatch covers the "neither was
// available" error path (exo-cli#154): a source project that has never
// been bundled yet has no lineage.collections entry for any collection,
// so the fallback must say so (ok=false) instead of claiming an id.
func TestResolveCollectionSourceID_NoMatch(t *testing.T) {
	repo := newTestRepo(t)
	repo.writeFile("data/a.txt", "a")
	commit := repo.commit("initial")

	dataAID := "myproject:data/a.txt@main"
	docs := map[string]map[string]any{
		dataAID: artifactDoc(dataAID, "data/a.txt", repo.Dir, commit, "", "", nil),
	}
	srv := newCatalogServer(t, docs)
	defer srv.Close()

	ref, err := ParseReference("exohub:myproject:collection:qc@main")
	if err != nil {
		t.Fatal(err)
	}
	_, _, ok, err := ResolveCollectionSourceID(srv.URL, ref)
	if err != nil {
		t.Fatalf("ResolveCollectionSourceID() error = %v", err)
	}
	if ok {
		t.Error("ok = true, want false: no document carries a matching lineage.collections entry")
	}
}
