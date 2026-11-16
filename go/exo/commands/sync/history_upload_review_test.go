package sync

import (
	"strings"
	"testing"

	initcmd "github.com/Genentech/exohub/go/exo/commands/init"
)

// TestShouldUploadHistory_EachGuardConditionAlone is the maître's review
// requirement (!313): a table test covering each disabling condition of
// shouldUploadHistory individually, plus the all-off -> true case. Mutating
// `return !skipHistoryUpload && ...` to `return ...` (dropping the
// skipHistoryUpload guard) must make the "skip flag alone" case below FAIL.
func TestShouldUploadHistory_EachGuardConditionAlone(t *testing.T) {
	tests := []struct {
		name              string
		skipHistoryUpload bool
		noContent         bool
		bundleDir         string
		expandedPaths     []string
		includePatterns   []string
		excludePatterns   []string
		want              bool
	}{
		{
			name:              "skip flag alone disables",
			skipHistoryUpload: true,
			want:              false,
		},
		{
			name:      "no-content alone disables",
			noContent: true,
			want:      false,
		},
		{
			name:      "bundle dir alone disables",
			bundleDir: "main",
			want:      false,
		},
		{
			name:          "explicit paths alone disable",
			expandedPaths: []string{"some/path"},
			want:          false,
		},
		{
			name:            "include patterns alone disable",
			includePatterns: []string{"*.bin"},
			want:            false,
		},
		{
			name:            "exclude patterns alone disable",
			excludePatterns: []string{"*.tmp"},
			want:            false,
		},
		{
			name: "all off enables",
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldUploadHistory(tt.skipHistoryUpload, tt.noContent, tt.bundleDir, tt.expandedPaths, tt.includePatterns, tt.excludePatterns)
			if got != tt.want {
				t.Errorf("shouldUploadHistory(skip=%v, noContent=%v, bundleDir=%q, paths=%v, include=%v, exclude=%v) = %v, want %v",
					tt.skipHistoryUpload, tt.noContent, tt.bundleDir, tt.expandedPaths, tt.includePatterns, tt.excludePatterns, got, tt.want)
			}
		})
	}
}

// TestDefaultHistoryUpload_NeverTargetsCatalogRemote is the maître's review
// requirement (!313): with a .exohub/remotes containing a plain annex
// remote, a type: artifactdb role: atlas export remote, and a role:
// instance remote, both allTargetRemotes and uploadHistoryForRemotes must
// select only the annex remote, and no `git annex copy --to <catalog
// remote>` may ever be issued. Mutating allTargetRemotes to drop
// `isCatalogRemoteType(name, remotesConfig) ||` must make this test FAIL
// (the atlas/instance names would leak into the target list, and the real
// run would attempt `git annex copy --to atlas` / `--to instance`, which
// fail since those are never git-annex special remotes).
func TestDefaultHistoryUpload_NeverTargetsCatalogRemote(t *testing.T) {
	repoDir, _, restore := newAnnexRepo(t, "r1")
	defer restore()

	remotesConfig := &initcmd.RemotesConfig{
		Remotes: []initcmd.RemoteConfig{
			{Name: "r1", Type: "annex", S3URL: "s3://bucket/r1"},
			{Name: "atlas", Type: "artifactdb", Role: "atlas", Mode: "export", S3URL: "s3://bucket/atlas"},
			{Name: "instance", Type: "artifactdb", Role: "instance", S3URL: "s3://bucket/instance", URL: "https://instance.example.com/v1/proj"},
		},
	}

	candidates := []string{"r1", "atlas", "instance"}

	// allTargetRemotes must select only the plain annex remote.
	targets, err := allTargetRemotes(candidates, remotesConfig)
	if err != nil {
		t.Fatalf("allTargetRemotes: %v", err)
	}
	if len(targets) != 1 || targets[0] != "r1" {
		t.Fatalf("expected allTargetRemotes to select only [r1], got: %v", targets)
	}

	addAnnexFile(t, repoDir, "a.bin", "version-1")

	// The real incremental upload pass must likewise only ever touch r1:
	// it must succeed (no attempt to `git annex copy --to atlas` or
	// `--to instance`, which are not git-annex remotes at all).
	uploadHistoryForRemotes(candidates, remotesConfig, "1")

	if _, ok := readMarkerCommit(historyMarkerRef("r1", "main")); !ok {
		t.Errorf("expected a history-upload marker for the annex remote r1")
	}
	if _, ok := readMarkerCommit(historyMarkerRef("atlas", "main")); ok {
		t.Errorf("expected no history-upload marker for the catalog remote 'atlas'")
	}
	if _, ok := readMarkerCommit(historyMarkerRef("instance", "main")); ok {
		t.Errorf("expected no history-upload marker for the catalog remote 'instance'")
	}

	found := annexRun(t, repoDir, "git", "annex", "find", "--in", "r1")
	if !strings.Contains(found, "a.bin") {
		t.Errorf("expected a.bin to have been uploaded to r1, find output: %q", found)
	}

	// Belt-and-suspenders: directly exercise copyHistoryBranch against the
	// catalog remote names and confirm git-annex itself rejects them (they
	// were never `git annex initremote`'d). This proves that if routing
	// ever let a catalog remote through, the failure would be observable
	// as a hard error, not a silent no-op.
	for _, remote := range []string{"atlas", "instance"} {
		if err := copyHistoryBranch("HEAD", remote, "1", false); err == nil {
			t.Errorf("expected git annex copy --to %q to fail (not a real git-annex remote), got nil error", remote)
		}
	}
}
