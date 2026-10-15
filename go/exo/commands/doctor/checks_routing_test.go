package doctor

import (
	"os"
	"path/filepath"
	"testing"
)

func writeRoutingConfig(t *testing.T, dir, content string) {
	t.Helper()
	exohubDir := filepath.Join(dir, ".exohub")
	if err := os.MkdirAll(exohubDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(exohubDir, "config"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestRunRoutingChecksNoConfig(t *testing.T) {
	dir := t.TempDir()
	results := runRoutingChecks(dir)
	if len(results) != 1 || results[0].Status != StatusSkip {
		t.Fatalf("results = %+v", results)
	}
}

func TestRunRoutingChecksNoRoutingKey(t *testing.T) {
	dir := t.TempDir()
	writeRoutingConfig(t, dir, "annex:\n  addunlocked: true\n")
	results := runRoutingChecks(dir)
	if len(results) != 1 || results[0].Status != StatusSkip {
		t.Fatalf("results = %+v", results)
	}
}

func TestRunRoutingChecksValid(t *testing.T) {
	dir := t.TempDir()
	writeRoutingConfig(t, dir, "annex:\n  routing:\n    - path: '*.csv'\n      to: annex\n    - path: '*.md'\n      to: git\n")
	results := runRoutingChecks(dir)
	if len(results) != 1 || results[0].Status != StatusPass {
		t.Fatalf("results = %+v", results)
	}
}

func TestRunRoutingChecksInvalidRule(t *testing.T) {
	dir := t.TempDir()
	writeRoutingConfig(t, dir, "annex:\n  routing:\n    - path: '*.csv'\n      to: anex\n")
	results := runRoutingChecks(dir)
	if len(results) != 1 || results[0].Status != StatusFail {
		t.Fatalf("results = %+v", results)
	}
}

func TestRunRoutingChecksShadowedRuleWarns(t *testing.T) {
	dir := t.TempDir()
	writeRoutingConfig(t, dir, "annex:\n  routing:\n    - path: '**/*.csv'\n      to: git\n    - path: 'data/*.csv'\n      to: annex\n")
	results := runRoutingChecks(dir)
	if len(results) != 2 || results[0].Status != StatusPass || results[1].Status != StatusWarn {
		t.Fatalf("results = %+v", results)
	}
	if results[1].ID != "repo.routing-shadow" {
		t.Fatalf("unexpected id: %s", results[1].ID)
	}
}

func TestRunRoutingChecksInvalidYAML(t *testing.T) {
	dir := t.TempDir()
	writeRoutingConfig(t, dir, "not: valid: yaml: ::")
	results := runRoutingChecks(dir)
	if len(results) != 1 || results[0].Status != StatusFail {
		t.Fatalf("results = %+v", results)
	}
}
