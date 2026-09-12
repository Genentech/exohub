# Google Drive Remote

## What It Does

The Drive remote exports your annexed files to a Google Drive folder, making them accessible to collaborators who don't use exo. Files appear in Drive with their real names and directory structure — no cryptic hashes.

**This is an export-only remote.** Files you push appear in Drive exactly as they live in your repository tree. Collaborators can download them directly from the Drive web UI or via the Drive API.

> **v1 limitation:** Import (`exo import` / `importtree`) is not supported in this version. Drive is a push-only destination.

---

## Configuration

### Using `exo init remote`

The fastest way to add a Drive remote is via the interactive CLI:

```bash
exo init remote --type drive --name my-drive
```

This prompts for `drive_path` and `tracking_branch`, runs `git annex initremote`, and writes the entry to `.exohub/remotes` automatically.

### Manual configuration

Alternatively, add the remote to `.exohub/remotes` directly:

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
| `name` | ✅ | Name used in `exo sync --with <name>` |
| `type` | ✅ | Must be `drive` |
| `drive_path` | ✅ | Destination path in your Drive (see [Folder Constraint](#drive_path-constraint)) |
| `tracking_branch` | — | Prevents accidental exports from the wrong branch |
| `include` | — | Glob patterns — only matching files are exported |

---

## First-Time Authorization

The first time you sync with a Drive remote, exo needs access to your Google account. It only requests the `drive.file` scope (see [Scope Details](#drivefile-scope) below).

### Browser flow (TTY)

If your terminal is interactive, exo opens a browser tab for the Google consent screen and completes the flow automatically:

```
$ exo sync --with my-drive

Opening browser for Google Drive authorization...
If the browser does not open, visit:
  https://accounts.google.com/o/oauth2/v2/auth?...

✓ Authorization complete. Token stored at ~/.config/exo/drive-tokens/my-drive.json
```

After you grant access in the browser, exo continues with the export.

### Device flow (headless / no TTY)

On servers or CI where there is no interactive terminal, exo prints a URL and a short code instead:

```
$ exo sync --with my-drive

To authorize exo to access Google Drive:
  1. Go to: https://google.com/device
  2. Enter code: WXYZ-1234
```

Open the URL on any device, enter the code, and grant access. Exo polls until the authorization is complete (timeout: 30 minutes), then proceeds with the export.

### Token storage

Tokens are stored at `~/.config/exo/drive-tokens/<remote-name>.json`. The refresh token is persisted, so you only need to authorize once per machine. If the token is ever revoked (e.g., you remove app access from your Google account), exo automatically re-triggers the consent flow on the next sync.

---

## `drive.file` Scope

Exo requests the narrowest possible Google OAuth scope: **`drive.file`**.

This means:
- Exo can **only** see files and folders **that it created** — nothing else in your Drive.
- It **cannot** read, modify, or delete any pre-existing files or folders.
- Other apps and Drive contents remain completely invisible to exo.

This is the principal reason for the [folder constraint](#drive_path-constraint) below, and also why the scope is safe to grant even on a personal account.

---

## `drive_path` Constraint

The folder specified in `drive_path` **must not already exist** in your Drive before the first sync.

Because exo uses the `drive.file` scope, it can only interact with folders it created. If you point `drive_path` at a pre-existing folder, exo will not be able to locate it and will create a new folder with the same name alongside it, leading to confusion.

**Correct approach:**

1. Choose a `drive_path` that does not yet exist.
2. Run `exo sync --with my-drive` — exo creates the full folder hierarchy.

```yaml
# ✅ Good: folder does not exist yet
drive_path: /My Drive/datasets/gwasdb-2024

# ❌ Bad: folder already exists and was not created by exo
drive_path: /My Drive/datasets/shared-folder
```

---

## Custom OAuth Credentials

By default, exo uses a shared built-in OAuth client, which counts toward a project-wide quota. To use your own Google Cloud project and quota, set these environment variables before running any `exo` command:

```bash
export GOOGLE_CLIENT_ID="your-client-id.apps.googleusercontent.com"
export GOOGLE_CLIENT_SECRET="your-client-secret"
```

To set this up:

1. Go to [Google Cloud Console](https://console.cloud.google.com/) → **APIs & Services** → **Credentials**.
2. Create an **OAuth 2.0 Client ID** (Desktop application).
3. Enable the **Google Drive API** for your project.
4. Copy the client ID and secret into the environment variables above.

---

## Limitations

### Drive API Rate Limits

Google Drive API enforces **12,000 queries per 100 seconds per project** (shared across all users of the same OAuth client). Exo uses exponential backoff with jitter (up to 7 retries) to handle transient rate-limit errors (HTTP 403/429) automatically.

If you hit sustained rate limits on the shared client, [use your own credentials](#custom-oauth-credentials).

### No Import in v1

Import (`importtree`) is not supported. The Drive remote is export-only in this version. You cannot pull changes made directly in Drive back into your repository with `exo import`.

### No Google Shared Drives (Team Drives)

Only personal "My Drive" folders are supported in v1. Exporting to Shared Drives (Team Drives) is not available.

### Cannot Export to Pre-Existing Folders

Due to the `drive.file` scope, exo cannot locate or write to folders it did not create. The `drive_path` must point to a new location. See [`drive_path` Constraint](#drive_path-constraint).

### Large Files Use Resumable Upload

Files larger than **5 MB** are automatically uploaded using Google's resumable upload protocol. This is transparent — you don't need to configure anything — but it means large uploads are multi-step and may be slower than a single-request upload. The resumable protocol is more reliable over unstable connections.

---

## Example Workflow

```bash
# Configure the remote in .exohub/remotes (see Configuration above)

# First sync — triggers browser auth, then exports matching files
exo sync --with my-drive

# Subsequent syncs — token is reused, no browser needed
exo sync --with my-drive

# Or sync all configured remotes at once
exo sync
```

Files appear in Drive at the path you specified, mirroring the repository structure under your `tracking_branch`.
