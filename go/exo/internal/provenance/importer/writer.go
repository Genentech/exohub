package importer

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	lib "github.com/Genentech/exohub/go/exo/internal/provenance"
)

// WriteOptions carries the parts of a `exo provenance import` record that
// come from the command invocation rather than the run model (design D7).
type WriteOptions struct {
	// AssertedBy is the exo:assertedBy id (a person or software agent).
	AssertedBy string
	// PersonID, if non-empty, is added as a Person node and associated
	// with every activity (design D7: "wasAssociatedWith ... the importing
	// person").
	PersonID    string
	PersonLabel string
	// Supersedes lists record ids this record supersedes (--supersedes).
	Supersedes []string
	// Evidence lists paths (for example kept source documents) to record
	// in exo:evidence (design D7, "Keeping the source lineage").
	Evidence    []string
	GeneratedAt time.Time
	// Tasks selects one Activity per task instead of one for the whole run
	// (spec "Run and task granularity").
	Tasks bool
}

// UnmatchedFile is one run file the importer could not resolve to
// unchanged repository content (spec "Matching run files to repository
// content").
type UnmatchedFile struct {
	Path   string
	Reason string
}

// MatchedFile is one run file the importer resolved to repository content,
// for the command's human and --json output (design D7's matcher,
// "recorded as 'verified by size' in the import summary" when the
// engine's checksum algorithm isn't one exo can compute).
type MatchedFile struct {
	Path string `json:"path"`
	// Verified is "checksum" or "size" (Matched.Verified), or "" when there
	// was nothing to verify against.
	Verified string `json:"verified"`
}

// Summary reports what an import did, for the command's human and --json
// output (spec "a summary of activities, inputs, outputs and unmatched
// files").
type Summary struct {
	Activities      int
	MatchedInputs   int
	MatchedOutputs  int
	ExternalInputs  int
	ExternalOutputs int
	// Matched lists every input/output file resolved to repository content,
	// with how it was verified.
	Matched   []MatchedFile
	Unmatched []UnmatchedFile
}

// Writer builds an ExoHub provenance Record from a Run (design D7).
type Writer struct {
	matcher *Matcher
}

// NewWriter returns a Writer that resolves run files with matcher.
func NewWriter(matcher *Matcher) *Writer {
	return &Writer{matcher: matcher}
}

