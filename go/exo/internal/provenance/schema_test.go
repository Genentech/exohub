package provenance

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

// publishedSchemaPath resolves schemas/provenance-record/v1.json at the
// repository root, regardless of the working directory the test runs from.
func publishedSchemaPath(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "schemas", "provenance-record", "v1.json")
}

// TestPublishedSchemaMatchesEmbedded asserts schemas/provenance-record/v1.json
// is byte-for-byte the same file as the embedded copy go/exo uses to
// validate records (go:embed cannot reach outside its own package
// directory tree, so the single source of truth is enforced by this test
// instead): agents without `exo` must be able to validate a hand-written
// record against the exact schema `exo provenance validate` uses.
func TestPublishedSchemaMatchesEmbedded(t *testing.T) {
	published, err := os.ReadFile(publishedSchemaPath(t))
	if err != nil {
		t.Fatalf("read published schema: %v", err)
	}
	if !bytes.Equal(published, RecordSchemaDocument()) {
		t.Error("schemas/provenance-record/v1.json is not byte-identical to the embedded go/exo/internal/provenance/schema/record.schema.json")
	}
}

// TestReservedContextNameMatchesIsReservedContextName asserts the schema's
// definitions.reservedContextName.enum (the list the published/embedded
// schema forbids an additional @context object's keys from redefining) is
// exactly the set isReservedContextName is built from: the four namespace
// prefixes plus every unprefixed node type and property alias. This keeps
// the schema's hand-written enum in step with context.go without relying
// on reviewers to notice drift if a term is added or removed.
func TestReservedContextNameMatchesIsReservedContextName(t *testing.T) {
	data, err := os.ReadFile(publishedSchemaPath(t))
	if err != nil {
		t.Fatalf("read published schema: %v", err)
	}
	var doc struct {
		Definitions struct {
			ReservedContextName struct {
				Enum []string `json:"enum"`
			} `json:"reservedContextName"`
		} `json:"definitions"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse published schema: %v", err)
	}
	schemaNames := doc.Definitions.ReservedContextName.Enum
	if len(schemaNames) == 0 {
		t.Fatal("definitions.reservedContextName.enum is empty or missing")
	}

	want := map[string]bool{"exo": true, "prov": true, "rdfs": true, "xsd": true}
	for name := range nodeTypes {
		want[name] = true
	}
	for name := range properties {
		want[name] = true
	}

	got := make(map[string]bool, len(schemaNames))
	for _, name := range schemaNames {
		got[name] = true
	}

	var missing, extra []string
	for name := range want {
		if !got[name] {
			missing = append(missing, name)
		}
	}
	for name := range got {
		if !want[name] {
			extra = append(extra, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) != 0 {
		t.Errorf("reservedContextName.enum is missing names isReservedContextName treats as reserved: %v", missing)
	}
	if len(extra) != 0 {
		t.Errorf("reservedContextName.enum has names isReservedContextName does not treat as reserved: %v", extra)
	}

	// isReservedContextName must independently agree, name by name.
	for name := range want {
		if !isReservedContextName(name) {
			t.Errorf("isReservedContextName(%q) = false, want true (schema enum disagrees)", name)
		}
	}
}

// loadPublishedSchema compiles schemas/provenance-record/v1.json as
// Draft-07, independently of the embedded recordSchema() compiler.
func loadPublishedSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	path := publishedSchemaPath(t)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read published schema: %v", err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.Draft = jsonschema.Draft7
	if err := compiler.AddResource("provenance-record/v1.json", bytes.NewReader(data)); err != nil {
		t.Fatalf("add published schema resource: %v", err)
	}
	schema, err := compiler.Compile("provenance-record/v1.json")
	if err != nil {
		t.Fatalf("compile published schema: %v", err)
	}
	return schema
}

// validateDoc parses raw record JSON and validates it against schema,
// mirroring ValidateSchema's behavior for data that is not valid JSON.
func validateDoc(t *testing.T, schema *jsonschema.Schema, data []byte) error {
	t.Helper()
	var doc any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	return schema.Validate(doc)
}

// TestPublishedSchemaFixtureVerdicts asserts every fixture under
// testdata/valid and testdata/invalid gets the same pass/fail verdict from
// the published schema as it does from the embedded one (ValidateSchema):
// every valid fixture passes, and every invalid fixture fails except the
// ones the JSON Schema cannot reject by design (see schemaCannotReject in
// record_test.go — an undeclared prefix requires checking a node's
// "prefix:local" property against the record's own per-record context
// declarations, which only Go-level checks see; a reserved-prefix
// redefinition is schema-checkable and is rejected here).
func TestPublishedSchemaFixtureVerdicts(t *testing.T) {
	schema := loadPublishedSchema(t)

	validMatches, err := filepath.Glob("testdata/valid/*.jsonld")
	if err != nil {
		t.Fatalf("Glob() error = %v", err)
	}
	for _, path := range validMatches {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", path, err)
		}
		if err := validateDoc(t, schema, data); err != nil {
			t.Errorf("published schema rejected valid fixture %s: %v", path, err)
		}
	}

	invalidMatches, err := filepath.Glob("testdata/invalid/*.jsonld")
	if err != nil {
		t.Fatalf("Glob() error = %v", err)
	}
	for _, path := range invalidMatches {
		name := filepath.Base(path)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", path, err)
		}
		err = validateDoc(t, schema, data)
		if schemaCannotReject[name] {
			if err != nil {
				t.Errorf("published schema rejected %s, want it accepted (only Parse's Go-level checks reject this fixture): %v", path, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("published schema accepted invalid fixture %s, want it rejected", path)
		}
	}
}
