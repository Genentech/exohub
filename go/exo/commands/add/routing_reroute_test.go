package add

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRerouteCandidatesSelectsOnlyMismatched is a unit test for
// RerouteCandidates (design D6): a tracked git file whose rule now says
// annex is a candidate; a tracked git file whose rule still says git is not.
func TestRerouteCandidatesSelectsOnlyMismatched(t *testing.T) {
	root := routingRepo(t)
	routingWrite(t, root, "data.csv", "a,b\n1,2\n")
	routingWrite(t, root, "notes.md", "hello\n")
	routingGit(t, root, "add", "data.csv", "notes.md")
	routingGit(t, root, "commit", "-qm", "tracked")
	routingWrite(t, root, ".exohub/config", "annex:\n  routing:\n    - path: '*.csv'\n      to: annex\n    - path: '*.md'\n      to: git\n")

	candidates, err := RerouteCandidates(root, []string{"."}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates = %+v, want exactly data.csv", candidates)
	}
	if candidates[0].Path != "data.csv" || candidates[0].Backend != Git || candidates[0].rerouteTo != Annex {
		t.Fatalf("candidate = %+v", candidates[0])
	}
}

// TestRerouteCandidatesExcludesProtectedPaths is a regression test for
// design D6's "protected paths are never moved" guarantee: even when a
// protected path's current backend disagrees with the router's decision
// (here, .gitattributes was force-annexed directly, bypassing routing),
// --reroute must never select it.
func TestRerouteCandidatesExcludesProtectedPaths(t *testing.T) {
	root := routingAnnexRepo(t)
	routingWrite(t, root, ".gitattributes", "* -text\n")
	// git-annex excludes dotfiles from its default largefiles heuristic, so
	// force the annex association directly to simulate a protected path
	// that somehow ended up on the wrong backend.
	routingGit(t, root, "annex", "add", "--force-large", ".gitattributes")
	routingGit(t, root, "commit", "-qm", "force-annexed gitattributes")
	if !routingIsAnnex(root, ".gitattributes") {
		t.Fatal("setup: .gitattributes should be annexed")
	}
	routingWrite(t, root, ".exohub/config", "annex:\n  routing:\n    - path: '*.md'\n      to: git\n")

	candidates, err := RerouteCandidates(root, []string{"."}, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range candidates {
		if c.Path == ".gitattributes" {
			t.Fatalf("protected path .gitattributes must never be a reroute candidate, got %+v", c)
		}
	}

	if err := routingCLI(t, root, "--reroute", "--yes", "."); err != nil {
		t.Fatal(err)
	}
	if !routingIsAnnex(root, ".gitattributes") {
		t.Fatal(".gitattributes was moved by --reroute, but protected paths must never move")
	}
}

// TestRerouteDefaultPreviewChangesNothing verifies the preview mode (no
// --yes) is pure: no git/annex invocation happens, and the moves it would
// make are described in the output.
func TestRerouteDefaultPreviewChangesNothing(t *testing.T) {
	root := routingRepo(t)
	routingWrite(t, root, "data.csv", "a,b\n1,2\n")
	routingGit(t, root, "add", "data.csv")
	routingGit(t, root, "commit", "-qm", "tracked")
	routingWrite(t, root, ".exohub/config", "annex:\n  routing:\n    - path: '*.csv'\n      to: annex\n")

	before := routingGit(t, root, "status", "--porcelain")
	stdout, _ := runExplainCLIFull(t, root, "--reroute", ".")
	after := routingGit(t, root, "status", "--porcelain")
	if before != after {
		t.Fatalf("preview changed the working tree/index:\nbefore=%q\nafter=%q", before, after)
	}
	if !strings.Contains(stdout, "data.csv") || !strings.Contains(stdout, "annex") {
		t.Fatalf("preview output missing expected move: %q", stdout)
	}
	if !strings.Contains(stdout, "--yes") {
		t.Fatalf("preview output should mention --yes to apply: %q", stdout)
	}
}

// TestRerouteNoCandidatesMessage checks the no-op path when nothing needs
// to move.
func TestRerouteNoCandidatesMessage(t *testing.T) {
	root := routingRepo(t)
	routingWrite(t, root, "notes.md", "hello\n")
	routingGit(t, root, "add", "notes.md")
	routingGit(t, root, "commit", "-qm", "tracked")
	routingWrite(t, root, ".exohub/config", "annex:\n  routing:\n    - path: '*.md'\n      to: git\n")

	stdout, _ := runExplainCLIFull(t, root, "--reroute", ".")
	if !strings.Contains(stdout, "no tracked files need rerouting") {
		t.Fatalf("expected no-op message, got %q", stdout)
	}
}

// TestRerouteYesMovesOnlyMismatched is the real git-annex round trip in
// both directions (design D6/tasks.md §4): a tracked git file whose rule
// now says annex moves to annex, and a tracked annex file whose rule now
// says git moves back to git. A file already matching its rule stays put.
func TestRerouteYesMovesOnlyMismatched(t *testing.T) {
	root := routingAnnexRepo(t)

	// to_annex.csv: tracked in git, no rule applied yet.
	routingWrite(t, root, "to_annex.csv", "a,b\n1,2\n")
	// to_git.bin: tracked in annex, rule will later say git.
	routingWrite(t, root, "to_git.bin", "\x00\x01binary\x02")
	// unchanged.csv: already matches the eventual rule (annex).
	routingWrite(t, root, "unchanged.csv", "x,y\n3,4\n")

	routingGit(t, root, "add", "to_annex.csv")
	routingGit(t, root, "annex", "add", "to_git.bin")
	routingGit(t, root, "annex", "add", "unchanged.csv")
	routingGit(t, root, "commit", "-qm", "tracked before rules")

	if routingIsAnnex(root, "to_annex.csv") {
		t.Fatal("setup: to_annex.csv should start as git")
	}
	if !routingIsAnnex(root, "to_git.bin") {
		t.Fatal("setup: to_git.bin should start as annex")
	}

	routingWrite(t, root, ".exohub/config", "annex:\n  routing:\n    - path: 'to_annex.csv'\n      to: annex\n    - path: 'to_git.bin'\n      to: git\n    - path: 'unchanged.csv'\n      to: annex\n")

	if err := routingCLI(t, root, "--reroute", "--yes", "."); err != nil {
		t.Fatal(err)
	}

	if !routingIsAnnex(root, "to_annex.csv") {
		t.Fatal("to_annex.csv was not moved to annex by --reroute --yes")
	}
	if routingIsAnnex(root, "to_git.bin") {
		t.Fatal("to_git.bin was not moved back to git by --reroute --yes")
	}
	if !routingIsAnnex(root, "unchanged.csv") {
		t.Fatal("unchanged.csv (already matching its rule) should remain annexed")
	}

	// A second run should find nothing left to move.
	stdout, _ := runExplainCLIFull(t, root, "--reroute", ".")
	if !strings.Contains(stdout, "no tracked files need rerouting") {
		t.Fatalf("expected everything settled after one apply, got %q", stdout)
	}
}

// TestRerouteFlagValidation covers the mutually exclusive flag
// combinations runReroute's caller rejects before doing any work.
func TestRerouteFlagValidation(t *testing.T) {
	root := routingRepo(t)
	routingWrite(t, root, "a.txt", "hello\n")
	routingGit(t, root, "add", "a.txt")
	routingGit(t, root, "commit", "-qm", "init")

	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{"yes without reroute", []string{"--yes", "a.txt"}, "--yes requires --reroute"},
		{"reroute with explain", []string{"--reroute", "--explain", "."}, "--reroute cannot be combined with --explain"},
		{"reroute with annex", []string{"--reroute", "--annex", "."}, "--reroute cannot be combined with --annex, --git, or --force"},
		{"reroute with force", []string{"--reroute", "--git", "--force", "."}, "--reroute cannot be combined with --annex, --git, or --force"},
		{"reroute with json", []string{"--reroute", "--json", "."}, "--json requires --explain"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := routingCLI(t, root, tt.args...); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("args %v: err = %v, want containing %q", tt.args, err, tt.want)
			}
		})
	}
}

