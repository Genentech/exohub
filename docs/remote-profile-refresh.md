# Refreshing a Remote From Its Profile

## The Problem

A server profile can change after repos were initialized from it — e.g. a
profile adds a new field to one of its remotes. Repos initialized earlier
keep their old `.exohub/remotes` and git-annex remote config. Re-running
`exo init --profile <name>` on an already-initialized repo either does
nothing for existing remotes or needs `--force`, which rewrites everything
— too blunt for a single changed field.

## Refreshing One Remote

```bash
exo init --profile my-profile --remote catalog
```

This re-renders just the `catalog` remote from `my-profile` and prints a
diff of the changed/added fields, e.g.:

```
==> Remote catalog drift from profile "my-profile":
  + atlas_url: https://atlas.example.com/...
```

Unless `--yes` is also passed, you're asked to confirm. On confirmation, only
the changed/added fields are written to `.exohub/remotes` and applied to the
git-annex remote config (`git annex enableremote`) — every other remote,
file, and field is left untouched. Comments, key order, and any key the
profile doesn't know about are preserved; nothing the profile doesn't
define is ever cleared.

```bash
exo init --profile my-profile --remote catalog --yes
```

### Where the answers come from

Re-rendering a remote may need the same prompt answers the repo was
originally initialized with (e.g. a templated path). In order:

1. `--set key=value` (repeatable), highest priority.
2. `.exohub/profile.lock`, written automatically by `exo init --profile`
   runs from now on. It stores the profile name and the prompt answers in
   clear text (no secret prompt type exists today) — don't put secrets in
   profile prompts.
3. Reverse-matched from the remote's current fields in `.exohub/remotes`,
   when a prompt's key happens to match a remote field name (e.g. `s3url`).

If a prompt can't be resolved any of these ways, the command fails and
names exactly which keys are missing — it never guesses silently. Pass them
with `--set`.

## Reporting Drift for Automation

```bash
exo init --profile my-profile --dry-run --json
```

Reports drift between every remote in `.exohub/remotes` and what the
profile would render, without changing anything, as a JSON array of
entries:

```json
[
  {
    "remote": "catalog",
    "field": "atlas_url",
    "profile": "https://atlas.example.com/...",
    "status": "added"
  }
]
```

`status` is one of:

- `added` — the profile defines the field, the repo doesn't have it yet.
- `changed` — both define the field, with different values (`current` and
  `profile` are both set).
- `local-only` — the repo has a value for a field the profile doesn't
  define. Reported for visibility only; never proposed for removal.
- `unresolved` — the remote's prompts couldn't be re-rendered (see above);
  `profile` holds the error naming the missing answers.

Combine with `--remote <name>` to scope the report to a single remote.

The command exits `3` if any `added`/`changed`/`unresolved` entry is found,
and `0` if there's none — distinct from the generic exit `1` used for
command errors, so automation (e.g. obol) can tell "stale repo" apart from
"exo itself failed".
