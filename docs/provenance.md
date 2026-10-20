# Provenance

`exo provenance` records and inspects the lineage of files in an ExoHub
dataset: what generated them, what they were derived from, and who (or what)
asserted that. Records follow the W3C PROV data model (PROV-O terms) and are
stored as add-only JSON-LD files, versioned with the data in git.

This document covers the record format, how to create and inspect records,
repository-level coverage rules, and the workflow for AI agents and scripts
that produce data in an ExoHub repository.

## Format overview

Records live at `.exohub/provenance/<UTC timestamp>-<slug>-<short-id>.jsonld`,
one JSON-LD document per file, for example:

```
.exohub/provenance/20261014T101200Z-normalize-7f3a2c.jsonld
```

Every record shares one `@context` IRI:

```
https://genentech.github.io/exohub/ns/provenance/v1.jsonld
```

That context maps short PROV-O aliases (`Entity`, `Collection`, `Activity`,
`Person`, `SoftwareAgent`, `used`, `wasGeneratedBy`, `wasDerivedFrom`,
`wasRevisionOf`, `wasAssociatedWith`, `wasAttributedTo`, `actedOnBehalfOf`,
`hadPlan`, `startedAtTime`, `endedAtTime`, `generatedAtTime`, `label`) plus
ExoHub-specific `exo:` terms (`exo:path`, `exo:assertion`, `exo:assertedBy`,
`exo:evidence`, `exo:supersedes`, `exo:model`, `exo:version`,
`exo:fromRecord`). `exo` embeds this context and never fetches it over the
network, so validation and lineage work fully offline.

A record is a JSON-LD named graph with a required envelope:

| Field | Meaning |
| --- | --- |
| `@context` | the context IRI above |
| `@id` | `urn:exohub:record:<UUIDv7>` |
| `exo:assertion` | `recorded`, `imported` or `asserted` |
| `exo:assertedBy` | the agent that made the claim |
| `generatedAtTime` | when the record was written |
| `exo:evidence` | optional; recommended for inferred claims |
| `exo:supersedes` | optional; record ids this record replaces |
| `@graph` | a non-empty array of PROV nodes |

Records are **add-only**: once committed, a record file is never modified or
deleted. A correction is a new record whose `exo:supersedes` lists the ids of
the records it replaces. Superseded records stay in the repository for audit
but are excluded from compiled output and from `exo provenance show`.
`exo provenance validate` reports an error if a committed record is later
changed or deleted.

Records must be tracked as **git blobs**, never annexed, even in a repository
whose `.gitattributes` annexes everything by default. `exo provenance add`
stages new records with `git -c annex.largefiles=nothing add`. If you
hand-write a record, stage it with `exo add` (never plain `git add`) — `exo
add` routes `.exohub/**` to git automatically. A record file staged as an
annex symlink or pointer file is a validation error.

### Identifiers

Content is identified by hash, never by path, because a record cannot carry
the id of the commit it lives in and because paths change on rename:

| Kind | Identifier |
| --- | --- |
| Annexed file | `urn:exohub:annex:<git-annex key>` |
| Git-tracked file | `urn:exohub:git:blob:<object id>` |
| Folder collection | `urn:exohub:git:tree:<object id>` |
| Record | `urn:exohub:record:<UUIDv7>` |
| Activity | `urn:exohub:activity:<UUIDv7>` |
| Person | `urn:exohub:agent:person:<unix id>` |
| Software or AI agent | `urn:exohub:agent:software:<name>` |
| External resource | its own IRI (URL, DOI, `s3://` URI) |

The path a file had when the record was written is kept as the `exo:path`
label, for display only.

A person's identifier is the `preferred_username` claim of the `exo` login
token (the unix id). Their full name, when the token provides it, becomes
the agent's `label`; email addresses are never recorded, and there is no git
author fallback. Without a login token, pass `--agent person:<unix id>`
explicitly.

Annex keys from non-hashing backends (`WORM`, `URL`) are not stable content
identifiers; validation warns about them.

## Examples

The examples below use `alice` as a placeholder unix id.

### A derivation

