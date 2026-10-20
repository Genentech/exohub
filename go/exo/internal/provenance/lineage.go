package provenance

import (
	"fmt"
	"sort"
	"strings"
)

// LineageSource describes how exo provenance show's subject relates to the
// path the user asked about (design D9, D13 "Lineage fallbacks").
type LineageSource string

const (
	// LineageSourceOwn means the path's own content id is the subject.
	LineageSourceOwn LineageSource = "own"
	// LineageSourceFolderFallback means the path has no provenance of its
	// own, and the subject is the nearest enclosing folder collection that
	// does (design D13).
	LineageSourceFolderFallback LineageSource = "folder-fallback"
	// LineageSourceDeclaredCollection means the path lies under a
	// provenance.collections folder, which is covered only through the
	// folder (design "Declared collection folders").
	LineageSourceDeclaredCollection LineageSource = "declared-collection"
)

// Subject is exo provenance show's resolved starting point.
type Subject struct {
	ID         string
	Source     LineageSource
	FolderPath string // set when Source != LineageSourceOwn
}

// FolderHistoryHint is the nearest older tree id of a folder path that is
// covered by a provenance record, surfaced when the folder's current tree
// id is not (design D15 Risk: "Folder tree ids change when annexed members
// switch between locked and unlocked mode... show reports the nearest
// older covered tree of the same path as a hint").
type FolderHistoryHint struct {
	FolderPath string
	TreeID     string
	CommitSHA  string
}

// refOrHead returns the git ref a Reader reads from for history lookups
// (design D9 "a ref (default HEAD)"): the Reader's ref in ModeRef, or
// "HEAD" otherwise.
func refOrHead(reader *Reader) string {
	if reader.mode == ModeRef && reader.ref != "" {
		return reader.ref
	}
	return "HEAD"
}

// nodeIsCovered reports whether node is covered by its own provenance
// (design D14): it is the subject of wasGeneratedBy, wasDerivedFrom,
// wasAttributedTo or wasRevisionOf (coveringProperties, coverage.go). A
// node that is only "used" as another activity's input is not covered —
// review: "'own provenance' should mean covered, not 'appears in the
// graph'".
func nodeIsCovered(node *MergedNode) bool {
	if node == nil {
		return false
	}
	for _, prop := range coveringProperties {
		if _, ok := node.Node[prop]; ok {
			return true
		}
	}
	return false
}

// ResolveSubject resolves path (repo-relative) to exo provenance show's
// subject at reader's checked state (design D9, D13, D15): the path's own
// content id if it is covered by its own provenance, else the nearest
// enclosing covered folder collection, or the folder a provenance.collections
// declaration covers it through (covered or not — a declared collection
// covers its members exclusively through the folder, never through the
// member's own records, matching coverage.go's fileIsCovered). It also
// returns a FolderHistoryHint when a candidate folder's current tree id is
// uncovered but an older one is (design D15 Risk).
func ResolveSubject(reader *Reader, path string, merged *MergedGraph) (Subject, *FolderHistoryHint, error) {
	if strings.HasSuffix(path, "/") {
		id, err := reader.ResolveContentID(path)
		if err != nil {
			return Subject{}, nil, err
		}
		subj := Subject{ID: id, Source: LineageSourceOwn}
		if nodeIsCovered(merged.Nodes[id]) {
			return subj, nil, nil
		}
		dir := strings.TrimSuffix(path, "/")
		return subj, folderHistoryHint(reader, dir, merged), nil
	}

	cfg, err := reader.LoadConfig()
	if err != nil {
		return Subject{}, nil, fmt.Errorf("load .exohub/config: %w", err)
	}
	var declared []string
	if cfg != nil && cfg.Provenance != nil {
		declared = cfg.Provenance.Collections
	}

	if len(declared) > 0 {
		dir, err := findDeclaredAncestor(path, declared)
		if err != nil {
			return Subject{}, nil, err
		}
		if dir != "" {
			id, err := reader.ResolveContentID(dir + "/")
			if err != nil {
				return Subject{}, nil, err
			}
			subj := Subject{ID: id, Source: LineageSourceDeclaredCollection, FolderPath: dir}
			if nodeIsCovered(merged.Nodes[id]) {
				return subj, nil, nil
			}
			return subj, folderHistoryHint(reader, dir, merged), nil
		}
	}

	ownID, err := reader.ResolveContentID(path)
	if err != nil {
		return Subject{}, nil, err
	}
	if nodeIsCovered(merged.Nodes[ownID]) {
		return Subject{ID: ownID, Source: LineageSourceOwn}, nil, nil
	}

	var hint *FolderHistoryHint
	for _, dir := range pathAncestors(path) {
		treeID, err := reader.ResolveContentID(dir + "/")
		if err != nil {
			continue
		}
		if nodeIsCovered(merged.Nodes[treeID]) {
			return Subject{ID: treeID, Source: LineageSourceFolderFallback, FolderPath: dir}, nil, nil
		}
		if hint == nil {
			hint = folderHistoryHint(reader, dir, merged)
		}
	}
	return Subject{ID: ownID, Source: LineageSourceOwn}, hint, nil
}

