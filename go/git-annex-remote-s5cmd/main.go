package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Minimal external special-remote for git-annex using s5cmd.
// Required init param: s3url=s3://bucket/optional/prefix
// Optional env: S5CMD_BIN (default: "s5cmd")

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
	s5cmdBin     = envOr("S5CMD_BIN", "s5cmd")
	config       = map[string]string{}
	keyToFile    = map[string]string{}
	exportName   string
	exportRef    string
	exportRefSet bool

	// Import support state
	currentLocation string
	expectedContent = map[string]string{} // location -> content identifier

	// Grants credential expiration tracking
	grantsExpiration time.Time

	// Base credential expiration tracking (grants:false path)
	baseCredsExpiration time.Time

	in   = bufio.NewReader(os.Stdin)
	outw = bufio.NewWriter(os.Stdout)
)

// S3Object represents an object returned from s5cmd ls --json
type S3Object struct {
	Key          string    `json:"key"`
	Type         string    `json:"type"`
	Size         int64     `json:"size"`
	ETag         string    `json:"etag"`
	LastModified time.Time `json:"last_modified"`
	StorageClass string    `json:"storage_class"`
}

func envOr(k, def string) string {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	return v
}

var writeLine = func(line string) {
	// All protocol output goes to stdout, line-delimited
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
	err := cmd.Run()
	if err == nil {
		return runResult{code: 0, stdout: outBuf.Bytes(), stderr: errBuf.Bytes()}
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return runResult{code: ee.ExitCode(), stdout: outBuf.Bytes(), stderr: errBuf.Bytes()}
	}
	// Likely not found
	return runResult{code: 127, stdout: outBuf.Bytes(), stderr: []byte(err.Error())}
}

func getS5cmdVersion() string {
	r := run(s5cmdBin, "version")
	if r.code == 0 && len(r.stdout) > 0 {
		line := firstLineSafe(string(r.stdout))
		return line
	}
	r2 := run(s5cmdBin, "--version")
	if r2.code == 0 && len(r2.stdout) > 0 {
		return firstLineSafe(string(r2.stdout))
	}
	return "unknown"
}

func firstLineSafe(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

// getConfigFromAnnex asks git-annex for a persisted config value by
// emitting GETCONFIG and then consuming lines from stdin until VALUE.
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
		// Ignore unrelated lines
	}
}

func readLine() (string, bool) {
	s, err := in.ReadString('\n')
	if err != nil {
		if len(s) == 0 {
			return "", false
		}
		// Return what we got
	}
	s = strings.TrimRight(s, "\n")
	return s, true
}

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
	if _, ok := config["s3url"]; !ok || config["s3url"] == "" {
		if env := os.Getenv("ANNEX_S3URL"); env != "" {
			config["s3url"] = env
		} else if env2 := os.Getenv("S3URL"); env2 != "" {
			config["s3url"] = env2
		}
	}
	if config["s3url"] == "" {
		_ = getConfigFromAnnex("s3url")
	}
	if config["s3url"] == "" {
		return "", "", fmt.Errorf("missing required config: s3url")
	}
	return parseS3url(config["s3url"])
}

func s3Key(key, prefix string) string {
	if prefix != "" {
		return strings.TrimRight(prefix, "/") + "/" + key
	}
	return key
}

func getExportRef() string {
	if !exportRefSet {
		exportRef = os.Getenv("GIT_ANNEX_EXPORT_REF")
		exportRefSet = true
	}
	return exportRef
}

func s3ExportKey(name, prefix string) string {
	ref := strings.Trim(getExportRef(), "/")
	if ref != "" {
		if prefix != "" {
			return strings.TrimRight(prefix, "/") + "/" + ref + "/" + name
		}
		return ref + "/" + name
	}
	return s3Key(name, prefix)
}

func handleInitRemote() {
	if v, ok := config["s3url"]; ok && v != "" {
		writeLine("CONFIG s3url=" + v)
	}
	if v, ok := config["grants"]; ok && v != "" {
		writeLine("CONFIG grants=" + v)
	}
	writeLine("INITREMOTE-SUCCESS")
}

// grantsPermission tracks the permission level of the current credentials.
var grantsPermission string

