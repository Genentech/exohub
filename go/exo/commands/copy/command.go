package copy

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/Genentech/exohub/go/exo/commandutil"
)

type stringValue string

func (s *stringValue) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode {
		return fmt.Errorf("unsupported yaml kind: %v", node.Kind)
	}
	*s = stringValue(strings.TrimSpace(node.Value))
	return nil
}

type manifest struct {
	From stringValue `yaml:"from"`
	To   stringValue `yaml:"to"`
	Ref  stringValue `yaml:"ref"`
	Auto stringValue `yaml:"auto"`
	Jobs stringValue `yaml:"jobs"`
}

func NewCommand() *cobra.Command {
	var (
		manifestPath string
		fromRemote   string
		toRemote     string
		ref          string
		autoFlag     bool
		jobs         = strings.TrimSpace(os.Getenv("EXOHUB_JOBS"))
		jobsFlag     string
	)
	if jobs == "" {
		jobs = "1"
	}

	rootCmd := &cobra.Command{
		Use:   "copy",
		Short: "Copy annexed content between remotes",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireBins("git-annex"); err != nil {
				exitWithError(err)
			}

			if manifestPath != "" {
				m, err := parseManifest(manifestPath)
				if err != nil {
					exitWithError(err)
				}
				if fromRemote == "" && m.From != "" {
					fromRemote = string(m.From)
				}
				if toRemote == "" && m.To != "" {
					toRemote = string(m.To)
				}
				if ref == "" && m.Ref != "" {
					ref = string(m.Ref)
				}
				if m.Auto != "" {
					autoFlag = parseBool(string(m.Auto))
				}
				if m.Jobs != "" {
					jobs = string(m.Jobs)
				}
			}

			// CLI flag takes precedence over manifest and environment variable
			if jobsFlag != "" {
				jobs = jobsFlag
			}

			if fromRemote == "" {
				exitWithError(fmt.Errorf("--from is required"))
			}
			if toRemote == "" {
				exitWithError(fmt.Errorf("--to is required"))
			}

			if err := ensureGitRepo(); err != nil {
				exitWithError(err)
			}

			if ref != "" {
				if err := verifyRef(ref); err != nil {
					exitWithError(fmt.Errorf("ref '%s' not found (must be a commit, tag, or branch)", ref))
				}
				autoFlag = true
			}

			fmt.Printf("Enabling git-annex remote '%s'\n", fromRemote)
			if err := runCommand([]string{"git", "annex", "enableremote", fromRemote}); err != nil {
				exitWithError(fmt.Errorf("failed to enable remote '%s'", fromRemote))
			}
			fmt.Printf("Enabling git-annex remote '%s'\n", toRemote)
			if err := runCommand([]string{"git", "annex", "enableremote", toRemote}); err != nil {
				exitWithError(fmt.Errorf("failed to enable remote '%s'", toRemote))
			}

			if autoFlag {
				fmt.Printf("Copying annexed content from '%s' to '%s' (auto mode using wanted rules)\n", fromRemote, toRemote)
				if ref != "" {
					fmt.Printf("Using ref '%s' (no checkout)\n", ref)
					if err := runCommand([]string{"git", "annex", "copy", "--from", fromRemote, "--to", toRemote, "--auto", "--branch", ref, "--jobs", jobs}); err != nil {
						exitWithError(err)
					}
					return nil
				}
				if err := runCommand([]string{"git", "annex", "copy", "--from", fromRemote, "--to", toRemote, "--auto", "--jobs", jobs}); err != nil {
					exitWithError(err)
				}
				return nil
			}

			fmt.Printf("Copying annexed content from '%s' to '%s' for current directory\n", fromRemote, toRemote)
			if err := runCommand([]string{"git", "annex", "copy", "--from", fromRemote, "--to", toRemote, "."}); err != nil {
				exitWithError(err)
			}
			return nil
		},
	}

	rootCmd.Flags().StringVar(&manifestPath, "manifest", "", "Path to manifest YAML")
	rootCmd.Flags().StringVar(&fromRemote, "from", "", "Source annex remote")
	rootCmd.Flags().StringVar(&toRemote, "to", "", "Destination annex remote")
	rootCmd.Flags().StringVar(&ref, "ref", "", "Commit, tag, or branch")
	rootCmd.Flags().BoolVar(&autoFlag, "auto", false, "Use annex wanted rules")
	rootCmd.Flags().StringVarP(&jobsFlag, "jobs", "J", "", "Number of parallel jobs (default: 1)")

	return rootCmd
}

func exitWithError(err error) {
	var exitErr exitError
	if errors.As(err, &exitErr) {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(exitErr.code)
	}
	fmt.Fprintln(os.Stderr, err.Error())
	os.Exit(1)
}

type exitError struct {
	code int
	err  error
}

func (e exitError) Error() string {
	if e.err != nil {
		return e.err.Error()
	}
	return fmt.Sprintf("exit code %d", e.code)
}

func requireBins(names ...string) error {
	missing := false
	for _, name := range names {
		if !commandExists(name) {
			fmt.Fprintf(os.Stderr, "Required binary '%s' not found in PATH\n", name)
			missing = true
		}
	}
	if missing {
		return errors.New("missing binaries")
	}
	return nil
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func parseManifest(path string) (manifest, error) {
	if _, err := os.Stat(path); err != nil {
		return manifest{}, fmt.Errorf("Manifest not found: %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return manifest{}, err
	}
	var m manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return manifest{}, err
	}
	return m, nil
}

func parseBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "yes", "1":
		return true
	case "false", "no", "0":
		return false
	default:
		return false
	}
}

func ensureGitRepo() error {
	info, err := os.Stat(".git")
	if err != nil || !info.IsDir() {
		return errors.New("Current directory is not a git repository")
	}
	return nil
}

func verifyRef(ref string) error {
	cmd := commandutil.Command("git", "rev-parse", "--quiet", "--verify", fmt.Sprintf("%s^{commit}", ref))
	return cmd.Run()
}

func runCommand(args []string) error {
	cmd := commandutil.Command(args[0], args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}
