# Routing files to Git or git-annex

`exo add` auto mode reads shared, versioned rules from `.exohub/config`.
Rules apply to **new (untracked) files only**. Modified tracked files keep their
current backend, even after you change the rules. Ignored paths are excluded;
explicitly adding an ignored path reports an error.

## Evaluation order

1. Protected paths (`.exohub/**`, `.gitignore`, `.gitattributes`) go to Git.
   User rules cannot override them.
2. `annex.routing` rules are evaluated top-down: **the first match wins**.
   This is the opposite of `.gitignore` / `.gitattributes` (last match wins).
3. Unmatched binary files go to annex. Unmatched text files go to Git unless
   their size is at least the fallback threshold (default **5 MiB**). Large text
   prompts on a TTY; without a TTY it goes to annex. Explicit rules never prompt.

Exo's auto-mode decisions take precedence over `annex.largefiles`. These rules
are not translated into `.gitattributes`; plain `git annex add` does not use them.
Explicit `--git` and `--annex` modes bypass auto routing. URLs use
`git annex addurl`, not local-file routing.

## Worked example

New `exo init` configurations contain a **commented-out** example: no policy is
activated automatically. Uncomment the example to opt in. If your config
already has `annex:`, add `routing:` beneath that key, rather than creating a
second `annex:` mapping. Preserve settings such as `thin` and `addunlocked`.

```yaml
annex:
  routing:
    - path: "**/*.md"
      to: git
    - path: "**/*.csv"
      larger_than: 1KB
      to: annex
    - path: "**/*.json"
      larger_than: 1MB
      to: annex
```

This implements “CSV > 1KB → annex”, “JSON > 1MB → annex”, and “*.md → git”,
with an important boundary detail: `larger_than` means **greater than or equal**.
A CSV of exactly 1,000 bytes also matches; for strictly greater than 1KB, use
`larger_than: 1001`. Similarly, strictly greater than 1MB uses `1000001`.

Assuming new files, text content, and the default fallback threshold:

| File | Size (bytes) | Destination | Reason |
| --- | ---: | --- | --- |
| `notes/README.md` | 6,000,000 | Git | rule 1, regardless of size |
| `data/table.csv` | 2,000 | annex | rule 2 |
| `data/boundary.csv` | 1,000 | annex | rule 2, inclusive boundary |
| `data/small.csv` | 999 | Git | fallback: small text |
| `data/records.json` | 1,200,000 | annex | rule 3 |
| `data/small.json` | 500,000 | Git | fallback: small text |
| `.exohub/config` | 1,500 | Git | protected path |

A binary `data/raw.bin` goes to annex via the fallback. A CSV already tracked in
Git stays in Git even when rule 2 would match; use explicit rerouting to move it.

## Rule keys

All match keys in a rule must match (**AND**). Every rule needs `to` and at least
one match key.

| Key | Meaning |
| --- | --- |
| `path` | Repository-relative glob using `/` separators; `**` spans directories. Without `/`, a pattern such as `*.md` matches basenames at any depth. |
| `larger_than` | Size **>=** the specified value. |
| `smaller_than` | Size **<** the specified value. |
| `content` | `text` or `binary`, detected from the first 512 bytes. Text means `text/*`, not a file extension. |
| `to` | Required destination: `git` or `annex`. |

Sizes accept bytes, decimal `KB` / `MB` (1,000 / 1,000,000 bytes), and binary
`KiB` / `MiB` (1,024 / 1,048,576 bytes). Quote globs in YAML. For a size range,
`larger_than` must be less than `smaller_than`.

Unknown keys, missing/invalid destinations, invalid content values, invalid
globs, invalid sizes, and rules without match keys cause an error before staging.
`exo doctor` validates routing and warns about completely shadowed rules.

## Override only the fallback threshold

Precedence is **flag > environment > built-in 5 MiB**:

```sh
exo add --large-text-threshold 1MiB data/
EXOHUB_LARGE_TEXT_THRESHOLD=1MiB exo add data/
```

Invalid overrides are errors. Neither override changes explicit rules; for
example, the Markdown rule above keeps even a large Markdown file in Git.

## Inspect and deliberately reroute

```sh
exo add --explain .                 # decisions and reasons; no staging
exo add --explain --json .          # machine-readable output
exo doctor                         # validate rules and detect shadowing
exo add --reroute data/             # preview tracked-file backend changes
exo add --reroute --yes data/       # execute the previewed policy
```

`--json` requires `--explain`; `--yes` requires `--reroute`. Rerouting cannot be
combined with `--explain`, `--git`, `--annex`, or `--force`. Protected paths are
never moved by rerouting. Review previews before applying.

Locked annex files are evaluated using their content size, not symlink length.
When content is dropped, recorded annex key sizes still allow path/size-only
rules to be evaluated. Rules requiring content (including the default fallback)
are reported as skipped with `exo get` guidance; preview never downloads content.
Applying a known annex-to-Git move requires local content for every such move,
checked before any staging or migration. Skipped files are reported in both
preview and apply output.

For manual migration independent of the routing rules, use
`exo add --annex --force <paths>` or `exo add --git --force <paths>`.
