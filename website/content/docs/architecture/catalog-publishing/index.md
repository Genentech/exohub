---
title: "Catalog Publishing"
description: "How ExoHub publishes dataset metadata to external catalogs like ArtifactDB"
weight: 20
---

## Overview

ExoHub can publish structured metadata from data repositories to external **catalog systems** for indexing, search, and discovery. This bridges the gap between data-as-code (git + git-annex) and data catalogs that make datasets findable and accessible.

The architecture is **pluggable** — different catalog systems can be integrated by implementing a simple binary protocol, without modifying the `exo` CLI itself.

<img src="cataloging.png" alt="Metadata Catalog Publishing Architecture" class="clickable-image"/>

## Concepts

### Catalog Remotes

A **catalog remote** is a new category of remote in `.exohub/remotes` that targets a metadata catalog instead of a storage backend. Unlike `annex`, `export`, `import`, or `exospace` remotes that manage data files on S3 or local storage, catalog remotes publish metadata to indexing services.

```yaml
# .exohub/remotes
remotes:
  - name: s3-annex
    type: annex
    s3url: s3://bucket/dataset/_annex

  - name: my-catalog
    type: artifactdb                    # ← catalog remote (role: atlas, the default)
    mode: export                        # required for role: atlas: determines binary (git-annex-remote-artifactdb-export)
    s3url: s3://bucket/dataset/_catalog
    grants: true
    publish_on: tag

  - name: my-catalog-instance
    type: artifactdb
    role: instance                      # ← publishes .artifactdb/ to an ArtifactDB instance instead
    url: {{< exohub "catalogURL" >}}
    s3url: s3://bucket/dataset-project-id
```

Each catalog remote has a **role**: `role: atlas` (the default) publishes the bundle (`.exohub-bundle/`) to an Atlas; `role: instance` publishes the git-tracked `.artifactdb/` directory to its own bucket and asks the ExoHub API to forward the ingest to an ArtifactDB instance. The two roles are separate remotes, never combined on one entry — see [Catalog Remote Publishing]({{< ref "syncing#catalog-remote-publishing" >}}) in the user guide for the full model, including the `data_from` delegation field, required fields per role, and migrating a pre-v0.141.0 single-remote config (`atlas_url`/`instance_url`) to this role-split one.

The `type` and `mode` fields together determine which binary handles the publishing for a `role: atlas` remote. For `type: artifactdb` with `mode: export`, the CLI delegates to `git-annex-remote-artifactdb-export`. A future `mode: import` would use `git-annex-remote-artifactdb-import`. A `role: instance` remote takes no `mode` and is never a git-annex special remote — the ExoHub API forwards its ingest directly.

### The Publish Flow

Publishing is triggered by `exo sync --with <catalog-remote>`. Every `role: atlas` remote publishes first; only once its delegation Atlas has been accepted does any `role: instance` remote publish (skipped, with a clear message, if the Atlas publish failed):

```
exo sync --with my-catalog --with my-catalog-instance
  │
  ├→ role: atlas remote "my-catalog" (git-annex-remote-artifactdb-export publish)
  │    │
  │    ├─ 📊 Read bundle metadata from .exohub/bundles/<ref>/
  │    ├─ ☁️  Upload to S3 (s3url/<ref_name>/.exohub-bundle/)
  │    ├─ 🔒 Upload permissions.json
  │    ├─ 📡 Notify ExoHub API (POST /api/publish)
  │    │     └→ ExoHub forwards to the Atlas (default, or this remote's `url`)
  │    └─ ⚙️  Atlas triggers IngestWorkflow
  │           ├─ Read metadata from S3
  │           ├─ Index into search engine
  │           └─ Store pointer for re-indexing
  │
  └→ role: instance remote "my-catalog-instance" (only if the Atlas above succeeded)
       │
       ├─ ☁️  git-archive .artifactdb/ from the ref, upload to this remote's own S3 bucket
       │       (s3url/<ref_name>/<meta_folder>/, per the instance's tenant config)
       ├─ 🔒 Upload permissions.json
       └─ 📡 Notify ExoHub API, which forwards the ingest to the instance's `url`
              (`POST <url>/project/ingest`)
```

Tag immutability and the source-repo guard apply to both roles: publishing a **tag** is refused if a remote already has a record naming a different commit (no flag, not even `--force`, bypasses this), and publishing the same project/version from a different repository than it was first published from is refused unless `--force`. See [Catalog Remote Publishing]({{< ref "syncing#catalog-remote-publishing" >}}) for the full guard semantics.

### Pluggable Protocol

The `exo` CLI doesn't contain any catalog-specific logic. Instead, it discovers a binary matching the remote type:

```
git-annex-remote-<type>-<mode>
```