```sh
exo provenance add \
  --generated results/de.tsv \
  --used data/counts.csv \
  --plan scripts/de.R \
  --agent person:alice
```

This writes a record whose `@graph` contains an `Activity` that `used` the
counts entity and has the script blob as `hadPlan`, a generated entity
`wasGeneratedBy` that activity and `wasDerivedFrom` the counts entity, and
`wasAssociatedWith` the person agent. The example below is the exact output
of `exo provenance add --dry-run` with the flags above, run in a scratch
repository where `data/counts.csv` is annexed and `scripts/de.R` is a git
file:

```json
{
  "@context": "https://genentech.github.io/exohub/ns/provenance/v1.jsonld",
  "@id": "urn:exohub:record:01a13bc5-5a2a-7309-b782-95be6a4916d8",
  "exo:assertion": "asserted",
  "exo:assertedBy": "urn:exohub:agent:person:alice",
  "generatedAtTime": "2026-10-14T18:55:51Z",
  "@graph": [
    {
      "@id": "urn:exohub:activity:01a13bc5-5a2a-72f8-983f-8f5c1d457563",
      "@type": "Activity",
      "hadPlan": "urn:exohub:git:blob:aec92917403fe09d962fb43a3bcef0ae21f4c217",
      "used": "urn:exohub:annex:SHA256E-s4--5be08c9684a1d25efcee09318204824278b08bbfb4aef973ffefd0b9d7478313.csv",
      "wasAssociatedWith": "urn:exohub:agent:person:alice"
    },
    {
      "@id": "urn:exohub:agent:person:alice",
      "@type": "Person"
    },
    {
      "@id": "urn:exohub:annex:SHA256E-s4--5be08c9684a1d25efcee09318204824278b08bbfb4aef973ffefd0b9d7478313.csv",
      "@type": "Entity",
      "exo:path": "data/counts.csv"
    },
    {
      "@id": "urn:exohub:annex:SHA256E-s4--9c6536d38fa37da58fac066342747e6d20fdace12ab5b287cf04506f2afe95b0.tsv",
      "@type": "Entity",
      "exo:path": "results/de.tsv",
      "wasDerivedFrom": "urn:exohub:annex:SHA256E-s4--5be08c9684a1d25efcee09318204824278b08bbfb4aef973ffefd0b9d7478313.csv",
      "wasGeneratedBy": "urn:exohub:activity:01a13bc5-5a2a-72f8-983f-8f5c1d457563"
    },
    {
      "@id": "urn:exohub:git:blob:aec92917403fe09d962fb43a3bcef0ae21f4c217",
      "@type": "Entity",
      "exo:path": "scripts/de.R"
    }
  ]
}
```

(The `Person` agent has no `label` here because `--agent person:alice` was
given explicitly; with a real `exo login` token, the person's full name is
added automatically.) Dropping `--dry-run` writes this exact JSON to
`.exohub/provenance/` and stages it.

Note the annex key's extension: git-annex only keeps a file's extension in
its key when it is 4 characters or fewer (its default
`annex.maxextensionlength`), so `results/de.parquet` would produce a key
with no extension at all — `results/de.tsv` keeps `.tsv`.

### Re-running add

Before writing, `exo provenance add` checks each `--generated` id against
existing provenance (worktree records, including ones not yet staged): if a
non-superseded record already states `wasGeneratedBy` for it — for example,
re-running the same `add` command in a loop — the command exits non-zero and
writes nothing, naming the conflicting record file and id. This is a
targeted check (a couple of `git grep` calls), not a full read of every
record, so it stays fast even in a large repository.

To replace the earlier record with a correction, rerun `add` with
`--supersedes` naming that record's id. To make a generation loop safe to
rerun — skipping files that already have provenance instead of failing —
add `--skip-existing`: it drops already-covered `--generated` paths (noting
each on stderr) and writes a record for whatever remains, or writes nothing
and exits 0 if every path was already covered.

### A folder collection with `wasRevisionOf`

A folder can be one artifact: a PROV `Collection` identified by its git tree
object id. This covers multi-file formats (TileDB arrays, Zarr stores,
partitioned Parquet datasets) where per-file provenance would be noise. Any
change to any member, annexed or not, gives the folder a new tree id, so a
record does not need to list members — the tree id pins them.

