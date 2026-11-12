---
title: "exo pull"
description: "Clone or update a repo at a ref"
---

Full `exo pull --help` output, generated from exo-cli v0.140.2.

```text
Clone or update a git repository at a specific ref.

Clones the repository if it does not exist locally, or resets to the specified
ref if it does. Accepts a manifest YAML file as an alternative to flags.

Usage:
  exo pull [flags]


Flags:
  -h, --help              help for pull
      --manifest string   Manifest file path
      --ref string        Git ref (commit, tag, or branch)
      --url string        Git URL

Global Flags:
      --config-dir string   Root directory for all config, cache and credentials (overrides EXO_CONFIG_DIR)
      --context string      Use specific context (overrides selected context)

Docs for agents (llms.txt): https://genentech.github.io/exohub/llms.txt

Full manual: https://genentech.github.io/exohub/docs/reference/cli/exo-pull/
```
