package standalone

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"
)

func newDownCommand() *cobra.Command {
	var dataDir string

	cmd := &cobra.Command{
		Use:   "down",
		Short: "Stop the adb-standalone stack",
		RunE: func(cmd *cobra.Command, args []string) error {
			if dataDir == "" {
				home, err := os.UserConfigDir()
				if err != nil {
					return fmt.Errorf("resolve config dir: %w", err)
				}
				dataDir = filepath.Join(home, "exo", "standalone")
			}
			return runDown(dataDir)
		},
	}

	cmd.Flags().StringVar(&dataDir, "data-dir", "", "Directory used by 'exo standalone up' (default: $HOME/.config/exo/standalone)")
	return cmd
}

func runDown(dataDir string) error {
	pids, err := readPIDFile(dataDir)
	if err != nil {
		return fmt.Errorf("no running stack found (read pid file: %w)", err)
	}

	stopped := 0
	for _, pid := range []int{pids.ADBStandalone, pids.Versitygw, pids.Surreal} {
		if pid <= 0 {
			continue
		}
		proc, err := os.FindProcess(pid)
		if err != nil {
			continue
		}
		if err := proc.Signal(syscall.SIGTERM); err == nil {
			fmt.Fprintf(os.Stderr, "sent SIGTERM to pid %d\n", pid)
			stopped++
		}
	}

	// Give processes time to exit.
	if stopped > 0 {
		time.Sleep(2 * time.Second)
	}

	removePIDFile(dataDir)
	fmt.Fprintln(os.Stderr, "stack stopped")
	return nil
}
