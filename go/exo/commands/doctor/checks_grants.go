package doctor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path"      // path (not filepath) for S3 key construction — avoids backslash on Windows
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"gopkg.in/yaml.v3"

	"github.com/Genentech/exohub/go/exo/commands/login"
	"github.com/Genentech/exohub/go/exo/configdir"
	"github.com/Genentech/exohub/go/exo/internal/credhelper"
)

const groupGrants = "Grants & S3 Access"

func runGrantsChecks(repoDir, remoteFilter string, writeTest bool) []CheckResult {
	remotesPath := filepath.Join(repoDir, ".exohub", "remotes")
	data, err := os.ReadFile(remotesPath)
	if err != nil {
		return []CheckResult{skip("grants.remotes", groupGrants, "Grants checks skipped (no .exohub/remotes)")}
	}

	var rf remotesFile
	if err := yaml.Unmarshal(data, &rf); err != nil {
		return []CheckResult{skip("grants.remotes", groupGrants, "Grants checks skipped (cannot parse .exohub/remotes)")}
	}

	var grantedRemotes []remoteEntry
	for _, r := range rf.Remotes {
		if r.Grants && r.S3URL != "" {
			if remoteFilter == "" || r.Name == remoteFilter {
				grantedRemotes = append(grantedRemotes, r)
			}
		}
	}

	if len(grantedRemotes) == 0 {
		return []CheckResult{skip("grants.remotes", groupGrants, "No grants-enabled remotes found")}
	}

	// Build the token loader once for all remotes.
	tokenLoader := buildTokenLoader()

	var results []CheckResult
	for _, remote := range grantedRemotes {
		// Pass repoDir so write-test uses the correct permissions file regardless of CWD.
		results = append(results, checkRemoteGrants(repoDir, remote, tokenLoader, writeTest)...)
	}
	return results
}

func checkRemoteGrants(repoDir string, remote remoteEntry, tokenLoader func() (string, error), writeTest bool) []CheckResult {
	prefix := fmt.Sprintf("grants.%s", remote.Name)
	s3url := remote.S3URL
	ctx := context.Background()

	var results []CheckResult

	// 1. Flag root-vs-_annex non-hierarchical matching
	results = append(results, checkGrantScope(prefix, remote))

	// 2. GetDataAccess READ
	results = append(results, checkGetDataAccess(ctx, prefix, s3url, "READ"))

	// 3. GetDataAccess READWRITE
	results = append(results, checkGetDataAccess(ctx, prefix, s3url, "READWRITE"))

	// 4. Server fallback (POST /api/grants/credentials) + vended principal
	results = append(results, checkServerFallback(ctx, prefix, s3url, tokenLoader))

	// 5. Write canary (opt-in)
	if writeTest {
		results = append(results, checkWriteTest(ctx, prefix, repoDir, s3url))
	}

	return results
}

func checkGrantScope(prefix string, remote remoteEntry) CheckResult {
	id := prefix + ".scope"
	s3url := remote.S3URL
	return pass(id, groupGrants, fmt.Sprintf("Remote %q s3url scope: %s", remote.Name, s3url))
}

func checkGetDataAccess(ctx context.Context, prefix, s3url, permission string) CheckResult {
	id := fmt.Sprintf("%s.get-data-access-%s", prefix, strings.ToLower(permission))

	result, err := credhelper.GetDataAccess(ctx, "", "", s3url, permission, 900, nil)
	if err != nil {
		if credhelper.IsMissingCredentialsError(err) {
			return warn(id, groupGrants,
				fmt.Sprintf("GetDataAccess(%s): no local AWS profile (exohub)", permission),
				Redact(err.Error()),
				"Run 'exo login' to provision the exohub AWS profile")
		}
		if credhelper.IsAccessDeniedError(err) {
			return fail(id, groupGrants,
				fmt.Sprintf("GetDataAccess(%s): AccessDenied — no matching grant", permission),
				Redact(err.Error()),
				"Run 'exo init' to provision grants, or ask a dataset owner to grant access")
		}
		return warn(id, groupGrants,
			fmt.Sprintf("GetDataAccess(%s): %v", permission, err),
			Redact(err.Error()),
			"Check VPN/network or run 'exo login'")
	}

	expiry := ""
	if result.Credentials.Expiration != "" {
		exp, err := time.Parse(time.RFC3339, result.Credentials.Expiration)
		if err == nil {
			expiry = fmt.Sprintf(" (expires in %s)", time.Until(exp).Round(time.Second))
		}
	}

	// For READWRITE, verify the vended credentials actually allow writes.
	// Read-only credentials are vended successfully by GetDataAccess READWRITE
	// when the grant is scoped to READ — the call succeeds but PutObject is denied.
	if strings.EqualFold(permission, "READWRITE") {
		if probeErr := probeS3Write(ctx, result.Credentials, s3url); probeErr != nil {
			return fail(id, groupGrants,
				fmt.Sprintf("GetDataAccess(%s): credentials vended but write probe denied — read-only creds", permission),
				Redact(probeErr.Error()),
				"Run 'exo init' to provision READWRITE grants, or ask a dataset owner to grant write access")
		}
	}

	return pass(id, groupGrants,
		fmt.Sprintf("GetDataAccess(%s) OK — role=%s%s", permission, result.GranteeType, expiry))
}

