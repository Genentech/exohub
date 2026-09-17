//go:build artifactdb

package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// External special-remote for git-annex that exports files as plain objects
// to S3, preserving their paths. Designed for ArtifactDB to read directly.
//
// Files exported under .exohub/bundles/<ref>/ are uploaded to s3url/<ref>/...
// Files exported under .artifactdb/ are uploaded to s3url/<ref>/.artifactdb/...
//
// Required init param: s3url=s3://bucket/prefix
// Optional init param: instance_url=https://artifactdb-api/v1/project
// Optional env: S5CMD_BIN, EXOHUB_CLI_DEBUG, EXO_CREDENTIAL_HELPER_BIN

var appVersion = "dev"

var credHelperBin = envOr("EXO_CREDENTIAL_HELPER_BIN", "exo-credential-helper")

// grantsCreds holds credentials fetched via the credential helper
type grantsCreds struct {
	AccessKeyID     string `json:"AccessKeyId"`
	SecretAccessKey string `json:"SecretAccessKey"`
	SessionToken    string `json:"SessionToken"`
	Expiration      string `json:"Expiration"`
}

var (
	s5cmdBin   = envOr("S5CMD_BIN", "s5cmd")
	config     = map[string]string{}
	exportName string

	// Grants credential expiration tracking
	grantsExpiration time.Time

	in   = bufio.NewReader(os.Stdin)
	outw = bufio.NewWriter(os.Stdout)
)

func envOr(k, def string) string {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	return v
}

var writeLine = func(line string) {
	outw.WriteString(line)
	outw.WriteByte('\n')
	outw.Flush()
}

var debug = func(msg string) {
	writeLine("DEBUG " + msg)
}

func debugStderr(format string, args ...any) {
	if os.Getenv("EXOHUB_CLI_DEBUG") == "1" {
		fmt.Fprintf(os.Stderr, "DEBUG: "+format+"\n", args...)
	}
}

type runResult struct {
	code   int
	stdout []byte
	stderr []byte
}

var run = func(args ...string) runResult {
	if len(args) == 0 {
		return runResult{code: 127}
	}
	cmd := exec.Command(args[0], args[1:]...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return runResult{code: ee.ExitCode(), stdout: outBuf.Bytes(), stderr: errBuf.Bytes()}
		}
		return runResult{code: 127, stdout: outBuf.Bytes(), stderr: []byte(err.Error())}
	}
	return runResult{code: 0, stdout: outBuf.Bytes(), stderr: errBuf.Bytes()}
}

func readLine() (string, bool) {
	s, err := in.ReadString('\n')
	if err != nil {
		if len(s) == 0 {
			return "", false
		}
	}
	s = strings.TrimRight(s, "\n")
	return s, true
}

func sanitizeMsg(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}

// --- S3 helpers ---

func parseS3url(s string) (bucket, prefix string, err error) {
	u, err := url.Parse(s)
	if err != nil {
		return "", "", err
	}
	if u.Scheme != "s3" || u.Host == "" {
		return "", "", fmt.Errorf("invalid s3url: %s", s)
	}
	bucket = u.Host
	prefix = strings.TrimPrefix(u.Path, "/")
	return bucket, prefix, nil
}

func ensureCfg() (bucket, prefix string, err error) {
	if config["s3url"] == "" {
		// s3url is a required config stored by initremote — safe to GETCONFIG
		_ = getConfigFromAnnex("s3url")
	}
	if config["s3url"] == "" {
		return "", "", fmt.Errorf("missing required config: s3url")
	}
	return parseS3url(config["s3url"])
}

// s3ExportPath builds the S3 object path for an exported file.
// The export name from git-annex is the file's path in the working tree.
// We upload to s3url/<name> directly — the ref_name is already embedded
// in the path (e.g. .exohub/bundles/v1.2.0/bundle.json).
func s3ExportPath(name, prefix string) string {
	if prefix != "" {
		return strings.TrimRight(prefix, "/") + "/" + name
	}
	return name
}

func getConfigFromAnnex(key string) string {
	writeLine("GETCONFIG " + key)
	for {
		line, ok := readLine()
		if !ok {
			return ""
		}
		if strings.HasPrefix(line, "VALUE ") {
			val := strings.TrimPrefix(line, "VALUE ")
			config[key] = val
			return val
		}
		if line == "VALUE" {
			config[key] = ""
			return ""
		}
	}
}

