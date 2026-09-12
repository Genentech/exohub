---
title: "Advanced Operations"
description: "Copying, mirroring, and linking"
weight: 8
---

This section covers advanced operations for managing data across multiple remotes and storage systems.

## Copying Between Remotes

The `exo copy` command transfers data directly between remotes without downloading to your local machine.

### Basic Copy

Copy all data from one remote to another:

```bash
exo copy --from s3-primary --to s3-backup
```

### Copy Specific Paths

```bash
exo copy --from s3-primary --to s3-backup --path data/important/
```

### Copy a Specific Version

Copy data as it exists at a specific tag or branch:

```bash
exo copy --from s3-primary --to s3-backup --ref v1.0.0
```

### Auto Mode

Use wanted rules configured on the destination:

```bash
exo copy --from s3-primary --to s3-backup --auto
```

This copies only files that the destination remote "wants" based on its preferred content settings.

### Using a Manifest

```yaml
# copy-manifest.yaml
name: backup-copy
url: https://github.com/org/dataset.git
ref: main

from-remote: s3-primary
to-remote: s3-backup
paths:
  - data/
```

```bash
exo copy --manifest copy-manifest.yaml
```

## Mirroring from S3

The `exo mirror` command efficiently downloads large amounts of data from S3 by generating optimized transfer plans. It works closely with `exo link` — typically you run `mirror` to download files to a local cache, then `link` to connect them to your repository.

### How It Works

1. **Plan phase** — Analyzes your repository and generates an s5cmd command file
2. **Execute phase** — Runs the commands to download files in parallel

This is faster than `exo sync` for bulk downloads because it uses s5cmd's optimized S3 transfers.

### Generate a Plan

```bash
exo mirror plan \
    --s3-annex s3://my-bucket/datasets/genomics \
    --store /mnt/fast-storage/annex-cache
```

This creates a plan file at `.git/exohub/exo-mirror-plan.txt`.

### Review the Plan

```bash
# See what would be downloaded
cat .git/exohub/exo-mirror-plan.txt | head -20
```

### Execute the Plan

```bash
exo mirror execute
```

Or specify a plan file:

```bash
exo mirror execute /path/to/custom-plan.txt
```

### Mirror Specific Paths

```bash
exo mirror plan \
    --s3-annex s3://my-bucket/datasets \
    --store /mnt/cache \
    --path data/samples/
```

### Mirror Options

| Flag | Description |
|------|-------------|
| `--s3-annex <uri>` | S3 prefix (required) |
| `--store <dir>` | Local cache directory (required) |
| `--path <path>` | Limit to specific paths (repeatable) |
| `--jobs <n>` | s5cmd worker count (default: 16) |
| `--no-clobber` | Don't overwrite existing files |
| `--dry-run` | Show summary without writing files |

### After Mirroring

Once files are in your local store, link them to your repository:

```bash
exo link --store /mnt/fast-storage/annex-cache
```

## Linking to External Stores

The `exo link` command reconnects git-annex symlinks to files in an external directory. It's typically used after `exo mirror` to link downloaded files to your repository.

This is useful when:

- You've mirrored data from S3 to a local cache
- You're migrating data from one storage system to another
- You want to use a shared cache across multiple clones

### Basic Link

```bash
exo link --store /mnt/shared-cache
```

This scans your repository for broken symlinks and fixes them by linking to files in the store.

### Link Modes

| Mode | Description |
|------|-------------|
| `auto` | Try hardlink first, fall back to copy |
| `hardlink` | Create hard links (fastest, same filesystem only) |
| `symlink` | Create symbolic links |
| `copy` | Copy files (slowest, works across filesystems) |

```bash
exo link --store /mnt/cache --mode hardlink
```

### Link Specific Paths

```bash
exo link --store /mnt/cache --path data/samples/
```

### Dry Run

See what would be linked:

```bash
exo link --store /mnt/cache --dry-run
```

### Query Key Mappings

Find where a specific annex key maps to:

```bash
exo link --store /mnt/cache --key MD5E-s1234--abc123def456
```

### Export Key Mappings

Generate a TSV of all symlink mappings:

```bash
exo link --map-out mappings.tsv
```

## Workflow: Bulk Data Migration

Here's a complete workflow for migrating a large dataset to a new storage location.

### 1. Mirror from Source

```bash
cd my-dataset

# Plan the mirror
exo mirror plan \
    --s3-annex s3://old-bucket/datasets/my-data \
    --store /mnt/migration-cache \
    --jobs 32

# Execute
exo mirror execute
```

### 2. Link to Local Repository

```bash
exo link --store /mnt/migration-cache --mode hardlink
```

### 3. Verify Files

```bash
exo fsck --path data/
```

### 4. Upload to New Remote