```sh
exo provenance add \
  --generated data/expr.tdb/ \
  --used data/counts.csv \
  --agent person:alice
```

requires every file under `data/expr.tdb/` to be tracked and unchanged, and
records the folder as a `Collection` (again, exact `--dry-run` output):

```json
{
  "@context": "https://genentech.github.io/exohub/ns/provenance/v1.jsonld",
  "@id": "urn:exohub:record:01a13bc5-d976-70f2-81a9-c0a75991f98a",
  "exo:assertion": "asserted",
  "exo:assertedBy": "urn:exohub:agent:person:alice",
  "generatedAtTime": "2026-10-14T18:56:24Z",
  "@graph": [
    {
      "@id": "urn:exohub:activity:01a13bc5-d976-70e2-8ad3-0134b083e35a",
      "@type": "Activity",
      "used": "urn:exohub:annex:SHA256E-s4--5be08c9684a1d25efcee09318204824278b08bbfb4aef973ffefd0b9d7478313.csv",
      "wasAssociatedWith": "urn:exohub:agent:person:alice"
    },
    {
      "@id": "urn:exohub:agent:person:alice",
      "@type": "Person"
    },
    {
      "@id": "urn:exohub:annex:SHA256E-s4--5be08c9684a1d25efcee09318204824278b08bbfb4aef973ffefd0b9d7478313.csv",
      "@type": "Entity",
      "exo:path": "data/counts.csv"
    },
    {
      "@id": "urn:exohub:git:tree:e2bfb109131335b1286b542a2782a79885467a11",
      "@type": "Collection",
      "exo:path": "data/expr.tdb/",
      "wasDerivedFrom": "urn:exohub:annex:SHA256E-s4--5be08c9684a1d25efcee09318204824278b08bbfb4aef973ffefd0b9d7478313.csv",
      "wasGeneratedBy": "urn:exohub:activity:01a13bc5-d976-70e2-8ad3-0134b083e35a"
    }
  ]
}
```

A later write to the array (a new fragment under `data/expr.tdb/`) produces
a new tree id — a new entity, unrelated to the old one unless a record says
so. Link the two with `--revision-of <ref>:<path>`, where `<ref>` is a tag,
branch or commit at which the folder still has its previous tree id:

```sh
exo provenance add \
  --generated data/expr.tdb/ \
  --revision-of v1.2.0:data/expr.tdb/ \
  --agent person:alice
```

`v1.2.0:data/expr.tdb/` resolves the folder's tree id at that ref the same
way `exo provenance show --ref` does; a plain `--revision-of data/expr.tdb/`
is rejected when the folder is unchanged, since an entity cannot be a
revision of itself. The previous tree id is not added to `wasDerivedFrom` —
`wasRevisionOf` is a different PROV relation. Exact `--dry-run` output:

```json
{
  "@context": "https://genentech.github.io/exohub/ns/provenance/v1.jsonld",
  "@id": "urn:exohub:record:01a13bea-7b95-720b-b75e-10244f72fe03",
  "exo:assertion": "asserted",
  "exo:assertedBy": "urn:exohub:agent:person:alice",
  "generatedAtTime": "2026-10-14T19:36:25Z",
  "@graph": [
    {
      "@id": "urn:exohub:activity:01a13bea-7b95-71fb-b8da-f8835b1e1c06",
      "@type": "Activity",
      "wasAssociatedWith": "urn:exohub:agent:person:alice"
    },
    {
      "@id": "urn:exohub:agent:person:alice",
      "@type": "Person"
    },
    {
      "@id": "urn:exohub:git:tree:0f7ce151135569d3142dc190bc4cac90e080a674",
      "@type": "Collection",
      "exo:path": "data/expr.tdb/",
      "wasGeneratedBy": "urn:exohub:activity:01a13bea-7b95-71fb-b8da-f8835b1e1c06",
      "wasRevisionOf": "urn:exohub:git:tree:4d3bf77d48af72c5702c9c7b72d600f542527c65"
    },
    {
      "@id": "urn:exohub:git:tree:4d3bf77d48af72c5702c9c7b72d600f542527c65",
      "@type": "Collection",
      "exo:path": "data/expr.tdb/"
    }
  ]
}
```

