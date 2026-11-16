---
title: "Syncing Data"
description: "Download, upload, and export your data"
weight: 5
---

Syncing is how you transfer data between your local machine and remote storage. This section covers downloading data for analysis, uploading your changes, and exporting data for external access.

## Basic Sync

After cloning and initializing a repository, sync to download the data:

```bash
exo sync
```

This performs bidirectional synchronization: it downloads content from remotes and uploads locally available content to remotes. Depending on the dataset size, this may take a few minutes.

> `exo sync` transfers content in both directions but does not create git commits. Use `exo broadcast` after syncing to propagate metadata, and `git push` to share pointer changes.

```bash
# Before sync - symlinks point to missing content
ls -la data/
# train.parquet -> ../.git/annex/objects/... (broken symlink)

# After sync - symlinks resolve to actual files
ls -la data/
# train.parquet -> ../.git/annex/objects/... (valid symlink)

# Verify the data is there
file data/train.parquet
# data/train.parquet: Apache Parquet
```

## Downloading Specific Files with `exo get`

When you only need a few files — rather than syncing the entire repository — use `exo get` to cherry-pick exactly what you want:

```bash
# Get a single file
exo get data/train.parquet

# Get everything under a directory
exo get data/processed/

# Get several paths at once
exo get data/train.parquet data/test.parquet
```

This is lighter than `exo sync` (which synchronizes all content) and doesn't require writing a manifest. It also automatically resumes any previously interrupted downloads.

```bash
# Get from a specific remote
exo get --from s3-backup data/

# Get with 8 parallel jobs
exo get -J 8 data/

# Get all versions of all files
exo get --all
```

| Flag | Description | Default |
|------|-------------|---------|
| `--from <remote>` | Source annex remote | _(all remotes)_ |
| `--all` | Get all versions of all files | `false` |
| `-J, --jobs <N>` | Parallel download jobs | `$EXOHUB_JOBS` or `1` |

## Syncing with Specific Remotes

If you have multiple remotes configured, specify which one to sync with:

```bash
exo sync --with s3-archive
```

Sync with multiple remotes:

```bash
exo sync --with s3-archive --with backup-remote
```

## Read-Only Grants

If you have read-only access to a grants-enabled remote (e.g., you are a viewer), `exo sync` automatically detects this and syncs in download-only mode:

```bash
exo sync
# Syncing content from remote 's3-data' (read-only access)
```

No special flags are needed — the CLI probes your access level via the credential helper and adjusts accordingly.

## Syncing Specific Paths

For large datasets, you might only need a subset of the data. Use `--path` to sync specific directories or files:

```bash
# Sync only the processed data
exo sync --path data/processed/

# Sync multiple paths
exo sync --path data/processed/ --path notebooks/
```

Path patterns support wildcards:

```bash
# Sync all parquet files
exo sync --path '*.parquet'

# Sync a specific subdirectory pattern
exo sync --path 'data/2024-*/'
```

## Filtering with Patterns

For fine-grained control, use `--include` and `--exclude` patterns:

```bash
# Only sync CSV files
exo sync --include '*.csv'

# Sync everything except BAM files
exo sync --exclude '*.bam'

# Combine patterns
exo sync --include 'results/**/*.csv' --exclude '**/temp/*'
```

Pattern syntax:
- `*` — matches any characters except `/`
- `**` — matches any characters including `/`
- `?` — matches a single character

Examples:
- `*.bam` — all BAM files in the root
- `results/**/*.csv` — all CSV files anywhere under `results/`
- `data/2024-*/*.fastq.gz` — FASTQ files in 2024 date folders

## Dry Run

See what would be synced without actually transferring data:

```bash
exo sync --dry-run
```

The dry-run output shows:
- Files to be uploaded (`here -> <remote>`) and downloaded (`<remote> -> here`)
- **Unexport candidates** (export remotes only) — files that would be removed from the export remote because they were deleted from HEAD or no longer match its preferred-content rules

> **Note:** The real `exo sync` never drops local content, so no "drop candidates" section is shown.

Example output for a regular annex remote:

