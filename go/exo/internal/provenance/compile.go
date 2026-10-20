package provenance

import (
	"encoding/json"
	"fmt"
	"sort"
)

// CompileBundle follows the same upstream properties exo provenance show's
// Walk does (design D8.5, lineage.go upstreamProperties) to keep an
// artifact's upstream lineage in scope even when the referenced node is
// outside the bundle, and the same nodeRefs (graph.go) to extract a
// property value's id references.

// CompiledRecordRef is one entry in a compiled document's exo:records list
// (design D15 "Compiled document shape").
type CompiledRecordRef struct {
	ID              string    `json:"@id"`
	Assertion       Assertion `json:"exo:assertion"`
	AssertedBy      string    `json:"exo:assertedBy"`
	GeneratedAtTime string    `json:"generatedAtTime"`
}

// CompiledDocument is exo bundle's compiled provenance.jsonld (design D8,
// D15 "Compiled document shape"): the embedded context object, the
// records that contributed a statement to Graph, and the scoped, merged
// node graph itself.
type CompiledDocument struct {
	Context any                 `json:"@context"`
	Records []CompiledRecordRef `json:"exo:records"`
	Graph   []Node              `json:"@graph"`
}

// Serialize renders doc as deterministic JSON: @graph is sorted by @id and
// every multi-valued property is sorted (design D15 "deterministic...
// nodes are sorted by @id, lists are sorted, and the JSON has stable key
// order").
func (d *CompiledDocument) Serialize() ([]byte, error) {
	data, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("serialize compiled document: %w", err)
	}
	return append(data, '\n'), nil
}

// CompileResult is CompileBundle's successful output.
type CompileResult struct {
	// Document is nil when there are no records, or no record's graph is
	// in scope for the bundle (design D8/D15: a bundle with nothing to
	// compile writes no provenance.jsonld).
	Document    *CompiledDocument
	RecordCount int
	EntityCount int
	// Lineage is exo bundle's per-artifact lineage (design D16), keyed by
	// repo-relative path (files by their path, folder collections by their
	// path with a trailing slash). It is nil when Document is nil.
	Lineage map[string]*ArtifactLineage
	// Catalog is exo bundle's provenance.json (design D16). It is nil when
	// Document is nil.
	Catalog *ProvenanceDocument
}

// CompileBundle reads provenance records from reader (a ModeRef Reader for
// the bundle's target ref, design D8.1), validates them the same way `exo
// provenance validate` does (profile, identifier, cross-record and
// immutability checks — not coverage/provenance.require, which a bundle
// enforces separately via RequireViolations), and — when there are no
// errors — compiles a provenance.jsonld document (design D8) scoped to
// selected, the bundle's selected file set at the ref, plus the transitive
// upstream of those artifacts and the asserting agents of every
// contributing record (design D8.5).
//
// selected is repo-relative file paths (not directories). A folder
// collection is added to scope automatically when a merged record node is
// a Collection whose tree id matches an ancestor directory of a selected
// path at the ref (design D8.5 "Folder collections").
//
// CompileBundle itself never fails on an ordinary validation Finding:
// callers (for example `exo bundle`) should check report.HasErrors() and
// abort before writing any bundle output (design D8.2). CompileBundle
// returns a non-nil error only for a lower-level failure, for example a
// git command error.
func CompileBundle(reader *Reader, selected []string) (*CompileResult, *Report, error) {
	report := &Report{}

	parsed, err := parseRecordFiles(reader, report)
	if err != nil {
		return nil, nil, err
	}
	checkCrossRecord(report, parsed)
	checkIdentifiers(reader, report, parsed)
	if err := checkImmutability(reader, report); err != nil {
		return nil, nil, err
	}

	if report.HasErrors() {
		return nil, report, nil
	}
	if len(parsed) == 0 {
		return &CompileResult{}, report, nil
	}

	merged := MergeRecords(parsed)

	entries, err := reader.FileEntries()
	if err != nil {
		return nil, nil, fmt.Errorf("list tracked files: %w", err)
	}
	contentIDs, err := reader.AllContentIDs(entries)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve content ids: %w", err)
	}
	treeIDs, err := reader.AllTreeIDs()
	if err != nil {
		return nil, nil, fmt.Errorf("resolve folder tree ids: %w", err)
	}

	scoped, includedRecords, err := scopeGraph(contentIDs, treeIDs, merged, selected)
	if err != nil {
		return nil, nil, err
	}
	if len(scoped) == 0 {
		return &CompileResult{RecordCount: report.RecordCount}, report, nil
	}

	ctx, err := ContextObject()
	if err != nil {
		return nil, nil, fmt.Errorf("load provenance context: %w", err)
	}

	records := make([]CompiledRecordRef, 0, len(includedRecords))
	for id := range includedRecords {
		meta, ok := merged.Records[id]
		if !ok {
			continue
		}
		records = append(records, CompiledRecordRef{
			ID:              id,
			Assertion:       meta.Assertion,
			AssertedBy:      meta.AssertedBy,
			GeneratedAtTime: meta.GeneratedAtTime,
		})
	}
	sort.Slice(records, func(i, j int) bool { return records[i].ID < records[j].ID })
	sort.Slice(scoped, func(i, j int) bool { return scoped[i].ID() < scoped[j].ID() })

	doc := &CompiledDocument{Context: ctx, Records: records, Graph: scoped}
	lineage := BuildArtifactLineage(doc, contentIDs, treeIDs)
	catalog := BuildProvenanceDocument(doc, reader.ref)
	return &CompileResult{
		Document:    doc,
		RecordCount: len(records),
		EntityCount: len(scoped),
		Lineage:     lineage,
		Catalog:     catalog,
	}, report, nil
}

