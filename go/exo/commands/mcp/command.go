package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/spf13/cobra"

	"github.com/Genentech/exohub/go/exo/internal/defaults"
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
			tool: mcp.NewTool("profiles",
				mcp.WithDescription(
					"List available ExoHub setup profiles, or show one profile's prompts. "+
						"Call without 'name' to list profiles (name, description) so you can offer the user a choice. "+
						"Call with 'name' to get that profile's prompts (key, type, choices, default) so you can ask the "+
						"user every question yourself — never assume a default, especially for a 'read_access' prompt: it "+
						"controls who can see the published catalog entries (e.g. 'public' vs 'authenticated'), and some "+
						"profiles default to 'public'. Pass the answers to 'init' as 'profile' + 'answers'. Read-only.",
				),
				mcp.WithString("name", mcp.Description("Profile name to show prompts for (omit to list all available profiles)")),
				mcp.WithReadOnlyHintAnnotation(true),
			),
			handler: handleProfiles,
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
						"Required before using tools that interact with remote services (init, sync, broadcast, get, publish).",
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
			tool: mcp.NewTool("whoami",
				mcp.WithDescription(
					"Show who you're logged in as: identity, session expiry, organisation claims, "+
						"and the AWS profile/credentials exo will use. "+
						"Returns an error if not logged in (call 'login' first). "+
						"Never returns tokens or AWS secrets.",
				),
				mcp.WithBoolean("aws", mcp.Description("Also perform a live sts:GetCallerIdentity call (network call)")),
				mcp.WithReadOnlyHintAnnotation(true),
			),
			handler: handleWhoami,
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
					"Initialize or reconfigure git-annex in an existing git repository, optionally applying a server-authored "+
						"profile. The repository must already exist locally (clone it with 'git clone <url>', or 'git init' + "+
						"'git remote add origin <url>' for a brand new repository) — this tool does not create the git "+
						"repository itself. Without 'profile', runs 'exo init --yes' to ensure git-annex is set up and all "+
						"remotes defined in '.exohub/remotes' are properly configured — idempotent, safe to call multiple times. "+
						"With 'profile', also fetches and applies that server-authored profile: use the 'profiles' tool first "+
						"to discover available profiles and their prompts, ask the user about every prompt (never assume a value, "+
						"especially 'read_access' — it controls who can see the published catalog entries, e.g. public vs "+
						"authenticated, and some profiles default to public), then pass the answers here as 'answers' "+
						"(key -> value, matching the profile's prompt keys and types).",
				),
				mcp.WithString("repo_dir", mcp.Required(), mcp.Description("Path to the git repository; must already exist (see 'git clone' / 'git init' first)")),
				mcp.WithString("profile", mcp.Description("Name of a server-authored profile to apply (see the 'profiles' tool)")),
				mcp.WithString("name", mcp.Description("Explicit repository name (passed through as --name)")),
				mcp.WithObject("answers", mcp.Description("Answers to the chosen profile's prompts, keyed by prompt key; unanswered prompts fall back to their default")),
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
			tool: mcp.NewTool("get",
				mcp.WithDescription(
					"Download git-annex managed content for specific paths into the local repository (wraps 'exo get'). "+
						"Use this for a targeted download instead of 'sync', which transfers content for the whole repository. "+
						"Requires git-annex to be initialized in the repository (run 'init' first).",
				),
				mcp.WithString("repo_dir", mcp.Required(), mcp.Description("Path to the git repository")),
				mcp.WithArray("paths", mcp.Description("Paths to get (defaults to the current directory, i.e. everything)"), mcp.WithStringItems()),
				mcp.WithString("from", mcp.Description("Source remote to get content from (defaults to any remote with the content)")),
				mcp.WithBoolean("all", mcp.Description("Get all versions of the content, not just the current one")),
			),
			handler: handleGet,
		},
		{
			tool: mcp.NewTool("doctor",
				mcp.WithDescription(
					"Run ExoHub client diagnostics (wraps 'exo doctor'): environment, auth, network, repo config, "+
						"grants/S3 access, and provenance checks. Each check reports pass, warn, fail, or skip. "+
						"Use this to diagnose a failing sync, login, or permission issue before asking the user to "+
						"investigate manually.",
				),
				mcp.WithString("repo_dir", mcp.Description("Path to an ExoHub repo to scope repo-specific checks (default: current directory)")),
				mcp.WithString("remote", mcp.Description("Only run per-remote checks for this remote name")),
				mcp.WithBoolean("write_test", mcp.Description("Also perform an opt-in S3 write canary (put + delete) under a caller-owned prefix")),
				mcp.WithReadOnlyHintAnnotation(true),
			),
			handler: handleDoctor,
		},
		{
			tool: mcp.NewTool("provenance_add",
				mcp.WithDescription(
					"Record PROV-O provenance for files in this repository (wraps 'exo provenance add'): what was generated, "+
						"what it was derived from or used, and who/what asserted it. Call this after generating or transforming "+
						"files and before committing, so the commit carries both the data and its provenance. "+
						"Unless 'agent' is given explicitly, the record is attributed to this calling tool as a software agent, "+
						"acting on behalf of the logged-in user (exo resolves the on-behalf-of person automatically). "+
						"Choose 'assertion' based on how you know the lineage: use 'recorded' when you (this agent) yourself "+
						"just ran the step that produced 'generated' — you observed the activity directly; use 'asserted' "+
						"(the default) plus 'evidence' when you are inferring lineage after the fact (e.g. from file names, "+
						"timestamps, or a README) rather than having executed the step; use 'imported' when the lineage was "+
						"taken from another system's records rather than observed or inferred here.",
				),
				mcp.WithString("repo_dir", mcp.Required(), mcp.Description("Path to the git repository")),
				mcp.WithArray("generated", mcp.Required(), mcp.Description("Paths that were generated/created by this activity"), mcp.WithStringItems()),
				mcp.WithArray("used", mcp.Description("Paths that were read as inputs without being transformed into 'generated'"), mcp.WithStringItems()),
				mcp.WithArray("derived_from", mcp.Description("Paths that 'generated' was derived/transformed from"), mcp.WithStringItems()),
				mcp.WithBoolean("no_derived_from", mcp.Description("Explicitly assert 'generated' has no inputs (e.g. hand-authored files)")),
				mcp.WithArray("revision_of", mcp.Description("Paths/ids that 'generated' is a revision of"), mcp.WithStringItems()),
				mcp.WithString("plan", mcp.Description("Path to a plan/script/notebook document describing the activity")),
				mcp.WithString("agent", mcp.Description("person:<unix id> or software:<name> asserting this record; defaults to software:<this MCP client>")),
				mcp.WithString("on_behalf_of", mcp.Description("person:<unix id> the software agent acted on behalf of; defaults to the logged-in user when agent is a software agent")),
				mcp.WithString("model", mcp.Description("Model identifier for a software agent (requires agent software:<name>)")),
				mcp.WithString("started", mcp.Description("Activity start time (RFC3339); defaults to now")),
				mcp.WithString("ended", mcp.Description("Activity end time (RFC3339); defaults to now")),
				mcp.WithString("assertion", mcp.Description("Assertion kind (default: asserted). 'recorded': you directly executed/observed the activity that produced 'generated'. "+
					"'asserted': you are inferring lineage after the fact (pair with 'evidence'). 'imported': lineage taken from another system's records."), mcp.Enum("recorded", "imported", "asserted")),
				mcp.WithString("evidence", mcp.Description("Free-text justification for how you inferred the lineage; required context for an 'asserted' record")),
				mcp.WithArray("supersedes", mcp.Description("Record ids this record supersedes"), mcp.WithStringItems()),
				mcp.WithBoolean("skip_existing", mcp.Description("Skip 'generated' paths that already have provenance instead of erroring")),
				mcp.WithBoolean("dry_run", mcp.Description("Print the record that would be written without writing it")),
			),
			handler: handleProvenanceAdd,
		},
		{
			tool: mcp.NewTool("provenance_show",
				mcp.WithDescription(
					"Show the recorded lineage for a path from the git repository's provenance records (wraps "+
						"'exo provenance show'): what generated it, what it was derived from, and the agents involved. "+
						"Use this to answer \"where did this file come from\" before reusing or publishing it. "+
						"Complements 'catalog_lineage', which reads lineage already published to the catalog.",
				),
				mcp.WithString("repo_dir", mcp.Required(), mcp.Description("Path to the git repository")),
				mcp.WithString("path", mcp.Required(), mcp.Description("Path to show lineage for")),
				mcp.WithString("ref", mcp.Description("Show lineage as of this git ref (default: HEAD)")),
				mcp.WithNumber("depth", mcp.Description("Maximum number of upstream hops to walk (default: unlimited)")),
				mcp.WithReadOnlyHintAnnotation(true),
			),
			handler: handleProvenanceShow,
		},
		{
			tool: mcp.NewTool("provenance_validate",
				mcp.WithDescription(
					"Validate recorded provenance for consistency (wraps 'exo provenance validate'): schema conformance, "+
						"dangling references, and coverage. Returns a report of errors (exit-affecting) and warnings per record. "+
						"Run this before committing or publishing to catch provenance problems early.",
				),
				mcp.WithString("repo_dir", mcp.Required(), mcp.Description("Path to the git repository")),
				mcp.WithBoolean("staged", mcp.Description("Validate the staged (index) state instead of the worktree")),
				mcp.WithString("ref", mcp.Description("Validate records as of this git ref instead of the worktree (mutually exclusive with staged)")),
				mcp.WithReadOnlyHintAnnotation(true),
			),
			handler: handleProvenanceValidate,
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
		{
			tool: mcp.NewTool("docs",
				mcp.WithDescription(
					"Fetch exo usage documentation for agents. "+
						"With no arguments, returns the llms.txt index of available doc pages. "+
						"Pass 'page' with a link from that index (or a path under the docs base) to fetch that page's content. "+
						"If the docs site is unreachable, returns the attempted URL and the error instead of failing.",
				),
				mcp.WithString("page", mcp.Description("A page link from the llms.txt index, or a path under the docs base")),
				mcp.WithReadOnlyHintAnnotation(true),
			),
			handler: handleDocs,
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

func serverInstructions() string {
	instructions := baseServerInstructions
	if docsURL := defaults.DocsURL(); docsURL != "" {
		instructions += "\n\nFor exo usage docs, use the 'docs' tool or fetch " + docsURL + " directly."
	}
	return instructions
}

const baseServerInstructions = `ExoHub is a git-annex based data repository management system. It extends git with large file support via git-annex, storing data on S3 or local remotes while tracking metadata in git.

Key concepts:
- Remotes: storage backends for data files. Types: annex (versioned S3), export (S3 file tree), artifactdb (ArtifactDB catalog / ExoHub Atlas), exospace (local/rsync).
- Grants: fine-grained S3 Access Grants for permissions (owners, viewers). Configured in .exohub/permissions.
- Sync: transfers file content between local repo and remotes. Broadcast syncs metadata only.

Creating or publishing a dataset:
1. login — authenticate
2. Get a local git repository pointed at 'origin', either way:
   - If the repository already exists on the git host: ask the user for its git URL, then prefer
     'git clone <url>' (recommended). Use 'git init' + 'git remote add origin <url>' instead
     only if you need to work in an existing non-empty local directory.
   - If it does not exist yet: ask the user to create it on the git host themselves (exo no longer does this for
     you) and give you the URL, then 'git clone <url>' (or 'git init' + 'git remote add origin <url>').
   'init' requires an existing git repository, and pushing metadata later requires 'origin' — so this step comes
   first either way, profile or no profile.
3. profiles — list available server-authored profiles (if any fit, this is usually the fastest path: it asks the
   right questions and sets up remotes and permissions for you). Call profiles with 'name' to see one profile's
   prompts, then ask the user about EVERY prompt yourself — never assume a value, especially 'read_access': it
   controls who can see the published catalog entries (e.g. 'public' vs 'authenticated'), and some profiles
   default to 'public'. Then proceed to step 4 with 'profile' + 'answers' set.
4. init — configure git-annex (pass 'profile' + 'answers' here if using a profile; it applies the profile, then
   configures git-annex and any remotes the profile defined)
5. If not using a profile: ask the user "Which remote types do you want? (select all that apply)"
   Offer these choices (multiple can be selected): annex, export, artifactdb, exospace
   Then call init_remote for each selected type, one at a time (NOT in parallel — git-annex locks conflict).
6. add — stage files (auto-detects text→git vs binary→annex)
7. provenance_add — record what was generated/derived, if the files were produced by a pipeline, script, or this
   agent itself (skip for hand-authored files with no inputs worth recording)
8. git commit + git push — push metadata
9. publish if an artifactdb remote is configured (it syncs content to all remotes first, then bundles metadata and
   syncs to the catalog), otherwise sync — upload data to the configured remotes

Cloning an existing dataset:
1. login — authenticate
2. git clone <url> — clone the repository
3. init — configure git-annex and remotes from .exohub/remotes
4. get or sync — download data from remotes (get for specific paths, sync for everything)

Recording and checking provenance:
- Use 'provenance_add' after generating or transforming files, before committing, to record what was generated,
  what it was derived from or used, and who/what is responsible. Unless you pass 'agent' explicitly, the record is
  attributed to you (this MCP client) as a software agent, acting on behalf of the logged-in user. Pick
  'assertion' honestly: 'recorded' when you directly ran the step that produced the files, 'asserted' + 'evidence'
  when you are inferring lineage after the fact, 'imported' when the lineage comes from another system.
- Use 'provenance_show' to see a path's recorded lineage from the repository directly — what produced it and its
  upstream inputs, walking back through recorded activities.
- Use 'provenance_validate' before committing or publishing to catch schema errors, dangling references, or
  missing coverage in recorded provenance.
- Use 'catalog_lineage' to read lineage already published to the catalog for an artifact, plus its direct
  consumers (other catalog artifacts that used it as an input) — this is the cross-repository view;
  'provenance_show' is the local, pre-publish view of the same data.

Important:
- NEVER manually create or edit .exohub/ or .artifactdb/ files. These are managed automatically by exo tools.
- To publish to an artifactdb remote, use 'publish' (there is no separate bundle tool — it syncs content, bundles
  catalog metadata, and syncs the bundle to the catalog in one step).
- Syncing to artifactdb remotes returns a Job URL in the output — use it to check indexing status.
- artifactdb is a publish-only remote: 'locate' will not show it as a storage location for files.
- NEVER run 'git annex' commands directly — use the exo MCP tools which handle remote configuration, credentials, and error recovery.
- After cloning a repo, always call 'init' to configure remotes before syncing.
- Use 'info' to see configured remotes and repository state.
- Use 'add' instead of 'git add' to ensure correct backend routing.
- Use 'get' for a targeted download and 'sync' instead of 'git annex sync'/'git annex get' for whole-repo transfers.
- Monitor long syncs with 'heartbeat_record' + 'heartbeat_show'.
- If something isn't working — a failing login, sync, or permission error — run 'doctor' before asking the user
  to investigate manually; it checks environment, auth, network, repo config, and grants/S3 access.
- Use 'whoami' to check who you're authenticated as without making any changes.

Searching the catalog:
- Use 'catalog_schemas' to discover available schema types (filtered to exohub-* by default), including
  exohub-provenance/v1 (flattened provenance.json) alongside exohub-artifact/v1 and friends.
- Use 'catalog_search' to find datasets and artifacts in the ExoHub catalog.
- Searches support dotfield notation for field-specific queries (e.g. _extra.project_id:"myproject",
  path:*.parquet, lineage.inputs.id:"<content id>" to find what consumed a given artifact).
- Combine schema, project, and free-text filters for precise results.
- The catalog URL is resolved from EXOHUB_CATALOG_URL env, exo context config, or the default.
- Use 'catalog_download' to download artifacts found via search. Accepts artifact IDs, project@version, bundle IDs, or commit IDs.
- Use 'catalog_semantic_search' for natural language queries (e.g. "datasets related to neurodegeneration"). Returns semantically similar chunks, entities, and relationships.
- Use 'catalog_entities' to retrieve entity details (genes, diseases, pathways, etc.) and their relationships from the catalog knowledge graph.
- Use 'catalog_lineage' to look up a specific artifact's recorded lineage and direct consumers once you have its id.`

func runServer(version string) error {
	s := server.NewMCPServer(
		"exo",
		version,
		server.WithToolCapabilities(true),
		server.WithInstructions(serverInstructions()),
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
