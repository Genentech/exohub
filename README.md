# Exo CLI

`exo` is a CLI that orchestrates `git-annex` operations.

`exo` streamlines working with large, versioned datasets by managing `git-annex`
repositories and remotes (S3, exospace/rsync, and more) through a declarative
configuration and simple, composable commands.

## About Exohub

Exohub is a platform for managing and sharing large scientific datasets. It builds
on [`git-annex`](https://git-annex.branchable.com/) to keep large files versioned
alongside a normal Git history, while the actual file contents live in one or more
backing stores (S3 buckets, rsync/`exospace` shares, and other git-annex remotes).
This lets teams track, distribute, and reproduce datasets without checking terabytes
of data into Git itself.

Within Exohub, datasets are described declaratively:

- A repository holds the dataset's Git history and git-annex metadata (which files
  exist, their sizes, and where their content is stored).
- Remotes describe the physical locations where file content is kept and moved
  between (for example, an S3 archive, an S3 export for consumers, or a shared
  filesystem).
- Content and metadata can be synced, exported, and broadcast between remotes so
  that collaborators can fetch exactly the data they need.

`exo` is the command-line interface to these workflows. It orchestrates the
underlying `git-annex` operations behind a small set of high-level commands
(`init`, `pull`, `sync`, `copy`, `export`) and a declarative configuration stored
in the repository under `.exohub/`. Authentication to Exohub-backed storage is
handled via `exo login`.

> **Note:** `exo` can be used against any `git-annex`-compatible remotes. Some
> features (such as authentication and credential provisioning) integrate with
> hosted Exohub services; those parts are optional and clearly marked in the docs
> below.

## Install

From the repo root:

```
./scripts/install.sh [version]
```

If `version` is omitted, the installer resolves the latest published version from the configured artifact server (`EXOHUB_ARTIFACT_URL`).

## Requirements

CLI prerequisites:

- `git-annex`
- `jq`
- `yq`
- `rsync`
- `s5cmd`

Environment variables:

- `EXOHUB_JOBS`: parallel jobs for git-annex (default: `1`)
- `EXOHUB_NO_PROGRESS_LIMIT`: consecutive no-progress attempts before stopping (default: `3`)
- `EXO_OIDC_CLIENT_ID`: override OIDC client ID for authentication (optional, for testing)
- `AWS_PROFILE`: AWS profile name for storing credentials (default: `default`)
- `AWS_SHARED_CREDENTIALS_FILE`: custom path for AWS credentials file (default: `~/.aws/credentials`)

## Commands

### Authentication

Authenticate via OIDC device flow:

```bash
# Log in with device flow
exo login

# Force re-authentication
exo login --force

# Log out (clear cached credentials)
exo logout
```

**Credentials Storage:**

The login command stores credentials in two locations:

1. **OAuth2 tokens** (for exo CLI authentication):
   - **Linux**: `~/.config/exo/credentials/`
   - **macOS**: `~/Library/Application Support/exo/credentials/`
   - **Windows**: `%APPDATA%\exo\credentials\`

2. **AWS credentials** (for s5cmd and AWS CLI):
   - Stored in `~/.aws/credentials` (or `$AWS_SHARED_CREDENTIALS_FILE` if set)
   - Uses profile from `$AWS_PROFILE` environment variable (default: `default`)
   - Format compatible with AWS CLI and s5cmd

**Note**: The `login` command uses the OIDC device flow, which allows you to authenticate on any device with a browser. A QR code is displayed for easy mobile authentication. AWS credentials are automatically fetched and stored for use with s5cmd.

### Version

Check CLI version:

```
exo version
```

Initialize repositories and remotes:

```
# Initialize repository (prompts to create .exohub/context if missing)
exo init

# Create git-annex remotes declaratively
exo init remote --type annex --name s3-backup --s3url s3://bucket/prefix/_annex
exo init remote --type export --name s3-export --s3url s3://bucket/prefix/_export
exo init remote --type exospace --name exospace --rsyncurl /mnt/shared