// hasCollectionAtPath reports whether merged has a Collection node whose
// recorded exo:path equals dirWithSlash: the only situation an older tree
// id of dir could ever be covered (review: "only compute a hint for a
// directory when the merged graph has a Collection node whose recorded
// exo:path equals dir + '/'"). It scans merged's nodes, which are bounded
// by the repository's record count, not its commit history.
func hasCollectionAtPath(merged *MergedGraph, dirWithSlash string) bool {
	for _, n := range merged.Nodes {
		path, ok := n.Node["exo:path"].(string)
		if !ok || path != dirWithSlash {
			continue
		}
		for _, t := range n.Types() {
			if t == "Collection" {
				return true
			}
		}
	}
	return false
}

// folderHistoryHint implements the D15 Risk hint: it walks dir's commit
// history at reader's ref and returns the nearest earlier commit whose
// tree id at dir is covered by a provenance record, or nil if none is.
//
// It only runs any git history commands when hasCollectionAtPath confirms
// a Collection record exists for dir at all (otherwise no older covered
// tree can exist), and resolves every candidate commit's tree id at dir in
// one batched `git cat-file --batch-check` rather than one `git rev-parse`
// per commit (review: "on a 400-commit repo, show on an uncovered file
// takes 2.1s... please get <commit>:<dir> tree ids in one batch").
func folderHistoryHint(reader *Reader, dir string, merged *MergedGraph) *FolderHistoryHint {
	if !hasCollectionAtPath(merged, dir+"/") {
		return nil
	}
	out, err := gitRun(reader.root, "log", refOrHead(reader), "--format=%H", "--", dir)
	if err != nil {
		return nil
	}
	shas := splitLines(out)
	if len(shas) == 0 {
		return nil
	}
	treeIDs, err := batchTreeIDsAt(reader.root, shas, dir)
	if err != nil {
		return nil
	}
	for _, sha := range shas {
		oid, ok := treeIDs[sha]
		if !ok {
			continue
		}
		id := GitTreeEntityID(oid)
		if nodeIsCovered(merged.Nodes[id]) {
			return &FolderHistoryHint{FolderPath: dir, TreeID: id, CommitSHA: sha}
		}
	}
	return nil
}

// upstreamProperties lists the node properties Walk follows upstream from
// a node (design D9: "walks wasDerivedFrom and wasGeneratedBy/used
// upstream"), plus the agent, plan and revision properties needed to print
// each step's activities, plans and agents (including actedOnBehalfOf).
var upstreamProperties = []string{
	"wasDerivedFrom", "wasRevisionOf", "wasGeneratedBy", "used",
	"hadPlan", "wasAssociatedWith", "actedOnBehalfOf", "wasAttributedTo",
}

// LineageEdge is one upstream statement Walk followed, carrying the record
// ids that asserted it (its "assertion source", design D9).
type LineageEdge struct {
	From        string
	To          string
	Property    string
	FromRecords []string
}

