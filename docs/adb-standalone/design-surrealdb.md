# adb-standalone — SurrealDB schema & endpoint mapping (design)

Goal: a Go service exposing a **byte-identical subset** of the ArtifactDB REST contract,
backed by a **single SurrealDB node** (FTS + KV + optional vector), an **S3/MinIO** storage
adapter for file bytes, and **synchronous ingest**. Versions come from **git** (exohub
bundle `ref_name`), so no ADB sequence machinery.

## 1. Document model (from live ArtifactDB fixtures)

Each indexed record = schema-specific top-level fields **plus** an ADB-managed `_extra`:

```jsonc
{
  // ...schema fields (e.g. exohub-bundle): ref_name, file_count, repo_url, path, ref, ...
  "_extra": {
    "$schema": "exohub-bundle/v1.json",
    "type": "exohub bundle",
    "id": "sample-app:bundle.json@v2.2.0",   // project:path@version
    "gprn": "gprn:wip:adb::artifact:sample-app:bundle.json@v2.2.0",
    "project_id": "sample-app",
    "version": "v2.2.0",
    "latest": false,
    "permissions": {"read_access":"public","write_access":"owners","scope":"project","owners":["example-org"]},
    "location": {"type":"s3","s3":{"bucket":"example-bucket"}},
    "tenant": {"path":"/exohub","alias":"exohub"},
    "metapath": "bundle.json",
    "meta_indexed": "2026-06-05T19:08:53.862133+00:00",
    "index_name": "adb-standalone-v1-exohub-20260415"
  }
}
```

## 2. SurrealDB schema (SurrealQL)

```surql
-- ---------- documents ----------
DEFINE TABLE document SCHEMALESS;               -- schemaless: metadata varies by $schema
-- record id = the ADB id, so lookups by id are O(1): document:⟨sample-app:bundle.json@v2.2.0⟩
DEFINE FIELD _extra          ON document TYPE object;
DEFINE FIELD _extra.project_id ON document TYPE string;
DEFINE FIELD _extra.version  ON document TYPE string;
DEFINE FIELD _extra.latest   ON document TYPE bool  DEFAULT false;
DEFINE FIELD _extra.type     ON document TYPE string;
DEFINE FIELD _extra.permissions.read_access  ON document TYPE string;   -- public|authenticated|...
DEFINE FIELD search_text     ON document TYPE string;  -- concat of searchable fields, built at ingest

DEFINE INDEX doc_project ON document FIELDS _extra.project_id;
DEFINE INDEX doc_pv      ON document FIELDS _extra.project_id, _extra.version;
DEFINE INDEX doc_latest  ON document FIELDS _extra.project_id, _extra.latest;
DEFINE INDEX doc_read    ON document FIELDS _extra.permissions.read_access;

-- ---------- full-text search (BM25) ----------
DEFINE ANALYZER adb_txt TOKENIZERS class FILTERS lowercase, ascii, snowball(english);
DEFINE INDEX doc_fts ON document FIELDS search_text SEARCH ANALYZER adb_txt BM25 HIGHLIGHTS;

-- ---------- optional vector (only when semantic.model_path set) ----------
-- Applied dynamically by db.InitSchema() when semantic.model_path is non-empty.
DEFINE FIELD IF NOT EXISTS embedding ON document TYPE array<float>;
DEFINE INDEX IF NOT EXISTS doc_vec ON document FIELDS embedding MTREE DIMENSION 256 DIST COSINE;

-- ---------- scroll sessions (replaces Redis) ----------
DEFINE TABLE scroll_session SCHEMAFULL;
DEFINE FIELD query      ON scroll_session TYPE object;   -- q, filters, sort, size
DEFINE FIELD cursor     ON scroll_session TYPE int;      -- offset already returned
DEFINE FIELD total      ON scroll_session TYPE int;
DEFINE FIELD expires_at ON scroll_session TYPE datetime;

-- ---------- schema registry (for /schemas) ----------
DEFINE TABLE schema_type SCHEMAFULL;                     -- loaded from artifactdb-schemas files
DEFINE FIELD name ON schema_type TYPE string;
DEFINE FIELD json ON schema_type TYPE object;

-- ---------- knowledge graph: entities + relationships ----------
DEFINE TABLE IF NOT EXISTS entity SCHEMAFULL;
DEFINE FIELD IF NOT EXISTS entity_id   ON entity TYPE string;
DEFINE FIELD IF NOT EXISTS name        ON entity TYPE string;
DEFINE FIELD IF NOT EXISTS type        ON entity TYPE string;
DEFINE FIELD IF NOT EXISTS summary     ON entity TYPE string;
DEFINE FIELD IF NOT EXISTS source_docs ON entity TYPE array DEFAULT [];
DEFINE INDEX IF NOT EXISTS entity_name ON entity FIELDS name;

DEFINE TABLE IF NOT EXISTS relationship SCHEMAFULL;
DEFINE FIELD IF NOT EXISTS rel_id    ON relationship TYPE string;
DEFINE FIELD IF NOT EXISTS subject   ON relationship TYPE string;
DEFINE FIELD IF NOT EXISTS predicate ON relationship TYPE string;
DEFINE FIELD IF NOT EXISTS object    ON relationship TYPE string;
DEFINE INDEX IF NOT EXISTS rel_subject ON relationship FIELDS subject;
DEFINE INDEX IF NOT EXISTS rel_object  ON relationship FIELDS object;
```

