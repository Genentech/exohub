//go:build artifactdb

package download

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Genentech/exohub/go/exo/internal/eds"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

type fakeClock struct {
	t time.Time
}

func (c *fakeClock) Now() time.Time          { return c.t }
func (c *fakeClock) Advance(d time.Duration) { c.t = c.t.Add(d) }

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{12 * time.Second, "12s"},
		{92 * time.Second, "1m32s"},
		{120 * time.Second, "2m"},
		{0, "0s"},
	}
	for _, c := range cases {
		if got := formatDuration(c.d); got != c.want {
			t.Errorf("formatDuration(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestHumanizeStep_KnownAndUnknown(t *testing.T) {
	if got := humanizeStep("waiting_for_repo_lock"); got != "waiting for another download of this dataset" {
		t.Errorf("humanizeStep(waiting_for_repo_lock) = %q", got)
	}
	if got := humanizeStep("cloning"); got != "cloning" {
		t.Errorf("humanizeStep(cloning) = %q", got)
	}
	if got := humanizeStep("exo_init"); got != "initialising" {
		t.Errorf("humanizeStep(exo_init) = %q", got)
	}
	if got := humanizeStep("some_future_step"); got != "some_future_step" {
		t.Errorf("humanizeStep(unknown) = %q, want passthrough", got)
	}
}

func TestFormatEDSFinalLine_CompletedFixture(t *testing.T) {
	// Values from the completed-operation JSON fixture in issue #103.
	result := &eds.Result{
		Status:          eds.ResultOK,
		Verified:        true,
		MissingFiles:    nil,
		FilesSynced:     1,
		BytesSynced:     60715200,
		FilesTotal:      1,
		AnnexFilesTotal: 1,
		BytesTotal:      60715200,
		Scope:           "user",
		RepoDir:         "/data/eds/users/alice/workspace/cvrm_exohub/main",
	}

	line := formatEDSFinalLine(result)

	if !strings.Contains(line, "100%") {
		t.Errorf("expected 100%% in line, got: %q", line)
	}
	if !strings.Contains(line, "1/1 files, verified") {
		t.Errorf("expected '1/1 files, verified' in line, got: %q", line)
	}
	if strings.Count(line, "%") != 1 {
		t.Errorf("expected exactly one percentage, got: %q", line)
	}
}

func TestFormatEDSFinalLine_VerifiedWithMissingFiles(t *testing.T) {
	result := &eds.Result{
		Verified:        true,
		AnnexFilesTotal: 1410,
		MissingFiles:    []string{"a.txt", "b.txt"},
		BytesSynced:     100,
	}

	line := formatEDSFinalLine(result)
	if !strings.Contains(line, "1408/1410 files, verified") {
		t.Errorf("expected present = total-missing, got: %q", line)
	}
}

func TestFormatEDSFinalLine_VerifiedNoMissingUsesBytesTotal(t *testing.T) {
	// Live winery numbers from MR !238 review: bytes_synced under-counts
	// content that was already present, so a fully-verified, nothing-missing
	// result must show bytes_total next to the 100% bar, not bytes_synced.
	result := &eds.Result{
		Verified:        true,
		MissingFiles:    nil,
		AnnexFilesTotal: 1410,
		BytesSynced:     188429675536,
		BytesTotal:      188645144770,
	}

	line := formatEDSFinalLine(result)
	if !strings.Contains(line, formatSize(result.BytesTotal)) {
		t.Errorf("expected bytes_total (%s) in line, got: %q", formatSize(result.BytesTotal), line)
	}
	if strings.Contains(line, formatSize(result.BytesSynced)) {
		t.Errorf("must not show the smaller bytes_synced next to a 100%% bar, got: %q", line)
	}
	if !strings.Contains(line, "1410/1410 files, verified") {
		t.Errorf("expected '1410/1410 files, verified', got: %q", line)
	}
}

func TestFormatEDSFinalLine_NotVerified(t *testing.T) {
	result := &eds.Result{
		Verified:        false,
		FilesSynced:     1408,
		AnnexFilesTotal: 1410,
		BytesSynced:     100,
	}

	line := formatEDSFinalLine(result)
	if strings.Contains(line, "verified") {
		t.Errorf("must not claim verified when Result.Verified is false, got: %q", line)
	}
	if !strings.Contains(line, "1408/1410 files") {
		t.Errorf("expected files_synced/annex_files_total when not verified, got: %q", line)
	}
}

func TestFormatEDSFinalLine_NilResult(t *testing.T) {
	if got := formatEDSFinalLine(nil); got != "" {
		t.Errorf("formatEDSFinalLine(nil) = %q, want empty", got)
	}
}

func TestRetryText_WithLastFailure(t *testing.T) {
	failure := "heartbeat timeout"
	text, ok := retryText([]eds.Activity{{State: "started", Attempt: 2, LastFailure: &failure}})
	if !ok {
		t.Fatal("expected ok=true")
	}
	if want := "retrying (attempt 2: heartbeat timeout)"; text != want {
		t.Errorf("text = %q, want %q", text, want)
	}
}

func TestRetryText_NoRetryWhenFirstAttempt(t *testing.T) {
	_, ok := retryText([]eds.Activity{{State: "started", Attempt: 1}})
	if ok {
		t.Error("expected ok=false when no activity has attempt > 1")
	}
}

func TestFormatEDSLiveLine_WaitingForFirstReport(t *testing.T) {
	now := time.Now()
	line := formatEDSLiveLine(now, now, "⠹", nil, 0, false, false)
	if !strings.Contains(line, "waiting for first report") {
		t.Errorf("expected 'waiting for first report', got: %q", line)
	}
}

func TestFormatEDSLiveLine_ReportAgeAndRetry(t *testing.T) {
	now := time.Now()
	failure := "heartbeat timeout"
	p := &eds.Progress{
		Step:            "syncing",
		BytesSynced:     12_300_000,
		BytesTotal:      57_900_000,
		FilesSynced:     0,
		AnnexFilesTotal: 1,
		Activities:      []eds.Activity{{State: "started", Attempt: 2, LastFailure: &failure}},
	}

	line := formatEDSLiveLine(now, now.Add(-92*time.Second), "⠹", p, 12*time.Second, true, false)

	if !strings.Contains(line, "syncing") {
		t.Errorf("expected humanised phase in line, got: %q", line)
	}
	if !strings.Contains(line, "0/1 files") {
		t.Errorf("expected file counters in line, got: %q", line)
	}
	if !strings.Contains(line, "1m32s") {
		t.Errorf("expected elapsed time in line, got: %q", line)
	}
	if !strings.Contains(line, "EDS reported 12s ago") {
		t.Errorf("expected report age in line, got: %q", line)
	}
	if !strings.Contains(line, "retrying (attempt 2: heartbeat timeout)") {
		t.Errorf("expected retry text in line, got: %q", line)
	}
}

func TestFormatEDSLiveLine_Stalled(t *testing.T) {
	now := time.Now()
	line := formatEDSLiveLine(now, now, "⠹", &eds.Progress{Step: "syncing"}, 125*time.Second, true, true)
	if !strings.Contains(line, "no report from the EDS for") {
		t.Errorf("expected stall warning, got: %q", line)
	}
}

func TestEdsLiveRenderer_ReportAge_ResetsOnNewTS_UnaffectedByClockSkew(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	renderer := newEDSLiveRenderer(false, clock.Now(), clock.Now)

	// EDS clock is wildly skewed from the client's; report age must not
	// depend on it.
	captureStdout(t, func() {
		renderer.Update(&eds.Progress{Step: "syncing", TS: "2099-01-01T00:00:00Z"})
	})

	clock.Advance(5 * time.Second)
	age, has := reportAge(clock.Now(), renderer.firstSeenAt, renderer.hasSeenReport)
	if !has {
		t.Fatal("expected hasSeenReport=true after first ts")
	}
	if age != 5*time.Second {
		t.Errorf("age = %v, want 5s", age)
	}

	clock.Advance(10 * time.Second)
	captureStdout(t, func() {
		renderer.Update(&eds.Progress{Step: "syncing", TS: "1999-01-01T00:00:00Z"})
	})
	age, has = reportAge(clock.Now(), renderer.firstSeenAt, renderer.hasSeenReport)
	if !has || age != 0 {
		t.Errorf("age = %v, has = %v, want 0/true immediately after a new ts", age, has)
	}
}

func TestEdsLiveRenderer_StallTransitionAlonePrints(t *testing.T) {
	clock := &fakeClock{t: time.Now()}
	renderer := newEDSLiveRenderer(false, clock.Now(), clock.Now)
	p := &eds.Progress{Step: "syncing", TS: "t1", BytesTotal: 100, AnnexFilesTotal: 1}

	out1 := captureStdout(t, func() { renderer.Update(p) })
	if strings.Count(out1, "\n") != 1 {
		t.Fatalf("expected first update to print one line, got: %q", out1)
	}
	if strings.Contains(out1, "no report from the EDS for") {
		t.Errorf("should not warn immediately after first report, got: %q", out1)
	}

	clock.Advance(91 * time.Second)
	out2 := captureStdout(t, func() { renderer.Update(p) })
	if !strings.Contains(out2, "no report from the EDS for") {
		t.Errorf("expected stall warning once 90s pass with no new ts, got: %q", out2)
	}

	clock.Advance(1 * time.Second)
	out3 := captureStdout(t, func() {
		renderer.Update(&eds.Progress{Step: "syncing", TS: "t2", BytesTotal: 100, AnnexFilesTotal: 1})
	})
	if strings.Contains(out3, "no report from the EDS for") {
		t.Errorf("expected stall warning to clear once a new ts arrives, got: %q", out3)
	}
}

func TestEdsLiveRenderer_NonTTY_KeepAliveAfterFiveMinutes(t *testing.T) {
	clock := &fakeClock{t: time.Now()}
	renderer := newEDSLiveRenderer(false, clock.Now(), clock.Now)

	out1 := captureStdout(t, func() {
		renderer.Update(&eds.Progress{Step: "syncing", TS: "t1", BytesSynced: 10, BytesTotal: 100, AnnexFilesTotal: 1})
	})
	if strings.Count(out1, "\n") != 1 {
		t.Fatalf("expected initial line, got: %q", out1)
	}

	clock.Advance(4 * time.Minute)
	out2 := captureStdout(t, func() {
		renderer.Update(&eds.Progress{Step: "syncing", TS: "t2", BytesSynced: 10, BytesTotal: 100, AnnexFilesTotal: 1})
	})
	if out2 != "" {
		t.Errorf("expected no output before the 5-minute keep-alive, got: %q", out2)
	}

	clock.Advance(2 * time.Minute)
	out3 := captureStdout(t, func() {
		renderer.Update(&eds.Progress{Step: "syncing", TS: "t3", BytesSynced: 10, BytesTotal: 100, AnnexFilesTotal: 1})
	})
	if out3 == "" {
		t.Error("expected a keep-alive line once 5 minutes have passed since the last print")
	}
	if !strings.Contains(out3, "EDS reported") {
		t.Errorf("expected keep-alive line to include report age, got: %q", out3)
	}
}

func TestEdsLiveRenderer_NonTTY_NoCarriageReturnEver(t *testing.T) {
	clock := &fakeClock{t: time.Now()}
	renderer := newEDSLiveRenderer(false, clock.Now(), clock.Now)

	out := captureStdout(t, func() {
		renderer.Update(&eds.Progress{Step: "cloning", TS: "t1", BytesSynced: 1, BytesTotal: 10, AnnexFilesTotal: 1})
		renderer.Update(&eds.Progress{Step: "syncing", TS: "t2", BytesSynced: 2, BytesTotal: 10, AnnexFilesTotal: 1})
	})

	if strings.Contains(out, "\r") {
		t.Errorf("non-TTY output must never contain \\r, got: %q", out)
	}
}

func longestRealisticLiveLine(now time.Time) string {
	failure := "connection reset by peer\nwhile fetching annex object\nfrom remote storage backend after multiple retries"
	p := &eds.Progress{
		Step:            "waiting_for_repo_lock",
		BytesSynced:     12_300_000,
		BytesTotal:      57_900_000,
		FilesSynced:     0,
		AnnexFilesTotal: 1,
		Activities:      []eds.Activity{{State: "started", Attempt: 3, LastFailure: &failure}},
	}
	return formatEDSLiveLine(now, now.Add(-92*time.Second), "⠹", p, 125*time.Second, true, true)
}

func TestTruncateToWidth_FitsWithinWidthMinusOne(t *testing.T) {
	now := time.Now()
	line := longestRealisticLiveLine(now)

	for _, width := range []int{40, 80, 200} {
		got := truncateToWidth(line, width)
		if w := lipgloss.Width(got); w > width-1 {
			t.Errorf("width=%d: truncated line display width = %d, want <= %d; line: %q", width, w, width-1, got)
		}
		if strings.Contains(got, "\n") {
			t.Errorf("width=%d: truncated line must not contain a newline, got: %q", width, got)
		}
	}
}

func TestTruncateToWidth_ShortLineUnchanged(t *testing.T) {
	line := "  short line"
	if got := truncateToWidth(line, 80); got != line {
		t.Errorf("truncateToWidth should not alter a line shorter than width-1, got: %q", got)
	}
}

func TestTruncateToWidth_UsesDisplayWidthNotByteLength(t *testing.T) {
	// Spinner glyph, middle dot, warning glyph and lipgloss ANSI styling all
	// make byte length diverge from display width; truncation must be
	// driven by the latter.
	line := warnStyle.Render("⠹ syncing · 12.3 MB / 57.9 MB (0/1 files) · ⚠ no report from the EDS for 2m")
	if lipgloss.Width(line) == len(line) {
		t.Fatalf("fixture line must have byte length != display width to exercise this test, got equal: %d", len(line))
	}

	got := truncateToWidth(line, 40)
	if w := lipgloss.Width(got); w > 39 {
		t.Errorf("display width = %d, want <= 39; got: %q", w, got)
	}
}

func TestTruncateToWidth_StyledTailCutClosesOpenStyle(t *testing.T) {
	// Force ANSI output regardless of the test runner's terminal: lipgloss
	// otherwise detects a non-TTY and renders warnStyle as plain text, which
	// would make this test pass trivially.
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(old)

	// The stall warning (warnStyle, yellow) sits at the end of the line,
	// exactly where truncation happens, so it's the segment most at risk
	// of having its ANSI style opened but never reset by the cut.
	now := time.Now()
	p := &eds.Progress{
		Step:            "waiting_for_repo_lock",
		BytesSynced:     12_300_000,
		BytesTotal:      57_900_000,
		FilesSynced:     0,
		AnnexFilesTotal: 1,
	}
	line := formatEDSLiveLine(now, now.Add(-92*time.Second), "⠹", p, 125*time.Second, true, true)
	if !strings.Contains(line, "\x1b[") {
		t.Fatalf("fixture line must contain ANSI styling to exercise this test, got: %q", line)
	}

	sgr := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	for _, width := range []int{60, 70, 80} {
		got := truncateToWidth(line, width)
		if lipgloss.Width(line) <= width-1 {
			t.Fatalf("width=%d: fixture line must be longer than width-1 to exercise truncation", width)
		}
		if w := lipgloss.Width(got); w > width-1 {
			t.Errorf("width=%d: display width = %d, want <= %d; got: %q", width, w, width-1, got)
		}
		if stripped := sgr.ReplaceAllString(got, ""); strings.ContainsRune(stripped, '\x1b') {
			t.Errorf("width=%d: left a partial or unmatched escape sequence, got: %q", width, got)
		}
		if !strings.HasSuffix(got, "\x1b[0m") {
			t.Errorf("width=%d: truncated styled line must end with an SGR reset, got: %q", width, got)
		}
	}
}

func TestSanitizeLastFailure_CollapsesWhitespaceAndCaps(t *testing.T) {
	input := "connection reset\nby peer\n\twhile   fetching\nannex object from remote storage backend after multiple retries and backoffs"
	got := sanitizeLastFailure(input)

	if strings.Contains(got, "\n") {
		t.Errorf("sanitized LastFailure must not contain a newline, got: %q", got)
	}
	if strings.Contains(got, "  ") {
		t.Errorf("sanitized LastFailure must not contain repeated spaces, got: %q", got)
	}
	if r := []rune(got); len(r) > edsLastFailureMaxRunes+1 { // +1 for the ellipsis rune
		t.Errorf("sanitized LastFailure too long: %d runes, got: %q", len(r), got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("expected truncated LastFailure to end with an ellipsis, got: %q", got)
	}
}

func TestSanitizeLastFailure_ShortMessageUnchangedExceptWhitespace(t *testing.T) {
	got := sanitizeLastFailure("heartbeat timeout")
	if got != "heartbeat timeout" {
		t.Errorf("got %q, want unchanged short message", got)
	}
}

func TestRetryText_MultilineLastFailureRendersOnOneLine(t *testing.T) {
	failure := "connection reset by peer\nwhile fetching annex object\nfrom remote storage backend after multiple retries"
	text, ok := retryText([]eds.Activity{{State: "started", Attempt: 2, LastFailure: &failure}})
	if !ok {
		t.Fatal("expected ok=true")
	}
	if strings.Contains(text, "\n") {
		t.Errorf("retry text must not contain a newline, got: %q", text)
	}
}

func TestRenderRedrawLine_UsesCarriageReturnAndClearToEOL(t *testing.T) {
	out := renderRedrawLine("  hello", 80)
	if !strings.HasPrefix(out, "\r") {
		t.Errorf("expected redraw output to start with \\r, got: %q", out)
	}
	if !strings.HasSuffix(out, "\x1b[K") {
		t.Errorf("expected redraw output to end with the ANSI clear-line sequence, got: %q", out)
	}
	if strings.Contains(out, "  \x1b[K") || strings.HasSuffix(strings.TrimSuffix(out, "\x1b[K"), " ") {
		t.Errorf("redraw output must not pad with trailing spaces before the clear sequence, got: %q", out)
	}
}

func TestEdsTermWidth_FallsBackWhenSizeUnavailable(t *testing.T) {
	// go test's stdout is not a real TTY, so term.GetSize fails here and
	// edsTermWidth must fall back to edsFallbackTermWidth.
	if got := edsTermWidth(); got != edsFallbackTermWidth {
		t.Errorf("edsTermWidth() = %d, want fallback %d", got, edsFallbackTermWidth)
	}
}
