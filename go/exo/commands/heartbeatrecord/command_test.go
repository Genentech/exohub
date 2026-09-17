package heartbeatrecord

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestHrBytesAndRate(t *testing.T) {
	if got := hrBytes(0); got != "0B" {
		t.Fatalf("hrBytes(0): %q", got)
	}
	if got := hrBytes(1536); got != "1.5KB" {
		t.Fatalf("hrBytes(1536): %q", got)
	}
	if got := hrRate(-5); got != "0B/s" {
		t.Fatalf("hrRate(-5): %q", got)
	}
}

func TestProgressBarClamp(t *testing.T) {
	progress := progressBar(5, 0)
	if progress.Bar != "---------- 0.00%" {
		t.Fatalf("progressBar: %q", progress.Bar)
	}
	progress = progressBar(12, 10)
	if progress.Percent != 1 {
		t.Fatalf("progressBar percent: %v", progress.Percent)
	}
}

func TestSplitAndUniqueHelpers(t *testing.T) {
	parts := splitCSVOrSpace("a, b\tc\n")
	if len(parts) != 3 || parts[0] != "a" || parts[1] != "b" || parts[2] != "c" {
		t.Fatalf("splitCSVOrSpace: %#v", parts)
	}

	unique := uniqueStrings([]string{"a", " ", "a", "b"})
	if len(unique) != 2 {
		t.Fatalf("uniqueStrings: %#v", unique)
	}

	if !equalStringSets([]string{"a", "b"}, []string{"b", "a"}) {
		t.Fatalf("equalStringSets expected true")
	}
	if equalStringSets([]string{"a"}, []string{"a", "b"}) {
		t.Fatalf("equalStringSets expected false")
	}
}

func TestClampNonNegative(t *testing.T) {
	if got := clampNonNegative(-1); got != 0 {
		t.Fatalf("clampNonNegative: %d", got)
	}
	if got := clampNonNegative(5); got != 5 {
		t.Fatalf("clampNonNegative: %d", got)
	}
}

func TestInt64FromAny(t *testing.T) {
	if got := int64FromAny(1); got != 1 {
		t.Fatalf("int64FromAny int: %d", got)
	}
	if got := int64FromAny(float64(2.9)); got != 2 {
		t.Fatalf("int64FromAny float: %d", got)
	}
	if got := int64FromAny("nope"); got != 0 {
		t.Fatalf("int64FromAny default: %d", got)
	}
}

func TestReadCounterAndCopyFile(t *testing.T) {
	dir := t.TempDir()
	src := dir + "/counter.txt"
	if err := os.WriteFile(src, []byte("123\n"), 0o644); err != nil {
		t.Fatalf("write counter: %v", err)
	}
	if got := readCounter(src); got != 123 {
		t.Fatalf("readCounter: %d", got)
	}
	dst := dir + "/copy.txt"
	if err := copyFile(src, dst); err != nil {
		t.Fatalf("copyFile: %v", err)
	}
	if got := readCounter(dst); got != 123 {
		t.Fatalf("copied value: %d", got)
	}
}

func TestDuBytesUsesCommand(t *testing.T) {
	dir := t.TempDir()
	withCommand(t, func(name string, args ...string) *exec.Cmd {
		if name == "du" {
			return exec.Command("sh", "-c", "printf '456\t/path'")
		}
		return exec.Command("sh", "-c", "exit 1")
	})
	if got := duBytes(dir); got != 456 {
		t.Fatalf("duBytes: %d", got)
	}
}

