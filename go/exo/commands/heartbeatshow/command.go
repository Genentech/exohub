package heartbeatshow

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"

	"github.com/Genentech/exohub/go/exo/commandutil"
	"github.com/Genentech/exohub/go/exo/internal/defaults"
	"github.com/Genentech/exohub/go/exo/palette"
	"github.com/spf13/cobra"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"

	manifestutil "github.com/Genentech/exohub/go/exo/commandutil"
)

type ui struct {
	enabled        bool
	greenStyle     lipgloss.Style
	blueStyle      lipgloss.Style
	redStyle       lipgloss.Style
	dimStyle       lipgloss.Style
	orangeStyle    lipgloss.Style
	purpleStyle    lipgloss.Style
	pinkStyle      lipgloss.Style
	yellowStyle    lipgloss.Style
	barFilledStyle lipgloss.Style
	barEmptyStyle  lipgloss.Style
}

func newUI() *ui {
	enabled := term.IsTerminal(int(os.Stdout.Fd()))
	p := palette.Current()
	return &ui{
		enabled:        enabled,
		greenStyle:     lipgloss.NewStyle().Foreground(p.Success.Adaptive()),
		blueStyle:      lipgloss.NewStyle().Foreground(p.AccentDull.Adaptive()),
		redStyle:       lipgloss.NewStyle().Foreground(p.Error.Adaptive()),
		dimStyle:       lipgloss.NewStyle().Foreground(p.Dim.Adaptive()),
		orangeStyle:    lipgloss.NewStyle().Foreground(p.Label.Adaptive()).Bold(true),
		purpleStyle:    lipgloss.NewStyle().Foreground(p.LabelAlt.Adaptive()).Bold(true),
		pinkStyle:      lipgloss.NewStyle().Foreground(p.Accent.Adaptive()).Bold(true),
		yellowStyle:    lipgloss.NewStyle().Foreground(p.Highlight.Adaptive()).Bold(true),
		barFilledStyle: lipgloss.NewStyle().Foreground(p.ProgressFilled.Adaptive()),
		barEmptyStyle:  lipgloss.NewStyle().Foreground(p.ProgressEmpty.Adaptive()),
	}
}

func (u *ui) render(style lipgloss.Style, s string) string {
	if u.enabled {
		return style.Render(s)
	}
	return s
}

func (u *ui) green(s string) string  { return u.render(u.greenStyle, s) }
func (u *ui) blue(s string) string   { return u.render(u.blueStyle, s) }
func (u *ui) red(s string) string    { return u.render(u.redStyle, s) }
func (u *ui) dim(s string) string    { return u.render(u.dimStyle, s) }
func (u *ui) orange(s string) string { return u.render(u.orangeStyle, s) }
func (u *ui) purple(s string) string { return u.render(u.purpleStyle, s) }
func (u *ui) pink(s string) string   { return u.render(u.pinkStyle, s) }
func (u *ui) yellow(s string) string { return u.render(u.yellowStyle, s) }

const longText = `Show heartbeat metrics for a repository.

Reads from the local metrics file by default, or fetches from the API when --workflow-id is provided.`

type manifest struct {
	RepoDir       string `yaml:"repo_dir"`
	RepoDirHyphen string `yaml:"repo-dir"`
}

func (m manifest) repoDir() string {
	if m.RepoDir != "" {
		return m.RepoDir
	}
	return m.RepoDirHyphen
}

type metrics struct {
	Timestamp string        `json:"ts"`
	Annex     annexMetrics  `json:"annex"`
	Network   networkMetric `json:"network"`
}

type annexMetrics struct {
	Tmp            annexTmp        `json:"tmp"`
	Downloads      transferMetrics `json:"downloads"`
	Uploads        transferMetrics `json:"uploads"`
	RemotesTracked []string        `json:"remotes_tracked"`
	Bad            badMetrics      `json:"bad"`
	Paths          []pathMetrics   `json:"paths"`
	Summary        summaryMetrics  `json:"summary"`
	Local          summaryMetrics  `json:"local"`
}

type annexTmp struct {
	Timestamp string `json:"ts"`
	Bytes     int64  `json:"bytes"`
	Size      string `json:"size"`
}

type transferMetrics struct {
	Timestamp string   `json:"ts"`
	Files     []string `json:"files"`
	Count     int      `json:"count"`
}

type badMetrics struct {
	Timestamp string   `json:"ts"`
	Files     []string `json:"files"`
	Count     int      `json:"count"`
}

type pathMetrics struct {
	Timestamp string         `json:"ts"`
	Path      string         `json:"path"`
	Files     filesMetrics   `json:"files"`
	Storage   storageMetrics `json:"storage"`
	Remotes   []remoteMetric `json:"remotes"`
}

type filesMetrics struct {
	Total    int64         `json:"total"`
	Count    int64         `json:"count"`
	Progress progressValue `json:"progress"`
}

type storageMetrics struct {
	Total    sizeValue     `json:"total"`
	Synced   sizeValue     `json:"synced"`
	Progress progressValue `json:"progress"`
}