// Lineage is the result of walking a Subject's PROV lineage upstream
// (design D9).
type Lineage struct {
	Subject Subject
	Hint    *FolderHistoryHint
	Graph   *MergedGraph
	// Ref is the git ref the lineage was resolved at (design D9 "a ref,
	// default HEAD"), for display and for PathIndex's "not present at
	// <ref>" annotation.
	Ref string
	// Paths maps every entity/collection node's content id to its current
	// path(s) at Ref (review: "the human output prints only URNs...
	// build a reverse map content id -> path(s)").
	Paths PathIndex
	// Covered reports whether Subject.ID itself is covered by its own
	// provenance (nodeIsCovered), as opposed to only appearing in the
	// graph as something else's input.
	Covered bool
	// UsedByActivities lists the ids of Activity nodes that record Subject
	// as "used", populated only when Covered is false (review: "when
	// there is none, still print what is known, for example 'used by
	// activity X'").
	UsedByActivities []string
	Visited          []string // node ids, visit order, subject first; each appears once
	Edges            []LineageEdge
	Cycles           []string // node ids where an upstream edge closed a cycle
	DepthReached     bool     // true if --depth stopped the walk before it ran out of nodes
}

// Walk follows a subject's PROV lineage upstream through graph (design D9):
// a depth-first walk via upstreamProperties, visiting every reachable node
// once, detecting cycles (a back edge to a node still being visited) rather
// than flagging ordinary reconvergence in a DAG, and stopping new nodes
// beyond maxDepth hops from the subject. maxDepth <= 0 means unlimited.
func Walk(graph *MergedGraph, subjectID string, maxDepth int) *Lineage {
	visited := map[string]bool{}
	onStack := map[string]bool{}
	cycleSet := map[string]bool{}
	var order []string
	var edges []LineageEdge
	depthReached := false

	var dfs func(id string, depth int)
	dfs = func(id string, depth int) {
		visited[id] = true
		onStack[id] = true
		order = append(order, id)
		defer func() { onStack[id] = false }()

		node := graph.Nodes[id]
		if node == nil {
			return
		}
		if maxDepth > 0 && depth >= maxDepth {
			depthReached = true
			return
		}
		for _, prop := range upstreamProperties {
			raw, ok := node.Node[prop]
			if !ok {
				continue
			}
			for _, to := range nodeRefs(raw) {
				edges = append(edges, LineageEdge{
					From:        id,
					To:          to,
					Property:    prop,
					FromRecords: append([]string(nil), node.FromRecords...),
				})
				if onStack[to] {
					cycleSet[to] = true
					continue
				}
				if visited[to] {
					continue
				}
				dfs(to, depth+1)
			}
		}
	}
	dfs(subjectID, 0)

	// review item 4: a record's exo:assertedBy agent is envelope metadata,
	// not necessarily referenced by any PROV property on a visited node (a
	// software agent may be recorded only as the asserter, with no
	// wasAssociatedWith edge, for example), so it would otherwise be
	// missing from the lineage even though it signed a statement shown
	// above. Walk every contributing record's assertedBy agent (synthesized
	// as a plain exo:Agent node when the record's own @graph never defines
	// one) as an extra root, which also walks its actedOnBehalfOf chain via
	// the same upstreamProperties dfs; repeat until no new agent ids turn
	// up, since an agent can itself be the subject of its own record with a
	// different asserter.
	for {
		var newAgents []string
		recordsByAgent := map[string][]string{}
		for _, id := range order {
			node := graph.Nodes[id]
			if node == nil {
				continue
			}
			for _, recID := range node.FromRecords {
				meta, ok := graph.Records[recID]
				if !ok || meta.AssertedBy == "" || visited[meta.AssertedBy] {
					continue
				}
				if _, seen := recordsByAgent[meta.AssertedBy]; !seen {
					newAgents = append(newAgents, meta.AssertedBy)
				}
				recordsByAgent[meta.AssertedBy] = appendUniqueID(recordsByAgent[meta.AssertedBy], recID)
			}
		}
		if len(newAgents) == 0 {
			break
		}
		sort.Strings(newAgents)
		for _, agentID := range newAgents {
			if visited[agentID] {
				continue
			}
			node := graph.Nodes[agentID]
			if node == nil {
				// Synthesized so the agent is visible even though no record's
				// @graph defines it: its FromRecords is the record(s) that
				// name it as exo:assertedBy, so show still prints a "source"
				// line for it.
				node = &MergedNode{Node: Node{"@id": agentID, "@type": "Agent"}}
				graph.Nodes[agentID] = node
			}
			for _, recID := range recordsByAgent[agentID] {
				node.FromRecords = appendUniqueID(node.FromRecords, recID)
			}
			dfs(agentID, 0)
		}
	}

	cycles := make([]string, 0, len(cycleSet))
	for id := range cycleSet {
		cycles = append(cycles, id)
	}
	sort.Strings(cycles)

	return &Lineage{
		Graph:        graph,
		Visited:      order,
		Edges:        edges,
		Cycles:       cycles,
		DepthReached: depthReached,
	}
}

