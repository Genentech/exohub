// Package doctor implements the `exo doctor` diagnostics command.
package doctor

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	flagJSON      bool
	flagVerbose   bool
	flagRemote    string
	flagRepo      string
	flagWriteTest bool
)

// NewCommand returns the `exo doctor` cobra command.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Run diagnostics on your ExoHub client environment",
		Long: `Run grouped, non-destructive diagnostics of your ExoHub client environment.

Each check reports PASS, WARN, FAIL, or SKIP with a short summary and an
actionable remediation hint. Repo-scoped checks are skipped unless run inside
an ExoHub repository (or pointed at one with --repo).

Secrets (access keys, tokens, JWTs) are never printed.

Exit code is non-zero if any check is FAIL.`,
		RunE:         runDoctor,
		SilenceUsage: true,
	}
	cmd.Flags().BoolVar(&flagJSON, "json", false, "Emit results as a JSON array")
	cmd.Flags().BoolVarP(&flagVerbose, "verbose", "v", false, "Show detail/remediation for PASS checks too")
	cmd.Flags().StringVar(&flagRemote, "remote", "", "Only run per-remote checks for this remote name")
	cmd.Flags().StringVar(&flagRepo, "repo", "", "Path to an ExoHub repo (default: current directory)")
	cmd.Flags().BoolVar(&flagWriteTest, "write-test", false, "Perform an opt-in write canary (Put+Delete) under a caller-owned prefix")
	return cmd
}

func runDoctor(cmd *cobra.Command, args []string) error {
	repoDir := flagRepo
	if repoDir == "" {
		var err error
		repoDir, err = os.Getwd()
		if err != nil {
			repoDir = "."
		}
	}

	var results []CheckResult

	// 1. Environment & tooling
	results = append(results, runEnvChecks()...)

	// 2. Identity & auth
	results = append(results, runAuthChecks()...)

	// 3. Network reachability
	results = append(results, runNetworkChecks()...)

	// 4. Repo config (SKIP outside a repo)
	results = append(results, runRepoChecks(repoDir)...)

	// 5. Grants & S3 access (per grants-enabled remote)
	results = append(results, runGrantsChecks(repoDir, flagRemote, flagWriteTest)...)

	if flagJSON {
		return PrintJSON(cmd.OutOrStdout(), results)
	}

	PrintResults(cmd.OutOrStdout(), results, flagVerbose)

	if hasFail(results) {
		fmt.Fprintln(cmd.ErrOrStderr(), "One or more checks FAILED. See remediation hints above.")
		return fmt.Errorf("diagnostics failed")
	}
	return nil
}
