---
title: "exo standalone status"
description: "Show health of the adb-standalone stack"
---

Full `exo standalone status --help` output, generated from exo-cli v0.140.2.

```text
Show health of the adb-standalone stack

Usage:
  exo standalone status [flags]


Flags:
      --adb-port int         adb-standalone listen port (default 8080)
      --data-dir string      Directory used by 'exo standalone up' (default: $HOME/.config/exo/standalone)
  -h, --help                 help for status
      --surreal-port int     SurrealDB listen port (default 8000)
      --versitygw-port int   versitygw listen port (default 9100)

Global Flags:
      --config-dir string   Root directory for all config, cache and credentials (overrides EXO_CONFIG_DIR)
      --context string      Use specific context (overrides selected context)

Docs for agents (llms.txt): https://genentech.github.io/exohub/llms.txt

Full manual: https://genentech.github.io/exohub/docs/reference/cli/exo-standalone-status/
```
