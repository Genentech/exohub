---
title: "ExoHub Data Service: Agent-Facing Dataset Gateway"
---

## 1. Overview

The **ExoHub Data Service** is a standalone HTTP service that gives AI agents and automated pipelines
a simple, secure interface for consuming and producing ExoHub datasets. It sits between the compute
environment and ExoHub, handling the git-level mechanics so that agents never need direct git or
git-annex access.

Two fundamental operations are supported:

| Direction | Operation | Description |
|-----------|-----------|-------------|
| **Consume** | `request_dataset` | Download a dataset from ExoHub into the compute environment |
| **Produce** | `upload_dataset` | Upload a local folder in place as a new dataset version in ExoHub |

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

Each deployment runs with a **service account** used for **git-level** operations (cloning dataset
repositories). **Data access is per-user, not service-account-bounded**: the Data Service exchanges
the requesting user's identity token for that user's *own* short-lived cloud credentials, so an
agent can only read the object-storage data the requesting user is entitled to — never more. In
addition, per-dataset access checks are always enforced against the requesting user's JWT.

---

## 2. REST API

For integrations that prefer plain HTTP over the MCP protocol, the Data Service exposes a REST API.
It shares the MCP tools' handlers and returns the same asynchronous operation model.

An interactive OpenAPI reference is served by every deployment at `/api/docs`.

### Endpoints

| Method | Path | Success | Description |
|--------|------|---------|-------------|
| `GET` | `/` | `200` | Deployment discovery — service version, supported scopes, data root, endpoints. No auth. |
| `POST` | `/api/datasets/download` | `202` | Start (or re-use) a dataset download |
| `POST` | `/api/datasets/upload` | `202` | Start (or re-use) a dataset upload |
| `GET` | `/api/operations/{id}` | `200` | Poll operation status, progress, and result |

The two `POST` endpoints return `202 Accepted` immediately — they start background work and never
block on the transfer.

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

### POST /api/datasets/download

Starts a download. The dataset is identified in **one of two ways** — supply either shape, never
both. A body mixing them (or carrying any unknown key) is rejected with `422`.

**Shape A — by repository:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `repository` | string | ✓ | Full git URL of the dataset repository |
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
curl -X POST "$EDS/api/datasets/download" \
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
curl -X POST "$EDS/api/datasets/download" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{ "project_id": "mnist", "scope": "user" }'
```

Both return:

```json
{
  "operation_id": "ds-download-mnist-1ad7be7e5b6d17ac",
  "operation_url": "{{< exohub "dataServiceURL" >}}/api/operations/ds-download-mnist-1ad7be7e5b6d17ac?t=Ux3n_S9pQ2t7bK1v"
}
```

> **The catalog is the source of truth.** Both shapes are resolved against the ExoHub catalog before
> anything is dispatched, and a dataset that is not catalogued cannot be downloaded — you get `404`
> even when the git URL is valid and reachable. Resolution also means `latest` is pinned to a
> concrete version at request time, and that a repository request and a project request for the same
> dataset converge on the same operation.

### POST /api/datasets/upload

Uploads a folder from the compute filesystem **in place**: the source directory itself becomes the
dataset. It must already be inside the requesting user's own workspace. Uploads are user-scope only
— there is no `scope` field. Files are left as ordinary writable files (not locked into read-only
symlinks), so the same directory can be changed and uploaded again to publish an updated version.

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `source_folder` | string | ✓ | Absolute path to the directory to upload, as the worker sees it. Must be inside your own workspace. |
| `project_id` | string | | Catalog project id (also the repository name). Defaults to the last path segment of `source_folder`. An id the catalog already holds is an update, allowed only to the project's owners. |
| `read_access` | `"public"` \| `"authenticated"` \| `"viewers"` \| `"owners"` | | Who may read the dataset. Omit it to keep the project's current value, or the deployment's default on a new project. |
| `owners` | array of string \| null | | Usernames/groups that may publish new versions and always read the dataset. **Replaces** the current owners; cannot be empty. Omit it to keep the current owners. |
| `viewers` | array of string \| null | | Usernames/groups that may read the dataset when `read_access` is `"viewers"`. Replaces the current viewers; omit it to keep them. |
| `commit_message` | string | | Message for the upload's commit. Omit it for `"feat: upload dataset <project_id>"`. |

Returns the same `202` shape as download: `operation_id` and `operation_url`.

By default the published dataset is **not** private: the repository is created with internal
visibility and the dataset is readable by any authenticated ExoHub user. Narrow it with
`read_access` and `viewers`, or widen it to anyone with `read_access: "public"`.

```bash
curl -X POST "$EDS/api/datasets/upload" \
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

