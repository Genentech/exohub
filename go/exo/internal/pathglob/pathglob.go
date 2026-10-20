// Package pathglob implements the gitignore-style path matching shared by
// annex.routing, provenance.require and provenance.collections (design D9,
// D15 "Glob semantics"): a pattern containing "/" is matched against the
// full repo-relative path; a pattern with no "/" matches the basename at
// any depth.
package pathglob

import (
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// Match reports whether relPath matches pattern using the shared glob
// semantics. relPath is normalized to forward slashes before matching.
func Match(pattern, relPath string) (bool, error) {
	relPath = filepath.ToSlash(relPath)
	if strings.Contains(pattern, "/") {
		return doublestar.Match(pattern, relPath)
	}
	return doublestar.Match(pattern, filepath.Base(relPath))
}

// Valid reports whether pattern is a syntactically valid glob.
func Valid(pattern string) bool {
	return doublestar.ValidatePattern(pattern)
}

// MatchDir reports whether dirPath, a directory path without a trailing
// slash, matches pattern using the provenance.collections semantics
// (design D15 "Glob semantics"): pattern must end in "/" and is matched
// against dirPath with a trailing slash added, so "**/*.tdb/" matches the
// directory "data/expr.tdb" but not a file of the same name.
func MatchDir(pattern, dirPath string) (bool, error) {
	if !strings.HasSuffix(pattern, "/") {
		return false, nil
	}
	return Match(pattern, filepath.ToSlash(dirPath)+"/")
}
