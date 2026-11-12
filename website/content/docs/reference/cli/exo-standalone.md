---
title: "exo standalone"
description: "Manage the local adb-standalone stack"
---

Full `exo standalone --help` output, generated from exo-cli v0.140.2.

```text
Manage a batteries-included local ArtifactDB stack.

The stack consists of three processes:
  surrealdb     — document store (FTS + optional vector)
  versitygw     — S3-compatible object store (POSIX-FS backend, Apache-2.0)
  adb-standalone — ArtifactDB-compatible API server

Subcommands:
  up       Bring up the full stack (foreground, supervised)
  down     Stop and clean up child processes
  status   Show health of the three processes + endpoints
  seed     Bulk-load finished ADB docs from an NDJSON file
  ingest   Catalog a new project version from a bundle directory

Usage:
  exo standalone [flags]
  exo standalone [command]

Available Commands:
  down        Stop the adb-standalone stack
  ingest      Catalog a new project version from a bundle directory
  seed        Bulk-load ADB documents from an NDJSON file
  status      Show health of the adb-standalone stack
  up          Bring up the full adb-standalone stack (foreground, supervised)

Flags:
  -h, --help   help for standalone

Global Flags:
      --config-dir string   Root directory for all config, cache and credentials (overrides EXO_CONFIG_DIR)
      --context string      Use specific context (overrides selected context)

Use "exo standalone [command] --help" for more information about a command.

Docs for agents (llms.txt): https://genentech.github.io/exohub/llms.txt

Full manual: https://genentech.github.io/exohub/docs/reference/cli/exo-standalone/
```
