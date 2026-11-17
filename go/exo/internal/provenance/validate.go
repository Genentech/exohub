package provenance

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/google/uuid"
)

// Severity is how a Finding affects exo provenance validate's exit code
// (design D7 "Validation output": warnings and info never change it).
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
	SeverityInfo    Severity = "info"
)

// Finding classes group related checks. Group 3b (coverage, D14) appends
// ClassCoverage findings to the same Report without changing Validate's
// signature or this type.
const (
	ClassProfile      = "profile"
	ClassIdentifier   = "identifier"
	ClassCrossRecord  = "cross-record"
	ClassImmutability = "immutability"
	ClassWarning      = "warning"
	ClassCoverage     = "coverage"
)

// Finding is one validation result (design D7).
type Finding struct {
	Severity Severity `json:"severity"`
	Class    string   `json:"class"`
	Code     string   `json:"code"`
	Path     string   `json:"path,omitempty"`
	Message  string   `json:"message"`
	Detail   string   `json:"detail,omitempty"`
}

// Report is the result of Validate: every Finding at a checked state, plus
// how many record files were read (design D7: "Without records and without
// provenance.require rules, validation SHALL report that no records
// exist").
type Report struct {
	RecordCount int              `json:"record_count"`
	Findings    []Finding        `json:"findings"`
	Coverage    *CoverageSummary `json:"coverage,omitempty"`
}

func (r *Report) add(f Finding) {
	r.Findings = append(r.Findings, f)
}

// HasErrors reports whether any Finding is an error, the condition under
// which exo provenance validate and the exo doctor provenance check exit
// non-zero / FAIL (design D7, exo-doctor spec "Provenance checks").
func (r *Report) HasErrors() bool {
	for _, f := range r.Findings {
		if f.Severity == SeverityError {
			return true
		}
	}
	return false
}

type parsedRecord struct {
	path string
	rec  *Record
}

