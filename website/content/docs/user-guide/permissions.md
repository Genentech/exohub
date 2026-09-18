---
title: "Permissions & Access Control"
description: "Manage who can read and write data using S3 Access Grants"
weight: 4
---

ExoHub uses **S3 Access Grants** to control who can read and write data in your repositories. When a remote has `grants: true`, `exo init` provisions scoped, temporary AWS credentials through the `exo-credential-helper` — replacing long-lived IAM keys with short-lived grants tied to specific users and paths.

## Git Permissions vs. Data Permissions

ExoHub repositories have two independent permission layers:

- **Git repository permissions** (managed in GitLab/GitHub) control who can clone, push, and pull the repository — including code, metadata, and annex pointers.
- **`.exohub/permissions`** controls who can read and write the **actual data** stored in S3 via Access Grants.

These are not synchronized: a user with Git access can see annex pointers without being able to download the data, and vice versa. Both must be configured independently.

When grants are not enabled on a remote, data access is determined by bucket-level policies with no per-repository granularity.

## Enabling Grants

To enable grant-based access control on a remote, set `grants: true` in `.exohub/remotes`:

```yaml
remotes:
  - name: s3-data
    type: annex
    url: s3://my-bucket/datasets/my-dataset
    grants: true
```

When you run `exo init`, the CLI:

1. Reads `.exohub/permissions` (or creates a default if it doesn't exist)
2. Calls the ExoHub API to provision S3 Access Grants matching the desired permissions
3. Configures the `exo-credential-helper` to obtain temporary, grant-scoped AWS credentials

> **Note:** The `grants` setting applies to `annex`, `export`, and `import` remote types only. Exospace remotes use a separate permission model (`public`, `group`, `private`).

## The `.exohub/permissions` File

This file controls who gets access and at what level. It lives at `.exohub/permissions` in your repository and is auto-created by `exo init` with the current user as owner.

```yaml
owners:
  - owner-username
  # - collaborator-username

# Users with read-only access
viewers: []

read_access: viewers
write_access: owners
```

### Fields

| Field | Description | Values |
|-------|-------------|--------|
| `owners` | UnixIDs with full access and permission management | _(required, at least one)_ |
| `viewers` | UnixIDs with read-only access | _(optional)_ |
| `read_access` | Who gets read grants | `viewers`, `owners`, `authenticated`, `public`, `none` |
| `write_access` | Who gets write grants | `owners`, `viewers`, `public`, `none` |

**`read_access` values:**

- `viewers` — grant READ to both viewers and owners _(default)_
- `owners` — grant READ to owners only
- `authenticated` — any logged-in ExoHub user can read, without being listed in `viewers`; anonymous users cannot
- `public` — open to everyone, including anonymous users browsing the ExoHub catalog; downloading raw files via `exo get` / `exo sync` still requires `exo login` (S3 access is never anonymous)
- `none` — no read grants

**`write_access` values:**

- `owners` — grant READWRITE to owners only _(default)_
- `viewers` — grant READWRITE to both owners and viewers
- `public` — anyone can write
- `none` — no write grants

### Grant Matrix

| `read_access` | `write_access` | Owners get | Viewers get |
|----------------|----------------|------------|-------------|
| `viewers` | `owners` | READWRITE | READ |
| `viewers` | `viewers` | READWRITE | READWRITE |
| `owners` | `owners` | READWRITE | _(none)_ |
| `none` | `none` | _(none)_ | _(none)_ |

> **Key distinction:** `public` datasets are visible and downloadable by anonymous users through the ExoHub catalog. `authenticated` datasets require an `exo login` session — anonymous users are denied. In both cases, fetching raw files over S3 (via `exo get` or `exo sync`) requires `exo login`, because S3 access grants cannot be issued without an identity.

The default configuration (`read_access: default`, `write_access: owners`) means read access depends on how the bucket policy integrates with {{< exohub "authName" >}}, while only owners can write through grants.

## Common Workflows

### Adding a Viewer

Edit `.exohub/permissions` to add a user to the `viewers` list, set `read_access` to `viewers`, then re-run `exo init`:

```yaml
owners:
  - alice
viewers:
  - bob
read_access: viewers
write_access: owners
```

```bash
exo init
# Grants synced for s3://my-bucket/datasets/my-dataset
```

Commit the updated permissions file so the change is tracked in version control.

Viewers with read-only grants can `exo sync` to download data but cannot upload or export. During `exo init`, export remotes are automatically skipped for users without write access:

```
Skipping export remote 's3-export' (read-only access)
```

### Restricting Write Access

To ensure only owners can write (the default):

```yaml
write_access: owners
```

To allow both owners and viewers to write:

```yaml
write_access: viewers
```

### Transferring Ownership

Add the new owner to the `owners` list, run `exo init`, then (optionally) remove yourself:

```yaml
owners:
  - alice
  - bob    # new owner
```

> **Important:** `exo init` warns if you are not listed as an owner before provisioning grants. You can use `--yes` to force past this check (e.g., for service accounts). If you proceed without being an owner, the grants API will still reject the request with a 403 error.

## Drift Detection

On subsequent runs, `exo init` automatically checks whether the actual S3 Access Grants match the desired state in `.exohub/permissions`. If drift is detected, you'll see output like:

```
Grants drift: 2 missing grant(s)
Grants drift: 1 extra grant(s)
Grants synced for s3://my-bucket/datasets/my-dataset
```

This means grants that should exist but don't are created, and grants that shouldn't exist are removed — keeping the actual state in sync with the permissions file.

If the registry file (`_grants.json`) stored on S3 also differs from the desired state, you'll see an additional message:

```
Grants drift: registry (_grants.json) differs — 2 field(s) changed
  alice: read
  bob: write
```

This is surfaced automatically during `exo init` — no extra flags are needed.

## Permissions Upload

When syncing to catalog remotes, `exo sync` automatically uploads `.exohub/permissions` as `permissions.json` to the remote's S3 location. This makes the permissions file available to the catalog system for access control verification.

## Permissions at Scale

For managing permissions across many repositories, use a permissions manifest:

```bash
exo manifest init --type permissions > permissions-manifest.yaml
```

This generates a manifest template that can be submitted as a job to reconcile grants across all repositories listed in the manifest. See [Running Jobs at Scale]({{< ref "jobs-at-scale" >}}) for details on manifest submission and monitoring.

## Troubleshooting

### 403 — Permission Denied

```
Permission denied: you are not an owner of s3://my-bucket/datasets/my-dataset
Ask an existing owner to add you to .exohub/permissions
```

Only users listed in `owners` can modify grants. Ask an existing owner to add you.

### Grants Sync Warnings

If the ExoHub API is unreachable, `exo init` prints a warning but continues:

```
Warning: grants API unreachable: ...
```

This is non-fatal — your remotes will still work if bucket-level IAM policies allow access, but grant-scoped credentials won't be available until the API is reachable.

### Environment Variables

See [Environment Variables]({{< ref "environment-variables" >}}) — the grants-related variables are `EXOHUB_GRANTS_ACCOUNT_ID` and `EXOHUB_GRANTS_REGION`.
