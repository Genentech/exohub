---
title: "Troubleshooting"
description: "Diagnosing and fixing issues"
weight: 12
---

This section covers common issues and how to resolve them using `exo doctor`, `exo fsck`, and other diagnostic tools.

## exo doctor

`exo doctor` is the first tool to reach for when something goes wrong. It runs grouped, non-destructive diagnostics across every layer of the ExoHub client stack — tooling, identity, network, repository config, and S3 access — and reports actionable results with remediation hints.

### Quick Start

```bash
exo doctor
```

Each check reports **PASS**, **WARN**, **FAIL**, or **SKIP** with a short summary. FAILs always include a remediation hint. The exit code is non-zero if any check is **FAIL** (WARN and SKIP do not affect exit).

Example output:

```
Environment & Tooling
  ✓  [PASS] exo version: v0.109.1
  ✓  [PASS] exo-credential-helper: v0.109.1 (/usr/local/bin/exo-credential-helper)
  ✓  [PASS] git-annex: 10.20231129-1 (upstream)
  ✓  [PASS] s5cmd: s5cmd version 2.2.2
  ✓  [PASS] EXOHUB_API_URL not set (using built-in default)
  ✓  [PASS] EXOHUB_AWS_PROFILE=exohub (default)
  ✓  [PASS] Clock skew: 342ms (within threshold)

Identity & Auth
  ✓  [PASS] Token valid (expires in 23h)
  ✓  [PASS] user=jdoe iss=<redacted> groups=5 exp=2026-08-20T12:00:00Z
  ✓  [PASS] Member of mandatory group "EXOHUB_USERS"
  ✓  [PASS] AWS identity OK (account=123456789012, principal=...ExoHubRole/jdoe)

Network Reachability
  ✓  [PASS] exohub API reachable (HTTP 200)
  ✓  [PASS] AWS S3 (us-west-2) reachable (HTTP 403)
  ✓  [PASS] AWS S3 Control (us-west-2) reachable (HTTP 403)
  ✓  [PASS] OIDC issuer reachable (HTTP 200, source=env)

Repository Config
  ✓  [PASS] .exohub/remotes parses cleanly (1 remote)
  ✓  [PASS] git-annex initialized
  ✓  [PASS] you are listed as owner

Grants & S3 Access
  ✓  [PASS] Remote "s3-archive" s3url scope: s3://exohub-bucket/datasets/mydata
  ✓  [PASS] GetDataAccess(READ) OK — role=oidc
  ✓  [PASS] GetDataAccess(READWRITE) OK — role=location
  ✓  [PASS] Server fallback READWRITE OK — vended principal: location role (write-capable)
```

### Check Groups

**1. Environment & Tooling** — Verifies that `exo`, `exo-credential-helper`, `git-annex`, and `s5cmd` are installed and on PATH, that `EXOHUB_API_URL` includes `/api`, and that your system clock is within tolerance (clock skew breaks SigV4 signing and JWT validation).

Common fixes:
- `EXOHUB_API_URL` missing `/api` → set to `https://<host>/api`
- `exo-credential-helper` not found → install it or set `EXO_CREDENTIAL_HELPER_BIN`
- Clock skew warning → `timedatectl set-ntp true` (Linux) or sync system time

**2. Identity & Auth** — Checks that your `exo login` session is valid, attempts a silent token refresh if expired, and decodes JWT claims (username, groups, expiry) without printing secrets. Verifies that your token includes the mandatory `EXOHUB_USERS` group (you will get a `FAIL` with a subscription link if it is missing). Also verifies the `exohub` AWS profile resolves and `sts:GetCallerIdentity` returns the expected principal.

Common fixes:
- Token expired → `exo login`
- AWS profile missing → `exo login` (rewrites `~/.aws/credentials [exohub]`)
- Identity mismatch → `exo login` to sync token and AWS credentials

