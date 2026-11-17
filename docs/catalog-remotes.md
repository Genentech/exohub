# Catalog Remotes: Publishing to an Atlas and an ArtifactDB Instance

A **catalog remote** (type `artifactdb`) publishes a repo's metadata to S3,
then notifies the ExoHub API so it gets indexed. Each catalog remote has a
**role**, and each role publishes one thing to one destination:

- `role: atlas` (the default when `role` is absent): publishes the bundle
  (`.exohub-bundle/`) to an **Atlas** (search catalog). This is a git-annex
  export special remote (`mode: export`, same as before).
- `role: instance`: publishes the git-tracked `.artifactdb/` directory of
  the published ref to its own bucket, then asks the ExoHub API to forward
  the ingest to an ArtifactDB instance. It is **not** a git-annex special
  remote — `exo init` never runs `initremote` for it, and it holds no
  annexed content.

`exo sync` and `exo publish` both drive catalog remotes the same way;
`exo publish` orchestrates `exo sync` as one step of its
sync → bundle → publish → broadcast workflow.

```yaml
# .exohub/remotes
- name: atlas
  type: artifactdb
  mode: export
  s3url: s3://my-bucket/my-prefix
  url: https://atlas.example.com/v1/main   # optional; absent = deployment default Atlas

- name: instance
  type: artifactdb
  role: instance
  url: https://artifactdb.example.com/v1/myproject
  s3url: s3://my-instance-bucket/my-project-id
  data_from: atlas                              # optional; inferred when there's exactly one role: atlas remote
  grants_account_id: rcp                        # optional; falls back to the build default
```

An artifactdb remote with no `role` (or `role: atlas`) is unchanged from
before except for the field name: `url` is what used to be `atlas_url`.
`instance_url` no longer exists — an ArtifactDB instance is now its own
remote entry with `role: instance`.

## `role: atlas` — publishing the bundle

By default, a `role: atlas` remote's bundle goes to the ExoHub deployment's
**default Atlas**. Set `url` on the remote to publish to a specific,
non-default Atlas instead — but only Atlas URLs the deployment has
allowlisted are accepted; everything else is refused. A `role: atlas` remote
**never** uploads `.artifactdb/`, regardless of whether one exists in the
repo.

`exo` always shows, per catalog remote, where it will publish before
anything happens — in `exo sync --dry-run`, `exo sync --dry-run --json`,
and again right before the real publish:

```
$ exo sync --with atlas --dry-run
Would publish to catalog remote 'atlas' via git-annex-remote-artifactdb-export
  api-url:      https://api.exohub.example.com
  s3url:        s3://my-bucket/my-prefix
  bundle → https://atlas.example.com/v1/main (url)
  grants:       false
```

When no `url` is configured, exo asks the API for the deployment's default
Atlas and visually flags the line, since publishing to the default Atlas
without an explicit `url` is the case most likely to surprise someone
expecting a specific destination:

```
  bundle → ⚠ default Atlas https://atlas.example.com/v1/default
```

If the API doesn't expose the default Atlas URL (older deployment), the line
falls back to:

```
  bundle → ⚠ default Atlas (server-chosen)
```

Once a real publish succeeds, exo also prints which Atlas actually received
the bundle (from the publish response).

## `role: instance` — publishing `.artifactdb/`

A `role: instance` remote publishes **only** the git-tracked `.artifactdb/`
directory of the published ref (read with `git archive`, never the working
tree directly) into its own bucket, then asks the ExoHub API to forward the
ingest to the instance. `.artifactdb/` can never reach an Atlas prefix.

Required fields:

- `url`: the ArtifactDB instance's HTTPS endpoint.
- `s3url`: must be exactly `s3://<bucket>/<project_id>` — one path segment,
  equal to the remote's project id, and the segment must not start with
  `_`. exo refuses anything else, before requesting credentials.

An instance remote delegates to one **Atlas** remote for the actual data:
`data_from: <name>` names it explicitly, or, when omitted, exo infers it as
the single `role: atlas` remote in `.exohub/remotes`. Zero or several
`role: atlas` remotes with no `data_from` is a refusal that lists the
candidates.

An instance remote takes no `mode` — it is refused if one is set.

### Excluding files from the instance upload

`exclude: [<glob>...]` on a `role: instance` remote keeps the matching
`.artifactdb/` files out of that instance's upload, while they still reach
the Atlas (an Atlas remote's own upload is unaffected by another remote's
`exclude`). Globs are matched against each file's path relative to the
`.artifactdb/` root using the exact same matcher as the existing `exclude:`
on import/annex remotes (`matchAnnexGlob`):