// --- Credential helpers ---

func fetchGrantsCredentials(s3url string) error {
	cmd := exec.Command(credHelperBin, "--s3url", s3url)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		msg := sanitizeMsg(errBuf.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("credential helper failed: %s", msg)
	}

	var creds grantsCreds
	if err := json.Unmarshal(outBuf.Bytes(), &creds); err != nil {
		return fmt.Errorf("failed to parse credential helper output: %v", err)
	}

	if creds.AccessKeyID == "" {
		return fmt.Errorf("credential helper returned empty AccessKeyId")
	}

	os.Setenv("AWS_ACCESS_KEY_ID", creds.AccessKeyID)
	os.Setenv("AWS_SECRET_ACCESS_KEY", creds.SecretAccessKey)
	os.Setenv("AWS_SESSION_TOKEN", creds.SessionToken)

	if t, err := time.Parse(time.RFC3339, creds.Expiration); err == nil {
		grantsExpiration = t
	}

	debugStderr("grants: credentials set (expires %s)", creds.Expiration)
	return nil
}

func refreshCredentialsIfNeeded() {
	if config["grants"] != "true" {
		return
	}
	if grantsExpiration.IsZero() || time.Until(grantsExpiration) > 5*time.Minute {
		return
	}
	debugStderr("grants: credentials expiring soon, refreshing")
	if err := fetchGrantsCredentials(config["s3url"]); err != nil {
		debugStderr("grants: proactive refresh failed: %s", err.Error())
	}
}

func isCredentialError(errMsg string) bool {
	lower := strings.ToLower(errMsg)
	patterns := []string{
		"expiredtoken", "expired", "invalid credentials",
		"invalidaccesskeyid", "signaturedoesnotmatch",
		"the security token included in the request is invalid",
		"access denied", "status code: 400", "status code: 401", "status code: 403",
	}
	for _, p := range patterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

func runS5cmd(args ...string) runResult {
	refreshCredentialsIfNeeded()
	r := run(args...)
	if r.code != 0 && config["grants"] == "true" {
		msg := sanitizeMsg(string(r.stderr))
		if isCredentialError(msg) {
			debugStderr("grants: credential error detected, refreshing and retrying")
			if err := fetchGrantsCredentials(config["s3url"]); err != nil {
				debugStderr("grants: reactive refresh failed: %s", err.Error())
				return r
			}
			r = run(args...)
		}
	}
	return r
}

// runS5cmdSync runs s5cmd sync with progress counter on stderr.
// Returns the number of files synced.
func runS5cmdSync(s5cmd, src, dst string) (int, error) {
	refreshCredentialsIfNeeded()
	cmd := exec.Command(s5cmd, "sync", src, dst)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return 0, err
	}

	count := 0
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		count++
		fmt.Fprintf(os.Stderr, "\r    syncing [%d]", count)
	}

	if err := cmd.Wait(); err != nil {
		return count, fmt.Errorf("s5cmd sync failed: %w", err)
	}
	return count, nil
}

// --- Protocol handlers ---

func handleInitRemote() {
	if v, ok := config["s3url"]; ok && v != "" {
		writeLine("CONFIG s3url=" + v)
	}
	if v, ok := config["instance_url"]; ok && v != "" {
		writeLine("CONFIG instance_url=" + v)
	}
	if v, ok := config["grants"]; ok && v != "" {
		writeLine("CONFIG grants=" + v)
	}
	writeLine("INITREMOTE-SUCCESS")
}

func handlePrepare() {
	if _, _, err := ensureCfg(); err != nil {
		writeLine("PREPARE-FAILURE " + err.Error())
		return
	}

	// If grants=true, fetch credentials
	grants := config["grants"]
	if grants == "" {
		grants = getConfigFromAnnex("grants")
	}
	if grants == "true" {
		debugStderr("grants: fetching credentials for %s", config["s3url"])
		if err := fetchGrantsCredentials(config["s3url"]); err != nil {
			writeLine("PREPARE-FAILURE " + err.Error())
			return
		}
	}

	// Ensure s5cmd is available
	r := run(s5cmdBin, "version")
	if r.code != 0 {
		r2 := run(s5cmdBin, "--help")
		if r2.code != 0 {
			msg := sanitizeMsg(string(r.stderr))
			if msg == "" {
				msg = sanitizeMsg(string(r2.stderr))
			}
			writeLine("PREPARE-FAILURE s5cmd not found/usable: " + msg)
			return
		}
	}

	writeLine("PREPARE-SUCCESS")
}

