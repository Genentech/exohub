package provenance

import (
	"strings"
	"testing"
	"time"
)

func TestRecordFileName(t *testing.T) {
	ts := time.Date(2026, 10, 14, 10, 12, 0, 0, time.UTC)
	id := "urn:exohub:record:01928c9e-7f3a-7c2d-9b1e-4a5f6b7c8d9e"
	name, err := RecordFileName(ts, "Normalize Counts!", id)
	if err != nil {
		t.Fatalf("RecordFileName() error = %v", err)
	}
	want := "20261014T101200Z-normalize-counts-7c8d9e.jsonld"
	if name != want {
		t.Errorf("RecordFileName() = %q, want %q", name, want)
	}
}

func TestRecordFileNameConvertsNonUTCToUTC(t *testing.T) {
	loc := time.FixedZone("PDT", -7*60*60)
	ts := time.Date(2026, 10, 14, 3, 12, 0, 0, loc) // 10:12 UTC
	id := "urn:exohub:record:01928c9e-7f3a-7c2d-9b1e-4a5f6b7c8d9e"
	name, err := RecordFileName(ts, "normalize", id)
	if err != nil {
		t.Fatalf("RecordFileName() error = %v", err)
	}
	if name != "20261014T101200Z-normalize-7c8d9e.jsonld" {
		t.Errorf("RecordFileName() = %q, want UTC-normalized timestamp", name)
	}
}

// TestRecordFileNameUsesLastHexCharsForUniqueness is a regression test for
// review of !260: a UUIDv7's first 48 bits are a millisecond timestamp, so
// two records generated in the same second with the same slug previously
// got identical short ids (the first 6 hex characters), colliding on file
// name. The short id must come from the UUID's random tail instead.
func TestRecordFileNameUsesLastHexCharsForUniqueness(t *testing.T) {
	ts := time.Date(2026, 10, 14, 10, 12, 0, 0, time.UTC)
	id1 := NewRecordID()
	id2 := NewRecordID()

	name1, err := RecordFileName(ts, "expr", id1)
	if err != nil {
		t.Fatalf("RecordFileName() error = %v", err)
	}
	name2, err := RecordFileName(ts, "expr", id2)
	if err != nil {
		t.Fatalf("RecordFileName() error = %v", err)
	}
	if name1 == name2 {
		t.Fatalf("RecordFileName() collided for two different record ids generated in the same second: %q", name1)
	}

	u1, _ := RecordUUID(id1)
	hex1 := strings.ReplaceAll(u1.String(), "-", "")
	if !strings.HasSuffix(name1, hex1[len(hex1)-shortIDLen:]+".jsonld") {
		t.Errorf("RecordFileName() = %q, want it to end with the UUID's last %d hex characters", name1, shortIDLen)
	}
}

func TestRecordFileNameRejectsInvalidID(t *testing.T) {
	if _, err := RecordFileName(time.Now(), "slug", "not-a-record-id"); err == nil {
		t.Errorf("RecordFileName() expected error for invalid record id")
	}
}

func TestRecordFileNameRejectsEmptySlug(t *testing.T) {
	id := "urn:exohub:record:01928c9e-7f3a-7c2d-9b1e-4a5f6b7c8d9e"
	if _, err := RecordFileName(time.Now(), "!!!", id); err == nil {
		t.Errorf("RecordFileName() expected error for slug with no valid characters")
	}
}

func TestSlugify(t *testing.T) {
	tests := map[string]string{
		"Normalize Counts!":    "normalize-counts",
		"  leading/trailing  ": "leading-trailing",
		"already-slug":         "already-slug",
		"":                     "",
	}
	for in, want := range tests {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRecordPath(t *testing.T) {
	ts := time.Date(2026, 10, 14, 10, 12, 0, 0, time.UTC)
	id := "urn:exohub:record:01928c9e-7f3a-7c2d-9b1e-4a5f6b7c8d9e"
	path, err := RecordPath(ts, "normalize", id)
	if err != nil {
		t.Fatalf("RecordPath() error = %v", err)
	}
	want := ".exohub/provenance/20261014T101200Z-normalize-7c8d9e.jsonld"
	if path != want {
		t.Errorf("RecordPath() = %q, want %q", path, want)
	}
}
