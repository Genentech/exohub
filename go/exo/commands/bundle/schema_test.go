package bundle

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Genentech/exohub/go/exo/internal/provenance"
	"github.com/santhosh-tekuri/jsonschema/v5"
)

// schemaNames are exo bundle's JSON Schemas (design D16, task 8.1), the
// repository's single source of truth for what a bundle document looks
// like. Catalogs copy them from schemas/exohub/.
var schemaNames = []string{
	"exohub-commit",
	"exohub-bundle",
	"exohub-artifact",
	"exohub-markdown",
	"exohub-provenance",
}

// schemasDir resolves schemas/exohub/ at the repository root, regardless of
// the working directory the test runs from.
func schemasDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "schemas", "exohub")
}

// loadCatalogSchemas compiles every bundle JSON Schema as Draft-07. Each
// schema's properties are fully self-contained (no $defs, no property-level
// $ref, design D16 "Bundle JSON Schemas loadable by ArtifactDB"), so no
// cross-file $ref resolution between schemas is needed.
//
// Each schema's own "$id" is a path relative to a shared retrieval root
// (e.g. "exohub-artifact/v1.json"), matching the on-disk layout one level
// below schemasDir (schemas/exohub/exohub-artifact/v1.json). The compiler
// resolves a document's own "$id" against the URL it was registered under,
// so registering a schema directly at its own nested file path would merge
// its "$id" on top of that path and double its last directory segment
// (schemas/exohub/exohub-artifact/exohub-artifact/v1.json). Every schema
// declares "$id", so all of them are registered under a sibling marker URL
// in schemasDir itself, so resolving each one's own relative "$id" lands on
// its real on-disk path.
func loadCatalogSchemas(t *testing.T) map[string]*jsonschema.Schema {
	t.Helper()
	dir := schemasDir(t)
	compiler := jsonschema.NewCompiler()
	compiler.Draft = jsonschema.Draft7

	rootURLs := make(map[string]string, len(schemaNames))
	for _, name := range schemaNames {
		path := filepath.Join(dir, name, "v1.json")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s schema: %v", name, err)
		}
		var doc map[string]any
		if err := json.Unmarshal(data, &doc); err != nil {
			t.Fatalf("parse %s schema: %v", name, err)
		}
		url := "file://" + path
		if _, hasID := doc["$id"]; hasID {
			url = "file://" + filepath.Join(dir, "__root_"+name)
		}
		rootURLs[name] = url
		if err := compiler.AddResource(url, bytes.NewReader(data)); err != nil {
			t.Fatalf("add %s schema resource: %v", name, err)
		}
	}

	schemas := make(map[string]*jsonschema.Schema, len(schemaNames))
	for _, name := range schemaNames {
		s, err := compiler.Compile(rootURLs[name])
		if err != nil {
			t.Fatalf("compile %s schema: %v", name, err)
		}
		schemas[name] = s
	}
	return schemas
}

func validateAgainst(t *testing.T, schema *jsonschema.Schema, doc any) {
	t.Helper()
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal document: %v", err)
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("unmarshal document: %v", err)
	}
	if err := schema.Validate(v); err != nil {
		t.Fatalf("schema validation failed: %v\ndocument: %s", err, data)
	}
}

func TestCatalogSchemas_AreValidDraft7(t *testing.T) {
	loadCatalogSchemas(t)
}

// loadRawSchemas parses every schemaNames entry as a plain JSON document
// (not compiled), for structural assertions that don't need Draft-07
// semantics.
func loadRawSchemas(t *testing.T) map[string]map[string]any {
	t.Helper()
	dir := schemasDir(t)
	docs := make(map[string]map[string]any, len(schemaNames))
	for _, name := range schemaNames {
		data, err := os.ReadFile(filepath.Join(dir, name, "v1.json"))
		if err != nil {
			t.Fatalf("read %s schema: %v", name, err)
		}
		var doc map[string]any
		if err := json.Unmarshal(data, &doc); err != nil {
			t.Fatalf("parse %s schema: %v", name, err)
		}
		docs[name] = doc
	}
	return docs
}

