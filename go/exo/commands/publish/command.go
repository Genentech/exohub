package publish

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	initcmd "github.com/Genentech/exohub/go/exo/commands/init"
	"github.com/Genentech/exohub/go/exo/commandutil"
)

var resolveCloneURL = commandutil.ResolveCloneURL

var (
	stepStyle = lipgloss.NewStyle().Foreground(commandutil.ColorDarkOrange).Bold(true)
	warnStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
)

var command = commandutil.Command

func logStep(step int, msg string) {
	fmt.Fprintf(os.Stderr, "%s %s\n", stepStyle.Render(fmt.Sprintf("[%d/4]", step)), msg)
}

func NewCommand() *cobra.Command {
	var syncRemotes []string
	var refs []string
	var force bool

	cmd := &cobra.Command{
		Use:   "publish",
		Short: "Orchestrate sync → bundle → sync (catalog) → broadcast",
		Long: `Publish data by orchestrating a four-step workflow:

  1. Sync content to all configured remotes EXCEPT artifactdb/catalog types
  2. Generate bundle metadata (exo bundle)
  3. Sync bundle metadata to artifactdb/catalog remotes only
  4. Broadcast metadata to all remotes (exo broadcast)

Use --sync to override step 1 and sync only to specified remotes.

Publishing an older ref (--ref):
  --ref <tag|commit> publishes that ref's content and metadata (bundle,
  .artifactdb/, provenance) while routing (remotes, s3url, role, url,
  data_from) is taken from the CURRENT .exohub/remotes (the checked-out
  working tree) — never from the ref's own, possibly stale, copy of that
  file. This is what makes it safe to publish a tag that predates a routing
  change (a bucket/prefix move, a new role: atlas url): it always lands on
  the current catalog instead of silently falling back to a default because
  the old tag's routing is incomplete. Repeatable: --ref 1 --ref 2 publishes
  each ref in turn with the same current routing. Without --ref, publish
  behaves exactly as before (HEAD's own checked-out content and routing,
  subject to the stale-routing refusal below).

  The bundle's content and keys always come from the ref itself, never the
  worktree: 'exo publish --ref 1' runs 'exo bundle --ref 1' under the hood,
  which resolves the ref's own file list and every annexed file's own key
  by reading the ref's git tree directly (never 'git ls-files'/'git annex
  find' against the checked-out working tree). A file added after the ref
  is absent from the published bundle; a file removed after the ref is
  present; a file modified after the ref publishes with the REF's content
  and the REF's annex key, even while HEAD differs.

  Per ref, before publishing, unless --force: 1) every catalog remote the
  ref's own .exohub/remotes references must still exist in the CURRENT
  config (matched by name, or by git-annex UUID when both record one) —
  otherwise publish refuses rather than silently drop it; 2) the ref's
  annexed content must already be present on the current annex remote(s) (a
  completeness check with --verify) — otherwise publish refuses with a hint
  ('exo sync --all', or 'exo init --allow-move --migrate-content' if a
  remote's storage moved). The bundle and .artifactdb/ (git-archived from the
  ref) are built into a temporary directory that is removed after
  publishing.

Source-repo guard (every publish, --ref or not):
  Each published version records the repository it was published from; a
  publish of the same project/version from a different repository is
  refused unless --force. --force bypasses both the ref-to-remotes check
  above and the source-repo guard, but never tag immutability (a moved tag
  still needs a new tag, with or without --force).

Stale-routing refusal (no --ref):
  Running plain 'exo publish' from a checkout (detached HEAD, or any branch
  other than the repository's default) whose own .exohub/remotes routing
  differs from the default branch's refuses, non-zero, rather than silently
  publishing to stale or default routing. Publish that content with
  'exo publish --ref <ref>' from the default branch instead.

Rewritten remote main:
  Steps 1 (sync) and 4 (broadcast) refuse, non-zero, if a content remote
  force-pushed (rewrote) its main branch since this clone last synced with
  it, instead of hitting a merge conflict. See 'exo sync --help' for the
  'exo sync --reset-synced <remote>' fix.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				_ = cmd.Help()
				os.Exit(1)
			}
			return runPublish(syncRemotes, refs, force)
		},
	}

	cmd.Flags().StringArrayVar(&syncRemotes, "sync", nil, "Remote names to sync in step 1 (repeatable, overrides auto-discovery)")
	cmd.Flags().StringArrayVar(&refs, "ref", nil, "Publish this ref's content+metadata with the current routing (repeatable, e.g. --ref 1 --ref 2)")
	cmd.Flags().BoolVar(&force, "force", false, "Bypass the ref-to-remotes check and the source-repo guard (with --ref, also skip the content-availability check); never bypasses tag immutability")

	return cmd
}

func runPublish(syncRemotes []string, refs []string, force bool) error {
	if _, err := os.Stat(".git"); os.IsNotExist(err) {
		return fmt.Errorf("not a git repository")
	}

	if _, err := resolveCloneURL(); err != nil {
		return fmt.Errorf("no usable git remote — the catalog entry would have no clone URL (%v).\n"+
			"Add one with: git remote add origin <url>   (then re-run exo publish)", err)
	}

	remotesConfig, err := initcmd.LoadRemotesConfig()
	if err != nil {
		return fmt.Errorf("failed to load .exohub/remotes: %w", err)
	}

	contentRemotes, catalogRemotes := classifyRemotes(remotesConfig)

	// Step 1: Sync to content remotes (non-artifactdb). Ref-agnostic: tagged
	// content was already uploaded when that tag was made, independent of
	// which ref's bundle metadata step 2/3 publish below.
	step1Remotes := syncRemotes
	if len(step1Remotes) == 0 {
		step1Remotes = contentRemotes
	}

	if len(step1Remotes) > 0 {
		logStep(1, fmt.Sprintf("Syncing content to %d remote(s)", len(step1Remotes)))
		if err := runExoSync(step1Remotes, force); err != nil {
			return fmt.Errorf("sync failed: %w", err)
		}
	} else {
		logStep(1, "No content remotes configured, skipping sync")
	}

	if len(refs) == 0 {
		// Step 2: Generate bundle for HEAD
		logStep(2, "Generating bundle metadata")
		if err := runExoBundle(); err != nil {
			return fmt.Errorf("bundle failed: %w", err)
		}

		// Step 3: Sync to artifactdb/catalog remotes
		if len(catalogRemotes) > 0 {
			logStep(3, fmt.Sprintf("Syncing to %d catalog remote(s)", len(catalogRemotes)))
			if err := runExoSync(catalogRemotes, force); err != nil {
				return fmt.Errorf("catalog sync failed: %w", err)
			}
		} else {
			fmt.Fprintf(os.Stderr, "%s No artifactdb/catalog remotes configured, skipping step 3\n", warnStyle.Render("⚠"))
		}
	} else {
		// Step 2+3 per ref: content+metadata come from the ref, routing from
		// the current .exohub/remotes (loaded above, from whatever is
		// checked out right now) — never from the ref's own, possibly stale,
		// copy of that file.
		for i, ref := range refs {
			if err := publishRef(ref, i+1, len(refs), catalogRemotes, remotesConfig, force); err != nil {
				return err
			}
		}
	}

	// Step 4: Broadcast metadata to all remotes
	logStep(4, "Broadcasting metadata")
	if err := runExoBroadcast(); err != nil {
		return fmt.Errorf("broadcast failed: %w", err)
	}

	fmt.Fprintf(os.Stderr, "\n%s Publish complete\n", stepStyle.Render("✓"))
	return nil
}

// publishRef runs steps 2+3 for one --ref: validate its catalog remotes map
// onto the current config, verify its annexed content is present on the
// current remote(s), build its bundle into one temporary directory and the
// ref's .artifactdb/ into a SEPARATE sibling temporary directory, publish
// those to the current catalog remotes, then remove both temporary
// directories. The bundle dir is never allowed to carry .artifactdb/: it is
// uploaded whole to a role: atlas remote's .exohub-bundle/, and the Atlas
// rejects instance-typed documents it finds there (exo-cli#177). The
// sibling .artifactdb/ dir is handed to 'exo sync' explicitly via
// --artifactdb-dir, which buildInstancePublishArgs (sync package) consumes
// in priority over the bundleDir/.artifactdb and HEAD-archive fallbacks.
// force skips both the ref-to-remotes mapping check and the
// content-availability check (it never skips tag immutability or, by
// itself, the source-repo guard enforced downstream by the catalog binary —
// force is passed through so that guard can be bypassed there too).
func publishRef(ref string, index, total int, catalogRemotes []string, remotesConfig *initcmd.RemotesConfig, force bool) error {
	if !force {
		if err := validateRefRemoteMapping(ref, remotesConfig); err != nil {
			return err
		}
	}

	if !force {
		if err := verifyRefContentAvailable(ref); err != nil {
			return err
		}
	}

	tempDir, err := os.MkdirTemp("", "exo-publish-ref-*")
	if err != nil {
		return fmt.Errorf("creating temp bundle dir for ref %q: %w", ref, err)
	}
	defer os.RemoveAll(tempDir)

	artifactdbTempDir, err := os.MkdirTemp("", "exo-publish-ref-artifactdb-*")
	if err != nil {
		return fmt.Errorf("creating temp artifactdb dir for ref %q: %w", ref, err)
	}
	defer os.RemoveAll(artifactdbTempDir)

	logStep(2, fmt.Sprintf("Generating bundle metadata for ref %q (%d/%d)", ref, index, total))
	if err := runExoBundleRef(ref, tempDir); err != nil {
		return fmt.Errorf("bundle failed for ref %q: %w", ref, err)
	}

	artifactdbDir, err := extractArtifactDBFromRef(ref, artifactdbTempDir)
	if err != nil {
		return fmt.Errorf("extracting .artifactdb/ from ref %q: %w", ref, err)
	}

	if len(catalogRemotes) > 0 {
		logStep(3, fmt.Sprintf("Syncing ref %q to %d catalog remote(s)", ref, len(catalogRemotes)))
		if err := runExoSyncBundleDir(catalogRemotes, tempDir, artifactdbDir, force); err != nil {
			return fmt.Errorf("catalog sync failed for ref %q: %w", ref, err)
		}
	} else {
		fmt.Fprintf(os.Stderr, "%s No artifactdb/catalog remotes configured, skipping step 3 for ref %q\n", warnStyle.Render("⚠"), ref)
	}
	return nil
}

// validateRefRemoteMapping refuses loudly when ref's own .exohub/remotes
// references a catalog remote that the CURRENT config lacks (matched by
// name, or by git-annex UUID when both record one, via
// initcmd.FindRemoteConfig): silently publishing to the current catalog
// remotes while ignoring one the ref expected would be a worse surprise than
// refusing (exo-cli#176). The ref's own file is read leniently (exo-cli#196):
// a tag made before the role-split migration necessarily carries legacy
// atlas_url/instance_url keys, and routing comes from the CURRENT
// .exohub/remotes regardless, so the ref's file is never validated or
// refused on those grounds here — only its remotes' Name/Type/UUID matter.
func validateRefRemoteMapping(ref string, currentConfig *initcmd.RemotesConfig) error {
	refConfig, err := initcmd.LoadRemotesConfigFromRefLenient(ref)
	if err != nil {
		return fmt.Errorf("loading ref %q's .exohub/remotes: %w", ref, err)
	}
	for _, r := range refConfig.Remotes {
		if isStandardRemoteType(r.Type) {
			continue // only catalog remotes need a current-routing home
		}
		if initcmd.FindRemoteConfig(r, currentConfig) == nil {
			return fmt.Errorf(
				"refusing to publish ref %q: it references catalog remote %q (type %s), which is not configured in the current .exohub/remotes (matched by name or UUID) — add it to the current routing before publishing this ref",
				ref, r.Name, r.Type)
		}
	}
	return nil
}

// verifyRefContentAvailable runs 'exo sync --check --ref <ref> --verify' and
// refuses the publish when it finds fixable or lost content (exit codes 3
// and 4, see the sync reference docs): an old ref's annexed content may not
// be on the current annex remote(s) if a remote's storage moved without a
// UUID change (exo-cli#166/#168). --force skips this check entirely.
func verifyRefContentAvailable(ref string) error {
	exePath, err := exec.LookPath("exo")
	if err != nil {
		return fmt.Errorf("exo not found in PATH: %w", err)
	}
	cmd := command(exePath, "sync", "--check", "--ref", ref, "--verify")
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	cmd.Stdin = os.Stdin
	runErr := cmd.Run()
	if cmd.ProcessState == nil {
		return fmt.Errorf("content-availability check failed to run for ref %q: %w", ref, runErr)
	}
	switch cmd.ProcessState.ExitCode() {
	case 0:
		return nil
	case 3, 4:
		return fmt.Errorf(
			"refusing to publish ref %q: its annexed content is not fully present on the current remote(s)\n%s\n"+
				"Fix with 'exo sync --all' (re-upload fixable content) or 'exo init --allow-move --migrate-content' (if a remote's storage moved), or pass --force to publish anyway",
			ref, strings.TrimSpace(buf.String()))
	default:
		return fmt.Errorf("content-availability check failed for ref %q:\n%s", ref, strings.TrimSpace(buf.String()))
	}
}

// extractArtifactDBFromRef git-archives .artifactdb/ from ref into destDir
// (a directory dedicated to .artifactdb/, never the bundle dir — see
// publishRef), so the instance payload published to a role: instance remote
// comes from the ref's own tree rather than whatever is checked out in the
// working tree. Returns the path to the extracted .artifactdb/ directory, or
// "" when the ref has no .artifactdb/ at all — not an error, there is simply
// nothing to extract, and a notice is printed so a role: instance remote's
// resulting skip (see runCatalogPublish in the sync package) isn't a silent
// no-op.
func extractArtifactDBFromRef(ref, destDir string) (string, error) {
	lsCmd := command("git", "ls-tree", "--name-only", ref, "--", ".artifactdb")
	out, err := lsCmd.Output()
	if err != nil {
		return "", fmt.Errorf("checking ref %q for .artifactdb/: %w", ref, err)
	}
	if strings.TrimSpace(string(out)) == "" {
		fmt.Fprintf(os.Stderr, "ℹ️  No .artifactdb/ for ref %q; any role: instance remote will be skipped for it\n", ref)
		return "", nil
	}

	archiveCmd := command("git", "archive", ref, "--", ".artifactdb")
	tarCmd := exec.Command("tar", "-x", "-C", destDir)

	pipeReader, pipeWriter := io.Pipe()
	archiveCmd.Stdout = pipeWriter
	archiveCmd.Stderr = os.Stderr
	tarCmd.Stdin = pipeReader
	tarCmd.Stderr = os.Stderr

	if err := archiveCmd.Start(); err != nil {
		return "", fmt.Errorf("git archive %s -- .artifactdb: %w", ref, err)
	}
	if err := tarCmd.Start(); err != nil {
		return "", fmt.Errorf("tar -x: %w", err)
	}

	archiveErr := archiveCmd.Wait()
	pipeWriter.Close()
	tarErr := tarCmd.Wait()

	if archiveErr != nil {
		return "", fmt.Errorf("git archive %s -- .artifactdb: %w", ref, archiveErr)
	}
	if tarErr != nil {
		return "", fmt.Errorf("tar -x: %w", tarErr)
	}
	return filepath.Join(destDir, ".artifactdb"), nil
}

// classifyRemotes separates remotes into content (standard) and catalog (non-standard/artifactdb) types.
func classifyRemotes(config *initcmd.RemotesConfig) (content []string, catalog []string) {
	if config == nil {
		return nil, nil
	}
	for _, r := range config.Remotes {
		if isStandardRemoteType(r.Type) {
			content = append(content, r.Name)
		} else {
			catalog = append(catalog, r.Name)
		}
	}
	return content, catalog
}

var standardRemoteTypes = map[string]bool{
	"annex": true, "export": true, "import": true, "exospace": true, "drive": true,
}

func isStandardRemoteType(t string) bool {
	return standardRemoteTypes[t]
}

func runExoSync(remotes []string, force bool) error {
	exePath, err := exec.LookPath("exo")
	if err != nil {
		return fmt.Errorf("exo not found in PATH: %w", err)
	}
	args := []string{"sync"}
	for _, r := range remotes {
		args = append(args, "--with", r)
	}
	if force {
		args = append(args, "--force")
	}
	cmd := command(exePath, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func runExoBundle() error {
	exePath, err := exec.LookPath("exo")
	if err != nil {
		return fmt.Errorf("exo not found in PATH: %w", err)
	}
	cmd := command(exePath, "bundle", "--yes")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func runExoBundleRef(ref, outputDir string) error {
	exePath, err := exec.LookPath("exo")
	if err != nil {
		return fmt.Errorf("exo not found in PATH: %w", err)
	}
	cmd := command(exePath, "bundle", "--ref", ref, "--output", outputDir, "--yes")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func runExoSyncBundleDir(remotes []string, bundleDir string, artifactdbDir string, force bool) error {
	exePath, err := exec.LookPath("exo")
	if err != nil {
		return fmt.Errorf("exo not found in PATH: %w", err)
	}
	args := []string{"sync", "--bundle-dir", bundleDir}
	// The ref's .artifactdb/ lives in a dir separate from bundleDir (see
	// publishRef); hand it to 'exo sync' explicitly rather than relying on it
	// being nested inside --bundle-dir (exo-cli#177). Omitted when the ref had
	// no .artifactdb/ to extract.
	if artifactdbDir != "" {
		args = append(args, "--artifactdb-dir", artifactdbDir)
	}
	for _, r := range remotes {
		args = append(args, "--with", r)
	}
	if force {
		args = append(args, "--force")
	}
	cmd := command(exePath, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func runExoBroadcast() error {
	exePath, err := exec.LookPath("exo")
	if err != nil {
		return fmt.Errorf("exo not found in PATH: %w", err)
	}
	cmd := command(exePath, "broadcast")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}
