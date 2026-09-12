package tui

// Package-level provider instances, set via NewProgram or SetProviders.
var (
	client           Client
	contextProvider  ContextProvider
	uiConfigProvider UIConfigProvider
	downloader       Downloader
	outputDir        string
)

// SetProviders sets the package-level providers without starting the TUI.
// Use this when you need to call functions like NeedsUIElements or
// DownloadUIConfig before launching NewProgram.
func SetProviders(c Client, cp ContextProvider, ucp UIConfigProvider) {
	client = c
	contextProvider = cp
	uiConfigProvider = ucp
}

// SetDownloader sets the optional download provider.
func SetDownloader(d Downloader, outDir string) {
	downloader = d
	outputDir = outDir
}
