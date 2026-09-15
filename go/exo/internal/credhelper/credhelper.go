// Package credhelper provides shared credential-fetching logic used by both
// exo-credential-helper and exo doctor. It covers the AWS GetDataAccess fast
// path (baseline, always available) and the server-side POST /api/grants/credentials
// fallback (available only when built with the exohubapi build tag).
package credhelper

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3control"
	"github.com/aws/aws-sdk-go-v2/service/s3control/types"
)

const (
	DefaultDuration = 3600
)

// DefaultRegion is empty in the OSS base; the internal overlay (credhelper_internal.go)
// sets it to the internal production region. Override at runtime via
// EXOHUB_GRANTS_REGION.
var DefaultRegion = ""

// DefaultAccountID is empty in the OSS base; the internal overlay (credhelper_internal.go)
// sets it to the internal production account ID. Override at runtime via
// EXOHUB_GRANTS_ACCOUNT_ID.
var DefaultAccountID = ""

// Credentials holds temporary AWS credential output (credential_process format).
type Credentials struct {
	Version         int    `json:"Version"`
	AccessKeyID     string `json:"AccessKeyId"`
	SecretAccessKey string `json:"SecretAccessKey"`
	SessionToken    string `json:"SessionToken"`
	Expiration      string `json:"Expiration"`
}

// ErrAuthRequired is returned when the API responds 401.
var ErrAuthRequired = fmt.Errorf("authentication required: run 'exo login' to authenticate")

// ErrAccessDenied is returned when the API responds 403.
var ErrAccessDenied = fmt.Errorf("access denied")

// IsAccessDeniedError returns true when the AWS error indicates 403 AccessDenied.
func IsAccessDeniedError(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "AccessDenied") ||
		strings.Contains(msg, "StatusCode: 403") ||
		strings.Contains(msg, "access denied")
}

// IsMissingCredentialsError returns true when no local AWS profile/credentials exist.
func IsMissingCredentialsError(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "failed to get shared config profile") ||
		strings.Contains(msg, "SharedConfigProfileNotExistError") ||
		strings.Contains(msg, "NoCredentialProviders")
}

// GetDataAccessResult extends Credentials with grant metadata.
type GetDataAccessResult struct {
	Credentials *Credentials
	// GranteeType is a local diagnostic label: "location" (READWRITE) or "oidc" (READ).
	// It is NOT sent to any server — it is derived locally after the AWS GetDataAccess
	// call returns and is used only for display in 'exo doctor' output.
	GranteeType string
}

// GetDataAccess calls AWS S3 Control GetDataAccess using the exohub AWS profile.
// Pass empty strings / 0 for accountID, region, permission, duration to use defaults.
func GetDataAccess(ctx context.Context, accountID, region, s3URL, permission string, duration int, debugf func(string, ...any)) (*GetDataAccessResult, error) {
	if accountID == "" {
		accountID = resolveEnv("EXOHUB_GRANTS_ACCOUNT_ID", DefaultAccountID)
	}
	if region == "" {
		region = resolveEnv("EXOHUB_GRANTS_REGION", DefaultRegion)
	}
	if permission == "" {
		permission = "READWRITE"
	}
	if duration == 0 {
		duration = DefaultDuration
	}
	if debugf == nil {
		debugf = func(string, ...any) {}
	}

	profile := resolveEnv("EXOHUB_AWS_PROFILE", "exohub")
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
		config.WithSharedConfigProfile(profile),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	client := s3control.NewFromConfig(cfg)

	target := s3URL
	if !strings.HasSuffix(target, "/*") {
		target = strings.TrimSuffix(target, "/") + "/*"
	}

	debugf("GetDataAccess: AccountId=%s Target=%s Permission=%s Duration=%d", accountID, target, permission, duration)

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

	creds := &Credentials{
		Version:         1,
		AccessKeyID:     aws.ToString(resp.Credentials.AccessKeyId),
		SecretAccessKey: aws.ToString(resp.Credentials.SecretAccessKey),
		SessionToken:    aws.ToString(resp.Credentials.SessionToken),
		Expiration:      expiration,
	}

	granteeType := "oidc"
	if strings.EqualFold(permission, "READWRITE") {
		granteeType = "location"
	}

	return &GetDataAccessResult{
		Credentials: creds,
		GranteeType: granteeType,
	}, nil
}

func resolveEnv(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}
