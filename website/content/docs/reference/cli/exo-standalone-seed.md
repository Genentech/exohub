---
title: "exo standalone seed"
description: "Bulk-load ADB documents from an NDJSON file"
---

Full `exo standalone seed --help` output, generated from exo-cli v0.140.2.

```text
Bulk-load finished ADB documents into the running adb-standalone instance.

Each line of the NDJSON file must be a complete document with _extra.id set.
Documents are upserted verbatim (builds search_text). Designed for bootstrapping
from an adb-capture snapshot or migrating from a private → enterprise deployment.

Wraps: adb-standalone seed <ndjson-file>

Usage:
  exo standalone seed <ndjson-file> [flags]


Flags:
      --adb-bin string   Path to the adb-standalone binary (default "adb-standalone")
      --config string    adb-standalone config file
  -h, --help             help for seed

Global Flags:
      --config-dir string   Root directory for all config, cache and credentials (overrides EXO_CONFIG_DIR)
      --context string      Use specific context (overrides selected context)

Docs for agents (llms.txt): https://genentech.github.io/exohub/llms.txt

Full manual: https://genentech.github.io/exohub/docs/reference/cli/exo-standalone-seed/
```