type sizeValue struct {
	Bytes int64  `json:"bytes"`
	Human string `json:"human-readable"`
}

type progressValue struct {
	Percent float64 `json:"percent"`
	Bar     string  `json:"bar"`
}

type summaryMetrics struct {
	Files   filesMetrics   `json:"files"`
	Storage storageMetrics `json:"storage"`
}

type remoteMetric struct {
	Name    string        `json:"name"`
	Files   filesMetrics  `json:"files"`
	Storage remoteStorage `json:"storage"`
}

type remoteStorage struct {
	Uploaded sizeValue     `json:"uploaded"`
	Total    sizeValue     `json:"total"`
	Progress progressValue `json:"progress"`
}

type networkMetric struct {
	StartTS  string        `json:"ts_start"`
	EndTS    string        `json:"ts_end"`
	Duration float64       `json:"duration_seconds"`
	Ifaces   []string      `json:"interfaces"`
	RX       trafficMetric `json:"rx"`
	TX       trafficMetric `json:"tx"`
}

type trafficMetric struct {
	StartBytes int64   `json:"start_bytes"`
	EndBytes   int64   `json:"end_bytes"`
	DeltaBytes int64   `json:"delta_bytes"`
	RateBps    float64 `json:"rate_Bps"`
	RateHuman  string  `json:"human-readable-rate"`
}

func NewCommand() *cobra.Command {
	var repoDir string
	var manifestPath string
	var metricFile string
	var workflowID string
	var runID string
	var jsonOut bool
	var verbose bool

	rootCmd := &cobra.Command{
		Use:   "show",
		Short: "Show heartbeat metrics",
		Long:  longText,
		Run: func(cmd *cobra.Command, args []string) {
			if len(args) > 0 {
				_ = cmd.Help()
				os.Exit(2)
			}
			if runID != "" && workflowID == "" {
				fmt.Fprintln(os.Stderr, "--run-id requires --workflow-id")
				os.Exit(2)
			}
			runShow(repoDir, manifestPath, metricFile, workflowID, runID, jsonOut, verbose)
		},
	}

	rootCmd.Flags().StringVar(&repoDir, "repo", "", "Repository root")
	rootCmd.Flags().StringVar(&manifestPath, "manifest", "", "Manifest file providing repo-dir")
	rootCmd.Flags().StringVar(&metricFile, "metric-file", "latest.json", "Metrics filename to read")
	rootCmd.Flags().StringVar(&workflowID, "workflow-id", "", "Fetch metrics from API")
	rootCmd.Flags().StringVar(&runID, "run-id", "", "Fetch metrics for a specific workflow run (requires --workflow-id)")
	rootCmd.Flags().BoolVar(&jsonOut, "json", false, "Output raw JSON")
	rootCmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Expand all paths with full detail")

	return rootCmd
}

