package main

import (
	"bytes"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/Genentech/exohub/go/exo/internal/defaults"
	"github.com/Genentech/exohub/go/exo/internal/help"
)

// TestExitCode verifies exit code extraction from errors
func TestExitCode(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		expected    int
		description string
	}{
		{
			name:        "nil_error",
			err:         nil,
			expected:    0,
			description: "nil error should return exit code 0",
		},
		{
			name:        "generic_error",
			err:         exec.ErrNotFound,
			expected:    1,
			description: "generic error should return exit code 1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldStderr := os.Stderr
			r, w, _ := os.Pipe()
			os.Stderr = w

			code := exitCode(tt.err)

			w.Close()
			os.Stderr = oldStderr

			buf := make([]byte, 1024)
			r.Read(buf)

			if code != tt.expected {
				t.Errorf("%s: exitCode() = %d, want %d", tt.description, code, tt.expected)
			}
		})
	}
}

// TestExitCodeWithExitError verifies exit code extraction from ExitError
func TestExitCodeWithExitError(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 5")
	err := cmd.Run()

	if err == nil {
		t.Fatal("Expected command to fail with exit code 5")
	}

	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("Expected *exec.ExitError, got %T", err)
	}

	code := exitCode(exitErr)
	if code != 5 {
		t.Errorf("exitCode() = %d, want 5", code)
	}
}

// TestExecCommand verifies the execCommand helper function
func TestExecCommand(t *testing.T) {
	err := execCommand("echo", "test")
	if err != nil {
		t.Errorf("execCommand('echo', 'test') failed: %v", err)
	}
}

// TestExecCommandWithFailure verifies error handling in execCommand
func TestExecCommandWithFailure(t *testing.T) {
	err := execCommand("nonexistent-command-12345")
	if err == nil {
		t.Error("execCommand with nonexistent command should return an error")
	}
}

// TestExecCommandWithNonZeroExit verifies exit code handling
func TestExecCommandWithNonZeroExit(t *testing.T) {
	err := execCommand("sh", "-c", "exit 42")

	if err == nil {
		t.Fatal("Expected command to return an error")
	}

	if exitErr, ok := err.(*exec.ExitError); ok {
		code := exitErr.ExitCode()
		if code != 42 {
			t.Errorf("Expected exit code 42, got %d", code)
		}
	} else {
		t.Errorf("Expected *exec.ExitError, got %T", err)
	}
}

func runHelp(t *testing.T, args ...string) string {
	t.Helper()
	root := buildRootCmd("dev", "", "")
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(append(args, "--help"))
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute(%v) failed: %v", args, err)
	}
	return buf.String()
}

// TestFullHelp_EndsWithManPageURL verifies that EXO_HELP unset (full/default)
// still renders today's full help, plus a trailing man-page URL line.
func TestFullHelp_EndsWithManPageURL(t *testing.T) {
	t.Setenv("EXO_HELP", "")
	out := runHelp(t, "sync")

	if !strings.Contains(out, "Sync git-annex content and record bad files") {
		t.Errorf("full help missing Long/Short description:\n%s", out)
	}
	if !strings.Contains(out, "Usage:") || !strings.Contains(out, "Flags:") {
		t.Errorf("full help missing Usage/Flags sections:\n%s", out)
	}

	want := "Full manual: " + defaults.ManPageBaseURL() + "/docs/reference/cli/exo-sync/"
	trimmed := strings.TrimRight(out, "\n")
	if !strings.HasSuffix(trimmed, want) {
		t.Errorf("full help does not end with %q:\n%s", want, out)
	}
}

// TestFullHelp_ManPageURL_NestedCommand verifies the man-page URL pattern
// for a nested command.
func TestFullHelp_ManPageURL_NestedCommand(t *testing.T) {
	t.Setenv("EXO_HELP", "")
	out := runHelp(t, "mcp", "list")

	want := "Full manual: " + defaults.ManPageBaseURL() + "/docs/reference/cli/exo-mcp-list/"
	trimmed := strings.TrimRight(out, "\n")
	if !strings.HasSuffix(trimmed, want) {
		t.Errorf("full help does not end with %q:\n%s", want, out)
	}
}

// TestShortHelp_Layout verifies EXO_HELP=short produces a compact help that
// still exposes the description-before-Usage:/Flags:/Global Flags: layout
// EDS's --help parser relies on, and ends with the man-page URL.
func TestShortHelp_Layout(t *testing.T) {
	t.Setenv("EXO_HELP", "short")
	fullOut := func() string {
		t.Setenv("EXO_HELP", "")
		defer t.Setenv("EXO_HELP", "short")
		return runHelp(t, "sync")
	}()
	shortOut := runHelp(t, "sync")

	if len(strings.Split(shortOut, "\n")) >= len(strings.Split(fullOut, "\n")) {
		t.Errorf("EXO_HELP=short output (%d lines) is not shorter than full output (%d lines)",
			len(strings.Split(shortOut, "\n")), len(strings.Split(fullOut, "\n")))
	}

	descIdx := strings.Index(shortOut, "Usage:")
	flagsIdx := strings.Index(shortOut, "Flags:")
	if descIdx == -1 || flagsIdx == -1 || flagsIdx < descIdx {
		t.Errorf("short help missing description-before-Usage:/Flags: layout:\n%s", shortOut)
	}

	want := "Full manual: " + defaults.ManPageBaseURL() + "/docs/reference/cli/exo-sync/"
	trimmed := strings.TrimRight(shortOut, "\n")
	if !strings.HasSuffix(trimmed, want) {
		t.Errorf("short help does not end with %q:\n%s", want, shortOut)
	}
}

