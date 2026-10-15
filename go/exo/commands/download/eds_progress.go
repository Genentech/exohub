//go:build artifactdb

package download

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/Genentech/exohub/go/exo/internal/eds"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"golang.org/x/term"
)

// edsSpinnerFrames animate the live progress line between polls. The
// spinner runs on its own ~100ms timer, independent of the poll interval
// (which stays at whatever the caller configured, 10s by default).
var edsSpinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const edsSpinnerInterval = 100 * time.Millisecond

// edsStallThreshold is how long to wait for a new heartbeat (ts) from the
// EDS before warning that it looks stalled. The EDS reports roughly every
// 30s (beat loop, exohub-workers !472), so this tolerates two missed
// heartbeats before warning.
const edsStallThreshold = 90 * time.Second

// edsFallbackTermWidth is used when the terminal width can't be read.
const edsFallbackTermWidth = 80

// edsLastFailureMaxRunes caps how much of an activity's LastFailure is
// shown in the retry text, after whitespace (including newlines) has been
// collapsed to single spaces.
const edsLastFailureMaxRunes = 60

// edsStepPhrases lightly humanises the step values the EDS emits
// (download_activities.py). Values not listed here are shown as-is.
var edsStepPhrases = map[string]string{
	"checking_permissions":    "checking permissions",
	"reading_permissions":     "reading permissions",
	"permission_granted":      "permission granted",
	"checking_public_access":  "checking public access",
	"public_access_confirmed": "public access confirmed",
	"waiting_for_repo_lock":   "waiting for another download of this dataset",
	"cloning":                 "cloning",
	"clone_complete":          "clone complete",
	"exo_init":                "initialising",
	"exo_init_complete":       "initialisation complete",
	"syncing":                 "syncing",
}

// humanizeStep turns an EDS step value into a short human phrase. Unknown
// values pass through unchanged rather than erroring, since the EDS can add
// new steps independently of the CLI.
func humanizeStep(step string) string {
	if phrase, ok := edsStepPhrases[step]; ok {
		return phrase
	}
	return step
}

