//go:build !internal

package whoami

import "testing"

// TestExtraWhoamiFieldsNilByDefault verifies the OSS build never wires the
// internal-only extraWhoamiFields hook (used by internal-only builds to add
// an extra identity field — see command_internal_test.go for the
// internal-build counterpart proving the field DOES appear there).
func TestExtraWhoamiFieldsNilByDefault(t *testing.T) {
	if extraWhoamiFields != nil {
		t.Error("extraWhoamiFields must be nil in the default (non-internal) build")
	}
}
