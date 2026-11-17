package help

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/Genentech/exohub/go/exo/internal/defaults"
)

func newTestTree() *cobra.Command {
	root := &cobra.Command{Use: "exo", Short: "ExoHub CLI"}
	atlas := &cobra.Command{Use: "atlas", Short: "Browse and search an ArtifactDB catalog"}
	serve := &cobra.Command{
		Use:   "serve",
		Short: "Start the web server for browser-based catalog browsing",
		Run:   func(*cobra.Command, []string) {},
	}
	serve.Flags().String("port", "8080", "Port to listen on")
	atlas.AddCommand(serve)
	root.AddCommand(atlas)

	hiddenCmd := &cobra.Command{
		Use:    "admin",
		Short:  "Internal admin tooling",
		Hidden: true,
		Run:    func(*cobra.Command, []string) {},
	}
	hiddenSub := &cobra.Command{
		Use:   "reset",
		Short: "Reset internal state",
		Run:   func(*cobra.Command, []string) {},
	}
	hiddenCmd.AddCommand(hiddenSub)
	root.AddCommand(hiddenCmd)

	root.InitDefaultHelpCmd()
	return root
}

func TestManPageURL_NestedCommand(t *testing.T) {
	root := newTestTree()
	atlas, _, err := root.Find([]string{"atlas"})
	if err != nil {
		t.Fatalf("find atlas: %v", err)
	}
	serve, _, err := root.Find([]string{"atlas", "serve"})
	if err != nil {
		t.Fatalf("find atlas serve: %v", err)
	}

	base := defaults.ManPageBaseURL()

	gotAtlas := ManPageURL(atlas)
	wantAtlas := base + "/docs/reference/cli/exo-atlas/"
	if gotAtlas != wantAtlas {
		t.Errorf("ManPageURL(atlas) = %q, want %q", gotAtlas, wantAtlas)
	}

	gotServe := ManPageURL(serve)
	wantServe := base + "/docs/reference/cli/exo-atlas-serve/"
	if gotServe != wantServe {
		t.Errorf("ManPageURL(atlas serve) = %q, want %q", gotServe, wantServe)
	}
}

func TestManPageURL_EnvOverride(t *testing.T) {
	t.Setenv("EXOHUB_DOCS_URL", "https://internal.example.com/exohub/llms.txt")
	root := newTestTree()
	serve, _, err := root.Find([]string{"atlas", "serve"})
	if err != nil {
		t.Fatalf("find atlas serve: %v", err)
	}

	got := ManPageURL(serve)
	want := "https://internal.example.com/exohub/docs/reference/cli/exo-atlas-serve/"
	if got != want {
		t.Errorf("ManPageURL(atlas serve) = %q, want %q", got, want)
	}
}

func TestShortMode(t *testing.T) {
	t.Setenv(ModeEnvVar, "short")
	if !ShortMode() {
		t.Error("ShortMode() = false, want true when EXO_HELP=short")
	}

	t.Setenv(ModeEnvVar, "")
	if ShortMode() {
		t.Error("ShortMode() = true, want false when EXO_HELP is unset")
	}

	t.Setenv(ModeEnvVar, "full")
	if ShortMode() {
		t.Error("ShortMode() = true, want false when EXO_HELP=full")
	}
}

func TestRenderShort_Layout(t *testing.T) {
	root := newTestTree()
	serve, _, err := root.Find([]string{"atlas", "serve"})
	if err != nil {
		t.Fatalf("find atlas serve: %v", err)
	}

	var b strings.Builder
	RenderShort(serve, &b)
	out := b.String()

	descIdx := strings.Index(out, serve.Short)
	usageIdx := strings.Index(out, "Usage:")
	flagsIdx := strings.Index(out, "Flags:")
	manualIdx := strings.Index(out, "Full manual: ")

	if descIdx == -1 || usageIdx == -1 || flagsIdx == -1 || manualIdx == -1 {
		t.Fatalf("missing expected sections in short help:\n%s", out)
	}
	if !(descIdx < usageIdx && usageIdx < flagsIdx && flagsIdx < manualIdx) {
		t.Errorf("short help sections out of order (description=%d, Usage=%d, Flags=%d, Full manual=%d):\n%s",
			descIdx, usageIdx, flagsIdx, manualIdx, out)
	}
	if !strings.Contains(out, "exo atlas serve [flags]") {
		t.Errorf("short help missing usage line:\n%s", out)
	}
	if !strings.HasSuffix(strings.TrimRight(out, "\n"), ManPageURL(serve)) {
		t.Errorf("short help does not end with the man page URL:\n%s", out)
	}
	if strings.Count(out, "\n") > 12 {
		t.Errorf("short help is not compact (%d lines):\n%s", strings.Count(out, "\n"), out)
	}
}

