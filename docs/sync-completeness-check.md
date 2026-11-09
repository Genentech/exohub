# Checking that history isn't missing annexed content (`exo sync --check`)

`exo sync` normally uploads the annexed content of the **current checkout**.
If a repository's history replaces an annexed file commit after commit (for
example, importing several versions of a dataset chronologically and tagging
each one), the content referenced by an **older** tag or commit can end up
on **no remote at all** unless it was synced while that version was checked
out. Nothing about a plain `exo sync` tells you this happened.

`exo sync --check` answers "is the content this ref points at actually safe?"
without running any sync, and without ever shelling out to `git-annex`
directly from your terminal — it's an `exo`-only, automation-friendly surface.

## Usage

```
exo sync --check                  # check every local branch and tag
exo sync --check --ref v1         # check only ref v1 (repeatable)
exo sync --check --ref v1 --ref v2
exo sync --check --json           # stable JSON report, see schema below
exo sync --check --with my-remote # only require content on my-remote
```

## Worked example

```
$ echo "version one" > example.bin
$ git annex add example.bin && git commit -m "v1"
$ git tag v1
$ echo "version two" > example.bin   # replaces the annexed content
$ git annex add example.bin && git commit -m "v2"
$ git tag v2
$ git annex copy --to my-remote example.bin   # only the current (v2) content is copied

$ exo sync --check --ref v1
Checked 1 ref(s) for annexed content missing from configured remotes: v1
  fixable (present here, missing from every configured remote): 1
    example.bin@v1 (key SHA256E-...): missing from my-remote

Fixable: this clone has the content above but no configured remote does; check out that ref and run exo sync to upload it.
$ echo $?
3

$ exo sync --check --ref v2
Checked 1 ref(s) for annexed content missing from configured remotes: v2
  OK: every annexed file referenced by the checked refs is present on a configured remote
$ echo $?
0
```

## Classification

Every annexed file referenced by a checked ref is classified as:

- **ok** — present on at least one configured annex remote (not listed in
  `--json` output; only non-ok items are reported).
- **fixable** — present in this clone, but on no configured remote. The fix is
  to make that version's content reach a remote (check out the ref and run
  `exo sync`).
- **lost** — not in this clone and on no configured remote. If the location
  log shows another, unconfigured repository that might still have a copy,
  it's listed under `seen_in`.

## Exit codes

`exo sync --check` is designed for automation to branch on exit code alone,
without parsing `--json`:

| Exit code | Meaning |
|-----------|---------|
| `0` | Every item in the resolved ref set is `ok`. |
| `3` | At least one `fixable` item, no `lost` item. |
| `4` | At least one `lost` item (takes precedence over `fixable`). |

## `--json` schema

```json
{
  "refs_checked": ["main", "v1", "v2"],
  "items": [
    {
      "path": "example.bin",
      "key": "SHA256E-...",
      "state": "fixable",
      "refs": ["v1"],
      "remotes_missing": ["my-remote"]
    }
  ],
  "summary": {"fixable": 1, "lost": 0}
}
```

Items are grouped by `(path, key)`, not emitted once per `(ref, path)`
occurrence: a file unchanged across many tags produces **one** item with a
`refs` list naming every tag that references it, instead of one
near-identical item per tag. A key's classification never depends on which
ref references it, so this keeps the report (and `summary.fixable` /
`summary.lost`, which count grouped items) sized to the number of distinct
missing files rather than refs × files.

- `items[].state` is `"fixable"` or `"lost"` (`"ok"` items are omitted).
- `items[].seen_in` is present only for `lost` items where the location log
  names a repository that isn't a configured remote.
- `--with <remote>` (repeatable, same flag as a normal `exo sync`) narrows the
  set of "configured remotes" the check requires content on; a remote that
  exists but isn't passed via `--with` does not count toward `ok`.

## Fixing fixable content (`exo sync --all`)

`exo sync --all` is the explicit, full-cost uploader: it pushes the annexed
content of **every local branch and tag** to every writable, plain annex
remote, per ref, respecting each remote's own preferred content expression
(a remote with no preferred content configured receives everything reachable;
a remote with `include=`/`exclude=` receives only what matches). Catalog,
import, export, readonly, and write-restricted remotes are skipped — they use
other sync mechanisms.

```
exo sync --all               # upload history to every writable remote
exo sync --all --dry-run --json   # preview without uploading
```

Continuing the worked example above:

```
$ exo sync --all
Synced 2 ref(s) to every writable remote: main, v1, v2
  my-remote: copied 2 file(s)

$ exo sync --check --ref v1
Checked 1 ref(s) for annexed content missing from configured remotes: v1
  OK: every annexed file referenced by the checked refs is present on a configured remote
```

`--all` can only upload content that exists in this clone. If some content is
`lost` (not here and on no remote), `--all` reports it after uploading
everything it could, with the same `lost` exit code as `--check`.

`--dry-run` previews using git-annex's own matcher (per ref and remote,
equivalent to the real copy's filtering), so the preview always agrees with
what a real `--all` would transfer — including remotes whose `wanted` is the
literal `standard` (resolved from the remote's group, e.g. `archive`,
`backup`, `manual`), not just explicit `include=`/`exclude=` expressions.

## Scope

`exo sync --check` and `exo sync --all` are the first two consumers of one
shared completeness engine (see issue #161 for the full design). Two more are
planned: a time-budgeted warning at the end of a normal `exo sync` (with a
default, incremental content upload across commit ranges), a refusal in
`exo publish` when the published ref references missing content, and a full,
unbudgeted check in `exo doctor`. Those are tracked separately.
