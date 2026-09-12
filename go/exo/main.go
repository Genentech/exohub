package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/Genentech/exohub/go/exo/branding"
	addcmd "github.com/Genentech/exohub/go/exo/commands/add"
	atlascmd "github.com/Genentech/exohub/go/exo/commands/atlas"
	authcmd "github.com/Genentech/exohub/go/exo/commands/auth"
	broadcast "github.com/Genentech/exohub/go/exo/commands/broadcast"
	bundlecmd "github.com/Genentech/exohub/go/exo/commands/bundle"
	clonecmd "github.com/Genentech/exohub/go/exo/commands/clone"
	contextcmd "github.com/Genentech/exohub/go/exo/commands/context"
	copycmd "github.com/Genentech/exohub/go/exo/commands/copy"
	doctorcmd "github.com/Genentech/exohub/go/exo/commands/doctor"
	downloadcmd "github.com/Genentech/exohub/go/exo/commands/download"
	exportcmd "github.com/Genentech/exohub/go/exo/commands/export"
	fsck "github.com/Genentech/exohub/go/exo/commands/fsck"
	getcmd "github.com/Genentech/exohub/go/exo/commands/get"
	heartbeat "github.com/Genentech/exohub/go/exo/commands/heartbeat"
	info "github.com/Genentech/exohub/go/exo/commands/info"
	initcmd "github.com/Genentech/exohub/go/exo/commands/init"
	link "github.com/Genentech/exohub/go/exo/commands/link"
	locatecmd "github.com/Genentech/exohub/go/exo/commands/locate"
	lockcmd "github.com/Genentech/exohub/go/exo/commands/lock"
	logincmd "github.com/Genentech/exohub/go/exo/commands/login"
	manifest "github.com/Genentech/exohub/go/exo/commands/manifest"
	mcpcmd "github.com/Genentech/exohub/go/exo/commands/mcp"
	mirror "github.com/Genentech/exohub/go/exo/commands/mirror"
	publishcmd "github.com/Genentech/exohub/go/exo/commands/publish"
	pull "github.com/Genentech/exohub/go/exo/commands/pull"
	safecmd "github.com/Genentech/exohub/go/exo/commands/safe"
	servecmd "github.com/Genentech/exohub/go/exo/commands/serve"
	submit "github.com/Genentech/exohub/go/exo/commands/submit"
	sync "github.com/Genentech/exohub/go/exo/commands/sync"
	themecmd "github.com/Genentech/exohub/go/exo/commands/theme"
	tipcmd "github.com/Genentech/exohub/go/exo/commands/tip"
	unlockcmd "github.com/Genentech/exohub/go/exo/commands/unlock"
	upgrade "github.com/Genentech/exohub/go/exo/commands/upgrade"
	versioncmd "github.com/Genentech/exohub/go/exo/commands/version"
	"github.com/Genentech/exohub/go/exo/commandutil"
	"github.com/Genentech/exohub/go/exo/configdir"
	"github.com/Genentech/exohub/go/exo/internal/tips"
	"github.com/Genentech/exohub/go/exo/palette"
	"github.com/Genentech/exohub/go/exo/preferences"
)

var (
	version = "dev"
	commit  = ""
	date    = ""
)

func init() {
	// Set MAGIC env var to a compiled magic database next to the exo binary,
	// if present. This suppresses libmagic parse warnings that git-annex
	// produces on systems with mismatched magic database versions.
	if os.Getenv("MAGIC") == "" {
		if exe, err := os.Executable(); err == nil {
			mgc := filepath.Join(filepath.Dir(exe), "magic.mgc")
			if _, err := os.Stat(mgc); err == nil {
				os.Setenv("MAGIC", mgc[:len(mgc)-4]) // without .mgc extension
			}
		}
	}
}

