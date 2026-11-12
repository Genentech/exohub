---
title: "exo completion zsh"
description: "Generate the autocompletion script for zsh"
---

Full `exo completion zsh --help` output, generated from exo-cli v0.140.2.

```text
Generate the autocompletion script for the zsh shell.

If shell completion is not already enabled in your environment you will need
to enable it.  You can execute the following once:

	echo "autoload -U compinit; compinit" >> ~/.zshrc

To load completions in your current shell session:

	source <(exo completion zsh)

To load completions for every new session, execute once:

#### Linux:

	exo completion zsh > "${fpath[1]}/_exo"

#### macOS:

	exo completion zsh > $(brew --prefix)/share/zsh/site-functions/_exo

You will need to start a new shell for this setup to take effect.

Usage:
  exo completion zsh [flags]


Flags:
  -h, --help              help for zsh
      --no-descriptions   disable completion descriptions

Global Flags:
      --config-dir string   Root directory for all config, cache and credentials (overrides EXO_CONFIG_DIR)
      --context string      Use specific context (overrides selected context)

Docs for agents (llms.txt): https://genentech.github.io/exohub/llms.txt

Full manual: https://genentech.github.io/exohub/docs/reference/cli/exo-completion-zsh/
```
