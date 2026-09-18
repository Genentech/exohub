---
title: "Credentials Everywhere with ExoSafe"
description: "Use ExoSafe to store SSH keys and tokens once, then provision them automatically across laptops, HPC clusters, and containers"
draft: true
---

This tutorial shows how to use ExoSafe to manage credentials centrally and provision them automatically on every machine and environment you work in — no manual key setup required.

By the end you will know how to:

- Store an SSH key or git token in ExoSafe
- Provision credentials automatically on a fresh machine or container with `exo login`
- List and manage stored credentials
- Integrate ExoSafe into a CI/CD pipeline

For full reference, see the [Managing Credentials]({{< ref "docs/user-guide/credentials" >}}) user-guide page.

## Prerequisites

Install `exo` (Linux/macOS):

```bash
{{< exohub "installCmd" >}}
export PATH="$HOME/.local/bin:$PATH"
exo version
```

Authenticate with ExoHub once so that ExoSafe can reach the credential store:

```bash
exo login
```

## 1. Store a Credential



ExoSafe stores credentials under a name that matches the git provider hostname (e.g., `github.com`, `gitlab.com`).


### Interactive wizard

Running `exo safe store` without arguments launches a step-by-step wizard:

```bash
exo safe store
```



```
? Credential name (e.g. github.com): github.com
? Type: [SSH key / Token]
> SSH key
? Path to private key file: ~/.ssh/id_ed25519
✓ Stored credential "github.com" (ssh-key)
```

### Store an SSH key directly

```bash
exo safe store github.com --type ssh-key --file ~/.ssh/id_ed25519
```

```
✓ Stored credential "github.com" (ssh-key)
```

### Store a git token

You can read the token from a file or from an environment variable:

```bash
# from a file
exo safe store github.com --type token --file ~/github-token.txt

# from an environment variable
exo safe store github.com --type token --from-env GITHUB_TOKEN
```


> **Tip:** You only need to do this once, from any machine where you already have the key or token.
> ExoSafe encrypts the credential and stores it in AWS Parameter Store, scoped to your account.

## 2. Provision Credentials on a Fresh Machine

Once credentials are stored, getting set up on a new machine is a single command:

```bash
exo login
```

ExoSafe retrieves all stored credentials and configures them locally. For SSH keys, it writes them to `~/.ssh/` and sets correct permissions. For tokens, it configures `git credential.helper` automatically.

You can verify what was provisioned:



```bash
ssh -T git@ssh.github.com
```

```
Welcome to GitLab, @yourname!
```


This works identically in a Docker container, a fresh HPC login node, or a new laptop — no manual key copying needed.

## 3. List and Manage Stored Credentials

### List everything stored in ExoSafe

```bash
exo safe list
```



```
NAME               TYPE
github.com     ssh-key
github.com         token
```

### Inspect a specific credential

```bash
exo safe show github.com
```

```
name:  github.com
type:  ssh-key
added: 2026-05-15T09:32:11Z
```

### Remove a credential

```bash
exo safe delete github.com
```

### Rotate a credential

Simply overwrite it with a new `store`:

```bash
exo safe store github.com --type ssh-key --file ~/.ssh/id_ed25519_new
```


ExoSafe replaces the existing entry in place.

## 4. CI/CD Integration

ExoSafe is especially useful in ephemeral CI runners. Add `exo login` as an early step and all downstream git operations authenticate automatically.

### GitLab CI example



```yaml
# .gitlab-ci.yml
default:
  image: ghcr.io/genentech/exo-cli:latest

stages:
  - setup
  - run

provision:
  stage: setup
  script:
    - exo login                     # provisions SSH key + token from ExoSafe
    - ssh -T git@ssh.github.com # optional: sanity check

train:
  stage: run
  script:
    - exo clone https://github.com/myteam/mydata.git
    - exo sync                      # pulls annexed data; credentials are already set up
    - python train.py
```


The CI runner never stores any secrets in the pipeline configuration. The only requirement is that the runner has IAM access to the Parameter Store namespace, which is handled by the ExoHub platform.

> For projects that use ExoHub's job-submission features, see
> [Jobs at Scale]({{< ref "docs/user-guide/jobs-at-scale" >}}).

## 5. Working Across Laptop, HPC, and Containers

A typical multi-environment workflow looks like this:

| Environment | What you do |
|---|---|
| Laptop (first time) | `exo safe store` to save SSH key and token |
| HPC login node | `exo login` — credentials provisioned automatically |
| Docker container | `exo login` in entrypoint or CI step |
| New laptop | `exo login` — same experience, zero manual setup |

Because ExoSafe is tied to your ExoHub account rather than any single machine, rotating a key only requires re-running `exo safe store` once. All environments pick up the new credential the next time they call `exo login`.

## Backup and Recovery

If you ever need to migrate to a new ExoHub account or take an offline backup:

```bash
# Export credentials to a JSON file
exo safe dump > credentials-backup.json

# Restore on a new account
exo safe restore < credentials-backup.json
```

> **Security:** The dump file contains plaintext credential values. Encrypt it at rest and delete it after use.

## What's Next

- Learn how to work with repositories: [Repositories]({{< ref "docs/user-guide/repositories" >}})
- Discover how to version and sync data: [Syncing]({{< ref "docs/user-guide/syncing" >}})
- Set up environment variables alongside credentials: [Environment Variables]({{< ref "docs/user-guide/environment-variables" >}})
