//go:build artifactdb

package atlas

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/caarlos0/env/v11"
	"github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/log"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/Genentech/exohub/go/exo/internal/defaults"
	"github.com/Genentech/exohub/go/exo/internal/tui"
	"github.com/Genentech/exohub/go/exo/palette"

	"github.com/Genentech/exohub/go/exo/commands/context"
)

var (
	catalogURL    string
	outputDir     string
	downloadUI    bool
	searchQuery   string
	searchSort    string
	searchFields  string
	searchSize    int
	searchLatest  bool
	searchProject string
	searchVersion string
	searchSchema  string
)

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "atlas [artifact-id]",
		Short: "Browse and search an ArtifactDB catalog",
		Long: `Browse and search content in an ArtifactDB catalog using an interactive TUI.

If an artifact ID is provided, the TUI opens directly to that document.

The catalog URL is resolved in this order:
  1. --url flag
  2. EXOHUB_CATALOG_URL environment variable
  3. catalog_url field in the active exo context

Examples:
  exo atlas                                          # search and browse
  exo atlas "myproject:file.txt@v1"                  # open a specific document
  exo atlas --url https://catalog.example.com/v1/mydb
  exo atlas --query "human" --latest                 # search with pre-populated query
  exo atlas --query "rnaseq" --sort "-_extra.uploaded"`,
		Args: cobra.MaximumNArgs(1),
		RunE: runBrowse,
	}
	cmd.Flags().StringVar(&catalogURL, "url", "", "ArtifactDB instance URL")
	cmd.Flags().StringVarP(&outputDir, "output-dir", "o", ".", "Output directory for downloaded files")
	cmd.Flags().BoolVar(&downloadUI, "download-ui", false, "Automatically download UI elements without confirmation")
	cmd.Flags().StringVarP(&searchQuery, "query", "q", "", "Pre-populate search query")
	cmd.Flags().StringVar(&searchSort, "sort", "", "Sort order (e.g. -_extra.uploaded)")
	cmd.Flags().StringVar(&searchFields, "fields", "", "Response fields")
	cmd.Flags().IntVar(&searchSize, "size", 0, "Number of results")
	cmd.Flags().BoolVar(&searchLatest, "latest", false, "Only show latest versions")
	cmd.Flags().StringVar(&searchProject, "project", "", "Filter by project ID")
	cmd.Flags().StringVar(&searchVersion, "version", "", "Filter by version (e.g. v1.0.0, or 'latest')")
	cmd.Flags().StringVar(&searchSchema, "schema", "", "Filter by schema (e.g. exohub-artifact/v1)")
	return cmd
}

// resolveCatalogURL returns the catalog URL from flag, env, or context.
func resolveCatalogURL() (string, error) {
	// 1. --url flag
	if catalogURL != "" {
		return strings.TrimRight(catalogURL, "/"), nil
	}

	// 2. EXOHUB_CATALOG_URL env
	if envURL := os.Getenv("EXOHUB_CATALOG_URL"); envURL != "" {
		return strings.TrimRight(envURL, "/"), nil
	}

	// 3. Context catalog_url field
	ctx, err := context.GetCurrentContext()
	if err == nil && ctx.CatalogURL != "" {
		return strings.TrimRight(ctx.CatalogURL, "/"), nil
	}

	// 4. Built-in default
	return defaults.CatalogURL(), nil
}

func runBrowse(cmd *cobra.Command, args []string) error {
	catalogURL, err := resolveCatalogURL()
	if err != nil {
		return err
	}

	// Redirect logs to file before any TUI code runs
	closer, err := setupLog()
	if err != nil {
		return err
	}
	defer closer()

	// Set up providers early so NeedsUIElements/DownloadUIConfig can use them
	tui.SetProviders(
		newClientAdapter(catalogURL),
		newContextAdapter(catalogURL),
		newUIConfigAdapter(),
	)
	tui.SetDownloader(newDownloaderAdapter(catalogURL), outputDir)

	// Check if UI templates need downloading from the instance
	autoDownload := downloadUI || os.Getenv("EXO_DOWNLOAD_UI") == "1"
	if autoDownload || tui.NeedsUIElements() {
		if err := tui.DownloadUIConfig(autoDownload); err != nil {
			log.Warn("Could not download UI elements", "err", err)
		}
	}

	var initialDoc string
	if len(args) > 0 {
		initialDoc = args[0]
	}

	return runTUI(catalogURL, initialDoc, tui.SearchParams{
		Q:       searchQuery,
		Sort:    searchSort,
		Fields:  searchFields,
		Size:    searchSize,
		Latest:  searchLatest,
		Project: searchProject,
		Version: searchVersion,
		Schema:  searchSchema,
	})
}