func TestBuildPathMetricsWithRemote(t *testing.T) {
	repoDir := t.TempDir()
	metricsDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoDir, "data"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	filePath := filepath.Join(repoDir, "data", "a.txt")
	if err := os.WriteFile(filePath, []byte("data"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	outputs := map[string]string{
		cmdKey("git", "annex", "info", "--json", "--bytes", "--fast", "--", "data/a.txt"): `{"size of annexed files in working tree":1000,"annexed files in working tree":1}`,
		cmdKey("git", "annex", "find", "--in=here", "--format", "${bytesize}\n", "--", "data/a.txt"): "500\n",
		cmdKey("git", "annex", "find", "--in", "remote-a", "--format", "${bytesize}\n", "--", "data/a.txt"): "200\n",
	}
	calls := map[string]int{}
	withCommandOutputs(t, outputs, calls)

	metrics := buildPathMetrics(repoDir, []string{"data/*.txt"}, []string{"remote-a"}, metricsDir)
	if len(metrics) != 1 {
		t.Fatalf("expected 1 path metric, got %d", len(metrics))
	}
	pm := metrics[0]
	if pm.Files.Total != 1 || pm.Files.Count != 1 {
		t.Fatalf("files totals: %#v", pm.Files)
	}
	if pm.Storage.Total.Bytes != 1000 || pm.Storage.Synced.Bytes != 500 {
		t.Fatalf("storage totals: %#v", pm.Storage)
	}
	if pm.Storage.Progress.Percent != 0.5 {
		t.Fatalf("storage progress: %#v", pm.Storage.Progress)
	}
	if len(pm.Remotes) != 1 {
		t.Fatalf("expected 1 remote metric, got %d", len(pm.Remotes))
	}
	rm := pm.Remotes[0]
	if rm.Name != "remote-a" {
		t.Fatalf("remote name: %s", rm.Name)
	}
	if rm.Storage.Uploaded.Bytes != 200 || rm.Storage.Total.Bytes != 1000 {
		t.Fatalf("remote storage: %#v", rm.Storage)
	}
	if rm.Storage.Progress.Percent != 0.2 {
		t.Fatalf("remote progress: %#v", rm.Storage.Progress)
	}
	assertCommandCalls(t, calls, outputs)
}

func TestBuildPathMetricsRemoteLargerThanLocal(t *testing.T) {
	// Simulates --fast returning a stale local total (only files present
	// locally = 36GB) while the remote has the full dataset (2.1TB).
	repoDir := t.TempDir()
	metricsDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoDir, "data"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	filePath := filepath.Join(repoDir, "data", "a.txt")
	if err := os.WriteFile(filePath, []byte("data"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	localTotal := int64(36_000_000_000)   // 36GB from --fast
	localSynced := int64(36_000_000_000)  // 36GB present
	remoteBytes := int64(2_100_000_000_000) // 2.1TB on remote

	outputs := map[string]string{
		cmdKey("git", "annex", "info", "--json", "--bytes", "--fast", "--", "data/a.txt"): `{"size of annexed files in working tree":` + strconv.FormatInt(localTotal, 10) + `,"annexed files in working tree":35}`,
		cmdKey("git", "annex", "find", "--in=here", "--format", "${bytesize}\n", "--", "data/a.txt"): strconv.FormatInt(localSynced, 10) + "\n",
		cmdKey("git", "annex", "find", "--in", "s5-annex", "--format", "${bytesize}\n", "--", "data/a.txt"): strconv.FormatInt(remoteBytes, 10) + "\n",
	}
	calls := map[string]int{}
	withCommandOutputs(t, outputs, calls)

	metrics := buildPathMetrics(repoDir, []string{"data/*.txt"}, []string{"s5-annex"}, metricsDir)
	if len(metrics) != 1 {
		t.Fatalf("expected 1 path metric, got %d", len(metrics))
	}
	pm := metrics[0]

	// Remote total should be remoteBytes (larger than localTotal).
	rm := pm.Remotes[0]
	if rm.Storage.Total.Bytes != remoteBytes {
		t.Fatalf("remote total: got %d, want %d", rm.Storage.Total.Bytes, remoteBytes)
	}
	if rm.Storage.Uploaded.Bytes != remoteBytes {
		t.Fatalf("remote uploaded: got %d, want %d", rm.Storage.Uploaded.Bytes, remoteBytes)
	}

	// Local total should be backfilled from the remote.
	if pm.Storage.Total.Bytes != remoteBytes {
		t.Fatalf("local total should be backfilled from remote: got %d, want %d", pm.Storage.Total.Bytes, remoteBytes)
	}
	if pm.Storage.Synced.Bytes != localSynced {
		t.Fatalf("local synced: got %d, want %d", pm.Storage.Synced.Bytes, localSynced)
	}
}

func TestBuildPathMetricsLocalZeroRemoteNonZero(t *testing.T) {
	// Simulates local total=0, synced=0 (no files downloaded yet)
	// while remote has all files.
	repoDir := t.TempDir()
	metricsDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoDir, "data"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	filePath := filepath.Join(repoDir, "data", "a.txt")
	if err := os.WriteFile(filePath, []byte("data"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	remoteBytes := int64(275_000_000) // 275MB on remote

	outputs := map[string]string{
		cmdKey("git", "annex", "info", "--json", "--bytes", "--fast", "--", "data/a.txt"): `{"size of annexed files in working tree":0,"annexed files in working tree":3}`,
		cmdKey("git", "annex", "find", "--in=here", "--format", "${bytesize}\n", "--", "data/a.txt"): "",
		cmdKey("git", "annex", "find", "--in", "s5-annex", "--format", "${bytesize}\n", "--", "data/a.txt"): strconv.FormatInt(remoteBytes, 10) + "\n",
	}
	calls := map[string]int{}
	withCommandOutputs(t, outputs, calls)

	metrics := buildPathMetrics(repoDir, []string{"data/*.txt"}, []string{"s5-annex"}, metricsDir)
	if len(metrics) != 1 {
		t.Fatalf("expected 1 path metric, got %d", len(metrics))
	}
	pm := metrics[0]

	// Remote total should equal remoteBytes.
	rm := pm.Remotes[0]
	if rm.Storage.Total.Bytes != remoteBytes {
		t.Fatalf("remote total: got %d, want %d", rm.Storage.Total.Bytes, remoteBytes)
	}

	// Local total should be backfilled from remote (not 0).
	if pm.Storage.Total.Bytes != remoteBytes {
		t.Fatalf("local total should be backfilled from remote: got %d, want %d", pm.Storage.Total.Bytes, remoteBytes)
	}
	if pm.Storage.Synced.Bytes != 0 {
		t.Fatalf("local synced: got %d, want 0", pm.Storage.Synced.Bytes)
	}
}

func TestAggregateRemotesAndSummaries(t *testing.T) {
	paths := []pathMetrics{
		{
			Path: "a",
			Files: filesMetrics{Total: 2, Count: 1},
			Storage: storageMetrics{
				Total:  sizeValue{Bytes: 300},
				Synced: sizeValue{Bytes: 100},
			},
			Remotes: []remoteMetric{
				{
					Name: "r1",
					Files: filesMetrics{Total: 2, Count: 1},
					Storage: remoteStorage{
						Total:    sizeValue{Bytes: 300},
						Uploaded: sizeValue{Bytes: 120},
					},
				},
			},
		},
		{
			Path: "b",
			Files: filesMetrics{Total: 3, Count: 2},
			Storage: storageMetrics{
				Total:  sizeValue{Bytes: 700},
				Synced: sizeValue{Bytes: 500},
			},
			Remotes: []remoteMetric{
				{
					Name: "r1",
					Files: filesMetrics{Total: 3, Count: 2},
					Storage: remoteStorage{
						Total:    sizeValue{Bytes: 700},
						Uploaded: sizeValue{Bytes: 380},
					},
				},
			},
		},
	}

	summary := summarizePaths(paths)
	if summary.Files.Total != 5 || summary.Files.Count != 3 {
		t.Fatalf("summary files: %#v", summary.Files)
	}
	if summary.Storage.Total.Bytes != 1000 || summary.Storage.Synced.Bytes != 600 {
		t.Fatalf("summary storage: %#v", summary.Storage)
	}

	remotes := aggregateRemotes(paths)
	if len(remotes) != 1 {
		t.Fatalf("expected 1 remote, got %d", len(remotes))
	}
	remote := remotes[0]
	if remote.Name != "r1" {
		t.Fatalf("remote name: %s", remote.Name)
	}
	if remote.Files.Total != 5 || remote.Files.Count != 3 {
		t.Fatalf("remote files: %#v", remote.Files)
	}
	if remote.Storage.Total.Bytes != 1000 || remote.Storage.Uploaded.Bytes != 500 {
		t.Fatalf("remote storage: %#v", remote.Storage)
	}
}

func TestCurrentTransfers(t *testing.T) {
	outputs := map[string]string{
		cmdKey("git", "annex", "info", "-F", "--json"): `{"transfers in progress":[{"transfer":"upload","file":"b.txt"},{"transfer":"download","file":"a.txt"},{"transfer":"upload","file":"c.txt"}]}`,
	}
	calls := map[string]int{}
	withCommandOutputs(t, outputs, calls)

	downloads, uploads := currentTransfers(t.TempDir())
	if len(downloads) != 1 || downloads[0] != "a.txt" {
		t.Fatalf("downloads: %#v", downloads)
	}
	if len(uploads) != 2 || uploads[0] != "b.txt" || uploads[1] != "c.txt" {
		t.Fatalf("uploads: %#v", uploads)
	}
	assertCommandCalls(t, calls, outputs)
}

func TestLoadBadFilesLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad")
	var lines []string
	for i := 0; i < 55; i++ {
		lines = append(lines, "file-"+strconv.Itoa(i))
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatalf("write bad: %v", err)
	}
	files, count := loadBadFiles(path, 50)
	if count != 55 {
		t.Fatalf("count: %d", count)
	}
	if len(files) != 50 {
		t.Fatalf("files len: %d", len(files))
	}
	if files[0] != "file-0" {
		t.Fatalf("first file: %s", files[0])
	}
}

func withCommand(t *testing.T, fn func(name string, args ...string) *exec.Cmd) {
	t.Helper()
	old := command
	command = fn
	t.Cleanup(func() { command = old })
}

func TestManifestRemotesAnnex(t *testing.T) {
	path := writeTempManifest(t, `name: sample
url: https://example.com
ref: HEAD
remote-type: annex
repo-dir: /data/repo
with-remotes:
  - remote-a
  - remote-b
`)
	got, err := manifestRemotes(path)
	if err != nil {
		t.Fatalf("manifestRemotes annex error: %v", err)
	}
	if len(got) != 2 || got[0] != "remote-a" || got[1] != "remote-b" {
		t.Fatalf("manifestRemotes annex: %#v", got)
	}
}

func TestManifestRemotesExport(t *testing.T) {
	path := writeTempManifest(t, `name: sample
url: https://example.com
ref: HEAD
remote-type: export
repo-dir: /data/repo
to: s3-export
`)
	got, err := manifestRemotes(path)
	if err != nil {
		t.Fatalf("manifestRemotes export error: %v", err)
	}
	if len(got) != 1 || got[0] != "s3-export" {
		t.Fatalf("manifestRemotes export: %#v", got)
	}
}

func TestManifestRemotesMissingType(t *testing.T) {
	path := writeTempManifest(t, `name: sample
url: https://example.com
ref: HEAD
repo-dir: /data/repo
`)
	_, err := manifestRemotes(path)
	if err == nil {
		t.Fatalf("expected error for missing remote-type")
	}
}

func TestManifestRemotesExportMissingTo(t *testing.T) {
	path := writeTempManifest(t, `name: sample
url: https://example.com
ref: HEAD
remote-type: export
repo-dir: /data/repo
`)
	_, err := manifestRemotes(path)
	if err == nil {
		t.Fatalf("expected error for missing to")
	}
}

func TestManifestRemotesUnknownField(t *testing.T) {
	path := writeTempManifest(t, `name: sample
url: https://example.com
ref: HEAD
remote-type: annex
repo-dir: /data/repo
with:
  - remote-a
`)
	_, err := manifestRemotes(path)
	if err == nil {
		t.Fatalf("expected error for unknown field")
	}
}

func TestBuildPathMetricsSingleFile(t *testing.T) {
	// When a path is a single file (not a directory), git annex info
	// returns {"key":..., "size":...} instead of directory-level fields.
	// Verify that file count is correctly reported as 1/1.
	repoDir := t.TempDir()
	metricsDir := t.TempDir()
	filePath := filepath.Join(repoDir, "index.duckdb")
	if err := os.WriteFile(filePath, []byte("db"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	outputs := map[string]string{
		cmdKey("git", "annex", "info", "--json", "--bytes", "--fast", "--", "index.duckdb"): `{"key":"SHA256E-s123000000--abc.duckdb","size":123000000,"file":"index.duckdb","present":true}`,
		cmdKey("git", "annex", "find", "--in=here", "--format", "${bytesize}\n", "--", "index.duckdb"):                  "123000000\n",
		cmdKey("git", "annex", "find", "--in", "s5-annex", "--format", "${bytesize}\n", "--", "index.duckdb"):            "123000000\n",
	}
	calls := map[string]int{}
	withCommandOutputs(t, outputs, calls)

	metrics := buildPathMetrics(repoDir, []string{"index.duckdb"}, []string{"s5-annex"}, metricsDir)
	if len(metrics) != 1 {
		t.Fatalf("expected 1 path metric, got %d", len(metrics))
	}
	pm := metrics[0]
	if pm.Files.Total != 1 {
		t.Errorf("files total = %d, want 1", pm.Files.Total)
	}
	if pm.Files.Count != 1 {
		t.Errorf("files count = %d, want 1", pm.Files.Count)
	}
	if pm.Files.Progress.Percent != 1.0 {
		t.Errorf("files progress = %v, want 1.0", pm.Files.Progress.Percent)
	}
	if pm.Storage.Total.Bytes != 123000000 {
		t.Errorf("storage total = %d, want 123000000", pm.Storage.Total.Bytes)
	}
	if pm.Storage.Synced.Bytes != 123000000 {
		t.Errorf("storage synced = %d, want 123000000", pm.Storage.Synced.Bytes)
	}
	if len(pm.Remotes) != 1 {
		t.Fatalf("expected 1 remote, got %d", len(pm.Remotes))
	}
	rm := pm.Remotes[0]
	if rm.Files.Total != 1 || rm.Files.Count != 1 {
		t.Errorf("remote files = %d/%d, want 1/1", rm.Files.Count, rm.Files.Total)
	}
	assertCommandCalls(t, calls, outputs)
}

func writeTempManifest(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := dir + "/manifest.yaml"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return path
}

func withCommandOutputs(t *testing.T, outputs map[string]string, calls map[string]int) {
	t.Helper()
	withCommand(t, func(name string, args ...string) *exec.Cmd {
		key := cmdKey(name, args...)
		calls[key]++
		out, ok := outputs[key]
		if !ok {
			out = ""
		}
		cmd := exec.Command("sh", "-c", "printf '%s' "+shQuote(out))
		return cmd
	})
}

func assertCommandCalls(t *testing.T, calls map[string]int, outputs map[string]string) {
	t.Helper()
	for key := range outputs {
		if calls[key] == 0 {
			t.Fatalf("expected command not called: %s", key)
		}
	}
}

func cmdKey(name string, args ...string) string {
	return name + "\x1f" + strings.Join(args, "\x1f")
}

func shQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
