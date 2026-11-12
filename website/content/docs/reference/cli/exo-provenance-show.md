---
title: "exo provenance show"
description: "Show an artifact's provenance lineage"
---

Full `exo provenance show --help` output, generated from exo-cli v0.140.2.

```text
Resolve <path> to its content identifier at a ref (default HEAD) and show
its upstream lineage: generating activities, used and derived-from
entities, plans, agents (including actedOnBehalfOf), timestamps and the
record that asserted each statement. Every repository entity is
shown with its path at the ref, not just its identifier.

A path is covered by its own provenance only when it is the subject of a
generation, derivation, attribution or revision statement; a
path that is only used as an input falls back the same way an uncovered
path does, folded into a "used as an input by" list.

When the path has no provenance of its own, or lies under a declared
provenance.collections folder, show falls back to the nearest enclosing
folder collection with provenance and says so. Failing that, it falls back
to a covered named collection (.exohub/collections/<name>.json) the path is
a member of; when more than one applies, the human-readable output shows
the first by name and states the others, while --json lists every
applicable collection.

The walk follows --depth upstream hops (default unlimited) and detects
cycles. A separate "Commit history" section shows the path's commits as
context, capped at --max-commits (default 10, 0 = unlimited); it is not a
provenance claim. --json emits the lineage subgraph as JSON-LD, with
exo:path set to each entity's path at the ref.

Usage:
  exo provenance show <path> [flags]


Flags:
      --depth int         maximum number of upstream hops to walk (0 = unlimited)
  -h, --help              help for show
      --json              emit the lineage subgraph as JSON-LD
      --max-commits int   cap the commit history section to this many most recent commits (0 = unlimited) (default 10)
      --ref string        show lineage as of this git ref (default "HEAD")

Global Flags:
      --config-dir string   Root directory for all config, cache and credentials (overrides EXO_CONFIG_DIR)
      --context string      Use specific context (overrides selected context)

Docs for agents (llms.txt): https://genentech.github.io/exohub/llms.txt

Full manual: https://genentech.github.io/exohub/docs/reference/cli/exo-provenance-show/
```
