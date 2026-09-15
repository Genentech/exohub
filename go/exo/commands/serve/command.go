package serve

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Genentech/exohub/go/exo/commands/context"
	"github.com/Genentech/exohub/go/exo/internal/defaults"
)

// Version is set by the build via ldflags (from main.go).
var Version = "dev"

// BasePath is the URL prefix for reverse proxy deployments (e.g. "/atlas").
var BasePath = ""

func NewCommand(version string) *cobra.Command {
	Version = version
	var addr string
	var catalogURL string
	var basePath string
	var downloadUI bool

	cmd := &cobra.Command{
		Use:    "serve",
		Short:  "Start the web server for browser-based catalog browsing",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			url, err := resolveCatalogURL(catalogURL)
			if err != nil {
				return err
			}
			BasePath = strings.TrimRight(basePath, "/")
			if BasePath != "" && !strings.HasPrefix(BasePath, "/") {
				BasePath = "/" + BasePath
			}

			// Pass --download-ui to atlas subprocesses via env var
			if downloadUI {
				os.Setenv("EXO_DOWNLOAD_UI", "1")
			}

			return runServe(addr, url)
		},
	}
	cmd.Flags().StringVar(&addr, "addr", ":8080", "Listen address (host:port)")
	cmd.Flags().StringVar(&catalogURL, "url", "", "ArtifactDB catalog URL (falls back to EXOHUB_CATALOG_URL or context)")
	cmd.Flags().StringVar(&basePath, "base-path", "", "Base URL path (e.g. /atlas for reverse proxy deployment)")
	cmd.Flags().BoolVar(&downloadUI, "download-ui", false, "Download UI elements at startup without confirmation")
	return cmd
}

// resolveCatalogURL returns the catalog URL from flag, env, or context.
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

	catalogDefault := defaults.CatalogURL()
	if catalogDefault != "" {
		fmt.Fprintf(os.Stderr, "No catalog URL specified, using default: %s\n", catalogDefault)
	}
	return catalogDefault, nil
}