- a single `*` or `?` stays **within one path segment** — it never crosses
  a `/`;
- `**` crosses segments, matching zero or more path components;
- a pattern with no `**` is also tried against the file's **basename**
  alone, so e.g. `dataset_merged.json*` matches `dataset_merged.json.bak`
  at any depth (`dataset_merged.json.bak` and
  `sub/dataset_merged.json.bak` both match), without needing a leading
  `**/`.

Examples (path relative to `.artifactdb/`):

| glob                    | path                  | excluded? |
| ------------------------ | --------------------- | --------- |
| `dataset_merged.json*`  | `dataset_merged.json.bak` | yes |
| `dataset_merged.json*`  | `sub/dataset_merged.json.bak` | yes (basename fallback) |
| `meta/*`                | `meta/x.json`         | yes |
| `meta/*`                | `meta/a/b.json`       | **no** (`*` stays within `meta/`, doesn't reach `meta/a/`) |
| `meta/**`               | `meta/a/b.json`       | yes |
| `*.json`                | `a/b.json`            | yes (basename fallback) |

```yaml
- name: instance
  type: artifactdb
  role: instance
  url: https://artifactdb.example.com/v1/myproject
  s3url: s3://example-bucket/DS1
  exclude:
    - "dataset_merged.json*"
```

A file kept off the upload this way never appears in that publish's
`..complete` marker (`files`/`objects` only ever reflect what was actually
uploaded). The upload itself never passes these globs to `s5cmd` (which has
its own, different wildcard dialect): exo stages exactly the kept files
into a fresh directory first and syncs that, so what lands in S3 always
matches `matchAnnexGlob` exactly, never s5cmd's own interpretation of the
pattern.

If a file was uploaded by an earlier publish — before `exclude` was added,
or before the glob matched it — the next instance publish **self-heals**:
it deletes the matching key(s) from the instance's version prefix
(`<s3url>/<ref_name>/...`) so they stop being crawled. This runs for both
branch and tag publishes (a tag re-publish of the same commit still passes
tag immutability and still needs the cleanup), deletes nothing outside that
version prefix, and never touches `..complete`, `..publishing` or `..meta`.
A key that was never actually uploaded is not an error.

This self-heal only considers files that **currently exist locally** and
match an exclude glob — it does not scan the previous publish's full file
list for stale matches. A key that was published, then both excluded AND
deleted locally in the same change, is only cleaned up by the ordinary
branch re-publish stale-file cleanup (which diffs the previous marker's
file list against the current tree); on a **tag** re-publish of the same
commit there is no "current tree changed" signal to drive that cleanup, so
such a key is not removed. In practice this only matters if a file is
simultaneously excluded and deleted between two publishes of the exact same
tag/commit, which is not the common case; a branch re-publish, or a fresh
tag that never published the file, is unaffected.

### Where metadata lands

Locally, `.artifactdb/` is always the directory name. Remotely, the
destination folder is whatever the ArtifactDB instance's tenant declares as
its `meta_folder` (`GET <url>/` → `storage.clients[].s3.meta_folder`,
matched to the remote's `s3url` bucket):

- tenant declares a `meta_folder` (e.g. `.artifactdb`): uploaded to
  `<s3url>/<ref_name>/<meta_folder>/`.
- tenant declares none: uploaded to the version root,
  `<s3url>/<ref_name>/`.

exo discovers this at publish time; an unreachable instance, or no storage
client matching the bucket, is a refusal before any upload.

Project permissions (converted from `.exohub/permissions`, `scope: project`,
same validation as the Atlas gets) are uploaded to
`<s3url>/..meta/permissions.json` before the completion marker.

The **last** object written, only after a fully successful upload, is the
completion marker `<s3url>/<ref_name>/..complete` — a small JSON document
(`version`, `commit`, `objects`, `exo_version`, `source_repo`). A failed
upload never leaves a marker behind.

## Publish order

Per published ref, every `role: atlas` remote publishes first (upload +
`/api/publish`, 202 = accepted, not yet indexed). Only once a ref's
delegation Atlas has been accepted does its `role: instance` remote(s)
publish. If the Atlas publish fails, the instance is **skipped** with a
clear message and the command exits non-zero — it is never attempted
against stale or unpublished Atlas data.

## Publishing an older ref (`--ref`)

`exo publish --ref <tag|commit>` (repeatable) publishes that ref's own
content and metadata while routing (remotes, `s3url`, `role`, `url`,
`data_from`) comes from the CURRENT `.exohub/remotes`, never the ref's own
possibly-stale copy of that file — see `exo publish --help` for the full
ref-to-remotes and content-availability preconditions.

The bundle and `.artifactdb/` built for `--ref` always come from the ref's
own git tree, never the current working tree: `exo bundle --ref <ref>`
(which `exo publish --ref` runs internally) resolves the file list and
every annexed file's git-annex key by reading the ref's tree directly
(`git ls-tree`, `git cat-file`, and the ref's own symlink targets /
unlocked pointer file content) — never `git ls-files`/`git annex find`
against the checked-out worktree or index. Concretely, with HEAD ahead of
the published ref:

- A file added after the ref is **absent** from the published bundle.
- A file removed after the ref is **present** in the published bundle,
  with the ref's own content.
- A file modified after the ref — including an annexed file whose content
  (and therefore git-annex key) changed — is published with the **ref's**
  content and the **ref's** annex key, never HEAD's.

## Tag immutability (both roles)

Publishing a **tag** is refused when the remote already has a record (the
instance's `..complete`, the Atlas's `.exohub-bundle/bundle.json`) naming a
different commit than the tag now resolves to — a moved tag. The same
commit is a safe retry. No record at all means a first attempt, or a
previous attempt that never completed. Branches are overwritten freely, with
no such check. There is no flag to bypass this — not even `--force`:
republishing a released version under the same tag is an instance-side
admin action, not something exo will do for you.

## Source-repo guard (both roles)

Each published version records the repository it was published from (the
instance's `..complete` `source_repo`, the Atlas's `bundle.json`
`repo_url`). Publishing the same project/version from a **different**
repository — e.g. a fork that inherited `.exohub/remotes` — is refused
before any upload, with the fix:

```
refusing to publish: this version was already published from "<old repo>", but this is "<new repo>" — pass --force to publish anyway
```

`exo publish --force` (and `exo sync --force`, for a direct catalog sync)
passes `--force` through to the catalog binary, which bypasses the
source-repo guard **and** the ref-to-remotes check — but never tag
immutability. A legacy record with no recorded source repository doesn't
block (unknown, not a mismatch).

## Per-remote grants

`grants_account_id` on a remote selects the S3 Access Grants account used
for `GetDataAccess` credential requests for that remote; it falls back to
the build's default account when unset. Grants are scoped to the remote's
own `s3url`, so an instance remote served by a different account than its
Atlas just sets its own `grants_account_id`. `exo doctor` reports grants
health per remote, with the account it used.

An instance bucket has no permissions registry of its own, so a `role:
instance` remote's credential request (the server fallback, used when the
fast path `GetDataAccess` is unavailable) also carries the `s3url` of its
resolved delegation Atlas (`data_from`, or the single `role: atlas` remote).
The server authorizes the request against that source project's permissions
instead of the instance bucket. This happens automatically; nothing in
`.exohub/remotes` needs to change beyond `data_from` when it's ambiguous.

## Migrating from `atlas_url` / `instance_url`

**BREAKING:** `atlas_url` and `instance_url` are no longer accepted in
`.exohub/remotes`. exo refuses to run any command against a remotes file
that still has either key, before any upload or API call:

```
remote "atlas": atlas_url/instance_url are no longer accepted in .exohub/remotes; exo refuses before any upload or API call.
Rewrite it with the equivalent role-split configuration:
  - name: atlas
    type: artifactdb
    role: atlas
    url: https://atlas.example.com/v1/main
    s3url: s3://my-bucket/my-prefix
  - name: atlas-instance
    type: artifactdb
    role: instance
    url: https://artifactdb.example.com/v1/myproject
    s3url: s3://<bucket>/<project_id>
    data_from: atlas
Rewrite .exohub/remotes with: exo init --profile <profile> --remote <remote>
```

The error shows the role-split YAML to replace the old entry with — the
repo's actual profile-rendered remotes (real names, real instance bucket,
real `data_from`) when `.exohub/profile.lock` names one and it's
reachable, otherwise the generic example above — and the
`exo init --profile <profile> --remote <remote>` command that rewrites
`.exohub/remotes` for you. To migrate:

