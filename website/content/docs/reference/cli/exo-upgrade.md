---
title: "exo upgrade"
description: "Upgrade exo CLI"
---

Full `exo upgrade --help` output, generated from exo-cli v0.140.2.

```text
Upgrade exo CLI.

The version may be given as a bare semver (0.134.0) or a v-prefixed
release tag (v0.134.0); bare semvers are normalized to the tag form.

Note: right after a new release, 'exo upgrade' with no version may
briefly still install the previous version, until the artifact store's
'latest' pointer catches up. If that happens, pass the version
explicitly, e.g. 'exo upgrade v0.134.0'.

Usage:
  exo upgrade [version] [flags]


Flags:
  -h, --help             help for upgrade
      --version string   upgrade to a specific version

Global Flags:
      --config-dir string   Root directory for all config, cache and credentials (overrides EXO_CONFIG_DIR)
      --context string      Use specific context (overrides selected context)

Docs for agents (llms.txt): https://genentech.github.io/exohub/llms.txt

Full manual: https://genentech.github.io/exohub/docs/reference/cli/exo-upgrade/
```
