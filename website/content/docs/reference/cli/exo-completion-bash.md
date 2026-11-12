---
title: "exo completion bash"
description: "Generate the autocompletion script for bash"
---

Full `exo completion bash --help` output, generated from exo-cli v0.140.2.

```text
Generate the autocompletion script for the bash shell.

This script depends on the 'bash-completion' package.
If it is not installed already, you can install it via your OS's package manager.

To load completions in your current shell session:

	source <(exo completion bash)

To load completions for every new session, execute once:

#### Linux:

	exo completion bash > /etc/bash_completion.d/exo

#### macOS:

	exo completion bash > $(brew --prefix)/etc/bash_completion.d/exo

You will need to start a new shell for this setup to take effect.

Usage:
  exo completion bash


Flags:
  -h, --help              help for bash
      --no-descriptions   disable completion descriptions

Global Flags:
      --config-dir string   Root directory for all config, cache and credentials (overrides EXO_CONFIG_DIR)
      --context string      Use specific context (overrides selected context)

Docs for agents (llms.txt): https://genentech.github.io/exohub/llms.txt

Full manual: https://genentech.github.io/exohub/docs/reference/cli/exo-completion-bash/
```
