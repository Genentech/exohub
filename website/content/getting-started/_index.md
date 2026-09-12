---
title: "Getting Started"
description: "Get started with ExoHub"
---



## What is ExoHub?

ExoHub lets you manage datasets like code using Git. You get easy versioning, change tracking, and collaboration — the same workflow you use for source code, now for your data.

Under the hood, ExoHub uses [git-annex](https://git-annex.branchable.com/) to handle large files efficiently, but the `exo` CLI abstracts that complexity away.

## Install the CLI

Run the installer (Linux/macOS):

```bash
{{< exohub "installCmd" >}}
```

This downloads and installs `exo` along with its dependencies (`git-annex`, `s5cmd`) to `~/.local/bin`.

Add it to your PATH if needed:

```bash
export PATH="$HOME/.local/bin:$PATH"
```

Verify the installation:

```bash
exo version
```

## Authenticate



Before accessing datasets, authenticate with your credentials:


```bash
exo login
```



This opens a browser window for SSO authentication. Once complete, your credentials are cached locally for future use.


## Clone and Sync a Dataset

Let's walk through cloning the MNIST tutorial dataset — a classic "hello world" for machine learning.

### Clone the repository

```bash
exo clone {{< exohub "cloneExample" >}}
cd mnist
```

This downloads the repository metadata and structure, but not the actual data files yet. Large files are tracked as lightweight pointers until you need them.

### Initialize the repository

```bash
exo init
```

This sets up the local repository for data operations, configuring the necessary remotes and git-annex settings.

### Sync the data

```bash
exo sync
```

This downloads the actual data files from remote storage to your local machine. Depending on the dataset size, this may take a moment.

Once complete, your data is ready to use:

```bash
ls data/
```

## Next Steps

- [User Guide](/docs/user-guide/) — comprehensive guide to using ExoHub
- [Vision](/docs/vision/) — understand the "data as code" philosophy
- [Architecture](/docs/architecture/) — see how ExoHub components fit together
