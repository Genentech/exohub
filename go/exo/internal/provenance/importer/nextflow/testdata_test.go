package nextflow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// fixtureStore builds a lineage store directory under t.TempDir() from
// version (lineage.json's "version" field), runs (lineage.json's "runs"
// list) and records (one file per entry, keyed by the hex part of its
// "lid://<hex>" id).
func fixtureStore(t *testing.T, version string, runs []indexRun, records map[string]any) string {
	t.Helper()
	dir := t.TempDir()
	writeJSON(t, filepath.Join(dir, indexFileName), map[string]any{
		"version": version,
		"runs":    runs,
	})
	recordsDir := filepath.Join(dir, recordsDirName)
	if err := os.MkdirAll(recordsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	for lid, rec := range records {
		hex := lid[len("lid://"):]
		writeJSON(t, filepath.Join(recordsDir, hex+".json"), rec)
	}
	return dir
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}
