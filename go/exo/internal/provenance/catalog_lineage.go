package provenance

import "sort"

// LineageRef is an id with the path it resolves to at the bundle's ref,
// when known (design D16).
type LineageRef struct {
	ID   string `json:"id"`
	Path string `json:"path,omitempty"`
}

// LineageAgent is one entry of lineage.generated_by.agents (design D16):
// every agent an activity's wasAssociatedWith names, with its type, label,
// declared model and actedOnBehalfOf chain.
type LineageAgent struct {
	ID              string   `json:"id"`
	Type            string   `json:"type,omitempty"`
	Label           string   `json:"label,omitempty"`
	Model           string   `json:"model,omitempty"`
	ActedOnBehalfOf []string `json:"acted_on_behalf_of,omitempty"`
}

// LineageActivity is lineage.generated_by (design D16).
type LineageActivity struct {
	ID      string         `json:"id"`
	Started string         `json:"started,omitempty"`
	Ended   string         `json:"ended,omitempty"`
	Plan    *LineageRef    `json:"plan,omitempty"`
	Agents  []LineageAgent `json:"agents,omitempty"`
}

// ArtifactLineage is the "lineage" object exo bundle writes into an
// artifact document (design D16, spec "Artifact lineage in bundle
// documents" and "Lineage of folder collection members").
type ArtifactLineage struct {
	Covered      bool                  `json:"covered"`
	GeneratedBy  *LineageActivity      `json:"generated_by,omitempty"`
	Inputs       []LineageRef          `json:"inputs,omitempty"`
	RevisionOf   []string              `json:"revision_of,omitempty"`
	AttributedTo []string              `json:"attributed_to,omitempty"`
	Records      []ProvenanceRecordRef `json:"records,omitempty"`
	Collection   *LineageRef           `json:"collection,omitempty"`
}

// BuildArtifactLineage derives exo bundle's per-artifact lineage (design
// D16) from doc, the already compiled and scoped provenance.jsonld
// document, plus contentIDs and treeIDs (Reader.AllContentIDs,
// Reader.AllTreeIDs — the same batch-resolved maps CompileBundle already
// has, so building lineage costs no extra git invocation).
//
// Every path whose content id, or an ancestor directory's tree id, is an
// Entity or Collection node of doc.Graph gets an entry. A path under a
// covered folder collection that has no node of its own (design D13: a
// TileDB fragment, for example) gets the folder's generated_by, inputs,
// revision_of and records, plus a collection reference (design D16
// "Lineage of folder collection members"). Paths without any provenance
// get no entry, matching "no lineage key" at the call site.
func BuildArtifactLineage(doc *CompiledDocument, contentIDs, treeIDs map[string]string) map[string]*ArtifactLineage {
	if doc == nil {
		return nil
	}

	nodeByID := make(map[string]Node, len(doc.Graph))
	for _, n := range doc.Graph {
		nodeByID[n.ID()] = n
	}
	recordByID := make(map[string]ProvenanceRecordRef, len(doc.Records))
	for _, r := range doc.Records {
		recordByID[r.ID] = ProvenanceRecordRef{
			ID:              r.ID,
			Assertion:       r.Assertion,
			AssertedBy:      r.AssertedBy,
			GeneratedAtTime: r.GeneratedAtTime,
		}
	}

	lineageByID := map[string]*ArtifactLineage{}
	var buildFor func(id string) *ArtifactLineage
	buildFor = func(id string) *ArtifactLineage {
		if l, ok := lineageByID[id]; ok {
			return l
		}
		node, ok := nodeByID[id]
		if !ok || !isLineageSubject(node) {
			lineageByID[id] = nil
			return nil
		}
		l := &ArtifactLineage{Covered: nodeCovered(node)}
		lineageByID[id] = l // guard before recursing into related nodes

		if actID := singleRef(node["wasGeneratedBy"]); actID != "" {
			l.GeneratedBy = buildActivity(nodeByID, actID)
		}
		l.Inputs = buildInputs(nodeByID, node, l.GeneratedBy)
		l.RevisionOf = sortedRefs(node["wasRevisionOf"])
		l.AttributedTo = sortedRefs(node["wasAttributedTo"])
		l.Records = recordsFor(node, recordByID)
		return l
	}

	result := map[string]*ArtifactLineage{}
	for path, id := range contentIDs {
		if l := buildFor(id); l != nil {
			result[path] = l
		}
	}
	for dir, id := range treeIDs {
		if l := buildFor(id); l != nil {
			result[dir+"/"] = l
		}
	}

	// Folder collection members (design D13, D16): a path whose own
	// content id has no lineage of its own inherits its nearest covered
	// ancestor folder's lineage, matched against the folder's current tree
	// id — never an older, revised tree at the same path.
	for path := range contentIDs {
		if _, ok := result[path]; ok {
			continue
		}
		for _, dir := range pathAncestors(path) {
			tid, ok := treeIDs[dir]
			if !ok {
				continue
			}
			folder := lineageByID[tid]
			if folder == nil || !folder.Covered {
				continue
			}
			result[path] = &ArtifactLineage{
				Covered:      true,
				GeneratedBy:  folder.GeneratedBy,
				Inputs:       folder.Inputs,
				RevisionOf:   folder.RevisionOf,
				AttributedTo: folder.AttributedTo,
				Records:      folder.Records,
				Collection:   &LineageRef{ID: tid, Path: dir + "/"},
			}
			break
		}
	}

	return result
}