1. **Upgrade exo first.** Role-split remotes are only understood by the
   version of exo that ships this change; there is no fail-closed
   protection against an older exo, so upgrade exo (and anywhere else that
   publishes this repo, e.g. a CI runner or an automation) *before*
   rewriting `.exohub/remotes`.
2. Update the server profile to declare two remotes instead of one:
   `role: atlas` with the old `atlas_url` as `url`, and `role: instance`
   with the old `instance_url` as `url` and a project-scoped `s3url`.
3. Run `exo init --profile <profile> --remote <remote>` on the repo. This
   now performs the legacy→role-split conversion directly: it loads the
   legacy `.exohub/remotes` leniently, renders the profile's role-split
   remotes, shows the diff, and rewrites the file on confirmation (`--yes`
   to skip), keeping the atlas remote's git-annex UUID (same special
   remote) and never running `initremote` for the new instance entry. If
   the rendered `s3url` also moves prefix/bucket, add `--allow-move` to
   confirm the move is intentional; grants are synced automatically for a
   `grants: true` remote, with an explicit warning printed if a sync has
   to be skipped (e.g. not an owner — rerun as one, or with
   `--force-grants-sync`). `exo init --profile <profile> --remote
   <remote> --dry-run` (add `--json` for machine-readable output) previews
   the plan without changing anything. Hand-editing `.exohub/remotes`
   remains an option too.
