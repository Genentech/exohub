---
title: "exo lock"
description: "Lock annexed files (switch back to read-only symlinks)"
---

Full `exo lock --help` output, generated from exo-cli v0.140.2.

```text
Lock annexed files, switching them back to read-only symlinks.

Wraps git-annex lock.

Examples:
  exo lock                     # lock all annexed files in current directory
  exo lock data/               # lock files under data/
  exo lock --force .           # lock even if files have unsaved modifications

Usage:
  exo lock [flags] [path ...]


Flags:
      --force   Lock even with unsaved modifications
  -h, --help    help for lock

Global Flags:
      --config-dir string   Root directory for all config, cache and credentials (overrides EXO_CONFIG_DIR)
      --context string      Use specific context (overrides selected context)

Docs for agents (llms.txt): https://genentech.github.io/exohub/llms.txt

Full manual: https://genentech.github.io/exohub/docs/reference/cli/exo-lock/
```
