package provenance

import "sort"

// RoleOfUsage returns the hadRole activityID's qualifiedUsage states for
// entityID (spec "Roles of inputs and outputs"), and whether one does.
func RoleOfUsage(graph *MergedGraph, activityID, entityID string) (string, bool) {
	act := graph.Nodes[activityID]
	if act == nil {
		return "", false
	}
	for _, usageID := range nodeRefs(act.Node["qualifiedUsage"]) {
		u := graph.Nodes[usageID]
		if u == nil || !hasType(u.Node, "Usage") {
			continue
		}
		if containsString(nodeRefs(u.Node["entity"]), entityID) {
			if role, ok := u.Node["hadRole"].(string); ok && role != "" {
				return role, true
			}
		}
	}
	return "", false
}

// RoleOfGeneration returns the hadRole entityID's qualifiedGeneration
// states for generation by activityID (spec "Roles of inputs and
// outputs"), and whether one does.
func RoleOfGeneration(graph *MergedGraph, entityID, activityID string) (string, bool) {
	ent := graph.Nodes[entityID]
	if ent == nil {
		return "", false
	}
	for _, genID := range nodeRefs(ent.Node["qualifiedGeneration"]) {
		g := graph.Nodes[genID]
		if g == nil || !hasType(g.Node, "Generation") {
			continue
		}
		if containsString(nodeRefs(g.Node["activity"]), activityID) {
			if role, ok := g.Node["hadRole"].(string); ok && role != "" {
				return role, true
			}
		}
	}
	return "", false
}

// ActivityResult is one non-file result (spec "Non-file results") an
// activity generated.
type ActivityResult struct {
	EntityID string
	Role     string
	Value    any
}

// ActivityResults returns every non-file result activityID generated (spec
// "Non-file results"): a value entity (one with a "value" property) whose
// wasGeneratedBy references activityID and whose qualifiedGeneration
// states the role for that activity, sorted by role for a deterministic
// order.
func ActivityResults(graph *MergedGraph, activityID string) []ActivityResult {
	var out []ActivityResult
	for id, n := range graph.Nodes {
		value, hasValue := n.Node["value"]
		if !hasValue || !containsString(nodeRefs(n.Node["wasGeneratedBy"]), activityID) {
			continue
		}
		role, ok := RoleOfGeneration(graph, id, activityID)
		if !ok {
			continue
		}
		out = append(out, ActivityResult{EntityID: id, Role: role, Value: value})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Role < out[j].Role })
	return out
}
