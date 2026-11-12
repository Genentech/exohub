---
title: "exo locate"
description: "Show where files are stored (git, annex, remotes)"
---

Full `exo locate --help` output, generated from exo-cli v0.140.2.

```text
Show storage location and metadata for files.

Reports whether each file is tracked by regular git or git-annex.
For annexed files, shows which remotes hold a copy (with remote type
from .exohub/remotes).

Use --fast for lightweight git/annex/untracked classification only.

Filter flags narrow output to specific file types:

  exo locate --annex              # only annexed files
  exo locate --missing            # annexed files not present locally
  exo locate --git --untracked    # everything except annex
  exo locate --present data/      # annexed files present locally in data/
  exo locate --locked             # only locked (symlink) annexed files
  exo locate --unlocked           # only unlocked (editable) annexed files
  exo locate --locked --present   # locked annexed files present locally

Multiple type flags combine as OR. --present and --missing imply --annex.
--locked and --unlocked imply --annex and compose with --present/--missing.

Usage:
  exo locate [paths...] [flags]


Flags:
      --annex       Show only annexed files
      --fast        Only classify files as git/annex/untracked (no remote info)
      --git         Show only git-tracked files
  -h, --help        help for locate
      --json        Output machine-readable JSON
      --locked      Show only locked annexed files
      --missing     Show only annexed files not present locally
      --present     Show only annexed files present locally
      --unlocked    Show only unlocked annexed files
      --untracked   Show only untracked files

Global Flags:
      --config-dir string   Root directory for all config, cache and credentials (overrides EXO_CONFIG_DIR)
      --context string      Use specific context (overrides selected context)

Docs for agents (llms.txt): https://genentech.github.io/exohub/llms.txt

Full manual: https://genentech.github.io/exohub/docs/reference/cli/exo-locate/
```
