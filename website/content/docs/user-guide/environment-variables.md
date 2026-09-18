---
title: "Environment Variables"
description: "All environment variables supported by the exo CLI"
weight: 14
---

This page lists all environment variables recognized by the `exo` CLI. Set them in your shell profile (`~/.bashrc` or `~/.zshrc`) or inline before a command.

## Configuration Root

| Variable | Description | Default |
|----------|-------------|---------|
| `EXO_CONFIG_DIR` | Root directory for all exo config, cache, and credentials. When set, all paths (contexts, credentials, SSH config, AWS credentials, drive tokens, credential-helper cache) are rooted under this directory instead of the system defaults. Equivalent to the `--config-dir` persistent flag. | _(none — system defaults)_ |

This is useful for running multiple isolated `exo` sessions on the same machine (e.g., CI/CD pipelines, container builds, or multi-user environments):

```bash
# Isolate all exo state under a per-job directory
export EXO_CONFIG_DIR=/tmp/exo-job-42
exo login
exo sync
```

Or use the `--config-dir` flag directly (it sets `EXO_CONFIG_DIR` for all subprocesses automatically):

```bash
exo --config-dir /tmp/exo-job-42 sync
```

Directory layout under `EXO_CONFIG_DIR`:

| Path | Contents |
|------|----------|
| `<root>/exo/` | Contexts, SSH config, drive tokens, credentials |
| `<root>/aws/credentials` | AWS shared credentials file |
| `<root>/cache/exo-credential-helper/` | S3 credential cache |

## Core

| Variable | Description | Default |
|----------|-------------|---------|
| `EXOHUB_CONTEXT` | Active context name (alternative to `--context` flag) | _(none)_ |
| `EXOHUB_JOBS` | Parallel jobs for sync, get, export, copy, fsck, and add operations | `1` |
| `EXOHUB_API_URL` | ExoHub API endpoint | _(none — must be set or provided by your ExoHub deployment)_ |
| `EXOHUB_GIT_HOST` | Default git host URL used by the built-in `default` context. Useful when deploying ExoHub against a custom GitLab or GitHub instance. | _(built-in on internal binary; none on OSS build)_ |
| `EXOHUB_CATALOG_URL` | ArtifactDB catalog URL for `exo atlas` and `exo download` | _(auto-resolved from context)_ |
| `EXOHUB_PROFILE` | Profile name to apply when running `exo init` in a new directory (equivalent to `--profile`; ignored if the current directory is already a git repository) | _(none)_ |
| `EXOHUB_TPL_<NAME>` | Expose an environment variable to profile templates as `.<NAME>` (top-level, e.g. `EXOHUB_TPL_BUCKET` → `.BUCKET`) | _(none)_ |
| `EXO_UPGRADE_URL` | URL of the upgrade script used by `exo upgrade`. Must use `https://`. Overrides the built-in default when set. The internal binary has a built-in default; the open-source build has none (`exo upgrade` fails if neither built-in nor env var is set). | _(built-in on internal binary; none on OSS build)_ |
| `EXOHUB_VERSION_CHECK_URL` | Override the version-update-check service URL used by `exo version`. The internal binary has a built-in default; the open-source build has none (update check is skipped unless set). | _(built-in on internal binary; none on OSS build)_ |



## Appearance

| Variable | Description | Default |
|----------|-------------|---------|
| `EXO_THEME` | Override the active color theme (e.g., `exohub`, `mono`) | _(from preferences file, or `exohub`)_ |
| `EXO_THEME_MODE` | Override dark/light mode (`auto`, `dark`, `light`) | `auto` _(auto-detects terminal background)_ |
| `EXOHUB_DISABLE_TIPS` | Unconditionally disable daily tips when set to `1`, `true`, or `yes`. Intended for CI/server environments. Takes precedence over `EXOHUB_TIPS` and preferences. Any other non-empty value is ignored. | _(unset)_ |
| `EXOHUB_TIPS` | Control daily tips display (`false` or `0` to disable). Checked only when `EXOHUB_DISABLE_TIPS` is not active. | _(from preferences `tips_enabled`, default enabled)_ |

## Authentication

| Variable | Description | Default |
|----------|-------------|---------|
| `EXO_OIDC_ISSUER` | OIDC issuer URL used by `exo doctor`'s network probe and by the generic/open-source build of the CLI for device flow login | _(none — internal binary uses built-in OIDC defaults)_ |
| `EXO_OIDC_CLIENT_ID` | OIDC client ID for the generic/open-source build | _(none — internal binary uses built-in defaults)_ |
| `EXO_OIDC_SCOPES` | Space-separated OIDC scopes for the generic/open-source build | `openid profile` |
| `EXO_OIDC_AUDIENCE` | OIDC audience for the generic/open-source build | _(none)_ |
| `EXO_AWS_ROLE_ARN` | IAM role ARN for `sts:AssumeRoleWithWebIdentity` in the generic/open-source build | _(none — internal binary uses built-in defaults)_ |
| `EXO_AWS_SESSION_DURATION` | STS session duration in seconds for the generic/open-source build | `3600` |
| `EXO_AWS_REGION` | AWS region for the STS call in the generic/open-source build | `us-east-1` |
| `EXOHUB_AWS_PROFILE` | AWS credentials profile used by exo | `exohub` |
| `EXO_NO_AWS_PROFILE_INJECT` | Set to `1` to prevent exo from injecting its own `AWS_PROFILE` into subcommands (git-annex, s5cmd, etc.). Use this when you want subcommands to use your own pre-set AWS credentials (`AWS_PROFILE`, `AWS_ACCESS_KEY_ID`, etc.) instead of exo's credentials. Note: if any AWS identity variable (`AWS_PROFILE`, `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, or `AWS_SESSION_TOKEN`) is already set in the environment, exo automatically skips injection even without this flag. | _(unset)_ |
| `EXOHUB_GIT_TOKEN` | Generic git authentication token (used if no provider-specific token is set) | _(none)_ |
| `EXOHUB_GITLAB_TOKEN` | GitLab-specific git token | _(none)_ |
| `EXOHUB_GITHUB_TOKEN` | GitHub-specific git token | _(none)_ |
| `EXOHUB_GITEA_TOKEN` | Gitea-specific git token | _(none)_ |
| `EXO_TOKEN_FILE` | Path to the OAuth token file (overrides `EXO_CONFIG_DIR`-derived path and system default) | `~/.config/exo/credentials/token.json` |

## S3 Access Grants

| Variable | Description | Default |
|----------|-------------|---------|
| `EXOHUB_GRANTS_ACCOUNT_ID` | AWS account ID for S3 Access Grants. Required on open-source builds (no built-in default); the internal binary has a built-in production value. When not set on an OSS build, `exo doctor`'s S3 Control network probe is skipped. | _(none — set by internal binary on internal builds)_ |
| `EXOHUB_GRANTS_REGION` | AWS region for S3 Access Grants. Required on open-source builds (no built-in default); the internal binary has a built-in production value. | _(none — set by internal binary on internal builds)_ |
| `EXO_CREDENTIAL_HELPER_BIN` | Path to the credential helper binary | `exo-credential-helper` |

## Debugging

| Variable | Description | Default |
|----------|-------------|---------|
| `EXOHUB_CLI_DEBUG` | Enable verbose HTTP logging and command echoing | _(unset)_ |