func main() {
	rootCmd := &cobra.Command{
		Use:   "exo",
		Short: "", // Empty to avoid duplication with logo
		Run: func(cmd *cobra.Command, args []string) {
			// Print ExoHub branding
			fmt.Println(branding.ExoHubBanner(version))
			_ = cmd.Help()
			os.Exit(1)
		},
	}
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true

	// Register preferences loaders for palette resolution (avoids import cycle)
	palette.SetThemeLoader(preferences.GetTheme)
	palette.SetThemeModeLoader(preferences.GetThemeMode)

	// Resolve theme and mode before any command runs; propagate --config-dir to subprocesses.
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if configdir.FlagValue != "" {
			os.Setenv("EXO_CONFIG_DIR", configdir.FlagValue)
		}
		palette.Init()
		tips.MaybeShowDailyTip(os.Stderr)
		return nil
	}

	// Custom help template with branded colors
	rootCmd.SetUsageTemplate(customHelpTemplate())

	rootCmd.PersistentFlags().StringVar(&configdir.FlagValue, "config-dir", "", "Root directory for all config, cache and credentials (overrides EXO_CONFIG_DIR)")
	rootCmd.PersistentFlags().StringVar(&contextcmd.OverrideContext, "context", "", "Use specific context (overrides selected context)")

	rootCmd.AddCommand(addcmd.NewCommand())
	rootCmd.AddCommand(atlascmd.NewCommand())
	rootCmd.AddCommand(broadcast.NewCommand())
	rootCmd.AddCommand(bundlecmd.NewCommand(version))
	rootCmd.AddCommand(clonecmd.NewCommand())
	rootCmd.AddCommand(contextcmd.NewCommand())
	rootCmd.AddCommand(copycmd.NewCommand())
	rootCmd.AddCommand(downloadcmd.NewCommand())
	rootCmd.AddCommand(exportcmd.NewCommand())
	rootCmd.AddCommand(fsck.NewCommand())
	rootCmd.AddCommand(getcmd.NewCommand())
	rootCmd.AddCommand(heartbeat.NewCommand())
	rootCmd.AddCommand(info.NewCommand())
	rootCmd.AddCommand(initcmd.NewCommand())
	rootCmd.AddCommand(link.NewCommand())
	rootCmd.AddCommand(locatecmd.NewCommand())
	rootCmd.AddCommand(lockcmd.NewCommand())
	rootCmd.AddCommand(authcmd.NewCommand())
	rootCmd.AddCommand(doctorcmd.NewCommand())
	rootCmd.AddCommand(logincmd.NewLoginCommand())
	rootCmd.AddCommand(logincmd.NewLogoutCommand())
	rootCmd.AddCommand(manifest.NewCommand())
	rootCmd.AddCommand(mcpcmd.NewCommand(version))
	rootCmd.AddCommand(mirror.NewCommand())
	rootCmd.AddCommand(publishcmd.NewCommand())
	rootCmd.AddCommand(pull.NewCommand())
	rootCmd.AddCommand(safecmd.NewCommand())
	serveVersion := version
	if version == "dev" && commit != "" {
		serveVersion = "dev-" + commit
	} else if commit != "" {
		serveVersion = version + "-" + commit
	}
	rootCmd.AddCommand(servecmd.NewCommand(serveVersion))
	rootCmd.AddCommand(submit.NewCommand())
	rootCmd.AddCommand(sync.NewCommand())
	rootCmd.AddCommand(themecmd.NewCommand())
	rootCmd.AddCommand(tipcmd.NewCommand())
	rootCmd.AddCommand(unlockcmd.NewCommand())
	rootCmd.AddCommand(upgrade.NewCommand())
	rootCmd.AddCommand(versioncmd.NewCommand(version, commit, date))

	if err := rootCmd.Execute(); err != nil {
		os.Exit(exitCode(err))
	}
}

func execCommand(name string, args ...string) error {
	cmd := commandutil.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}
	fmt.Fprintln(os.Stderr, err.Error())
	return 1
}

func customHelpTemplate() string {
	purpleStyle := lipgloss.NewStyle().Foreground(branding.GradientPurple).Bold(true)
	orangeStyle := lipgloss.NewStyle().Foreground(branding.GradientOrange).Bold(true)

	return `Usage:
  ` + purpleStyle.Render("{{.UseLine}}") + `{{if .HasAvailableSubCommands}}
  ` + purpleStyle.Render("{{.CommandPath}}") + ` [command]{{end}}
{{if .HasAvailableSubCommands}}
Available Commands:{{range .Commands}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  ` + orangeStyle.Render(`{{rpad .Name .NamePadding}}`) + ` {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

Flags:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

Global Flags:
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasHelpSubCommands}}

Additional help topics:{{range .Commands}}{{if .IsAdditionalHelpTopicCommand}}
  {{rpad .CommandPath .CommandPathPadding}} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableSubCommands}}

Use "` + purpleStyle.Render("{{.CommandPath}} [command]") + ` --help" for more information about a command.{{end}}
`
}
