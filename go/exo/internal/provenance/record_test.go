package provenance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestParseValidFixtures(t *testing.T) {
	matches, err := filepath.Glob("testdata/valid/*.jsonld")
	if err != nil {
		t.Fatalf("Glob() error = %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("no valid fixtures found")
	}
	for _, path := range matches {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("ReadFile() error = %v", err)
			}
			record, err := Parse(data)
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			if record.Context != ContextIRI {
				t.Errorf("Context = %q, want %q", record.Context, ContextIRI)
			}
			if len(record.Graph) == 0 {
				t.Errorf("Graph is empty")
			}
		})
	}
}

func TestParseInvalidFixtures(t *testing.T) {
	matches, err := filepath.Glob("testdata/invalid/*.jsonld")
	if err != nil {
		t.Fatalf("Glob() error = %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("no invalid fixtures found")
	}
	for _, path := range matches {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("ReadFile() error = %v", err)
			}
			if _, err := Parse(data); err == nil {
				t.Errorf("Parse(%s) expected an error, got none", path)
			}
		})
	}
}

func TestParseAssertedByObjectForm(t *testing.T) {
	data := []byte(`{
		"@context": "` + ContextIRI + `",
		"@id": "urn:exohub:record:01928c9e-0000-7000-8000-0000000000aa",
		"exo:assertion": "asserted",
		"exo:assertedBy": {"@id": "urn:exohub:agent:software:claude-code"},
		"generatedAtTime": "2026-10-14T10:12:00Z",
		"@graph": [{"@id": "urn:exohub:git:blob:abc", "@type": "Entity"}]
	}`)
	record, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if record.AssertedBy != "urn:exohub:agent:software:claude-code" {
		t.Errorf("AssertedBy = %q, want urn:exohub:agent:software:claude-code", record.AssertedBy)
	}
}

func TestSerializeIsDeterministic(t *testing.T) {
	data, err := os.ReadFile("testdata/valid/derivation.jsonld")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	record, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	out1, err := Serialize(record)
	if err != nil {
		t.Fatalf("Serialize() error = %v", err)
	}
	out2, err := Serialize(record)
	if err != nil {
		t.Fatalf("Serialize() error = %v", err)
	}
	if string(out1) != string(out2) {
		t.Fatalf("Serialize() is not deterministic across calls")
	}

	// Round trip: re-parsing the serialized bytes must produce an
	// equivalent record.
	roundTripped, err := Parse(out1)
	if err != nil {
		t.Fatalf("Parse(Serialize()) error = %v", err)
	}
	if roundTripped.ID != record.ID || roundTripped.Assertion != record.Assertion {
		t.Errorf("round trip changed envelope fields: got %+v, want %+v", roundTripped, record)
	}
}

func TestSerializeSortsGraphByID(t *testing.T) {
	record := &Record{
		Context:         ContextIRI,
		ID:              "urn:exohub:record:01928c9e-0000-7000-8000-0000000000bb",
		Assertion:       AssertionAsserted,
		AssertedBy:      "urn:exohub:agent:person:jrivers",
		GeneratedAtTime: "2026-10-14T10:12:00Z",
		Graph: []Node{
			{"@id": "urn:exohub:git:blob:zzz", "@type": "Entity"},
			{"@id": "urn:exohub:git:blob:aaa", "@type": "Entity"},
		},
	}
	out, err := Serialize(record)
	if err != nil {
		t.Fatalf("Serialize() error = %v", err)
	}
	var decoded struct {
		Graph []map[string]any `json:"@graph"`
	}
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if decoded.Graph[0]["@id"] != "urn:exohub:git:blob:aaa" || decoded.Graph[1]["@id"] != "urn:exohub:git:blob:zzz" {
		t.Errorf("graph not sorted by @id: %+v", decoded.Graph)
	}
}

func TestSerializeSortsMultiValuedProperties(t *testing.T) {
	record := &Record{
		Context:         ContextIRI,
		ID:              "urn:exohub:record:01928c9e-0000-7000-8000-0000000000cc",
		Assertion:       AssertionAsserted,
		AssertedBy:      "urn:exohub:agent:person:jrivers",
		GeneratedAtTime: "2026-10-14T10:12:00Z",
		Graph: []Node{
			{
				"@id":   "urn:exohub:activity:01928c9e-0000-7000-8000-0000000000dd",
				"@type": "Activity",
				"used":  []any{"urn:exohub:git:blob:zzz", "urn:exohub:git:blob:aaa"},
			},
		},
	}
	out, err := Serialize(record)
	if err != nil {
		t.Fatalf("Serialize() error = %v", err)
	}
	var decoded struct {
		Graph []map[string]any `json:"@graph"`
	}
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	used, ok := decoded.Graph[0]["used"].([]any)
	if !ok || len(used) != 2 {
		t.Fatalf("used = %v, want a 2-element array", decoded.Graph[0]["used"])
	}
	if used[0] != "urn:exohub:git:blob:aaa" || used[1] != "urn:exohub:git:blob:zzz" {
		t.Errorf("used not sorted: %v", used)
	}
}

