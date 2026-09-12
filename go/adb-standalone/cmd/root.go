package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var cfgFile string

var rootCmd = &cobra.Command{
	Use:   "adb-standalone",
	Short: "Standalone ArtifactDB service backed by SurrealDB",
	Long: `adb-standalone is a lightweight ArtifactDB-compatible service that stores
documents in SurrealDB and serves the ADB REST API subset used by Exohub.`,
}

// Execute is the entry point called by main.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default: config.yaml in current directory)")
	rootCmd.AddCommand(serveCmd)
	rootCmd.AddCommand(seedCmd)
}