**3. Network Reachability** — Probes four network planes independently with a short timeout: ExoHub API, AWS S3, AWS S3 Control (required for S3 Access Grants), and the OIDC issuer. The result distinguishes "no VPN" (API blocked, AWS reachable) from "no AWS egress" (e.g., restricted HPC node). On open-source builds, the S3 Control probe is automatically **SKIP**ped when `EXOHUB_GRANTS_ACCOUNT_ID` is not set — set it to enable this check.

Common fixes:
- API blocked → connect to VPN
- AWS blocked → check firewall/proxy settings

**4. Repository Config** *(skipped outside a repo)* — Checks `.exohub/remotes` parses cleanly and UUIDs match `git annex info`, that git-annex is initialized, and whether you are listed as owner/viewer in `.exohub/permissions`. Also probes for registry drift (local vs S3 `_grants.json`).

Common fixes:
- UUID not found in git-annex (**FAIL**) → run `exo sync` on the machine where the dataset was originally set up, then re-clone or re-run `exo sync` here; for a brand-new dataset not yet synced anywhere, you may instead remove the `uuid:` line for the affected remote in `.exohub/remotes` and re-run `exo init`
- UUID registered under a different name (**WARN**) → fix the remote name in `.exohub/remotes`, or run `exo sync` on the source repo
- Not in permissions → ask a dataset owner to add you to `.exohub/permissions` and re-run `exo init`

**5. Grants & S3 Access** *(per grants-enabled remote)* — For each remote with `grants: true`, runs `GetDataAccess READ` and `GetDataAccess READWRITE`, then reports which principal is vended: a *location role* (write-capable) vs the identity role (read-only). This is the most diagnostic signal for read-vs-write problems.

Common fixes:
- No matching grant → `exo init` to provision, or ask an owner to grant access
- Read works, write doesn't (read-only identity role vended) → owner must re-run `exo init` to provision a write grant

### Flags

| Flag | Description |
|------|-------------|
| `--json` | Emit results as a JSON array (machine-readable) |
| `--verbose` | Show detail/remediation for PASS checks too |
| `--remote <name>` | Only run per-remote checks for this remote |
| `--repo <path>` | Point at an ExoHub repo (default: current directory) |
| `--write-test` | Opt-in: perform a zero-byte Put+Delete canary write to verify the full write path |

### The Write Test

The `--write-test` flag verifies the full write path end-to-end:

```bash
exo doctor --write-test
```

This checks that you are listed as an owner, gets READWRITE credentials, puts a zero-byte object at `<remote_prefix>/.exo-doctor-canary-<user>-<ts>`, then deletes it immediately (no artifacts left). Use this when reads succeed but writes fail.

### Common Scenarios

**"AccessDenied" on `exo push`:**
```bash
exo doctor --write-test
```
Check the **Grants & S3 Access** group. If `GetDataAccess(READWRITE)` passes but the write canary fails, the vended credentials don't have write permission — ask a dataset owner to re-provision grants.

**"You are not an owner":**
```bash
exo doctor
```
Check the **Repository Config** group. If you are not listed in `owners`, ask a dataset owner to add you to `.exohub/permissions` and re-run `exo init`.

**Stale token / "run exo login":**
```bash
exo login && exo doctor
```

**Missing groups claim:**
If the **Identity & Auth** group shows `groups=0`, your JWT is missing the ExoHub groups claim. Contact your administrator to verify your group membership.

**Not a member of `EXOHUB_USERS`:**
If `exo doctor` shows a `FAIL` for the required-group check, your token does not carry the mandatory `EXOHUB_USERS` group:
```
✗  [FAIL] Not a member of mandatory group "EXOHUB_USERS"
   Subscribe to the EXOHUB_USERS CIDM group, then run 'exo login' to refresh your token
```
Subscribe to the `EXOHUB_USERS` CIDM group, then run `exo login` to obtain a fresh token with the updated group claims.

**On an sHPC/restricted node:**
`exo doctor` will show AWS S3/S3 Control as blocked if the node has no egress. In this case, the server fallback (VPN) is the only credential path. The **Network Reachability** group makes this explicit.

