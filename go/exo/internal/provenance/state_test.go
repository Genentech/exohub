package provenance

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// stateTestRepo creates a temp git repository (optionally git-annex
// initialized) and returns its root plus a helper to run commands in it.
func stateTestRepo(t *testing.T, withAnnex bool) (root string, run func(args ...string) string) {
	t.Helper()
	root = t.TempDir()
	run = func(args ...string) string {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+root)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("command %v failed: %v\n%s", args, err, out)
		}
		return string(out)
	}
	run("git", "init", "-q")
	run("git", "config", "user.email", "test@test.com")
	run("git", "config", "user.name", "Test")
	// Never let git start background housekeeping in test repos: a commit
	// with thousands of objects (TestListRecordsLargeRepoIsFast) can trigger a
	// detached `git gc --auto` / auto-maintenance that is still writing into
	// .git when t.TempDir's cleanup runs ("unlinkat .git: directory not
	// empty"), which failed the public GitHub CI.
	run("git", "config", "gc.auto", "0")
	run("git", "config", "gc.autoDetach", "false")
	run("git", "config", "maintenance.auto", "false")
	if withAnnex {
		if _, err := exec.LookPath("git-annex"); err != nil {
			t.Skip("git-annex not available")
		}
		run("git", "annex", "init", "-q", "test-repo")
		// git-annex makes object files read-only; make everything writable
		// again before TempDir's cleanup tries to remove them.
		t.Cleanup(func() {
			exec.Command("chmod", "-R", "u+w", root).Run()
		})
	}
	return root, run
}

func writeRepoFile(t *testing.T, root, relPath, content string) {
	t.Helper()
	full := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func TestListRecordsAcrossStates(t *testing.T) {
	root, run := stateTestRepo(t, false)

	writeRepoFile(t, root, ".exohub/provenance/a.jsonld", "{}")
	writeRepoFile(t, root, ".exohub/provenance/README.md", "not a record")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	writeRepoFile(t, root, ".exohub/provenance/b.jsonld", "{}")
	run("git", "add", ".exohub/provenance/b.jsonld")

	writeRepoFile(t, root, ".exohub/provenance/c.jsonld", "{}")

	worktree, err := NewWorktreeReader(root).ListRecords()
	if err != nil {
		t.Fatalf("worktree ListRecords() error = %v", err)
	}
	if got := recordNames(worktree); !equalSets(got, []string{"a.jsonld", "b.jsonld", "c.jsonld"}) {
		t.Errorf("worktree records = %v, want a,b,c", got)
	}

	staged, err := NewStagedReader(root).ListRecords()
	if err != nil {
		t.Fatalf("staged ListRecords() error = %v", err)
	}
	if got := recordNames(staged); !equalSets(got, []string{"a.jsonld", "b.jsonld"}) {
		t.Errorf("staged records = %v, want a,b", got)
	}

	ref, err := NewRefReader(root, "HEAD").ListRecords()
	if err != nil {
		t.Fatalf("ref ListRecords() error = %v", err)
	}
	if got := recordNames(ref); !equalSets(got, []string{"a.jsonld"}) {
		t.Errorf("ref records = %v, want a", got)
	}
}

func recordNames(files []RecordFile) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = filepath.Base(f.Path)
	}
	return out
}

// TestListRecordsAnnexedField covers the non-blocking review item: a
// record file backed by git-annex (a locked symlink here) must be flagged
// Annexed so callers can report it without re-querying git.
func TestListRecordsAnnexedField(t *testing.T) {
	root, run := stateTestRepo(t, true)
	// git-annex skips "dotfiles" (any path with a leading-dot component,
	// which .exohub/provenance always has) by default; opt back in so this
	// test can actually produce an annexed record.
	run("git", "config", "annex.dotfiles", "true")
	writeRepoFile(t, root, ".exohub/provenance/plain.jsonld", "{}")

	// A record added through git-annex becomes a locked symlink.
	writeRepoFile(t, root, ".exohub/provenance/annexed.jsonld", "{}")
	run("git", "annex", "add", ".exohub/provenance/annexed.jsonld")
	run("git", "add", ".exohub/provenance/plain.jsonld")
	run("git", "commit", "-q", "-m", "init")

	for _, reader := range []*Reader{
		NewWorktreeReader(root),
		NewStagedReader(root),
		NewRefReader(root, "HEAD"),
	} {
		records, err := reader.ListRecords()
		if err != nil {
			t.Fatalf("ListRecords() error = %v", err)
		}
		byName := map[string]RecordFile{}
		for _, rf := range records {
			byName[filepath.Base(rf.Path)] = rf
		}
		if byName["plain.jsonld"].Annexed {
			t.Errorf("plain.jsonld reported Annexed = true, want false")
		}
		if !byName["annexed.jsonld"].Annexed {
			t.Errorf("annexed.jsonld reported Annexed = false, want true")
		}
	}
}

