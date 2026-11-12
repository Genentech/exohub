---
title: "exo login"
description: "Authenticate via device flow"
---

Full `exo login --help` output, generated from exo-cli v0.140.2.

```text
Authenticate via OIDC device flow.

This command initiates a device authentication flow that allows you to log in
using any device with a web browser. After authentication, credentials are
securely cached for future use.

Usage:
  exo login [flags]


Flags:
      --force    Force re-authentication even if valid credentials exist
  -h, --help     help for login
      --json     Output JSON (for MCP/automation)
      --qrcode   Display QR code for authentication

Global Flags:
      --config-dir string   Root directory for all config, cache and credentials (overrides EXO_CONFIG_DIR)
      --context string      Use specific context (overrides selected context)

Docs for agents (llms.txt): https://genentech.github.io/exohub/llms.txt

Full manual: https://genentech.github.io/exohub/docs/reference/cli/exo-login/
```
