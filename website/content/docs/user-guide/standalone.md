---
title: "Local Standalone Catalog"
description: "Run a local ArtifactDB catalog stack with exo standalone"
weight: 14
---

The `exo standalone` command lets you run a fully local ArtifactDB catalog stack — no cloud account, no ExoHub deployment required. It is ideal for local development, testing pipelines, or creating a private catalog before migrating to a shared ExoHub deployment.

The stack consists of three processes managed automatically by `exo standalone up`:

| Process | Role | Default port |
|---------|------|-------------|
| `surrealdb` | Document store (full-text search + key-value) | `8000` |
| `versitygw` | S3-compatible object store (POSIX filesystem backend) | `9100` |
| `adb-standalone` | ArtifactDB-compatible API server | `8080` |

The responses are byte-compatible with the enterprise ArtifactDB API, so datasets cataloged locally round-trip cleanly to a shared ExoHub deployment.

## Prerequisites

- **`surreal`** — [Install SurrealDB](https://surrealdb.com/docs/surrealdb/installation)
- **`adb-standalone`** — Install from ExoHub releases
- **`versitygw`** — Downloaded automatically from GitHub Releases if not in PATH

## Starting the Stack

```bash
exo standalone up --secret-key mysecret
```

This starts all three processes in the foreground and writes a default `config.yaml` the first time. Press **Ctrl+C** to stop everything gracefully.

> **Note:** `--secret-key` (or the `VERSITYGW_SECRET_KEY` environment variable) is required when using the managed versitygw object store.

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--data-dir` | `$XDG_CONFIG_HOME/exo/standalone` | Directory for persistent data and PID files |
| `--surreal-bin` | `surreal` | Path to the surreal binary |
| `--adb-bin` | `adb-standalone` | Path to the adb-standalone binary |
| `--config` | `<data-dir>/config.yaml` | adb-standalone config file |
| `--surreal-port` | `8000` | SurrealDB listen port |
| `--versitygw-port` | `9100` | versitygw listen port |
| `--adb-port` | `8080` | adb-standalone API listen port |
| `--access-key` | `versitygw` | versitygw root access key |
| `--secret-key` | _(required)_ | versitygw root secret key |
| `--bucket` | `adb-standalone` | S3 bucket to auto-create on startup |

Once running, the stack prints the three endpoints:

```
Stack is up:
  surrealdb      ws://localhost:8000
  versitygw      http://localhost:9100
  adb-standalone http://localhost:8080
```

### Custom Configuration

On the first run, `exo standalone up` writes a default `config.yaml` under `<data-dir>/`. Edit this file to change ports, bucket names, storage locations, or enable optional features such as semantic search.

Pass `--config <path>` to use a custom config file:

```bash
exo standalone up --config ~/my-adb-config.yaml --secret-key mysecret
```

## Checking Stack Health

```bash
exo standalone status
```

Displays the PID, endpoint, and HTTP health status for each process:

```
PROCESS        PID    ENDPOINT                  STATUS
surrealdb      12345  ws://localhost:8000        running
versitygw      12346  http://localhost:9100      ok
adb-standalone 12347  http://localhost:8080      ok
```

Supports `--surreal-port`, `--versitygw-port`, `--adb-port`, and `--data-dir` to match a non-default stack.

## Stopping the Stack

In a second terminal, stop the running stack:

```bash
exo standalone down
```

This sends `SIGTERM` to each process and removes the PID file. Pressing **Ctrl+C** in the foreground terminal where `up` is running also stops everything gracefully.

## Loading Data

### Bulk-loading an Existing Snapshot (seed)

Populate the catalog from an NDJSON snapshot — for example, from an `adb-capture` dump or a migration from a private deployment:

```bash
exo standalone seed documents.ndjson
```

Each line must be a complete ArtifactDB document with `_extra.id` set. Documents are upserted and the full-text search index is rebuilt automatically.

Flags:

| Flag | Default | Description |
|------|---------|-------------|
| `--adb-bin` | `adb-standalone` | Path to the adb-standalone binary |
| `--config` | _(auto-detected)_ | adb-standalone config file |

### Ingesting a New Project Version (ingest)

Catalog a new project version from a local exo bundle directory:

```bash
exo standalone ingest ./my-bundle --project-id myproject --version v1.0.0
```

`<bundle-dir>` must be a directory in the exo bundle format (`bundle.json` plus per-file `.json` metadata files). The server computes all `_extra` fields (`id`, `gprn`, `tenant`, `permissions`, `latest`).

Flags:

| Flag | Default | Description |
|------|---------|-------------|
| `--url` | `http://localhost:8080` | adb-standalone base URL |
| `--tenant` | `exohub` | ADB tenant path segment |
| `--project-id` | _(required)_ | Project ID |
| `--version` | _(required)_ | Version string (e.g., `v1.0.0`) |
| `--s3-url` | _(optional)_ | S3 bundle location (`s3://bucket/key`); auto-detected if omitted |

On success, the server returns the job ID:

```json
{
  "job_id": "..."
}
```

## Typical Workflow

```bash
# 1. Start the stack
exo standalone up --secret-key mysecret &

# 2. Check it is healthy
exo standalone status

# 3. Load an existing snapshot
exo standalone seed snapshot.ndjson

# 4. Ingest a new project version
exo standalone ingest ./bundle --project-id myproject --version v1.0.0

# 5. Stop the stack
exo standalone down
```

## Environment Variable

| Variable | Description |
|----------|-------------|
| `VERSITYGW_SECRET_KEY` | Alternative to `--secret-key`; used by `exo standalone up` when `--secret-key` is not provided |
