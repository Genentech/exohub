---
title: "exo export"
description: "Export annexed content"
---

Full `exo export --help` output, generated from exo-cli v0.140.2.

```text
Export annexed content to a remote.

Branch Prefix Mode:
  Controls whether branch names appear in S3 paths when using --path flag.

  Default behavior (no flag):
    Uses branch only (no colon notation)
    Example: main → s3://bucket/project/data/
    Assumes s3url contains the full target path

  With --branch-prefix flag:
    Uses branch:subdir notation (creates branch directories in S3)
    Example: main:data/ → s3://bucket/_export/refs/heads/main/data/
    Branch name becomes part of the S3 path structure

Examples:
  # Default (no colon)
  exo export --to my-remote --path data/
  → Uses: main
  → Result: s3://bucket/target/file.txt
  (Assumes s3url is s3://bucket/target/)

  # With branch prefix
  exo export --to my-remote --path data/ --branch-prefix
  → Uses: main:data/
  → Result: s3://bucket/_export/refs/heads/main/data/file.txt

  # Multiple paths require --branch-prefix
  exo export --to my-remote --path dir1/ --path dir2/ --branch-prefix

Note: Multiple --path flags require --branch-prefix flag.

Usage:
  exo export [flags]


Flags:
      --branch-prefix       Use branch:subdir notation (creates branch directories in S3 paths)
      --dry-run             Report planned exports without running git-annex export
      --from string         Source annex remote
  -h, --help                help for export
  -J, --jobs string         Number of parallel jobs (default: 1)
      --json                Emit a single JSON dry-run report to stdout
      --log-prefix string   Prefix for log filenames (useful for Temporal workflows)
      --manifest string     Path to manifest YAML
      --path stringArray    Path to limit export (repeatable)
      --ref string          Commit, tag, or branch (default "HEAD")
      --to string           Destination annex remote
      --yes                 Automatically confirm tracking branch updates

Global Flags:
      --config-dir string   Root directory for all config, cache and credentials (overrides EXO_CONFIG_DIR)
      --context string      Use specific context (overrides selected context)

Docs for agents (llms.txt): https://genentech.github.io/exohub/llms.txt

Full manual: https://genentech.github.io/exohub/docs/reference/cli/exo-export/
```