```bash
# Add the new remote to .exohub/remotes
# Then reinitialize
exo init

# Sync to new location
exo sync --with new-s3-remote -J 16

# Broadcast metadata
exo broadcast --with new-s3-remote
```

### 5. Update Export Remote

```bash
exo export --from new-s3-remote --to new-s3-export
```

## Workflow: Shared Cache Setup

Multiple users or jobs can share an annex cache to avoid redundant downloads.

### 1. Set Up Shared Storage

Create a shared directory accessible to all users:

```bash
mkdir -p /shared/annex-cache
chmod 2775 /shared/annex-cache
```

### 2. Populate the Cache

One user downloads the data:

```bash
exo mirror plan \
    --s3-annex s3://bucket/dataset \
    --store /shared/annex-cache

exo mirror execute
```

### 3. Other Users Link to Cache

Other users can link without downloading:

```bash
cd my-clone
exo link --store /shared/annex-cache --mode hardlink
```

### 4. Keep Cache Updated

As new data is added, update the cache:

```bash
# In any clone with the latest data
exo mirror plan --s3-annex s3://bucket/dataset --store /shared/annex-cache
exo mirror execute
```

## Performance Tips

### Parallel Jobs

Most commands support parallel execution:

```bash
exo sync -J 8
exo mirror plan --jobs 32
exo copy --jobs 16
```

### Use Mirroring for Bulk Downloads

For initial downloads of large datasets, `exo mirror` is faster than `exo sync`:

```bash
# Slower for bulk downloads
exo sync --with s3-remote

# Faster for bulk downloads
exo mirror plan --s3-annex s3://bucket/data --store /mnt/cache
exo mirror execute
exo link --store /mnt/cache
```

### Use Hard Links When Possible

Hard links are faster than copies and don't duplicate data:

```bash
exo link --store /mnt/cache --mode hardlink
```

Requirements:
- Store and repository must be on the same filesystem
- Filesystem must support hard links

## Generating Metadata Bundles

The `exo bundle` command generates JSON metadata describing a repository at a specific git revision. This is used for catalog ingest and automated indexing. You can pass one or more paths to restrict bundle generation to specific files or directories.

### Basic Usage

```bash
exo bundle
exo bundle path/to/subset/
```

This reads the `.exohub/bundle` manifest and generates output under `.exohub/bundles/<scope>/`:
- `bundle.json` — bundle manifest with summary metadata (counts, sizes, ref info)
- `commits/*.json` — per-commit metadata (one file per commit, `exohub-commit/v1.json` schema)
- `files/*.json` — per-file artifact metadata (`exohub-artifact/v1.json` schema)
- `markdown/*.json` — per-markdown-file metadata (`exohub-markdown/v1.json` schema, includes title, sections, and front matter)

> **Note:** By default, the output directory is cleaned before each run to avoid stale artifacts. Use `--resume` to skip existing artifacts instead (useful for interrupted runs). The `-y` flag skips confirmation and regenerates from scratch.
>
> When the output contains many files, `exo bundle` automatically packs them into `.artipack` ZIP archives to reduce file count.

### Options

| Flag | Description | Default |
|------|-------------|---------|
| `--scope <all\|git\|annex>` | Metadata categories to generate | From preset (`all`) |
| `--from-ref <ref>` | Start of commit range | Auto-detected |
| `--preset <name>` | Bundle preset from `.exohub/bundle` | `default` |
| `--output <dir>` | Custom output directory | `.exohub/bundles/<scope>/` |
| `--ref <ref>` | Commit hash or tag to build the bundle for | HEAD |
| `--include-hidden` | Include files in hidden directories | `false` (skip) |
| `--markdown-recursive` | Parse markdown files in all directories (default: root level only) | `false` |
| `--incremental` | Only generate artifacts for files modified in the commit range | `false` |
| `--resume` | Resume a previous run, skipping existing artifacts | `false` |
| `-y, --yes` | Skip confirmation prompts (remove and regenerate) | `false` |
| `--dry-run` | Preview what the bundle would do without generating anything | `false` |

### Using Presets

```bash
# Use the default preset
exo bundle

# Use a named preset
exo bundle --preset parquet-only

# Override scope regardless of preset
exo bundle --scope annex
```

### Markdown Indexing

Bundles always index markdown files (`.md`) found at the repository root, regardless of `--scope`. Even `--scope annex` (which restricts file artifacts to annexed data) still produces markdown artifacts. Markdown indexing extracts titles, section headings, and YAML front matter into `exohub-markdown/v1.json` artifacts.

To index markdown files in all directories (not just the root):

```bash
exo bundle --markdown-recursive
```

Or set `markdown_recursive: true` in your `.exohub/bundle` preset:

```yaml
presets:
  default:
    markdown_recursive: true
```

### Commit Range

By default, `exo bundle` includes all commits. Use `--from-ref` to limit the range:

```bash
# Commits since v1.0
exo bundle --from-ref v1.0
```

