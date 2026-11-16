package provenance

import (
	"strings"
	"testing"
)

func TestComputeMinVersion_NoNewTerms(t *testing.T) {
	graph := []Node{
		{"@id": "urn:exohub:git:blob:abc", "@type": "Entity", "wasGeneratedBy": "urn:exohub:activity:abc"},
		{"@id": "urn:exohub:activity:abc", "@type": "Activity", "used": "urn:exohub:git:blob:def"},
	}
	if _, ok := ComputeMinVersion(graph); ok {
		t.Error("ComputeMinVersion() ok = true, want false for a record using only original v1 terms")
	}
}

func TestComputeMinVersion_SomeNewTerms(t *testing.T) {
	graph := []Node{
		{"@id": "urn:exohub:activity:abc", "@type": "Activity", "qualifiedUsage": "urn:exohub:usage:abc"},
		{"@id": "urn:exohub:usage:abc", "@type": "Usage", "entity": "urn:exohub:git:blob:abc", "hadRole": "input"},
	}
	version, ok := ComputeMinVersion(graph)
	if !ok {
		t.Fatal("ComputeMinVersion() ok = false, want true")
	}
	if version != firstMinVersionRelease {
		t.Errorf("ComputeMinVersion() = %q, want %q", version, firstMinVersionRelease)
	}
}

func TestComputeMinVersion_AllNewTerms(t *testing.T) {
	graph := []Node{
		{
			"@id":                 "urn:exohub:activity:abc",
			"@type":               "Activity",
			"qualifiedUsage":      "urn:exohub:usage:abc",
			"qualifiedGeneration": "urn:exohub:generation:abc",
		},
		{"@id": "urn:exohub:usage:abc", "@type": "Usage", "entity": "urn:exohub:git:blob:abc", "hadRole": "input", "value": 1},
		{"@id": "urn:exohub:generation:abc", "@type": "Generation", "activity": "urn:exohub:activity:abc", "hadRole": "output"},
	}
	version, ok := ComputeMinVersion(graph)
	if !ok || version != firstMinVersionRelease {
		t.Errorf("ComputeMinVersion() = (%q, %v), want (%q, true)", version, ok, firstMinVersionRelease)
	}
}

func TestCompareSemver(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"0.138.0", "0.137.1", 1},
		{"0.137.1", "0.138.0", -1},
		{"0.138.0", "0.138.0", 0},
		{"v0.138.0", "0.138.0", 0},
		{"0.138.0-rc1", "0.138.0", 0},
	}
	for _, tt := range tests {
		if got := compareSemver(tt.a, tt.b); got != tt.want {
			t.Errorf("compareSemver(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestNeedsUpgrade(t *testing.T) {
	tests := []struct {
		minVersion, running string
		want                bool
	}{
		{"", "0.137.0", false},
		{"0.150.0", "0.140.0", true},
		{"0.140.0", "0.150.0", false},
		{"0.140.0", "0.140.0", false},
		{"0.150.0", "dev", false},
		{"0.150.0", "0.0.0-dev", false},
		{"0.150.0", "", false},
	}
	for _, tt := range tests {
		if got := NeedsUpgrade(tt.minVersion, tt.running); got != tt.want {
			t.Errorf("NeedsUpgrade(%q, %q) = %v, want %v", tt.minVersion, tt.running, got, tt.want)
		}
	}
}

func TestCheckMinVersion(t *testing.T) {
	rec := &Record{ID: "urn:exohub:record:01928c9e-0000-7000-8000-000000000099", MinVersion: "0.150.0"}
	err := CheckMinVersion(rec, "0.140.0")
	if err == nil {
		t.Fatal("CheckMinVersion() error = nil, want an error")
	}
	wantSubstrings := []string{rec.ID, "0.150.0", "exo upgrade"}
	for _, s := range wantSubstrings {
		if !strings.Contains(err.Error(), s) {
			t.Errorf("CheckMinVersion() error = %q, want it to contain %q", err.Error(), s)
		}
	}

	if err := CheckMinVersion(rec, "0.150.0"); err != nil {
		t.Errorf("CheckMinVersion() at exactly minVersion error = %v, want nil", err)
	}
	if err := CheckMinVersion(rec, "0.160.0"); err != nil {
		t.Errorf("CheckMinVersion() above minVersion error = %v, want nil", err)
	}
	if err := CheckMinVersion(rec, "dev"); err != nil {
		t.Errorf("CheckMinVersion() for a dev build error = %v, want nil", err)
	}
}
