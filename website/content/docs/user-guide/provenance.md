---
title: "Provenance"
description: "Record and inspect the lineage of files with exo provenance"
weight: 7
---

`exo provenance` records and inspects the lineage of files in an ExoHub dataset: what generated them, what they were derived from, and who (or what) asserted that. This section covers the record format, how to create and inspect records, repository-level coverage rules, and the workflow for AI agents and scripts that produce data in a repository.

## What a Provenance Record Is

Records follow the [W3C PROV](https://www.w3.org/TR/prov-overview/) data model and are stored as **add-only** JSON-LD files, versioned with the data in git. Each record lives at `.exohub/provenance/<UTC timestamp>-<slug>-<short-id>.jsonld`, for example:

```
.exohub/provenance/20261014T101200Z-normalize-7f3a2c.jsonld
```

Every record shares one `@context` IRI, `https://genentech.github.io/exohub/ns/provenance/v1.jsonld`, which maps PROV-O terms (`Entity`, `Activity`, `Person`, `SoftwareAgent`, `used`, `wasGeneratedBy`, `wasDerivedFrom`, `wasRevisionOf`, `wasAssociatedWith`, `wasAttributedTo`, `actedOnBehalfOf`, and more) plus ExoHub-specific `exo:` terms. `exo` embeds this context and never fetches it over the network, so recording and validating provenance works fully offline.

Records are **add-only**: once committed, a record file is never modified or deleted. A correction is a new record whose `exo:supersedes` lists the ids of the records it replaces — superseded records stay in the repository for audit, but are excluded from `exo provenance show` and from compiled bundle output. Records must be tracked as **git blobs**, never annexed — `exo provenance add` handles this automatically, and if you hand-write a record you must stage it with `exo add` (never plain `git add`), or, without exo, `git -c annex.largefiles=nothing add`.

Content is identified by hash, not by path, so provenance survives renames: an annexed file is `urn:exohub:annex:<git-annex key>`, a git-tracked file is `urn:exohub:git:blob:<object id>`, and a folder collection (below) is `urn:exohub:git:tree:<object id>`.

## Recording Provenance

Use `exo provenance add` to build a record from flags rather than hand-writing JSON-LD:

```bash
exo provenance add \
  --generated results/de.tsv \
  --used data/counts.csv \
  --plan scripts/de.R \
  --agent person:alice
```

This records an activity that `used` `data/counts.csv`, ran `scripts/de.R` as its plan, generated `results/de.tsv`, and was associated with the `alice` person agent. Add `--dry-run` to preview the exact JSON before it's written and staged. A person's identity comes from your `exo login` token (the `preferred_username` claim) by default; pass `--agent person:<unix id>` explicitly when no token is available.

Re-running `add` for a file that already has a non-superseded generation record fails fast rather than silently duplicating provenance. To correct an existing record, rerun with `--supersedes <record-id>`. To make a generation loop safe to rerun, add `--skip-existing` — it skips `--generated` paths that already have provenance and writes a record for whatever remains.

### Folder Collections

A folder can be one artifact — a PROV `Collection` identified by its git tree object id — which covers multi-file formats like **TileDB arrays**, **Zarr stores**, and **partitioned Parquet datasets**, where per-file provenance would be noise:

```bash
exo provenance add \
  --generated data/expr.tdb/ \
  --used data/counts.csv \
  --agent person:alice
```

Any change to any member of the folder, annexed or not, gives it a new tree id — a new entity, unrelated to the old one unless a record says so. Link the new version to the old one with `--revision-of <ref>:<path>`:

```bash
exo provenance add \
  --generated data/expr.tdb/ \
  --revision-of v1.2.0:data/expr.tdb/ \
  --agent person:alice
```

> Declare folders that should *always* be treated as a single unit with `provenance.collections` in `.exohub/config` (see [Coverage and Configuration](#coverage-and-configuration)) — files under a declared folder are only covered through the folder, never individually.

### AI Agents and Scripts

An AI agent or script identifies itself as a `SoftwareAgent`, states who it acted for, and should back inferred claims with evidence:

```bash
exo provenance add \
  --generated out.tsv \
  --used in.csv \
  --agent software:claude-code \
  --model claude-sonnet \
  --on-behalf-of person:alice \
  --evidence "scripts/normalize.py reads in.csv and writes out.tsv"
```

The resulting record's activity is associated with both the software agent and the person it acted for. `exo provenance validate` warns when an `asserted` record made by a software agent has no `exo:evidence`. See [Agent Workflow](#agent-workflow) below for the full set of recommendations.

## Inspecting Provenance

```bash
exo provenance show results/de.parquet
```

resolves the path to its content identifier at a ref (default `HEAD`) and prints its upstream lineage: generating activities, used and derived-from entities, plans, agents, timestamps, and the record that asserted each statement. A path that is only `used` as an input, with no statement of its own, is folded into a "used as an input by" list instead of being shown as covered. The walk follows `--depth` upstream hops (unlimited by default) and detects cycles; `--json` emits the lineage subgraph as JSON-LD.

## Coverage and Configuration

A file is **covered** at a ref when its content id is the subject of a `wasGeneratedBy`, `wasDerivedFrom`, `wasAttributedTo`, or `wasRevisionOf` statement in a non-superseded record — or when an enclosing folder collection with the same tree id is covered. Being only `used` as an input does not cover a file. `exo provenance validate` always reports a coverage summary.

Two keys under `provenance` in `.exohub/config` use the same glob syntax as `annex.routing` (see [Repository-Level File Routing]({{< ref "advanced#repository-level-file-routing" >}})):

```yaml
provenance:
  require:
    - "results/**"
    - "models/**"
  collections:
    - "**/*.tdb/"
    - "**/*.zarr/"
```

- **`provenance.require`** makes coverage mandatory for matching files. `exo provenance validate` reports an error for every matching file that isn't covered; `exo provenance validate --staged` checks only staged files, so adding a rule never blocks commits on older uncovered files. `exo bundle` enforces `require` on the bundle's selected files at the ref — this is the authoritative check, since client-side hooks can be skipped.
- **`provenance.collections`** declares folders that are units by default. Files under a declared folder are covered only through the folder, and `exo provenance show` resolves them to the folder.

### Enforcing Provenance with a Hook

```bash
exo provenance install-hook
```

installs a git pre-commit hook that runs `exo provenance validate --staged`. Running the command again updates the hook block in place rather than duplicating it. To use the [pre-commit](https://pre-commit.com/) framework instead:

```yaml
- repo: local
  hooks:
    - id: exo-provenance-validate
      name: exo provenance validate --staged
      entry: exo provenance validate --staged
      language: system
      pass_filenames: false
```

## Agent Workflow

AI agents and scripts that produce or import artifacts in an ExoHub repository should:

1. Prefer `exo provenance add` to build a correct record from flags. If you must hand-write a record instead: with `exo` available, stage it with `exo add` (which routes `.exohub/**` to git), then run `exo provenance validate`; without `exo`, validate the record against the published JSON Schema at [`schemas/provenance-record/v1.json`](https://github.com/Genentech/exohub/blob/main/schemas/provenance-record/v1.json) in the public ExoHub repository, then stage it with `git -c annex.largefiles=nothing add` (never plain `git add`, which may annex it — an annexed record is a validation error). The schema can't check that a node's `prefix:local` property uses a prefix actually declared in the record's own `@context` — verify that by hand, or run `exo provenance validate` once `exo` is available.
2. Identify as a `SoftwareAgent` with `--agent software:<name>`, pass `--model` when applicable, and state who you acted for with `--on-behalf-of person:<unix id>`.
3. Include `--evidence` for inferred claims — anything not directly observed (for example, claiming a generation when the activity's output was read from a log rather than written directly).
4. **Never edit or delete an existing record.** A correction is a new record that supersedes the ones it replaces (`--supersedes`).
5. Run `exo provenance validate` before committing; `exo provenance install-hook` automates this.

## How Provenance Reaches the Catalog

When a bundle ref's tree contains `.exohub/provenance/` records, `exo bundle` compiles them into `provenance.jsonld` at the bundle output root (unless `--no-provenance` is given — which skips compilation but does **not** bypass `provenance.require`). It reads records from the bundle ref, validates them, drops superseded records, and merges graphs by node id, scoped to the bundle's selected file set plus the transitive upstream chain and asserting agents that back every kept statement.

Two companion artifacts carry this into the catalog:

- **Per-artifact `lineage`** — every artifact document (`files/**.json`) covered by the compiled graph gets a `lineage` object with `covered`, `generated_by` (the producing activity and its agents), `inputs` (direct `used`/`wasDerivedFrom` entities), `revision_of`, `attributed_to`, and the contributing `records`. Lineage carries only these direct, PROV-aligned facts — **there is no transitive `upstream` field** — consumers follow `inputs` hop by hop to walk the full chain.
- **`provenance.json`** — written next to `provenance.jsonld` at the bundle root (`$schema: exohub-provenance/v1.json`), with the whole compiled dataset flattened into `records`, `agents`, `activities`, and `entities` lists, plus a pointer to `graph_file: provenance.jsonld`. It always describes the whole dataset at the ref, including with `--incremental`.

Both files are excluded from artipacks, like `bundle.json`. A bundle of a ref with no records and no `require` rule is byte-identical to a bundle produced without this feature.

## See Also

- [Repository-Level File Routing]({{< ref "advanced#repository-level-file-routing" >}}) — the glob syntax shared by `provenance.require` and `provenance.collections`.
- [Building a MNIST Dataset]({{< ref "tutorials/mnist" >}}) — a hands-on tutorial that builds a repository from scratch.
