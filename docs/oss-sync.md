# OSS Sync — Publishing exo-cli to github.com/Genentech/exohub

This document describes how to sync the internal `exo-cli` repository to the public
GitHub mirror at [github.com/Genentech/exohub](https://github.com/Genentech/exohub).

## Overview

The public repo is the **source of truth** for the base code. Each sync is an
incremental commit on top of public history — no orphan commits, no force-push,
no GITHUB_TOKEN. The **owner pushes manually** from their local workstation.

exo-cli owns: `go/`, root governance files (`LICENSE`, `NOTICE`, `README.md`,
`CONTRIBUTING.md`, `SECURITY.md`), and `.github/`. The `website/` subtree is
owned by the exohub-website sync pipeline and is **never touched** by this script.

## Scripts

| Script | Purpose |
|--------|---------|
| `scripts/oss-export.sh` | Stage a clean OSS snapshot under `dist/oss/` (module-path rewrite, prune janus/roche files, leak-scan, build verify) |
| `scripts/denylist-patterns.sh` | Sourceable canonical denylist of internal identifiers (shared by `leak-scan.sh` and `oss-sync.sh` changelog scrub) |
| `scripts/verify-oss.sh` | Full pre-publish gate on a staged tree (leak-scan + build check + sentinel + governance + path checks) |
| `scripts/oss-sync.sh` | End-to-end sync: export → verify → rsync into owner's local clone → commit |

**INTERNAL ONLY — none of the `scripts/` directory ships in the public tree.** `oss-export.sh`
strips `scripts/` entirely from the staged tree to prevent internal module paths and hostnames
from leaking via hygiene script source code. Public-repo secret hygiene relies on
**gitleaks** in CI (`.github/workflows/leak-guard.yml`). Full public CI/pre-commit wiring
is tracked in issue #73.

## Prerequisites

- A local clone of `github.com/Genentech/exohub`:
  ```sh
  git clone git@github.com:Genentech/exohub.git ~/repos/exohub
  ```
- Go toolchain (`go build ./...` must work on the staged tree)
- The internal `exo-cli` repo on a clean `main` branch

## Typical workflow

### 1. Dry-run first (always)

Review what will change before committing:

```sh
scripts/oss-sync.sh --public-dir ~/repos/exohub --dry-run
```

This runs the full export + verify pipeline and prints an itemized rsync diff,
but makes no commits and no changes to the public clone.

### 2. Run the sync (commit only, no push)

```sh
scripts/oss-sync.sh --public-dir ~/repos/exohub
```

The script will:
1. `git fetch origin` on the internal repo
2. Run `scripts/oss-export.sh` → staged tree in a temp dir under `/tmp/`
3. Run `scripts/verify-oss.sh` — fails hard if any check does not pass
4. `rsync -a --delete --exclude='.git/' --exclude='website/'` from the staged tree into the public clone
5. `git add -A` in the public clone and show a summary
6. Prompt for confirmation (bypass with `--yes`)
7. Check that the committer identity is not the internal automation bot
8. Commit: `Sync from internal <sha> (<date>)` with a scrubbed changelog

### 3. Review and push

```sh
git -C ~/repos/exohub log --oneline -3   # review the commit
git -C ~/repos/exohub push               # owner pushes
```

### Syncing a specific ref

`oss-export.sh` always exports HEAD of the working tree. To sync a specific commit
or tag, check it out first:

```sh
git checkout <ref>
scripts/oss-sync.sh --public-dir ~/repos/exohub
git checkout main   # restore
```

### Optional flags

| Flag | Effect |
|------|--------|
| `--dest <dir>` | Use a specific scratch dir instead of `/tmp/exo-oss-sync-<date>` |
| `--yes` | Skip interactive confirmation |
| `--push` | Run `git push` inside the script after committing (direct-to-main) |
| `--pr` | Push a branch and print the PR URL (for repos that protect main) |
| `--base <branch>` | Base branch for `--pr` (default: `main`) |
| `--pr-branch <name>` | Branch name for `--pr` (default: `sync/internal-<sha>`) |

### Makefile shortcuts

```sh
make oss-sync-dry-run PUBLIC_DIR=~/repos/exohub   # dry-run
make oss-sync         PUBLIC_DIR=~/repos/exohub   # commit only
```

## Scope of rsync

The `rsync --delete` removes files in the public clone that no longer exist in the
staged export, **except** for the `website/` subtree which is excluded:

```
rsync -a --delete --exclude='.git/' --exclude='website/' <staged>/ <public-clone>/
```

This means the public repo's `website/` content is never modified by the exo-cli sync.

## Denylist and changelog scrub

`scripts/denylist-patterns.sh` defines `INTERNAL_REF_PATTERNS` — a list of ERE patterns
for internal hostnames, IDs, and names. It is sourced by:

- `scripts/leak-scan.sh` — supplementary denylist grep pass (run on internal working tree)
- `scripts/oss-sync.sh` — scrubs commit subjects from the public changelog (commits
  whose subject matches any pattern are replaced with a count notice)

Neither script ships in the public tree. Public hygiene is enforced by gitleaks CI (#73).

## Identity guard

`oss-sync.sh` checks the committer identity and refuses to run if it detects an
internal automation identity. Public commits must be made by a human maintainer.
Set your identity in the public clone if needed:

```sh
git -C ~/repos/exohub config user.name  "Your Name"
git -C ~/repos/exohub config user.email "you@example.com"
```

## Commit convention

Each sync produces a commit with this subject:

```
Sync from internal <sha> (<date>)
```

The next run recovers `<sha>` from this subject to compute the changelog since the last
sync. Do not rename this subject line or the changelog will start from scratch.