### Targeting a Specific Ref

Use `--ref` to build a bundle for a specific tag or commit without manually checking it out:

```bash
exo bundle --ref v5.3.0
exo bundle --ref v5.3.0 --dry-run   # preview first
```

The working tree must be clean. `exo bundle` checks out the target ref, runs the bundle, then restores the original branch.

### Dry Run

Preview what the bundle would generate without writing any files:

```bash
exo bundle --dry-run
```

### Incremental Bundles

Only generate file artifacts for files modified in the commit range:

```bash
exo bundle --incremental
```

This is useful for large repositories where only a subset of files have changed.

## Annex Configuration

When you run `exo init`, it configures git-annex with sensible defaults so that annexed files behave like normal git files. You can check the current configuration with `exo info`.

### Unlocked Mode

By default, `exo init` enables **unlocked mode** (`annex.addunlocked=true`). This changes how annexed files appear in your working tree:

| Mode | File appearance | How to edit |
|------|----------------|-------------|
| Locked (default git-annex) | Symlink to `.git/annex/objects/` | `git annex unlock` first |
| **Unlocked (exo default)** | Regular file | Just edit it |

With unlocked mode, you never need to run `git annex unlock`. Files look and behave like normal files.

### Locking and Unlocking Files

Even in unlocked mode, you may want to lock specific files back to read-only symlinks (to save disk space or prevent accidental edits), or unlock them again when you're ready to edit.

```bash
# Lock all annexed files in the current directory
exo lock

# Lock files under a specific path
exo lock data/archive/

# Lock even if files have unsaved modifications
exo lock --force data/

# Unlock all annexed files in the current directory
exo unlock

# Unlock specific paths
exo unlock data/samples/
```

| Command | Flag | Description |
|---------|------|-------------|
| `exo lock` | _(none)_ | Lock annexed files (read-only symlinks) |
| `exo lock` | `--force` | Lock even with unsaved modifications |
| `exo unlock` | _(none)_ | Unlock annexed files (writable copies) |

### Thin Mode

By default, unlocked files are **copies** of the annex object — the file content exists twice on disk. This is safe because `git restore` and `git checkout` work as expected.

For large repos where disk space is a concern, you can enable **thin mode** by adding `thin: true` to `.exohub/config`:

```yaml
thin: true
```

Then run `exo init` to apply. With thin mode, annexed files use hard links instead of copies, saving disk space. The trade-off is that editing a file overwrites the only local copy of the original content — `git restore` won't work, and recovering old versions requires `git annex get` from a remote.

> **Note:** In previous versions, thin mode was configured in `.exohub/context`. That file is no longer used by `exo init`.

### File Routing with .gitattributes

`exo add` automatically detects whether a file is text or binary and routes it to git or git-annex accordingly. If you want `git add` (used by IDEs, git hooks, etc.) to also route files correctly, you can add an `annex.largefiles` rule to `.gitattributes`:

```
* annex.largefiles=not(include=*.txt or include=*.md or include=*.json or include=*.xml or include=*.yaml or include=*.yml or include=*.py or include=*.go or include=*.sh or include=*.R or include=*.r) or largerthan=100kb
```

This tells git-annex:
- Files matching listed extensions go to **git**
- Everything else goes to **git-annex**
- Files larger than 100kb are always annexed, regardless of extension

You can customize the rule for your project. For example, to always annex everything in a `data/` directory:

```
data/** annex.largefiles=anything
```

> **Note:** Use extension-based matching (`include=`) rather than `mimetype=`. The `mimetype=` matcher depends on libmagic, which is unreliable across environments and can silently put binary files into git when the magic database is not found.

### Large Text File Guardrail

When running `exo add` in auto mode, the CLI checks whether any detected text files exceed **5 MB**. Files above this threshold are unusual for version-controlled source files and are likely data files that belong in git-annex rather than git.

In interactive terminals, a confirmation prompt appears:

```
Warning: 1 file is detected as text but larger than 5.0 MiB:
  data/embeddings.tsv (12 MiB)
Use git-annex for these files? (recommended)
> Yes, use git-annex
  No, use git
```

In non-interactive environments (CI pipelines, scripts), the guardrail defaults silently to **annex**.

To bypass the prompt, pass an explicit mode:

```bash
# Force git-annex for all files (skips prompt)
exo add --annex data/embeddings.tsv

# Force git tracking (use only if you are sure the file should be in git)
exo add --git data/embeddings.tsv
```

The guardrail applies to any file the CLI detects as text (MIME prefix `text/*`). In practice this includes plain text, Markdown, CSV, YAML, XML, and JSON files — the CLI uses content sniffing (`http.DetectContentType`) rather than file extensions, so detection is based on file content, not the filename.

## Next Steps

If you encounter issues, see [Troubleshooting]({{< ref "troubleshooting" >}}).
