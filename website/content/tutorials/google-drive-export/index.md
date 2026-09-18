---
title: "Exporting Data to Google Drive"
description: "Share processed results with stakeholders by exporting annexed data to Google Drive — no CLI required on their end"
---

This tutorial shows how to export data from an ExoHub repository to a Google Drive folder so that
collaborators — analysts, reviewers, or stakeholders who use Google Workspace — can browse, preview,
and download files without installing `exo` or touching the command line.

**Use case:** you've processed a dataset and want to hand off the results to non-technical
collaborators. Instead of packing and emailing files, you push them to a Drive folder they already
have access to, keeping everything reproducible and versioned on your side.

> See the {{< ref "docs/user-guide/drive-remote" >}} reference page for all configuration options
> and authentication details.

## Prerequisites

- `exo` installed and up to date — run the installer if needed:

  ```bash
  {{< exohub "installCmd" >}}
  ```

- An existing ExoHub repository with annexed files. If you need one, follow the
  [MNIST tutorial](/tutorials/mnist/) first.

- A Google account with Google Drive access.

## 1. Add a Drive Remote

Open `.exohub/remotes` in your repository. Add a new entry for the Drive remote alongside any
existing remotes:

```yaml
remotes:
  - name: s3-archive
    type: annex
    s3url: s3://exohub-sandbox-uat/tutorials/YOUR_UNIXID/mnist/_annex

  - name: s3-export
    type: export
    s3url: s3://exohub-sandbox-uat/tutorials/YOUR_UNIXID/mnist/_export

  - name: my-drive                              # any name you like
    type: drive
    drive_path: /My Drive/datasets/mnist        # folder path inside Drive
    tracking_branch: main
    include:
      - data/processed/**                       # export only processed results
```

Key fields:

| Field | What it does |
|-------|-------------|
| `type: drive` | Tells `exo` to use the Google Drive backend |
| `drive_path` | The Drive folder that will receive the files (created automatically) |
| `tracking_branch` | The branch whose content is tracked for preferred-content rules |
| `include` | Glob patterns — only matching files are exported; omit to export everything |

`include` patterns are enforced at the remote level, so both plain files (like `.exohub/` metadata)
and annexed files respect them. See {{< ref "docs/user-guide/drive-remote" >}} for `exclude`
patterns and other options.

## 2. Initialize the Remote

After editing `.exohub/remotes`, register the new remote with git-annex:

```bash
exo init
```

Expected output:

```
✅ Initialized remote 's3-archive'
✅ Initialized remote 's3-export'
⚠️  Drive remote 'my-drive' needs authorization. Run: exo auth my-drive
```

The Drive remote is registered but not yet authorized. Commit the updated config:

```bash
git add .exohub/
git commit -m "feat: add Google Drive export remote"
```

## 3. Authorize Google Drive (one-time)

`exo` uses Google's device flow, so you can authorize from any device — even a remote server without
a browser:

```bash
exo auth my-drive
```

```
To authorize exo to access Google Drive:
  1. Go to: https://www.google.com/device
  2. Enter code: WXYZ-1234

Waiting for authorization... ✅

✅ Authorized. Token saved to ~/.config/exo/drive-tokens/my-drive.json
   You can now run: exo sync --with my-drive
```

Open the URL on any device (phone, laptop, browser), enter the code shown, and grant access. The
CLI polls until you confirm.

Tokens are stored locally in `~/.config/exo/drive-tokens/` and are **never** committed to the
repository. Subsequent syncs refresh the token automatically.

## 4. Export to Drive

Now sync your data to Drive:

```bash
exo sync --with my-drive
```

`exo` uploads every annexed file that matches the `include` patterns and mirrors the working-tree
layout as a Drive folder hierarchy:

```
data/processed/train.parquet  →  /My Drive/datasets/mnist/data/processed/train.parquet
data/processed/test.parquet   →  /My Drive/datasets/mnist/data/processed/test.parquet
```

You can also target the Drive remote alone, without touching your S3 remotes:

```bash
exo sync --to my-drive
```

### Export a specific path

```bash
exo sync --to my-drive --path data/processed/
```

### Export a tagged version

```bash
exo sync --to my-drive --ref v1.0.0
```

## 5. What It Looks Like in Drive

Once the sync completes, collaborators open their Google Drive and navigate to:

```
My Drive / datasets / mnist / data / processed /
```

They see the real file names — `train.parquet`, `test.parquet` — as ordinary Drive items. They can
share the folder, preview supported formats, or download directly. No ExoHub account or CLI is
needed on their side.

## 6. Exporting Only Specific Files

Suppose your repository has multiple output directories and you only want to share one set of files
with a particular audience. Update the `include` list:

```yaml
  - name: stakeholder-drive
    type: drive
    drive_path: /My Drive/project-x/results
    tracking_branch: main
    include:
      - reports/**/*.pdf
      - summaries/*.csv
```

After editing, run `exo init` again and then sync:

```bash
exo init
exo sync --to stakeholder-drive
```

Only files matching those patterns are pushed to Drive — raw data files, scripts, and metadata stay
out of the shared folder.

## What's Next

- {{< ref "docs/user-guide/drive-remote" >}} — full reference for Drive remote configuration,
  authentication details, `drive.file` scope, and known limitations
- {{< ref "docs/user-guide/syncing" >}} — sync, export, and broadcast workflows in detail
- {{< ref "docs/user-guide/repositories" >}} — overview of all remote types and `.exohub/remotes`
  configuration
