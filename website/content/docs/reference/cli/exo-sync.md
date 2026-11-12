---
title: "exo sync"
description: "Sync git-annex content and record bad files"
---

Full `exo sync --help` output, generated from exo-cli v0.140.2.

```text
Sync git-annex content and record bad files.

Catalog publishing:
  --bundle-dir selects one existing bundle directory (relative or absolute).
  Version comes from bundle.json, not HEAD or the directory name.
  Without it, selection remains automatic. --path filters annex files, not bundles.
  Example: exo sync --with exohub-atlas --bundle-dir .exohub/bundles/v1

Filtering options (for import and annex remotes):
  --include and --exclude accept git-annex glob patterns:
    *       matches any characters except /
    **      matches any characters including /
    ?       matches single character
  Examples: "*.bam", "results/**/*.csv", "data/2024-*/*.fastq.gz"
  Note: Filtering is not supported for export remotes (they mirror entire branches).

Import options (for import remotes):
  --no-content imports metadata only without storing file content locally.
               Creates tracking info (checksums, keys, locations) but drops
               content immediately. Useful for populating repos without storage.

Dry-run guarantee (--dry-run):
  The --dry-run flag produces a faithful, remote-type-aware preview of exactly
  what the real sync would do. Specifically:
  - Export remotes: actions are computed against the remote's annex-tracking-branch
    (not HEAD). If the current branch differs from the tracking branch, the dry-run
    reports no export actions and emits a clear warning.
  - Annex remotes with preferred content: the "to copy" list is filtered by the
    preferred content expression using a matcher that correctly handles ** globs.
  - No "drop candidates" section is ever shown. The real annex/export sync never
    drops local content, so reporting drops would be misleading.

Completeness check (--check):
  A tag or commit can reference annexed content that was never copied to any
  remote (e.g. a file replaced in a later commit, never synced at that old
  version). --check reports this without running any sync:
    exo sync --check                  check every local branch and tag
    exo sync --check --ref v1          check only ref v1 (repeatable)
    exo sync --check --json            stable {refs_checked,items,summary} report
    exo sync --check --verify          also verify presence on remotes (checkpresentkey)
                                        instead of trusting the location log; catches a
                                        remote whose storage moved without a UUID change
                                        (e.g. a re-pointed S3 prefix, see 'exo init --remote')
  Exit codes: 0 clean, 3 fixable (present in this clone but on no configured
  remote — check out that ref and run 'exo sync' to upload it), 4 lost
  (missing here and on every remote). --json items get a "verified" flag set
  when --verify is what caught them (as opposed to the location log alone).

Full-history sweep (--all):
  --all uploads the annexed content of every local branch and tag to every
  writable, plain annex remote, per ref and routing-aware (a remote's own
  preferred content expression governs what it receives, same as a normal
  sync). It is the explicit, full-cost counterpart to --check: use it to
  repair fixable content found by --check, or to seed a remote from an
  existing repository's full history. Catalog, import, export, readonly and
  write-restricted remotes are skipped (they use other sync mechanisms).
  Supports --dry-run and --json.

Previewing a ref's catalog target (--dry-run --json --ref):
  exo sync --dry-run --json --ref <ref> [--ref <ref>...] previews, for a
  catalog remote, the routing (s3url, atlas_url, instance_url) that
  publishing <ref> would use right now — read fresh from the CURRENT
  .exohub/remotes, never from <ref>'s own checked-out copy — alongside
  <ref>'s own identity (ref, ref_commit). This is how a guard (e.g. a
  catalog service's automation) verifies the target before 'exo publish
  --ref' actually runs: a ref that predates a routing change (a
  bucket/prefix move, a new atlas_url) still resolves to the current
  catalog, not a stale or missing one. Requires at least one catalog remote
  (via --with or auto-discovery).

Handling a force-pushed (rewritten) remote main (--reset-synced):
  If a remote's main branch is rewritten (force-pushed) after this clone last
  synced with it, the local git-annex synced/main branch no longer shares
  history with the remote's new main, and the sync/broadcast fails (typically
  with a merge conflict). exo never refuses up front: only when the sync has
  actually failed does it check whether <r>'s main was rewritten since this
  clone last observed it, and if so it adds a hint to the error:
    remote <r> rewrote main (force-push) since this clone last synced, which
    is why the sync failed: its synced/main refs still hold the old history.
    Run 'exo sync --reset-synced <r>' ...
  and aborts any merge the failed sync left in progress, so the working tree
  is back to its state before the sync. Unpushed local commits alone never
  trigger this hint.
  'exo sync --reset-synced <r>' resets the local synced/main branch (and, if
  <r>'s own synced/main was left stale by the rewrite, force-updates <r>'s
  synced/main too -- otherwise the next sync would just re-pull the stale
  history and hit the same conflict) to match <r>'s rewritten main. It
  always refuses, regardless of --yes, if local main has genuinely unpushed
  commits -- ones <r> doesn't have in either its current or pre-rewrite
  history (listing them) -- so no work is ever silently discarded. If local
  main instead turns out to hold ONLY history <r> rewrote away (no local
  work at risk), it refuses with a plain hint to adopt the new history first
  (e.g. 'git fetch <r> && git reset --hard <r>/main', after checking that's
  safe) and re-run. Once local main has no unpushed or stale-only commits,
  --reset-synced previews the reset; pass --yes to perform it. It never
  touches main or any other branch. Affects exo sync, exo broadcast, and exo
  publish (step 1 and step 4, which call them).

Usage:
  exo sync [flags]


Flags:
      --all                   Upload annexed content referenced by every local branch and tag to every writable remote (full sweep; see exo sync --check for a report without uploading)
      --bundle-dir string     Publish one existing bundle directory (catalog remotes only; not an annex file filter)
      --check                 Check that annexed content referenced by branches/tags is present on configured remotes (no sync performed; see exit codes in the sync reference docs)
      --dry-run               Report planned sync actions without executing git-annex sync
      --exclude stringArray   Exclude files matching glob pattern (repeatable, for import/annex remotes)
  -h, --help                  help for sync
      --import-dir string     Directory to place imported files (for import remotes, e.g. 'input/')
      --include stringArray   Include files matching glob pattern (repeatable, for import/annex remotes)
  -J, --jobs string           Number of parallel jobs (default: 1)
      --json                  Emit a single JSON dry-run report to stdout
      --log-prefix string     Prefix for log filenames (useful for Temporal workflows)
      --manifest string       Manifest YAML file
      --no-content            Import metadata only without storing content locally (import remotes only)
      --path stringArray      Path to limit sync (repeatable)
      --ref stringArray       Limit --check to this ref (repeatable; default checks all local branches and tags), or with --dry-run --json preview a catalog publish of this ref's content+version under the current routing
      --reset-synced string   Reset local synced/main for this remote after it force-pushed (rewrote) main (see exo-cli#178); refuses if local main has commits not on the remote
      --verify                With --check, also verify presence on remotes (checkpresentkey) instead of trusting the location log
      --with stringArray      Annex remote to sync
      --yes                   With --reset-synced, perform the reset instead of only previewing it

Global Flags:
      --config-dir string   Root directory for all config, cache and credentials (overrides EXO_CONFIG_DIR)
      --context string      Use specific context (overrides selected context)

Docs for agents (llms.txt): https://genentech.github.io/exohub/llms.txt

Full manual: https://genentech.github.io/exohub/docs/reference/cli/exo-sync/
```
