//go:build exohubapi

package doctor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestCheckServerFallback_SourceS3URLSentForInstance verifies that
// checkServerFallback includes source_s3url in the request when given one
// (role: instance remotes), and that the server's exact
// authorize_instance_project refusal text (exohub-workers!651) surfaces as
// an actionable hint distinct from the generic denial message.
func TestCheckServerFallback_SourceS3URLSentForInstance(t *testing.T) {
	var gotBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		r.Body.Read(buf)
		gotBody = string(buf)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"detail":"instance project 'DS1' has no recorded source yet; a READWRITE request with write access on the source is required to claim it"}`))
	}))
	defer ts.Close()
	t.Setenv("EXOHUB_API_URL", ts.URL)

	tokenLoader := func() (string, error) { return "", nil }
	result := checkServerFallback(context.Background(), "grants.instance", "s3://instance-bucket/DS1", "s3://atlas-bucket/DS1", tokenLoader)

	if !strings.Contains(gotBody, `"source_s3url":"s3://atlas-bucket/DS1"`) {
		t.Errorf("expected request body to carry source_s3url, got: %q", gotBody)
	}
	if result.Status != "FAIL" {
		t.Errorf("expected FAIL, got %s: %s", result.Status, result.Summary)
	}
	if !strings.Contains(result.Remediation, "claim") {
		t.Errorf("expected remediation to mention claiming, got: %q", result.Remediation)
	}
}

// TestCheckServerFallback_NoSourceS3URLForAtlas verifies that an Atlas
// remote's checkServerFallback call omits source_s3url entirely.
func TestCheckServerFallback_NoSourceS3URLForAtlas(t *testing.T) {
	var gotBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		r.Body.Read(buf)
		gotBody = string(buf)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"AccessKeyId":"ASIA","SecretAccessKey":"s","SessionToken":"t","Expiration":"2099-01-01T00:00:00Z"}`))
	}))
	defer ts.Close()
	t.Setenv("EXOHUB_API_URL", ts.URL)
	t.Setenv("AWS_ENDPOINT_URL", ts.URL)
	t.Setenv("EXOHUB_GRANTS_REGION", "us-east-1")

	tokenLoader := func() (string, error) { return "", nil }
	checkServerFallback(context.Background(), "grants.atlas", "s3://atlas-bucket/prefix", "", tokenLoader)

	if strings.Contains(gotBody, "source_s3url") {
		t.Errorf("expected no source_s3url in request body for an Atlas remote, got: %q", gotBody)
	}
}
