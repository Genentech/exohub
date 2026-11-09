package add

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Genentech/exohub/go/exo/internal/exohub"
)

func TestExohubTargetPath(t *testing.T) {
	asset, err := exohub.ParseReference("exohub:myproject:data/file.csv@main")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := exohubTargetPath(asset, exohub.ResolvedFile{Path: "data/file.csv"}, ""); err != nil || got != "file.csv" {
		t.Errorf("asset default = (%q, %v), want (%q, nil)", got, err, "file.csv")
	}
	// --to not ending in "/" and not an existing directory is the exact
	// target file path (cp convention).
	if got, err := exohubTargetPath(asset, exohub.ResolvedFile{Path: "data/file.csv"}, "renamed.csv"); err != nil || got != "renamed.csv" {
		t.Errorf("asset --to file = (%q, %v), want (%q, nil)", got, err, "renamed.csv")
	}
	// --to ending in "/" places the base name inside it, even if the
	// directory doesn't exist yet.
	if got, err := exohubTargetPath(asset, exohub.ResolvedFile{Path: "data/file.csv"}, "out/"); err != nil || got != filepath.Join("out", "file.csv") {
		t.Errorf("asset --to trailing slash = (%q, %v), want (%q, nil)", got, err, filepath.Join("out", "file.csv"))
	}
	// --to naming an existing directory also places the base name inside it.
	existingDir := t.TempDir()
	if got, err := exohubTargetPath(asset, exohub.ResolvedFile{Path: "data/file.csv"}, existingDir); err != nil || got != filepath.Join(existingDir, "file.csv") {
		t.Errorf("asset --to existing dir = (%q, %v), want (%q, nil)", got, err, filepath.Join(existingDir, "file.csv"))
	}

	folder, err := exohub.ParseReference("exohub:myproject:data/@main")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := exohubTargetPath(folder, exohub.ResolvedFile{Path: "data/a/b.csv"}, "mydata"); err != nil || got != filepath.Join("mydata", "a/b.csv") {
		t.Errorf("folder --to = (%q, %v)", got, err)
	}
	if got, err := exohubTargetPath(folder, exohub.ResolvedFile{Path: "data/a/b.csv"}, ""); err != nil || got != filepath.Join(".", "a/b.csv") {
		t.Errorf("folder default = (%q, %v)", got, err)
	}
	// A catalog/manifest path with a ".." component must never escape --to.
	if _, err := exohubTargetPath(folder, exohub.ResolvedFile{Path: "data/../../etc/passwd"}, "mydata"); err == nil {
		t.Error("expected an error for a path escaping --to via \"..\"")
	}

	project, err := exohub.ParseReference("exohub:myproject@main")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := exohubTargetPath(project, exohub.ResolvedFile{Path: "data/a.csv"}, ""); err != nil || got != filepath.Join("myproject", "data/a.csv") {
		t.Errorf("project default = (%q, %v)", got, err)
	}
	if got, err := exohubTargetPath(project, exohub.ResolvedFile{Path: "data/a.csv"}, "vendor"); err != nil || got != filepath.Join("vendor", "data/a.csv") {
		t.Errorf("project --to = (%q, %v)", got, err)
	}
	// An absolute catalog path must never be allowed to write outside --to.
	if _, err := exohubTargetPath(project, exohub.ResolvedFile{Path: "/etc/passwd"}, "vendor"); err == nil {
		t.Error("expected an error for an absolute catalog path")
	}

	collection, err := exohub.ParseReference("exohub:myproject:collection:qc@main")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := exohubTargetPath(collection, exohub.ResolvedFile{Path: "README.md"}, "out"); err != nil || got != filepath.Join("out", "README.md") {
		t.Errorf("collection --to = (%q, %v)", got, err)
	}
	// A collection manifest path escaping --to via ".." must be rejected.
	if _, err := exohubTargetPath(collection, exohub.ResolvedFile{Path: "../../etc/passwd"}, "out"); err == nil {
		t.Error("expected an error for a collection member path escaping --to")
	}
}

