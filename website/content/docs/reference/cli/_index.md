---
title: "CLI Reference"
description: "Full --help output for every exo command and subcommand"
---


Full `--help` output for every `exo` command and subcommand, generated from **exo-cli v0.140.2**. Regenerated daily by `scripts/gen-cli-reference` — do not hand-edit this section.

## add

- [exo add]({{< ref "exo-add" >}}) — Add files or URLs with repository-level git/annex routing

## atlas

- [exo atlas]({{< ref "exo-atlas" >}}) — Browse and search an ArtifactDB catalog

## auth

- [exo auth]({{< ref "exo-auth" >}}) — Authorize a Google Drive remote

## broadcast

- [exo broadcast]({{< ref "exo-broadcast" >}}) — Broadcast git-annex metadata

## bundle

- [exo bundle]({{< ref "exo-bundle" >}}) — Generate JSON metadata bundle for catalog ingest

## clone

- [exo clone]({{< ref "exo-clone" >}}) — Clone repository with preset-based sparse checkout

## completion

- [exo completion]({{< ref "exo-completion" >}}) — Generate the autocompletion script for the specified shell
  - [exo completion bash]({{< ref "exo-completion-bash" >}}) — Generate the autocompletion script for bash
  - [exo completion fish]({{< ref "exo-completion-fish" >}}) — Generate the autocompletion script for fish
  - [exo completion powershell]({{< ref "exo-completion-powershell" >}}) — Generate the autocompletion script for powershell
  - [exo completion zsh]({{< ref "exo-completion-zsh" >}}) — Generate the autocompletion script for zsh

## context

- [exo context]({{< ref "exo-context" >}}) — Manage git host configurations
  - [exo context create]({{< ref "exo-context-create" >}}) — Create a new context
  - [exo context delete]({{< ref "exo-context-delete" >}}) — Delete a context
  - [exo context list]({{< ref "exo-context-list" >}}) — List all context names
  - [exo context show]({{< ref "exo-context-show" >}}) — Show a context
  - [exo context update]({{< ref "exo-context-update" >}}) — Update an existing context

## copy

- [exo copy]({{< ref "exo-copy" >}}) — Copy annexed content between remotes

## doctor

- [exo doctor]({{< ref "exo-doctor" >}}) — Run diagnostics on your ExoHub client environment

## download

- [exo download]({{< ref "exo-download" >}}) — Download artifact files from the catalog

## export

- [exo export]({{< ref "exo-export" >}}) — Export annexed content

## fsck

- [exo fsck]({{< ref "exo-fsck" >}}) — Inspect annex logs and repair bad files

## get

- [exo get]({{< ref "exo-get" >}}) — Get annexed content (wrapper around git-annex get with sensible defaults)

## heartbeat

- [exo heartbeat]({{< ref "exo-heartbeat" >}}) — Record and show heartbeat metrics
  - [exo heartbeat record]({{< ref "exo-heartbeat-record" >}}) — Record exohub metrics into .git/exohub/metrics/
  - [exo heartbeat show]({{< ref "exo-heartbeat-show" >}}) — Show heartbeat metrics

## info

- [exo info]({{< ref "exo-info" >}}) — Show git-annex repository info

## init

- [exo init]({{< ref "exo-init" >}}) — Initialize repository and remotes
  - [exo init remote]({{< ref "exo-init-remote" >}}) — Create and configure git-annex remotes

## link

- [exo link]({{< ref "exo-link" >}}) — Reconnect git-annex symlinks to an external store of annex key files

## locate

- [exo locate]({{< ref "exo-locate" >}}) — Show where files are stored (git, annex, remotes)

## lock

- [exo lock]({{< ref "exo-lock" >}}) — Lock annexed files (switch back to read-only symlinks)

## login

- [exo login]({{< ref "exo-login" >}}) — Authenticate via device flow

## logout

- [exo logout]({{< ref "exo-logout" >}}) — Clear cached authentication credentials

## manifest

- [exo manifest]({{< ref "exo-manifest" >}}) — Validate and initialize exohub manifests
  - [exo manifest init]({{< ref "exo-manifest-init" >}}) — Print a manifest template
  - [exo manifest validate]({{< ref "exo-manifest-validate" >}}) — Validate a manifest

## mcp

