---
title: "Running Sync Jobs at Scale"
description: "Use exo submit and manifests to automate large-scale sync operations on HPC infrastructure"
draft: true
---

This tutorial shows how to move beyond interactive `exo sync` and submit long-running sync jobs to ExoHub's backend workers. You will write a manifest, submit it to an HPC queue, monitor its progress, and handle failures — using a large genomics dataset as a running example.

See the [Jobs at Scale reference]({{< ref "docs/user-guide/jobs-at-scale" >}}) for the complete list of manifest fields and CLI flags.

## When to Use Job Submission

| Situation | Recommended approach |
|-----------|----------------------|
| Exploring a dataset interactively | `exo sync`, `exo get` |
| Dataset < ~10 GB on a workstation | `exo sync` |
| Dataset > 100 GB, or hundreds of files | `exo submit` |
| Nightly / scheduled automation | `exo submit` via cron or CI |
| Reproducible production pipeline | `exo submit` with a versioned manifest |

The key difference is where the transfer runs. `exo sync` runs on **your machine** and blocks your terminal. `exo submit` hands the job to an ExoHub worker — a long-lived process on cloud or HPC infrastructure — so you can close your laptop and check back later.

## Prerequisites

Install `exo` and authenticate:

```bash
{{< exohub "installCmd" >}}
export PATH="$HOME/.local/bin:$PATH"
exo login
```

## 1. Write a Manifest

A manifest is a YAML file that describes exactly what to sync and how. Create `sequencing-sync.yaml`:



```yaml
name: sequencing-nightly
url: https://github.com/genomics/sequencing-data.git
ref: main

remote-type: annex
repo-dir: /data/work/sequencing
with-remotes:
  - s3-primary
  - s3-backup

paths:
  - data/samples/
  - results/alignments/

include:
  - "**/*.fastq.gz"
  - "**/*.bam"
  - "**/*.bai"

exclude:
  - "**/temp/*"

jobs: 16
```


### Key fields explained

| Field | Description |
|-------|-------------|
| `name` | Human-readable identifier; appears in monitoring output |
| `url` | Repository URL |
| `ref` | Branch, tag, or commit SHA to sync |
| `remote-type` | `annex` (bidirectional) or `export` (snapshot to S3) |
| `repo-dir` | Where the worker checks out the repository |
| `with-remotes` | Which remotes to sync with (matches names in `.exohub/remotes`) |
| `paths` | Limit the sync to specific subdirectories |
| `include` / `exclude` | Fine-grained file patterns |
| `jobs` | Parallel transfer threads; tune to your network/storage throughput |

If `paths`, `include`, and `exclude` are all omitted, the entire repository is synced.

### Validate before submitting

Always validate the manifest before sending it to the queue:

```bash
exo manifest validate sequencing-sync.yaml
```

```
✓ sequencing-sync.yaml is valid
```

If a field is missing or has the wrong type, the validator explains the problem before anything runs on the backend.

### Generate a template

Not sure where to start? Generate a pre-filled template:

```bash
exo manifest init --type annex > sequencing-sync.yaml
```

Edit the generated file and fill in your repository URL, remotes, and paths.

## 2. Submit the Job

Send the manifest to the backend:

```bash
exo submit --manifest sequencing-sync.yaml --queue exohub-sync-shpc
```

```
Workflow: exosync-sequencing-nightly-main-annex (run: a1b2c3d4-e5f6-7890-abcd-ef1234567890)
```

Save the **workflow ID** — you will need it to check status.

### Choosing a queue

| Queue | Use when |
|-------|----------|
| `exohub-sync-aws` | General purpose, cloud-based workers |
| `exohub-sync-shpc` | On-premises HPC with fast local storage |

Pick `exohub-sync-shpc` when your remotes are mounted on the HPC filesystem or when you need very high throughput to on-premises NAS storage.

## 3. Monitor Progress

Check the status of your job using the workflow ID printed at submit time:

```bash
exo heartbeat show --workflow-id exosync-sequencing-nightly-main-annex
```

