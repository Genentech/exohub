// Package features provides a compile-time registry of build tags active in
// the current binary. Each supported tag has a companion build-constrained
// file that calls register() in its init function.
package features

import "sort"

var registered []string

func register(tag string) {
	registered = append(registered, tag)
}

// Enabled returns a sorted, deduplicated list of feature tags compiled into
// the current binary. Returns nil when no tags are active (OSS/generic build).
func Enabled() []string {
	if len(registered) == 0 {
		return nil
	}
	out := make([]string, len(registered))
	copy(out, registered)
	sort.Strings(out)
	// dedup
	n := 0
	for i, v := range out {
		if i == 0 || v != out[i-1] {
			out[n] = v
			n++
		}
	}
	return out[:n]
}