func handleExport(name string) {
	exportName = name
	debug("EXPORT name=" + name)
}

func handleTransferExport(op, key, filePath string) {
	if exportName == "" {
		writeLine("TRANSFER-FAILURE " + op + " " + key + " missing export name")
		return
	}
	bucket, prefix, err := ensureCfg()
	if err != nil {
		writeLine("TRANSFER-FAILURE " + op + " " + key + " " + err.Error())
		return
	}
	obj := s3ExportPath(exportName, prefix)
	debug(fmt.Sprintf("TRANSFEREXPORT %s key=%s file=%s obj=%s", op, key, filePath, obj))

	switch op {
	case "STORE":
		if fi, err := os.Stat(filePath); err != nil || fi.IsDir() {
			writeLine("TRANSFER-FAILURE STORE " + key + " local file not found")
			return
		}
		r := runS5cmd(s5cmdBin, "cp", filePath, fmt.Sprintf("s3://%s/%s", bucket, obj))
		if r.code == 0 {
			writeLine("TRANSFER-SUCCESS STORE " + key)
			return
		}
		msg := sanitizeMsg(string(r.stderr))
		if msg == "" {
			msg = fmt.Sprintf("s5cmd cp failed (exit %d)", r.code)
		}
		writeLine("TRANSFER-FAILURE STORE " + key + " " + msg)
	case "RETRIEVE":
		r := runS5cmd(s5cmdBin, "cp", fmt.Sprintf("s3://%s/%s", bucket, obj), filePath)
		if r.code == 0 {
			writeLine("TRANSFER-SUCCESS RETRIEVE " + key)
			return
		}
		msg := sanitizeMsg(string(r.stderr))
		if msg == "" {
			msg = fmt.Sprintf("s5cmd cp failed (exit %d)", r.code)
		}
		writeLine("TRANSFER-FAILURE RETRIEVE " + key + " " + msg)
	default:
		writeLine("ERROR unknown TRANSFEREXPORT " + op)
	}
}

func handleExportStore(key, filePath string) {
	bucket, prefix, err := ensureCfg()
	if err != nil {
		writeLine("EXPORTSTORE-FAILURE " + key + " " + err.Error())
		return
	}

	if exportName == "" {
		writeLine("EXPORTSTORE-FAILURE " + key + " missing export name")
		return
	}

	obj := s3ExportPath(exportName, prefix)
	debug(fmt.Sprintf("EXPORTSTORE key=%s file=%s obj=%s", key, filePath, obj))

	if fi, err := os.Stat(filePath); err != nil || fi.IsDir() {
		writeLine("EXPORTSTORE-FAILURE " + key + " local file not found")
		return
	}

	r := runS5cmd(s5cmdBin, "cp", filePath, fmt.Sprintf("s3://%s/%s", bucket, obj))
	if r.code == 0 {
		writeLine("EXPORTSTORE-SUCCESS " + key)
		return
	}
	msg := sanitizeMsg(string(r.stderr))
	if msg == "" {
		msg = fmt.Sprintf("s5cmd cp failed (exit %d)", r.code)
	}
	writeLine("EXPORTSTORE-FAILURE " + key + " " + msg)
}

func handleCheckPresentExport(key string) {
	if exportName == "" {
		writeLine("CHECKPRESENT-UNKNOWN " + key + " missing export name")
		return
	}
	bucket, prefix, err := ensureCfg()
	if err != nil {
		writeLine("CHECKPRESENT-UNKNOWN " + key + " " + err.Error())
		return
	}

	obj := s3ExportPath(exportName, prefix)
	debug(fmt.Sprintf("CHECKPRESENTEXPORT key=%s obj=%s", key, obj))

	r := runS5cmd(s5cmdBin, "ls", fmt.Sprintf("s3://%s/%s", bucket, obj))
	if r.code == 0 {
		writeLine("CHECKPRESENT-SUCCESS " + key)
		return
	}
	writeLine("CHECKPRESENT-FAILURE " + key)
}