func runShow(repoDir, manifestPath, metricFile, workflowID, runID string, jsonOut, verbose bool) {
	if workflowID != "" {
		if runID == "" || strings.EqualFold(runID, "last") {
			if strings.EqualFold(runID, "last") {
				// Resolve "last" to the most recent run
				runs, err := fetchWorkflowRuns(workflowID)
				if err != nil {
					fmt.Fprintln(os.Stderr, err.Error())
					os.Exit(1)
				}
				if len(runs) == 0 {
					fmt.Fprintf(os.Stderr, "no runs found for workflow %s\n", workflowID)
					os.Exit(1)
				}
				runID = runs[0].RunID
			} else {
				resolved, err := selectRunID(workflowID)
				if err != nil {
					fmt.Fprintln(os.Stderr, err.Error())
					os.Exit(1)
				}
				runID = resolved
			}
		}

		tmpDir, err := os.MkdirTemp("", "exo-heartbeat")
		if err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
		defer os.RemoveAll(tmpDir)
		metricsDir := tmpDir
		filePath := filepath.Join(metricsDir, metricFile)
		var wfStatus string
		hb, err := fetchHeartbeatMetrics(metricsDir, metricFile, workflowID, runID)
		if err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
		wfStatus = hb.status
		printWorkflowHeader(workflowID, hb)
		if hb.hasMetrics {
			showFileOnce(filePath, metricsDir, "(remote)", "", jsonOut, verbose, wfStatus, hb.dryRun)
		} else {
			printDryRun(hb.dryRun)
			fmt.Println("No heartbeat metrics yet")
		}
		return
	}

	if repoDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
		repoDir = cwd
	}
	var manifestHash string
	if manifestPath != "" {
		if err := validateHeartbeatManifest(manifestPath); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
		m, err := parseManifest(manifestPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
		if repoDirValue := m.repoDir(); repoDirValue != "" {
			repoDir = repoDirValue
		}
		// Compute manifest hash for metrics directory
		hash, err := manifestutil.ManifestHash(manifestPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
		manifestHash = hash
	}
	repoAbs, err := filepath.Abs(repoDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
	repoDir = repoAbs
	if !isDir(repoDir) {
		fmt.Fprintf(os.Stderr, "Repo directory does not exist: %s\n", repoDir)
		os.Exit(1)
	}
	metricsDir := filepath.Join(repoDir, ".git", "exohub", "metrics")
	if manifestHash != "" {
		metricsDir = filepath.Join(metricsDir, manifestHash)
	}
	filePath := filepath.Join(metricsDir, metricFile)
	showFileOnce(filePath, metricsDir, repoDir, manifestPath, jsonOut, verbose, "")
}

func printWorkflowHeader(workflowID string, hb heartbeatResult) {
	u := newUI()
	label := u.orange("Workflow:")
	name := workflowID

	var meta []string
	if hb.runID != "" {
		meta = append(meta, fmt.Sprintf("%s %s", u.dim("run:"), hb.runID))
	}
	if hb.status != "" {
		meta = append(meta, colorizeStatus(u, hb.status))
	}
	if hb.activityType != "" {
		meta = append(meta, fmt.Sprintf("%s %s", u.dim("activity:"), hb.activityType))
	}
	if len(meta) == 0 {
		fmt.Printf("%s %s\n", label, name)
	} else {
		fmt.Printf("%s %s (%s)\n", label, name, strings.Join(meta, ", "))
	}
}

func colorizeStatus(u *ui, status string) string {
	switch strings.ToLower(status) {
	case "completed":
		return u.green(status)
	case "running":
		return u.pink(status)
	case "canceled", "cancelled":
		return u.orange(status)
	default: // terminated, failed, timed_out, etc.
		return u.red(status)
	}
}

func statusIcon(status string) string {
	switch strings.ToLower(status) {
	case "completed":
		return "✅"
	case "running":
		return "🔄"
	case "canceled", "cancelled":
		return "⚠️"
	default: // terminated, failed, timed_out
		return "❌"
	}
}

func printDryRun(dr *dryRunResult) {
	if dr == nil {
		return
	}
	u := newUI()
	fmt.Printf("%s %d files (%s) here → remote, %d files (%s) remote → here\n",
		u.orange("Dry run:"),
		dr.HereToRemoteCount, dr.HereToRemoteSize,
		dr.RemoteToHereCount, dr.RemoteToHereSize,
	)
}

type dryRunResult struct {
	HereToRemoteCount int64  `json:"here_to_remote_count"`
	HereToRemoteBytes int64  `json:"here_to_remote_bytes"`
	HereToRemoteSize  string `json:"here_to_remote_size"`
	RemoteToHereCount int64  `json:"remote_to_here_count"`
	RemoteToHereBytes int64  `json:"remote_to_here_bytes"`
	RemoteToHereSize  string `json:"remote_to_here_size"`
}

type heartbeatResult struct {
	status       string
	runID        string
	activityType string
	dryRun       *dryRunResult
	hasMetrics   bool
}

type workflowRun struct {
	RunID     string `json:"run_id"`
	Status    string `json:"status"`
	StartTime string `json:"start_time"`
}

type runsResponse struct {
	WorkflowID string        `json:"workflow_id"`
	Runs       []workflowRun `json:"runs"`
}

func fetchWorkflowRuns(workflowID string) ([]workflowRun, error) {
	reqURL := strings.TrimRight(defaults.APIBase(), "/") + "/runs/" + workflowID
	manifestutil.DebugHTTP("GET", reqURL)
	resp, err := http.Get(reqURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch runs: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("failed to fetch runs: HTTP %d for %s", resp.StatusCode, reqURL)
	}
	body, _ := io.ReadAll(resp.Body)
	var result runsResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse runs response: %w", err)
	}
	return result.Runs, nil
}

func selectRunID(workflowID string) (string, error) {
	runs, err := fetchWorkflowRuns(workflowID)
	if err != nil {
		return "", err
	}
	if len(runs) == 0 {
		return "", fmt.Errorf("no runs found for workflow %s", workflowID)
	}
	if len(runs) == 1 {
		return runs[0].RunID, nil
	}

	// Non-TTY: auto-select newest run (first in list)
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return runs[0].RunID, nil
	}

	u := newUI()

	// Compute max status length for alignment
	maxStatus := 0
	for _, r := range runs {
		if len(r.Status) > maxStatus {
			maxStatus = len(r.Status)
		}
	}

	options := make([]huh.Option[string], 0, len(runs))
	for i, r := range runs {
		startDisplay := r.StartTime
		if t, err := time.Parse(time.RFC3339, r.StartTime); err == nil {
			startDisplay = t.UTC().Format("2006-01-02 15:04:05 UTC")
		}
		icon := statusIcon(r.Status)
		coloredStatus := colorizeStatus(u, r.Status)
		// Pad status with spaces for alignment (account for ANSI codes in colored output)
		padding := strings.Repeat(" ", maxStatus-len(r.Status))
		label := fmt.Sprintf("%s %s  %s%s  %s", icon, r.RunID, coloredStatus, padding, startDisplay)
		if i == 0 {
			label = fmt.Sprintf("%s %s  %s%s  %s  %s", icon, u.yellow(r.RunID), coloredStatus, padding, startDisplay, u.yellow("(latest)"))
		}
		options = append(options, huh.NewOption(label, r.RunID))
	}

	var selected string
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title(fmt.Sprintf("Select run for %s", workflowID)).
				Options(options...).
				Value(&selected),
		),
	).WithTheme(commandutil.ExoTheme()).WithKeyMap(commandutil.ExoKeyMap())
	if err := form.Run(); err != nil {
		return "", fmt.Errorf("run selection cancelled: %w", err)
	}
	return selected, nil
}

