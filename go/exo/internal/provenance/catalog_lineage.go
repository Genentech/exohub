package provenance

import (
	"encoding/json"
	"sort"
	"strings"
)

// LineageRef is an id with the path it resolves to at the bundle's ref,
// when known (design D16). Role is design D9's lineage.inputs[].role,
// flattened from a qualified usage.
type LineageRef struct {
	ID   string `json:"id"`
	Path string `json:"path,omitempty"`
	Role string `json:"role,omitempty"`
}

// LineageValue is one entry of lineage.generated_by.parameters or
// .results (design D9): a name and a value coerced through catalogValue so
// an array or object never reaches the catalog's keyword index.
type LineageValue struct {
	Name  string `json:"name"`
	Value any    `json:"value"`
}

// LineageEnvironment is one entry of lineage.generated_by.environment
// (design D9): the software environment entity's kind ("container" or
// "conda", from its label) and id.
type LineageEnvironment struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// LineagePlan is lineage.generated_by.plan (design D9): the plan entity's
// id and path, plus its optional checksum, repository, commit and url
// pointer fields.
type LineagePlan struct {
	ID         string `json:"id"`
	Path       string `json:"path,omitempty"`
	Checksum   string `json:"checksum,omitempty"`
	Repository string `json:"repository,omitempty"`
	Commit     string `json:"commit,omitempty"`
	URL        string `json:"url,omitempty"`
}

// catalogValue coerces a provenance record's typed value into the shape
// exo bundle writes to catalog documents (lineage.generated_by.parameters/
// results, provenance.json entities[].value): a scalar (string, number,
// boolean, nil) passes through typed, while an array or object is encoded
// as its canonical JSON string, since ES indexes "value" as keyword+text
// and rejects object values at index time (P1/!315 review). The record's
// own provenance.jsonld keeps the original typed value; this only applies
// to catalog documents.
func catalogValue(v any) any {
	switch v.(type) {
	case []any, map[string]any:
		b, err := json.Marshal(v)
		if err != nil {
			return v
		}
		return string(b)
	default:
		return v
	}
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

// LineageActivity is lineage.generated_by (design D16, D9). Source is the
// hadPrimarySource IRI of the run this activity was imported from.
// Parameters, Results and Environment are design D9's additive fields,
// flattened from qualified usages and generations.
type LineageActivity struct {
	ID          string               `json:"id"`
	Started     string               `json:"started,omitempty"`
	Ended       string               `json:"ended,omitempty"`
	Plan        *LineagePlan         `json:"plan,omitempty"`
	Agents      []LineageAgent       `json:"agents,omitempty"`
	Source      string               `json:"source,omitempty"`
	Parameters  []LineageValue       `json:"parameters,omitempty"`
	Results     []LineageValue       `json:"results,omitempty"`
	Environment []LineageEnvironment `json:"environment,omitempty"`
}

// LineageCollectionEntry is one entry of lineage.collections (design D8):
// a folder or named collection an artifact is a current member of. Only
// id and kind are always set; path is set for kind "folder", name for kind
// "named" — never both (enforced here, not in the schema, design D8
// amendment "no if/then/oneOf").
type LineageCollectionEntry struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Path string `json:"path,omitempty"`
	Name string `json:"name,omitempty"`
	// Project and Version identify the other ExoHub project and version
	// this collection comes from: defined there (external members, design
	// D5 of add-exohub-asset-import) or imported from there (`exo add
	// exohub:<project>@<version>`, design D4). For an import, both are set
	// when the collection's node carries hadPrimarySource, read from the
	// source node's label, "<project>@<version>", split on the last "@".
	// For an external collection reached through some local manifest's
	// "members", both are set once some record covers its digest. Never set
	// for a collection with no external source.
	Project string `json:"project,omitempty"`
	Version string `json:"version,omitempty"`
}

