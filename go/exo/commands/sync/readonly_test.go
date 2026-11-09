package sync

import (
	"testing"

	initcmd "github.com/Genentech/exohub/go/exo/commands/init"
)

// TestIsReadonlyRemote covers exo-cli#147 (Z1 default-fallback
// materialisation, design D3 "readonly: true" defense-in-depth flag): a
// remote declared readonly: true in .exohub/remotes is always get-only,
// independent of whatever S3 Access Grants happen to allow.
func TestIsReadonlyRemote(t *testing.T) {
	remotesConfig := &initcmd.RemotesConfig{
		Remotes: []initcmd.RemoteConfig{
			{Name: "s3-archive-ro", Type: "annex", S3URL: "s3://bucket/prefix", Readonly: true},
			{Name: "s3-archive", Type: "annex", S3URL: "s3://bucket/other"},
		},
	}

	if !isReadonlyRemote("s3-archive-ro", remotesConfig) {
		t.Error("isReadonlyRemote(s3-archive-ro) = false, want true")
	}
	if isReadonlyRemote("s3-archive", remotesConfig) {
		t.Error("isReadonlyRemote(s3-archive) = true, want false")
	}
	if isReadonlyRemote("unknown", remotesConfig) {
		t.Error("isReadonlyRemote(unknown) = true, want false")
	}
	if isReadonlyRemote("s3-archive-ro", nil) {
		t.Error("isReadonlyRemote(_, nil) = true, want false")
	}
}
