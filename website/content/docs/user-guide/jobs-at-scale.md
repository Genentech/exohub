---
title: "Running Jobs at Scale"
description: "Manifests, job submission, and monitoring"
weight: 7
---

For large datasets or automated pipelines, you can submit sync jobs to ExoHub's backend infrastructure. This section covers writing manifests, submitting jobs, and monitoring their progress.

## When to Use Job Submission

Use the interactive CLI (`exo sync`) for:
- Small to medium datasets
- Interactive exploration
- Development and testing

Use job submission (`exo submit`) for:
- Large datasets (hundreds of GB or more)
- Scheduled/automated syncs
- Production pipelines
- When you need monitoring and retry capabilities

## Manifests

A manifest is a YAML file that describes a sync operation. Instead of passing flags to the CLI, you define everything in the manifest.

### Basic Sync Manifest

```yaml
name: mnist-sync
url: {{< exohub "cloneExample" >}}
ref: main

remote-type: annex
repo-dir: /data/work/mnist
with-remotes:
  - s3-archive
```

### Manifest Fields

| Field | Description |
|-------|-------------|
| `name` | Identifier for this job |
| `url` | Git repository URL |
| `ref` | Branch, tag, or commit to sync |
| `remote-type` | Type of operation: `annex`, `export` |
| `repo-dir` | Local directory for the repository |
| `with-remotes` | List of remotes to sync with |
| `paths` | Paths to limit the sync (optional) |
| `jobs` | Number of parallel jobs (optional) |
| `include` | Include patterns (optional) |
| `exclude` | Exclude patterns (optional) |

### Export Manifest

```yaml
name: mnist-export
url: {{< exohub "cloneExample" >}}
ref: v1.0.0

remote-type: export
repo-dir: /data/work/mnist
from-remote: s3-archive
to-remote: s3-export
paths:
  - data/processed/
```

### Manifest with Filtering

```yaml
name: genomics-sync
url: {{< exohub "gitHost" >}}/org/genomics-data.git
ref: main

remote-type: annex
repo-dir: /data/work/genomics
with-remotes:
  - s3-primary

paths:
  - results/
  - data/samples/

include:
  - "*.csv"
  - "*.parquet"

exclude:
  - "**/temp/*"
  - "*.bam"

jobs: 8
```

## Validating Manifests

Before submitting, validate your manifest:

```bash
exo manifest validate my-manifest.yaml
```

This checks:
- Required fields are present
- Field types are correct
- Remote types are valid

### Generate a Template

To get a starting template:

```bash
exo manifest init --type annex > sync-manifest.yaml
exo manifest init --type export > export-manifest.yaml
exo manifest init --type permissions > permissions-manifest.yaml
exo manifest init --type bundle > bundle-manifest.yaml
```

The `permissions` type generates a manifest for managing S3 Access Grants across repositories (see [Permissions & Access Control]({{< ref "permissions" >}})). The `bundle` type generates a manifest for `exo bundle` metadata generation (see [Advanced Operations]({{< ref "advanced" >}})).

## Submitting Jobs

Submit a manifest to the ExoHub backend:

```bash
exo submit --manifest my-manifest.yaml
```

This sends the job to the processing queue. You'll receive a job ID for tracking.

### Specifying a Queue

Jobs can run on different infrastructure:

```bash
# Run on AWS infrastructure
exo submit --manifest my-manifest.yaml --queue exohub-sync-aws

# Run on on-premises HPC
exo submit --manifest my-manifest.yaml --queue exohub-sync-shpc
```

Available queues:
- `exohub-sync-aws` — AWS-based workers
- `exohub-sync-shpc` — On-premises HPC cluster

## Monitoring with Heartbeat

When you submit a job, the CLI displays the workflow and run IDs:

```bash
exo submit --manifest my-manifest.yaml
# Workflow: exosync-my-dataset-main-annex (run: a1b2c3d4-e5f6-7890-abcd-ef1234567890)
```

### Viewing Job Status

Check the status of a running or completed job using the `workflow_id`:

