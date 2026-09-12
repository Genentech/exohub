package standalone

import (
	"github.com/spf13/cobra"
)

// NewCommand returns the root `exo standalone` cobra command.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "standalone",
		Short: "Manage the local adb-standalone stack",
		Long: `Manage a batteries-included local ArtifactDB stack.

The stack consists of three processes:
  surrealdb     — document store (FTS + optional vector)
  versitygw     — S3-compatible object store (POSIX-FS backend, Apache-2.0)
  adb-standalone — ArtifactDB-compatible API server

Subcommands:
  up       Bring up the full stack (foreground, supervised)
  down     Stop and clean up child processes
  status   Show health of the three processes + endpoints
  seed     Bulk-load finished ADB docs from an NDJSON file
  ingest   Catalog a new project version from a bundle directory`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(newUpCommand())
	cmd.AddCommand(newDownCommand())
	cmd.AddCommand(newStatusCommand())
	cmd.AddCommand(newSeedCommand())
	cmd.AddCommand(newIngestCommand())

	return cmd
}
