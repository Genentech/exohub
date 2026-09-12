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
	RepoDir               string      `yaml:"repo_dir"`
	RepoDirHyphen         string      `yaml:"repo-dir"`
	With                  stringList  `yaml:"with"`
	WithRemotes           stringList  `yaml:"with_remotes"`
	WithRemotesHyphenated stringList  `yaml:"with-remotes"`
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
				return runCommand([]string{"git", "annex", "sync", "--no-content"})
			}

			for _, remote := range withRemotes {
				if remote == "" {
					continue
				}
				ui.info(fmt.Sprintf("Enabling git-annex remote '%s'", remote))
				if err := runCommand([]string{"git", "annex", "enableremote", remote}); err != nil {
					return fmt.Errorf("failed to enable remote '%s'", remote)
				}
				ui.info(fmt.Sprintf("Broadcasting metadata with remote '%s'", remote))
				if err := runCommand([]string{"git", "annex", "sync", "--no-content", remote}); err != nil {
					return err
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