func fetchHeartbeatMetrics(metricsDir, outFile, workflowID, runID string) (heartbeatResult, error) {
	reqURL := strings.TrimRight(defaults.APIBase(), "/") + "/heartbeat/" + workflowID
	if runID != "" {
		reqURL += "?run_id=" + runID
	}
	manifestutil.DebugHTTP("GET", reqURL)
	resp, err := http.Get(reqURL)
	if err != nil {
		return heartbeatResult{}, fmt.Errorf("Failed to fetch heartbeat: %s", reqURL)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return heartbeatResult{}, fmt.Errorf("Failed to fetch heartbeat: %s", reqURL)
	}
	body, _ := io.ReadAll(resp.Body)

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return heartbeatResult{}, err
	}
	status, _ := payload["status"].(string)
	respRunID, _ := payload["run_id"].(string)
	activityType := extractActivityType(payload)
	res := heartbeatResult{status: status, runID: respRunID, activityType: activityType}

	// Parse dry_run if present
	if drRaw, ok := payload["dry_run"].(map[string]any); ok {
		drBytes, _ := json.Marshal(drRaw)
		var dr dryRunResult
		if json.Unmarshal(drBytes, &dr) == nil {
			res.dryRun = &dr
		}
	}

	metricsData := findHeartbeatMetrics(payload)
	if metricsData == nil {
		return res, nil
	}
	res.hasMetrics = true
	if err := os.MkdirAll(metricsDir, 0o755); err != nil {
		return res, err
	}
	return res, os.WriteFile(filepath.Join(metricsDir, outFile), metricsData, 0o644)
}

func extractActivityType(payload map[string]any) string {
	activities, ok := payload["pending_activities"].([]any)
	if !ok {
		return ""
	}
	for _, entry := range activities {
		item, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if actType, ok := item["activity_type"].(string); ok && actType != "" {
			return actType
		}
	}
	return ""
}

func findHeartbeatMetrics(payload map[string]any) []byte {
	// Case 1: completed workflow – metrics in top-level last_heartbeat
	if lastHB, ok := payload["last_heartbeat"].(map[string]any); ok {
		if metrics, ok := lastHB["metrics"]; ok {
			data, _ := json.Marshal(metrics)
			return data
		}
	}

	// Case 2: running workflow – metrics in pending_activities heartbeat
	activities, ok := payload["pending_activities"].([]any)
	if !ok {
		return nil
	}
	for _, entry := range activities {
		item, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if hb, ok := item["heartbeat"].(map[string]any); ok {
			if hbJSON, ok := hb["json"].(map[string]any); ok {
				if metrics, ok := hbJSON["metrics"]; ok {
					data, _ := json.Marshal(metrics)
					return data
				}
			}
			if hbB64, ok := hb["base64"].(string); ok && hbB64 != "" {
				decoded, err := base64.StdEncoding.DecodeString(hbB64)
				if err != nil {
					continue
				}
				var decodedPayload map[string]any
				if err := json.Unmarshal(decoded, &decodedPayload); err != nil {
					continue
				}
				if metrics, ok := decodedPayload["metrics"]; ok {
					data, _ := json.Marshal(metrics)
					return data
				}
			}
		}
	}
	return nil
}

