package provenance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContextIRIMatchesEmbeddedDocument(t *testing.T) {
	ctx, err := ContextObject()
	if err != nil {
		t.Fatalf("ContextObject() error = %v", err)
	}
	if ctx["exo"] != ExoNamespace {
		t.Errorf("exo prefix = %v, want %v", ctx["exo"], ExoNamespace)
	}
	if ctx["prov"] != ProvNamespace {
		t.Errorf("prov prefix = %v, want %v", ctx["prov"], ProvNamespace)
	}
}

// TestContextObjectIsNotDoubleWrapped is a regression test for review item
// 2 on !257: ContextObject() must return the context *object* itself
// (prov/exo/... term table), not the outer JSON-LD document that wraps it
// — embedding the latter as a caller's "@context" value produces the
// invalid {"@context": {"@context": {...}}}.
func TestContextObjectIsNotDoubleWrapped(t *testing.T) {
	ctx, err := ContextObject()
	if err != nil {
		t.Fatalf("ContextObject() error = %v", err)
	}
	if _, ok := ctx["@context"]; ok {
		t.Fatalf("ContextObject() = %+v, must not itself contain an @context key", ctx)
	}
	if _, ok := ctx["prov"]; !ok {
		t.Error("ContextObject() missing the prov prefix")
	}
	if _, ok := ctx["exo"]; !ok {
		t.Error("ContextObject() missing the exo prefix")
	}
}

func TestContextDocumentIsACopy(t *testing.T) {
	a := ContextDocument()
	a[0] = '!'
	b := ContextDocument()
	if b[0] == '!' {
		t.Fatalf("ContextDocument() returned a shared buffer, mutation leaked")
	}
}

func TestExpandTermKnownAliases(t *testing.T) {
	tests := []struct {
		term string
		want string
	}{
		{"Entity", ProvNamespace + "Entity"},
		{"Activity", ProvNamespace + "Activity"},
		{"wasGeneratedBy", ProvNamespace + "wasGeneratedBy"},
		{"label", RDFSNamespace + "label"},
		{"exo:path", ExoNamespace + "path"},
		{"exo:fromRecord", ExoNamespace + "fromRecord"},
	}
	for _, tt := range tests {
		got, ok := ExpandTerm(tt.term)
		if !ok {
			t.Errorf("ExpandTerm(%q) not found, want %q", tt.term, tt.want)
			continue
		}
		if got != tt.want {
			t.Errorf("ExpandTerm(%q) = %q, want %q", tt.term, got, tt.want)
		}
	}
}

func TestExpandTermRejectsUnknownTerms(t *testing.T) {
	for _, term := range []string{"producedBy", "exo:notATerm", "unknown"} {
		if _, ok := ExpandTerm(term); ok {
			t.Errorf("ExpandTerm(%q) unexpectedly resolved", term)
		}
	}
}

func TestExpandNodeTermUsesDeclaredPrefixes(t *testing.T) {
	prefixes := map[string]string{"myorg": "https://example.org/ns/myorg#"}

	got, ok := ExpandNodeTerm("myorg:batchId", prefixes)
	if !ok || got != "https://example.org/ns/myorg#batchId" {
		t.Errorf("ExpandNodeTerm(myorg:batchId) = (%q, %v), want (https://example.org/ns/myorg#batchId, true)", got, ok)
	}

	if got, ok := ExpandNodeTerm("Entity", prefixes); !ok || got != ProvNamespace+"Entity" {
		t.Errorf("ExpandNodeTerm(Entity) = (%q, %v), want context term table result", got, ok)
	}

	if _, ok := ExpandNodeTerm("other:batchId", prefixes); ok {
		t.Errorf("ExpandNodeTerm(other:batchId) unexpectedly resolved against an undeclared prefix")
	}

	if _, ok := ExpandNodeTerm("myorg:batchId", nil); ok {
		t.Errorf("ExpandNodeTerm(myorg:batchId, nil) unexpectedly resolved with no declared prefixes")
	}
}

func TestValidateContextPrefixesRejectsReservedNames(t *testing.T) {
	for _, prefix := range []string{"exo", "prov", "rdfs", "xsd", "Entity", "wasGeneratedBy"} {
		err := ValidateContextPrefixes(map[string]string{prefix: "https://example.org/x#"})
		if err == nil {
			t.Errorf("ValidateContextPrefixes(%q) expected an error", prefix)
		}
	}
}

func TestValidateContextPrefixesAcceptsNewPrefix(t *testing.T) {
	if err := ValidateContextPrefixes(map[string]string{"myorg": "https://example.org/ns/myorg#"}); err != nil {
		t.Errorf("ValidateContextPrefixes() error = %v", err)
	}
	if err := ValidateContextPrefixes(nil); err != nil {
		t.Errorf("ValidateContextPrefixes(nil) error = %v", err)
	}
}

// TestValidFixturesExpandToPROVO proves the context's term table maps every
// valid fixture's types and properties to their expected IRI (design D1/D2,
// spec "Record vocabulary": "every type and property resolves to its
// http://www.w3.org/ns/prov# IRI"). The expected IRIs are committed fixtures
// (design D15 "Libraries": no JSON-LD processor dependency), so the test
// also catches accidental edits to the embedded context.
func TestValidFixturesExpandToPROVO(t *testing.T) {
	matches, err := filepath.Glob("testdata/valid/*.jsonld")
	if err != nil {
		t.Fatalf("Glob() error = %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("no valid fixtures found")
	}

	for _, path := range matches {
		name := strings.TrimSuffix(filepath.Base(path), ".jsonld")
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("ReadFile() error = %v", err)
			}
			record, err := Parse(data)
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}

			expandedPath := filepath.Join("testdata", "expanded", name+".json")
			expectedRaw, err := os.ReadFile(expandedPath)
			if err != nil {
				t.Fatalf("ReadFile(%s) error = %v", expandedPath, err)
			}
			var expected map[string]string
			if err := json.Unmarshal(expectedRaw, &expected); err != nil {
				t.Fatalf("parse %s: %v", expandedPath, err)
			}

			terms := collectTerms(record)
			for term, wantIRI := range expected {
				gotIRI, ok := ExpandNodeTerm(term, record.ContextPrefixes)
				if !ok {
					t.Errorf("term %q (expected in fixture) does not expand", term)
					continue
				}
				if gotIRI != wantIRI {
					t.Errorf("ExpandNodeTerm(%q) = %q, want %q", term, gotIRI, wantIRI)
				}
			}
			for term := range terms {
				if _, ok := expected[term]; !ok {
					t.Errorf("fixture uses term %q with no entry in %s", term, expandedPath)
				}
			}
		})
	}
}

// collectTerms returns the set of every @type value and property name used
// across a record's @graph nodes.
func collectTerms(r *Record) map[string]bool {
	terms := map[string]bool{}
	for _, node := range r.Graph {
		for _, typ := range node.Types() {
			terms[typ] = true
		}
		for key := range node {
			if key == "@id" || key == "@type" {
				continue
			}
			terms[key] = true
		}
	}
	return terms
}