**Using in CI/scripts:**
```bash
# Fail fast if the environment is not configured correctly
exo doctor || exit 1

# Machine-readable output
exo doctor --json | jq '.[] | select(.status == "FAIL") | .summary'
```

## Common Issues

### Broken Symlinks

**Symptom:** Files appear as broken symlinks after cloning.

```bash
ls -la data/
# train.parquet -> ../.git/annex/objects/... (red/broken)
```

**Solution:** Sync the data from a remote:

```bash
exo sync
```

If sync doesn't help, check if the file exists on any remote:

```bash
git annex whereis data/train.parquet
```

### "Unable to Access Remote"

**Symptom:** Sync fails with authentication or connection errors.

**Solutions:**

1. Check authentication:
   ```bash
   exo login --force
   ```

2. Verify remote configuration:
   ```bash
   exo info
   ```

3. Test S3 access directly:
   ```bash
   aws s3 ls s3://your-bucket/path/
   ```

### "No Content Available"

**Symptom:** A file exists in the repository but can't be synced.

```
git-annex: get data/file.parquet failed
  Unable to access these remotes: s3-archive
  No other known copies exist.
```

**Solution:** Check which remotes have the file:

```bash
git annex whereis data/file.parquet
```

If no remotes have the file, it may have been lost. Check backup remotes or contact the dataset maintainer.

### Corrupted Files

**Symptom:** Files exist but have incorrect content (wrong checksum).

**Solution:** Use fsck to detect and mark bad files:

```bash
exo fsck --deep data/file.parquet
```

## The fsck Command

`exo fsck` (file system check) helps diagnose and repair issues with annexed files.

### Scan for Bad Files

Scan sync logs for files that failed:

```bash
exo fsck --scan-bad
```

This reads git-annex logs and records problematic files in `.git/exohub/bad`.

To just print bad files without recording:

```bash
exo fsck --scan-bad --print
```

### View Bad Files

See which files are marked as bad:

```bash
exo fsck --show-bad
```

### Verify Specific Files

Run a deep verification on specific paths:

```bash
exo fsck --deep data/
```

This runs `git annex fsck` which verifies checksums.

Verify against a specific remote:

```bash
exo fsck --deep data/ --remote s3-archive
```

### Parallel Verification

Speed up fsck on large datasets by running multiple parallel jobs:

```bash
exo fsck --deep data/ -J 4
```

Or set it globally via environment variable:

```bash
export EXOHUB_JOBS=4
exo fsck --deep data/
```

| Flag | Description | Default |
|------|-------------|----------|
| `-J, --jobs <N>` | Parallel jobs for `git annex fsck` | `$EXOHUB_JOBS` or `1` |

### Verify Multiple Paths

```bash
exo fsck --path data/train.parquet --path data/test.parquet
```

Or use a manifest:

```yaml
# fsck-manifest.yaml
paths:
  - data/batch-1/
  - data/batch-2/
remote: s3-archive
```

```bash
exo fsck --manifest fsck-manifest.yaml
```

### Mark Bad Files

Mark files as bad in git-annex (prevents sync from trying them):

```bash
exo fsck --mark-bad data/corrupted-file.parquet
```

With confirmation prompt disabled:

```bash
exo fsck --mark-bad data/corrupted-file.parquet --no-confirm
```

### Drop Bad Files from Remote

Remove corrupted copies from a remote:

```bash
exo fsck --drop-bad data/corrupted-file.parquet --remote s3-archive
```

This removes the bad copy so you can re-upload a good version.

### Clear Bad File List

After fixing issues, clear the bad file list:

```bash
exo fsck --clear-bad
```

## Diagnostic Commands

### Check Repository Status

```bash
# Git status
git status

# Annex status
git annex status

# ExoHub info
exo info
```

### Find Where Files Are

```bash
git annex whereis data/file.parquet
```

Example output:
```
whereis data/file.parquet (2 copies)
    00000000-0000-0000-0000-000000000001 -- s3-archive
    00000000-0000-0000-0000-000000000002 -- backup-remote
    web: http://yann.lecun.com/exdb/mnist/train-images-idx3-ubyte.gz
ok
```

