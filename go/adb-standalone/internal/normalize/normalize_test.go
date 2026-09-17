package normalize_test

import (
	"encoding/json"
	"testing"

	"github.com/Genentech/exohub/go/adb-standalone/internal/normalize"
)

func mustNormalize(t *testing.T, raw string) map[string]any {
	t.Helper()
	out, err := normalize.Response([]byte(raw))
	if err != nil {
		t.Fatalf("normalize.Response: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	return m
}

func TestDrop3aVolatileFields(t *testing.T) {
	raw := `{
		"_extra": {
			"id": "p:f@v",
			"index_name": "adb-uat-v1-exohub-20260415",
			"meta_indexed": "2026-06-05T19:08:53Z",
			"meta_uploaded": "2026-06-05T20:00:00Z",
			"uploaded": "2026-06-05T21:00:00Z",
			"project_id": "p"
		}
	}`
	result := mustNormalize(t, raw)
	extra, _ := result["_extra"].(map[string]any)
	for _, field := range []string{"index_name", "meta_indexed", "meta_uploaded", "uploaded"} {
		if _, ok := extra[field]; ok {
			t.Errorf("volatile field %q should have been dropped", field)
		}
	}
	if extra["id"] != "p:f@v" {
		t.Errorf("id should be kept, got %v", extra["id"])
	}
	if extra["project_id"] != "p" {
		t.Errorf("project_id should be kept, got %v", extra["project_id"])
	}
}

func TestNormalizeBucket3b(t *testing.T) {
	raw := `{
		"_extra": {
			"id": "p:f@v",
			"location": {
				"type": "s3",
				"s3": {"bucket": "adb-uat"}
			}
		}
	}`
	result := mustNormalize(t, raw)
	extra := result["_extra"].(map[string]any)
	loc := extra["location"].(map[string]any)
	s3 := loc["s3"].(map[string]any)
	if s3["bucket"] != "<bucket>" {
		t.Errorf("bucket should be normalised to <bucket>, got %v", s3["bucket"])
	}
	if loc["type"] != "s3" {
		t.Errorf("type should be kept, got %v", loc["type"])
	}
}

func TestNormalizeScrollNext(t *testing.T) {
	raw := `{"next": "/scroll/Y3VzdG9t-c2Nyb2xsX3F1ZXJ5-abc123=="}`
	result := mustNormalize(t, raw)
	if result["next"] != "/scroll/<scroll-id>" {
		t.Errorf("next scroll token should be normalised, got %v", result["next"])
	}
}

func TestNullNextKept(t *testing.T) {
	raw := `{"next": null}`
	result := mustNormalize(t, raw)
	if result["next"] != nil {
		t.Errorf("null next should remain null, got %v", result["next"])
	}
}

func TestKeep3cExact(t *testing.T) {
	raw := `{
		"_extra": {
			"id": "p:f@v",
			"gprn": "gprn:wip:adb::artifact:p:f@v",
			"project_id": "p",
			"version": "v1.0.0",
			"latest": true,
			"permissions": {"read_access": "public"}
		},
		"author_date": "2026-01-01T00:00:00Z",
		"committer_date": "2026-01-02T00:00:00Z",
		"generated_at": "2026-01-03T00:00:00Z",
		"count": 5,
		"total": 100
	}`
	result := mustNormalize(t, raw)
	extra := result["_extra"].(map[string]any)
	if extra["id"] != "p:f@v" {
		t.Errorf("id mismatch: %v", extra["id"])
	}
	if extra["version"] != "v1.0.0" {
		t.Errorf("version mismatch: %v", extra["version"])
	}
	// Timestamps are normalised: Z -> +00:00 so both sides compare equal.
	if result["author_date"] != "2026-01-01T00:00:00+00:00" {
		t.Errorf("author_date should be normalised to +00:00: %v", result["author_date"])
	}
	if result["committer_date"] != "2026-01-02T00:00:00+00:00" {
		t.Errorf("committer_date should be normalised to +00:00: %v", result["committer_date"])
	}
	if result["generated_at"] != "2026-01-03T00:00:00+00:00" {
		t.Errorf("generated_at should be normalised to +00:00: %v", result["generated_at"])
	}
	if result["count"] != float64(5) {
		t.Errorf("count mismatch: %v", result["count"])
	}
	// "total" is root-volatile (ES vs SurrealDB semantics differ); must be dropped.
	if _, ok := result["total"]; ok {
		t.Errorf("total should be dropped at root level")
	}
}

func TestTotalNotDroppedWhenNested(t *testing.T) {
	// "total" must only be dropped at root depth, not inside nested objects.
	raw := `{"results": [{"total_size": 1234, "_extra": {"total": 42}}]}`
	result := mustNormalize(t, raw)
	results := result["results"].([]any)
	doc := results[0].(map[string]any)
	if doc["total_size"] != float64(1234) {
		t.Errorf("total_size should be preserved in nested doc: %v", doc["total_size"])
	}
	extra := doc["_extra"].(map[string]any)
	if extra["total"] != float64(42) {
		t.Errorf("total inside _extra should be preserved: %v", extra["total"])
	}
}

func TestTimestampFractionalSeconds(t *testing.T) {
	// Timestamps with fractional seconds ending in Z must also be normalised.
	raw := `{"ts": "2026-01-01T00:00:00.123Z", "ts2": "2026-01-01T00:00:00.000000Z"}`
	result := mustNormalize(t, raw)
	if result["ts"] != "2026-01-01T00:00:00.123+00:00" {
		t.Errorf("fractional timestamp not normalised: %v", result["ts"])
	}
	if result["ts2"] != "2026-01-01T00:00:00.000000+00:00" {
		t.Errorf("fractional timestamp not normalised: %v", result["ts2"])
	}
}

func TestNormalizePresignedURL(t *testing.T) {
	raw := `{"url": "https://mybucket.s3.amazonaws.com/key?X-Amz-Algorithm=AWS4&X-Amz-Signature=abc&Expires=99999"}`
	result := mustNormalize(t, raw)
	u, _ := result["url"].(string)
	if u == "" {
		t.Fatal("url missing from result")
	}
	for _, param := range []string{"X-Amz-Algorithm", "X-Amz-Signature", "Expires"} {
		if contains(u, param) {
			t.Errorf("presigned param %q should be stripped from URL: %s", param, u)
		}
	}
}

func TestSortedKeys(t *testing.T) {
	raw := `{"z": 1, "a": 2, "m": 3}`
	out, err := normalize.Response([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	posA := indexOf(s, `"a"`)
	posM := indexOf(s, `"m"`)
	posZ := indexOf(s, `"z"`)
	if posA > posM || posM > posZ {
		t.Errorf("keys not sorted: %s", s)
	}
}

func TestIdempotent(t *testing.T) {
	// Use count (not total) at root — total is dropped at root depth.
	raw := `{
		"_extra": {"id": "p:f@v", "index_name": "x", "project_id": "p"},
		"next": "/scroll/token123",
		"count": 2
	}`
	first, err := normalize.Response([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	second, err := normalize.Response(first)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Errorf("normalize not idempotent:\nfirst:  %s\nsecond: %s", first, second)
	}
}

func contains(s, sub string) bool {
	return len(s) > 0 && len(sub) > 0 && (func() bool {
		for i := 0; i <= len(s)-len(sub); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

func indexOf(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
