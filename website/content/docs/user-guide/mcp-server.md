---
title: "MCP Server"
description: "Use AI agents to manage your data through the Model Context Protocol"
weight: 13
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
| `create_repo` | Initialize current directory as a git-annex repo with S3 remote |
| `add` | Add files to tracking with automatic content-type detection (text → git, binary → annex) |
| `status` | Show working tree status: untracked, modified, and staged files |
| `locate` | Show where git-annex content is physically stored across remotes |
| `sync` | Download and upload git-annex managed data files to/from remote storage (background) |
| `sync_status` | Check whether a background sync is still running |
| `publish` | Orchestrate the full publish workflow: sync content → bundle metadata → sync to catalog → broadcast |
| `broadcast` | Broadcast git-annex metadata to remotes without transferring file content |
| `login` | Authenticate via OIDC device flow (non-blocking) |
| `login_status` | Check whether authentication has completed |
| `heartbeat_record` | Start recording sync progress metrics in the background |
| `heartbeat_show` | Show the latest sync progress metrics |
| `init` | Initialize or reconfigure git-annex in an existing repository (run after `git clone`) |
| `init_remote` | Add a new storage remote to the repository |
| `info` | Show repository information: configured remotes, annex state, and storage statistics |
| `catalog_search` | Search the ExoHub catalog for datasets and artifacts |
| `catalog_schemas` | List available document schema names in the catalog |
| `catalog_download` | Download files, artifacts, or entire projects from the catalog |
| `catalog_semantic_search` | Semantic (natural language) search over the catalog using vector similarity |
| `catalog_entities` | Retrieve entity details and relationships from the catalog knowledge graph |

## Workflows

### Creating a Repository

First create a git repository on your git host (e.g., GitLab, GitHub). Then the agent asks for the repository URL and initializes the local directory:

```
1. login          → authenticate (returns auth URL for you to visit)
2. login_status   → poll until authentication completes
3. create_repo    → initialize current directory with git-annex + S3 remote
4. add            → add files to tracking
5. sync           → push content to remotes
```

### Cloning and Downloading Data

The MCP server does not expose a `clone` tool — cloning is done via `git clone` in a terminal. Once cloned, use `init` to configure git-annex remotes, then `sync` to download content:

```
1. login          → authenticate
2. login_status   → poll until authentication completes
                   (then: git clone <url> in a terminal)
3. init           → configure git-annex remotes from .exohub/remotes
4. sync           → download content from remotes
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
5. catalog_download         → download artifacts found in search results
```

## Tool Details

### create_repo

Initialize the current directory as a git-annex data repository with an S3 remote. You must first create a git repository on your git host and provide its URL.

This tool performs the following steps:
1. `git init` in the directory
2. Adds the URL as the `origin` remote
3. Initializes git-annex
4. Creates `.exohub/context` (parsed from the git URL)
5. Creates `.exohub/remotes` with an `s3-archive` annex remote configuration
6. Runs `exo init --yes` to set up the annex remote

The S3 path is derived from the directory name: `s3://exohub-sandbox-uat/sandbox/<directory-name>/_annex`.

**Parameters:**

| Parameter | Required | Description |
|-----------|----------|-------------|
| `repo_url` | Yes | Git remote URL (you must create this repo on your git host first) |
| `directory` | No | Local directory to initialize (defaults to current working directory) |

**Prerequisites:** Authentication (`login`).

### add

Add files to tracking with automatic content-type detection. In `auto` mode (default), text files go to git and binary/data files go to git-annex based on mimetype.

**Parameters:**

| Parameter | Required | Description |
|-----------|----------|-------------|
| `repo_dir` | Yes | Path to the git repository |
| `paths` | Yes | File paths to add |
| `mode` | No | `auto` (default), `annex`, or `git` |

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

Initialize or reconfigure an existing git-annex repository. Runs `exo init --yes` to ensure git-annex is set up and all remotes defined in `.exohub/remotes` are properly configured. Use this after `git clone` to enable remotes, or any time remote configuration may be stale. This is idempotent — safe to call multiple times.

**Parameters:**

| Parameter | Required | Description |
|-----------|----------|-------------|
| `repo_dir` | Yes | Path to the git repository |

### init_remote

Add a new storage remote to the repository. Creates an S3 or local remote for storing git-annex data. The remote is saved to `.exohub/remotes` for reproducible setup across clones.

**Parameters:**

| Parameter | Required | Description |
|-----------|----------|-------------|
| `repo_dir` | Yes | Path to the git repository |
| `name` | Yes | Remote name (e.g., `s3-annex`, `s3-export`) |
| `type` | Yes | Remote type: `annex`, `export`, `artifactdb`, or `exospace` |
| `s3url` | No | S3 URL (required for `annex`, `export`, `artifactdb`). Example: `s3://exohub-sandbox-uat/sandbox/<org>/<repo>/_annex` |
| `rsyncurl` | No | Rsync URL or local path (required for `exospace`). Example: `rsync://host/path` |
| `tracking_branch` | No | Tracking branch for export remotes (defaults to current branch) |
| `grants` | No | Enable fine-grained S3 Access Grants permissions (default: `true`) |

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

**Required before:** `create_repo`, `sync`, `broadcast`.

### login_status

Check the status of an in-progress login or verify existing credentials.

**Returns:** `waiting` (device flow pending), `ok` (authenticated), `not_authenticated`, or `expired`.

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

Search the ExoHub catalog for datasets and artifacts using full-text or field-specific queries.

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

List available document schema names from the catalog. Useful for discovering what schema values to pass to `catalog_search`.

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

## Architecture

The MCP server runs as a child process of your agent, communicating over stdin/stdout via JSON-RPC. When the agent session ends, stdin closes and the server exits cleanly, killing any background processes it spawned (sync, heartbeat recorder, login).

All tool definitions are centralized in a shared registry, ensuring that `exo mcp list`, `exo mcp show`, and the running server always expose the same tools and descriptions.