### Check File Integrity

```bash
# Verify specific files
exo fsck --deep data/file.parquet

# Verify all files
exo fsck --deep
```

## Recovery Procedures

### Recovering from Corrupted Files

1. **Identify bad files:**
   ```bash
   exo fsck --scan-bad
   exo fsck --show-bad
   ```

2. **Drop bad copies:**
   ```bash
   exo fsck --drop-bad --remote s3-archive --no-confirm
   ```

3. **Try to get good copies from other remotes:**
   ```bash
   exo sync --with backup-remote
   ```

4. **Verify the fix:**
   ```bash
   exo fsck --deep data/
   ```

5. **Clear the bad file list:**
   ```bash
   exo fsck --clear-bad
   ```

### Recovering from Failed Sync

If a sync was interrupted:

1. **Resume the sync:**
   ```bash
   exo sync
   ```

   Git-annex automatically resumes from where it left off.

2. **For stubborn files, try a specific remote:**
   ```bash
   exo sync --with backup-remote --path data/problem-file.parquet
   ```

### Recovering from Repository Corruption

If the git-annex metadata is corrupted:

1. **Check git-annex health:**
   ```bash
   git annex fsck
   ```

2. **Repair if needed:**
   ```bash
   git annex repair
   ```

3. **Re-initialize remotes:**
   ```bash
   exo init
   ```

### Recovering from Lost Remote Access

If a remote becomes unavailable:

1. **Check available remotes:**
   ```bash
   exo info
   git annex whereis --all
   ```

2. **Sync from alternative remotes:**
   ```bash
   exo sync --with backup-remote
   ```

3. **If the remote is permanently lost, mark it as dead:**
   ```bash
   exo init --dead <remote-uuid>
   ```

## Performance Issues

### Slow Sync

**Causes:**
- Network latency
- Too few parallel jobs
- Large number of small files

**Solutions:**

1. Increase parallelism:
   ```bash
   exo sync -J 8
   ```

2. For bulk downloads, use mirror:
   ```bash
   exo mirror plan --s3-annex s3://bucket/path --store /mnt/cache
   exo mirror execute
   exo link --store /mnt/cache
   ```

3. Sync specific paths instead of everything:
   ```bash
   exo sync --path data/needed-directory/
   ```

### Slow Clone

**Causes:**
- Full history clone when shallow would suffice
- Cloning entire repository when only subset needed

**Solutions:**

1. Use a shallow clone preset:
   ```bash
   exo clone --preset data-only https://repo.url/dataset.git
   ```

2. Use preset discovery to clone a subset:
   ```bash
   exo clone https://repo.url/dataset.git
   # Presets are auto-discovered; select a subset to clone less data
   ```

### High Disk Usage

**Causes:**
- Multiple copies of files (local + cache)
- Unused historical versions

**Solutions:**

1. Drop unused files:
   ```bash
   git annex drop --auto
   ```

2. Drop specific files:
   ```bash
   git annex drop data/old-version/
   ```

3. Use hard links instead of copies:
   ```bash
   exo link --store /mnt/cache --mode hardlink
   ```

## Getting Help

If you're still stuck:

1. **Run the diagnostics command:**
   ```bash
   exo doctor
   ```

2. **Check repository info:**
   ```bash
   exo info --json > repo-info.json
   ```

3. **Gather additional diagnostics:**
   ```bash
   git annex version
   exo version
   git annex fsck 2>&1 | head -100 > fsck-output.txt
   ```

4. **Contact support** with:
   - The error message
   - Output of `exo doctor`
   - Repository info
   - Steps to reproduce

## Summary of fsck Commands

| Command | Description |
|---------|-------------|
| `exo fsck --scan-bad` | Scan logs for bad files |
| `exo fsck --show-bad` | List recorded bad files |
| `exo fsck --clear-bad` | Clear bad file list |
| `exo fsck --deep <path>` | Verify file checksums |
| `exo fsck --mark-bad <path>` | Mark files as bad |
| `exo fsck --drop-bad <path> --remote <name>` | Remove bad copies |
