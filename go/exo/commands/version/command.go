package version

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Genentech/exohub/go/exo/internal/defaults"
	"github.com/Genentech/exohub/go/exo/internal/features"
)

const versionProject = "exocli"

func NewCommand(version, commit, date string) *cobra.Command {
	if version == "" {
		version = "dev"
	}
	var full bool
	var all bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print exo CLI version",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return fmt.Errorf("unexpected argument: %s", args[0])
			}
			output := version
			if full {
				output = formatFullVersion(version, commit, date)
			}
			if all {
				output = "exo: " + output
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), output)
			if all {
				printCompanionVersions(cmd.OutOrStdout(), full)
			}
			if version != "" && version != "dev" {
				if err := checkForUpdate(cmd.Context(), cmd.OutOrStdout(), version); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "Failed to check for updates: %s\n", err)
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&full, "full", false, "show version with build metadata")
	cmd.Flags().BoolVar(&all, "all", false, "show versions of all companion binaries")
	return cmd
}

func formatFullVersion(version, commit, date string) string {
	parts := make([]string, 0, 2)
	if commit != "" {
		parts = append(parts, "commit "+commit)
	}
	if date != "" {
		parts = append(parts, date)
	}
	var base string
	if len(parts) == 0 {
		base = version
	} else {
		base = fmt.Sprintf("%s (%s)", version, strings.Join(parts, ", "))
	}
	if tags := features.Enabled(); len(tags) > 0 {
		return base + " [features: " + strings.Join(tags, ", ") + "]"
	}
	return base
}

var companionBinaries = []struct {
	name         string
	binary       string
	args         []string
	supportsFull bool
}{
	{"git-annex-remote-s5cmd", "git-annex-remote-s5cmd", []string{"--version"}, true},
	{"exo-credential-helper", "exo-credential-helper", []string{"--version"}, true},
	{"git-annex-remote-artifactdb-export", "git-annex-remote-artifactdb-export", []string{"--version"}, true},
	{"s5cmd", "s5cmd", []string{"version"}, false},
	{"git-annex", "git-annex", []string{"version", "--raw"}, false},
}

func printCompanionVersions(out io.Writer, full bool) {
	for _, bin := range companionBinaries {
		args := bin.args
		if full && bin.supportsFull {
			args = append(append([]string{}, args...), "--full")
		}
		ver := getBinaryVersion(bin.binary, args)
		_, _ = fmt.Fprintf(out, "%s: %s\n", bin.name, ver)
	}
}

func getBinaryVersion(binary string, args []string) string {
	cmd := exec.Command(binary, args...)
	output, err := cmd.Output()
	if err != nil {
		return "not found"
	}
	ver := strings.TrimSpace(string(output))
	if ver == "" {
		return "unknown"
	}
	// Take only the first line
	if idx := strings.IndexByte(ver, '\n'); idx >= 0 {
		ver = ver[:idx]
	}
	return ver
}

func checkForUpdate(ctx context.Context, out io.Writer, current string) error {
	latest, err := fetchLatestVersion(ctx)
	if err != nil {
		return err
	}
	if compareVersions(latest, current) > 0 {
		_, _ = fmt.Fprintf(out, "A new release of exo is available: %s -> %s\n", current, latest)
		_, _ = fmt.Fprintln(out, "Run `exo upgrade` to install the latest version.")
	}
	return nil
}

func fetchLatestVersion(ctx context.Context) (string, error) {
	versionBase := defaults.VersionBaseURL()
	if versionBase == "" {
		return "", errors.New("version check unavailable: no version check URL configured (set EXOHUB_VERSION_CHECK_URL)")
	}
	url := fmt.Sprintf("%s/projects/%s/versions", versionBase, versionProject)
	if os.Getenv("EXOHUB_CLI_DEBUG") == "1" {
		fmt.Fprintf(os.Stderr, "DEBUG: version check URL: %s\n", url)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		msg := strings.TrimSpace(string(body))
		if msg != "" {
			return "", fmt.Errorf("request failed: %s: %s", resp.Status, msg)
		}
		return "", fmt.Errorf("request failed: %s", resp.Status)
	}

	var payload any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}

	latest := extractLatestVersion(payload)
	if latest == "" {
		return "", errors.New("latest version not found")
	}
	return latest, nil
}

func extractLatestVersion(payload any) string {
	root, ok := payload.(map[string]any)
	if !ok {
		return ""
	}
	latest, ok := root["latest"]
	if !ok {
		return ""
	}
	switch typed := latest.(type) {
	case string:
		return typed
	case map[string]any:
		if ver, ok := typed["_extra.version"].(string); ok && ver != "" {
			return ver
		}
		if ver, ok := typed["version"].(string); ok && ver != "" {
			return ver
		}
		if extra, ok := typed["_extra"].(map[string]any); ok {
			if ver, ok := extra["version"].(string); ok {
				return ver
			}
		}
	}
	if aggs, ok := root["aggs"].([]any); ok {
		latestVersion := ""
		for _, entry := range aggs {
			item, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			ver, _ := item["_extra.version"].(string)
			if ver == "" {
				if extra, ok := item["_extra"].(map[string]any); ok {
					ver, _ = extra["version"].(string)
				}
			}
			if ver == "" {
				continue
			}
			if latestVersion == "" || compareVersions(ver, latestVersion) > 0 {
				latestVersion = ver
			}
		}
		return latestVersion
	}
	return ""
}

func compareVersions(a, b string) int {
	a = normalizeVersion(a)
	b = normalizeVersion(b)
	if a == b {
		return 0
	}

	aParts, aSuffix, aOK := parseNumericVersion(a)
	bParts, bSuffix, bOK := parseNumericVersion(b)
	if aOK && bOK {
		maxParts := len(aParts)
		if len(bParts) > maxParts {
			maxParts = len(bParts)
		}
		for i := 0; i < maxParts; i++ {
			var av, bv int
			if i < len(aParts) {
				av = aParts[i]
			}
			if i < len(bParts) {
				bv = bParts[i]
			}
			if av > bv {
				return 1
			}
			if av < bv {
				return -1
			}
		}
		if aSuffix == "" && bSuffix != "" {
			return 1
		}
		if aSuffix != "" && bSuffix == "" {
			return -1
		}
		if aSuffix != "" || bSuffix != "" {
			return strings.Compare(aSuffix, bSuffix)
		}
		return 0
	}

	return strings.Compare(a, b)
}

func normalizeVersion(version string) string {
	version = strings.TrimSpace(version)
	version = strings.TrimPrefix(version, "v")
	version = strings.TrimPrefix(version, "V")
	if idx := strings.Index(strings.ToLower(version), "-snapshot"); idx >= 0 {
		version = version[:idx]
	}
	return version
}

func parseNumericVersion(version string) ([]int, string, bool) {
	if version == "" {
		return nil, "", false
	}
	if idx := strings.Index(version, "+"); idx >= 0 {
		version = version[:idx]
	}

	suffix := ""
	if idx := strings.Index(version, "-"); idx >= 0 {
		suffix = version[idx+1:]
		version = version[:idx]
	}

	parts := strings.Split(version, ".")
	values := make([]int, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			values = append(values, 0)
			continue
		}
		val, err := strconv.Atoi(part)
		if err != nil {
			return nil, "", false
		}
		values = append(values, val)
	}
	return values, suffix, true
}