// Write builds the imported record for run (design D7, spec "Mapping a run
// to PROV"): exo:assertion "imported", the engine and person agents, a
// plan entity for the pipeline, qualified usages/generations with roles
// for files, parameters, environments and results, and hadPrimarySource on
// every activity referencing run.ID (used for re-import detection).
func (w *Writer) Write(run Run, opts WriteOptions) (*lib.Record, Summary, error) {
	graph := map[string]lib.Node{}
	summary := Summary{}

	// The run itself, so hadPrimarySource has something to point to
	// (design D6: "Entity {@id: lid://... | crate id}").
	graph[run.ID] = lib.Node{"@id": run.ID, "@type": "Entity"}

	if opts.PersonID != "" {
		person := lib.Node{"@id": opts.PersonID, "@type": "Person"}
		if opts.PersonLabel != "" {
			person["label"] = opts.PersonLabel
		}
		graph[opts.PersonID] = person
	}

	var engineID string
	if run.Engine.Name != "" {
		engineID = lib.SoftwareAgentID(run.Engine.Name)
		node := lib.Node{"@id": engineID, "@type": "SoftwareAgent"}
		if run.Engine.Version != "" {
			node["exo:version"] = run.Engine.Version
		}
		graph[engineID] = node
	}

	var associated []string
	if engineID != "" {
		associated = append(associated, engineID)
	}
	if opts.PersonID != "" {
		associated = append(associated, opts.PersonID)
	}

	plans := Aggregate(run, opts.Tasks)
	planEntities := map[string]string{} // code checksum -> plan entity id, memoized across activities.

	for _, plan := range plans {
		activityID := lib.NewActivityID()
		activity := lib.Node{"@id": activityID, "@type": "Activity", "hadPrimarySource": run.ID}
		if !plan.Started.IsZero() {
			activity["startedAtTime"] = plan.Started.UTC().Format(time.RFC3339)
		}
		if !plan.Ended.IsZero() {
			activity["endedAtTime"] = plan.Ended.UTC().Format(time.RFC3339)
		}
		if len(associated) > 0 {
			activity["wasAssociatedWith"] = toValue(associated)
		}

		checksum := plan.CodeChecksum
		if checksum == "" {
			checksum = run.Pipeline.Checksum
		}
		if checksum != "" || run.Pipeline.Repository != "" || run.Pipeline.URL != "" {
			planID, ok := planEntities[checksum]
			if !ok {
				planID = w.writePlan(graph, run.Pipeline, checksum)
				planEntities[checksum] = planID
			}
			activity["hadPlan"] = planID
		}

		var used []string
		var usages []string

		for _, f := range plan.Inputs {
			matched := w.matcher.Match(f)
			w.recordFile(graph, matched, false, activityID, &summary)
			used = append(used, matched.ID)
			usages = append(usages, writeUsage(graph, matched.ID, f.Role, "input"))
		}
		for _, v := range plan.Params {
			valueID, err := writeValue(graph, v)
			if err != nil {
				return nil, Summary{}, fmt.Errorf("parameter %s: %w", v.Name, err)
			}
			used = append(used, valueID)
			usages = append(usages, writeUsage(graph, valueID, v.Name, ""))
		}
		for _, env := range plan.Environments {
			node := graph[env.ID]
			if node == nil {
				node = lib.Node{"@id": env.ID, "@type": "Entity"}
			}
			node["label"] = env.Kind
			graph[env.ID] = node
			used = append(used, env.ID)
			usages = append(usages, writeUsage(graph, env.ID, "environment", ""))
		}
		if len(used) > 0 {
			activity["used"] = toValue(used)
		}
		if len(usages) > 0 {
			activity["qualifiedUsage"] = toValue(usages)
		}

		for _, f := range plan.Outputs {
			matched := w.matcher.Match(f)
			w.recordFile(graph, matched, true, activityID, &summary)
			writeGeneration(graph, matched.ID, activityID, f.Role, "output")
		}
		for _, v := range plan.Results {
			valueID, err := writeValue(graph, v)
			if err != nil {
				return nil, Summary{}, fmt.Errorf("result %s: %w", v.Name, err)
			}
			writeGeneration(graph, valueID, activityID, v.Name, "")
		}

		graph[activityID] = activity
		summary.Activities++
	}

	rec := &lib.Record{
		Context:         lib.ContextIRI,
		ID:              lib.NewRecordID(),
		Assertion:       lib.AssertionImported,
		AssertedBy:      opts.AssertedBy,
		GeneratedAtTime: opts.GeneratedAt.UTC().Format(time.RFC3339),
		Evidence:        joinEvidence(opts.Evidence),
		Supersedes:      append([]string(nil), opts.Supersedes...),
		Graph:           graphNodes(graph),
	}
	if minVersion, ok := lib.ComputeMinVersion(rec.Graph); ok {
		rec.MinVersion = minVersion
	}

	data, err := lib.Serialize(rec)
	if err != nil {
		return nil, Summary{}, fmt.Errorf("build record: %w", err)
	}
	if _, err := lib.Parse(data); err != nil {
		return nil, Summary{}, fmt.Errorf("built an invalid record: %w", err)
	}

	return rec, summary, nil
}

// recordFile adds matched's entity node to graph and the usage/generation
// edge's shortcut property (used by the activity, or wasGeneratedBy on the
// entity), and tallies it into summary.
func (w *Writer) recordFile(graph map[string]lib.Node, matched Matched, generated bool, activityID string, summary *Summary) {
	node := graph[matched.ID]
	if node == nil {
		node = lib.Node{"@id": matched.ID, "@type": "Entity"}
	}
	if matched.File.Path != "" {
		node["exo:path"] = matched.File.Path
	}
	graph[matched.ID] = node

	switch {
	case matched.Unmatched != "":
		summary.Unmatched = append(summary.Unmatched, UnmatchedFile{Path: filePath(matched.File), Reason: matched.Unmatched})
	case matched.External:
		if generated {
			summary.ExternalOutputs++
		} else {
			summary.ExternalInputs++
		}
	default:
		summary.Matched = append(summary.Matched, MatchedFile{Path: filePath(matched.File), Verified: matched.Verified})
		if generated {
			summary.MatchedOutputs++
		} else {
			summary.MatchedInputs++
		}
	}
}

