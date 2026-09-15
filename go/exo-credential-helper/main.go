package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3control"
	"github.com/aws/aws-sdk-go-v2/service/s3control/types"
	"gopkg.in/yaml.v3"
)

var appVersion = "dev"

const (
	defaultDuration = 3600
)

// defaultRegion is empty in the OSS base; the internal overlay (defaults_internal.go)
// sets it to the production region. Override at runtime via
// EXOHUB_GRANTS_REGION or --region.
var defaultRegion = ""

// defaultAccountID is empty in the OSS base; the internal overlay (defaults_internal.go)
// sets it to the production account ID. Override at runtime via
// EXOHUB_GRANTS_ACCOUNT_ID or --account-id.
var defaultAccountID = ""

// builtinAPIBase is the built-in fallback for EXOHUB_API_URL.
// Overridden by defaults_internal.go (//go:build internal).
var builtinAPIBase = ""

type credentialOutput struct {
	Version         int    `json:"Version"`
	AccessKeyID     string `json:"AccessKeyId"`
	SecretAccessKey string `json:"SecretAccessKey"`
	SessionToken    string `json:"SessionToken"`
	Expiration      string `json:"Expiration"`
}

func main() {
	s3URL := flag.String("s3url", "", "S3 URL (s3://bucket/prefix/) - detected from .exohub/remotes if not provided")
	grantsFlag := flag.Bool("grants", true, "Use S3 Access Grants for credentials (default: true; set false for plain base-creds path)")
	permission := flag.String("permission", "READWRITE", "Permission level: READ or READWRITE")
	duration := flag.Int("duration", defaultDuration, "Credential duration in seconds (default: 3600 = 1 hour)")
	accountID := flag.String("account-id", "", "AWS account ID (overrides EXOHUB_GRANTS_ACCOUNT_ID env)")
	region := flag.String("region", "", "AWS region (overrides EXOHUB_GRANTS_REGION env)")
	export := flag.Bool("export", false, "Output as shell export commands instead of JSON")
	status := flag.Bool("status", false, "Show credential status (cached, expiry time)")
	verify := flag.Bool("verify", false, "Verify credentials work (exit 0 if OK, 1 if not)")
	noCache := flag.Bool("no-cache", false, "Skip cache, always fetch fresh credentials")
	debug := flag.Bool("debug", false, "Enable debug output")
	showVersion := flag.Bool("version", false, "Show version")
	showFull := flag.Bool("full", false, "Show version with feature tags (use with --version)")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: exo-credential-helper [options] [s3url]

Get temporary AWS credentials via S3 Access Grants.

This tool calls AWS GetDataAccess directly using your current AWS credentials
(from ~/.aws/credentials, environment variables, or IAM role).

Options:
  --s3url URL         S3 URL (s3://bucket/prefix/) - detected from .exohub/remotes if not provided
  --permission P      Permission level: READ or READWRITE (default: READWRITE)
  --duration SEC      Credential duration in seconds (default: 3600 = 1 hour)
  --account-id ID     AWS account ID (overrides EXOHUB_GRANTS_ACCOUNT_ID env)
  --region REGION     AWS region (overrides EXOHUB_GRANTS_REGION env)
  --export            Output as shell export commands instead of JSON
  --status            Show credential status (cached, expiry time)
  --verify            Verify credentials work (exit 0 if OK, 1 if not)
  --no-cache          Skip cache, always fetch fresh credentials
  --debug             Enable debug output
  --version           Show version
  --help              Show this help

Examples:
  # Get credentials for a specific S3 prefix
  exo-credential-helper --s3url s3://exohub-sandbox-uat/myproject/

  # Auto-detect from .exohub/remotes in current directory
  exo-credential-helper

  # Export for shell use
  eval $(exo-credential-helper --export)

  # Check if credentials are working
  exo-credential-helper --verify

Output format (default):
  AWS credential_process compatible JSON:
  {
    "Version": 1,
    "AccessKeyId": "ASIA...",
    "SecretAccessKey": "...",
    "SessionToken": "...",
    "Expiration": "2026-03-04T22:00:00Z"
  }
`)
	}
	flag.Parse()

	if *showVersion {
		if *showFull {
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

	// Accept s3url as positional argument
	if *s3URL == "" && flag.NArg() > 0 {
		*s3URL = flag.Arg(0)
	}

	resolvedAccountID := resolveEnvDefault(*accountID, "EXOHUB_GRANTS_ACCOUNT_ID", defaultAccountID)
	resolvedRegion := resolveEnvDefault(*region, "EXOHUB_GRANTS_REGION", defaultRegion)

	debugf := func(format string, args ...any) {}
	if *debug {
		debugf = func(format string, args ...any) {
			fmt.Fprintf(os.Stderr, "[DEBUG] "+format+"\n", args...)
		}
	}

	// Track whether --grants was set explicitly on the command line.
	grantsExplicit := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "grants" {
			grantsExplicit = true
		}
	})

	// Auto-detect s3url (and grants) from .exohub/remotes if not provided
	if *s3URL == "" {
		detected, err := detectRemote()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: No --s3url provided and could not detect from .exohub/remotes: %v\n", err)
			os.Exit(1)
		}
		*s3URL = detected.S3URL
		if !grantsExplicit {
			*grantsFlag = detected.Grants
		}
		debugf("Auto-detected s3url from .exohub/remotes: %s (grants=%v)", *s3URL, *grantsFlag)
	}

	if *permission != "READ" && *permission != "READWRITE" {
		fmt.Fprintf(os.Stderr, "Error: --permission must be READ or READWRITE, got %q\n", *permission)
		os.Exit(1)
	}

	debugf("exo-credential-helper starting")
	debugf("  s3url: %s", *s3URL)
	debugf("  permission: %s", *permission)
	debugf("  duration: %d", *duration)
	debugf("  account_id: %s", resolvedAccountID)
	debugf("  region: %s", resolvedRegion)

	// Handle --status
	if *status {
		handleStatus(*s3URL, *permission, *grantsFlag)
		return
	}

	// Acquire a per-cache-key file lock so concurrent subprocess invocations
	// (e.g. git-annex spawning one process per file) share a single API fetch.
	// --no-cache skips locking entirely.
	var lockFile *os.File
	if !*noCache {
		var lockErr error
		lockFile, lockErr = acquireCacheLock(*s3URL, *permission, *grantsFlag)
		if lockErr != nil {
			debugf("Warning: could not acquire cache lock: %v", lockErr)
		}
		if lockFile != nil {
			defer func() {
				_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
				lockFile.Close()
			}()
		}
	}

	// Check cache (or re-check after acquiring lock — a sibling may have
	// fetched and written credentials while we were waiting).
	if !*noCache {
		cached, err := loadCachedCredentials(*s3URL, *permission, *grantsFlag)
		if err == nil && cached != nil {
			debugf("Using cached credentials (expires %s)", cached.Expiration)
			if *verify {
				fmt.Println("OK (cached)")
				return
			}
			outputCredentials(cached, *export)
			return
		}
	}

	var creds *credentialOutput
	if !*grantsFlag {
		// grants:false path — return base AWS credentials directly.
		// No GetDataAccess, no POST /api/grants/credentials.
		debugf("grants=false: using base credentials path")
		var err error
		creds, err = getBaseCreds(context.Background(), *s3URL, *duration, debugf)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: Failed to get base credentials: %v\n", err)
			os.Exit(1)
		}
	} else {
		// grants:true path — existing GetDataAccess → server fallback.
		var err error
		creds, err = getDataAccess(context.Background(), resolvedAccountID, resolvedRegion, *s3URL, *permission, *duration, debugf)
		if err != nil {
			if isExpiredTokenError(err) {
				// AWS credentials expired — refresh via exo login and retry.
				debugf("AWS credentials expired, attempting refresh via exo login...")
				if refreshErr := refreshViaExoLogin(debugf); refreshErr != nil {
					debugf("Refresh failed: %v", refreshErr)
					fmt.Fprintf(os.Stderr, "Error: Failed to get credentials: %v\n", err)
					fmt.Fprintf(os.Stderr, "Hint: run 'exo login' to refresh your credentials\n")
					os.Exit(1)
				}
				creds, err = getDataAccess(context.Background(), resolvedAccountID, resolvedRegion, *s3URL, *permission, *duration, debugf)
				if err != nil {
					// After token refresh, fall through to server path for AccessDenied or missing creds.
					if !isAccessDeniedError(err) && !isMissingCredentialsError(err) {
						fmt.Fprintf(os.Stderr, "Error: Failed to get credentials after refresh: %v\n", err)
						os.Exit(1)
					}
				}
			}

			// If direct GetDataAccess returned AccessDenied or could not load local AWS
			// credentials (e.g. no 'exohub' profile on a pure Model 2 / EDS worker),
			// fall back to the exohub API which provisions credentials via the Bearer token.
			if err != nil && (isAccessDeniedError(err) || isMissingCredentialsError(err)) {
				debugf("GetDataAccess unavailable (%v), falling back to POST /api/grants/credentials", err)
				creds, err = getCredsFromServer(*s3URL, *permission, debugf)
				if err != nil {
					printCredsFromServerError(err, *s3URL)
					os.Exit(1)
				}
			} else if err != nil {
				fmt.Fprintf(os.Stderr, "Error: Failed to get credentials: %v\n", err)
				os.Exit(1)
			}
		}
	}

	// Cache the credentials
	if err := cacheCredentials(*s3URL, *permission, *grantsFlag, creds); err != nil {
		debugf("Warning: failed to cache credentials: %v", err)
	}

	if *verify {
		fmt.Println("OK")
		return
	}

	outputCredentials(creds, *export)
}

func resolveEnvDefault(flagVal, envVar, defaultVal string) string {
	if flagVal != "" {
		return flagVal
	}
	if v := os.Getenv(envVar); v != "" {
		return v
	}
	return defaultVal
}

func getDataAccess(ctx context.Context, accountID, region, s3URL, permission string, duration int, debugf func(string, ...any)) (*credentialOutput, error) {
	// Use the exohub AWS profile (written by exo login), not $AWS_PROFILE
	profile := os.Getenv("EXOHUB_AWS_PROFILE")
	if profile == "" {
		profile = "exohub"
	}
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region), config.WithSharedConfigProfile(profile))
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	client := s3control.NewFromConfig(cfg)

	target := s3URL
	if !strings.HasSuffix(target, "/*") {
		target = strings.TrimSuffix(target, "/") + "/*"
	}

	debugf("Calling GetDataAccess:")
	debugf("  AccountId: %s", accountID)
	debugf("  Target: %s", target)
	debugf("  Permission: %s", permission)
	debugf("  DurationSeconds: %d", duration)

	dur32 := int32(duration)
	perm := types.Permission(permission)
	resp, err := client.GetDataAccess(ctx, &s3control.GetDataAccessInput{
		AccountId:       aws.String(accountID),
		Target:          aws.String(target),
		Permission:      perm,
		DurationSeconds: &dur32,
	})
	if err != nil {
		return nil, fmt.Errorf("GetDataAccess failed: %w", err)
	}

	if resp.Credentials == nil {
		return nil, fmt.Errorf("GetDataAccess returned no credentials")
	}

	expiration := ""
	if resp.Credentials.Expiration != nil {
		expiration = resp.Credentials.Expiration.UTC().Format(time.RFC3339)
	}

	return &credentialOutput{
		Version:         1,
		AccessKeyID:     aws.ToString(resp.Credentials.AccessKeyId),
		SecretAccessKey: aws.ToString(resp.Credentials.SecretAccessKey),
		SessionToken:    aws.ToString(resp.Credentials.SessionToken),
		Expiration:      expiration,
	}, nil
}

// isAccessDeniedError returns true when the AWS error indicates a 403 AccessDenied,
// meaning the caller has no pre-provisioned S3 Access Grant for the prefix.
func isAccessDeniedError(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "AccessDenied") ||
		strings.Contains(msg, "StatusCode: 403") ||
		strings.Contains(msg, "access denied")
}

// isMissingCredentialsError returns true when the error is caused by the absence
// of a local AWS profile or credentials (e.g. pure Model 2 / EDS worker with no
// 'exohub' profile written by 'exo login'). These errors should trigger the same
// server-path fallback as AccessDenied.
func isMissingCredentialsError(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "failed to get shared config profile") ||
		strings.Contains(msg, "SharedConfigProfileNotExistError") ||
		strings.Contains(msg, "NoCredentialProviders")
}

// loadBearerToken reads the access token stored by 'exo login'.
// Mirrors go/exo/commands/login/storage.go GetTokenFile + LoadToken logic.
func loadBearerToken() (string, error) {
	// Allow inline token via environment (used in tests and service accounts).
	if inline := os.Getenv("EXO_TOKEN"); inline != "" {
		var tok struct {
			AccessToken string `json:"access_token"`
		}
		if err := json.Unmarshal([]byte(inline), &tok); err != nil {
			return "", fmt.Errorf("failed to parse EXO_TOKEN: %w", err)
		}
		return tok.AccessToken, nil
	}

	// EXO_TOKEN_FILE takes next precedence.
	tokenFile := os.Getenv("EXO_TOKEN_FILE")
	if tokenFile == "" {
		// EXO_CONFIG_DIR (set by exo --config-dir or environment) roots the token path.
		if configRoot := os.Getenv("EXO_CONFIG_DIR"); configRoot != "" {
			tokenFile = filepath.Join(configRoot, "exo", "credentials", "token.json")
		} else {
			configDir, err := os.UserConfigDir()
			if err != nil {
				return "", fmt.Errorf("failed to get config dir: %w", err)
			}
			tokenFile = filepath.Join(configDir, "exo", "credentials", "token.json")
		}
	}

	data, err := os.ReadFile(tokenFile)
	if err != nil {
		return "", fmt.Errorf("failed to read token file: %w", err)
	}

	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(data, &tok); err != nil {
		return "", fmt.Errorf("failed to parse token file: %w", err)
	}
	return tok.AccessToken, nil
}

// usernameFromToken extracts preferred_username from the stored JWT access token.
func usernameFromToken() string {
	token, err := loadBearerToken()
	if err != nil || token == "" {
		return os.Getenv("USER")
	}

	parts := strings.SplitN(token, ".", 3)
	if len(parts) < 2 {
		return os.Getenv("USER")
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return os.Getenv("USER")
	}

	var claims struct {
		PreferredUsername string `json:"preferred_username"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return os.Getenv("USER")
	}
	if claims.PreferredUsername != "" {
		return claims.PreferredUsername
	}
	return os.Getenv("USER")
}

