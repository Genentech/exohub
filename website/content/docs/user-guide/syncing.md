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

## Checking Completeness Across History

A plain `exo sync` only uploads the annexed content of the **current checkout**. If a repository's history replaces an annexed file commit after commit (e.g. importing several versions of a dataset chronologically and tagging each one), the content an **older** tag or commit points at can end up on no remote at all, unless it was synced while that version was checked out. Nothing about a normal `exo sync` tells you this happened. After importing several versions this way (one commit or tag per version), run `exo sync --all` (then `exo sync --check` to confirm) instead of a plain `exo sync` — the default `exo sync` never uploads older commits' content.

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

`exo sync --dry-run --json --ref <ref>` previews, for a catalog remote, the routing (`s3url`, `atlas_url`, `instance_url`) that publishing `<ref>` would use right now — read fresh from the **current** `.exohub/remotes`, never from `<ref>`'s own checked-out copy — alongside `<ref>`'s own identity (`ref`, `ref_commit`):

```bash
exo sync --dry-run --json --ref v1.0.0
exo sync --dry-run --json --ref v1.0.0 --ref v1.1.0
```

This requires at least one catalog remote (via `--with` or auto-discovery), and is how a guard can verify the publish target before `exo publish --ref` actually runs — a ref that predates a routing change (a bucket/prefix move, a new `atlas_url`) still resolves to the current catalog, not a stale or missing one.

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

When your repository has catalog remotes (e.g., `artifactdb` type), `exo sync` automatically handles publishing to the catalog. The sync command:

1. Generates metadata bundles (equivalent to running `exo bundle`)
2. Uploads bundle files to the remote's S3 location under `.exohub-bundle/`
3. Calls the catalog remote's publish binary to register the data

> **Requires a git remote:** Syncing to a catalog remote requires the generated bundle to have a `repo_url` (see [Repository URL in Bundle Metadata]({{< ref "advanced#repository-url-in-bundle-metadata" >}})). If your repository has no usable git remote, sync refuses to publish to the catalog remote with an error instead of registering an entry with no clone URL. Configure a remote (`git remote add origin <url>`) and re-run `exo bundle` before syncing.

### Choosing the Target Atlas

By default, a catalog remote's bundle goes to the ExoHub deployment's **default Atlas**. Set `atlas_url` on the remote in `.exohub/remotes` to publish to a specific, non-default Atlas instead — only Atlas URLs the deployment has allowlisted are accepted.

`exo` always shows, per catalog remote, where the bundle will go before anything is published — in `exo sync --dry-run` and again right before the real publish:

```
$ exo sync --with atlas --dry-run
Would publish to catalog remote 'atlas' via git-annex-remote-artifactdb-export
  api-url:      https://api.exohub.example.com
  s3url:        s3://my-bucket/my-prefix
  bundle → https://atlas.example.com/v1/sandbox1 (atlas_url)
  .artifactdb/ → https://artifactdb.example.com/v1/myproject
  grants:       false
```

When no `atlas_url` is configured, exo asks the API for the deployment's default Atlas and visually flags the line, since publishing to the default Atlas without an explicit `atlas_url` is the case most likely to surprise someone expecting a specific destination:

```
  bundle → ⚠ default Atlas https://atlas.example.com/v1/default
```

If the API can't be reached or doesn't expose the default Atlas URL (older deployment), the line falls back to `bundle → ⚠ default Atlas (server-chosen)`. Once a real publish succeeds, exo also prints which Atlas actually received the bundle.

If `atlas_url` or `instance_url` isn't on the deployment's allowlist, the publish is refused (HTTP 422) with a clear, non-zero-exit error naming the fix and the allowlisted values. The bundle's files are still uploaded to S3 at that point, but nothing is indexed. To fix it, update `atlas_url`/`instance_url` in `.exohub/remotes` to an allowlisted value (or remove `atlas_url` to use the default Atlas), run `exo init`, and re-run `exo sync`/`exo publish`.

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

Use `--ref` to publish an older tag or commit's content and metadata while keeping **current** routing — the remotes, `s3url`, `atlas_url`, and `instance_url` come from whatever is checked out right now, never from the ref's own, possibly stale, copy of `.exohub/remotes`. This is what makes it safe to publish a tag made before a routing change (a bucket/prefix move, a new `atlas_url`): it always lands on the current catalog instead of silently falling back to a default because the old tag's routing is incomplete.

```bash
exo publish --ref v1.2.0

# Repeatable: publish several refs in turn, same current routing
exo publish --ref v1.2.0 --ref v1.3.0
```

For each ref, `exo publish` builds its bundle and `.artifactdb/` (read from the ref's own tree, not the working tree) into a temporary directory, publishes that directory to the **current** catalog remotes, then removes the temporary directory. Two guards run first, per ref:

1. Every catalog remote the ref's own `.exohub/remotes` references must still exist in the current config (matched by name or git-annex UUID) — otherwise publish refuses rather than silently drop it.
2. The ref's annexed content must already be present on the current annex remote(s) (checked via `exo sync --check --ref <ref> --verify`) — otherwise publish refuses with a hint to run `exo sync --all`, or `exo init --allow-move --migrate-content` if a remote's storage moved (see [Migrating Content on a Move]({{< ref "repositories#migrating-content-on-a-move---migrate-content" >}})).

Pass `--force` to skip the content-availability check and publish anyway:

```bash
exo publish --ref v1.2.0 --force
```

> Without `--ref`, a plain `exo publish` refuses, non-zero, when run from a checkout (detached HEAD, or any branch other than the repository's default) whose own `.exohub/remotes` routing differs from the default branch's — publish that content with `exo publish --ref <ref>` from the default branch instead.

| Flag | Description | Default |
|------|-------------|---------|
| `--ref <tag\|commit>` | Publish this ref's content+metadata with the current routing; repeatable | _(none — publishes HEAD)_ |
| `--force` | With `--ref`, publish even if the content-availability check finds fixable/lost content on the current remote(s) | `false` |

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