func routingLockedAnnexRepo(t *testing.T) string {
	t.Helper()
	root := routingAnnexRepo(t)
	routingGit(t, root, "config", "annex.addunlocked", "false")
	return root
}

func routingLockedFile(t *testing.T, root, name, content string) {
	t.Helper()
	routingWrite(t, root, name, content)
	routingGit(t, root, "annex", "add", "--force-large", "--", name)
	routingGit(t, root, "commit", "-qm", "annex file")
	info, err := os.Lstat(filepath.Join(root, name))
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected locked annex symlink: %v, %v", info, err)
	}
}

func TestLockedReroutePresentPreviewAndApply(t *testing.T) {
	root := routingLockedAnnexRepo(t)
	content := strings.Repeat("a,b\n", 600)
	routingLockedFile(t, root, "present.csv", content)
	routingGit(t, root, "config", "annex.largefiles", "anything")
	routingWrite(t, root, ".exohub/config", "annex:\n  routing:\n    - path: '*.csv'\n      larger_than: 2KB\n      content: text\n      to: git\n")
	// D5: even with a git rule, normal add must keep the tracked backend.
	if err := routingCLI(t, root, "present.csv"); err != nil {
		t.Fatal(err)
	}
	if !routingIsAnnex(root, "present.csv") {
		t.Fatal("normal add rerouted locked tracked file")
	}
	before := routingGit(t, root, "ls-files", "--stage", "-z")
	beforeStatus := routingGit(t, root, "status", "--porcelain")
	link, _ := os.Readlink(filepath.Join(root, "present.csv"))
	stdout, _ := runExplainCLIFull(t, root, "--reroute", ".")
	if !strings.Contains(stdout, "present.csv") || !strings.Contains(stdout, "annex") || !strings.Contains(stdout, "git") || !strings.Contains(stdout, "1 file(s) would move") {
		t.Fatalf("preview: %s", stdout)
	}
	if after := routingGit(t, root, "ls-files", "--stage", "-z"); after != before {
		t.Fatal("preview changed index")
	}
	if after := routingGit(t, root, "status", "--porcelain"); after != beforeStatus {
		t.Fatal("preview changed worktree status")
	}
	if after, _ := os.Readlink(filepath.Join(root, "present.csv")); after != link {
		t.Fatal("preview changed locked worktree")
	}
	data, err := os.ReadFile(filepath.Join(root, "present.csv"))
	if err != nil || string(data) != content {
		t.Fatal("preview changed content")
	}
	plan, err := PlanAutoAddWithOptions(root, []string{"present.csv"}, "", PlanOptions{RerouteHints: true})
	if err != nil || len(plan) != 1 || plan[0].Size != int64(len(content)) {
		t.Fatalf("explain size: %+v %v", plan, err)
	}
	if err := routingCLI(t, root, "--reroute", "--yes", "."); err != nil {
		t.Fatal(err)
	}
	if routingIsAnnex(root, "present.csv") {
		t.Fatal("did not migrate to git")
	}
	data, err = os.ReadFile(filepath.Join(root, "present.csv"))
	if err != nil || string(data) != content {
		t.Fatal("apply lost content")
	}
	if info, _ := os.Lstat(filepath.Join(root, "present.csv")); !info.Mode().IsRegular() {
		t.Fatal("file still locked")
	}
}