func TestValidateSchemaAcceptsValidFixtures(t *testing.T) {
	matches, err := filepath.Glob("testdata/valid/*.jsonld")
	if err != nil {
		t.Fatalf("Glob() error = %v", err)
	}
	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile() error = %v", err)
		}
		issues, err := ValidateSchema(data)
		if err != nil {
			t.Fatalf("ValidateSchema(%s) error = %v", path, err)
		}
		if len(issues) != 0 {
			t.Errorf("ValidateSchema(%s) = %v, want no issues", path, issues)
		}
	}
}

// schemaCannotReject lists invalid fixtures that the JSON Schema cannot
// reject by design: the schema has no visibility into which additional
// prefixes a record declares when checking a node's "prefix:local"
// properties, so it allows any "prefix:local"-shaped node property
// regardless of whether its prefix was actually declared in the record's
// own @context object (a cross-field check). The schema does reject a
// redefinition of a reserved name in the additional context object itself
// (reservedContextName, kept in step with isReservedContextName). Only
// Record.Parse's validateGraph/ValidateContextPrefixes reject an undeclared
// prefix.
var schemaCannotReject = map[string]bool{
	"undeclared-prefix.jsonld": true,
}

func TestValidateSchemaRejectsInvalidFixtures(t *testing.T) {
	matches, err := filepath.Glob("testdata/invalid/*.jsonld")
	if err != nil {
		t.Fatalf("Glob() error = %v", err)
	}
	for _, path := range matches {
		name := filepath.Base(path)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile() error = %v", err)
		}
		issues, err := ValidateSchema(data)
		if err != nil {
			// A schema-level parse error also counts as rejection.
			continue
		}
		if schemaCannotReject[name] {
			if len(issues) != 0 {
				t.Errorf("ValidateSchema(%s) = %v, want no issues (only Parse's Go-level checks reject this fixture)", path, issues)
			}
			continue
		}
		if len(issues) == 0 {
			t.Errorf("ValidateSchema(%s) = no issues, want at least one", path)
		}
	}
}

func TestParseAcceptsExtensionPropertyFixture(t *testing.T) {
	data, err := os.ReadFile("testdata/valid/extension-property.jsonld")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	record, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if record.ContextPrefixes["myorg"] != "https://example.org/ns/myorg#" {
		t.Errorf("ContextPrefixes[myorg] = %q, want https://example.org/ns/myorg#", record.ContextPrefixes["myorg"])
	}

	out, err := Serialize(record)
	if err != nil {
		t.Fatalf("Serialize() error = %v", err)
	}
	var decoded struct {
		Context []any `json:"@context"`
	}
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(decoded.Context) != 2 {
		t.Fatalf("Serialize() @context = %v, want a 2-element array (round-tripped prefixes)", decoded.Context)
	}
	if decoded.Context[0] != ContextIRI {
		t.Errorf("Serialize() @context[0] = %v, want %q", decoded.Context[0], ContextIRI)
	}

	// Re-parsing the serialized bytes must still accept the extension
	// property.
	if _, err := Parse(out); err != nil {
		t.Errorf("Parse(Serialize()) error = %v", err)
	}
}

func TestParseRejectsUndeclaredPrefix(t *testing.T) {
	data, err := os.ReadFile("testdata/invalid/undeclared-prefix.jsonld")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if _, err := Parse(data); err == nil {
		t.Errorf("Parse() expected an error for an undeclared prefix")
	}
}

func TestParseRejectsReservedPrefixRedefinition(t *testing.T) {
	data, err := os.ReadFile("testdata/invalid/reserved-prefix.jsonld")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if _, err := Parse(data); err == nil {
		t.Errorf("Parse() expected an error for a record that redefines the exo prefix")
	}
}

func TestParseAcceptsSupersedesIDRefObjectForm(t *testing.T) {
	data := []byte(`{
		"@context": "` + ContextIRI + `",
		"@id": "urn:exohub:record:01928c9e-0000-7000-8000-0000000000bc",
		"exo:assertion": "asserted",
		"exo:assertedBy": "urn:exohub:agent:person:jrivers",
		"generatedAtTime": "2026-10-14T10:12:00Z",
		"exo:supersedes": [{"@id": "urn:exohub:record:01928c9e-0000-7000-8000-0000000000bd"}],
		"@graph": [{"@id": "urn:exohub:git:blob:abc", "@type": "Entity"}]
	}`)
	record, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(record.Supersedes) != 1 || record.Supersedes[0] != "urn:exohub:record:01928c9e-0000-7000-8000-0000000000bd" {
		t.Errorf("Supersedes = %v, want the decoded idRef object", record.Supersedes)
	}

	// The schema must accept the same {"@id": ...} shape (non-blocking
	// review item: Parse and the schema must agree on exo:supersedes).
	issues, err := ValidateSchema(data)
	if err != nil {
		t.Fatalf("ValidateSchema() error = %v", err)
	}
	if len(issues) != 0 {
		t.Errorf("ValidateSchema() = %v, want no issues", issues)
	}
}
