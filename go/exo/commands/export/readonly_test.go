package export

import (
	"testing"

	initcmd "github.com/Genentech/exohub/go/exo/commands/init"
)

// TestIsReadonlyRemote covers exo-cli#147 (design D3): `exo export --to`
// must refuse a remote declared readonly: true in .exohub/remotes, e.g. one
// importing another project's annex storage.
func TestIsReadonlyRemote(t *testing.T) {
	t.Chdir(t.TempDir())

	if isReadonlyRemote("s3-archive-ro") {
		t.Error("isReadonlyRemote() = true before .exohub/remotes exists, want false")
	}

	remotesConfig := &initcmd.RemotesConfig{
		Remotes: []initcmd.RemoteConfig{
			{Name: "s3-archive-ro", Type: "annex", S3URL: "s3://bucket/prefix", Readonly: true},
			{Name: "s3-archive", Type: "annex", S3URL: "s3://bucket/other"},
		},
	}
	if err := initcmd.SaveRemotesConfig(remotesConfig); err != nil {
		t.Fatalf("SaveRemotesConfig() error = %v", err)
	}

	if !isReadonlyRemote("s3-archive-ro") {
		t.Error("isReadonlyRemote(s3-archive-ro) = false, want true")
	}
	if isReadonlyRemote("s3-archive") {
		t.Error("isReadonlyRemote(s3-archive) = true, want false")
	}
	if isReadonlyRemote("unknown") {
		t.Error("isReadonlyRemote(unknown) = true, want false")
	}
}
