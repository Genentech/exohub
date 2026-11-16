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

	"github.com/Genentech/exohub/go/useragent"
)

// External special-remote for git-annex that exports files as plain objects
// to S3, preserving their paths. Designed for ArtifactDB to read directly.
//
// Files exported under .exohub/bundles/<ref>/ are uploaded to s3url/<ref>/...
// Files exported under .artifactdb/ are uploaded to s3url/<ref>/.artifactdb/...
//
// Required init param: s3url=s3://bucket/prefix
// Optional init param: instance_url=https://artifactdb-api/v1/project
// Optional init param: atlas_url=https://atlas.example.com/v1/sandbox1 (alternate ExoHub Atlas
// to receive .exohub-bundle/ instead of the deployment's default Atlas)
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

// currentGrantsAccountID is the Access Grants account id (--account-id) to
// request credentials from, sticky across the proactive/reactive refreshes
// in refreshCredentialsIfNeeded/runS5cmd, which only know the s3url, not the
// remote's grants_account_id. Set by fetchGrantsCredentialsForAccount.
var currentGrantsAccountID string

func fetchGrantsCredentials(s3url string) error {
	return fetchGrantsCredentialsForAccount(s3url, currentGrantsAccountID, "")
}

// fetchGrantsCredentialsForAccount fetches S3 Access Grants credentials for
// s3url, requesting them under accountID when set (per-remote
// grants_account_id, passed as --account-id to the credential helper) and
// falling back to the helper's own default resolution otherwise. sourceS3URL,
// when non-empty (role: instance publish only), is passed as --source-s3url
// so the helper's server fallback authorizes against the source Atlas
// project instead of the instance bucket.
func fetchGrantsCredentialsForAccount(s3url, accountID, sourceS3URL string) error {
	currentGrantsAccountID = accountID
	args := []string{"--s3url", s3url}
	if accountID != "" {
		args = append(args, "--account-id", accountID)
	}
	if sourceS3URL != "" {
		args = append(args, "--source-s3url", sourceS3URL)
	}
	cmd := exec.Command(credHelperBin, args...)
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

// configuredGrantsAccountID returns the git-annex special remote's
// grants_account_id config (the Access Grants account to request
// credentials from for the PREPARE/export protocol path), falling back to
// exo-credential-helper's own resolution (EXOHUB_GRANTS_ACCOUNT_ID, then the
// build default) when unset.
func configuredGrantsAccountID() string {
	if v := config["grants_account_id"]; v != "" {
		return v
	}
	return getConfigFromAnnex("grants_account_id")
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

// runS5cmdSync runs s5cmd sync with progress counter on stderr. excludes are
// passed through as repeated --exclude <pattern> flags (relative to src),
// e.g. to keep a bundle upload from ever carrying a nested .artifactdb/
// subtree regardless of how the bundle dir was built (exo-cli#177).
// Returns the number of files synced.
func runS5cmdSync(s5cmd, src, dst string, excludes ...string) (int, error) {
	refreshCredentialsIfNeeded()
	args := []string{"sync"}
	for _, pattern := range excludes {
		args = append(args, "--exclude", pattern)
	}
	args = append(args, src, dst)
	cmd := exec.Command(s5cmd, args...)
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

// cleanupStaleBundleArtifactDB removes a stray <s3Base>/.exohub-bundle/.artifactdb/
// prefix, if any, left over from a publish made with a buggy exo-cli version
// that nested a ref's .artifactdb/ inside the bundle dir (exo-cli#177).
// s5cmd sync never deletes destination objects, so without this, re-running
// a fixed publish would leave the stale nested objects in place forever,
// still rejected by the Atlas as instance-typed documents it shouldn't have
// received. Listing failures are treated as "nothing to clean up" rather
// than fatal, since this is a best-effort self-heal, not a required step of
// publishing.
func cleanupStaleBundleArtifactDB(s5cmd, s3Base string) {
	stalePrefix := strings.TrimRight(s3Base, "/") + "/.exohub-bundle/.artifactdb/"
	lsResult := runS5cmd(s5cmd, "ls", stalePrefix+"*")
	if lsResult.code != 0 || len(bytes.TrimSpace(lsResult.stdout)) == 0 {
		return
	}

	fmt.Fprintf(os.Stderr, "\U0001f9f9  Removing stale nested %s (exo-cli#177 self-heal)\n", stalePrefix)
	rmResult := runS5cmd(s5cmd, "rm", stalePrefix+"*")
	if rmResult.code != 0 {
		msg := sanitizeMsg(string(rmResult.stderr))
		if msg == "" {
			msg = fmt.Sprintf("s5cmd rm failed (exit %d)", rmResult.code)
		}
		fmt.Fprintf(os.Stderr, "⚠️  failed to remove stale %s: %s\n", stalePrefix, msg)
	}
}

// runPublishBundleUpload is the testable core of the --role atlas (default)
// bundle upload: sync bundleDir to <s3Base>/.exohub-bundle/ with the
// .artifactdb/ subtree excluded (defense in depth: the bundle upload must
// never carry a nested .artifactdb/ subtree, since the Atlas crawls
// .exohub-bundle/ and rejects instance-typed documents it finds there,
// regardless of whether bundleDir happens to contain one), then self-heal
// any stray <s3Base>/.exohub-bundle/.artifactdb/ objects left over from a
// publish made with a buggy exo-cli version that nested a ref's
// .artifactdb/ inside the bundle dir (exo-cli#177).
func runPublishBundleUpload(s5cmd, bundleDir, s3Base string) (int, error) {
	bundleDst := s3Base + "/.exohub-bundle/"
	// Progress counter shown during sync, no separate "Syncing to..." line
	count, err := runS5cmdSync(s5cmd, bundleDir+"/", bundleDst, ".artifactdb/*")
	if err != nil {
		return count, err
	}

	cleanupStaleBundleArtifactDB(s5cmd, s3Base)
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
	if v, ok := config["atlas_url"]; ok && v != "" {
		writeLine("CONFIG atlas_url=" + v)
	}
	if v, ok := config["grants"]; ok && v != "" {
		writeLine("CONFIG grants=" + v)
	}
	if v, ok := config["grants_account_id"]; ok && v != "" {
		writeLine("CONFIG grants_account_id=" + v)
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
		if err := fetchGrantsCredentialsForAccount(config["s3url"], configuredGrantsAccountID(), ""); err != nil {
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

// stringSliceFlag collects a repeatable flag's values in the order given,
// e.g. --exclude a --exclude b.
type stringSliceFlag []string

func (s *stringSliceFlag) String() string {
	return strings.Join(*s, ",")
}

func (s *stringSliceFlag) Set(value string) error {
	*s = append(*s, value)
	return nil
}

// runPublish dispatches the publish subcommand by --role: "atlas" (default,
// bundle-only, runPublishAtlas) or "instance" (runPublishInstance). It also
// enforces the removed flags (--instance-url always, --artifactdb-dir for
// --role atlas, --bundle-dir for --role instance) before any upload or API
// call, by checking which flags the caller actually passed (fs.Visit) rather
// than their (possibly non-empty-by-default) values.
func runPublish(args []string) {
	fs := flag.NewFlagSet("publish", flag.ExitOnError)
	s3url := fs.String("s3url", "", "S3 URL for upload")
	bundleDir := fs.String("bundle-dir", "", "Path to bundle output directory (role: atlas only)")
	artifactdbDir := fs.String("artifactdb-dir", "", "Path to .artifactdb/ directory (role: instance only)")
	grants := fs.Bool("grants", false, "Use S3 Access Grants for credentials")
	grantsAccountID := fs.String("grants-account-id", "", "Access Grants account id override for the credential helper")
	sourceS3URL := fs.String("source-s3url", "", "s3url of the resolved data_from Atlas remote (role: instance only), so the credential helper's server fallback authorizes against the source project")
	_ = fs.String("instance-url", "", "removed — use --role instance --url instead")
	url := fs.String("url", "", "ArtifactDB instance URL (role: instance only)")
	atlasURL := fs.String("atlas-url", "", "ExoHub Atlas URL (optional)")
	projectIDFlag := fs.String("project-id", "", "Project ID override (defaults to repo name from bundle.json)")
	refName := fs.String("ref-name", "", "Published ref name (role: instance only)")
	apiURL := fs.String("api-url", resolveAPIBase(), "ExoHub API URL")
	outputJSON := fs.Bool("json", false, "Print a single JSON publish result line to stdout")
	role := fs.String("role", "atlas", "Publish role: atlas or instance")
	refKind := fs.String("ref-kind", "", "Published ref kind: tag or branch (enables tag-immutability checks)")
	commit := fs.String("commit", "", "Commit SHA the published ref resolves to")
	repoURL := fs.String("repo-url", "", "Repository clone URL this publish comes from (role: instance only; role: atlas reads it from bundle.json)")
	force := fs.Bool("force", false, "Bypass the source-repo guard (never bypasses tag immutability)")
	var excludes stringSliceFlag
	fs.Var(&excludes, "exclude", "Glob pattern (relative to .artifactdb/) to exclude from an instance publish (repeatable; role: instance only)")
	fs.Parse(args)

	visited := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { visited[f.Name] = true })

	if visited["instance-url"] {
		fmt.Fprintln(os.Stderr, "Error: --instance-url was removed; publish instances with: publish --role instance --url <instance> --s3url <s3url> --artifactdb-dir <dir>")
		os.Exit(1)
	}

	debugStderr("publish: role=%s s3url=%s api-url=%s atlas-url=%s grants=%v", *role, *s3url, *apiURL, *atlasURL, *grants)

	switch *role {
	case "", "atlas":
		if visited["artifactdb-dir"] {
			fmt.Fprintln(os.Stderr, "Error: --artifactdb-dir was removed from --role atlas; publish .artifactdb/ with: publish --role instance")
			os.Exit(1)
		}
		if visited["exclude"] {
			fmt.Fprintln(os.Stderr, "Error: --exclude is not allowed for --role atlas")
			os.Exit(1)
		}
		runPublishAtlas(*s3url, *bundleDir, *grants, *grantsAccountID, *atlasURL, *projectIDFlag, *apiURL, *outputJSON, *refKind, *commit, *force)
	case "instance":
		if visited["bundle-dir"] {
			fmt.Fprintln(os.Stderr, "Error: --bundle-dir is not allowed for --role instance")
			os.Exit(1)
		}
		runPublishInstance(instancePublishArgs{
			s3url:           *s3url,
			artifactdbDir:   *artifactdbDir,
			instanceURL:     *url,
			atlasURL:        *atlasURL,
			sourceS3URL:     *sourceS3URL,
			projectID:       *projectIDFlag,
			refName:         *refName,
			apiURL:          *apiURL,
			grants:          *grants,
			grantsAccountID: *grantsAccountID,
			refKind:         *refKind,
			commit:          *commit,
			repoURL:         *repoURL,
			force:           *force,
			outputJSON:      *outputJSON,
			excludes:        []string(excludes),
		})
	default:
		fmt.Fprintf(os.Stderr, "Error: invalid --role %q (must be: atlas or instance)\n", *role)
		os.Exit(1)
	}
}

// runPublishAtlas implements --role atlas (the default): upload the bundle
// directory to <s3url>/<ref_name>/.exohub-bundle/ and notify the ExoHub API
// with targets: [] (instances are reached only via publish --role instance).
func runPublishAtlas(s3url, bundleDir string, grants bool, grantsAccountID, atlasURL, projectIDFlag, apiURL string, outputJSON bool, refKind, commit string, force bool) {
	if s3url == "" || bundleDir == "" {
		fmt.Fprintln(os.Stderr, "Error: --s3url and --bundle-dir are required")
		os.Exit(1)
	}

	// Read bundle.json for ref info
	bundleJSON, err := os.ReadFile(filepath.Join(bundleDir, "bundle.json"))
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

	projectID := projectIDFlag
	if projectID == "" {
		projectID = repoNameFromURL(info.RepoURL)
	}
	s3Base := strings.TrimRight(s3url, "/") + "/" + info.RefName

	fmt.Fprintf(os.Stderr, "📊  Project: %s, Version: %s\n", orange(projectID), orange(info.RefName))
	fmt.Fprintf(os.Stderr, "☁️  Target: %s\n", yellow(s3Base+"/"))
	printBundleTarget(apiURL, atlasURL)

	s5 := envOr("S5CMD_BIN", "s5cmd")

	// Get credentials if grants enabled, BEFORE the tag-immutability read
	// below: that read uses s5cmd and must run with credentials in place,
	// or a credentials/access failure would otherwise look just like "no
	// record" and the check would silently never apply.
	if grants {
		fmt.Fprintln(os.Stderr, "🔑  Fetching S3 credentials via grants")
		if err := fetchGrantsCredentialsForAccount(s3url, grantsAccountID, ""); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	}

	effectiveCommit := commit
	if effectiveCommit == "" {
		effectiveCommit = info.Ref
	}
	if err := checkTagImmutability(s5, refKind, info.RefName, effectiveCommit, s3Base+"/.exohub-bundle/bundle.json", extractBundleRef); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		if outputJSON {
			printPublishJSON(publishJSONResult{Published: false, Error: &publishJSONError{Message: err.Error()}})
		}
		os.Exit(1)
	}

	if err := checkSourceRepoGuard(s5, info.RepoURL, s3Base+"/.exohub-bundle/bundle.json", extractBundleRepoURL, force); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		if outputJSON {
			printPublishJSON(publishJSONResult{Published: false, Error: &publishJSONError{Message: err.Error()}})
		}
		os.Exit(1)
	}

	count, err := runPublishBundleUpload(s5, bundleDir, s3Base)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\n⚠️  bundle sync failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "\r🔄  Synced %d file(s)              \n", count)

	// Upload permissions.json if .exohub/permissions exists
	if err := uploadPermissions(s5, s3Base, "."); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  permissions upload failed: %v\n", err)
		// Non-fatal — continue with notification
	}

	// Notify ExoHub API — targets is always empty: instances are reached by
	// publish --role instance, never forwarded from here.
	s3Location := s3Base + "/"
	respBody, err := publishNotify(apiURL, projectID, info.RepoURL, info.Ref, info.RefName, s3Location, s3url, "", atlasURL, force)
	if err != nil {
		var apiErr *apiError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusUnprocessableEntity {
			fmt.Fprintf(os.Stderr, "❌  %s\n", describePublishError(apiErr.Body))
			fmt.Fprintf(os.Stderr, "⚠️  bundle uploaded to %s but NOT published: %v\n", s3Location, err)
			if outputJSON {
				printPublishJSON(publishJSONResult{Published: false, Error: publishJSONErrorFromBody(apiErr.Body)})
			}
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "⚠️  ExoHub API notification failed: %v\n", err)
		fmt.Fprintln(os.Stderr, "⚠️  Upload completed but indexing may not have been triggered.")
		if outputJSON {
			printPublishJSON(publishJSONResult{Published: false, Error: &publishJSONError{Message: err.Error()}})
		}
	} else {
		fmt.Fprintf(os.Stderr, "📡  Notified ExoHub API (project: %s, version: %s)\n", orange(projectID), orange(info.RefName))
		// Display per-target results
		var resp struct {
			Status       string `json:"status"`
			AtlasURLUsed string `json:"atlas_url_used,omitempty"`
			Results      []struct {
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
		if resp.AtlasURLUsed != "" {
			fmt.Fprintf(os.Stderr, "✅  Published to %s\n", orange(resp.AtlasURLUsed))
		}
		if outputJSON {
			var atlasURLUsed *string
			if resp.AtlasURLUsed != "" {
				v := resp.AtlasURLUsed
				atlasURLUsed = &v
			}
			printPublishJSON(publishJSONResult{AtlasURLUsed: atlasURLUsed, Published: true})
		}
	}
}

// checkTagImmutability enforces the spec "Published tags are immutable on
// every role": when refKind is "tag", it reads the remote's existing record
// of the version at recordURL (via s5cmd cat) and extracts the previously
// published commit with extractCommit. No record (object absent, or the
// field missing/empty) means a first or earlier-failed attempt and is
// allowed to proceed; the same commit means an idempotent retry and is also
// allowed; a different commit is refused before any upload. refKind
// "branch", or empty (caller didn't opt in), never checks.
func checkTagImmutability(s5cmd, refKind, refName, commit, recordURL string, extractCommit func([]byte) (string, bool)) error {
	if refKind != "tag" {
		return nil
	}
	if commit == "" {
		return fmt.Errorf("--commit is required with --ref-kind tag")
	}
	r := runS5cmd(s5cmd, "cat", recordURL)
	if r.code != 0 {
		msg := sanitizeMsg(string(r.stderr))
		if isNotFoundS5cmdError(msg) {
			return nil // no record: first publish, or an earlier attempt that never completed
		}
		// Any other failure (access denied, bad/expired credentials, network)
		// must NOT be treated as "no record" — that would let tag immutability
		// fail open. Refuse instead of guessing.
		return fmt.Errorf("could not read the existing record for tag %q at %s: %s", refName, recordURL, msg)
	}
	prevCommit, ok := extractCommit(r.stdout)
	if !ok {
		// The record exists but doesn't parse (corrupted/unexpected content)
		// — anomalous, not "no record". Treating it as "proceed" would let a
		// tag overwrite a version exo can't actually verify. Refuse.
		return fmt.Errorf("refusing to publish tag %q: the existing record at %s could not be parsed", refName, recordURL)
	}
	if prevCommit == commit {
		return nil // retry of the same tag
	}
	return fmt.Errorf("refusing to publish tag %q: already published from commit %s, but it now resolves to %s — publish a new tag instead", refName, prevCommit, commit)
}

// isNotFoundS5cmdError reports whether an s5cmd `cat`/`ls` failure's stderr
// looks like "no such object" (safe to treat as "no record exists" in
// checkTagImmutability) rather than a credentials, access, or network
// problem, which must be surfaced as a refusal instead of silently treated
// as "no record" (tag immutability must fail closed, not open).
func isNotFoundS5cmdError(stderr string) bool {
	lower := strings.ToLower(stderr)
	patterns := []string{"nosuchkey", "no such key", "key not found", "not found", "404", "no object found", "does not exist"}
	for _, p := range patterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// extractBundleRef extracts the previously published commit from an Atlas
// bundle.json body (its "ref" field), for checkTagImmutability.
func extractBundleRef(body []byte) (string, bool) {
	var v struct {
		Ref string `json:"ref"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return "", false
	}
	return v.Ref, v.Ref != ""
}

// extractMarkerCommit extracts the previously published commit from an
// instance's ..complete marker body (its "commit" field), for
// checkTagImmutability.
func extractMarkerCommit(body []byte) (string, bool) {
	var v struct {
		Commit string `json:"commit"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return "", false
	}
	return v.Commit, v.Commit != ""
}

// extractBundleRepoURL extracts the source repository from an Atlas
// bundle.json body (its "repo_url" field), for checkSourceRepoGuard.
func extractBundleRepoURL(body []byte) (string, bool) {
	var v struct {
		RepoURL string `json:"repo_url"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return "", false
	}
	return v.RepoURL, v.RepoURL != ""
}

// extractMarkerSourceRepo extracts the source repository from an instance's
// ..complete marker body (its "source_repo" field), for
// checkSourceRepoGuard.
func extractMarkerSourceRepo(body []byte) (string, bool) {
	var v struct {
		SourceRepo string `json:"source_repo"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return "", false
	}
	return v.SourceRepo, v.SourceRepo != ""
}

// normalizeRepoURL normalises a repository clone URL for the source-repo
// guard (spec "Source-repo guard on every role"): scheme and userinfo
// (credentials) stripped, host lowercased, a trailing ".git" and/or "/"
// removed. Two clone URLs that normalise to the same value identify the
// same source repository, regardless of scheme (https vs ssh/scp-like
// user@host:path), credentials, host case, or a trailing slash/".git".
func normalizeRepoURL(repoURL string) string {
	s := strings.TrimSpace(repoURL)
	if s == "" {
		return ""
	}
	// scp-like syntax (user@host:path, no scheme) becomes host/path before
	// the generic scheme/userinfo handling below.
	if !strings.Contains(s, "://") {
		if at := strings.Index(s, "@"); at >= 0 {
			rest := s[at+1:]
			if colon := strings.Index(rest, ":"); colon >= 0 && !strings.Contains(rest[:colon], "/") {
				s = rest[:colon] + "/" + rest[colon+1:]
			}
		}
	}
	if idx := strings.Index(s, "://"); idx >= 0 {
		s = s[idx+3:]
	}
	if at := strings.LastIndex(s, "@"); at >= 0 {
		if slash := strings.Index(s, "/"); slash < 0 || at < slash {
			s = s[at+1:]
		}
	}
	host, path := s, ""
	if idx := strings.Index(s, "/"); idx >= 0 {
		host, path = s[:idx], s[idx:]
	}
	s = strings.ToLower(host) + path
	s = strings.TrimSuffix(s, "/")
	s = strings.TrimSuffix(s, ".git")
	s = strings.TrimSuffix(s, "/")
	return s
}

// readSourceRepoRecord reads recordURL and extracts its source repository
// with extractSourceRepo. found=false with a nil error covers both "no
// record" (object absent: first publish, or an earlier attempt that never
// completed) and "a record exists but carries no source repository" (a
// legacy record) — both are unknown and don't block a source-repo guard.
// Any other read failure (access denied, bad/expired credentials, network)
// is returned as an error: it must NOT be treated as "no record", or a
// guard built on this would fail open.
func readSourceRepoRecord(s5cmd, recordURL string, extractSourceRepo func([]byte) (string, bool)) (repo string, found bool, err error) {
	r := runS5cmd(s5cmd, "cat", recordURL)
	if r.code != 0 {
		msg := sanitizeMsg(string(r.stderr))
		if isNotFoundS5cmdError(msg) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("could not read the existing record at %s: %s", recordURL, msg)
	}
	prevRepo, ok := extractSourceRepo(r.stdout)
	if !ok {
		return "", false, nil
	}
	return prevRepo, true, nil
}

// checkSourceRepoGuard enforces the spec "Source-repo guard on every
// role": before any upload, on both roles and for branches as well as
// tags, exo compares the current source repository (currentRepoURL) with
// the remote's existing record of the version at recordURL (the instance's
// ..complete "source_repo", or the Atlas's bundle.json "repo_url"),
// extracted by extractSourceRepo, both normalised with normalizeRepoURL.
// No record (object absent), or a record without a source repository (a
// legacy record), is unknown and does not block. A different source
// repository is refused, naming both, unless force is true — force never
// bypasses tag immutability (checkTagImmutability, a separate check).
func checkSourceRepoGuard(s5cmd, currentRepoURL, recordURL string, extractSourceRepo func([]byte) (string, bool), force bool) error {
	prevRepo, found, err := readSourceRepoRecord(s5cmd, recordURL, extractSourceRepo)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	if normalizeRepoURL(prevRepo) == normalizeRepoURL(currentRepoURL) {
		return nil // same source repository
	}
	if force {
		return nil
	}
	return fmt.Errorf("refusing to publish: this version was already published from %q, but this is %q — pass --force to publish anyway", normalizeRepoURL(prevRepo), normalizeRepoURL(currentRepoURL))
}

// printBundleTarget prints, before publishing, where the bundle will be sent:
// the configured atlas_url, or the deployment's default Atlas (read from the
// API when available). The default case is visually flagged with ⚠ — it's
// the case that caused the original incident, a bundle silently landing in
// the wrong Atlas — while an explicit atlas_url is not flagged. Falls back
// to a generic "server-chosen" marker for older servers that don't expose
// default_atlas_url.
func printBundleTarget(apiBase, atlasURL string) {
	if atlasURL != "" {
		fmt.Fprintf(os.Stderr, "📍  bundle → %s (atlas_url)\n", atlasURL)
	} else if defaultURL, ok := getServiceInfo(apiBase); ok {
		fmt.Fprintf(os.Stderr, "📍  bundle → ⚠ default Atlas %s\n", defaultURL)
	} else {
		fmt.Fprintln(os.Stderr, "📍  bundle → ⚠ default Atlas (server-chosen)")
	}
}

// getServiceInfo fetches the deployment's default Atlas URL from the ExoHub
// API's service-info endpoint (GET /api/), added as a companion change.
// It tolerates connection errors, non-200 responses, and a missing field
// (older servers) by reporting ok=false.
func getServiceInfo(apiBase string) (defaultAtlasURL string, ok bool) {
	endpoint := strings.TrimRight(apiBase, "/") + "/"
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return "", false
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", false
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", false
	}
	var info struct {
		DefaultAtlasURL string `json:"default_atlas_url"`
	}
	if err := json.Unmarshal(body, &info); err != nil || info.DefaultAtlasURL == "" {
		return "", false
	}
	return info.DefaultAtlasURL, true
}

// publishErrorDetail is the structured 422 "detail" shape the ExoHub API
// returns for a refused publish (exohub-workers#115): a stable code plus a
// human message, so exo maps the code to a concrete fix instead of guessing
// from free text.
type publishErrorDetail struct {
	Code    string   `json:"code"`
	Message string   `json:"message"`
	Allowed []string `json:"allowed,omitempty"`
}

// publishErrorFixes maps a known refusal code to the action that resolves it.
// The API returns these two allowlist-check codes (evaluated before
// dry_run): atlas_url_not_allowlisted when the remote's atlas_url isn't on
// the deployment's allowlist, and instance_url_not_allowlisted when its
// instance_url isn't. Unknown codes fall back to the server's own message
// (see describePublishError).
var publishErrorFixes = map[string]string{
	"atlas_url_not_allowlisted":    "update the catalog remote's atlas_url in .exohub/remotes to an allowed value, then run exo init",
	"instance_url_not_allowlisted": "update the catalog remote's instance_url in .exohub/remotes to an allowed value, then run exo init",
}

// parseStructuredPublishError extracts the structured {"detail": {"code",
// "message", "allowed"}} shape from a /publish error body, if present
// (ok=false when the body doesn't have a non-empty "code").
func parseStructuredPublishError(body []byte) (publishErrorDetail, bool) {
	var structured struct {
		Detail publishErrorDetail `json:"detail"`
	}
	if err := json.Unmarshal(body, &structured); err == nil && structured.Detail.Code != "" {
		return structured.Detail, true
	}
	return publishErrorDetail{}, false
}

// describePublishError renders a readable message from a /publish error body.
// It tries the structured {"detail": {"code", "message", "allowed"}} shape
// first and maps known codes to a concrete fix; unknown codes fall back to
// the server's message. It then falls back to the existing FastAPI
// string/list "detail" shapes (plain message, or pydantic validation-error
// list), and finally to the raw body.
func describePublishError(body []byte) string {
	if detail, ok := parseStructuredPublishError(body); ok {
		msg := detail.Message
		if msg == "" {
			msg = detail.Code
		}
		if fix, ok := publishErrorFixes[detail.Code]; ok {
			msg = fmt.Sprintf("%s — fix: %s", msg, fix)
		}
		if len(detail.Allowed) > 0 {
			msg = fmt.Sprintf("%s (allowed: %s)", msg, strings.Join(detail.Allowed, ", "))
		}
		return msg
	}

	var withString struct {
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(body, &withString); err == nil && withString.Detail != "" {
		return withString.Detail
	}

	var withList struct {
		Detail []struct {
			Loc []interface{} `json:"loc"`
			Msg string        `json:"msg"`
		} `json:"detail"`
	}
	if err := json.Unmarshal(body, &withList); err == nil && len(withList.Detail) > 0 {
		lines := make([]string, 0, len(withList.Detail))
		for _, e := range withList.Detail {
			parts := make([]string, 0, len(e.Loc))
			for _, p := range e.Loc {
				parts = append(parts, fmt.Sprintf("%v", p))
			}
			lines = append(lines, fmt.Sprintf("%s: %s", strings.Join(parts, "."), e.Msg))
		}
		return strings.Join(lines, "\n")
	}

	return string(body)
}

// publishJSONError is the "error" object in the --json publish result line:
// {code, message, allowed}. code/message are always present (empty string
// when the server's error shape doesn't carry a code, e.g. an old-server
// plain-string detail); allowed is omitted when empty.
type publishJSONError struct {
	Code    string   `json:"code"`
	Message string   `json:"message"`
	Allowed []string `json:"allowed,omitempty"`
}

// publishJSONResult is the single-line JSON object --json prints to stdout
// after a publish attempt: {atlas_url_used, published, error}.
// atlas_url_used is null on failure or when the response didn't carry it.
// error is present only when published is false.
type publishJSONResult struct {
	AtlasURLUsed *string           `json:"atlas_url_used"`
	Published    bool              `json:"published"`
	Error        *publishJSONError `json:"error,omitempty"`
}

// publishJSONErrorFromBody builds the structured JSON error object for a
// /publish error body: the server's {code, message, allowed} when present,
// otherwise code="" and message=describePublishError's rendered fallback
// (plain-string detail, FastAPI validation-error list, or raw body).
func publishJSONErrorFromBody(body []byte) *publishJSONError {
	if detail, ok := parseStructuredPublishError(body); ok {
		return &publishJSONError{Code: detail.Code, Message: detail.Message, Allowed: detail.Allowed}
	}
	return &publishJSONError{Message: describePublishError(body)}
}

// printPublishJSON writes a single-line JSON publish result to stdout.
func printPublishJSON(result publishJSONResult) {
	data, err := json.Marshal(result)
	if err != nil {
		return
	}
	fmt.Println(string(data))
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

// loadPermissionsDocument reads <rootDir>/.exohub/permissions and converts
// it to the ArtifactDB permissions document (scope: "project", owners,
// read_access, write_access, optional viewers), validating required fields.
// Returns (nil, nil) when the file does not exist (nothing to upload). This
// is split from the upload itself so a caller can validate everything
// up front, before any upload (spec, instance role: "An invalid permissions
// file SHALL stop the publish before any upload").
func loadPermissionsDocument(rootDir string) (map[string]interface{}, error) {
	permFile := filepath.Join(rootDir, ".exohub", "permissions")
	data, err := os.ReadFile(permFile)
	if os.IsNotExist(err) {
		return nil, nil // no permissions file, nothing to upload
	}
	if err != nil {
		return nil, err
	}

	// Parse YAML using a proper parser so quoted values ("public", "example-user")
	// are stripped of their surrounding quotes before being uploaded.
	var perm permissionsYAML
	if err := yaml.Unmarshal(data, &perm); err != nil {
		return nil, fmt.Errorf("parse .exohub/permissions: %w", err)
	}

	// Validate required fields before uploading — ArtifactDB rejects zero-values
	// with confusing enum/validation errors.
	if len(perm.Owners) == 0 {
		return nil, fmt.Errorf(".exohub/permissions: owners must be non-empty")
	}
	if perm.ReadAccess == "" {
		return nil, fmt.Errorf(".exohub/permissions: read_access must be set")
	}
	if perm.WriteAccess == "" {
		return nil, fmt.Errorf(".exohub/permissions: write_access must be set")
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
	return adbPerm, nil
}

// uploadPermissionsDocument uploads doc (from loadPermissionsDocument) as
// JSON to dst (an exact S3 object key). A nil doc is a no-op.
func uploadPermissionsDocument(s5cmd, dst string, doc map[string]interface{}) error {
	if doc == nil {
		return nil
	}

	tmpFile, err := os.CreateTemp("", "permissions-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmpFile.Name())

	body, _ := json.MarshalIndent(doc, "", "  ")
	if _, err := tmpFile.Write(body); err != nil {
		return err
	}
	tmpFile.Close()

	r := runS5cmd(s5cmd, "cp", tmpFile.Name(), dst)
	if r.code != 0 {
		return fmt.Errorf("s5cmd cp failed: %s", sanitizeMsg(string(r.stderr)))
	}
	fmt.Fprintf(os.Stderr, "🔒  Permissions uploaded\n")
	return nil
}

// uploadPermissionsTo loads and uploads <rootDir>/.exohub/permissions in
// one step, to dst (an exact S3 object key). Used by the Atlas role
// (version-root permissions.json via uploadPermissions below), which has no
// "validate everything up front" requirement. The instance role instead
// calls loadPermissionsDocument early (with the rest of its pre-upload
// validation) and uploadPermissionsDocument later, so an invalid file is
// caught before any upload (see doPublishInstance in publish_instance.go).
func uploadPermissionsTo(s5cmd, dst, rootDir string) error {
	doc, err := loadPermissionsDocument(rootDir)
	if err != nil {
		return err
	}
	return uploadPermissionsDocument(s5cmd, dst, doc)
}

// uploadPermissions uploads <rootDir>/.exohub/permissions, converted, to
// <s3Base>/permissions.json (the Atlas destination: the version root).
// rootDir is the directory that contains .exohub/ (usually ".").
func uploadPermissions(s5cmd, s3Base, rootDir string) error {
	return uploadPermissionsTo(s5cmd, s3Base+"/permissions.json", rootDir)
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

func publishNotify(apiBase, projectID, repoURL, ref, refName, s3Location, s3url, instanceURL, atlasURL string, force bool) ([]byte, error) {
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
	// atlas_url is only added when set, so the payload stays byte-identical
	// to pre-atlas_url requests for remotes that don't configure it (an API
	// that predates atlas_url support may reject unknown keys).
	if atlasURL != "" {
		payload["atlas_url"] = atlasURL
	}
	// force is only added when true, so the payload stays byte-identical to
	// pre-source-repo-guard requests for a non-forced publish (an older API
	// may reject an unknown key).
	if force {
		payload["force"] = true
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
		return respBody, &apiError{StatusCode: resp.StatusCode, Body: respBody}
	}
	return respBody, nil
}

// apiError is returned by publishNotify for a non-2xx /publish response, so
// callers can distinguish a 422 refusal (readable message, hard failure)
// from other failures (soft warning, as before) without string-matching the
// error text.
type apiError struct {
	StatusCode int
	Body       []byte
}

func (e *apiError) Error() string {
	return fmt.Sprintf("API returned %d: %s", e.StatusCode, string(e.Body))
}

// loadExoToken resolves the exo login bearer token.
// Mirrors go/exo/commands/login/storage.go LoadToken and
// go/exo-credential-helper/main.go loadBearerToken; keep these three in sync.
// Precedence: EXO_TOKEN > EXO_TOKEN_FILE > EXO_CONFIG_DIR-derived path > default path.
func loadExoToken() string {
	if inline := os.Getenv("EXO_TOKEN"); inline != "" {
		return extractAccessToken([]byte(inline))
	}

	tokenPath := os.Getenv("EXO_TOKEN_FILE")
	if tokenPath == "" {
		if configRoot := os.Getenv("EXO_CONFIG_DIR"); configRoot != "" {
			tokenPath = filepath.Join(configRoot, "exo", "credentials", "token.json")
		} else {
			configDir, err := os.UserConfigDir()
			if err != nil {
				return ""
			}
			tokenPath = filepath.Join(configDir, "exo", "credentials", "token.json")
		}
	}

	data, err := os.ReadFile(tokenPath)
	if err != nil {
		return ""
	}
	return extractAccessToken(data)
}

// extractAccessToken parses a JSON oauth2 token ({"access_token": "..."}),
// or, if the data isn't JSON, treats it as a raw JWT/API key string.
func extractAccessToken(data []byte) string {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return ""
	}
	if trimmed[0] != '{' {
		return string(trimmed)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(trimmed, &tok); err != nil {
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
	useragent.Install(appVersion)

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
			writeLine("CONFIG atlas_url")
			writeLine("CONFIG grants")
			writeLine("CONFIG grants_account_id")
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
