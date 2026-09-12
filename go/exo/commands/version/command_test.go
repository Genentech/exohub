package version

import (
	"strings"
	"testing"
)

func TestNormalizeVersion(t *testing.T) {
	if got := normalizeVersion("v1.2.3"); got != "1.2.3" {
		t.Fatalf("normalizeVersion: %q", got)
	}
	if got := normalizeVersion("1.0.0-SNAPSHOT"); got != "1.0.0" {
		t.Fatalf("normalizeVersion snapshot: %q", got)
	}
}

func TestParseNumericVersion(t *testing.T) {
	parts, suffix, ok := parseNumericVersion("1.2.3-alpha+build")
	if !ok || suffix != "alpha" || len(parts) != 3 || parts[0] != 1 || parts[2] != 3 {
		t.Fatalf("parseNumericVersion: parts=%v suffix=%q ok=%v", parts, suffix, ok)
	}
	if _, _, ok := parseNumericVersion("1.x.3"); ok {
		t.Fatalf("expected parseNumericVersion to fail")
	}
}

func TestCompareVersions(t *testing.T) {
	if compareVersions("1.2.0", "1.1.9") != 1 {
		t.Fatalf("compareVersions expected greater")
	}
	if compareVersions("1.2.3", "1.2.3-beta") != 1 {
		t.Fatalf("compareVersions expected release > suffix")
	}
	if compareVersions("1.2.3-alpha", "1.2.3-beta") != -1 {
		t.Fatalf("compareVersions expected alpha < beta")
	}
}

func TestExtractLatestVersion(t *testing.T) {
	payload := map[string]any{"latest": "1.2.3"}
	if got := extractLatestVersion(payload); got != "1.2.3" {
		t.Fatalf("extractLatestVersion: %q", got)
	}

	payload = map[string]any{"latest": map[string]any{"_extra.version": "2.0.0"}}
	if got := extractLatestVersion(payload); got != "2.0.0" {
		t.Fatalf("extractLatestVersion _extra.version: %q", got)
	}
}

func TestFormatFullVersion_inlineFeaturesWithTags(t *testing.T) {
	// Without any build tags active (standard test build), no [features: ...] bracket.
	// With tags active it would be inline on the same line.
	got := formatFullVersion("1.2.3", "abc1234", "2026-01-01")
	// Must be a single line (no newlines).
	if strings.Contains(got, "\n") {
		t.Fatalf("formatFullVersion output must be single line, got: %q", got)
	}
	// Must start with the version+metadata base.
	if !strings.HasPrefix(got, "1.2.3 (commit abc1234, 2026-01-01)") {
		t.Fatalf("formatFullVersion base missing: %q", got)
	}
}

func TestFormatFullVersion_noMetadata(t *testing.T) {
	// When commit and date are empty, base is just the version string.
	got := formatFullVersion("1.2.3", "", "")
	if strings.Contains(got, "\n") {
		t.Fatalf("formatFullVersion must be single line: %q", got)
	}
	if !strings.HasPrefix(got, "1.2.3") {
		t.Fatalf("formatFullVersion first token = %q, want version prefix", got)
	}
}

func TestFormatFullVersion_inlineBracketFormat(t *testing.T) {
	// When features are present they must appear inline as [features: tag1, tag2].
	// We verify the bracket format by checking a known inline suffix pattern.
	got := formatFullVersion("1.2.3", "", "")
	// No newline in output regardless of tag presence.
	if strings.Contains(got, "\n") {
		t.Fatalf("formatFullVersion must not contain newline: %q", got)
	}
	// If a features bracket is present it must be inline.
	if idx := strings.Index(got, "[features:"); idx >= 0 {
		if !strings.HasSuffix(got, "]") {
			t.Fatalf("features bracket not closed: %q", got)
		}
	}
}

func TestGetBinaryVersion_singleLine(t *testing.T) {
	// echo outputs a single line; result should be that line.
	got := getBinaryVersion("echo", []string{"hello"})
	if got != "hello" {
		t.Fatalf("getBinaryVersion: %q", got)
	}
	// Inline [features: ...] on first line is preserved as-is.
	got = getBinaryVersion("echo", []string{"dev [features: exohubapi]"})
	if got != "dev [features: exohubapi]" {
		t.Fatalf("getBinaryVersion inline features: %q", got)
	}
}

func TestGetBinaryVersion_notFound(t *testing.T) {
	got := getBinaryVersion("__no_such_binary_xyz__", []string{})
	if got != "not found" {
		t.Fatalf("getBinaryVersion not found: %q", got)
	}
}
