---
title: "exo auth"
description: "Authorize a Google Drive remote"
---

Full `exo auth --help` output, generated from exo-cli v0.140.2.

```text
Authorize exo to access Google Drive for a specific remote.

Run this before syncing with a drive remote for the first time.
Credentials are stored at ~/.config/exo/drive-tokens/<remote-name>.json
and reused automatically by subsequent exo sync calls.

Examples:
  exo auth drive-export

Usage:
  exo auth <remote-name> [flags]


Flags:
      --force   Re-authorize even if credentials already exist
  -h, --help    help for auth

Global Flags:
      --config-dir string   Root directory for all config, cache and credentials (overrides EXO_CONFIG_DIR)
      --context string      Use specific context (overrides selected context)

Docs for agents (llms.txt): https://genentech.github.io/exohub/llms.txt

Full manual: https://genentech.github.io/exohub/docs/reference/cli/exo-auth/
```
