package standalone

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

func newStatusCommand() *cobra.Command {
	var (
		dataDir       string
		surrealPort   int
		versitygwPort int
		adbPort       int
	)

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show health of the adb-standalone stack",
		RunE: func(cmd *cobra.Command, args []string) error {
			if dataDir == "" {
				home, err := os.UserConfigDir()
				if err != nil {
					return fmt.Errorf("resolve config dir: %w", err)
				}
				dataDir = filepath.Join(home, "exo", "standalone")
			}
			return runStatus(cmd, dataDir, surrealPort, versitygwPort, adbPort)
		},
	}

	cmd.Flags().StringVar(&dataDir, "data-dir", "", "Directory used by 'exo standalone up' (default: $HOME/.config/exo/standalone)")
	cmd.Flags().IntVar(&surrealPort, "surreal-port", 8000, "SurrealDB listen port")
	cmd.Flags().IntVar(&versitygwPort, "versitygw-port", 9100, "versitygw listen port")
	cmd.Flags().IntVar(&adbPort, "adb-port", 8080, "adb-standalone listen port")

	return cmd
}

type serviceStatus struct {
	name     string
	pid      int
	endpoint string
	running  bool
	healthy  bool
}

func runStatus(cmd *cobra.Command, dataDir string, surrealPort, vgwPort, adbPort int) error {
	pids, _ := readPIDFile(dataDir) // ignore error — may not be managed

	services := []serviceStatus{
		{name: "surrealdb", pid: pids.Surreal, endpoint: fmt.Sprintf("ws://localhost:%d", surrealPort)},
		{name: "versitygw", pid: pids.Versitygw, endpoint: fmt.Sprintf("http://localhost:%d", vgwPort)},
		{name: "adb-standalone", pid: pids.ADBStandalone, endpoint: fmt.Sprintf("http://localhost:%d", adbPort)},
	}

	for i := range services {
		s := &services[i]
		s.running = isPIDRunning(s.pid)
		s.healthy = checkHTTPHealth(s.endpoint)
	}

	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "SERVICE\tPID\tENDPOINT\tSTATUS")
	for _, s := range services {
		pid := "-"
		if s.pid > 0 {
			pid = fmt.Sprintf("%d", s.pid)
		}
		status := "stopped"
		if s.running && s.healthy {
			status = "healthy"
		} else if s.running {
			status = "running (unhealthy)"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", s.name, pid, s.endpoint, status)
	}
	return w.Flush()
}

// isPIDRunning returns true if the process with given pid is alive.
func isPIDRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// checkHTTPHealth returns true if GET <endpoint>/health returns 200.
func checkHTTPHealth(endpoint string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	url := endpoint + "/health"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
