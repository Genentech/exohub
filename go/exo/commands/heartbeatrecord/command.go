package heartbeatrecord

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/Genentech/exohub/go/exo/commandutil"
	manifestutil "github.com/Genentech/exohub/go/exo/commandutil"
)

var command = commandutil.Command

const longText = `Record exohub metrics into .git/exohub/metrics/.

Collects git-annex transfer progress, path-level sync status, network I/O, and
bad-file counts, then writes the result as JSON. Use --watch to record continuously.`

type manifest struct {
	Name       string   `yaml:"name"`
	URL        string   `yaml:"url"`
	Ref        string   `yaml:"ref"`
	RemoteType string   `yaml:"remote-type"`
	Queue      string   `yaml:"queue"`
	RepoDir    string   `yaml:"repo-dir"`
	With       []string `yaml:"with-remotes"`
	To         string   `yaml:"to"`
	From       string   `yaml:"from"`
	Paths      []string `yaml:"paths"`
	Path       string   `yaml:"path"`
}

func (m manifest) repoDir() string {
	return m.RepoDir
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
	Remotes        []remoteMetric  `json:"remotes"`
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
	var metricsDir string
	var outFile string
	var paths []string
	var smartPaths bool
	var watch bool
	var interval float64

	rootCmd := &cobra.Command{
		Use:   "record",
		Short: "Record exohub metrics into .git/exohub/metrics/",
		Long:  longText,
		Run: func(cmd *cobra.Command, args []string) {
			if len(args) > 0 {
				_ = cmd.Help()
				os.Exit(2)
			}
			runRecord(repoDir, manifestPath, metricsDir, outFile, paths, smartPaths, watch, interval)
		},
	}

	rootCmd.Flags().StringVar(&repoDir, "repo", "", "Repository root")
	rootCmd.Flags().StringVar(&manifestPath, "manifest", "", "Manifest file providing repo-dir")
	rootCmd.Flags().StringVar(&metricsDir, "metrics-dir", "", "Directory for metrics output")
	rootCmd.Flags().StringVar(&outFile, "out", "latest.json", "Output metrics filename")
	rootCmd.Flags().StringArrayVar(&paths, "path", nil, "Path or glob (repeatable)")
	rootCmd.Flags().BoolVar(&smartPaths, "smart-paths", os.Getenv("EXO_TRACK_SMART_PATHS") == "1", "Smart per-path recompute")
	rootCmd.Flags().BoolVar(&watch, "watch", false, "Continuously record snapshots")
	rootCmd.Flags().Float64Var(&interval, "interval", defaultInterval(), "Interval for --watch (seconds)")

	return rootCmd
}

