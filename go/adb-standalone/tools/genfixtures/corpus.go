// Package main implements genfixtures, a deterministic synthetic corpus
// generator for the adb-standalone parity test fixtures.
//
// It invents a small, fully synthetic document set (no captured data) that
// exercises the exohub-bundle, exohub-commit and exohub-artifact document
// shapes, then drives the standalone server's own seed + search pipeline to
// produce the golden search fixtures. Run via `make genfixtures` from
// go/adb-standalone.
package main

import (
	"fmt"
	"sort"
)

// project describes one synthetic project with its own version history.
type project struct {
	id       string
	repoHost string
	repoOrg  string
	paths    []string // artifact file paths relative to bundle root, shared across versions
}

// corpusProjects defines the synthetic projects. Invented names only.
var corpusProjects = []project{
	{
		id:       "sample-app",
		repoHost: "github.com",
		repoOrg:  "example-org",
		paths: []string{
			"packages/widget-core/index.js",
			"packages/widget-core/utils.js",
			"packages/widget-ui/button.js",
			"assets/icons/logo.svg",
			"dist/sample-app-bundle.tar.gz",
		},
	},
	{
		id:       "sample-dataset",
		repoHost: "github.com",
		repoOrg:  "example-org",
		paths: []string{
			"records/batch-a/shard-1.table",
			"records/batch-a/shard-2.table",
			"records/batch-b/index.csv",
			"records/batch-b/labels.csv",
		},
	},
}

// versionsPerProject is the number of synthetic versions generated per project.
const versionsPerProject = 6

// synthVersions returns versionsPerProject semver strings in ascending order,
// deterministic given the project index. Each project gets its own major
// version so fixtures referencing a specific version stay unambiguous.
func synthVersions(projectIdx int) []string {
	out := make([]string, 0, versionsPerProject)
	major := 2 + projectIdx
	for i := 0; i < versionsPerProject; i++ {
		out = append(out, fmt.Sprintf("v%d.0.%d", major, i))
	}
	sort.Strings(out)
	return out
}

// synthCommitSubjects is a fixed pool of invented, generic commit subjects —
// no project-specific or real-world content.
var synthCommitSubjects = []string{
	"feat: add initial scaffolding",
	"fix: correct off-by-one in pagination",
	"chore: bump dependency versions",
	"refactor: extract shared helper function",
	"docs: update usage examples",
	"test: add coverage for edge cases",
	"fix: handle empty input gracefully",
	"feat: support additional output format",
	"perf: reduce allocations in hot path",
	"chore: tidy up project layout",
}

// synthOwners are the invented owner/author identities used across the corpus.
var synthOwners = []string{"example-user"}

const (
	synthAuthorName  = "Example Author"
	synthAuthorEmail = "author@example.com"
	synthBucket      = "example-bucket"
	synthRemoteType  = "s3-annex"
)
