package broadcast

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"

	"github.com/Genentech/exohub/go/exo/commandutil"
	"github.com/Genentech/exohub/go/exo/palette"
)

var command = commandutil.Command

type ui struct {
	enabled      bool
	infoStyle    lipgloss.Style
	warnStyle    lipgloss.Style
	successStyle lipgloss.Style
}

func newUI() *ui {
	enabled := term.IsTerminal(int(os.Stdout.Fd()))
	p := palette.Current()
	return &ui{
		enabled:      enabled,
		infoStyle:    lipgloss.NewStyle().Foreground(p.Dim.Adaptive()),
		warnStyle:    lipgloss.NewStyle().Foreground(p.Warning.Adaptive()),
		successStyle: lipgloss.NewStyle().Foreground(p.Success.Adaptive()),
	}
}

func (u *ui) info(msg string) {
	if u.enabled {
		fmt.Fprintln(os.Stdout, u.infoStyle.Render(msg))
		return
	}
	fmt.Fprintln(os.Stdout, msg)
}

func (u *ui) warn(msg string) {
	if u.enabled {
		fmt.Fprintln(os.Stderr, u.warnStyle.Render(msg))
		return
	}
	fmt.Fprintln(os.Stderr, msg)
}

func (u *ui) success(msg string) {
	if u.enabled {
		fmt.Fprintln(os.Stdout, u.successStyle.Render(msg))
		return
	}
	fmt.Fprintln(os.Stdout, msg)
}

type stringList []string

func (s *stringList) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		if node.Value == "" {
			return nil
		}
		*s = append(*s, node.Value)
		return nil
	case yaml.SequenceNode:
		for _, item := range node.Content {
			if item.Kind != yaml.ScalarNode {
				return fmt.Errorf("unsupported list value: %v", item.Kind)
			}
			if item.Value != "" {
				*s = append(*s, item.Value)
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported yaml kind: %v", node.Kind)
	}
}

type stringValue string

func (s *stringValue) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode {
		return fmt.Errorf("unsupported yaml kind: %v", node.Kind)
	}
	*s = stringValue(strings.TrimSpace(node.Value))
	return nil
}

type manifest struct {
	RepoDir               string     `yaml:"repo_dir"`
	RepoDirHyphen         string     `yaml:"repo-dir"`
	With                  stringList `yaml:"with"`
	WithRemotes           stringList `yaml:"with_remotes"`
	WithRemotesHyphenated stringList `yaml:"with-remotes"`
}

func (m manifest) repoDir() string {
	if m.RepoDir != "" {
		return m.RepoDir
	}
	return m.RepoDirHyphen
}

func (m manifest) remotes() []string {
	remotes := append([]string{}, m.With...)
	remotes = append(remotes, m.WithRemotes...)
	remotes = append(remotes, m.WithRemotesHyphenated...)
	return remotes
}

func NewCommand() *cobra.Command {
	ui := newUI()

	var manifestPath string
	var withRemotes []string

	rootCmd := &cobra.Command{
		Use:   "broadcast",
		Short: "Broadcast git-annex metadata",
		Long: `Broadcast git-annex metadata (git annex sync --no-content) to the configured remotes.

If a remote force-pushed (rewrote) its main branch since this clone last synced with
it, the sync fails; broadcast then adds a hint to the error naming the remote and
'exo sync --reset-synced <r>', and aborts any merge the failed sync left in
progress. There is no up-front refusal: unpushed local commits broadcast normally.
See 'exo sync --help' for --reset-synced.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireBinary("git-annex"); err != nil {
				return err
			}

			if manifestPath != "" {
				if err := verifyManifest(manifestPath); err != nil {
					return err
				}
				manifestData, err := parseManifest(manifestPath)
				if err != nil {
					return err
				}
				if len(withRemotes) == 0 {
					withRemotes = append(withRemotes, manifestData.remotes()...)
				}
				if repoDir := manifestData.repoDir(); repoDir != "" {
					if err := os.Chdir(repoDir); err != nil {
						return fmt.Errorf("failed to enter repo dir: %s", repoDir)
					}
				}
			}

			if err := ensureGitRepo(); err != nil {
				return err
			}

			if err := ensureAnnexInit(ui); err != nil {
				return err
			}

			if len(withRemotes) == 0 {
				ui.info("Broadcasting metadata with all remotes")
				remotes, err := listGitRemotes()
				if err != nil {
					return fmt.Errorf("listing git remotes: %w", err)
				}
				before := snapshotRemoteTips(remotes)
				if err := runCommand([]string{"git", "annex", "sync", "--no-content"}); err != nil {
					return withRewriteDiagnosis(err, before)
				}
				return nil
			}

			for _, remote := range withRemotes {
				if remote == "" {
					continue
				}
				before := snapshotRemoteTips([]string{remote})
				ui.info(fmt.Sprintf("Enabling git-annex remote '%s'", remote))
				if err := runCommand([]string{"git", "annex", "enableremote", remote}); err != nil {
					return fmt.Errorf("failed to enable remote '%s'", remote)
				}
				ui.info(fmt.Sprintf("Broadcasting metadata with remote '%s'", remote))
				if err := runCommand([]string{"git", "annex", "sync", "--no-content", remote}); err != nil {
					return withRewriteDiagnosis(err, before)
				}
			}
			ui.success("Broadcast complete")
			return nil
		},
	}

	rootCmd.Flags().StringVar(&manifestPath, "manifest", "", "Path to manifest YAML")
	rootCmd.Flags().StringArrayVar(&withRemotes, "with", nil, "Remote name to sync (repeatable)")

	return rootCmd
}

// listGitRemotes returns the names registered via `git remote` (plain git
// remotes, as opposed to git-annex special remotes that are never `git
// fetch`-able).
func listGitRemotes() ([]string, error) {
	out, err := command("git", "remote").Output()
	if err != nil {
		return nil, err
	}
	var remotes []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			remotes = append(remotes, line)
		}
	}
	return remotes, nil
}

