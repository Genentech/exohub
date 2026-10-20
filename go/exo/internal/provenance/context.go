// Package provenance implements the ExoHub provenance record format: the
// embedded JSON-LD context (design D1), the compact record profile (D2,
// D4), content-addressed identifiers (D5, D13), record file naming (D3)
// and state readers shared by the provenance commands (D15).
package provenance

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

// ContextIRI is the ExoHub provenance context IRI embedded in every record's
// @context. exo never fetches it over the network (design D1); it is also
// published at this IRI on the documentation site.
const ContextIRI = "https://genentech.github.io/exohub/ns/provenance/v1.jsonld"

// ExoNamespace is the IRI namespace for the exo: prefix used by
// ExoHub-specific terms (design D1).
const ExoNamespace = "https://genentech.github.io/exohub/ns/provenance#"

// ProvNamespace is the W3C PROV-O namespace that the unprefixed context
// aliases expand to (design D1).
const ProvNamespace = "http://www.w3.org/ns/prov#"

// RDFSNamespace is the namespace rdfs:label (the "label" alias) expands to.
const RDFSNamespace = "http://www.w3.org/2000/01/rdf-schema#"

//go:embed context/v1.jsonld
var contextDocument []byte

// ContextDocument returns a copy of the embedded context document bytes, as
// shipped in the exo binary and published at ContextIRI.
func ContextDocument() []byte {
	out := make([]byte, len(contextDocument))
	copy(out, contextDocument)
	return out
}

// ContextObject parses the embedded context document and returns the inner
// @context object itself (design D15 "Compiled document shape": "@context
// (the embedded context object)") — not the outer document that wraps it.
// The embedded file on disk is a full JSON-LD document shaped
// {"@context": {...the term table...}}; callers embed the returned value
// as the *value* of their own "@context" key, so returning the outer
// document would nest it twice ({"@context": {"@context": {...}}}), which
// is not valid JSON-LD.
func ContextObject() (map[string]any, error) {
	var doc map[string]any
	if err := json.Unmarshal(contextDocument, &doc); err != nil {
		return nil, fmt.Errorf("parse embedded provenance context: %w", err)
	}
	ctx, ok := doc["@context"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("embedded provenance context document has no @context object")
	}
	return ctx, nil
}

// nodeTypes maps the unprefixed node type aliases declared in the context
// to the PROV-O IRIs they expand to (design D1).
var nodeTypes = map[string]string{
	"Entity":        ProvNamespace + "Entity",
	"Collection":    ProvNamespace + "Collection",
	"Activity":      ProvNamespace + "Activity",
	"Agent":         ProvNamespace + "Agent",
	"Person":        ProvNamespace + "Person",
	"SoftwareAgent": ProvNamespace + "SoftwareAgent",
	"Organization":  ProvNamespace + "Organization",
}

// properties maps the unprefixed property aliases declared in the context
// to the IRIs they expand to (design D1): PROV-O properties, plus "label"
// which aliases rdfs:label.
var properties = map[string]string{
	"used":              ProvNamespace + "used",
	"wasGeneratedBy":    ProvNamespace + "wasGeneratedBy",
	"wasDerivedFrom":    ProvNamespace + "wasDerivedFrom",
	"wasRevisionOf":     ProvNamespace + "wasRevisionOf",
	"wasAssociatedWith": ProvNamespace + "wasAssociatedWith",
	"wasAttributedTo":   ProvNamespace + "wasAttributedTo",
	"actedOnBehalfOf":   ProvNamespace + "actedOnBehalfOf",
	"hadPlan":           ProvNamespace + "hadPlan",
	"startedAtTime":     ProvNamespace + "startedAtTime",
	"endedAtTime":       ProvNamespace + "endedAtTime",
	"generatedAtTime":   ProvNamespace + "generatedAtTime",
	"label":             RDFSNamespace + "label",
}

// exoTerms lists the local names of the exo: prefixed terms declared in the
// context (design D1, D15 "exo:records is added to the v1 term set").
var exoTerms = map[string]bool{
	"path":       true,
	"assertion":  true,
	"assertedBy": true,
	"evidence":   true,
	"supersedes": true,
	"model":      true,
	"version":    true,
	"fromRecord": true,
	"records":    true,
}

// NodeTypeIRI returns the PROV-O IRI an unprefixed node type alias (for
// example "Entity") expands to, and whether it is in the context term set.
func NodeTypeIRI(term string) (string, bool) {
	iri, ok := nodeTypes[term]
	return iri, ok
}

// PropertyIRI returns the IRI an unprefixed property alias (for example
// "wasGeneratedBy" or "label") expands to, and whether it is in the
// context term set.
func PropertyIRI(term string) (string, bool) {
	iri, ok := properties[term]
	return iri, ok
}

// IsExoTerm reports whether name (without its "exo:" prefix, for example
// "assertedBy") is a term declared by the embedded context.
func IsExoTerm(name string) bool {
	return exoTerms[name]
}

// ExpandTerm resolves a compact term as it would appear in a record (an
// unprefixed context alias such as "Entity" or "wasDerivedFrom", or an
// "exo:"-prefixed term such as "exo:assertedBy") to its full IRI. It
// reports false for any term outside the context term set (design D2).
func ExpandTerm(term string) (string, bool) {
	if rest, ok := cutPrefix(term, "exo:"); ok {
		if IsExoTerm(rest) {
			return ExoNamespace + rest, true
		}
		return "", false
	}
	if iri, ok := nodeTypes[term]; ok {
		return iri, true
	}
	if iri, ok := properties[term]; ok {
		return iri, true
	}
	return "", false
}

func cutPrefix(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && s[:len(prefix)] == prefix {
		return s[len(prefix):], true
	}
	return "", false
}

// isReservedContextName reports whether name is a prefix or term a record's
// additional context object must not redefine (design D2): the four
// namespace prefixes the embedded context declares, and every unprefixed
// node type or property alias.
func isReservedContextName(name string) bool {
	switch name {
	case "exo", "prov", "rdfs", "xsd":
		return true
	}
	if _, ok := nodeTypes[name]; ok {
		return true
	}
	if _, ok := properties[name]; ok {
		return true
	}
	return false
}

// ValidateContextPrefixes checks the additional prefixes a record declares
// in the array form of @context (design D2: "Additional properties SHALL
// use a prefix declared in an additional context object"): none may be
// empty, map to an empty IRI, or redefine exo, prov, rdfs, xsd or any term
// already declared by the embedded context.
func ValidateContextPrefixes(prefixes map[string]string) error {
	for prefix, iri := range prefixes {
		if prefix == "" {
			return fmt.Errorf("additional context prefix must not be empty")
		}
		if isReservedContextName(prefix) {
			return fmt.Errorf("additional context prefix %q redefines a reserved name", prefix)
		}
		if iri == "" {
			return fmt.Errorf("additional context prefix %q must map to a non-empty IRI", prefix)
		}
	}
	return nil
}

// ExpandNodeTerm resolves a @graph node's property or @type key: first
// against the fixed context term table (ExpandTerm), then, for
// "prefix:local" keys whose prefix is not covered by ExpandTerm, against
// prefixes — the additional prefixes a record declared in its @context
// array form (design D2). It reports false if term resolves against
// neither.
func ExpandNodeTerm(term string, prefixes map[string]string) (string, bool) {
	if iri, ok := ExpandTerm(term); ok {
		return iri, true
	}
	prefix, local, found := strings.Cut(term, ":")
	if !found || prefix == "" || local == "" {
		return "", false
	}
	base, ok := prefixes[prefix]
	if !ok {
		return "", false
	}
	return base + local, true
}