The CLI calls this binary with a `publish` subcommand, passing configuration from `.exohub/remotes` as flags:

```bash
git-annex-remote-artifactdb-export publish \
  --s3url <s3url> \
  --bundle-dir .exohub/bundles/v1.0/ \
  --artifactdb-dir .artifactdb \
  --grants \
  --api-url {{< exohub "appBaseURL" >}}/api
```

Anyone can create a new catalog integration by implementing a binary with this interface. See the [Catalog Remote Protocol](/specs/catalog-remote-protocol) for the full specification.

### Bundle Metadata

The `exo bundle` command generates structured JSON metadata describing a repository at a specific git revision:

- **`bundle.json`** — manifest with repo URL, ref, file counts, generation time
- **`commits/*.json`** — per-commit metadata (author, date, message, modified files)
- **`files/*.json`** — per-file metadata (path, size, annex key, storage locations)

This metadata is the input for catalog indexing. It lives in `.exohub/bundles/<ref>/` and is git-annex tracked so it can be synced like any other data.

### S3 Layout

Each publication creates a versioned directory on S3, with catalog-specific subdirectories:

```
s3://bucket/dataset/_catalog/
  v1.0/
    .exohub-bundle/              → generic bundle metadata
      bundle.json
      commits/
      files/
    .artifactdb/                 → business metadata (if any)
      dataset.json
    permissions.json              → access control
  v1.1/
    ...
```

This separation allows multiple catalog systems to feed from the same S3 location, each reading only the subdirectory relevant to them.

### Permissions

Permissions from `.exohub/permissions` are translated to the catalog's format and uploaded alongside the metadata. During ingestion, the catalog applies these permissions to control who can search and access the indexed data.

S3 Access Grants provide the underlying security boundary — users can only upload to S3 prefixes they have been granted access to. The catalog verifies project ownership by comparing the `s3url` of incoming publications against previously registered sources.

### Release-Only Publishing

The `publish_on: tag` option restricts publishing to tagged commits only. When set, `exo sync` skips the catalog remote unless HEAD points to a git tag. This is designed for CI/CD pipelines where publishing should only happen on releases:

```
git tag v1.0
exo bundle
exo sync        # publishes to catalog (HEAD is a tag)

# on a feature branch:
exo sync        # skips catalog (HEAD is not a tag)
```

## ArtifactDB Integration

The reference implementation uses [ArtifactDB](https://artifactdb.io) as the catalog system, deployed as a multi-tenant ArtifactDB instance.

### Architecture

```
ExoHub Ecosystem                          ArtifactDB / {{< exohub "catalogName" >}}
─────────────────                         ────────────────────

exo CLI                                   {{< exohub "catalogName" >}} API
  │                                         │
  ├─ exo bundle                             ├─ POST /project/ingest
  │   → generates metadata                  │   → validates $schema
  │                                         │   → triggers IngestWorkflow
  ├─ exo sync --with my-catalog                 │
  │   → uploads to S3                       ├─ IngestWorkflow (Temporal)
  │   → notifies ExoHub API                 │   → reads from external S3
  │                                         │   → indexes into Elasticsearch
  └─ git-annex-remote-artifactdb-export     │   → stores pointer in managed S3
      → publish subcommand                  │
                                            └─ GET /jobs/{id}
ExoHub API                                    → track indexing progress
  │
  └─ POST /api/publish
      → forwards to {{< exohub "catalogName" >}} /project/ingest
      → adds default /exohub tenant target
```

### Multi-Tenant Support

{{< exohub "catalogName" >}} is a multi-tenant ArtifactDB instance. Each tenant manages a separate set of projects with its own storage and search index:

- **`/exohub` tenant (Atlas)** — indexes generic bundle metadata (commits, files, locations), fed by a `role: atlas` remote's `.exohub-bundle/`. Always receives publications from a `role: atlas` remote.
- **Custom tenant** (e.g. `/cancerdb`) — an ArtifactDB instance that indexes business metadata from `.artifactdb/`, fed by a separate `role: instance` remote whose `url` points at that instance.

The ExoHub API routes each role's publish notification to its own target: a `role: atlas` remote's bundle to the Atlas, a `role: instance` remote's `.artifactdb/` upload to that instance's `/project/ingest` endpoint.

### Project Ownership

To prevent accidental overwrites when two repositories share the same project name, the system uses S3-path-based ownership:

1. On first publish, the catalog stores a pointer linking the `project_id` to the source `s3url`
2. On subsequent publishes, the catalog verifies the `s3url` matches — rejecting mismatches with HTTP 403
3. S3 Access Grants ensure users can only write to their own S3 prefixes

This provides two-layer protection without requiring a separate user/project registry.
