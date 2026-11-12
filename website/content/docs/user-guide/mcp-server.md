---
title: "MCP Server"
description: "Use AI agents to manage your data through the Model Context Protocol"
weight: 14
---

ExoCLI includes a built-in [Model Context Protocol](https://modelcontextprotocol.io/) (MCP) server that exposes data management operations as tools for AI agents. This lets agents like Claude, Cursor, or any MCP-compatible client create repositories, sync data, monitor progress, and more — without requiring knowledge of git-annex internals.

## Quick Start

### Starting the Server

```bash
exo mcp start
```

This starts the MCP server over stdio. You typically don't run this directly — instead, you register it in your agent's MCP configuration.

### Claude Code Configuration

Register the MCP server with Claude Code:

```bash
claude mcp add --transport stdio exo -- exo mcp start
```

Or add it manually to your `.mcp.json` or project settings:

```json
{
  "mcpServers": {
    "exo": {
      "command": "exo",
      "args": ["mcp", "start"]
    }
  }
}
```

### Discovering Tools

List all available tools and their descriptions:

```bash
exo mcp list
```

Show full details for a specific tool, including parameters:

```bash
exo mcp show sync
```

## Available Tools

| Tool | Description |
|------|-------------|
| `profiles` | List available server-authored setup profiles, or show one profile's prompts |
| `add` | Add files to tracking with automatic content-type detection (text → git, binary → annex) |
| `status` | Show working tree status: untracked, modified, and staged files |
| `locate` | Show where git-annex content is physically stored across remotes |
| `sync` | Download and upload git-annex managed data files to/from remote storage (background) |
| `sync_status` | Check whether a background sync is still running |
| `publish` | Orchestrate the full publish workflow: sync content → bundle metadata → sync to catalog → broadcast |
| `broadcast` | Broadcast git-annex metadata to remotes without transferring file content |
| `login` | Authenticate via OIDC device flow (non-blocking) |
| `login_status` | Check whether authentication has completed |
| `whoami` | Show identity, session, organisation, and AWS credential info for the current login |
| `heartbeat_record` | Start recording sync progress metrics in the background |
| `heartbeat_show` | Show the latest sync progress metrics |
| `init` | Initialize or reconfigure git-annex in an existing repository, optionally applying a server-authored profile |
| `init_remote` | Add a new storage remote to the repository |
| `get` | Download git-annex managed content for specific paths |
| `doctor` | Run ExoHub client diagnostics: environment, auth, network, repo config, grants/S3 access, provenance |
| `provenance_add` | Record PROV-O provenance for generated/derived files |
| `provenance_show` | Show a path's recorded lineage from the repository's provenance records |
| `provenance_validate` | Validate recorded provenance for schema conformance, dangling references, and coverage |
| `info` | Show repository information: configured remotes, annex state, and storage statistics |
| `docs` | Fetch exo usage documentation (the llms.txt index, or a specific page) |
| `catalog_search` | Search the ExoHub catalog for datasets and artifacts |
| `catalog_schemas` | List available document schema names in the catalog |
| `catalog_download` | Download files, artifacts, or entire projects from the catalog |
| `catalog_semantic_search` | Semantic (natural language) search over the catalog using vector similarity |
| `catalog_entities` | Retrieve entity details and relationships from the catalog knowledge graph |
| `catalog_lineage` | Show an artifact's recorded lineage from the catalog, plus its direct consumers |

> The MCP server does not expose a `create_repo` tool — the agent must get a local git repository pointed at `origin` first (`git clone <url>`, or `git init` + `git remote add origin <url>` for a brand-new repository on the git host), then call `init` to configure git-annex and remotes.

## Workflows

### Creating or Publishing a Repository

The MCP server does not expose a `create_repo` or `clone` tool — the agent runs `git clone`/`git init` in a terminal, then uses `init` (optionally with a server-authored profile) to configure git-annex:

```
1. login          → authenticate (returns auth URL for you to visit)
2. login_status   → poll until authentication completes
                   (then: git clone <url>, or git init + git remote add origin <url>, in a terminal)
3. profiles       → list available profiles (optional, usually the fastest path);
                     call again with 'name' to see that profile's prompts and ask the user about every one
4. init           → configure git-annex (pass 'profile' + 'answers' if using a profile);
                     without a profile, ask the user which remote types they want, then call init_remote
                     for each one, one at a time
5. add            → add files to tracking
6. provenance_add → record what was generated/derived, if the files came from a pipeline, script, or this agent
7. git commit, git push → push metadata
8. publish        → if an artifactdb remote is configured (syncs content, bundles metadata, syncs to the catalog);
                     otherwise sync         → upload data to the configured remotes
```

### Cloning and Downloading Data

Cloning is done via `git clone` in a terminal. Once cloned, use `init` to configure git-annex remotes, then `get` or `sync` to download content:

```
1. login          → authenticate
2. login_status   → poll until authentication completes
                   (then: git clone <url> in a terminal)
3. init           → configure git-annex remotes from .exohub/remotes
4. get or sync    → download content from remotes (get for specific paths, sync for everything)
```

### Syncing with Progress Monitoring

The `sync` tool runs in the background and returns immediately. The agent can monitor progress for you:

```
1. heartbeat_record   → start background metrics capture
2. sync               → start the sync (runs in background)
3. heartbeat_show     → poll periodically to check progress
4. sync_status        → check if sync has completed
```

### Checking Repository State

```
1. status    → see untracked/modified/staged files
2. locate    → find which remotes hold specific files
```

Use `info` to see configured remotes and their types, or `exo info` from the CLI. Remotes are defined in the `.exohub/remotes` manifest file.

### Discovering and Downloading Catalog Data

```
1. catalog_schemas          → discover available schema types
2. catalog_search           → find datasets matching your criteria
3. catalog_semantic_search  → natural language search (e.g. "datasets related to neurodegeneration")
4. catalog_entities         → look up entity details and relationships (genes, diseases, pathways, etc.)
5. catalog_lineage          → look up a specific artifact's recorded lineage and direct consumers once you have its id
6. catalog_download         → download artifacts found in search results
```

### Recording and Checking Provenance

```
1. provenance_add       → after generating/transforming files, before committing: what was generated, what it
                           was derived from or used, and who/what is responsible
2. provenance_show      → see a path's recorded lineage from the repository directly
3. provenance_validate  → before committing or publishing: catch schema errors, dangling references, or
                           missing coverage
```

## Tool Details

### profiles

List available server-authored ExoHub setup profiles, or show one profile's prompts. Call without `name` to list profiles (name, description) so you can offer the user a choice. Call with `name` to get that profile's prompts (key, type, choices, default) so you can ask the user every question yourself — never assume a default, especially for a `read_access` prompt: it controls who can see the published catalog entries (e.g. `public` vs `authenticated`), and some profiles default to `public`. Pass the answers to `init` as `profile` + `answers`. Read-only.

**Parameters:**

| Parameter | Required | Description |
|-----------|----------|-------------|
| `name` | No | Profile name to show prompts for (omit to list all available profiles) |

### add

Add files to tracking with automatic content-type detection. In `auto` mode (default), text files go to git and binary/data files go to git-annex based on mimetype.

`paths` also accepts `exohub:<project>[:path]@<version>` references to import an asset, folder, whole project, or named collection from another ExoHub project (see [Importing from Another Project]({{< ref "provenance#importing-from-another-project-exo-add-exohub" >}})). References cannot be mixed with local paths/URLs in the same call, or combined with `mode`.

**Parameters:**

| Parameter | Required | Description |
|-----------|----------|-------------|
| `repo_dir` | Yes | Path to the git repository |
| `paths` | Yes | File paths, URLs, or `exohub:` references to add |
| `mode` | No | `auto` (default), `annex`, or `git` — not used with `exohub:` references |
| `to` | No | Destination path for an `exohub:` reference (`--to`) |
| `repo` | No | Git remote URL to resolve an `exohub:` reference against when it isn't reachable via a local clone or the catalog (`--repo`) |
| `verify` | No | Re-resolve an `exohub:` reference via the source's git remote and fail on a mismatch (`--verify`) |

**Prerequisites:** git-annex initialized in the repository.

### status

Show working tree status including untracked, modified, and staged files. Read-only.

**Parameters:**

| Parameter | Required | Description |
|-----------|----------|-------------|
| `repo_dir` | Yes | Path to the git repository |

### locate

Show where git-annex managed content is physically stored across remotes. Read-only.

**Parameters:**

| Parameter | Required | Description |
|-----------|----------|-------------|
| `repo_dir` | Yes | Path to the git repository |
| `paths` | No | Specific file paths to locate (defaults to all annexed files) |

**Prerequisites:** git-annex initialized. Remotes defined in `.exohub/remotes`.

### sync

Download and upload git-annex managed data files to/from remote storage. Runs in the background and returns immediately.

**Parameters:**

| Parameter | Required | Description |
|-----------|----------|-------------|
| `repo_dir` | Yes | Path to the git repository |
| `with` | No | Remote names to sync with (defaults to all) |

**Prerequisites:** git-annex initialized (`exo init`). Remotes defined in `.exohub/remotes`; use `exo info` to list them.

**Background execution:** This tool starts the sync process in the background and returns immediately. Use `sync_status` to check completion and `heartbeat_show` for progress details.

### sync_status

Check whether a background sync is still running for the given repository.

**Parameters:**

| Parameter | Required | Description |
|-----------|----------|-------------|
| `repo_dir` | Yes | Path to the git repository |

**Returns:** `running` or `not_running`.

### publish

Orchestrate the full publish workflow: sync content to storage remotes, generate bundle metadata, sync to catalog remotes, then broadcast metadata to all remotes.

**Parameters:**

| Parameter | Required | Description |
|-----------|----------|-------------|
| `repo_dir` | Yes | Path to the git repository |
| `sync` | No | Array of remote names to sync in step 1; overrides auto-discovery when provided |
| `ref` | No | Array of tags/commits to publish with the current routing (repeatable). Content and metadata come from each ref, while routing (remotes, `atlas_url`, `instance_url`) comes from the current checked-out `.exohub/remotes` — this is how an older tag whose own routing predates a migration gets published to the current catalog instead of a stale or missing target |
| `force` | No | With `ref`, publish even if the ref's content-availability check finds fixable/lost content on the current remote(s) |

**Prerequisites:** git-annex initialized. Remotes defined in `.exohub/remotes`.

### broadcast

Broadcast git-annex metadata and tracking information to remotes without transferring file content. Use this instead of `git annex sync --no-content`.

**Parameters:**

| Parameter | Required | Description |
|-----------|----------|-------------|
| `repo_dir` | Yes | Path to the git repository |
| `with` | No | Remote names to broadcast to (defaults to all) |

**Prerequisites:** git-annex initialized. Remotes defined in `.exohub/remotes`.

### init

Initialize or reconfigure git-annex in an existing git repository, optionally applying a server-authored profile. The repository must already exist locally (`git clone <url>`, or `git init` + `git remote add origin <url>` for a brand new repository) — this tool does not create the git repository itself.

Without `profile`, runs `exo init --yes` to ensure git-annex is set up and all remotes defined in `.exohub/remotes` are properly configured — idempotent, safe to call multiple times. Use this after `git clone` to enable remotes, or any time remote configuration may be stale.

With `profile`, also fetches and applies that server-authored profile: use the `profiles` tool first to discover available profiles and their prompts, ask the user about every prompt (never assume a value, especially `read_access`), then pass the answers here as `answers` (key → value, matching the profile's prompt keys and types). This is equivalent to the CLI's `--profile`/`--set key=value` flags (see [Using Profiles]({{< ref "repositories#using-profiles" >}})).

**Parameters:**

| Parameter | Required | Description |
|-----------|----------|-------------|
| `repo_dir` | Yes | Path to the git repository; must already exist (see `git clone` / `git init` first) |
| `profile` | No | Name of a server-authored profile to apply (see the `profiles` tool) |
| `name` | No | Explicit repository name (passed through as `--name`) |
| `answers` | No | Answers to the chosen profile's prompts, keyed by prompt key; unanswered prompts fall back to their default |

### init_remote

Add a new storage remote to the repository. Creates an S3 or local remote for storing git-annex data. The remote is saved to `.exohub/remotes` for reproducible setup across clones.

**Parameters:**

| Parameter | Required | Description |
|-----------|----------|-------------|
| `repo_dir` | Yes | Path to the git repository |
| `name` | Yes | Remote name (e.g., `s3-annex`, `s3-export`) |
| `type` | Yes | Remote type: `annex`, `export`, `artifactdb`, or `exospace` |
| `s3url` | No | S3 URL (required for `annex`, `export`, `artifactdb`). Example: `s3://example-sandbox-bucket/sandbox/<org>/<repo>/_annex` |
| `rsyncurl` | No | Rsync URL or local path (required for `exospace`). Example: `rsync://host/path` |
| `tracking_branch` | No | Tracking branch for export remotes (defaults to current branch) |
| `grants` | No | Enable fine-grained S3 Access Grants permissions (default: `true`) |

### get

Download git-annex managed content for specific paths into the local repository (wraps `exo get`). Use this for a targeted download instead of `sync`, which transfers content for the whole repository.

**Parameters:**

| Parameter | Required | Description |
|-----------|----------|-------------|
| `repo_dir` | Yes | Path to the git repository |
| `paths` | No | Paths to get (defaults to the current directory, i.e. everything) |
| `from` | No | Source remote to get content from (defaults to any remote with the content) |
| `all` | No | Get all versions of the content, not just the current one |

**Prerequisites:** git-annex initialized in the repository (run `init` first).

### doctor

Run ExoHub client diagnostics (wraps `exo doctor`): environment, auth, network, repo config, grants/S3 access, and provenance checks. Each check reports pass, warn, fail, or skip. Use this to diagnose a failing sync, login, or permission issue before asking the user to investigate manually. Read-only.

**Parameters:**

| Parameter | Required | Description |
|-----------|----------|-------------|
| `repo_dir` | No | Path to an ExoHub repo to scope repo-specific checks (default: current directory) |
| `remote` | No | Only run per-remote checks for this remote name |
| `write_test` | No | Also perform an opt-in S3 write canary (put + delete) under a caller-owned prefix |

### provenance_add

Record PROV-O provenance for files in this repository (wraps `exo provenance add`): what was generated, what it was derived from or used, and who/what asserted it. Call this after generating or transforming files and before committing, so the commit carries both the data and its provenance.

Unless `agent` is given explicitly, the record is attributed to this calling tool as a software agent, acting on behalf of the logged-in user (exo resolves the on-behalf-of person automatically). Choose `assertion` based on how you know the lineage: use `recorded` when you (this agent) yourself just ran the step that produced `generated` — you observed the activity directly; use `asserted` (the default) plus `evidence` when you are inferring lineage after the fact (e.g. from file names, timestamps, or a README) rather than having executed the step; use `imported` when the lineage was taken from another system's records rather than observed or inferred here.

**Parameters:**

| Parameter | Required | Description |
|-----------|----------|-------------|
| `repo_dir` | Yes | Path to the git repository |
| `generated` | Yes | Paths that were generated/created by this activity |
| `used` | No | Paths that were read as inputs without being transformed into `generated` |
| `derived_from` | No | Paths that `generated` was derived/transformed from |
| `no_derived_from` | No | Explicitly assert `generated` has no inputs (e.g. hand-authored files) |
| `revision_of` | No | Paths/ids that `generated` is a revision of |
| `plan` | No | Path to a plan/script/notebook document describing the activity |
| `agent` | No | `person:<unix id>` or `software:<name>` asserting this record; defaults to `software:<this MCP client>` |
| `on_behalf_of` | No | `person:<unix id>` the software agent acted on behalf of; defaults to the logged-in user when `agent` is a software agent |
| `model` | No | Model identifier for a software agent (requires `agent software:<name>`) |
| `started` | No | Activity start time (RFC3339); defaults to now |
| `ended` | No | Activity end time (RFC3339); defaults to now |
| `assertion` | No | Assertion kind: `recorded`, `asserted` (default), or `imported` |
| `evidence` | No | Free-text justification for how you inferred the lineage; required context for an `asserted` record |
| `supersedes` | No | Record ids this record supersedes |
| `skip_existing` | No | Skip `generated` paths that already have provenance instead of erroring |
| `dry_run` | No | Print the record that would be written without writing it |

See [Provenance]({{< ref "provenance" >}}) for the full record format and agent-workflow recommendations.

### provenance_show

Show the recorded lineage for a path from the git repository's provenance records (wraps `exo provenance show`): what generated it, what it was derived from, and the agents involved. Use this to answer "where did this file come from" before reusing or publishing it. Complements `catalog_lineage`, which reads lineage already published to the catalog. Read-only.

**Parameters:**

| Parameter | Required | Description |
|-----------|----------|-------------|
| `repo_dir` | Yes | Path to the git repository |
| `path` | Yes | Path to show lineage for |
| `ref` | No | Show lineage as of this git ref (default: `HEAD`) |
| `depth` | No | Maximum number of upstream hops to walk (default: unlimited) |

### provenance_validate

Validate recorded provenance for consistency (wraps `exo provenance validate`): schema conformance, dangling references, and coverage. Returns a report of errors (exit-affecting) and warnings per record. Run this before committing or publishing to catch provenance problems early. Read-only.

**Parameters:**

| Parameter | Required | Description |
|-----------|----------|-------------|
| `repo_dir` | Yes | Path to the git repository |
| `staged` | No | Validate the staged (index) state instead of the worktree |
| `ref` | No | Validate records as of this git ref instead of the worktree (mutually exclusive with `staged`) |

### info

Show repository information including configured remotes, annex state, and storage statistics. Use this to discover which remotes are available before running `sync` or `broadcast`. Read-only.

**Parameters:**

| Parameter | Required | Description |
|-----------|----------|-------------|
| `repo_dir` | Yes | Path to the git repository |

### login

Authenticate via OIDC device flow. If valid credentials exist, returns immediately. Otherwise, starts a device flow in the background and returns an auth URL that the agent shows to you.

**Parameters:**

| Parameter | Required | Description |
|-----------|----------|-------------|
| `force` | No | Force re-authentication even if valid credentials exist |

**Non-blocking:** When device flow is needed, returns the auth URL immediately. Poll `login_status` to check when authentication completes.

**Required before:** `init` (with a profile), `sync`, `broadcast`, `get`, `publish`.

### login_status

Check the status of an in-progress login or verify existing credentials.

**Returns:** `waiting` (device flow pending), `ok` (authenticated), `not_authenticated`, or `expired`.

### whoami

Show who you're logged in as: identity, session expiry, organisation claims, and the AWS profile/credentials exo will use. Read-only. Returns an error if not logged in (call `login` first). Never returns tokens or AWS secrets.

**Parameters:**

| Parameter | Required | Description |
|-----------|----------|-------------|
| `aws` | No | Also perform a live `sts:GetCallerIdentity` call (network call) |

**Prerequisites:** Authentication (`login`).

### heartbeat_record

Start recording sync progress metrics in the background. Launches a background process that continuously captures annex sync metrics every `interval` seconds.

**Parameters:**

| Parameter | Required | Description |
|-----------|----------|-------------|
| `repo_dir` | Yes | Path to the git repository |
| `interval` | No | Recording interval in seconds (default: 2) |
| `paths` | No | Paths to track (defaults to `["."]` for the entire repo) |

**Returns immediately** after starting the background recorder. Use `heartbeat_show` to read captured metrics.

### heartbeat_show

Show the latest sync progress metrics captured by `heartbeat_record`. Read-only.

**Parameters:**

| Parameter | Required | Description |
|-----------|----------|-------------|
| `repo_dir` | Yes | Path to the git repository |

**Returns:** Structured data including per-path file counts, storage progress, active downloads/uploads, network transfer rates, and completion percentages.

**Prerequisites:** `heartbeat_record` must be running (or have run) to produce metrics.

### catalog_search

Search the ExoHub catalog for datasets and artifacts using full-text or field-specific queries. Artifact documents carry a `lineage` object when provenance was recorded: query `lineage.inputs.id:"<id>"` to find what consumed a given artifact, or `lineage.generated_by.agents.id:"<agent id>"` to find what an agent produced. Prefer `catalog_lineage` for a single artifact's lineage and direct consumers.

**Parameters:**

| Parameter | Required | Description |
|-----------|----------|-------------|
| `q` | Yes | Search query. Use `*` for all results. Supports dotfield notation (e.g., `_extra.project_id:"myproject"`, `path:*.parquet`) |
| `schema` | No | Filter by schema name (e.g., `exohub-artifact/v1`) |
| `project` | No | Filter by project ID |
| `latest` | No | Return only latest versions (default: `true`) |
| `fields` | No | Comma-separated response fields (default: `_extra,path`) |
| `sort` | No | Sort order (default: `-_extra.uploaded`, prefix `-` for descending) |
| `size` | No | Results per page (default: 10, max: 100) |

**Prerequisites:** Authentication (`login`).

### catalog_schemas

List available document schema names from the catalog. Useful for discovering what schema values to pass to `catalog_search`, including `exohub-provenance/v1` (a bundle's whole compiled provenance graph, flattened for search) alongside `exohub-artifact/v1` and friends.

**Parameters:**

| Parameter | Required | Description |
|-----------|----------|-------------|
| `filter` | No | Glob pattern to filter schema names (default: `exohub-*`) |

**Prerequisites:** Authentication (`login`).

### catalog_download

Download data files, artifacts, or entire projects from the catalog.

**Parameters:**

| Parameter | Required | Description |
|-----------|----------|-------------|
| `id` | Yes | Artifact ID (e.g., `myproject:file.txt@v1`), project@version (e.g., `myproject@v1`), bundle ID, or commit ID |
| `output_dir` | No | Output directory (default: current directory) |
| `force` | No | Re-download even if file exists with matching size (default: `false`) |

**Prerequisites:** Authentication (`login`).

### catalog_semantic_search

Perform a semantic (natural language) search over the ExoHub catalog using vector similarity. Unlike `catalog_search` which does full-text keyword matching, this tool embeds the query and retrieves semantically related chunks, entities, and relationships.

**Parameters:**

| Parameter | Required | Description |
|-----------|----------|-------------|
| `q` | Yes | Natural language query (e.g. `"datasets related to cell proliferation"`, `"BRCA1 gene expression studies"`) |
| `types` | No | Result types to include: `chunks`, `entities`, `relationships` (defaults to all) |
| `project` | No | Filter results by project ID |
| `limit` | No | Maximum results per type (default: 10, max: 100) |

**Prerequisites:** Authentication (`login`).

### catalog_entities

Retrieve entity details and relationships from the ExoHub catalog knowledge graph. Entities are extracted from catalog documents (genes, diseases, pathways, datasets, etc.). Use this to explore how a specific entity connects to other entities across the catalog.

**Parameters:**

| Parameter | Required | Description |
|-----------|----------|-------------|
| `name` | Yes | Entity name to look up (e.g. `"BRCA1"`, `"Alzheimer's disease"`, `"RNAseq"`) |
| `include_relationships` | No | Whether to include related relationships (default: `true`) |

**Prerequisites:** Authentication (`login`).

### catalog_lineage

Show an artifact's recorded lineage from the ExoHub catalog (its `lineage` field, written by `exo bundle` from provenance records: what generated it, its inputs, and the agents involved) plus its direct consumers — other catalog artifacts whose `lineage.inputs` reference this artifact. Complements `provenance_show`, which reads an artifact's lineage from the git repository directly rather than from what has been published to the catalog. Use `catalog_search` (schema `exohub-artifact/v1` or `exohub-provenance/v1`) to find artifact ids first. Read-only.

**Parameters:**

| Parameter | Required | Description |
|-----------|----------|-------------|
| `id` | Yes | Artifact ID to look up (e.g. `"myproject:file.txt@v1"`), as returned by `catalog_search` in `_extra.id` |
| `consumers_size` | No | Max number of direct consumers to return (default: 10, max: 100) |

**Prerequisites:** Authentication (`login`).

### docs

Fetch exo usage documentation for agents. Read-only. With no arguments, returns the llms.txt index of available doc pages. Pass `page` with a link from that index (or a path under the docs base) to fetch that page's content. If the docs site is unreachable, returns the attempted URL and the error instead of failing.

**Parameters:**

| Parameter | Required | Description |
|-----------|----------|-------------|
| `page` | No | A page link from the llms.txt index, or a path under the docs base |

## Architecture

The MCP server runs as a child process of your agent, communicating over stdin/stdout via JSON-RPC. When the agent session ends, stdin closes and the server exits cleanly, killing any background processes it spawned (sync, heartbeat recorder, login).

All tool definitions are centralized in a shared registry, ensuring that `exo mcp list`, `exo mcp show`, and the running server always expose the same tools and descriptions.
