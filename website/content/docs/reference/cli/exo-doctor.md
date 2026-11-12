---
title: "exo doctor"
description: "Run diagnostics on your ExoHub client environment"
---

Full `exo doctor --help` output, generated from exo-cli v0.140.2.

```text
Run grouped, non-destructive diagnostics of your ExoHub client environment.

Each check reports PASS, WARN, FAIL, or SKIP with a short summary and an
actionable remediation hint. Repo-scoped checks are skipped unless run inside
an ExoHub repository (or pointed at one with --repo).

Secrets (access keys, tokens, JWTs) are never printed.

Exit code is non-zero if any check is FAIL.

Usage:
  exo doctor [flags]


Flags:
  -h, --help            help for doctor
      --json            Emit results as a JSON array
      --remote string   Only run per-remote checks for this remote name
      --repo string     Path to an ExoHub repo (default: current directory)
  -v, --verbose         Show detail/remediation for PASS checks too
      --write-test      Perform an opt-in write canary (Put+Delete) under a caller-owned prefix

Global Flags:
      --config-dir string   Root directory for all config, cache and credentials (overrides EXO_CONFIG_DIR)
      --context string      Use specific context (overrides selected context)

Docs for agents (llms.txt): https://genentech.github.io/exohub/llms.txt

Full manual: https://genentech.github.io/exohub/docs/reference/cli/exo-doctor/
```
