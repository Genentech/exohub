package provenance

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
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

func TestCollectionIDIsDeterministicAndOrderIndependent(t *testing.T) {
	members := []string{
		AnnexEntityID("SHA256E-s2048--ab12.bam"),
		AnnexEntityID("SHA256E-s4096--cd34.bam"),
	}
	reversed := []string{members[1], members[0]}

	id := CollectionID(members)
	if got := CollectionID(reversed); got != id {
		t.Errorf("CollectionID(reversed) = %q, want %q (order-independent)", got, id)
	}
	if !strings.HasPrefix(id, CollectionURNPrefix) {
		t.Errorf("CollectionID() = %q, want prefix %q", id, CollectionURNPrefix)
	}
	hexDigest := strings.TrimPrefix(id, CollectionURNPrefix)
	if len(hexDigest) != 64 {
		t.Errorf("CollectionID() digest = %q, want 64 lowercase hex characters", hexDigest)
	}

	// Design D1: the digest is the SHA-256 of the sorted, newline-joined
	// member ids, as hex.
	sorted := []string{members[0], members[1]}
	sort.Strings(sorted)
	sum := sha256.Sum256([]byte(strings.Join(sorted, "\n")))
	want := CollectionURNPrefix + hex.EncodeToString(sum[:])
	if id != want {
		t.Errorf("CollectionID() = %q, want %q", id, want)
	}
}

func TestCollectionIDChangesWithMembership(t *testing.T) {
	members := []string{AnnexEntityID("SHA256E-s2048--ab12.bam")}
	id := CollectionID(members)

	changed := CollectionID(append(members, AnnexEntityID("SHA256E-s4096--cd34.bam")))
	if changed == id {
		t.Errorf("CollectionID() did not change when membership changed")
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