Dropping `--dry-run` writes and stages this record. `--revision-of` is
repeatable and also accepts an IRI (taken as-is) or a plain path (resolved
from the index, like `--used`).

### An AI agent acting on behalf of a user

An AI agent or script identifies itself as a `SoftwareAgent`, states who it
acted for with `--on-behalf-of`, and should include `--evidence` for claims
it inferred rather than directly observed:

```sh
exo provenance add \
  --generated out.tsv \
  --used in.csv \
  --agent software:claude-code \
  --model claude-sonnet \
  --on-behalf-of person:alice \
  --evidence "scripts/normalize.py reads in.csv and writes out.tsv"
```

The record's `exo:assertedBy` references the software agent, which in turn
has `actedOnBehalfOf` the person. The `Activity`'s `wasAssociatedWith` names
both agents that carried out the work: the software agent and the person it
acted for. Again, this is the exact `--dry-run` output, run in a scratch
repository where both files are annexed:

```json
{
  "@context": "https://genentech.github.io/exohub/ns/provenance/v1.jsonld",
  "@id": "urn:exohub:record:01a1459e-752a-791d-836d-393669d9ca08",
  "exo:assertion": "asserted",
  "exo:assertedBy": "urn:exohub:agent:software:claude-code",
  "generatedAtTime": "2026-10-16T16:49:35Z",
  "exo:evidence": "scripts/normalize.py reads in.csv and writes out.tsv",
  "@graph": [
    {
      "@id": "urn:exohub:activity:01a1459e-752a-790d-8eae-8c0779dc7202",
      "@type": "Activity",
      "used": "urn:exohub:annex:SHA256E-s2--73cb3858a687a8494ca3323053016282f3dad39d42cf62ca4e79dda2aac7d9ac.csv",
      "wasAssociatedWith": [
        "urn:exohub:agent:person:alice",
        "urn:exohub:agent:software:claude-code"
      ]
    },
    {
      "@id": "urn:exohub:agent:person:alice",
      "@type": "Person"
    },
    {
      "@id": "urn:exohub:agent:software:claude-code",
      "@type": "SoftwareAgent",
      "actedOnBehalfOf": "urn:exohub:agent:person:alice",
      "exo:model": "claude-sonnet"
    },
    {
      "@id": "urn:exohub:annex:SHA256E-s2--3bb2abb69ebb27fbfe63c7639624c6ec5e331b841a5bc8c3ebc10b9285e90877.tsv",
      "@type": "Entity",
      "exo:path": "out.tsv",
      "wasDerivedFrom": "urn:exohub:annex:SHA256E-s2--73cb3858a687a8494ca3323053016282f3dad39d42cf62ca4e79dda2aac7d9ac.csv",
      "wasGeneratedBy": "urn:exohub:activity:01a1459e-752a-790d-8eae-8c0779dc7202"
    },
    {
      "@id": "urn:exohub:annex:SHA256E-s2--73cb3858a687a8494ca3323053016282f3dad39d42cf62ca4e79dda2aac7d9ac.csv",
      "@type": "Entity",
      "exo:path": "in.csv"
    }
  ]
}
```

The `Person` node comes from `--on-behalf-of person:alice`; with a real
login token for that user it would also carry a `label`. `wasAssociatedWith`
is sorted alphabetically like other multi-valued properties, so the person
agent appears first in the written record even though the software agent
asserted it.

`exo provenance validate` warns when an `asserted` record made by a software
agent has no `exo:evidence`.

## Coverage and configuration

A file is **covered** at a ref when its content id is the subject of a
`wasGeneratedBy`, `wasDerivedFrom`, `wasAttributedTo` or `wasRevisionOf`
statement in a non-superseded record, or when an enclosing folder collection
with the same tree id is covered. Being only `used` as an input does not
cover a file. `exo provenance validate` always reports a coverage summary.

