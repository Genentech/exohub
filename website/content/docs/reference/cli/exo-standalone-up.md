---
title: "exo standalone up"
description: "Bring up the full adb-standalone stack (foreground, supervised)"
---

Full `exo standalone up --help` output, generated from exo-cli v0.140.2.

```text
Launch and supervise the three-process adb-standalone stack:
  1. surrealdb     — document store
  2. versitygw     — S3-compatible object store (POSIX-FS, Apache-2.0)
  3. adb-standalone — ArtifactDB API server

Blocks in the foreground. SIGINT/SIGTERM stops all children gracefully.
Set --endpoint in config or use STORAGE_S3_ENDPOINT=managed to use managed versitygw.

Usage:
  exo standalone up [flags]


Flags:
      --access-key string    versitygw root access key (default "versitygw")
      --adb-bin string       Path to the adb-standalone binary (default "adb-standalone")
      --adb-port int         adb-standalone listen port (default 8080)
      --bucket string        S3 bucket name to auto-create on startup (default "adb-standalone")
      --config string        adb-standalone config file (default: <data-dir>/config.yaml)
      --data-dir string      Directory for persistent data and PID files (default: $EXO_CONFIG_DIR/standalone)
  -h, --help                 help for up
      --no-install           Abort instead of downloading missing stack binaries
      --secret-key string    versitygw root secret key (required)
      --surreal-bin string   Path to the surreal binary (default "surreal")
      --surreal-port int     SurrealDB listen port (default 8000)
      --versitygw-port int   versitygw listen port (default 9100)
  -y, --yes                  Skip the install confirmation prompt

Global Flags:
      --config-dir string   Root directory for all config, cache and credentials (overrides EXO_CONFIG_DIR)
      --context string      Use specific context (overrides selected context)

Docs for agents (llms.txt): https://genentech.github.io/exohub/llms.txt

Full manual: https://genentech.github.io/exohub/docs/reference/cli/exo-standalone-up/
```
