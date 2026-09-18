---
title: "Working with Repositories"
description: "Clone, initialize, and explore dataset repositories"
weight: 3
---

This section covers how to clone existing datasets, create new repositories, and explore what's inside them.

## Cloning a Dataset

To download a dataset, use `exo clone`:

```bash
exo clone {{< exohub "cloneExample" >}}
```

This creates a `mnist/` directory with the repository structure. At this point, you have the metadata and pointers, but not the actual data files yet.

```bash
cd mnist
ls data/
# train.parquet -> ../.git/annex/objects/...  (symlink to annexed file)
# test.parquet  -> ../.git/annex/objects/...
```

The files appear as symlinks. To get the actual data, you'll need to initialize and sync (covered in the next sections).

### Specifying a Destination

Clone to a specific directory:

```bash
exo clone https://github.com/org/dataset.git my-local-folder
```

### Clone Presets

Large datasets often contain more than you need. Clone presets let you download just the parts relevant to your work.

#### Automatic Preset Discovery

When a repository defines presets, `exo clone` automatically discovers them and shows a selection menu:

```bash
exo clone https://github.com/org/dataset.git
```

If presets exist, you'll see:

```
Available presets:
  1. full-clone   - Complete repository with everything
  2. data-only    - Just the processed data for quick experimentation
  3. notebooks    - Notebooks and processed data for learning
  4. raw-data     - Original files for custom preprocessing

Select a preset [1-4]:
```

If no presets are defined, a full clone proceeds automatically. Use `--yes` to auto-accept the default (full-clone) without prompting.

#### Using a Specific Preset

To skip the selection menu and use a specific preset directly:

```bash
exo clone --preset notebooks https://github.com/org/dataset.git
```

#### What Presets Control

Presets are defined in `.exohub/presets` and can configure:

- **Sparse paths** — Only checkout specified directories

Example preset configuration:

```yaml
presets:
  - name: notebooks
    description: "Notebooks and processed data for learning"
    sparse_paths:
      - "notebooks/"
      - "data/processed/"
      - "README.md"
```

#### Dry Run

See what a clone would do without actually cloning:

```bash
exo clone --preset notebooks --dry-run https://github.com/org/dataset.git
```

## Initializing a Repository

After cloning, initialize the repository to set up remotes:

```bash
cd mnist
exo init
```

This reads the `.exohub/remotes` configuration and sets up git-annex remotes accordingly. Now you're ready to sync data.

### Initializing from a URL

You can initialize a repository directly from a git URL:

```bash
exo init https://github.com/my-org/my-dataset.git
```

This parses the URL to extract the host, organization, and repository name, then creates the repository on the git host (if `--create-repo` is used) and sets up remotes. Supports HTTPS, SSH SCP (`git@host:org/repo.git`), and SSH URL (`ssh://git@host/org/repo.git`) formats.

### What `exo init` Does