```
Dry-run plan for remote 's3-archive'
  here -> s3-archive (1.2 GiB)
    data/results/model.pkl
    data/results/predictions.parquet
  s3-archive -> here
    (none)
```

For export remotes, unexport candidates are also shown:

```
Dry-run plan for remote 's3-export'
  here -> s3-export (tracking branch: main)
    data/new-file.csv
  unexport from s3-export
    data/old-file.csv
```

### Branch Mismatch Warning (Export Remotes)

Export remotes are linked to a specific tracking branch. If your current branch differs from that tracking branch, the dry-run reports nothing to export and shows a warning:

```
Dry-run plan for remote 's3-export'
  ⚠️  WARNING: current branch 'feature/my-branch' differs from remote 's3-export' tracking branch 'main'
     Files on the current branch will NOT be exported until they are merged into 'main'.
  here -> s3-export (tracking branch: main)
    (none — current branch is not the tracking branch)
  unexport from s3-export
    (none — current branch is not the tracking branch)
```

This reflects the real behavior: `exo sync` only exports content from the tracking branch.

Add `--json` for machine-readable output:

```bash
exo sync --dry-run --json
```

## Default Incremental History Upload

A plain `exo sync` (no flags) doesn't just upload the current checkout's content anymore. For the current branch, it also uploads the content of every commit since the last successful upload to each remote — so a tag or commit whose content was replaced before it was ever synced doesn't get silently left behind, without you having to remember `exo sync --all`.

Progress is tracked per remote and branch with a local-only marker ref, `refs/exo/content-synced/<remote>/<branch>`. This marker is never a branch or tag, and is never pushed, fetched, or cloned. It advances only after that remote's upload succeeds, one commit at a time, so a failure partway through a long catch-up leaves already-uploaded commits marked and retries just the remainder next time. It also re-points itself at the new tip after a rebase or force-push, and falls back to the upstream tracking branch's tip (or full history, if there's no upstream either) the first time it runs.

This default pass only runs for a plain, unfiltered `exo sync`: it's skipped when `--path`, `--include`, `--exclude`, or `--bundle-dir` narrow what the invocation syncs, when `--no-content` is set, or when you pass `--skip-history-upload` to disable it for one run. A failure uploading history to one remote is reported as a warning and does not fail the overall `exo sync` — the current checkout's content already synced normally, and the incremental catch-up is retried next time.

### Previewing the Pending Catch-Up

`exo sync --dry-run` (and `--dry-run --json`) report what the default incremental pass *would* upload — per remote, the commit range and the file count of each pending commit — without uploading anything or touching the marker:

```
$ exo sync --dry-run
...
Dry-run plan for remote 'my-remote'
  ...
  pending history catch-up: 2 commit(s), 3 file(s) total
    3f1a9c2e1b7a: 1 file(s)
    9b7e2a0c44de: 2 file(s)
```

`pending_history_upload` is included in `--dry-run --json` output only when at least one remote has something pending.

> **A remote's markers reset when its location moves.** If `exo init` detects a real `s3url` change on a remote (see [s3url Changes Are Never Silent]({{< ref "repositories#s3url-changes-are-never-silent" >}})), its `refs/exo/content-synced/*` markers are pruned, since they claimed "uploaded through commit X" against a location the new one knows nothing about — the next `exo sync` re-baselines instead of trusting stale markers. The one exception is a verified `exo init --migrate-content` move: migration already copied and verified everything, so the markers are left alone.

## Checking Completeness Across History

Besides the current branch's incremental catch-up (above), a repository's full history across **every** branch and tag can still end up with annexed content on no remote at all — for example, importing several versions of a dataset chronologically on different branches, each tagged once. After importing several versions this way, run `exo sync --all` (then `exo sync --check` to confirm) instead of relying on the default incremental pass, which only follows the current branch.

### `exo sync --check`

Reports whether the content referenced by your branches and tags is actually safe, without running any sync:

```bash
exo sync --check                  # check every local branch and tag
exo sync --check --ref v1         # check only ref v1 (repeatable)
exo sync --check --ref v1 --ref v2
exo sync --check --json           # stable {refs_checked, items, summary} report
exo sync --check --with my-remote # only require content on my-remote
```

Every annexed file referenced by a checked ref is classified as:

- **ok** — present on at least one configured annex remote (omitted from `--json` output; only non-ok items are reported).
- **fixable** — present in this clone, but on no configured remote. Run `exo sync --all` to upload it (or check out that ref and run a plain `exo sync`).
- **lost** — not in this clone and on no configured remote. If another, unconfigured repository might still have a copy, it's listed under `seen_in`.

Exit codes are designed for automation to branch on without parsing `--json`:

| Exit code | Meaning |
|-----------|---------|
| `0` | Every item in the resolved ref set is `ok`. |
| `3` | At least one `fixable` item, no `lost` item. |
| `4` | At least one `lost` item (takes precedence over `fixable`). |

Add `--verify` to also check presence on each remote directly (`checkpresentkey`) instead of trusting the location log — this catches a remote whose storage moved without a UUID change, e.g. after a remote's `s3url` was moved (see [s3url Changes Are Never Silent]({{< ref "repositories#s3url-changes-are-never-silent" >}})):

```bash
exo sync --check --verify
```

### Previewing a Ref's Catalog Target

`exo sync --dry-run --json --ref <ref>` previews, for a catalog remote, the routing (`s3url`, `role`, `url`, `data_from`) that publishing `<ref>` would use right now — read fresh from the **current** `.exohub/remotes`, never from `<ref>`'s own checked-out copy — alongside `<ref>`'s own identity (`ref`, `ref_commit`). Each entry also carries `url_source`: `"explicit"` when the remote sets its own `url`, `"default"` when it doesn't (so a `role: atlas` remote correctly falling back to the deployment's default Atlas is distinguishable from one whose live lookup simply failed); `url_source` is always `null` for a `role: instance` remote, whose `url` is always explicit:

