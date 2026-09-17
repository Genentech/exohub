# adb-standalone — Testing / parity plan (Go vs Python ArtifactDB)

Objective: prove the Go standalone returns responses **equivalent to ArtifactDB** for the
endpoints Exohub uses. "Equivalent" = **byte-identical modulo a documented allowlist of
volatile fields** (see §3). The ArtifactDB Python service is the **reference oracle**.

## 0. Definition of done (acceptance gates)
- **G1 (structural)**: every endpoint's response is JSON-equal to ArtifactDB after normalization, for the whole request corpus (§2). 100% required.
- **G2 (result-set)**: for every search/scroll query, the **set of result `_extra.id`** matches ArtifactDB exactly (filters, pagination, permission scoping). 100% required.
- **G3 (ordering/relevance)**: top-K ordering matches ArtifactDB within a documented tolerance; tracked with a relevance metric (nDCG / Spearman vs ArtifactDB). Target ≥ 0.95; divergences triaged (analyzer tuning), not silently accepted.
- **G4 (client E2E)**: real `exo atlas`, `exo download`, and ingest work unchanged against the standalone.
- **G5 (errors)**: status codes + error envelope (`{"reason","status":"error"}`) match for 400/401/403/404.

## 1. Test levels

| Level | What | Where it runs | Speed |
|---|---|---|---|
| **L1 Contract (golden replay)** | Seed standalone from a captured ArtifactDB snapshot; replay recorded requests; assert normalized-equal to captured ArtifactDB responses. | exo-cli CI (Go test + in-memory SurrealDB) | fast, deterministic |
| **L2 Live differential** | Same request corpus hit against **both** ArtifactDB and standalone; normalize; diff. Catches drift fixtures miss. | scheduled/manual vs dev-ArtifactDB | slow, networked |
| **L3 Client E2E** | Run actual `exo`/exohub-workers flows against a containerized standalone. | CI (docker) + manual | medium |
| **L4 Ingest parity** | Ingest the same bundle into standalone; compare resulting `/files/{id}/metadata` + `/search` to ArtifactDB for that project. | CI + L2 | medium |

L1 is the backbone (offline, in CI, gates every MR). L2 is the safety net against oracle drift.

## 2. Request corpus (the shared test vectors)
Derived from real client usage (already inventoried) + systematic variations. One YAML corpus,
consumed by L1 (replay) and L2 (diff):
- **/search**: `q=*`; single-term; multi-term; phrase; field-scoped (`fields=_extra,path,size`);
  schema filter (`schema=exohub-bundle/v1`); by project_id; empty result; special chars/unicode;
  `size=` boundaries (0/1/default/large).
- **/scroll**: full traversal of a large query → assert every page + terminal `next=null`;
  reuse an exhausted scroll_id (error/empty); expired scroll_id.
- **/files/{id}/metadata**: existing id; url-encoded id with `/`, `:`, `@`; non-existent (404);
  permission-scoped (public vs restricted → anon 403/redacted).
- **/files/{id}** and `?chunks=true`: assert **redirect target shape** (host/bucket/key + that a
  presigned signature is present), not the signature value (volatile).
- **/projects/{id}/versions**: multi-version project (agg + latest); single version; unknown project.
- **/schemas**: full list (names; url host normalized).
- **/project/ingest** + **/jobs/{id}** (L4): ingest a fixture bundle → job SUCCESS → doc queryable.
- **errors**: bad token, missing auth on restricted, malformed id, unknown route.

## 3. Response normalization spec (three explicit buckets)
Derived from mining 600 live docs (3 schemas). A single shared normalizer (Go + py reference),
documented and reviewed. **Critical: not all timestamps are volatile** — git/content timestamps
must stay exact; only *server provenance* is normalized.

