---
title: "exo provenance"
description: "Record and inspect data lineage"
---

Full `exo provenance --help` output, generated from exo-cli v0.140.2.

```text
Record and inspect data lineage as W3C PROV records.

Records live as JSON-LD files in .exohub/provenance/, one file per record.
They are add-only: a committed record is never edited or deleted. A
correction is a new record that supersedes the ones it replaces
(--supersedes).

A folder can be one artifact (a folder collection, identified by its git
tree id). A named collection covers an arbitrary, named set of files that
is not one folder, declared in .exohub/collections/<name>.json and used as
--generated collection:<name>; see docs/provenance.md for the manifest
format and the minimum exo version it requires.

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
  install-hook Install a pre-commit hook that runs validate --staged

Usage:
  exo provenance [flags]
  exo provenance [command]

Available Commands:
  add          Create a new provenance record
  install-hook Install a pre-commit hook that runs exo provenance validate --staged
  show         Show an artifact's provenance lineage
  validate     Validate provenance records

Flags:
  -h, --help   help for provenance

Global Flags:
      --config-dir string   Root directory for all config, cache and credentials (overrides EXO_CONFIG_DIR)
      --context string      Use specific context (overrides selected context)

Use "exo provenance [command] --help" for more information about a command.

Docs for agents (llms.txt): https://genentech.github.io/exohub/llms.txt

Full manual: https://genentech.github.io/exohub/docs/reference/cli/exo-provenance/
```