// Validate checks every provenance record at reader's checked state (design
// D15) against the record envelope, profile and vocabulary (D1, D2, D4),
// the add-only and git-blob rules (D3, D15 "Records must be git blobs"),
// cross-record consistency (D7, D15 "Single-valued properties") and the D7
// warnings. Coverage and provenance.require enforcement (D14) are a later
// change; this Report shape (Findings, Class) lets that change append
// ClassCoverage findings without altering Validate's signature or callers.
func Validate(reader *Reader) (*Report, error) {
	report := &Report{}

	parsed, err := parseRecordFiles(reader, report)
	if err != nil {
		return nil, err
	}

	checkCrossRecord(report, parsed)
	checkIdentifiers(reader, report, parsed)
	checkMinVersionDeclared(report, parsed)
	checkQualifiedNodes(report, parsed)

	if err := checkCollectionManifests(reader, report); err != nil {
		return nil, err
	}

	if err := checkImmutability(reader, report); err != nil {
		return nil, err
	}

	if err := checkCoverage(reader, report, parsed); err != nil {
		return nil, err
	}

	sort.SliceStable(report.Findings, func(i, j int) bool {
		a, b := report.Findings[i], report.Findings[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Code < b.Code
	})
	return report, nil
}

// parseRecordFiles reads every provenance record file at reader's checked
// state and parses the ones that pass the envelope/profile/identifier
// checks (design D4): an annexed record file, an invalid envelope, a
// schema violation or a malformed record id each append an error Finding
// to report and exclude that record from the returned list, rather than
// failing the whole read. Validate and CompileBundle (design D8.2) share
// this so their pre-cross-record checks stay identical.
func parseRecordFiles(reader *Reader, report *Report) ([]parsedRecord, error) {
	files, err := reader.ListRecords()
	if err != nil {
		return nil, fmt.Errorf("list provenance records: %w", err)
	}
	report.RecordCount = len(files)

	var parsed []parsedRecord
	for _, f := range files {
		if f.Annexed {
			report.add(Finding{
				Severity: SeverityError,
				Class:    ClassProfile,
				Code:     "annexed-record",
				Path:     f.Path,
				Message:  "record file is stored in git-annex, not git",
				Detail:   fmt.Sprintf("run \"exo add %s\" to stage it as a git blob", f.Path),
			})
			continue
		}
		rec, err := Parse(f.Data)
		if err != nil {
			report.add(Finding{
				Severity: SeverityError,
				Class:    ClassProfile,
				Code:     "invalid-record",
				Path:     f.Path,
				Message:  err.Error(),
			})
			continue
		}
		// Parse checks the envelope fields every reader needs to trust
		// (D4) but, by design, not the full closed profile: ValidateSchema
		// additionally rejects unknown envelope keys (schema
		// "additionalProperties": false) and malformed timestamps
		// (schema "format": "date-time" on generatedAtTime/
		// startedAtTime/endedAtTime, envelope and node level).
		if schemaErrs, err := ValidateSchema(f.Data); err != nil {
			report.add(Finding{
				Severity: SeverityError,
				Class:    ClassProfile,
				Code:     "invalid-record",
				Path:     f.Path,
				Message:  err.Error(),
			})
			continue
		} else if len(schemaErrs) > 0 {
			report.add(Finding{
				Severity: SeverityError,
				Class:    ClassProfile,
				Code:     "schema-violation",
				Path:     f.Path,
				Message:  strings.Join(schemaErrs, "; "),
			})
			continue
		}
		if !IsUUIDv7(rec.ID) {
			report.add(Finding{
				Severity: SeverityError,
				Class:    ClassIdentifier,
				Code:     "malformed-record-id",
				Path:     f.Path,
				Message:  fmt.Sprintf("@id %q is not urn:exohub:record:<UUIDv7>", rec.ID),
			})
			continue
		}
		parsed = append(parsed, parsedRecord{path: f.Path, rec: rec})
	}
	return parsed, nil
}

// checkCollectionManifests validates every named collection manifest's
// format and member pinning, fully offline (design D5 of
// add-exohub-asset-import: "Offline validate checks format and pinning
// only"): LoadCollectionManifest parses the JSON and checks each
// "members" entry is a well-formed, pinned exohub: reference without ever
// resolving it over the network. This runs unconditionally, unlike
// checkCoverage, so a malformed manifest is always reported even in a
// repository with no provenance records or provenance.require rules.
func checkCollectionManifests(reader *Reader, report *Report) error {
	names, err := ListCollectionNames(reader)
	if err != nil {
		if looksLikeNoCommits(err) {
			return nil
		}
		return err
	}
	for _, name := range names {
		if _, err := LoadCollectionManifest(reader, name); err != nil {
			report.add(Finding{
				Severity: SeverityError,
				Class:    ClassProfile,
				Code:     "invalid-collection-manifest",
				Path:     CollectionManifestPath(name),
				Message:  err.Error(),
			})
		}
	}
	return nil
}

// checkCoverage implements D14/D15: it evaluates coverage and
// provenance.require at reader's checked state and appends an info
// finding with the covered/uncovered summary plus an error finding for
// every uncovered file a require rule matches. It runs only when there are
// records or require rules to check (design D15 "Doctor and validate
// triggers"), matching the same skip condition callers already apply for
// the "no records" message.
func checkCoverage(reader *Reader, report *Report, parsed []parsedRecord) error {
	cfg, err := reader.LoadConfig()
	if err != nil {
		return fmt.Errorf("load .exohub/config: %w", err)
	}
	var requireGlobs []string
	if cfg != nil && cfg.Provenance != nil {
		requireGlobs = cfg.Provenance.Require
	}
	if report.RecordCount == 0 && len(requireGlobs) == 0 {
		return nil
	}

	superseded := supersededIDs(parsed)
	summary, uncovered, err := evaluateCoverage(reader, parsed, superseded)
	if err != nil {
		return fmt.Errorf("evaluate coverage: %w", err)
	}
	report.Coverage = summary

	violations, err := evaluateRequire(reader, uncovered, requireGlobs)
	if err != nil {
		return fmt.Errorf("evaluate provenance.require: %w", err)
	}
	for _, path := range violations {
		report.add(Finding{
			Severity: SeverityError,
			Class:    ClassCoverage,
			Code:     "required-provenance",
			Path:     path,
			Message:  "matches provenance.require and has no covering record",
			Detail:   "run \"exo provenance add\" to record its provenance",
		})
	}
	return nil
}

// checkCrossRecord implements the D7/D15 cross-record checks: duplicate
// record ids, exo:supersedes referencing an unknown record, and conflicting
// single-valued properties on the same node across non-superseded records.
func checkCrossRecord(report *Report, parsed []parsedRecord) {
	byID := map[string][]string{}
	for _, p := range parsed {
		byID[p.rec.ID] = append(byID[p.rec.ID], p.path)
	}
	for id, paths := range byID {
		if len(paths) < 2 {
			continue
		}
		sorted := append([]string(nil), paths...)
		sort.Strings(sorted)
		for _, path := range sorted {
			report.add(Finding{
				Severity: SeverityError,
				Class:    ClassCrossRecord,
				Code:     "duplicate-record-id",
				Path:     path,
				Message:  fmt.Sprintf("record @id %s is used by %d records: %s", id, len(paths), strings.Join(sorted, ", ")),
			})
		}
	}

	known := map[string]bool{}
	for _, p := range parsed {
		known[p.rec.ID] = true
	}
	superseded := map[string]bool{}
	for _, p := range parsed {
		for _, s := range p.rec.Supersedes {
			superseded[s] = true
			if !known[s] {
				report.add(Finding{
					Severity: SeverityError,
					Class:    ClassCrossRecord,
					Code:     "unknown-supersedes",
					Path:     p.path,
					Message:  fmt.Sprintf("exo:supersedes references unknown record %s", s),
				})
			}
		}
	}

	checkSingleValuedConflicts(report, parsed, superseded)
}

// singleValuedProperties lists the node properties D15 treats as
// single-valued: a conflicting value for the same node across
// non-superseded records is an error.
var singleValuedProperties = []string{
	"startedAtTime", "endedAtTime", "generatedAtTime",
	"wasGeneratedBy", "exo:model", "exo:version",
}

type conflictKey struct{ node, prop string }

type conflictValue struct {
	text string
	path string
}

// normalizeIDRefText renders a property value for conflict comparison: an
// IRI-valued property may be written as a bare string or as a compact
// {"@id": "..."} object (same shape idRef decodes for the envelope), so
// both forms of the same reference must compare equal rather than being
// reported as conflicting (review: "wasGeneratedBy": "x" and {"@id": "x"}
// are not a conflict). Non-idRef values (for example a plain date-time
// string) pass through unchanged.
func normalizeIDRefText(raw any) string {
	switch v := raw.(type) {
	case string:
		return v
	case map[string]any:
		if id, ok := v["@id"].(string); ok {
			return id
		}
	}
	return fmt.Sprintf("%v", raw)
}

func checkSingleValuedConflicts(report *Report, parsed []parsedRecord, superseded map[string]bool) {
	seen := map[conflictKey]conflictValue{}
	for _, p := range parsed {
		if superseded[p.rec.ID] {
			continue
		}
		for _, n := range p.rec.Graph {
			nodeID := n.ID()
			for _, prop := range singleValuedProperties {
				raw, ok := n[prop]
				if !ok {
					continue
				}
				text := normalizeIDRefText(raw)
				key := conflictKey{nodeID, prop}
				if prev, exists := seen[key]; exists {
					if prev.text != text {
						report.add(Finding{
							Severity: SeverityError,
							Class:    ClassCrossRecord,
							Code:     "conflicting-property",
							Path:     p.path,
							Message:  fmt.Sprintf("%s %s=%s conflicts with %s=%s in %s", nodeID, prop, text, prop, prev.text, prev.path),
						})
					}
					continue
				}
				seen[key] = conflictValue{text: text, path: p.path}
			}
		}
	}
}

// checkMinVersionDeclared implements the D1 part of the "Record envelope"
// and "Readers refuse records that need a newer exo" requirements that
// does not depend on the running exo version (design D1, task 2.1
// scenarios "New terms require minVersion" and "Original records need no
// minVersion"): a record that uses a term from termIntroducedIn must
// carry exo:minVersion at least as high as that term requires.
func checkMinVersionDeclared(report *Report, parsed []parsedRecord) {
	for _, p := range parsed {
		required, ok := ComputeMinVersion(p.rec.Graph)
		if !ok {
			continue
		}
		if p.rec.MinVersion == "" {
			report.add(Finding{
				Severity: SeverityError,
				Class:    ClassProfile,
				Code:     "missing-minversion",
				Path:     p.path,
				Message:  fmt.Sprintf("record uses a term introduced in exo %s but has no exo:minVersion", required),
			})
			continue
		}
		if compareSemver(required, p.rec.MinVersion) > 0 {
			report.add(Finding{
				Severity: SeverityError,
				Class:    ClassProfile,
				Code:     "minversion-too-low",
				Path:     p.path,
				Message:  fmt.Sprintf("exo:minVersion %s is lower than %s, the version required by the terms this record uses", p.rec.MinVersion, required),
			})
		}
	}
}

// checkRecordVersions implements "Readers refuse records that need a newer
// exo" for a []parsedRecord already in hand (compileBundle): every record
// whose exo:minVersion is refused by runningVersion (NeedsUpgrade) is an
// error Finding. runningVersion == "" skips the check entirely (treated as
// a development build by NeedsUpgrade/IsDevVersion), which keeps
// CompileBundle and CompileBundleWithVerify's behavior unchanged for
// callers that do not pass a running version.
func checkRecordVersions(report *Report, parsed []parsedRecord, runningVersion string) {
	for _, p := range parsed {
		if NeedsUpgrade(p.rec.MinVersion, runningVersion) {
			report.add(Finding{
				Severity: SeverityError,
				Class:    ClassProfile,
				Code:     "needs-upgrade",
				Path:     p.path,
				Message:  MinVersionMessage(p.rec.MinVersion),
			})
		}
	}
}

// checkQualifiedNodes implements design D2/D3/D4/D5 validation (task 2.4,
// specs "Roles of inputs and outputs", "Parameter values", "Software
// environment", "Step code pointers", "Non-file results"): qualified
// usage/generation nodes have the required entity/activity and hadRole and
// their unqualified shortcut, value entities have a deterministic id,
// environment entities identified by tag only get a warning, and plan
// entities with no traceable reference get a warning.
func checkQualifiedNodes(report *Report, parsed []parsedRecord) {
	for _, p := range parsed {
		nodes := make(map[string]Node, len(p.rec.Graph))
		for _, n := range p.rec.Graph {
			nodes[n.ID()] = n
		}
		checkUsageNodes(report, p.path, p.rec.Graph)
		checkGenerationNodes(report, p.path, p.rec.Graph)
		checkValueEntities(report, p.path, p.rec.Graph)
		checkEnvironmentNodes(report, p.path, p.rec.Graph)
		checkPlanPointers(report, p.path, p.rec.Graph, nodes)
	}
}

// idRefValues extracts every id a property value references (a bare IRI
// string, a compact {"@id": ...} object, or an array of either).
func idRefValues(v any) []string {
	var out []string
	collectValueIdentifiers(v, func(id string) { out = append(out, id) })
	return out
}

func containsString(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}

func hasType(n Node, typ string) bool {
	return containsString(n.Types(), typ)
}

// checkUsageNodes validates every Usage-typed node (spec "Roles of inputs
// and outputs"): it must have a non-empty entity and hadRole, and the
// activity that references it via qualifiedUsage must also list that
// entity in its unqualified used.
func checkUsageNodes(report *Report, path string, graph []Node) {
	for _, n := range graph {
		if !hasType(n, "Usage") {
			continue
		}
		entityIDs := idRefValues(n["entity"])
		role, _ := n["hadRole"].(string)
		if len(entityIDs) == 0 {
			report.add(Finding{
				Severity: SeverityError,
				Class:    ClassProfile,
				Code:     "qualified-usage-missing-entity",
				Path:     path,
				Message:  fmt.Sprintf("qualifiedUsage node %s is missing entity", n.ID()),
			})
		}
		if role == "" {
			report.add(Finding{
				Severity: SeverityError,
				Class:    ClassProfile,
				Code:     "qualified-usage-missing-role",
				Path:     path,
				Message:  fmt.Sprintf("qualifiedUsage node %s is missing hadRole", n.ID()),
			})
		}
		for _, act := range graph {
			if !hasType(act, "Activity") || !containsString(idRefValues(act["qualifiedUsage"]), n.ID()) {
				continue
			}
			used := idRefValues(act["used"])
			for _, eid := range entityIDs {
				if !containsString(used, eid) {
					report.add(Finding{
						Severity: SeverityError,
						Class:    ClassProfile,
						Code:     "missing-usage-shortcut",
						Path:     path,
						Message:  fmt.Sprintf("activity %s has qualifiedUsage of %s but no matching used", act.ID(), eid),
					})
				}
			}
		}
	}
}

// checkGenerationNodes validates every Generation-typed node (spec "Roles
// of inputs and outputs"): it must have a non-empty activity and hadRole,
// and the entity that references it via qualifiedGeneration must also list
// that activity in its unqualified wasGeneratedBy.
func checkGenerationNodes(report *Report, path string, graph []Node) {
	for _, n := range graph {
		if !hasType(n, "Generation") {
			continue
		}
		activityIDs := idRefValues(n["activity"])
		role, _ := n["hadRole"].(string)
		if len(activityIDs) == 0 {
			report.add(Finding{
				Severity: SeverityError,
				Class:    ClassProfile,
				Code:     "qualified-generation-missing-activity",
				Path:     path,
				Message:  fmt.Sprintf("qualifiedGeneration node %s is missing activity", n.ID()),
			})
		}
		if role == "" {
			report.add(Finding{
				Severity: SeverityError,
				Class:    ClassProfile,
				Code:     "qualified-generation-missing-role",
				Path:     path,
				Message:  fmt.Sprintf("qualifiedGeneration node %s is missing hadRole", n.ID()),
			})
		}
		for _, ent := range graph {
			if !containsString(idRefValues(ent["qualifiedGeneration"]), n.ID()) {
				continue
			}
			generatedBy := idRefValues(ent["wasGeneratedBy"])
			for _, aid := range activityIDs {
				if !containsString(generatedBy, aid) {
					report.add(Finding{
						Severity: SeverityError,
						Class:    ClassProfile,
						Code:     "missing-generation-shortcut",
						Path:     path,
						Message:  fmt.Sprintf("entity %s has qualifiedGeneration by %s but no matching wasGeneratedBy", ent.ID(), aid),
					})
				}
			}
		}
	}
}

// rolesForValueEntity returns the hadRole values of every Usage node that
// uses entityID, and every Generation node that entityID's own
// qualifiedGeneration references (spec "Parameter values", "Non-file
// results").
func rolesForValueEntity(graph []Node, entityID string) []string {
	var roles []string
	for _, n := range graph {
		if hasType(n, "Usage") && containsString(idRefValues(n["entity"]), entityID) {
			if role, _ := n["hadRole"].(string); role != "" {
				roles = append(roles, role)
			}
		}
	}
	for _, ent := range graph {
		if ent.ID() != entityID {
			continue
		}
		for _, genID := range idRefValues(ent["qualifiedGeneration"]) {
			for _, gn := range graph {
				if gn.ID() == genID && hasType(gn, "Generation") {
					if role, _ := gn["hadRole"].(string); role != "" {
						roles = append(roles, role)
					}
				}
			}
		}
	}
	return roles
}

// checkValueEntities validates every entity with a value property (spec
// "Parameter values", "Non-file results"): its @id must be the
// deterministic urn:exohub:value:sha256(...) id of at least one role it is
// used or generated with.
func checkValueEntities(report *Report, path string, graph []Node) {
	for _, n := range graph {
		value, hasValue := n["value"]
		if !hasValue {
			continue
		}
		roles := rolesForValueEntity(graph, n.ID())
		if len(roles) == 0 {
			continue
		}
		var expected []string
		match := false
		for _, role := range roles {
			id, err := ValueEntityID(role, value)
			if err != nil {
				continue
			}
			expected = append(expected, id)
			if n.ID() == id {
				match = true
			}
		}
		if !match && len(expected) > 0 {
			report.add(Finding{
				Severity: SeverityError,
				Class:    ClassProfile,
				Code:     "value-entity-id-mismatch",
				Path:     path,
				Message:  fmt.Sprintf("value entity %s does not match urn:exohub:value:sha256(role, value); expected %s", n.ID(), strings.Join(expected, " or ")),
			})
		}
	}
}

// dockerDigestPattern matches a container image reference identified by
// digest ("...@sha256:..."), as opposed to a tag.
var dockerDigestPattern = regexp.MustCompile(`@sha256:[0-9a-f]{64}$`)

// checkEnvironmentNodes warns on an environment entity identified by tag
// only (spec "Software environment"): a qualifiedUsage with hadRole
// "environment" whose entity is a docker:// reference with no digest.
func checkEnvironmentNodes(report *Report, path string, graph []Node) {
	for _, n := range graph {
		if !hasType(n, "Usage") {
			continue
		}
		role, _ := n["hadRole"].(string)
		if role != "environment" {
			continue
		}
		for _, eid := range idRefValues(n["entity"]) {
			if strings.HasPrefix(eid, "docker://") && !dockerDigestPattern.MatchString(eid) {
				report.add(Finding{
					Severity: SeverityWarning,
					Class:    ClassWarning,
					Code:     "environment-tag-only",
					Path:     path,
					Message:  fmt.Sprintf("environment %s is identified by tag only, not reproducible", eid),
				})
			}
		}
	}
}

// planIsTraceable reports whether plan has a reference better than its
// exo:checksum alone (spec "Step code pointers"): a repository content id,
// exo:repository + exo:commit, or another addressable absolute URL.
func planIsTraceable(plan Node) bool {
	id := plan.ID()
	if strings.HasPrefix(id, AnnexURNPrefix) || strings.HasPrefix(id, GitBlobURNPrefix) {
		return true
	}
	_, hasRepository := plan["exo:repository"]
	_, hasCommit := plan["exo:commit"]
	if hasRepository && hasCommit {
		return true
	}
	if !strings.HasPrefix(id, "urn:exohub:") && absoluteIRIPattern.MatchString(id) {
		return true
	}
	return false
}

// checkPlanPointers warns when a hadPlan entity has only exo:checksum and
// no other reference (spec "Step code pointers", scenario "Untraceable
// code").
func checkPlanPointers(report *Report, path string, graph []Node, nodes map[string]Node) {
	for _, n := range graph {
		if !hasType(n, "Activity") {
			continue
		}
		for _, planID := range idRefValues(n["hadPlan"]) {
			plan, ok := nodes[planID]
			if !ok || planIsTraceable(plan) {
				continue
			}
			if _, hasChecksum := plan["exo:checksum"]; hasChecksum {
				report.add(Finding{
					Severity: SeverityWarning,
					Class:    ClassWarning,
					Code:     "plan-not-traceable",
					Path:     path,
					Message:  "pipeline code not traceable",
				})
			}
		}
	}
}

// gitOIDPattern matches a git object id: 40 lowercase hex characters
// (SHA-1) or 64 (SHA-256).
var gitOIDPattern = regexp.MustCompile(`^[0-9a-f]{40}$|^[0-9a-f]{64}$`)

// sha256HexPattern matches a lowercase hex SHA-256 digest: exactly 64
// characters (design D1 of add-explicit-collections,
// urn:exohub:collection:sha256:<hex>).
var sha256HexPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// validAgentName reports whether name is a non-empty agent name containing
// no whitespace or "/" (design D5 agent:person:<name> /
// agent:software:<name>).
func validAgentName(name string) bool {
	return name != "" && !strings.ContainsAny(name, " \t\r\n/")
}

// ValidAgentName reports whether name is usable as the <name> in a
// person:<name> or software:<name> agent reference: non-empty, and free of
// whitespace and "/". Exported so callers that construct agent references
// before validation runs (e.g. 'exo provenance add') can reject an invalid
// name up front instead of writing a record that 'exo provenance validate'
// would later reject.
func ValidAgentName(name string) bool {
	return validAgentName(name)
}

// classifyIdentifier checks a urn:exohub:-prefixed id's shape against
// design D5's identifier kinds: a recognized kind (annex:, git:blob:,
// git:tree:, git:commit:, record:, activity:, agent:person:,
// agent:software:) with a malformed value, or an unrecognized
// urn:exohub: kind, is an error (review: "Malformed identifiers are not
// errors" is a gap in the spec's "Validation errors"). Callers must only
// pass values that already have the "urn:exohub:" prefix; a value in any
// other namespace is not an identifier kind this function knows how to
// check.
func classifyIdentifier(id string) (ok bool, message string) {
	switch {
	case strings.HasPrefix(id, AnnexURNPrefix):
		if key := strings.TrimPrefix(id, AnnexURNPrefix); !annexKeyPattern.MatchString(key) {
			return false, fmt.Sprintf("%s is not a well-formed git-annex key", id)
		}
	case strings.HasPrefix(id, GitBlobURNPrefix):
		if oid := strings.TrimPrefix(id, GitBlobURNPrefix); !gitOIDPattern.MatchString(oid) {
			return false, fmt.Sprintf("%s is not a 40 or 64 character lowercase hex git object id", id)
		}
	case strings.HasPrefix(id, GitTreeURNPrefix):
		if oid := strings.TrimPrefix(id, GitTreeURNPrefix); !gitOIDPattern.MatchString(oid) {
			return false, fmt.Sprintf("%s is not a 40 or 64 character lowercase hex git object id", id)
		}
	case strings.HasPrefix(id, GitCommitURNPrefix):
		if oid := strings.TrimPrefix(id, GitCommitURNPrefix); !gitOIDPattern.MatchString(oid) {
			return false, fmt.Sprintf("%s is not a 40 or 64 character lowercase hex git object id", id)
		}
	case strings.HasPrefix(id, CollectionURNPrefix):
		if digest := strings.TrimPrefix(id, CollectionURNPrefix); !sha256HexPattern.MatchString(digest) {
			return false, fmt.Sprintf("%s is not a 64 character lowercase hex SHA-256 digest", id)
		}
	case strings.HasPrefix(id, RecordURNPrefix):
		if !IsUUIDv7(id) {
			return false, fmt.Sprintf("%s is not urn:exohub:record:<UUIDv7>", id)
		}
	case strings.HasPrefix(id, ActivityURNPrefix):
		rest := strings.TrimPrefix(id, ActivityURNPrefix)
		u, err := uuid.Parse(rest)
		if err != nil || u.Version() != 7 {
			return false, fmt.Sprintf("%s is not urn:exohub:activity:<UUIDv7>", id)
		}
	case strings.HasPrefix(id, UsageURNPrefix):
		rest := strings.TrimPrefix(id, UsageURNPrefix)
		u, err := uuid.Parse(rest)
		if err != nil || u.Version() != 7 {
			return false, fmt.Sprintf("%s is not urn:exohub:usage:<UUIDv7>", id)
		}
	case strings.HasPrefix(id, GenerationURNPrefix):
		rest := strings.TrimPrefix(id, GenerationURNPrefix)
		u, err := uuid.Parse(rest)
		if err != nil || u.Version() != 7 {
			return false, fmt.Sprintf("%s is not urn:exohub:generation:<UUIDv7>", id)
		}
	case strings.HasPrefix(id, ValueURNPrefix):
		if digest := strings.TrimPrefix(id, ValueURNPrefix); !sha256HexPattern.MatchString(digest) {
			return false, fmt.Sprintf("%s is not a 64 character lowercase hex SHA-256 digest", id)
		}
	case strings.HasPrefix(id, PersonAgentURNPrefix):
		if name := strings.TrimPrefix(id, PersonAgentURNPrefix); !validAgentName(name) {
			return false, fmt.Sprintf("%s has an empty name, or one containing whitespace or \"/\"", id)
		}
	case strings.HasPrefix(id, SoftwareAgentURNPrefix):
		if name := strings.TrimPrefix(id, SoftwareAgentURNPrefix); !validAgentName(name) {
			return false, fmt.Sprintf("%s has an empty name, or one containing whitespace or \"/\"", id)
		}
	default:
		return false, fmt.Sprintf("%s is not a recognized urn:exohub: identifier kind", id)
	}
	return true, ""
}

// absoluteIRIPattern matches a value starting with "<scheme>:", the
// minimum shape of an absolute IRI (RFC 3987): a non-urn:exohub: value in
// a property the profile declares IRI-valued must have this shape.
var absoluteIRIPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)

