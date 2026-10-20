package doctor

import (
	"fmt"

	initcmd "github.com/Genentech/exohub/go/exo/commands/init"
	lib "github.com/Genentech/exohub/go/exo/internal/provenance"
)

const groupProvenance = "Provenance"

// runProvenanceChecks runs the exo doctor provenance check
// (openspec/changes/add-provenance specs/exo-doctor/spec.md "Provenance
// checks"): it runs the same validation as `exo provenance validate` on the
// worktree when records or provenance.require rules exist, mapping errors
// to FAIL and warnings to WARN; otherwise it is SKIP.
func runProvenanceChecks(repoDir string) []CheckResult {
	id := "repo.provenance"

	cfg, err := initcmd.LoadExohubConfigAt(repoDir)
	if err != nil {
		return []CheckResult{fail(id, groupProvenance, "Cannot read .exohub/config", err.Error(), "Fix YAML syntax in .exohub/config")}
	}
	hasRequireRules := cfg != nil && cfg.Provenance != nil && len(cfg.Provenance.Require) > 0

	reader := lib.NewWorktreeReader(repoDir)
	report, err := lib.Validate(reader)
	if err != nil {
		return []CheckResult{fail(id, groupProvenance, "Could not run provenance validation", err.Error(), "Run 'exo provenance validate' for details")}
	}

	// A repo with no current records and no provenance.require rules is
	// SKIP only if there is nothing else to report either: if every record
	// was deleted, RecordCount is 0 but checkImmutability may still have
	// flagged the deletion, and that FAIL must not be hidden (review: "SKIP
	// should require 'no records and no rules and no findings'. Otherwise,
	// a repo whose records were all deleted in history SKIPs and hides the
	// immutability error").
	if report.RecordCount == 0 && !hasRequireRules && len(report.Findings) == 0 {
		return []CheckResult{skip(id, groupProvenance, "No provenance records or provenance.require rules")}
	}

	var results []CheckResult
	for _, f := range report.Findings {
		switch f.Severity {
		case lib.SeverityError:
			results = append(results, fail(id+"."+f.Code, groupProvenance,
				fmt.Sprintf("%s: %s", f.Path, f.Message), f.Detail,
				"Run 'exo provenance validate' for details"))
		case lib.SeverityWarning:
			results = append(results, warn(id+"."+f.Code, groupProvenance,
				fmt.Sprintf("%s: %s", f.Path, f.Message), f.Detail,
				"Run 'exo provenance validate' for details"))
		}
	}
	if len(results) == 0 {
		results = append(results, pass(id, groupProvenance, fmt.Sprintf("%d provenance record(s) OK", report.RecordCount)))
	}
	if report.Coverage != nil {
		results = append(results, pass(id+".coverage", groupProvenance,
			fmt.Sprintf("Coverage: %d covered, %d uncovered", report.Coverage.Covered, report.Coverage.Uncovered)))
	}
	return results
}
