---
title: "exo fsck"
description: "Inspect annex logs and repair bad files"
---

Full `exo fsck --help` output, generated from exo-cli v0.140.2.

```text
Inspect annex logs and repair bad files.

Modes:
  --scan-bad [--print]                          Scan logs for bad files and record them
  --deep [<path>] [--remote NAME]               Run git-annex fsck on bad files or a path
  --path <path>... [--remote NAME] [--manifest] Verify annexed paths
  --mark-bad [<path>] [--remote NAME]           Mark bad files in git-annex
  --drop-bad [<path>] --remote NAME             Drop bad files from a remote
  --show-bad                                    Show stored bad file paths
  --clear-bad [--no-confirm]                    Clear stored bad file paths

Usage:
  exo fsck [flags]


Flags:
      --clear-bad         Clear stored bad file paths
      --deep              Run git-annex fsck on bad files or a single path
      --drop-bad          Drop bad files from a remote
  -h, --help              help for fsck
  -J, --jobs string       Number of parallel jobs (default: $EXOHUB_JOBS or 1)
      --manifest string   Path to manifest YAML
      --mark-bad          Mark bad files in git-annex
      --no-confirm        Skip confirmation prompt
      --path              Verify annexed paths (provide paths as arguments)
      --print             Print bad file paths without recording them
      --remote string     Annex remote
      --scan-bad          Scan logs for bad files and record them
      --show-bad          Show stored bad file paths

Global Flags:
      --config-dir string   Root directory for all config, cache and credentials (overrides EXO_CONFIG_DIR)
      --context string      Use specific context (overrides selected context)

Docs for agents (llms.txt): https://genentech.github.io/exohub/llms.txt

Full manual: https://genentech.github.io/exohub/docs/reference/cli/exo-fsck/
```
