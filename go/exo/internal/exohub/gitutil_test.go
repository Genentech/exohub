package exohub

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// testRepo is a throwaway git repository used by the exohub package's
// resolver tests to exercise real git plumbing (ls-tree, cat-file, fetch)
// without requiring a real git-annex init or network access.
type testRepo struct {
	t    *testing.T
	Dir  string
	HEAD string
}

func newTestRepo(t *testing.T) *testRepo {
	t.Helper()
	dir := t.TempDir()
	r := &testRepo{t: t, Dir: dir}
	r.run("init", "-q")
	r.run("symbolic-ref", "HEAD", "refs/heads/main")
	r.run("config", "user.email", "test@example.com")
	r.run("config", "user.name", "Test")
	return r
}

func (r *testRepo) run(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", r.Dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// writeFile writes content to a plain, git-tracked file at relPath.
func (r *testRepo) writeFile(relPath, content string) {
	r.t.Helper()
	full := filepath.Join(r.Dir, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

// writeAnnexSymlink writes a symlink at relPath pointing at an
// annex-objects-style target for key, the same shape git-annex's locked
// mode produces, so annexKeyFromReference recognizes it.
func (r *testRepo) writeAnnexSymlink(relPath, key string) {
	r.t.Helper()
	full := filepath.Join(r.Dir, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		r.t.Fatal(err)
	}
	backend := backendFromKey(key)
	target := fmt.Sprintf("../.git/annex/objects/xx/yy/%s/%s/%s", key, backend, key)
	if err := os.Symlink(target, full); err != nil {
		r.t.Fatal(err)
	}
}

// commit stages everything and commits it, returning the new commit sha.
func (r *testRepo) commit(message string) string {
	r.t.Helper()
	r.run("add", "-A")
	r.run("commit", "-q", "-m", message)
	sha := r.run("rev-parse", "HEAD")
	return trimNL(sha)
}

// markPublished records branch's current commit as refs/remotes/origin/
// <branch>, simulating what a real local clone's origin-tracking ref would
// be after a fetch/pull: ResolveLocalClone only trusts a clone's
// origin-tracking refs (or tags, or a literal commit sha) for the version
// it resolves, never the clone's own local branch tip (exo-cli#152
// review), so tests that want `ref.Version` to resolve must mark it
// published this way instead of relying on the local branch alone.
func (r *testRepo) markPublished(branch string) {
	r.t.Helper()
	r.run("update-ref", "refs/remotes/origin/"+branch, "refs/heads/"+branch)
}

func trimNL(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