// iriValuedNodeProperties lists the node properties the record profile
// (schema "idRefOrArray") declares IRI-valued: their values are always
// identifiers, so a non-urn:exohub: value must at least be an absolute
// IRI (review: "any other value in an IRI-valued property must be an
// absolute IRI"). Any other node property (for example exo:path or a
// custom extension property) has a profile-defined but otherwise open
// value shape (D2) and is not held to this rule; its value is only
// inspected far enough to catch an accidentally malformed urn:exohub:
// identifier, never to require one.
var iriValuedNodeProperties = map[string]bool{
	"used":                true,
	"wasGeneratedBy":      true,
	"wasDerivedFrom":      true,
	"wasRevisionOf":       true,
	"wasAssociatedWith":   true,
	"wasAttributedTo":     true,
	"actedOnBehalfOf":     true,
	"hadPlan":             true,
	"hadMember":           true,
	"hadPrimarySource":    true,
	"qualifiedUsage":      true,
	"qualifiedGeneration": true,
	"entity":              true,
	"activity":            true,
}

type identifierRef struct {
	path string
	id   string
}

// checkIdentifiers implements the D7/D5 identifier checks: every
// identifier a record declares IRI-valued (a node @id, exo:assertedBy,
// exo:supersedes[i], or an idRefOrArray-typed node property) is
// well-formed for its kind (ClassIdentifier, malformed-identifier), and,
// for well-formed ids only, the D7 warnings (non-hashing annex backends,
// repository content referenced by a record but missing from the checked
// state, and asserted software-agent records without evidence). Content
// existence is checked with one batched git/git-annex call across every
// record rather than one process per identifier, since doctor and
// validate run this on every invocation.
func checkIdentifiers(reader *Reader, report *Report, parsed []parsedRecord) {
	var blobTreeRefs, annexRefs []identifierRef
	seenMalformed := map[identifierRef]bool{}

	reportMalformed := func(path, id, msg string) {
		ref := identifierRef{path: path, id: id}
		if seenMalformed[ref] {
			return
		}
		seenMalformed[ref] = true
		report.add(Finding{
			Severity: SeverityError,
			Class:    ClassIdentifier,
			Code:     "malformed-identifier",
			Path:     path,
			Message:  msg,
		})
	}

	// checkValue validates id's shape (design D5): a urn:exohub: value
	// must match its kind wherever it appears; a non-urn:exohub: value is
	// only required to be an absolute IRI when requireIRI is set (an
	// iriValuedNodeProperties property, a node @id, exo:assertedBy or
	// exo:supersedes[i]). It returns whether id is well-formed, so
	// content-existence/backend checks (queued by the caller) are skipped
	// for identifiers that are not (review: "missing-content stays a
	// warning, for well-formed ids only").
	checkValue := func(path, id string, requireIRI bool) bool {
		if id == "" {
			return true
		}
		if strings.HasPrefix(id, "urn:exohub:") {
			if ok, msg := classifyIdentifier(id); !ok {
				reportMalformed(path, id, msg)
				return false
			}
			return true
		}
		if requireIRI && !absoluteIRIPattern.MatchString(id) {
			reportMalformed(path, id, fmt.Sprintf("%s is not an absolute IRI", id))
			return false
		}
		return true
	}

	queueContentCheck := func(path, id string) {
		switch {
		case strings.HasPrefix(id, AnnexURNPrefix):
			annexRefs = append(annexRefs, identifierRef{path: path, id: id})
		case strings.HasPrefix(id, GitBlobURNPrefix), strings.HasPrefix(id, GitTreeURNPrefix):
			blobTreeRefs = append(blobTreeRefs, identifierRef{path: path, id: id})
		}
	}

	for _, p := range parsed {
		if checkValue(p.path, p.rec.AssertedBy, true) {
			queueContentCheck(p.path, p.rec.AssertedBy)
		}
		for _, s := range p.rec.Supersedes {
			checkValue(p.path, s, true)
		}
		for _, n := range p.rec.Graph {
			if id := n.ID(); checkValue(p.path, id, true) {
				queueContentCheck(p.path, id)
			}
			for key, v := range n {
				if key == "@id" || key == "@type" {
					continue
				}
				requireIRI := iriValuedNodeProperties[key]
				collectValueIdentifiers(v, func(id string) {
					if checkValue(p.path, id, requireIRI) {
						queueContentCheck(p.path, id)
					}
				})
			}
		}
		if p.rec.Assertion == AssertionAsserted &&
			strings.HasPrefix(p.rec.AssertedBy, SoftwareAgentURNPrefix) &&
			p.rec.Evidence == "" {
			report.add(Finding{
				Severity: SeverityWarning,
				Class:    ClassWarning,
				Code:     "missing-evidence",
				Path:     p.path,
				Message:  "asserted record by a software agent has no exo:evidence",
			})
		}
	}

	for _, r := range annexRefs {
		key := strings.TrimPrefix(r.id, AnnexURNPrefix)
		if backend, nonHashing := annexKeyBackend(key); nonHashing {
			report.add(Finding{
				Severity: SeverityWarning,
				Class:    ClassWarning,
				Code:     "non-hashing-backend",
				Path:     r.path,
				Message:  fmt.Sprintf("%s uses the non-content-addressed %s backend", r.id, backend),
			})
		}
	}

	if len(blobTreeRefs) > 0 {
		exists := batchGitObjectsExist(reader, uniqueTrimmedIDs(blobTreeRefs, GitBlobURNPrefix, GitTreeURNPrefix))
		for _, r := range blobTreeRefs {
			oid := strings.TrimPrefix(strings.TrimPrefix(r.id, GitBlobURNPrefix), GitTreeURNPrefix)
			if !exists[oid] {
				report.add(Finding{
					Severity: SeverityWarning,
					Class:    ClassWarning,
					Code:     "missing-content",
					Path:     r.path,
					Message:  fmt.Sprintf("%s is not present in the repository", r.id),
				})
			}
		}
	}

	if len(annexRefs) > 0 && annexAvailable(reader) {
		known := batchAnnexKeysKnown(reader, uniqueTrimmedIDs(annexRefs, AnnexURNPrefix))
		for _, r := range annexRefs {
			key := strings.TrimPrefix(r.id, AnnexURNPrefix)
			if !known[key] {
				report.add(Finding{
					Severity: SeverityWarning,
					Class:    ClassWarning,
					Code:     "missing-content",
					Path:     r.path,
					Message:  fmt.Sprintf("%s is not known to this repository's git-annex", r.id),
				})
			}
		}
	}
}