Two keys under `provenance` in `.exohub/config` use the same glob syntax as
`annex.routing` (see [annex-routing.md](annex-routing.md)):

```yaml
provenance:
  require:
    - "results/**"
    - "models/**"
  collections:
    - "**/*.tdb/"
    - "**/*.zarr/"
```

- `provenance.require` makes coverage mandatory for matching files.
  `exo provenance validate` reports an error for every matching file at the
  checked state that is not covered. `exo provenance validate --staged`
  checks only staged added or modified files, so adding a rule never blocks
  commits on older uncovered files, but new outputs staged afterwards must
  come with provenance. Records and data staged in the same commit are
  checked together, since records reference index content ids. `exo bundle`
  enforces `require` on the bundle's selected files at the ref — this is the
  authoritative check, because client-side hooks can be skipped.
- `provenance.collections` declares folders that are units by default
  (patterns end in `/` and match directory paths). Files under a declared
  folder are covered only through the folder, and `exo provenance show`
  resolves them to the folder.

### `install-hook` and the `pre-commit` framework

```sh
exo provenance install-hook
```

installs a git pre-commit hook that runs `exo provenance validate --staged`.
The hook is added as a marked block inserted right after the shebang line of
`.git/hooks/pre-commit` (or the repository's configured hooks directory);
existing hook content is kept, and running the command again updates the
block in place instead of duplicating it.

To use the [pre-commit](https://pre-commit.com/) framework instead, add this
to `.pre-commit-config.yaml`:

```yaml
- repo: local
  hooks:
    - id: exo-provenance-validate
      name: exo provenance validate --staged
      entry: exo provenance validate --staged
      language: system
      pass_filenames: false
```

### The bundle output

When the bundle ref's tree contains `.exohub/provenance/` records (and
`--no-provenance` is not given), `exo bundle` compiles them into
`provenance.jsonld` at the bundle output root:

- it reads records **from the bundle ref**, not the worktree, and validates
  them, failing the bundle before any output is written on an error;
- it drops superseded records and merges graphs by node `@id`;
- scope is the bundle's **selected file set** — the files left after preset
  filters, `--scope`, `--include-hidden` and path filters — **ignoring**
  `--incremental` and `--resume`, plus their transitive upstream chain and
  the asserting agents (and `actedOnBehalfOf` chains) that back every kept
  statement;
- `exo:path` on a kept node is set to its path at the bundle ref whenever its
  content id or folder tree id exists anywhere at the ref, selected or not;
  a node whose content no longer exists anywhere at the ref keeps its
  recorded `exo:path`, if any.

`bundle.json` gains an optional `provenance` object (`file`, `record_count`,
`entity_count`). `provenance.jsonld` is excluded from artipacks, like
`bundle.json`. `--no-provenance` skips compilation but does **not** bypass
`provenance.require`: a repository that requires coverage still cannot
publish uncovered files. A bundle of a ref with no records and no `require`
rule is byte-identical to a bundle produced without this feature.

#### Per-artifact lineage

When the ref has provenance, every `files/**.json` artifact document whose
content id is a node of the compiled graph, or which lies under a covered
folder collection, gets a `lineage` object:

- `covered` — whether the artifact (or its enclosing collection) has a
  statement at all;
- `generated_by` — the activity that produced it (`id`, `started`, `ended`,
  `plan`) and every associated agent, including `acted_on_behalf_of` chains;
- `inputs` — the activity's direct `used` entities plus anything the
  artifact `wasDerivedFrom`;
- `revision_of` and `attributed_to`;
- `records` — the contributing records (superseded records are never
  listed, only the correcting one);
- `collection` — set instead of the above, with the enclosing folder's `id`
  and `path`, when the artifact has no record of its own but lies under a
  covered folder collection; members are matched against the folder's
  **current** tree at the ref.

Lineage only carries these direct, PROV-aligned facts — not the transitive
closure of upstream nodes. Transitive traversal belongs to consumers, and
to a future graph database fed from `provenance.json`/`provenance.jsonld`.

Artifacts with no lineage at all (nothing in the compiled graph covers them,
directly or through a collection) get no `lineage` key, so an unrelated
file's document is unchanged from a bundle without this feature.

#### `provenance.json`

`exo bundle` also writes `provenance.json` (`$schema:
exohub-provenance/v1.json`) next to `provenance.jsonld` at the bundle root:
`ref`, `ref_name`, `record_count`, `entity_count`, and the whole compiled
dataset flattened into `records`, `agents`, `activities` and `entities`
lists, plus `graph_file: provenance.jsonld`. It always describes the whole
dataset at the ref, including with `--incremental`. Like `provenance.jsonld`
and `bundle.json`, it is excluded from artipacks.

#### `--incremental` and lineage

An incremental bundle normally re-emits only the artifacts whose git history
changed in the commit range. Because a new provenance record can cover an
already-committed, otherwise unchanged file, `exo bundle --incremental` also
computes lineage at `--from-ref` and at the target ref, and re-emits the
document of every artifact whose lineage differs between the two, even when
its content did not change.

#### Bundle JSON Schemas

exo-cli ships the JSON Schema of every bundle document type under
`schemas/exohub/` (`exohub-artifact`, `exohub-markdown`, `exohub-bundle`,
`exohub-commit`, `exohub-provenance`), so downstream consumers
can validate `exo bundle` output without depending on exo-cli's Go types.
exo-cli's own tests validate generated bundle documents against them.

## `exo provenance show`

```sh
exo provenance show results/de.parquet
```

resolves the path to its content identifier at a ref (default `HEAD`) and
prints its upstream lineage: generating activities, used and derived-from
entities, plans, agents (including `actedOnBehalfOf`), timestamps and the
record that asserted each statement. Every entity is shown with its path at
the ref.

A path that is only `used` as an input, with no statement of its own, falls
back the same way an uncovered path does: it is folded into a **"used as an
input by"** list instead of being shown as covered. When the path has no
provenance of its own, or lies under a declared `provenance.collections`
folder, `show` falls back to the nearest enclosing covered folder collection
and says so.

The walk follows `--depth` upstream hops (default unlimited) and detects
cycles. A separate "Commit history" section lists the path's commits as
context, capped at `--max-commits` (default `10`, `0` = unlimited) — this is
not a provenance claim, just git history. `--json` emits the lineage
subgraph as JSON-LD, like every other `exo provenance` command's `--json`
output, to stdout.

## Agent workflow

AI agents and scripts that produce or import artifacts in an ExoHub
repository should:

1. Prefer `exo provenance add` to build a correct record from flags. If you
   need to hand-write a record instead, stage it with `exo add` — **never**
   plain `git add`, which may route it to the annex depending on
   `.gitattributes` (an annexed record file is a validation error) — then
   run `exo provenance validate`. Without `exo`: validate the record against
   the published JSON Schema at `schemas/provenance-record/v1.json` in this
   repository (mirrored in the public repo's `schemas/` directory), then
   stage it with `git -c annex.largefiles=nothing add`. The schema cannot
   check that a node's `prefix:local` property uses a prefix actually
   declared in the record's own `@context` object (a cross-field check) —
   verify that by hand, or run `exo provenance validate` once `exo` is
   available.
2. Identify as a `SoftwareAgent` with `--agent software:<name>`, pass
   `--model` when applicable, and state who you acted for with
   `--on-behalf-of person:<unix id>`.
3. Include `--evidence` for inferred claims — anything you did not directly
   observe (for example, claiming a generation when you read the activity's
   output from a log rather than from writing it yourself).
4. **Never edit or delete an existing record.** A correction is a new record
   that supersedes the ones it replaces (`--supersedes`).
5. Run `exo provenance validate` before committing; `exo provenance
   install-hook` automates this.

### Known limitation

A folder's tree id — and therefore its identity as a `Collection` — changes
whenever an annexed member switches between locked and unlocked mode, even
if the file's content is unchanged, because the tree contains a different
symlink or pointer object. The folder then appears as a new, uncovered
entity. `exo provenance show` reports the nearest older covered tree of the
same path as a hint when this happens.

## See also

- [annex-routing.md](annex-routing.md) — the glob syntax shared by
  `annex.routing`, `provenance.require` and `provenance.collections`.