func showFileOnce(path, metricsDir, repoDir, manifestPath string, jsonOut, verbose bool, workflowStatus string, dryRun ...*dryRunResult) {
	if !fileExists(path) {
		fmt.Fprintf(os.Stderr, "Metrics file not found: %s\n", path)
		os.Exit(1)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
	if jsonOut {
		fmt.Print(string(data))
		return
	}
	var payload metrics
	if err := json.Unmarshal(data, &payload); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
	var dr *dryRunResult
	if len(dryRun) > 0 {
		dr = dryRun[0]
	}
	printFriendly(payload, metricsDir, repoDir, manifestPath, path, verbose, workflowStatus, dr)
}

func printFriendly(payload metrics, metricsDir, repoDir, manifestPath, filePath string, verbose bool, workflowStatus string, dryRun *dryRunResult) {
	u := newUI()

	// Dimmed metadata header
	fmt.Println(u.dim(fmt.Sprintf("Repo: %s", repoDir)))
	if repoDir != "(remote)" {
		fmt.Println(u.dim(fmt.Sprintf("Metrics: %s", metricsDir)))
	}
	if manifestPath != "" {
		fmt.Println(u.dim(fmt.Sprintf("Manifest: %s", manifestPath)))
	}

	tsVal := payload.Timestamp
	frames := listMetricFiles(metricsDir)
	metricCount := len(frames)
	if fileExists(filepath.Join(metricsDir, "latest.json")) {
		metricCount++
	}
	currentFrame := ""
	if filepath.Base(filePath) == "latest.json" {
		currentFrame = fmt.Sprintf("%d", metricCount)
	} else {
		for idx, frame := range frames {
			if filepath.Join(metricsDir, frame) == filePath {
				currentFrame = fmt.Sprintf("%d", idx+1)
				break
			}
		}
	}
	ageDetails := ""
	if tsVal != "" {
		if ts, err := time.Parse(time.RFC3339, tsVal); err == nil {
			ageDetails = fmt.Sprintf(", %s ago", durCompact(int(time.Since(ts).Seconds())))
		}
	}
	if tsVal == "" && metricCount <= 1 {
		// No real metric data yet — skip the timestamp line entirely.
		// printStatusSummary will show "Metrics are being computed..." for running workflows.
	} else if currentFrame != "" {
		fmt.Println(u.dim(fmt.Sprintf("Timestamp: %s (metric: %s/%d%s)", tsVal, currentFrame, metricCount, ageDetails)))
	} else {
		fmt.Println(u.dim(fmt.Sprintf("Timestamp: %s (metric: %d/%d%s)", tsVal, metricCount, metricCount, ageDetails)))
	}

	// Status summary line
	printStatusSummary(payload, u, workflowStatus)
	printDryRun(dryRun)

	if payload.Network.RX.RateHuman != "" && payload.Network.TX.RateHuman != "" {
		if payload.Network.Duration > 0 {
			fmt.Printf("%s  RX %s  TX %s (over %.0fs)\n", u.orange("Network:"), payload.Network.RX.RateHuman, payload.Network.TX.RateHuman, payload.Network.Duration)
		} else {
			fmt.Printf("%s  RX %s  TX %s\n", u.orange("Network:"), payload.Network.RX.RateHuman, payload.Network.TX.RateHuman)
		}
	}

	completed := strings.EqualFold(workflowStatus, "completed")

	summary := payload.Annex.Summary
	if summary.Files.Total == 0 && summary.Storage.Total.Bytes == 0 {
		summary = summarizePaths(payload.Annex.Paths)
	}
	if !completed || verbose {
		localNoData := summary.Files.Total == 0 && summary.Storage.Total.Bytes == 0
		localSummaryComplete := localNoData || (summary.Files.Progress.Percent >= 1.0 && summary.Storage.Progress.Percent >= 1.0)
		if localSummaryComplete && !verbose {
			fmt.Printf("%s %d files  %s\n", u.orange("Local:"), summary.Files.Total, hrBytes(summary.Storage.Total.Bytes))
		} else {
			filesBar, filesPct := formatProgress(summary.Files)
			storageBar, storagePct := formatStorageProgress(summary.Storage.Progress, summary.Storage.Synced.Bytes, summary.Storage.Total.Bytes)
			fmt.Println(u.orange("Local:"))
			printAlignedLines("  ", []progressLine{
				{"files:    ", fmt.Sprintf("%d/%d", summary.Files.Count, summary.Files.Total), colorBar(u, filesBar), filesPct, ""},
				{"storage:  ", fmt.Sprintf("%s/%s", hrBytes(summary.Storage.Synced.Bytes), hrBytes(summary.Storage.Total.Bytes)), colorBar(u, storageBar), storagePct, ""},
			})
		}
	}

	printRemoteSummary(payload.Annex.Paths, u)

	if !completed || verbose {
		fmt.Printf("%s %s (%d bytes)\n", u.purple("Annex tmp:"), payload.Annex.Tmp.Size, payload.Annex.Tmp.Bytes)
		fmt.Printf("%s %d\n", u.purple("Downloading:"), payload.Annex.Downloads.Count)
		for _, f := range firstN(payload.Annex.Downloads.Files, 10) {
			fmt.Printf("  - %s\n", f)
		}
		fmt.Printf("%s %d\n", u.purple("Uploading:"), payload.Annex.Uploads.Count)
		for _, f := range firstN(payload.Annex.Uploads.Files, 10) {
			fmt.Printf("  - %s\n", f)
		}

		printBadFiles(payload.Annex.Bad, u)
	}

	if len(payload.Annex.Paths) == 0 {
		fmt.Println(u.pink("Paths:") + " (none)")
		return
	}
	fmt.Println(u.pink("Paths:"))
	for _, p := range payload.Annex.Paths {
		complete := isPathComplete(p)
		if complete && !verbose {
			// Collapsed single-line summary for complete paths
			fmt.Printf("  ✅ %s  %d files  %s\n", p.Path, p.Files.Total, hrBytes(p.Storage.Total.Bytes))
		} else {
			// Expanded detail for incomplete paths (or --verbose)
			prefix := "🔄"
			if complete {
				prefix = "✅"
			}
			fmt.Printf("  %s %s\n", prefix, p.Path)
			localComplete := (p.Files.Total == 0 && p.Storage.Total.Bytes == 0) || (p.Files.Progress.Percent >= 1.0 && p.Storage.Progress.Percent >= 1.0)
			if localComplete && !verbose {
				fmt.Printf("    %s %d files  %s\n", u.pink("local:"), p.Files.Total, hrBytes(p.Storage.Total.Bytes))
			} else {
				filesBar, filesPct := formatProgress(p.Files)
				storageBar, storagePct := formatStorageProgress(p.Storage.Progress, p.Storage.Synced.Bytes, p.Storage.Total.Bytes)
				remaining := remainingAnnotation(p.Storage.Progress.Percent, p.Storage.Synced.Bytes, p.Storage.Total.Bytes)
				fmt.Printf("    %s\n", u.pink("local:"))
				printAlignedLines("      ", []progressLine{
					{"files:    ", fmt.Sprintf("%d/%d", p.Files.Count, p.Files.Total), colorBar(u, filesBar), filesPct, ""},
					{"storage:  ", fmt.Sprintf("%s/%s", hrBytes(p.Storage.Synced.Bytes), hrBytes(p.Storage.Total.Bytes)), colorBar(u, storageBar), storagePct, remaining},
				})
			}
			if len(p.Remotes) > 0 {
				fmt.Printf("    %s\n", u.pink("remote:"))
				for _, remote := range p.Remotes {
					rFilesBar, rFilesPct := formatProgress(remote.Files)
					rStorageBar, rStoragePct := formatStorageProgress(remote.Storage.Progress, remote.Storage.Uploaded.Bytes, remote.Storage.Total.Bytes)
					rRemaining := remainingAnnotation(remote.Storage.Progress.Percent, remote.Storage.Uploaded.Bytes, remote.Storage.Total.Bytes)
					fmt.Printf("      - %s\n", remote.Name)
					printAlignedLines("        ", []progressLine{
						{"files:    ", fmt.Sprintf("%d/%d", remote.Files.Count, remote.Files.Total), colorBar(u, rFilesBar), rFilesPct, ""},
						{"storage:  ", fmt.Sprintf("%s/%s", hrBytes(remote.Storage.Uploaded.Bytes), hrBytes(remote.Storage.Total.Bytes)), colorBar(u, rStorageBar), rStoragePct, rRemaining},
					})
				}
			}
		}
	}
}

func isWorkflowTerminal(status string) bool {
	s := strings.ToLower(status)
	return s == "cancelled" || s == "canceled" || s == "terminated" || s == "failed" || s == "timed_out" || s == "timedout"
}

func printStatusSummary(payload metrics, u *ui, workflowStatus string) {
	badCount := payload.Annex.Bad.Count
	syncingCount := 0
	for _, p := range payload.Annex.Paths {
		if !isPathComplete(p) {
			syncingCount++
		}
	}
	activeTransfers := payload.Annex.Downloads.Count + payload.Annex.Uploads.Count
	hasMetrics := len(payload.Annex.Paths) > 0 || payload.Annex.Local.Files.Total > 0 || badCount > 0
	wfLower := strings.ToLower(workflowStatus)

	switch {
	case wfLower == "completed":
		fmt.Printf("%s %s %s\n", u.orange("Status:"), statusIcon("completed"), u.green("All paths fully synced"))
	case wfLower == "running" && !hasMetrics:
		fmt.Printf("%s %s\n", u.orange("Status:"), u.yellow("⏳ Metrics are being computed..."))
	case isWorkflowTerminal(workflowStatus) && !hasMetrics:
		statusColor := colorizeStatus(u, workflowStatus)
		icon := statusIcon(wfLower)
		fmt.Printf("%s %s %s — no metrics available\n", u.orange("Status:"), icon, statusColor)
	case isWorkflowTerminal(workflowStatus):
		lastSeen := ""
		if badCount > 0 {
			lastSeen = fmt.Sprintf("%d bad file(s), ", badCount)
		}
		if syncingCount > 0 {
			lastSeen += fmt.Sprintf("%d path(s) syncing", syncingCount)
		} else {
			lastSeen += "all paths fully synced"
		}
		statusColor := colorizeStatus(u, workflowStatus)
		icon := statusIcon(wfLower)
		fmt.Printf("%s %s %s — last seen: %s\n", u.orange("Status:"), icon, statusColor, lastSeen)
	case badCount > 0:
		fmt.Printf("%s %s\n", u.orange("Status:"), u.red(fmt.Sprintf("🔴 %d bad file(s) detected", badCount)))
	case syncingCount > 0:
		detail := fmt.Sprintf("🔄 %d path(s) syncing", syncingCount)
		if activeTransfers > 0 {
			detail += fmt.Sprintf(", %d active transfer(s)", activeTransfers)
		}
		fmt.Printf("%s %s\n", u.orange("Status:"), u.pink(detail))
	default:
		fmt.Printf("%s %s %s\n", u.orange("Status:"), statusIcon("completed"), u.green("All paths fully synced"))
	}
}

func isPathComplete(p pathMetrics) bool {
	if p.Files.Progress.Percent < 1.0 || p.Storage.Progress.Percent < 1.0 {
		return false
	}
	for _, r := range p.Remotes {
		if r.Files.Progress.Percent < 1.0 || r.Storage.Progress.Percent < 1.0 {
			return false
		}
	}
	return true
}

func colorBar(u *ui, bar string) string {
	filled := strings.TrimRight(bar, "○")
	empty := bar[len(filled):]
	return u.render(u.barFilledStyle, filled) + u.render(u.barEmptyStyle, empty)
}

func remainingAnnotation(percent float64, synced, total int64) string {
	if percent >= 1.0 || total <= 0 {
		return ""
	}
	remaining := total - synced
	if remaining <= 0 {
		return ""
	}
	return fmt.Sprintf(" (%s remaining)", hrBytes(remaining))
}

type progressLine struct {
	label  string // e.g. "files:    " or "storage:  "
	ratio  string // e.g. "133404/133404" or "11TB/11TB"
	bar    string // colored bar (may contain ANSI)
	pct    string // percentage text
	suffix string // e.g. " (1006GB remaining)"
}

func printAlignedLines(indent string, lines []progressLine) {
	maxRatio := 0
	for _, l := range lines {
		if len(l.ratio) > maxRatio {
			maxRatio = len(l.ratio)
		}
	}
	for _, l := range lines {
		fmt.Printf("%s%s%-*s %s %s%s\n", indent, l.label, maxRatio, l.ratio, l.bar, l.pct, l.suffix)
	}
}

func listMetricFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var files []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if len(name) == len("20060102T150405Z.json") && strings.HasSuffix(name, ".json") && strings.Contains(name, "T") {
			files = append(files, name)
		}
	}
	sort.Strings(files)
	return files
}

