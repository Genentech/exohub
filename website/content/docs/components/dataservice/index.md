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
| **Inspect** | `list_projects` | List the projects in a workspace, catalogued or not |
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
| `POST` | `/api/projects/download` | `202` | Start (or reuse) a project download |
| `POST` | `/api/projects/upload` | `202` | Start (or reuse) a project upload |
| `POST` | `/api/projects/prune` | `202` | Start (or reuse) the deletion of a project's local copy |
| `GET` | `/api/projects/list` | `200` | List the projects in a workspace, synchronously |
| `GET` | `/api/projects/describe` | `200` | Describe a project's local copy, synchronously |
| `POST` | `/api/exo/<command>[/<subcommand>]` | `202` | Run an exo command the deployment exposes; with `help=true`, `200` and its help (see [Exo commands](#exo-commands)) |
| `GET` | `/api/exo/commands` | `200` | List the exo commands the deployment exposes |
| `GET` | `/api/operations/{id}` | `200` | Poll operation status, progress, and result |

The `POST` endpoints return `202 Accepted` immediately — they start background work and never
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
| `ref` | string \| null | | A catalogued ref: commit SHA, tag or branch. `latest` is the latest catalogued version (unless a version is catalogued under that name). Omit it to get the default branch's current tip. |
| `scope` | `"user"` \| `"global"` | | Defaults to `"user"`. `"global"` is for the project's owners and deployment admins only |

**Shape B — by catalog project:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `project_id` | string | ✓ | ExoHub catalog project ID |
| `version` | string \| null | | A catalogued version. `latest` is the latest catalogued version (unless a version is catalogued under that name). Omit it to get the default branch's current tip. |
| `scope` | `"user"` \| `"global"` | | Defaults to `"user"`. `"global"` is for the project's owners and deployment admins only |

**Both shapes, user scope only** (in `global` scope these are rejected with `422`):

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `worktrees` | bool \| null | | `true`: one detached worktree per version under the project folder (a read-only copy). `false`: one plain repository, as upload produces. Omit it to keep the layout already on disk; a new download is a plain repository. |
| `checkout` | bool | | Let an existing plain repository switch to the requested version; without it a different version fails with `version_mismatch`. Defaults to `false`. |
| `force` | bool | | Let an update or checkout of an existing plain repository go ahead over its unpublished work: changed and untracked files, and the commits not on origin of the branch it moves or of a detached HEAD, are discarded, and a nested repository (an untracked folder with its own `.git`, named in the refusal) is deleted with its own history and content; commits on other branches and annexed content are kept. Ignored files outside nested repositories are not counted and kept; as in git, one the requested version tracks is replaced. Defaults to `false`. |
| `content` | bool | | `false`: clone or update and initialise the project, but fetch no annexed content: you get the history, the files in git and the annex pointers. Content already present is kept. The operation is separate from the same download with content, and its result carries `content: "skipped"`. A later download with content, or `exo get` in the folder, fetches the content. Defaults to `true`. |

> **Without a version you get the default branch's tip**, not the latest catalogued version as before.
> Send `"version": "latest"` (or `"ref": "latest"`) to keep the old result. A branch is checked out as a
> local branch tracking origin, and later downloads fast-forward it to origin's tip, so the same request
> can bring newer content over time; a tag or commit is checked out detached. Progress totals are
> `null` until the worker has resolved the tip, and stay `null` if no catalog entry has that commit. In
> `global` scope a request without a version is refused unless the tip is a catalogued commit.

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

Download by catalog project (the default branch's tip, since `version` is omitted):

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
> even when the git URL is valid and reachable (without a version, the project must still be catalogued:
> its catalog entry gives the repository). Resolution also means `latest` is pinned to a concrete
> version at request time, and that a repository request and a project request for the same project
> converge on the same operation.

### POST /api/projects/upload

Uploads a folder from the compute filesystem **in place**: the source directory itself becomes the
project. It must already be inside the requesting user's own workspace. Uploads are user-scope only
— there is no `scope` field. Files are left as ordinary writable files (not locked into read-only
symlinks), so the same directory can be changed and uploaded again to publish an updated version.

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `source_folder` | string | ✓ | The directory to upload: an absolute path as the worker sees it, or the name of a folder directly in your workspace (e.g. `"mnist"` for `{workspace}/mnist`). Must be inside your own workspace. |
| `project_id` | string | | Catalog project id (also the repository name). Defaults to the last path segment of `source_folder`. An id the catalog already holds is an update, allowed only to the project's owners. |
| `read_access` | `"public"` \| `"authenticated"` \| `"viewers"` \| `"owners"` | | Who may read the project. Omit it to keep the project's current value, or the deployment's default on a new project. |
| `owners` | array of string \| null | | Usernames/groups that may publish new versions and always read the project. **Replaces** the current owners; cannot be empty. Omit it to keep the current owners. |
| `viewers` | array of string \| null | | Usernames/groups that may read the project when `read_access` is `"viewers"`. Replaces the current viewers; omit it to keep them. |
| `commit_message` | string | | Message for the upload's commit. Omit it for `"feat: upload project <project_id>"`. |
| `paths` | array of string \| null | | Upload only these paths, relative to `source_folder`: files, directories (everything under them) or glob patterns (`*` within a folder, `**` across folders). Only the new or modified files they match are committed; other changes stay in the folder, uncommitted. Entries that match nothing are listed in the result's `skipped_paths`. 1 to 1000 entries; omit it to upload every change. See [Uploading selected paths](#uploading-selected-paths). |

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

#### Which folder state is uploaded

An update (a project the catalog already holds) is uploaded only from a branch that origin has and is
not behind, and is pushed to that branch. The checks run before the upload changes the folder's files
or configuration, so a refused upload leaves them as they were:

| Source folder | Outcome |
|---|---|
| no repository yet | a new project on the default branch |
| on branch B, origin has B, and origin's B is B or an ancestor of it | commit, push to B |
| on branch B, origin has B with commits B lacks | refused: B is behind origin; bring it up to date first |
| on branch B, origin has no B | refused: B is not a branch of the project on origin |
| detached HEAD (at a tag or a commit) | refused: check out a branch first |
| the project's `.exohub/` but no commit of the project | refused: download the project and upload from that folder |
| a version folder of a worktrees download, or its `.git-store` | refused with `400`: a worktrees download is a read-only copy |

Uploads add and replace, they never remove. With or without `paths`, an upload is refused before
anything is committed when the folder has a staged deletion or rename (`git rm`, `git mv`; unstage it
with `git restore --staged`) or a file of the project deleted from the folder (restore it with
`git checkout -- <file>`).

After the commit and before the push, the upload checks that no tracked file differs from the commit
(what publishing requires) and fails without pushing otherwise. If publishing fails after the push
(for example a transfer cut short), send the same request again: it finds the earlier commit,
commits nothing new, and finishes the push and publish.

#### Uploading selected paths

With `paths`, only what the entries match is committed:

- Each entry is matched by git, relative to `source_folder`: a file, a directory (everything under it),
  or a glob pattern. `*` stays within a folder and `**` crosses folders, so `data/*.csv` matches
  `data/a.csv` but not `data/sub/b.csv`. Ignored files never match.
- An entry matches when it has new or modified files, or files staged or committed by an earlier
  attempt of this upload that origin does not have yet. Entries that match nothing are skipped and
  listed in the result's `skipped_paths`.
- If every entry is skipped, nothing new is committed except the upload's own files (`.exohub/`,
  `.gitignore`), and the push and publish still run, so a re-sent request finishes an earlier run. In
  a repository without any commit, the upload fails with nothing to upload.
- The upload's own files, `.exohub/` and `.gitignore`, always go along when they changed.
- Unlisted new files stay in the folder, untouched.

Refused before anything is committed:

| Folder state | Why |
|---|---|
| changes staged outside the entries | the commit would take them along; unstage them or upload without `paths` |
| tracked files changed on disk outside the entries (also type changes such as `git annex unlock`, and `git add -N` entries) | publishing refuses a folder whose tracked files differ from the commit; add them to `paths` or revert them |
| an entry covers a file whose name contains `*`, `?`, `[` or `\`, or starts with `:` | `exo add` would match the name again as a pattern and stage unlisted files; upload without `paths` |

Rejected with `422` before anything starts: an empty list, more than 1000 entries, an absolute entry,
one that leaves the folder (`..`), one starting with `:`, or one containing a NUL or line break.

```bash
curl -X POST "$EDS/api/projects/upload" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
        "source_folder": "/data/users/alice/workspace/mnist",
        "paths": ["results/", "figures/**/*.png", "README.md"],
        "commit_message": "add March results"
      }'
```

### POST /api/projects/prune

Deletes a project's local copy — the whole project, or one downloaded version of it — from your
workspace or the shared path. Give `path` as an absolute path, whose scope follows from where it is,
or as the project folder's name, looked up in `scope`.

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `path` | string | ✓ | A directory **directly** inside your workspace or the shared path: an absolute path as the worker sees it, or the folder's name (e.g. `"mnist"`); symlinks are resolved first. A path deeper inside a project is refused with `400`. Any directory at that level is accepted, project or not — a directory that is not a repository counts as entirely unpublished, so only `force` deletes it. |
| `scope` | `"user"` \| `"global"` | | Where a folder name is looked up: `"user"` (your workspace, the default) or `"global"` (the shared path). With an absolute `path`, a `scope` that does not match where it is is refused with `400`. |
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

- **Shared path**: a `global` copy can be pruned only by the project's owners (`owners` and
  `write_access` in its `.exohub/permissions` at the default branch, by username or group) and by
  deployment admins, the same people who may download it there. It is checked once the operation
  runs, so anyone else gets `202` and then a failed operation, and nothing is deleted. A folder there
  that is not a repository, or whose project has no `.exohub/permissions` or can no longer be fetched,
  has no owners to check: only an admin can prune it.
- **One prune at a time**: a prune of the same directory and version while one runs returns the
  running operation — whoever started it, and **whatever its `force`**, because `force` is not part
  of the operation's identity. A prune is refused while a download or upload of the project runs (see
  [Project lock](#project-lock)).

```bash
curl -X POST "$EDS/api/projects/prune" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"path": "mnist", "version": "v0.1.0"}'
```

The same request by absolute path, `{"path": "/data/users/alice/workspace/mnist", "version": "v0.1.0"}`,
is the same operation.

### GET /api/projects/list

Lists, synchronously, the projects in your workspace: one entry per folder directly in it, downloads,
uploads and folders not yet pushed alike. It is quick because it reads, per folder, only whether it is
a repository and its `origin` URL, and asks the catalog once per repository whether it holds it; for a
project's versions and unpublished work, [describe](#get-apiprojectsdescribe) it.

| Query parameter | Required | Description |
|-----------------|----------|-------------|
| `scope` | | What to list: `user` (your workspace, the default) or `global` (the shared path) |
| `user` | | **Deployment admins only**: whose workspace to list in `user` scope. Anyone else who sends it, even with their own name, gets `403`; a value that is not a plain username gets `400`; with `scope=global`, `422`. |

| Field | Type | Description |
|-------|------|-------------|
| `scope` | string | The scope listed |
| `user` | string \| null | Whose workspace was listed; `null` in `global` scope |
| `workspace` | string | The scope's root: that user's workspace, or the shared path |
| `projects` | array | One entry per folder directly in `workspace`, sorted by name. Files and symlinks are not listed. |

Each `projects` entry:

| Field | Type | Description |
|-------|------|-------------|
| `path` | string | The folder's name: what describe and prune take as `path` |
| `project_id` | string \| null | The catalog's project id; `null` when the folder is not catalogued |
| `repository` | boolean | Whether the folder is a git repository |
| `layout` | string \| null | `worktrees` (a download) or `repository` (an upload, or a plain-repository download); `null` when it is not a repository |
| `catalogued` | boolean \| null | For a repository, whether the catalog holds at least one version of the repository its `origin` names (an exact match); `null` when it is not a repository, or could not be read |
| `error` | string \| null | Why the folder could not be read; `null` when it was. The other folders are listed as usual. |

```json
{
  "scope": "user",
  "user": "alice",
  "workspace": "/data/users/alice/workspace",
  "projects": [
    {"path": "draft",   "project_id": null,    "repository": true,  "layout": "repository", "catalogued": false, "error": null},
    {"path": "mnist",   "project_id": "mnist", "repository": true,  "layout": "worktrees",  "catalogued": true,  "error": null},
    {"path": "scratch", "project_id": null,    "repository": false, "layout": null,         "catalogued": null,  "error": null}
  ]
}
```

A workspace that does not exist yet (you have neither downloaded nor uploaded anything) answers
`404`. Catalog searches and folder reads run at most 16 at a time, and a listing that takes longer
than the time limit (20 s by default) answers `504`.

### GET /api/projects/describe

Reports, synchronously, what a project's local copy holds. It reads the copy on the API itself and
never waits for a worker, so it answers even while long downloads keep every worker busy. It changes
nothing in your files; it only reads the repositories (with git and git-annex) and the project's
lock file.

| Query parameter | Required | Description |
|-----------------|----------|-------------|
| `path` | ✓ | A project in your workspace or the shared path: an absolute path of the project or of anything inside it, or the project folder's name (e.g. `mnist`) |
| `scope` | | Where a folder name is looked up: `user` (your workspace, the default) or `global` (the shared path). With an absolute `path`, a `scope` that does not match where it is is refused with `400`. |

| Field | Type | Description |
|-------|------|-------------|
| `project_root` | string | The project directory `path` belongs to |
| `project_id` | string | The catalog's project id, found through the copy's origin URL (else the directory name) |
| `layout` | string | `worktrees` (a worktrees download: one folder per version), `repository` (a plain repository: an upload's, or a download's), or `none` |
| `running_operations` | array | Operations currently holding the project: `operation_id` and `mode` (`shared` for worktrees downloads, `exclusive` for plain-repository downloads, uploads, prunes and exo commands). A sync exo command running in the API has no operation: it shows as `operation_id: null`, `kind: "exo_command"`, its `command` and `expires_at` |
| `local_versions` | array | One entry per version folder of a `worktrees` copy, or the one checkout of a `repository`: its branch, or an empty `version` when detached (below) |
| `catalog_versions` | array of string | Versions the catalog holds, visible to you, for the repository the copy's `origin` URL names (an exact match). Empty when the folder holds no repository, the repository has no `origin`, or the catalog has no entry for that URL. |

Each `local_versions` entry: `version`, `commit`, `detached` (`true` for a download worktree, or a
repository on a tag or commit; `false` for a branch you can change and upload), `catalogued` (whether `commit` is the one the catalog
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

### Exo commands

A deployment can expose chosen exo commands — for example `exo doctor`, `exo locate` or
`exo provenance add` — to run in your workspace. This is **optional**: which commands and parameters
are exposed is set per deployment, and many expose none. Ask the deployment with `GET /api/exo/commands`
or the MCP tool `exo_commands`; anything else answers `404` (an unlisted command) or `422` (an unlisted
parameter). A command with subcommands runs only through the subcommands listed, never bare.

`GET /api/exo/commands` lists what this deployment exposes: for each command its description, REST
endpoint, MCP tool, allowed parameters (with their group and type, read from exo), `mode` (`sync`
or `async`) and time limit. The same list is the MCP tool `exo_commands`.

Each command's description is generated from exo's own help, and is the same in the listing, in the
command's MCP tool and in `/api/docs`: what the command does and how it answers, exo's text with its
command-line examples rewritten as request fields (`exo locate --present data/` becomes
`{"present": true, "args": ["data/"]}`), the flags that text mentions but the command does not accept,
the values the request accepts (which parameters are paths inside the project, and the characters
each may use) and the parameters it does accept. Depending on the deployment, paragraphs about flags
it does not accept may be left out.

```bash
curl -H "Authorization: Bearer $TOKEN" "$EDS/api/exo/commands"
```

#### Help

`?help=true` on a command endpoint answers `200` at once with exo's own help for the command, and
runs nothing; no body is needed:

| Field | Description |
|-------|-------------|
| `kind` | `"exo_command_help"`, so it is told from a command's answer at once |
| `command` | The command, e.g. `"provenance show"` |
| `help` | exo's `--help` for it, written for exo's command line; its `Flags:` list holds only the flags the command accepts here |
| `note` | That the text is exo's and may mention flags the command does not accept; the accepted ones are under `Flags:` and in the command's description |

```bash
curl -X POST -H "Authorization: Bearer $TOKEN" "$EDS/api/exo/provenance/show?help=true"
```

#### Request

Without `help=true`, the command runs as the deployment configured it (its `mode` in the listing):

- **sync**: it runs at once, and the answer is its result: see [Sync answers](#sync-answers).
- **async**: it starts an operation: `202` with `operation_id` and `operation_url`, read with
  [`GET /api/operations/{id}`](#get-apioperationsid) (see [Exo operations](#exo-operations)).

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `path` | string | ✓ | Where the command runs: a project folder, or a version folder of a download (e.g. `"mnist/v0.1.0"`), relative to your workspace or absolute; symlinks are resolved first. Anything else, or anything in the shared path, is refused with `400`: exo commands never reach `global` copies. |
| `args` | array of string | | Positional arguments, in order; only for a command configured to take them |
| *a parameter* | boolean, string, number or array | | One of the command's allowed parameters: `true` for a flag (`false` leaves it out), a value for any other (a list, for a repeatable one) |

| Query parameter (MCP: tool argument) | Required | Description |
|--------------------------------------|----------|-------------|
| `help` | | `true` for the help instead of a run |
| `user` | | **Deployment admins only**: run in that user's workspace. Anyone else who sends it gets `403`. |

```bash
curl -X POST "$EDS/api/exo/locate" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"path": "mnist/v0.1.0", "json": true, "missing": true, "args": ["data/"]}'
```

runs `exo locate --json --missing data/` in `{workspace}/mnist/v0.1.0`.

#### Validation and safety

The API checks every request before anything runs, and refuses with `422`:

- a parameter the command does not allow, or a value of the wrong type (flag, string, integer, list,
  as exo declares it);
- a character outside the deployment's `allowed_characters` for the value (`paths` or `values`):
  `"x; rm -rf ~"` or `"$(id)"` is refused;
- whatever the configuration says: a path parameter or path argument that is absolute, contains
  `..` or leaves the project; an argument starting with `-`; a NUL, line break or other control
  character anywhere, `path` included; more than 100 parameters, more than 1000 entries in a list or
  in `args`, or a value over 4 KiB.

exo is started with an argument list, never through a shell, each value written as `--key=value`.
It runs as you, with your credentials, and `path` and the paths in the request are checked again just
before exo starts, in case one was replaced by a symlink in the meantime.

#### Running

- **One attempt.** An exo command is never retried: commands are not idempotent (a retried
  `provenance add` writes a second record). If it fails, fix the cause and send the request again.
- **Time limit.** Each command has one, set per deployment: at most 60 s for a sync command (it runs
  within one request); an async one may have none. exo is stopped when it is reached, cleanly: the git
  and git-annex processes under it get to remove their lock files first.
- **Project lock.** The command holds its project exclusively (see [Project lock](#project-lock)). A
  sync command that finds its project held answers `409` at once, and exo does not run.
- **Busy API** (sync). The API runs a limited number of sync commands at once; one more answers
  `503` with a `Retry-After` header, and exo does not run. Send it again after that many seconds.
- **One operation per request** (async). An identical request (same user, `path`, command, parameters
  and `args`) while one runs returns the running operation; different parameters start their own. A
  sync call always runs anew.
- **exo version.** The API reads each command's help when it starts. If exo's version changes after
  that, calls answer `503` until the API is restarted; a worker with another exo version fails the
  operation with `exo_version_drift` without running exo.

#### Sync answers

A sync command answers with its result, in the shape of an [exo operation](#exo-operations) without
`operation_id`, `last_heartbeat_at` and `progress`: `kind`, `command`, `input`, `status`
(`completed` or `failed`), `started_at`, `completed_at`, `duration_seconds`, `result` and, when
failed, `failure`. A completed command answers `200`; a failed one answers with the same body and the
status of its `failure.reason`:

| `failure.reason` | Status |
|------------------|--------|
| `command_failed` | `422` |
| `command_timeout` | `504` |
| `project_busy` | `409` |
| `exo_version_drift` | `503` |
| `internal_error` | `500` |

A failed command never answers `2xx`, and never `5xx` for `command_failed`: clients and proxies
retry `5xx`, and a command must not run twice. After `command_timeout`, the command was stopped
part-way and may have done some of its work: check before sending it again.

```json
{
  "kind": "exo_command",
  "command": "locate",
  "input": {"path": "mnist", "json": true, "fast": true},
  "status": "completed",
  "started_at": "2026-11-05T10:00:00+00:00",
  "completed_at": "2026-11-05T10:00:01+00:00",
  "duration_seconds": 0.7,
  "result": {"exit_code": 0, "text": "{\"file\":\"GB/file1\",\"type\":\"annex\",\"locked\":true}\n..."},
  "failure": null
}
```

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
| `repo_dir` | string | The project folder a download materialised, in both layouts — see [On-disk layout](#on-disk-layout); for a prune, the project or version directory it removed |
| `repo_url` | string | Git URL of the project's repository (uploads) |
| `files_synced` | integer | Annexed files fetched (downloads) |
| `bytes_synced` | integer | Bytes fetched (downloads) |
| `verified` | boolean | Whether the download's completeness check ran |
| `missing_files` | array of string | Files still absent after the completeness check and one repair attempt (downloads) |
| `content` | string | `skipped` for a download with `content: false` (`files_synced` and `bytes_synced` are `0`, status `ok`); absent otherwise |
| `files_total` | integer \| null | Total files in this version |
| `bytes_total` | integer \| null | Total bytes in this version |
| `annex_files_total` | integer \| null | Total annexed files in this version |
| `files_added` | integer | Files committed (uploads) |
| `commit_sha` | string | Commit created (uploads) |
| `skipped_paths` | array of string | Entries of an upload's `paths` that matched no new or modified file, so nothing of them was uploaded |
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
    "repo_dir": "/data/users/alice/workspace/mnist",
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

#### Exo operations

An async [exo command](#exo-commands)'s operation has its own shape instead of the envelope above;
every other operation keeps the envelope.

| Field | Type | Description |
|-------|------|-------------|
| `operation_id` | string | The operation's id |
| `kind` | string | `"exo_command"` |
| `command` | string | The command run, e.g. `"provenance add"` |
| `input` | object | The request as validated: `path`, the parameters sent, and `args` when sent |
| `status` | string | `running`, `completed` or `failed` |
| `started_at`, `completed_at` | string \| null | `completed_at` is `null` while running |
| `duration_seconds` | number | So far, while running |
| `last_heartbeat_at` | string \| null | When the worker last reported, while running |
| `progress` | object \| null | While running: `stdout_tail` and `stderr_tail`, the last 20 lines of each |
| `result` | object \| null | Once finished: `exit_code` (`null` after a time-out, a lost worker or a version drift), `data` when exo printed JSON or `text` otherwise, and `stderr` only when the command failed |
| `failure` | object \| null | When failed: `reason` (below), `message` (for `command_failed`, the last lines of stderr) and `detail` (`null` unless there is more) |

| `failure.reason` | Meaning | What to do |
|------------------|---------|------------|
| `command_failed` | exo exited with a non-zero code; `result` keeps its output | Read `message` and `result`, fix the cause, send the request again |
| `command_timeout` | The command's time limit was reached and exo was stopped | Narrow the command, or ask for a longer limit |
| `worker_lost` | The worker running exo stopped reporting (it died or was restarted) | Send the request again |
| `project_busy` | Another running operation holds the project; `message` names it | Wait for it, then send the request again |
| `exo_version_drift` | The worker's exo is not the version the API read the command's help from | Retry once the deployment runs one version everywhere |
| `internal_error` | Anything else, including output too large for an operation (2 MiB) | Read `message` |

Secrets are masked in `progress`, `result` and `failure`, by the worker before anything is stored and again when
the operation is read: signed URLs, `token=…` and the like in any text, and in JSON output the whole value of any
key that names a secret (`secret`, `token`, `password`, `credential`, `signature`, `session`, `api_key`,
`access_key`, …).

```json
{
  "operation_id": "ds-exo-dev-mnist-1a2b3c4d5e6f7a8b",
  "kind": "exo_command",
  "command": "doctor",
  "input": {"path": "mnist", "json": true},
  "status": "failed",
  "started_at": "2026-11-03T10:00:00+00:00",
  "completed_at": "2026-11-03T10:00:12+00:00",
  "duration_seconds": 12.0,
  "last_heartbeat_at": null,
  "progress": null,
  "result": {"exit_code": 1, "data": [{"check": "auth", "status": "FAIL"}], "stderr": "auth: FAIL ..."},
  "failure": {"reason": "command_failed", "message": "auth: FAIL ...", "detail": null}
}
```

### Errors

Request-level problems are returned as standard HTTP errors with a `{"detail": "..."}` body:

| Status | Meaning |
|--------|---------|
| `400` | Scope not supported by this deployment, a required destination path is unconfigured, or a prune/describe `path` outside your workspace and the shared path (prune: not directly in it, or `version` on a project that is not a download); or an upload `source_folder` that is a version folder of a worktrees download or its `.git-store`; or a `path`/`source_folder` that is neither absolute nor a single folder name, or a `scope` that does not match where an absolute `path` is; or a list `user` that is not a plain username; or an exo command `path` that is not a project or version folder in the workspace (or is in the shared path), or a `user` that is not a plain username |
| `401` | Missing or invalid JWT |
| `403` | Your groups are not authorised for this deployment, or `global` scope was denied because the project is not public; or a list or exo command `user` from a caller who is not a deployment admin |
| `404` | Project is not in the catalog; or the operation does not exist / is not yours; or (list) the workspace does not exist; or an `/api/exo/…` command the deployment does not expose |
| `422` | Body matched neither download shape, mixed both, or contained an unknown key; or a `global`-scope download set `worktrees`, `checkout` or `force`, or `content: false`; or an upload's `paths` was empty, had more than 1000 entries, or had an entry that is absolute, leaves the folder, starts with `:`, or contains a NUL or line break; or a list `user` with `scope=global`; or an exo command request the API refuses (see [Validation and safety](#validation-and-safety)) |
| `502` | Malformed catalog entry, or the workflow could not be started |
| `503` | Catalog unreachable, the deployment's task queue is not configured, or (describe, list) the API does not run as the worker's user and cannot read the repositories, or (list) the workspace cannot be read, or (exo commands) exo's version changed since the API started, or exo cannot be run on the API, or (sync exo commands) the API runs as many as it allows already, with `Retry-After` |
| `504` | Describe or list took longer than the deployment's time limit |

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
| `layout_mismatch` | The project folder holds the other layout than the request asks for (`worktrees`, or `checkout` on a worktrees copy) | Download with the layout on disk, or prune the project first |
| `version_mismatch` | The plain repository is on another version; `message` names it | Send the request with `checkout: true` to switch it |
| `local_changes` | The update or checkout would discard unpublished work; `message` lists it | Publish or move the work, or send `force: true` to discard it |
| `diverged` | The local branch and origin's have diverged; `message` lists the local work | Publish or move the work, or send `force: true` to reset to origin |
| `foreign_directory` | The project folder holds something else: files without a repository, another project's repository, a repository with no commit, or a symlink | Move it away; `force` does not apply |

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
| `ref` | string | | A catalogued ref: commit SHA, tag or branch; `latest` for the latest catalogued version. Omit it to get the default branch's current tip. |
| `project_id` | string | | Catalog project id — the alternative to `repository` (give one or the other) |
| `version` | string | | Catalogued version of `project_id`; `latest` for the latest catalogued version. Omit it to get the default branch's current tip. |
| `scope` | `"user"` \| `"global"` | | Destination scope (see [Scopes](#4-scopes)). Defaults to `"user"`. |
| `worktrees`, `checkout`, `force`, `content` | boolean | | User scope only, as in the [REST endpoint](#post-apiprojectsdownload). |
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
| `source_folder` | string | ✓ | The directory to upload: an absolute path, or the name of a folder directly in your workspace. Must be inside your own workspace. |
| `project_id` | string | | Catalog project id (also the repository name). Defaults to the last path segment of `source_folder`. |
| `read_access` | `"public"` \| `"authenticated"` \| `"viewers"` \| `"owners"` | | Who may read the project. Omit it to keep the current/default value. |
| `owners` | array of string | | Usernames/groups that may publish new versions. Replaces the current owners; cannot be empty. |
| `viewers` | array of string | | Usernames/groups that may read the project when `read_access` is `"viewers"`. |
| `commit_message` | string | | Message for the upload's commit. Omit it for `"feat: upload project <project_id>"`. |
| `paths` | array of string | | Upload only these paths (1 to 1000 entries), as in the [REST endpoint](#uploading-selected-paths). |

**Returns**: an `operation_id` and an `operation_url` — poll with `get_operation` (or long-poll with
`wait_for_operation`) for progress.

#### `prune_project` — Delete a project's local copy

Same parameters and behaviour as [`POST /api/projects/prune`](#post-apiprojectsprune): `path`
(required; an absolute path or a folder name), `scope`, `version`, `force`.

**Returns**: an `operation_id` and an `operation_url` — poll with `get_operation` (or long-poll with
`wait_for_operation`).

#### `describe_project` — Describe a project's local copy

Same parameters and response as [`GET /api/projects/describe`](#get-apiprojectsdescribe): `path`
(required; an absolute path or a folder name) and `scope`. Answers at once — no operation to poll.

#### `list_projects` — List the projects in a workspace

Same parameters and response as [`GET /api/projects/list`](#get-apiprojectslist): `scope` and, for
deployment admins only, `user`. Answers at once — no operation to poll.

#### `exo_commands` and `exo_<command>[_<subcommand>]` — Exo commands

Only on a deployment that exposes [exo commands](#exo-commands): `exo_commands` lists them, as
`GET /api/exo/commands`, and each exposed command is a tool named after it (`exo_doctor`,
`exo_provenance_show`; a `-` becomes `_`), its description the command's generated description. A
command's tool takes the same body as its REST endpoint (`path`, `args`, the allowed parameters),
plus `help` (`true` for exo's help instead of a run) and, for deployment admins only, `user`.

**Returns**: for a sync command, its result, as the [sync answer](#sync-answers); a failure is a tool
error carrying the same body. For an async one, an `operation_id` and an `operation_url` — poll with
`get_operation` (or long-poll with `wait_for_operation`). With `help: true`, exo's help, as
[`?help=true`](#help) answers it.

#### `get_operation` — Poll async operation status

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `operation_id` | string | ✓ | Operation ID returned by `download_project`, `upload_project`, `prune_project` or an `exo_*` tool |

**Returns**: the same envelope as [`GET /api/operations/{id}`](#get-apioperationsid) — `status`,
`progress`, `result`, and failure fields.

#### `wait_for_operation` — Long-poll an operation to completion

Long-polls an existing operation (from `download_project`, `upload_project`, `prune_project` or an `exo_*` tool) until it reaches a
terminal state or `timeout_seconds` elapses, instead of repeatedly calling `get_operation` yourself.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `operation_id` | string | ✓ | Operation ID returned by `download_project`, `upload_project`, `prune_project` or an `exo_*` tool |
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
- **Who**: the project's **owners** (`owners` and `write_access` in its `.exohub/permissions` at the
  default branch) and **deployment admins**. They are also the only ones who may prune a global copy
- **Restrictions**: externally-stored projects (IAM-only access) are never eligible for global
  scope, even if they are marked public
- **Use case**: shared reference data, pre-staged inputs for batch jobs

> **Fail-closed**: if the project does not meet the global eligibility criteria, the operation
> is rejected with an access error. No partial downloads occur.

### On-disk layout

Within the scope's destination root, each project gets one folder, `{destination}/{project_id}`
(`result.repo_dir`), in one of two layouts:

- **Plain repository** — the default for a new `user`-scope download: one git repository, as upload
  produces, on the requested version. You can change it and upload it again. A later download of the
  same version updates it (a branch fast-forwards to origin's tip); another version needs
  `checkout: true`.
- **Worktrees** — with `worktrees: true`, and always in `global` scope: **each version gets its own
  git worktree**, a read-only copy, backed by a single shared object store:

```
{destination}/{project_id}/
├── .git-store/        shared git + annex objects for every version
├── v0.1.0/            worktree — what you read
└── v0.2.0/            worktree — a second version, sharing the store above
```

A project keeps the layout it was first downloaded in. Because worktree versions share one object
store, downloading a second version of the same project only transfers what is genuinely new.
Version names are URL-encoded, so a ref like `release/2.9.0` becomes the directory
`release%2F2.9.0`; a request without a version uses the default branch's folder (e.g. `main`).

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
| Download, `user` scope | project + version (or the default branch) + `worktrees`/`checkout`/`force` + username |
| Download, `global` scope | project + version or the default branch (shared across its owners and admins) |
| Upload | source directory + username (not the project id — the source directory is consumed in place, so two uploads must never run against it at once) |
| Prune, `user` scope | project directory + version + username |
| Prune, `global` scope | project directory + version (shared across its owners and admins) |

Re-issuing a request that is already in flight does **not** start a second workflow — it returns the
existing `operation_id`. This makes retries safe, lets several agents converge on one shared global
download, and means a repository-shaped request and a project-shaped request for the same project
return the same operation. Different versions of the same project are distinct operations; whether
they may run at the same time is decided by the project lock below.

### Project lock

Every download, upload, prune and exo command holds its **project** — the directory directly inside your
workspace or the shared path — for as long as it runs:

| Operation | Holds the project | Runs alongside |
|-----------|-------------------|----------------|
| Download, worktrees (`global` scope always) | shared | other worktrees downloads of the project (e.g. another version) — their steps take turns on the project's shared store, so the transfers themselves run one after another |
| Download, plain repository | exclusive | nothing on that project: a plain repository has one working tree |
| Upload | exclusive | nothing on that project |
| Prune | exclusive | nothing on that project |
| Exo command | exclusive | nothing on that project |

An operation that would conflict is still accepted (`202`) but fails at once with
`reason: "project_busy"`, and `message` names the operation in its way — wait for it, then send
the request again. `describe` shows who holds a project in `running_operations`. A holder that
ended without releasing (a crash, an operator terminating it) never blocks the project: the next
operation finds it is no longer running and takes over.

A sync exo command is answered at once instead: `409` with `failure.reason: "project_busy"`. While it
runs, it holds the project like any operation; if the API process running it dies, its hold lapses on
its own, at the latest about 100 seconds after it started.

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
