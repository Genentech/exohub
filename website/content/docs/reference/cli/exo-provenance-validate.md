---
title: "exo provenance validate"
description: "Validate provenance records"
---

Full `exo provenance validate --help` output, generated from exo-cli v0.140.2.

```text
Validate provenance records against the record profile and vocabulary,
cross-record consistency and the add-only rule.

By default it checks worktree record files (including untracked ones) with
content identifiers resolved from the index. --staged checks the index.
--ref checks a git ref's tree. Warnings do not affect the exit code; the
command exits non-zero only when an error is found.

Usage:
  exo provenance validate [flags]


Flags:
  -h, --help         help for validate
      --json         emit machine-readable JSON findings
      --ref string   validate records as of this git ref instead of the worktree
      --staged       validate the staged (index) state instead of the worktree

Global Flags:
      --config-dir string   Root directory for all config, cache and credentials (overrides EXO_CONFIG_DIR)
      --context string      Use specific context (overrides selected context)

Docs for agents (llms.txt): https://genentech.github.io/exohub/llms.txt

Full manual: https://genentech.github.io/exohub/docs/reference/cli/exo-provenance-validate/
```
