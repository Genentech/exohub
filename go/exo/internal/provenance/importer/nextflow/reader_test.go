package nextflow

import (
	"strings"
	"testing"
	"time"
)

func minimalWorkflowRun(name string) map[string]any {
	return map[string]any{
		"type":    "WorkflowRun",
		"runName": name,
	}
}

func TestRead_UnsupportedVersion(t *testing.T) {
	dir := fixtureStore(t, "lineage/v2", []indexRun{{ID: "lid://aaaa", Name: "run1"}}, map[string]any{
		"lid://aaaa": minimalWorkflowRun("run1"),
	})

	_, err := Read(dir, "")
	if err == nil {
		t.Fatal("Read() error = nil, want an unsupported-version error")
	}
	want := "unsupported Nextflow lineage version lineage/v2 (supported: lineage/v1beta1)"
	if err.Error() != want {
		t.Errorf("err = %q, want %q", err.Error(), want)
	}
}

func TestRead_MissingVersion(t *testing.T) {
	dir := fixtureStore(t, "", nil, nil)
	_, err := Read(dir, "")
	if err == nil || !strings.Contains(err.Error(), "unsupported Nextflow lineage version (none)") {
		t.Errorf("err = %v, want a missing-version error", err)
	}
}

func TestRead_NoStoreAtPath(t *testing.T) {
	_, err := Read(t.TempDir(), "")
	if err == nil || !strings.Contains(err.Error(), "no Nextflow lineage store found") {
		t.Errorf("err = %v, want a no-store error", err)
	}
}

func TestRead_SelectByLID(t *testing.T) {
	dir := fixtureStore(t, supportedVersion, []indexRun{
		{ID: "lid://aaaa", Name: "run1"},
		{ID: "lid://bbbb", Name: "run2"},
	}, map[string]any{
		"lid://aaaa": minimalWorkflowRun("run1"),
		"lid://bbbb": minimalWorkflowRun("run2"),
	})

	run, err := Read(dir, "lid://bbbb")
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if run.ID != "lid://bbbb" || run.Name != "run2" {
		t.Errorf("got ID=%q Name=%q, want lid://bbbb/run2", run.ID, run.Name)
	}
}

func TestRead_SelectByName(t *testing.T) {
	dir := fixtureStore(t, supportedVersion, []indexRun{
		{ID: "lid://aaaa", Name: "happy_curie"},
		{ID: "lid://bbbb", Name: "sad_lovelace"},
	}, map[string]any{
		"lid://aaaa": minimalWorkflowRun("happy_curie"),
		"lid://bbbb": minimalWorkflowRun("sad_lovelace"),
	})

	run, err := Read(dir, "happy_curie")
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if run.ID != "lid://aaaa" {
		t.Errorf("ID = %q, want lid://aaaa", run.ID)
	}
}

func TestRead_UnknownRun(t *testing.T) {
	dir := fixtureStore(t, supportedVersion, []indexRun{{ID: "lid://aaaa", Name: "run1"}}, map[string]any{
		"lid://aaaa": minimalWorkflowRun("run1"),
	})

	_, err := Read(dir, "no-such-run")
	if err == nil || !strings.Contains(err.Error(), `no run "no-such-run"`) {
		t.Errorf("err = %v, want a no-such-run error", err)
	}
}

func TestRead_SourceDocumentsCoverEveryRecordRead(t *testing.T) {
	dir := fixtureStore(t, supportedVersion,
		[]indexRun{{ID: "lid://run1", Name: "r"}},
		map[string]any{
			"lid://run1": map[string]any{
				"type":    "WorkflowRun",
				"runName": "r",
				"tasks":   []string{"lid://task1"},
			},
			"lid://task1": map[string]any{
				"type":    "TaskRun",
				"name":    "t",
				"inputs":  []string{"lid://in1"},
				"outputs": []string{"lid://out1"},
			},
			"lid://in1":  map[string]any{"type": "FileOutput", "path": "data/reads.fq"},
			"lid://out1": map[string]any{"type": "FileOutput", "path": "results/out.txt"},
		})

	run, err := Read(dir, "")
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if len(run.SourceDocuments) != 4 {
		t.Fatalf("got %d source documents, want 4 (run, task, in, out)", len(run.SourceDocuments))
	}
	names := map[string]bool{}
	for _, doc := range run.SourceDocuments {
		names[doc.Name] = true
		if len(doc.Content) == 0 {
			t.Errorf("source document %s has no content", doc.Name)
		}
	}
	for _, want := range []string{"run1.json", "task1.json", "in1.json", "out1.json"} {
		if !names[want] {
			t.Errorf("source documents %v missing %s", names, want)
		}
	}
}

func TestRead_DefaultsToLatestRun(t *testing.T) {
	dir := fixtureStore(t, supportedVersion, []indexRun{
		{ID: "lid://old", Name: "earlier", StartTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
		{ID: "lid://new", Name: "later", StartTime: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)},
	}, map[string]any{
		"lid://old": minimalWorkflowRun("earlier"),
		"lid://new": minimalWorkflowRun("later"),
	})

	run, err := Read(dir, "")
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if run.ID != "lid://new" {
		t.Errorf("ID = %q, want lid://new (the later run)", run.ID)
	}
}
