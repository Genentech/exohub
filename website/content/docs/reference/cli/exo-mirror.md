---
title: "exo mirror"
description: "Resolve annex keys and mirror them from S3"
---

Full `exo mirror --help` output, generated from exo-cli v0.140.2.

```text
Resolve annex keys under repo paths and fetch them from S3 via s5cmd.

Subcommands:
  plan              Generate an s5cmd plan file (no execution)
  execute [FILE]    Execute a previously generated plan (default: .git/exohub/exo-mirror-plan.txt)

The execute subcommand reads metadata from a sidecar file (same basename with .env)
to recover recorded settings. Honors ignore-errors if present; otherwise fails on s5cmd errors.

Examples:
  exo mirror plan --s3-annex s3://my-bucket/annex --store /mnt/store --path db
  exo mirror execute                      # uses .git/exohub/exo-mirror-plan.txt
  exo mirror execute /path/to/plan.txt    # execute a specific plan file

Usage:
  exo mirror [flags]
  exo mirror [command]

Available Commands:
  execute     Execute a previously generated plan
  plan        Generate an s5cmd plan file without executing

Flags:
      --dry-run            Show summary without writing files
      --execute            Execute plan immediately
  -h, --help               help for mirror
      --ignore-errors      Ignore s5cmd failures
      --jobs int           s5cmd worker count (default 16)
      --keys-out string    Write resolved keys
      --map-out string     Write key->path map
      --mode string        auto or direct (default "auto")
      --no-clobber         Avoid overwriting existing files
      --path stringArray   Restrict scan to specific paths
      --plan-out string    Write s5cmd command file
      --repo string        Git repository root
      --s3-annex string    S3 prefix like s3://bucket/path
      --store string       Local store directory
  -v, --verbose            Extra logging

Global Flags:
      --config-dir string   Root directory for all config, cache and credentials (overrides EXO_CONFIG_DIR)
      --context string      Use specific context (overrides selected context)

Use "exo mirror [command] --help" for more information about a command.

Docs for agents (llms.txt): https://genentech.github.io/exohub/llms.txt

Full manual: https://genentech.github.io/exohub/docs/reference/cli/exo-mirror/
```
