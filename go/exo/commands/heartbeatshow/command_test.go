package heartbeatshow

import (
	"strings"
	"testing"
)

func TestProgressFromValuesAndFormat(t *testing.T) {
	emptyBar := strings.Repeat("○", barWidth)

	progress := progressFromValues(0, 0)
	if progress.Bar != emptyBar+" 0.00%" || progress.Percent != 0 {
		t.Fatalf("progressFromValues: %#v", progress)
	}

	progress = progressFromValues(5, 10)
	// 50% of 15 = 7.5, rounds to 8
	wantBar := strings.Repeat("●", 8) + strings.Repeat("○", 7) + " 50.00%"
	if progress.Bar != wantBar {
		t.Fatalf("progressFromValues bar: %q, want %q", progress.Bar, wantBar)
	}

	bar, pct := formatProgressFromProgress(progressValue{Percent: 0.4}, 4, 10)
	// 40% of 15 = 6
	wantBarOnly := strings.Repeat("●", 6) + strings.Repeat("○", 9)
	if bar != wantBarOnly || pct != "40.00%" {
		t.Fatalf("formatProgressFromProgress: bar=%q pct=%q", bar, pct)
	}

	bar, pct = formatProgressFromProgress(progressValue{}, 0, 0)
	if bar != emptyBar || pct != "0.00%" {
		t.Fatalf("formatProgressFromProgress default: bar=%q pct=%q", bar, pct)
	}
}

func TestExtractPercentAndDurCompact(t *testing.T) {
	if got := extractPercent("████ 12.34%"); got != "12.34%" {
		t.Fatalf("extractPercent: %q", got)
	}
	if got := extractPercent("no percent"); got != "percent" {
		t.Fatalf("extractPercent empty: %q", got)
	}
	if got := durCompact(3661); got != "1h1m1s" {
		t.Fatalf("durCompact: %q", got)
	}
	if got := durCompact(-1); got != "0s" {
		t.Fatalf("durCompact negative: %q", got)
	}
}

func TestFirstNAndHrBytes(t *testing.T) {
	input := []string{"a", "b", "c"}
	if got := firstN(input, 2); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("firstN: %#v", got)
	}
	if got := firstN(input, 5); len(got) != 3 {
		t.Fatalf("firstN full: %#v", got)
	}
	if got := hrBytes(1024); got != "1.0KB" {
		t.Fatalf("hrBytes: %q", got)
	}
	if got := hrBytes(10 * 1024 * 1024); got != "10MB" {
		t.Fatalf("hrBytes: %q", got)
	}
}

func TestIsPathComplete(t *testing.T) {
	complete := pathMetrics{
		Files:   filesMetrics{Total: 10, Count: 10, Progress: progressValue{Percent: 1.0}},
		Storage: storageMetrics{Progress: progressValue{Percent: 1.0}},
		Remotes: []remoteMetric{
			{Name: "origin", Files: filesMetrics{Progress: progressValue{Percent: 1.0}}, Storage: remoteStorage{Progress: progressValue{Percent: 1.0}}},
		},
	}
	if !isPathComplete(complete) {
		t.Fatal("expected complete path to be complete")
	}

	incomplete := pathMetrics{
		Files:   filesMetrics{Total: 10, Count: 5, Progress: progressValue{Percent: 0.5}},
		Storage: storageMetrics{Progress: progressValue{Percent: 0.5}},
	}
	if isPathComplete(incomplete) {
		t.Fatal("expected incomplete path to not be complete")
	}

	incompleteRemote := pathMetrics{
		Files:   filesMetrics{Total: 10, Count: 10, Progress: progressValue{Percent: 1.0}},
		Storage: storageMetrics{Progress: progressValue{Percent: 1.0}},
		Remotes: []remoteMetric{
			{Name: "origin", Files: filesMetrics{Progress: progressValue{Percent: 0.8}}, Storage: remoteStorage{Progress: progressValue{Percent: 0.8}}},
		},
	}
	if isPathComplete(incompleteRemote) {
		t.Fatal("expected path with incomplete remote to not be complete")
	}
}

func TestRemainingAnnotation(t *testing.T) {
	if got := remainingAnnotation(1.0, 100, 100); got != "" {
		t.Fatalf("expected empty for 100%%: %q", got)
	}
	if got := remainingAnnotation(0.5, 512, 1024); got != " (512B remaining)" {
		t.Fatalf("remainingAnnotation: %q", got)
	}
	if got := remainingAnnotation(0.0, 0, 0); got != "" {
		t.Fatalf("expected empty for zero total: %q", got)
	}
}

func TestBarWidth(t *testing.T) {
	if barWidth != 15 {
		t.Fatalf("expected barWidth=15, got %d", barWidth)
	}
	full := progressFromValues(10, 10)
	barOnly := strings.Fields(full.Bar)[0]
	if len([]rune(barOnly)) != barWidth {
		t.Fatalf("full bar rune count: %d, want %d", len([]rune(barOnly)), barWidth)
	}
}
