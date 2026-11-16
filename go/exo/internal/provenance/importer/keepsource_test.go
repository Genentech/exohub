package importer

import (
	"strings"
	"testing"
)

func TestKeepSource_StagesAsGitBlobs(t *testing.T) {
	root, run := importerTestRepo(t)

	runModel := Run{
		ID: "lid://0d1f0000",
		SourceDocuments: []SourceDocument{
			{Name: "lineage.json", Content: []byte(`{"run":"0d1f0000"}`)},
		},
	}

	paths, err := KeepSource(root, runModel)
	if err != nil {
		t.Fatalf("KeepSource() error = %v", err)
	}
	if len(paths) != 1 {
		t.Fatalf("got %d paths, want 1", len(paths))
	}
	if !strings.HasPrefix(paths[0], SourcesDir+"/") {
		t.Errorf("path %q is not under %s/", paths[0], SourcesDir)
	}
	if !strings.HasSuffix(paths[0], "/lineage.json") {
		t.Errorf("path %q does not end in /lineage.json", paths[0])
	}

	staged := run("git", "ls-files", "--", paths[0])
	if strings.TrimSpace(staged) != paths[0] {
		t.Errorf("source document was not staged: ls-files = %q", staged)
	}

	// It's staged as an ordinary git blob, never annexed: a plain `git add`
	// of the same unlocked content would be identical, so there's no annex
	// pointer file or symlink to check for -- confirm the staged blob holds
	// the literal content instead.
	blobID := gitBlobID(t, run, paths[0])
	content := run("git", "cat-file", "-p", blobID)
	if content != `{"run":"0d1f0000"}` {
		t.Errorf("staged content = %q, want the source document's content", content)
	}
}

func TestKeepSource_NoDocumentsIsNoop(t *testing.T) {
	root, _ := importerTestRepo(t)
	paths, err := KeepSource(root, Run{ID: "lid://x"})
	if err != nil {
		t.Fatalf("KeepSource() error = %v", err)
	}
	if paths != nil {
		t.Errorf("paths = %v, want nil", paths)
	}
}