func outputCredentials(creds *credentialOutput, exportMode bool) {
	if exportMode {
		fmt.Printf("export AWS_ACCESS_KEY_ID=%s\n", creds.AccessKeyID)
		fmt.Printf("export AWS_SECRET_ACCESS_KEY=%s\n", creds.SecretAccessKey)
		fmt.Printf("export AWS_SESSION_TOKEN=%s\n", creds.SessionToken)
		return
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(creds)
}

// Cache

func cacheKey(s3URL, permission string, grants bool) string {
	grantsStr := "1"
	if !grants {
		grantsStr = "0"
	}
	h := sha256.Sum256([]byte(s3URL + "|" + permission + "|" + grantsStr))
	return fmt.Sprintf("%x", h[:8])
}

func cacheDir() (string, error) {
	if configRoot := os.Getenv("EXO_CONFIG_DIR"); configRoot != "" {
		p := filepath.Join(configRoot, "cache", "exo-credential-helper")
		return p, os.MkdirAll(p, 0700)
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	p := filepath.Join(dir, "exo-credential-helper")
	return p, os.MkdirAll(p, 0700)
}

func cachePath(s3URL, permission string, grants bool) (string, error) {
	dir, err := cacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, cacheKey(s3URL, permission, grants)+".json"), nil
}

// acquireCacheLock opens (or creates) a sidecar .lock file for the given cache
// key and acquires an exclusive POSIX file lock (flock LOCK_EX). Concurrent
// processes block until the lock is released. The caller must close the returned
// file (and optionally call LOCK_UN) when done; using defer is recommended.
func acquireCacheLock(s3URL, permission string, grants bool) (*os.File, error) {
	dir, err := cacheDir()
	if err != nil {
		return nil, err
	}
	lockPath := filepath.Join(dir, cacheKey(s3URL, permission, grants)+".lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func cacheCredentials(s3URL, permission string, grants bool, creds *credentialOutput) error {
	path, err := cachePath(s3URL, permission, grants)
	if err != nil {
		return err
	}
	data, err := json.Marshal(creds)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func loadCachedCredentials(s3URL, permission string, grants bool) (*credentialOutput, error) {
	path, err := cachePath(s3URL, permission, grants)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var creds credentialOutput
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, err
	}
	// Check expiration with 5-minute buffer
	if creds.Expiration != "" {
		exp, err := time.Parse(time.RFC3339, creds.Expiration)
		if err == nil && time.Now().Add(5*time.Minute).After(exp) {
			os.Remove(path)
			return nil, fmt.Errorf("cached credentials expired")
		}
	}
	return &creds, nil
}

func handleStatus(s3URL, permission string, grants bool) {
	cached, err := loadCachedCredentials(s3URL, permission, grants)
	if err != nil || cached == nil {
		fmt.Println("No cached credentials")
		return
	}
	exp, err := time.Parse(time.RFC3339, cached.Expiration)
	if err != nil {
		fmt.Println("Cached (unknown expiry)")
		return
	}
	remaining := time.Until(exp).Round(time.Second)
	fmt.Printf("Cached, expires in %s (%s)\n", remaining, exp.Format(time.RFC3339))
}

// Auto-detection from .exohub/remotes

type remotesFile struct {
	Remotes []remoteEntry `yaml:"remotes"`
}

type remoteEntry struct {
	S3URL  string `yaml:"s3url"`
	Grants *bool  `yaml:"grants"`
}

// detectedRemote holds the result of auto-detecting remote config from .exohub/remotes.
type detectedRemote struct {
	S3URL  string
	Grants bool // defaults to true when not specified
}

// isExpiredTokenError checks if an error is due to expired AWS credentials.
func isExpiredTokenError(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "expiredtoken") ||
		strings.Contains(msg, "the provided token has expired") ||
		strings.Contains(msg, "expired")
}

// refreshViaExoLogin attempts to refresh credentials by running exo login --json.
func refreshViaExoLogin(debugf func(string, ...any)) error {
	exoBin := "exo"
	if v := os.Getenv("EXO_BIN"); v != "" {
		exoBin = v
	}

	cmd := exec.Command(exoBin, "login", "--json")
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("exo login failed: %s", strings.TrimSpace(errBuf.String()))
	}

	debugf("exo login refresh succeeded: %s", strings.TrimSpace(outBuf.String()))
	return nil
}

func detectRemote() (detectedRemote, error) {
	data, err := os.ReadFile(".exohub/remotes")
	if err != nil {
		return detectedRemote{}, fmt.Errorf("could not read .exohub/remotes: %w", err)
	}

	var remotes remotesFile
	if err := yaml.Unmarshal(data, &remotes); err != nil {
		return detectedRemote{}, fmt.Errorf("could not parse .exohub/remotes: %w", err)
	}

	for _, r := range remotes.Remotes {
		if r.S3URL != "" {
			grants := true
			if r.Grants != nil {
				grants = *r.Grants
			}
			return detectedRemote{S3URL: r.S3URL, Grants: grants}, nil
		}
	}

	return detectedRemote{}, fmt.Errorf("no s3url found in .exohub/remotes")
}