func defaultInterval() float64 {
	if v := os.Getenv("EXO_TRACK_RECORD_INTERVAL"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return 2
}

func runRecord(repoDir, manifestPath, metricsDir, outFile string, paths []string, smartPaths, watch bool, interval float64) {
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
		if len(paths) == 0 {
			paths = append(paths, m.Paths...)
			if strings.TrimSpace(m.Path) != "" {
				paths = append(paths, m.Path)
			}
		}
		if repoDir == "." || repoDir == "" || repoDir == os.Getenv("PWD") {
			if manifestRepo := m.repoDir(); manifestRepo != "" {
				repoDir = manifestRepo
			}
		}
		if manifestRepo := m.repoDir(); manifestRepo != "" {
			repoDir = manifestRepo
		}
		// Compute manifest hash for metrics directory (if not explicitly set)
		if metricsDir == "" {
			hash, err := manifestutil.ManifestHash(manifestPath)
			if err != nil {
				fmt.Fprintln(os.Stderr, err.Error())
				os.Exit(1)
			}
			manifestHash = hash
		}
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

	if metricsDir == "" {
		metricsDir = filepath.Join(repoDir, ".git", "exohub", "metrics")
		if manifestHash != "" {
			metricsDir = filepath.Join(metricsDir, manifestHash)
		}
	}
	if err := os.MkdirAll(metricsDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
	badFile := filepath.Join(repoDir, ".git", "exohub", "bad")
	tmpDir := filepath.Join(repoDir, ".git", "annex", "tmp")

	manifestRemotes, err := manifestRemotes(manifestPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}

	for {
		start := time.Now().UTC()
		netIfaces := netListIfaces()
		rxStart, txStart := netTotalsBytes(netIfaces)

		tmpBytes := duBytes(tmpDir)
		tmpSize := hrBytes(tmpBytes)

		prevTmp, prevPaths, prevRemotes := loadPrevious(metricsDir, outFile)

		recomputePaths := shouldRecomputePaths(smartPaths, tmpBytes, prevTmp, prevRemotes, manifestRemotes, prevPaths)
		var pathMetricsList []pathMetrics
		if recomputePaths {
			pathMetricsList = buildPathMetrics(repoDir, paths, manifestRemotes, metricsDir)
		} else {
			pathMetricsList = prevPaths
		}

		downloads, uploads := currentTransfers(repoDir)
		badFiles, badCount := loadBadFiles(badFile, 50)

		summary := summarizePaths(pathMetricsList)
		remotes := aggregateRemotes(pathMetricsList)

		end := time.Now().UTC()
		rxEnd, txEnd := netTotalsBytes(netIfaces)
		netDuration := end.Sub(start).Seconds()
		if netDuration <= 0 {
			netDuration = 0.001
		}
		rxDelta := clampNonNegative(rxEnd - rxStart)
		txDelta := clampNonNegative(txEnd - txStart)
		rxRate := float64(rxDelta) / netDuration
		txRate := float64(txDelta) / netDuration

		ts := end.Format(time.RFC3339)
		metrics := metrics{
			Timestamp: ts,
			Annex: annexMetrics{
				Tmp: annexTmp{
					Timestamp: ts,
					Bytes:     tmpBytes,
					Size:      tmpSize,
				},
				Downloads: transferMetrics{
					Timestamp: ts,
					Files:     downloads,
					Count:     len(downloads),
				},
				Uploads: transferMetrics{
					Timestamp: ts,
					Files:     uploads,
					Count:     len(uploads),
				},
				RemotesTracked: manifestRemotes,
				Bad: badMetrics{
					Timestamp: ts,
					Files:     badFiles,
					Count:     badCount,
				},
				Paths:   pathMetricsList,
				Summary: summary,
				Local:   summary,
				Remotes: remotes,
			},
			Network: networkMetric{
				StartTS:  start.Format(time.RFC3339),
				EndTS:    end.Format(time.RFC3339),
				Duration: netDuration,
				Ifaces:   netIfaces,
				RX: trafficMetric{
					StartBytes: rxStart,
					EndBytes:   rxEnd,
					DeltaBytes: rxDelta,
					RateBps:    rxRate,
					RateHuman:  hrRate(rxRate),
				},
				TX: trafficMetric{
					StartBytes: txStart,
					EndBytes:   txEnd,
					DeltaBytes: txDelta,
					RateBps:    txRate,
					RateHuman:  hrRate(txRate),
				},
			},
		}

		outPath := filepath.Join(metricsDir, outFile)
		tmpPath := outPath + ".tmp"
		writeJSON(tmpPath, metrics)

		tsFile := filepath.Join(metricsDir, end.Format("20060102T150405Z")+".json")
		_ = copyFile(tmpPath, tsFile)

		_ = os.Rename(tmpPath, outPath)

		if !watch {
			break
		}
		time.Sleep(time.Duration(interval * float64(time.Second)))
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
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&m); err != nil {
		return manifest{}, err
	}
	return m, nil
}

func manifestRemotes(manifestPath string) ([]string, error) {
	if manifestPath == "" {
		return nil, nil
	}
	m, err := parseManifest(manifestPath)
	if err != nil {
		return nil, err
	}
	remoteType := strings.TrimSpace(m.RemoteType)
	if remoteType == "" {
		return nil, fmt.Errorf("Manifest missing remote-type")
	}
	switch remoteType {
	case "annex":
		return uniqueStrings(m.With), nil
	case "export":
		remote := strings.TrimSpace(m.To)
		if remote == "" {
			return nil, fmt.Errorf("Manifest remote-type export requires to")
		}
		return []string{remote}, nil
	default:
		return nil, fmt.Errorf("Unsupported remote-type: %s", remoteType)
	}
}

func shouldRecomputePaths(smart bool, tmpBytes, prevTmp int64, prevRemotes, currentRemotes []string, prevPaths []pathMetrics) bool {
	if !smart {
		return true
	}
	if len(prevPaths) == 0 {
		return true
	}
	if tmpBytes < prevTmp {
		return true
	}
	if !equalStringSets(prevRemotes, currentRemotes) && len(currentRemotes) > 0 {
		return true
	}
	if len(currentRemotes) > 0 {
		for _, p := range prevPaths {
			if len(p.Remotes) == 0 {
				return true
			}
		}
	}
	return false
}

func buildPathMetrics(repoDir string, paths []string, remotes []string, metricsDir string) []pathMetrics {
	var results []pathMetrics
	if len(paths) == 0 {
		return results
	}
	for _, p := range paths {
		matches := expandMatches(repoDir, p)
		if len(matches) == 0 {
			matches = []string{repoRelPath(repoDir, p)}
		}
		totalCount := int64(0)
		presentCount := int64(0)
		presentBytes := int64(0)
		totalBytes := int64(0)
		cachedTotal := cachedTotalBytes(metricsDir, p)
		useCache := cachedTotal > 0

		for _, match := range matches {
			match = repoRelPath(repoDir, match)
			infoTotalBytes, infoTotalCount := annexInfoTotals(repoDir, match)
			if !useCache {
				totalBytes += infoTotalBytes
			}
			totalCount += infoTotalCount

			bytes, count := annexFindTotals(repoDir, match, "here")
			presentBytes += bytes
			presentCount += count
		}

		if useCache {
			totalBytes = cachedTotal
		}
		if totalBytes <= 0 && presentBytes > 0 {
			totalBytes = presentBytes
		}

		filesProgress := progressBar(float64(presentCount), float64(totalCount))
		storageProgress := progressBar(float64(presentBytes), float64(totalBytes))

		pm := pathMetrics{
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			Path:      p,
			Files: filesMetrics{
				Total:    totalCount,
				Count:    presentCount,
				Progress: filesProgress,
			},
			Storage: storageMetrics{
				Total: sizeValue{
					Bytes: totalBytes,
					Human: hrBytes(totalBytes),
				},
				Synced: sizeValue{
					Bytes: presentBytes,
					Human: hrBytes(presentBytes),
				},
				Progress: storageProgress,
			},
		}

		if len(remotes) > 0 {
			pm.Remotes = buildRemoteMetrics(repoDir, remotes, matches, totalCount, totalBytes)
			// When --fast returns a stale local total (only counting
			// files present in the working tree), the remote that has
			// all files reports a larger byte count.  Use the max
			// remote total so the local progress bar reflects the
			// true dataset size still to be downloaded.
			for _, r := range pm.Remotes {
				if r.Storage.Total.Bytes > totalBytes {
					totalBytes = r.Storage.Total.Bytes
				}
			}
			if totalBytes != pm.Storage.Total.Bytes {
				pm.Storage.Total = sizeValue{
					Bytes: totalBytes,
					Human: hrBytes(totalBytes),
				}
				pm.Storage.Progress = progressBar(float64(presentBytes), float64(totalBytes))
			}
		}
		results = append(results, pm)
	}
	return results
}

func buildRemoteMetrics(repoDir string, remotes []string, matches []string, totalCount, totalBytes int64) []remoteMetric {
	var out []remoteMetric
	for _, remote := range remotes {
		remoteCount := int64(0)
		remoteBytes := int64(0)
		for _, match := range matches {
			match = repoRelPath(repoDir, match)
			bytes, count := annexFindTotals(repoDir, match, remote)
			remoteBytes += bytes
			remoteCount += count
		}
		rTotalBytes := totalBytes
		if remoteBytes > rTotalBytes {
			rTotalBytes = remoteBytes
		}
		filesProgress := progressBar(float64(remoteCount), float64(totalCount))
		storageProgress := progressBar(float64(remoteBytes), float64(rTotalBytes))
		out = append(out, remoteMetric{
			Name: remote,
			Files: filesMetrics{
				Total:    totalCount,
				Count:    remoteCount,
				Progress: filesProgress,
			},
			Storage: remoteStorage{
				Uploaded: sizeValue{
					Bytes: remoteBytes,
					Human: hrBytes(remoteBytes),
				},
				Total: sizeValue{
					Bytes: rTotalBytes,
					Human: hrBytes(rTotalBytes),
				},
				Progress: storageProgress,
			},
		})
	}
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
	filesProgress := progressBar(float64(countFiles), float64(totalFiles))
	storageProgress := progressBar(float64(syncedBytes), float64(totalBytes))
	return summaryMetrics{
		Files: filesMetrics{
			Total:    totalFiles,
			Count:    countFiles,
			Progress: filesProgress,
		},
		Storage: storageMetrics{
			Total: sizeValue{
				Bytes: totalBytes,
				Human: hrBytes(totalBytes),
			},
			Synced: sizeValue{
				Bytes: syncedBytes,
				Human: hrBytes(syncedBytes),
			},
			Progress: storageProgress,
		},
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
				Progress: progressBar(float64(entry.filesCount), float64(entry.filesTotal)),
			},
			Storage: remoteStorage{
				Uploaded: sizeValue{
					Bytes: entry.uploadedBytes,
					Human: hrBytes(entry.uploadedBytes),
				},
				Total: sizeValue{
					Bytes: totalBytes,
					Human: hrBytes(totalBytes),
				},
				Progress: progressBar(float64(entry.uploadedBytes), float64(totalBytes)),
			},
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func currentTransfers(repoDir string) ([]string, []string) {
	output, err := runCommandOutput(repoDir, "git", "annex", "info", "-F", "--json")
	if err != nil {
		return nil, nil
	}
	var payload struct {
		Transfers []struct {
			Transfer string `json:"transfer"`
			File     string `json:"file"`
		} `json:"transfers in progress"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		return nil, nil
	}
	var downloads []string
	var uploads []string
	for _, entry := range payload.Transfers {
		if entry.File == "" {
			continue
		}
		switch entry.Transfer {
		case "download":
			downloads = append(downloads, entry.File)
		case "upload":
			uploads = append(uploads, entry.File)
		}
	}
	sort.Strings(downloads)
	sort.Strings(uploads)
	return downloads, uploads
}

func loadBadFiles(path string, limit int) ([]string, int) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0
	}
	var files []string
	count := 0
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		count++
		if len(files) < limit {
			files = append(files, line)
		}
	}
	return files, count
}

func cachedTotalBytes(metricsDir, path string) int64 {
	data, err := os.ReadFile(filepath.Join(metricsDir, "latest.json"))
	if err != nil {
		return 0
	}
	var payload metrics
	if err := json.Unmarshal(data, &payload); err != nil {
		return 0
	}
	for _, p := range payload.Annex.Paths {
		if p.Path == path {
			return p.Storage.Total.Bytes
		}
	}
	return 0
}

func loadPrevious(metricsDir, outFile string) (int64, []pathMetrics, []string) {
	data, err := os.ReadFile(filepath.Join(metricsDir, "latest.json"))
	if err != nil {
		return 0, nil, nil
	}
	var payload metrics
	if err := json.Unmarshal(data, &payload); err != nil {
		return 0, nil, nil
	}
	return payload.Annex.Tmp.Bytes, payload.Annex.Paths, payload.Annex.RemotesTracked
}

func expandMatches(repoDir, pattern string) []string {
	p := pattern
	if !filepath.IsAbs(p) {
		p = filepath.Join(repoDir, p)
	}
	matches, err := filepath.Glob(p)
	if err != nil || len(matches) == 0 {
		return nil
	}
	var existing []string
	for _, match := range matches {
		if fileExists(match) {
			existing = append(existing, repoRelPath(repoDir, match))
		}
	}
	return existing
}

func repoRelPath(repoDir, path string) string {
	if path == "" {
		return path
	}
	abs := path
	if !filepath.IsAbs(path) {
		abs = filepath.Join(repoDir, path)
	}
	rel, err := filepath.Rel(repoDir, abs)
	if err != nil {
		return path
	}
	if rel == "." {
		return "."
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return path
	}
	return rel
}

func annexInfoTotals(repoDir, path string) (int64, int64) {
	out, err := runCommandOutput(repoDir, "git", "annex", "info", "--json", "--bytes", "--fast", "--", path)
	if err != nil || strings.TrimSpace(out) == "" {
		return 0, 0
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		return 0, 0
	}
	// git annex info on a single file returns {"key":..., "size":...}
	// instead of the directory-level fields. Detect this and return (size, 1).
	if _, hasKey := payload["key"]; hasKey {
		totalBytes := int64FromAny(payload["size"])
		return totalBytes, 1
	}
	totalBytes := int64FromAny(payload["size of annexed files in working tree"])
	totalCount := int64FromAny(payload["annexed files in working tree"])
	return totalBytes, totalCount
}

func annexFindTotals(repoDir, path, remote string) (int64, int64) {
	args := []string{"git", "annex", "find", "--format", "${bytesize}\n", "--", path}
	if remote == "here" {
		args = []string{"git", "annex", "find", "--in=here", "--format", "${bytesize}\n", "--", path}
	} else if remote != "" {
		args = []string{"git", "annex", "find", "--in", remote, "--format", "${bytesize}\n", "--", path}
	}
	out, err := runCommandOutput(repoDir, args...)
	if err != nil {
		return 0, 0
	}
	var totalBytes int64
	var count int64
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		if size, err := strconv.ParseInt(strings.TrimSpace(line), 10, 64); err == nil {
			totalBytes += size
		}
		count++
	}
	return totalBytes, count
}

func runCommandOutput(repoDir string, args ...string) (string, error) {
	cmd := command(args[0], args[1:]...)
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		if len(out) > 0 {
			fmt.Fprintf(os.Stderr, "command failed: %v\n%s", err, string(out))
		} else {
			fmt.Fprintf(os.Stderr, "command failed: %v\n", err)
		}
		os.Exit(1)
	}
	return string(out), err
}

func duBytes(path string) int64 {
	if !isDir(path) {
		return 0
	}
	out, err := command("du", "-s", "-B1", "-L", path).Output()
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return 0
	}
	val, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return 0
	}
	return val
}

func netListIfaces() []string {
	ifacesEnv := os.Getenv("EXO_TRACK_NET_IFACES")
	if ifacesEnv != "" {
		parts := splitCSVOrSpace(ifacesEnv)
		return parts
	}
	var ifaces []string
	netDir := "/sys/class/net"
	entries, err := os.ReadDir(netDir)
	if err != nil {
		return nil
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == "lo" {
			continue
		}
		rx := filepath.Join(netDir, name, "statistics", "rx_bytes")
		tx := filepath.Join(netDir, name, "statistics", "tx_bytes")
		if fileExists(rx) && fileExists(tx) {
			ifaces = append(ifaces, name)
		}
	}
	sort.Strings(ifaces)
	return ifaces
}

func netTotalsBytes(ifaces []string) (int64, int64) {
	var rx int64
	var tx int64
	for _, iface := range ifaces {
		rx += readCounter(filepath.Join("/sys/class/net", iface, "statistics", "rx_bytes"))
		tx += readCounter(filepath.Join("/sys/class/net", iface, "statistics", "tx_bytes"))
	}
	return rx, tx
}

func readCounter(path string) int64 {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	val, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0
	}
	return val
}

func hrRate(bps float64) string {
	rounded := int64(bps)
	if rounded < 0 {
		rounded = 0
	}
	return fmt.Sprintf("%s/s", hrBytes(rounded))
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

func progressBar(count, total float64) progressValue {
	if total <= 0 {
		return progressValue{Percent: 0, Bar: "---------- 0.00%"}
	}
	ratio := count / total
	if ratio < 0 {
		ratio = 0
	}
	if ratio > 1 {
		ratio = 1
	}
	filled := int(ratio*10 + 0.5)
	if filled < 0 {
		filled = 0
	}
	if filled > 10 {
		filled = 10
	}
	bar := strings.Repeat("#", filled) + strings.Repeat("-", 10-filled)
	pct := ratio * 100
	return progressValue{
		Percent: ratio,
		Bar:     fmt.Sprintf("%s %.2f%%", bar, pct),
	}
}

func writeJSON(path string, payload any) {
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return
	}
	_ = os.WriteFile(path, append(data, '\n'), 0o644)
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

func int64FromAny(val any) int64 {
	switch v := val.(type) {
	case int:
		return int64(v)
	case int64:
		return v
	case float64:
		return int64(v)
	case json.Number:
		n, _ := v.Int64()
		return n
	default:
		return 0
	}
}

func splitCSVOrSpace(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n'
	})
	var out []string
	for _, f := range fields {
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

func uniqueStrings(input []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, item := range input {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

func equalStringSets(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	a = uniqueStrings(a)
	b = uniqueStrings(b)
	sort.Strings(a)
	sort.Strings(b)
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func clampNonNegative(val int64) int64 {
	if val < 0 {
		return 0
	}
	return val
}

func fileExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
