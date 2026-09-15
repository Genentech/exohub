//go:build !artifactdb

package download

import (
	"fmt"

	"github.com/spf13/cobra"
)

func NewCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "download",
		Short:  "Download artifact files from the catalog (not available)",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("not built with 'artifactdb' support")
		},
	}
}