- [exo mcp]({{< ref "exo-mcp" >}}) — MCP server for agent-driven data operations
  - [exo mcp list]({{< ref "exo-mcp-list" >}}) — List available MCP tools
  - [exo mcp show]({{< ref "exo-mcp-show" >}}) — Show full details for a tool
  - [exo mcp start]({{< ref "exo-mcp-start" >}}) — Start the MCP server over stdio

## mirror

- [exo mirror]({{< ref "exo-mirror" >}}) — Resolve annex keys and mirror them from S3
  - [exo mirror execute]({{< ref "exo-mirror-execute" >}}) — Execute a previously generated plan
  - [exo mirror plan]({{< ref "exo-mirror-plan" >}}) — Generate an s5cmd plan file without executing

## provenance

- [exo provenance]({{< ref "exo-provenance" >}}) — Record and inspect data lineage
  - [exo provenance add]({{< ref "exo-provenance-add" >}}) — Create a new provenance record
  - [exo provenance install-hook]({{< ref "exo-provenance-install-hook" >}}) — Install a pre-commit hook that runs exo provenance validate --staged
  - [exo provenance show]({{< ref "exo-provenance-show" >}}) — Show an artifact's provenance lineage
  - [exo provenance validate]({{< ref "exo-provenance-validate" >}}) — Validate provenance records

## publish

- [exo publish]({{< ref "exo-publish" >}}) — Orchestrate sync → bundle → sync (catalog) → broadcast

## pull

- [exo pull]({{< ref "exo-pull" >}}) — Clone or update a repo at a ref

## safe

- [exo safe]({{< ref "exo-safe" >}}) — Manage credentials in ExoSafe
  - [exo safe clear]({{< ref "exo-safe-clear" >}}) — Remove all credentials from ExoSafe
  - [exo safe delete]({{< ref "exo-safe-delete" >}}) — Delete a credential from ExoSafe
  - [exo safe dump]({{< ref "exo-safe-dump" >}}) — Dump all credentials to a file
  - [exo safe list]({{< ref "exo-safe-list" >}}) — List credentials in ExoSafe
  - [exo safe pull]({{< ref "exo-safe-pull" >}}) — Provision credentials from ExoSafe (non-interactive)
  - [exo safe restore]({{< ref "exo-safe-restore" >}}) — Restore credentials from a dump file
  - [exo safe show]({{< ref "exo-safe-show" >}}) — Show a credential from ExoSafe
  - [exo safe store]({{< ref "exo-safe-store" >}}) — Store a credential in ExoSafe

## standalone

- [exo standalone]({{< ref "exo-standalone" >}}) — Manage the local adb-standalone stack
  - [exo standalone down]({{< ref "exo-standalone-down" >}}) — Stop the adb-standalone stack
  - [exo standalone ingest]({{< ref "exo-standalone-ingest" >}}) — Catalog a new project version from a bundle directory
  - [exo standalone seed]({{< ref "exo-standalone-seed" >}}) — Bulk-load ADB documents from an NDJSON file
  - [exo standalone status]({{< ref "exo-standalone-status" >}}) — Show health of the adb-standalone stack
  - [exo standalone up]({{< ref "exo-standalone-up" >}}) — Bring up the full adb-standalone stack (foreground, supervised)

## submit

- [exo submit]({{< ref "exo-submit" >}}) — Submit a manifest to the ExoHub API

## sync

- [exo sync]({{< ref "exo-sync" >}}) — Sync git-annex content and record bad files

## theme

- [exo theme]({{< ref "exo-theme" >}}) — Manage terminal color theme
  - [exo theme list]({{< ref "exo-theme-list" >}}) — List available themes
  - [exo theme mode]({{< ref "exo-theme-mode" >}}) — Set dark/light mode preference
  - [exo theme set]({{< ref "exo-theme-set" >}}) — Set the active theme
  - [exo theme show]({{< ref "exo-theme-show" >}}) — Show the current active theme and mode

## tip

- [exo tip]({{< ref "exo-tip" >}}) — Show a random tip

## unlock

- [exo unlock]({{< ref "exo-unlock" >}}) — Unlock annexed files (make them writable)

## upgrade

- [exo upgrade]({{< ref "exo-upgrade" >}}) — Upgrade exo CLI

## version

- [exo version]({{< ref "exo-version" >}}) — Print exo CLI version

## whoami

- [exo whoami]({{< ref "exo-whoami" >}}) — Show who you're logged in as, session info, and AWS credentials
