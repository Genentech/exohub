package heartbeat

import (
	"github.com/spf13/cobra"

	heartbeatrecord "github.com/Genentech/exohub/go/exo/commands/heartbeatrecord"
	heartbeatshow "github.com/Genentech/exohub/go/exo/commands/heartbeatshow"
)

func NewCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "heartbeat",
		Short: "Record and show heartbeat metrics",
		Long: `Record and show heartbeat metrics.

Commands:
  record   Generate metrics into .git/exohub/metrics/
  show     Print metrics (latest, JSON, or interactive)`,
	}

	rootCmd.AddCommand(heartbeatrecord.NewCommand())
	rootCmd.AddCommand(heartbeatshow.NewCommand())

	return rootCmd
}