func handleRemoveExport(key string) {
	if exportName == "" {
		writeLine("REMOVE-FAILURE " + key + " missing export name")
		return
	}
	bucket, prefix, err := ensureCfg()
	if err != nil {
		writeLine("REMOVE-FAILURE " + key + " " + err.Error())
		return
	}

	obj := s3ExportPath(exportName, prefix)
	debug(fmt.Sprintf("REMOVEEXPORT key=%s obj=%s", key, obj))

	r := runS5cmd(s5cmdBin, "rm", fmt.Sprintf("s3://%s/%s", bucket, obj))
	if r.code == 0 {
		writeLine("REMOVE-SUCCESS " + key)
		return
	}
	msg := sanitizeMsg(string(r.stderr))
	if msg == "" {
		msg = fmt.Sprintf("s5cmd rm failed (exit %d)", r.code)
	}
	writeLine("REMOVE-FAILURE " + key + " " + msg)
}

func handleRemoveExportDirectoryWhenEmpty(directory string) {
	// S3 doesn't have real directories, nothing to do
	writeLine("REMOVEEXPORTDIRECTORY-SUCCESS")
}

// --- Publish subcommand ---

const (
	artipackExtension        = ".artipack"
	defaultArtipackThreshold = 1000
	defaultArtipackMaxFiles  = 100000
)

// builtinAPIBase is the built-in fallback for --api-url.
// Overridden by defaults_internal.go (//go:build internal).
var builtinAPIBase = ""

func resolveAPIBase() string {
	if v := strings.TrimSpace(os.Getenv("EXOHUB_API_URL")); v != "" {
		return v
	}
	return builtinAPIBase
}

type publishEntry struct {
	localPath string
	s3Key     string
}

func runPublish(args []string) {
	fs := flag.NewFlagSet("publish", flag.ExitOnError)
	s3url := fs.String("s3url", "", "S3 URL for upload")
	bundleDir := fs.String("bundle-dir", "", "Path to bundle output directory")
	artifactdbDir := fs.String("artifactdb-dir", "", "Path to .artifactdb/ directory (optional)")
	grants := fs.Bool("grants", false, "Use S3 Access Grants for credentials")
	instanceURL := fs.String("instance-url", "", "ArtifactDB instance URL (optional)")
	projectIDFlag := fs.String("project-id", "", "Project ID override (defaults to repo name from bundle.json)")
	apiURL := fs.String("api-url", resolveAPIBase(), "ExoHub API URL")
	fs.Parse(args)

	debugStderr("publish: s3url=%s bundle-dir=%s api-url=%s instance-url=%s grants=%v", *s3url, *bundleDir, *apiURL, *instanceURL, *grants)

	if *s3url == "" || *bundleDir == "" {
		fmt.Fprintln(os.Stderr, "Error: --s3url and --bundle-dir are required")
		os.Exit(1)
	}

	// Read bundle.json for ref info
	bundleJSON, err := os.ReadFile(filepath.Join(*bundleDir, "bundle.json"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to read bundle.json: %v\n", err)
		os.Exit(1)
	}
	var info struct {
		RefName string `json:"ref_name"`
		Ref     string `json:"ref"`
		RepoURL string `json:"repo_url"`
	}
	if err := json.Unmarshal(bundleJSON, &info); err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to parse bundle.json: %v\n", err)
		os.Exit(1)
	}

	projectID := *projectIDFlag
	if projectID == "" {
		projectID = repoNameFromURL(info.RepoURL)
	}
	s3Base := strings.TrimRight(*s3url, "/") + "/" + info.RefName

	fmt.Fprintf(os.Stderr, "📊  Project: %s, Version: %s\n", orange(projectID), orange(info.RefName))
	fmt.Fprintf(os.Stderr, "☁️  Target: %s\n", yellow(s3Base+"/"))

	// Get credentials if grants enabled
	if *grants {
		fmt.Fprintln(os.Stderr, "🔑  Fetching S3 credentials via grants")
		if err := fetchGrantsCredentials(*s3url); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	}

	s5 := envOr("S5CMD_BIN", "s5cmd")

	// Sync bundle dir → .exohub-bundle/ on S3
	bundleDst := s3Base + "/.exohub-bundle/"
	// Progress counter shown during sync, no separate "Syncing to..." line
	count, err := runS5cmdSync(s5, *bundleDir+"/", bundleDst)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\n⚠️  bBndle sync failed: %v\n", err)
		os.Exit(1)
	}

	// Sync .artifactdb/ if present
	if *artifactdbDir != "" {
		artifactdbDst := s3Base + "/.artifactdb/"
		n, err := runS5cmdSync(s5, *artifactdbDir+"/", artifactdbDst)
		if err != nil {
			fmt.Fprintf(os.Stderr, "\n⚠️  .artifactdb sync failed: %v\n", err)
			os.Exit(1)
		}
		count += n
	}

	fmt.Fprintf(os.Stderr, "\r🔄  Synced %d file(s)              \n", count)

	// Upload permissions.json if .exohub/permissions exists
	if err := uploadPermissions(s5, s3Base, "."); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  pPrmissions upload failed: %v\n", err)
		// Non-fatal — continue with notification
	}

	// Notify ExoHub API
	s3Location := s3Base + "/"
	respBody, err := publishNotify(*apiURL, projectID, info.RepoURL, info.Ref, info.RefName, s3Location, *s3url, *instanceURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  ExoHub API notification failed: %v\n", err)
		fmt.Fprintln(os.Stderr, "⚠️  Upload completed but indexing may not have been triggered.")
	} else {
		fmt.Fprintf(os.Stderr, "📡  Notified ExoHub API (project: %s, version: %s)\n", orange(projectID), orange(info.RefName))
		// Display per-target results
		var resp struct {
			Status  string `json:"status"`
			Results []struct {
				URL    string `json:"url"`
				Type   string `json:"type"`
				Status string `json:"status"`
				Detail string `json:"detail,omitempty"`
				JobID  string `json:"job_id,omitempty"`
				JobURL string `json:"job_url,omitempty"`
			} `json:"results"`
		}
		if json.Unmarshal(respBody, &resp) == nil {
			for _, r := range resp.Results {
				if r.Status == "ok" {
					if r.JobURL != "" {
						fmt.Fprintf(os.Stderr, "⚙️  Job: %s\n", dim(r.JobURL))
					} else {
						fmt.Fprintf(os.Stderr, "     %s %s %s\n", green("✓"), dim(r.Type), dim(r.URL))
					}
				} else {
					fmt.Fprintf(os.Stderr, "     %s %s %s: %s\n", "✗", dim(r.Type), dim(r.URL), r.Detail)
				}
			}
		}
	}
}

