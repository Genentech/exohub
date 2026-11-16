package provenance

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	lib "github.com/Genentech/exohub/go/exo/internal/provenance"
	"github.com/Genentech/exohub/go/exo/internal/provenance/importer"
)

// Adapter reads one run from a format-specific source (design D7 "Import
// architecture": "format adapter -> run model -> matcher -> record
// writer"). store is --store's value (format-specific, for example a
// lineage store directory; "" for the format's default location), and run
// is the positional <run> argument ("" to import the latest run).
type Adapter func(store, run string) (importer.Run, error)

// formatAdapters holds the registered `exo provenance import <format>`
// adapters. No format ships in this change; adapters (for example
// Nextflow) register themselves via RegisterFormat from their own
// package's init.
var formatAdapters = map[string]Adapter{}

// RegisterFormat registers an import format adapter under name, so `exo
// provenance import <name> [<run>]` can use it.
func RegisterFormat(name string, adapter Adapter) {
	formatAdapters[name] = adapter
}

type importOptions struct {
	store      string
	tasks      bool
	supersedes []string
	keepSource bool
	dryRun     bool
	jsonOutput bool
}

// newImportCommand returns `exo provenance import` (design D7).
func newImportCommand() *cobra.Command {
	var opts importOptions

	cmd := &cobra.Command{
		Use:   "import <format> [<run>]",
		Short: "Import provenance a workflow engine already recorded",
		Long: `Read one run from a supported source format and write ExoHub provenance
records under .exohub/provenance/ with exo:assertion "imported", staged
like records written by 'exo provenance add'. When <run> is omitted, the
latest run found in the format's default location is imported.

Each run file is resolved to repository content the same way 'exo
provenance add' resolves a path: an annex key or git blob id, verified
against the size and checksum the engine recorded when exo can compute
that checksum algorithm (sha256, md5), otherwise by size only. A file
outside the repository, or whose content no longer matches, is referenced
by an external IRI and reported as unmatched; an untracked repository file
is reported with a hint to run 'exo add' first.

By default one Activity is written for the whole run, using its overall
inputs, outputs, parameters and environments. --tasks writes one Activity
per task instead.

Importing a run that was already imported writes nothing and exits zero,
reporting the existing record. --supersedes <record-id> writes new records
that supersede it instead.

--keep-source commits a copy of the run's source lineage documents under
.exohub/provenance/sources/ as git blobs, referenced from the record's
exo:evidence.

This command never modifies an existing record file; never writes on
--dry-run; and requires an exo login token to identify the importing
person (run 'exo login').`,
		Args:         cobra.RangeArgs(1, 2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			format := args[0]
			var run string
			if len(args) > 1 {
				run = args[1]
			}
			return runImport(cmd, format, run, opts)
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&opts.store, "store", "", "location of the format's lineage store (format-specific; defaults to the format's usual location)")
	flags.BoolVar(&opts.tasks, "tasks", false, "write one activity per task instead of one for the whole run")
	flags.StringArrayVar(&opts.supersedes, "supersedes", nil, "record id this import supersedes (repeatable); required to re-import an already-imported run")
	flags.BoolVar(&opts.keepSource, "keep-source", false, "commit a copy of the run's source lineage documents under .exohub/provenance/sources/, referenced as evidence")
	flags.BoolVar(&opts.dryRun, "dry-run", false, "print the records that would be written; write and stage nothing")
	flags.BoolVar(&opts.jsonOutput, "json", false, "emit machine-readable JSON")

	return cmd
}

func runImport(cmd *cobra.Command, format, runArg string, opts importOptions) error {
	adapter, ok := formatAdapters[format]
	if !ok {
		return fmt.Errorf("unsupported import format %q (supported: %s)", format, supportedFormats())
	}
	for _, id := range opts.supersedes {
		if !lib.IsUUIDv7(id) {
			return fmt.Errorf("--supersedes must be urn:exohub:record:<UUIDv7>, got %q", id)
		}
	}

	run, err := adapter(opts.store, runArg)
	if err != nil {
		return err
	}

	root, err := repoRoot()
	if err != nil {
		return err
	}

	if len(opts.supersedes) == 0 {
		existing, err := importer.FindExistingImport(root, run.ID)
		if err != nil {
			return fmt.Errorf("check for an existing import: %w", err)
		}
		if existing != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "run %s already imported in record %s\n", run.ID, existing.RecordID)
			return nil
		}
	}

	personID, personLabel, assertedByID, err := resolveImportAgent()
	if err != nil {
		return err
	}

	var evidence []string
	if opts.keepSource && !opts.dryRun {
		evidence, err = importer.KeepSource(root, run)
		if err != nil {
			return err
		}
	}

	reader := lib.NewWorktreeReader(root)
	writer := importer.NewWriter(importer.NewMatcher(reader))
	generatedAt := time.Now().UTC()
	rec, summary, err := writer.Write(run, importer.WriteOptions{
		AssertedBy:  assertedByID,
		PersonID:    personID,
		PersonLabel: personLabel,
		Supersedes:  opts.supersedes,
		Evidence:    evidence,
		GeneratedAt: generatedAt,
		Tasks:       opts.tasks,
	})
	if err != nil {
		return err
	}

	data, err := lib.Serialize(rec)
	if err != nil {
		return fmt.Errorf("serialize record: %w", err)
	}

	if opts.dryRun {
		fmt.Fprintln(cmd.OutOrStdout(), string(data))
		printImportSummary(cmd, summary, opts.jsonOutput)
		return nil
	}

	slug := lib.Slugify(run.Name)
	if slug == "" {
		slug = "run"
	}
	path, err := lib.RecordPath(generatedAt, slug, rec.ID)
	if err != nil {
		return fmt.Errorf("build record path: %w", err)
	}

	if err := writeRecordFile(root, path, data); err != nil {
		return err
	}
	if err := stageRecord(root, path); err != nil {
		return err
	}

	if opts.jsonOutput {
		out := struct {
			Path    string           `json:"path"`
			Record  json.RawMessage  `json:"record"`
			Summary importer.Summary `json:"summary"`
		}{Path: path, Record: data, Summary: summary}
		enc, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return fmt.Errorf("encode result: %w", err)
		}
		fmt.Fprintln(cmd.OutOrStdout(), string(enc))
		return nil
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Wrote and staged %s\n", path)
	printImportSummary(cmd, summary, false)
	return nil
}

