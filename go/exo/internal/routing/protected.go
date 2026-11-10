// Package routing holds `exo add` routing primitives shared with packages
// that cannot depend on commands/add itself (internal/provenance also
// depends on commands/add transitively through commands/add's own use of
// internal/provenance, via exo add exohub:...'s provenance recording).
package routing

import (
	"fmt"
	"path/filepath"

	"github.com/Genentech/exohub/go/exo/internal/pathglob"
)

// ProtectedPatterns are evaluated before any user annex.routing rule and
// always route to git; user rules cannot override them (design D3 of `exo
// add`'s routing).
var ProtectedPatterns = []string{".exohub/**", ".gitignore", ".gitattributes"}

// IsProtectedPath reports whether relPath matches one of the built-in
// protected patterns. Protected paths always route to git and are never
// moved by `exo add --reroute`, regardless of their current backend.
func IsProtectedPath(relPath string) (bool, error) {
	relPath = filepath.ToSlash(relPath)
	for _, pattern := range ProtectedPatterns {
		ok, err := pathglob.Match(pattern, relPath)
		if err != nil {
			return false, fmt.Errorf("protected pattern %q: %w", pattern, err)
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}