func printBadFiles(bad badMetrics, u *ui) {
	if bad.Count > 0 {
		fmt.Printf("%s %s\n", u.purple("Bad files:"), u.red(fmt.Sprintf("%d", bad.Count)))
	} else {
		fmt.Printf("%s %d\n", u.purple("Bad files:"), bad.Count)
	}
	for _, f := range firstN(bad.Files, 10) {
		fmt.Printf("  - %s\n", f)
	}
	if bad.Count > 10 {
		fmt.Printf("  - [%d more]\n", bad.Count-10)
	}
}

func printRemoteSummary(paths []pathMetrics, u *ui) {
	agg := aggregateRemotes(paths)
	if len(agg) == 0 {
		return
	}
	fmt.Println(u.orange("Remote:"))
	for _, remote := range agg {
		filesBar, filesPct := formatProgress(remote.Files)
		storageBar, storagePct := formatStorageProgress(remote.Storage.Progress, remote.Storage.Uploaded.Bytes, remote.Storage.Total.Bytes)
		fmt.Printf("  %s\n", remote.Name)
		printAlignedLines("    ", []progressLine{
			{"files:    ", fmt.Sprintf("%d/%d", remote.Files.Count, remote.Files.Total), colorBar(u, filesBar), filesPct, ""},
			{"storage:  ", fmt.Sprintf("%s/%s", hrBytes(remote.Storage.Uploaded.Bytes), hrBytes(remote.Storage.Total.Bytes)), colorBar(u, storageBar), storagePct, ""},
		})
	}
}

