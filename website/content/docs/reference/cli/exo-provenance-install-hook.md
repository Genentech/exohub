---
title: "exo provenance install-hook"
description: "Install a pre-commit hook that runs exo provenance validate --staged"
---

Full `exo provenance install-hook --help` output, generated from exo-cli v0.140.2.

```text
Install a git pre-commit hook that runs "exo provenance validate --staged"
before every commit: a staged file matching provenance.require
without a covering record blocks the commit.

The hook is added as a marked block inserted right after the shebang line
of .git/hooks/pre-commit (or the repository's configured hooks directory).
Existing hook content is kept; running this command again updates the
block in place instead of duplicating it.

pre-commit framework: add this to .pre-commit-config.yaml instead of
running this command:

  - repo: local
    hooks:
      - id: exo-provenance-validate
        name: exo provenance validate --staged
        entry: exo provenance validate --staged
        language: system
        pass_filenames: false

Usage:
  exo provenance install-hook [flags]


Flags:
  -h, --help   help for install-hook

Global Flags:
      --config-dir string   Root directory for all config, cache and credentials (overrides EXO_CONFIG_DIR)
      --context string      Use specific context (overrides selected context)

Docs for agents (llms.txt): https://genentech.github.io/exohub/llms.txt

Full manual: https://genentech.github.io/exohub/docs/reference/cli/exo-provenance-install-hook/
```
