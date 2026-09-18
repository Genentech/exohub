---
title: "Versioning & Collaboration"
description: "Git workflows for data teams"
weight: 6
---

ExoHub uses Git for versioning, which means you get all the collaboration features you're used to from code development: branches, merge requests, tags, and history. This section covers how to apply these workflows to data.

## Why Version Data?

Versioning your data gives you:

- **Reproducibility** — Know exactly which data version was used for an experiment
- **Collaboration** — Multiple people can work on the same dataset safely
- **History** — See what changed, when, and why
- **Rollback** — Revert to a previous version if something goes wrong

## Git Basics for Data

If you're familiar with Git for code, working with data is similar. The main difference is that large files are tracked by git-annex instead of Git directly.

### Checking Status

```bash
git status
```

This shows:
- Modified files (both regular and annexed)
- Untracked files
- Staged changes

### Viewing History

```bash
git log --oneline
```

See what files changed in a commit:

```bash
git show --stat abc123
```

### Checking Out a Previous Version

```bash
# Checkout a specific commit
git checkout abc123

# Don't forget to sync the data for that version
exo sync

# Return to the latest
git checkout main
```

## Working with Branches

Branches let you work on changes without affecting the main dataset.

### Create a Branch

```bash
git checkout -b add-new-samples
```

### Make Changes

```bash
# Add new data files
exo add data/new-samples/

# Commit
git commit -m "Add Q1 2024 samples"

# Sync to remote storage
exo sync --with s3-archive
exo broadcast --with s3-archive

# Push the branch
git push -u origin add-new-samples
```

### Switch Between Branches

```bash
# Switch to another branch
git checkout main

# Sync data for this branch
exo sync
```

When you switch branches, the annexed file symlinks update to point to the correct versions. Run `exo sync` to ensure you have the data locally.

## Creating Merge Requests

Merge requests (or pull requests) let you propose changes for review before merging them into the main branch.

### 1. Push Your Branch

```bash
git push -u origin my-feature-branch
```

### 2. Create the Merge Request



On your Git host (e.g. GitHub or GitLab):


1. Go to your repository
2. Click **Merge Requests** → **New merge request**
3. Select your source branch and target branch (usually `main`)
4. Fill in the title and description
5. Assign reviewers if needed
6. Click **Create merge request**

### 3. Review Process

Reviewers can:
- See which files changed
- Comment on specific changes
- Request modifications
- Approve the merge request

### 4. Merge

Once approved, click **Merge** to incorporate your changes into the target branch.

### What Gets Reviewed?

In a data merge request, reviewers see:
- Changes to regular files (scripts, notebooks, configs)
- Changes to annexed file pointers (which data files were added/modified/removed)
- The commit history and messages

They won't see the actual data content in the diff, but they can check out your branch locally to inspect the data.

## Tags and Releases

Tags mark specific versions of your dataset for easy reference.

### Creating a Tag

```bash
# Lightweight tag
git tag v1.0.0

# Annotated tag with message
git tag -a v1.0.0 -m "Initial release with training and test data"

# Push tags
git push --tags
```

### Listing Tags

```bash
git tag
```

### Checking Out a Tag

```bash
git checkout v1.0.0
exo sync
```

### Release Naming Conventions

Consider a naming scheme like:

- `v1.0.0` — Major releases (breaking changes to schema)
- `v1.1.0` — Minor releases (new data added)
- `v1.1.1` — Patch releases (corrections, metadata fixes)

Or date-based:

- `2024-01` — Monthly snapshots
- `2024-Q1` — Quarterly releases

## Release Branches

For datasets with multiple supported versions, use release branches:

```bash
# Create a release branch
git checkout -b release/v1.x

# Make hotfixes on the release branch
git commit -m "Fix corrupted sample in batch 42"

# Tag releases from the branch
git tag v1.0.1
git push origin release/v1.x --tags
```

This lets you maintain older versions while continuing development on `main`.

## GitOps for Data (DataOps)

GitOps principles can be applied to data workflows:

### Declarative Configuration

Define your dataset configuration in version-controlled files:

```yaml
# .exohub/remotes
remotes:
  - name: production
    type: annex
    url: s3://prod-bucket/datasets/my-data

  - name: staging
    type: annex
    url: s3://staging-bucket/datasets/my-data
```

### Automated Pipelines

Use CI/CD to automate data operations:

```yaml
# .gitlab-ci.yml
stages:
  - validate
  - sync

validate:
  stage: validate
  script:
    - exo manifest validate sync-manifest.yaml

sync-to-staging:
  stage: sync
  script:
    - exo sync --with staging --manifest sync-manifest.yaml
  only:
    - develop

sync-to-production:
  stage: sync
  script:
    - exo sync --with production --manifest sync-manifest.yaml
  only:
    - main
```

### Pull-Based Sync

Downstream systems can pull specific versions:

```bash
# In a consumer pipeline
exo clone --preset data-only https://github.com/org/dataset.git
cd dataset
git checkout v1.2.3
exo init
exo sync
```

## Best Practices

### Commit Messages

Use [Conventional Commits](https://www.conventionalcommits.org/) for consistent, machine-readable commit messages:

```
feat(data): add Q1 2024 sequencing data

- 1,234 new samples from sites A, B, C
- Updated manifest with sample metadata
- Ran QC validation (all passed)
```

Common prefixes:
- `feat:` — New data or features
- `fix:` — Corrections to existing data
- `docs:` — Documentation changes
- `chore:` — Maintenance tasks (config updates, cleanup)

### Branch Naming

Use descriptive branch names:

- `add-2024-samples`
- `fix-corrupted-batch-42`
- `update-metadata-schema`

### Review Large Changes

For significant dataset changes:
- Create a merge request
- Include context in the description
- Tag relevant reviewers
- Consider running validation scripts

### Document Versions

Maintain a CHANGELOG or use release notes. When using conventional commits, you can automate changelog generation with tools like [Commitizen](https://commitizen-tools.github.io/commitizen/) (`cz`):

```bash
# Generate changelog from conventional commits
cz changelog
```

Example output:

```markdown
## v1.2.0 (2024-03-01)

### Added
- Q1 2024 samples (1,234 new records)
- Validation reports for all batches

### Fixed
- Corrected sample IDs in batch 42
```

## Future: DataOps Workflows

ExoHub is working on deeper DataOps integration:

- **Automated validation** — Run checks on data changes
- **Data quality gates** — Block merges if quality thresholds aren't met
- **Scheduled syncs** — Keep environments in sync automatically

These features are on the roadmap. For now, you can implement similar workflows using CI/CD pipelines and the `exo` CLI.

## Next Steps

For large datasets that need scheduled jobs and monitoring, see [Running Jobs at Scale]({{< ref "jobs-at-scale" >}}).
