package theme

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/Genentech/exohub/go/exo/commandutil"
	"github.com/Genentech/exohub/go/exo/palette"
	"github.com/Genentech/exohub/go/exo/preferences"
)

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "theme",
		Short: "Manage terminal color theme",
		Long:  "Select, view, or change the terminal color theme used across all exo commands",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInteractive()
		},
	}

	cmd.AddCommand(newListCommand())
	cmd.AddCommand(newSetCommand())
	cmd.AddCommand(newShowCommand())
	cmd.AddCommand(newModeCommand())

	return cmd
}

func newListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List available themes",
		RunE: func(cmd *cobra.Command, args []string) error {
			current := palette.Current().Name
			for _, name := range palette.ThemeNames() {
				p := palette.Get(name)
				marker := "  "
				if name == current {
					marker = "● "
				}
				fmt.Printf("%s%-10s %s\n", marker, p.Name, p.Description)
			}
			return nil
		},
	}
}

func newSetCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "set <name>",
		Short: "Set the active theme",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			p := palette.Get(name)
			if p == nil {
				return fmt.Errorf("unknown theme %q (available: %s)", name, strings.Join(palette.ThemeNames(), ", "))
			}

			prefs, err := preferences.Load()
			if err != nil {
				return err
			}
			prefs.Theme = name
			if err := preferences.Save(prefs); err != nil {
				return err
			}
			fmt.Printf("Theme set to %s\n", name)
			return nil
		},
	}
}

func newShowCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show the current active theme and mode",
		RunE: func(cmd *cobra.Command, args []string) error {
			mode := "dark"
			if palette.CurrentMode() == palette.ModeLight {
				mode = "light"
			}
			fmt.Printf("%s (%s)\n", palette.Current().Name, mode)
			return nil
		},
	}
}

func newModeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mode [auto|dark|light]",
		Short: "Set dark/light mode preference",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return runModeInteractive()
			}
			mode := args[0]
			if mode != "auto" && mode != "dark" && mode != "light" {
				return fmt.Errorf("invalid mode %q (must be auto, dark, or light)", mode)
			}
			return saveMode(mode)
		},
	}
	return cmd
}

func saveMode(mode string) error {
	prefs, err := preferences.Load()
	if err != nil {
		return err
	}
	if mode == "auto" {
		prefs.ThemeMode = ""
	} else {
		prefs.ThemeMode = mode
	}
	if err := preferences.Save(prefs); err != nil {
		return err
	}
	fmt.Printf("Theme mode set to %s\n", mode)
	return nil
}

func runModeInteractive() error {
	prefs, err := preferences.Load()
	if err != nil {
		return err
	}
	current := prefs.ThemeMode
	if current == "" {
		current = "auto"
	}

	options := []huh.Option[string]{
		huh.NewOption("auto — detect from terminal background", "auto"),
		huh.NewOption("dark — always use dark-background colors", "dark"),
		huh.NewOption("light — always use light-background colors", "light"),
	}

	var selected string
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Select theme mode").
				Description(fmt.Sprintf("Currently: %s", current)).
				Options(options...).
				Value(&selected),
		),
	).WithTheme(commandutil.ExoTheme()).WithKeyMap(commandutil.ExoKeyMap())

	if err := form.Run(); err != nil {
		return err
	}
	return saveMode(selected)
}

func runInteractive() error {
	current := palette.Current().Name

	var options []huh.Option[string]
	for _, name := range palette.ThemeNames() {
		p := palette.Get(name)
		preview := renderPreview(p)
		label := fmt.Sprintf("%s — %s\n%s", p.Name, p.Description, preview)
		options = append(options, huh.NewOption(label, name))
	}

	var selected string
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Select a theme").
				Options(options...).
				Value(&selected),
		),
	).WithTheme(commandutil.ExoTheme()).WithKeyMap(commandutil.ExoKeyMap())

	if err := form.Run(); err != nil {
		return err
	}

	if selected == current {
		fmt.Printf("Theme is already %s\n", selected)
		return nil
	}

	prefs, err := preferences.Load()
	if err != nil {
		return err
	}
	prefs.Theme = selected
	if err := preferences.Save(prefs); err != nil {
		return err
	}
	fmt.Printf("Theme set to %s\n", selected)
	return nil
}

func renderPreview(p *palette.Palette) string {
	label := lipgloss.NewStyle().Foreground(p.Label.Adaptive()).Bold(true)
	labelAlt := lipgloss.NewStyle().Foreground(p.LabelAlt.Adaptive()).Bold(true)
	accent := lipgloss.NewStyle().Foreground(p.Accent.Adaptive())
	success := lipgloss.NewStyle().Foreground(p.Success.Adaptive())
	errStyle := lipgloss.NewStyle().Foreground(p.Error.Adaptive())
	warning := lipgloss.NewStyle().Foreground(p.Warning.Adaptive())
	dim := lipgloss.NewStyle().Foreground(p.Dim.Adaptive())
	highlight := lipgloss.NewStyle().Foreground(p.Highlight.Adaptive())
	filled := lipgloss.NewStyle().Foreground(p.ProgressFilled.Adaptive())
	empty := lipgloss.NewStyle().Foreground(p.ProgressEmpty.Adaptive())

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("    %s all synced   %s 3 remotes\n",
		label.Render("Status:"), labelAlt.Render("Network:")))
	sb.WriteString(fmt.Sprintf("    %s 2 files  %s\n",
		accent.Render("Downloading:"),
		filled.Render("●●●●")+empty.Render("○○")))
	sb.WriteString(fmt.Sprintf("    %s 1 bad file   %s check permissions\n",
		errStyle.Render("Error:"), warning.Render("Warning:")))
	sb.WriteString(fmt.Sprintf("    %s docs  %s /data/repos/sample  %s v1.2.3",
		success.Render("Ready:"), highlight.Render("Path:"), dim.Render("updated 2h ago")))
	return sb.String()
}
