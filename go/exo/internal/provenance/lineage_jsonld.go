package provenance

import "sort"

// RecordRef is one contributing record's entry in the --json document's
// exo:records list (design D15 "Compiled document shape": "exo:records
// (one entry per included record with @id, exo:assertion, exo:assertedBy
// and generatedAtTime)") — the same shape #121's compiled provenance.jsonld
// uses, so one consumer can read both (review item 3 on !257).
type RecordRef struct {
	ID              string    `json:"@id"`
	Assertion       Assertion `json:"exo:assertion"`
	AssertedBy      string    `json:"exo:assertedBy"`
	GeneratedAtTime string    `json:"generatedAtTime"`
}

// LineageDocument is the `exo provenance show --json` output (design D9:
// "--json emits the lineage subgraph as JSON-LD"): the embedded context
// plus every node the walk reached, each carrying exo:fromRecord (design
// D15 "Compiled document shape"). Subject, fallback and cycle information,
// which has no JSON-LD term of its own, sits alongside @graph rather than
// inside it.
type LineageDocument struct {
	Context    any                `json:"@context"`
	Subject    string             `json:"subject"`
	Source     LineageSource      `json:"source"`
	FolderPath string             `json:"folderPath,omitempty"`
	Hint       *FolderHistoryHint `json:"olderCoveredTreeHint,omitempty"`
	Cycles     []string           `json:"cycles,omitempty"`
	Graph      []Node             `json:"@graph"`
	Records    []RecordRef        `json:"exo:records,omitempty"`
}

// BuildLineageDocument renders lineage as the --json subgraph document: one
// node per id lineage.Visited reached, each a copy of its merged node with
// "exo:fromRecord" set to its contributing record ids, sorted by @id for
// determinism (design D15 "nodes are sorted by @id").
func BuildLineageDocument(lineage *Lineage) (*LineageDocument, error) {
	context, err := ContextObject()
	if err != nil {
		return nil, err
	}

	graph := make([]Node, 0, len(lineage.Visited))
	recordIDs := map[string]bool{}
	for _, id := range lineage.Visited {
		merged := lineage.Graph.Nodes[id]
		if merged == nil {
			continue
		}
		node := make(Node, len(merged.Node)+1)
		for k, v := range merged.Node {
			node[k] = normalizeValue(v)
		}
		// exo:path is set from the ref, the same rule exo bundle's
		// compilation uses (design D8 step 6, D15 "In compiled output,
		// exo:path is set from the ref") — review: "the same rule #121
		// uses for compilation". A node with no current path (not present
		// at Ref) keeps its recorded exo:path untouched.
		if path, ok := lineage.Paths.Path(id); ok {
			node["exo:path"] = path
		}
		fromRecords := append([]string(nil), merged.FromRecords...)
		sort.Strings(fromRecords)
		node["exo:fromRecord"] = fromRecords
		graph = append(graph, node)
		for _, recID := range fromRecords {
			recordIDs[recID] = true
		}
	}
	sort.Slice(graph, func(i, j int) bool { return graph[i].ID() < graph[j].ID() })

	records := make([]RecordRef, 0, len(recordIDs))
	for recID := range recordIDs {
		meta, ok := lineage.Graph.Records[recID]
		if !ok {
			continue
		}
		records = append(records, RecordRef{
			ID:              recID,
			Assertion:       meta.Assertion,
			AssertedBy:      meta.AssertedBy,
			GeneratedAtTime: meta.GeneratedAtTime,
		})
	}
	sort.Slice(records, func(i, j int) bool { return records[i].ID < records[j].ID })

	return &LineageDocument{
		Context:    context,
		Subject:    lineage.Subject.ID,
		Source:     lineage.Subject.Source,
		FolderPath: lineage.Subject.FolderPath,
		Hint:       lineage.Hint,
		Cycles:     lineage.Cycles,
		Graph:      graph,
		Records:    records,
	}, nil
}
