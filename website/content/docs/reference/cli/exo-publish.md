---
title: "exo publish"
description: "Orchestrate sync → bundle → sync (catalog) → broadcast"
---

Full `exo publish --help` output, generated from exo-cli v0.140.2.

```text
Publish data by orchestrating a four-step workflow:

  1. Sync content to all configured remotes EXCEPT artifactdb/catalog types
  2. Generate bundle metadata (exo bundle)
  3. Sync bundle metadata to artifactdb/catalog remotes only
  4. Broadcast metadata to all remotes (exo broadcast)

Use --sync to override step 1 and sync only to specified remotes.

Publishing an older ref (--ref):
  --ref <tag|commit> publishes that ref's content and metadata (bundle,
  .artifactdb/, provenance) while routing (remotes, s3url, atlas_url,
  instance_url) is taken from the CURRENT .exohub/remotes (the checked-out
  working tree) — never from the ref's own, possibly stale, copy of that
  file. This is what makes it safe to publish a tag that predates a routing
  change (a bucket/prefix move, a new atlas_url): it always lands on the
  current catalog instead of silently falling back to a default because the
  old tag's routing is incomplete. Repeatable: --ref 1 --ref 2 publishes each
  ref in turn with the same current routing. Without --ref, publish behaves
  exactly as before (HEAD's own checked-out content and routing, subject to
  the stale-routing refusal below).

  Per ref, before publishing: 1) every catalog remote the ref's own
  .exohub/remotes references must still exist in the CURRENT config (matched
  by name, or by git-annex UUID when both record one) — otherwise publish
  refuses rather than silently drop it; 2) the ref's annexed content must
  already be present on the current annex remote(s) (a completeness check
  with --verify) — otherwise publish refuses with a hint ('exo sync --all',
  or 'exo init --allow-move --migrate-content' if a remote's storage moved),
  unless --force. The bundle and .artifactdb/ (git-archived from the ref) are
  built into a temporary directory that is removed after publishing.

Stale-routing refusal (no --ref):
  Running plain 'exo publish' from a checkout (detached HEAD, or any branch
  other than the repository's default) whose own .exohub/remotes routing
  differs from the default branch's refuses, non-zero, rather than silently
  publishing to stale or default routing. Publish that content with
  'exo publish --ref <ref>' from the default branch instead.

Rewritten remote main:
  Steps 1 (sync) and 4 (broadcast) refuse, non-zero, if a content remote
  force-pushed (rewrote) its main branch since this clone last synced with
  it, instead of hitting a merge conflict. See 'exo sync --help' for the
  'exo sync --reset-synced <remote>' fix.

Usage:
  exo publish [flags]


Flags:
      --force              With --ref, publish even if the ref's content-availability check (completeness --verify) finds fixable/lost content on the current remote(s)
  -h, --help               help for publish
      --ref stringArray    Publish this ref's content+metadata with the current routing (repeatable, e.g. --ref 1 --ref 2)
      --sync stringArray   Remote names to sync in step 1 (repeatable, overrides auto-discovery)

Global Flags:
      --config-dir string   Root directory for all config, cache and credentials (overrides EXO_CONFIG_DIR)
      --context string      Use specific context (overrides selected context)

Docs for agents (llms.txt): https://genentech.github.io/exohub/llms.txt

Full manual: https://genentech.github.io/exohub/docs/reference/cli/exo-publish/
```