// scopeGraph keeps merged's nodes that are the bundle's selected artifacts
// at the ref (including folder collections whose tree id matches, design
// D8.5), their transitive upstream, and the asserting agent — plus that
// agent's actedOnBehalfOf chain — of every record that contributes to a
// kept node (design D8 "Attribution": the asserting agent is not always
// reachable through an entity's own properties). It returns the kept
// nodes, each with exo:fromRecord set from MergedNode.FromRecords, and the
// set of record ids that contributed to them (design D15 "exo:records
// lists exactly the records that appear in some kept node's
// exo:fromRecord").
//
// exo:path is set on every kept node whose content id or folder tree id
// exists anywhere at the ref — not only among selected's files — to that
// content's path there (design D8.6). A kept node whose content is no
// longer present at the ref keeps its recorded exo:path, if any (design
// D8.5 "Upstream content outside the bundle").
//
// Every file's content id and every directory's tree id at the ref are
// resolved up front via Reader.FileEntries, Reader.AllContentIDs and
// Reader.AllTreeIDs, in a constant number of git invocations regardless of
// how many files the repository or selected has.
func scopeGraph(contentIDs, treeIDs map[string]string, merged *MergedGraph, selected []string) ([]Node, map[string]bool, error) {
	// pathByID covers every node id present anywhere at the ref, file or
	// folder, selected or not, so a node kept only as an upstream
	// reference still shows where it lives if it happens to still be in
	// the tree. Ties (two paths resolving to the same content id) keep
	// the lexicographically first path, for determinism.
	pathByID := make(map[string]string, len(contentIDs)+len(treeIDs))
	setPath := func(id, path string) {
		if _, known := merged.Nodes[id]; !known {
			return
		}
		if existing, ok := pathByID[id]; !ok || path < existing {
			pathByID[id] = path
		}
	}
	for path, id := range contentIDs {
		setPath(id, path)
	}
	for path, id := range treeIDs {
		setPath(id, path+"/")
	}

	inScope := map[string]bool{}
	for _, path := range selected {
		if id, ok := contentIDs[path]; ok {
			if _, known := merged.Nodes[id]; known {
				inScope[id] = true
			}
		}
		for _, dir := range pathAncestors(path) {
			id, ok := treeIDs[dir]
			if !ok {
				continue
			}
			if _, known := merged.Nodes[id]; known {
				inScope[id] = true
			}
		}
	}

	visited := map[string]bool{}
	var walk func(id string)
	walk = func(id string) {
		if visited[id] {
			return
		}
		visited[id] = true
		node, ok := merged.Nodes[id]
		if !ok {
			return
		}
		for _, prop := range upstreamProperties {
			raw, ok := node.Node[prop]
			if !ok {
				continue
			}
			for _, ref := range nodeRefs(raw) {
				walk(ref)
			}
		}
	}
	for id := range inScope {
		walk(id)
	}

	// A record's asserting agent is attribution, not upstream lineage: it
	// is reachable here, not by the walk above, by looking at every
	// record that contributed a kept node so far (design D8 "Attribution"
	// — "the primary use case"). Adding it to the walk also pulls in its
	// own actedOnBehalfOf chain, since that property is in
	// upstreamProperties.
	for id := range visited {
		node, ok := merged.Nodes[id]
		if !ok {
			continue
		}
		for _, recID := range node.FromRecords {
			meta, ok := merged.Records[recID]
			if !ok || meta.AssertedBy == "" {
				continue
			}
			if _, known := merged.Nodes[meta.AssertedBy]; known {
				walk(meta.AssertedBy)
			}
		}
	}

	includedRecords := map[string]bool{}
	result := make([]Node, 0, len(visited))
	for id := range visited {
		node, ok := merged.Nodes[id]
		if !ok {
			continue
		}
		for _, recID := range node.FromRecords {
			includedRecords[recID] = true
		}

		out := make(Node, len(node.Node)+1)
		for k, v := range node.Node {
			out[k] = v
		}
		if path, ok := pathByID[id]; ok {
			out["exo:path"] = path
		}
		fromRecords := append([]string(nil), node.FromRecords...)
		sort.Strings(fromRecords)
		out["exo:fromRecord"] = fromRecords

		result = append(result, out)
	}
	return result, includedRecords, nil
}