```
Workflow: exosync-sequencing-nightly-main-annex
Run:      a1b2c3d4-e5f6-7890-abcd-ef1234567890
Status:   🔄 Running

Files synced:  1 842 / 3 210
Bytes synced:  142.3 GB / 248.7 GB
Elapsed:       00:23:41
```

Status indicators:

| Icon | Meaning |
|------|---------|
| 🔄 Running | Transfer in progress |
| ✅ Completed | All files synced successfully |
| ⏳ Pending | Job queued, not yet started |
| ⚠️ Cancelled | Job was cancelled manually |
| ❌ Failed | An error stopped the job |

### View a specific run

If you have resubmitted the same manifest multiple times, list all runs interactively:

```bash
exo heartbeat show --workflow-id exosync-sequencing-nightly-main-annex
# An interactive picker lets you select a run
```

Or target a run directly:

```bash
exo heartbeat show \
  --workflow-id exosync-sequencing-nightly-main-annex \
  --run-id a1b2c3d4
```

Always fetch the most recent run (useful in CI/CD scripts):

```bash
exo heartbeat show \
  --workflow-id exosync-sequencing-nightly-main-annex \
  --run-id last
```

### Machine-readable output

For dashboards and scripts, use `--json`:

```bash
exo heartbeat show \
  --workflow-id exosync-sequencing-nightly-main-annex \
  --json
```

## 4. Handle Failures and Retries

### When a job fails

Check the status first:

```bash
exo heartbeat show --workflow-id exosync-sequencing-nightly-main-annex --run-id last
```

Common causes:

- **Authentication expired** — run `exo login` and resubmit
- **Remote unreachable** — verify network access and remote names in `.exohub/remotes`
- **Disk full on the worker** — free space in `repo-dir` or update the path in the manifest
- **Corrupted annex objects** — run `exo fsck` locally, then resubmit

### Retrying

Resubmit the same manifest:

```bash
exo submit --manifest sequencing-sync.yaml --queue exohub-sync-shpc
```

The worker is idempotent: it skips files that are already present on the remote and only transfers what is missing. A retry picks up exactly where the previous run stopped.

### Isolate the problem first

Before resubmitting the full job, create a debug manifest that targets a small subset:

```yaml
# debug-sync.yaml  (keep all other fields the same)
paths:
  - data/samples/sample-001/
jobs: 1
```

```bash
exo manifest validate debug-sync.yaml
exo submit --manifest debug-sync.yaml --queue exohub-sync-shpc
```

Once the small sync succeeds, restore the full manifest and resubmit.

## 5. Full Example: Nightly HPC Dataset Sync

### Manifest (`/etc/exohub/sequencing-nightly.yaml`)



```yaml
name: sequencing-nightly
url: https://github.com/genomics/sequencing-data.git
ref: main

remote-type: annex
repo-dir: /data/work/sequencing
with-remotes:
  - s3-primary
  - s3-backup

include:
  - "**/*.fastq.gz"
  - "**/*.bam"
  - "**/*.bai"

exclude:
  - "**/temp/*"

jobs: 16
```


### Cron job

```bash
# /etc/cron.d/exohub-sequencing
# Every night at 02:00
0 2 * * * datauser /usr/local/bin/exo submit \
    --manifest /etc/exohub/sequencing-nightly.yaml \
    --queue exohub-sync-shpc
```

### Morning check

```bash
exo heartbeat show \
  --workflow-id exosync-sequencing-nightly-main-annex \
  --run-id last
```

### Verify locally after completion

```bash
cd /data/work/sequencing
exo info
```

## What's Next

- **Automate with CI/CD** — see the [Jobs at Scale reference]({{< ref "docs/user-guide/jobs-at-scale" >}}) for a complete GitLab CI pipeline with validate → submit → monitor stages.
- **Export to S3** — use `remote-type: export` to make results directly accessible without git-annex.
- **Advanced operations** — copying between remotes, mirroring, and bundle generation are in [Advanced Operations]({{< ref "docs/user-guide/advanced" >}}).