4. Re-publish.

There is no compatibility layer and no override flag for the legacy-key
refusal itself — every other command still refuses outright. The
`--profile` migration path above is the only way to convert a legacy file,
and it is a one-time rewrite, not a deployment setting.

## Machine-readable output (stable contracts)

Both JSON shapes below are stable: field names, types, and null-vs-absent
behavior won't change without a version bump. A remote always appears with
all its keys present, using `null` (not an absent key) for an unset nullable
field.

### Dry-run target report: `exo sync --dry-run --json`

Catalog remotes appear in a separate `catalog_remotes` array, one entry per
remote, alongside the existing `remotes` array used for standard
annex/export/import remotes. This is a **clean break** from the
pre-role-split report: there is no `bundle_target`, `bundle_target_source`
or `instance_url` anywhere — every catalog remote, Atlas or instance, is
reported the same way, as one `catalog_remotes` entry with its `role`.

```json
{
  "$schema": "...",
  "remotes": [],
  "catalog_remotes": [
    {
      "remote": "atlas",
      "role": "atlas",
      "url": "https://atlas.example.com/v1/default",
      "url_source": "default",
      "s3_location": null,
      "data_from": null,
      "meta_folder": null
    },
    {
      "remote": "instance",
      "role": "instance",
      "url": "https://artifactdb.example.com/v1/myproject",
      "url_source": null,
      "s3_location": "s3://my-instance-bucket/my-project-id/v1.0.0/.artifactdb/",
      "data_from": "atlas",
      "meta_folder": ".artifactdb",
      "excluded": ["dataset_merged.json"]
    }
  ]
}
```

Every entry carries `remote`, `role`, `url`, `url_source`, `s3_location`,
`data_from` and `meta_folder` — always present, `null` (not an absent key)
when not applicable:

- `url`: for `role: atlas`, the configured `url`, or the resolved deployment
  default Atlas when the remote sets none (`null` if that live lookup
  failed — older deployment, no network). For `role: instance`, the
  configured instance URL.
