---
title: "Sharing Data with Your Team"
description: "Learn how to control who can access your dataset's data using ExoHub permissions and S3 Access Grants"
---

This tutorial shows you how to share a dataset with a collaborator using ExoHub's access control system. You will configure `.exohub/permissions`, provision S3 Access Grants with `exo init`, verify a collaborator can sync the data, and then revoke their access.

It builds on the [Building a MNIST Dataset]({{< ref "tutorials/mnist" >}}) tutorial — you can use that repository or any ExoHub repository with `grants: true` enabled on its remotes.

## Prerequisites

- `exo` installed and authenticated (`exo login`)
- An ExoHub repository with at least one remote that has `grants: true`
- A collaborator's UnixID (we'll call them `bob` throughout)

> **Not familiar with Access Grants?** Read the [Permissions & Access Control]({{< ref "docs/user-guide/permissions" >}}) reference page for a full explanation of the two-layer permission model.

## Understanding the Two Permission Layers

ExoHub repositories have two independent permission layers:

1. **Git repository permissions** (managed in GitLab) — controls who can clone the repository, view metadata, and push commits. This is configured in GitLab's project settings under *Members*.

2. **Data permissions** (`.exohub/permissions`) — controls who can read and write the **actual data files** stored in S3, via short-lived S3 Access Grants. This is configured in your repository and applied by `exo init`.

These are independent: a user with Git access can see annex pointers but cannot download data without a grant. Both must be configured separately.

## 1. Enable Grants on Your Remote

Open `.exohub/remotes` and add `grants: true` to the remotes you want to protect:

```yaml
remotes:
  - name: s3-archive
    type: annex
    s3url: s3://exohub-sandbox-uat/tutorials/alice/mnist/_annex
    grants: true

  - name: s3-export
    type: export
    s3url: s3://exohub-sandbox-uat/tutorials/alice/mnist/_export
    grants: true
```

> If you're following the MNIST tutorial, replace `alice` with your UnixID.

## 2. Review the Permissions File

When you first ran `exo init`, it created `.exohub/permissions` with you as the only owner:

```yaml
owners:
  - alice

viewers: []

read_access: viewers
write_access: owners
```

This means:

- `alice` has full read/write access
- No one else has a grant yet

With `read_access: viewers`, grants are issued to both owners and viewers for reads. Since there are no viewers, only `alice` can access the data.

## 3. Add a Collaborator as a Viewer

Edit `.exohub/permissions` to add `bob` to the `viewers` list:

```yaml
owners:
  - alice
viewers:
  - bob
read_access: viewers
write_access: owners
```

This configuration will give:
- `alice` — read and write grants
- `bob` — read-only grant

Commit the change so it's tracked in version control:

```bash
git add .exohub/permissions
git commit -m "feat: grant bob read access to dataset"
```

## 4. Apply the Grants with `exo init`

Run `exo init` to provision the S3 Access Grants:

```bash
exo init
```

Expected output:

```
Initializing remote 's3-archive'...
Grants synced for s3://exohub-sandbox-uat/tutorials/alice/mnist/_annex
  + alice: READWRITE
  + bob: READ

Initializing remote 's3-export'...
Grants synced for s3://exohub-sandbox-uat/tutorials/alice/mnist/_export
  + alice: READWRITE
  + bob: READ
```

ExoHub reads `.exohub/permissions`, calls the grants API, and provisions short-lived scoped credentials for each listed user.

Push the permissions change so Bob can see the updated file:

```bash
git push
```

## 5. Verify Bob Can Sync the Data

Ask Bob to clone the repository and sync the data. On Bob's machine:

```bash
exo clone {{< exohub "cloneExample" >}}
cd mnist
exo init
exo sync
```

Bob's `exo init` output will show his read-only grant is active:

```
Initializing remote 's3-archive'...
Grants synced for s3://exohub-sandbox-uat/tutorials/alice/mnist/_annex
  alice: READWRITE
  bob: READ (you)

Initializing remote 's3-export'...
Skipping export remote 's3-export' (read-only access)
```

Export remotes are automatically skipped for users without write access.

After `exo sync`, Bob can download the data files:

```bash
ls data/processed/
# train.parquet  test.parquet
```

If Bob tries to push data, it will be rejected with a permission error:

```bash
exo sync --push
# Error: 403 Permission denied: write grant required for s3://exohub-sandbox-uat/tutorials/alice/mnist/_annex
```

## 6. Revoking Access

To revoke Bob's access, remove him from `.exohub/permissions`:

```yaml
owners:
  - alice
viewers: []
read_access: viewers
write_access: owners
```

Commit and run `exo init` again:

```bash
git add .exohub/permissions
git commit -m "feat: revoke bob's access to dataset"
exo init
git push
```

Expected output:

```
Initializing remote 's3-archive'...
Grants synced for s3://exohub-sandbox-uat/tutorials/alice/mnist/_annex
  + alice: READWRITE
  - bob: READ (removed)
```

ExoHub detects the drift between the current grants and the desired state, removes Bob's grant, and reports what changed.

> **Note:** Revoking a grant prevents Bob from obtaining *new* short-lived credentials. Any credentials he already has will expire on their own within a short TTL (typically minutes). There is no need to rotate keys manually.

After the next `git pull` and `exo init` on Bob's side, his credentials will no longer be issued:

```
Error: no grant found for user 'bob' on s3://exohub-sandbox-uat/tutorials/alice/mnist/_annex
Ask an owner to add you to .exohub/permissions
```

## What's Next

- **Groups and scale**: To manage permissions across many repositories at once, see [Running Jobs at Scale]({{< ref "docs/user-guide/jobs-at-scale" >}}).
- **Ownership transfer**: Add the new owner to `owners`, run `exo init`, then remove yourself. See [Permissions & Access Control]({{< ref "docs/user-guide/permissions" >}}) for the full ownership transfer workflow.
- **Exospace remotes**: Exospace remotes use a separate `public`/`group`/`private` model — see [Exospace]({{< ref "docs/components/exospace" >}}) for details.