// formatDuration renders a duration the way the live/final lines want it:
// "12s", "1m32s", "2m" (no trailing "0s" once there's a minute component).
// This differs from time.Duration.String(), which would print "2m0s".
func formatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	d = d.Round(time.Second)

	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second

	switch {
	case h > 0:
		return fmt.Sprintf("%dh%dm", h, m)
	case m > 0:
		if s == 0 {
			return fmt.Sprintf("%dm", m)
		}
		return fmt.Sprintf("%dm%ds", m, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}

// reportAge returns how long it has been since the current EDS heartbeat
// (ts) was first observed locally, and whether any heartbeat has been seen
// yet. It is computed from the local wall-clock time a new ts value was
// first seen, not from now-minus-ts, so client/EDS clock skew cannot affect
// it.
func reportAge(now, firstSeenAt time.Time, hasSeenReport bool) (time.Duration, bool) {
	if !hasSeenReport {
		return 0, false
	}
	return now.Sub(firstSeenAt), true
}

// isStalled reports whether a report age means the EDS looks stalled.
func isStalled(age time.Duration) bool {
	return age > edsStallThreshold
}

// retryText reports the retry line for the first activity with an attempt
// greater than 1, making a heartbeat-timeout retry (exohub-workers #66)
// visible instead of silent.
func retryText(activities []eds.Activity) (string, bool) {
	for _, a := range activities {
		if a.Attempt <= 1 {
			continue
		}
		if a.LastFailure != nil && *a.LastFailure != "" {
			return fmt.Sprintf("retrying (attempt %d: %s)", a.Attempt, sanitizeLastFailure(*a.LastFailure)), true
		}
		return fmt.Sprintf("retrying (attempt %d)", a.Attempt), true
	}
	return "", false
}

// sanitizeLastFailure collapses all whitespace (including newlines) in an
// EDS-reported failure message to single spaces and caps its length, so a
// multi-line or very long LastFailure can't break the single-line progress
// output (TTY and non-TTY alike).
func sanitizeLastFailure(s string) string {
	collapsed := strings.Join(strings.FieldsFunc(s, unicode.IsSpace), " ")
	runes := []rune(collapsed)
	if len(runes) <= edsLastFailureMaxRunes {
		return collapsed
	}
	return string(runes[:edsLastFailureMaxRunes]) + "…"
}

// formatEDSFinalLine builds the single determinate-bar line printed once an
// operation completes successfully, using the numbers in Result (Progress
// is nil by then). "Present" means AnnexFilesTotal-len(MissingFiles) when
// Verified is true: FilesSynced counts fetch attempts, not files confirmed
// present. "verified" is only claimed when Result.Verified is true. The
// byte figure shown next to a 100% bar is BytesTotal, not BytesSynced, when
// the download is fully verified with nothing missing: BytesSynced
// under-counts content that was already present (a live winery run reported
// bytes_synced below bytes_total with verified: true and no missing files),
// which would print a mismatched "100% <smaller number>".
func formatEDSFinalLine(result *eds.Result) string {
	if result == nil {
		return ""
	}

	bar := progressBar(100, 20)

	var present int64
	var verifiedSuffix string
	bytes := result.BytesSynced
	if result.Verified {
		present = result.AnnexFilesTotal - int64(len(result.MissingFiles))
		verifiedSuffix = ", verified"
		if len(result.MissingFiles) == 0 {
			bytes = result.BytesTotal
		}
	} else {
		present = result.FilesSynced
	}

	return fmt.Sprintf("  %s %s (%d/%d files%s)",
		progressStyle.Render(bar),
		formatSize(bytes),
		present, result.AnnexFilesTotal, verifiedSuffix,
	)
}

// formatEDSLiveLine is the pure renderer behind the live TTY and non-TTY
// progress lines. It takes an explicit clock (now) and all other state as
// arguments so it can be unit-tested without timing flakiness. spinnerFrame
// is omitted from the line entirely when empty (the non-TTY case).
func formatEDSLiveLine(now, submittedAt time.Time, spinnerFrame string, p *eds.Progress, age time.Duration, hasSeenReport, stalled bool) string {
	phase := "starting"
	var bytesSynced, bytesTotal, filesSynced, filesTotal int64
	var activities []eds.Activity
	if p != nil {
		phase = humanizeStep(p.Step)
		bytesSynced, bytesTotal = p.BytesSynced, p.BytesTotal
		filesSynced, filesTotal = p.FilesSynced, p.AnnexFilesTotal
		activities = p.Activities
	}

	phaseSegment := phase
	if spinnerFrame != "" {
		phaseSegment = spinnerFrame + " " + phase
	}

	var counters string
	if bytesTotal > 0 {
		counters = fmt.Sprintf("%s / %s (%d/%d files)",
			formatSize(bytesSynced), formatSize(bytesTotal), filesSynced, filesTotal)
	} else {
		counters = fmt.Sprintf("%s synced", formatSize(bytesSynced))
	}

	elapsed := formatDuration(now.Sub(submittedAt))

	var reportPart string
	switch {
	case !hasSeenReport:
		reportPart = "waiting for first report"
	case stalled:
		reportPart = warnStyle.Render(fmt.Sprintf("⚠ no report from the EDS for %s", formatDuration(age)))
	default:
		reportPart = fmt.Sprintf("EDS reported %s ago", formatDuration(age))
	}

	segments := []string{phaseSegment, counters, elapsed, reportPart}
	if text, ok := retryText(activities); ok {
		segments = append(segments, text)
	}

	return "  " + strings.Join(segments, " · ")
}

// edsChangeKey identifies the parts of the live state that should trigger a
// new line on non-TTY output: phase, counters, stall state, and retry
// state. Elapsed time and the exact report age are deliberately excluded
// since those change on every tick and would defeat de-duplication.
func edsChangeKey(p *eds.Progress, stalled bool, retry string) string {
	var step string
	var bytesSynced, bytesTotal, filesSynced, filesTotal int64
	if p != nil {
		step = p.Step
		bytesSynced, bytesTotal, filesSynced, filesTotal = p.BytesSynced, p.BytesTotal, p.FilesSynced, p.AnnexFilesTotal
	}
	return fmt.Sprintf("%s|%d|%d|%d|%d|%v|%s", step, bytesSynced, bytesTotal, filesSynced, filesTotal, stalled, retry)
}

// edsLiveRenderer drives the live progress output for `exo download --eds`.
// On a TTY it redraws a single line in place, animated by its own ~100ms
// ticker so the spinner moves between polls (which stay at the configured
// poll interval, 10s by default). On a non-TTY it prints a new line only
// when the phase, counters, stall state, or retry state changes, plus a
// keep-alive every edsProgressKeepAlive.
type edsLiveRenderer struct {
	isTTY       bool
	submittedAt time.Time
	clock       func() time.Time

	mu            sync.Mutex
	latest        *eds.Progress
	hasSeenReport bool
	lastTS        string
	firstSeenAt   time.Time

	spinnerIdx int
	stopCh     chan struct{}
	stoppedCh  chan struct{}

	lastPrintedKey string
	lastPrintedAt  time.Time
}

// newEDSLiveRenderer starts a renderer for a download submitted at
// submittedAt (using clock as the time source, so tests can inject a fake
// one). On a TTY it prints the one-time explanation of why the transfer
// figures jump, and starts the spinner ticker.
func newEDSLiveRenderer(isTTY bool, submittedAt time.Time, clock func() time.Time) *edsLiveRenderer {
	r := &edsLiveRenderer{
		isTTY:       isTTY,
		submittedAt: submittedAt,
		clock:       clock,
	}
	if !r.isTTY {
		return r
	}

	fmt.Println(dimStyle.Render("  transfer figures refresh every ~2 min on the EDS"))

	r.stopCh = make(chan struct{})
	r.stoppedCh = make(chan struct{})
	go r.run()
	return r
}

func (r *edsLiveRenderer) run() {
	defer close(r.stoppedCh)
	ticker := time.NewTicker(edsSpinnerInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.stopCh:
			return
		case <-ticker.C:
			r.redraw()
		}
	}
}

