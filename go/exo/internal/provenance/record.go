package provenance

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Assertion is the exo:assertion value of a record (design D4).
type Assertion string

const (
	AssertionRecorded Assertion = "recorded"
	AssertionImported Assertion = "imported"
	AssertionAsserted Assertion = "asserted"
)

func (a Assertion) valid() bool {
	switch a {
	case AssertionRecorded, AssertionImported, AssertionAsserted:
		return true
	}
	return false
}

// Node is one PROV node in a record's @graph: an Entity, Activity or Agent.
// Its shape is intentionally open (design D2's profile is checked by
// ValidateSchema/ExpandTerm, not by a fixed Go struct), since the property
// set and value shapes (bare IRI string, {"@id": ...} object, or array of
// either) vary by node type.
type Node map[string]any

// ID returns the node's "@id", or "" if absent or not a string.
func (n Node) ID() string {
	id, _ := n["@id"].(string)
	return id
}

// Types returns the node's "@type" as a slice, accepting either a single
// string or an array of strings.
func (n Node) Types() []string {
	switch v := n["@type"].(type) {
	case string:
		return []string{v}
	case []any:
		out := make([]string, 0, len(v))
		for _, t := range v {
			if s, ok := t.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// Record is a parsed ExoHub provenance record envelope (design D4).
type Record struct {
	Context string `json:"@context"`
	// ContextPrefixes holds the additional prefix -> absolute IRI mappings
	// a record declares by writing @context as [ContextIRI, {...}] instead
	// of a bare string (design D2: "Additional properties SHALL use a
	// prefix declared in an additional context object"). It is nil if the
	// record used the plain string form.
	ContextPrefixes map[string]string `json:"-"`
	ID              string            `json:"@id"`
	Assertion       Assertion         `json:"exo:assertion"`
	AssertedBy      string            `json:"exo:assertedBy"`
	GeneratedAtTime string            `json:"generatedAtTime"`
	Evidence        string            `json:"exo:evidence,omitempty"`
	Supersedes      []string          `json:"exo:supersedes,omitempty"`
	Graph           []Node            `json:"@graph"`
}

// recordWire mirrors Record but keeps @context, exo:assertedBy and
// exo:supersedes as raw JSON: @context may be the bare context IRI string
// or [IRI, {prefix: IRI}] (design D2), and a hand-written record may spell
// an IRI-valued property as a bare string or as a compact {"@id": ...}
// object (both are valid JSON-LD once the context declares the property as
// "@type": "@id").
type recordWire struct {
	Context         json.RawMessage   `json:"@context"`
	ID              string            `json:"@id"`
	Assertion       Assertion         `json:"exo:assertion"`
	AssertedBy      json.RawMessage   `json:"exo:assertedBy"`
	GeneratedAtTime string            `json:"generatedAtTime"`
	Evidence        string            `json:"exo:evidence,omitempty"`
	Supersedes      []json.RawMessage `json:"exo:supersedes,omitempty"`
	Graph           []Node            `json:"@graph"`
}

// parseContextValue decodes a record's "@context" field: either the bare
// context IRI string, or a 2-element array of the IRI and an object of
// additional prefix -> absolute IRI mappings (design D2).
func parseContextValue(raw json.RawMessage) (iri string, prefixes map[string]string, err error) {
	if len(raw) == 0 {
		return "", nil, fmt.Errorf("is required")
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return asString, nil, nil
	}
	var asArray []json.RawMessage
	if err := json.Unmarshal(raw, &asArray); err != nil {
		return "", nil, fmt.Errorf("must be a string or [string, object]: %w", err)
	}
	if len(asArray) != 2 {
		return "", nil, fmt.Errorf("array form must have exactly 2 elements (the context IRI and a prefix object), got %d", len(asArray))
	}
	if err := json.Unmarshal(asArray[0], &iri); err != nil {
		return "", nil, fmt.Errorf("[0] must be the context IRI string: %w", err)
	}
	var prefixObj map[string]string
	if err := json.Unmarshal(asArray[1], &prefixObj); err != nil {
		return "", nil, fmt.Errorf("[1] must be an object mapping each additional prefix to an absolute IRI: %w", err)
	}
	return iri, prefixObj, nil
}

// idRef decodes a single IRI reference that may be written as a bare string
// or as a compact {"@id": "..."} object.
func idRef(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}
	var obj struct {
		ID string `json:"@id"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return "", fmt.Errorf("expected a string or {\"@id\": ...} object: %w", err)
	}
	if obj.ID == "" {
		return "", fmt.Errorf("expected a string or {\"@id\": ...} object")
	}
	return obj.ID, nil
}

// Parse decodes and validates a record envelope's required fields (design
// D4). It does not run the full closed-profile check (ExpandTerm covers
// that for callers that need it); it validates only what every reader of a
// record needs to trust: the envelope shape.
func Parse(data []byte) (*Record, error) {
	var wire recordWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return nil, fmt.Errorf("invalid record JSON: %w", err)
	}

	contextIRI, prefixes, err := parseContextValue(wire.Context)
	if err != nil {
		return nil, fmt.Errorf("@context %w", err)
	}
	if err := ValidateContextPrefixes(prefixes); err != nil {
		return nil, fmt.Errorf("@context: %w", err)
	}

	assertedBy, err := idRef(wire.AssertedBy)
	if err != nil {
		return nil, fmt.Errorf("exo:assertedBy: %w", err)
	}

	supersedes := make([]string, 0, len(wire.Supersedes))
	for i, raw := range wire.Supersedes {
		id, err := idRef(raw)
		if err != nil {
			return nil, fmt.Errorf("exo:supersedes[%d]: %w", i, err)
		}
		supersedes = append(supersedes, id)
	}

	r := &Record{
		Context:         contextIRI,
		ContextPrefixes: prefixes,
		ID:              wire.ID,
		Assertion:       wire.Assertion,
		AssertedBy:      assertedBy,
		GeneratedAtTime: wire.GeneratedAtTime,
		Evidence:        wire.Evidence,
		Supersedes:      supersedes,
		Graph:           wire.Graph,
	}
	if err := r.validateEnvelope(); err != nil {
		return nil, err
	}
	if err := r.validateGraph(); err != nil {
		return nil, err
	}
	return r, nil
}

// validateEnvelope checks the required envelope fields (design D4, spec
// "Record envelope").
func (r *Record) validateEnvelope() error {
	if r.Context != ContextIRI {
		return fmt.Errorf("@context must be %q, got %q", ContextIRI, r.Context)
	}
	if !strings.HasPrefix(r.ID, "urn:exohub:record:") {
		return fmt.Errorf("@id must be urn:exohub:record:<UUIDv7>, got %q", r.ID)
	}
	if !r.Assertion.valid() {
		return fmt.Errorf("exo:assertion must be recorded, imported or asserted, got %q", r.Assertion)
	}
	if r.AssertedBy == "" {
		return fmt.Errorf("exo:assertedBy is required")
	}
	if r.GeneratedAtTime == "" {
		return fmt.Errorf("generatedAtTime is required")
	}
	if len(r.Graph) == 0 {
		return fmt.Errorf("@graph must be non-empty")
	}
	for i, n := range r.Graph {
		if n.ID() == "" {
			return fmt.Errorf("@graph[%d] is missing @id", i)
		}
	}
	return nil
}

// validateGraph checks every node's @type and property keys against the
// context term set, plus any additional prefixes the record declared in
// its @context array form (design D2: "Additional properties SHALL use a
// prefix declared in an additional context object"). A property or type
// that resolves against neither is rejected, so a typo or an undeclared
// prefix is caught at parse time rather than silently dropped.
func (r *Record) validateGraph() error {
	for i, n := range r.Graph {
		for _, typ := range n.Types() {
			if _, ok := NodeTypeIRI(typ); !ok {
				return fmt.Errorf("@graph[%d] (%s): @type %q is not a context node type", i, n.ID(), typ)
			}
		}
		for key := range n {
			if key == "@id" || key == "@type" {
				continue
			}
			if _, ok := ExpandNodeTerm(key, r.ContextPrefixes); !ok {
				return fmt.Errorf("@graph[%d] (%s): property %q is not a context term or a declared prefix", i, n.ID(), key)
			}
		}
	}
	return nil
}

// Serialize renders the record as deterministically formatted JSON: fields
// are emitted in a fixed order (Go struct field order, preserved by
// encoding/json), @graph nodes are sorted by @id, and multi-valued (array
// of string) node properties are sorted (design D15 "deterministic...
// nodes are sorted by @id, lists are sorted, and the JSON has stable key
// order").
func Serialize(r *Record) ([]byte, error) {
	graph := make([]Node, len(r.Graph))
	for i, n := range r.Graph {
		graph[i] = normalizeNode(n)
	}
	sort.Slice(graph, func(i, j int) bool { return graph[i].ID() < graph[j].ID() })

	// @context is the bare IRI string unless the record declared additional
	// prefixes, in which case it round-trips as [IRI, {prefix: IRI}]
	// (design D2).
	var context any = r.Context
	if len(r.ContextPrefixes) > 0 {
		context = []any{r.Context, r.ContextPrefixes}
	}

	out := struct {
		Context         any       `json:"@context"`
		ID              string    `json:"@id"`
		Assertion       Assertion `json:"exo:assertion"`
		AssertedBy      string    `json:"exo:assertedBy"`
		GeneratedAtTime string    `json:"generatedAtTime"`
		Evidence        string    `json:"exo:evidence,omitempty"`
		Supersedes      []string  `json:"exo:supersedes,omitempty"`
		Graph           []Node    `json:"@graph"`
	}{
		Context:         context,
		ID:              r.ID,
		Assertion:       r.Assertion,
		AssertedBy:      r.AssertedBy,
		GeneratedAtTime: r.GeneratedAtTime,
		Evidence:        r.Evidence,
		Supersedes:      append([]string(nil), r.Supersedes...),
		Graph:           graph,
	}
	sort.Strings(out.Supersedes)

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("serialize record: %w", err)
	}
	return append(data, '\n'), nil
}

// normalizeNode returns a copy of n with every array-of-strings value
// sorted, for deterministic serialization. Node keys are not reordered here
// since Go's encoding/json already marshals map[string]any keys in sorted
// order.
func normalizeNode(n Node) Node {
	out := make(Node, len(n))
	for k, v := range n {
		out[k] = normalizeValue(v)
	}
	return out
}

func normalizeValue(v any) any {
	list, ok := v.([]any)
	if !ok {
		return v
	}
	out := make([]any, len(list))
	allStrings := true
	for i, item := range list {
		out[i] = item
		if _, ok := item.(string); !ok {
			allStrings = false
		}
	}
	if allStrings {
		strs := make([]string, len(out))
		for i, item := range out {
			strs[i] = item.(string)
		}
		sort.Strings(strs)
		for i, s := range strs {
			out[i] = s
		}
	}
	return out
}
