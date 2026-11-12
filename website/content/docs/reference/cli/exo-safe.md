---
title: "exo safe"
description: "Manage credentials in ExoSafe"
---

Full `exo safe --help` output, generated from exo-cli v0.140.2.

```text
Manage SSH keys and git tokens in your personal ExoSafe credential store.

ExoSafe stores credentials in the cloud (AWS Parameter Store) so they can be
provisioned automatically when you run 'exo login' in new environments.

Usage:
  exo safe [flags]
  exo safe [command]

Available Commands:
  clear       Remove all credentials from ExoSafe
  delete      Delete a credential from ExoSafe
  dump        Dump all credentials to a file
  list        List credentials in ExoSafe
  pull        Provision credentials from ExoSafe (non-interactive)
  restore     Restore credentials from a dump file
  show        Show a credential from ExoSafe
  store       Store a credential in ExoSafe

Flags:
  -h, --help   help for safe

Global Flags:
      --config-dir string   Root directory for all config, cache and credentials (overrides EXO_CONFIG_DIR)
      --context string      Use specific context (overrides selected context)

Use "exo safe [command] --help" for more information about a command.

Docs for agents (llms.txt): https://genentech.github.io/exohub/llms.txt

Full manual: https://genentech.github.io/exohub/docs/reference/cli/exo-safe/
```
