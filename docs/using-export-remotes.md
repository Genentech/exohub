# Using Export Remotes in Exo

## What is an Export Remote?

An **export remote** stores files using their actual filenames and directory structure (like a normal filesystem), rather than as content-addressed blobs. Think of it like "publishing" your git tree to S3 or a directory.

**Example:** A file `data/file.txt` appears as `data/file.txt` on S3, not as a cryptic hash like `.git/annex/objects/abc123...`

## Basic Concepts

### 1. **ref** - What to export

The git commit/branch/tag you want to export:

```bash
# Export the main branch
exo export --to s3-export --ref main

# Export a specific commit
exo export --to s3-export --ref abc123

# Export HEAD (current commit) - this is the default
exo export --to s3-export
```

### 2. **--path with --branch-prefix** - Export specific subdirectories

Use `--path` flags to export only parts of your tree:

```bash
# Export only the data/ directory
exo export --to s3-export --path data/ --branch-prefix
→ S3 structure: _export/refs/heads/main/data/file.txt

# Export only docs/ directory
exo export --to s3-export --path docs/ --branch-prefix
→ S3 structure: _export/refs/heads/main/docs/guide.md

# Export multiple directories
exo export --to s3-export --path data/ --path results/ --branch-prefix
→ Both directories exported with branch structure
```

**Note:** The `--branch-prefix` flag is required for multiple paths. It uses `ref:path` notation internally, which creates branch directories in S3.

### 3. **Tracking Branch** - Safety guard

Configured in `.exohub/remotes` or git config, prevents accidental exports from wrong branch:

```yaml
# .exohub/remotes
- name: s3-export
  type: export
  tracking-branch: main  # ← Safety: must export from main
```

If you try to export from a different branch, exo warns you and asks for confirmation.

### 4. **Preferred Content** - File filtering

Define in `.exohub/remotes` which files should be exported:

```yaml
# .exohub/remotes
- name: s3-export
  type: export
  wanted: "include=**/*.parquet"  # ← Only .parquet files
```

When you run `exo sync`, it automatically respects this filter.

## Common Usage Patterns

### A. Simple full export (most common)
```bash
exo export --to s3-export
```
- ✓ Exports entire current branch (HEAD)
- ✓ All files go to the remote
- ✓ No filtering

**Use when:** You want to publish your entire dataset to S3

### B. Export specific subdirectories
```bash
exo export --to s3-export --path "data/*.parquet" --path "results/" --branch-prefix
```
- ✓ Expands glob patterns: `data/*.parquet` → matches all .parquet files in data/
- ✓ Creates branch structure in S3
- ✓ Only exports the specified paths

**Use when:** You want to publish only certain directories or file patterns

### C. Export with preferred content (automatic filtering)
```yaml
# Configure in .exohub/remotes:
- name: s3-parquet
  type: export
  wanted: "include=**/*.parquet"
```

```bash
# Then just sync:
exo sync
```
- ✓ Automatically filters by preferred content
- ✓ Only `.parquet` files get exported
- ✓ No need to specify paths manually

**Use when:** You have permanent filtering rules for a remote (like "only production data" or "only parquet files")

### D. Export specific version/tag
```bash
exo export --to s3-export --ref v1.0
```
- ✓ Export a specific tag/commit
- ✓ Useful for versioned releases
- ✓ Must match tracking branch or you'll be prompted

**Use when:** Publishing a specific release or snapshot

### E. Export with manifest (reproducible exports)
```yaml
# export-manifest.yaml
name: production-export
to: s3-prod
ref: v2.0
paths:
  - data/processed/
  - results/final/
```

```bash
exo export --manifest export-manifest.yaml --branch-prefix
```

**Use when:** You want reproducible, documented export configurations (great for CI/CD)

## What `exo sync` Does for Export Remotes

`exo sync` intelligently handles export remotes based on their configuration:

```bash
# If remote has NO preferred content:
exo sync
→ Exports: entire tracking branch using git annex sync

# If remote HAS preferred content (e.g., "include=**/*.parquet"):
exo sync
→ Exports: only files matching preferred content using git annex export
→ Filters automatically
```

**This is the magic:** You don't need to remember which command to use. Just `exo sync` and it does the right thing based on your `.exohub/remotes` configuration.

## Key Differences Summary

| Scenario | Command | What Gets Exported |
|----------|---------|-------------------|
| Full export | `exo export --to remote` | Entire current branch |
| Specific paths | `exo export --to remote --path data/ --branch-prefix` | Only data/ directory |
| Filtered export | `exo sync` (with wanted in .exohub/remotes) | Only files matching filter |
| Multiple paths | `exo export --to remote --path a/ --path b/ --branch-prefix` | Both directories |
| Specific version | `exo export --to remote --ref v1.0` | Specific tag/commit |

## Practical Example

Let's say you have a repo with:
```
├── data/
│   ├── raw.csv
│   └── processed.parquet
├── docs/
│   └── README.md
└── scripts/
    └── process.py
```

**Different export strategies:**

### Strategy 1: Export everything
```bash
exo export --to s3-all
```
→ All files go to S3

### Strategy 2: Export only data directory
```bash
exo export --to s3-data --path data/ --branch-prefix
```
→ Only `data/raw.csv` and `data/processed.parquet` go to S3

### Strategy 3: Export only parquet files (using preferred content)
```yaml
# .exohub/remotes
- name: s3-parquet
  type: export
  wanted: "include=**/*.parquet"
```
```bash
exo sync --with s3-parquet
```
→ Only `data/processed.parquet` goes to S3

### Strategy 4: Export multiple specific paths
```bash
exo export --to s3-subset --path data/processed.parquet --path docs/ --branch-prefix
```
→ Only `data/processed.parquet` and everything in `docs/` go to S3

### Strategy 5: Set it and forget it (recommended)
```yaml
# .exohub/remotes - configure once
- name: s3-production
  type: export
  tracking-branch: main
  wanted: "include=**/*.parquet"

- name: s3-docs
  type: export
  tracking-branch: main
  wanted: "include=docs/**"
```

```bash
# Then just:
exo sync
```
→ Automatically exports parquet to s3-production, docs to s3-docs, respecting all filters

## When to Use What?

**Use `exo export`:**
- ✓ One-time exports
- ✓ Testing/experimentation
- ✓ Exporting specific refs/versions
- ✓ Need explicit control over what/when

**Use `exo sync`:**
- ✓ Regular workflow
- ✓ Syncing multiple remotes at once
- ✓ Automatic filtering by preferred content
- ✓ "Just sync everything" convenience

## Common Questions

### Q: What's the difference between export and annex remotes?

**Export remotes:**
- Files stored with real names: `data/file.txt`
- Good for: S3 buckets, web publishing, sharing with non-git-annex tools
- Use when: You need human-readable paths

**Annex remotes:**
- Files stored as content-addressed blobs: `.git/annex/objects/abc123...`
- Good for: Deduplication, verification, git-annex ecosystem
- Use when: You need git-annex features

### Q: Can I have both annex and export remotes?

Yes! Common pattern:
```yaml
# .exohub/remotes
- name: s3-annex
  type: annex  # ← For backup, deduplication

- name: s3-export
  type: export  # ← For sharing/publishing
```

### Q: Does preferred content work for dry-run?

Yes! `exo sync --dry-run` shows exactly what would be exported based on preferred content filters.

### Q: What happens if I export from the wrong branch?

Exo checks the tracking branch and warns you:
```
⚠️  Warning: tracking branch is 'main' but you're exporting 'feature-branch'
Do you want to update tracking branch? [y/N]
```

You can override with `--yes` flag for automation.