# Interactive TUI mode
exo init remote
```

The `.exohub/context` file (YAML format) in your project directory is used by `exo init`. If the file is missing, `exo init` will prompt you to create it.

```yaml
host: https://git.example.com/
org: myteam
```

The `.exohub/remotes` file (YAML format) stores git-annex remote configurations:

```yaml
remotes:
  - name: s3-backup
    type: annex
    s3url: s3://my-bucket/backup
    chunk: 1GiB
  - name: s3-export
    type: export
    s3url: s3://my-bucket/export
    tracking_branch: main
  - name: exospace-data
    type: exospace
    rsyncurl: rsync://host/path
```

When you run `exo init` in a non-git directory with `.exohub/context`, it will create the repository (placeholder for now). When you run it in an existing git repo, it:
- Validates remote configurations in `.exohub/remotes` (checks for invalid parameters like chunk on export remotes)
- Checks that remotes exist and match the expected type (annex vs export vs exospace)
- Detects configuration drift and attempts to reconfigure remotes automatically
- Reports orphaned remotes (exist in git-annex but not in `.exohub/remotes`)

Note: Chunk size is an advanced parameter for annex remotes (defaults to 1GiB). Configure it in `.exohub/remotes` only if needed.

Pull a repo to a specific ref:

```
exo pull --url https://github.com/example/repo.git --ref main
```

Export a ref from one annex remote to another:

```
exo export --from source-remote --to dest-remote --ref main
```

Copy annexed content between remotes:

```
# Use wanted rules on destination
exo copy --from source-remote --to dest-remote --auto

# Operate on a given ref without checkout (implies --auto)
exo copy --from source-remote --to dest-remote --ref main
```

Sync content and metadata:

```
# All remotes
exo sync

# Specific remotes
exo sync --with remoteA --with remoteB

# Restrict content sync by paths/globs (repeatable flag form)
exo sync --path 'folder-*' --path data/subdir
```

Note: `--path` accepts one value per flag; pass it multiple times for multiple paths.

## Troubleshooting

### Authentication Issues

**Problem**: `exo login` times out

**Solution**: 
- The device flow has a 2-minute timeout. Make sure to complete authentication promptly.
- Check your network connection to your OIDC provider (configured via `EXO_OIDC_ISSUER`)
- Try again with `exo login`

**Problem**: "Authentication failed" error

**Solution**:
- Ensure `EXO_OIDC_ISSUER`, `EXO_OIDC_CLIENT_ID`, and `EXO_AWS_ROLE_ARN` are configured (see `docs/authentication.md`)
- Verify network access to your OIDC provider
- Check your identity provider documentation

**Problem**: Want to switch accounts

**Solution**:
```bash
exo login --force
```

### Credential Storage

Credentials are stored in two locations:

**OAuth2 Tokens** (for exo CLI):
- **Linux**: `~/.config/exo/credentials/<hash>`
- **macOS**: `~/Library/Application Support/exo/credentials/<hash>`
- **Windows**: `%APPDATA%\exo\credentials\<hash>`

**AWS Credentials** (for s5cmd and AWS CLI):
- **Location**: `~/.aws/credentials` (respects `$AWS_SHARED_CREDENTIALS_FILE`)
- **Profile**: From `$AWS_PROFILE` environment variable (default: `default`)

To manually clear credentials:
```bash
# Clear OAuth2 tokens
# Linux/macOS
rm -rf ~/.config/exo/credentials/  # or ~/Library/Application Support/exo/credentials/

# Windows
rmdir /s %APPDATA%\exo\credentials\

# Clear AWS credentials
rm ~/.aws/credentials

# Or use the logout command (clears OAuth2 only)
exo logout
```

## License

This project is licensed under the terms of the MIT License. See [LICENSE](LICENSE)
for details.

Copyright (c) 2026 Genentech, Inc.