// uniqueTrimmedIDs strips any of prefixes from each ref's id and returns
// the distinct results, in first-seen order.
func uniqueTrimmedIDs(refs []identifierRef, prefixes ...string) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range refs {
		id := r.id
		for _, prefix := range prefixes {
			id = strings.TrimPrefix(id, prefix)
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// collectIdentifiers returns every identifier a record's @graph references,
// as a node @id or as a property value (bare string, {"@id": ...} object,
// or an array of either), deduplicated in first-seen order.
func collectIdentifiers(rec *Record) []string {
	var ids []string
	seen := map[string]bool{}
	add := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			ids = append(ids, s)
		}
	}
	for _, n := range rec.Graph {
		add(n.ID())
		for k, v := range n {
			if k == "@id" || k == "@type" {
				continue
			}
			collectValueIdentifiers(v, add)
		}
	}
	return ids
}

func collectValueIdentifiers(v any, add func(string)) {
	switch val := v.(type) {
	case string:
		add(val)
	case []any:
		for _, item := range val {
			collectValueIdentifiers(item, add)
		}
	case map[string]any:
		if id, ok := val["@id"].(string); ok {
			add(id)
		}
	}
}

// annexKeyBackend returns a git-annex key's backend name and whether that
// backend is non-content-addressed (design D5: "Annex keys from
// non-hashing backends (WORM, URL) are not stable content identifiers").
// A key is "<backend>-s<size>[-...]--<name>"; the backend name never
// contains "-", so it is the field before the first one.
func annexKeyBackend(key string) (backend string, nonHashing bool) {
	if idx := strings.Index(key, "--"); idx < 0 {
		return "", false
	}
	backend, _, _ = strings.Cut(key, "-")
	switch backend {
	case "WORM", "URL":
		return backend, true
	}
	return backend, false
}

