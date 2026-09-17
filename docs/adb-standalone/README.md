# adb-standalone — standalone ArtifactDB (Go + SurrealDB)

Goal: ship a **standalone, simple ArtifactDB** with the exo/exohub experience so users can
catalog datasets **without a full ArtifactDB deployment** — a single Go binary/Docker image
backed by one **SurrealDB** node (FTS + KV + optional vector), an **S3/MinIO** storage adapter,
and **synchronous ingest**. Responses are a **byte-identical subset** of enterprise ArtifactDB
(ArtifactDB), so datasets round-trip private ⇄ enterprise.

## Design decisions (locked)
1. **Response fidelity: byte-identical to ArtifactDB** (private/single vs enterprise deployment).
2. **S3 API kept** (S3/MinIO/pluggable) with **presigned URLs + chunked download**.
3. **Preserve ADB ID grammar** `project:path@version` (GPRN) for portability.
4. **FTS-only by default**; semantic search enabled only when given a **filesystem path to an
   embedding model** (`semantic.model_path`).
5. **Full read/write-access permission lists** kept.
6. Home = exo-cli repo for now; ultimately the OSS **exohub** repo, likely a **Docker image**.
7. **Versions come from git** (bundle `ref_name`) — no ADB sequence machinery.

## Endpoint contract (what Exohub uses)
Read: `GET /search`, `GET /scroll/{id}`, `GET /files/{id}/metadata`, `GET /files/{id}`(+`?chunks=true`),
`GET /projects/{id}/versions`, `GET /schemas`, `GET /entities`+`GET /semantic-search` (opt-in, xp).
Write: `POST /project/ingest` → `{job_id}`, `GET /jobs/{id}`.
Permissions: `GET/PUT/DELETE /projects/{id}/permissions`.
(Everything else in the full ADB backend surface is unused by Exohub — see design-surrealdb.md §5.)

## Documents
- [`design-surrealdb.md`](./design-surrealdb.md) — document model, SurrealQL schema, per-endpoint mapping, config strategy.
- [`design-testing.md`](./design-testing.md) — parity/testing plan vs ArtifactDB (oracle), 5 gates, 4 levels, 3-bucket normalizer.
- [`fixtures/`](./fixtures/) — golden fixtures + `seed/documents.ndjson` (600 live docs) = the L1 oracle.

## tools/adb-capture — oracle dumper (slice 1/6 ✅)

`go/adb-standalone/tools/adb-capture` is a standalone Go CLI that regenerates the L1 oracle
from a live ArtifactDB instance so fixtures don't rot.

### Build

```sh
make adb-capture        # → bin/adb-capture
# or
cd go/adb-standalone/tools/adb-capture && go build -o adb-capture .
```

### Usage

```sh
adb-capture \
  --base http://catalog.example.com \
  --tenant exohub \
  --out docs/adb-standalone/fixtures \
  [--token <bearer>] \
  [--corpus go/adb-standalone/tools/adb-capture/corpus.yaml] \
  [--page-size 100] \
  [--snapshot-version 2026-09-09]
```

| Flag | Default | Description |
|---|---|---|
| `--base` | *(required)* | ArtifactDB base URL |
| `--tenant` | `exohub` | Tenant path segment |
| `--out` | `.` | Output directory |
| `--token` | | Bearer token for restricted tenants |
| `--corpus` | `corpus.yaml` next to binary | Path to corpus.yaml test vectors |
| `--page-size` | `100` | Documents per scroll page |
| `--snapshot-version` | | Label written to `snapshot.json` |
| `--skip-docs` | | Skip document scroll |
| `--skip-fixtures` | | Skip corpus fixture replay |
| `--skip-schemas` | | Skip schema dump |

### Outputs

| Path | Description |
|---|---|
| `<out>/seed/documents.ndjson` | All documents, sorted by `_extra.id` (L1 seed) |
| `<out>/seed/schemas/<name>.json` | Per-schema JSON bodies |
| `<out>/fixtures/<name>.json` | One file per corpus request (canonicalized) |
| `<out>/fixtures/schemas.json` | `/schemas` list response |
| `<out>/snapshot.json` | Capture metadata (base, tenant, timestamp, doc count) |

### Corpus

Test vectors are defined in `corpus.yaml` (co-located with the binary source).
The corpus covers all endpoints from `design-testing.md §2`: search (wildcard, single-term,
schema-filter, project-filter, field-scoped, empty, size boundaries), scroll pagination,
`/files/{id}/metadata`, `/projects/{id}/versions`, `/schemas`, and error cases (404, unknown route).

### Refreshing the oracle

Re-run `adb-capture` pointing at the same ArtifactDB instance; commit the diff.
The `snapshot.json` records when and where the capture was taken.
Used by the `adb-live-diff` nightly job (`design-testing.md §7`).

## Implementation slices
1. `tools/adb-capture` + committed snapshot/fixtures ✅
2. SurrealDB schema + seed loader ✅
3. Read endpoints + **L1 parity tests** (CI gate) ✅
4. Ingest (`/project/ingest` + `/jobs`) + L4 parity ✅
5. S3/MinIO storage adapter (`/files/{id}` presigned + chunks) ✅
6. Optional vector/semantic (`semantic.model_path`) ✅
7. Deployment: versitygw default object store + `exo standalone` CLI ✅

## Deployment & storage modes (slice 7)

### Storage modes

