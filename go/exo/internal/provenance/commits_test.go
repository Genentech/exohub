package provenance

import (
	"strings"
	"testing"
)

func TestCommitHistory_ListsCommitsNewestFirst(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "data.csv", "v1")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "first")

	writeRepoFile(t, root, "data.csv", "v2")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "second")

	commits, err := CommitHistory(root, "HEAD", "data.csv")
	if err != nil {
		t.Fatalf("CommitHistory() error = %v", err)
	}
	if len(commits) != 2 {
		t.Fatalf("len(commits) = %d, want 2: %+v", len(commits), commits)
	}
	if commits[0].Subject != "second" || commits[1].Subject != "first" {
		t.Errorf("commits = %+v, want second then first", commits)
	}
	if commits[0].Author == "" || commits[0].SHA == "" || commits[0].Date == "" {
		t.Errorf("commits[0] = %+v, want author, sha and date set", commits[0])
	}
}

func TestCommitHistory_FollowsRenames(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "old.csv", "same content")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "create")

	run("git", "mv", "old.csv", "new.csv")
	run("git", "commit", "-q", "-m", "rename")

	commits, err := CommitHistory(root, "HEAD", "new.csv")
	if err != nil {
		t.Fatalf("CommitHistory() error = %v", err)
	}
	if len(commits) != 2 {
		t.Fatalf("len(commits) = %d, want 2 (create and rename): %+v", len(commits), commits)
	}
}

func TestCommitHistory_NoHistory(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "other.csv", "x")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	commits, err := CommitHistory(root, "HEAD", "never-existed.csv")
	if err != nil {
		t.Fatalf("CommitHistory() error = %v", err)
	}
	if len(commits) != 0 {
		t.Errorf("commits = %+v, want none", commits)
	}
}

func TestBatchTreeIDsAt_ResolvesEachCommit(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "data/expr.tdb/fragment1", "f1")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "first")
	firstSHA := strings.TrimSpace(run("git", "rev-parse", "HEAD"))

	writeRepoFile(t, root, "data/expr.tdb/fragment2", "f2")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "second")
	secondSHA := strings.TrimSpace(run("git", "rev-parse", "HEAD"))

	ids, err := batchTreeIDsAt(root, []string{secondSHA, firstSHA}, "data/expr.tdb")
	if err != nil {
		t.Fatalf("batchTreeIDsAt() error = %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("len(ids) = %d, want 2: %+v", len(ids), ids)
	}
	if ids[firstSHA] == ids[secondSHA] {
		t.Errorf("tree ids equal across commits that added a member: %+v", ids)
	}
}

func TestBatchTreeIDsAt_MissingPathOmitted(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "other.csv", "x")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")
	sha := strings.TrimSpace(run("git", "rev-parse", "HEAD"))

	ids, err := batchTreeIDsAt(root, []string{sha}, "never/existed")
	if err != nil {
		t.Fatalf("batchTreeIDsAt() error = %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("ids = %+v, want none (path never existed)", ids)
	}
}

func TestCommitHistory_FolderPath(t *testing.T) {
	root, run := stateTestRepo(t, false)
	writeRepoFile(t, root, "data/expr.tdb/fragment1", "f1")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")

	writeRepoFile(t, root, "data/expr.tdb/fragment2", "f2")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "second")

	commits, err := CommitHistory(root, "HEAD", "data/expr.tdb/")
	if err != nil {
		t.Fatalf("CommitHistory() error = %v", err)
	}
	if len(commits) != 2 {
		t.Fatalf("len(commits) = %d, want 2: %+v", len(commits), commits)
	}
}
