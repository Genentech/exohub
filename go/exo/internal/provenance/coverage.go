package provenance

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Genentech/exohub/go/exo/commands/add"
	"github.com/Genentech/exohub/go/exo/internal/pathglob"
)

// coveringProperties lists the node properties whose presence on a node
// covers it (design D14): generation, derivation, attribution and
// revision statements. Being only "used" as an input does not cover a
// node.
var coveringProperties = []string{
	"wasGeneratedBy", "wasDerivedFrom", "wasAttributedTo", "wasRevisionOf",
}

// CoverageSummary is the result of evaluating coverage at a Reader's
// checked state (design D14): how many tracked files, excluding protected
// paths, are covered versus uncovered.
type CoverageSummary struct {
	Covered   int `json:"covered"`
	Uncovered int `json:"uncovered"`
}

// supersededIDs returns the set of record ids any parsed record declares
// exo:supersedes, independent of whether those ids are known (design D7).
func supersededIDs(parsed []parsedRecord) map[string]bool {
	superseded := map[string]bool{}
	for _, p := range parsed {
		for _, s := range p.rec.Supersedes {
			superseded[s] = true
		}
	}
	return superseded
}

// coveredEntityIDs returns the set of node ids covered by a non-superseded
// record (design D14): any node with a wasGeneratedBy, wasDerivedFrom,
// wasAttributedTo or wasRevisionOf property.
func coveredEntityIDs(parsed []parsedRecord, superseded map[string]bool) map[string]bool {
	covered := map[string]bool{}
	for _, p := range parsed {
		if superseded[p.rec.ID] {
			continue
		}
		for _, n := range p.rec.Graph {
			for _, prop := range coveringProperties {
				if _, ok := n[prop]; ok {
					covered[n.ID()] = true
					break
				}
			}
		}
	}
	return covered
}

// trackedFileEntries returns every tracked file's git entry at reader's
// checked state, excluding protected paths (design D15 "Coverage
// universe": .exohub/**, .gitignore, .gitattributes, the same list as
// add.IsProtectedPath). It resolves the whole file set in a single git
// invocation via Reader.FileEntries (review: "coverage spawns several git
// processes per tracked file").
func trackedFileEntries(reader *Reader) (map[string]FileEntry, error) {
	all, err := reader.FileEntries()
	if err != nil {
		return nil, err
	}
	entries := make(map[string]FileEntry, len(all))
	for path, e := range all {
		protected, err := add.IsProtectedPath(path)
		if err != nil {
			return nil, fmt.Errorf("check protected path %s: %w", path, err)
		}
		if !protected {
			entries[path] = e
		}
	}
	return entries, nil
}

// pathAncestors returns path's ancestor directories, nearest first (for
// example "a/b/c" -> ["a/b", "a"]).
func pathAncestors(path string) []string {
	var ancestors []string
	dir := filepath.Dir(filepath.ToSlash(path))
	for dir != "." && dir != "/" && dir != "" {
		ancestors = append(ancestors, dir)
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ancestors
}

// findDeclaredAncestor returns the nearest ancestor directory of path that
// matches a provenance.collections pattern (design "Declared collection
// folders"), or "" if none does.
func findDeclaredAncestor(path string, patterns []string) (string, error) {
	for _, dir := range pathAncestors(path) {
		for _, pattern := range patterns {
			ok, err := pathglob.MatchDir(pattern, dir)
			if err != nil {
				return "", fmt.Errorf("provenance.collections pattern %q: %w", pattern, err)
			}
			if ok {
				return dir, nil
			}
		}
	}
	return "", nil
}

// evaluateCoverage computes coverage (design D14) for every tracked,
// non-protected file at reader's checked state: a declared collection
// folder (design "Declared collection folders") covers its members
// exclusively through the folder; otherwise a file is covered by its own
// content id or by any enclosing folder whose tree id is covered. It
// returns the covered/uncovered counts and the uncovered file paths, for
// provenance.require enforcement.
//
// Every file's content id and every directory's tree id are resolved
// up front, in a constant number of git invocations regardless of how many
// files or directories the repository has (Reader.AllContentIDs,
// Reader.AllTreeIDs), rather than one or more git processes per file
// (review: "coverage spawns several git processes per tracked file").
func evaluateCoverage(reader *Reader, parsed []parsedRecord, superseded map[string]bool) (*CoverageSummary, []string, error) {
	entries, err := trackedFileEntries(reader)
	if err != nil {
		return nil, nil, err
	}
	return evaluateCoverageForEntries(reader, entries, parsed, superseded)
}

// evaluateCoverageForEntries is evaluateCoverage restricted to entries
// instead of every tracked, non-protected file (design D15 "Bundle
// scope"): RequireViolations reuses this to enforce provenance.require on
// only a bundle's selected file set, without resolving content ids one
// file at a time (Reader.AllContentIDs, Reader.AllTreeIDs).
func evaluateCoverageForEntries(reader *Reader, entries map[string]FileEntry, parsed []parsedRecord, superseded map[string]bool) (*CoverageSummary, []string, error) {
	cfg, err := reader.LoadConfig()
	if err != nil {
		return nil, nil, fmt.Errorf("load .exohub/config: %w", err)
	}
	var declared []string
	if cfg != nil && cfg.Provenance != nil {
		declared = cfg.Provenance.Collections
	}

	covered := coveredEntityIDs(parsed, superseded)
	hasTreeCoverage := false
	for id := range covered {
		if strings.HasPrefix(id, GitTreeURNPrefix) {
			hasTreeCoverage = true
			break
		}
	}

	contentIDs, err := reader.AllContentIDs(entries)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve content ids: %w", err)
	}
	var treeIDs map[string]string
	if len(declared) > 0 || hasTreeCoverage {
		treeIDs, err = reader.AllTreeIDs()
		if err != nil {
			return nil, nil, fmt.Errorf("resolve folder tree ids: %w", err)
		}
	}

	files := make([]string, 0, len(entries))
	for path := range entries {
		files = append(files, path)
	}
	sort.Strings(files)

	summary := &CoverageSummary{}
	var uncovered []string
	for _, path := range files {
		isCovered, err := fileIsCovered(path, declared, covered, hasTreeCoverage, contentIDs, treeIDs)
		if err != nil {
			return nil, nil, err
		}
		if isCovered {
			summary.Covered++
		} else {
			summary.Uncovered++
			uncovered = append(uncovered, path)
		}
	}
	return summary, uncovered, nil
}

