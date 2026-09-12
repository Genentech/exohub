package manifest

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	manifestutil "github.com/Genentech/exohub/go/exo/commandutil"
	"github.com/Genentech/exohub/go/exo/manifests/templates"
)

const longText = `Validate and initialize exohub manifests.

Commands:
  validate <yaml>   Validate a manifest file against its schema
  init              Print a manifest template to stdout

Notes:
  - validate requires canonical keys (no aliases) and validates the manifest as-is.
  - validate infers the manifest type from remote-type (and treats manifests with activities as workflow).
  - --schema validates against a local JSON schema; otherwise schemas are fetched from EXOHUB_API_URL.
  - "sync" is accepted as an alias for "annex".
  - YAML parsing uses the built-in parser.`



const initWorkflowTemplate = `# Exo workflow manifest (used by run.py)
name: my-dataset
url: https://github.com/org/repo.git
ref: main
remote-type: annex
repo-dir: /data/work/my-dataset
# with-remotes:
#   - s3-private
# paths:
#   - data/**
# from: s3-private   # required if remote-type is export
# to: gcs-public     # required if remote-type is export
# activities:
#   mirror-plan:
#     manifest: manifests/mirror-plan.yaml
#   link:
#     manifest: manifests/link.yaml
`

func NewCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "manifest",
		Short: "Validate and initialize exohub manifests",
		Long:  longText,
	}

	rootCmd.AddCommand(newValidateCmd())
	rootCmd.AddCommand(newInitCmd())

	return rootCmd
}

func newValidateCmd() *cobra.Command {
	var schemaPath string

	cmd := &cobra.Command{
		Use:   "validate <yaml>",
		Short: "Validate a manifest",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			manifestFile := args[0]
			payload, err := manifestutil.ReadManifest(manifestFile)
			if err != nil {
				fmt.Fprintln(os.Stderr, "Failed to parse YAML")
				os.Exit(3)
			}

			manifestType, err := manifestutil.InferManifestType(payload)
			if err != nil {
				fmt.Fprintln(os.Stderr, err.Error())
				os.Exit(2)
			}
			schemaErrors, err := manifestutil.ValidateSchema(manifestType, schemaPath, payload)
			if err != nil {
				fmt.Fprintln(os.Stderr, err.Error())
				os.Exit(2)
			}

			errors := schemaErrors
			if len(errors) == 0 {
				fmt.Fprintf(os.Stderr, "Manifest is valid (%s)\n", manifestType)
				os.Exit(0)
			}
			fmt.Fprintf(os.Stderr, "Manifest validation errors (%s):\n", manifestType)
			for _, msg := range errors {
				fmt.Fprintf(os.Stderr, "  - %s\n", msg)
			}
			os.Exit(1)
		},
	}

	cmd.Flags().StringVar(&schemaPath, "schema", "", "Path to JSON schema for validation")

	return cmd
}

func newInitCmd() *cobra.Command {
	var manifestType string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Print a manifest template",
		Run: func(cmd *cobra.Command, args []string) {
			if manifestType == "" {
				fmt.Fprintln(os.Stderr, "--type is required")
				os.Exit(2)
			}
			switch manifestType {
			case "sync", "annex":
				fmt.Print(templates.Annex())
			case "export":
				fmt.Print(templates.Export())
			case "workflow":
				fmt.Print(initWorkflowTemplate)
			case "permissions":
				fmt.Print(templates.Permissions())
			case "bundle":
				fmt.Print(templates.Bundle())
			default:
				fmt.Fprintln(os.Stderr, "--type must be annex, export, workflow, permissions, or bundle")
				os.Exit(2)
			}
		},
	}

	cmd.Flags().StringVar(&manifestType, "type", "", "Manifest type (annex, export, workflow, permissions, or bundle)")
	return cmd
}


