package provenance

import (
	"strings"
	"testing"
)

func TestIdentifierFormatting(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{"annex", AnnexEntityID("SHA256E-s2048--ab12.csv"), "urn:exohub:annex:SHA256E-s2048--ab12.csv"},
		{"git blob", GitBlobEntityID("9e1f4c"), "urn:exohub:git:blob:9e1f4c"},
		{"git tree", GitTreeEntityID("deadbeef"), "urn:exohub:git:tree:deadbeef"},
		{"git commit", GitCommitID("c0ffee"), "urn:exohub:git:commit:c0ffee"},
		{"person", PersonAgentID("lelongs"), "urn:exohub:agent:person:lelongs"},
		{"software", SoftwareAgentID("claude-code"), "urn:exohub:agent:software:claude-code"},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s = %q, want %q", tt.name, tt.got, tt.want)
		}
	}
}

func TestNewRecordAndActivityIDsAreUniqueUUIDv7(t *testing.T) {
	r1, r2 := NewRecordID(), NewRecordID()
	if r1 == r2 {
		t.Fatalf("NewRecordID() returned the same id twice: %s", r1)
	}
	if !strings.HasPrefix(r1, RecordURNPrefix) {
		t.Errorf("NewRecordID() = %q, want prefix %q", r1, RecordURNPrefix)
	}
	if _, ok := RecordUUID(r1); !ok {
		t.Errorf("RecordUUID(%q) failed to parse", r1)
	}

	a1 := NewActivityID()
	if !strings.HasPrefix(a1, ActivityURNPrefix) {
		t.Errorf("NewActivityID() = %q, want prefix %q", a1, ActivityURNPrefix)
	}
}

func TestRecordUUIDRejectsMalformedIDs(t *testing.T) {
	for _, id := range []string{"", "urn:exohub:record:not-a-uuid", "urn:exohub:activity:01928c9e-7f3a-7c2d-9b1e-4a5f6b7c8d9e"} {
		if _, ok := RecordUUID(id); ok {
			t.Errorf("RecordUUID(%q) unexpectedly succeeded", id)
		}
	}
}

func TestIsUUIDv7(t *testing.T) {
	if !IsUUIDv7(NewRecordID()) {
		t.Errorf("IsUUIDv7(NewRecordID()) = false, want true")
	}
	// A well-formed but non-v7 (v4) UUID must be rejected.
	if IsUUIDv7("urn:exohub:record:01928c9e-7f3a-4c2d-9b1e-4a5f6b7c8d9e") {
		t.Errorf("IsUUIDv7() accepted a v4 UUID")
	}
	for _, id := range []string{"", "not-a-record-id", "urn:exohub:record:not-a-uuid"} {
		if IsUUIDv7(id) {
			t.Errorf("IsUUIDv7(%q) = true, want false", id)
		}
	}
}
