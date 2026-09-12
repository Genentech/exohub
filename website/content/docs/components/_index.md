---
title: "ExoHub Components"
description: "Overview of the ExoHub distributed data management architecture components"
---

## ExoHub Component Architecture

ExoHub is implemented as a collection of cohesive components that work together to provide a unified
interface for managing distributed data. Each component serves a distinct role in abstracting
complexity and automating sophisticated data management workflows.

The architecture enables the creation of logical projects that serve as single, version-controlled
entry points to data physically scattered across hybrid storage locations, including AWS S3 buckets,
Google Drive, on-premises HPC filesystems, and other storage backends.




<!-- OSS variant: text-based component list (image contains internal-only components). -->
<!-- Replace with an OSS-specific image (exohub-components-oss.png) once available. -->




## Core Components

### {{< logo name="exocli" size="24x24" alt="ExoCLI" class="mr-2" >}}<a href="{{< baseurl >}}/docs/components/exocli/" class="component-link">ExoCLI</a>: The Interface
The primary entry point for developers, data scientists, and automated agents. ExoCLI abstracts git-annex complexity through simple, high-level commands and declarative YAML manifests for repeatable data operations.



### {{< logo name="exospace" size="24x24" alt="ExoSpace" class="mr-2" >}}<a href="{{< baseurl >}}/docs/components/exospace/" class="component-link">ExoSpace</a>: The Cache
A git-annex remote that acts as a geo-localized caching layer, situated locally to compute environments to prevent excessive data transfer and accelerate access through preemptive caching.

### {{< logo name="exosafe" size="24x24" alt="ExoSafe" class="mr-2" >}}<a href="{{< baseurl >}}/docs/components/exosafe/" class="component-link">ExoSafe</a>: The Vault
A cloud-backed credential store that securely provisions SSH keys and git tokens into ephemeral or agentic environments. ExoSafe stores typed credentials in AWS Parameter Store and automatically delivers them during `exo login`, enabling seamless git operations without manual key management.

### {{< logo name="atlas" size="24x24" alt="ExoAtlas" class="mr-2" >}}<a href="{{< baseurl >}}/docs/components/atlas/" class="component-link">ExoAtlas</a>: The Explorer
The discovery and access layer for published datasets and artifacts. ExoAtlas lets users search, browse, inspect, and download content from the ExoHub catalog through either the interactive `exo atlas` TUI or a browser-based web interface, with both modes sharing the same underlying experience.

### <a href="{{< baseurl >}}/docs/components/dataservice/" class="component-link">Data Service</a>: The Agent Gateway
An agent-facing HTTP service that gives AI agents and automated pipelines a simple, secure interface for consuming and producing ExoHub datasets. Agents connect via a central MCP endpoint (`/mcp/{deployment_id}`) and use three tools — `request_dataset`, `save_dataset`, and `get_operation` — without needing direct git or git-annex access.

### Extensible Remotes
The architecture supports custom git-annex special remotes for specialized storage backends, demonstrated by git-annex-remote-s5cmd for high-performance S3 transfers.