func equalSets(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := map[string]bool{}
	for _, g := range got {
		seen[g] = true
	}
	for _, w := range want {
		if !seen[w] {
			return false
		}
	}
	return true
}

func TestListRecordsNoDirectory(t *testing.T) {
	root, _ := stateTestRepo(t, false)
	records, err := NewWorktreeReader(root).ListRecords()
	if err != nil {
		t.Fatalf("ListRecords() error = %v", err)
	}
	if len(records) != 0 {
		t.Errorf("ListRecords() = %v, want empty", records)
	}
}

// TestListRecordsIgnoresNestedFiles covers the review item that a
// "*.jsonld directly under RecordDir" restriction must hold in every mode:
// `git ls-files --stage -- .exohub/provenance/` matches every index entry
// under that pathspec, including ones nested in a subdirectory, so
// ModeStaged's batch listing must filter those out itself to match
// ModeWorktree's os.ReadDir (non-recursive) and ModeRef's non-recursive
// `git ls-tree`.
func TestListRecordsIgnoresNestedFiles(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, ".exohub/provenance/a.jsonld", "{}")
	writeRepoFile(t, root, ".exohub/provenance/nested/b.jsonld", "{}")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	for _, reader := range []*Reader{NewStagedReader(root), NewRefReader(root, "HEAD")} {
		records, err := reader.ListRecords()
		if err != nil {
			t.Fatalf("ListRecords() error = %v", err)
		}
		if got := recordNames(records); !equalSets(got, []string{"a.jsonld"}) {
			t.Errorf("records = %v, want only a.jsonld (nested/b.jsonld must be ignored)", got)
		}
	}
}

// TestListRecordsLargeRepoIsFast covers the review's performance fix:
// Reader.ListRecords must list and read every record in a constant number
// of git invocations for ModeStaged and ModeRef, not one `git ls-files`/
// `ls-tree` plus one `git cat-file` per record. Before the fix, a
// synthetic 20,000-record repository took over two minutes for
// `validate --staged` and `show` (issue measurement); this asserts a
// generous bound the old per-record implementation could not meet.
func TestListRecordsLargeRepoIsFast(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large-repo timing test in -short mode")
	}
	root, run := stateTestRepo(t, false)

	const recordCount = 3000
	for i := 0; i < recordCount; i++ {
		writeRepoFile(t, root, fmt.Sprintf(".exohub/provenance/r%d.jsonld", i), fmt.Sprintf(`{"n":%d}`, i))
	}
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	for _, reader := range []*Reader{NewStagedReader(root), NewRefReader(root, "HEAD")} {
		start := time.Now()
		records, err := reader.ListRecords()
		elapsed := time.Since(start)
		if err != nil {
			t.Fatalf("ListRecords() error = %v", err)
		}
		if len(records) != recordCount {
			t.Fatalf("ListRecords() returned %d records, want %d", len(records), recordCount)
		}
		// Generous bound: a per-record implementation (one git ls-files/
		// ls-tree plus one git cat-file per record) takes well over a
		// minute at this record count; the batch implementation (two git
		// processes total) takes well under a second even on a loaded CI
		// machine.
		const bound = 10 * time.Second
		if elapsed > bound {
			t.Errorf("ListRecords() took %s for %d records, want under %s", elapsed, recordCount, bound)
		}
		t.Logf("ListRecords() on %d records took %s", recordCount, elapsed)
	}
}

