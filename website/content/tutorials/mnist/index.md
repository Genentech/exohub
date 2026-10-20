---
title: "Building a MNIST Dataset"
description: "Learn how to build a data repository with ExoHub using the MNIST dataset as an example"
---

This tutorial walks you through creating a complete data repository using `exo` and `git-annex`. You will download raw MNIST data, process it, record its provenance, and publish it to S3 storage.

## Prerequisites

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

The tutorial also uses python libraries:

```python
pip install pandas pyarrow numpy
```

## 1. Create and Initialize the Repository

Create a new directory for your dataset and initialize it using the `tutorials` profile:

```bash
mkdir mnist && cd mnist
exo login
exo init --profile tutorials
```

A **profile** is a server-authored scaffold fetched by name. It automatically fills in `.exohub/remotes` (with your unixID and folder name substituted in), `.exohub/permissions`, and the bundle — no forking, no manual editing required.

The `tutorials` profile provisions three remotes under `s3://exohub-sandbox-uat/tutorials/<your-unixid>/mnist/`:

- `s3-archive` — annex remote (full versioned history)
- `s3-export` — export remote (snapshot for direct S3 access)
- `exohub-atlas` — catalog remote (publishes the `_catalog`)

The `read_access` setting defaults to `authenticated`; the `exo init` prompt lets you choose between `public`, `authenticated`, or `viewers`.

To browse all available profiles interactively, run:

```bash
exo init --profile-select
```

## 2. Download Raw Data

Use `exo add` to register the original MNIST files. This downloads each file and records where it came from (via `git annex addurl` under the hood), so anyone can verify the data source later.

```bash
BASE_URL="https://github.com/golbin/TensorFlow-MNIST/raw/refs/heads/master/mnist/data"

exo add --file data/raw/train-images-idx3-ubyte.gz \
    "${BASE_URL}/train-images-idx3-ubyte.gz"

exo add --file data/raw/train-labels-idx1-ubyte.gz \
    "${BASE_URL}/train-labels-idx1-ubyte.gz"

exo add --file data/raw/t10k-images-idx3-ubyte.gz \
    "${BASE_URL}/t10k-images-idx3-ubyte.gz"

exo add --file data/raw/t10k-labels-idx1-ubyte.gz \
    "${BASE_URL}/t10k-labels-idx1-ubyte.gz"
```

You can verify provenance at any time with:

```bash
git annex whereis data/raw/train-images-idx3-ubyte.gz
```

Notice at the end, git-annex reports the original URL:

```
whereis data/raw/train-images-idx3-ubyte.gz (2 copies)
        00000000-0000-0000-0000-000000000001 -- web
        c4a18941-197f-4863-b647-032e12fc46d5 -- lelongs@ip-10-158-202-194:/tmp/mnist [here]

  web: https://github.com/golbin/TensorFlow-MNIST/raw/refs/heads/master/mnist/data/train-images-idx3-ubyte.gz
ok
```

## 3. Create the Preprocessing Script

Create the `scripts/preprocess.py` file that converts raw IDX files to Parquet format:

```bash
mkdir -p scripts
```

Save the following as `scripts/preprocess.py`:

```python
#!/usr/bin/env python3
"""
Convert raw MNIST IDX files to Parquet format for easier use.

Usage:
    python scripts/preprocess.py
"""

import gzip
import struct
import numpy as np
import pandas as pd
from pathlib import Path


def read_idx_images(filepath: Path) -> np.ndarray:
    """Read IDX image file and return numpy array."""
    with gzip.open(filepath, "rb") as f:
        magic, num_images, rows, cols = struct.unpack(">IIII", f.read(16))
        assert magic == 2051, f"Invalid magic number: {magic}"
        data = np.frombuffer(f.read(), dtype=np.uint8)
        return data.reshape(num_images, rows * cols)


def read_idx_labels(filepath: Path) -> np.ndarray:
    """Read IDX label file and return numpy array."""
    with gzip.open(filepath, "rb") as f:
        magic, num_labels = struct.unpack(">II", f.read(8))
        assert magic == 2049, f"Invalid magic number: {magic}"
        return np.frombuffer(f.read(), dtype=np.uint8)


def main():
    raw_dir = Path("data/raw")
    processed_dir = Path("data/processed")
    processed_dir.mkdir(parents=True, exist_ok=True)

    # Process training set
    print("Processing training set...")
    train_images = read_idx_images(raw_dir / "train-images-idx3-ubyte.gz")
    train_labels = read_idx_labels(raw_dir / "train-labels-idx1-ubyte.gz")

    train_df = pd.DataFrame(train_images, columns=[f"pixel_{i}" for i in range(784)])
    train_df["label"] = train_labels
    train_df.to_parquet(processed_dir / "train.parquet", index=False)
    print(f"  Saved {len(train_df)} samples to data/processed/train.parquet")

    # Process test set
    print("Processing test set...")
    test_images = read_idx_images(raw_dir / "t10k-images-idx3-ubyte.gz")
    test_labels = read_idx_labels(raw_dir / "t10k-labels-idx1-ubyte.gz")

    test_df = pd.DataFrame(test_images, columns=[f"pixel_{i}" for i in range(784)])
    test_df["label"] = test_labels
    test_df.to_parquet(processed_dir / "test.parquet", index=False)
    print(f"  Saved {len(test_df)} samples to data/processed/test.parquet")

    print("Done!")


if __name__ == "__main__":
    main()
```

