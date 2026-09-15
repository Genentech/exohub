//go:build !artifactdb

package atlas

import (
	"fmt"

	"github.com/spf13/cobra"
)

func NewCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "atlas",
		Short:  "Browse and search an ArtifactDB catalog (not available)",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("not built with 'artifactdb' support")
		},
	}
}
