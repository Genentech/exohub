// Package provenance implements the `exo provenance` command group: tools
// for creating and maintaining W3C PROV lineage records in
// `.exohub/provenance/` (openspec/changes/add-provenance).
package provenance

import (
	"github.com/spf13/cobra"
)

// NewCommand returns the root `exo provenance` cobra command.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "provenance",
		Short: "Record and inspect data lineage",
		Long: `Record and inspect data lineage as W3C PROV records.

Records live as JSON-LD files in .exohub/provenance/, one file per record.
They are add-only: a committed record is never edited or deleted. A
correction is a new record that supersedes the ones it replaces
(--supersedes).

Workflow for AI agents and scripts:
  - Record provenance when producing or importing artifacts, with
    'exo provenance add' (preferred), or by writing a new record file and
    running 'exo provenance validate'.
  - Identify as a SoftwareAgent with --agent software:<name> and --model,
    acting on behalf of the user with --on-behalf-of person:<unix id>.
  - Include --evidence for inferred claims.
  - Never edit or delete existing records; supersede them instead.
  - Run 'exo provenance validate' before committing.

Subcommands:
  add          Create a new provenance record
  validate     Validate provenance records
  show         Show an artifact's provenance lineage
  install-hook Install a pre-commit hook that runs validate --staged`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(newAddCommand())
	cmd.AddCommand(newValidateCommand())
	cmd.AddCommand(newShowCommand())
	cmd.AddCommand(newInstallHookCommand())

	return cmd
}