## 4. Process the Data

Run the preprocessing script to convert the raw IDX files to Parquet format:

```bash
python scripts/preprocess.py
```

This reads the gzipped IDX files from `data/raw/` and produces:
- `data/processed/train.parquet` — 60,000 training samples (~18 MB)
- `data/processed/test.parquet` — 10,000 test samples (~3.8 MB)

Add the processed files to git-annex. Also add the script itself — it needs to be tracked so the next step can reference it as a plan:

```bash
exo add scripts/preprocess.py
exo add data/processed/
```

## 5. Record Provenance

Now that the raw files and the Parquet outputs are tracked, record how each one came to be with `exo provenance add`:

```bash
BASE_URL="https://github.com/golbin/TensorFlow-MNIST/raw/refs/heads/master/mnist/data"

# where each raw file comes from
for f in train-images-idx3-ubyte train-labels-idx1-ubyte t10k-images-idx3-ubyte t10k-labels-idx1-ubyte; do
  exo provenance add --generated data/raw/$f.gz --derived-from "$BASE_URL/$f.gz"
done

# how the Parquet files were produced
exo provenance add --generated data/processed/train.parquet \
  --used data/raw/train-images-idx3-ubyte.gz --used data/raw/train-labels-idx1-ubyte.gz \
  --plan scripts/preprocess.py
exo provenance add --generated data/processed/test.parquet \
  --used data/raw/t10k-images-idx3-ubyte.gz --used data/raw/t10k-labels-idx1-ubyte.gz \
  --plan scripts/preprocess.py
```

```text
Wrote and staged .exohub/provenance/20261019T210602Z-train-images-idx3-ubyte-55e84a.jsonld
Wrote and staged .exohub/provenance/20261019T210602Z-train-labels-idx1-ubyte-a6fb4c.jsonld
Wrote and staged .exohub/provenance/20261019T210602Z-t10k-images-idx3-ubyte-3b1d17.jsonld
Wrote and staged .exohub/provenance/20261019T210602Z-t10k-labels-idx1-ubyte-7a9872.jsonld
Wrote and staged .exohub/provenance/20261019T210606Z-train-297a94.jsonld
Wrote and staged .exohub/provenance/20261019T210607Z-test-5b40ed.jsonld
```

There's no `--agent` flag here: the tutorial already ran `exo login`, so each record is attributed to the person from your login token. Each `exo provenance add` writes a small, add-only JSON-LD record under `.exohub/provenance/` and stages it automatically — records identify files by content (an annex key or git blob/tree id), not by path, so they stay valid across renames.

## 6. Commit

The data files and the new provenance records were already staged by `exo add` and `exo provenance add`. Commit them together:

```bash
git commit -m "feat: initial MNIST dataset with provenance, processed to Parquet format for easy loading."
```

`exo provenance show` resolves a path to its content identifier at a git ref (`HEAD` by default), so it needs this commit to exist first. Inspect what you just recorded:

```bash
exo provenance show data/processed/train.parquet
```

A trimmed version of the output:

```text
Lineage for data/processed/train.parquet (urn:exohub:annex:SHA256E-s18107628--57f75a2c...):

- [Entity] data/processed/train.parquet (urn:exohub:annex:SHA256E-s18107628--57f75a2c...)
    wasDerivedFrom: data/raw/train-labels-idx1-ubyte.gz (...), data/raw/train-images-idx3-ubyte.gz (...)
    wasGeneratedBy: urn:exohub:activity:01a155fc-6559-753e-...
    source: asserted by urn:exohub:agent:person:alice (.exohub/provenance/20261019T210606Z-train-297a94.jsonld)

- [Entity] data/raw/train-images-idx3-ubyte.gz (urn:exohub:annex:SHA256E-s9912422--440fcabf...)
    wasDerivedFrom: https://github.com/golbin/TensorFlow-MNIST/raw/refs/heads/master/mnist/data/train-images-idx3-ubyte.gz
    wasGeneratedBy: urn:exohub:activity:01a155fc-552b-7e1f-...
    source: asserted by urn:exohub:agent:person:alice (.exohub/provenance/20261019T210602Z-train-images-idx3-ubyte-55e84a.jsonld)

- [Activity] urn:exohub:activity:01a155fc-6559-753e-...
    hadPlan: scripts/preprocess.py (urn:exohub:git:blob:dd3ca9c4...)
    used: data/raw/train-labels-idx1-ubyte.gz (...), data/raw/train-images-idx3-ubyte.gz (...)
    wasAssociatedWith: Alice (urn:exohub:agent:person:alice)
    source: asserted by urn:exohub:agent:person:alice (.exohub/provenance/20261019T210606Z-train-297a94.jsonld)

… (data/raw/train-labels-idx1-ubyte.gz, the [Person] node, and a "Commit history" section with the commit above follow the same pattern)
```

