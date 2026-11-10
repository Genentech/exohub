package exohub

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// artifactDoc builds a minimal exohub-artifact/v1 catalog document, as
// seen in spike-zero-copy.md, for one project file.
func artifactDoc(id, path, repoURL, commit, key, backend string, locations []map[string]any) map[string]any {
	doc := map[string]any{
		"path":     path,
		"size":     2239317,
		"repo_url": repoURL,
		"ref":      commit,
		"backend":  backend,
		"_extra": map[string]any{
			"id":         id,
			"project_id": "rnaseq-yeast-gse110004-fastq-mini",
			"version":    "main",
			"$schema":    "exohub-artifact/v1",
		},
	}
	if key != "" {
		doc["key"] = key
	}
	if locations != nil {
		doc["locations"] = locations
	}
	return doc
}

func newCatalogServer(t *testing.T, docs map[string]map[string]any) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/files/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/files/")
		id = strings.TrimSuffix(id, "/metadata")
		id, err := url.QueryUnescape(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		doc, ok := docs[id]
		if !ok {
			http.Error(w, fmt.Sprintf(`{"reason":"not found: %s"}`, id), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(doc)
	})
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		var results []map[string]any
		for _, doc := range docs {
			results = append(results, doc)
		}
		json.NewEncoder(w).Encode(map[string]any{"results": results})
	})
	return httptest.NewServer(mux)
}

func TestResolveCatalogAsset(t *testing.T) {
	const id = "rnaseq-yeast-gse110004-fastq-mini:fastq/SRR6357070_1.fastq.gz@main"
	doc := artifactDoc(id, "fastq/SRR6357070_1.fastq.gz",
		"git@git.example.com:group/fastq-mini.git",
		"d3845ed5894787fe074e5d5e5419f3d6d727727e",
		"SHA256E-s2239317--3f50541fa9cf2bedc87e7b682ada0fccfdfcd6d27b9bb81f17be230ff140ebe7.gz",
		"SHA256E",
		[]map[string]any{
			{
				"type": "annex", "remote": "s3-archive",
				"s3url":  "s3://example-bucket/project/_annex",
				"chunks": []map[string]any{{"chunk_number": 1, "chunk_size": 2239317}},
			},
		})
	srv := newCatalogServer(t, map[string]map[string]any{id: doc})
	defer srv.Close()

	ref, err := ParseReference("exohub:rnaseq-yeast-gse110004-fastq-mini:fastq/SRR6357070_1.fastq.gz@main")
	if err != nil {
		t.Fatalf("ParseReference() error = %v", err)
	}

	res, err := ResolveCatalogAsset(srv.URL, ref)
	if err != nil {
		t.Fatalf("ResolveCatalogAsset() error = %v", err)
	}
	if res.Commit != "d3845ed5894787fe074e5d5e5419f3d6d727727e" {
		t.Errorf("Commit = %q", res.Commit)
	}
	if len(res.Files) != 1 {
		t.Fatalf("Files = %d, want 1", len(res.Files))
	}
	f := res.Files[0]
	if f.Kind != ContentAnnex {
		t.Errorf("Kind = %v, want ContentAnnex", f.Kind)
	}
	if f.Backend != "SHA256E" {
		t.Errorf("Backend = %q", f.Backend)
	}
	if len(f.Locations) != 1 || f.Locations[0].ChunkSize != 2239317 {
		t.Errorf("Locations = %+v", f.Locations)
	}
}

func TestResolveCatalogAssetNotFound(t *testing.T) {
	srv := newCatalogServer(t, map[string]map[string]any{})
	defer srv.Close()

	ref, err := ParseReference("exohub:myproject:missing.csv@main")
	if err != nil {
		t.Fatalf("ParseReference() error = %v", err)
	}
	_, err = ResolveCatalogAsset(srv.URL, ref)
	if err == nil {
		t.Fatal("expected an error for a missing catalog entry")
	}
	if !strings.Contains(err.Error(), "not found in the catalog") {
		t.Errorf("error = %v, want a not-found hint", err)
	}
}

func TestResolveCatalogAssetGitFile(t *testing.T) {
	const id = "myproject:README.md@main"
	doc := artifactDoc(id, "README.md", "git@git.example.com:group/myproject.git", "abc123", "", "", nil)
	srv := newCatalogServer(t, map[string]map[string]any{id: doc})
	defer srv.Close()

	ref, err := ParseReference("exohub:myproject:README.md@main")
	if err != nil {
		t.Fatalf("ParseReference() error = %v", err)
	}
	res, err := ResolveCatalogAsset(srv.URL, ref)
	if err != nil {
		t.Fatalf("ResolveCatalogAsset() error = %v", err)
	}
	if res.Files[0].Kind != ContentGit {
		t.Errorf("Kind = %v, want ContentGit", res.Files[0].Kind)
	}
}
