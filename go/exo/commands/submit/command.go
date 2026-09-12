package submit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	manifestutil "github.com/Genentech/exohub/go/exo/commandutil"
	"github.com/Genentech/exohub/go/exo/internal/defaults"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const longText = `Submit a manifest to the ExoHub API.

Reads the manifest file to determine the target queue and submits the job.
Supported queues: exohub-sync-aws, exohub-sync-shpc.`

var allowedQueues = map[string]struct{}{
	"exohub-sync-aws":  {},
	"exohub-sync-shpc": {},
}

func NewCommand() *cobra.Command {
	var manifestPath string
	var queue string

	rootCmd := &cobra.Command{
		Use:   "submit",
		Short: "Submit a manifest to the ExoHub API",
		Long:  longText,
		Run: func(cmd *cobra.Command, args []string) {
			if len(args) > 0 {
				_ = cmd.Help()
				os.Exit(1)
			}
			if manifestPath == "" {
				fmt.Fprintln(os.Stderr, "--manifest is required")
				os.Exit(2)
			}
			payload, err := submitManifest(manifestPath, queue, http.DefaultClient)
			if err != nil {
				fmt.Fprintln(os.Stderr, err.Error())
				os.Exit(1)
			}
			if payload == "" {
				fmt.Fprintln(cmd.OutOrStdout(), "Submission started")
				return
			}
			// Parse response to display workflow_id and run_id
			var resp map[string]any
			if err := json.Unmarshal([]byte(payload), &resp); err == nil {
				wfID, _ := resp["workflow_id"].(string)
				runID, _ := resp["run_id"].(string)
				if wfID != "" {
					if runID != "" {
						fmt.Fprintf(cmd.OutOrStdout(), "Workflow: %s (run: %s)\n", wfID, runID)
					} else {
						fmt.Fprintf(cmd.OutOrStdout(), "Workflow: %s\n", wfID)
					}
					return
				}
			}
			fmt.Fprintln(cmd.OutOrStdout(), payload)
		},
	}

	rootCmd.Flags().StringVar(&manifestPath, "manifest", "", "Path to manifest YAML/JSON")
	rootCmd.Flags().StringVar(&queue, "queue", "", "Task queue (exohub-sync-aws or exohub-sync-shpc)")

	return rootCmd
}

func submitManifest(manifestPath, queueFlag string, client *http.Client) (string, error) {
	if err := validateSubmitManifest(manifestPath); err != nil {
		return "", err
	}
	content, err := os.ReadFile(filepath.Clean(manifestPath))
	if err != nil {
		return "", err
	}
	manifestQueue, err := queueFromManifest(content)
	if err != nil {
		return "", err
	}
	queue, err := resolveQueue(queueFlag, manifestQueue)
	if err != nil {
		return "", err
	}
	apiBase := defaults.APIBase()
	endpoint, err := buildManifestURL(apiBase, queue)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(content))
	if err != nil {
		return "", err
	}
	manifestutil.DebugHTTP(http.MethodPost, endpoint)

	// Set Content-Type based on file extension
	if strings.HasSuffix(manifestPath, ".json") {
		req.Header.Set("Content-Type", "application/json")
	} else {
		req.Header.Set("Content-Type", "application/x-yaml")
	}

	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	} else if client.Timeout == 0 {
		client.Timeout = 60 * time.Second
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("Failed to submit manifest: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	payload := strings.TrimSpace(string(body))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if payload == "" {
			return "", fmt.Errorf("Submit failed: %s", resp.Status)
		}
		return "", fmt.Errorf("Submit failed: %s", payload)
	}
	return payload, nil
}

func validateSubmitManifest(path string) error {
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

func queueFromManifest(data []byte) (string, error) {
	var manifest map[string]any
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return "", fmt.Errorf("Failed to parse manifest: %w", err)
	}
	if manifest == nil {
		return "", nil
	}
	raw, ok := manifest["queue"]
	if !ok {
		return "", nil
	}
	queue, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("queue must be a string")
	}
	return strings.TrimSpace(queue), nil
}

func resolveQueue(flagQueue, manifestQueue string) (string, error) {
	queue := strings.TrimSpace(flagQueue)
	if queue == "" {
		queue = strings.TrimSpace(manifestQueue)
	}
	if queue == "" {
		return "", fmt.Errorf("queue is required (manifest or --queue)")
	}
	if _, ok := allowedQueues[queue]; !ok {
		return "", fmt.Errorf("unsupported queue: %s", queue)
	}
	return queue, nil
}

func buildManifestURL(apiBase, queue string) (string, error) {
	base := strings.TrimRight(apiBase, "/")
	endpoint := base + "/manifest"
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("Invalid EXOHUB_API_URL: %s", apiBase)
	}
	query := parsed.Query()
	query.Set("queue", queue)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}
