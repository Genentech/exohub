package commandutil

import (
	"os"
	"os/exec"
	"testing"
)

func setupGitRepo(t *testing.T) (dir string, run func(args ...string)) {
	t.Helper()
	dir = t.TempDir()
	run = func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+dir)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("command %v failed: %v\n%s", args, err, out)
		}
	}
	run("git", "init")
	return dir, run
}

func chdir(t *testing.T, dir string) {
	t.Helper()
	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { os.Chdir(oldDir) }) //nolint:errcheck
}

func TestResolveCloneURLPrefersOrigin(t *testing.T) {
	dir, run := setupGitRepo(t)
	run("git", "remote", "add", "upstream", "https://example.com/upstream/repo.git")
	run("git", "remote", "add", "origin", "https://example.com/org/repo.git")
	chdir(t, dir)

	url, err := ResolveCloneURL()
	if err != nil {
		t.Fatalf("ResolveCloneURL: %v", err)
	}
	if url != "https://example.com/org/repo.git" {
		t.Errorf("url = %q, want origin's URL", url)
	}
}

func TestResolveCloneURLSkipsLocalOrigin(t *testing.T) {
	dir, run := setupGitRepo(t)
	run("git", "remote", "add", "origin", "/srv/git/repo.git")
	run("git", "remote", "add", "gitlab", "https://example.com/org/repo.git")
	chdir(t, dir)

	url, err := ResolveCloneURL()
	if err != nil {
		t.Fatalf("ResolveCloneURL: %v", err)
	}
	if url != "https://example.com/org/repo.git" {
		t.Errorf("url = %q, want the https remote, never the local path", url)
	}
}

func TestResolveCloneURLNonOriginRemote(t *testing.T) {
	dir, run := setupGitRepo(t)
	run("git", "remote", "add", "upstream", "https://example.com/org/repo.git")
	chdir(t, dir)

	url, err := ResolveCloneURL()
	if err != nil {
		t.Fatalf("ResolveCloneURL: %v", err)
	}
	if url != "https://example.com/org/repo.git" {
		t.Errorf("url = %q, want %q", url, "https://example.com/org/repo.git")
	}
}

func TestResolveCloneURLScpStyleNonGitUser(t *testing.T) {
	dir, run := setupGitRepo(t)
	run("git", "remote", "add", "origin", "gitlab@example.com:org/repo.git")
	chdir(t, dir)

	url, err := ResolveCloneURL()
	if err != nil {
		t.Fatalf("ResolveCloneURL: %v", err)
	}
	if url != "gitlab@example.com:org/repo.git" {
		t.Errorf("url = %q, want %q", url, "gitlab@example.com:org/repo.git")
	}
}

func TestResolveCloneURLInsteadOfAlias(t *testing.T) {
	dir, run := setupGitRepo(t)
	run("git", "config", "url.https://example.com/.insteadOf", "gl:")
	run("git", "remote", "add", "origin", "gl:org/repo.git")
	chdir(t, dir)

	url, err := ResolveCloneURL()
	if err != nil {
		t.Fatalf("ResolveCloneURL: %v", err)
	}
	if url != "https://example.com/org/repo.git" {
		t.Errorf("url = %q, want the insteadOf-expanded URL", url)
	}
}

func TestResolveCloneURLLocalPathOnlyErrors(t *testing.T) {
	dir, run := setupGitRepo(t)
	run("git", "remote", "add", "origin", "/srv/git/repo.git")
	chdir(t, dir)

	if url, err := ResolveCloneURL(); err == nil {
		t.Fatalf("expected an error for a local-path-only remote, got url=%q", url)
	}
}

func TestResolveCloneURLNoRemotesErrors(t *testing.T) {
	dir, _ := setupGitRepo(t)
	chdir(t, dir)

	if url, err := ResolveCloneURL(); err == nil {
		t.Fatalf("expected an error when no remotes are configured, got url=%q", url)
	}
}

func TestIsCloneableURL(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"https://example.com/org/repo.git", true},
		{"http://example.com/org/repo.git", true},
		{"ssh://git@example.com/org/repo.git", true},
		{"git://example.com/org/repo.git", true},
		{"git@example.com:org/repo.git", true},
		{"gitlab@example.com:org/repo.git", true},
		{"file:///srv/git/repo.git", false},
		{"/srv/git/repo.git", false},
		{"./repo.git", false},
		{"../repo.git", false},
		{"C:\\Users\\me\\repo.git", false},
		{"s3://bucket/repo", false},
		{"ftp://h/r", false},
		{"rclone://remote/path", false},
		{"annex::f00?type=directory", false},
		{"datalad-annex::?type=s3", false},
	}
	for _, tt := range tests {
		if got := isCloneableURL(tt.url); got != tt.want {
			t.Errorf("isCloneableURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}
