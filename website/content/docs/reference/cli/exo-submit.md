---
title: "exo submit"
description: "Submit a manifest to the ExoHub API"
---

Full `exo submit --help` output, generated from exo-cli v0.140.2.

```text
Submit a manifest to the ExoHub API.

Reads the manifest file to determine the target queue and submits the job.
Supported queues: exohub-sync-aws, exohub-sync-shpc.

Usage:
  exo submit [flags]


Flags:
  -h, --help              help for submit
      --manifest string   Path to manifest YAML/JSON
      --queue string      Task queue (exohub-sync-aws or exohub-sync-shpc)

Global Flags:
      --config-dir string   Root directory for all config, cache and credentials (overrides EXO_CONFIG_DIR)
      --context string      Use specific context (overrides selected context)

Docs for agents (llms.txt): https://genentech.github.io/exohub/llms.txt

Full manual: https://genentech.github.io/exohub/docs/reference/cli/exo-submit/
```
