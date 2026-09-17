package standalone

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// stackPIDs holds the PIDs of the three managed child processes.
type stackPIDs struct {
	Surreal    int `json:"surreal"`
	Versitygw  int `json:"versitygw"`
	ADBStandalone int `json:"adb_standalone"`
}

func pidfilePath(dataDir string) string {
	return filepath.Join(dataDir, "stack.pid.json")
}

func writePIDFile(dataDir string, pids stackPIDs) error {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	data, err := json.Marshal(pids)
	if err != nil {
		return err
	}
	return os.WriteFile(pidfilePath(dataDir), data, 0o600)
}

func readPIDFile(dataDir string) (stackPIDs, error) {
	data, err := os.ReadFile(pidfilePath(dataDir))
	if err != nil {
		return stackPIDs{}, err
	}
	var pids stackPIDs
	if err := json.Unmarshal(data, &pids); err != nil {
		return stackPIDs{}, fmt.Errorf("parse pid file: %w", err)
	}
	return pids, nil
}

func removePIDFile(dataDir string) {
	_ = os.Remove(pidfilePath(dataDir))
}
