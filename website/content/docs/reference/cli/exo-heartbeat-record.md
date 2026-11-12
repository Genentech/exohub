---
title: "exo heartbeat record"
description: "Record exohub metrics into .git/exohub/metrics/"
---

Full `exo heartbeat record --help` output, generated from exo-cli v0.140.2.

```text
Record exohub metrics into .git/exohub/metrics/.

Collects git-annex transfer progress, path-level sync status, network I/O, and
bad-file counts, then writes the result as JSON. Use --watch to record continuously.

Usage:
  exo heartbeat record [flags]


Flags:
  -h, --help                 help for record
      --interval float       Interval for --watch (seconds) (default 2)
      --manifest string      Manifest file providing repo-dir
      --metrics-dir string   Directory for metrics output
      --out string           Output metrics filename (default "latest.json")
      --path stringArray     Path or glob (repeatable)
      --repo string          Repository root
      --smart-paths          Smart per-path recompute
      --watch                Continuously record snapshots

Global Flags:
      --config-dir string   Root directory for all config, cache and credentials (overrides EXO_CONFIG_DIR)
      --context string      Use specific context (overrides selected context)

Docs for agents (llms.txt): https://genentech.github.io/exohub/llms.txt

Full manual: https://genentech.github.io/exohub/docs/reference/cli/exo-heartbeat-record/
```