// PathIndex maps a content or tree id to the paths that currently resolve
// to it at a Reader's checked state (review: "the human output prints only
// URNs... build a reverse map content id -> path(s)").
type PathIndex map[string][]string

// Path returns id's current path, if any — the lexicographically first
// when more than one path resolves to the same id, for determinism.
func (idx PathIndex) Path(id string) (string, bool) {
	paths := idx[id]
	if len(paths) == 0 {
		return "", false
	}
	return paths[0], true
}

// BuildPathIndex resolves every tracked file's and directory's current
// path at reader's checked state, in a constant number of git invocations
// regardless of repository size (Reader.FileEntries, Reader.AllContentIDs,
// Reader.AllTreeIDs — the same batch helpers coverage.go uses, review:
// "one batch, no per-node git calls"). exo provenance show uses it to
// annotate lineage nodes with their path at the ref even when a recorded
// exo:path has gone stale, for example after a rename.
func BuildPathIndex(reader *Reader) (PathIndex, error) {
	idx := PathIndex{}

	entries, err := reader.FileEntries()
	if err != nil {
		return nil, err
	}
	contentIDs, err := reader.AllContentIDs(entries)
	if err != nil {
		return nil, err
	}
	for path, id := range contentIDs {
		idx[id] = append(idx[id], path)
	}

	treeIDs, err := reader.AllTreeIDs()
	if err != nil {
		return nil, err
	}
	for path, id := range treeIDs {
		idx[id] = append(idx[id], path+"/")
	}

	for id := range idx {
		sort.Strings(idx[id])
	}
	return idx, nil
}

// usedByActivities returns the sorted ids of every Activity node in graph
// that records "used" referencing id: the context exo provenance show
// prints for an uncovered subject that is only an input to something else
// (review: "'used-only' file... still print what is known, for example
// 'used by activity X'").
func usedByActivities(graph *MergedGraph, id string) []string {
	var out []string
	for actID, n := range graph.Nodes {
		raw, ok := n.Node["used"]
		if !ok {
			continue
		}
		for _, used := range nodeRefs(raw) {
			if used == id {
				out = append(out, actID)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// BuildLineage resolves path to its show subject and walks its lineage,
// combining ResolveSubject, LoadRecords, MergeRecords, BuildPathIndex and
// Walk (design D9, D13, D15) into the single entry point exo provenance
// show calls.
func BuildLineage(reader *Reader, path string, maxDepth int) (*Lineage, error) {
	parsed, err := LoadRecords(reader)
	if err != nil {
		return nil, fmt.Errorf("load provenance records: %w", err)
	}
	merged := MergeRecords(parsed)

	subject, hint, err := ResolveSubject(reader, path, merged)
	if err != nil {
		return nil, err
	}

	paths, err := BuildPathIndex(reader)
	if err != nil {
		return nil, fmt.Errorf("resolve current paths: %w", err)
	}

	lineage := Walk(merged, subject.ID, maxDepth)
	lineage.Subject = subject
	lineage.Hint = hint
	lineage.Ref = refOrHead(reader)
	lineage.Paths = paths
	lineage.Covered = nodeIsCovered(merged.Nodes[subject.ID])
	if !lineage.Covered {
		lineage.UsedByActivities = usedByActivities(merged, subject.ID)
	}
	return lineage, nil
}