// annexAvailable reports whether reader's repository has git-annex
// initialized (a repo-local "annex.uuid" git config value), so
// missing-content checks on annex: identifiers can be skipped (rather than
// producing false positives from an uninitialized annex) when it does not.
// Checking git config directly, rather than running `git annex version`,
// avoids a false positive when the git-annex binary is installed but this
// particular repository was never `git annex init`-ed (review: "whereis
// then fails and gives false missing-content warnings").
func annexAvailable(reader *Reader) bool {
	out, err := reader.git("config", "annex.uuid")
	return err == nil && strings.TrimSpace(out) != ""
}

// batchGitObjectsExist reports, for each oid, whether it exists in the
// repository's object database, using a single `git cat-file --batch-check`
// process rather than one `git cat-file -e` per identifier (review:
// "Performance: ... batch the calls").
func batchGitObjectsExist(reader *Reader, oids []string) map[string]bool {
	exists := make(map[string]bool, len(oids))
	if len(oids) == 0 {
		return exists
	}
	out := reader.gitStdinBytes(strings.Join(oids, "\n")+"\n", "cat-file", "--batch-check")
	for _, line := range splitLines(string(out)) {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		exists[fields[0]] = len(fields) >= 2 && fields[1] != "missing"
	}
	return exists
}

// annexWhereisResult is the subset of `git annex whereis --json` fields
// needed to tell whether a key is known to this repository at all,
// regardless of whether its content is currently present locally.
type annexWhereisResult struct {
	Key     string `json:"key"`
	Whereis []any  `json:"whereis"`
}

