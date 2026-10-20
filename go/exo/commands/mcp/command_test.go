package mcp

import "testing"

// TestToolRegistryNoCreateRepoOrClone guards against create_repo / clone
// reappearing: exo mcp no longer creates git host repos or clones (the
// caller is expected to do that with its own git host credentials/CLI).
func TestToolRegistryNoCreateRepoOrClone(t *testing.T) {
	for _, entry := range toolRegistry() {
		if entry.tool.Name == "create_repo" || entry.tool.Name == "clone" {
			t.Errorf("toolRegistry() still has a %q tool", entry.tool.Name)
		}
	}
}

// TestToolRegistryHasNewTools guards the tool set this change adds.
func TestToolRegistryHasNewTools(t *testing.T) {
	want := []string{"profiles", "get", "doctor", "provenance_add", "provenance_show", "provenance_validate"}
	seen := make(map[string]bool)
	for _, entry := range toolRegistry() {
		seen[entry.tool.Name] = true
	}
	for _, name := range want {
		if !seen[name] {
			t.Errorf("toolRegistry() missing tool %q", name)
		}
	}
}

// TestToolRegistryUniqueNames guards against accidental duplicate tool
// registrations.
func TestToolRegistryUniqueNames(t *testing.T) {
	seen := make(map[string]bool)
	for _, entry := range toolRegistry() {
		if seen[entry.tool.Name] {
			t.Errorf("duplicate tool name %q in toolRegistry()", entry.tool.Name)
		}
		seen[entry.tool.Name] = true
	}
}
