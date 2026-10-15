package add

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func routingAnnexRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git-annex"); err != nil {
		t.Skip("git-annex is required")
	}
	root := routingRepo(t)
	t.Cleanup(func() {
		_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err == nil && info.IsDir() {
				return os.Chmod(path, 0755)
			}
			return err
		})
	})
	routingGit(t, root, "config", "annex.addunlocked", "true")
	routingGit(t, root, "annex", "init", "routing-test")
	return root
}

func routingCLI(t *testing.T, root string, args ...string) error {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(cwd); err != nil {
			t.Fatal(err)
		}
	}()
	cmd := NewCommand()
	cmd.SetArgs(args)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	return cmd.Execute()
}

func routingIsAnnex(root, file string) bool {
	cmd := exec.Command("git", "annex", "lookupkey", "--", file)
	cmd.Dir = root
	out, err := cmd.Output()
	return err == nil && len(out) > 0
}

func TestRoutingIntegrationAuthoritativeAndTracked(t *testing.T) {
	root := routingAnnexRepo(t)
	routingWrite(t, root, ".exohub/config", "annex:\n  routing:\n    - path: '*.csv'\n      to: annex\n    - path: '*.md'\n      to: git\n")
	routingWrite(t, root, "old.csv", "original\n")
	routingWrite(t, root, "tracked\nname.txt", "tracked newline\n")
	routingGit(t, root, "add", "old.csv", "tracked\nname.txt")
	routingGit(t, root, "commit", "-qm", "tracked git")
	routingWrite(t, root, "old.csv", "modified\n")
	routingWrite(t, root, "new.csv", "new\n")
	routingWrite(t, root, "notes.md", strings.Repeat("text", 2*1024*1024))
	routingGit(t, root, "config", "annex.largefiles", "largerthan=10mb")
	if err := routingCLI(t, root, "."); err != nil {
		t.Fatal(err)
	}
	if !routingIsAnnex(root, "new.csv") {
		t.Fatal("rule was overridden by annex.largefiles")
	}
	if routingIsAnnex(root, "old.csv") {
		t.Fatal("tracked git file was annexed")
	}
	if routingIsAnnex(root, "notes.md") {
		t.Fatal("explicit git rule was ignored")
	}

	// The opposite largefiles setting must not annex any git-bound category.
	routingGit(t, root, "config", "annex.largefiles", "anything")
	routingWrite(t, root, "another.md", "explicit git rule\n")
	routingWrite(t, root, "small.txt", "default text\n")
	routingWrite(t, root, "old.csv", "modified again\n")
	if err := routingCLI(t, root, "another.md", "small.txt", "old.csv"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"another.md", "small.txt", "old.csv"} {
		if routingIsAnnex(root, name) {
			t.Fatalf("git-bound %s was annexed by annex.largefiles=anything", name)
		}
	}

	// A modified unlocked annex file remains annexed even with a new git rule.
	routingGit(t, root, "commit", "-qm", "added files")
	routingWrite(t, root, ".exohub/config", "annex:\n  routing:\n    - path: '*.csv'\n      to: git\n")
	routingWrite(t, root, "new.csv", "modified unlocked annex\n")
	routingGit(t, root, "config", "annex.largefiles", "nothing")
	if err := routingCLI(t, root, "."); err != nil {
		t.Fatal(err)
	}
	if !routingIsAnnex(root, "new.csv") {
		t.Fatal("tracked annex file moved to git")
	}
}

func TestRoutingIntegrationInvalidStagesNothing(t *testing.T) {
	for _, invalid := range []string{"rule", "env", "flag"} {
		t.Run(invalid, func(t *testing.T) {
			root := routingAnnexRepo(t)
			routingWrite(t, root, "notes.txt", "new text\n")
			args := []string{"."}
			switch invalid {
			case "rule":
				routingWrite(t, root, ".exohub/config", "annex:\n  routing:\n    - path: '*.txt'\n      to: anex\n")
			case "env":
				t.Setenv("EXOHUB_LARGE_TEXT_THRESHOLD", "lots")
			case "flag":
				args = append([]string{"--large-text-threshold=lots"}, args...)
			}
			before := routingGit(t, root, "ls-files", "--stage", "-z")
			if err := routingCLI(t, root, args...); err == nil {
				t.Fatal("invalid config accepted")
			}
			if after := routingGit(t, root, "ls-files", "--stage", "-z"); after != before {
				t.Fatal("index changed")
			}
		})
	}
}

func TestRoutingIntegrationThresholdFallback(t *testing.T) {
	root := routingAnnexRepo(t)
	routingWrite(t, root, ".exohub/config", "annex:\n  routing:\n    - path: '*.md'\n      to: git\n")
	routingWrite(t, root, "notes.md", strings.Repeat("x", 2000))
	routingWrite(t, root, "fallback.txt", strings.Repeat("x", 2000))
	t.Setenv("EXOHUB_LARGE_TEXT_THRESHOLD", "1KB")
	if err := routingCLI(t, root, "notes.md", "fallback.txt"); err != nil {
		t.Fatal(err)
	}
	if routingIsAnnex(root, "notes.md") || !routingIsAnnex(root, "fallback.txt") {
		t.Fatal("env override did not affect fallback only")
	}
}
