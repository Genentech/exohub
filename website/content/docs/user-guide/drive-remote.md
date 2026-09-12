---
title: "Google Drive Remote"
description: "Export annexed files to Google Drive for collaborators using Google Workspace"
weight: 9
---

The Google Drive remote lets you export annexed data to a Drive folder where collaborators can browse, download, and share files using the Drive UI — without needing S3 access or the `exo` CLI.

## What It Does

An `exo` Drive remote exports your git-annexed files to a Google Drive folder, preserving the working tree layout as Drive folder hierarchy. For example:

```
data/results/sample_001.parquet  →  Drive: my-drive/results/sample_001.parquet
data/results/sample_002.parquet  →  Drive: my-drive/results/sample_002.parquet
```

Collaborators see real filenames and a familiar folder structure in the Drive UI. They can share, preview, and download files as normal Drive items.

**Drive remotes are export-only in v1.** You cannot import data from Drive into git-annex with this remote type.

## Configuration

Add a Drive remote to `.exohub/remotes`:

```yaml
remotes:
  - name: my-drive
    type: drive
    drive_path: /My Drive/datasets/gwasdb
    tracking_branch: main
    include:
      - gwasdb-studies/**/*.parquet
```

| Field | Required | Description |
|-------|----------|-------------|
| `name` | Yes | Remote name; used with `--with` and `--to` flags |
| `type` | Yes | Must be `drive` |
| `drive_path` | Yes | Full Drive path to the target folder (see [constraints](#drive_path-constraints)) |
| `tracking_branch` | No | Branch to track for preferred content |
| `include` | No | Glob patterns — only matching files are exported |
| `exclude` | No | Glob patterns — matching files are skipped |

Run `exo init` after editing `.exohub/remotes` to configure the remote.

Alternatively, use `exo init remote` to set up the remote interactively or declaratively without editing the file manually:

```bash
# Interactive TUI — prompts for name, drive_path, and tracking branch
exo init remote --type drive

# Declarative — provide values directly
exo init remote --type drive --name my-drive
```

The `git-annex-remote-drive` helper binary is installed automatically alongside `exo` by the standard install script.

## Authentication

Google Drive access requires OAuth2 authorization. Authorization is a **separate step** from syncing — run it once per machine using the `exo auth` command.

### Device Flow

`exo auth` always uses Google's device flow (TV/Limited Input), which works in any environment — interactive terminals, remote servers, or CI runners — without requiring a browser on the same machine:

```bash
exo auth my-drive
```

```
To authorize exo to access Google Drive:
  1. Go to: https://www.google.com/device
  2. Enter code: ABCD-EFGH

Waiting for authorization... ✅

✅ Authorized. Token saved to ~/.config/exo/drive-tokens/my-drive.json
   You can now run: exo sync --with my-drive
```

Open the URL on any device (phone, laptop, etc.), enter the code, and grant access. The CLI polls until consent is complete.

To re-authorize a remote that is already authorized (e.g., to switch accounts), use `--force`:

```bash
exo auth --force my-drive
```

> **Note:** `exo sync --with <remote>` fails immediately if no token exists for the remote. It does **not** trigger authorization inline. Always run `exo auth <remote-name>` first.

### Token Storage and Renewal

Tokens are stored in `~/.config/exo/drive-tokens/<remote-name>.json` (file permissions `0600`). Subsequent syncs are fully automatic — the CLI refreshes the access token using the stored refresh token without prompting again.

If the refresh token is revoked (e.g., you disconnected the app in your Google account settings), `exo` automatically re-triggers the consent flow the next time you sync:

- In an interactive terminal: opens a browser tab for the Google consent screen
- On a headless server or CI: prints a device code URL to enter on another device

You do not need to run `exo auth` again manually — just re-run `exo sync --with <remote-name>` and follow the prompt.

## Using the Remote

### Sync (upload + export)

```bash
exo sync --with my-drive
```

This exports files matching the remote's preferred content rules (from `include`/`exclude`) to Drive.

### Export to Drive

```bash
exo export --to my-drive
```

Exports all tracked files to the Drive folder with their original directory layout.

### Export specific paths

```bash
exo export --to my-drive --path data/processed/
```

### Export a specific version

```bash
exo export --to my-drive --ref v1.0.0
```

## `drive.file` Scope

The `exo` Drive remote uses the `drive.file` OAuth2 scope **only**. This means:

- The app can **only** access files and folders it created itself
- It cannot see other users' files or files created by other apps
- Querying with parent `"root"` lists the app's own files at root level

This is a permanent constraint — there is no configuration option to grant broader Drive access.

## `drive_path` Constraints

The `drive_path` in your `.exohub/remotes` configuration specifies the target folder path in Drive.

**The folder must not already exist before the first sync.** Because `exo` uses the `drive.file` scope, it can only see folders it created itself. If you point `drive_path` at a pre-existing folder, `exo` cannot locate it and will create a new folder with the same name alongside it — leading to duplicate folders and confusion.

Choose a path that does not yet exist, and `exo` will create the full folder hierarchy on first sync:

```yaml
# ✅ Good: folder does not exist yet — exo will create it
drive_path: /My Drive/datasets/gwasdb-2024

# ❌ Bad: folder already exists and was not created by exo
drive_path: /My Drive/datasets/shared-folder
```

Due to the `drive.file` scope, the app can only access folders it created. This constraint is intentional — it ensures `exo` can never read or modify files unrelated to the dataset.

## `include` / `exclude` Patterns

Patterns in `.exohub/remotes` are enforced at the **remote binary level**, not just via git-annex preferred content. This means both plain git files (like `.exohub/` metadata) and annexed files respect the patterns.

Patterns use glob syntax:

- `seekers/**` matches all files under the `seekers/` directory
- Trailing-slash patterns like `.exohub/` are automatically normalized to `.exohub/**`

## Large Files

Files larger than **5 MB** are uploaded using Google's resumable upload protocol automatically. This is transparent — no configuration is needed. Resumable uploads are more reliable over unstable connections because each chunk is retried independently.

For smaller files (under 5 MB), `exo` uses a single-part multipart upload.

## Limitations

| Limitation | Details |
|------------|---------|
| Export only (no import) | Files can be pushed to Drive but not pulled back in v1 |
| No Shared Drives | Google Workspace Shared Drives (Team Drives) are not supported in v1; use a personal My Drive folder |
| Drive API rate limits | 12,000 requests per 100 seconds; large exports with many small files may be throttled — `exo` retries automatically with backoff |
| Files are permanently deleted | `exo` deletes exported files via the Drive API (`REMOVEEXPORT`); deleted files do not go to Drive Trash |

## OAuth2 Client Credentials

The `exo` binary includes a built-in Google OAuth2 client ID for the `drive.file` scope. This is a Desktop/TV application credential — the same pattern used by tools like `gcloud`, `rclone`, and `gh`.

> **Security note:** For Desktop/TV OAuth2 clients, the client secret embedded in the binary is not a true secret. It allows initiating an OAuth2 flow, but access always requires explicit user consent. Combined with `drive.file` scope, even a leaked credential can only access files created by the app.

All `exo` users share the same Google Cloud project quota (12,000 queries/100s). For teams with high-volume exports, you can supply your own credentials via environment variables:

```bash
export GOOGLE_CLIENT_ID=your-client-id.apps.googleusercontent.com
export GOOGLE_CLIENT_SECRET=your-client-secret
exo sync --with my-drive
```

## Complete Workflow Example

```bash
# 1. Add Drive remote to .exohub/remotes (see Configuration above)

# 2. Initialize
exo init
# ⚠️  Drive remote 'my-drive' needs authorization. Run: exo auth my-drive

# 3. Authorize (one-time, per machine)
exo auth my-drive
# Follow device flow: visit https://www.google.com/device, enter code

# 4. Sync
exo sync --with my-drive

# 5. Check what was exported
exo locate data/

# 6. Subsequent syncs are automatic (token refreshed silently)
exo sync --with my-drive
```

## For Collaborators

When someone clones a repository that has a Drive remote configured:

1. Clone and initialize as usual:
   ```bash
   exo clone <repo-url>
   cd <repo>
   exo init
   # ⚠️  Drive remote 'my-drive' needs authorization. Run: exo auth my-drive
   ```

2. Run `exo auth` to authorize their own Google account:
   ```bash
   exo auth my-drive
   ```

3. Sync normally:
   ```bash
   exo sync --with my-drive
   ```

Each user authorizes independently using their own Google account. Tokens are stored per-machine in `~/.config/exo/drive-tokens/` and are never committed to the repository.

## Next Steps

- [Syncing Data]({{< ref "syncing" >}}) — full sync, export, and broadcast workflows
- [Working with Repositories]({{< ref "repositories" >}}) — configuration files and remote types overview