func TestLockedRerouteDroppedSizeAndPreflight(t *testing.T) {
	root := routingLockedAnnexRepo(t)
	content := strings.Repeat("x", 2500)
	routingLockedFile(t, root, "dropped.csv", content)
	routingGit(t, root, "annex", "drop", "--force", "--", "dropped.csv")
	routingWrite(t, root, "to_annex.txt", "text\n")
	routingGit(t, root, "add", "to_annex.txt")
	routingGit(t, root, "commit", "-qm", "git file")
	routingWrite(t, root, ".exohub/config", "annex:\n  routing:\n    - path: '*.csv'\n      larger_than: 2KB\n      smaller_than: 3KB\n      to: git\n    - path: '*.txt'\n      to: annex\n")
	before := routingGit(t, root, "ls-files", "--stage", "-z")
	plan, err := PlanReroute(root, []string{"."}, "")
	if err != nil || len(plan.Moves) != 2 || len(plan.Skipped) != 0 {
		t.Fatalf("plan: %+v %v", plan, err)
	}
	stdout, _ := runExplainCLIFull(t, root, "--reroute", ".")
	if !strings.Contains(stdout, "dropped.csv") || !strings.Contains(stdout, "2.5 kB") {
		t.Fatalf("size preview: %s", stdout)
	}
	explain, err := PlanAutoAddWithOptions(root, []string{"dropped.csv"}, "", PlanOptions{RerouteHints: true})
	if err != nil || explain[0].Size != int64(len(content)) {
		t.Fatalf("dropped explain: %+v %v", explain, err)
	}
	if err := routingCLI(t, root, "--reroute", "--yes", "."); err == nil || !strings.Contains(err.Error(), "exo get") {
		t.Fatalf("preflight: %v", err)
	}
	if after := routingGit(t, root, "ls-files", "--stage", "-z"); after != before {
		t.Fatal("preflight failure mutated index")
	}
	if routingIsAnnex(root, "to_annex.txt") {
		t.Fatal("migrated another file before preflight")
	}
	if _, err := os.Stat(filepath.Join(root, "dropped.csv")); !os.IsNotExist(err) {
		t.Fatal("preview/apply fetched content")
	}
	// A size-only rule retaining annex is also evaluable with dropped content.
	routingWrite(t, root, ".exohub/config", "annex:\n  routing:\n    - larger_than: 2KB\n      to: annex\n")
	plan, err = PlanReroute(root, []string{"dropped.csv"}, "")
	if err != nil || len(plan.Moves) != 0 || len(plan.Skipped) != 0 {
		t.Fatalf("unchanged dropped plan: %+v %v", plan, err)
	}
}