func checkServerFallback(ctx context.Context, prefix, s3url string, tokenLoader func() (string, error)) CheckResult {
	id := prefix + ".server-fallback"

	// Try READWRITE first, then READ, to determine the vended principal.
	//
	// Note: we infer the vended principal from whether READWRITE succeeds, not
	// from the actual credential ARN. A future improvement would call
	// sts:GetCallerIdentity with the vended credentials and report the real ARN
	// (e.g. "arn:aws:sts::123:assumed-role/ExoHubLocationRole/..." vs
	// "arn:aws:sts::123:assumed-role/<identity-role>/lelongs").
	creds, err := credhelper.GetCredsFromServer(ctx, s3url, "READWRITE", tokenLoader, nil)
	if err == nil {
		// Verify the vended READWRITE credentials actually allow writes.
		if probeErr := probeS3Write(ctx, creds, s3url); probeErr != nil {
			return fail(id, groupGrants,
				"Server fallback READWRITE: credentials vended but write probe denied — read-only creds",
				Redact(probeErr.Error()),
				"Ask a dataset owner to grant you write access")
		}
		return pass(id, groupGrants,
			"Server fallback READWRITE OK — vended principal: location role (write-capable)")
	}

	if errors.Is(err, credhelper.ErrAuthRequired) {
		return fail(id, groupGrants,
			"Server fallback: not authenticated (401)",
			Redact(err.Error()),
			"Run 'exo login' to authenticate")
	}
	if errors.Is(err, credhelper.ErrAccessDenied) {
		// Try READ
		creds, err2 := credhelper.GetCredsFromServer(ctx, s3url, "READ", tokenLoader, nil)
		if err2 == nil {
			_ = creds
			return warn(id, groupGrants,
				"Server fallback READ OK but READWRITE denied — read-only access",
				fmt.Sprintf("Vended principal: identity role (read-only). s3url=%s", s3url),
				"Ask a dataset owner to grant you write access")
		}
		return fail(id, groupGrants,
			"Server fallback: access denied for both READ and READWRITE",
			Redact(err.Error()),
			"Ask a dataset owner to add you to .exohub/permissions")
	}

	// Unreachable API (no VPN)
	return warn(id, groupGrants,
		"Server fallback API unreachable",
		Redact(err.Error()),
		"Check VPN connection. The server fallback requires VPN access.")
}

// probeS3Write performs a zero-byte PutObject + DeleteObject canary write to
// confirm that the provided credentials carry actual write permission.
// It returns nil on success, or an error (including AccessDenied) if the write
// is denied. The canary key is placed under the s3url prefix and cleaned up
// immediately; any failure to delete is silently ignored (the canary is zero bytes).
func probeS3Write(ctx context.Context, creds *credhelper.Credentials, s3url string) error {
	bucket, keyPrefix, err := parseS3URL(s3url)
	if err != nil {
		return fmt.Errorf("cannot parse s3url: %w", err)
	}

	caller := currentUsername()
	if caller == "" {
		caller = "unknown"
	}
	ts := time.Now().UTC().Format("20060102T150405Z")
	// path.Join (not filepath.Join) so the separator is always '/' even on Windows.
	canaryKey := path.Join(keyPrefix, ".exo-doctor-canary-"+caller+"-"+ts)
	canaryKey = strings.TrimPrefix(canaryKey, "/")

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(resolveEnv("EXOHUB_GRANTS_REGION", credhelper.DefaultRegion)),
		awsconfig.WithCredentialsProvider(aws.CredentialsProviderFunc(func(ctx context.Context) (aws.Credentials, error) {
			return aws.Credentials{
				AccessKeyID:     creds.AccessKeyID,
				SecretAccessKey: creds.SecretAccessKey,
				SessionToken:    creds.SessionToken,
			}, nil
		})),
	)
	if err != nil {
		return fmt.Errorf("cannot build AWS config: %w", err)
	}

	s3Client := s3.NewFromConfig(awsCfg)

	_, putErr := s3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(canaryKey),
		Body:   bytes.NewReader([]byte{}),
	})
	if putErr != nil {
		return fmt.Errorf("PutObject denied: %w", putErr)
	}

	// Best-effort cleanup — ignore delete errors.
	_, _ = s3Client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(canaryKey),
	})

	return nil
}