func (r *edsLiveRenderer) redraw() {
	r.mu.Lock()
	frame := edsSpinnerFrames[r.spinnerIdx%len(edsSpinnerFrames)]
	r.spinnerIdx++
	now := r.clock()
	age, hasReport := reportAge(now, r.firstSeenAt, r.hasSeenReport)
	stalled := hasReport && isStalled(age)
	p := r.latest
	r.mu.Unlock()

	line := formatEDSLiveLine(now, r.submittedAt, frame, p, age, hasReport, stalled)
	fmt.Print(renderRedrawLine(line, edsTermWidth()))
}

// renderRedrawLine composes the escape sequence for one in-place TTY
// redraw: carriage return, the line truncated to width, then a clear to
// end-of-line (so a shorter line than the previous one leaves no stray
// characters, without padding with spaces).
func renderRedrawLine(line string, width int) string {
	return "\r" + truncateToWidth(line, width) + "\x1b[K"
}

// edsTermWidth returns the current terminal width, falling back to
// edsFallbackTermWidth when it can't be read (e.g. not a real TTY), so a
// resize takes effect on the next redraw.
func edsTermWidth() int {
	width, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || width <= 0 {
		return edsFallbackTermWidth
	}
	return width
}

// truncateToWidth truncates line to at most width-1 display columns,
// appending an ellipsis when cut. width-1 leaves room so the cursor doesn't
// sit in the last column, which on some terminals triggers an unwanted wrap.
//
// It uses ansi.Truncate rather than cutting runes directly: the line can
// carry lipgloss ANSI styling (e.g. the stall warning), and a styled
// segment can sit right at the cut point. Rune-by-rune truncation can keep
// an opening style code while dropping its closing reset, or even split an
// escape sequence in two; ansi.Truncate measures display width and never
// breaks a sequence. An explicit trailing reset is still added as a
// safety net so a truncated line can never leave the terminal in a styled
// state, regardless of exactly where in the original string any reset fell.
func truncateToWidth(line string, width int) string {
	maxWidth := width - 1
	if maxWidth < 0 {
		maxWidth = 0
	}
	if lipgloss.Width(line) <= maxWidth {
		return line
	}
	if maxWidth == 0 {
		return ""
	}

	return ansi.Truncate(line, maxWidth, "…") + "\x1b[0m"
}

// Update is the progressFn passed to eds.PollOperation. It stores the
// latest snapshot and, when a new (non-empty) ts value appears, records the
// local time it was first seen — the basis for the report-age calculation.
func (r *edsLiveRenderer) Update(p *eds.Progress) {
	if p == nil {
		return
	}
	now := r.clock()

	r.mu.Lock()
	r.latest = p
	if p.TS != "" && p.TS != r.lastTS {
		r.lastTS = p.TS
		r.firstSeenAt = now
		r.hasSeenReport = true
	}
	r.mu.Unlock()

	if !r.isTTY {
		r.printNonTTY(p, now)
	}
}

func (r *edsLiveRenderer) printNonTTY(p *eds.Progress, now time.Time) {
	r.mu.Lock()
	age, hasReport := reportAge(now, r.firstSeenAt, r.hasSeenReport)
	r.mu.Unlock()
	stalled := hasReport && isStalled(age)
	retry, _ := retryText(p.Activities)

	key := edsChangeKey(p, stalled, retry)

	r.mu.Lock()
	changed := key != r.lastPrintedKey
	keepAliveDue := r.lastPrintedAt.IsZero() || now.Sub(r.lastPrintedAt) >= edsProgressKeepAlive
	if !changed && !keepAliveDue {
		r.mu.Unlock()
		return
	}
	r.lastPrintedKey = key
	r.lastPrintedAt = now
	r.mu.Unlock()

	fmt.Println(formatEDSLiveLine(now, r.submittedAt, "", p, age, hasReport, stalled))
}

// Stop stops the spinner ticker (TTY only) and clears the current line so
// callers can safely print final, error, or Ctrl-C output afterward. It is
// a no-op on non-TTY output, which never redraws in place.
func (r *edsLiveRenderer) Stop() {
	if !r.isTTY {
		return
	}
	close(r.stopCh)
	<-r.stoppedCh

	fmt.Print("\r\x1b[K")
}
