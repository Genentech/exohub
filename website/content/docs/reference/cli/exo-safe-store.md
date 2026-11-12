---
title: "exo safe store"
description: "Store a credential in ExoSafe"
---

Full `exo safe store --help` output, generated from exo-cli v0.140.2.

```text
Upload a credential to your ExoSafe.

Run without arguments for an interactive wizard, or provide flags directly.

The name should be the git provider hostname (e.g., 'github.com', 'gitlab.com').

Examples:
  exo safe store                                                  # interactive wizard
  exo safe store github.com --type ssh-key --file ~/.ssh/id_ed25519
  exo safe store github.com --type token --file ~/token.txt
  exo safe store github.com --type token --from-env GITHUB_TOKEN
  exo safe store github.com --type token                          # auto-detects $GITHUB_TOKEN

Usage:
  exo safe store [name] --type <ssh-key|token> [flags]


Flags:
      --file string       Path to the credential file
      --from-env string   Read credential value from this environment variable
  -h, --help              help for store
      --type string       Credential type: ssh-key or token
  -y, --yes               Skip confirmation prompts

Global Flags:
      --config-dir string   Root directory for all config, cache and credentials (overrides EXO_CONFIG_DIR)
      --context string      Use specific context (overrides selected context)

Docs for agents (llms.txt): https://genentech.github.io/exohub/llms.txt

Full manual: https://genentech.github.io/exohub/docs/reference/cli/exo-safe-store/
```