// checkWriteTest performs an opt-in zero-byte Put+Delete canary write under a
// caller-owned prefix. repoDir must be the repo root (from --repo or CWD at
// command invocation time) so the owner guard always reads the correct
// .exohub/permissions regardless of the process CWD.
func checkWriteTest(ctx context.Context, prefix, repoDir, s3url string) CheckResult {
	id := prefix + ".write-test"

	caller := currentUsername()
	if caller == "" {
		return fail(id, groupGrants, "Write test: cannot determine caller identity", "", "Run 'exo login'")
	}

	// Owner guard: read .exohub/permissions from repoDir (NOT os.Getwd()).
	// Fail explicitly when the file is absent rather than silently skipping the guard.
	permsPath := filepath.Join(repoDir, ".exohub", "permissions")
	permsData, err := os.ReadFile(permsPath)
	if err != nil {
		return fail(id, groupGrants,
			"Write test refused: cannot read .exohub/permissions",
			fmt.Sprintf("path=%s err=%v", permsPath, err),
			"Run 'exo init' to create the permissions file, or check --repo path")
	}
	var perms permissionsConfig
	if err := yaml.Unmarshal(permsData, &perms); err != nil {
		return fail(id, groupGrants,
			"Write test refused: cannot parse .exohub/permissions",
			err.Error(), "Fix YAML syntax in .exohub/permissions")
	}
	isOwner := false
	for _, o := range perms.Owners {
		if o == caller {
			isOwner = true
			break
		}
	}
	if !isOwner {
		return fail(id, groupGrants,
			"Write test refused: caller is not an owner",
			fmt.Sprintf("caller=%q is not in owners list %v", caller, perms.Owners),
			"Only dataset owners can run --write-test")
	}

	// Get READWRITE credentials via GetDataAccess
	creds, err := credhelper.GetDataAccess(ctx, "", "", s3url, "READWRITE", 900, nil)
	if err != nil {
		return fail(id, groupGrants,
			"Write test: cannot get READWRITE credentials",
			Redact(err.Error()),
			"GetDataAccess READWRITE failed — see grants checks above")
	}

	_, keyPrefix, err := parseS3URL(s3url)
	if err != nil {
		return fail(id, groupGrants, "Write test: cannot parse s3url", err.Error(), "")
	}

	if probeErr := probeS3Write(ctx, creds.Credentials, s3url); probeErr != nil {
		return fail(id, groupGrants,
			"Write test: PutObject failed",
			Redact(probeErr.Error()),
			"Write access is not functional — check grants and role vending")
	}

	return pass(id, groupGrants,
		fmt.Sprintf("Write test OK (put+delete canary under s3://%s/%s, no artifacts left)", func() string {
			bucket, _, _ := parseS3URL(s3url)
			return bucket
		}(), keyPrefix))
}

// parseS3URL parses s3://bucket/prefix/ into (bucket, prefix, error).
func parseS3URL(s3url string) (bucket, prefix string, err error) {
	s3url = strings.TrimPrefix(s3url, "s3://")
	parts := strings.SplitN(s3url, "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		return "", "", fmt.Errorf("invalid s3url: %s", s3url)
	}
	bucket = parts[0]
	if len(parts) > 1 {
		prefix = strings.TrimSuffix(parts[1], "/")
	}
	return bucket, prefix, nil
}

// buildTokenLoader returns a function that reads the bearer token from the login store.
func buildTokenLoader() func() (string, error) {
	return func() (string, error) {
		tokenFile, err := configdir.TokenFile()
		if err != nil {
			return "", err
		}
		token, err := login.LoadToken(tokenFile)
		if err != nil || token == nil {
			return "", err
		}
		return token.AccessToken, nil
	}
}

// resolveEnv returns the env var value or a default.
func resolveEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
