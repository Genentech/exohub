package init

import (
	"strings"
	"testing"
)

func TestComputeDriftEntriesAddedAndLocalOnly(t *testing.T) {
	spec := &ProfileSpec{
		Name:    "my-profile",
		Prompts: []PromptSpec{},
		Templates: map[string]string{
			"remotes": "remotes:\n  - name: exohub-atlas\n    type: artifactdb\n    mode: export\n    s3url: s3://bucket/_catalog\n    atlas_url: https://atlas.example.com\n",
		},
	}
	remotesConfig := &RemotesConfig{Remotes: []RemoteConfig{
		{Name: "exohub-atlas", Type: "artifactdb", Mode: "export", S3URL: "s3://bucket/_catalog", InstanceURL: "https://local-only.example.com"},
	}}

	entries, err := computeDriftEntries(spec, remotesConfig, "")
	if err != nil {
		t.Fatalf("computeDriftEntries: %v", err)
	}

	var sawAdded, sawLocalOnly bool
	for _, e := range entries {
		if e.Remote != "exohub-atlas" {
			t.Errorf("unexpected remote in entry: %+v", e)
		}
		switch e.Field {
		case "atlas_url":
			if e.Status != "added" || e.Profile != "https://atlas.example.com" {
				t.Errorf("atlas_url entry = %+v", e)
			}
			sawAdded = true
		case "instance_url":
			if e.Status != "local-only" || e.Current != "https://local-only.example.com" {
				t.Errorf("instance_url entry = %+v", e)
			}
			sawLocalOnly = true
		}
	}
	if !sawAdded {
		t.Error("expected an added atlas_url entry")
	}
	if !sawLocalOnly {
		t.Error("expected a local-only instance_url entry")
	}
	if !hasActionableDrift(entries) {
		t.Error("expected hasActionableDrift to be true")
	}
}

func TestComputeDriftEntriesNoDrift(t *testing.T) {
	spec := &ProfileSpec{
		Name: "my-profile",
		Templates: map[string]string{
			"remotes": "remotes:\n  - name: exohub-atlas\n    type: artifactdb\n    mode: export\n    s3url: s3://bucket/_catalog\n",
		},
	}
	remotesConfig := &RemotesConfig{Remotes: []RemoteConfig{
		{Name: "exohub-atlas", Type: "artifactdb", Mode: "export", S3URL: "s3://bucket/_catalog"},
	}}

	entries, err := computeDriftEntries(spec, remotesConfig, "")
	if err != nil {
		t.Fatalf("computeDriftEntries: %v", err)
	}
	if hasActionableDrift(entries) {
		t.Errorf("expected no actionable drift, got %+v", entries)
	}
}

func TestComputeDriftEntriesUnresolvedPrompt(t *testing.T) {
	spec := &ProfileSpec{
		Name:    "my-profile",
		Prompts: []PromptSpec{{Name: "read_access", Kind: "choice", Choices: []string{"public", "authenticated"}}},
		Templates: map[string]string{
			"remotes": "remotes:\n  - name: exohub-atlas\n    type: artifactdb\n    mode: export\n    s3url: s3://bucket/_catalog\n",
		},
	}
	remotesConfig := &RemotesConfig{Remotes: []RemoteConfig{
		{Name: "exohub-atlas", Type: "artifactdb", Mode: "export", S3URL: "s3://bucket/_catalog"},
	}}

	entries, err := computeDriftEntries(spec, remotesConfig, "")
	if err != nil {
		t.Fatalf("computeDriftEntries: %v", err)
	}
	if len(entries) != 1 || entries[0].Status != "unresolved" {
		t.Fatalf("expected a single unresolved entry, got %+v", entries)
	}
	if !hasActionableDrift(entries) {
		t.Error("unresolved should be actionable (needs attention)")
	}
}

func TestComputeDriftEntriesUnknownRemoteFilterErrors(t *testing.T) {
	spec := &ProfileSpec{Name: "my-profile", Templates: map[string]string{"remotes": "remotes: []\n"}}
	remotesConfig := &RemotesConfig{Remotes: []RemoteConfig{{Name: "other", Type: "annex", S3URL: "s3://bucket/_annex"}}}

	_, err := computeDriftEntries(spec, remotesConfig, "exohub-atlas")
	if err == nil {
		t.Fatal("expected error for unknown --remote filter")
	}
}