```bash
exo sync --dry-run --json --ref v1.0.0
exo sync --dry-run --json --ref v1.0.0 --ref v1.1.0
```

This requires at least one catalog remote (via `--with` or auto-discovery), and is how a guard can verify the publish target before `exo publish --ref` actually runs — a ref that predates a routing change (a bucket/prefix move, a new `role: atlas` url) still resolves to the current catalog, not a stale or missing one.

### `exo sync --all`

The explicit, full-cost uploader: pushes the annexed content of **every local branch and tag** to every writable, plain annex remote, respecting each remote's own preferred content expression. Catalog, import, export, readonly, and write-restricted remotes are skipped (they use other sync mechanisms).

```bash
exo sync --all                     # upload history to every writable remote
exo sync --all --dry-run --json    # preview without uploading
```

Use `--all` to repair `fixable` content reported by `--check`, or to seed a remote from an existing repository's full history. `--all` can only upload content that exists in this clone — if some content is `lost`, `--all` still uploads everything it can and reports the loss with the same exit code as `--check`.

### Recovering From a Force-Pushed Remote (`--reset-synced`)

If a content remote's `main` branch is rewritten (force-pushed) after this clone last synced with it, the local git-annex `synced/main` branch no longer shares history with the remote's new `main`, and a plain merge would hit a confusing conflict. `exo sync` and `exo broadcast` (and `exo publish`'s steps 1 and 4, which call them) no longer check for this up front — doing so used to misfire on a brand-new repo with only unpushed local commits. Instead, each records what it last observed on the remote's `main` before running, runs the sync, and only if it **fails** checks whether that failure was actually caused by a real rewrite (comparing against tips observed on the remote's `main` — its pre-fetch tip and remote-tracking reflog, never local `synced/main`). If so, it appends a hint to the original error instead of leaving you with a bare git-annex conflict:

```
Sync failed: ...
remote s3-archive rewrote main (force-push) since this clone last synced, which is why the sync failed: its synced/main refs still hold the old history. Run `exo sync --reset-synced s3-archive` (it previews first; add --yes to apply) after checking your local work is safe
```

If the failed sync left a merge in progress, exo aborts it automatically and says so (`exo aborted the half-done merge the sync left behind; your working tree is back to its state before the sync.`) — or tells you to run `git merge --abort` yourself if it couldn't.

Fix the rewrite by resetting the local `synced/main` branch to match the remote's rewritten history:

```bash
exo sync --reset-synced s3-archive     # preview the reset
exo sync --reset-synced s3-archive --yes   # perform it
```

`--reset-synced` cannot be combined with `--check`, `--all`, `--dry-run`, `--json`, `--with`, or `--bundle-dir`, and `--yes` only has an effect alongside `--reset-synced`.

Two safety checks happen before anything changes:

- If local `main` has commits the remote doesn't have in either its current or pre-rewrite history, the reset always refuses — regardless of `--yes` — and lists the unpushed commits so nothing is silently discarded.
- If local `main` holds **only** history the remote rewrote away (no work at risk), the command instead points you at adopting the new history yourself (e.g. `git fetch s3-archive && git reset --hard s3-archive/main`, after confirming that's safe) and re-running.

Once neither case applies, `--reset-synced` only ever touches `synced/main` (and, if needed, the remote's own stale `synced/main`) — never `main` or any other branch.

## Parallel Jobs

Speed up syncing by running multiple transfers in parallel:

```bash
exo sync -J 4
```

Or set it globally via environment variable:

```bash
export EXOHUB_JOBS=4
exo sync
```

The optimal number depends on your network and storage backend. Start with 4 and adjust.

## Exporting Data

Export remotes store files with their original names, making them accessible without git-annex. This is useful for:

- Sharing data with tools that don't understand git-annex
- Creating human-readable directory structures in S3
- Setting up direct download links

### Export to a Remote

```bash
exo export --to s3-export
```

This exports all annexed files to the export remote with their original directory structure.

### Export Specific Paths

```bash
exo export --to s3-export --path data/processed/
```

### Export from a Source Remote

If you want to export directly from one remote to another (without downloading locally):

```bash
exo export --from s3-archive --to s3-export --path data/processed/
```

### Export a Specific Version

Export a tagged release or branch:

```bash
exo export --to s3-export --ref v1.0.0
```

### Dry Run

```bash
exo export --to s3-export --dry-run
```

> Dry-run shows which files would be exported.

### Branch Prefix Mode

When exporting multiple paths or branches, use `--branch-prefix` to organize exports by branch:

```bash
exo export --to s3-export --path data/ --branch-prefix
```

This creates paths like `s3://bucket/_export/refs/heads/main/data/`.

## Broadcasting Metadata

After syncing data to remotes, broadcast the metadata so other users can discover it:

```bash
exo broadcast --with s3-archive
```

This runs a metadata-only sync (no content transfer) to update remote tracking information. It's fast and should be run after any sync operation that uploads new data.

### Why Broadcast?

When you sync data to a remote, the remote knows about the files. But other clones of the repository don't know that the remote has this data until you broadcast the metadata.

Think of it as "announcing" where the data lives.

### Broadcast to Multiple Remotes

```bash
exo broadcast --with s3-archive --with backup-remote
```

## Catalog Remote Publishing

When your repository has catalog remotes (type `artifactdb`), `exo sync` automatically handles publishing to the catalog. Each catalog remote has a **role**, and each role publishes one thing to one destination:

- `role: atlas` (the default when `role` is absent): publishes the bundle (`.exohub-bundle/`) to an **Atlas** (search catalog). This is the familiar git-annex export special remote (`mode: export`).
- `role: instance`: publishes the git-tracked `.artifactdb/` directory to its own bucket, then asks the ExoHub API to forward the ingest to an ArtifactDB instance. It is **not** a git-annex special remote — `exo init` never runs `initremote` for it.

For a `role: atlas` remote, the sync command:

1. Generates metadata bundles (equivalent to running `exo bundle`)
2. Uploads bundle files to the remote's S3 location under `.exohub-bundle/`
3. Calls the catalog remote's publish binary to register the data

> **Requires a git remote:** Syncing to a catalog remote requires the generated bundle to have a `repo_url` (see [Repository URL in Bundle Metadata]({{< ref "advanced#repository-url-in-bundle-metadata" >}})). If your repository has no usable git remote, sync refuses to publish to the catalog remote with an error instead of registering an entry with no clone URL. Configure a remote (`git remote add origin <url>`) and re-run `exo bundle` before syncing.

### Choosing the Target Atlas

By default, a `role: atlas` remote's bundle goes to the ExoHub deployment's **default Atlas**. Set `url` on the remote in `.exohub/remotes` to publish to a specific, non-default Atlas instead — only Atlas URLs the deployment has allowlisted are accepted. A `role: atlas` remote never uploads `.artifactdb/`, regardless of whether one exists in the repo.

`exo` always shows, per catalog remote, where it will publish before anything happens — in `exo sync --dry-run` and again right before the real publish:

```
$ exo sync --with atlas --dry-run
Would publish to catalog remote 'atlas' via git-annex-remote-artifactdb-export
  api-url:      https://api.exohub.example.com
  s3url:        s3://my-bucket/my-prefix
  bundle → https://atlas.example.com/v1/main (url)
  grants:       false
```

When no `url` is configured, exo asks the API for the deployment's default Atlas and visually flags the line, since publishing to the default Atlas without an explicit `url` is the case most likely to surprise someone expecting a specific destination:

```
  bundle → ⚠ default Atlas https://atlas.example.com/v1/default
```

If the API can't be reached or doesn't expose the default Atlas URL (older deployment), the line falls back to `bundle → ⚠ default Atlas (server-chosen)`. Once a real publish succeeds, exo also prints which Atlas actually received the bundle.

If a remote's `url` isn't on the deployment's allowlist, the publish is refused (HTTP 422) with a clear, non-zero-exit error naming the fix and the allowlisted values. The bundle's files are still uploaded to S3 at that point, but nothing is indexed. To fix it, update the remote's `url` in `.exohub/remotes` to an allowlisted value (or remove it, on a `role: atlas` remote, to use the default Atlas), run `exo init`, and re-run `exo sync`/`exo publish`.

### Publishing to an ArtifactDB Instance (`role: instance`)

A `role: instance` remote publishes **only** the git-tracked `.artifactdb/` directory of the published ref (read with `git archive`, never the working tree directly) into its own bucket, then asks the ExoHub API to forward the ingest to the instance:

```yaml
remotes:
  - name: atlas
    type: artifactdb
    url: https://atlas.example.com/v1/main   # optional; absent = deployment default Atlas

  - name: instance
    type: artifactdb
    role: instance
    url: https://artifactdb.example.com/v1/myproject
    s3url: s3://my-instance-bucket/my-project-id
    data_from: atlas                         # optional; inferred when there's exactly one role: atlas remote
```

Required fields on a `role: instance` remote:

- `url` — the ArtifactDB instance's HTTPS endpoint.
- `s3url` — must be exactly `s3://<bucket>/<project_id>` (one path segment, equal to the remote's project id).

An instance remote delegates to one **Atlas** remote for the actual data: `data_from: <name>` names it explicitly, or, when omitted, exo infers it as the single `role: atlas` remote in `.exohub/remotes`. It takes no `mode` — it is refused if one is set, and it is never a git-annex special remote, so `exo init` doesn't run `initremote` for it.

`exo sync --dry-run`/`--with` also previews a `role: instance` remote's target:

```
$ exo sync --with instance --dry-run
Would publish to catalog remote 'instance' via git-annex-remote-artifactdb-export
  api-url:      https://api.exohub.example.com
  s3url:        s3://my-instance-bucket/my-project-id
  role:         instance
  .artifactdb/ → https://artifactdb.example.com/v1/myproject
  grants:       false
```

**Publish order:** every `role: atlas` remote publishes first; only once its delegation Atlas has been accepted does a ref's `role: instance` remote(s) publish. If the Atlas publish fails, the instance is **skipped** with a clear message and the command exits non-zero.

#### Excluding Files From the Instance Upload

Add `exclude: [<glob>...]` to a `role: instance` remote to keep matching `.artifactdb/` files out of that instance's upload — an Atlas remote's own upload is unaffected by another remote's `exclude`:

```yaml
  - name: instance
    type: artifactdb
    role: instance
    url: https://artifactdb.example.com/v1/myproject
    s3url: s3://example-bucket/DS1
    exclude:
      - "dataset_merged.json*"
```

Globs are matched against each file's path relative to the `.artifactdb/` root, using the same matcher as the `exclude:` on import/annex remotes: a single `*`/`?` stays within one path segment, `**` crosses segments, and a pattern with no `**` is also tried against the file's basename alone (so `dataset_merged.json*` matches `sub/dataset_merged.json.bak` without needing a leading `**/`).

A file kept off the upload this way never appears in that publish's `..complete` marker. If a file was uploaded by an earlier publish — before `exclude` was added, or before the glob matched it — the next instance publish **self-heals** by deleting the matching key(s) from the instance's version prefix, for both branch and tag publishes.

### Tag Immutability and the Source-Repo Guard

Two guards apply to publishing a catalog remote, regardless of role:

- **Tag immutability:** publishing a **tag** (not a branch) is refused when the remote already has a record naming a different commit than the tag now resolves to (a moved tag). The same commit is a safe retry. Branches are overwritten freely. There is no flag to bypass this — not even `--force`.
- **Source-repo guard:** publishing the same project/version from a different repository than the one it was first published from is refused before any upload, with a fix message naming the old and new repository. Pass `exo publish --force` (or `exo sync --force`) to bypass this guard — but never tag immutability.

### Migrating from `atlas_url` / `instance_url`

**Breaking change:** `atlas_url` and `instance_url` are no longer accepted in the **current** `.exohub/remotes` — exo refuses to run any command against the checked-out remotes file if it still has either key, before any upload or API call, and prints the role-split YAML to replace the old entry with (the repo's actual profile-rendered remotes when `.exohub/profile.lock` names a reachable profile, otherwise a generic example). To migrate:

1. **Upgrade exo first** on every machine/CI runner that publishes this repo — older exo versions don't understand role-split remotes.
2. Update the server profile to declare two remotes instead of one: `role: atlas` with the old `atlas_url` as `url`, and `role: instance` with the old `instance_url` as `url` and a project-scoped `s3url`.
3. Run `exo init --profile <profile> --remote <remote>` on the repo. This now performs the legacy→role-split conversion directly: it loads the legacy `.exohub/remotes` leniently, renders the profile's role-split remotes, shows the diff, and rewrites the file on confirmation (`--yes` to skip) — keeping the atlas remote's git-annex UUID (same special remote) and never running `initremote` for the new instance entry. If the rendered `s3url` also moves prefix/bucket, add `--allow-move` to confirm the move is intentional; grants are synced automatically for a `grants: true` remote, with a warning if a sync has to be skipped (e.g. not an owner — rerun as one, or with `--force-grants-sync`). Add `--dry-run` (optionally `--json`) to preview the plan without changing anything. Hand-editing `.exohub/remotes` remains an option too.
4. Re-publish.
5. **Old tags remain publishable** after the migration: `exo publish --ref <old tag>` reads the ref's own, pre-migration `.exohub/remotes` **leniently** — a tag whose committed file still has `atlas_url`/`instance_url` is not refused. That file is only used to check the ref's catalog remotes still have a home in the **current**, migrated config (matched by name or git-annex UUID); routing always comes from the current config, never the ref's. Run `exo publish --ref <tag>` from the migrated checkout to re-publish a pre-migration tag.

There is no compatibility layer and no override flag for the legacy-key refusal itself — every other command still refuses outright. The `--profile` migration path above is the only way to convert a legacy file, and it is a one-time rewrite, not a deployment setting.

### Publish Constraints

The `publish_on` field in `.exohub/remotes` controls when publishing happens:

| Value | Behavior |
|-------|----------|
| `always` | Publish on every sync (default) |
| `tag` | Only publish when HEAD is a tagged commit |

This lets you sync data without triggering a catalog publish until you're ready to tag a release.

### Publishing a Specific Bundle

By default, `exo sync` publishes the bundle most recently generated by `exo bundle` (resolved from HEAD). Use `--bundle-dir` to publish a specific, already-generated bundle directory instead — its version comes from that bundle's `bundle.json`, not from HEAD or the directory name:

```bash
exo sync --with exohub-atlas --bundle-dir .exohub/bundles/v1
```

Notes:
- `--bundle-dir` only affects catalog publishing; it does not filter which annex files are synced (that's what `--path` is for).
- Requires at least one catalog remote selected via `--with`.
- Not supported together with `--dry-run --json` (use the text `--dry-run` output instead).

## Publishing Data

The `exo publish` command orchestrates the full publish workflow in a single step: it syncs your content, generates bundle metadata, publishes to the catalog, and broadcasts the metadata to all remotes.

```bash
exo publish
```

Before running any step, `exo publish` checks that the repository has a usable git remote (the same resolution `exo bundle` uses). If none is found, it fails immediately:

```
no usable git remote — the catalog entry would have no clone URL (no git remote configured).
Add one with: git remote add origin <url>   (then re-run exo publish)
```

This runs four steps automatically:

1. **Sync content** — uploads data to all configured content remotes (annex, export, exospace types)
2. **Generate bundle** — runs `exo bundle --yes` to create metadata
3. **Sync to catalog** — uploads bundle metadata to artifactdb/catalog remotes
4. **Broadcast metadata** — runs `exo broadcast` so all remotes know about the new content

Each step logs its progress:

```
[1/4] Syncing content to 2 remote(s)
[2/4] Generating bundle metadata
[3/4] Syncing to 1 catalog remote(s)
[4/4] Broadcasting metadata

✓ Publish complete
```

### Override Content Remotes

By default, step 1 syncs to all content remotes. Use `--sync` to target specific remotes instead:

```bash
# Only sync to s3-archive in step 1
exo publish --sync s3-archive

# Sync to multiple specific remotes
exo publish --sync s3-archive --sync backup-remote
```

| Flag | Description | Default |
|------|-------------|---------|
| `--sync <remote>` | Remote to sync in step 1; repeat to specify multiple remotes | _(all content remotes)_ |

### Publishing an Older Ref (`--ref`)

Use `--ref` to publish an older tag or commit's content and metadata while keeping **current** routing — the remotes, `s3url`, `role`, `url`, and `data_from` come from whatever is checked out right now, never from the ref's own, possibly stale, copy of `.exohub/remotes`. This is what makes it safe to publish a tag made before a routing change (a bucket/prefix move, a new `role: atlas` url): it always lands on the current catalog instead of silently falling back to a default because the old tag's routing is incomplete.

```bash
exo publish --ref v1.2.0

# Repeatable: publish several refs in turn, same current routing
exo publish --ref v1.2.0 --ref v1.3.0
```

For each ref, `exo publish` builds its bundle and `.artifactdb/` (read from the ref's own tree, not the working tree) into a temporary directory, publishes that directory to the **current** catalog remotes, then removes the temporary directory. Two guards run first, per ref, unless `--force`:

1. Every catalog remote the ref's own `.exohub/remotes` references must still exist in the current config (matched by name or git-annex UUID) — otherwise publish refuses rather than silently drop it.
2. The ref's annexed content must already be present on the current annex remote(s) (checked via `exo sync --check --ref <ref> --verify`) — otherwise publish refuses with a hint to run `exo sync --all`, or `exo init --allow-move --migrate-content` if a remote's storage moved (see [Migrating Content on a Move]({{< ref "repositories#migrating-content-on-a-move---migrate-content" >}})).

Every publish (with or without `--ref`) also enforces a **source-repo guard**: publishing the same project/version from a different repository than the one it was first published from is refused before any upload. Pass `--force` to bypass the ref-to-remotes check, the content-availability check, and the source-repo guard — it never bypasses tag immutability (see [Tag Immutability and the Source-Repo Guard]({{< ref "#tag-immutability-and-the-source-repo-guard" >}})):

```bash
exo publish --ref v1.2.0 --force
```

> Without `--ref`, a plain `exo publish` refuses, non-zero, when run from a checkout (detached HEAD, or any branch other than the repository's default) whose own `.exohub/remotes` routing differs from the default branch's — publish that content with `exo publish --ref <ref>` from the default branch instead.

| Flag | Description | Default |
|------|-------------|---------|
| `--ref <tag\|commit>` | Publish this ref's content+metadata with the current routing; repeatable | _(none — publishes HEAD)_ |
| `--force` | Bypass the ref-to-remotes check and the source-repo guard (with `--ref`, also skip the content-availability check); never bypasses tag immutability | `false` |

### When to Use Publish vs. Manual Steps

| Scenario | Command |
|----------|---------|
| Full publish workflow (most common) | `exo publish` |
| Sync without generating a bundle | `exo sync` |
| Generate a bundle without syncing | `exo bundle` |
| Sync only metadata, no content | `exo broadcast` |

## Complete Workflow Example

Here's a typical workflow for downloading, modifying, and uploading data:

### 1. Clone and Initialize

```bash
exo clone https://github.com/org/my-dataset.git
cd my-dataset
exo init
```

### 2. Download the Data

```bash
# Sync everything
exo sync

# Or just what you need
exo sync --path data/processed/
```

### 3. Do Your Work

```bash
# Run your analysis, generate new files, etc.
python scripts/analyze.py
```

### 4. Add New Files

`exo add` routes new files to git or git-annex based on your repository's `annex.routing` rules in `.exohub/config` (first match wins), falling back to automatic text/binary detection when no rule matches — text files go to git, binary files go to git-annex. See [Repository-Level File Routing]({{< ref "advanced#repository-level-file-routing" >}}) for the full rule syntax and `exo add --explain`/`--reroute`.

```bash
# Auto-routes: text files → git, binary files → git-annex
exo add results/ scripts/analyze.py

# Force all files to git-annex (bypass detection)
exo add --annex results/

# Force all files to git (bypass detection)
exo add --git config.json

# Add files from URLs (always uses git-annex)
# Uses the server-provided filename (Content-Disposition header) when available,
# falling back to the last path component of the URL.
exo add https://example.com/data/dataset.csv

# Override the local filename with --file
exo add --file raw/dataset.csv https://example.com/data/dataset.csv

# Mix local files and URLs
exo add local-data.parquet https://example.com/remote-data.csv

# Add with parallel git-annex jobs
exo add -J 4 large-dataset/

# Commit
git commit -m "Add analysis results"
```

For URLs, `exo add` uses the filename provided by the server (via the `Content-Disposition` response header) when available. If the server does not provide one, the last path component of the URL is used as a fallback. Use `--file` to override the local filename (you can include a subdirectory path).

| Flag | Description |
|------|-------------|
| _(no flag)_ | Route by `annex.routing` rules, falling back to auto-detect: text → git, binary → git-annex, URLs → git-annex |
| `--annex` | Force all local files to git-annex (bypasses detection) |
| `--git` | Force all local files to git (bypasses detection) |
| `--file <path>` | Override the local filename for a URL download (may include a subdirectory) |
| `-J, --jobs <N>` | Parallel git-annex operations (default: `$EXOHUB_JOBS` or `1`) |

Files added to git-annex store only a lightweight pointer in Git, while the actual content goes to your configured remotes after syncing.

> If a tracked file was removed from disk (e.g. `rm data/old.csv`), `exo add` skips it and leaves the deletion unstaged — it never aborts the rest of the add for the files around it. It prints a note naming the skipped file(s) and suggesting `git rm` to stage the deletion. `exo bundle`/`exo publish` refuse to run over any uncommitted change to a tracked file, staged or not, so run `git rm` and commit the deletion (or restore the file) before bundling/publishing.

### 5. Publish

```bash
# Sync, bundle, and publish in one step
exo publish
```

Or, if you need more control over individual steps:

```bash
exo sync --with s3-archive
exo broadcast --with s3-archive
```

### 6. Push Git Changes

```bash
git push
```

Now your collaborators can pull your changes and sync the new data.

## Troubleshooting Sync Issues

### "Unable to access remote"

Check your authentication:

```bash
exo login
```

Verify the remote is configured:

```bash
exo info
```

### "No content available"

The file exists in the repository but isn't available on any accessible remote. Check which remotes have the file:

```bash
exo locate data/missing-file.parquet
```

### Slow Transfers

Try increasing parallelism:

```bash
exo sync -J 8
```

Check your network connection to the remote storage.

## Next Steps

Your data is synced. Let's learn about [versioning and collaboration]({{< ref "versioning" >}}).
