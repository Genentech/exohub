package publish

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	initcmd "github.com/Genentech/exohub/go/exo/commands/init"
	"github.com/Genentech/exohub/go/exo/commandutil"
)

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

	cmd := &cobra.Command{
		Use:   "publish",
		Short: "Orchestrate sync → bundle → sync (catalog) → broadcast",
		Long: `Publish data by orchestrating a four-step workflow:

  1. Sync content to all configured remotes EXCEPT artifactdb/catalog types
  2. Generate bundle metadata (exo bundle)
  3. Sync bundle metadata to artifactdb/catalog remotes only
  4. Broadcast metadata to all remotes (exo broadcast)

Use --sync to override step 1 and sync only to specified remotes.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				_ = cmd.Help()
				os.Exit(1)
			}
			return runPublish(syncRemotes)
		},
	}

	cmd.Flags().StringArrayVar(&syncRemotes, "sync", nil, "Remote names to sync in step 1 (repeatable, overrides auto-discovery)")

	return cmd
}

func runPublish(syncRemotes []string) error {
	if _, err := os.Stat(".git"); os.IsNotExist(err) {
		return fmt.Errorf("not a git repository")
	}

	remotesConfig, err := initcmd.LoadRemotesConfig()
	if err != nil {
		return fmt.Errorf("failed to load .exohub/remotes: %w", err)
	}

	contentRemotes, catalogRemotes := classifyRemotes(remotesConfig)

	// Step 1: Sync to content remotes (non-artifactdb)
	step1Remotes := syncRemotes
	if len(step1Remotes) == 0 {
		step1Remotes = contentRemotes
	}

	if len(step1Remotes) > 0 {
		logStep(1, fmt.Sprintf("Syncing content to %d remote(s)", len(step1Remotes)))
		if err := runExoSync(step1Remotes); err != nil {
			return fmt.Errorf("sync failed: %w", err)
		}
	} else {
		logStep(1, "No content remotes configured, skipping sync")
	}

	// Step 2: Generate bundle
	logStep(2, "Generating bundle metadata")
	if err := runExoBundle(); err != nil {
		return fmt.Errorf("bundle failed: %w", err)
	}

	// Step 3: Sync to artifactdb/catalog remotes
	if len(catalogRemotes) > 0 {
		logStep(3, fmt.Sprintf("Syncing to %d catalog remote(s)", len(catalogRemotes)))
		if err := runExoSync(catalogRemotes); err != nil {
			return fmt.Errorf("catalog sync failed: %w", err)
		}
	} else {
		fmt.Fprintf(os.Stderr, "%s No artifactdb/catalog remotes configured, skipping step 3\n", warnStyle.Render("⚠"))
	}

	// Step 4: Broadcast metadata to all remotes
	logStep(4, "Broadcasting metadata")
	if err := runExoBroadcast(); err != nil {
		return fmt.Errorf("broadcast failed: %w", err)
	}

	fmt.Fprintf(os.Stderr, "\n%s Publish complete\n", stepStyle.Render("✓"))
	return nil
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

func runExoSync(remotes []string) error {
	exePath, err := exec.LookPath("exo")
	if err != nil {
		return fmt.Errorf("exo not found in PATH: %w", err)
	}
	args := []string{"sync"}
	for _, r := range remotes {
		args = append(args, "--with", r)
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