// ArtifactLineage is the "lineage" object exo bundle writes into an
// artifact document (design D16, spec "Artifact lineage in bundle
// documents", and design D8 "Lineage of collection membership").
type ArtifactLineage struct {
	Covered      bool                  `json:"covered"`
	GeneratedBy  *LineageActivity      `json:"generated_by,omitempty"`
	Inputs       []LineageRef          `json:"inputs,omitempty"`
	RevisionOf   []string              `json:"revision_of,omitempty"`
	AttributedTo []string              `json:"attributed_to,omitempty"`
	Records      []ProvenanceRecordRef `json:"records,omitempty"`
	// Collection is the legacy single-folder-collection reference
	// (deprecated, design D8 amendment, 2026-10-21): exo bundle no longer
	// sets it — kept only so a document decoded from an older bundle still
	// round-trips.
	Collection *LineageRef `json:"collection,omitempty"`
	// Collections is design D8's replacement: one entry per covered folder
	// or named collection the artifact is a current member of.
	Collections []LineageCollectionEntry `json:"collections,omitempty"`
}

// BuildArtifactLineage derives exo bundle's per-artifact lineage (design
// D16) from doc, the already compiled and scoped provenance.jsonld
// document, plus contentIDs and treeIDs (Reader.AllContentIDs,
// Reader.AllTreeIDs — the same batch-resolved maps CompileBundle already
// has, so building lineage costs no extra git invocation) and
// namedCollections, every named collection resolved once by
// resolveNamedCollections (compile.go).
//
// Every path whose content id, or an ancestor directory's tree id, is an
// Entity or Collection node of doc.Graph gets an entry. A path that is a
// current member of a covered folder or named collection also gets a
// lineage.collections entry per applicable collection (design D8 "Lineage
// of collection membership"), in addition to any lineage of its own. A
// member with no lineage of its own inherits generated_by, inputs,
// revision_of, attributed_to and records from the first applicable
// collection (nearest covered ancestor folder, else the first covered
// named collection by name — the same preference order as show's fallback,
// design D6). Paths without any provenance get no entry, matching "no
// lineage key" at the call site.
func BuildArtifactLineage(doc *CompiledDocument, contentIDs, treeIDs map[string]string, namedCollections []namedCollectionInfo) map[string]*ArtifactLineage {
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
	generatedByAct := generatedEntitiesByActivity(doc.Graph)

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

		var quals *activityQualifications
		if actID := singleRef(node["wasGeneratedBy"]); actID != "" {
			quals = classifyQualifiedUsage(nodeByID, actID)
			l.GeneratedBy = buildActivity(nodeByID, actID, quals, generatedByAct)
		}
		l.Inputs = buildInputs(nodeByID, node, l.GeneratedBy, quals)
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
	// A named collection has no path of its own (design D4), so it never
	// gets a result entry, but its lineage is still built here so the
	// membership pass below can inherit from it.
	for _, nc := range namedCollections {
		buildFor(nc.ID)
	}

	// Collection membership (design D7 "Overlap and nesting", D8 "Lineage
	// of collection membership"): every file or folder that is a current
	// member of a covered folder or named collection gets a
	// lineage.collections entry, regardless of whether it also has
	// provenance of its own.
	addMembership := func(path, id string, ancestors []string) {
		entries := collectionEntriesFor(id, ancestors, lineageByID, treeIDs, namedCollections, nodeByID)
		if len(entries) == 0 {
			return
		}
		if existing, ok := result[path]; ok {
			existing.Collections = entries
			return
		}
		source := lineageByID[entries[0].ID]
		result[path] = &ArtifactLineage{
			Covered:      true,
			GeneratedBy:  source.GeneratedBy,
			Inputs:       source.Inputs,
			RevisionOf:   source.RevisionOf,
			AttributedTo: source.AttributedTo,
			Records:      source.Records,
			Collections:  entries,
		}
	}
	for path, id := range contentIDs {
		addMembership(path, id, pathAncestors(path))
	}
	for dir, id := range treeIDs {
		addMembership(dir+"/", id, pathAncestors(dir))
	}

	return result
}