// isLineageSubject reports whether node is the kind of node exo bundle
// computes a lineage object for: an Entity or Collection, not an Activity
// or an Agent.
func isLineageSubject(node Node) bool {
	for _, t := range node.Types() {
		if t == "Entity" || t == "Collection" {
			return true
		}
	}
	return false
}

// nodeCovered reports whether node is covered by its own provenance
// (design D14, coveringProperties).
func nodeCovered(node Node) bool {
	for _, prop := range coveringProperties {
		if _, ok := node[prop]; ok {
			return true
		}
	}
	return false
}

// buildActivity builds lineage.generated_by for the activity node actID
// (design D16): id, started, ended, plan and every wasAssociatedWith
// agent.
func buildActivity(nodeByID map[string]Node, actID string) *LineageActivity {
	act := &LineageActivity{ID: actID}
	node, ok := nodeByID[actID]
	if !ok {
		return act
	}
	if s, ok := node["startedAtTime"].(string); ok {
		act.Started = s
	}
	if e, ok := node["endedAtTime"].(string); ok {
		act.Ended = e
	}
	if planID := singleRef(node["hadPlan"]); planID != "" {
		act.Plan = &LineageRef{ID: planID, Path: pathOf(nodeByID, planID)}
	}
	agentIDs := nodeRefs(node["wasAssociatedWith"])
	sort.Strings(agentIDs)
	for _, agentID := range agentIDs {
		act.Agents = append(act.Agents, buildAgent(nodeByID, agentID))
	}
	return act
}

// buildAgent builds one lineage.generated_by.agents entry (design D16).
func buildAgent(nodeByID map[string]Node, id string) LineageAgent {
	agent := LineageAgent{ID: id}
	node, ok := nodeByID[id]
	if !ok {
		return agent
	}
	agent.Type = primaryType(node.Types())
	if l, ok := node["label"].(string); ok {
		agent.Label = l
	}
	if m, ok := node["exo:model"].(string); ok {
		agent.Model = m
	}
	agent.ActedOnBehalfOf = sortedRefs(node["actedOnBehalfOf"])
	return agent
}

// primaryType picks the most specific PROV type for display, preferring a
// concrete agent or entity type over the generic "Agent".
func primaryType(types []string) string {
	for _, t := range types {
		if t != "Agent" {
			return t
		}
	}
	if len(types) > 0 {
		return types[0]
	}
	return ""
}