func TestLockedRerouteDroppedContentReported(t *testing.T) {
	for _, rules := range []string{
		"    - path: '*.csv'\n      content: text\n      to: git\n    - path: '*.csv'\n      to: git\n",
		"    - path: '*.md'\n      to: git\n", // fallback needs content
	} {
		root := routingLockedAnnexRepo(t)
		routingLockedFile(t, root, "dropped.csv", "a,b\n")
		routingLockedFile(t, root, "present.csv", "c,d\n")
		routingGit(t, root, "annex", "drop", "--force", "dropped.csv")
		routingWrite(t, root, ".exohub/config", "annex:\n  routing:\n"+rules)
		before := routingGit(t, root, "ls-files", "--stage", "-z")
		stdout, _ := runExplainCLIFull(t, root, "--reroute", ".")
		if !strings.Contains(stdout, "dropped.csv: skipped:") || !strings.Contains(stdout, "exo get") || !strings.Contains(stdout, "1 file(s) skipped") {
			t.Fatalf("skip preview: %s", stdout)
		}
		if after := routingGit(t, root, "ls-files", "--stage", "-z"); after != before {
			t.Fatal("preview changed index")
		}
		stdout, stderr := runExplainCLIFull(t, root, "--reroute", "--yes", ".")
		if strings.Contains(stdout, "no tracked files need rerouting") || !strings.Contains(stderr, "dropped.csv: skipped:") {
			t.Fatalf("apply reporting: %s / %s", stdout, stderr)
		}
		if routingIsAnnex(root, "present.csv") || !routingIsAnnex(root, "dropped.csv") {
			t.Fatal("apply did not migrate only evaluable content")
		}
		stdout, _ = runExplainCLIFull(t, root, "--reroute", "dropped.csv")
		if strings.Contains(stdout, "no tracked files need rerouting") {
			t.Fatalf("misleading no-op: %s", stdout)
		}
	}
}

func TestLockedRerouteProtectedAndPlainForce(t *testing.T) {
	root := routingLockedAnnexRepo(t)
	routingLockedFile(t, root, ".gitattributes", "* -text\n")
	routingLockedFile(t, root, "manual.csv", "manual content\n")
	routingWrite(t, root, ".exohub/config", "annex:\n  routing:\n    - larger_than: 0\n      to: git\n")
	routingGit(t, root, "annex", "drop", "--force", ".gitattributes")
	plan, err := PlanReroute(root, []string{".gitattributes"}, "")
	if err != nil || len(plan.Moves) != 0 || len(plan.Skipped) != 0 {
		t.Fatalf("protected: %+v %v", plan, err)
	}
	if err := routingCLI(t, root, "--reroute", "--yes", ".gitattributes"); err != nil {
		t.Fatal(err)
	}
	if !routingIsAnnex(root, ".gitattributes") {
		t.Fatal("protected path moved")
	}
	routingGit(t, root, "config", "annex.largefiles", "anything")
	if err := routingCLI(t, root, "--git", "--force", "manual.csv"); err != nil {
		t.Fatal(err)
	}
	if routingIsAnnex(root, "manual.csv") {
		t.Fatal("manual migration re-annexed by largefiles")
	}
	data, err := os.ReadFile(filepath.Join(root, "manual.csv"))
	if err != nil || string(data) != "manual content\n" {
		t.Fatal("manual migration lost content")
	}
}