## 3. Endpoint → SurrealDB mapping

| Endpoint | Implementation |
|---|---|
| `GET /search?q=&fields=&size=` | `SELECT *, search::score(1) AS _score FROM document WHERE search_text @1@ $q [AND read_access filter] ORDER BY _score DESC LIMIT $size`; wrap as `{results, count, total, next}`. Create a `scroll_session` (cursor=size) → `next="/scroll/{id}"`. If total ≤ size → `next=null`. |
| `GET /scroll/{id}` | Load session; return rows `START cursor LIMIT size`; advance cursor; **same `next` until cursor≥total, then null** (matches ArtifactDB's stateful scroll). |
| `GET /files/{id}/metadata` | `SELECT * FROM document:⟨$id⟩` → return the doc verbatim. |
| `GET /files/{id}` | look up `_extra.location` → **307 redirect to presigned URL** from storage adapter. |
| `GET /files/{id}?chunks=true` | return chunk/presigned-part URLs (storage adapter, multipart). |
| `GET /projects/{id}/versions` | `SELECT _extra.version FROM document WHERE _extra.project_id=$pid GROUP BY _extra.version` → `{project_id, aggs:[{"_extra.version":…}], total, latest:{"_extra.version": <where _extra.latest=true>}}`. |
| `GET /schemas` | `SELECT name FROM schema_type` → `{document_types:[{name,url}]}` (url built from base). |
| `POST /project/ingest` | **sync**: read bundle from S3/local (`external-project/v1.json`), for each metadata doc compute `_extra` (id, gprn, tenant, permissions from `.exohub/permissions`, latest flag), build `search_text` (+`embedding` if semantic on), `UPSERT document:⟨id⟩`; recompute `latest` per project. Return `{job_id}` (synthetic, already-complete). |
| `GET /jobs/{id}` | return `{status:"SUCCESS", ...}` (sync → always done). |
| `GET/PUT/DELETE /projects/{id}/permissions` | read/write `_extra.permissions` across the project's docs. |

## 4. Fidelity risks & how they're handled
1. **Search relevance/highlighting** — SurrealDB BM25 + `search::score`/`search::highlight` cover ES basics; tune analyzer to match ArtifactDB tokenization. Golden fixtures = regression oracle.
2. **`gprn`/`id` grammar** — reproduced from `gprn.{service,environment}` config; identical to enterprise → export/import portable.
3. **`latest` semantics** — recomputed at ingest (highest git semver per project). Version supplied by git, not sequences.
4. **Scroll statefulness** — SurrealDB `scroll_session` record == ArtifactDB's Redis session; stable scroll_id, server-advanced cursor.

## 5. Config (recreated, compatible subset)
KEEP: `prefixes`, `gprn`, `schema`/`internal_schema`, `permissions`, `tenants`, `storage.s3`.
REPLACE: `es` → `surreal`, nuros env → `semantic:{model_path,dimensions}` (absent ⇒ FTS-only).
DROP: celery, lock, sequences, audit, metrics, s3_inventory, content_inventory, hermes, sqs, dynamodb, localstack, inspectors, plugins, cache, almighty, cors.
Target ≈ 30–50 lines vs ArtifactDB's 578.