func TestSplitArgsExohubReference(t *testing.T) {
	locals, urls, refs, flags, _, toDir, repoURL, _, _, _, _, verify, err := splitArgs([]string{
		"exohub:myproject:data/file.csv@main", "--to", "out.csv", "--verify", "--repo", "git@example.com:group/myproject.git",
	})
	if err != nil {
		t.Fatalf("splitArgs() error = %v", err)
	}
	if len(locals) != 0 || len(urls) != 0 {
		t.Errorf("locals=%v urls=%v, want none", locals, urls)
	}
	if len(flags) != 0 {
		t.Errorf("flags = %v, want none (--to/--verify/--repo consumed)", flags)
	}
	if len(refs) != 1 || refs[0].Project != "myproject" {
		t.Fatalf("refs = %+v", refs)
	}
	if toDir != "out.csv" {
		t.Errorf("toDir = %q, want %q", toDir, "out.csv")
	}
	if repoURL != "git@example.com:group/myproject.git" {
		t.Errorf("repoURL = %q", repoURL)
	}
	if !verify {
		t.Error("verify = false, want true")
	}
}

func TestSplitArgsToRequiresValue(t *testing.T) {
	if _, _, _, _, _, _, _, _, _, _, _, _, err := splitArgs([]string{"--to"}); err == nil {
		t.Fatal("expected an error for --to with no value")
	}
}

func TestSplitArgsRepoRequiresExohubReference(t *testing.T) {
	cmd := NewCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--repo", "git@example.com:group/myproject.git", "local.csv"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected an error using --repo without an exohub: reference")
	}
}

func TestSplitArgsExohubRejectsLocalMix(t *testing.T) {
	cmd := NewCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"exohub:myproject:data/file.csv@main", "local.csv"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected an error mixing an exohub: reference with a local path")
	}
}

func TestSplitArgsExohubRejectsExplain(t *testing.T) {
	cmd := NewCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--explain", "exohub:myproject:data/file.csv@main"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected an error combining --explain with an exohub: reference")
	}
}

// --- End-to-end materialisation tests (design D3), using real git/git-annex ---

type testGitRepo struct {
	t   *testing.T
	Dir string
}

func newTestGitRepo(t *testing.T) *testGitRepo {
	t.Helper()
	dir := t.TempDir()
	// git-annex makes its object directories read-only; make sure
	// t.TempDir()'s own cleanup (registered above, so it runs after this
	// one) can still remove everything.
	t.Cleanup(func() {
		_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
			if err != nil || info == nil {
				return nil
			}
			return os.Chmod(p, info.Mode()|0o700)
		})
	})
	r := &testGitRepo{t: t, Dir: dir}
	r.run("init", "-q")
	r.run("symbolic-ref", "HEAD", "refs/heads/main")
	r.run("config", "user.email", "test@example.com")
	r.run("config", "user.name", "Test")
	return r
}

