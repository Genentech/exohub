---
title: "ExoHub Data Service: Agent-Facing Project Gateway"
---

## 1. Overview

The **ExoHub Data Service** is a standalone HTTP service that gives AI agents and automated pipelines
a simple, secure interface for consuming and producing ExoHub projects. It sits between the compute
environment and ExoHub, handling the git-level mechanics so that agents never need direct git or
git-annex access.

Four operations are supported:

| Direction | Operation | Description |
|-----------|-----------|-------------|
| **Consume** | `download_project` | Download a project from ExoHub into the compute environment |
| **Produce** | `upload_project` | Upload a local folder in place as a new project version in ExoHub |
| **Clean up** | `prune_project` | Delete a project's local copy, or one downloaded version of it |
| **Inspect** | `describe_project` | Report the state of a project's local copy |

### Standalone Deployments

Each Data Service instance is deployed per compute environment and registered under a `deployment_id`.
A central `/mcp` dispatcher routes agent requests to the correct deployment based on the
`deployment_id` in the request path.



```
Agent  ──POST /mcp/research──▶  Data Service (research deployment)
Agent  ──POST /mcp/staging──▶   Data Service (staging deployment)
```


A single Data Service process can host multiple deployments; each deployment is configured
independently with its own data root, workspace paths, and git remotes.

### Security Model

Each deployment runs with a **service account** used for **git-level** operations (cloning project
repositories). **Data access is per-user, not service-account-bounded**: the Data Service exchanges
the requesting user's identity token for that user's *own* short-lived cloud credentials, so an
agent can only read the object-storage data the requesting user is entitled to — never more. In
addition, per-project access checks are always enforced against the requesting user's JWT.

---

## 2. REST API

For integrations that prefer plain HTTP over the MCP protocol, the Data Service exposes a REST API.
It shares the MCP tools' handlers and returns the same asynchronous operation model.

An interactive OpenAPI reference is served by every deployment at `/api/docs`.

### Endpoints

| Method | Path | Success | Description |
|--------|------|---------|-------------|
| `GET` | `/` | `200` | Deployment discovery — service version, supported scopes, data root, endpoints. No auth. |
| `POST` | `/api/projects/download` | `202` | Start (or re-use) a project download |
| `POST` | `/api/projects/upload` | `202` | Start (or re-use) a project upload |
| `POST` | `/api/projects/prune` | `202` | Start (or re-use) the deletion of a project's local copy |
| `GET` | `/api/projects/describe` | `200` | Describe a project's local copy, synchronously |
| `GET` | `/api/operations/{id}` | `200` | Poll operation status, progress, and result |

The three `POST` endpoints return `202 Accepted` immediately — they start background work and never
block on the transfer.

The former paths under `/api/datasets/` still answer, but are no longer listed; use the paths above.

### Authentication

All `/api/*` endpoints require an ExoHub JWT:

```
Authorization: Bearer <jwt>
```

