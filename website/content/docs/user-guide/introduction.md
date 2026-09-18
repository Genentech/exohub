---
title: "Introduction"
description: "What is ExoHub and how does it work"
weight: 1
---

ExoHub brings Git-style version control to your datasets. Just like you version code with Git, ExoHub lets you version, share, and collaborate on data — with full history, branching, and merge capabilities.

## Why ExoHub?

If you've ever struggled with:

- "Which version of this dataset did I use for that experiment?"
- "Someone updated the data — how do I get the latest?"
- "This file is 50GB, I can't push it to Git"
- "The data is on S3, but I need it locally for analysis"

Then ExoHub is for you.

## How It Works

ExoHub is built on [git-annex](https://git-annex.branchable.com/), a powerful tool that extends Git to handle large files. The `exo` CLI wraps git-annex with a simpler interface designed for data scientists.

Here's the key insight: **Git tracks pointers, storage backends hold the actual data.**

```
┌─────────────────────────────────────────────────────────┐
│                    Your Repository                       │
├─────────────────────────────────────────────────────────┤
│  README.md          ← regular file (in Git)             │
│  notebooks/         ← regular files (in Git)            │
│  data/train.parquet ← pointer (in Git) → actual data    │
│  data/test.parquet  ← pointer (in Git) → actual data    │
└─────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────┐
│                   Storage Backends                       │
├─────────────────────────────────────────────────────────┤
│  S3 bucket     │  On-prem HPC    │  Your laptop         │
│  (primary)     │  (cache)        │  (working copy)      │
└─────────────────────────────────────────────────────────┘
```

When you clone a repository, you get the structure and metadata immediately. The actual data files are downloaded on-demand when you run `exo sync`.

## Key Concepts

### Repositories

An ExoHub repository is a Git repository with git-annex enabled. It contains:

- **Regular files** — code, notebooks, configs (tracked by Git)
- **Annexed files** — large data files (tracked by git-annex)
- **`.exohub/` directory** — ExoHub configuration (remotes, presets, permissions)

### Remotes

Remotes are storage locations where your data lives. ExoHub supports several types:

| Type | Description | Example |
|------|-------------|---------|
| **annex** | Content-addressed storage | S3 bucket with git-annex layout |
| **export** | Human-readable file layout | S3 bucket with original filenames |
| **import** | Pull data from external sources | Existing S3 bucket |
| **exospace** | Geo-localized caching layer | Local or regional cache for faster access |
| **artifactdb** | Catalog publishing remote | ArtifactDB instance with S3 storage |

Most repositories have at least one `annex` remote for primary storage.

### Syncing

Syncing is the process of transferring data between your local machine and remotes:

- **Sync** — `exo sync` performs bidirectional synchronization, downloading files you don't have locally and uploading files the remote doesn't have
- **Export** — `exo export` copies data to an export remote with original filenames

### Clone Presets

Large datasets often contain more than you need. Clone presets let you download just the parts you want:

```bash
# Clone auto-discovers presets and shows selection
exo clone https://github.com/org/dataset.git

# Use a specific preset directly
exo clone --preset notebooks https://github.com/org/dataset.git
```

Presets are defined in `.exohub/presets` and can specify which directories to include (sparse checkout).

## The exo CLI

The `exo` command is your interface to ExoHub. Here are the commands you'll use most often:

| Command | What it does |
|---------|--------------|
| `exo login` | Authenticate with your credentials |
| `exo clone` | Clone a dataset repository |
| `exo init` | Initialize a new repository or configure remotes |
| `exo sync` | Bidirectional data synchronization with remotes |
| `exo get` | Download specific files without a full sync |
| `exo add` | Stage files with automatic text/binary routing |
| `exo broadcast` | Share metadata updates with remotes (no data transfer) |
| `exo context` | Manage git host configurations (create, list, show, update, delete) |
| `exo info` | Show repository and remote information |
| `exo bundle` | Generate JSON metadata bundles for catalog ingest |
| `exo atlas` | Browse and search an ArtifactDB catalog (interactive TUI) |
| `exo download` | Download artifact files from the catalog |
| `exo locate` | Show where files are stored across remotes |
| `exo theme` | Customize terminal color theme and dark/light mode |
| `exo mcp` | Start the MCP server for AI assistant integration |
| `exo safe` | Manage SSH keys and git tokens in ExoSafe |
| `exo tip` | Show a random tip from the ExoHub tip bank |

We'll cover each of these in detail throughout this guide.

## Next Steps

Ready to install the CLI? Continue to [Installation & Setup]({{< ref "installation" >}}).