// TestCatalogSchemas_AllHaveID asserts every bundle JSON Schema declares
// "$id" equal to "<dir>/v1.json" (design D16, ArtifactDB schema clients
// identify types by $id).
func TestCatalogSchemas_AllHaveID(t *testing.T) {
	for name, doc := range loadRawSchemas(t) {
		want := name + "/v1.json"
		got, _ := doc["$id"].(string)
		if got != want {
			t.Errorf("%s: $id = %q, want %q", name, got, want)
		}
	}
}

// TestCatalogSchemas_NoPropertyLevelRef asserts no property schema is a
// bare "$ref" (ArtifactDB's model generator does not resolve $ref and
// requires a "type" on every property). A $ref is only allowed inside the
// "items" of a property that itself declares "type": "array".
func TestCatalogSchemas_NoPropertyLevelRef(t *testing.T) {
	for name, doc := range loadRawSchemas(t) {
		props, _ := doc["properties"].(map[string]any)
		walkPropertiesForRef(t, name, "", props)
	}
}

// walkPropertiesForRef recursively checks every entry of props: a property
// value must not itself be a bare "$ref" object. "$ref" is only tolerated
// inside a property's own "items" (the property has "type": "array" at
// that level), which walkPropertiesForRef does not descend into further
// for the $ref check, but does recurse into "items.properties" and nested
// "properties" to catch $ref buried deeper.
func walkPropertiesForRef(t *testing.T, schemaName, path string, props map[string]any) {
	t.Helper()
	for fieldName, raw := range props {
		fieldPath := fieldName
		if path != "" {
			fieldPath = path + "." + fieldName
		}
		def, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if _, hasRef := def["$ref"]; hasRef {
			t.Errorf("%s: property %q is a bare $ref, which ArtifactDB's model generator cannot resolve", schemaName, fieldPath)
			continue
		}
		if nested, ok := def["properties"].(map[string]any); ok {
			walkPropertiesForRef(t, schemaName, fieldPath, nested)
		}
		if items, ok := def["items"].(map[string]any); ok {
			if nested, ok := items["properties"].(map[string]any); ok {
				walkPropertiesForRef(t, schemaName, fieldPath+"[]", nested)
			}
		}
	}
}

// fieldType is the (type, format, elasticsearch type) tuple a JSON Schema
// property declares, for cross-schema consistency checks (design D16,
// "all schemas of a catalog client are merged into ONE index mapping").
type fieldType struct {
	Type          string
	Format        string
	Elasticsearch string
}

// collectFieldTypes walks props (and nested "properties"/"items.properties")
// recursively, recording every property's fieldType keyed by its dotted
// path from the document root into out[path][schemaName] = fieldType. An
// array's "items" contributes no path segment of its own, matching how
// Elasticsearch flattens an array of objects onto the same field paths as
// a single nested object (design D16: "any property name used at the same
// path in more than one schema").
func collectFieldTypes(schemaName, pathPrefix string, props map[string]any, out map[string]map[string]fieldType) {
	for fieldName, raw := range props {
		def, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		fieldPath := fieldName
		if pathPrefix != "" {
			fieldPath = pathPrefix + "." + fieldName
		}
		ft := fieldType{}
		ft.Type, _ = def["type"].(string)
		ft.Format, _ = def["format"].(string)
		if es, ok := def["_elasticsearch"].(map[string]any); ok {
			ft.Elasticsearch, _ = es["type"].(string)
		}
		if ft.Type != "" || ft.Format != "" || ft.Elasticsearch != "" {
			if out[fieldPath] == nil {
				out[fieldPath] = map[string]fieldType{}
			}
			out[fieldPath][schemaName] = ft
		}
		if nested, ok := def["properties"].(map[string]any); ok {
			collectFieldTypes(schemaName, fieldPath, nested, out)
		}
		if items, ok := def["items"].(map[string]any); ok {
			if nested, ok := items["properties"].(map[string]any); ok {
				collectFieldTypes(schemaName, fieldPath, nested, out)
			}
		}
	}
}