- `url_source`: `role: atlas` only — `"explicit"` when the remote sets its
  own `url`, `"default"` whenever it doesn't, i.e. whenever the remote is
  meant to use the deployment's default Atlas — set independently of
  whether that live lookup actually resolved a URL, so a `role: atlas`
  entry with no own `url` is still distinguishable from a `role: instance`
  entry even when `url` ends up `null` for both (e.g. an older deployment,
  no network). Always `null` for `role: instance` (an instance's `url` is
  always explicit, by validation, so the distinction doesn't apply).
- `data_from`: `role: instance` only — the resolved delegation Atlas's
  remote name (explicit or inferred), `null` if it couldn't be resolved or
  for a `role: atlas` entry.
- `meta_folder` / `s3_location`: `role: instance` only — looked up live from
  the instance's tenant config; `null` for `role: atlas`, and for `role:
  instance` whenever the lookup fails (unreachable instance, no matching
  storage client).
- `excluded`: `role: instance` only, and only present when the remote has
  `exclude:` globs configured — the list of the ref's `.artifactdb/` files
  those globs would keep off the upload. An additive field: absent (not
  `null`) for a `role: atlas` entry or an instance remote with no
  `exclude:`.

Any consumer that read the pre-role-split `bundle_target`,
`bundle_target_source` or `instance_url` fields must switch to
`catalog_remotes[]`: a `role: atlas` entry's `url` (+ `url_source`)
replaces `bundle_target` (+ `bundle_target_source`); a `role: instance`
entry's own `url` replaces `instance_url`.

### Post-publish report: `exo sync --json` (without `--dry-run`)

For an explicit, all-catalog `--with` selection, `exo sync --json` (no
`--dry-run`) runs the real publish, role-ordered (every `role: atlas` remote
before any `role: instance` one), and prints a single JSON document:

```json
{
  "catalog_remotes": [ /* same shape as the dry-run report above */ ],
  "published": [
    {
      "remote": "atlas",
      "role": "atlas",
      "atlas_url_used": "https://atlas.example.com/v1/main",
      "s3_location": null,
      "published": true,
      "skipped": null
    },
    {
      "remote": "instance",
      "role": "instance",
      "atlas_url_used": null,
      "s3_location": "s3://my-instance-bucket/my-project-id/v1.0.0/.artifactdb/",
      "published": true,
      "skipped": null
    }
  ]
}
```

Every record always carries `remote`, `role`, `atlas_url_used`,
`s3_location`, `published` and `skipped` (as `null` when not applicable —
`atlas_url_used` for an instance role, `s3_location` for an atlas role that
never resolved a destination). On a refusal, `published: false` and an
`error` object replace the normal outcome; when an instance remote is
skipped because its delegation Atlas failed, `skipped` carries the reason
instead:

```json
{
  "published": [
    {
      "remote": "atlas",
      "role": "atlas",
      "atlas_url_used": null,
      "s3_location": null,
      "published": false,
      "skipped": null,
      "error": {
        "code": "atlas_url_not_allowlisted",
        "message": "atlas_url not allowed",
        "allowed": ["https://atlas.example.com/v1/a", "https://atlas.example.com/v1/b"]
      }
    },
    {
      "remote": "instance",
      "role": "instance",
      "atlas_url_used": null,
      "s3_location": null,
      "published": false,
      "skipped": "delegation Atlas 'atlas' failed: atlas_url not allowed"
    }
  ]
}
```

`error.code` and `error.message` are always present when `error` is present;
`error.allowed` is omitted when the server doesn't provide an allowlist.
`exo sync --json` exits non-zero when any remote's record has
`published: false` or a non-null `skipped`, after printing the report — the
same no-partial-state guarantee as the human-output path.

## When the API refuses a publish

The API checks a `role: atlas` remote's `url` against the deployment's Atlas
allowlist, and a `role: instance` remote's `url` against its ArtifactDB
instance allowlist, **before indexing anything**. If either isn't
allowlisted, it refuses the publish (HTTP 422) with a structured error code
exo maps to a specific fix:

| Code | Cause | Fix |
|------|-------|-----|
| `atlas_url_not_allowlisted` | a `role: atlas` remote's `url` isn't on the deployment's Atlas allowlist | update `url` in `.exohub/remotes` to an allowed value, then run `exo init` |
| `instance_url_not_allowlisted` | a `role: instance` remote's `url` isn't on the deployment's ArtifactDB instance allowlist | update `url` in `.exohub/remotes` to an allowed value, then run `exo init` |

exo reports either as a clear, non-zero-exit failure with the fix to apply
and the allowlisted values, for example:

```
❌  atlas_url not allowed — fix: update the catalog remote's url in .exohub/remotes to an allowed value, then run exo init (allowed: https://atlas.example.com/v1/a, https://atlas.example.com/v1/b)
⚠️  bundle uploaded to s3://my-bucket/my-prefix/v1.0.0/ but NOT published: API returned 422: ...
```

The Atlas bundle's files are still uploaded to S3 at that point (so a
subsequent successful publish can reuse them), but the publish itself did
not happen: nothing was indexed, and the command exits non-zero. In the
post-publish JSON report (see above), the same refusal appears as
`published: false, error: {code, message, allowed}`.

An unknown/future error code falls back to the server's own message
(no mapped fix text), so older exo versions still report *something*
readable against a newer server.

To fix it:
1. Change the remote's `url` in `.exohub/remotes` to one of the allowlisted
   values (ask the deployment owner which values are allowlisted), or
   remove it (on a `role: atlas` remote) to use the deployment's default
   Atlas instead.
2. Run `exo init` to re-apply the remote configuration.
3. Re-run `exo sync` / `exo publish`.

There is no override flag for this check — it's a deployment-level
allowlist, by design. To bypass it you must change the remote's
configuration.
