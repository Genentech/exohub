---
title: "exo heartbeat show"
description: "Show heartbeat metrics"
---

Full `exo heartbeat show --help` output, generated from exo-cli v0.140.2.

```text
Show heartbeat metrics for a repository.

Reads from the local metrics file by default, or fetches from the API when --workflow-id is provided.

Usage:
  exo heartbeat show [flags]


Flags:
  -h, --help                 help for show
      --json                 Output raw JSON
      --manifest string      Manifest file providing repo-dir
      --metric-file string   Metrics filename to read (default "latest.json")
      --repo string          Repository root
      --run-id string        Fetch metrics for a specific workflow run (requires --workflow-id)
  -v, --verbose              Expand all paths with full detail
      --workflow-id string   Fetch metrics from API

Global Flags:
      --config-dir string   Root directory for all config, cache and credentials (overrides EXO_CONFIG_DIR)
      --context string      Use specific context (overrides selected context)

Docs for agents (llms.txt): https://genentech.github.io/exohub/llms.txt

Full manual: https://genentech.github.io/exohub/docs/reference/cli/exo-heartbeat-show/
```