// buildInputs builds lineage.inputs (design D16): the generating activity's
// direct "used" entities, plus node's own "wasDerivedFrom" entities.
func buildInputs(nodeByID map[string]Node, node Node, act *LineageActivity) []LineageRef {
	seen := map[string]bool{}
	var ids []string
	add := func(raw any) {
		for _, id := range nodeRefs(raw) {
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	if act != nil {
		if actNode, ok := nodeByID[act.ID]; ok {
			add(actNode["used"])
		}
	}
	add(node["wasDerivedFrom"])
	sort.Strings(ids)

	refs := make([]LineageRef, 0, len(ids))
	for _, id := range ids {
		refs = append(refs, LineageRef{ID: id, Path: pathOf(nodeByID, id)})
	}
	return refs
}

// recordsFor returns node's contributing, non-superseded records (design
// D16), from node's exo:fromRecord (set by scopeGraph/CompileBundle).
func recordsFor(node Node, recordByID map[string]ProvenanceRecordRef) []ProvenanceRecordRef {
	fromRecords, _ := node["exo:fromRecord"].([]string)
	if len(fromRecords) == 0 {
		return nil
	}
	out := make([]ProvenanceRecordRef, 0, len(fromRecords))
	for _, id := range fromRecords {
		if r, ok := recordByID[id]; ok {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// recordIDsFor returns the sorted, deduplicated ids of node's contributing,
// non-superseded records (design D16 scope addition: provenance.json's
// "foreign key" back to records[]), from node's exo:fromRecord (set by
// scopeGraph/CompileBundle).
func recordIDsFor(node Node) []string {
	fromRecords, _ := node["exo:fromRecord"].([]string)
	if len(fromRecords) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(fromRecords))
	for _, id := range fromRecords {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// pathOf returns id's exo:path on doc.Graph (set by scopeGraph: the path
// at the ref, or the last recorded path when the content is absent at the
// ref — design D8.5, reused here so lineage path annotations follow the
// same rule as provenance.jsonld).
func pathOf(nodeByID map[string]Node, id string) string {
	node, ok := nodeByID[id]
	if !ok {
		return ""
	}
	p, _ := node["exo:path"].(string)
	return p
}

// singleRef returns the first id a property value references, or "".
func singleRef(v any) string {
	ids := nodeRefs(v)
	if len(ids) == 0 {
		return ""
	}
	return ids[0]
}

// sortedRefs returns the sorted, deduplicated ids a property value
// references, or nil.
func sortedRefs(v any) []string {
	ids := nodeRefs(v)
	if len(ids) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// ProvenanceRecordRef is one entry of provenance.json's "records" (design
// D16), mirroring CompiledRecordRef with the catalog document's snake_case
// field names.
type ProvenanceRecordRef struct {
	ID              string    `json:"id"`
	Assertion       Assertion `json:"assertion"`
	AssertedBy      string    `json:"asserted_by"`
	GeneratedAtTime string    `json:"generated_at_time"`
}

// ProvenanceAgentRef is one entry of provenance.json's "agents" (design
// D16). Records is the agent's contributing, non-superseded record ids
// (design D16 scope addition: the "foreign key" back to records[]).
type ProvenanceAgentRef struct {
	ID              string   `json:"id"`
	Type            string   `json:"type,omitempty"`
	Label           string   `json:"label,omitempty"`
	Model           string   `json:"model,omitempty"`
	ActedOnBehalfOf []string `json:"acted_on_behalf_of,omitempty"`
	Records         []string `json:"records,omitempty"`
}

// ProvenanceActivity is one entry of provenance.json's "activities"
// (design D16): id, plan, started, ended, associated agents, used and
// generated entity ids. Records is the activity's contributing,
// non-superseded record ids (design D16 scope addition).
type ProvenanceActivity struct {
	ID        string   `json:"id"`
	Plan      string   `json:"plan,omitempty"`
	Started   string   `json:"started,omitempty"`
	Ended     string   `json:"ended,omitempty"`
	Agents    []string `json:"agents,omitempty"`
	Used      []string `json:"used,omitempty"`
	Generated []string `json:"generated,omitempty"`
	Records   []string `json:"records,omitempty"`
}

// ProvenanceEntity is one entry of provenance.json's "entities" (design
// D16): id, type, path at the ref, coverage and revision. Records is the
// entity's contributing, non-superseded record ids (design D16 scope
// addition).
type ProvenanceEntity struct {
	ID         string   `json:"id"`
	Type       string   `json:"type,omitempty"`
	Path       string   `json:"path,omitempty"`
	Covered    bool     `json:"covered"`
	RevisionOf []string `json:"revision_of,omitempty"`
	Records    []string `json:"records,omitempty"`
}

// ProvenanceDocument is exo bundle's provenance.json (design D16,
// $schema: exohub-provenance/v1.json): the whole compiled graph flattened
// for generic catalog ingestion, next to the unchanged provenance.jsonld.
type ProvenanceDocument struct {
	Schema      string                `json:"$schema"`
	Ref         string                `json:"ref"`
	RefName     string                `json:"ref_name,omitempty"`
	RecordCount int                   `json:"record_count"`
	EntityCount int                   `json:"entity_count"`
	Records     []ProvenanceRecordRef `json:"records"`
	Agents      []ProvenanceAgentRef  `json:"agents"`
	Activities  []ProvenanceActivity  `json:"activities"`
	Entities    []ProvenanceEntity    `json:"entities"`
	GraphFile   string                `json:"graph_file"`
}

// ProvenanceSchemaID is provenance.json's $schema value (design D16, task
// 8.1).
const ProvenanceSchemaID = "exohub-provenance/v1.json"

// BuildProvenanceDocument flattens doc, the already compiled and scoped
// provenance.jsonld document, into provenance.json's shape (design D16).
// ref is the bundle's target ref (commit hash); callers set RefName
// afterward since CompileBundle's Reader does not resolve a human-readable
// ref name.
func BuildProvenanceDocument(doc *CompiledDocument, ref string) *ProvenanceDocument {
	if doc == nil {
		return nil
	}

	nodeByID := make(map[string]Node, len(doc.Graph))
	for _, n := range doc.Graph {
		nodeByID[n.ID()] = n
	}

	records := make([]ProvenanceRecordRef, 0, len(doc.Records))
	for _, r := range doc.Records {
		records = append(records, ProvenanceRecordRef{
			ID:              r.ID,
			Assertion:       r.Assertion,
			AssertedBy:      r.AssertedBy,
			GeneratedAtTime: r.GeneratedAtTime,
		})
	}
	sort.Slice(records, func(i, j int) bool { return records[i].ID < records[j].ID })

	generatedBy := map[string][]string{} // activity id -> generated entity ids
	var agents, activities, entities []string
	for _, n := range doc.Graph {
		isAgent := false
		isActivity := false
		for _, t := range n.Types() {
			switch t {
			case "Agent", "Person", "SoftwareAgent", "Organization":
				isAgent = true
			case "Activity":
				isActivity = true
			}
		}
		switch {
		case isAgent:
			agents = append(agents, n.ID())
		case isActivity:
			activities = append(activities, n.ID())
		default:
			entities = append(entities, n.ID())
			if actID := singleRef(n["wasGeneratedBy"]); actID != "" {
				generatedBy[actID] = append(generatedBy[actID], n.ID())
			}
		}
	}
	sort.Strings(agents)
	sort.Strings(activities)
	sort.Strings(entities)

	agentRefs := make([]ProvenanceAgentRef, 0, len(agents))
	for _, id := range agents {
		agent := buildAgent(nodeByID, id)
		agentRefs = append(agentRefs, ProvenanceAgentRef{
			ID:              agent.ID,
			Type:            agent.Type,
			Label:           agent.Label,
			Model:           agent.Model,
			ActedOnBehalfOf: agent.ActedOnBehalfOf,
			Records:         recordIDsFor(nodeByID[id]),
		})
	}

	activityRefs := make([]ProvenanceActivity, 0, len(activities))
	for _, id := range activities {
		node := nodeByID[id]
		a := ProvenanceActivity{ID: id}
		if s, ok := node["startedAtTime"].(string); ok {
			a.Started = s
		}
		if e, ok := node["endedAtTime"].(string); ok {
			a.Ended = e
		}
		a.Plan = singleRef(node["hadPlan"])
		a.Agents = sortedRefs(node["wasAssociatedWith"])
		a.Used = sortedRefs(node["used"])
		generated := append([]string(nil), generatedBy[id]...)
		sort.Strings(generated)
		a.Generated = generated
		a.Records = recordIDsFor(node)
		activityRefs = append(activityRefs, a)
	}

	entityRefs := make([]ProvenanceEntity, 0, len(entities))
	for _, id := range entities {
		node := nodeByID[id]
		e := ProvenanceEntity{
			ID:         id,
			Type:       primaryType(node.Types()),
			Path:       pathOf(nodeByID, id),
			Covered:    nodeCovered(node),
			RevisionOf: sortedRefs(node["wasRevisionOf"]),
			Records:    recordIDsFor(node),
		}
		entityRefs = append(entityRefs, e)
	}

	return &ProvenanceDocument{
		Schema:      ProvenanceSchemaID,
		Ref:         ref,
		RecordCount: len(records),
		EntityCount: len(doc.Graph),
		Records:     records,
		Agents:      agentRefs,
		Activities:  activityRefs,
		Entities:    entityRefs,
		GraphFile:   "provenance.jsonld",
	}
}
