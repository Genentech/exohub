package standalone

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
)

func newSeedCommand() *cobra.Command {
	var (
		adbBin     string
		configFile string
	)

	cmd := &cobra.Command{
		Use:   "seed <ndjson-file>",
		Short: "Bulk-load ADB documents from an NDJSON file",
		Long: `Bulk-load finished ADB documents into the running adb-standalone instance.

Each line of the NDJSON file must be a complete document with _extra.id set.
Documents are upserted verbatim (builds search_text). Designed for bootstrapping
from an adb-capture snapshot or migrating from a private → enterprise deployment.

Wraps: adb-standalone seed <ndjson-file>`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSeed(adbBin, configFile, args[0])
		},
	}

	cmd.Flags().StringVar(&adbBin, "adb-bin", "adb-standalone", "Path to the adb-standalone binary")
	cmd.Flags().StringVar(&configFile, "config", "", "adb-standalone config file")

	return cmd
}

func runSeed(adbBin, configFile, ndjsonPath string) error {
	adbBinPath, err := exec.LookPath(adbBin)
	if err != nil {
		return fmt.Errorf("%q not found in PATH — install adb-standalone or set --adb-bin", adbBin)
	}

	args := []string{"seed", ndjsonPath}
	if configFile != "" {
		args = append([]string{"--config", configFile}, args...)
	}

	seedCmd := exec.Command(adbBinPath, args...) //nolint:gosec
	seedCmd.Stdout = os.Stdout
	seedCmd.Stderr = os.Stderr
	seedCmd.Stdin = os.Stdin

	if err := seedCmd.Run(); err != nil {
		return fmt.Errorf("adb-standalone seed: %w", err)
	}
	return nil
}