// TestShortDescriptions_WithinBudget walks every registered command and
// asserts its Short description stays within the agreed agent context
// budget: short, single-sentence, no trailing period.
func TestShortDescriptions_WithinBudget(t *testing.T) {
	const maxShortLen = 100
	multiSentence := regexp.MustCompile(`\.\s+[A-Z]`)

	root := buildRootCmd("dev", "", "")
	var walk func(cmd *cobra.Command)
	walk = func(cmd *cobra.Command) {
		if cmd.Short != "" {
			if len(cmd.Short) > maxShortLen {
				t.Errorf("%s: Short is %d chars (max %d): %q", cmd.CommandPath(), len(cmd.Short), maxShortLen, cmd.Short)
			}
			if multiSentence.MatchString(cmd.Short) {
				t.Errorf("%s: Short looks like multiple sentences, move prose to Long: %q", cmd.CommandPath(), cmd.Short)
			}
		}
		for _, sub := range cmd.Commands() {
			walk(sub)
		}
	}
	walk(root)
}

// TestManualLine_RootAndHelpCommand_LinkIndex verifies that the root
// command's and cobra's own "help" command's full help end with the
// generated man-page index, not a per-command slug (website#276 only
// generates pages for visible, specific commands).
func TestManualLine_RootAndHelpCommand_LinkIndex(t *testing.T) {
	t.Setenv("EXO_HELP", "")
	want := "Full manual: " + help.ManPageIndexURL()

	rootOut := runHelp(t)
	if trimmed := strings.TrimRight(rootOut, "\n"); !strings.HasSuffix(trimmed, want) {
		t.Errorf("root --help does not end with %q:\n%s", want, rootOut)
	}

	helpOut := runHelp(t, "help")
	if trimmed := strings.TrimRight(helpOut, "\n"); !strings.HasSuffix(trimmed, want) {
		t.Errorf("exo help --help does not end with %q:\n%s", want, helpOut)
	}
}

// TestManualLine_HiddenCommand_Omitted verifies that a hidden command (e.g.
// exo serve) prints no "Full manual:" line in either help mode, since
// website#276 does not generate a page for it.
func TestManualLine_HiddenCommand_Omitted(t *testing.T) {
	root := buildRootCmd("dev", "", "")
	cmd, _, err := root.Find([]string{"serve"})
	if err != nil {
		t.Fatalf("find serve: %v", err)
	}
	if !help.IsHidden(cmd) {
		t.Fatalf("expected exo serve to be hidden")
	}

	t.Setenv("EXO_HELP", "")
	fullOut := runHelp(t, "serve")
	if strings.Contains(fullOut, "Full manual:") {
		t.Errorf("full help for hidden command exo serve contains a Full manual: line:\n%s", fullOut)
	}

	t.Setenv("EXO_HELP", "short")
	shortOut := runHelp(t, "serve")
	if strings.Contains(shortOut, "Full manual:") {
		t.Errorf("short help for hidden command exo serve contains a Full manual: line:\n%s", shortOut)
	}
}

// TestManPageURL_AllVisibleCommands walks the whole registered command tree
// and asserts every visible, non-root, non-help command's man-page URL
// matches <base>/docs/reference/cli/exo-<words-joined-by-dashes>/ — the
// contract shared with website#276's man-page generator.
func TestManPageURL_AllVisibleCommands(t *testing.T) {
	base := defaults.ManPageBaseURL()
	urlPattern := regexp.MustCompile(`^` + regexp.QuoteMeta(base) + `/docs/reference/cli/exo(-[a-z0-9]+(-[a-z0-9]+)*)?/$`)

	root := buildRootCmd("dev", "", "")
	var walk func(cmd *cobra.Command)
	walk = func(cmd *cobra.Command) {
		if cmd.HasParent() && !help.IsHidden(cmd) && cmd.Use != "help [command]" {
			got := help.ManualURL(cmd)
			wantSlug := strings.ReplaceAll(cmd.CommandPath(), " ", "-")
			want := base + "/docs/reference/cli/" + wantSlug + "/"
			if got != want {
				t.Errorf("%s: ManualURL() = %q, want %q", cmd.CommandPath(), got, want)
			}
			if !urlPattern.MatchString(got) {
				t.Errorf("%s: ManualURL() = %q does not match the expected pattern", cmd.CommandPath(), got)
			}
		}
		for _, sub := range cmd.Commands() {
			walk(sub)
		}
	}
	walk(root)
}
