package standalone

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

func newIngestCommand() *cobra.Command {
	var (
		adbURL    string
		tenant    string
		projectID string
		version   string
		s3URL     string
	)

	cmd := &cobra.Command{
		Use:   "ingest <bundle-dir>",
		Short: "Catalog a new project version from a bundle directory",
		Long: `Publish a new project version to the running adb-standalone catalog.

Reads metadata from <bundle-dir> and posts to POST /v1/{tenant}/project/ingest.
The server computes _extra fields (id, gprn, tenant, permissions, latest).

<bundle-dir> must be a directory in the exo bundle format (bundle.json + per-file .json metadata).

Wraps: POST /v1/{tenant}/project/ingest`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runIngest(adbURL, tenant, projectID, version, s3URL, args[0])
		},
	}

	cmd.Flags().StringVar(&adbURL, "url", "http://localhost:8080", "adb-standalone base URL")
	cmd.Flags().StringVar(&tenant, "tenant", "exohub", "ADB tenant path segment")
	cmd.Flags().StringVar(&projectID, "project-id", "", "Project ID (required)")
	cmd.Flags().StringVar(&version, "version", "", "Version string, e.g. v1.0.0 (required)")
	cmd.Flags().StringVar(&s3URL, "s3-url", "", "S3 bundle location (s3://bucket/key), optional if server auto-detects")

	_ = cmd.MarkFlagRequired("project-id")
	_ = cmd.MarkFlagRequired("version")

	return cmd
}

func runIngest(adbURL, tenant, projectID, version, s3URL, bundleDir string) error {
	absDir, err := filepath.Abs(bundleDir)
	if err != nil {
		return fmt.Errorf("resolve bundle dir: %w", err)
	}
	if _, err := os.Stat(absDir); err != nil {
		return fmt.Errorf("bundle dir %q: %w", absDir, err)
	}

	body := map[string]string{
		"project_id":  projectID,
		"version":     version,
		"s3_location": absDir,
	}
	if s3URL != "" {
		body["s3url"] = s3URL
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/v1/%s/project/ingest", strings.TrimRight(adbURL, "/"), tenant)
	resp, err := http.Post(url, "application/json", bytes.NewReader(payload)) //nolint:noctx
	if err != nil {
		return fmt.Errorf("POST %s: %w", url, err)
	}
	defer resp.Body.Close()

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("ingest failed (HTTP %d): %v", resp.StatusCode, result)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}
