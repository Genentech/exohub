package tui

import "testing"

func artifactDoc(backend, key, path string) *markdown {
	return &markdown{
		ArfifactDBID: "proj:" + path + "@main",
		Extra:        Extra{Schema: "exohub-artifact/v1.json"},
		Result: SearchResult{
			Metadata: map[string]interface{}{
				"backend": backend,
				"key":     key,
				"path":    path,
			},
		},
	}
}

func TestIsArtifactDocument(t *testing.T) {
	cases := []struct {
		name   string
		schema string
		want   bool
	}{
		{"artifact", "exohub-artifact/v1.json", true},
		{"markdown", "exohub-markdown/v1.json", true},
		{"other", "exohub-commit/v1.json", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			md := &markdown{Extra: Extra{Schema: tc.schema}}
			if got := isArtifactDocument(md); got != tc.want {
				t.Errorf("isArtifactDocument(%q) = %v, want %v", tc.schema, got, tc.want)
			}
		})
	}
}

func TestContentIDForDocument(t *testing.T) {
	t.Run("annex backend", func(t *testing.T) {
		md := artifactDoc("SHA256E", "SHA256E-s12345--abc", "fastq/a.fastq.gz")
		id, ok := contentIDForDocument(md)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if want := "urn:exohub:annex:SHA256E-s12345--abc"; id != want {
			t.Errorf("contentIDForDocument() = %q, want %q", id, want)
		}
	})

	t.Run("git backend", func(t *testing.T) {
		md := artifactDoc("git", "0123456789abcdef0123456789abcdef01234567", "README.md")
		id, ok := contentIDForDocument(md)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if want := "urn:exohub:git:blob:0123456789abcdef0123456789abcdef01234567"; id != want {
			t.Errorf("contentIDForDocument() = %q, want %q", id, want)
		}
	})

	t.Run("missing key", func(t *testing.T) {
		md := &markdown{Result: SearchResult{Metadata: map[string]interface{}{"backend": "git"}}}
		if _, ok := contentIDForDocument(md); ok {
			t.Error("expected ok=false when key is missing")
		}
	})
}

func TestCollectionIDForDocument(t *testing.T) {
	t.Run("member of a folder collection", func(t *testing.T) {
		md := artifactDoc("SHA256E", "SHA256E-s1--abc", "data.tdb/part1")
		md.Result.Metadata["lineage"] = map[string]interface{}{
			"collection": map[string]interface{}{"id": "urn:exohub:git:tree:deadbeef"},
		}
		id, ok := collectionIDForDocument(md)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if want := "urn:exohub:git:tree:deadbeef"; id != want {
			t.Errorf("collectionIDForDocument() = %q, want %q", id, want)
		}
	})

	t.Run("no collection", func(t *testing.T) {
		md := artifactDoc("SHA256E", "SHA256E-s1--abc", "standalone.csv")
		if _, ok := collectionIDForDocument(md); ok {
			t.Error("expected ok=false when lineage.collection is absent")
		}
	})

	t.Run("no lineage at all", func(t *testing.T) {
		md := artifactDoc("SHA256E", "SHA256E-s1--abc", "standalone.csv")
		delete(md.Result.Metadata, "lineage")
		if _, ok := collectionIDForDocument(md); ok {
			t.Error("expected ok=false when lineage is absent")
		}
	})
}

func TestUsedByQuery(t *testing.T) {
	t.Run("annex file, no collection", func(t *testing.T) {
		md := artifactDoc("SHA256E", "SHA256E-s1--abc", "fastq/SRR6357070_1.fastq.gz")
		query, path, ok := usedByQuery(md)
		if !ok {
			t.Fatal("expected ok=true")
		}
		wantQuery := `lineage.inputs.id:"urn:exohub:annex:SHA256E-s1--abc"`
		if query != wantQuery {
			t.Errorf("usedByQuery() query = %q, want %q", query, wantQuery)
		}
		if path != "fastq/SRR6357070_1.fastq.gz" {
			t.Errorf("usedByQuery() path = %q, want %q", path, "fastq/SRR6357070_1.fastq.gz")
		}
	})

	t.Run("git file, no collection", func(t *testing.T) {
		md := artifactDoc("git", "cafebabe", "README.md")
		query, _, ok := usedByQuery(md)
		if !ok {
			t.Fatal("expected ok=true")
		}
		wantQuery := `lineage.inputs.id:"urn:exohub:git:blob:cafebabe"`
		if query != wantQuery {
			t.Errorf("usedByQuery() query = %q, want %q", query, wantQuery)
		}
	})

	t.Run("folder member ORs in the collection id, parenthesized", func(t *testing.T) {
		md := artifactDoc("SHA256E", "SHA256E-s1--abc", "data.tdb/fragments/f1")
		md.Result.Metadata["lineage"] = map[string]interface{}{
			"collection": map[string]interface{}{"id": "urn:exohub:git:tree:deadbeef"},
		}
		query, _, ok := usedByQuery(md)
		if !ok {
			t.Fatal("expected ok=true")
		}
		// Parenthesized: generateQuery joins this with other filters using " AND ",
		// which binds tighter than "OR" ("filter AND a OR b" == "(filter AND a) OR b").
		// Without parens, a project/version/schema filter would leak across the OR.
		want := `(lineage.inputs.id:"urn:exohub:annex:SHA256E-s1--abc" OR lineage.inputs.id:"urn:exohub:git:tree:deadbeef")`
		if query != want {
			t.Errorf("usedByQuery() query = %q, want %q", query, want)
		}

		// Simulate generateQuery ANDing in a project/version/schema filter: the OR
		// must stay scoped inside the parens, not spill out across the AND.
		filtered := `project:myproj AND ` + query
		wantFiltered := `project:myproj AND (lineage.inputs.id:"urn:exohub:annex:SHA256E-s1--abc" OR lineage.inputs.id:"urn:exohub:git:tree:deadbeef")`
		if filtered != wantFiltered {
			t.Errorf("filtered query = %q, want %q", filtered, wantFiltered)
		}
	})

	t.Run("missing content id", func(t *testing.T) {
		md := &markdown{Extra: Extra{Schema: "exohub-artifact/v1.json"}}
		if _, _, ok := usedByQuery(md); ok {
			t.Error("expected ok=false when the document has no content id")
		}
	})

	t.Run("falls back to the ArtifactDB id when path is missing", func(t *testing.T) {
		md := &markdown{
			ArfifactDBID: "proj:file.csv@v1",
			Result:       SearchResult{Metadata: map[string]interface{}{"backend": "git", "key": "cafe"}},
		}
		_, path, ok := usedByQuery(md)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if path != "proj:file.csv@v1" {
			t.Errorf("usedByQuery() path = %q, want %q", path, "proj:file.csv@v1")
		}
	})
}
