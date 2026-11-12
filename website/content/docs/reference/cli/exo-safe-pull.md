---
title: "exo safe pull"
description: "Provision credentials from ExoSafe (non-interactive)"
---

Full `exo safe pull --help` output, generated from exo-cli v0.140.2.

```text
Fetch and provision credentials from ExoSafe without any interactive prompt.

Reads the bearer token via the standard precedence:
  EXO_TOKEN_FILE > EXO_CONFIG_DIR credentials/token.json > system default

Provisions SSH keys to <EXO_CONFIG_DIR>/exo/ssh/ and git tokens to
<EXO_CONFIG_DIR>/exo/git-credentials.

Exits non-zero if the token is missing or expired, or if the safe is empty.

Usage:
  exo safe pull [flags]


Flags:
  -h, --help   help for pull
      --json   Output result as JSON

Global Flags:
      --config-dir string   Root directory for all config, cache and credentials (overrides EXO_CONFIG_DIR)
      --context string      Use specific context (overrides selected context)

Docs for agents (llms.txt): https://genentech.github.io/exohub/llms.txt

Full manual: https://genentech.github.io/exohub/docs/reference/cli/exo-safe-pull/
```
