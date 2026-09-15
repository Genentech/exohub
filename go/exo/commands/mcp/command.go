package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/spf13/cobra"
)

// toolEntry holds a tool definition and its handler in a single registry entry.
type toolEntry struct {
	tool    mcp.Tool
	handler server.ToolHandlerFunc
}

// toolRegistry returns the canonical list of MCP tools. Every command
// (start, list, show) reads from this slice so definitions stay in sync.
func toolRegistry() []toolEntry {
	entries := []toolEntry{
		{
			tool: mcp.NewTool("create_repo",
				mcp.WithDescription(
					"Create and initialize a git-annex data repository on the git host. "+
						"Provide the full git remote URL — the provider (GitLab, GitHub, Gitea) is auto-detected from the URL. "+
						"No manual configuration files are needed. "+
						"Requires authentication (run 'login' first). "+
						"After creating the repo, use 'init_remote' to add S3 or exospace remotes.",
				),
				mcp.WithString("repo_url", mcp.Required(), mcp.Description("Git remote URL for the repository to create (e.g. git@github.com:org/repo.git)")),
				mcp.WithString("directory", mcp.Description("Local directory to initialize (defaults to current working directory)")),
			),
			handler: handleCreateRepo,
		},
		{
			tool: mcp.NewTool("add",
				mcp.WithDescription(
					"Add files to tracking with automatic content-type detection. "+
						"In 'auto' mode (default), text files are added to git and binary/data files to git-annex based on mimetype. "+
						"Use 'annex' or 'git' mode to override detection. "+
						"Use this instead of 'git add' or 'git annex add' to ensure files are tracked by the correct backend. "+
						"Requires git-annex to be initialized in the repository. "+
						"Returns the list of files added and which backend (git or annex) each was assigned to.",
				),
				mcp.WithString("repo_dir", mcp.Required(), mcp.Description("Path to the git repository")),
				mcp.WithArray("paths", mcp.Required(), mcp.Description("File paths to add"), mcp.WithStringItems()),
				mcp.WithString("mode", mcp.Description("Add mode: auto (default, mimetype-based), annex (force git-annex), git (force git)"), mcp.Enum("auto", "annex", "git")),
			),
			handler: handleAdd,
		},
		{
			tool: mcp.NewTool("status",
				mcp.WithDescription(
					"Show working tree status including untracked, modified, and staged files. "+
						"Use this instead of 'git status' to get a structured view that includes git-annex tracked files. "+
						"Returns categorized file lists (untracked, modified, staged) for the repository.",
				),
				mcp.WithString("repo_dir", mcp.Required(), mcp.Description("Path to the git repository")),
				mcp.WithReadOnlyHintAnnotation(true),
			),
			handler: handleStatus,
		},
		{
			tool: mcp.NewTool("locate",
				mcp.WithDescription(
					"Show where git-annex managed content is physically stored across remotes. "+
						"Use this to find which remotes have copies of specific data files before syncing or to verify redundancy. "+
						"Remotes are defined in '.exohub/remotes' and can be listed with 'exo info'. "+
						"Requires git-annex to be initialized in the repository. "+
						"Returns a map of file paths to the list of remotes holding each file's content.",
				),
				mcp.WithString("repo_dir", mcp.Required(), mcp.Description("Path to the git repository")),
				mcp.WithArray("paths", mcp.Description("Specific file paths to locate (defaults to all annexed files)"), mcp.WithStringItems()),
				mcp.WithReadOnlyHintAnnotation(true),
			),
			handler: handleLocate,
		},
		{
			tool: mcp.NewTool("sync",
				mcp.WithDescription(
					"IMPORTANT: Before calling this tool, you MUST ask the user whether they want to monitor sync progress. "+
						"If yes: call 'heartbeat_record' first, then 'sync', then poll 'heartbeat_show' periodically "+
						"to show transfer status, and 'sync_status' to check completion. "+
						"If no: just call 'sync' and check 'sync_status' later. "+
						"Download and upload git-annex managed data files to/from remote storage. "+
						"Runs in the background and returns immediately. "+
						"Use this instead of 'git annex sync' or 'git annex get' to transfer actual file content between remotes. "+
						"Remotes are defined in '.exohub/remotes'; run 'exo info' to list configured remotes and their types. "+
						"Requires git-annex to be initialized in the repository (run 'exo init' first). "+
						"When called without 'with', syncs with all configured remotes.",
				),
				mcp.WithString("repo_dir", mcp.Required(), mcp.Description("Path to the git repository")),
				mcp.WithArray("with", mcp.Description("Remote names to sync with (defaults to all)"), mcp.WithStringItems()),
			),
			handler: handleSync,
		},
		{
			tool: mcp.NewTool("sync_status",
				mcp.WithDescription(
					"Check whether a background sync is still running for the given repository. "+
						"Returns 'running' or 'not_running'. "+
						"Use this after calling 'sync' to know when the transfer has completed.",
				),
				mcp.WithString("repo_dir", mcp.Required(), mcp.Description("Path to the git repository")),
				mcp.WithReadOnlyHintAnnotation(true),
			),
			handler: handleSyncStatus,
		},
		{
			tool: mcp.NewTool("publish",
				mcp.WithDescription(
					"Orchestrate the full publish workflow: sync content → bundle metadata → sync to catalog. "+
						"Step 1: Syncs data to all configured remotes EXCEPT artifactdb/catalog types. "+
						"Step 2: Runs exo bundle to generate metadata. "+
						"Step 3: Syncs to artifactdb/catalog remotes only. "+
						"Use 'sync' parameter to override step 1 and sync only to specified remotes. "+
						"If no artifactdb remote is configured, step 3 is skipped with a warning.",
				),
				mcp.WithString("repo_dir", mcp.Required(), mcp.Description("Path to the git repository")),
				mcp.WithArray("sync", mcp.Description("Remote names to sync in step 1 (overrides auto-discovery)"), mcp.WithStringItems()),
			),
			handler: handlePublish,
		},
		{
			tool: mcp.NewTool("broadcast",
				mcp.WithDescription(
					"Broadcast git-annex metadata and tracking information to remotes without transferring file content. "+
						"Use this instead of 'git annex sync --no-content' to push metadata changes (location tracking, "+
						"branch updates) while skipping large data transfers. "+
						"Remotes are defined in '.exohub/remotes'; run 'exo info' to list them. "+
						"Requires git-annex to be initialized in the repository. "+
						"Returns broadcast status per remote.",
				),
				mcp.WithString("repo_dir", mcp.Required(), mcp.Description("Path to the git repository")),
				mcp.WithArray("with", mcp.Description("Remote names to broadcast to (defaults to all)"), mcp.WithStringItems()),
			),
			handler: handleBroadcast,
		},
		{
			tool: mcp.NewTool("login",
				mcp.WithDescription(
					"Authenticate via OIDC device flow. "+
						"If valid credentials exist, returns immediately. "+
						"Otherwise, starts a device flow in the background and returns an auth URL "+
						"that you MUST show to the user so they can visit it in a browser. "+
						"Then poll 'login_status' to check when authentication completes. "+
						"Required before using tools that interact with remote services (create_repo, sync, broadcast, clone).",
				),
				mcp.WithBoolean("force", mcp.Description("Force re-authentication even if valid credentials exist")),
			),
			handler: handleLogin,
		},
		{
			tool: mcp.NewTool("login_status",
				mcp.WithDescription(
					"Check the status of an in-progress login or verify existing credentials. "+
						"Returns 'waiting' if device flow is still pending, 'ok' if authenticated, "+
						"'not_authenticated' if no credentials, or 'expired' if credentials have expired. "+
						"Call this after 'login' to know when the user has completed authentication.",
				),
				mcp.WithReadOnlyHintAnnotation(true),
			),
			handler: handleLoginStatus,
		},
		{
			tool: mcp.NewTool("heartbeat_record",
				mcp.WithDescription(
					"Start recording sync progress metrics in the background. "+
						"Launches 'exo heartbeat record --watch --path .' as a background process that "+
						"continuously captures annex sync metrics (file counts, storage progress, transfer rates) "+
						"every 'interval' seconds (default 2s). "+
						"Call this before or alongside a long-running sync to enable progress monitoring. "+
						"Use 'heartbeat_show' to read the captured metrics. "+
						"Returns immediately after starting the background recorder.",
				),
				mcp.WithString("repo_dir", mcp.Required(), mcp.Description("Path to the git repository")),
				mcp.WithString("interval", mcp.Description("Recording interval in seconds (default: 2)")),
				mcp.WithArray("paths", mcp.Description("Paths to track (defaults to [\".\"] for the entire repo)"), mcp.WithStringItems()),
			),
			handler: handleHeartbeatRecord,
		},
		{
			tool: mcp.NewTool("heartbeat_show",
				mcp.WithDescription(
					"Show the latest sync progress metrics captured by heartbeat_record. "+
						"Reads the most recent metrics snapshot from .git/exohub/metrics/latest.json. "+
						"Returns structured data including: per-path file counts and storage progress, "+
						"active downloads/uploads, network transfer rates, and completion percentages. "+
						"Requires heartbeat_record to be running (or to have run) to produce metrics.",
				),
				mcp.WithString("repo_dir", mcp.Required(), mcp.Description("Path to the git repository")),
				mcp.WithReadOnlyHintAnnotation(true),
			),
			handler: handleHeartbeatShow,
		},
		{
			tool: mcp.NewTool("init",
				mcp.WithDescription(
					"Initialize or reconfigure an existing git-annex repository. "+
						"Runs 'exo init --yes' to ensure git-annex is set up and all remotes "+
						"defined in '.exohub/remotes' are properly configured. "+
						"Call this after 'clone' to enable remotes, or any time remote configuration may be stale. "+
						"This is idempotent — safe to call multiple times.",
				),
				mcp.WithString("repo_dir", mcp.Required(), mcp.Description("Path to the git repository")),
			),
			handler: handleInit,
		},
		{
			tool: mcp.NewTool("init_remote",
				mcp.WithDescription(
					"Add a new remote to the repository. "+
						"Creates an S3 or local remote for storing git-annex data. "+
						"Remote types: annex (versioned S3 storage), export (S3 file tree), "+
						"artifactdb (ArtifactDB catalog / ExoHub Atlas), exospace (local/rsync shared cache). "+
						"The remote is saved to '.exohub/remotes' for reproducible setup across clones.",
				),
				mcp.WithString("repo_dir", mcp.Required(), mcp.Description("Path to the git repository")),
				mcp.WithString("name", mcp.Required(), mcp.Description("Remote name (e.g., s3-annex, s3-export)")),
				mcp.WithString("type", mcp.Required(), mcp.Description("Remote type"), mcp.Enum("annex", "export", "artifactdb", "exospace")),
				mcp.WithString("s3url", mcp.Description("S3 URL. Ask the user, suggest default: s3://exohub-sandbox-uat/sandbox/<org>/<repo>/<suffix> where suffix is _annex for annex, _export for export, _catalog for artifactdb")),
				mcp.WithString("rsyncurl", mcp.Description("Rsync URL or local path (required for exospace). Example: rsync://host/path")),
				mcp.WithString("tracking_branch", mcp.Description("Tracking branch for export remotes (defaults to current branch)")),
				mcp.WithBoolean("grants", mcp.Description("Enable fine-grained S3 Access Grants permissions (default: true). Ask the user.")),
			),
			handler: handleInitRemote,
		},
		{
			tool: mcp.NewTool("info",
				mcp.WithDescription(
					"Show repository information including configured remotes, annex state, and storage statistics. "+
						"Returns structured data about the repository's git-annex configuration. "+
						"Use this to discover which remotes are available before running 'sync' or 'broadcast'.",
				),
				mcp.WithString("repo_dir", mcp.Required(), mcp.Description("Path to the git repository")),
				mcp.WithReadOnlyHintAnnotation(true),
			),
			handler: handleInfo,
		},
	}
	return append(entries, catalogTools()...)
}