1. Initializes git-annex if not already initialized
2. Reads remote configurations from `.exohub/remotes`
3. Configures git-annex remotes (S3 buckets, etc.)
4. Sets up authentication for remotes
5. Provisions [S3 Access Grants]({{< ref "permissions" >}}) for remotes that have `grants: true`
6. Enables unlocked mode (`annex.addunlocked=true`) so annexed files appear as regular editable files
7. Applies annex defaults from `.exohub/config` (`addunlocked`, `thin`) — see [Configuration Files](#configuration-files)

### Creating a New Repository

To create a new dataset repository from scratch:

```bash
mkdir my-dataset
cd my-dataset
git init
exo init
```

This initializes both Git and git-annex. You'll need to create the `.exohub/remotes` configuration manually (see [Configuration Reference](#configuration-files) below).

Or create and initialize from a URL in one step:

```bash
exo init --create-repo https://github.com/my-org/my-dataset.git
```

#### Using Profiles

Your organization may have profiles available to bootstrap new repositories. A profile is a server-authored definition that handles variable resolution, interactive prompts, file rendering (`.exohub/` configuration files), and optional repository creation actions — all in one step. After the profile's config files are written, `exo init --profile` automatically completes full repository initialization: git-annex init, annex defaults, and any remotes defined by the profile.

To apply a named profile:

```bash
exo init --profile sandbox
```

To browse available profiles interactively:

```bash
exo init --profile-select
```

You can also set the profile via the `EXOHUB_PROFILE` environment variable (ignored if the current directory is already a git repository):

```bash
EXOHUB_PROFILE=sandbox exo init
```

Profile mode supports additional flags:

```bash
# Accept all prompts with defaults (non-interactive)
exo init --profile sandbox --yes

# Preview what would be written without making changes
exo init --profile sandbox --dry-run

# Overwrite an existing .exohub/ directory
exo init --profile sandbox --force
```

**Template variables available inside profiles:**

All variables are exposed at the top level — reference them directly with `.VarName`:

| Variable | Description |
|----------|-------------|
| `.UnixID` | Your username (from JWT or OS) |
| `.Folder` | Current directory base name |
| `.<key>` | Values from the profile's `vars` block (by key name) |
| `.<NAME>` | Value of `EXOHUB_TPL_<NAME>` environment variable (by suffix) |
| `.<key>` | Responses to the profile's interactive prompts (by prompt key) |

> **Precedence:** Prompt answers override vars; environment variables override vars. On key collision, last write wins.

### Re-initializing

If remote configurations change, re-run `exo init` to update:

```bash
exo init
```

Re-initializing updates remote configurations and refreshes S3 Access Grants.

### Re-initializing a Single Remote

To configure or re-validate just one remote without touching the others, pass `--remote`:

```bash
exo init --remote s3-annex
```

This is useful when only one remote's credentials or configuration have changed, or when debugging a specific remote. The orphaned-remote check is skipped when `--remote` is active to avoid false positives.

### Init Options

| Flag | Description |
|------|-------------|
| `--host <url>` | Override context host |
| `--org <org>` | Override context organization |
| `--create-repo` | Create the repository on the git host |
| `--name <repo>` | Explicit repository name |
| `--profile <name>` | Fetch and apply a server-authored profile |
| `--profile-select` | Interactively select a profile to apply |
| `--force` | Overwrite existing `.exohub/` when applying a profile |
| `--dry-run` | Show what would be done |
| `--reset` | Remove `.exohub/` and `.git/` before initializing |
| `--remote <name>` | Only configure or validate the named remote |
| `--dead <uuid>` | Mark a remote UUID as dead |
| `--destroy <uuid>` | Destroy a UUID from git-annex history (destructive) |
| `--confirm <uuid>` | Confirmation UUID for `--destroy` in non-interactive mode |

## Exploring a Repository

### Repository Info

Use `exo info` to see what's in a repository:

```bash
exo info
```

This displays:

- Repository URL
- Clone preset used (if any)
- Configured remotes and their types
- Trust levels for each remote
- Your current location indicator
- Annex configuration (unlocked mode, thin mode, file routing)

Example output:

```
Data Repo: {{< exohub "cloneExample" >}}

Clone Preset
  Name: notebooks
  Commit: abc123
  Branch: main
  Cloned: 2024-01-15T10:30:00Z

Configured remotes
┏━━━━━━━━━━━━━┳━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┓
┃ name        ┃ uuid                                 ┃
┣━━━━━━━━━━━━━╋━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┫
┃   s3-annex  ┃ 0d80d418-7b86-421a-9690-e01b0910b317 ┃
┃ ♥ s3-export ┃ bba7ca80-6235-4d2c-9bb0-18cc25d25c62 ┃
┗━━━━━━━━━━━━━┻━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┛

Other repositories
┏━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┳━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┓
┃ name                         ┃ uuid                                 ┃
┣━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━╋━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┫
┃ * user@host:/path/repo [here]┃ 26625354-4dc2-415c-8d32-4b635722fdb0 ┃
┃   web                        ┃ 00000000-0000-0000-0000-000000000001 ┃
┗━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┻━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┛

Annex Config
  Unlocked: yes    — files are editable without git annex unlock
  Thin:     no     — copies; git restore works
  Routing:  active — .gitattributes has annex.largefiles rules
```

Legend:
- `*` — your current local repository
- `♥` — remote has preferred content rules configured

### Detailed Remote Info

Get information about a specific remote:

```bash
exo info --remote s3-archive
```

### JSON Output

For scripting:

```bash
exo info --json
```

### Locating Files

Use `exo locate` to see where each file is stored — whether it's tracked by Git, git-annex, or untracked — and which remotes hold annexed content:

```bash
exo locate
```

Example output:

```
git        README.md
git        notebooks/analysis.ipynb
annex      data/train.parquet        *here, 📦 s3-annex
annex🔒   data/test.parquet         📦 s3-annex
untracked  data/scratch.csv
```

Each annexed file shows which remotes hold a copy, with `*here` indicating local presence and emoji badges for remote types. Locked annexed files (symlinks) show a 🔒 indicator.

#### Options

| Flag | Description | Default |
|------|-------------|---------|
| `--json` | Machine-readable JSON Lines output (one object per file) | `false` |
| `--fast` | Skip remote location queries; only report git/annex/untracked status | `false` |
| `--annex` | Show only annexed files | `false` |
| `--git` | Show only git-tracked files | `false` |
| `--untracked` | Show only untracked files | `false` |
| `--present` | Show only annexed files present locally | `false` |
| `--missing` | Show only annexed files not present locally | `false` |
| `--locked` | Show only locked annexed files (read-only symlinks) | `false` |
| `--unlocked` | Show only unlocked annexed files (editable copies) | `false` |

#### Locate Specific Paths

```bash
exo locate data/
exo locate data/train.parquet models/
```

#### JSON Output

```bash
exo locate --json
```

Each line is a JSON object with `file`, `type`, and (for annexed files) `key`, `size`, `present`, `locked`, and `remotes` fields.

#### Fast Mode

For large repositories, skip remote queries to get a quick classification:

```bash
exo locate --fast
```

This reports only whether each file is git-tracked, annexed, or untracked — without contacting remotes.

#### Filtering by Type

Filter flags narrow the output to specific file types. Multiple type flags combine as OR:

```bash
# Only annexed files
exo locate --annex

# Annexed files not present locally (need to sync)
exo locate --missing

# Everything except annex (git-tracked and untracked)
exo locate --git --untracked

# Annexed files present locally in a specific directory
exo locate --present data/

# Only locked annexed files (read-only symlinks)
exo locate --locked

# Only unlocked annexed files (editable copies)
exo locate --unlocked

# Locked annexed files that are present locally
exo locate --locked --present
```

`--present` and `--missing` imply `--annex` and require full mode (they cannot be combined with `--fast`). The two flags are mutually exclusive.

`--locked` and `--unlocked` also imply `--annex` and compose with `--present`/`--missing` to further narrow results. They are mutually exclusive with each other.

### Migrating File Tracking

Use `exo add --force` with `--annex` or `--git` to migrate files between tracking modes without removing and re-adding them:

```bash
# Move git-tracked files to git-annex
exo add --annex --force images/

# Move annexed files back to git
exo add --git --force config.yaml
```

The `--force` flag requires either `--annex` or `--git` to specify the migration direction. Files not eligible for migration (e.g., already in the target mode) are added normally.

## Remotes Overview

ExoHub supports several remote types for different storage and sharing scenarios:

| Type | Backend | Use Case | Import | Export |
|------|---------|----------|--------|--------|
| `annex` | S3 / rsync | Primary content-addressed storage | ✓ | — |
| `export` | S3 | Human-readable layout in S3 for direct access | — | ✓ |
| `import` | S3-compatible | Pull data from external sources (e.g., MinIO) | ✓ | — |
| `exospace` | ExoSpace | Geo-localized regional cache for faster access | ✓ | ✓ |
| `drive` | Google Drive | Share datasets with Google Workspace collaborators | — | ✓ (v1) |
| `artifactdb` | ArtifactDB | Catalog publishing and metadata indexing | ✓ | ✓ |

**Choosing a remote:**

- **Primary storage** → `annex` (content-addressed, all operations)
- **Human-readable S3** → `export` (direct download links, no git-annex required)
- **Google Workspace sharing** → `drive` (collaborators use Drive UI, no S3 access needed)
- **Regional performance** → `exospace` (geo-caching for distributed teams)
- **Catalog and discovery** → `artifactdb` (metadata indexing, ArtifactDB integration)

For Drive remote setup, see [Google Drive Remote]({{< ref "drive-remote" >}}).

## Configuration Files

### `.exohub/remotes`

Defines storage backends for your data:

```yaml
remotes:
  - name: s3-archive
    type: annex
    url: s3://my-bucket/datasets/mnist

  - name: s3-export
    type: export
    url: s3://my-bucket/datasets/mnist/export

  - name: s3-grants
    type: annex
    url: s3://my-bucket/datasets/mnist
    grants: true

  - name: catalog
    type: artifactdb
    mode: export
    s3url: s3://my-bucket/datasets/mnist
    instance_url: https://artifactdb.example.com
    publish_on: tag

  - name: minio-import
    type: import
    s3url: s3://local-bucket/data
    host: localhost
    port: "9000"
    protocol: http

  - name: my-drive
    type: drive
    drive_path: /My Drive/datasets/gwasdb
    tracking_branch: main
    include:
      - gwasdb-studies/**/*.parquet
```

> **`grants: true`** — When set, `exo init` provisions S3 Access Grants for this remote. See [Permissions & Access Control]({{< ref "permissions" >}}) for details on configuring owners, viewers, and access levels.

Remote types:
- `annex` — Content-addressed storage (S3 or rsync backend)
- `export` — Human-readable file layout with original filenames
- `import` — Pull data from external sources
- `exospace` — Geo-localized caching layer for faster regional access
- `artifactdb` — Catalog remote for ArtifactDB publishing (requires `mode: export` or `mode: import`)
- `drive` — Export-only Google Drive remote for sharing with Google Workspace collaborators

Import remotes support optional `host`, `port`, and `protocol` fields for custom S3-compatible endpoints (e.g., MinIO).

Catalog remote fields:

| Field | Description |
|-------|-------------|
| `mode` | Required: `export` or `import` |
| `instance_url` | ArtifactDB instance URL |
| `publish_on` | When to publish: `tag` (only on tags) or `always` (default) |
| `project_id` | Custom project ID override (defaults to repo name) |

### `.exohub/presets`

Defines clone presets for users:

```yaml
presets:
  - name: data-only
    description: "Just the data files"
    sparse_paths:
      - "data/"

  - name: full
    description: "Everything"
    # No sparse_paths means clone the entire repository
```

### `.exohub/config`

Optional. Shared repository settings:

```yaml
annex:
  addunlocked: true  # Unlock annexed files automatically (default: true)
  thin: true         # Use hard links to save disk (see Advanced Operations)
```

> **Note:** In previous versions, `thin` was a top-level key (and before that, in `.exohub/context`). Starting with v0.83.0, annex settings live under the `annex:` key. The old top-level `thin: true` format is deprecated.

### `.exohub/permissions`

Controls S3 Access Grants for remotes with `grants: true`. See [Permissions & Access Control]({{< ref "permissions" >}}) for the full reference, grant matrix, and common workflows.

### `.exohub/bundle`

Defines presets for the `exo bundle` command. Created automatically during `exo init`.

```yaml
presets:
  - name: default
    scope: all
    # include_hidden: false
    # include:
    #   - "**/*"
    # exclude:
    #   - "*.tmp"
```

Hidden directories (paths starting with `.`) are excluded by default. The `.exohub/` directory is always excluded.

Preset fields:

| Field | Description |
|-------|-------------|
| `name` | Preset name (used with `--preset` flag) |
| `scope` | `all`, `git`, or `annex` — controls output categories |
| `include` | Glob patterns for files to include |
| `exclude` | Glob patterns for files to skip |
| `include_hidden` | Include files in hidden directories (default: `false`) |
| `incremental` | Only generate artifacts for files modified in the commit range (default: `false`) — equivalent to `--incremental` flag |

## Example: Full Clone Workflow

Let's put it all together with the MNIST tutorial dataset:

```bash
# Clone the repository
exo clone {{< exohub "cloneExample" >}}

# Enter the directory
cd mnist

# Initialize remotes
exo init

# Check what's configured
exo info

# Now you're ready to sync data (next section)
```

## Next Steps

Your repository is set up. Let's [sync some data]({{< ref "syncing" >}}).
