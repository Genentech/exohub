<p align="center">
  <img src="docs/assets/exohub-logo.png" alt="ExoHub logo" width="120">
</p>

<h1 align="center">ExoHub</h1>

<p align="center"><em>Manage data as code.</em></p>

ExoHub versions, synchronizes, and governs large scientific datasets using Git-based
principles — track terabytes alongside a normal Git history without checking the data
into Git itself.

ExoHub builds on [`git-annex`](https://git-annex.branchable.com/): your repository holds
the dataset's Git history and metadata (which files exist, their sizes, where their
content lives), while file contents are stored in one or more backing remotes (S3,
rsync/`exospace` shares, Google Drive, ArtifactDB, and other git-annex remotes). Content
and metadata can be synced, exported, and broadcast between remotes so collaborators
fetch exactly the data they need.

📖 **Full documentation: https://genentech.github.io/exohub/**

## What's in this repository

This is the ExoHub monorepo:

| Component | Path | Description |
|-----------|------|-------------|
| **`exo` CLI** | `go/exo` | Command-line interface — orchestrates git-annex for versioned datasets (`init`, `pull`, `sync`, `copy`, `export`, `login`, `standalone`, `upgrade`). |
| **Credential helper** | `go/exo-credential-helper` | OIDC/AWS credential helper for authenticating to ExoHub-backed storage. |
| **git-annex remotes** | `go/git-annex-remote-{s5cmd,drive,artifactdb-export}` | Custom git-annex special remotes: fast S3 via s5cmd, Google Drive, and ArtifactDB export. |
| **`adb-standalone`** | `go/adb-standalone` | Single-binary standalone ArtifactDB catalog server (Go + SurrealDB) — an API-compatible subset of enterprise ArtifactDB for local/self-hosted cataloging. |
| **Documentation site** | `website/` | The Hugo site published to GitHub Pages. |

## Install

```sh
curl -fsSL https://github.com/Genentech/exohub/releases/latest/download/install.sh | sh
```

Installs `exo` and its helper binaries for your OS/architecture (SHA256-verified) into
`~/.local/bin` (or `/usr/local/bin` as root). Pass `--dest <dir>` for a custom location;
`--version exo/vX.Y.Z` to pin a release.

`git-annex` is not bundled — install it separately:
- macOS: `brew install git-annex`
- Debian/Ubuntu: `sudo apt-get install git-annex`
- Other: https://git-annex.branchable.com/install/

Other CLI prerequisites: `jq`, `yq`, `rsync`, `s5cmd`.

## Quick start

```sh
exo login                          # OIDC device flow — optional, for hosted ExoHub storage
exo init                           # set up a dataset repo from .exohub/context
exo pull --url <repo> --ref main
exo sync                           # sync content + metadata across remotes
```

See the [User Guide](https://genentech.github.io/exohub/docs/user-guide/) for declarative
remote configuration (`.exohub/remotes`), authentication, syncing, versioning, and running
jobs at scale.

See [Git/annex routing rules](docs/annex-routing.md) to configure how new files
are stored and inspect or deliberately reroute existing files.

## Standalone mode — a local catalog, no cloud required

Run a fully local ArtifactDB catalog stack — ideal for development, testing, or a private
catalog before migrating to a shared deployment:

```sh
exo standalone up --secret-key mysecret
```

On first run it offers to install the whole stack (SurrealDB + a versitygw S3 store +
`adb-standalone`) and brings up three local endpoints. Responses are byte-compatible with
the enterprise ArtifactDB API, so datasets round-trip cleanly. See
[Local Standalone Catalog](https://genentech.github.io/exohub/docs/user-guide/standalone/).

## Documentation

- **User Guide** — install, credentials, repositories, syncing, versioning, standalone:
  https://genentech.github.io/exohub/docs/user-guide/
- **Tutorials** — build/consume/share datasets, explore the catalog:
  https://genentech.github.io/exohub/tutorials/
- **Components & Architecture:** https://genentech.github.io/exohub/docs/
- **`adb-standalone` design notes:** [`docs/adb-standalone/`](docs/adb-standalone/)

## Contributing & Security

Contributions welcome — see [CONTRIBUTING.md](CONTRIBUTING.md).
Report vulnerabilities per [SECURITY.md](SECURITY.md).

## License

MIT — see [LICENSE](LICENSE). Copyright (c) 2026 Genentech, Inc.
