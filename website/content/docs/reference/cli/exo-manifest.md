---
title: "exo manifest"
description: "Validate and initialize exohub manifests"
---

Full `exo manifest --help` output, generated from exo-cli v0.140.2.

```text
Validate and initialize exohub manifests.

Commands:
  validate <yaml>   Validate a manifest file against its schema
  init              Print a manifest template to stdout

Notes:
  - validate requires canonical keys (no aliases) and validates the manifest as-is.
  - validate infers the manifest type from remote-type (and treats manifests with activities as workflow).
  - --schema validates against a local JSON schema; otherwise schemas are fetched from EXOHUB_API_URL.
  - "sync" is accepted as an alias for "annex".
  - YAML parsing uses the built-in parser.

Usage:
  exo manifest [flags]
  exo manifest [command]

Available Commands:
  init        Print a manifest template
  validate    Validate a manifest

Flags:
  -h, --help   help for manifest

Global Flags:
      --config-dir string   Root directory for all config, cache and credentials (overrides EXO_CONFIG_DIR)
      --context string      Use specific context (overrides selected context)

Use "exo manifest [command] --help" for more information about a command.

Docs for agents (llms.txt): https://genentech.github.io/exohub/llms.txt

Full manual: https://genentech.github.io/exohub/docs/reference/cli/exo-manifest/
```