## 7. Sync Data with Remotes

Sync all annexed data to the remotes and broadcast the metadata:

```bash
exo sync
exo broadcast
```

This uploads the data files to the `s3-archive` remote, exports processed files to `s3-export`, and `exohub-atlas` publishes the catalog (`_catalog`) — all as configured by the `tutorials` profile. The published bundle now also carries `provenance.jsonld` (the compiled lineage graph) and `provenance.json` (the same data as flat JSON); each file artifact gains a `lineage` block — what generated it, what it used, and the records behind those claims.

We can verify the content of s3:

```bash
# => annex remote
$ aws s3 ls s3://exohub-sandbox-uat/tutorials/lelongs/mnist/_annex/
2026-03-21 00:37:38    1648877 SHA256E-s1648877-S1073741824-C1--8d422c7b0a1c1c79245a5bcf07fe86e33eeafee792b84584aec276f5a2dbc4e6.gz
2026-03-21 00:37:37   18165756 SHA256E-s18165756-S1073741824-C1--655b844dfda9e78639c8f05c1b46870fe960fad72cd186266e80871c18db2d26
2026-03-21 00:37:39      28881 SHA256E-s28881-S1073741824-C1--3552534a0a558bbed6aed32b30c495cca23d567ec52cac8be1a0730e8010255c.gz
2026-03-21 00:37:37    3813084 SHA256E-s3813084-S1073741824-C1--283480492754248db7ba676d8b4e2ba2eebeea15c8ddc4f7df175107f647273c
2026-03-21 00:37:38       4542 SHA256E-s4542-S1073741824-C1--f7ae60f92e00ec6debd23a6088c31dbd2371eca3ffa0defaefb259924204aec6.gz
2026-03-21 00:37:39    9912422 SHA256E-s9912422-S1073741824-C1--440fcabf73cc546fa21475e81ea370265605f56be210a4024d2ca8f203523609.gz


# => export remote
$ aws s3 ls s3://exohub-sandbox-uat/tutorials/lelongs/mnist/_export/
                           PRE .exohub/
                           PRE data/
                           PRE scripts/
2026-03-21 00:37:34       1210 README.md

$ aws s3 ls s3://exohub-sandbox-uat/tutorials/lelongs/mnist/_export/data/processed/
2026-03-21 00:37:34    3813084 test.parquet
2026-03-21 00:37:35   18165756 train.parquet
```

Notice the difference in the content between the remote type `annex` vs `export`:

- `annex`: contains all versions of the data files, hashed, similar to a git object. This provides
  the full history of all files in the dataset over time, and consitute a traceable backup.
- `export`: replicates files to the remote, like a snapshot in time. We loose the history and hash
  of the files, but the remote type allows to replicate files on s3 that can later be accessed
  directly from there (here, parquet files could be queried directly)

## 8. Verify

Test the full clone workflow from scratch:

```bash
cd /tmp
exo clone {{< exohub "cloneExample" >}}  # git + sparse checkout (no annex remotes yet)
cd mnist
exo init                                                      # wire up the git-annex remotes from .exohub/remotes
exo sync                                                      # fetch content from those remotes
ls data/processed/
```

You should see `train.parquet` and `test.parquet`.

The repository lives under your own `tutorials/<your-unixid>/mnist` path on S3, and can be cloned
by anyone with access.

## File Sizes Reference

| File | Size |
|------|------|
| `train-images-idx3-ubyte.gz` | 9.9 MB |
| `train-labels-idx1-ubyte.gz` | 29 KB |
| `t10k-images-idx3-ubyte.gz` | 1.6 MB |
| `t10k-labels-idx1-ubyte.gz` | 5 KB |
| `train.parquet` | ~18 MB |
| `test.parquet` | ~3.8 MB |

## What's Next

Your dataset is now published and can be cloned by anyone with access. Users can choose a [clone preset](/docs/clone-presets/) to download only the parts they need:

```bash
exo clone --preset data-only {{< exohub "cloneExample" >}}
```



To learn more about `exo provenance` — the record format, folder collections, and the agent workflow — see the exo-cli [provenance documentation](https://github.com/Genentech/exohub/blob/main/docs/provenance.md).


If your team wants to enforce lineage, require it with `provenance.require` in `.exohub/config` and install a pre-commit check with `exo provenance install-hook`.
