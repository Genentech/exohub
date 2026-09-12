package standalone

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"
)

func newUpCommand() *cobra.Command {
	var (
		dataDir       string
		surrealBin    string
		adbBin        string
		configFile    string
		surrealPort   int
		versitygwPort int
		adbPort       int
		accessKey     string
		secretKey     string
		bucket        string
	)

	cmd := &cobra.Command{
		Use:   "up",
		Short: "Bring up the full adb-standalone stack (foreground, supervised)",
		Long: `Launch and supervise the three-process adb-standalone stack:
  1. surrealdb     — document store
  2. versitygw     — S3-compatible object store (POSIX-FS, Apache-2.0)
  3. adb-standalone — ArtifactDB API server

Blocks in the foreground. SIGINT/SIGTERM stops all children gracefully.
Set --endpoint in config or use STORAGE_S3_ENDPOINT=managed to use managed versitygw.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if dataDir == "" {
				home, err := os.UserConfigDir()
				if err != nil {
					return fmt.Errorf("resolve config dir: %w", err)
				}
				dataDir = filepath.Join(home, "exo", "standalone")
			}
			return runUp(runUpOpts{
				dataDir:       dataDir,
				surrealBin:    surrealBin,
				adbBin:        adbBin,
				configFile:    configFile,
				surrealPort:   surrealPort,
				versitygwPort: versitygwPort,
				adbPort:       adbPort,
				accessKey:     accessKey,
				secretKey:     secretKey,
				bucket:        bucket,
			})
		},
	}

	cmd.Flags().StringVar(&dataDir, "data-dir", "", "Directory for persistent data and PID files (default: $EXO_CONFIG_DIR/standalone)")
	cmd.Flags().StringVar(&surrealBin, "surreal-bin", "surreal", "Path to the surreal binary")
	cmd.Flags().StringVar(&adbBin, "adb-bin", "adb-standalone", "Path to the adb-standalone binary")
	cmd.Flags().StringVar(&configFile, "config", "", "adb-standalone config file (default: <data-dir>/config.yaml)")
	cmd.Flags().IntVar(&surrealPort, "surreal-port", 8000, "SurrealDB listen port")
	cmd.Flags().IntVar(&versitygwPort, "versitygw-port", 9100, "versitygw listen port")
	cmd.Flags().IntVar(&adbPort, "adb-port", 8080, "adb-standalone listen port")
	cmd.Flags().StringVar(&accessKey, "access-key", "versitygw", "versitygw root access key")
	cmd.Flags().StringVar(&secretKey, "secret-key", "", "versitygw root secret key (required)")
	cmd.Flags().StringVar(&bucket, "bucket", "adb-standalone", "S3 bucket name to auto-create on startup")

	return cmd
}

type runUpOpts struct {
	dataDir       string
	surrealBin    string
	adbBin        string
	configFile    string
	surrealPort   int
	versitygwPort int
	adbPort       int
	accessKey     string
	secretKey     string
	bucket        string
}

func runUp(opts runUpOpts) error {
	if err := os.MkdirAll(opts.dataDir, 0o700); err != nil {
		return fmt.Errorf("create data dir %q: %w", opts.dataDir, err)
	}

	surrealDataDir := filepath.Join(opts.dataDir, "surreal")
	versitygwDataDir := filepath.Join(opts.dataDir, "objects")
	if err := os.MkdirAll(surrealDataDir, 0o700); err != nil {
		return fmt.Errorf("create surreal data dir: %w", err)
	}
	if err := os.MkdirAll(versitygwDataDir, 0o700); err != nil {
		return fmt.Errorf("create versitygw data dir: %w", err)
	}

	// Locate or download versitygw.
	vgwBin, err := findOrDownloadBinary("versitygw", opts.dataDir)
	if err != nil {
		return fmt.Errorf("versitygw: %w", err)
	}

	// Locate adb-standalone binary.
	adbBinPath, err := exec.LookPath(opts.adbBin)
	if err != nil {
		return fmt.Errorf("%q not found in PATH — install adb-standalone or set --adb-bin", opts.adbBin)
	}

	// Resolve adb-standalone config.
	cfgPath := opts.configFile
	if cfgPath == "" {
		cfgPath = filepath.Join(opts.dataDir, "config.yaml")
		if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
			if err := writeDefaultConfig(cfgPath, opts); err != nil {
				return fmt.Errorf("write default config: %w", err)
			}
			fmt.Fprintf(os.Stderr, "wrote default config to %s\n", cfgPath)
		}
	}

	secretKey := opts.secretKey
	if secretKey == "" {
		secretKey = os.Getenv("VERSITYGW_SECRET_KEY")
	}
	if secretKey == "" {
		return fmt.Errorf("--secret-key or VERSITYGW_SECRET_KEY is required for managed versitygw")
	}
	opts.secretKey = secretKey

	fmt.Fprintf(os.Stderr, "starting adb-standalone stack in %s\n", opts.dataDir)

	// 1. Start SurrealDB.
	surrealArgs := []string{
		"start",
		fmt.Sprintf("--bind=0.0.0.0:%d", opts.surrealPort),
		"--log=info",
		"--user=root",
		"--pass=surreal-local",
		fmt.Sprintf("file://%s/surreal.db", surrealDataDir),
	}
	surrealCmd := exec.Command(opts.surrealBin, surrealArgs...) //nolint:gosec
	surrealCmd.Stdout = os.Stdout
	surrealCmd.Stderr = os.Stderr
	if err := surrealCmd.Start(); err != nil {
		return fmt.Errorf("start surrealdb: %w", err)
	}
	fmt.Fprintf(os.Stderr, "surrealdb started (pid %d)\n", surrealCmd.Process.Pid)

	// 2. Start versitygw.
	vgwArgs := []string{
		fmt.Sprintf("--access=%s", opts.accessKey),
		fmt.Sprintf("--secret=%s", secretKey),
		fmt.Sprintf("--port=%d", opts.versitygwPort),
		"posix",
		versitygwDataDir,
	}
	vgwCmd := exec.Command(vgwBin, vgwArgs...) //nolint:gosec
	vgwCmd.Stdout = os.Stdout
	vgwCmd.Stderr = os.Stderr
	if err := vgwCmd.Start(); err != nil {
		killProcess(surrealCmd)
		return fmt.Errorf("start versitygw: %w", err)
	}
	fmt.Fprintf(os.Stderr, "versitygw started (pid %d) on :%d\n", vgwCmd.Process.Pid, opts.versitygwPort)

	// Wait briefly for versitygw to be ready before starting adb-standalone.
	time.Sleep(500 * time.Millisecond)

	// 3. Start adb-standalone.
	adbArgs := []string{"serve", "--config", cfgPath}
	adbCmd := exec.Command(adbBinPath, adbArgs...) //nolint:gosec
	adbCmd.Stdout = os.Stdout
	adbCmd.Stderr = os.Stderr
	if err := adbCmd.Start(); err != nil {
		killProcess(surrealCmd)
		killProcess(vgwCmd)
		return fmt.Errorf("start adb-standalone: %w", err)
	}
	fmt.Fprintf(os.Stderr, "adb-standalone started (pid %d) on :%d\n", adbCmd.Process.Pid, opts.adbPort)

	// Write PID file.
	pids := stackPIDs{
		Surreal:       surrealCmd.Process.Pid,
		Versitygw:     vgwCmd.Process.Pid,
		ADBStandalone: adbCmd.Process.Pid,
	}
	if err := writePIDFile(opts.dataDir, pids); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: could not write PID file: %v\n", err)
	}

	fmt.Fprintf(os.Stderr, "\nStack is up:\n")
	fmt.Fprintf(os.Stderr, "  surrealdb     ws://localhost:%d\n", opts.surrealPort)
	fmt.Fprintf(os.Stderr, "  versitygw     http://localhost:%d\n", opts.versitygwPort)
	fmt.Fprintf(os.Stderr, "  adb-standalone http://localhost:%d\n", opts.adbPort)
	fmt.Fprintf(os.Stderr, "\nPress Ctrl+C to stop.\n\n")

	// Wait for SIGINT/SIGTERM.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	doneCh := make(chan struct{})
	go func() {
		<-sigCh
		close(doneCh)
	}()

	// Also stop if any child exits unexpectedly.
	exitCh := make(chan error, 3)
	go waitProcess(surrealCmd, "surrealdb", exitCh)
	go waitProcess(vgwCmd, "versitygw", exitCh)
	go waitProcess(adbCmd, "adb-standalone", exitCh)

	select {
	case <-doneCh:
		fmt.Fprintln(os.Stderr, "shutting down…")
	case err := <-exitCh:
		fmt.Fprintf(os.Stderr, "child exited unexpectedly: %v — shutting down\n", err)
	}

	// Stop all children.
	killProcess(adbCmd)
	killProcess(vgwCmd)
	killProcess(surrealCmd)
	removePIDFile(opts.dataDir)
	fmt.Fprintln(os.Stderr, "stack stopped")
	return nil
}

func killProcess(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
	}
}

func waitProcess(cmd *exec.Cmd, name string, ch chan<- error) {
	err := cmd.Wait()
	if err != nil {
		ch <- fmt.Errorf("%s: %w", name, err)
	} else {
		ch <- fmt.Errorf("%s exited with status 0", name)
	}
}

// writeDefaultConfig generates a minimal adb-standalone config for managed mode.
func writeDefaultConfig(path string, opts runUpOpts) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	content := fmt.Sprintf(`gprn:
  service: adb-standalone
  environment: local
  placeholder: artifact

schema: artifactdb-schema/v1
internal_schema: artifactdb-internal/v1

permissions:
  default_read: public
  default_write: owners

prefixes:
  - /exohub

tenants:
  - path: /exohub
    alias: exohub

surreal:
  url: ws://localhost:%d
  ns: adb
  db: adb
  username: root
  password: surreal-local

storage:
  s3:
    endpoint: http://localhost:%d
    public_endpoint: http://localhost:%d
    bucket: %s
    region: us-east-1
    access_key_id: %s
    secret_access_key: %s
    use_path_style: true
    presigned_url_expiration: 3600
    signature_version: v4

server:
  addr: :%d
  base_url: http://localhost:%d
`,
		opts.surrealPort,
		opts.versitygwPort, opts.versitygwPort,
		opts.bucket,
		opts.accessKey,
		opts.secretKey,
		opts.adbPort, opts.adbPort,
	)
	return os.WriteFile(path, []byte(content), 0o600)
}
