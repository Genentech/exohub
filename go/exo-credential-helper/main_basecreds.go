//go:build !janus

package main

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// getBaseCreds returns base AWS credentials for a grants:false remote.
// Resolution order:
//  1. Try the exohub AWS profile — if it exists and is valid, reuse it directly.
//  2. Fall back to AssumeRoleWithWebIdentity using the stored OIDC id token and
//     EXO_AWS_ROLE_ARN / EXO_AWS_REGION. No Janus symbols in this build (!janus).
//     The id_token is refreshed via refresh_token first if expired or near-expiry.
func getBaseCreds(ctx context.Context, _ string, duration int, debugf func(string, ...any)) (*credentialOutput, error) {
	if debugf == nil {
		debugf = func(string, ...any) {}
	}

	region := resolveEnvDefault("", "EXO_AWS_REGION", "us-east-1")
	profile := resolveEnvDefault("", "EXOHUB_AWS_PROFILE", "exohub")

	// 1. Profile-first: try the exohub profile.
	debugf("base-creds: probing exohub AWS profile %q", profile)
	profileCreds, err := fetchProfileCreds(ctx, profile, region, duration, debugf)
	if err == nil {
		debugf("base-creds: using exohub profile credentials")
		return profileCreds, nil
	}
	debugf("base-creds: exohub profile unavailable (%v), falling back to OIDC+STS", err)

	// 2. OIDC id token → AssumeRoleWithWebIdentity.
	roleARN := resolveEnvDefault("", "EXO_AWS_ROLE_ARN", "")
	if roleARN == "" {
		return nil, fmt.Errorf("EXO_AWS_ROLE_ARN is not set; required for the grants:false base-credentials path")
	}

	// Refresh id_token via refresh_token if expired or near-expiry.
	idToken := refreshIDTokenIfNeeded(ctx, debugf)
	if idToken == "" {
		// No id_token — fall back to access_token from loadBearerToken.
		var bearerErr error
		idToken, bearerErr = loadBearerToken()
		if bearerErr != nil || idToken == "" {
			return nil, fmt.Errorf("no OIDC id token available (run 'exo login'): %w", bearerErr)
		}
	}

	debugf("base-creds: AssumeRoleWithWebIdentity (role=%s region=%s)", roleARN, region)
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config for STS: %w", err)
	}

	stsClient := sts.NewFromConfig(cfg)
	dur := int32(duration)
	resp, err := stsClient.AssumeRoleWithWebIdentity(ctx, &sts.AssumeRoleWithWebIdentityInput{
		RoleArn:          aws.String(roleARN),
		RoleSessionName:  aws.String("exo-credential-helper"),
		WebIdentityToken: aws.String(idToken),
		DurationSeconds:  &dur,
	})
	if err != nil {
		return nil, fmt.Errorf("AssumeRoleWithWebIdentity failed: %w", err)
	}
	if resp.Credentials == nil {
		return nil, fmt.Errorf("STS returned no credentials")
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