func TestComputeDriftEntriesRemoteFilterScopesResults(t *testing.T) {
	spec := &ProfileSpec{
		Name: "my-profile",
		Templates: map[string]string{
			"remotes": "remotes:\n" +
				"  - name: exohub-atlas\n    type: artifactdb\n    mode: export\n    s3url: s3://bucket/_catalog\n    atlas_url: https://atlas.example.com\n" +
				"  - name: exohub-annex\n    type: annex\n    s3url: s3://bucket/_annex\n    chunk: 2GiB\n",
		},
	}
	remotesConfig := &RemotesConfig{Remotes: []RemoteConfig{
		{Name: "exohub-atlas", Type: "artifactdb", Mode: "export", S3URL: "s3://bucket/_catalog"},
		{Name: "exohub-annex", Type: "annex", S3URL: "s3://bucket/_annex", Chunk: "1GiB"},
	}}

	entries, err := computeDriftEntries(spec, remotesConfig, "exohub-annex")
	if err != nil {
		t.Fatalf("computeDriftEntries: %v", err)
	}
	for _, e := range entries {
		if e.Remote != "exohub-annex" {
			t.Errorf("--remote filter leaked another remote: %+v", e)
		}
	}
	if len(entries) != 1 || entries[0].Field != "chunk" {
		t.Fatalf("expected a single chunk drift entry, got %+v", entries)
	}
}

// TestComputeDriftEntriesUsesLockedFolderNotDirectory verifies that
// --dry-run --json drift reports resolve .Folder from .exohub/profile.lock
// rather than the cwd, so renaming the checkout directory never reports
// spurious drift on fields derived from .Folder (exo-cli#167).
func TestComputeDriftEntriesUsesLockedFolderNotDirectory(t *testing.T) {
	chdirTemp(t)

	if err := SaveProfileLock(&ProfileLock{Profile: "my-profile", Folder: "DS000000001"}); err != nil {
		t.Fatalf("SaveProfileLock: %v", err)
	}

	spec := &ProfileSpec{
		Name: "my-profile",
		Templates: map[string]string{
			"remotes": "remotes:\n  - name: exohub-annex\n    type: annex\n    s3url: s3://bucket/{{.Folder}}/_annex\n    chunk: 1GiB\n",
		},
	}
	remotesConfig := &RemotesConfig{Remotes: []RemoteConfig{
		{Name: "exohub-annex", Type: "annex", S3URL: "s3://bucket/DS000000001/_annex", Chunk: "1GiB"},
	}}

	entries, err := computeDriftEntries(spec, remotesConfig, "")
	if err != nil {
		t.Fatalf("computeDriftEntries: %v", err)
	}
	if hasActionableDrift(entries) {
		t.Errorf("expected no drift (cwd rename must not affect locked .Folder), got %+v", entries)
	}
}

// TestComputeDriftEntriesS3URLChangeHasMoveHint verifies a changed s3url is
// reported as status "changed" with a hint pointing at --allow-move /
// exo-cli#166, so --dry-run --json automation can distinguish a location
// move from any other field drift.
func TestComputeDriftEntriesS3URLChangeHasMoveHint(t *testing.T) {
	spec := &ProfileSpec{
		Name: "my-profile",
		Templates: map[string]string{
			"remotes": "remotes:\n  - name: exohub-annex\n    type: annex\n    s3url: s3://bucket/new-prefix/_annex\n    chunk: 1GiB\n",
		},
	}
	remotesConfig := &RemotesConfig{Remotes: []RemoteConfig{
		{Name: "exohub-annex", Type: "annex", S3URL: "s3://bucket/old-prefix/_annex", Chunk: "1GiB"},
	}}

	entries, err := computeDriftEntries(spec, remotesConfig, "")
	if err != nil {
		t.Fatalf("computeDriftEntries: %v", err)
	}

	var s3Entry *driftEntry
	for i := range entries {
		if entries[i].Field == "s3url" {
			s3Entry = &entries[i]
		}
	}
	if s3Entry == nil {
		t.Fatal("expected an s3url drift entry")
	}
	if s3Entry.Status != "changed" {
		t.Errorf("s3url entry status = %q, want changed", s3Entry.Status)
	}
	if s3Entry.Hint == "" || !strings.Contains(s3Entry.Hint, "--allow-move") {
		t.Errorf("s3url entry hint = %q, want a mention of --allow-move", s3Entry.Hint)
	}
}
