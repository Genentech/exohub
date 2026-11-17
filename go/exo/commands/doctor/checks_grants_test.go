package doctor

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// TestCheckGetDataAccess_UsesRemoteAccountID verifies that checkGetDataAccess
// reports which Access Grants account was used for a given remote, so two
// remotes with two different grants_account_id values produce distinguishable
// results for the same s3url shape.
func TestCheckGetDataAccess_UsesRemoteAccountID(t *testing.T) {
	ctx := context.Background()

	withAccount := checkGetDataAccess(ctx, "grants.instance-remote", "s3://instance-bucket/prefix", "111122223333", "READ")
	if !strings.Contains(withAccount.Summary, "account=111122223333") {
		t.Errorf("expected summary to mention account=111122223333, got %q", withAccount.Summary)
	}

	withoutAccount := checkGetDataAccess(ctx, "grants.atlas-remote", "s3://atlas-bucket/prefix", "", "READ")
	if !strings.Contains(withoutAccount.Summary, "account=default") {
		t.Errorf("expected summary to mention account=default, got %q", withoutAccount.Summary)
	}
}

// TestCheckRemoteGrants_TwoRemotesTwoAccounts verifies that checkRemoteGrants
// resolves each remote's own grants_account_id independently: an Atlas
// remote with no grants_account_id reports "default" while an instance
// remote with grants_account_id set reports that account, each against its
// own s3url.
func TestCheckRemoteGrants_TwoRemotesTwoAccounts(t *testing.T) {
	tokenLoader := func() (string, error) { return "", nil }

	atlas := remoteEntry{Name: "atlas-remote", S3URL: "s3://atlas-bucket/prefix", Grants: true}
	instance := remoteEntry{Name: "instance-remote", S3URL: "s3://instance-bucket/prefix", Grants: true, GrantsAccountID: "111122223333"}
	allRemotes := []remoteEntry{atlas, instance}

	atlasResults := checkRemoteGrants("/tmp", atlas, allRemotes, tokenLoader, false)
	instanceResults := checkRemoteGrants("/tmp", instance, allRemotes, tokenLoader, false)

	assertHasAccountAndS3URL(t, atlasResults, "account=default", "s3://atlas-bucket/prefix")
	assertHasAccountAndS3URL(t, instanceResults, "account=111122223333", "s3://instance-bucket/prefix")
}

// TestResolveSourceS3URL_InstanceWithDataFrom verifies a role: instance
// remote's data_from is resolved to that remote's s3url.
func TestResolveSourceS3URL_InstanceWithDataFrom(t *testing.T) {
	atlas := remoteEntry{Name: "atlas", Type: "artifactdb", Role: "atlas", S3URL: "s3://atlas-bucket/prefix"}
	instance := remoteEntry{Name: "instance", Type: "artifactdb", Role: "instance", DataFrom: "atlas", S3URL: "s3://instance-bucket/proj"}
	all := []remoteEntry{atlas, instance}

	got := resolveSourceS3URL(instance, all)
	if got != "s3://atlas-bucket/prefix" {
		t.Errorf("resolveSourceS3URL = %q, want s3://atlas-bucket/prefix", got)
	}
}

// TestResolveSourceS3URL_InstanceSingleAtlasImplicit verifies the single
// role: atlas remote is used when data_from is unset.
func TestResolveSourceS3URL_InstanceSingleAtlasImplicit(t *testing.T) {
	atlas := remoteEntry{Name: "atlas", Type: "artifactdb", Role: "atlas", S3URL: "s3://atlas-bucket/prefix"}
	instance := remoteEntry{Name: "instance", Type: "artifactdb", Role: "instance", S3URL: "s3://instance-bucket/proj"}
	all := []remoteEntry{atlas, instance}

	got := resolveSourceS3URL(instance, all)
	if got != "s3://atlas-bucket/prefix" {
		t.Errorf("resolveSourceS3URL = %q, want s3://atlas-bucket/prefix", got)
	}
}