func TestResolveContentIDGitBlob(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "data/result.csv", "a,b,c\n1,2,3\n")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	wantOID := strings.TrimSpace(run("git", "rev-parse", "HEAD:data/result.csv"))

	for _, reader := range []*Reader{
		NewStagedReader(root),
		NewRefReader(root, "HEAD"),
	} {
		id, err := reader.ResolveContentID("data/result.csv")
		if err != nil {
			t.Fatalf("ResolveContentID() error = %v", err)
		}
		want := GitBlobEntityID(wantOID)
		if id != want {
			t.Errorf("ResolveContentID() = %q, want %q", id, want)
		}
	}
}

func TestResolveContentIDUntracked(t *testing.T) {
	root, _ := stateTestRepo(t, false)
	writeRepoFile(t, root, "data/new.csv", "x\n")
	if _, err := NewStagedReader(root).ResolveContentID("data/new.csv"); err == nil {
		t.Errorf("ResolveContentID() expected error for untracked file")
	}
}

// TestResolveContentIDDoesNotMisidentifyGitContentAsAnnex covers review
// comment 1 on !253: a git-tracked file whose name or content merely looks
// like an annex key, or a symlink to a non-annex target, must resolve to a
// plain git blob id, never an annex entity id.
func TestResolveContentIDDoesNotMisidentifyGitContentAsAnnex(t *testing.T) {
	root, run := stateTestRepo(t, false)

	// A plain git-tracked file whose single-line content has the
	// "backend--rest" shape of an annex key, but is not one.
	writeRepoFile(t, root, "notes.txt", "hello--world")
	// A git-tracked symlink to an unrelated, non-annex target.
	if err := os.Symlink("notes.txt", filepath.Join(root, "link-to-notes")); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	wantNotesOID := strings.TrimSpace(run("git", "rev-parse", "HEAD:notes.txt"))
	wantLinkOID := strings.TrimSpace(run("git", "rev-parse", "HEAD:link-to-notes"))

	for _, reader := range []*Reader{NewStagedReader(root), NewRefReader(root, "HEAD")} {
		id, err := reader.ResolveContentID("notes.txt")
		if err != nil {
			t.Fatalf("ResolveContentID(notes.txt) error = %v", err)
		}
		if want := GitBlobEntityID(wantNotesOID); id != want {
			t.Errorf("ResolveContentID(notes.txt) = %q, want %q (a plain git blob, not an annex id)", id, want)
		}

		id, err = reader.ResolveContentID("link-to-notes")
		if err != nil {
			t.Fatalf("ResolveContentID(link-to-notes) error = %v", err)
		}
		if want := GitBlobEntityID(wantLinkOID); id != want {
			t.Errorf("ResolveContentID(link-to-notes) = %q, want %q (a plain git blob, not an annex id)", id, want)
		}
	}
}

// TestResolveContentIDDirectoryWithoutTrailingSlash covers review comment 2
// on !253: without a trailing slash, a path naming a directory must error
// rather than silently resolve to one of the files inside it.
func TestResolveContentIDDirectoryWithoutTrailingSlash(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "data/a.csv", "a\n")
	writeRepoFile(t, root, "data/b.csv", "b\n")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	for _, reader := range []*Reader{NewStagedReader(root), NewRefReader(root, "HEAD")} {
		_, err := reader.ResolveContentID("data")
		if err == nil {
			t.Fatalf("ResolveContentID(data) expected an error (data is a directory)")
		}
		if !strings.Contains(err.Error(), "is a directory") {
			t.Errorf("ResolveContentID(data) error = %q, want it to mention \"is a directory\"", err)
		}
	}
}

// TestResolveContentIDLiteralPathspec covers the --literal-pathspecs half
// of review comment 2: a path containing pathspec magic characters must be
// matched literally, not interpreted as a glob.
func TestResolveContentIDLiteralPathspec(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "weird[1].csv", "x\n")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	wantOID := strings.TrimSpace(run("git", "rev-parse", "HEAD:weird[1].csv"))
	id, err := NewRefReader(root, "HEAD").ResolveContentID("weird[1].csv")
	if err != nil {
		t.Fatalf("ResolveContentID() error = %v", err)
	}
	if want := GitBlobEntityID(wantOID); id != want {
		t.Errorf("ResolveContentID(weird[1].csv) = %q, want %q", id, want)
	}
}

