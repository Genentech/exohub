---
title: "Managing Credentials"
description: "Store and manage SSH keys and git tokens with ExoSafe"
weight: 3
---

This section covers how to store, manage, and provision credentials for accessing Git hosts through ExoHub. ExoSafe is ExoHub's cloud-based credential store — it lets you keep SSH keys and git tokens centrally so they're automatically provisioned whenever you log in.

## Why Use ExoSafe?

When you work across multiple machines, containers, or CI/CD pipelines, setting up SSH keys and git tokens on each environment is tedious and error-prone. ExoSafe solves this:

- **Store once, use everywhere** — credentials are provisioned automatically on `exo login`
- **Ephemeral environments** — containers, CI runners, and new workstations get credentials instantly
- **Secure storage** — credentials are stored encrypted in AWS Parameter Store, scoped to your account

## How It Works

1. You store your credentials (SSH keys, git tokens) in ExoSafe using `exo safe store`
2. When you run `exo login`, ExoSafe credentials are retrieved and configured locally
3. Subsequent `exo` commands (clone, sync, get) use them automatically

## Storing Credentials

Run `exo safe store` without arguments for an interactive wizard, or use flags:

```bash
# Interactive wizard
exo safe store

# Store an SSH key
exo safe store github.com --type ssh-key --file ~/.ssh/id_ed25519

# Store a git token from a file
exo safe store github.com --type token --file ~/token.txt

# Store a git token from an environment variable
exo safe store github.com --type token --from-env GITHUB_TOKEN
```

The credential name should be the git provider hostname (e.g., `github.com`, `gitlab.com`).

## Listing and Inspecting Credentials

```bash
# List all stored credentials
exo safe list

# Show details for a specific credential
exo safe show github.com
```

## Deleting Credentials

```bash
# Delete a single credential
exo safe delete github.com

# Remove all credentials
exo safe clear
```

## Backup and Restore

You can export your entire credential store for migration or disaster recovery:

```bash
# Export all credentials
exo safe dump credentials-backup.json

# Restore from backup
exo safe restore credentials-backup.json
```

> **Security note:** The dump file contains sensitive credential values. Store it securely and delete it after use.

## Non-Interactive Provisioning (CI/CD)

Use `exo safe pull` to provision credentials from ExoSafe **without any interactive prompt**. This is designed for CI/CD pipelines, containers, and scripts where you cannot run `exo login`.

```bash
# Provision all stored credentials (SSH keys and git tokens)
exo safe pull

# Same, but output a JSON summary (useful for scripts)
exo safe pull --json
```

Example output:

```
🔑 Provisioned 1 SSH key(s).
🎫 Provisioned 2 git credential(s).
```

With `--json`:

```json
{"ssh_keys":1,"git_credentials":2}
```

The command reads the bearer token using this precedence:
`EXO_TOKEN_FILE` → `EXO_CONFIG_DIR/credentials/token.json` → system default.

It exits non-zero if the token is missing or expired, or if ExoSafe is empty. In a CI/CD context, pair it with `EXO_CONFIG_DIR` and `EXO_TOKEN_FILE` to keep all state isolated:

```bash
export EXO_CONFIG_DIR=/tmp/exo-ci
export EXO_TOKEN_FILE=/run/secrets/exo-token
exo safe pull
exo sync
```

## Command Reference

| Subcommand | Description |
|------------|-------------|
| `exo safe store` | Store a credential (interactive wizard or flags) |
| `exo safe list` | List stored credentials |
| `exo safe show <name>` | Show credential details |
| `exo safe delete <name>` | Delete a credential |
| `exo safe clear` | Remove all credentials |
| `exo safe pull` | Provision credentials non-interactively (CI/CD) |
| `exo safe dump <file>` | Export credentials as JSON backup to `<file>` |
| `exo safe restore <file>` | Restore credentials from a JSON backup in `<file>` |

## AWS Credential Auto-Refresh

When using S3 Access Grants, the `exo-credential-helper` automatically detects expired AWS tokens and refreshes them by calling `exo login`. This happens transparently during sync, export, and other data operations — you don't need to manually re-authenticate when tokens expire.

## Next Steps

With credentials configured, learn how to [work with repositories]({{< ref "repositories" >}}).