// maxSizeOnlyListed caps how many size-only-verified file paths
// printImportSummary names before collapsing the rest into "... and N
// more" (review: "names the size-only files (capped)").
const maxSizeOnlyListed = 5

// printImportSummary prints summary (spec "a summary of activities,
// inputs, outputs and unmatched files"; design D7's matcher, "recorded as
// 'verified by size' in the import summary"). jsonOutput suppresses the
// human form, since the caller embeds summary in its own JSON output
// instead.
func printImportSummary(cmd *cobra.Command, summary importer.Summary, jsonOutput bool) {
	if jsonOutput {
		return
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "%d activity(ies): %d matched input(s), %d matched output(s), %d external input(s), %d external output(s)\n",
		summary.Activities, summary.MatchedInputs, summary.MatchedOutputs, summary.ExternalInputs, summary.ExternalOutputs)

	var checksumVerified, sizeOnly int
	var sizeOnlyPaths []string
	for _, m := range summary.Matched {
		switch m.Verified {
		case "checksum":
			checksumVerified++
		case "size":
			sizeOnly++
			sizeOnlyPaths = append(sizeOnlyPaths, m.Path)
		}
	}
	fmt.Fprintf(out, "  verified by checksum: %d, verified by size only: %d, unmatched: %d, external: %d\n",
		checksumVerified, sizeOnly, len(summary.Unmatched), summary.ExternalInputs+summary.ExternalOutputs)
	if len(sizeOnlyPaths) > 0 {
		fmt.Fprintln(out, "  verified by size only (not checksum):")
		for i, p := range sizeOnlyPaths {
			if i == maxSizeOnlyListed {
				fmt.Fprintf(out, "    ... and %d more\n", len(sizeOnlyPaths)-maxSizeOnlyListed)
				break
			}
			fmt.Fprintf(out, "    %s\n", p)
		}
	}
	for _, u := range summary.Unmatched {
		fmt.Fprintf(out, "  unmatched: %s (%s)\n", u.Path, u.Reason)
	}
}

func supportedFormats() string {
	if len(formatAdapters) == 0 {
		return "none registered"
	}
	names := make([]string, 0, len(formatAdapters))
	for name := range formatAdapters {
		names = append(names, name)
	}
	return fmt.Sprintf("%v", names)
}

// resolveImportAgent resolves the importing person from the exo login
// identity (design D7: "exo:assertedBy = the importing person"): import
// has no --agent flag, unlike add, since an imported record is always
// asserted by the person who ran the command.
func resolveImportAgent() (personID, personLabel, assertedByID string, err error) {
	unixID, label, ok := loginPersonIdentity()
	if !ok {
		return "", "", "", fmt.Errorf("no exo login token found; run \"exo login\"")
	}
	personID = lib.PersonAgentID(unixID)
	personLabel = label
	assertedByID = personID
	return personID, personLabel, assertedByID, nil
}
