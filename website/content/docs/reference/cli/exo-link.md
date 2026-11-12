---
title: "exo link"
description: "Reconnect git-annex symlinks to an external store of annex key files"
---

Full `exo link --help` output, generated from exo-cli v0.140.2.

```text
Reconnect git-annex symlinks to an external store of annex key files.

Examples:
  exo link -s /mnt/annex-store
  exo link -s /mnt/annex-store -r /path/to/repo --mode auto -v

Usage:
  exo link [flags]


Flags:
      --cache string       Load/save a basename->path index to this file
  -n, --dry-run            Show actions without making changes
  -f, --force              Overwrite existing targets instead of skipping
  -h, --help               help for link
  -k, --key stringArray    Query mapping for this annex key basename
      --key-file string    Read annex keys (one per line) to query
      --map-out string     Write TSV of annex_key_basename->repo_path for all symlinks
  -m, --mode string        One of: auto | hardlink | symlink | copy (default "hardlink")
      --no-index           Skip pre-indexing store; do on-demand lookups
  -p, --path stringArray   Path within repo to limit scan
      --refresh-cache      Rebuild cache from store even if cache file exists
  -r, --repo string        Path to git-annex repo
  -s, --store string       Path to directory containing annex key files
      --strict             Exit non-zero if any objects are missing
  -v, --verbose            Increase logging

Global Flags:
      --config-dir string   Root directory for all config, cache and credentials (overrides EXO_CONFIG_DIR)
      --context string      Use specific context (overrides selected context)

Docs for agents (llms.txt): https://genentech.github.io/exohub/llms.txt

Full manual: https://genentech.github.io/exohub/docs/reference/cli/exo-link/
```