// fetchGrantsCredentials calls exo-credential-helper to get temporary AWS
// credentials via S3 Access Grants and sets them as environment variables.
// permission should be "READ" or "READWRITE". If empty, defaults to "READWRITE"
// with fallback to "READ" for viewers who only have read access.
func fetchGrantsCredentials(s3url string, permission ...string) error {
	perm := "READWRITE"
	if len(permission) > 0 && permission[0] != "" {
		perm = permission[0]
	}
	cmd := exec.Command(credHelperBin, "--s3url", s3url, "--permission", perm)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		msg := sanitizeMsg(errBuf.String())
		if msg == "" {
			msg = err.Error()
		}
		// If READWRITE failed and no explicit permission was requested,
		// fall back to READ (viewer-level access).
		if perm == "READWRITE" && len(permission) == 0 {
			debugStderr("grants: READWRITE failed, falling back to READ")
			return fetchGrantsCredentials(s3url, "READ")
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

	// Track expiration for proactive refresh
	if t, err := time.Parse(time.RFC3339, creds.Expiration); err == nil {
		grantsExpiration = t
	}

	grantsPermission = perm
	debugStderr("grants: credentials set (permission=%s, expires %s)", perm, creds.Expiration)
	return nil
}

// fetchBaseCredentials calls exo-credential-helper on its grants=false path to
// obtain plain base AWS credentials (via the caller's identity — no S3 Access
// Grants, no POST /api/grants/credentials) and sets them as environment
// variables for s5cmd. Used for remotes whose bucket is not under Access Grants
// (grants: false, or absent, in .exohub/remotes).
//
// Callers treat failure as non-fatal: on error we leave the environment
// untouched so s5cmd falls back to whatever ambient credentials it can find
// (the prior behaviour), so non-grants remotes are not regressed.
func fetchBaseCredentials(s3url string) error {
	cmd := exec.Command(credHelperBin, "--s3url", s3url, "--grants=false")
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		msg := sanitizeMsg(errBuf.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("credential helper (base) failed: %s", msg)
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
		baseCredsExpiration = t
	}
	debugStderr("grants=false: base credentials set (expires %s)", creds.Expiration)
	return nil
}

// refreshCredentialsIfNeeded proactively refreshes credentials if they are
// within 5 minutes of expiration. Handles both grants:true and grants:false paths.
func refreshCredentialsIfNeeded() {
	if config["grants"] == "true" {
		if !grantsExpiration.IsZero() && time.Until(grantsExpiration) <= 5*time.Minute {
			debugStderr("grants: credentials expiring soon, refreshing")
			if err := fetchGrantsCredentials(config["s3url"], grantsPermission); err != nil {
				debugStderr("grants: proactive refresh failed: %s", err.Error())
			}
		}
		return
	}
	// grants:false — proactively refresh base credentials.
	if !baseCredsExpiration.IsZero() && time.Until(baseCredsExpiration) <= 5*time.Minute {
		debugStderr("grants=false: base credentials expiring soon, refreshing")
		if err := fetchBaseCredentials(config["s3url"]); err != nil {
			debugStderr("grants=false: proactive refresh failed: %s", err.Error())
		}
	}
}

// isCredentialError checks if an error message indicates an AWS credential issue.
func isCredentialError(errMsg string) bool {
	if errMsg == "" {
		return false
	}
	lower := strings.ToLower(errMsg)
	patterns := []string{
		"expiredtoken",
		"the provided token has expired",
		"token has expired",
		"expired",
		"invalid credentials",
		"invalidaccesskeyid",
		"signaturedoesnotmatch",
		"the security token included in the request is invalid",
		"credentials have expired",
		"nocredentialproviders",
		"no valid providers in chain",
		"unable to locate credentials",
		"not authorized",
		"access denied",
		"status code: 400",
		"status code: 401",
		"status code: 403",
	}
	for _, p := range patterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// runS5cmd runs an s5cmd command with proactive credential refresh before
// execution and reactive retry on credential errors.
func runS5cmd(args ...string) runResult {
	refreshCredentialsIfNeeded()
	r := run(args...)
	if r.code == 0 {
		return r
	}
	msg := sanitizeMsg(string(r.stderr))
	if !isCredentialError(msg) {
		return r
	}

	grants := config["grants"]
	if grants == "" {
		grants = getConfigFromAnnex("grants")
	}
	if grants == "true" {
		debugStderr("grants: credential error detected, refreshing and retrying")
		if err := fetchGrantsCredentials(config["s3url"], grantsPermission); err != nil {
			debugStderr("grants: reactive refresh failed: %s", err.Error())
			return r
		}
		r = run(args...)
	} else if os.Getenv("AWS_ACCESS_KEY_ID") == "" {
		// grants:false (or absent) and no ambient AWS credentials — the failure is
		// a NoCredentialProviders-class error. Fetch base credentials via the
		// helper's grants=false path and retry once. Reactive (only after a real
		// failure) so the success path is never touched; guarded on an empty
		// AWS_ACCESS_KEY_ID so a caller that supplied explicit credentials is never
		// overridden.
		debugStderr("grants=false: credential error detected, fetching base credentials and retrying")
		if err := fetchBaseCredentials(config["s3url"]); err != nil {
			debugStderr("grants=false: base credential fetch failed: %s", err.Error())
			return r
		}
		r = run(args...)
	}
	return r
}

func handlePrepare() {
	if _, _, err := ensureCfg(); err != nil {
		writeLine("PREPARE-FAILURE " + err.Error())
		return
	}

	// If grants=true, fetch credentials via the credential helper
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
		debugStderr("grants: credentials acquired")
	} else if os.Getenv("AWS_ACCESS_KEY_ID") == "" {
		// grants:false (or absent) and no ambient AWS credentials: proactively
		// fetch base OIDC credentials so the very first transfer has
		// creds. Non-fatal — if it fails we still PREPARE-SUCCESS and fall back
		// to whatever ambient creds exist / the reactive retry in runS5cmd, so
		// remotes that work via ambient creds are not regressed.
		debugStderr("grants=false: fetching base credentials for %s", config["s3url"])
		if err := fetchBaseCredentials(config["s3url"]); err != nil {
			debugStderr("grants=false: base credential fetch failed (continuing): %s", err.Error())
		} else {
			debugStderr("grants=false: base credentials acquired")
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
	// Optional: quick probe
	bucket, prefix, _ := ensureCfg()
	target := "s3://" + bucket
	if prefix != "" {
		target += "/" + prefix
	}
	if rr := run(s5cmdBin, "ls", target); rr.code != 0 {
		msg := sanitizeMsg(string(rr.stderr))
		if msg != "" {
			debug("s3 probe failed: " + msg)
		}
	}
	writeLine("PREPARE-SUCCESS")
}

func handleCheckPresent(key string) {
	defer func() {
		if rec := recover(); rec != nil {
			writeLine("CHECKPRESENT-FAILURE " + key)
		}
	}()
	bucket, prefix, err := ensureCfg()
	if err != nil {
		debug("CHECKPRESENT exception for key=" + key + ": " + err.Error())
		writeLine("CHECKPRESENT-FAILURE " + key)
		return
	}
	obj := s3Key(key, prefix)
	if f, ok := keyToFile[key]; ok {
		debug(fmt.Sprintf("CHECKPRESENT key=%s file=%s obj=%s", key, f, obj))
	} else {
		debug(fmt.Sprintf("CHECKPRESENT key=%s file=<unknown> obj=%s", key, obj))
	}
	r := runS5cmd(s5cmdBin, "ls", fmt.Sprintf("s3://%s/%s", bucket, obj))
	if r.code == 0 {
		writeLine("CHECKPRESENT-SUCCESS " + key)
		return
	}
	msg := sanitizeMsg(string(r.stderr))
	if msg != "" {
		debug(fmt.Sprintf("s5cmd ls failed for key=%s obj=%s: %s", key, obj, msg))
	}
	writeLine("CHECKPRESENT-FAILURE " + key)
}

func handleRemove(key string) {
	bucket, prefix, err := ensureCfg()
	if err != nil {
		writeLine("REMOVE-FAILURE " + key + " " + err.Error())
		return
	}
	obj := s3Key(key, prefix)
	r := runS5cmd(s5cmdBin, "rm", fmt.Sprintf("s3://%s/%s", bucket, obj))
	if r.code == 0 {
		writeLine("REMOVE-SUCCESS " + key)
		return
	}
	msg := sanitizeMsg(string(r.stderr))
	writeLine("REMOVE-FAILURE " + key + " " + msg)
}

func handleTransferStore(key, filePath string) {
	bucket, prefix, err := ensureCfg()
	if err != nil {
		writeLine("TRANSFER-FAILURE STORE " + key + " " + err.Error())
		return
	}
	obj := s3Key(key, prefix)
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
}

func handleTransferRetrieve(key, filePath string) {
	bucket, prefix, err := ensureCfg()
	if err != nil {
		writeLine("TRANSFER-FAILURE RETRIEVE " + key + " " + err.Error())
		return
	}
	obj := s3Key(key, prefix)
	// Ensure directory exists
	_ = os.MkdirAll(filepath.Dir(filePath), 0o755)
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
}

func handleExport(name string) {
	exportName = name
	if !exportRefSet {
		_ = getExportRef()
	}
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
	obj := s3ExportKey(exportName, prefix)
	debug(fmt.Sprintf("TRANSFEREXPORT %s key=%s file=%s obj=%s", op, key, filePath, obj))
	var r runResult
	switch op {
	case "STORE":
		if fi, err := os.Stat(filePath); err != nil || fi.IsDir() {
			writeLine("TRANSFER-FAILURE STORE " + key + " local file not found")
			return
		}
		r = runS5cmd(s5cmdBin, "cp", filePath, fmt.Sprintf("s3://%s/%s", bucket, obj))
	case "RETRIEVE":
		_ = os.MkdirAll(filepath.Dir(filePath), 0o755)
		r = runS5cmd(s5cmdBin, "cp", fmt.Sprintf("s3://%s/%s", bucket, obj), filePath)
	default:
		writeLine("ERROR unknown TRANSFEREXPORT " + op)
		return
	}
	if r.code == 0 {
		writeLine("TRANSFER-SUCCESS " + op + " " + key)
		return
	}
	msg := sanitizeMsg(string(r.stderr))
	if msg == "" {
		msg = fmt.Sprintf("s5cmd cp failed (exit %d)", r.code)
	}
	writeLine("TRANSFER-FAILURE " + op + " " + key + " " + msg)
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
	obj := s3ExportKey(exportName, prefix)
	debug(fmt.Sprintf("CHECKPRESENTEXPORT key=%s obj=%s", key, obj))
	r := runS5cmd(s5cmdBin, "ls", fmt.Sprintf("s3://%s/%s", bucket, obj))
	if r.code == 0 {
		writeLine("CHECKPRESENT-SUCCESS " + key)
		return
	}
	writeLine("CHECKPRESENT-FAILURE " + key)
}

func handleExportStore(key, filePath string) {
	bucket, prefix, err := ensureCfg()
	if err != nil {
		writeLine("EXPORTSTORE-FAILURE " + key + " " + err.Error())
		return
	}
	obj := s3Key(key, prefix)
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
	obj := s3ExportKey(exportName, prefix)
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

func sanitizeMsg(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimSpace(s)
}

// parseS5cmdJSON parses the JSON output from s5cmd ls --json
func parseS5cmdJSON(output []byte) ([]S3Object, error) {
	var objects []S3Object
	// s5cmd outputs one JSON object per line
	lines := bytes.Split(output, []byte("\n"))
	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var obj S3Object
		if err := json.Unmarshal(line, &obj); err != nil {
			// Skip invalid lines
			continue
		}
		// Only include files, not directories
		if obj.Type == "file" {
			objects = append(objects, obj)
		}
	}
	return objects, nil
}

// formatContentIdentifier creates a content identifier from S3 object metadata
// Prefers ETag if available, falls back to size:mtime format
func formatContentIdentifier(etag string, size int64, mtime time.Time) string {
	// Clean up ETag (remove quotes if present)
	etag = strings.Trim(etag, "\"")
	if etag != "" && etag != "-" {
		return "etag:" + etag
	}
	// Fallback to size:mtime format
	return fmt.Sprintf("size:%d:%d", size, mtime.Unix())
}

// getContentIdentifier retrieves the content identifier for an S3 object
func getContentIdentifier(bucket, key string) (string, error) {
	// Use s5cmd ls with the specific object to get metadata
	r := runS5cmd(s5cmdBin, "ls", "--json", fmt.Sprintf("s3://%s/%s", bucket, key))
	if r.code != 0 {
		return "", fmt.Errorf("s5cmd ls failed: %s", sanitizeMsg(string(r.stderr)))
	}

	objects, err := parseS5cmdJSON(r.stdout)
	if err != nil {
		return "", fmt.Errorf("failed to parse s5cmd output: %v", err)
	}
	if len(objects) == 0 {
		return "", fmt.Errorf("object not found")
	}

	obj := objects[0]
	return formatContentIdentifier(obj.ETag, obj.Size, obj.LastModified), nil
}

// verifyExpectedContent checks if the current S3 object matches the expected content identifier
func verifyExpectedContent(bucket, key, expectedCID string) error {
	if expectedCID == "" {
		// NOTHINGEXPECTED - object should not exist
		r := runS5cmd(s5cmdBin, "ls", fmt.Sprintf("s3://%s/%s", bucket, key))
		if r.code == 0 {
			return fmt.Errorf("object exists but nothing was expected")
		}
		return nil
	}

	currentCID, err := getContentIdentifier(bucket, key)
	if err != nil {
		return fmt.Errorf("failed to get current content: %v", err)
	}

	if currentCID != expectedCID {
		return fmt.Errorf("content mismatch: expected %s, got %s", expectedCID, currentCID)
	}

	return nil
}

// resetLocationState clears the current location and expected content
func resetLocationState() {
	if currentLocation != "" {
		delete(expectedContent, currentLocation)
		currentLocation = ""
	}
}

// handleListImportableContents lists all importable content from S3
func handleListImportableContents() {
	bucket, prefix, err := ensureCfg()
	if err != nil {
		debug("LISTIMPORTABLECONTENTS failed: " + err.Error())
		writeLine("END")
		return
	}

	// Build S3 path - include export ref if set
	s3Path := "s3://" + bucket
	ref := strings.Trim(getExportRef(), "/")
	if prefix != "" {
		s3Path += "/" + strings.TrimRight(prefix, "/")
	}
	if ref != "" {
		s3Path += "/" + ref
	}
	s3Path += "/"

	debug("Listing importable contents from: " + s3Path)

	// Use s5cmd ls --json to get all objects recursively
	r := runS5cmd(s5cmdBin, "ls", "--json", s3Path)
	if r.code != 0 {
		msg := sanitizeMsg(string(r.stderr))
		debug("s5cmd ls failed: " + msg)
		writeLine("END")
		return
	}

	objects, err := parseS5cmdJSON(r.stdout)
	if err != nil {
		debug("Failed to parse s5cmd output: " + err.Error())
		writeLine("END")
		return
	}

	// Emit CONTENT and CONTENTIDENTIFIER for each object
	basePrefix := bucket
	if prefix != "" {
		basePrefix += "/" + strings.TrimRight(prefix, "/")
	}
	if ref != "" {
		basePrefix += "/" + ref
	}
	basePrefix += "/"

	for _, obj := range objects {
		// Remove the base prefix to get relative name
		name := obj.Key
		if strings.HasPrefix(name, basePrefix) {
			name = strings.TrimPrefix(name, basePrefix)
		}
		// Skip if name is empty or just a directory marker
		if name == "" || strings.HasSuffix(name, "/") {
			continue
		}

		cid := formatContentIdentifier(obj.ETag, obj.Size, obj.LastModified)
		writeLine(fmt.Sprintf("CONTENT %d %s", obj.Size, name))
		writeLine("CONTENTIDENTIFIER " + cid)
	}

	writeLine("END")
}

// handleLocation stores the location for subsequent export operations
func handleLocation(name string) {
	currentLocation = name
	debug("LOCATION set to: " + name)
}

// handleExpected stores the expected content identifier for the current location
func handleExpected(contentIdentifier string) {
	if currentLocation == "" {
		debug("EXPECTED called but no current location")
		return
	}
	expectedContent[currentLocation] = contentIdentifier
	debug(fmt.Sprintf("EXPECTED for %s: %s", currentLocation, contentIdentifier))
}

// handleNothingExpected marks that nothing is expected at the current location
func handleNothingExpected() {
	if currentLocation == "" {
		debug("NOTHINGEXPECTED called but no current location")
		return
	}
	expectedContent[currentLocation] = ""
	debug("NOTHINGEXPECTED for: " + currentLocation)
}

// handleRetrieveExportExpected retrieves content only if it matches expected
func handleRetrieveExportExpected(filePath string) {
	if currentLocation == "" {
		writeLine("RETRIEVE-FAILURE missing location")
		return
	}

	bucket, prefix, err := ensureCfg()
	if err != nil {
		writeLine("RETRIEVE-FAILURE " + err.Error())
		return
	}

	obj := s3ExportKey(currentLocation, prefix)
	expected := expectedContent[currentLocation]

	// Verify expected content
	if err := verifyExpectedContent(bucket, obj, expected); err != nil {
		writeLine("RETRIEVE-FAILURE " + err.Error())
		resetLocationState()
		return
	}

	// Download the file
	_ = os.MkdirAll(filepath.Dir(filePath), 0o755)
	r := runS5cmd(s5cmdBin, "cp", fmt.Sprintf("s3://%s/%s", bucket, obj), filePath)
	resetLocationState()

	if r.code == 0 {
		writeLine("RETRIEVE-SUCCESS")
		return
	}

	msg := sanitizeMsg(string(r.stderr))
	if msg == "" {
		msg = fmt.Sprintf("s5cmd cp failed (exit %d)", r.code)
	}
	writeLine("RETRIEVE-FAILURE " + msg)
}

// handleStoreExportExpected stores content only if current content matches expected
func handleStoreExportExpected(key, filePath string) {
	if currentLocation == "" {
		writeLine("STORE-FAILURE " + key + " missing location")
		return
	}

	bucket, prefix, err := ensureCfg()
	if err != nil {
		writeLine("STORE-FAILURE " + key + " " + err.Error())
		return
	}

	obj := s3ExportKey(currentLocation, prefix)
	expected := expectedContent[currentLocation]

	// Verify expected content before storing
	if err := verifyExpectedContent(bucket, obj, expected); err != nil {
		writeLine("STORE-FAILURE " + key + " " + err.Error())
		resetLocationState()
		return
	}

	// Check local file exists
	if fi, err := os.Stat(filePath); err != nil || fi.IsDir() {
		writeLine("STORE-FAILURE " + key + " local file not found")
		resetLocationState()
		return
	}

	// Upload the file
	r := runS5cmd(s5cmdBin, "cp", filePath, fmt.Sprintf("s3://%s/%s", bucket, obj))
	if r.code != 0 {
		msg := sanitizeMsg(string(r.stderr))
		if msg == "" {
			msg = fmt.Sprintf("s5cmd cp failed (exit %d)", r.code)
		}
		writeLine("STORE-FAILURE " + key + " " + msg)
		resetLocationState()
		return
	}

	// Get the new content identifier
	newCID, err := getContentIdentifier(bucket, obj)
	if err != nil {
		// Store succeeded but we can't get CID - use fallback
		writeLine("STORE-SUCCESS " + key + " unknown")
		resetLocationState()
		return
	}

	writeLine("STORE-SUCCESS " + key + " " + newCID)
	resetLocationState()
}

// handleCheckPresentExportExpected checks presence and verifies expected content
func handleCheckPresentExportExpected(key string) {
	if currentLocation == "" {
		writeLine("CHECKPRESENT-UNKNOWN " + key + " missing location")
		return
	}

	bucket, prefix, err := ensureCfg()
	if err != nil {
		writeLine("CHECKPRESENT-UNKNOWN " + key + " " + err.Error())
		resetLocationState()
		return
	}

	obj := s3ExportKey(currentLocation, prefix)
	expected := expectedContent[currentLocation]

	// Verify expected content
	if err := verifyExpectedContent(bucket, obj, expected); err != nil {
		writeLine("CHECKPRESENT-FAILURE " + key)
		resetLocationState()
		return
	}

	writeLine("CHECKPRESENT-SUCCESS " + key)
	resetLocationState()
}

// handleRemoveExportExpected removes content only if it matches expected
func handleRemoveExportExpected(key string) {
	if currentLocation == "" {
		writeLine("REMOVE-FAILURE " + key + " missing location")
		return
	}

	bucket, prefix, err := ensureCfg()
	if err != nil {
		writeLine("REMOVE-FAILURE " + key + " " + err.Error())
		resetLocationState()
		return
	}

	obj := s3ExportKey(currentLocation, prefix)
	expected := expectedContent[currentLocation]

	// Verify expected content before removing
	if err := verifyExpectedContent(bucket, obj, expected); err != nil {
		writeLine("REMOVE-FAILURE " + key + " " + err.Error())
		resetLocationState()
		return
	}

	// Remove the object
	r := runS5cmd(s5cmdBin, "rm", fmt.Sprintf("s3://%s/%s", bucket, obj))
	resetLocationState()

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

// handleRemoveExportDirectoryWhenEmpty removes a directory if empty
func handleRemoveExportDirectoryWhenEmpty(directory string) {
	bucket, prefix, err := ensureCfg()
	if err != nil {
		writeLine("REMOVEEXPORTDIRECTORY-FAILURE")
		return
	}

	// Build S3 path for directory
	ref := strings.Trim(getExportRef(), "/")
	dirPath := directory
	if ref != "" {
		dirPath = ref + "/" + directory
	}
	obj := s3Key(dirPath, prefix)

	// Check if directory is empty by listing objects under it
	s3Path := fmt.Sprintf("s3://%s/%s/", bucket, strings.TrimRight(obj, "/"))
	_ = runS5cmd(s5cmdBin, "ls", s3Path)

	// If ls returns non-zero or no output, directory is empty or doesn't exist
	// In S3, directories don't really exist, so we just report success
	writeLine("REMOVEEXPORTDIRECTORY-SUCCESS")
}

func getInfoDict() map[string]string {
	m := map[string]string{}
	// Prefer already known config/env; avoid GETCONFIG during INFO/GETINFO.
	s3url := config["s3url"]
	if s3url == "" {
		s3url = envOr("ANNEX_S3URL", envOr("S3URL", ""))
	}
	if s3url != "" {
		m["s3url"] = s3url
	}
	m["driver"] = "s5cmd"
	m["tool.version"] = getS5cmdVersion()
	m["availability"] = "global"
	return m
}

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
			// Accept but ignore
			continue
		case line == "LISTCONFIGS":
			writeLine("CONFIG s3url")
			writeLine("CONFIG grants")
			writeLine("CONFIGEND")
			continue
		case line == "INFO":
			info := getInfoDict()
			keys := []string{"s3url", "driver", "tool.version", "availability"}
			for _, k := range keys {
				if v := strings.TrimSpace(info[k]); v != "" {
					writeLine(fmt.Sprintf("INFO %s: %s", k, v))
				}
			}
			writeLine("INFOEND")
			continue
		case line == "GETINFO":
			info := getInfoDict()
			keys := []string{"s3url", "driver", "tool.version", "availability"}
			for _, k := range keys {
				if v := strings.TrimSpace(info[k]); v != "" {
					writeLine(fmt.Sprintf("INFO %s: %s", k, v))
				}
			}
			writeLine("INFOEND")
			continue
		case strings.HasPrefix(line, "GETINFO "):
			// GETINFO key
			parts := strings.SplitN(line, " ", 2)
			key := ""
			if len(parts) == 2 {
				key = parts[1]
			}
			info := getInfoDict()
			if key == "url" || key == "remoteurl" || key == "config.s3url" || key == "setting.s3url" {
				if _, ok := info["s3url"]; ok {
					key = "s3url"
				}
			}
			if v := info[key]; v != "" {
				writeLine("VALUE " + v)
			} else {
				writeLine("VALUE")
			}
			continue
		case line == "GETGITREMOTENAME":
			writeLine("VALUE ")
			continue
		case strings.HasPrefix(line, "SETCONFIG "):
			// SETCONFIG key value
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
			writeLine("IMPORTSUPPORTED-SUCCESS")
			continue
		case line == "LISTIMPORTABLECONTENTS":
			handleListImportableContents()
			continue
		case strings.HasPrefix(line, "LOCATION "):
			name := strings.TrimPrefix(line, "LOCATION ")
			handleLocation(name)
			continue
		case strings.HasPrefix(line, "EXPECTED "):
			cid := strings.TrimPrefix(line, "EXPECTED ")
			handleExpected(cid)
			continue
		case line == "NOTHINGEXPECTED":
			handleNothingExpected()
			continue
		case strings.HasPrefix(line, "RETRIEVEEXPORTEXPECTED "):
			file := strings.TrimPrefix(line, "RETRIEVEEXPORTEXPECTED ")
			handleRetrieveExportExpected(file)
			continue
		case strings.HasPrefix(line, "STOREEXPORTEXPECTED "):
			// STOREEXPORTEXPECTED <key> <file>
			rest := strings.TrimPrefix(line, "STOREEXPORTEXPECTED ")
			parts := strings.SplitN(rest, " ", 2)
			if len(parts) != 2 {
				writeLine("ERROR malformed STOREEXPORTEXPECTED")
				continue
			}
			key := parts[0]
			file := parts[1]
			keyToFile[key] = file
			handleStoreExportExpected(key, file)
			continue
		case strings.HasPrefix(line, "CHECKPRESENTEXPORTEXPECTED "):
			key := strings.TrimPrefix(line, "CHECKPRESENTEXPORTEXPECTED ")
			handleCheckPresentExportExpected(key)
			continue
		case strings.HasPrefix(line, "REMOVEEXPORTEXPECTED "):
			key := strings.TrimPrefix(line, "REMOVEEXPORTEXPECTED ")
			handleRemoveExportExpected(key)
			continue
		case strings.HasPrefix(line, "REMOVEEXPORTDIRECTORYWHENEMPTY "):
			directory := strings.TrimPrefix(line, "REMOVEEXPORTDIRECTORYWHENEMPTY ")
			handleRemoveExportDirectoryWhenEmpty(directory)
			continue
		case strings.HasPrefix(line, "CHECKPRESENT "):
			key := strings.TrimPrefix(line, "CHECKPRESENT ")
			handleCheckPresent(key)
			continue
		case strings.HasPrefix(line, "CHECKPRESENTEXPORT "):
			key := strings.TrimPrefix(line, "CHECKPRESENTEXPORT ")
			handleCheckPresentExport(key)
			continue
		case strings.HasPrefix(line, "REMOVE "):
			key := strings.TrimPrefix(line, "REMOVE ")
			handleRemove(key)
			continue
		case strings.HasPrefix(line, "TRANSFER "):
			// TRANSFER <op> <key> <file>
			rest := strings.TrimPrefix(line, "TRANSFER ")
			parts := strings.SplitN(rest, " ", 2)
			if len(parts) != 2 {
				writeLine("ERROR malformed TRANSFER")
				continue
			}
			op := parts[0]
			rest2 := parts[1]
			parts2 := strings.SplitN(rest2, " ", 2)
			if len(parts2) != 2 {
				writeLine("ERROR malformed TRANSFER")
				continue
			}
			key := parts2[0]
			file := parts2[1]
			keyToFile[key] = file
			debug(fmt.Sprintf("TRANSFER %s key=%s file=%s", op, key, file))
			switch op {
			case "STORE":
				handleTransferStore(key, file)
			case "RETRIEVE":
				handleTransferRetrieve(key, file)
			default:
				writeLine("ERROR unknown TRANSFER op")
			}
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
			keyToFile[key] = file
			handleTransferExport(op, key, file)
			continue
		case strings.HasPrefix(line, "EXPORTSTORE "):
			// EXPORTSTORE <key> <file>
			rest := strings.TrimPrefix(line, "EXPORTSTORE ")
			parts := strings.SplitN(rest, " ", 2)
			if len(parts) != 2 {
				writeLine("ERROR malformed EXPORTSTORE")
				continue
			}
			key := parts[0]
			file := parts[1]
			keyToFile[key] = file
			handleExportStore(key, file)
			continue
		case strings.HasPrefix(line, "REMOVEEXPORT "):
			key := strings.TrimPrefix(line, "REMOVEEXPORT ")
			handleRemoveExport(key)
			continue
		case line == "GETCOST":
			writeLine("COST 200")
			continue
		case line == "GETAVAILABILITY":
			writeLine("AVAILABILITY GLOBAL")
			continue
		case strings.HasPrefix(line, "EXPORT "):
			name := strings.TrimPrefix(line, "EXPORT ")
			handleExport(name)
			continue
		default:
			writeLine("UNSUPPORTED-REQUEST")
		}
	}
}