// TestCatalogSchemas_ConsistentFieldTypes asserts that any property path
// used by more than one bundle JSON Schema has the same type/format/
// _elasticsearch.type everywhere (design D16: "all schemas of a catalog
// client are merged into ONE index mapping, so a field name shared across
// document types must have the same Elasticsearch type everywhere").
func TestCatalogSchemas_ConsistentFieldTypes(t *testing.T) {
	byPath := map[string]map[string]fieldType{}
	for name, doc := range loadRawSchemas(t) {
		props, _ := doc["properties"].(map[string]any)
		collectFieldTypes(name, "", props, byPath)
	}
	for fieldPath, bySchema := range byPath {
		var first fieldType
		var firstSchema string
		for name, ft := range bySchema {
			if firstSchema == "" {
				first, firstSchema = ft, name
				continue
			}
			if ft != first {
				t.Errorf("field %q: %s has %+v, but %s has %+v", fieldPath, name, ft, firstSchema, first)
			}
		}
	}
}

func TestCatalogSchemas_ValidateCommitArtifact(t *testing.T) {
	schemas := loadCatalogSchemas(t)
	validateAgainst(t, schemas["exohub-commit"], CommitArtifact{
		Schema:         "exohub-commit/v1.json",
		RepoURL:        "https://example.com/repo.git",
		Ref:            "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Hash:           "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Parents:        []string{"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
		AuthorName:     "Alice",
		AuthorEmail:    "alice@example.com",
		AuthorDate:     "2026-01-01T00:00:00Z",
		CommitterName:  "Alice",
		CommitterEmail: "alice@example.com",
		CommitterDate:  "2026-01-01T00:00:00Z",
		Subject:        "init",
		Body:           "",
		AnnexFiles:     []string{"data.bin"},
		GitFiles:       []string{"README.md"},
	})
}

func TestCatalogSchemas_ValidateBundleInfo(t *testing.T) {
	schemas := loadCatalogSchemas(t)
	validateAgainst(t, schemas["exohub-bundle"], BundleInfo{
		Schema:            "exohub-bundle/v1.json",
		RepoURL:           "https://example.com/repo.git",
		Ref:               "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		RefName:           "main",
		RefType:           "branch",
		GeneratedAt:       "2026-01-01T00:00:00Z",
		Preset:            "default",
		Scope:             "all",
		CommitHashes:      []string{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		CommitCount:       1,
		FileCount:         2,
		AnnexFileCount:    1,
		GitFileCount:      1,
		MarkdownFileCount: 1,
		TotalSize:         1024,
		ExoVersion:        "0.1.0",
		Provenance: &ProvenanceInfo{
			File:        "provenance.jsonld",
			RecordCount: 1,
			EntityCount: 1,
		},
	})
}

func TestCatalogSchemas_ValidateArtifactWithLineage(t *testing.T) {
	schemas := loadCatalogSchemas(t)
	validateAgainst(t, schemas["exohub-artifact"], Artifact{
		Schema:   "exohub-artifact/v1.json",
		RepoURL:  "https://example.com/repo.git",
		Ref:      "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Path:     "results/out.csv",
		Key:      "SHA256E-s3--deadbeef",
		Backend:  "SHA256E",
		Size:     3,
		Metadata: map[string][]string{"tag": {"release"}},
		Commits: []Commit{{
			Hash:          "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Parents:       []string{},
			AuthorName:    "Alice",
			AuthorEmail:   "alice@example.com",
			AuthorDate:    "2026-01-01T00:00:00Z",
			CommitterName: "Alice",
			CommitterDate: "2026-01-01T00:00:00Z",
			Subject:       "add output",
		}},
		Locations: []Location{{
			Remote: "s3-primary",
			Type:   "annex",
			UUID:   "00000000-0000-0000-0000-000000000001",
			Chunks: []Chunk{{ChunkKey: "chunk-1", ChunkNumber: 1, ChunkSize: 3}},
		}},
		Lineage: &provenance.ArtifactLineage{
			Covered: true,
			GeneratedBy: &provenance.LineageActivity{
				ID:      "urn:exohub:activity:1",
				Started: "2026-01-01T00:00:00Z",
				Ended:   "2026-01-01T00:01:00Z",
				Plan:    &provenance.LineageRef{ID: "urn:exohub:git:blob:plan", Path: "pipeline.nf"},
				Agents: []provenance.LineageAgent{{
					ID:              "urn:exohub:agent:software:bot",
					Type:            "SoftwareAgent",
					Model:           "gpt",
					ActedOnBehalfOf: []string{"urn:exohub:agent:person:alice"},
				}},
			},
			Inputs:       []provenance.LineageRef{{ID: "urn:exohub:git:blob:input", Path: "raw/in.csv"}},
			RevisionOf:   []string{"urn:exohub:git:blob:old"},
			AttributedTo: []string{"urn:exohub:agent:person:alice"},
			Records: []provenance.ProvenanceRecordRef{{
				ID:              "urn:exohub:record:1",
				Assertion:       provenance.AssertionRecorded,
				AssertedBy:      "urn:exohub:agent:person:alice",
				GeneratedAtTime: "2026-01-01T00:00:00Z",
			}},
		},
	})
}

func TestCatalogSchemas_ValidateMarkdownArtifactWithLineage(t *testing.T) {
	schemas := loadCatalogSchemas(t)
	validateAgainst(t, schemas["exohub-markdown"], MarkdownArtifact{
		Schema:   "exohub-markdown/v1.json",
		RepoURL:  "https://example.com/repo.git",
		Ref:      "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Path:     "README.md",
		Key:      "deadbeef",
		Backend:  "git",
		Size:     42,
		Metadata: map[string][]string{},
		Commits:  []Commit{},
		Title:    "Project",
		Sections: []Section{{Level: 1, Text: "Project"}},
		Content:  "# Project\n",
		IsReadme: true,
		Lineage: &provenance.ArtifactLineage{
			Covered: true,
			Collection: &provenance.LineageRef{
				ID:   "urn:exohub:git:tree:abc",
				Path: "docs/",
			},
		},
	})
}

func TestCatalogSchemas_ValidateProvenanceDocument(t *testing.T) {
	schemas := loadCatalogSchemas(t)
	validateAgainst(t, schemas["exohub-provenance"], provenance.ProvenanceDocument{
		Schema:      provenance.ProvenanceSchemaID,
		Ref:         "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		RefName:     "main",
		RecordCount: 1,
		EntityCount: 2,
		GraphFile:   "provenance.jsonld",
		Records: []provenance.ProvenanceRecordRef{{
			ID:              "urn:exohub:record:1",
			Assertion:       provenance.AssertionRecorded,
			AssertedBy:      "urn:exohub:agent:person:alice",
			GeneratedAtTime: "2026-01-01T00:00:00Z",
		}},
		Agents: []provenance.ProvenanceAgentRef{{
			ID:      "urn:exohub:agent:person:alice",
			Type:    "Person",
			Records: []string{"urn:exohub:record:1"},
		}},
		Activities: []provenance.ProvenanceActivity{{
			ID:        "urn:exohub:activity:1",
			Started:   "2026-01-01T00:00:00Z",
			Ended:     "2026-01-01T00:01:00Z",
			Agents:    []string{"urn:exohub:agent:person:alice"},
			Generated: []string{"urn:exohub:git:blob:out"},
			Records:   []string{"urn:exohub:record:1"},
		}},
		Entities: []provenance.ProvenanceEntity{{
			ID:      "urn:exohub:git:blob:out",
			Type:    "Entity",
			Path:    "out.csv",
			Covered: true,
			Records: []string{"urn:exohub:record:1"},
		}},
	})
}

// setupSchemaTestRepo builds a git-annex-initialized repository exercising
// every document kind exo bundle writes: a plain git file, an annexed file,
// a root-level markdown file, and a folder collection with two members, one
// of which inherits its lineage from the collection (design D16, "Lineage
// of folder collection members"). It returns the repo root and a command
// runner, or skips the test if git-annex is unavailable.
func setupSchemaTestRepo(t *testing.T) (dir string, run func(args ...string) string) {
	t.Helper()
	if _, err := exec.LookPath("git-annex"); err != nil {
		t.Skip("git-annex not available")
	}
	dir = t.TempDir()
	run = func(args ...string) string {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+dir)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("command %v failed: %v\n%s", args, err, out)
		}
		return string(out)
	}
	run("git", "init", "-q")
	run("git", "config", "user.email", "test@test.com")
	run("git", "config", "user.name", "Test")
	run("git", "annex", "init", "-q", "test-repo")
	t.Cleanup(func() {
		exec.Command("chmod", "-R", "u+w", dir).Run()
	})

	if err := os.MkdirAll(filepath.Join(dir, ".exohub"), 0o755); err != nil {
		t.Fatal(err)
	}
	remotes := "remotes:\n  - name: s3-primary\n    type: s3\n    uuid: 00000000-0000-0000-0000-000000000001\n    s3url: s3://bucket/prefix\n"
	if err := os.WriteFile(filepath.Join(dir, ".exohub", "remotes"), []byte(remotes), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, run
}

func annexWriteFile(t *testing.T, run func(args ...string) string, dir, relPath, content string) {
	t.Helper()
	full := filepath.Join(dir, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	run("git", "annex", "add", relPath)
}

// TestBundleOutput_ValidatesAgainstSchemas runs exo bundle on a repository
// exercising every document kind (plain git file, annexed file, root-level
// markdown, and a folder collection member that inherits its lineage) and
// validates every document exo bundle writes against the shipped JSON
// Schemas (design D16, task 8.1), dispatching each document to its schema
// by its own "$schema" field.
func TestBundleOutput_ValidatesAgainstSchemas(t *testing.T) {
	dir, run := setupSchemaTestRepo(t)

	writeFile := func(relPath, content string) {
		full := filepath.Join(dir, relPath)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// v1: a plain-git input, an annexed derived file, a folder collection
	// of two plain-git members, and a root-level README.
	writeFile("counts.csv", "counts\n")
	writeFile("pipeline.nf", "process {}\n")
	writeFile("README.md", "# Project\n\nSee results/.\n")
	annexWriteFile(t, run, dir, "results/out.csv", "out\n")
	writeFile("results/report.tdb/f1.bin", "one\n")
	writeFile("results/report.tdb/f2.bin", "two\n")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "v1")

	countsID := provenance.GitBlobEntityID(strings.TrimSpace(run("git", "rev-parse", "HEAD:counts.csv")))
	outID := provenance.AnnexEntityID(strings.TrimSpace(run("git", "annex", "lookupkey", "results/out.csv")))
	treeID := provenance.GitTreeEntityID(strings.TrimSpace(run("git", "rev-parse", "HEAD:results/report.tdb")))
	readmeID := provenance.GitBlobEntityID(strings.TrimSpace(run("git", "rev-parse", "HEAD:README.md")))
	planID := provenance.GitBlobEntityID(strings.TrimSpace(run("git", "rev-parse", "HEAD:pipeline.nf")))

	act1 := provenance.NewActivityID()
	rec1ID := provenance.NewRecordID()
	rec1 := fmt.Sprintf(`{
  "@context": "https://genentech.github.io/exohub/ns/provenance/v1.jsonld",
  "@id": %q,
  "exo:assertion": "asserted",
  "exo:assertedBy": "urn:exohub:agent:software:claude-code",
  "exo:evidence": "pipeline.nf processes counts.csv into results/out.csv",
  "generatedAtTime": "2026-10-16T10:00:00Z",
  "@graph": [
    {"@id": %q, "@type": "Entity"},
    {"@id": %q, "@type": "Entity"},
    {"@id": %q, "@type": "Entity", "wasGeneratedBy": %q, "wasDerivedFrom": %q, "wasAttributedTo": "urn:exohub:agent:person:alice"},
    {"@id": %q, "@type": "Activity", "hadPlan": %q, "wasAssociatedWith": ["urn:exohub:agent:software:claude-code", "urn:exohub:agent:person:alice"]},
    {"@id": "urn:exohub:agent:software:claude-code", "@type": "SoftwareAgent", "exo:model": "claude-sonnet", "actedOnBehalfOf": "urn:exohub:agent:person:alice"},
    {"@id": "urn:exohub:agent:person:alice", "@type": "Person"}
  ]
}`, rec1ID, planID, countsID, outID, act1, countsID, act1, planID)

	act2 := provenance.NewActivityID()
	rec2ID := provenance.NewRecordID()
	rec2 := fmt.Sprintf(`{
  "@context": "https://genentech.github.io/exohub/ns/provenance/v1.jsonld",
  "@id": %q,
  "exo:assertion": "recorded",
  "exo:assertedBy": "urn:exohub:agent:person:alice",
  "generatedAtTime": "2026-10-16T10:05:00Z",
  "@graph": [
    {"@id": %q, "@type": "Collection", "wasGeneratedBy": %q, "wasDerivedFrom": %q},
    {"@id": %q, "@type": "Activity", "wasAssociatedWith": "urn:exohub:agent:person:alice"},
    {"@id": "urn:exohub:agent:person:alice", "@type": "Person"}
  ]
}`, rec2ID, treeID, act2, outID, act2)

	rec3ID := provenance.NewRecordID()
	rec3 := fmt.Sprintf(`{
  "@context": "https://genentech.github.io/exohub/ns/provenance/v1.jsonld",
  "@id": %q,
  "exo:assertion": "recorded",
  "exo:assertedBy": "urn:exohub:agent:person:alice",
  "generatedAtTime": "2026-10-16T10:10:00Z",
  "@graph": [{"@id": %q, "@type": "Entity", "wasAttributedTo": "urn:exohub:agent:person:alice"}]
}`, rec3ID, readmeID)

	writeFile(".exohub/provenance/r1.jsonld", rec1)
	writeFile(".exohub/provenance/r2.jsonld", rec2)
	writeFile(".exohub/provenance/r3.jsonld", rec3)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "provenance records")

	origDir, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	outputDir := filepath.Join(".exohub", "bundles", "out")
	if err := runBundle("", "", "default", outputDir, "", false, false, true, false, false, false, false, "test", nil); err != nil {
		t.Fatalf("runBundle() error = %v", err)
	}

	schemas := loadCatalogSchemas(t)
	validatedKinds := map[string]int{}
	walkErr := filepath.Walk(outputDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || filepath.Ext(path) != ".json" {
			return nil
		}
		if filepath.Base(path) == "provenance.json" {
			// provenance.json has no "$schema" dispatch ambiguity, but
			// validate it explicitly below for clearer failure messages.
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var withSchema struct {
			Schema string `json:"$schema"`
		}
		if err := json.Unmarshal(data, &withSchema); err != nil {
			t.Errorf("%s: invalid JSON: %v", path, err)
			return nil
		}
		if withSchema.Schema == "" {
			t.Errorf("%s: no $schema field", path)
			return nil
		}
		kind := strings.TrimSuffix(withSchema.Schema, "/v1.json")
		schema, ok := schemas[kind]
		if !ok {
			t.Errorf("%s: unknown $schema %q", path, withSchema.Schema)
			return nil
		}
		var v any
		if err := json.Unmarshal(data, &v); err != nil {
			t.Errorf("%s: unmarshal: %v", path, err)
			return nil
		}
		if err := schema.Validate(v); err != nil {
			t.Errorf("%s: schema validation failed: %v", path, err)
			return nil
		}
		validatedKinds[kind]++
		return nil
	})
	if walkErr != nil {
		t.Fatalf("filepath.Walk error = %v", walkErr)
	}

	for _, kind := range []string{"exohub-bundle", "exohub-commit", "exohub-artifact", "exohub-markdown"} {
		if validatedKinds[kind] == 0 {
			t.Errorf("no generated document validated against %s", kind)
		}
	}

	provData, err := os.ReadFile(filepath.Join(outputDir, "provenance.json"))
	if err != nil {
		t.Fatalf("ReadFile(provenance.json) error = %v", err)
	}
	var provDoc any
	if err := json.Unmarshal(provData, &provDoc); err != nil {
		t.Fatalf("Unmarshal(provenance.json) error = %v", err)
	}
	if err := schemas["exohub-provenance"].Validate(provDoc); err != nil {
		t.Fatalf("provenance.json schema validation failed: %v\ndocument: %s", err, provData)
	}

	// Every record id in provenance.json's top-level "records" must appear
	// in at least one node's own "records", and every node's "records" ids
	// must exist in the top-level "records" (design D16 scope addition:
	// the "foreign key" between records[] and the activities/entities/agents
	// they assert).
	var prov provenance.ProvenanceDocument
	if err := json.Unmarshal(provData, &prov); err != nil {
		t.Fatalf("Unmarshal(provenance.json) into ProvenanceDocument error = %v", err)
	}
	topLevelRecords := map[string]bool{}
	for _, r := range prov.Records {
		topLevelRecords[r.ID] = true
	}
	if len(topLevelRecords) == 0 {
		t.Fatal("provenance.json has no top-level records")
	}
	seenInNodes := map[string]bool{}
	checkNodeRecords := func(kind, id string, records []string) {
		for _, rid := range records {
			seenInNodes[rid] = true
			if !topLevelRecords[rid] {
				t.Errorf("%s %s: records %q not in provenance.json's top-level records", kind, id, rid)
			}
		}
	}
	for _, a := range prov.Agents {
		checkNodeRecords("agent", a.ID, a.Records)
	}
	for _, a := range prov.Activities {
		checkNodeRecords("activity", a.ID, a.Records)
	}
	for _, e := range prov.Entities {
		checkNodeRecords("entity", e.ID, e.Records)
	}
	for rid := range topLevelRecords {
		if !seenInNodes[rid] {
			t.Errorf("record %q does not appear in any node's records", rid)
		}
	}

	// The folder member with no record of its own (f2.bin) inherits the
	// collection's lineage (design D16, "Lineage of folder collection
	// members").
	f2Data, err := os.ReadFile(filepath.Join(outputDir, "files", "results", "report.tdb", "f2.bin.json"))
	if err != nil {
		t.Fatalf("ReadFile(files/results/report.tdb/f2.bin.json) error = %v", err)
	}
	var f2 Artifact
	if err := json.Unmarshal(f2Data, &f2); err != nil {
		t.Fatalf("Unmarshal(f2.bin.json) error = %v", err)
	}
	if f2.Lineage == nil || !f2.Lineage.Covered {
		t.Fatalf("f2.bin lineage = %+v, want a covered lineage object inherited from the collection", f2.Lineage)
	}
	if f2.Lineage.Collection == nil || f2.Lineage.Collection.ID != treeID {
		t.Errorf("f2.bin lineage.collection = %+v, want id %s", f2.Lineage.Collection, treeID)
	}

	readmeData, err := os.ReadFile(filepath.Join(outputDir, "files", "README.md.json"))
	if err != nil {
		t.Fatalf("ReadFile(files/README.md.json) error = %v", err)
	}
	var readme MarkdownArtifact
	if err := json.Unmarshal(readmeData, &readme); err != nil {
		t.Fatalf("Unmarshal(README.md.json) error = %v", err)
	}
	if readme.Lineage == nil || len(readme.Lineage.AttributedTo) != 1 || readme.Lineage.AttributedTo[0] != "urn:exohub:agent:person:alice" {
		t.Errorf("README.md lineage = %+v, want attributed_to [urn:exohub:agent:person:alice]", readme.Lineage)
	}
}