// collectionEntriesFor returns id's lineage.collections entries (design
// D8): one per covered ancestor folder collection (nearest first) and
// covered named collection (namedCollections' order, i.e. alphabetical by
// name) that currently counts id, or an ancestor directory's folder tree
// id, as a member. A collection counts only when it has a lineage object
// of its own (isLineageSubject) that is itself covered (design D6: "the
// collection's current id matches the record's subject").
func collectionEntriesFor(id string, ancestors []string, lineageByID map[string]*ArtifactLineage, treeIDs map[string]string, namedCollections []namedCollectionInfo, nodeByID map[string]Node) []LineageCollectionEntry {
	var entries []LineageCollectionEntry
	for _, dir := range ancestors {
		tid, ok := treeIDs[dir]
		if !ok {
			continue
		}
		folder := lineageByID[tid]
		if folder == nil || !folder.Covered {
			continue
		}
		entry := LineageCollectionEntry{ID: tid, Kind: "folder", Path: dir + "/"}
		entry.Project, entry.Version = importSourceProjectVersion(nodeByID, tid)
		entries = append(entries, entry)
	}
	for _, nc := range namedCollections {
		member := nc.CoveredIDs[id]
		if !member {
			for _, dir := range ancestors {
				if tid, ok := treeIDs[dir]; ok && nc.CoveredIDs[tid] {
					member = true
					break
				}
			}
		}
		if !member {
			continue
		}
		named := lineageByID[nc.ID]
		if named == nil || !named.Covered {
			continue
		}
		// nc.Project/nc.Version are already set for an external collection
		// (design D5, reached through some local manifest's "members"); for
		// an ordinary same-project named collection, fall back to the import
		// path (design D4: hadPrimarySource on the collection's own node).
		entry := LineageCollectionEntry{ID: nc.ID, Kind: "named", Name: nc.Name, Project: nc.Project, Version: nc.Version}
		if entry.Project == "" {
			entry.Project, entry.Version = importSourceProjectVersion(nodeByID, nc.ID)
		}
		entries = append(entries, entry)
	}
	return entries
}