func (r *testGitRepo) run(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", r.Dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// markPublished records branch's current commit as refs/remotes/origin/
// <branch>, simulating a real clone's origin-tracking ref. ResolveLocalClone
// only trusts a clone's origin-tracking refs (or tags, or a literal commit
// sha) for the version it resolves, never the clone's own local branch tip
// (exo-cli#152 review), so tests resolving a plain branch name like "main"
// against a local clone must call this after committing.
func (r *testGitRepo) markPublished(branch string) {
	r.t.Helper()
	r.run("update-ref", "refs/remotes/origin/"+branch, "refs/heads/"+branch)
}

// withDir chdirs into dir for the duration of the calling test, restoring
// the previous working directory afterwards (exo add's git/git-annex
// plumbing always operates on the process's current directory).
func withDir(t *testing.T, dir string) {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
}

func TestMaterializeGitFile(t *testing.T) {
	source := newTestGitRepo(t)
	if err := os.WriteFile(filepath.Join(source.Dir, "README.md"), []byte("hello from source\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source.run("add", "-A")
	source.run("commit", "-q", "-m", "initial")
	source.markPublished("main")

	ref, err := exohub.ParseReference("exohub:myproject:README.md@main")
	if err != nil {
		t.Fatal(err)
	}
	res, err := exohub.ResolveLocalClone(source.Dir, ref)
	if err != nil {
		t.Fatalf("ResolveLocalClone() error = %v", err)
	}
	defer res.Close()
	if len(res.Files) != 1 || res.Files[0].Kind != exohub.ContentGit {
		t.Fatalf("Files = %+v, want one ContentGit file", res.Files)
	}

	importer := newTestGitRepo(t)
	withDir(t, importer.Dir)

	target := "README.md"
	if err := materializeFile(res, res.Files[0], target); err != nil {
		t.Fatalf("materializeFile() error = %v", err)
	}
	content, err := os.ReadFile(filepath.Join(importer.Dir, target))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "hello from source\n" {
		t.Errorf("content = %q, want %q", content, "hello from source\n")
	}
	// Staged, like a plain `git add`.
	out := importer.run("diff", "--cached", "--name-only")
	if strings.TrimSpace(out) != target {
		t.Errorf("staged files = %q, want %q", out, target)
	}
}

func TestMaterializeAnnexFileZ3LocalCopy(t *testing.T) {
	if _, err := exec.LookPath("git-annex"); err != nil {
		t.Skip("git-annex not in PATH")
	}
	// A real git-annex source: materializeByCopy's local-clone fast path
	// (`git annex contentlocation`) needs a genuinely annexed file.
	source := newTestGitRepo(t)
	source.run("annex", "init", "-q", "source-repo")
	if err := os.WriteFile(filepath.Join(source.Dir, "data.bin"), []byte("binary content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source.run("annex", "add", "data.bin")
	source.run("commit", "-q", "-m", "initial")
	source.markPublished("main")

	ref, err := exohub.ParseReference("exohub:myproject:data.bin@main")
	if err != nil {
		t.Fatal(err)
	}
	res, err := exohub.ResolveLocalClone(source.Dir, ref)
	if err != nil {
		t.Fatalf("ResolveLocalClone() error = %v", err)
	}
	defer res.Close()
	if len(res.Files) != 1 || res.Files[0].Kind != exohub.ContentAnnex {
		t.Fatalf("Files = %+v, want one ContentAnnex file", res.Files)
	}
	// No catalog locations are known for a local-clone resolution, so Z1
	// is not attempted; materializeAnnexFile must fall back to Z3, reading
	// the content directly out of the source clone's own annex store.
	if len(res.Files[0].Locations) != 0 {
		t.Fatalf("Locations = %+v, want none", res.Files[0].Locations)
	}

	importer := newTestGitRepo(t)
	importer.run("annex", "init", "-q", "importer-repo")
	withDir(t, importer.Dir)

	target := "data.bin"
	if err := materializeFile(res, res.Files[0], target); err != nil {
		t.Fatalf("materializeFile() error = %v", err)
	}

	content, err := os.ReadFile(filepath.Join(importer.Dir, target))
	if err != nil {
		t.Fatalf("read materialized file: %v", err)
	}
	if string(content) != "binary content\n" {
		t.Errorf("content = %q, want %q", content, "binary content\n")
	}

	key := importer.run("annex", "lookupkey", target)
	if strings.TrimSpace(key) != res.Files[0].Key {
		t.Errorf("imported key = %q, want %q (same key as the source)", strings.TrimSpace(key), res.Files[0].Key)
	}
}

// TestMaterializeGitFileNoClobber and TestMaterializeAnnexFileNoClobber
// cover exo-cli#152 review item 3: materialisation must never silently
// overwrite a file that is already at the target path, whether or not it
// happens to already hold the same content/key (a no-op) or something
// different (a hard error naming the conflicting path).
func TestMaterializeGitFileNoClobber(t *testing.T) {
	source := newTestGitRepo(t)
	if err := os.WriteFile(filepath.Join(source.Dir, "README.md"), []byte("hello from source\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source.run("add", "-A")
	source.run("commit", "-q", "-m", "initial")
	source.markPublished("main")

	ref, err := exohub.ParseReference("exohub:myproject:README.md@main")
	if err != nil {
		t.Fatal(err)
	}
	res, err := exohub.ResolveLocalClone(source.Dir, ref)
	if err != nil {
		t.Fatalf("ResolveLocalClone() error = %v", err)
	}
	defer res.Close()

	importer := newTestGitRepo(t)
	withDir(t, importer.Dir)
	target := "README.md"

	// Same content already at the target: a no-op, not an error.
	if err := os.WriteFile(target, []byte("hello from source\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := materializeFile(res, res.Files[0], target); err != nil {
		t.Fatalf("materializeFile() with identical existing content, error = %v", err)
	}

	// Different content already at the target: a hard error, and the file
	// must be left untouched.
	if err := os.WriteFile(target, []byte("a pre-existing, unrelated file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = materializeFile(res, res.Files[0], target)
	if err == nil {
		t.Fatal("expected an error when the target already has different content")
	}
	if !strings.Contains(err.Error(), target) {
		t.Errorf("error = %v, want it to name the conflicting path %q", err, target)
	}
	content, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(content) != "a pre-existing, unrelated file\n" {
		t.Errorf("target was modified despite the conflict: %q", content)
	}
}

func TestMaterializeAnnexFileNoClobber(t *testing.T) {
	if _, err := exec.LookPath("git-annex"); err != nil {
		t.Skip("git-annex not in PATH")
	}
	source := newTestGitRepo(t)
	source.run("annex", "init", "-q", "source-repo")
	if err := os.WriteFile(filepath.Join(source.Dir, "data.bin"), []byte("binary content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source.run("annex", "add", "data.bin")
	source.run("commit", "-q", "-m", "initial")
	source.markPublished("main")

	ref, err := exohub.ParseReference("exohub:myproject:data.bin@main")
	if err != nil {
		t.Fatal(err)
	}
	res, err := exohub.ResolveLocalClone(source.Dir, ref)
	if err != nil {
		t.Fatalf("ResolveLocalClone() error = %v", err)
	}
	defer res.Close()

	importer := newTestGitRepo(t)
	importer.run("annex", "init", "-q", "importer-repo")
	withDir(t, importer.Dir)
	target := "data.bin"

	// First materialisation succeeds normally.
	if err := materializeFile(res, res.Files[0], target); err != nil {
		t.Fatalf("materializeFile() error = %v", err)
	}
	// Materializing the same key again at the same path is a no-op.
	if err := materializeFile(res, res.Files[0], target); err != nil {
		t.Fatalf("materializeFile() re-run with the same key, error = %v", err)
	}

	// A genuinely different, unrelated file at the target must never be
	// clobbered by `git annex fromkey --force`.
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("a pre-existing, unrelated file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = materializeFile(res, res.Files[0], target)
	if err == nil {
		t.Fatal("expected an error when the target already has unrelated content")
	}
	if !strings.Contains(err.Error(), target) {
		t.Errorf("error = %v, want it to name the conflicting path %q", err, target)
	}
	content, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(content) != "a pre-existing, unrelated file\n" {
		t.Errorf("target was modified despite the conflict: %q", content)
	}
}

func TestRunExohubAddRepoFallback(t *testing.T) {
	withLoginToken(t, fakeJWT(`{"preferred_username":"bob","name":"Bob Smith"}`))
	source := newTestGitRepo(t)
	if err := os.WriteFile(filepath.Join(source.Dir, "README.md"), []byte("from --repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source.run("add", "-A")
	source.run("commit", "-q", "-m", "initial")

	importer := newTestGitRepo(t)
	withDir(t, importer.Dir)

	// No local clone discoverable under this made-up project name, and no
	// reachable catalog: only --repo can resolve this reference.
	t.Setenv("EXOHUB_CLONE_REPO_FALLBACK_PROJECT_XYZ", "")
	t.Setenv("EXOHUB_CATALOG_URL", "http://127.0.0.1:0")

	cmd := NewCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"exohub:repo-fallback-project-xyz:README.md@main", "--repo", source.Dir})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("exo add exohub:...@main --repo %s: %v\n%s", source.Dir, err, out.String())
	}

	content, err := os.ReadFile(filepath.Join(importer.Dir, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "from --repo\n" {
		t.Errorf("content = %q, want %q", content, "from --repo\n")
	}
}

func TestRunExohubAddEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("git-annex"); err != nil {
		t.Skip("git-annex not in PATH")
	}
	withLoginToken(t, fakeJWT(`{"preferred_username":"bob","name":"Bob Smith"}`))
	source := newTestGitRepo(t)
	if err := os.WriteFile(filepath.Join(source.Dir, "README.md"), []byte("doc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source.run("annex", "init", "-q", "source-repo")
	if err := os.WriteFile(filepath.Join(source.Dir, "data.bin"), []byte("payload\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source.run("annex", "add", "data.bin")
	source.run("add", "README.md")
	source.run("commit", "-q", "-m", "initial")
	source.markPublished("main")

	importer := newTestGitRepo(t)
	importer.run("annex", "init", "-q", "importer-repo")
	withDir(t, importer.Dir)

	t.Setenv("EXOHUB_CLONE_MYPROJECT", source.Dir)

	cmd := NewCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"exohub:myproject@main", "--to", "vendor"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("exo add exohub:myproject@main --to vendor: %v\n%s", err, out.String())
	}

	for _, rel := range []string{"README.md", "data.bin"} {
		if _, err := os.Stat(filepath.Join(importer.Dir, "vendor", rel)); err != nil {
			t.Errorf("vendor/%s missing: %v", rel, err)
		}
	}
	binKey := importer.run("annex", "lookupkey", filepath.Join("vendor", "data.bin"))
	if strings.TrimSpace(binKey) == "" {
		t.Error("vendor/data.bin has no annex key")
	}
}