### GET /api/operations/{id}

Returns the current state of an operation. Authorize it **either** with your JWT **or** with the
capability token from `operation_url` — not both.

```bash
# With your JWT
curl -H "Authorization: Bearer $TOKEN" \
  "$EDS/api/operations/ds-download-mnist-1ad7be7e5b6d17ac"

# With the capability token from operation_url (no login required)
curl "$EDS/api/operations/ds-download-mnist-1ad7be7e5b6d17ac?t=Ux3n_S9pQ2t7bK1v"
```

**Response envelope:**

| Field | Type | Description |
|-------|------|-------------|
| `status` | string | `running`, `completed`, `failed`, `canceled`, or `terminated` |
| `progress` | object \| null | Live progress. Present while `running`; `null` otherwise. |
| `result` | object \| null | Final result. Present when `completed`; `null` otherwise. |
| `error` | string \| null | Failure detail — which activity failed and the root cause |
| `reason` | string \| null | Machine-readable failure class, when recognised |
| `message` | string \| null | Human-readable, actionable failure message, when recognised |

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
| `activity_type` | string | e.g. `check_dataset_read_permission`, `dataset_clone`, `dataset_exo_get` |
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
        "activity_type": "dataset_exo_get",
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
| `status` | string | `ok` on success |
| `error` | string | Empty on success |
| `scope` | string | `user` or `global` (downloads) |
| `repo_dir` | string | Where the dataset was materialised (downloads) — see [On-disk layout](#on-disk-layout) |
| `repo_url` | string | Git URL of the dataset's repository (uploads) |
| `files_synced` | integer | Annexed files fetched (downloads) |
| `bytes_synced` | integer | Bytes fetched (downloads) |
| `files_total` | integer \| null | Total files in this version |
| `bytes_total` | integer \| null | Total bytes in this version |
| `annex_files_total` | integer \| null | Total annexed files in this version |
| `files_added` | integer | Files committed (uploads) |
| `commit_sha` | string | Commit created (uploads) |
| `version` | string | Branch published to (uploads) |
| `catalog_job_url` | string | Cataloguing job that indexes the dataset after the operation finishes (uploads) |
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
    "repo_dir": "/data/users/alice/mnist/v0.1.0",
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
| `400` | Scope not supported by this deployment, or a required destination path is unconfigured |
| `401` | Missing or invalid JWT |
| `403` | Your groups are not authorised for this deployment, or `global` scope was denied because the dataset is not public |
| `404` | Dataset is not in the catalog; or the operation does not exist / is not yours |
| `422` | Body matched neither download shape, mixed both, or contained an unknown key |
| `502` | Malformed catalog entry, or the workflow could not be started |
| `503` | Catalog unreachable, or the deployment's task queue is not configured |

Failures that happen *during* the operation are not HTTP errors — they surface on the poll as
`status: "failed"` with `error` populated, and `reason` / `message` set for recognised classes:

```json
{
  "status": "failed",
  "progress": null,
  "result": null,
  "error": "activity 'dataset_exo_get' failed [MAXIMUM_ATTEMPTS_REACHED]: Code: 1: get: 12 failed",
  "reason": "credentials_expired",
  "message": "Your credentials expired during the download and could not be refreshed. Please submit a new download request with a fresh token."
}
```

### Example: end-to-end walkthrough

```bash
EDS={{< exohub "dataServiceURL" >}}     # your deployment's Data Service base URL
TOKEN=<your ExoHub JWT>

# 1. Start the download; capture the operation id
OP=$(curl -s -X POST "$EDS/api/datasets/download" \
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

# 4. Upload results back as a new dataset (source_folder must be inside your own workspace)
curl -s -X POST "$EDS/api/datasets/upload" \
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

#### `request_dataset` — Download a dataset

Downloads an ExoHub dataset into the compute environment for the requesting user.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `repository` | string | ✓ | Full git URL of the dataset repository |
| `ref` | string | | Git ref (branch, tag, or commit SHA). Defaults to the latest catalogued version. |
| `scope` | `"user"` \| `"global"` | | Destination scope (see [Scopes](#4-scopes)). Defaults to `"user"`. |
| `wait_seconds` | number | | Optional, default `0` (immediate return). A positive value waits up to that many seconds for a terminal state before returning the final payload inline, instead of a separate `get_operation`/`wait_for_operation` poll. MCP-only — there is no REST equivalent. |

**Returns**: an `operation_id` and an `operation_url` — poll with `get_operation` (or long-poll with
`wait_for_operation`) for progress.

> **Note:** the MCP tool identifies datasets by repository only. To download by catalog
> `project_id` / `version`, use the [REST endpoint](#post-apidatasetsdownload), which supports both
> request shapes.

#### `upload_dataset` — Upload a folder as a new dataset version

Uploads a local folder in the compute environment **in place** as a new ExoHub dataset version: the
source directory itself becomes the dataset. It must be inside the requester's own workspace —
there is no `scope` parameter, and uploads never target the global path. By **default** the
published dataset is **not** private: the repository is created with internal visibility and is
readable by any authenticated ExoHub user. Narrow it with `read_access` and `viewers`, or widen it
to anyone with `read_access: "public"`.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `source_folder` | string | ✓ | Absolute path to the directory to upload. Must be inside your own workspace. |
| `project_id` | string | | Catalog project id (also the repository name). Defaults to the last path segment of `source_folder`. |
| `read_access` | `"public"` \| `"authenticated"` \| `"viewers"` \| `"owners"` | | Who may read the dataset. Omit it to keep the current/default value. |
| `owners` | array of string | | Usernames/groups that may publish new versions. Replaces the current owners; cannot be empty. |
| `viewers` | array of string | | Usernames/groups that may read the dataset when `read_access` is `"viewers"`. |
| `commit_message` | string | | Message for the upload's commit. Omit it for `"feat: upload dataset <project_id>"`. |

**Returns**: an `operation_id` and an `operation_url` — poll with `get_operation` (or long-poll with
`wait_for_operation`) for progress.

#### `get_operation` — Poll async operation status

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `operation_id` | string | ✓ | Operation ID returned by `request_dataset` or `upload_dataset` |

**Returns**: the same envelope as [`GET /api/operations/{id}`](#get-apioperationsid) — `status`,
`progress`, `result`, and failure fields.

#### `wait_for_operation` — Long-poll an operation to completion

Long-polls an existing operation (from `request_dataset` or `upload_dataset`) until it reaches a
terminal state or `timeout_seconds` elapses, instead of repeatedly calling `get_operation` yourself.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `operation_id` | string | ✓ | Operation ID returned by `request_dataset` or `upload_dataset` |
| `timeout_seconds` | number | | How long to wait for a terminal state before returning. |

**Returns**: the same terminal payload as `get_operation` if the operation finishes in time
(`status: completed/failed/canceled/terminated` plus `result` or `error`). Otherwise returns a
`status: "running"` payload carrying `next_action` — a ready-to-call
`{tool: "wait_for_operation", arguments: {operation_id, timeout_seconds}}` — so you can call it again
to keep waiting; the workflow keeps running server-side even if the connection drops.

### Example: Full Agent Workflow



```json
// 1. Download a dataset into the user workspace
{
  "tool": "request_dataset",
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

// 4. Upload the results as a new dataset (source_folder must be inside your own workspace)
{
  "tool": "upload_dataset",
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

The `scope` parameter controls where a downloaded dataset lands. It defaults to `"user"`.

### `user` scope

- **Destination**: the requesting user's personal workspace directory
- **Access**: any dataset the user can read in ExoHub
- **Use case**: exploratory analysis, personal pipelines

### `global` scope

- **Destination**: a shared path accessible to all processes in the deployment
- **Access**: **public datasets only** — `read_access == "public"` in ExoHub
- **Restrictions**: externally-stored datasets (IAM-only access) are never eligible for global
  scope, even if they are marked public
- **Use case**: shared reference data, pre-staged inputs for batch jobs

> **Fail-closed**: if the dataset does not meet the global eligibility criteria, the operation
> is rejected with an access error. No partial downloads occur.

### On-disk layout

Within the scope's destination root, each dataset gets a container directory, and **each version
gets its own git worktree** backed by a single shared object store:

```
{destination}/{project_id}/
├── .git-store/        shared git + annex objects for every version
├── v0.1.0/            worktree — what you read
└── v0.2.0/            worktree — a second version, sharing the store above
```

`result.repo_dir` points at the per-version worktree, which is a fully-valid ExoHub clone you can
read directly. Because versions share one object store, downloading a second version of the same
dataset only transfers what is genuinely new. Version names are URL-encoded, so a ref like
`release/2.9.0` becomes the directory `release%2F2.9.0`.

---

## 5. Async Operations

All Data Service operations are **long-running** and backed by [Temporal](https://temporal.io/)
workflows inside ExoWorkers. Operations may take seconds to minutes depending on dataset size.

### Operation Lifecycle

```
POST /api/datasets/download   (or /upload)
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

Operation IDs are **deterministic**, derived from what is being downloaded and for whom:

| Operation | Identity |
|-----------|----------|
| Download, `user` scope | project + version + username |
| Download, `global` scope | project + version (shared across all users) |
| Upload | source directory + username (not the project id — the source directory is consumed in place, so two uploads must never run against it at once) |

Re-issuing a request that is already in flight does **not** start a second workflow — it returns the
existing `operation_id`. This makes retries safe, lets several agents converge on one shared global
download, and means a repository-shaped request and a project-shaped request for the same dataset
return the same operation. Different versions of the same dataset are distinct operations and run
concurrently.

### Polling

Poll `get_operation` (MCP) or `GET /api/operations/{id}` (REST) until `status` is `completed`,
`failed`, `canceled`, or `terminated`. There is no push or webhook mechanism — polling is the
intended pattern.

A few seconds between polls is plenty; every poll is a Temporal query, and progress counters only
advance as files land. Back off to 15–30 seconds for large datasets, and treat the `activities`
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
{{< exohub "dataServiceURL" >}}/api/operations/ds-download-mnist-1ad7be7e5b6d17ac?t=Ux3n_S9pQ2t7bK1v
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

### Per-Dataset Access Checks

Before starting any download, the Data Service checks that the requesting user (identified by the
JWT) has read access to the requested dataset. This check is performed against ExoHub's access
control system, not the service account's permissions. The outcome is recorded in the operation's
`result.permission` so you can see *why* access was granted.

### Deployment-Level Authorization

Each deployment restricts which user groups may use it via an `allowed_groups` configuration key.
Requests from users outside the allowed groups are rejected with `403` before any dataset check
occurs. The check is fail-closed: a deployment with no allowed groups configured accepts nobody, and
the wildcard `"*"` is required to allow all users.

---

## 7. Provenance

The Data Service records precise provenance for every upload operation, ensuring datasets are always
traceable back to their origin.

### Download Provenance

Downloads do **not** use `exo download`. Instead, the service performs a full git workflow:

1. `git clone` — clones the ExoHub dataset repository
2. `exo init` — initializes the local ExoHub workspace
3. `exo get` — materializes the requested file content from storage backends

`exo get` is a **uni-directional pull** (it only fetches annexed content into the local repo) —
deliberately not `exo sync`, which is bidirectional and would push local state back, violating the
read-only contract of a download. This ensures the local workspace is a fully-valid ExoHub clone,
not a bare file copy.

### Upload Provenance

Every `upload_dataset` operation commits as the **requesting user**, not a service account:

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
| `data_root` | Root directory on the compute filesystem where datasets are staged |
| `user_workspace_pattern` | Template for per-user workspace paths (e.g. `{data_root}/users/{username}`) |
| `global_path` | Shared path for `global`-scoped datasets |
| `scopes` | Which scopes this deployment accepts (a request for any other scope is rejected with `400`) |
| `allowed_groups` | ExoHub groups allowed to use this deployment (`"*"` allows all) |
| `git_location` | Base URL of the ExoHub git hosting (used for `git clone`) |
| `git_annex_remotes` | Annex remote definitions (annex, export, atlas) used during `exo get` |

A deployment's effective configuration can be read at runtime from its discovery endpoint:

```bash
curl -s "$EDS/" | jq .
```

### Integration with ExoHub Components

The Data Service is deployed alongside [ExoWorkers](/docs/components/exoworkers/) — Temporal
workflows execute the actual git and transfer operations. The service itself is a thin HTTP
frontend that dispatches work to ExoWorkers and forwards status back to the caller.
