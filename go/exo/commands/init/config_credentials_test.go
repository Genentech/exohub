package init

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// TestRemoteConfig_CredentialsYAMLRoundTrip covers exo-cli#131 scope item 1:
// the shared RemoteConfig parses an opaque credentials provider->settings map
// regardless of build, and never fails on an unrecognized provider.
func TestRemoteConfig_CredentialsYAMLRoundTrip(t *testing.T) {
	data := []byte(`
name: partner-raw
type: annex
s3url: s3://team-genomics-raw/cohort-2026/
grants: false
credentials:
  some-provider:
    group: GRP-Genomics-Raw-Readers
`)
	var r RemoteConfig
	if err := yaml.Unmarshal(data, &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got := r.Credentials["some-provider"]["group"]; got != "GRP-Genomics-Raw-Readers" {
		t.Errorf("Credentials[some-provider][group] = %q, want GRP-Genomics-Raw-Readers", got)
	}

	out, err := yaml.Marshal(&r)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var roundTripped RemoteConfig
	if err := yaml.Unmarshal(out, &roundTripped); err != nil {
		t.Fatalf("Unmarshal round-trip: %v", err)
	}
	if roundTripped.Credentials["some-provider"]["group"] != "GRP-Genomics-Raw-Readers" {
		t.Errorf("round-tripped Credentials[some-provider][group] = %q, want GRP-Genomics-Raw-Readers", roundTripped.Credentials["some-provider"]["group"])
	}
}

// TestRemoteConfig_NoCredentials covers the common case: a remote with no
// credentials key parses with a nil map and validates fine.
func TestRemoteConfig_NoCredentials(t *testing.T) {
	r := RemoteConfig{Name: "plain", Type: "annex", S3URL: "s3://bucket/prefix"}
	if r.Credentials != nil {
		t.Errorf("Credentials = %v, want nil when not set", r.Credentials)
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error = %v, want nil", err)
	}
}

// TestRemoteConfig_ValidateIgnoresUnknownCredentialProviderInOSS documents
// that the shared Validate() never rejects a credentials provider it does
// not know about: unknown providers are ignored by exo and only flagged by
// exo doctor (checks_repo.go), not by RemoteConfig.Validate.
func TestRemoteConfig_ValidateIgnoresUnknownCredentialProvider(t *testing.T) {
	r := RemoteConfig{
		Name:  "test",
		Type:  "annex",
		S3URL: "s3://bucket/prefix",
		Credentials: map[string]map[string]string{
			"some-unknown-provider": {"group": "whatever"},
		},
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error = %v, want nil for an unrecognized credentials provider", err)
	}
}