| Mode | `storage.s3.endpoint` | Object store | When to use |
|---|---|---|---|
| **Managed** (default) | `""`, `managed`, or `embedded` | versitygw POSIX-FS (Apache-2.0) | Local dev, CI, air-gapped |
| **External S3/MinIO** | Any real URL, e.g. `http://minio:9000` | Your S3/MinIO bucket | Production / enterprise |
| **Test-only (gofakes3)** | Injected by test code | In-memory, BSD-licensed | Unit tests only — never shipped |

`versitygw` provides full S3 SigV4 semantics (presigned URLs, multipart upload) on a
local POSIX directory. It replaces MinIO/gofakes3 as the shipped runtime default.
`gofakes3` (BSD-licensed) is reserved for CI unit tests only — no AGPL in the shipped artifact.

### `exo standalone` command group

The `exo standalone` commands supervise the three-process stack locally.
For Docker Compose deployment see `go/adb-standalone/docker-compose.yml`.

```
exo standalone up        # bring up the stack (foreground, supervised)
exo standalone down      # stop & clean up child processes
exo standalone status    # health of the 3 processes + endpoints
exo standalone seed <ndjson>          # bulk-load finished ADB docs (bootstrap/restore)
exo standalone ingest <bundle-dir>    # catalog a new project version
```

#### `exo standalone up`

Launches and supervises surrealdb + versitygw + adb-standalone as child processes.
Blocks in the foreground; SIGINT/SIGTERM stops all children gracefully.

```sh
exo standalone up \
  --secret-key my-secret \
  --bucket adb-standalone \
  [--data-dir ~/.config/exo/standalone] \
  [--surreal-port 8000] \
  [--versitygw-port 9100] \
  [--adb-port 8080]
```

On first run, generates `<data-dir>/config.yaml` pre-configured for the managed stack.
Data persists in `<data-dir>/surreal/` and `<data-dir>/objects/` across restarts.

#### `exo standalone seed <ndjson>`

Bulk-loads documents from an NDJSON file (one complete doc per line, with `_extra.id`).
Designed for bootstrapping from an `adb-capture` snapshot or migrating private → enterprise.
Wraps `adb-standalone seed <ndjson>` — the `adb-standalone` binary must be in PATH.

```sh
exo standalone seed docs/adb-standalone/fixtures/seed/documents.ndjson
```

#### `exo standalone ingest <bundle-dir>`

Publishes a new project version by posting to `POST /v1/{tenant}/project/ingest`.
The server computes `_extra` (id, gprn, tenant, permissions, latest) and upserts all files.

```sh
exo standalone ingest ./my-bundle \
  --project-id my-project \
  --version v1.0.0
```

### Config keys

| Key | Description |
|---|---|
| `storage.s3.endpoint` | S3/versitygw endpoint. Use `"managed"` or leave empty for managed mode. |
| `storage.s3.public_endpoint` | Host override for presigned URLs so they are client-reachable (e.g. `http://localhost:9100` when behind Docker NAT). |
| `storage.s3.bucket` | Bucket name; auto-created on startup if absent. |
| `storage.s3.access_key_id` | Access key (versitygw root key in managed mode). |
| `storage.s3.secret_access_key` | Secret key. |

### Docker Compose

For a fully containerised deployment:

```sh
cd go/adb-standalone
cp .env.example .env   # edit SURREAL_PASSWORD and VERSITYGW_SECRET_KEY
docker compose up
```

The compose stack uses named Docker volumes (`surreal-data`, `versitygw-data`) so data
survives container restarts. The `adb-standalone` container waits for both `surrealdb` and
`versitygw` to pass their healthchecks before starting.

Set `STORAGE_S3_PUBLIC_ENDPOINT` to the host-reachable versitygw address so that
presigned URLs in `GET /files/{id}` and `?chunks=true` responses are routable by clients
outside the Docker network.

### Enterprise path

Point `storage.s3.endpoint` at a real S3 or MinIO endpoint to bypass managed storage
entirely. The versitygw process is not launched by `exo standalone up` in this case.

## Semantic search (slice 6)

Semantic search is **opt-in**: set `semantic.model_path` in `config.yaml` to enable it.
When unset the service runs in FTS-only mode and `/semantic-search` returns 404,
matching ArtifactDB behaviour when nuros is not configured.

```yaml
semantic:
  model_path: /models/qwen3-embedding-q4.onnx  # Qwen3-Embedding ONNX export
  dimensions: 256                              # must match model output size
```

**Runtime**: `internal/embed.OnnxEmbedder` runs the model via a subprocess runner
(`adb-model-runner` binary, set `ADB_MODEL_RUNNER` env to override path).
The production path uses `github.com/yalue/onnxruntime_go` (pure-Go CGo wrapper around
the ONNX Runtime C library) — no Python sidecar needed, single binary.

**Pooling**: last-token pooling (nuros 0.5.1 convention) — the hidden state at the
final sequence position is used as the sentence embedding.

**Endpoints added**:
- `GET /v1/{tenant}/semantic-search?q=<query>[&limit=10][&project_id=<pid>]`
  → `{chunks: [...], entities: [], relationships: []}` — matches xp/ArtifactDB envelope.
- `GET /v1/{tenant}/entities?name=<name>[&include_relationships=false]`
  → `{entity: {...}, relationships: [...]}` — always enabled; 404 if entity not found.