// setupLog redirects charmbracelet/log to a file so TUI output stays clean.
// Logs go to ~/.cache/exo/browse.log (following XDG cache convention).
func setupLog() (func() error, error) {
	log.SetOutput(io.Discard)
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return func() error { return nil }, nil
	}
	dir := filepath.Join(cacheDir, "exo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return func() error { return nil }, nil
	}
	logFile := filepath.Join(dir, "browse.log")
	f, err := os.OpenFile(logFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return func() error { return nil }, nil
	}
	log.SetOutput(f)
	log.SetLevel(log.DebugLevel)
	return f.Close, nil
}

func runTUI(rootURL string, initialDoc string, searchParams ...tui.SearchParams) error {
	cfg, err := env.ParseAs[tui.Config]()
	if err != nil {
		return fmt.Errorf("error parsing config: %v", err)
	}

	// Detect terminal width
	if term.IsTerminal(int(os.Stdout.Fd())) {
		w, _, err := term.GetSize(int(os.Stdout.Fd()))
		if err == nil && w > 0 {
			width := uint(w)
			if width > 120 {
				width = 120
			}
			cfg.GlamourMaxWidth = width
		}
	}
	if cfg.GlamourMaxWidth == 0 {
		cfg.GlamourMaxWidth = 80
	}

	if cfg.GlamourStyle == "" {
		cfg.GlamourStyle = styles.AutoStyle
	}

	cfg.RootURL = rootURL
	cfg.GlamourEnabled = true
	cfg.InitialDocument = initialDoc

	// Override default search params from CLI flags (if provided)
	if len(searchParams) > 0 {
		sp := searchParams[0]
		if sp.Project != "" {
			cfg.DefaultSearchParams.Project = sp.Project
		}
		if sp.Version != "" {
			cfg.DefaultSearchParams.Version = sp.Version
		} else if sp.Latest {
			cfg.DefaultSearchParams.Version = "latest"
		}
		if sp.Schema != "" {
			cfg.DefaultSearchParams.Schema = sp.Schema
		}
		if sp.Q != "" {
			cfg.DefaultSearchParams.Q = sp.Q
		}
		if sp.Sort != "" {
			cfg.DefaultSearchParams.Sort = sp.Sort
		}
		if sp.Fields != "" {
			cfg.DefaultSearchParams.Fields = sp.Fields
		}
		if sp.Size > 0 {
			cfg.DefaultSearchParams.Size = sp.Size
		}
	}
	if os.Getenv("EXO_SERVE_MODE") == "1" {
		cfg.ServeMode = true
		cfg.EnableMouse = true
	}

	// Apply the resolved theme palette
	tui.ApplyTheme(tui.ThemeFromPalette(palette.Current()))

	// Create adapters
	c := newClientAdapter(rootURL)
	cp := newContextAdapter(rootURL)
	ucp := newUIConfigAdapter()

	// NewProgram loads search params and UI config internally
	if _, err := tui.NewProgram(cfg, c, cp, ucp).Run(); err != nil {
		return err
	}

	return nil
}

// getBrowseConfigDir returns the directory for browse-specific config/state
// (UI elements, config.yaml). This is always the shared default location.
func getBrowseConfigDir() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(configDir, "exo", "browse")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return dir, nil
}

// getSearchParamsDir returns the directory for search params persistence.
// If EXO_BROWSE_SEARCH_DIR is set, it is used (for per-connection isolation in serve mode).
// Otherwise falls back to the default browse config dir.
func getSearchParamsDir() (string, error) {
	if override := os.Getenv("EXO_BROWSE_SEARCH_DIR"); override != "" {
		if err := os.MkdirAll(override, 0755); err != nil {
			return "", err
		}
		return override, nil
	}
	return getBrowseConfigDir()
}