// batchAnnexKeysKnown reports, for each key, whether git-annex has any
// record of it in this repository (design D7 "referenced repository
// content not present in the history"), using a single `git annex whereis
// --batch-keys` process rather than one `git annex whereis --key` per
// identifier (review: "Performance: ... batch the calls"). `whereis` exits
// non-zero when any key has zero copies; that is expected and not a
// failure of the batch call, so only the parsed JSON lines matter.
func batchAnnexKeysKnown(reader *Reader, keys []string) map[string]bool {
	known := make(map[string]bool, len(keys))
	if len(keys) == 0 {
		return known
	}
	out := reader.gitStdinBytes(strings.Join(keys, "\n")+"\n", "annex", "whereis", "--batch-keys", "--json")
	for _, line := range splitLines(string(out)) {
		var resp annexWhereisResult
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			continue
		}
		known[resp.Key] = len(resp.Whereis) > 0
	}
	return known
}

// checkImmutability implements D3/D7: a committed record modified or
// deleted in a later commit is an error naming the file and the commit
// (checkCommittedImmutability), and, for the worktree and staged checked
// states, so is an uncommitted change to a record already committed at
// HEAD (checkUncommittedImmutability) — the pre-commit hook (#120) and
// `exo provenance validate --staged` rely on catching this before the
// commit lands, not only after (review: "Modifying a committed record is
// not caught before commit").
func checkImmutability(reader *Reader, report *Report) error {
	flagged := map[string]bool{}
	if err := checkCommittedImmutability(reader, report, flagged); err != nil {
		return err
	}
	if reader.mode != ModeRef {
		if err := checkUncommittedImmutability(reader, report, flagged); err != nil {
			return err
		}
	}
	return nil
}

