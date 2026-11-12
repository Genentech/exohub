---
title: "exo unlock"
description: "Unlock annexed files (make them writable)"
---

Full `exo unlock --help` output, generated from exo-cli v0.140.2.

```text
Unlock annexed files, replacing read-only symlinks with writable copies.

Wraps git-annex unlock.

Examples:
  exo unlock                   # unlock all annexed files in current directory
  exo unlock data/             # unlock files under data/

Usage:
  exo unlock [flags] [path ...]


Flags:
  -h, --help   help for unlock

Global Flags:
      --config-dir string   Root directory for all config, cache and credentials (overrides EXO_CONFIG_DIR)
      --context string      Use specific context (overrides selected context)

Docs for agents (llms.txt): https://genentech.github.io/exohub/llms.txt

Full manual: https://genentech.github.io/exohub/docs/reference/cli/exo-unlock/
```