// NewCommand creates the exo mcp parent command with start, list, and show subcommands.
func NewCommand(version string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "MCP server for agent-driven data operations",
		Long: `Model Context Protocol (MCP) server exposing exo data operations as tools for AI agents.

Subcommands:
  start   Start the MCP server over stdio
  list    List available tools
  show    Show full details for a tool`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(newStartCommand(version))
	cmd.AddCommand(newListCommand())
	cmd.AddCommand(newShowCommand())

	return cmd
}

func newStartCommand(version string) *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the MCP server over stdio",
		Long: `Start a Model Context Protocol (MCP) server over stdio.

Exposes exo data operations as MCP tools for AI agents.
The server communicates via JSON-RPC over stdin/stdout.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServer(version)
		},
	}
}

const serverInstructions = `ExoHub is a git-annex based data repository management system. It extends git with large file support via git-annex, storing data on S3 or local remotes while tracking metadata in git.

Key concepts:
- Remotes: storage backends for data files. Types: annex (versioned S3), export (S3 file tree), artifactdb (ArtifactDB catalog / ExoHub Atlas), exospace (local/rsync).
- Grants: fine-grained S3 Access Grants for permissions (owners, viewers). Configured in .exohub/permissions.
- Sync: transfers file content between local repo and remotes. Broadcast syncs metadata only.