// checkCommittedImmutability walks reader's commit history for records
// added and later modified or deleted. For ModeRef it considers only
// commits reachable from the ref; for ModeWorktree and ModeStaged it
// considers all of HEAD's history, since the rule concerns records already
// committed, independent of the worktree or index. --no-renames stops a
// moved or renamed record file from being hidden as an "R" status a
// --diff-filter=AMD would not match: moving a record file must count as
// delete+add, so it is flagged like any other post-commit change (review:
// "Add --no-renames").
func checkCommittedImmutability(reader *Reader, report *Report, flagged map[string]bool) error {
	rev := "HEAD"
	if reader.mode == ModeRef {
		rev = reader.ref
	}
	out, err := reader.git("log", "--reverse", "--no-renames", "--name-status", "--diff-filter=AMD", "--pretty=format:>>>%H", rev, "--", RecordDir)
	if err != nil {
		if looksLikeNoCommits(err) {
			return nil
		}
		return fmt.Errorf("read provenance record history: %w", err)
	}

	type status struct {
		code, commit string
	}
	firstStatus := map[string]status{}
	commit := ""
	for _, line := range splitLines(out) {
		if strings.HasPrefix(line, ">>>") {
			commit = strings.TrimPrefix(line, ">>>")
			continue
		}
		if line == "" || commit == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 2)
		if len(fields) != 2 {
			continue
		}
		code, path := fields[0], fields[1]
		if !strings.HasSuffix(path, ".jsonld") {
			continue
		}
		prev, seen := firstStatus[path]
		if !seen {
			firstStatus[path] = status{code: code, commit: commit}
			continue
		}
		if prev.code == "A" && (code == "M" || code == "D") && !flagged[path] {
			flagged[path] = true
			verb := "modified"
			if code == "D" {
				verb = "deleted"
			}
			report.add(Finding{
				Severity: SeverityError,
				Class:    ClassImmutability,
				Code:     "modified-record",
				Path:     path,
				Message:  fmt.Sprintf("record was %s in commit %s after being committed", verb, commit),
			})
		}
	}
	return nil
}