// isGitRemote reports whether name is registered as a plain git remote (has
// a remote.<name>.url), as opposed to a git-annex-only special remote.
func isGitRemote(name string) bool {
	return runCommandQuiet([]string{"git", "config", "--get", "remote." + name + ".url"}) == nil
}

// snapshotRemoteTips records refs/remotes/<r>/main for each plain git remote
// BEFORE syncing (no fetch, no refusal), so a sync failure can later be
// diagnosed against what the remote's main was observed to be.
func snapshotRemoteTips(remotes []string) map[string]string {
	tips := map[string]string{}
	for _, remote := range remotes {
		if remote == "" || !isGitRemote(remote) {
			continue
		}
		tip, _ := commandutil.RemoteBranchRef(remote, "main")
		tips[remote] = tip
	}
	return tips
}

// withRewriteDiagnosis is called only after a git-annex sync has FAILED: if
// one of the remotes' main was actually force-pushed (rewritten) since
// `before` was observed (exo-cli#178), it appends the `exo sync
// --reset-synced` hint to the original error; otherwise it returns the
// original error unchanged. Broadcast never refuses up front.
func withRewriteDiagnosis(syncErr error, before map[string]string) error {
	for remote, tip := range before {
		if diag := commandutil.DiagnoseRewrittenRemote(remote, "main", tip); diag != nil {
			return fmt.Errorf("%w\n%s%s", syncErr, diag.Error(), abortNote())
		}
	}
	return syncErr
}

// abortNote aborts a merge the failed sync left in progress and returns a
// sentence for the user saying so (or how to do it by hand if that failed).
func abortNote() string {
	aborted, err := commandutil.AbortMergeIfInProgress()
	switch {
	case err != nil:
		return "\nThe sync left a merge in progress that exo could not abort (" + err.Error() + "); run `git merge --abort` before anything else."
	case aborted:
		return "\nexo aborted the half-done merge the sync left behind; your working tree is back to its state before the sync."
	}
	return ""
}

func requireBinary(name string) error {
	if !commandExists(name) {
		return fmt.Errorf("required binary '%s' not found in PATH", name)
	}
	return nil
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func verifyManifest(path string) error {
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("manifest not found: %s", path)
	}
	return commandutil.ValidateManifestFile("sync", path)
}

func parseManifest(path string) (manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return manifest{}, err
	}
	var m manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return manifest{}, err
	}
	if m.RepoDir != "" {
		if info, err := os.Stat(m.RepoDir); err != nil || !info.IsDir() {
			return manifest{}, fmt.Errorf("repo directory does not exist: %s", m.RepoDir)
		}
	}
	return m, nil
}

func ensureGitRepo() error {
	info, err := os.Stat(".git")
	if err != nil || !info.IsDir() {
		return errors.New("current directory is not a git repository")
	}
	return nil
}

func ensureAnnexInit(ui *ui) error {
	if err := runCommandQuiet([]string{"git", "config", "--get", "annex.uuid"}); err == nil {
		return nil
	}
	ui.info(fmt.Sprintf("Initializing git-annex in %s", mustCwd()))
	return runCommand([]string{"git", "annex", "init"})
}

func runCommand(args []string) error {
	cmd := command(args[0], args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func runCommandQuiet(args []string) error {
	cmd := command(args[0], args[1:]...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	return cmd.Run()
}

func mustCwd() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return cwd
}
