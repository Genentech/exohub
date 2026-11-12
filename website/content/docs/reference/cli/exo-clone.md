---
title: "exo clone"
description: "Clone repository with preset-based sparse checkout"
---

Full `exo clone --help` output, generated from exo-cli v0.140.2.

```text
Clone an exo-enabled repository with preset-based sparse checkout.

Default behavior (no flags): Discovers presets from .exohub/presets.
If presets exist, shows an interactive selection menu with "full-clone"
as the first option. If no presets exist, performs a full clone.

Examples:
  # Clone with automatic preset discovery and selection
  exo clone https://exogit.example.com/org/repo.git

  # Use specific preset (skip interactive selection)
  exo clone --preset minimal https://exogit.example.com/org/repo.git

  # Clone to custom destination
  exo clone --preset minimal https://exogit.example.com/org/repo.git my-folder

  # Auto-accept default (full-clone) for CI/CD
  exo clone --yes https://exogit.example.com/org/repo.git

Destination directory behavior (matches git clone):
  - Default: Uses last part of URL (e.g., "repo" from "org/repo.git")
  - Custom: Provide destination as second argument
  - Error: Fails if destination exists and is not empty

Usage:
  exo clone <repo-url> [destination] [flags]


Flags:
      --dry-run         Show what would be done without making changes
  -h, --help            help for clone
      --preset string   Clone preset name to use (skip interactive selection)
      --yes             Accept default choice (full-clone) automatically

Global Flags:
      --config-dir string   Root directory for all config, cache and credentials (overrides EXO_CONFIG_DIR)
      --context string      Use specific context (overrides selected context)

Docs for agents (llms.txt): https://genentech.github.io/exohub/llms.txt

Full manual: https://genentech.github.io/exohub/docs/reference/cli/exo-clone/
```
