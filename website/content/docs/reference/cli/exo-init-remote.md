---
title: "exo init remote"
description: "Create and configure git-annex remotes"
---

Full `exo init remote --help` output, generated from exo-cli v0.140.2.

```text
Create git-annex remotes with declarative configuration or interactive TUI.

Examples:
  exo init remote --type annex --name s3-backup --s3url s3://bucket/prefix
  exo init remote --type export --name s3-export --s3url s3://bucket/prefix
  exo init remote --type import --name s3-import --s3url s3://bucket/prefix
  exo init remote --type exospace --name shared-data --rsyncurl /mnt/shared
  exo init remote --type drive --name my-drive
  exo init remote  # Interactive TUI mode

Exospace remotes support permission presets via .exohub/remotes:
  permissions: public   - World-readable files (--chmod=ugo=rwX,Do+t,Fo-w)
  permissions: group    - Group writable, others read-only
  permissions: private  - Owner-only access

Or use rsync_options for custom rsync flags (mutually exclusive with permissions).

Usage:
  exo init remote [flags]


Flags:
      --chunk string             Chunk size (for annex type) (default "1GiB")
      --grants                   Enable fine-grained S3 Access Grants permissions
  -h, --help                     help for remote
      --name string              Remote name
      --rsyncurl string          Rsync URL (for exospace type)
      --s3url string             S3 URL (for annex/export/import types)
      --tracking-branch string   Tracking branch (for export/import types)
      --type string              Remote type (annex, export, import, exospace, drive)

Global Flags:
      --config-dir string   Root directory for all config, cache and credentials (overrides EXO_CONFIG_DIR)
      --context string      Use specific context (overrides selected context)

Docs for agents (llms.txt): https://genentech.github.io/exohub/llms.txt

Full manual: https://genentech.github.io/exohub/docs/reference/cli/exo-init-remote/
```