// checkUncommittedImmutability catches a record committed at HEAD that has
// since been modified or deleted in the index (ModeStaged) or the worktree
// (ModeWorktree) without a new commit — the case
// checkCommittedImmutability's history walk cannot see, since nothing has
// been committed yet (review: "`exo provenance validate --staged` gave `1
// record(s) valid`, exit 0" after editing and `git add`-ing a committed
// record). --no-renames keeps a moved record from being hidden as a
// rename, for the same reason as in checkCommittedImmutability. paths
// already flagged by checkCommittedImmutability (modified in a past
// commit) are not reported again here.
func checkUncommittedImmutability(reader *Reader, report *Report, flagged map[string]bool) error {
	var args []string
	switch reader.mode {
	case ModeStaged:
		args = []string{"diff", "--cached", "--no-renames", "--name-status", "HEAD", "--", RecordDir}
	case ModeWorktree:
		args = []string{"diff", "--no-renames", "--name-status", "HEAD", "--", RecordDir}
	default:
		return nil
	}
	out, err := reader.git(args...)
	if err != nil {
		if looksLikeNoCommits(err) {
			return nil
		}
		return fmt.Errorf("diff provenance records against HEAD: %w", err)
	}
	for _, line := range splitLines(out) {
		fields := strings.SplitN(line, "\t", 2)
		if len(fields) != 2 {
			continue
		}
		code, path := fields[0], fields[1]
		if !strings.HasSuffix(path, ".jsonld") || flagged[path] {
			continue
		}
		if code == "M" || code == "D" {
			flagged[path] = true
			verb := "modified"
			if code == "D" {
				verb = "deleted"
			}
			report.add(Finding{
				Severity: SeverityError,
				Class:    ClassImmutability,
				Code:     "modified-record",
				Path:     path,
				Message:  fmt.Sprintf("record was %s since being committed, without a new commit", verb),
			})
		}
	}
	return nil
}

// looksLikeNoCommits reports whether err from a git command scoped to a
// revision (`git log <rev>`, `git diff <rev>`) means that revision has no
// history yet (a fresh repository), as opposed to a real failure.
func looksLikeNoCommits(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "unknown revision or path not in the working tree") ||
		strings.Contains(msg, "ambiguous argument") ||
		strings.Contains(msg, "bad revision") ||
		strings.Contains(msg, "not a git repository") ||
		strings.Contains(msg, "Could not access")
}
