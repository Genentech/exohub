package tip

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/Genentech/exohub/go/exo/internal/tips"
	"github.com/Genentech/exohub/go/exo/palette"
)

func NewCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "tip",
		Short: "Show a random tip",
		Long:  "Fetch and display a random tip from the ExoHub tip bank.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			bank, err := tips.FetchAndCache()
			if err != nil {
				return fmt.Errorf("failed to fetch tips: %w", err)
			}

			tip := tips.PickRandom(bank)
			if tip == nil {
				fmt.Fprintln(cmd.OutOrStdout(), "No tips available.")
				return nil
			}

			p := palette.Current()
			titleStyle := lipgloss.NewStyle().Foreground(p.Accent.Adaptive()).Bold(true)
			bodyStyle := lipgloss.NewStyle().Foreground(p.Label.Adaptive())

			fmt.Fprintln(cmd.OutOrStdout(), "")
			fmt.Fprintln(cmd.OutOrStdout(), titleStyle.Render("💡 "+tip.Title))
			fmt.Fprintln(cmd.OutOrStdout(), bodyStyle.Render(tip.Body))
			fmt.Fprintln(cmd.OutOrStdout(), "")

			return nil
		},
	}
}
