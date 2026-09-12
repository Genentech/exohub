package clone

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/Genentech/exohub/go/exo/commandutil"
)

var (
	flagPreset string
	flagYes    bool
	flagDryRun bool
)

var command = commandutil.Command

// NewCommand creates the exo clone command
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "clone <repo-url> [destination]",
		Short: "Clone repository with preset-based sparse checkout",
		Long: `Clone an exo-enabled repository with preset-based sparse checkout.

Default behavior (no flags): Discovers presets from .exohub/presets.
If presets exist, shows an interactive selection menu with "full-clone"
as the first option. If no presets exist, performs a full clone.

Examples:
  # Clone with automatic preset discovery and selection
  exo clone https://exogit.example.com/org/repo.git

  # Use specific preset (skip interactive selection)
  exo clone --preset minimal https://exogit.example.com/org/repo.git

  # Clone to custom destination
  exo clone --preset minimal https://exogit.example.com/org/repo.git my-folder

  # Auto-accept default (full-clone) for CI/CD
  exo clone --yes https://exogit.example.com/org/repo.git

Destination directory behavior (matches git clone):
  - Default: Uses last part of URL (e.g., "repo" from "org/repo.git")
  - Custom: Provide destination as second argument
  - Error: Fails if destination exists and is not empty
`,
		Args: cobra.RangeArgs(1, 2),
		RunE: runClone,
	}

	cmd.Flags().StringVar(&flagPreset, "preset", "", "Clone preset name to use (skip interactive selection)")
	cmd.Flags().BoolVar(&flagYes, "yes", false, "Accept default choice (full-clone) automatically")
	cmd.Flags().BoolVar(&flagDryRun, "dry-run", false, "Show what would be done without making changes")

	return cmd
}

func runClone(cmd *cobra.Command, args []string) error {
	repoURL := args[0]

	// Determine destination directory
	var destDir string
	if len(args) >= 2 {
		destDir = args[1]
	} else {
		destDir = extractRepoName(repoURL)
	}

	// Check if destination already exists
	if _, err := os.Stat(destDir); err == nil {
		return fmt.Errorf("destination directory '%s' already exists", destDir)
	}

	if flagDryRun {
		fmt.Printf("Would clone %s to %s\n", repoURL, destDir)
		if flagPreset != "" {
			fmt.Printf("Would use preset: %s\n", flagPreset)
		} else {
			fmt.Println("Would discover presets and show selection if available")
		}
		return nil
	}

	// Step 1: Shallow clone to discover presets
	fmt.Printf("Cloning repository (discovering presets)...\n")
	if err := shallowCloneExohub(repoURL, destDir); err != nil {
		os.RemoveAll(destDir)
		return err
	}

	// Step 2: Check for .exohub/ and load presets
	var presetsConfig *PresetsConfig
	if checkExohubExists(destDir) {
		var err error
		presetsConfig, err = LoadPresetsConfig(destDir)
		if err != nil {
			os.RemoveAll(destDir)
			return fmt.Errorf("failed to load presets: %w", err)
		}
	}

	hasPresets := presetsConfig != nil && len(presetsConfig.Presets) > 0

	// Step 3: Select preset
	var selectedPreset *ClonePreset
	if flagPreset != "" {
		// --preset flag: use the specified preset
		if !hasPresets {
			os.RemoveAll(destDir)
			return fmt.Errorf("preset '%s' not found: no presets defined in .exohub/presets", flagPreset)
		}
		selectedPreset = presetsConfig.GetPresetByName(flagPreset)
		if selectedPreset == nil {
			os.RemoveAll(destDir)
			return fmt.Errorf("preset '%s' not found\nAvailable presets: %s", flagPreset, listPresetNames(presetsConfig))
		}
		fmt.Printf("Using preset: %s\n", flagPreset)
	} else if hasPresets {
		// No --preset flag but presets exist: show interactive selection
		var err error
		selectedPreset, err = selectPresetInteractive(presetsConfig, flagYes)
		if err != nil {
			os.RemoveAll(destDir)
			return err
		}
	} else {
		// No presets at all: full clone
		selectedPreset = &ClonePreset{
			Name:        "full-clone",
			Description: "Full repository",
			Depth:       0,
		}
	}

	// Step 4: Apply preset
	fmt.Printf("Applying preset '%s'...\n", selectedPreset.Name)
	if err := applyPreset(destDir, selectedPreset); err != nil {
		return fmt.Errorf("failed to apply preset: %w", err)
	}

	// Step 5: Store metadata
	commit, err := getCurrentCommit(destDir)
	if err != nil {
		return fmt.Errorf("failed to get current commit: %w", err)
	}

	branch, err := getCurrentBranch(destDir)
	if err != nil {
		return fmt.Errorf("failed to get current branch: %w", err)
	}

	metadata := &CloneMetadata{
		Preset:   selectedPreset.Name,
		ClonedAt: time.Now().UTC().Format(time.RFC3339),
		RepoURL:  repoURL,
		Commit:   commit,
		Branch:   branch,
	}

	if err := SaveCloneMetadata(destDir, metadata); err != nil {
		return fmt.Errorf("failed to save clone metadata: %w", err)
	}

	fmt.Printf("\n✓ Successfully cloned to %s with preset '%s'\n", destDir, selectedPreset.Name)
	return nil
}

// selectPresetInteractive prompts the user to select a preset
func selectPresetInteractive(config *PresetsConfig, autoYes bool) (*ClonePreset, error) {
	// Build options list with "full-clone" as first option
	options := []huh.Option[string]{
		huh.NewOption("full-clone (Full repository, no sparse checkout)", "full-clone"),
	}

	for _, preset := range config.Presets {
		label := preset.Name
		if preset.Description != "" {
			label = fmt.Sprintf("%s (%s)", preset.Name, preset.Description)
		}
		options = append(options, huh.NewOption(label, preset.Name))
	}

	// Auto-select first option in --yes mode
	if autoYes {
		fmt.Println("Auto-selecting: full-clone")
		// Return a synthetic full clone preset
		return &ClonePreset{
			Name:        "full-clone",
			Description: "Full repository",
			SparsePaths: nil,
			GitConfig:   nil,
			Depth:       0,
		}, nil
	}

	var selected string
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Select clone preset").
				Options(options...).
				Value(&selected),
		),
	).WithTheme(commandutil.ExoTheme()).WithKeyMap(commandutil.ExoKeyMap())

	if err := form.Run(); err != nil {
		return nil, err
	}

	// Handle full-clone selection
	if selected == "full-clone" {
		return &ClonePreset{
			Name:        "full-clone",
			Description: "Full repository",
			SparsePaths: nil,
			GitConfig:   nil,
			Depth:       0,
		}, nil
	}

	// Return selected preset
	return config.GetPresetByName(selected), nil
}

// listPresetNames returns a comma-separated list of preset names
func listPresetNames(config *PresetsConfig) string {
	names := make([]string, len(config.Presets))
	for i, preset := range config.Presets {
		names[i] = preset.Name
	}
	return filepath.Join(names...)
}
