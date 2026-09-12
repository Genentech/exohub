package features

import (
	"reflect"
	"testing"
)

func TestEnabled_empty(t *testing.T) {
	orig := registered
	registered = nil
	defer func() { registered = orig }()

	if got := Enabled(); got != nil {
		t.Fatalf("expected nil for empty registry, got %v", got)
	}
}

func TestEnabled_sorted(t *testing.T) {
	orig := registered
	registered = []string{"roche", "exohubapi", "artifactdb", "janus"}
	defer func() { registered = orig }()

	got := Enabled()
	want := []string{"artifactdb", "exohubapi", "janus", "roche"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Enabled() = %v, want %v", got, want)
	}
}

func TestEnabled_dedup(t *testing.T) {
	orig := registered
	registered = []string{"exohubapi", "exohubapi", "roche"}
	defer func() { registered = orig }()

	got := Enabled()
	want := []string{"exohubapi", "roche"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Enabled() dedup = %v, want %v", got, want)
	}
}

func TestEnabled_doesNotMutateRegistry(t *testing.T) {
	orig := registered
	registered = []string{"roche", "exohubapi"}
	defer func() { registered = orig }()

	_ = Enabled()
	if registered[0] != "roche" || registered[1] != "exohubapi" {
		t.Fatalf("Enabled() mutated registered slice: %v", registered)
	}
}
