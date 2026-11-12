---
title: "exo whoami"
description: "Show who you're logged in as, session info, and AWS credentials"
---

Full `exo whoami --help` output, generated from exo-cli v0.140.2.

```text
Show identity, session, organisation, and AWS credential info from the
stored login.

Never prints tokens, refresh tokens, or AWS secrets.

Usage:
  exo whoami [flags]


Flags:
      --aws    Also perform a live sts:GetCallerIdentity call
  -h, --help   help for whoami
      --json   Output JSON (for MCP/automation)

Global Flags:
      --config-dir string   Root directory for all config, cache and credentials (overrides EXO_CONFIG_DIR)
      --context string      Use specific context (overrides selected context)

Docs for agents (llms.txt): https://genentech.github.io/exohub/llms.txt

Full manual: https://genentech.github.io/exohub/docs/reference/cli/exo-whoami/
```
