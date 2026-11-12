---
title: "exo atlas"
description: "Browse and search an ArtifactDB catalog"
---

Full `exo atlas --help` output, generated from exo-cli v0.140.2.

```text
Browse and search content in an ArtifactDB catalog using an interactive TUI.

If an artifact ID is provided, the TUI opens directly to that document.

The catalog URL is resolved in this order:
  1. --url flag
  2. EXOHUB_CATALOG_URL environment variable
  3. catalog_url field in the active exo context

Examples:
  exo atlas                                          # search and browse
  exo atlas "myproject:file.txt@v1"                  # open a specific document
  exo atlas --url https://catalog.example.com/v1/mydb
  exo atlas --query "human" --latest                 # search with pre-populated query
  exo atlas --query "rnaseq" --sort "-_extra.uploaded"

Usage:
  exo atlas [artifact-id] [flags]


Flags:
      --download-ui         Automatically download UI elements without confirmation
      --fields string       Response fields
  -h, --help                help for atlas
      --latest              Only show latest versions
  -o, --output-dir string   Output directory for downloaded files (default ".")
      --project string      Filter by project ID
  -q, --query string        Pre-populate search query
      --schema string       Filter by schema (e.g. exohub-artifact/v1)
      --size int            Number of results
      --sort string         Sort order (e.g. -_extra.uploaded)
      --url string          ArtifactDB instance URL
      --version string      Filter by version (e.g. v1.0.0, or 'latest')

Global Flags:
      --config-dir string   Root directory for all config, cache and credentials (overrides EXO_CONFIG_DIR)
      --context string      Use specific context (overrides selected context)

Docs for agents (llms.txt): https://genentech.github.io/exohub/llms.txt

Full manual: https://genentech.github.io/exohub/docs/reference/cli/exo-atlas/
```
