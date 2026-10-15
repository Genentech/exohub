---
title: "Atlas & Downloads"
description: "Browse the catalog and download artifacts"
weight: 10
---

ExoHub Atlas is an interactive terminal UI for browsing and searching an ArtifactDB catalog. It is also available as a web application at [`/atlas`]({{< baseurl >}}/atlas). The `exo download` command lets you download artifact files directly from the catalog without opening the TUI.

## Browsing with Atlas

Launch the interactive browser:

```bash
exo atlas
```

This opens a TUI where you can search, filter, and explore artifacts in the catalog. You can also open a specific document directly:

```bash
exo atlas "myproject:file.txt@v1"
```

### Search Results

Each search result displays:

- **Project@version badge** — the project name and version
- **Access icon** — 🌍 (public), 🔒 (private), or 🙈 (hidden)
- **Date and schema** — upload timestamp and document type

### Search Highlights

Press `h` to toggle the highlights pane, which shows where your search terms matched within each result. Matched terms are displayed in yellow. The pane auto-closes when no highlights are available.

### Web App

In the web app, Atlas supports mouse interaction:

- **Click** search results, dialog buttons, and toolbar items directly
- **Select mode** — press `v` or click "Select mode" in the toolbar to toggle text selection
- **Browser navigation** — use the browser back/forward buttons to navigate between search results and documents
- **Theme & font size** — use the toolbar dropdown to switch themes and the S/M/L buttons to adjust font size
- **Download limit** — browser downloads are capped at 20 files; for larger sets, use `exo download` from the CLI

### Options

| Flag | Description | Default |
|------|-------------|---------|
| `--url <url>` | ArtifactDB instance URL | Auto-resolved (see below) |
| `-o, --output-dir <dir>` | Output directory for downloaded files | `.` (current directory) |
| `-q, --query <query>` | Pre-populate the search query | (none) |
| `--project <id>` | Filter results by project ID | (none) |
| `--version <version>` | Filter by version (e.g. `v1.0.0` or `latest`) | (none) |
| `--schema <schema>` | Filter by schema (e.g. `exohub-artifact/v1`) | (none) |
| `--sort <field>` | Sort order (prefix `-` for descending) | `-_extra.uploaded` |
| `--size <n>` | Number of results per page | `10` |
| `--latest` | Show only latest versions | `true` |
| `--download-ui` | Download UI assets without confirmation | `false` |

### Catalog URL Resolution

The catalog URL is resolved in this order:

1. `--url` flag
2. `EXOHUB_CATALOG_URL` environment variable
3. `catalog_url` field in the active exo context
4. Default catalog URL

To set the catalog URL persistently, use the `EXOHUB_CATALOG_URL` environment variable:

```bash
export EXOHUB_CATALOG_URL={{< exohub "catalogURL" >}}
```

Alternatively, edit your context YAML file (e.g., `~/.config/exo/contexts/mycontext.yaml`) and add:

```yaml
catalog_url: {{< exohub "catalogURL" >}}
```

### Examples

```bash
# Browse the default catalog
exo atlas

# Browse a specific catalog
exo atlas --url {{< exohub "catalogURL" >}}

# Open a specific document
exo atlas "mnist:data/processed/train.parquet@v0.1.0"

# Download files to a specific directory
exo atlas -o ./downloads

# Search for parquet files in a specific project
exo atlas -q "*.parquet" --project mnist

# Show only latest versions, sorted by upload date
exo atlas --latest --sort "-_extra.uploaded"
```

## Downloading Artifacts

Use `exo download` to download artifact files from the command line without the interactive TUI:

```bash
exo download <artifact-id | project@version>
```

### Download a Single File

```bash
exo download "myproject:file.txt@v1"
```

### Download All Files in a Project Version

```bash
exo download "myproject@v1"
```

This lists all files in the project at the given version and downloads them, showing progress with file counts and sizes.

### Options

| Flag | Description | Default |
|------|-------------|---------|
| `--url <url>` | ArtifactDB catalog URL | Auto-resolved (same as atlas) |
| `-o, --output-dir <dir>` | Output directory for downloaded files | `.` (current directory) |
| `-f, --force` | Re-download even if file already exists with matching size | `false` |

### Examples

```bash
# Download a single artifact
exo download "mnist:data/processed/train.parquet@v0.1.0"

# Download all files in a project version
exo download "mnist@v0.1.0"

# Force re-download existing files
exo download "mnist@v0.1.0" --force

# Download to a specific directory
exo download "mnist@v0.1.0" -o ./data
```

## Remote Download via `--eds`

By default, `exo download` fetches files to this machine. With `--eds`, it instead
submits the download to a remote ExoHub Data Service (EDS) and the **EDS** fetches
the dataset into its own storage — nothing is written locally. The CLI only submits
the request and follows its progress.

```bash
exo download --eds <eds-id-or-url> <project | project@version>
```

`--eds` accepts either:

- An **index id**, resolved via the ExoHub `/eds` index (e.g. `my-eds`)
- A full **`https://` URL** to the EDS itself

Only datasets (`project` or `project@version`) are accepted — artifact ids (e.g.
`myproject:file.txt@v1`) are rejected, since EDS downloads operate on whole datasets.
`--eds` is also mutually exclusive with `-o`/`--output-dir` and `-f`/`--force`, since
those only apply to local downloads.

### Options

| Flag | Description | Default |
|------|-------------|---------|
| `--eds <id-or-url>` | Data Service to download to (index id or `https://` URL) | _(none — local download)_ |
| `--scope <scope>` | Scope for the remote download (e.g. `user`, `global`) | _(none — EDS applies its own default)_ |
| `--no-wait` | Submit the download and exit without following progress | `false` |
| `--max-wait <duration>` | Maximum time to follow the remote download before giving up (the download keeps running on the EDS regardless) | `6h` |
| `--poll-interval <duration>` | How often to poll the operation status | `10s` |

> **Note:** Ctrl-C does not cancel the remote download. The download keeps running on the EDS; the status URL is printed so it can be followed separately.

### Examples

```bash
# Submit a dataset download to the "my-eds" EDS and follow its progress
exo download --eds my-eds myproject@v1

# Target an EDS directly by URL
exo download --eds https://eds.example.com myproject

# Submit and return immediately without following progress
exo download --eds my-eds myproject@v1 --no-wait
```

## Environment Variables

See [Environment Variables]({{< ref "environment-variables" >}}) for the full list. The most relevant for Atlas is `EXOHUB_CATALOG_URL`.
