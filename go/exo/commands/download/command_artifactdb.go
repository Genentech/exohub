//go:build artifactdb

package download

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/Genentech/exohub/go/exo/branding"
	"github.com/Genentech/exohub/go/exo/commands/context"
	"github.com/Genentech/exohub/go/exo/internal/defaults"
	"github.com/Genentech/exohub/go/exo/internal/download"
	"github.com/Genentech/exohub/go/exo/palette"
)

var dlStylesOnce sync.Once

func initDlStyles() {
	dlStylesOnce.Do(func() {
		p := palette.Current()
		fileStyle = lipgloss.NewStyle().Foreground(p.Accent.Adaptive()).Bold(true)
		progressStyle = lipgloss.NewStyle().Foreground(p.Warning.Adaptive())
		doneStyle = lipgloss.NewStyle().Foreground(p.Label.Adaptive()).Bold(true)
		countStyle = lipgloss.NewStyle().Foreground(p.LabelAlt.Adaptive())
		skipStyle = lipgloss.NewStyle().Foreground(p.LabelAlt.Adaptive())
		errorStyle = lipgloss.NewStyle().Foreground(p.Error.Adaptive()).Bold(true)
	})
}

var (
	fileStyle     = lipgloss.NewStyle().Foreground(branding.GradientPink).Bold(true)
	progressStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#B8A038"))
	doneStyle     = lipgloss.NewStyle().Foreground(branding.GradientOrange).Bold(true)
	countStyle    = lipgloss.NewStyle().Foreground(branding.GradientPurple)
	skipStyle     = lipgloss.NewStyle().Foreground(branding.GradientPurple)
	errorStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF0000")).Bold(true)
)



