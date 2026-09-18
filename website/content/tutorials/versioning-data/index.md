---
title: "Versioning Your Data"
description: "Learn how to use branches, tags, and Git history to version your datasets and reproduce experiments with ExoHub"
---

This tutorial shows how to version a dataset with `exo` and Git: tag releases, update data on a
branch, inspect history, and reproduce an experiment from a specific data snapshot.

**Prerequisite:** Complete the [Building a MNIST Dataset]({{< ref "tutorials/mnist" >}}) tutorial
first, or clone an existing dataset and run `exo init` to get started. You should have a repository
with at least one commit and synchronized data.

## 1. Tag the Initial Release

Once your MNIST dataset is in a good state, mark it as `v1.0.0` so you can always come back to
this exact snapshot:

```bash
git tag -a v1.0.0 -m "Initial MNIST dataset: training and test sets in Parquet format"
git push --tags
```

List your tags at any time:

```bash
git tag
# v1.0.0
```

> For a versioning naming convention reference, see
> [Versioning & Collaboration]({{< ref "docs/user-guide/versioning" >}}).

## 2. Create a Branch to Update Data

Suppose you want to add a normalised version of the training set. Work on a branch to keep `main`
stable while you make the change:

```bash
git checkout -b add-normalized-data
```

Generate the new file (for example, with a short Python script):

```python
# scripts/normalize.py
import pandas as pd
import numpy as np

df = pd.read_parquet("data/processed/train.parquet")
pixel_cols = [c for c in df.columns if c != "label"]
df[pixel_cols] = df[pixel_cols] / 255.0
df.to_parquet("data/processed/train_normalized.parquet", index=False)
print("Saved data/processed/train_normalized.parquet")
```

```bash
python scripts/normalize.py
# Saved data/processed/train_normalized.parquet
```

Track the new file with `exo` and commit:

```bash
exo add data/processed/train_normalized.parquet
git commit -m "feat(data): add pixel-normalized training set"
```

Sync the new file to the remote and broadcast its metadata:

```bash
exo sync
exo broadcast
```

Push the branch:

```bash
git push -u origin add-normalized-data
```

## 3. Merge Back to Main

Once you're satisfied with the change, merge it back:

```bash
git checkout main
git merge add-normalized-data
git push
```

Tag the new release:

```bash
git tag -a v1.1.0 -m "Add normalized training set (pixel values scaled to [0, 1])"
git push --tags
```

Your repository now has two tags:

```bash
git tag
# v1.0.0
# v1.1.0
```

## 4. Inspect What Changed Between Versions

Use `git log` to see commits between the two tags:

```bash
git log --oneline v1.0.0..v1.1.0
# a3f8c12 feat(data): add pixel-normalized training set
```

Inspect exactly which files changed in that commit with `git show --stat`:

```bash
git show --stat a3f8c12
# commit a3f8c12...
# Author: Your Name <you@example.com>
# Date:   ...
#
#     feat(data): add pixel-normalized training set
#
#  data/processed/train_normalized.parquet | 1 +
#  1 file changed, 1 insertion(+)
```

You can also diff the pointers between tags directly:

```bash
git diff v1.0.0 v1.1.0 --stat
# data/processed/train_normalized.parquet | 1 +
# 1 file changed, 1 insertion(+)
```

## 5. Reproduce an Experiment with a Specific Version

A colleague tells you they trained a model using data version `v1.0.0` and wants you to reproduce
their results. Checkout that tag and pull the corresponding data:

```bash
git checkout v1.0.0
exo init     # re-configure remotes if needed (safe to re-run)
exo sync
```

You are now in a [detached HEAD](https://git-scm.com/docs/git-checkout) state — your working
directory contains exactly the files that existed at `v1.0.0`. Verify:

```bash
ls data/processed/
# test.parquet  train.parquet
```

Note that `train_normalized.parquet` is absent — it only exists from `v1.1.0` onwards. Now run
your training script with confidence that the inputs match your colleague's exactly:

```bash
python scripts/train.py --data data/processed/train.parquet
```

When you're done, return to the latest version:

```bash
git checkout main
exo sync
```

> **Tip:** Pin the data version used in an experiment by recording the tag or commit SHA in your
> notebook or experiment tracking system (e.g. MLflow, W&B). A one-liner that captures the current
> commit:
>
> ```python
> import subprocess
> data_version = subprocess.check_output(["git", "describe", "--tags"]).decode().strip()
> # => "v1.1.0"
> ```

## What You Learned

- `git tag -a` marks a stable snapshot of your dataset; use `git push --tags` to share it.
- Branches isolate data changes during development; merge back to `main` when ready.
- `git log v1.0.0..v1.1.0` and `git show --stat` reveal exactly what changed between releases.
- `git checkout <tag> && exo sync` reproduces any historical version of the data locally.

## What's Next

- For a full reference on branches, merge requests, and release strategies, see
  [Versioning & Collaboration]({{< ref "docs/user-guide/versioning" >}}).
- To sync large datasets or run periodic jobs, see
  [Running Jobs at Scale]({{< ref "docs/user-guide/jobs-at-scale" >}}).
