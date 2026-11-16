# Help modes

By default, `exo <command> --help` prints the full cobra help: description,
usage, examples, and every flag with its full usage text. This is meant for
humans.

Agents (e.g. MCP tools that build their descriptions from `exo ... --help`)
only need a short description and a link to the full manual. Set `EXO_HELP=short`
to switch any command (and `exo help <command>`) to a compact rendering:

```bash
EXO_HELP=short exo sync --help
```

```
Sync git-annex content and record bad files

Usage:
  exo sync [flags]

Flags:
      --all
      --bundle-dir string
      --check
      --dry-run
      --exclude stringArray
  -h, --help
      --import-dir string
      --include stringArray
  -J, --jobs string
      --json
      --log-prefix string
      --manifest string
      --no-content
      --path stringArray
      --ref stringArray
      --verify
      --with stringArray

Global Flags:
      --config-dir string
      --context string

Full manual: https://genentech.github.io/exohub/docs/reference/cli/exo-sync/
```

Short mode keeps the same `Flags:` / `Global Flags:` section layout as full
help so tooling that scrapes `--help` output (e.g. EDS) keeps working, but
drops each flag's usage description — only the name, shorthand, and type
remain. The full manual is one click away, so there is no point duplicating
possibly-long prose here; this is what shrinks `exo sync --help` from 99
lines to 29.

| Setting | Environment variable | Default |
|---|---|---|
| Help mode (`full` or `short`) | `EXO_HELP` | `full` |
| Agent docs / man page site base | `EXOHUB_DOCS_URL` | `https://genentech.github.io/exohub/llms.txt` |

The man page URL for a command is computed from its path, not looked up:
`exo atlas serve` maps to `<base>/docs/reference/cli/exo-atlas-serve/`, where
`<base>` is `EXOHUB_DOCS_URL` with the trailing `llms.txt` dropped (so the
same knob that points agents at the llms.txt index also controls the man-page
base — no second environment variable). Full help also prints this per-command
URL as a trailing line, so humans can find the generated manual page too.

Two exceptions, matching what the man-page generator (website#276) actually
produces:

- **Root and `exo help`** have no page of their own, so their `Full manual:`
  line links the generated index, `<base>/docs/reference/cli/`, instead of a
  (nonexistent) `exo/` or `exo-help/` page.
- **Hidden commands** (`cmd.Hidden`, or any hidden ancestor — e.g. `exo
  serve`) print no `Full manual:` line at all, in either help mode, since
  the generator only produces pages for visible commands.

`exo --help` (root only) documents `EXO_HELP` under an `Environment:` section;
subcommand help stays unchanged except for the trailing man-page line, to
keep EDS's `--help` scraping stable.