func aggregateRemotes(paths []pathMetrics) []remoteMetric {
	type agg struct {
		name          string
		filesTotal    int64
		filesCount    int64
		totalBytes    int64
		uploadedBytes int64
	}
	byName := map[string]*agg{}
	for _, p := range paths {
		for _, r := range p.Remotes {
			entry, ok := byName[r.Name]
			if !ok {
				entry = &agg{name: r.Name}
				byName[r.Name] = entry
			}
			entry.filesTotal += r.Files.Total
			entry.filesCount += r.Files.Count
			entry.totalBytes += r.Storage.Total.Bytes
			entry.uploadedBytes += r.Storage.Uploaded.Bytes
		}
	}
	var out []remoteMetric
	for _, entry := range byName {
		totalBytes := entry.totalBytes
		if totalBytes <= 0 && entry.uploadedBytes > 0 {
			totalBytes = entry.uploadedBytes
		}
		out = append(out, remoteMetric{
			Name: entry.name,
			Files: filesMetrics{
				Total:    entry.filesTotal,
				Count:    entry.filesCount,
				Progress: progressFromValues(entry.filesCount, entry.filesTotal),
			},
			Storage: remoteStorage{
				Uploaded: sizeValue{Bytes: entry.uploadedBytes, Human: hrBytes(entry.uploadedBytes)},
				Total:    sizeValue{Bytes: totalBytes, Human: hrBytes(totalBytes)},
				Progress: progressFromValues(entry.uploadedBytes, totalBytes),
			},
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func summarizePaths(paths []pathMetrics) summaryMetrics {
	totalFiles := int64(0)
	countFiles := int64(0)
	totalBytes := int64(0)
	syncedBytes := int64(0)
	for _, p := range paths {
		totalFiles += p.Files.Total
		countFiles += p.Files.Count
		totalBytes += p.Storage.Total.Bytes
		syncedBytes += p.Storage.Synced.Bytes
	}
	if totalBytes <= 0 && syncedBytes > 0 {
		totalBytes = syncedBytes
	}
	return summaryMetrics{
		Files: filesMetrics{
			Total:    totalFiles,
			Count:    countFiles,
			Progress: progressFromValues(countFiles, totalFiles),
		},
		Storage: storageMetrics{
			Total:    sizeValue{Bytes: totalBytes, Human: hrBytes(totalBytes)},
			Synced:   sizeValue{Bytes: syncedBytes, Human: hrBytes(syncedBytes)},
			Progress: progressFromValues(syncedBytes, totalBytes),
		},
	}
}

func formatProgress(metrics filesMetrics) (string, string) {
	return formatProgressFromProgress(metrics.Progress, metrics.Count, metrics.Total)
}

func formatStorageProgress(progress progressValue, synced, total int64) (string, string) {
	return formatProgressFromProgress(progress, synced, total)
}

func formatProgressFromProgress(progress progressValue, count, total int64) (string, string) {
	// Always regenerate the bar with Unicode characters, ignoring
	// any pre-rendered ASCII bar from the JSON payload.
	gen := progressFromValues(count, total)
	bar := gen.Bar

	pct := ""
	if progress.Percent > 0 {
		pct = fmt.Sprintf("%.2f%%", progress.Percent*100)
	} else {
		pct = extractPercent(bar)
	}
	if pct == "" {
		pct = "0.00%"
	}
	barOnly := strings.Fields(bar)
	if len(barOnly) > 0 {
		bar = barOnly[0]
	}
	return bar, pct
}

func extractPercent(bar string) string {
	parts := strings.Fields(bar)
	if len(parts) >= 2 {
		return parts[1]
	}
	return ""
}

const barWidth = 15

func progressFromValues(count, total int64) progressValue {
	if total <= 0 {
		return progressValue{Percent: 0, Bar: strings.Repeat("○", barWidth) + " 0.00%"}
	}
	ratio := float64(count) / float64(total)
	if ratio < 0 {
		ratio = 0
	}
	if ratio > 1 {
		ratio = 1
	}
	filled := int(ratio*float64(barWidth) + 0.5)
	if filled < 0 {
		filled = 0
	}
	if filled > barWidth {
		filled = barWidth
	}
	// Reserve at least one empty dot until truly 100%
	if ratio < 1.0 && filled >= barWidth {
		filled = barWidth - 1
	}
	bar := strings.Repeat("●", filled) + strings.Repeat("○", barWidth-filled)
	return progressValue{
		Percent: ratio,
		Bar:     fmt.Sprintf("%s %.2f%%", bar, ratio*100),
	}
}

func validateHeartbeatManifest(path string) error {
	payload, err := manifestutil.ReadManifest(path)
	if err != nil {
		return err
	}
	normalized := manifestutil.NormalizeManifest(payload)
	manifestType, err := manifestutil.InferManifestType(normalized)
	if err != nil {
		return err
	}
	return manifestutil.ValidateManifestFile(manifestType, path)
}

func parseManifest(path string) (manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return manifest{}, fmt.Errorf("Manifest not found: %s", path)
	}
	var m manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return manifest{}, err
	}
	return m, nil
}

func hrBytes(n int64) string {
	units := []string{"B", "KB", "MB", "GB", "TB", "PB"}
	size := float64(n)
	unit := 0
	for size >= 1024 && unit < len(units)-1 {
		size /= 1024
		unit++
	}
	if size >= 10 || unit == 0 {
		return fmt.Sprintf("%d%s", int64(size), units[unit])
	}
	return fmt.Sprintf("%.1f%s", size+1e-9, units[unit])
}

func durCompact(seconds int) string {
	if seconds < 0 {
		seconds = 0
	}
	d := seconds / 86400
	seconds %= 86400
	h := seconds / 3600
	seconds %= 3600
	m := seconds / 60
	s := seconds % 60
	var out strings.Builder
	if d > 0 {
		out.WriteString(fmt.Sprintf("%dd", d))
	}
	if h > 0 || d > 0 {
		out.WriteString(fmt.Sprintf("%dh", h))
	}
	if m > 0 || h > 0 || d > 0 {
		out.WriteString(fmt.Sprintf("%dm", m))
	}
	out.WriteString(fmt.Sprintf("%ds", s))
	return out.String()
}

func firstN(input []string, n int) []string {
	if len(input) <= n {
		return input
	}
	return input[:n]
}

func fileExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
