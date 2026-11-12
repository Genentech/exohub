---
title: "exo mirror plan"
description: "Generate an s5cmd plan file without executing"
---

Full `exo mirror plan --help` output, generated from exo-cli v0.140.2.

```text
Generate an s5cmd plan file without executing

Usage:
  exo mirror plan [flags]


Flags:
      --dry-run            Show summary without writing files
  -h, --help               help for plan
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

Docs for agents (llms.txt): https://genentech.github.io/exohub/llms.txt

Full manual: https://genentech.github.io/exohub/docs/reference/cli/exo-mirror-plan/
```