// TestResolveSourceS3URL_AtlasRemoteOmitted verifies role: atlas remotes
// never get a source_s3url.
func TestResolveSourceS3URL_AtlasRemoteOmitted(t *testing.T) {
	atlas := remoteEntry{Name: "atlas", Type: "artifactdb", Role: "atlas", S3URL: "s3://atlas-bucket/prefix"}
	got := resolveSourceS3URL(atlas, []remoteEntry{atlas})
	if got != "" {
		t.Errorf("resolveSourceS3URL = %q, want empty", got)
	}
}

// TestInstanceSourceRefusalHint_FirstClaim verifies the exact server detail
// text from authorize_instance_project (exohub-workers!651) for a project
// with no recorded source yet is recognized — quoting the server's own
// wording so a future change to it breaks this test, not users.
func TestInstanceSourceRefusalHint_FirstClaim(t *testing.T) {
	err := fmt.Errorf("access denied: instance project 'DS1' has no recorded source yet; a READWRITE request with write access on the source is required to claim it")
	hint, ok := instanceSourceRefusalHint(err)
	if !ok {
		t.Fatal("expected instanceSourceRefusalHint to recognize the first-claim refusal")
	}
	if !strings.Contains(hint, "claim") {
		t.Errorf("expected hint to mention claiming, got: %q", hint)
	}
}

// TestInstanceSourceRefusalHint_BoundToDifferentSource verifies the binding
// mismatch detail text is recognized.
func TestInstanceSourceRefusalHint_BoundToDifferentSource(t *testing.T) {
	err := fmt.Errorf("access denied: instance project 'DS1' is bound to a different source project")
	hint, ok := instanceSourceRefusalHint(err)
	if !ok {
		t.Fatal("expected instanceSourceRefusalHint to recognize the binding mismatch refusal")
	}
	if !strings.Contains(hint, "data_from") {
		t.Errorf("expected hint to mention data_from, got: %q", hint)
	}
}

// TestInstanceSourceRefusalHint_ProjectMismatch verifies the source_s3url
// project-id mismatch detail text is recognized.
func TestInstanceSourceRefusalHint_ProjectMismatch(t *testing.T) {
	err := fmt.Errorf("source_s3url project 'other-proj' does not match instance project_id 'DS1'")
	hint, ok := instanceSourceRefusalHint(err)
	if !ok {
		t.Fatal("expected instanceSourceRefusalHint to recognize the project-id mismatch refusal")
	}
	if !strings.Contains(hint, "project_id") {
		t.Errorf("expected hint to mention project_id, got: %q", hint)
	}
}

// TestInstanceSourceRefusalHint_SourceS3URLRequired verifies the 400
// "source_s3url is required" detail text (an old client that predates
// source_s3url support) is recognized.
func TestInstanceSourceRefusalHint_SourceS3URLRequired(t *testing.T) {
	err := fmt.Errorf("source_s3url is required for instance project credential requests")
	hint, ok := instanceSourceRefusalHint(err)
	if !ok {
		t.Fatal("expected instanceSourceRefusalHint to recognize the source_s3url-required refusal")
	}
	if !strings.Contains(hint, "upgrade") {
		t.Errorf("expected hint to mention upgrading, got: %q", hint)
	}
}

// TestInstanceSourceRefusalHint_GenericAccessDeniedNotMatched verifies a
// plain non-instance 403 (e.g. .exohub/permissions denial) is NOT matched,
// so the generic "ask a dataset owner" fallback still applies.
func TestInstanceSourceRefusalHint_GenericAccessDeniedNotMatched(t *testing.T) {
	err := fmt.Errorf("access denied: you are not authorized for s3://bucket/prefix")
	if _, ok := instanceSourceRefusalHint(err); ok {
		t.Error("expected instanceSourceRefusalHint to NOT match a generic access-denied message")
	}
}

func assertHasAccountAndS3URL(t *testing.T, results []CheckResult, accountLabel, s3url string) {
	t.Helper()
	var foundAccount, foundScope bool
	for _, r := range results {
		if strings.Contains(r.Summary, accountLabel) {
			foundAccount = true
		}
		if strings.Contains(r.Summary, s3url) {
			foundScope = true
		}
	}
	if !foundAccount {
		t.Errorf("expected a result mentioning %q", accountLabel)
	}
	if !foundScope {
		t.Errorf("expected a result mentioning s3url %q", s3url)
	}
}