// importSourceProjectVersion returns the project and version a collection
// (folder or named, identified by id) was imported from, when id's node
// carries hadPrimarySource (design D4 of add-exohub-asset-import): both
// come from the source node's label, "<project>@<version>" (set by `exo
// add exohub:...`'s automatic import record), split on the last "@". It
// returns "", "" when id has no node, no hadPrimarySource, or the source's
// label isn't in that form.
func importSourceProjectVersion(nodeByID map[string]Node, id string) (project, version string) {
	node, ok := nodeByID[id]
	if !ok {
		return "", ""
	}
	srcID := singleRef(node["hadPrimarySource"])
	if srcID == "" {
		return "", ""
	}
	src, ok := nodeByID[srcID]
	if !ok {
		return "", ""
	}
	label, _ := src["label"].(string)
	at := strings.LastIndex(label, "@")
	if at <= 0 || at == len(label)-1 {
		return "", ""
	}
	return label[:at], label[at+1:]
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

// activityQualifications is the result of classifying an activity's
// qualifiedUsage nodes (design D9): roles for plain input entities,
// parameters and the environment, plus the set of entity ids that are
// parameters or the environment and so must not also appear in
// lineage.inputs (the spec's "Roles, parameters and environment" scenario:
// a parameter or environment entity is reported only through
// generated_by, never as a plain input, even though it shares the
// activity's "used" shortcut).
type activityQualifications struct {
	roles       map[string]string
	parameters  []LineageValue
	environment []LineageEnvironment
	exclude     map[string]bool
}

// classifyQualifiedUsage walks actID's qualifiedUsage Usage nodes (design
// D2, D3, D4) and buckets each referenced entity into a role (a plain
// input), a parameter (the entity has a "value") or the environment
// (hadRole "environment").
func classifyQualifiedUsage(nodeByID map[string]Node, actID string) *activityQualifications {
	q := &activityQualifications{roles: map[string]string{}, exclude: map[string]bool{}}
	node, ok := nodeByID[actID]
	if !ok {
		return q
	}
	usageIDs := nodeRefs(node["qualifiedUsage"])
	sort.Strings(usageIDs)
	for _, usageID := range usageIDs {
		usage, ok := nodeByID[usageID]
		if !ok {
			continue
		}
		role, _ := usage["hadRole"].(string)
		entityID := singleRef(usage["entity"])
		if entityID == "" {
			continue
		}
		entity := nodeByID[entityID]
		switch {
		case role == "environment":
			q.environment = append(q.environment, LineageEnvironment{Kind: environmentKind(entityID), ID: entityID})
			q.exclude[entityID] = true
		case hasValueKey(entity):
			q.parameters = append(q.parameters, LineageValue{Name: role, Value: catalogValue(entity["value"])})
			q.exclude[entityID] = true
		case role != "":
			q.roles[entityID] = role
		}
	}
	return q
}

// environmentKind derives a software environment entity's kind (design D4
// "Kind is derived from the IRI scheme") from its id: a "docker://" IRI is
// a container, anything else used with hadRole "environment" (an annex
// key, git blob or the engine's env hash) is a conda environment.
func environmentKind(id string) string {
	if strings.HasPrefix(id, "docker://") {
		return "container"
	}
	return "conda"
}

// hasValueKey reports whether node carries a "value" property (design D3's
// value entity marker).
func hasValueKey(node Node) bool {
	_, ok := node["value"]
	return ok
}

// resultRoleFor returns the hadRole of entity's qualifiedGeneration node
// whose activity is actID (design D3 "Parameters and results as value
// entities"), or "" when entity has no such generation.
func resultRoleFor(nodeByID map[string]Node, entity Node, actID string) string {
	for _, genID := range nodeRefs(entity["qualifiedGeneration"]) {
		gen, ok := nodeByID[genID]
		if !ok || singleRef(gen["activity"]) != actID {
			continue
		}
		if role, _ := gen["hadRole"].(string); role != "" {
			return role
		}
	}
	return ""
}

// generatedEntitiesByActivity returns, for every activity id in graph, the
// sorted ids of the entities it generated (via each entity's
// wasGeneratedBy), reused by buildActivity's results computation and
// BuildProvenanceDocument's activities[].generated.
func generatedEntitiesByActivity(graph []Node) map[string][]string {
	generatedBy := map[string][]string{}
	for _, n := range graph {
		if actID := singleRef(n["wasGeneratedBy"]); actID != "" {
			generatedBy[actID] = append(generatedBy[actID], n.ID())
		}
	}
	for actID := range generatedBy {
		sort.Strings(generatedBy[actID])
	}
	return generatedBy
}

// buildPlan builds lineage.generated_by.plan (design D9): the plan
// entity's id and path, plus its exo:checksum, exo:repository, exo:commit
// and, when the plan is only referenced by an addressable URL (design D5
// option 4 — not a content id, not a repository+commit pair), its own id
// as url.
func buildPlan(nodeByID map[string]Node, planID string) *LineagePlan {
	plan := &LineagePlan{ID: planID, Path: pathOf(nodeByID, planID)}
	node := nodeByID[planID]
	if c, ok := node["exo:checksum"].(string); ok {
		plan.Checksum = c
	}
	repo, hasRepo := node["exo:repository"].(string)
	if hasRepo {
		plan.Repository = repo
	}
	commit, hasCommit := node["exo:commit"].(string)
	if hasCommit {
		plan.Commit = commit
	}
	if !(hasRepo && hasCommit) && !strings.HasPrefix(planID, "urn:exohub:") && absoluteIRIPattern.MatchString(planID) {
		plan.URL = planID
	}
	return plan
}

// buildActivity builds lineage.generated_by for the activity node actID
// (design D16, D9): id, started, ended, plan, every wasAssociatedWith
// agent, source, parameters, results and environment.
func buildActivity(nodeByID map[string]Node, actID string, quals *activityQualifications, generatedByAct map[string][]string) *LineageActivity {
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
		act.Plan = buildPlan(nodeByID, planID)
	}
	act.Source = singleRef(node["hadPrimarySource"])
	agentIDs := nodeRefs(node["wasAssociatedWith"])
	sort.Strings(agentIDs)
	for _, agentID := range agentIDs {
		act.Agents = append(act.Agents, buildAgent(nodeByID, agentID))
	}
	if quals != nil {
		act.Parameters = append([]LineageValue(nil), quals.parameters...)
		act.Environment = append([]LineageEnvironment(nil), quals.environment...)
		sort.Slice(act.Parameters, func(i, j int) bool { return act.Parameters[i].Name < act.Parameters[j].Name })
		sort.Slice(act.Environment, func(i, j int) bool { return act.Environment[i].ID < act.Environment[j].ID })
	}
	for _, entID := range generatedByAct[actID] {
		entity := nodeByID[entID]
		if !hasValueKey(entity) {
			continue
		}
		role := resultRoleFor(nodeByID, entity, actID)
		if role == "" {
			continue
		}
		act.Results = append(act.Results, LineageValue{Name: role, Value: catalogValue(entity["value"])})
	}
	sort.Slice(act.Results, func(i, j int) bool { return act.Results[i].Name < act.Results[j].Name })
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

// buildInputs builds lineage.inputs (design D16, D9): the generating
// activity's direct "used" entities, plus node's own "wasDerivedFrom"
// entities, with each entity's role attached when quals has one. Entities
// quals classified as a parameter or the environment are excluded (they
// are reported through generated_by instead, spec "Roles, parameters and
// environment in the artifact document").
func buildInputs(nodeByID map[string]Node, node Node, act *LineageActivity, quals *activityQualifications) []LineageRef {
	seen := map[string]bool{}
	var ids []string
	add := func(raw any) {
		for _, id := range nodeRefs(raw) {
			if quals != nil && quals.exclude[id] {
				continue
			}
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
		ref := LineageRef{ID: id, Path: pathOf(nodeByID, id)}
		if quals != nil {
			ref.Role = quals.roles[id]
		}
		refs = append(refs, ref)
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

// ProvenanceQualifiedRef is one entry of provenance.json's
// activities[].inputs or .outputs (design D9): a qualified usage or
// generation's entity id and role, flattened. Unlike LineageRef it has no
// path, matching the schema's additionalProperties: false for these
// items.
type ProvenanceQualifiedRef struct {
	ID   string `json:"id"`
	Role string `json:"role,omitempty"`
}

// ProvenanceActivity is one entry of provenance.json's "activities"
// (design D16, D9): id, plan, started, ended, associated agents, used and
// generated entity ids, plus qualified inputs/outputs (entity + role,
// unfiltered: parameters and the environment are included here, unlike
// lineage.inputs). Records is the activity's contributing, non-superseded
// record ids (design D16 scope addition).
type ProvenanceActivity struct {
	ID        string                   `json:"id"`
	Plan      string                   `json:"plan,omitempty"`
	Started   string                   `json:"started,omitempty"`
	Ended     string                   `json:"ended,omitempty"`
	Agents    []string                 `json:"agents,omitempty"`
	Used      []string                 `json:"used,omitempty"`
	Generated []string                 `json:"generated,omitempty"`
	Inputs    []ProvenanceQualifiedRef `json:"inputs,omitempty"`
	Outputs   []ProvenanceQualifiedRef `json:"outputs,omitempty"`
	Records   []string                 `json:"records,omitempty"`
}

// ProvenanceEntity is one entry of provenance.json's "entities" (design
// D16, D9): id, type, path at the ref, coverage, value and revision.
// Records is the entity's contributing, non-superseded record ids (design
// D16 scope addition).
type ProvenanceEntity struct {
	ID      string `json:"id"`
	Type    string `json:"type,omitempty"`
	Path    string `json:"path,omitempty"`
	Covered bool   `json:"covered"`
	// Value is the entity's original JSON-typed value for a value entity
	// (parameter or result), coerced through catalogValue. Set only when
	// the merged node has a "value" property.
	Value      any      `json:"value,omitempty"`
	RevisionOf []string `json:"revision_of,omitempty"`
	Records    []string `json:"records,omitempty"`
	// MemberCount is design D8's "Collection entity member counts": set
	// only for a Collection-typed entity (folder or named), never a member
	// list.
	MemberCount *int `json:"member_count,omitempty"`
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

// collectionMemberCount returns node's optional member_count (design D8
// "Collection entity member counts"): nil for anything that isn't a
// Collection. A named collection's count is its hadMember list length (the
// resolved members exo provenance add wrote, design D5) — that recorded
// set applies to the node regardless of ref, so it is always counted. A
// folder collection has no hadMember; its count is the number of currently
// tracked files at or under its own exo:path, but that count only applies
// to the current tree at that path. An older revision of the same folder
// (or a folder that no longer matches the ref) is a different node id from
// treeIDs[dir] and must not be given the current folder's count, so
// folder collections are counted only when node's id is the ref's current
// tree id for exo:path.
func collectionMemberCount(node Node, contentIDs, treeIDs map[string]string) *int {
	isCollection := false
	for _, t := range node.Types() {
		if t == "Collection" {
			isCollection = true
			break
		}
	}
	if !isCollection {
		return nil
	}
	if hadMember, ok := node["hadMember"]; ok {
		n := len(nodeRefs(hadMember))
		return &n
	}
	dir, _ := node["exo:path"].(string)
	dir = strings.TrimSuffix(dir, "/")
	if dir == "" {
		return nil
	}
	if treeIDs[dir] != node.ID() {
		return nil
	}
	prefix := dir + "/"
	n := 0
	for path := range contentIDs {
		if path == dir || strings.HasPrefix(path, prefix) {
			n++
		}
	}
	return &n
}

// qualifiedUsageRefs returns actID's qualifiedUsage entities and roles
// (design D9 "Usage/generation roles are flattened onto
// activities[].inputs[]"), sorted by entity id. Unlike lineage.inputs,
// every qualified entity is included: parameters and the environment too.
func qualifiedUsageRefs(nodeByID map[string]Node, actID string) []ProvenanceQualifiedRef {
	node, ok := nodeByID[actID]
	if !ok {
		return nil
	}
	var refs []ProvenanceQualifiedRef
	for _, usageID := range nodeRefs(node["qualifiedUsage"]) {
		usage, ok := nodeByID[usageID]
		if !ok {
			continue
		}
		entityID := singleRef(usage["entity"])
		if entityID == "" {
			continue
		}
		role, _ := usage["hadRole"].(string)
		refs = append(refs, ProvenanceQualifiedRef{ID: entityID, Role: role})
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].ID < refs[j].ID })
	return refs
}

// qualifiedGenerationRefs returns the role of each of generatedIDs that has
// a qualifiedGeneration naming actID (design D9 "...and outputs[]"),
// sorted by entity id.
func qualifiedGenerationRefs(nodeByID map[string]Node, generatedIDs []string, actID string) []ProvenanceQualifiedRef {
	var refs []ProvenanceQualifiedRef
	for _, id := range generatedIDs {
		role := resultRoleFor(nodeByID, nodeByID[id], actID)
		if role == "" {
			continue
		}
		refs = append(refs, ProvenanceQualifiedRef{ID: id, Role: role})
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].ID < refs[j].ID })
	return refs
}

// BuildProvenanceDocument flattens doc, the already compiled and scoped
// provenance.jsonld document, into provenance.json's shape (design D16).
// ref is the bundle's target ref (commit hash); callers set RefName
// afterward since CompileBundle's Reader does not resolve a human-readable
// ref name.
func BuildProvenanceDocument(doc *CompiledDocument, ref string, contentIDs, treeIDs map[string]string) *ProvenanceDocument {
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
		a.Inputs = qualifiedUsageRefs(nodeByID, id)
		a.Outputs = qualifiedGenerationRefs(nodeByID, generatedBy[id], id)
		a.Records = recordIDsFor(node)
		activityRefs = append(activityRefs, a)
	}

	entityRefs := make([]ProvenanceEntity, 0, len(entities))
	for _, id := range entities {
		node := nodeByID[id]
		e := ProvenanceEntity{
			ID:          id,
			Type:        primaryType(node.Types()),
			Path:        pathOf(nodeByID, id),
			Covered:     nodeCovered(node),
			Value:       catalogValue(node["value"]),
			RevisionOf:  sortedRefs(node["wasRevisionOf"]),
			Records:     recordIDsFor(node),
			MemberCount: collectionMemberCount(node, contentIDs, treeIDs),
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
