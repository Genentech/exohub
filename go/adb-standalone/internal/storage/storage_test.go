package storage_test

import (
	"testing"

	"github.com/Genentech/exohub/go/adb-standalone/internal/storage"
)

func TestIDToKey(t *testing.T) {
	tests := []struct {
		id   string
		want string
	}{
		{"build-cli-buddy:bundle.json@v2.2.0", "build-cli-buddy/bundle.json"},
		{"my-project:path/to/file.txt@v1.0.0", "my-project/path/to/file.txt"},
		{"proj:data.csv@latest", "proj/data.csv"},
		// Edge: no version suffix
		{"proj:file.txt", "proj/file.txt"},
		// Edge: no colon
		{"nocolon", "nocolon"},
	}
	for _, tt := range tests {
		got := storage.IDToKey(tt.id)
		if got != tt.want {
			t.Errorf("IDToKey(%q) = %q, want %q", tt.id, got, tt.want)
		}
	}
}