### 3a. VOLATILE — drop/ignore (server provenance, non-deterministic)
- `_extra.index_name`   (e.g. `adb-standalone-v1-exohub-20260415` — ES index, ArtifactDB-specific)
- `_extra.meta_indexed` (when the instance indexed it)
- `_extra.meta_uploaded`, `_extra.uploaded` (when uploaded to the instance)
- scroll_id token inside `next` → compare **shape** `"/scroll/<opaque>"`, not the value
- presigned-URL query params (`X-Amz-*`, `Expires`, `Signature`), response `Date`/timing headers

### 3b. ENV-DEPENDENT — normalize the environment part, keep structure
(bucket/host differ per deployment; that's expected for export private→enterprise)
- `_extra.location.s3.bucket`
- `locations[].s3url`, `locations[].chunks[].s3_path`, `locations[].chunks[].chunk_key`
- any absolute URL (`/schemas` `url`, redirect `Location`) → strip scheme+host, compare path/key layout only

### 3c. KEEP EXACT — everything else, including CONTENT timestamps
- **git/content timestamps (MUST match):** `author_date`, `committer_date`,
  `commits[].author_date`, `commits[].committer_date`, `generated_at`
- all metadata + `_extra.{id,gprn,project_id,version,latest,permissions,tenant,type,$schema,metapath,file_size,artipack}`
- `count`, `total`, and result ordering (subject to §G3)

**Canonicalize** before diff: sort object keys, stable float/number formatting.
The three buckets ARE the contract: moving a field into 3a/3b requires review (prevents hiding real diffs).

## 4. Golden capture tooling (build the oracle once)
`tools/adb-capture` (Go or Python) that, against a real ArtifactDB:
1. **Dumps a document snapshot** — scroll a query (e.g. one tenant / N projects) → `seed/documents.ndjson` (raw docs incl `_extra`). This is the **seed** for the standalone so both hold identical data.
2. **Records request→response pairs** for the whole corpus → `fixtures/*.json` (raw ArtifactDB responses = expected, pre-normalization).
3. Snapshots the **JSON schemas** from `/schemas/{name}` → `seed/schemas/`.
Re-runnable to refresh the oracle; snapshot is versioned so L1 stays deterministic offline.

## 5. Seeding the standalone in tests
- L1: start SurrealDB **in-memory** (`surreal start memory` or embedded surrealkv) → run standalone's
  ingest/seed loader on `seed/documents.ndjson` → deterministic, no external deps, fast.
- L3/L4: docker-compose (standalone + SurrealDB + MinIO) seeded from the same snapshot.

## 6. Search-ordering fidelity (the hard part)
BM25 differs between ES and SurrealDB, so treat ordering explicitly:
- **G2 first**: assert exact result-id **set** per query (ordering-independent) — this catches
  correctness bugs cheaply.
- **G3 then**: compare ranking vs ArtifactDB with **nDCG@K / Spearman**; gate at a threshold; emit a
  per-query divergence report to drive **analyzer tuning** (tokenizer/filters/stemming, field
  boosts). Keep a small **hand-labeled relevance set** for exohub queries so we can tune toward
  *good* ranking, not just "same as ES".

## 7. CI wiring
- exo-cli CI job `adb-parity`: spin in-memory SurrealDB, seed, run L1 + G2 + G3 (offline snapshot). Gates MRs.
- Scheduled job `adb-live-diff` (nightly): L2 vs dev-ArtifactDB; report-only (oracle may drift), opens an issue on regressions.
- `adb-e2e` (docker): L3/L4 on the parity slice.

## 8. Deliverables
1. `tools/adb-capture` (oracle dumper) + committed snapshot (`seed/`, `fixtures/`).
2. Shared `corpus.yaml` (test vectors) + `normalize` lib (Go).
3. Go test suite `parity_test.go` (L1/G1/G2/G3).
4. `docker-compose.parity.yml` + `adb-e2e` scripts (L3/L4).
5. CI jobs `adb-parity`, `adb-live-diff`, `adb-e2e`.
