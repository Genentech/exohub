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
	registered = []string{"exohubapi", "artifactdb", "beta", "alpha"}
	defer func() { registered = orig }()

	got := Enabled()
	want := []string{"alpha", "artifactdb", "beta", "exohubapi"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Enabled() = %v, want %v", got, want)
	}
}

func TestEnabled_dedup(t *testing.T) {
	orig := registered
	registered = []string{"exohubapi", "exohubapi", "beta"}
	defer func() { registered = orig }()

	got := Enabled()
	want := []string{"beta", "exohubapi"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Enabled() dedup = %v, want %v", got, want)
	}
}

func TestEnabled_doesNotMutateRegistry(t *testing.T) {
	orig := registered
	registered = []string{"beta", "exohubapi"}
	defer func() { registered = orig }()

	_ = Enabled()
	if registered[0] != "beta" || registered[1] != "exohubapi" {
		t.Fatalf("Enabled() mutated registered slice: %v", registered)
	}
}
