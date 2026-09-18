---
title: "Exploring the Catalog with Atlas"
description: "Learn how to discover, browse, and download datasets using exo atlas and exo download"
---

This tutorial walks you through discovering datasets published to ExoHub using **Atlas** — the interactive catalog browser available as a terminal UI and a web app. You will search for datasets, inspect documents, and download artifact files without cloning a full repository.

## Prerequisites

Install `exo` and authenticate:

```bash
{{< exohub "installCmd" >}}
export PATH="$HOME/.local/bin:$PATH"
exo login
```

Verify everything works:

```bash
exo version
```

## 1. Launch Atlas

Open the interactive catalog browser:

```bash
exo atlas
```

Atlas connects to the default catalog and opens a full-screen TUI in your terminal. You will see a search bar at the top, a list of results in the middle, and a status bar at the bottom.

To target a specific catalog instance:

```bash
exo atlas --url {{< exohub "catalogURL" >}}
```

You can also set the catalog URL once in your context so you don't have to repeat the flag:

```bash
exo context update --catalog-url {{< exohub "catalogURL" >}}
exo atlas
```

## 2. Search for Datasets

Type in the search bar to run a full-text search across all indexed artifacts. Results appear immediately as you type.

**Example:** search for MNIST datasets by typing `mnist`:

```
> mnist
```

Each result shows:
- **Project@version badge** — the project name and version (e.g., `mnist@v0.1.0`)
- **Access icon** — 🌍 public, 🔒 private, or 🙈 hidden
- **Date and schema** — upload timestamp and document type

Use `↑` / `↓` (or `j` / `k`) to move through the results list and `Enter` to open a document.

## 3. Use Filters

Narrow results using flags when launching Atlas:

```bash
# Only show results from a specific project
exo atlas --project mnist

# Only show latest versions
exo atlas --latest

# Filter by schema type
exo atlas --schema "exohub-artifact/v1"

# Combine filters — latest parquet files in the mnist project
exo atlas --project mnist --latest -q "*.parquet"
```

Filters can also be combined with a free-text query via `-q`:

```bash
exo atlas -q "train" --project mnist --latest
```

## 4. Inspect Search Highlights

After searching, press `h` to toggle the **highlights pane**. This pane shows exactly where your search terms matched inside each result — matched terms are highlighted in yellow.

The highlights pane closes automatically when a selected result has no matches to display.

## 5. Browse a Document

Press `Enter` on any search result to open the document view. This shows the full artifact metadata, including:
- File path and version
- Schema information
- Upload timestamp
- Any rendered markdown (for `README.md` and similar files)

Use `Esc` or `q` to go back to the results list.

To open a known document directly without searching first:

```bash
exo atlas "mnist:data/processed/train.parquet@v0.1.0"
```

Press `y` to copy a shareable link to the document to your clipboard.

## 6. Download Files from the TUI

While viewing a document or navigating results, you can download files directly. Atlas will save them to the current directory by default.

To download to a specific directory, launch Atlas with `-o`:

```bash
exo atlas -o ./downloads
```

> **Web mode note:** browser downloads are capped at 20 files per session. For larger sets, use `exo download` from the CLI (see below).

## 7. Download Artifacts from the CLI

Use `exo download` to download files without opening the TUI — useful for scripting or downloading entire project versions at once.

### Download a single file

```bash
exo download "mnist:data/processed/train.parquet@v0.1.0"
```

Expected output:

```
Downloading mnist:data/processed/train.parquet@v0.1.0 ... done (18.2 MB)
```

### Download all files in a project version

```bash
exo download "mnist@v0.1.0"
```

This lists every artifact in the version and downloads them all, showing progress with file counts and sizes:

```
Downloading mnist@v0.1.0 (6 files) ...
  [1/6] data/raw/train-images-idx3-ubyte.gz     9.9 MB  ✓
  [2/6] data/raw/train-labels-idx1-ubyte.gz    29 KB   ✓
  [3/6] data/raw/t10k-images-idx3-ubyte.gz     1.6 MB  ✓
  [4/6] data/raw/t10k-labels-idx1-ubyte.gz     5 KB    ✓
  [5/6] data/processed/train.parquet          18.2 MB  ✓
  [6/6] data/processed/test.parquet            3.8 MB  ✓
Done. 6 files, 33.5 MB total.
```

### Download to a specific directory

```bash
exo download "mnist@v0.1.0" -o ./data
```

### Force re-download

By default, `exo download` skips files that already exist locally with the same size. Use `--force` to re-download regardless:

```bash
exo download "mnist@v0.1.0" --force
```

For the full `exo download` reference, see {{< ref "docs/user-guide/atlas-downloads" >}}.

## 8. Use the Web Version

Atlas is also accessible from any web browser at the `/atlas` path of your ExoHub instance (no CLI required):

```
{{< exohub "appBaseURL" >}}/atlas
```

The web version supports **mouse interaction** — you can click search results, toolbar buttons, and dialog actions directly.

Additional web-only features accessible from the toolbar:

| Control | Description |
|---------|-------------|
| **Theme dropdown** | Switch between light, dark, and other color themes |
| **S / M / L buttons** | Adjust the terminal font size |
| **Select mode** (`v` or toolbar) | Toggle text-selection mode for copy-paste |

The browser's back/forward buttons work as expected — use them to navigate between search results and previously opened documents.

## Keyboard Reference

| Key | Action |
|-----|--------|
| `↑` / `↓` or `j` / `k` | Move through results |
| `Enter` | Open selected document |
| `Esc` / `q` | Go back / close |
| `h` | Toggle highlights pane |
| `y` | Copy document link to clipboard |
| `v` | Toggle text-selection mode (web) |
| `/` | Focus the search bar |

## What's Next

- **Clone a dataset** — once you've found what you need, use `exo clone` to get the full repository with version history. See the [MNIST tutorial](/tutorials/mnist/) for a complete example.
- **Publish your own data** — see the [MNIST tutorial](/tutorials/mnist/) to learn how to push a dataset to the catalog so others can discover it with Atlas.
- **Atlas reference** — for all flags and options, see {{< ref "docs/user-guide/atlas-downloads" >}}.
