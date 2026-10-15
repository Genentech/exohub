//go:build artifactdb

package download

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Genentech/exohub/go/exo/commands/login"
	"github.com/Genentech/exohub/go/exo/internal/defaults"
	"github.com/Genentech/exohub/go/exo/internal/eds"
	"golang.org/x/term"
)

const (
	fallbackDownloadEndpoint   = "/api/datasets/download"
	fallbackOperationsEndpoint = "/api/operations/{id}"
)

// edsOptions holds the parsed --eds invocation.
type edsOptions struct {
	eds              string
	arg              string
	scope            string
	noWait           bool
	maxWait          time.Duration
	pollInterval     time.Duration
	outputDirChanged bool
	force            bool
}

// runEDSDownload submits a dataset download to a remote EDS and, unless
// --no-wait was given, follows it to completion. Nothing is written to
// local disk: the EDS fetches the dataset into its own storage.
func runEDSDownload(opts edsOptions) error {
	if opts.outputDirChanged {
		return fmt.Errorf("%s --eds is a remote download and cannot be combined with -o/--output-dir", errorStyle.Render("✗"))
	}
	if opts.force {
		return fmt.Errorf("%s --eds is a remote download and cannot be combined with -f/--force", errorStyle.Render("✗"))
	}
	if err := eds.ValidatePollInterval(opts.pollInterval); err != nil {
		return fmt.Errorf("%s %s", errorStyle.Render("✗"), err)
	}

	projectID, version, err := parseEDSDatasetArg(opts.arg)
	if err != nil {
		return fmt.Errorf("%s %s", errorStyle.Render("✗"), err)
	}

	tokenFile, err := login.GetTokenFile()
	if err != nil {
		return fmt.Errorf("%s %s (run `exo login`)", errorStyle.Render("✗"), err)
	}
	token, err := login.LoadToken(tokenFile)
	if err != nil || token.AccessToken == "" {
		return fmt.Errorf("%s not logged in; run `exo login`", errorStyle.Render("✗"))
	}
	if !token.Valid() && token.RefreshToken != "" {
		if refreshed, _, refreshErr := login.RefreshTokenOnly(token.RefreshToken); refreshErr == nil {
			_ = login.SaveToken(tokenFile, refreshed)
			token = refreshed
		}
	}

	client := &http.Client{Timeout: 30 * time.Second}

	dep, err := eds.ResolveDeployment(client, opts.eds, defaults.BaseURL())
	if err != nil {
		return fmt.Errorf("%s resolving --eds %s: %s", errorStyle.Render("✗"), opts.eds, err)
	}

	downloadURL, err := eds.ResolveEndpoint(dep.URL, dep.Info.Endpoints.Download, fallbackDownloadEndpoint)
	if err != nil {
		return fmt.Errorf("%s %s", errorStyle.Render("✗"), err)
	}

	fmt.Printf("%s submitting %s to %s\n",
		doneStyle.Render("↓"),
		fileStyle.Render(datasetLabel(projectID, version)),
		fileStyle.Render(dep.URL),
	)

	sub, err := eds.Submit(client, downloadURL, token.AccessToken, projectID, version, opts.scope)
	if err != nil {
		return fmt.Errorf("%s submitting download: %s", errorStyle.Render("✗"), err)
	}

	if opts.noWait {
		fmt.Printf("%s operation_id: %s\n", countStyle.Render("•"), sub.OperationID)
		fmt.Printf("%s operation_url: %s\n", countStyle.Render("•"), sub.OperationURL)
		return nil
	}

	operationsEndpoint, endpointErr := eds.ResolveEndpoint(dep.URL, dep.Info.Endpoints.Operations, fallbackOperationsEndpoint)
	var fallbackOperationsURL string
	if endpointErr == nil {
		fallbackOperationsURL = strings.ReplaceAll(operationsEndpoint, "{id}", sub.OperationID)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	isTTY := term.IsTerminal(int(os.Stdout.Fd()))
	renderer := newEDSLiveRenderer(isTTY, time.Now(), time.Now)

	op, err := eds.PollOperation(
		ctx, client,
		sub.OperationURL, fallbackOperationsURL, token.AccessToken,
		opts.pollInterval, opts.maxWait,
		renderer.Update,
		func(fallbackURL string) {
			fmt.Fprintf(os.Stderr, "%s operation_url poll failed; following via %s instead\n", countStyle.Render("•"), fallbackURL)
		},
	)
	renderer.Stop()
	if err != nil {
		fmt.Println()
		fmt.Printf("%s %s\n", errorStyle.Render("✗"), err)
		fmt.Printf("%s the download continues on the EDS; follow it at: %s\n", countStyle.Render("•"), sub.OperationURL)
		return fmt.Errorf("download did not complete")
	}

	fmt.Println()

	outcome := eds.Classify(op)
	if outcome.Success {
		if op.Result != nil {
			fmt.Println(formatEDSFinalLine(op.Result))
		}
		printSuccess(outcome)
		return nil
	}

	fmt.Printf("%s %s\n", errorStyle.Render("✗"), outcome.Message)
	if len(outcome.MissingFiles) > 0 {
		shown := outcome.MissingFiles
		if len(shown) > 10 {
			shown = shown[:10]
		}
		for _, f := range shown {
			fmt.Printf("    %s\n", f)
		}
		if len(outcome.MissingFiles) > len(shown) {
			fmt.Printf("    ... and %d more\n", len(outcome.MissingFiles)-len(shown))
		}
	}
	return fmt.Errorf("remote download did not succeed")
}

// parseEDSDatasetArg validates and splits a dataset argument for --eds.
// EDS downloads datasets, not individual artifacts, so an artifact id
// (which contains a colon before any '@') is rejected.
func parseEDSDatasetArg(arg string) (projectID, version string, err error) {
	atIdx := strings.Index(arg, "@")
	if atIdx >= 0 && strings.Contains(arg[:atIdx], ":") {
		return "", "", fmt.Errorf("%q looks like an artifact id; --eds downloads datasets, not individual artifacts (use project or project@version)", arg)
	}
	if atIdx < 0 && strings.Contains(arg, ":") {
		return "", "", fmt.Errorf("%q looks like an artifact id; --eds downloads datasets, not individual artifacts (use project or project@version)", arg)
	}
	if atIdx < 0 {
		return arg, "", nil
	}
	return arg[:atIdx], arg[atIdx+1:], nil
}

func datasetLabel(projectID, version string) string {
	if version == "" {
		return projectID
	}
	return projectID + "@" + version
}

// printSuccess reports where the data landed on the EDS. The EDS does not
// always report a destination, so a bare "Downloaded to" with nothing after
// it is avoided in favor of an explicit message.
func printSuccess(outcome eds.Outcome) {
	scopeSuffix := ""
	if outcome.Scope != "" {
		scopeSuffix = fmt.Sprintf(" (scope: %s)", outcome.Scope)
	}

	if outcome.Destination == "" {
		fmt.Printf("%s%s\n", doneStyle.Render("✓ Completed (the EDS reported no destination path)"), scopeSuffix)
		return
	}
	fmt.Printf("%s %s%s\n", doneStyle.Render("✓ Downloaded to"), fileStyle.Render(outcome.Destination), scopeSuffix)
}

// edsProgressKeepAlive is how often a non-TTY log gets a repeated progress
// line even when nothing has changed, so a long-running download still
// shows signs of life without flooding the log every poll interval.
const edsProgressKeepAlive = 5 * time.Minute
