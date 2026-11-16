# Environment variables

This page lists environment variables that apply across `exo`,
`exo-credential-helper`, and `git-annex-remote-artifactdb-export`, rather
than being scoped to a single command or feature. Variables specific to a
feature are documented alongside it (e.g. `EXO_HELP` in
[`docs/help-modes.md`](help-modes.md), the OIDC/STS login variables in
[`docs/authentication.md`](authentication.md)).

| Variable | Default | Description |
|---|---|---|
| `EXO_USER_AGENT` | `exo/<version> (<os>/<arch>)` | Overrides the `User-Agent` header sent on every HTTP request. |

## `EXO_USER_AGENT`

`exo`, `exo-credential-helper`, and `git-annex-remote-artifactdb-export` send
`User-Agent: exo/<version> (<os>/<arch>)` (e.g. `exo/0.141.0 (linux/amd64)`)
on every HTTP request they make directly — including `exo doctor`'s network
checks — so the services exo calls (the ExoHub API, GitLab) can tell exo's
requests apart from other Go clients and identify which version and
deployment made them. Set `EXO_USER_AGENT` to replace this value with a
custom string.

**Scope and exceptions:**

- Requests made via the AWS SDK for Go v2 (S3, S3 Access Grants, STS, …) use
  the SDK's own HTTP transport, which does not go through exo's
  `User-Agent` header. Those clients instead carry an `app/exo-<version>`
  token in the SDK's own User-Agent string, via
  [`config.WithAppID`](https://pkg.go.dev/github.com/aws/aws-sdk-go-v2/config#WithAppID).
  `EXO_USER_AGENT` does not affect this token — it is a short, SDK-legal
  identifier, not a free-form header value.
- Subprocesses exo shells out to (`git`, `git-annex`, `s5cmd`, …) send their
  own User-Agent, if any; this is out of scope.