// isTTY checks if stderr is a terminal.
func isTTY() bool {
	fi, err := os.Stderr.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func ansiColor(code int, bold bool, s string) string {
	if !isTTY() {
		return s
	}
	b := ""
	if bold {
		b = ";1"
	}
	return fmt.Sprintf("\033[38;5;%d%sm%s\033[0m", code, b, s)
}

// Color mapping to palette semantic roles (keep in sync with go/exo/palette/):
//   209 (orange) = Label
//   250 (gray)   = Dim
//   136 (amber)  = Warning
//   220 (yellow) = Highlight
//   70  (green)  = Success

func orange(s string) string     { return ansiColor(209, true, s) }
func dim(s string) string        { return ansiColor(250, false, s) }
func darkYellow(s string) string { return ansiColor(136, true, s) }
func yellow(s string) string     { return ansiColor(220, false, s) }
func green(s string) string      { return ansiColor(70, false, s) }

// permissionsYAML mirrors the fields in .exohub/permissions that are relevant
// for the ArtifactDB Permissions model.
type permissionsYAML struct {
	Owners      []string `yaml:"owners"`
	Viewers     []string `yaml:"viewers"`
	ReadAccess  string   `yaml:"read_access"`
	WriteAccess string   `yaml:"write_access"`
}

// uploadPermissions reads <rootDir>/.exohub/permissions, converts to ArtifactDB
// format, and uploads as permissions.json to the version root on S3.
// rootDir is the directory that contains .exohub/ (usually ".").
func uploadPermissions(s5cmd, s3Base, rootDir string) error {
	permFile := filepath.Join(rootDir, ".exohub", "permissions")
	data, err := os.ReadFile(permFile)
	if os.IsNotExist(err) {
		return nil // no permissions file, skip
	}
	if err != nil {
		return err
	}

	// Parse YAML using a proper parser so quoted values ("public", "aboyounp")
	// are stripped of their surrounding quotes before being uploaded.
	var perm permissionsYAML
	if err := yaml.Unmarshal(data, &perm); err != nil {
		return fmt.Errorf("parse .exohub/permissions: %w", err)
	}

	// Validate required fields before uploading — ArtifactDB rejects zero-values
	// with confusing enum/validation errors.
	if len(perm.Owners) == 0 {
		return fmt.Errorf(".exohub/permissions: owners must be non-empty")
	}
	if perm.ReadAccess == "" {
		return fmt.Errorf(".exohub/permissions: read_access must be set")
	}
	if perm.WriteAccess == "" {
		return fmt.Errorf(".exohub/permissions: write_access must be set")
	}

	// Convert to ArtifactDB format.
	adbPerm := map[string]interface{}{
		"scope":        "project",
		"owners":       perm.Owners,
		"read_access":  perm.ReadAccess,
		"write_access": perm.WriteAccess,
	}
	if len(perm.Viewers) > 0 {
		adbPerm["viewers"] = perm.Viewers
	}

	// Write to temp file and upload
	tmpFile, err := os.CreateTemp("", "permissions-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmpFile.Name())

	body, _ := json.MarshalIndent(adbPerm, "", "  ")
	if _, err := tmpFile.Write(body); err != nil {
		return err
	}
	tmpFile.Close()

	dst := s3Base + "/permissions.json"
	r := runS5cmd(s5cmd, "cp", tmpFile.Name(), dst)
	if r.code != 0 {
		return fmt.Errorf("s5cmd cp failed: %s", sanitizeMsg(string(r.stderr)))
	}
	fmt.Fprintf(os.Stderr, "🔒  Permissions uploaded\n")
	return nil
}



func repoNameFromURL(repoURL string) string {
	u := strings.TrimSuffix(repoURL, ".git")
	if idx := strings.LastIndex(u, "/"); idx >= 0 {
		return u[idx+1:]
	}
	return u
}

func createPublishArtipack(archivePath string, uploads []publishEntry, s3Base string) error {
	f, err := os.Create(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	defer zw.Close()

	for _, u := range uploads {
		// Use the relative path from s3Base as the zip entry name
		relName := strings.TrimPrefix(u.s3Key, s3Base+"/")

		src, err := os.Open(u.localPath)
		if err != nil {
			return err
		}
		info, err := src.Stat()
		if err != nil {
			src.Close()
			return err
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			src.Close()
			return err
		}
		header.Name = relName
		header.Method = zip.Deflate
		w, err := zw.CreateHeader(header)
		if err != nil {
			src.Close()
			return err
		}
		_, err = io.Copy(w, src)
		src.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func publishNotify(apiBase, projectID, repoURL, ref, refName, s3Location, s3url, instanceURL string) ([]byte, error) {
	endpoint := strings.TrimRight(apiBase, "/") + "/publish"

	// Build targets: only include instance_url if set.
	// ExoHub API adds its own default target.
	targets := make([]map[string]string, 0)
	if instanceURL != "" {
		targets = append(targets, map[string]string{
			"url":  instanceURL,
			"type": "artifactdb",
		})
	}

	payload := map[string]interface{}{
		"project_id":  projectID,
		"repo_url":    repoURL,
		"ref":         ref,
		"ref_name":    refName,
		"s3_location": s3Location,
		"s3url":       s3url,
		"targets":     targets,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	debugStderr("publish: POST %s", endpoint)
	debugStderr("publish: payload=%s", string(body))

	req, err := http.NewRequest("POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	// Try to add JWT auth from exo login token store
	if token := loadExoToken(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return respBody, fmt.Errorf("API returned %d: %s", resp.StatusCode, string(respBody))
	}
	return respBody, nil
}

// loadExoToken reads the JWT access token from the exo login token store.
func loadExoToken() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	tokenPath := filepath.Join(configDir, "exo", "credentials", "token.json")
	data, err := os.ReadFile(tokenPath)
	if err != nil {
		return ""
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(data, &tok); err != nil {
		return ""
	}
	return tok.AccessToken
}

func envBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

// --- Main protocol loop ---

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-version") {
		full := len(os.Args) > 2 && os.Args[2] == "--full"
		if full {
			tags := enabledFeatures()
			if len(tags) > 0 {
				fmt.Printf("%s [features: %s]\n", appVersion, strings.Join(tags, ", "))
			} else {
				fmt.Println(appVersion)
			}
		} else {
			fmt.Println(appVersion)
		}
		return
	}

	// Publish subcommand (called by exo sync, not by git-annex)
	if len(os.Args) > 1 && os.Args[1] == "publish" {
		runPublish(os.Args[2:])
		return
	}

	writeLine("VERSION 1")

	for {
		line, ok := readLine()
		if !ok {
			break
		}
		if line == "" {
			continue
		}
		if line == "EXIT" {
			break
		}

		switch {
		case line == "EXTENSIONS" || strings.HasPrefix(line, "EXTENSIONS "):
			writeLine("EXTENSIONS")
			continue
		case strings.HasPrefix(line, "VERSION "):
			continue
		case line == "LISTCONFIGS":
			writeLine("CONFIG s3url")
			writeLine("CONFIG instance_url")
			writeLine("CONFIG grants")
			writeLine("CONFIGEND")
			continue
		case line == "GETINFO" || line == "INFO":
			writeLine("INFOEND")
			continue
		case line == "GETGITREMOTENAME":
			writeLine("VALUE ")
			continue
		case line == "GETCOST":
			writeLine("COST 200")
			continue
		case line == "GETAVAILABILITY":
			writeLine("AVAILABILITY GLOBAL")
			continue
		case strings.HasPrefix(line, "SETCONFIG "):
			rest := strings.TrimPrefix(line, "SETCONFIG ")
			kv := strings.SplitN(rest, " ", 2)
			if len(kv) == 2 {
				config[kv[0]] = kv[1]
			}
			continue
		case line == "INITREMOTE":
			handleInitRemote()
			continue
		case line == "PREPARE":
			handlePrepare()
			continue
		case line == "EXPORTSUPPORTED":
			writeLine("EXPORTSUPPORTED-SUCCESS")
			continue
		case line == "IMPORTSUPPORTED":
			writeLine("IMPORTSUPPORTED-FAILURE")
			continue

		// Export protocol
		case strings.HasPrefix(line, "EXPORT "):
			name := strings.TrimPrefix(line, "EXPORT ")
			handleExport(name)
			continue
		case strings.HasPrefix(line, "TRANSFEREXPORT "):
			rest := strings.TrimPrefix(line, "TRANSFEREXPORT ")
			parts := strings.SplitN(rest, " ", 2)
			if len(parts) != 2 {
				writeLine("ERROR malformed TRANSFEREXPORT")
				continue
			}
			op := parts[0]
			rest2 := parts[1]
			parts2 := strings.SplitN(rest2, " ", 2)
			if len(parts2) != 2 {
				writeLine("ERROR malformed TRANSFEREXPORT")
				continue
			}
			key := parts2[0]
			file := parts2[1]
			handleTransferExport(op, key, file)
			continue
		case strings.HasPrefix(line, "EXPORTSTORE "):
			rest := strings.TrimPrefix(line, "EXPORTSTORE ")
			parts := strings.SplitN(rest, " ", 2)
			if len(parts) != 2 {
				writeLine("ERROR malformed EXPORTSTORE")
				continue
			}
			handleExportStore(parts[0], parts[1])
			continue
		case strings.HasPrefix(line, "CHECKPRESENTEXPORT "):
			key := strings.TrimPrefix(line, "CHECKPRESENTEXPORT ")
			handleCheckPresentExport(key)
			continue
		case strings.HasPrefix(line, "REMOVEEXPORT "):
			key := strings.TrimPrefix(line, "REMOVEEXPORT ")
			handleRemoveExport(key)
			continue
		case strings.HasPrefix(line, "REMOVEEXPORTDIRECTORYWHENEMPTY "):
			directory := strings.TrimPrefix(line, "REMOVEEXPORTDIRECTORYWHENEMPTY ")
			handleRemoveExportDirectoryWhenEmpty(directory)
			continue

		// Annex key protocol — not supported
		case strings.HasPrefix(line, "TRANSFER "):
			rest := strings.TrimPrefix(line, "TRANSFER ")
			parts := strings.SplitN(rest, " ", 3)
			if len(parts) >= 2 {
				writeLine("TRANSFER-FAILURE " + parts[0] + " " + parts[1] + " annex key storage not supported by artifactdb-export remote")
			} else {
				writeLine("ERROR malformed TRANSFER")
			}
			continue
		case strings.HasPrefix(line, "CHECKPRESENT "):
			key := strings.TrimPrefix(line, "CHECKPRESENT ")
			writeLine("CHECKPRESENT-UNKNOWN " + key + " annex key storage not supported")
			continue
		case strings.HasPrefix(line, "REMOVE "):
			key := strings.TrimPrefix(line, "REMOVE ")
			writeLine("REMOVE-FAILURE " + key + " annex key storage not supported")
			continue

		default:
			writeLine("UNSUPPORTED-REQUEST")
		}
	}
}