Creating or publishing a dataset:
1. login — authenticate
2. Ask the user: "Does the repository already exist on the git host?"
   - If NO: use create_repo to create it (requires a git token)
   - If YES: ask for the git URL, then git init + git remote add origin <url>
3. init — configure git-annex
4. Ask the user: "Which remote types do you want? (select all that apply)"
   Offer these choices (multiple can be selected): annex, export, artifactdb, exospace
   Then call init_remote for each selected type, one at a time (NOT in parallel — git-annex locks conflict).
5. add — stage files (auto-detects text→git vs binary→annex)
6. git commit + git push — push metadata
7. sync — upload data to remotes

Cloning an existing dataset:
1. login — authenticate
2. git clone <url> — clone the repository
3. init — configure git-annex and remotes from .exohub/remotes
4. sync — download data from remotes

Important:
- NEVER manually create or edit .exohub/ or .artifactdb/ files. These are managed automatically by exo tools.
- To publish to an artifactdb remote, run 'exo bundle' first to generate catalog metadata, then 'sync'.
- Syncing to artifactdb remotes returns a Job URL in the output — use it to check indexing status.
- artifactdb is a publish-only remote: 'locate' will not show it as a storage location for files.
- NEVER run 'git annex' commands directly — use the exo MCP tools which handle remote configuration, credentials, and error recovery.
- After cloning a repo, always call 'init' to configure remotes before syncing.
- Use 'info' to see configured remotes and repository state.
- Use 'add' instead of 'git add' to ensure correct backend routing.
- Use 'sync' instead of 'git annex sync' for proper remote handling.
- Monitor long syncs with 'heartbeat_record' + 'heartbeat_show'.