func filePath(f File) string {
	if f.Path != "" {
		return f.Path
	}
	return f.URL
}

// writePlan adds the shared plan entity for pipeline's code at the given
// checksum (design D5 "Step code as a pointer"): label is the pipeline
// name, exo:checksum the engine code checksum, exo:repository/exo:commit
// or exo:evidence-free exo:path/url the best available reference.
func (w *Writer) writePlan(graph map[string]lib.Node, pipeline Pipeline, checksum string) string {
	id := planEntityID(pipeline, checksum)
	node := lib.Node{"@id": id, "@type": "Entity"}
	if pipeline.Name != "" {
		node["label"] = pipeline.Name
	}
	if checksum != "" {
		node["exo:checksum"] = checksum
	}
	if pipeline.Repository != "" {
		node["exo:repository"] = pipeline.Repository
	}
	if pipeline.Commit != "" {
		node["exo:commit"] = pipeline.Commit
	}
	graph[id] = node
	return id
}

// planEntityID derives a deterministic id for a plan entity (there is no
// repository content to key it to, design D7): the sha256 of the
// pipeline's repository, commit, URL and the activity's effective code
// checksum, under a custom "exo-plan:" scheme rather than "urn:exohub:",
// since that namespace is reserved for the identifier kinds exo provenance
// validate recognizes (design D5, D7).
func planEntityID(pipeline Pipeline, checksum string) string {
	digest := sha256.Sum256([]byte(pipeline.Repository + "\x00" + pipeline.Commit + "\x00" + pipeline.URL + "\x00" + checksum))
	return "exo-plan:sha256:" + hex.EncodeToString(digest[:])
}

// writeValue adds a parameter/result value entity (design D3) and returns
// its id.
func writeValue(graph map[string]lib.Node, v Value) (string, error) {
	id, err := lib.ValueEntityID(v.Name, v.Value)
	if err != nil {
		return "", err
	}
	graph[id] = lib.Node{"@id": id, "@type": "Entity", "value": v.Value}
	return id, nil
}

// writeUsage adds a qualifiedUsage node for entityID with hadRole (design
// D2), falling back to defaultRole when role is empty, and returns the
// usage node's id.
func writeUsage(graph map[string]lib.Node, entityID, role, defaultRole string) string {
	if role == "" {
		role = defaultRole
	}
	id := lib.NewUsageID()
	graph[id] = lib.Node{"@id": id, "@type": "Usage", "entity": entityID, "hadRole": role}
	return id
}

// writeGeneration adds a qualifiedGeneration node for activityID with
// hadRole (design D2), falling back to defaultRole when role is empty, and
// sets the required wasGeneratedBy/qualifiedGeneration shortcuts on
// entityID.
func writeGeneration(graph map[string]lib.Node, entityID, activityID, role, defaultRole string) {
	if role == "" {
		role = defaultRole
	}
	id := lib.NewGenerationID()
	graph[id] = lib.Node{"@id": id, "@type": "Generation", "activity": activityID, "hadRole": role}

	node := graph[entityID]
	if node == nil {
		node = lib.Node{"@id": entityID, "@type": "Entity"}
	}
	node["wasGeneratedBy"] = activityID
	node["qualifiedGeneration"] = id
	graph[entityID] = node
}

func toValue(ids []string) any {
	if len(ids) == 1 {
		return ids[0]
	}
	out := make([]any, len(ids))
	for i, id := range ids {
		out[i] = id
	}
	return out
}

func graphNodes(graph map[string]lib.Node) []lib.Node {
	out := make([]lib.Node, 0, len(graph))
	for _, n := range graph {
		out = append(out, n)
	}
	return out
}

func joinEvidence(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	out := paths[0]
	for _, p := range paths[1:] {
		out += "; " + p
	}
	return out
}