func NewCommand() *cobra.Command {
	var (
		urlFlag   string
		outputDir string
		force     bool
	)

	cmd := &cobra.Command{
		Use:   "download <artifact-id | project@version>",
		Short: "Download artifact files from the catalog",
		Long: `Download artifact files from an ArtifactDB catalog.

Download a single file by artifact ID:
  exo download "myproject:file.txt@v1"

Download all files in a project/version:
  exo download "myproject@v1"

The catalog URL is resolved in this order:
  1. --url flag
  2. EXOHUB_CATALOG_URL environment variable
  3. catalog_url field in the active exo context`,
		Args: cobra.ExactArgs(1),
		PreRun: func(cmd *cobra.Command, args []string) {
			initDlStyles()
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			catalogURL, err := resolveCatalogURL(urlFlag)
			if err != nil {
				return fmt.Errorf("%s %s", errorStyle.Render("✗"), err)
			}

			if outputDir == "" {
				outputDir = "."
			}

			dl := download.New(catalogURL)
			dl.Force = force
			progress := terminalProgress()

			arg := args[0]

			// Check if this is a project@version request (no colon before @)
			if isProjectVersion(arg) {
				parts := strings.SplitN(arg, "@", 2)
				projectID := parts[0]
				version := parts[1]

				// First, list files to show summary
				files, err := dl.SearchProjectFiles(projectID, version)
				if err != nil {
					return fmt.Errorf("%s %s", errorStyle.Render("✗"), err)
				}
				if len(files) == 0 {
					fmt.Printf("%s no artifact files found for %s\n",
						errorStyle.Render("✗"),
						fileStyle.Render(fmt.Sprintf("%s@%s", projectID, version)),
					)
					return nil
				}

				var totalSize int64
				for _, f := range files {
					totalSize += f.Size
				}
				fmt.Printf("%s %s from %s\n",
					doneStyle.Render("↓"),
					countStyle.Render(fmt.Sprintf("%d files (%s)", len(files), formatSize(totalSize))),
					fileStyle.Render(fmt.Sprintf("%s@%s", projectID, version)),
				)

				return dl.DownloadProject(projectID, version, outputDir, progress)
			}

			// Check if this is a bundle or commit — download all files
			meta, metaErr := dl.GetFileMetadata(arg)
			if metaErr == nil && strings.HasPrefix(meta.Extra.Schema, "exohub-bundle/") {
				fmt.Printf("%s downloading bundle %s\n", doneStyle.Render("↓"), fileStyle.Render(arg))
				return dl.DownloadBundle(arg, outputDir, progress)
			}
			if metaErr == nil && strings.HasPrefix(meta.Extra.Schema, "exohub-commit/") {
				fmt.Printf("%s downloading commit %s\n", doneStyle.Render("↓"), fileStyle.Render(arg))
				return dl.DownloadCommit(arg, outputDir, progress)
			}

			// Single file download
			outputPath, skip, err := dl.DownloadFile(arg, outputDir, progress)
			if err != nil {
				return fmt.Errorf("%s %s", errorStyle.Render("✗"), err)
			}
			if skip != nil {
				fmt.Printf("%s %s (%s)\n", skipStyle.Render("⊘ Skipped"), fileStyle.Render(outputPath), skip.Reason)
			} else {
				fmt.Printf("\n%s %s\n", doneStyle.Render("✓ Saved to"), fileStyle.Render(outputPath))
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&urlFlag, "url", "", "ArtifactDB catalog URL")
	cmd.Flags().StringVarP(&outputDir, "output-dir", "o", ".", "Output directory for downloaded files")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Re-download even if file already exists with matching size")

	return cmd
}

// isProjectVersion returns true if the arg looks like "project@version" (no colon,
// meaning it's not an artifact ID like "project:path@version").
func isProjectVersion(arg string) bool {
	atIdx := strings.Index(arg, "@")
	if atIdx < 0 {
		return false
	}
	// Artifact IDs contain a colon before the @ (project:path@version)
	// Project@version has no colon
	return !strings.Contains(arg[:atIdx], ":")
}

func resolveCatalogURL(flagURL string) (string, error) {
	if flagURL != "" {
		return strings.TrimRight(flagURL, "/"), nil
	}

	if envURL := os.Getenv("EXOHUB_CATALOG_URL"); envURL != "" {
		return strings.TrimRight(envURL, "/"), nil
	}

	ctx, err := context.GetCurrentContext()
	if err == nil && ctx.CatalogURL != "" {
		return strings.TrimRight(ctx.CatalogURL, "/"), nil
	}

	return defaults.CatalogURL(), nil
}

// terminalProgress returns a thread-safe ProgressFunc for concurrent downloads.
// Each file's in-progress line uses \r on the current line; when a file completes
// (downloaded >= total), it prints a newline so the next file starts fresh.
func terminalProgress() download.ProgressFunc {
	var mu sync.Mutex
	lastPrint := make(map[string]time.Time)

	return func(filename string, downloaded, total int64) {
		mu.Lock()
		defer mu.Unlock()

		// Throttle output per file
		now := time.Now()
		if now.Sub(lastPrint[filename]) < 100*time.Millisecond && (total <= 0 || downloaded < total) {
			return
		}
		lastPrint[filename] = now

		// Pad filename to 30 chars for alignment
		paddedName := fmt.Sprintf("%-30s", filename)
		if len(paddedName) > 30 {
			paddedName = paddedName[:27] + "..."
		}

		if total > 0 {
			pct := float64(downloaded) / float64(total) * 100
			bar := progressBar(pct, 20)
			sizeInfo := fmt.Sprintf("%8s / %-8s", formatSize(downloaded), formatSize(total))
			fmt.Printf("\r  %s %s %s",
				fileStyle.Render(paddedName),
				progressStyle.Render(bar),
				progressStyle.Render(sizeInfo),
			)
		} else {
			fmt.Printf("\r  %s %s",
				fileStyle.Render(paddedName),
				progressStyle.Render(formatSize(downloaded)),
			)
		}

		if total > 0 && downloaded >= total {
			fmt.Println()
			delete(lastPrint, filename)
		}
	}
}

func progressBar(pct float64, width int) string {
	filled := int(pct / 100 * float64(width))
	if filled > width {
		filled = width
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
	return fmt.Sprintf("[%s] %3.0f%%", bar, pct)
}

func formatSize(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
