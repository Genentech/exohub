package init

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMergeRemoteFieldsIntoFileAddsField(t *testing.T) {
	chdirTemp(t)

	src := "remotes:\n" +
		"  - name: store\n" +
		"    type: annex\n" +
		"    s3url: s3://bucket/ds/annex/\n" +
		"  - name: atlas\n" +
		"    type: artifactdb\n" +
		"    mode: export\n" +
		"    s3url: s3://bucket/ds/_catalog/\n"
	path := filepath.Join(t.TempDir(), "remotes")
	if err := os.WriteFile(path, []byte(src), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	rendered := RemoteConfig{Name: "atlas", Type: "artifactdb", Mode: "export", S3URL: "s3://bucket/ds/_catalog/", AtlasURL: "https://atlas.example.com/"}
	entries := []driftEntry{{Remote: "atlas", Field: "atlas_url", Profile: "https://atlas.example.com/", Status: "added"}}

	if err := mergeRemoteFieldsIntoFile(path, "atlas", rendered, entries); err != nil {
		t.Fatalf("mergeRemoteFieldsIntoFile: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	want := src + "    atlas_url: https://atlas.example.com/\n"
	if string(got) != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestMergeRemoteFieldsIntoFileReplacesExistingValue(t *testing.T) {
	chdirTemp(t)

	src := "remotes:\n" +
		"  - name: atlas\n" +
		"    type: artifactdb\n" +
		"    mode: export\n" +
		"    s3url: s3://bucket/ds/_catalog/\n" +
		"    instance_url: https://old-instance.example.com\n"
	path := filepath.Join(t.TempDir(), "remotes")
	if err := os.WriteFile(path, []byte(src), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	rendered := RemoteConfig{Name: "atlas", Type: "artifactdb", Mode: "export", S3URL: "s3://bucket/ds/_catalog/", InstanceURL: "https://new-instance.example.com"}
	entries := []driftEntry{{Remote: "atlas", Field: "instance_url", Current: "https://old-instance.example.com", Profile: "https://new-instance.example.com", Status: "changed"}}

	if err := mergeRemoteFieldsIntoFile(path, "atlas", rendered, entries); err != nil {
		t.Fatalf("mergeRemoteFieldsIntoFile: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	want := "remotes:\n" +
		"  - name: atlas\n" +
		"    type: artifactdb\n" +
		"    mode: export\n" +
		"    s3url: s3://bucket/ds/_catalog/\n" +
		"    instance_url: https://new-instance.example.com\n"
	if string(got) != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestMergeRemoteFieldsIntoFileOtherRemotesUntouched(t *testing.T) {
	chdirTemp(t)

	src := "remotes:\n" +
		"  # primary annex store\n" +
		"  - name: store\n" +
		"    type: annex\n" +
		"    s3url: s3://bucket/ds/annex/\n" +
		"    owner_note: keep-this # hand-added key\n" +
		"  - name: atlas\n" +
		"    type: artifactdb\n" +
		"    mode: export\n" +
		"    s3url: \"s3://bucket/ds/_catalog/\"\n"
	path := filepath.Join(t.TempDir(), "remotes")
	if err := os.WriteFile(path, []byte(src), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	rendered := RemoteConfig{Name: "atlas", Type: "artifactdb", Mode: "export", S3URL: "s3://bucket/ds/_catalog/", AtlasURL: "https://atlas.example.com/"}
	entries := []driftEntry{{Remote: "atlas", Field: "atlas_url", Profile: "https://atlas.example.com/", Status: "added"}}

	if err := mergeRemoteFieldsIntoFile(path, "atlas", rendered, entries); err != nil {
		t.Fatalf("mergeRemoteFieldsIntoFile: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	want := src + "    atlas_url: https://atlas.example.com/\n"
	if string(got) != want {
		t.Errorf("comment/unknown-key/quoting not preserved for the untouched remote:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestMergeRemoteFieldsIntoFileUnknownRemoteErrors(t *testing.T) {
	chdirTemp(t)

	src := "remotes:\n  - name: other\n    type: annex\n    s3url: s3://bucket/_annex\n"
	path := filepath.Join(t.TempDir(), "remotes")
	if err := os.WriteFile(path, []byte(src), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	err := mergeRemoteFieldsIntoFile(path, "atlas", RemoteConfig{Name: "atlas"}, []driftEntry{{Field: "s3url", Status: "added"}})
	if err == nil {
		t.Fatal("expected error for unknown remote")
	}
}
