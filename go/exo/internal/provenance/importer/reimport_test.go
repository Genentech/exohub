package importer

import (
	"time"

	lib "github.com/Genentech/exohub/go/exo/internal/provenance"

	"testing"
)

func TestFindExistingImport_NoneFound(t *testing.T) {
	root, _ := importerTestRepo(t)
	existing, err := FindExistingImport(root, "lid://none")
	if err != nil {
		t.Fatalf("FindExistingImport() error = %v", err)
	}
	if existing != nil {
		t.Errorf("existing = %+v, want nil", existing)
	}
}

func TestFindExistingImport_Found(t *testing.T) {
	root, run := importerTestRepo(t)
	reader := lib.NewWorktreeReader(root)
	writer := NewWriter(NewMatcher(reader))

	rec, _, err := writer.Write(Run{ID: "lid://0d1f0000", Name: "r"}, WriteOptions{
		AssertedBy:  lib.PersonAgentID("alice"),
		GeneratedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	path := writeAndStageRecord(t, root, rec)
	run("git", "add", path)

	existing, err := FindExistingImport(root, "lid://0d1f0000")
	if err != nil {
		t.Fatalf("FindExistingImport() error = %v", err)
	}
	if existing == nil {
		t.Fatal("existing = nil, want a match")
	}
	if existing.RecordID != rec.ID {
		t.Errorf("RecordID = %q, want %q", existing.RecordID, rec.ID)
	}
}

func TestFindExistingImport_SupersededIsIgnored(t *testing.T) {
	root, run := importerTestRepo(t)
	reader := lib.NewWorktreeReader(root)
	writer := NewWriter(NewMatcher(reader))

	first, _, err := writer.Write(Run{ID: "lid://0d1f0000", Name: "r"}, WriteOptions{
		AssertedBy:  lib.PersonAgentID("alice"),
		GeneratedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	path := writeAndStageRecord(t, root, first)
	run("git", "add", path)

	second, _, err := writer.Write(Run{ID: "lid://0d1f0000", Name: "r"}, WriteOptions{
		AssertedBy:  lib.PersonAgentID("alice"),
		GeneratedAt: time.Now().UTC(),
		Supersedes:  []string{first.ID},
	})
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	path2 := writeAndStageRecord(t, root, second)
	run("git", "add", path2)

	existing, err := FindExistingImport(root, "lid://0d1f0000")
	if err != nil {
		t.Fatalf("FindExistingImport() error = %v", err)
	}
	if existing == nil {
		t.Fatal("existing = nil, want the superseding record")
	}
	if existing.RecordID != second.ID {
		t.Errorf("RecordID = %q, want the superseding record %q", existing.RecordID, second.ID)
	}
}
