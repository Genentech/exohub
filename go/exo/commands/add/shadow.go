package add

import (
	"strings"

	"github.com/dustin/go-humanize"

	initcmd "github.com/Genentech/exohub/go/exo/commands/init"
)

// DetectShadowedRules returns the 1-based indices (within rules) of every
// rule that can never match because an earlier rule's conditions are a
// superset of its own (design D9). The analysis is conservative: when it
// cannot prove that every file matching a later rule would already have
// matched an earlier one, it does not report a shadow. Callers should only
// call this on rules that have already passed RoutingRule.Validate.
func DetectShadowedRules(rules []initcmd.RoutingRule) []int {
	var shadowed []int
	for j := 1; j < len(rules); j++ {
		for i := 0; i < j; i++ {
			if ruleSubsumes(rules[i], rules[j]) {
				shadowed = append(shadowed, j+1)
				break
			}
		}
	}
	return shadowed
}

// ruleSubsumes reports whether every file matching b would already have
// matched a, based on a key-by-key comparison of path, larger_than,
// smaller_than and content. It errs on the side of false, never true,
// when a key can't be compared.
func ruleSubsumes(a, b initcmd.RoutingRule) bool {
	if a.Content != "" && (b.Content == "" || b.Content != a.Content) {
		return false
	}
	if a.LargerThan != "" {
		aMin, err := humanize.ParseBytes(a.LargerThan)
		if err != nil {
			return false
		}
		var bMin uint64
		if b.LargerThan != "" {
			v, err := humanize.ParseBytes(b.LargerThan)
			if err != nil {
				return false
			}
			bMin = v
		}
		if bMin < aMin {
			return false
		}
	}
	if a.SmallerThan != "" {
		if b.SmallerThan == "" {
			return false
		}
		aMax, err := humanize.ParseBytes(a.SmallerThan)
		if err != nil {
			return false
		}
		bMax, err := humanize.ParseBytes(b.SmallerThan)
		if err != nil {
			return false
		}
		if bMax > aMax {
			return false
		}
	}
	if a.Path != "" && (b.Path == "" || !pathImplies(a.Path, b.Path)) {
		return false
	}
	return true
}

// pathImplies reports whether every path matching glob pattern b is
// guaranteed to also match pattern a, for the practical cases design D9's
// shadowing warning is meant to catch: an identical pattern, or an
// any-depth pattern (no "/", or an explicit "**/" prefix) whose final
// segment glob matches a more specific, directory-qualified pattern's
// final segment glob.
func pathImplies(a, b string) bool {
	aGlob, aAnyDepth, aOk := basenameGlob(a)
	bGlob, bAnyDepth, bOk := basenameGlob(b)
	if !aOk || !bOk || aGlob != bGlob {
		return false
	}
	if aAnyDepth {
		return true
	}
	return !bAnyDepth && a == b
}

// basenameGlob decomposes a routing path pattern into the glob matched
// against the final path segment, and whether that glob applies at any
// depth: a pattern with no "/" (design D2's basename semantics) or an
// explicit "**/" prefix followed by nothing else to match. ok is false for
// patterns this simple decomposition can't reason about (e.g. "**" or a
// wildcard in an intermediate directory segment), so pathImplies never
// over-claims subsumption for them.
func basenameGlob(pattern string) (glob string, anyDepth, ok bool) {
	if !strings.Contains(pattern, "/") {
		return pattern, true, true
	}
	if strings.HasPrefix(pattern, "**/") {
		rest := pattern[len("**/"):]
		if rest != "" && !strings.Contains(rest, "/") && !strings.Contains(rest, "**") {
			return rest, true, true
		}
		return "", false, false
	}
	idx := strings.LastIndex(pattern, "/")
	dir, glob := pattern[:idx], pattern[idx+1:]
	if strings.ContainsAny(dir, "*?[") {
		return "", false, false
	}
	return glob, false, true
}
