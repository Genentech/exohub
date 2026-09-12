package doctor

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"

	"github.com/charmbracelet/lipgloss"
)

// Status represents the outcome of a diagnostic check.
type Status string

const (
	StatusPass Status = "PASS"
	StatusWarn Status = "WARN"
	StatusFail Status = "FAIL"
	StatusSkip Status = "SKIP"
)

// CheckResult is the result of a single diagnostic check.
type CheckResult struct {
	ID          string `json:"id"`
	Group       string `json:"group"`
	Status      Status `json:"status"`
	Summary     string `json:"summary"`
	Detail      string `json:"detail,omitempty"`
	Remediation string `json:"remediation,omitempty"`
}

// Check is a function that runs a single diagnostic and returns a result.
type Check func() CheckResult

// pass/warn/fail/skip helpers
func pass(id, group, summary string) CheckResult {
	return CheckResult{ID: id, Group: group, Status: StatusPass, Summary: summary}
}

func warn(id, group, summary, detail, remediation string) CheckResult {
	return CheckResult{ID: id, Group: group, Status: StatusWarn, Summary: summary, Detail: detail, Remediation: remediation}
}

func fail(id, group, summary, detail, remediation string) CheckResult {
	return CheckResult{ID: id, Group: group, Status: StatusFail, Summary: summary, Detail: detail, Remediation: remediation}
}

func skip(id, group, summary string) CheckResult {
	return CheckResult{ID: id, Group: group, Status: StatusSkip, Summary: summary}
}

// hasFail returns true if any result has FAIL status.
func hasFail(results []CheckResult) bool {
	for _, r := range results {
		if r.Status == StatusFail {
			return true
		}
	}
	return false
}

// PrintResults writes human-readable grouped output to w.
func PrintResults(w io.Writer, results []CheckResult, verbose bool) {
	passStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Bold(true)   // green
	warnStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Bold(true)   // yellow
	failStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)   // red
	skipStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))              // gray
	groupStyle := lipgloss.NewStyle().Bold(true).Underline(true)
	detailStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	remStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("6"))

	currentGroup := ""
	for _, r := range results {
		if r.Group != currentGroup {
			if currentGroup != "" {
				fmt.Fprintln(w)
			}
			fmt.Fprintln(w, groupStyle.Render(r.Group))
			currentGroup = r.Group
		}

		var glyph, label string
		switch r.Status {
		case StatusPass:
			glyph = passStyle.Render("✓")
			label = passStyle.Render("PASS")
		case StatusWarn:
			glyph = warnStyle.Render("⚠")
			label = warnStyle.Render("WARN")
		case StatusFail:
			glyph = failStyle.Render("✗")
			label = failStyle.Render("FAIL")
		case StatusSkip:
			glyph = skipStyle.Render("–")
			label = skipStyle.Render("SKIP")
		}

		fmt.Fprintf(w, "  %s  [%s] %s\n", glyph, label, r.Summary)
		if verbose || r.Status == StatusFail || r.Status == StatusWarn {
			if r.Detail != "" {
				fmt.Fprintf(w, "       %s\n", detailStyle.Render(r.Detail))
			}
			if r.Remediation != "" {
				fmt.Fprintf(w, "       %s\n", remStyle.Render("→ "+r.Remediation))
			}
		}
	}
	fmt.Fprintln(w)
}

// PrintJSON writes the results as a JSON array to w.
func PrintJSON(w io.Writer, results []CheckResult) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(results)
}

// secretPatternAWSKey matches AWS access key IDs (ASIA/AKIA/AROA/AIDA + 16 chars).
var secretPatternAWSKey = regexp.MustCompile(`(?i)\b(ASIA|AKIA|AROA|AIDA)[A-Z0-9]{12,}\b`)

// secretPatternJWT matches raw JWTs (three base64url segments separated by dots).
var secretPatternJWT = regexp.MustCompile(`[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`)

// secretPatternB64 matches base64 blobs that have explicit padding (={1,2}) — a
// reliable signal of an encoded secret (session tokens, secret keys). We require
// padding so plain S3 URLs and other long alphanumeric strings are not redacted.
var secretPatternB64 = regexp.MustCompile(`[A-Za-z0-9+/]{40,}={1,2}`)

// Redact replaces any secret-looking substrings in s with [REDACTED].
func Redact(s string) string {
	s = secretPatternAWSKey.ReplaceAllString(s, "[REDACTED]")
	s = secretPatternJWT.ReplaceAllString(s, "[REDACTED]")
	s = secretPatternB64.ReplaceAllString(s, "[REDACTED]")
	return s
}
