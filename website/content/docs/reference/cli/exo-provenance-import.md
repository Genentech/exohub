---
title: "exo provenance import"
description: "Import provenance a workflow engine already recorded"
---

Full `exo provenance import --help` output, generated from exo-cli v0.145.1.

```text
Read one run from a supported source format and write ExoHub provenance
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
person (run 'exo login').

Usage:
  exo provenance import <format> [<run>] [flags]


Flags:
      --dry-run                  print the records that would be written; write and stage nothing
  -h, --help                     help for import
      --json                     emit machine-readable JSON
      --keep-source              commit a copy of the run's source lineage documents under .exohub/provenance/sources/, referenced as evidence
      --store string             location of the format's lineage store (format-specific; defaults to the format's usual location)
      --supersedes stringArray   record id this import supersedes (repeatable); required to re-import an already-imported run
      --tasks                    write one activity per task instead of one for the whole run

Global Flags:
      --config-dir string   Root directory for all config, cache and credentials (overrides EXO_CONFIG_DIR)
      --context string      Use specific context (overrides selected context)

Docs for agents (llms.txt): https://genentech.github.io/exohub/llms.txt

Full manual: https://genentech.github.io/exohub/docs/reference/cli/exo-provenance-import/
```
