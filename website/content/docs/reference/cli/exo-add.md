---
title: "exo add"
description: "Add files or URLs with repository-level git/annex routing"
---

Full `exo add --help` output, generated from exo-cli v0.140.2.

```text
Add files or URLs to git/git-annex tracking.

Auto mode routes new (untracked) files in this order:
  1. Protected paths: .exohub/**, .gitignore, .gitattributes → git
  2. annex.routing in .exohub/config: first match wins (top-down)
  3. Fallback: binary → annex; text → git unless size >= threshold
     (default 5 MiB; large text prompts only on a TTY, otherwise → annex)
Directories are expanded and ignored files are excluded. Tracked files keep
their current backend; changing rules never silently migrates them.

The fallback threshold resolves as --large-text-threshold >
EXOHUB_LARGE_TEXT_THRESHOLD > 5 MiB. Overrides affect only the fallback,
never explicit rules. Sizes accept bytes, KB/MB or KiB/MiB.

Use --explain to inspect destinations and reasons without staging anything.
Use --reroute to preview tracked-file migrations; add --yes to apply them.
Protected paths are never moved by --reroute.

Override with --annex (force git-annex) or --git (force git tracking).

Use --force with --annex or --git to migrate files that are already tracked:
  --annex --force  migrates git-tracked files to git-annex
  --git --force    migrates annexed files back to git

URLs (http://, https://, ftp://) are always added via git annex addurl.
By default, --preserve-filename is passed to git-annex, which instructs it
to use the filename provided by the server as-is (git-annex checks the
Content-Disposition response header automatically). If the URL has no
usable path component, the last path component of the URL is used as a
--file fallback.
Use --file to override the local filename (may include a subdirectory path).

Routing flags (consumed by exo, not forwarded to git):
  --explain                    Show routing decisions without side effects
  --json                       Machine-readable output (requires --explain)
  --reroute                    Preview tracked files whose backend would change
  --yes                        Apply migrations (requires --reroute)
  --large-text-threshold SIZE   Override fallback large-text threshold
Environment: EXOHUB_LARGE_TEXT_THRESHOLD sets the fallback threshold.

exo add exohub:<project>[:<path>]@<version> reuses an asset, folder, whole
project, or named collection from another ExoHub project, pinned to a
version, without copying its annexed content up front:
  exo add exohub:myproject:data/file.csv@main       # one asset (as file.csv)
  exo add exohub:myproject:data/@main --to mydata/  # a folder, under --to
  exo add exohub:myproject@main --to vendor/        # the whole project
  exo add exohub:myproject:collection:qc@main       # a named collection
Flags (exohub: references only):
  --to DIR        Destination directory (or file, for a single asset)
  --verify        Re-resolve via the source's git remote and fail on any
                  mismatch with the catalog/local-clone resolution
  --repo URL      Git remote to resolve from when the project has no local
                  clone and is not in the catalog

Flags:
  -J, --jobs NUMBER  Number of parallel jobs for git-annex operations
                     (default: $EXOHUB_JOBS or 1)

Usage:
  exo add [--annex|--git] [--force] [paths/URLs]...


Flags:
  -h, --help   help for add

Global Flags:
      --config-dir string   Root directory for all config, cache and credentials (overrides EXO_CONFIG_DIR)
      --context string      Use specific context (overrides selected context)

Docs for agents (llms.txt): https://genentech.github.io/exohub/llms.txt

Full manual: https://genentech.github.io/exohub/docs/reference/cli/exo-add/
```