// TestRenderShort_FlagsHaveNoDescriptions confirms short mode drops each
// flag's usage text (the full manual is one click away) while still
// printing name/shorthand/type, and still satisfies EDS's flag-line regex
// (>=2 trailing spaces before the now-empty description).
func TestRenderShort_FlagsHaveNoDescriptions(t *testing.T) {
	root := newTestTree()
	serve, _, err := root.Find([]string{"atlas", "serve"})
	if err != nil {
		t.Fatalf("find atlas serve: %v", err)
	}

	var b strings.Builder
	RenderShort(serve, &b)
	out := b.String()

	if !strings.Contains(out, "--port string  \n") {
		t.Errorf("expected a bare '--port string' flag line with no description:\n%q", out)
	}
	if strings.Contains(out, "Port to listen on") {
		t.Errorf("short help still contains the flag's usage text:\n%s", out)
	}
}

func TestRenderShort_WithSubcommands(t *testing.T) {
	root := newTestTree()
	atlas, _, err := root.Find([]string{"atlas"})
	if err != nil {
		t.Fatalf("find atlas: %v", err)
	}

	var b strings.Builder
	RenderShort(atlas, &b)
	out := b.String()

	if !strings.Contains(out, "Available Commands:") || !strings.Contains(out, "serve") {
		t.Errorf("short help for a parent command should list subcommands:\n%s", out)
	}
}

// TestManualURL_RootUsesIndex: website#276 generates a man page per visible
// command but not one for the root itself, so the root's "Full manual:"
// line must point at the generated index, not a nonexistent "exo/" page.
func TestManualURL_RootUsesIndex(t *testing.T) {
	root := newTestTree()
	want := ManPageIndexURL()
	if got := ManualURL(root); got != want {
		t.Errorf("ManualURL(root) = %q, want index %q", got, want)
	}
	if got := ManualLine(root); got != "Full manual: "+want {
		t.Errorf("ManualLine(root) = %q, want %q", got, "Full manual: "+want)
	}
}

// TestManualURL_HelpCommandUsesIndex: cobra's auto-generated "help" command
// (e.g. "exo help --help") has no generated page of its own either.
func TestManualURL_HelpCommandUsesIndex(t *testing.T) {
	root := newTestTree()
	helpCmd, _, err := root.Find([]string{"help"})
	if err != nil {
		t.Fatalf("find help: %v", err)
	}

	want := ManPageIndexURL()
	if got := ManualURL(helpCmd); got != want {
		t.Errorf("ManualURL(help) = %q, want index %q", got, want)
	}
}

// TestManualLine_Hidden: hidden commands, and commands nested under a
// hidden ancestor, have no generated man page, so ManualLine must be empty
// for them in either help mode.
func TestManualLine_Hidden(t *testing.T) {
	root := newTestTree()

	admin, _, err := root.Find([]string{"admin"})
	if err != nil {
		t.Fatalf("find admin: %v", err)
	}
	if !IsHidden(admin) {
		t.Fatalf("expected admin to be hidden")
	}
	if got := ManualLine(admin); got != "" {
		t.Errorf("ManualLine(admin) = %q, want empty for a hidden command", got)
	}

	reset, _, err := root.Find([]string{"admin", "reset"})
	if err != nil {
		t.Fatalf("find admin reset: %v", err)
	}
	if !IsHidden(reset) {
		t.Fatalf("expected admin reset to be hidden via its ancestor")
	}
	if got := ManualLine(reset); got != "" {
		t.Errorf("ManualLine(admin reset) = %q, want empty for a command with a hidden ancestor", got)
	}

	var b strings.Builder
	RenderShort(admin, &b)
	if strings.Contains(b.String(), "Full manual:") {
		t.Errorf("RenderShort for a hidden command printed a Full manual: line:\n%s", b.String())
	}
}

// TestManualURL_VisibleCommand confirms a normal visible, non-root,
// non-help command still gets its specific man-page URL, not the index.
func TestManualURL_VisibleCommand(t *testing.T) {
	root := newTestTree()
	serve, _, err := root.Find([]string{"atlas", "serve"})
	if err != nil {
		t.Fatalf("find atlas serve: %v", err)
	}

	want := ManPageURL(serve)
	if got := ManualURL(serve); got != want {
		t.Errorf("ManualURL(atlas serve) = %q, want %q", got, want)
	}
	if want == ManPageIndexURL() {
		t.Fatalf("test fixture bug: visible command's URL should not equal the index")
	}
}