`GET /api/operations/{id}` additionally accepts a **capability token** in place of a JWT, which is
what makes `operation_url` shareable — see [Capability Tokens](#capability-tokens).

The examples below assume two shell variables:

```bash
EDS={{< exohub "dataServiceURL" >}}     # your deployment's Data Service base URL
TOKEN=<your ExoHub JWT>
```

### POST /api/projects/download

Starts a download. The project is identified in **one of two ways** — supply either shape, never
both. A body mixing them (or carrying any unknown key) is rejected with `422`.

**Shape A — by repository:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `repository` | string | ✓ | Full git URL of the project repository |
| `ref` | string \| null | | Git ref (branch, tag, or SHA). Defaults to the latest catalogued version. |
| `scope` | `"user"` \| `"global"` | | Defaults to `"user"` |

**Shape B — by catalog project:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `project_id` | string | ✓ | ExoHub catalog project ID |
| `version` | string \| null | | Catalog version. Defaults to the latest version. |
| `scope` | `"user"` \| `"global"` | | Defaults to `"user"` |

**Response — `202 Accepted`:**

| Field | Type | Description |
|-------|------|-------------|
| `operation_id` | string | Deterministic operation ID — poll it, or reuse it to deduplicate |
| `operation_url` | string | Ready-to-open status URL carrying a capability token |

Download by repository:

```bash
curl -X POST "$EDS/api/projects/download" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
        "repository": "{{< exohub "cloneExample" >}}",
        "ref": "v0.1.0",
        "scope": "user"
      }'
```

Download by catalog project (latest version, since `version` is omitted):

```bash
curl -X POST "$EDS/api/projects/download" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{ "project_id": "mnist", "scope": "user" }'
```

Both return:

```json
{
  "operation_id": "ds-download-dev-mnist-1ad7be7e5b6d17ac",
  "operation_url": "{{< exohub "dataServiceURL" >}}/api/operations/ds-download-dev-mnist-1ad7be7e5b6d17ac?t=Ux3n_S9pQ2t7bK1v"
}
```

> **The catalog is the source of truth.** Both shapes are resolved against the ExoHub catalog before
> anything is dispatched, and a project that is not catalogued cannot be downloaded — you get `404`
> even when the git URL is valid and reachable. Resolution also means `latest` is pinned to a
> concrete version at request time, and that a repository request and a project request for the same
> project converge on the same operation.

### POST /api/projects/upload

Uploads a folder from the compute filesystem **in place**: the source directory itself becomes the
project. It must already be inside the requesting user's own workspace. Uploads are user-scope only
— there is no `scope` field. Files are left as ordinary writable files (not locked into read-only
symlinks), so the same directory can be changed and uploaded again to publish an updated version.

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `source_folder` | string | ✓ | Absolute path to the directory to upload, as the worker sees it. Must be inside your own workspace. |
| `project_id` | string | | Catalog project id (also the repository name). Defaults to the last path segment of `source_folder`. An id the catalog already holds is an update, allowed only to the project's owners. |
| `read_access` | `"public"` \| `"authenticated"` \| `"viewers"` \| `"owners"` | | Who may read the project. Omit it to keep the project's current value, or the deployment's default on a new project. |
| `owners` | array of string \| null | | Usernames/groups that may publish new versions and always read the project. **Replaces** the current owners; cannot be empty. Omit it to keep the current owners. |
| `viewers` | array of string \| null | | Usernames/groups that may read the project when `read_access` is `"viewers"`. Replaces the current viewers; omit it to keep them. |
| `commit_message` | string | | Message for the upload's commit. Omit it for `"feat: upload project <project_id>"`. |

Returns the same `202` shape as download: `operation_id` and `operation_url`.

By default the published project is **not** private: the repository is created with internal
visibility and the project is readable by any authenticated ExoHub user. Narrow it with
`read_access` and `viewers`, or widen it to anyone with `read_access: "public"`.

```bash
curl -X POST "$EDS/api/projects/upload" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
        "source_folder": "/data/users/alice/workspace/mnist-analysis-alice-20260601",
        "project_id": "mnist-analysis-alice-20260601",
        "read_access": "viewers",
        "viewers": ["bob"],
        "commit_message": "add MNIST classification results"
      }'
```

### POST /api/projects/prune

Deletes a project's local copy — the whole project, or one downloaded version of it — from your
workspace or the shared path. There is no `scope` field: it follows from where `path` is.

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `path` | string | ✓ | Absolute path, as the worker sees it, of a directory **directly** inside your workspace or the shared path; symlinks are resolved first. A path deeper inside a project is refused with `400`. Any directory at that level is accepted, project or not — a directory that is not a repository counts as entirely unpublished, so only `force` deletes it. |
| `version` | string | | One downloaded version to remove, with the content no other version uses. Omit it to remove the whole project. Refused with `400` when `path` is an existing directory that is not a download; a version (or project) that is not there completes with nothing removed. |
| `force` | boolean | | Delete even unpublished work. Default `false`. |

Returns the same `202` shape as download: `operation_id` and `operation_url`.

**What the check looks for without `force`.** Before deleting, the prune looks for work no remote
holds — in each version folder of a download (or only the one named by `version`), or in a
repository such as an upload's: uncommitted or untracked changes, commits that no remote-tracking
branch or tag contains, and annexed content no **configured** remote of the repository has (a copy
in someone's private clone does not count). In a version folder the commits looked at are those
reachable from its checked-out commit; in a repository, those reachable from its checked-out commit
or any local branch other than `git-annex` and `synced/*`. If it finds any, the operation fails and
names each kind of change with a count and a few example paths:

```text
…/workspace/mnist/v0.1.0 holds 3 unpublished change(s): 2 modified (e.g. v0.1.0/train.csv,
v0.1.0/labels.csv); 1 untracked (e.g. v0.1.0/notes.md). Prune again with force to delete them.
```

> **Not covered by the check** — these are deleted without warning, even without `force`: files
> ignored by `.gitignore`, stashes, commits reachable only from a tag you never pushed, and commits
> your local remote-tracking branches wrongly show as pushed (the check does not ask the server).
> When a whole download is pruned, also: anything in the project folder outside its version
> folders and `.git-store/` (such as files you added next to them), uncommitted or untracked
> changes in the working tree of `.git-store/` itself, and commits reachable only from local
> branches that no version folder has checked out. In a repository, commits reachable only from
> `git-annex` or `synced/*` branches are not checked either.
> Move anything you need out of the project first.

Without `force`, it then drops annexed content only once git-annex has verified it on a remote;
anything it cannot verify is kept and the directory is left in place. With `force` nothing is
verified: a whole-project prune deletes the directory as is, and a version prune drops its content
unchecked. If your credentials expire part-way, send the same request again — it continues where
it stopped.

- **Shared path**: pruning a `global` copy requires being an owner of the project in the catalog
  (by username or group). It is checked once the operation runs, so a non-owner gets `202` and then
  a failed operation; a project the catalog records no permissions for cannot be pruned there, not
  even with `force`.
- **One prune at a time**: a prune of the same directory and version while one runs returns the
  running operation — whoever started it, and **whatever its `force`**, because `force` is not part
  of the operation's identity. A prune is refused while a download or upload of the project runs (see
  [Project lock](#project-lock)).

```bash
curl -X POST "$EDS/api/projects/prune" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"path": "/data/users/alice/workspace/mnist", "version": "v0.1.0"}'
```

### GET /api/projects/describe

Reports, synchronously, what a project's local copy holds. It reads the copy on the API itself and
never waits for a worker, so it answers even while long downloads keep every worker busy. It changes
nothing in your files; it only reads the repositories (with git and git-annex) and the project's
lock file.

| Query parameter | Required | Description |
|-----------------|----------|-------------|
| `path` | ✓ | Absolute path of a project, or of anything inside it, in your workspace or the shared path |

| Field | Type | Description |
|-------|------|-------------|
| `project_root` | string | The project directory `path` belongs to |
| `project_id` | string | The catalog's project id, found through the copy's origin URL (else the directory name) |
| `layout` | string | `worktrees` (a download: one folder per version), `repository` (an upload's repository), or `none` |
| `running_operations` | array | Operations currently holding the project: `operation_id` and `mode` (`shared` for downloads, `exclusive` for uploads and prunes) |
| `local_versions` | array | One entry per version folder of a `worktrees` copy, or the current branch of a `repository` (below) |
| `catalog_versions` | array of string | Versions the catalog holds, visible to you, for the repository the copy's `origin` URL names (an exact match). Empty when the folder holds no repository, the repository has no `origin`, or the catalog has no entry for that URL. |

Each `local_versions` entry: `version`, `commit`, `detached` (`true` for a download worktree; `false`
for a branch you can change and upload), `catalogued` (whether `commit` is the one the catalog
records for that version), and `unpublished_changes` — grouped by kind, each kind once with its
`count` and at most five `examples`, so the response stays small however many files changed. These
come from the same check a prune runs, with the same blind spots (see **Not covered by the check**
under [prune](#post-apiprojectsprune)): an empty list does not mean a prune would lose nothing.

```json
{
  "project_root": "/data/users/alice/workspace/mnist",
  "project_id": "mnist",
  "layout": "worktrees",
  "running_operations": [],
  "local_versions": [
    {"version": "v0.1.0", "commit": "7ec78f52da961f6aa954759900518a5aabde05ae", "detached": true,
     "catalogued": true, "unpublished_changes": []},
    {"version": "v0.2.0", "commit": "20e7e8c534dca71c4e050dce14aaea91bdd013fa", "detached": true,
     "catalogued": true,
     "unpublished_changes": [{"change": "untracked", "count": 2, "examples": ["notes.md", "scratch/run.log"]}]}
  ],
  "catalog_versions": ["v0.1.0", "v0.2.0"]
}
```

Reading every version of a large project takes time: if reading the local copy exceeds the time
limit (60 s by default) describe answers `504`. The limit covers only that read; the catalog lookup
and the running-operations lookup come after it, so allow a client timeout somewhat above it.

### GET /api/operations/{id}

Returns the current state of an operation. Authorize it **either** with your JWT **or** with the
capability token from `operation_url` — not both.

```bash
# With your JWT
curl -H "Authorization: Bearer $TOKEN" \
  "$EDS/api/operations/ds-download-dev-mnist-1ad7be7e5b6d17ac"

# With the capability token from operation_url (no login required)
curl "$EDS/api/operations/ds-download-dev-mnist-1ad7be7e5b6d17ac?t=Ux3n_S9pQ2t7bK1v"
```

**Response envelope:**

| Field | Type | Description |
|-------|------|-------------|
| `status` | string | `running`, `completed`, `failed`, `canceled`, or `terminated` |
| `environment` | string \| null | Environment of the deployment that started the operation (e.g. `dev`, `prd`) |
| `progress` | object \| null | Live progress. Present while `running`; `null` otherwise. |
| `result` | object \| null | Final result. Present when `completed`; `null` otherwise. |
| `error` | string \| null | Failure detail — which activity failed and the root cause |
| `reason` | string \| null | Machine-readable failure class, when recognised |
| `message` | string \| null | Human-readable, actionable failure message, when recognised |
| `failed_files` | array \| null | For the download content failures below: a sample (up to 5) of the files that could not be fetched, each with `path`, `key`, `copies`, `remotes`, and the `commit` / `author` that introduced it |

Note that a **failed operation still returns HTTP `200`** — the HTTP status describes the poll, not
the work. Always branch on the `status` field.

#### The `progress` object

| Field | Type | Description |
|-------|------|-------------|
| `step` | string \| null | Current phase, e.g. `cloning`, `exo_init`, `syncing`, `waiting_for_repo_lock` |
| `files_synced` | integer | Annexed files fetched so far |
| `bytes_synced` | integer | Bytes fetched so far |
| `files_total` | integer \| null | Total files in this version (`null` when unknown) |
| `bytes_total` | integer \| null | Total bytes in this version (`null` when unknown) |
| `annex_files_total` | integer \| null | Total annexed files in this version (`null` when unknown) |
| `files_added` | integer | Files staged so far (upload operations) |
| `ts` | string \| null | Timestamp of the most recent progress report |
| `activities` | array | Per-activity detail (below) |

The `*_total` fields come from the catalog and are available as soon as the operation starts, so
`files_synced` / `files_total` can be rendered as a progress ratio from the very first poll.

Each entry in `activities` describes one in-flight unit of work:

| Field | Type | Description |
|-------|------|-------------|
| `activity_type` | string | e.g. `check_project_read_permission`, `project_clone`, `project_exo_get` |
| `state` | string | `scheduled`, `started`, or `cancel_requested` |
| `attempt` | integer \| null | Current attempt (counts from 1) — climbing means it is retrying |
| `last_failure` | string \| null | Why the last attempt failed |
| `stdout_tail` | string \| null | Tail of the underlying command's stdout |
| `stderr_tail` | string \| null | Tail of the underlying command's stderr |
| `last_heartbeat_time` | string \| null | When the activity last reported in |
| `step`, `files_synced`, `bytes_synced`, `files_added` | | Same meaning as above, for this activity alone |

A running download:

```json
{
  "status": "running",
  "progress": {
    "step": "syncing",
    "files_synced": 3,
    "bytes_synced": 12058624,
    "files_total": 8,
    "bytes_total": 33576840,
    "annex_files_total": 6,
    "files_added": 0,
    "ts": "2026-09-11T12:55:48.543299+00:00",
    "activities": [
      {
        "activity_type": "project_exo_get",
        "state": "started",
        "attempt": 1,
        "last_failure": null,
        "stdout_tail": "get train-images-idx3-ubyte.gz (from s3-annex...) ok",
        "stderr_tail": null,
        "last_heartbeat_time": "2026-09-11T12:55:48.550314Z",
        "step": "syncing",
        "files_synced": 3,
        "bytes_synced": 12058624,
        "files_added": 0
      }
    ]
  },
  "result": null,
  "error": null,
  "reason": null,
  "message": null
}
```

#### The `result` object

| Field | Type | Description |
|-------|------|-------------|
| `status` | string | `ok` on success; `degraded` for a download that completed but still misses files |
| `error` | string | Empty on success |
| `scope` | string | `user` or `global` (downloads and prunes) |
| `repo_dir` | string | Where the project was materialised (downloads) — see [On-disk layout](#on-disk-layout); for a prune, the project or version directory it removed |
| `repo_url` | string | Git URL of the project's repository (uploads) |
| `files_synced` | integer | Annexed files fetched (downloads) |
| `bytes_synced` | integer | Bytes fetched (downloads) |
| `verified` | boolean | Whether the download's completeness check ran |
| `missing_files` | array of string | Files still absent after the completeness check and one repair attempt (downloads) |
| `files_total` | integer \| null | Total files in this version |
| `bytes_total` | integer \| null | Total bytes in this version |
| `annex_files_total` | integer \| null | Total annexed files in this version |
| `files_added` | integer | Files committed (uploads) |
| `commit_sha` | string | Commit created (uploads) |
| `version` | string | Branch published to (uploads) |
| `catalog_job_url` | string | Cataloguing job that indexes the project after the operation finishes (uploads) |
| `permission` | object \| null | Why read access was granted (downloads) |
| `public_access` | object \| null | How publicness was established (`global` downloads only) |

A completed download:

```json
{
  "status": "completed",
  "progress": null,
  "result": {
    "status": "ok",
    "error": "",
    "scope": "user",
    "repo_dir": "/data/users/alice/workspace/mnist/v0.1.0",
    "repo_url": "",
    "files_synced": 6,
    "bytes_synced": 33576840,
    "files_total": 8,
    "bytes_total": 33576840,
    "annex_files_total": 6,
    "files_added": 0,
    "commit_sha": "",
    "permission": {
      "granted": true,
      "username": "alice",
      "read_access": "public",
      "matched": "public",
      "reason": "read_access=public grants every user."
    },
    "public_access": null
  },
  "error": null,
  "reason": null,
  "message": null
}
```

`files_synced` counts **annexed** files, so it converges on `annex_files_total` (6) rather than
`files_total` (8) — the difference is plain git files, which arrive with the clone.

### Errors

Request-level problems are returned as standard HTTP errors with a `{"detail": "..."}` body:

| Status | Meaning |
|--------|---------|
| `400` | Scope not supported by this deployment, a required destination path is unconfigured, or a prune/describe `path` outside your workspace and the shared path (prune: not directly in it, or `version` on a project that is not a download) |
| `401` | Missing or invalid JWT |
| `403` | Your groups are not authorised for this deployment, or `global` scope was denied because the project is not public |
| `404` | Project is not in the catalog; or the operation does not exist / is not yours |
| `422` | Body matched neither download shape, mixed both, or contained an unknown key |
| `502` | Malformed catalog entry, or the workflow could not be started |
| `503` | Catalog unreachable, the deployment's task queue is not configured, or (describe) the API does not run as the worker's user and cannot read the repositories |
| `504` | Describe took longer than the deployment's time limit |

Failures that happen *during* the operation are not HTTP errors — they surface on the poll as
`status: "failed"` with `error` populated, and `reason` / `message` set for recognised classes:

```json
{
  "status": "failed",
  "progress": null,
  "result": null,
  "error": "activity 'project_exo_get' failed [MAXIMUM_ATTEMPTS_REACHED]: Code: 1: get: 12 failed",
  "reason": "credentials_expired",
  "message": "Your credentials expired during the download and could not be refreshed. Please submit a new download request with a fresh token."
}
```

| `reason` | Meaning | What to do |
|----------|---------|------------|
| `credentials_expired` | Your token expired mid-operation and could not be refreshed | Send the same request again with a fresh token |
| `project_busy` | Another running operation holds the project (see [Project lock](#project-lock)); `message` names it | Wait for that operation, then retry |
| `content_unavailable` | Annexed content has no known copy, or only in a repository that is not a remote of this deployment | Ask the project's owners to publish it; see `failed_files` |
| `remote_not_authorized` | The content is on a configured remote you are not authorized to read | Request access to the project; see `failed_files` |
| `remote_unreachable` | The content is on a configured remote that could not be reached | Retry later; see `failed_files` |

`failed_files` is a bounded sample; `message` states the true total.

### Example: end-to-end walkthrough

```bash
EDS={{< exohub "dataServiceURL" >}}     # your deployment's Data Service base URL
TOKEN=<your ExoHub JWT>

# 1. Start the download; capture the operation id
OP=$(curl -s -X POST "$EDS/api/projects/download" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{ "project_id": "mnist", "version": "v0.1.0", "scope": "user" }' \
  | jq -r .operation_id)

# 2. Poll until the operation reaches a terminal state
while true; do
  BODY=$(curl -s -H "Authorization: Bearer $TOKEN" "$EDS/api/operations/$OP")
  STATUS=$(echo "$BODY" | jq -r .status)
  echo "$BODY" | jq -r '"\(.status) \(.progress.bytes_synced // 0)/\(.progress.bytes_total // 0) bytes"'
  case "$STATUS" in completed|failed|canceled|terminated) break ;; esac
  sleep 10
done

# 3. Read where the data landed
REPO_DIR=$(echo "$BODY" | jq -r .result.repo_dir)
ls "$REPO_DIR"

# 4. Upload results back as a new project (source_folder must be inside your own workspace)
curl -s -X POST "$EDS/api/projects/upload" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{ "source_folder": "/data/users/alice/workspace/mnist-analysis-alice-20260601", "project_id": "mnist-analysis-alice-20260601" }' \
  | jq .
```

---

## 3. MCP Interface

The Data Service exposes a true [Model Context Protocol](https://modelcontextprotocol.io/) (MCP)
server over HTTP. Agents connect using any MCP-compatible client.

### Endpoint

```
POST /mcp/{deployment_id}
```

Examples:


- `POST /mcp/research`
- `POST /mcp/staging`


### Authentication

Pass a valid ExoHub JWT in the `Authorization` header:

```
Authorization: Bearer <jwt>
```

An optional header can override the deployment resolved from the URL path:



```
X-ExoHub-Deployment: research
```


### Tools

The former tool names `request_dataset`, `upload_dataset`, `prune_dataset` and `describe_dataset`
are still accepted, but are no longer listed; use the names below.

#### `download_project` — Download a project

Downloads an ExoHub project into the compute environment for the requesting user.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `repository` | string | ✓¹ | Full git URL of the project repository |
| `ref` | string | | Git ref (branch, tag, or commit SHA). Defaults to the latest catalogued version. |
| `project_id` | string | | Catalog project id — the alternative to `repository` (give one or the other) |
| `version` | string | | Catalogued version of `project_id`. Defaults to the latest catalogued version. |
| `scope` | `"user"` \| `"global"` | | Destination scope (see [Scopes](#4-scopes)). Defaults to `"user"`. |
| `wait_seconds` | number | | Optional, default `0` (immediate return). A positive value waits up to that many seconds for a terminal state before returning the final payload inline, instead of a separate `get_operation`/`wait_for_operation` poll. MCP-only — there is no REST equivalent. |

**Returns**: an `operation_id` and an `operation_url` — poll with `get_operation` (or long-poll with
`wait_for_operation`) for progress.

¹ Give `repository` (with optional `ref`) **or** `project_id` (with optional `version`), never both
— the same two shapes as the [REST endpoint](#post-apiprojectsdownload).

#### `upload_project` — Upload a folder as a new project version

Uploads a local folder in the compute environment **in place** as a new ExoHub project version: the
source directory itself becomes the project. It must be inside the requester's own workspace —
there is no `scope` parameter, and uploads never target the global path. By **default** the
published project is **not** private: the repository is created with internal visibility and is
readable by any authenticated ExoHub user. Narrow it with `read_access` and `viewers`, or widen it
to anyone with `read_access: "public"`.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `source_folder` | string | ✓ | Absolute path to the directory to upload. Must be inside your own workspace. |
| `project_id` | string | | Catalog project id (also the repository name). Defaults to the last path segment of `source_folder`. |
| `read_access` | `"public"` \| `"authenticated"` \| `"viewers"` \| `"owners"` | | Who may read the project. Omit it to keep the current/default value. |
| `owners` | array of string | | Usernames/groups that may publish new versions. Replaces the current owners; cannot be empty. |
| `viewers` | array of string | | Usernames/groups that may read the project when `read_access` is `"viewers"`. |
| `commit_message` | string | | Message for the upload's commit. Omit it for `"feat: upload project <project_id>"`. |

**Returns**: an `operation_id` and an `operation_url` — poll with `get_operation` (or long-poll with
`wait_for_operation`) for progress.

#### `prune_project` — Delete a project's local copy

Same parameters and behaviour as [`POST /api/projects/prune`](#post-apiprojectsprune): `path`
(required), `version`, `force`.

**Returns**: an `operation_id` and an `operation_url` — poll with `get_operation` (or long-poll with
`wait_for_operation`).

#### `describe_project` — Describe a project's local copy

Same parameter and response as [`GET /api/projects/describe`](#get-apiprojectsdescribe): `path`
(required). Answers at once — no operation to poll.

#### `get_operation` — Poll async operation status

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `operation_id` | string | ✓ | Operation ID returned by `download_project`, `upload_project` or `prune_project` |

**Returns**: the same envelope as [`GET /api/operations/{id}`](#get-apioperationsid) — `status`,
`progress`, `result`, and failure fields.

#### `wait_for_operation` — Long-poll an operation to completion

Long-polls an existing operation (from `download_project`, `upload_project` or `prune_project`) until it reaches a
terminal state or `timeout_seconds` elapses, instead of repeatedly calling `get_operation` yourself.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `operation_id` | string | ✓ | Operation ID returned by `download_project`, `upload_project` or `prune_project` |
| `timeout_seconds` | number | | How long to wait for a terminal state before returning. |

**Returns**: the same terminal payload as `get_operation` if the operation finishes in time
(`status: completed/failed/canceled/terminated` plus `result` or `error`). Otherwise returns a
`status: "running"` payload carrying `next_action` — a ready-to-call
`{tool: "wait_for_operation", arguments: {operation_id, timeout_seconds}}` — so you can call it again
to keep waiting; the workflow keeps running server-side even if the connection drops.

### Example: Full Agent Workflow



```json
// 1. Download a project into the user workspace
{
  "tool": "download_project",
  "arguments": {
    "dataset": "datasets/reference-genome-hg38",
    "ref": "v2.1.0",
    "scope": "user"
  }
}
// → { "operation_id": "op_01HXYZ..." }

// 2. Poll until complete
{
  "tool": "get_operation",
  "arguments": { "operation_id": "op_01HXYZ..." }
}
// → { "status": "completed", "path": "/data/users/<you>/datasets/reference-genome-hg38" }

// 3. ... agent processes data, writes results into its own workspace at
//    /data/users/<you>/workspace/hg38-analysis-results ...

// 4. Upload the results as a new project (source_folder must be inside your own workspace)
{
  "tool": "upload_project",
  "arguments": {
    "source_folder": "/data/users/<you>/workspace/hg38-analysis-results",
    "project_id": "hg38-analysis-results"
  }
}
// → { "operation_id": "op_01HABC..." }

// 5. Poll until uploaded
{
  "tool": "get_operation",
  "arguments": { "operation_id": "op_01HABC..." }
}
// → { "status": "completed", "dataset": "datasets/hg38-analysis-results@v0.1.0" }
```


---

## 4. Scopes

The `scope` parameter controls where a downloaded project lands. It defaults to `"user"`.

### `user` scope

- **Destination**: the requesting user's personal workspace directory
- **Access**: any project the user can read in ExoHub
- **Use case**: exploratory analysis, personal pipelines

### `global` scope

- **Destination**: a shared path accessible to all processes in the deployment
- **Access**: **public projects only** — `read_access == "public"` in ExoHub
- **Restrictions**: externally-stored projects (IAM-only access) are never eligible for global
  scope, even if they are marked public
- **Use case**: shared reference data, pre-staged inputs for batch jobs

> **Fail-closed**: if the project does not meet the global eligibility criteria, the operation
> is rejected with an access error. No partial downloads occur.

### On-disk layout

Within the scope's destination root, each project gets a container directory, and **each version
gets its own git worktree** backed by a single shared object store:

```
{destination}/{project_id}/
├── .git-store/        shared git + annex objects for every version
├── v0.1.0/            worktree — what you read
└── v0.2.0/            worktree — a second version, sharing the store above
```

`result.repo_dir` points at the per-version worktree, which is a fully-valid ExoHub clone you can
read directly. Because versions share one object store, downloading a second version of the same
project only transfers what is genuinely new. Version names are URL-encoded, so a ref like
`release/2.9.0` becomes the directory `release%2F2.9.0`.

---

## 5. Async Operations

Downloads, uploads and prunes are **long-running** operations backed by [Temporal](https://temporal.io/)
workflows inside ExoWorkers; they may take seconds to minutes depending on project size. `describe`
is the exception: it answers synchronously and starts no operation.

### Operation Lifecycle

```
POST /api/projects/download   (or /upload, /prune)
        │
        ▼
  202 Accepted — operation_id + operation_url returned immediately
        │
        ▼
  Temporal workflow executes in background
        │
        ├── status: "running"    (actively transferring / committing)
        ├── status: "completed"  (done; `result` populated)
        ├── status: "failed"     (`error` populated)
        ├── status: "canceled"   (cancelled before finishing)
        └── status: "terminated" (forcibly stopped by an operator)
```

### Operation identity

Operation IDs are **deterministic**, derived from what the operation acts on and for whom:

| Operation | Identity |
|-----------|----------|
| Download, `user` scope | project + version + username |
| Download, `global` scope | project + version (shared across all users) |
| Upload | source directory + username (not the project id — the source directory is consumed in place, so two uploads must never run against it at once) |
| Prune, `user` scope | project directory + version + username |
| Prune, `global` scope | project directory + version (shared across all owners) |

Re-issuing a request that is already in flight does **not** start a second workflow — it returns the
existing `operation_id`. This makes retries safe, lets several agents converge on one shared global
download, and means a repository-shaped request and a project-shaped request for the same project
return the same operation. Different versions of the same project are distinct operations; whether
they may run at the same time is decided by the project lock below.

### Project lock

Every download, upload and prune holds its **project** — the directory directly inside your
workspace or the shared path — for as long as it runs:

| Operation | Holds the project | Runs alongside |
|-----------|-------------------|----------------|
| Download | shared | other downloads of the project (e.g. another version) — their steps take turns on the project's shared store, so the transfers themselves run one after another |
| Upload | exclusive | nothing on that project |
| Prune | exclusive | nothing on that project |

An operation that would conflict is still accepted (`202`) but fails at once with
`reason: "project_busy"`, and `message` names the operation in its way — wait for it, then send
the request again. `describe` shows who holds a project in `running_operations`. A holder that
ended without releasing (a crash, an operator terminating it) never blocks the project: the next
operation finds it is no longer running and takes over.

### Polling

Poll `get_operation` (MCP) or `GET /api/operations/{id}` (REST) until `status` is `completed`,
`failed`, `canceled`, or `terminated`. There is no push or webhook mechanism — polling is the
intended pattern.

A few seconds between polls is plenty; every poll is a Temporal query, and progress counters only
advance as files land. Back off to 15–30 seconds for large projects, and treat the `activities`
entries as the place to look when an operation seems stuck — a climbing `attempt` with a
`last_failure` means it is retrying, and `stderr_tail` usually says why.

---

## 6. Auth & Access Control

### JWT Validation

The Data Service validates the `Authorization: Bearer <jwt>` token independently on every request.
Tokens are issued by ExoHub's {{< exohub "authName" >}} OAuth server. The service never caches user identity across
requests.

### Capability Tokens

Every `202` response includes an `operation_url` — the operation's status URL with an unguessable
capability token appended as `?t=<token>`:

```
{{< exohub "dataServiceURL" >}}/api/operations/ds-download-dev-mnist-1ad7be7e5b6d17ac?t=Ux3n_S9pQ2t7bK1v
```

Anyone holding that URL can poll the operation without logging in, which makes it safe to paste into
a browser, a chat message, or a CI log line. The operation ID itself is deterministic and therefore
guessable, so it is **not** treated as a secret — the token is. A request carrying a wrong token
returns `404` rather than `403`, and never falls back to identity auth, so a bad link cannot be used
to probe which operations exist.

### Credential Vending

For data held in object storage, the Data Service does **not** hold standing data-access
credentials. Instead it exchanges the requesting user's identity token via {{< exohub "authName" >}} (the STS exchange)
for that user's **own** short-lived cloud credentials, and uses those to read the data directly.
Access is therefore bounded by the user's own identity and entitlements — the service cannot read
anything the user could not read themselves. These per-user credentials are cached briefly
server-side (until shortly before they expire), so a multi-file transfer reuses a single token
exchange rather than re-exchanging per object.

### Per-Project Access Checks

Before starting any download, the Data Service checks that the requesting user (identified by the
JWT) has read access to the requested project. This check is performed against ExoHub's access
control system, not the service account's permissions. The outcome is recorded in the operation's
`result.permission` so you can see *why* access was granted.

### Deployment-Level Authorization

Each deployment restricts which user groups may use it via an `allowed_groups` configuration key.
Requests from users outside the allowed groups are rejected with `403` before any project check
occurs. The check is fail-closed: a deployment with no allowed groups configured accepts nobody, and
the wildcard `"*"` is required to allow all users.

---

## 7. Provenance

The Data Service records precise provenance for every upload operation, ensuring projects are always
traceable back to their origin. This is git-level provenance (who committed what, and how data was
fetched) — a different, lower-level concept from the `exo provenance` W3C PROV records covered in the
[user guide]({{< ref "/docs/user-guide/provenance" >}}), which track the lineage of individual files and folders
within a repository.

### Download Provenance

Downloads do **not** use `exo download`. Instead, the service performs a full git workflow:

1. `git clone` — clones the ExoHub project repository
2. `exo init` — initializes the local ExoHub workspace
3. `exo get` — materializes the requested file content from storage backends

`exo get` is a **uni-directional pull** (it only fetches annexed content into the local repo) —
deliberately not `exo sync`, which is bidirectional and would push local state back, violating the
read-only contract of a download. This ensures the local workspace is a fully-valid ExoHub clone,
not a bare file copy.

### Upload Provenance

Every `upload_project` operation commits as the **requesting user**, not a service account:

| Git field | Value |
|-----------|-------|
| `Author` | Requesting user (name + email from JWT, falling back to the service identity if those claims are absent) |
| `Committer` | Requesting user (same name + email as `Author`) |

There is no `Co-authored-by` trailer and no separate "Data Service account as committer" step: the
upload sets both `GIT_AUTHOR_*` and `GIT_COMMITTER_*` to the uploading user's identity, so the
requesting user is recorded as both the author and the committer of the resulting commit.

---

## 8. Configuration

Each deployment is configured independently. Below are the key configuration parameters an operator
sets per deployment.

| Key | Description |
|-----|-------------|
| `deployment_id` | Unique identifier for this deployment (used in the MCP URL path) |
| `data_root` | Root directory on the compute filesystem where projects are staged |
| `user_workspace_pattern` | Template for per-user workspace paths (e.g. `{data_root}/users/{username}/workspace`) |
| `global_path` | Shared path for `global`-scoped projects |
| `scopes` | Which scopes this deployment accepts (a request for any other scope is rejected with `400`) |
| `allowed_groups` | ExoHub groups allowed to use this deployment (`"*"` allows all) |
| `git_location` | Base URL of the ExoHub git hosting (used for `git clone`) |
| `git_annex_remotes` | Annex remote definitions (annex, export, atlas) used during `exo get` |

A deployment's effective configuration can be read at runtime from its discovery endpoint:

```bash
curl -s "$EDS/" | jq .
```

One setting is an environment variable of the API process rather than a deployment key, and is not
shown by the discovery endpoint:

| Environment variable | Description |
|----------------------|-------------|
| `DS_DESCRIBE_TIMEOUT_SECONDS` | How long `describe` may read a project's local copy before answering `504` (default `60`) |

### Integration with ExoHub Components

The Data Service is deployed alongside [ExoWorkers](/docs/components/exoworkers/) — Temporal
workflows execute the actual git and transfer operations. The service itself is a thin HTTP
frontend that dispatches work to ExoWorkers and forwards status back to the caller. The one
exception is `describe`, which reads the project on the API itself; for that the API runs as the
same user as the workers.
