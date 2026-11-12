---
title: "exo get"
description: "Get annexed content (wrapper around git-annex get with sensible defaults)"
---

Full `exo get --help` output, generated from exo-cli v0.140.2.

```text
Get annexed content from remotes into the local repository.

Wraps git-annex get with sensible defaults:
  - Resumes incomplete downloads first (--incomplete), then gets remaining content
  - Parallelism from EXOHUB_JOBS environment variable (default: 1)

Examples:
  exo get                     # get all annexed content in current directory
  exo get data/               # get content under data/
  exo get --from s3-backup .  # get from a specific remote
  exo get --all               # get all versions of all files
  exo get -J 8 .              # get with 8 parallel jobs

Usage:
  exo get [flags] [path ...]


Flags:
      --all           Get all versions of all files
      --from string   Source annex remote
  -h, --help          help for get
  -J, --jobs string   Number of parallel jobs (default: $EXOHUB_JOBS or 1)

Global Flags:
      --config-dir string   Root directory for all config, cache and credentials (overrides EXO_CONFIG_DIR)
      --context string      Use specific context (overrides selected context)

Docs for agents (llms.txt): https://genentech.github.io/exohub/llms.txt

Full manual: https://genentech.github.io/exohub/docs/reference/cli/exo-get/
```
