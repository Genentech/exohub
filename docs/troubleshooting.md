# Troubleshooting ExoHub Client Issues with `exo doctor`

When something goes wrong — uploads failing with `AccessDenied`, `exo get` stalling,
or mysterious credential errors — `exo doctor` provides a single command that
inspects every layer of the ExoHub client stack and reports actionable results.

## Quick Start

```bash
exo doctor
```

Run this first whenever you hit an error. Each check reports **PASS**, **WARN**,
**FAIL**, or **SKIP** with a short summary. FAILs always include a remediation hint.

Exit code is non-zero if any check is **FAIL** (WARN and SKIP do not affect exit).

## Check Groups

### 1. Environment & Tooling
Verifies that `exo`, `exo-credential-helper`, `git-annex`, and `s5cmd` are installed
and on PATH, that `EXOHUB_API_URL` includes `/api`, and that your system clock is
within tolerance (clock skew breaks SigV4 signing and JWT validation).

**Common fixes:**
- `EXOHUB_API_URL` missing `/api` → set to `https://<host>/api`
- `exo-credential-helper` not found → install it or set `EXO_CREDENTIAL_HELPER_BIN`
- Clock skew warning → `timedatectl set-ntp true` (Linux) or sync system time

### 2. Identity & Auth
Checks that your `exo login` session is valid, attempts a silent token refresh if
expired, and decodes JWT claims (username, groups, expiry) without printing secrets.
Also verifies the `exohub` AWS profile resolves and `sts:GetCallerIdentity` returns
the expected principal.

**Common fixes:**
- Token expired → `exo login`
- AWS profile missing → `exo login` (rewrites `~/.aws/credentials [exohub]`)
- Identity mismatch → `exo login` to sync token and AWS credentials

### 3. Network Reachability
Probes each network plane independently with a short timeout:
- **exohub API** (VPN required for server fallback)
- **AWS S3 + S3 Control** (required for fast-path `GetDataAccess`)
- **OIDC provider** (required for token refresh)

The result distinguishes "no VPN" (API blocked, AWS reachable) from "no AWS egress"
(e.g., restricted HPC node where the fast path cannot run).

**Common fixes:**
- API blocked → connect to VPN
- AWS blocked → check firewall/proxy settings; use a node with egress

### 4. Repository Config  *(skipped outside a repo)*
Checks `.exohub/remotes` parses cleanly and UUIDs match `git annex info`, that
git-annex is initialized, and whether you are listed as owner/viewer in
`.exohub/permissions`. Also probes for registry drift (local vs S3 `_grants.json`).

**Common fixes:**
- UUID mismatch → `exo init` to re-enable remotes
- Not in permissions → ask a dataset owner to re-run `exo init` to add you
- Registry drift → `exo init` to sync local permissions with S3

### 5. Grants & S3 Access  *(per grants-enabled remote)*
For each remote with `grants: true`, runs:
1. `GetDataAccess READ` and `GetDataAccess READWRITE` (client-side, no VPN needed)
2. Server fallback (`POST /api/grants/credentials`) and reports **which principal is
   vended**: a *location role* (write-capable) vs `role/<provider>/<user>` (read-only)

The vended principal is the single most diagnostic signal for read-vs-write problems.

**Common fixes:**
- No matching grant → `exo init` to provision, or ask an owner to grant access
- Read works, write doesn't (read-only role vended) → owner must re-run `exo init` to
  provision a write grant, or your grant scope doesn't cover `_annex`

## Flags

| Flag | Description |
|------|-------------|
| `--json` | Emit results as a JSON array (machine-readable) |
| `--verbose` | Show detail/remediation for PASS checks too |
| `--remote <name>` | Only run per-remote checks for this remote |
| `--repo <path>` | Point at an ExoHub repo (default: current directory) |
| `--write-test` | Opt-in: perform a zero-byte Put+Delete canary write under a caller-owned prefix |

## The Write Test

`--write-test` verifies the full write path end-to-end. It:
1. Checks you are listed as an owner (refuses if not, to prevent data corruption)
2. Gets READWRITE credentials via `GetDataAccess`
3. Puts a zero-byte object at `<remote_prefix>/.exo-doctor-canary-<user>-<ts>`
4. Deletes it immediately (leaves no artifacts)

Use this when read works but write fails and you need ground truth.

## Using in CI/Scripts

```bash
# Fail fast if environment is not configured correctly
exo doctor --json | jq '.[] | select(.status == "FAIL") | .summary'
exo doctor || exit 1
```

## Common Scenarios

### "AccessDenied" on `exo push`
```bash
exo doctor --write-test
```
Look at the **Grants & S3 Access** group. If `GetDataAccess(READWRITE)` passes but
the write canary fails, the vended credentials don't have write permission — ask a
dataset owner to re-provision grants.

### "You are not an owner"
```bash
exo doctor
```
Check the **Repository Config** group. If you're not in `owners`, ask an owner to add
you to `.exohub/permissions` and re-run `exo init`.

### Stale token / "run exo login"
```bash
exo login && exo doctor
```

### Missing groups claim
If the **Identity & Auth** group shows `groups=0`, your JWT lacks the ExoHub groups
claim. Contact your admin to verify your identity provider group membership.

### On an sHPC/restricted node
`exo doctor` will show AWS S3/S3 Control as blocked if the node has no egress.
In this case, the server fallback (VPN) is the only credential path available.
The **Network Reachability** group makes this explicit.
