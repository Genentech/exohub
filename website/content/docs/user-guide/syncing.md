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

### Publish Constraints

The `publish_on` field in `.exohub/remotes` controls when publishing happens:

| Value | Behavior |
|-------|----------|
| `always` | Publish on every sync (default) |
| `tag` | Only publish when HEAD is a tagged commit |

This lets you sync data without triggering a catalog publish until you're ready to tag a release.

## Publishing Data

The `exo publish` command orchestrates the full publish workflow in a single step: it syncs your content, generates bundle metadata, publishes to the catalog, and broadcasts the metadata to all remotes.

```bash
exo publish
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

`exo add` automatically detects whether files are text or binary and routes them accordingly — text files go to git, binary files go to git-annex:

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
| _(no flag)_ | Auto-detect: text → git, binary → git-annex, URLs → git-annex |
| `--annex` | Force all local files to git-annex (bypasses detection) |
| `--git` | Force all local files to git (bypasses detection) |
| `--file <path>` | Override the local filename for a URL download (may include a subdirectory) |
| `-J, --jobs <N>` | Parallel git-annex operations (default: `$EXOHUB_JOBS` or `1`) |

Files added to git-annex store only a lightweight pointer in Git, while the actual content goes to your configured remotes after syncing.

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
