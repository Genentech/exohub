package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestBuildCorpusDeterministic verifies that buildCorpus produces identical
// output across repeated calls — the generator must never depend on the real
// clock, map iteration order, or any other non-deterministic input.
func TestBuildCorpusDeterministic(t *testing.T) {
	first := buildCorpus()
	second := buildCorpus()

	if len(first) != len(second) {
		t.Fatalf("doc count differs: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if !reflect.DeepEqual(first[i], second[i]) {
			t.Fatalf("document %d differs between runs:\n%#v\nvs\n%#v", i, first[i], second[i])
		}
	}
}

// TestBuildCorpusNoCapturedIdentifiers guards against accidentally
// reintroducing any identifier from a real capture: every document must only
// ever reference the invented project/org/bucket/owner/author identities
// declared in corpus.go.
func TestBuildCorpusNoCapturedIdentifiers(t *testing.T) {
	docs := buildCorpus()
	if len(docs) == 0 {
		t.Fatal("buildCorpus returned no documents")
	}

	allowedProjects := map[string]bool{}
	for _, p := range corpusProjects {
		allowedProjects[p.id] = true
	}

	for i, d := range docs {
		extra, ok := d["_extra"].(map[string]any)
		if !ok {
			t.Fatalf("document %d missing _extra", i)
		}
		projectID, _ := extra["project_id"].(string)
		if !allowedProjects[projectID] {
			t.Errorf("document %d has unexpected project_id %q", i, projectID)
		}
		if bucket := extraBucket(extra); bucket != synthBucket {
			t.Errorf("document %d has unexpected bucket %q", i, bucket)
		}
		if owners := extraOwners(extra); len(owners) > 0 {
			for _, o := range owners {
				if o != synthOwners[0] {
					t.Errorf("document %d has unexpected owner %q", i, o)
				}
			}
		}
	}
}

func extraBucket(extra map[string]any) string {
	loc, _ := extra["location"].(map[string]any)
	if loc == nil {
		return ""
	}
	s3, _ := loc["s3"].(map[string]any)
	if s3 == nil {
		return ""
	}
	bucket, _ := s3["bucket"].(string)
	return bucket
}

func extraOwners(extra map[string]any) []string {
	perms, _ := extra["permissions"].(map[string]any)
	if perms == nil {
		return nil
	}
	owners, _ := perms["owners"].([]string)
	return owners
}

// TestGenerateFixturesDeterministic runs the full pipeline (synthetic corpus
// -> ephemeral SurrealDB -> /search -> canonicalized fixtures) twice, into
// separate temp dirs, and asserts the three generated files are byte-for-byte
// identical across runs. This is the generator's determinism contract that
// `make verify-fixtures` also exercises against the committed fixtures in CI.
//
// Requires a real SurrealDB binary (SURREAL_BIN or `surreal` on PATH); it
// skips LOUDLY (via t.Skip with an explicit reason, not a silent pass) when
// one isn't available, so a missing binary is never mistaken for a passing
// determinism check.
func TestGenerateFixturesDeterministic(t *testing.T) {
	bin, err := resolveSurrealBin()
	if err != nil {
		t.Skipf("SKIPPING end-to-end determinism test: %v (set SURREAL_BIN or install surreal to exercise this test)", err)
	}

	ctx := context.Background()
	dirA := t.TempDir()
	dirB := t.TempDir()

	if err := generateFixtures(ctx, bin, dirA); err != nil {
		t.Fatalf("generateFixtures (run A): %v", err)
	}
	if err := generateFixtures(ctx, bin, dirB); err != nil {
		t.Fatalf("generateFixtures (run B): %v", err)
	}

	for _, rel := range []string{
		filepath.Join("seed", "documents.ndjson"),
		"search_p1.json",
		"search_p2.json",
	} {
		a, err := os.ReadFile(filepath.Join(dirA, rel))
		if err != nil {
			t.Fatalf("read run A %s: %v", rel, err)
		}
		b, err := os.ReadFile(filepath.Join(dirB, rel))
		if err != nil {
			t.Fatalf("read run B %s: %v", rel, err)
		}
		if string(a) != string(b) {
			t.Errorf("%s differs between runs A and B", rel)
		}
	}
}

// TestGenerateFixturesSearchResponseShape sanity-checks that the generated
// search_p1.json is valid JSON with the expected search-response envelope.
// It shares the same SURREAL_BIN requirement and loud-skip behavior as
// TestGenerateFixturesDeterministic.
func TestGenerateFixturesSearchResponseShape(t *testing.T) {
	bin, err := resolveSurrealBin()
	if err != nil {
		t.Skipf("SKIPPING end-to-end test: %v (set SURREAL_BIN or install surreal to exercise this test)", err)
	}

	dir := t.TempDir()
	if err := generateFixtures(context.Background(), bin, dir); err != nil {
		t.Fatalf("generateFixtures: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "search_p1.json"))
	if err != nil {
		t.Fatalf("read search_p1.json: %v", err)
	}
	var page struct {
		Count   int              `json:"count"`
		Next    string           `json:"next"`
		Results []map[string]any `json:"results"`
		Total   int              `json:"total"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatalf("unmarshal search_p1.json: %v", err)
	}
	if page.Count != len(page.Results) {
		t.Errorf("count=%d but got %d results", page.Count, len(page.Results))
	}
	if page.Next != deterministicScrollID {
		t.Errorf("next = %q, want pinned placeholder %q", page.Next, deterministicScrollID)
	}
	if page.Total <= 0 {
		t.Errorf("total = %d, want > 0", page.Total)
	}
}
