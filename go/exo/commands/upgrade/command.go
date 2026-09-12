package upgrade

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Genentech/exohub/go/exo/commandutil"
	"github.com/Genentech/exohub/go/exo/internal/defaults"
)

func NewCommand() *cobra.Command {
	var version string
	cmd := &cobra.Command{
		Use:   "upgrade [version]",
		Short: "Upgrade exo CLI",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 1 {
				return fmt.Errorf("unexpected argument: %s", args[1])
			}
			if len(args) == 1 {
				if version != "" {
					return fmt.Errorf("version specified via both flag and argument")
				}
				version = args[0]
			}

			upgradeURL := defaults.UpgradeURL()
			if upgradeURL == "" {
				return fmt.Errorf("EXO_UPGRADE_URL is not set; set it to the URL of the upgrade script")
			}
			if !strings.HasPrefix(upgradeURL, "https://") {
				return fmt.Errorf("EXO_UPGRADE_URL must use https://, got: %s", upgradeURL)
			}

			destDir, err := currentBinaryDir()
			if err != nil {
				return fmt.Errorf("determine current binary location: %w", err)
			}
			debugf("upgrade dest dir: %s", destDir)

			scriptPath, cleanup, err := downloadUpgradeScript(upgradeURL)
			if err != nil {
				return err
			}
			defer cleanup()
			debugf("upgrade script path: %s", scriptPath)

			execArgs := []string{scriptPath, "--dest", destDir}
			if version != "" {
				execArgs = append(execArgs, "--version", version)
			}
			debugf("upgrade command: sh %s", strings.Join(execArgs, " "))

			execCmd := commandutil.Command("sh", execArgs...)
			execCmd.Stdout = cmd.OutOrStdout()
			execCmd.Stderr = cmd.ErrOrStderr()
			execCmd.Stdin = os.Stdin
			return execCmd.Run()
		},
	}
	cmd.Flags().StringVar(&version, "version", "", "upgrade to a specific version")
	return cmd
}

func downloadUpgradeScript(url string) (string, func(), error) {
	debugf("upgrade URL: %s", url)
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", nil, fmt.Errorf("fetch upgrade script: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", nil, fmt.Errorf("upgrade script request failed: %s", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, fmt.Errorf("read upgrade script: %w", err)
	}

	tempDir, err := os.MkdirTemp("", "exo-upgrade-*")
	if err != nil {
		return "", nil, fmt.Errorf("create temp dir: %w", err)
	}
	debugf("upgrade temp dir: %s", tempDir)
	cleanup := func() {
		_ = os.RemoveAll(tempDir)
	}

	scriptPath := filepath.Join(tempDir, "upgrade.sh")
	if err := os.WriteFile(scriptPath, body, 0644); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("write upgrade script: %w", err)
	}
	if err := os.Chmod(scriptPath, 0755); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("chmod upgrade script: %w", err)
	}

	return scriptPath, cleanup, nil
}

func currentBinaryDir() (string, error) {
	execPath, err := os.Executable()
	if err != nil {
		return "", err
	}
	absPath, err := filepath.EvalSymlinks(execPath)
	if err != nil {
		return "", err
	}
	return filepath.Dir(absPath), nil
}

func debugf(format string, args ...any) {
	if os.Getenv("EXOHUB_CLI_DEBUG") != "1" {
		return
	}
	fmt.Fprintf(os.Stderr, "DEBUG: "+format+"\n", args...)
}