Searching the catalog:
- Use 'catalog_schemas' to discover available schema types (filtered to exohub-* by default).
- Use 'catalog_search' to find datasets and artifacts in the ExoHub catalog.
- Searches support dotfield notation for field-specific queries (e.g. _extra.project_id:"myproject", path:*.parquet).
- Combine schema, project, and free-text filters for precise results.
- The catalog URL is resolved from EXOHUB_CATALOG_URL env, exo context config, or the default.
- Use 'catalog_download' to download artifacts found via search. Accepts artifact IDs, project@version, bundle IDs, or commit IDs.
- Use 'catalog_semantic_search' for natural language queries (e.g. "datasets related to neurodegeneration"). Returns semantically similar chunks, entities, and relationships.
- Use 'catalog_entities' to retrieve entity details (genes, diseases, pathways, etc.) and their relationships from the catalog knowledge graph.`

func runServer(version string) error {
	s := server.NewMCPServer(
		"exo",
		version,
		server.WithToolCapabilities(true),
		server.WithInstructions(serverInstructions),
	)

	for _, entry := range toolRegistry() {
		s.AddTool(entry.tool, entry.handler)
	}

	err := server.ServeStdio(s)

	// Clean up any background processes (heartbeat recorders, syncs) on exit
	backgroundProcs.KillAll()

	return err
}

func newListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List available MCP tools",
		RunE: func(cmd *cobra.Command, args []string) error {
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "TOOL\tDESCRIPTION")
			for _, entry := range toolRegistry() {
				// Use first sentence as short description
				short := firstSentence(entry.tool.Description)
				fmt.Fprintf(w, "%s\t%s\n", entry.tool.Name, short)
			}
			return w.Flush()
		},
	}
}

func newShowCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "show <tool>",
		Short: "Show full details for a tool",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			for _, entry := range toolRegistry() {
				if entry.tool.Name == name {
					return printToolDetail(cmd, entry.tool)
				}
			}
			return fmt.Errorf("unknown tool: %s", name)
		},
	}
}

func printToolDetail(cmd *cobra.Command, t mcp.Tool) error {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Tool: %s\n\n", t.Name)
	fmt.Fprintf(out, "Description:\n  %s\n", t.Description)

	schema := t.InputSchema
	if len(schema.Properties) == 0 {
		return nil
	}

	fmt.Fprintf(out, "\nParameters:\n")

	requiredSet := make(map[string]bool, len(schema.Required))
	for _, r := range schema.Required {
		requiredSet[r] = true
	}

	for name, raw := range schema.Properties {
		propJSON, err := json.Marshal(raw)
		if err != nil {
			continue
		}
		var prop struct {
			Type        string   `json:"type"`
			Description string   `json:"description"`
			Enum        []string `json:"enum,omitempty"`
		}
		if err := json.Unmarshal(propJSON, &prop); err != nil {
			continue
		}

		req := "optional"
		if requiredSet[name] {
			req = "required"
		}

		fmt.Fprintf(out, "  %s (%s, %s)\n", name, prop.Type, req)
		if prop.Description != "" {
			fmt.Fprintf(out, "    %s\n", prop.Description)
		}
		if len(prop.Enum) > 0 {
			fmt.Fprintf(out, "    Allowed values: %s\n", strings.Join(prop.Enum, ", "))
		}
	}

	return nil
}

// firstSentence returns the text up to the first period followed by a space, or the whole string.
func firstSentence(s string) string {
	if idx := strings.Index(s, ". "); idx != -1 {
		return s[:idx+1]
	}
	return s
}
