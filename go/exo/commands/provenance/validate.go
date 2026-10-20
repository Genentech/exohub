package provenance

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	lib "github.com/Genentech/exohub/go/exo/internal/provenance"
)

type validateOptions struct {
	staged     bool
	ref        string
	jsonOutput bool
}

// newValidateCommand returns `exo provenance validate` (design D7).
func newValidateCommand() *cobra.Command {
	var opts validateOptions

	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate provenance records",
		Long: `Validate provenance records against the record profile and vocabulary,
cross-record consistency and the add-only rule.

By default it checks worktree record files (including untracked ones) with
content identifiers resolved from the index. --staged checks the index.
--ref checks a git ref's tree. Warnings do not affect the exit code; the
command exits non-zero only when an error is found.`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runValidate(cmd, opts)
		},
	}

	flags := cmd.Flags()
	flags.BoolVar(&opts.staged, "staged", false, "validate the staged (index) state instead of the worktree")
	flags.StringVar(&opts.ref, "ref", "", "validate records as of this git ref instead of the worktree")
	flags.BoolVar(&opts.jsonOutput, "json", false, "emit machine-readable JSON findings")

	return cmd
}

func runValidate(cmd *cobra.Command, opts validateOptions) error {
	if opts.staged && opts.ref != "" {
		return fmt.Errorf("--staged and --ref are mutually exclusive")
	}

	root, err := repoRoot()
	if err != nil {
		return err
	}

	var reader *lib.Reader
	switch {
	case opts.ref != "":
		reader = lib.NewRefReader(root, opts.ref)
	case opts.staged:
		reader = lib.NewStagedReader(root)
	default:
		reader = lib.NewWorktreeReader(root)
	}

	report, err := lib.Validate(reader)
	if err != nil {
		return err
	}

	cfg, err := reader.LoadConfig()
	if err != nil {
		return fmt.Errorf("load .exohub/config: %w", err)
	}
	hasRequireRules := cfg != nil && cfg.Provenance != nil && len(cfg.Provenance.Require) > 0

	if opts.jsonOutput {
		enc, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return fmt.Errorf("encode report: %w", err)
		}
		fmt.Fprintln(cmd.OutOrStdout(), string(enc))
	} else {
		printReport(cmd, report, hasRequireRules)
	}

	if report.HasErrors() {
		return fmt.Errorf("provenance validation failed")
	}
	return nil
}

func printReport(cmd *cobra.Command, report *lib.Report, hasRequireRules bool) {
	if report.RecordCount == 0 && !hasRequireRules {
		fmt.Fprintln(cmd.OutOrStdout(), "No provenance records found.")
		return
	}
	if len(report.Findings) == 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "%d record(s) valid, no warnings.\n", report.RecordCount)
	} else {
		for _, f := range report.Findings {
			label := strings.ToUpper(string(f.Severity))
			if f.Path != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "[%s] %s: %s\n", label, f.Path, f.Message)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "[%s] %s\n", label, f.Message)
			}
			if f.Detail != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "    %s\n", f.Detail)
			}
		}
	}
	if report.Coverage != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "Coverage: %d covered, %d uncovered.\n", report.Coverage.Covered, report.Coverage.Uncovered)
	}
}
