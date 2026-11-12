---
title: "exo standalone ingest"
description: "Catalog a new project version from a bundle directory"
---

Full `exo standalone ingest --help` output, generated from exo-cli v0.140.2.

```text
Publish a new project version to the running adb-standalone catalog.

Reads metadata from <bundle-dir> and posts to POST /v1/{tenant}/project/ingest.
The server computes _extra fields (id, gprn, tenant, permissions, latest).

<bundle-dir> must be a directory in the exo bundle format (bundle.json + per-file .json metadata).

Wraps: POST /v1/{tenant}/project/ingest

Usage:
  exo standalone ingest <bundle-dir> [flags]


Flags:
  -h, --help                help for ingest
      --project-id string   Project ID (required)
      --s3-url string       S3 bundle location (s3://bucket/key), optional if server auto-detects
      --tenant string       ADB tenant path segment (default "exohub")
      --url string          adb-standalone base URL (default "http://localhost:8080")
      --version string      Version string, e.g. v1.0.0 (required)

Global Flags:
      --config-dir string   Root directory for all config, cache and credentials (overrides EXO_CONFIG_DIR)
      --context string      Use specific context (overrides selected context)

Docs for agents (llms.txt): https://genentech.github.io/exohub/llms.txt

Full manual: https://genentech.github.io/exohub/docs/reference/cli/exo-standalone-ingest/
```
