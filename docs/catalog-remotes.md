# Catalog Remotes: Publishing Bundles to an ExoHub Atlas

A **catalog remote** (type `artifactdb`, mode `export`) publishes a repo's
bundle metadata — and optionally an `.artifactdb/` directory — to S3, then
notifies the ExoHub API so the bundle gets indexed into an **Atlas** (search
catalog). `exo sync` and `exo publish` both drive catalog remotes the same
way; `exo publish` just orchestrates `exo sync` as one step of its
sync → bundle → publish → broadcast workflow.

```yaml
# .exohub/remotes
- name: atlas
  type: artifactdb
  mode: export
  s3url: s3://my-bucket/my-prefix
  instance_url: https://artifactdb.example.com/v1/myproject   # optional
  atlas_url: https://atlas.example.com/v1/sandbox1             # optional
```

## Where does the bundle go?

By default, a catalog remote's bundle goes to the ExoHub deployment's
**default Atlas**. Set `atlas_url` on the remote to publish to a specific,
non-default Atlas instead — but only Atlas URLs the deployment has
allowlisted are accepted; everything else is refused.

`exo` always shows, per catalog remote, where the bundle will go before
anything is published — in `exo sync --dry-run`, `exo sync --dry-run --json`,
and again right before the real publish:

```
$ exo sync --with atlas --dry-run
Would publish to catalog remote 'atlas' via git-annex-remote-artifactdb-export
  api-url:      https://api.exohub.example.com
  s3url:        s3://my-bucket/my-prefix
  bundle → https://atlas.example.com/v1/sandbox1 (atlas_url)
  .artifactdb/ → https://artifactdb.example.com/v1/myproject
  grants:       false
```

When no `atlas_url` is configured, exo asks the API for the deployment's
default Atlas and visually flags the line, since publishing to the default
Atlas without an explicit `atlas_url` is the case most likely to surprise
someone expecting a specific destination — it's also the case that caused
the original incident this behavior guards against:

```
  bundle → ⚠ default Atlas https://atlas.example.com/v1/default
```

If the API doesn't expose the default Atlas URL (older deployment), the line
falls back to:

```
  bundle → ⚠ default Atlas (server-chosen)
```

Once a real publish succeeds, exo also prints which Atlas actually received
the bundle (from the publish response), so you don't have to infer it.

## Machine-readable output (stable contracts)

Both JSON shapes below are stable: field names, types, and null-vs-absent
behavior won't change without a version bump. A remote always appears with
all its keys present, using `null` (not an absent key) for an unset nullable
field.

### Dry-run target report: `exo sync --dry-run --json`

Catalog remotes appear in a separate `catalog_remotes` array, alongside the
existing `remotes` array used for standard annex/export/import remotes:

```json
{
  "$schema": "...",
  "remotes": [],
  "catalog_remotes": [
    {
      "remote": "atlas",
      "bundle_target": "https://atlas.example.com/v1/sandbox1",
      "bundle_target_source": "atlas_url",
      "instance_url": "https://artifactdb.example.com/v1/myproject"
    }
  ]
}
```

- `bundle_target`: the resolved URL, or `null` when the source is `"default"`
  and the live default-Atlas lookup failed (older deployment / no network).
  The human-readable `"default (server-chosen)"` text is text-output only —
  it is never serialized as if it were a URL.
- `bundle_target_source`: `"atlas_url"` or `"default"`.
- `instance_url`: the configured `.artifactdb/` instance URL, or `null` when
  unset.

### Post-publish report: `exo sync --json` (without `--dry-run`)

For an explicit, all-catalog `--with` selection, `exo sync --json` (no
`--dry-run`) runs the real publish and prints a single JSON document that
combines the same pre-publish `catalog_remotes` target array as the
dry-run report with a `published` array carrying the outcome per remote,
instead of the human-output log:

```json
{
  "catalog_remotes": [
    {
      "remote": "atlas",
      "bundle_target": "https://atlas.example.com/v1/sandbox1",
      "bundle_target_source": "atlas_url",
      "instance_url": "https://artifactdb.example.com/v1/myproject"
    }
  ],
  "published": [
    {
      "remote": "atlas",
      "atlas_url_used": "https://atlas.example.com/v1/sandbox1",
      "published": true
    }
  ]
}
```

On a refusal, the record carries `published: false` and an `error` object
instead of `atlas_url_used`:

```json
{
  "published": [
    {
      "remote": "atlas",
      "atlas_url_used": null,
      "published": false,
      "error": {
        "code": "atlas_url_not_allowlisted",
        "message": "atlas_url not allowed",
        "allowed": ["https://atlas.example.com/v1/a", "https://atlas.example.com/v1/b"]
      }
    }
  ]
}
```

`error.code` and `error.message` are always present when `error` is present;
`error.allowed` is omitted when the server doesn't provide an allowlist.
`exo sync --json` exits non-zero when any remote's record has
`published: false`, after printing the report — the same no-partial-state
guarantee as the human-output path.

## When the API refuses a publish

The API checks both `atlas_url` and `instance_url` against the
deployment's allowlists **before indexing anything**. If either isn't
allowlisted, it refuses the publish (HTTP 422) with a structured error
code exo maps to a specific fix:

| Code | Cause | Fix |
|------|-------|-----|
| `atlas_url_not_allowlisted` | the remote's `atlas_url` isn't on the deployment's Atlas allowlist | update `atlas_url` in `.exohub/remotes` to an allowed value, then run `exo init` |
| `instance_url_not_allowlisted` | the remote's `instance_url` isn't on the deployment's ArtifactDB instance allowlist | update `instance_url` in `.exohub/remotes` to an allowed value, then run `exo init` |

exo reports either as a clear, non-zero-exit failure with the fix to apply
and the allowlisted values, for example:

```
❌  atlas_url not allowed — fix: update the catalog remote's atlas_url in .exohub/remotes to an allowed value, then run exo init (allowed: https://atlas.example.com/v1/a, https://atlas.example.com/v1/b)
⚠️  bundle uploaded to s3://my-bucket/my-prefix/v1.0.0/ but NOT published: API returned 422: ...
```

The bundle's files are still uploaded to S3 at that point (so a subsequent
successful publish can reuse them), but the publish itself did not happen:
nothing was indexed, and the command exits non-zero. In the post-publish
JSON report (see above), the same refusal appears as
`published: false, error: {code, message, allowed}`.

An unknown/future error code falls back to the server's own message
(no mapped fix text), so older exo versions still report *something*
readable against a newer server.

To fix it:
1. Change `atlas_url` or `instance_url` in `.exohub/remotes` to one of the
   allowlisted values (ask the deployment owner which values are
   allowlisted), or remove `atlas_url` to use the deployment's default
   Atlas instead.
2. Run `exo init` to re-apply the remote configuration.
3. Re-run `exo sync` / `exo publish`.

There is no override flag for this check — it's a deployment-level
allowlist, by design. To bypass it you must change the remote's
configuration.