func fileIsCovered(path string, declared []string, covered map[string]bool, hasTreeCoverage bool, contentIDs, treeIDs map[string]string) (bool, error) {
	if len(declared) > 0 {
		dir, err := findDeclaredAncestor(path, declared)
		if err != nil {
			return false, err
		}
		if dir != "" {
			id, ok := treeIDs[dir]
			return ok && covered[id], nil
		}
	}

	if id, ok := contentIDs[path]; ok && covered[id] {
		return true, nil
	}

	if hasTreeCoverage {
		for _, dir := range pathAncestors(path) {
			if id, ok := treeIDs[dir]; ok && covered[id] {
				return true, nil
			}
		}
	}
	return false, nil
}

// evaluateRequire reports the uncovered files, among uncoveredPaths, that
// match a provenance.require pattern (design D14, D15). For ModeStaged it
// restricts the check to staged added or modified files, so an older
// uncovered file does not block an unrelated commit.
func evaluateRequire(reader *Reader, uncoveredPaths, requireGlobs []string) ([]string, error) {
	if len(requireGlobs) == 0 {
		return nil, nil
	}

	candidates := uncoveredPaths
	if reader.mode == ModeStaged {
		staged, err := stagedAddedOrModified(reader)
		if err != nil {
			return nil, err
		}
		stagedSet := make(map[string]bool, len(staged))
		for _, p := range staged {
			stagedSet[p] = true
		}
		candidates = nil
		for _, p := range uncoveredPaths {
			if stagedSet[p] {
				candidates = append(candidates, p)
			}
		}
	}

	var violations []string
	for _, path := range candidates {
		for _, pattern := range requireGlobs {
			ok, err := pathglob.Match(pattern, path)
			if err != nil {
				return nil, fmt.Errorf("provenance.require pattern %q: %w", pattern, err)
			}
			if ok {
				violations = append(violations, path)
				break
			}
		}
	}
	sort.Strings(violations)
	return violations, nil
}

// RequireViolations reports the paths in selected that match a
// provenance.require pattern and are not covered by any non-superseded
// record, at reader's checked state (design D8 "Bundle enforces
// provenance.require", D15 "Bundle scope"): coverage (declared
// collections, folder collections, D14) is evaluated exactly like `exo
// provenance validate`, restricted to selected instead of every tracked
// file, since a bundle only needs to enforce its own selected artifacts.
// selected is repo-relative file paths (not directories).
func RequireViolations(reader *Reader, selected []string) ([]string, error) {
	cfg, err := reader.LoadConfig()
	if err != nil {
		return nil, fmt.Errorf("load .exohub/config: %w", err)
	}
	var requireGlobs []string
	if cfg != nil && cfg.Provenance != nil {
		requireGlobs = cfg.Provenance.Require
	}
	if len(requireGlobs) == 0 || len(selected) == 0 {
		return nil, nil
	}

	report := &Report{}
	parsed, err := parseRecordFiles(reader, report)
	if err != nil {
		return nil, err
	}
	superseded := supersededIDs(parsed)

	all, err := trackedFileEntries(reader)
	if err != nil {
		return nil, err
	}
	entries := make(map[string]FileEntry, len(selected))
	for _, path := range selected {
		if e, ok := all[path]; ok {
			entries[path] = e
		}
	}

	_, uncovered, err := evaluateCoverageForEntries(reader, entries, parsed, superseded)
	if err != nil {
		return nil, fmt.Errorf("evaluate coverage: %w", err)
	}

	return evaluateRequire(reader, uncovered, requireGlobs)
}

// stagedAddedOrModified returns the repo-relative paths staged as added or
// modified relative to HEAD (or, in a repository with no commits yet, all
// staged paths). --no-renames turns off git's rename detection, so moving
// an uncovered file into a provenance.require path is reported as an add
// (or a delete+add) rather than a rename that evaluateRequire would never
// see (review: "moving an uncovered file into results/** shows as R and
// escapes the staged require check"); -z/NUL-splitting handles paths
// containing unusual characters, which --name-only would otherwise quote.
func stagedAddedOrModified(reader *Reader) ([]string, error) {
	out, err := reader.gitBytes("diff", "--cached", "--no-renames", "--diff-filter=AM", "--name-only", "-z")
	if err != nil {
		return nil, fmt.Errorf("list staged added/modified files: %w", err)
	}
	return splitNUL(string(out)), nil
}
