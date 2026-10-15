//go:build !artifactdb

package download

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "download",
		Short:  "Download artifact files from the catalog (not available)",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("not built with 'artifactdb' support")
		},
	}

	// Register the same flags as the artifactdb build so `exo download
	// --eds ...` fails with the clean error above instead of cobra's
	// "unknown flag" error.
	cmd.Flags().String("url", "", "")
	cmd.Flags().StringP("output-dir", "o", ".", "")
	cmd.Flags().BoolP("force", "f", false, "")
	cmd.Flags().String("eds", "", "")
	cmd.Flags().String("scope", "", "")
	cmd.Flags().Bool("no-wait", false, "")
	cmd.Flags().Duration("max-wait", 6*time.Hour, "")
	cmd.Flags().Duration("poll-interval", 10*time.Second, "")

	return cmd
}
