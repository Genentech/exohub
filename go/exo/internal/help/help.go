// Package help implements the EXO_HELP=short compact help mode and the
// computed man-page URL used to point both humans and agents at the full
// manual for a command.
package help

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/Genentech/exohub/go/exo/internal/defaults"
)

// ModeEnvVar is the environment variable selecting the help rendering mode.
const ModeEnvVar = "EXO_HELP"

// ShortMode reports whether EXO_HELP requests the compact help mode.
func ShortMode() bool {
	return strings.TrimSpace(os.Getenv(ModeEnvVar)) == "short"
}

// helpCommandUse is the literal Use string cobra assigns to the help command
// it auto-generates for any command with subcommands (InitDefaultHelpCmd).
// It identifies that command so its own --help links the man-page index
// rather than a nonexistent "exo-help" page.
const helpCommandUse = "help [command]"

// isHelpCommand reports whether cmd is cobra's auto-generated "help"
// command (e.g. "exo help --help"), as opposed to a real command named help.
func isHelpCommand(cmd *cobra.Command) bool {
	return cmd.Use == helpCommandUse
}

// IsHidden reports whether cmd, or any of its ancestors, is hidden. A
// command nested under a hidden command (e.g. a hidden stub's subcommands)
// has no man page either.
func IsHidden(cmd *cobra.Command) bool {
	for c := cmd; c != nil; c = c.Parent() {
		if c.Hidden {
			return true
		}
	}
	return false
}

// ManPageIndexURL returns the generated man-page site index, used for the
// root command and cobra's own "help" command instead of a specific page.
func ManPageIndexURL() string {
	return defaults.ManPageBaseURL() + "/docs/reference/cli/"
}

// ManPageURL computes the generated man-page URL for a specific command:
// <base>/docs/reference/cli/<command path with spaces replaced by '-'>/
func ManPageURL(cmd *cobra.Command) string {
	slug := strings.ReplaceAll(cmd.CommandPath(), " ", "-")
	return fmt.Sprintf("%s/docs/reference/cli/%s/", defaults.ManPageBaseURL(), slug)
}

// ManualURL returns the URL the "Full manual:" line should point to for
// cmd: the man-page index for the root command and cobra's "help" command
// (neither has its own generated page), otherwise cmd's specific man page.
func ManualURL(cmd *cobra.Command) string {
	if !cmd.HasParent() || isHelpCommand(cmd) {
		return ManPageIndexURL()
	}
	return ManPageURL(cmd)
}

// ManualLine returns the trailing "Full manual: <url>" line for cmd, or ""
// if cmd (or an ancestor) is hidden: website#276 only generates man pages
// for visible commands, so hidden commands (e.g. "exo serve") must not
// claim to have one.
func ManualLine(cmd *cobra.Command) string {
	if IsHidden(cmd) {
		return ""
	}
	return "Full manual: " + ManualURL(cmd)
}

// formatFlagsCompact renders fs's flags one per line, in the same style as
// cobra's default FlagUsages() ("  -x, --name type" or "      --name type"),
// but with the usage description dropped -- the full manual is one click
// away in short mode, so there is no point duplicating possibly-long prose
// here. Each line still ends with two trailing spaces so EDS's flag-line
// regex (which requires >=2 whitespace before the description group, even
// an empty one) keeps matching.
func formatFlagsCompact(fs *pflag.FlagSet) string {
	var lines []string
	fs.VisitAll(func(f *pflag.Flag) {
		if f.Hidden {
			return
		}
		var line string
		if f.Shorthand != "" {
			line = fmt.Sprintf("  -%s, --%s", f.Shorthand, f.Name)
		} else {
			line = fmt.Sprintf("      --%s", f.Name)
		}
		if varname, _ := pflag.UnquoteUsage(f); varname != "" {
			line += " " + varname
		}
		lines = append(lines, line+"  ")
	})
	return strings.Join(lines, "\n")
}

// RenderShort writes the compact help for cmd to w: its Short description,
// usage line, available subcommands (name + Short only), flag names/types
// without their descriptions, and a trailing "Full manual: <url>" line
// (omitted for hidden commands). The description-before-"Usage:" and
// "Flags:"/"Global Flags:" section layout matches cobra's default help so
// downstream parsers that scrape --help output keep working.
func RenderShort(cmd *cobra.Command, w io.Writer) {
	var b strings.Builder

	if short := strings.TrimSpace(cmd.Short); short != "" {
		b.WriteString(short)
		b.WriteString("\n\n")
	}

	b.WriteString("Usage:\n  ")
	b.WriteString(cmd.UseLine())
	b.WriteString("\n")

	if cmd.HasAvailableSubCommands() {
		b.WriteString("\nAvailable Commands:\n")
		for _, sub := range cmd.Commands() {
			if sub.IsAvailableCommand() || sub.Name() == "help" {
				fmt.Fprintf(&b, "  %-*s %s\n", sub.NamePadding(), sub.Name(), sub.Short)
			}
		}
	}

	if cmd.HasAvailableLocalFlags() {
		b.WriteString("\nFlags:\n")
		b.WriteString(formatFlagsCompact(cmd.LocalFlags()))
		b.WriteString("\n")
	}

	if cmd.HasAvailableInheritedFlags() {
		b.WriteString("\nGlobal Flags:\n")
		b.WriteString(formatFlagsCompact(cmd.InheritedFlags()))
		b.WriteString("\n")
	}

	if line := ManualLine(cmd); line != "" {
		b.WriteString("\n")
		b.WriteString(line)
		b.WriteString("\n")
	}

	fmt.Fprint(w, b.String())
}