func TestResolveContentIDFolderCollection(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "data/expr.tdb/f1", "fragment one")
	writeRepoFile(t, root, "data/expr.tdb/f2", "fragment two")
	run("git", "add", ".")

	staged, err := NewStagedReader(root).ResolveContentID("data/expr.tdb/")
	if err != nil {
		t.Fatalf("staged ResolveContentID() error = %v", err)
	}
	wantTree := strings.TrimSpace(run("git", "write-tree", "--prefix=data/expr.tdb/"))
	if staged != GitTreeEntityID(wantTree) {
		t.Errorf("staged folder id = %q, want %q", staged, GitTreeEntityID(wantTree))
	}

	run("git", "commit", "-q", "-m", "init")
	ref, err := NewRefReader(root, "HEAD").ResolveContentID("data/expr.tdb/")
	if err != nil {
		t.Fatalf("ref ResolveContentID() error = %v", err)
	}
	wantRefTree := strings.TrimSpace(run("git", "rev-parse", "HEAD:data/expr.tdb"))
	if ref != GitTreeEntityID(wantRefTree) {
		t.Errorf("ref folder id = %q, want %q", ref, GitTreeEntityID(wantRefTree))
	}
}

func TestResolveContentIDAnnexedFile(t *testing.T) {
	root, run := stateTestRepo(t, true)
	writeRepoFile(t, root, "data/big.bin", strings.Repeat("x", 4096))
	run("git", "annex", "add", "data/big.bin")
	run("git", "commit", "-q", "-m", "init")

	wantKey := strings.TrimSpace(run("git", "annex", "lookupkey", "data/big.bin"))

	for _, reader := range []*Reader{
		NewStagedReader(root),
		NewRefReader(root, "HEAD"),
	} {
		id, err := reader.ResolveContentID("data/big.bin")
		if err != nil {
			t.Fatalf("ResolveContentID() error = %v", err)
		}
		want := AnnexEntityID(wantKey)
		if id != want {
			t.Errorf("ResolveContentID() = %q, want %q", id, want)
		}
	}
}

func TestLoadConfigAcrossStates(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, ".exohub/config", "provenance:\n  require:\n    - \"results/**\"\n")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	// Uncommitted worktree edit.
	writeRepoFile(t, root, ".exohub/config", "provenance:\n  require:\n    - \"results/**\"\n    - \"models/**\"\n")

	worktreeCfg, err := NewWorktreeReader(root).LoadConfig()
	if err != nil {
		t.Fatalf("worktree LoadConfig() error = %v", err)
	}
	if worktreeCfg == nil || worktreeCfg.Provenance == nil || len(worktreeCfg.Provenance.Require) != 2 {
		t.Errorf("worktree config = %+v, want 2 require rules", worktreeCfg)
	}

	stagedCfg, err := NewStagedReader(root).LoadConfig()
	if err != nil {
		t.Fatalf("staged LoadConfig() error = %v", err)
	}
	if stagedCfg == nil || stagedCfg.Provenance == nil || len(stagedCfg.Provenance.Require) != 1 {
		t.Errorf("staged config = %+v, want 1 require rule (committed state)", stagedCfg)
	}

	refCfg, err := NewRefReader(root, "HEAD").LoadConfig()
	if err != nil {
		t.Fatalf("ref LoadConfig() error = %v", err)
	}
	if refCfg == nil || refCfg.Provenance == nil || len(refCfg.Provenance.Require) != 1 {
		t.Errorf("ref config = %+v, want 1 require rule", refCfg)
	}
}

func TestLoadConfigMissing(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "README.md", "hello\n")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	for _, reader := range []*Reader{
		NewWorktreeReader(root),
		NewStagedReader(root),
		NewRefReader(root, "HEAD"),
	} {
		cfg, err := reader.LoadConfig()
		if err != nil {
			t.Fatalf("LoadConfig() error = %v", err)
		}
		if cfg != nil {
			t.Errorf("LoadConfig() = %+v, want nil for missing config", cfg)
		}
	}
}
