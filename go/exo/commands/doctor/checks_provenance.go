package doctor

import (
	"fmt"
	"strings"

	initcmd "github.com/Genentech/exohub/go/exo/commands/init"
	lib "github.com/Genentech/exohub/go/exo/internal/provenance"
)

const groupProvenance = "Provenance"

// minCollectionsExoVersion is design D9's minimum exo version for a
// repository using named collections (openspec/changes/add-explicit-
// collections/design.md "D9. A minimum exo version for repositories using
// named collections"): the first exo release that recognizes the
// hadMember term and the collection:sha256: identifier kind.
const minCollectionsExoVersion = "v0.132.0"

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

	collectionNames, err := lib.ListCollectionNames(reader)
	if err != nil {
		// Outside a git repository there are no tracked files to list at
		// all (ListCollectionNames reads them via git); this is the same
		// benign "nothing to report" case lib.Validate's own git calls
		// already tolerate (looksLikeNoCommits), not a real failure.
		if !strings.Contains(err.Error(), "not a git repository") {
			return []CheckResult{fail(id, groupProvenance, "Could not list named collection manifests", err.Error(), "Run 'exo provenance validate' for details")}
		}
		collectionNames = nil
	}

	// A repo with no current records, no provenance.require rules and no
	// named collection manifests is SKIP only if there is nothing else to
	// report either: if every record was deleted, RecordCount is 0 but
	// checkImmutability may still have flagged the deletion, and that FAIL
	// must not be hidden (review: "SKIP should require 'no records and no
	// rules and no findings'. Otherwise, a repo whose records were all
	// deleted in history SKIPs and hides the immutability error"). A
	// repository with a named collection manifest but nothing else to
	// report still needs the minimum-version note below (design D9), so it
	// is never SKIP either.
	if report.RecordCount == 0 && !hasRequireRules && len(report.Findings) == 0 && len(collectionNames) == 0 {
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
	if len(collectionNames) > 0 {
		results = append(results, pass(id+".collections", groupProvenance,
			fmt.Sprintf("%d named collection manifest(s) found; requires exo %s or later to validate/bundle", len(collectionNames), minCollectionsExoVersion)))
	}
	return results
}
