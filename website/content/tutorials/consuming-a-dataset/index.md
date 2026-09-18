---
title: "Consuming a Dataset"
description: "Learn how to clone, filter, and work with an existing ExoHub dataset using presets and selective sync"
---

This tutorial walks you through consuming an existing dataset as an end-user. Rather than building a dataset from scratch (covered in the [MNIST tutorial]({{< ref "tutorials/mnist" >}})), you will clone the published MNIST dataset, download only what you need, verify file provenance, and load the data for analysis.

## Prerequisites

Run the installer (Linux/macOS):

```bash
{{< exohub "installCmd" >}}
```

Add `~/.local/bin` to your PATH if needed:

```bash
export PATH="$HOME/.local/bin:$PATH"
```

Verify the installation:

```bash
exo version
```

Authenticate with ExoHub:

```bash
exo login
```

## 1. Clone the Dataset

The MNIST dataset is published at:



- **HTTPS**: `https://github.com/genentech/exohub-data-tutorials/mnist.git`
- **SSH**: `git@github.com:genentech/exohub-data-tutorials/mnist.git`


Clone it with `exo clone`:

```bash
exo clone {{< exohub "cloneExample" >}}
```

When a repository defines presets, `exo clone` automatically detects them and offers a selection menu:

```
Available presets:
  1. full-clone   - Complete repository with everything
  2. data-only    - Just the processed data for quick experimentation

Select a preset [1-2]:
```

### Using a Specific Preset

If you already know which preset you want, pass it directly with `--preset` to skip the menu:

```bash
exo clone --preset data-only {{< exohub "cloneExample" >}}
```

The `data-only` preset uses [sparse checkout]({{< ref "docs/user-guide/repositories" >}}#clone-presets) to fetch only the `data/` directory — ideal when you do not need the raw files or preprocessing scripts.

```bash
cd mnist
ls
# data/  README.md
```

> **Tip:** Use `--dry-run` to see what a clone would do before committing:
> ```bash
> exo clone --preset data-only --dry-run {{< exohub "cloneExample" >}}
> ```

## 2. Initialize the Repository

After cloning, initialize the remotes so ExoHub knows where to fetch the data from:

```bash
cd mnist
exo init
```

`exo init` reads the remote configuration from `.exohub/remotes` and sets up git-annex with the appropriate S3 backends. See [Working with Repositories]({{< ref "docs/user-guide/repositories" >}}#initializing-a-repository) for details on what initialization does.

Confirm everything is in order:

```bash
exo info
```

Expected output (abbreviated):

```
Data Repo: {{< exohub "cloneExample" >}}

Clone Preset
  Name: data-only

Configured remotes
┏━━━━━━━━━━━━━┳━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┓
┃ name        ┃ uuid                                 ┃
┣━━━━━━━━━━━━━╋━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┫
┃   s3-annex  ┃ 0d80d418-7b86-421a-9690-e01b0910b317 ┃
┃ ♥ s3-export ┃ bba7ca80-6235-4d2c-9bb0-18cc25d25c62 ┃
┗━━━━━━━━━━━━━┻━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┛
```

## 3. Download the Data

### Option A — Sync Everything

To download all content in the current sparse checkout, run:

```bash
exo sync
```

This fetches every file pointer available in your checkout from the configured remotes. For a `data-only` clone, that means the processed Parquet files:

```bash
ls data/processed/
# test.parquet   train.parquet
```

### Option B — Cherry-pick Specific Files with `exo get`

When you only need a few files, `exo get` is more efficient than a full sync — it downloads exactly what you specify without touching anything else:

```bash
# Get a single file
exo get data/processed/train.parquet

# Get an entire directory
exo get data/processed/

# Get from a specific remote
exo get --from s3-annex data/processed/train.parquet
```

Use `exo locate` to see what is already present locally and what still needs to be downloaded:

```bash
exo locate data/
```

```
annex      data/processed/train.parquet    *here, 📦 s3-annex
annex      data/processed/test.parquet     📦 s3-annex
```

Files marked `*here` are already on disk; others can be fetched with `exo get`.

See [Syncing Data]({{< ref "docs/user-guide/syncing" >}}#downloading-specific-files-with-exo-get) for the full `exo get` reference.

## 4. Verify Provenance

ExoHub tracks which remotes hold each file. Use `exo locate` to inspect where a file lives:

```bash
exo locate data/processed/train.parquet
```

```
annex      data/processed/train.parquet    *here, 📦 s3-annex
```

The output tells you the file is present locally (`*here`) and also stored on the `s3-annex` remote — meaning it is safely backed up and can be re-downloaded at any time.

To check all files at once and spot anything that hasn't been downloaded yet:

```bash
exo locate data/
```

```
annex      data/processed/train.parquet    *here, 📦 s3-annex
annex      data/processed/test.parquet     *here, 📦 s3-annex
```

Files without `*here` are not yet on disk — fetch them with `exo get` (see the previous section).

For a machine-readable view (useful in scripts):

```bash
exo locate --json data/
```

See [Working with Repositories]({{< ref "docs/user-guide/repositories" >}}#locating-files) for the full `exo locate` reference.

## 5. Work with the Data

The processed Parquet files are ready to use with any DataFrame library.

### Python (pandas / pyarrow)

```python
import pandas as pd

train = pd.read_parquet("data/processed/train.parquet")
test  = pd.read_parquet("data/processed/test.parquet")

print(train.shape)   # (60000, 785)
print(test.shape)    # (10000, 785)

# First column is the label, remaining 784 are pixel values (28x28)
X_train = train.iloc[:, 1:].values / 255.0
y_train = train.iloc[:, 0].values
```

### Quick sanity check

```python
import numpy as np

print("Classes:", np.unique(y_train))
# Classes: [0 1 2 3 4 5 6 7 8 9]

print("Pixel range:", X_train.min(), "-", X_train.max())
# Pixel range: 0.0 - 1.0
```

## What's Next

- **Build your own dataset** — follow the [MNIST tutorial]({{< ref "tutorials/mnist" >}}) to learn how to create and publish a dataset from scratch.
- **Sync & versioning** — see [Syncing Data]({{< ref "docs/user-guide/syncing" >}}) to learn about `exo sync`, `exo export`, and `exo broadcast`.
- **Access control** — see [Permissions & Access Control]({{< ref "docs/user-guide/permissions" >}}) if you need to manage who can read your dataset.
