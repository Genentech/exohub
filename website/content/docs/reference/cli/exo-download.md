---
title: "exo download"
description: "Download artifact files from the catalog"
---

Full `exo download --help` output, generated from exo-cli v0.140.2.

```text
Download artifact files from an ArtifactDB catalog.

Download a single file by artifact ID:
  exo download "myproject:file.txt@v1"

Download all files in a project/version:
  exo download "myproject@v1"

The catalog URL is resolved in this order:
  1. --url flag
  2. EXOHUB_CATALOG_URL environment variable
  3. catalog_url field in the active exo context

Remote download via --eds:
  exo download --eds my-eds myproject@v1
  exo download --eds https://eds.example.com myproject
  exo download --eds my-eds myproject@v1 --no-content
  exo download --eds my-eds myproject@v1 --eds-params '{"content": false}'
  exo download --eds my-eds myproject@v1 --eds-params @opts.json

With --eds, nothing is downloaded to this machine. The named Data Service
(EDS) fetches the dataset into its own storage; this command only submits
the request and follows its progress. --eds accepts either an index id
(resolved via the ExoHub /eds index) or a full https:// URL to the EDS.
It is mutually exclusive with -o/--output-dir and -f/--force, and only
accepts a dataset (project or project@version), not an artifact id.
Ctrl-C does not cancel the remote download; the download keeps running on
the EDS and the status URL is printed so it can be followed separately.

--no-content, --worktrees, --checkout, --eds-force, and --eds-params are
only meaningful with --eds, and are user-scope-only options the EDS itself
enforces (using them with another scope is rejected by the EDS, not by
exo). Only flags you actually set are sent, so an exo built against a
newer EDS still works against an older one as long as you don't ask for a
field it predates. --eds-params carries a JSON object of additional,
forward-compatible options merged into the request; it can be inline JSON
or @path/to/file.json. Setting the same option both as a typed flag (or
--scope) and inside --eds-params is an error, not silently resolved.

Usage:
  exo download <artifact-id | project@version> [flags]


Flags:
      --checkout                 With --eds, let an existing plain repository on the EDS switch to the requested version (user scope only)
      --eds string               Trigger a remote download on this Data Service (index id or https:// URL) instead of downloading locally
      --eds-force                With --eds, let an update or checkout discard unpublished work on the EDS copy: changed/untracked files, commits not on origin, nested repositories (user scope only; distinct from local -f/--force, which --eds already rejects)
      --eds-params string        With --eds, a JSON object (inline or @path/to/file.json) of additional download options merged into the request, for options newer than this exo's typed flags; conflicts with a typed flag or --scope are an error
  -f, --force                    Re-download even if file already exists with matching size
  -h, --help                     help for download
      --max-wait duration        With --eds, maximum time to follow the remote download before giving up (it keeps running on the EDS regardless) (default 6h0m0s)
      --no-content               With --eds, clone or update and initialise the project but fetch no annexed content (user scope only); fetch it later with 'exo get' or a download without --no-content
      --no-wait                  With --eds, submit the download and exit without following progress
  -o, --output-dir string        Output directory for downloaded files (default ".")
      --poll-interval duration   With --eds, how often to poll the operation status (must be positive; the EDS only refreshes progress every 30-120s, so polling faster doesn't help) (default 10s)
      --scope string             Scope for the remote download (e.g. user, global); omitted by default so the EDS applies its own default
      --url string               ArtifactDB catalog URL
      --worktrees                With --eds, lay out the EDS copy as one read-only worktree per version (--worktrees=false: one plain repository); user scope only, unset keeps the existing layout on disk

Global Flags:
      --config-dir string   Root directory for all config, cache and credentials (overrides EXO_CONFIG_DIR)
      --context string      Use specific context (overrides selected context)

Docs for agents (llms.txt): https://genentech.github.io/exohub/llms.txt

Full manual: https://genentech.github.io/exohub/docs/reference/cli/exo-download/
```
