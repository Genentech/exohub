---
title: "Using AI Agents with ExoHub (MCP Server)"
description: "Learn how to connect AI agents like Claude Code or Cursor to ExoHub via the built-in MCP server"
---

ExoHub ships with a built-in [Model Context Protocol](https://modelcontextprotocol.io/) (MCP) server that exposes data management operations as tools for AI agents. Once registered, an agent like Claude Code or Cursor can clone repositories, sync data, check status, and publish datasets — without you having to remember git-annex commands.

This tutorial walks you through registering the MCP server, discovering its tools, and running a complete workflow with an agent.

For the full tool reference, see the [MCP Server user guide]({{< ref "docs/user-guide/mcp-server" >}}).

## Prerequisites

Install `exo` (Linux/macOS):

```bash
{{< exohub "installCmd" >}}
export PATH="$HOME/.local/bin:$PATH"
exo version
```

You also need an MCP-compatible client. This tutorial uses Claude Code as the primary example, but any client that supports stdio MCP servers works (Cursor, Windsurf, etc.).

## 1. Register the MCP Server

### Claude Code

Register the ExoHub MCP server with a single command:

```bash
claude mcp add --transport stdio exo -- exo mcp start
```

That's it. Claude Code will launch `exo mcp start` automatically whenever you start a session. No background process to manage.

### Other Clients (`.mcp.json`)

For Cursor, Windsurf, or any client that reads `.mcp.json`, add the following to your project's `.mcp.json` (or your global MCP config):

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

The server communicates over stdin/stdout (stdio transport) and exits cleanly when the agent session ends.

## 2. Discover Available Tools

Before asking your agent to do anything, it helps to know what tools are available. Run these from your terminal:

```bash
# List all tools with a one-line description
exo mcp list
```

```
add                Add files to tracking with automatic content-type detection
broadcast          Broadcast git-annex metadata to remotes without transferring file content
catalog_download   Download files, artifacts, or entire projects from the catalog
catalog_schemas    List available document schema names in the catalog
catalog_search     Search the ExoHub catalog for datasets and artifacts
clone              Clone an existing data repository with git-annex support
create_repo        Initialize current directory as a git-annex repo with S3 remote
heartbeat_record   Start recording sync progress metrics in the background
heartbeat_show     Show the latest sync progress metrics
locate             Show where git-annex content is physically stored across remotes
login              Authenticate with {{< exohub "authName" >}} via device flow (non-blocking)
login_status       Check whether authentication has completed
publish            Orchestrate the full publish workflow
status             Show working tree status
sync               Download and upload git-annex managed data files (background)
sync_status        Check whether a background sync is still running
```

To see the full parameter list for a specific tool:

```bash
exo mcp show sync
```

## 3. Example Workflow: Clone a Repo and Sync Data

Here is a complete example you can paste into your agent. It clones the published MNIST tutorial dataset and downloads the processed Parquet files.

**Prompt to your agent:**

> Please use the ExoHub MCP tools to:
> 1. Authenticate with ExoHub
> 2. Clone `{{< exohub "cloneExample" >}}` into `/tmp/mnist-agent`
> 3. Sync the data so the Parquet files are available locally
> 4. Report the final status

The agent will execute a sequence of tool calls like this:

### Step 1 — Authenticate

The `login` tool starts a device flow if no valid credentials exist. It returns an auth URL immediately rather than blocking:

```
Tool: login
Result: {
  "status": "device_flow_started",
  "auth_url": "{{< exohub \"appBaseURL\" >}}/activate?user_code=ABCD-1234",
  "message": "Please visit the URL above to complete authentication."
}
```

The agent will show you the URL. Visit it in your browser, then let the agent know it can continue.

### Step 2 — Poll Until Authenticated

```
Tool: login_status
Result: { "status": "ok" }
```

### Step 3 — Clone

```
Tool: clone
Args: {
  "repo_url": "{{< exohub "cloneExample" >}}",
  "directory": "/tmp/mnist-agent"
}
Result: { "status": "ok", "directory": "/tmp/mnist-agent" }
```

### Step 4 — Start Sync with Progress Monitoring

Because `sync` runs in the background, the agent starts a heartbeat recorder first, then launches the sync:

```
Tool: heartbeat_record
Args: { "repo_dir": "/tmp/mnist-agent" }

Tool: sync
Args: { "repo_dir": "/tmp/mnist-agent" }
Result: { "status": "started" }
```

### Step 5 — Poll for Completion

```
Tool: heartbeat_show
Args: { "repo_dir": "/tmp/mnist-agent" }
Result: {
  "progress": { "files_done": 4, "files_total": 6, "percent": 66 },
  "transfer_rate": "12.4 MB/s"
}

Tool: sync_status
Args: { "repo_dir": "/tmp/mnist-agent" }
Result: { "status": "not_running" }   ← sync complete
```

### Step 6 — Verify

```
Tool: status
Args: { "repo_dir": "/tmp/mnist-agent" }
Result: { "clean": true, "untracked": [], "modified": [] }
```

The agent reports back: the clone succeeded and all data files are present locally.

## 4. Example Workflow: Create and Publish a Repository

This workflow shows an agent setting up a brand-new data repository. You tell the agent the Git URL of an already-created GitLab project and the local directory to use:

**Prompt:**



> Create an ExoHub data repository in `/tmp/mydata` backed by `https://github.com/YOUR_NAMESPACE/mydata.git`. Add the CSV files from `/tmp/source/`, then sync and publish everything.


The agent will:

| Step | Tool | What happens |
|------|------|--------------|
| 1 | `login` → `login_status` | Authenticate |
| 2 | `create_repo` | `git init`, configure git-annex, create `.exohub/remotes` |
| 3 | `add` | Stage CSV files (text → git, large binaries → annex) |
| 4 | `sync` | Push content to S3 remotes |
| 5 | `sync_status` | Wait for background sync to finish |
| 6 | `publish` | Bundle metadata and sync to catalog |

At each step the agent shows you what it's doing. If anything goes wrong — a network error, a missing remote, an auth timeout — it stops and explains the problem rather than silently failing.

## 5. Searching the Catalog

You can also ask an agent to find and download data from the ExoHub catalog without knowing the exact project name:

**Prompt:**

> Search the ExoHub catalog for MNIST datasets and download the latest train.parquet to `/tmp/download/`.

```
Tool: login_status
Result: { "status": "ok" }

Tool: catalog_schemas
Result: ["exohub-artifact/v1", "exohub-bundle/v1", ...]

Tool: catalog_search
Args: { "q": "mnist path:*.parquet", "schema": "exohub-artifact/v1" }
Result: [
  { "_extra": { "project_id": "tutorials/mnist", "version": "v3" }, "path": "data/processed/train.parquet" },
  { "_extra": { "project_id": "tutorials/mnist", "version": "v3" }, "path": "data/processed/test.parquet" }
]

Tool: catalog_download
Args: { "id": "tutorials/mnist:data/processed/train.parquet@v3", "output_dir": "/tmp/download" }
Result: { "status": "ok", "path": "/tmp/download/train.parquet" }
```

## What Operations Are Supported

The MCP server covers the full ExoHub data lifecycle:

| Category | Tools |
|----------|-------|
| **Authentication** | `login`, `login_status` |
| **Repository setup** | `create_repo`, `clone` |
| **File tracking** | `add`, `status`, `locate` |
| **Sync & publish** | `sync`, `sync_status`, `broadcast`, `publish` |
| **Progress monitoring** | `heartbeat_record`, `heartbeat_show` |
| **Catalog** | `catalog_search`, `catalog_schemas`, `catalog_download` |

Operations the agent cannot perform (you still do these yourself):
- Create the GitLab repository — the agent needs an existing Git URL for `create_repo`
- Modify `.exohub/remotes` after initialization — edit the file directly, then re-run `exo init`
- Review and approve access control changes — use the [Permissions guide]({{< ref "docs/user-guide/permissions" >}})

## Tips

**Non-blocking by design** — `sync`, `login`, and `heartbeat_record` all return immediately. The agent is expected to poll `sync_status` / `login_status` / `heartbeat_show` for completion. This avoids timeouts on large transfers.

**Credentials are shared** — once you authenticate via `exo login` in the terminal (or via the agent), the credentials are cached and reused across sessions. The agent calls `login_status` to check before starting a device flow.

**Project-scoped registration** — add `.mcp.json` to your data repository so anyone who opens the project in a supported editor gets the ExoHub tools automatically.

## What's Next

- **Full tool reference** — see the [MCP Server user guide]({{< ref "docs/user-guide/mcp-server" >}}) for all parameters and return values
- **Build a dataset from scratch** — follow the [MNIST tutorial]({{< ref "tutorials/mnist" >}}) to understand the underlying `exo` commands the agent is calling
- **Syncing and versioning** — see [Syncing Data]({{< ref "docs/user-guide/syncing" >}}) for how `exo sync`, `exo broadcast`, and `exo publish` work under the hood
