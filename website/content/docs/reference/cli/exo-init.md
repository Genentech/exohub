---
title: "exo init"
description: "Initialize repository and remotes"
---

Full `exo init --help` output, generated from exo-cli v0.140.2.

```text
Initialize repository and remotes.

The 'exo init' command orchestrates repository creation and remote validation:
  - With --profile <name>: fetch a server-authored profile, resolve vars, prompt, render files, run actions
  - With --create-repo <url>: creates repo via provider API using the URL
  - With --create-repo + --context: creates repo using context defaults
  - Without --create-repo (existing git repo): validates remotes from .exohub/remotes

Profile mode (--profile):
  Fetches a named profile from GET /api/profiles/{name} and performs all
  variable resolution, prompting, Go-template rendering, file writing, and
  actions client-side, then proceeds with the normal init flow.

  Template variables (all top-level, no namespace prefix):
    .UnixID    - JWT preferred_username or OS username on first --profile
                 run; .exohub/profile.lock's recorded UnixID on any later
                 run, so a shared prefix never changes depending on who runs
                 the refresh/drift check
    .Folder    - the repo's identity, resolved in order: .exohub/profile.lock,
                 then the dataset name already baked into an existing
                 remote's rendered fields (e.g. its s3url, when present and
                 unambiguous), then the git origin remote's repo name, then
                 the current directory base name with a warning (override
                 with --folder or --set Folder=<name>)
    .<var>     - each key from the profile vars block
    .<NAME>    - value of EXOHUB_TPL_<NAME> environment variable
    .<answer>  - each answer from the prompts block
    (prompt answers override vars; env vars override vars; on key collision, last write wins)

  Refreshing one remote (--profile <name> --remote <remote-name>):
    On an already-initialized repo, re-renders just the named remote from
    the profile, prints a diff of the changed/added fields (e.g.
    '+ atlas_url: ...'), and on confirmation (or --yes) updates only that
    remote in .exohub/remotes and its git-annex remote config. Every other
    remote, file, and field is left untouched — no --force needed, and
    nothing is cleared that the profile doesn't define. Answers needed to
    re-render come from .exohub/profile.lock (written by a prior --profile
    run), reverse-matched from the remote's current fields, or --set.

  s3url changes are never silent (--profile, with or without --remote):
    A rendered s3url that differs from a remote's current one is a location
    move (exo-cli#166): it is always shown (old -> new), but never applied
    by --yes alone — pass --allow-move to confirm it's intentional. This
    applies both to --remote refresh and to a full --profile --force
    re-apply on an already-initialized repo.

  Migrating content on a move (--allow-move --migrate-content, exo-cli#168):
    By default, --allow-move only invalidates stale presence so 'exo sync
    --all' re-uploads whatever is missing from the new location — content
    is never copied automatically. Pass --migrate-content alongside
    --allow-move to copy content from the old s3url prefix to the new one
    *before* switching: credentials for each side are obtained from
    exo-credential-helper (the exohub profile, the identity-provider
    fallback, or S3 Access Grants), the same way git-annex-remote-s5cmd does
    — never the default AWS chain. A server-side S3 copy is used when a
    single credential set can read the old location and write the new one
    (same bucket and identity); otherwise objects are streamed through a
    bounded temp file.
    Every planned object is verified present at the destination (size, and
    checksum when comparable) before the switch is applied: if anything is
    missing, the command fails and the remote's s3url is left unchanged.
    Objects already at the destination are skipped, so a failed or
    interrupted migration can be resumed by re-running the same command;
    concurrency is bounded by --jobs. --dry-run (optionally with --json)
    prints the plan — object count, bytes, and mode (server-side|stream) —
    without copying or switching anything. The old prefix's content is
    never deleted — cleanup stays a separate, explicit step.

  Drift reporting (--profile <name> --dry-run --json):
    Reports per-field drift between the repo's .exohub/remotes and what the
    profile would render, as a JSON array of {remote, field, current,
    profile, status, hint?} entries, without writing anything. Fields only
    the repo defines (not the profile) are reported as status "local-only"
    and are never proposed for removal. A changed s3url carries a hint
    pointing at --allow-move. Exits 3 if drift is found, 0 if none —
    distinct from the generic exit 1 used for command errors, so automation
    can tell "stale repo" apart from "exo itself failed".

Repository creation:
  - Auto-detects provider (Gitea, GitHub, GitLab) from host via HTTP inspection
  - Repo name precedence: --name > URL-derived > context repo > cwd folder name
  - Creates repository via provider API if it doesn't exist
  - Prints summary and asks for confirmation (use --yes to auto-accept)
  - Initializes git repository and sets origin remote
  - Configures git-annex remotes from .exohub/remotes if present
  - Reads shared settings from .exohub/config (e.g., thin)

Context is resolved from: --context flag > EXOHUB_CONTEXT env var

Examples:
  exo init --profile sandbox
  exo init --profile sandbox --yes --dry-run
  exo init --profile sandbox --force  # overwrite existing .exohub/
  exo init --profile sandbox --yes --set read_access=authenticated  # answer one prompt, default the rest
  EXOHUB_PROFILE=sandbox exo init  # equivalent to --profile sandbox (ignored in existing repos)
  exo init --create-repo https://github.com/org/mynewrepo
  exo --context myctx init --create-repo --name mynewrepo
  EXOHUB_CONTEXT=default exo init --create-repo --name mynewrepo
  exo init  # validate remotes in existing git repo
  exo init --remote s5-annex  # only configure/validate the s5-annex remote
  exo init --profile my-profile --remote catalog --yes  # refresh one remote from its profile (diff, no --force)
  exo init --profile my-profile --remote catalog --yes --allow-move  # same, confirming an s3url location move (exo-cli#166)
  exo init --profile my-profile --dry-run --json  # report remote/profile drift as JSON; exits 3 if drift is found

Authentication:
  Set token via environment variable:
    EXOHUB_GITEA_TOKEN  - For Gitea providers
    EXOHUB_GITHUB_TOKEN - For GitHub providers
    EXOHUB_GITLAB_TOKEN - For GitLab providers
    EXOHUB_GIT_TOKEN    - Generic fallback for any provider

UUID Cleanup (for handling duplicate remotes):
  --dead <uuid>    Mark a remote UUID as dead and remove local config (SAFE, recommended).
                   Data on S3/rsync remains safe. Can be undone by re-running exo init.
  --destroy <uuid> Completely remove UUID from git-annex history (DESTRUCTIVE).
                   WARNING: Rewrites git-annex branch, cannot be easily undone.
                   Creates automatic backup branch for recovery.

UUID Cleanup Examples:
  # Safe cleanup of duplicate UUID (recommended)
  exo init --dead 018c3a7d-fff2-4128-8dbb-73596ecdc680

  # Preview what would be removed
  exo init --dead 018c3a7d-fff2-4128-8dbb-73596ecdc680 --dry-run

  # Non-interactive cleanup
  exo init --dead 018c3a7d-fff2-4128-8dbb-73596ecdc680 --yes

  # Complete destruction (use with caution)
  exo init --destroy 018c3a7d-fff2-4128-8dbb-73596ecdc680

  # Non-interactive destruction (requires confirmation)
  exo init --destroy <uuid> --yes --confirm <uuid>

The 'exo init remote' subcommand creates git-annex remotes:
  - With --type and --name: creates remote declaratively
  - Without args: launches interactive TUI

Supported remote types: annex, export, import, exospace, drive

Usage:
  exo init [url] [flags]
  exo init [command]

Available Commands:
  remote      Create and configure git-annex remotes

Flags:
      --allow-move          Confirm an s3url change (--profile, with or without --remote) is an intentional location move (exo-cli#166); --yes alone does not approve it
      --confirm string      Confirmation UUID for --destroy (required in non-interactive mode)
      --create-repo         Create repository on the git host
      --dead string         Mark a remote UUID as dead and remove its configuration
      --destroy string      Completely destroy a UUID from git-annex history (DESTRUCTIVE)
      --dry-run             Show what would be done without actually doing it
      --folder string       Override the resolved .Folder template variable instead of inferring it from the repo's identity
      --force               Overwrite existing .exohub/ when applying a profile
      --force-grants-sync   Attempt grants provisioning even when not listed as an owner (service/admin accounts only)
  -h, --help                help for init
      --jobs int            Bounded concurrency for --migrate-content's object copies (default 8)
      --json                With --profile --dry-run, report remote/profile drift as JSON instead of applying a profile
      --migrate-content     With --allow-move, copy existing content from the old s3url prefix to the new one via server-side S3 copy before switching (exo-cli#168); requires --allow-move
      --name string         Use explicit repository name
      --profile string      Fetch and apply a server-authored profile by name
      --profile-select      Interactively select a profile to apply
      --remote string       Only configure or validate the named remote (can be used with existing git repos); combined with --profile, refreshes that remote from the profile (diff, no --force)
      --reset               Remove .exohub/ and .git/ directories before initializing
      --set stringArray     Answer a profile prompt non-interactively (key=value, repeatable); unknown keys are an error
      --yes                 Accept default choices automatically

Global Flags:
      --config-dir string   Root directory for all config, cache and credentials (overrides EXO_CONFIG_DIR)
      --context string      Use specific context (overrides selected context)

Use "exo init [command] --help" for more information about a command.

Docs for agents (llms.txt): https://genentech.github.io/exohub/llms.txt

Full manual: https://genentech.github.io/exohub/docs/reference/cli/exo-init/
```
