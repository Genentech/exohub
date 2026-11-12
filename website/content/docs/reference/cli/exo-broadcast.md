---
title: "exo broadcast"
description: "Broadcast git-annex metadata"
---

Full `exo broadcast --help` output, generated from exo-cli v0.140.2.

```text
Broadcast git-annex metadata (git annex sync --no-content) to the configured remotes.

If a remote force-pushed (rewrote) its main branch since this clone last synced with
it, the sync fails; broadcast then adds a hint to the error naming the remote and
'exo sync --reset-synced <r>', and aborts any merge the failed sync left in
progress. There is no up-front refusal: unpushed local commits broadcast normally.
See 'exo sync --help' for --reset-synced.

Usage:
  exo broadcast [flags]


Flags:
  -h, --help               help for broadcast
      --manifest string    Path to manifest YAML
      --with stringArray   Remote name to sync (repeatable)

Global Flags:
      --config-dir string   Root directory for all config, cache and credentials (overrides EXO_CONFIG_DIR)
      --context string      Use specific context (overrides selected context)

Docs for agents (llms.txt): https://genentech.github.io/exohub/llms.txt

Full manual: https://genentech.github.io/exohub/docs/reference/cli/exo-broadcast/
```