```bash
exo heartbeat show --workflow-id exosync-my-dataset-main-annex
```

View a specific run:

```bash
exo heartbeat show --workflow-id exosync-my-dataset-main-annex --run-id a1b2c3d4

# Or view the most recent run
exo heartbeat show --workflow-id exosync-my-dataset-main-annex --run-id last
```

For JSON output (useful for dashboards and scripts):

```bash
exo heartbeat show --workflow-id exosync-my-dataset-main-annex --json
```

### Run Selection

When `--run-id` is omitted, an interactive picker lists all runs for the workflow so you can select one. In non-interactive terminals (CI/CD), the most recent run is selected automatically.

The output shows workflow status with colored indicators:
- ✅ Completed — all activities finished successfully
- 🔄 Running — workflow is still in progress
- ⏳ Pending — workflow is running but metrics are still being computed
- ⚠️ Cancelled — workflow was cancelled
- ❌ Failed — workflow encountered an error

## Workflow Example

Here's a complete workflow for a large dataset sync:

### 1. Create the Manifest



```yaml
# production-sync.yaml
name: production-genomics-sync
url: https://github.com/genomics/sequencing-data.git
ref: main

remote-type: annex
repo-dir: /data/production/sequencing
with-remotes:
  - s3-primary
  - s3-backup

jobs: 16

include:
  - "**/*.fastq.gz"
  - "**/*.bam"
```


### 2. Validate

```bash
exo manifest validate production-sync.yaml
```

### 3. Submit

```bash
exo submit --manifest production-sync.yaml --queue exohub-sync-aws
# Workflow: exosync-production-genomics-sync-main-annex (run: a1b2c3d4-...)
```

### 4. Monitor

The job runs on backend infrastructure. Check progress using the workflow ID from the submit output:

```bash
exo heartbeat show --workflow-id exosync-production-genomics-sync-main-annex
```

### 5. Verify

After completion, verify the sync locally:

```bash
cd /data/production/sequencing
exo info
```

## CI/CD Integration

Integrate job submission into your pipelines:

```yaml
# .gitlab-ci.yml
variables:
  MANIFEST_FILE: sync-manifest.yaml

stages:
  - validate
  - submit
  - monitor

validate-manifest:
  stage: validate
  script:
    - exo manifest validate $MANIFEST_FILE

submit-sync-job:
  stage: submit
  script:
    - |
      JOB_ID=$(exo submit --manifest $MANIFEST_FILE --queue exohub-sync-aws)
      echo "JOB_ID=$JOB_ID" >> job.env
  artifacts:
    reports:
      dotenv: job.env
  only:
    - main

verify-sync:
  stage: monitor
  script:
    - exo heartbeat show --workflow-id $JOB_ID --json > metrics.json
  artifacts:
    paths:
      - metrics.json
  when: delayed
  start_in: 30 minutes
```

## Scheduled Syncs

For recurring syncs, combine with cron or CI schedules:

### Using Cron

```bash
# /etc/cron.d/exohub-sync
0 2 * * * datauser /usr/local/bin/exo submit --manifest /etc/exohub/nightly-sync.yaml --queue exohub-sync-aws
```

### Using GitLab Schedules

1. Go to **CI/CD** → **Schedules**
2. Create a new schedule
3. Set the interval (e.g., daily at 2 AM)
4. Target your sync pipeline

## Troubleshooting Jobs

### Job Stuck

If a job isn't progressing:
1. Check the queue status via the ExoHub API
2. Verify network connectivity to remotes
3. Check for resource constraints (disk space, memory)

### Job Failed

Review the job logs:
- Check `.git/exohub/` for local logs
- Review backend logs via the ExoHub dashboard
- Run `exo fsck` to identify corrupted files

### Retrying

Jobs can be resubmitted:

```bash
exo submit --manifest my-manifest.yaml
```

The sync will resume from where it left off, skipping already-synced files.

## Next Steps

For specialized operations like copying between remotes or mirroring, see [Advanced Operations]({{< ref "advanced" >}}).
