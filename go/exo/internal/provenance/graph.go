package provenance

import "sort"

// RecordMeta is the per-record metadata exo provenance show attaches to
// every statement it prints, as that statement's "assertion source"
// (design D9).
type RecordMeta struct {
	Path            string
	Assertion       Assertion
	AssertedBy      string
	GeneratedAtTime string
}

// MergedNode is one @graph node merged across every non-superseded record
// that defines it (design D15 "Compiled document shape"). FromRecords
// lists every contributing record id.
type MergedNode struct {
	Node
	FromRecords []string
}

// MergedGraph is a provenance record set merged by node @id (design D15).
type MergedGraph struct {
	Nodes   map[string]*MergedNode
	Records map[string]RecordMeta
}

// mergeMostRecentWins lists node properties where merging a node across
// records keeps only the most recent non-superseded record's value (by
// envelope generatedAtTime) instead of a union: the single-valued
// properties design D15 defines for conflict detection, plus "label",
// which D15 says comes from the most recent non-superseded record.
var mergeMostRecentWins = map[string]bool{
	"startedAtTime":   true,
	"endedAtTime":     true,
	"generatedAtTime": true,
	"wasGeneratedBy":  true,
	"exo:model":       true,
	"exo:version":     true,
	"label":           true,
	"exo:path":        true,
}

// LoadRecords parses every non-annexed, well-formed provenance record at
// reader's checked state, silently skipping a record file that is annexed
// or fails to parse: unlike Validate, the lineage walk works with whatever
// a possibly-imperfect repository has rather than failing the whole
// command over one bad record file.
func LoadRecords(reader *Reader) ([]parsedRecord, error) {
	files, err := reader.ListRecords()
	if err != nil {
		return nil, err
	}
	var parsed []parsedRecord
	for _, f := range files {
		if f.Annexed {
			continue
		}
		rec, err := Parse(f.Data)
		if err != nil {
			continue
		}
		parsed = append(parsed, parsedRecord{path: f.Path, rec: rec})
	}
	return parsed, nil
}

// MergeRecords merges parsed's non-superseded records into one graph by
// node @id (design D15). Records are applied oldest to newest (by envelope
// generatedAtTime), so a mergeMostRecentWins property ends up holding the
// latest record's value; every other property merges as a deduplicated
// union of referenced ids.
func MergeRecords(parsed []parsedRecord) *MergedGraph {
	superseded := supersededIDs(parsed)

	ordered := append([]parsedRecord(nil), parsed...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].rec.GeneratedAtTime < ordered[j].rec.GeneratedAtTime
	})

	g := &MergedGraph{Nodes: map[string]*MergedNode{}, Records: map[string]RecordMeta{}}
	for _, p := range parsed {
		g.Records[p.rec.ID] = RecordMeta{
			Path:            p.path,
			Assertion:       p.rec.Assertion,
			AssertedBy:      p.rec.AssertedBy,
			GeneratedAtTime: p.rec.GeneratedAtTime,
		}
	}

	for _, p := range ordered {
		if superseded[p.rec.ID] {
			continue
		}
		for _, n := range p.rec.Graph {
			id := n.ID()
			m := g.Nodes[id]
			if m == nil {
				m = &MergedNode{Node: Node{"@id": id}}
				g.Nodes[id] = m
			}
			for key, value := range n {
				switch {
				case key == "@id":
					continue
				case key == "@type":
					m.Node["@type"] = mergeRefs(m.Node["@type"], value)
				case mergeMostRecentWins[key]:
					m.Node[key] = value
				default:
					m.Node[key] = mergeRefs(m.Node[key], value)
				}
			}
			m.FromRecords = appendUniqueID(m.FromRecords, p.rec.ID)
		}
	}
	return g
}

// nodeRefs extracts every id a property value references, accepting the
// shapes a hand-written record may use: a bare IRI string, a compact
// {"@id": ...} object, or an array of either (design D2).
func nodeRefs(v any) []string {
	switch t := v.(type) {
	case string:
		return []string{t}
	case map[string]any:
		if id, ok := t["@id"].(string); ok {
			return []string{id}
		}
	case []any:
		var out []string
		for _, item := range t {
			out = append(out, nodeRefs(item)...)
		}
		return out
	}
	return nil
}

// mergeRefs merges two property values (each a bare string, {"@id": ...}
// object, or array of either, design D2) into a deduplicated, sorted union.
func mergeRefs(existing, incoming any) any {
	seen := map[string]bool{}
	var ids []string
	for _, id := range nodeRefs(existing) {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	for _, id := range nodeRefs(incoming) {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	if len(ids) == 1 {
		return ids[0]
	}
	out := make([]any, len(ids))
	for i, id := range ids {
		out[i] = id
	}
	return out
}

func appendUniqueID(ids []string, id string) []string {
	for _, existing := range ids {
		if existing == id {
			return ids
		}
	}
	return append(ids, id)
}
