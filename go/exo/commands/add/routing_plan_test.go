package add

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func routingGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func routingRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	routingGit(t, dir, "init", "-q")
	routingGit(t, dir, "config", "user.name", "exo test")
	routingGit(t, dir, "config", "user.email", "test@example.invalid")
	return dir
}

func routingWrite(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestRoutingPlanSubdirectoryAndTracked(t *testing.T) {
	root := routingRepo(t)
	routingWrite(t, root, ".exohub/config", "annex:\n  routing:\n    - path: 'data/*.csv'\n      to: annex\n")
	routingWrite(t, root, ".gitignore", "*.ignored\n")
	routingWrite(t, root, "data/old.csv", "original\n")
	routingWrite(t, root, "data/new.csv", "new\n")
	routingWrite(t, root, "data/a\nb.txt", "text\n")
	routingWrite(t, root, "data/skip.ignored", "ignored\n")
	routingGit(t, root, "add", "data/old.csv")
	routingWrite(t, root, "data/old.csv", "modified\n")
	plan, err := PlanAutoAdd(filepath.Join(root, "data"), []string{".", "new.csv"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 3 {
		t.Fatalf("plan = %+v", plan)
	}
	byPath := make(map[string]PlannedFile)
	for _, file := range plan {
		byPath[file.Path] = file
	}
	// By default (used by plain `exo add` and MCP add, design D5), the
	// router is never consulted for tracked files, so old.csv's reason
	// carries no reroute hint even though a rule would route it to annex.
	old := byPath["old.csv"]
	if old.Backend != Git || old.Reason != "tracked (git) — unchanged" || old.Size != int64(len("modified\n")) {
		t.Fatalf("tracked = %+v", old)
	}
	newFile := byPath["new.csv"]
	if newFile.Backend != Annex || !strings.HasPrefix(newFile.Reason, "rule 1:") || newFile.Size != int64(len("new\n")) {
		t.Fatalf("new = %+v", newFile)
	}
	if _, err := PlanAutoAdd(root, []string{"data/skip.ignored"}, ""); err == nil {
		t.Fatal("explicit ignored file accepted")
	}

	// With RerouteHints (what `exo add --explain` sets), old.csv does get
	// the "--reroute would move to …" hint (design D8).
	explainPlan, err := PlanAutoAddWithOptions(filepath.Join(root, "data"), []string{".", "new.csv"}, "", PlanOptions{RerouteHints: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range explainPlan {
		if file.Path == "old.csv" && file.Reason != "tracked (git) — unchanged; --reroute would move to annex" {
			t.Fatalf("explain old.csv = %+v", file)
		}
	}
}

func TestRoutingPlanTrackedNoRerouteHintWhenUnchanged(t *testing.T) {
	root := routingRepo(t)
	routing := "annex:\n  routing:\n    - path: '*.md'\n      to: git\n"
	routingWrite(t, root, ".exohub/config", routing)
	routingWrite(t, root, "notes.md", "original\n")
	routingGit(t, root, "add", "notes.md")
	routingWrite(t, root, "notes.md", "modified\n")

	plan, err := PlanAutoAdd(root, []string{"notes.md"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if plan[0].Backend != Git || plan[0].Reason != "tracked (git) — unchanged" {
		t.Fatalf("plan = %+v", plan[0])
	}
}

// TestRoutingPlanTrackedDanglingSymlinkNoRerouteHints is a regression test
// for the maître's finding on MR !247: the default plan (used by plain
// `exo add` and MCP add, design D5) must never consult the router for
// tracked files, and even in explain mode (PlanOptions.RerouteHints) the
// router must not be asked to sniff through a locked-mode symlink whose
// target is missing (the shape of a dropped annex object) — that must
// degrade to "no hint", not an error.
func TestRoutingPlanTrackedDanglingSymlinkNoRerouteHints(t *testing.T) {
	root := routingRepo(t)
	linkPath := filepath.Join(root, "dropped.bin")
	if err := os.Symlink(filepath.Join(root, ".git", "annex", "objects", "nonexistent"), linkPath); err != nil {
		t.Fatal(err)
	}
	routingGit(t, root, "add", "dropped.bin")
	routingWrite(t, root, "new.txt", "new\n")

	plan, err := PlanAutoAdd(root, []string{"."}, "")
	if err != nil {
		t.Fatalf("PlanAutoAdd (default) must not consult the router for tracked files: %v", err)
	}
	byPath := make(map[string]PlannedFile)
	for _, f := range plan {
		byPath[f.Path] = f
	}
	if dropped := byPath["dropped.bin"]; dropped.Backend != Git || dropped.Reason != "tracked (git) — unchanged" {
		t.Fatalf("dropped = %+v", dropped)
	}

	plan, err = PlanAutoAddWithOptions(root, []string{"."}, "", PlanOptions{RerouteHints: true})
	if err != nil {
		t.Fatalf("PlanAutoAddWithOptions(RerouteHints) must not fail on a dangling tracked symlink: %v", err)
	}
	for _, f := range plan {
		if f.Path == "dropped.bin" && f.Reason != "tracked (git) — unchanged" {
			t.Fatalf("expected no reroute hint for a locked-mode symlink, got %+v", f)
		}
	}
}

func TestRoutingPlanFallbackOnly(t *testing.T) {
	root := routingRepo(t)
	routingWrite(t, root, ".exohub/config", "annex:\n  routing:\n    - path: '*.md'\n      to: git\n")
	routingWrite(t, root, "notes.md", strings.Repeat("x", 2000))
	routingWrite(t, root, "text.txt", strings.Repeat("x", 2000))
	t.Setenv("EXOHUB_LARGE_TEXT_THRESHOLD", "1KB")
	plan, err := PlanAutoAdd(root, []string{"notes.md", "text.txt"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if plan[0].Backend != Git || plan[0].LargeTextFallback {
		t.Fatalf("rule = %+v", plan[0])
	}
	if plan[1].Backend != Annex || !plan[1].LargeTextFallback {
		t.Fatalf("fallback = %+v", plan[1])
	}
	plan, err = PlanAutoAdd(root, []string{"text.txt"}, "3KB")
	if err != nil || plan[0].Backend != Git {
		t.Fatalf("flag override: %+v, %v", plan, err)
	}
}

func TestSplitArgsThreshold(t *testing.T) {
	for _, args := range [][]string{{"--large-text-threshold", "1KiB", "file.txt"}, {"--large-text-threshold=1KiB", "file.txt"}} {
		locals, _, flags, value, _, _, _, _, err := splitArgs(args)
		if err != nil || value != "1KiB" || len(flags) != 0 || len(locals) != 1 || locals[0] != "file.txt" {
			t.Fatalf("%v: %v %v %q %v", args, locals, flags, value, err)
		}
	}
	for _, args := range [][]string{{"--large-text-threshold"}, {"--large-text-threshold="}, {"--large-text-threshold", "--git"}, {"--large-text-threshold=lots"}} {
		if _, _, _, _, _, _, _, _, err := splitArgs(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

func TestRoutingPlanManyExplicitPaths(t *testing.T) {
	root := routingRepo(t)
	routingWrite(t, root, ".exohub/config", "annex:\n  routing:\n    - path: '*.csv'\n      to: annex\n")
	paths := make([]string, 2500)
	for i := range paths {
		extension := "txt"
		if i%2 == 0 {
			extension = "csv"
		}
		paths[i] = fmt.Sprintf("data/file-%04d.%s", i, extension)
		routingWrite(t, root, paths[i], "text\n")
	}
	plan, err := PlanAutoAdd(root, paths, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != len(paths) {
		t.Fatalf("got %d files, want %d", len(plan), len(paths))
	}
	for _, file := range plan {
		expected := Git
		if strings.HasSuffix(file.Path, ".csv") {
			expected = Annex
		}
		if file.Backend != expected {
			t.Fatalf("wrong destination: %+v", file)
		}
	}
	if _, err := PlanAutoAdd(root, append(paths, "missing.txt"), ""); err == nil {
		t.Fatal("missing input in nonempty batch accepted")
	}
	routingWrite(t, root, ".gitignore", "ignored.txt\n")
	routingWrite(t, root, "ignored.txt", "ignored\n")
	if _, err := PlanAutoAdd(root, append(paths, "ignored.txt"), ""); err == nil {
		t.Fatal("ignored input in nonempty batch accepted")
	}
}

func TestAnnexKeySize(t *testing.T) {
	for _, tt := range []struct {
		key  string
		size int64
		ok   bool
	}{
		{"SHA256E-s2500--hash.csv", 2500, true},
		{"SHA256-s0--hash", 0, true},
		{"SHA256E-s123-m100--hash", 123, true},
		{"URL--https://example.test/s2500--data", 0, false},
		{"SHA256E-s--hash", 0, false},
		{"SHA256E-s+12--hash", 0, false},
		{"SHA256E-s12x--hash", 0, false},
		{"SHA256E-s9223372036854775808--hash", 0, false},
		{"SHA256E-s10", 0, false},
	} {
		size, ok := annexKeySize(tt.key)
		if ok != tt.ok || (ok && size != tt.size) {
			t.Errorf("%q: %d %v, want %d %v", tt.key, size, ok, tt.size, tt.ok)
		}
	}
}

func TestRerouteUnlockedDroppedPointerNotContent(t *testing.T) {
	root := routingAnnexRepo(t)
	content := strings.Repeat("x", 2500)
	routingWrite(t, root, "dropped.csv", content)
	routingGit(t, root, "annex", "add", "--force-large", "dropped.csv")
	routingGit(t, root, "commit", "-qm", "annex")
	routingGit(t, root, "annex", "drop", "--force", "dropped.csv")
	routingWrite(t, root, ".exohub/config", "annex:\n  routing:\n    - content: text\n      to: git\n")
	plan, err := PlanReroute(root, []string{"dropped.csv"}, "")
	if err != nil || len(plan.Skipped) != 1 || len(plan.Moves) != 0 {
		t.Fatalf("pointer must not be sniffed as text: %+v %v", plan, err)
	}
	if plan.Skipped[0].Size != int64(len(content)) {
		t.Fatal("pointer length used as size")
	}
	routingWrite(t, root, ".exohub/config", "annex:\n  routing:\n    - larger_than: 2KB\n      to: git\n")
	plan, err = PlanReroute(root, []string{"dropped.csv"}, "")
	if err != nil || len(plan.Moves) != 1 || len(plan.Skipped) != 0 {
		t.Fatalf("pointer size metadata: %+v %v", plan, err)
	}
	if err := routingCLI(t, root, "--reroute", "--yes", "dropped.csv"); err == nil || !strings.Contains(err.Error(), "exo get") {
		t.Fatalf("pointer preflight: %v", err)
	}
}

func TestRerouteInvalidConfigStagesNothing(t *testing.T) {
	root := routingLockedAnnexRepo(t)
	routingLockedFile(t, root, "present.csv", "content\n")
	routingWrite(t, root, ".exohub/config", "annex:\n  routing:\n    - path: '*.csv'\n      to: invalid\n")
	before := routingGit(t, root, "ls-files", "--stage", "-z")
	if err := routingCLI(t, root, "--reroute", "--yes", "."); err == nil {
		t.Fatal("accepted invalid config")
	}
	if after := routingGit(t, root, "ls-files", "--stage", "-z"); before != after {
		t.Fatal("invalid config staged files")
	}
}

func TestExplainModifiedUnlockedAnnexUsesActualSize(t *testing.T) {
	root := routingAnnexRepo(t)
	routingWrite(t, root, "edited.csv", "original\n")
	routingGit(t, root, "annex", "add", "--force-large", "edited.csv")
	routingGit(t, root, "commit", "-qm", "unlocked annex file")
	originalKey := routingGit(t, root, "annex", "lookupkey", "edited.csv")
	modified := strings.Repeat("a,b\n", 600)
	routingWrite(t, root, "edited.csv", modified)
	routingWrite(t, root, ".exohub/config", "annex:\n  routing:\n    - path: '*.csv'\n      larger_than: 2KB\n      to: git\n")
	if key := routingGit(t, root, "annex", "lookupkey", "edited.csv"); key != originalKey {
		t.Fatal("setup changed recorded key")
	}
	stdout, _ := runExplainCLIFull(t, root, "--explain", "--json", "edited.csv")
	var entries []explainEntry
	if err := json.Unmarshal([]byte(stdout), &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Size != int64(len(modified)) {
		t.Fatalf("explain must show modified content size: %s", stdout)
	}
	if entries[0].Destination != "annex" || !strings.Contains(entries[0].Reason, "--reroute would move to git") {
		t.Fatalf("hint must evaluate modified content while retaining annex backend: %s", stdout)
	}
}
