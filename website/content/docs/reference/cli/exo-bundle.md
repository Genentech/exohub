---
title: "exo bundle"
description: "Generate JSON metadata bundle for catalog ingest"
---

Full `exo bundle --help` output, generated from exo-cli v0.140.2.

```text
Generate JSON metadata files describing a dataset repository at the current git revision.

Output consists of bundle.json (bundle manifest), per-commit JSONs under commits/
(exohub-commit/v1.json), and per-file artifact JSONs under files/ (exohub-artifact/v1.json).

Output is written to .git/exohub/bundle/<scope>/ where <scope> is derived
from the current ref context (tag, branch, or commit hash).

Examples:
  exo bundle                          # generate bundle with default preset
  exo bundle --preset parquet-only    # use a named preset
  exo bundle --from-ref v1.0          # commits since v1.0
  exo bundle --scope annex            # annexed file artifacts + markdown artifacts
  exo bundle --output /tmp/bundle     # custom output directory
  exo bundle --dry-run                # preview what the bundle would do
  exo bundle --ref v5.3.0             # build bundle for a specific tag
  exo bundle --ref v5.3.0 --dry-run   # preview bundle for a specific tag

Usage:
  exo bundle [PATH...] [flags]


Flags:
      --dry-run              Show what the bundle would do without generating anything
      --from-ref string      Start of commit range (ref or regex)
  -h, --help                 help for bundle
      --include-hidden       Include files in hidden directories (default: skip)
      --incremental          Only generate file artifacts for files modified in the commit range
      --markdown-recursive   Parse markdown files in all directories (default: root-level only)
      --no-provenance        Skip compiling provenance.jsonld (provenance.require is still enforced)
      --output string        Custom output directory
      --preset string        Bundle manifest preset name (default "default")
      --ref string           Commit hash or tag to build the bundle for (default: HEAD)
      --resume               Resume a previous bundle run, skipping existing artifacts
      --scope string         Which metadata to generate: all, git, or annex (default: from preset or all)
      --verify               Re-resolve every named collection's external members over the network and fail on a lock mismatch, instead of trusting the committed lock (design D5 amendment of add-exohub-asset-import)
  -y, --yes                  Skip confirmation prompts (remove and regenerate)

Global Flags:
      --config-dir string   Root directory for all config, cache and credentials (overrides EXO_CONFIG_DIR)
      --context string      Use specific context (overrides selected context)

Docs for agents (llms.txt): https://genentech.github.io/exohub/llms.txt

Full manual: https://genentech.github.io/exohub/docs/reference/cli/exo-bundle/
```
