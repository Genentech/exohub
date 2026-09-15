//go:build !internal

package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	gooidc "github.com/coreos/go-oidc/v3/oidc"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"golang.org/x/oauth2"
)

// oidcProvider implements Provider using the OIDC device flow + STS.
type oidcProvider struct {
	cfg     Config
	oauth2  *oauth2.Config
	pollDur time.Duration
}

// New returns a Provider backed by the generic OIDC + STS implementation.
// ctx is used for the OIDC discovery request. Use context.Background() when
// no deadline is needed.
// Requires EXO_OIDC_ISSUER, EXO_OIDC_CLIENT_ID, and EXO_AWS_ROLE_ARN to be set.
func New(ctx context.Context, cfg Config) (Provider, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	// Discover OIDC endpoints.
	discovered, err := gooidc.NewProvider(ctx, cfg.OIDC.Issuer)
	if err != nil {
		return nil, fmt.Errorf("OIDC discovery failed for issuer %q: %w", cfg.OIDC.Issuer, err)
	}

	o2cfg := &oauth2.Config{
		ClientID: cfg.OIDC.ClientID,
		Scopes:   defaultScopes(cfg.OIDC.Scopes),
		Endpoint: discovered.Endpoint(),
	}

	return &oidcProvider{
		cfg:     cfg,
		oauth2:  o2cfg,
		pollDur: 2 * time.Minute,
	}, nil
}

func (p *oidcProvider) Name() string { return "oidc" }

func (p *oidcProvider) Login(ctx context.Context) (Tokens, error) {
	opts := []oauth2.AuthCodeOption{}
	if p.cfg.OIDC.Audience != "" {
		opts = append(opts, oauth2.SetAuthURLParam("audience", p.cfg.OIDC.Audience))
	}

	da, err := p.oauth2.DeviceAuth(ctx, opts...)
	if err != nil {
		return Tokens{}, fmt.Errorf("failed to initiate device flow: %w", err)
	}

	fmt.Printf("\n🔐 Please visit the following URL to log in and verify code: %s\n\n", da.UserCode)
	if da.VerificationURIComplete != "" {
		fmt.Printf("    %s\n\n", da.VerificationURIComplete)
	} else {
		fmt.Printf("    %s\n\n", da.VerificationURI)
	}

	pollCtx, cancel := context.WithTimeout(ctx, p.pollDur)
	defer cancel()

	fmt.Println("⏳ Waiting for authentication...")
	tok, err := p.oauth2.DeviceAccessToken(pollCtx, da, opts...)
	if err != nil {
		if pollCtx.Err() == context.DeadlineExceeded {
			return Tokens{}, fmt.Errorf("authentication timed out after %v", p.pollDur)
		}
		return Tokens{}, fmt.Errorf("authentication failed: %w", err)
	}

	return tokensFromOAuth2(tok)
}

func (p *oidcProvider) Refresh(ctx context.Context, refreshToken string) (Tokens, error) {
	// Build a token source that refreshes using the stored refresh token.
	old := &oauth2.Token{RefreshToken: refreshToken}
	ts := p.oauth2.TokenSource(ctx, old)
	tok, err := ts.Token()
	if err != nil {
		return Tokens{}, fmt.Errorf("token refresh failed: %w", err)
	}

	// Preserve the original refresh token if the server didn't return a new one.
	if tok.RefreshToken == "" {
		tok.RefreshToken = refreshToken
	}

	return tokensFromOAuth2(tok)
}

func (p *oidcProvider) FetchAWSCredentials(ctx context.Context, idToken string) (AWSCredentials, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(p.cfg.AWS.Region),
	)
	if err != nil {
		return AWSCredentials{}, fmt.Errorf("failed to load AWS config: %w", err)
	}

	stsClient := sts.NewFromConfig(cfg)
	dur := int32(p.cfg.AWS.SessionDuration)
	resp, err := stsClient.AssumeRoleWithWebIdentity(ctx, &sts.AssumeRoleWithWebIdentityInput{
		RoleArn:          aws.String(p.cfg.AWS.RoleARN),
		RoleSessionName:  aws.String("exo-cli"),
		WebIdentityToken: aws.String(idToken),
		DurationSeconds:  &dur,
	})
	if err != nil {
		return AWSCredentials{}, fmt.Errorf("AssumeRoleWithWebIdentity failed: %w", err)
	}
	if resp.Credentials == nil {
		return AWSCredentials{}, fmt.Errorf("STS returned no credentials")
	}

	expiration := time.Time{}
	if resp.Credentials.Expiration != nil {
		expiration = *resp.Credentials.Expiration
	}
	return AWSCredentials{
		AccessKeyID:     aws.ToString(resp.Credentials.AccessKeyId),
		SecretAccessKey: aws.ToString(resp.Credentials.SecretAccessKey),
		SessionToken:    aws.ToString(resp.Credentials.SessionToken),
		Expiration:      expiration,
	}, nil
}

// tokensFromOAuth2 extracts our Tokens from an oauth2.Token.
// The ID token is stored in the "id_token" extra field by the oauth2 library.
func tokensFromOAuth2(tok *oauth2.Token) (Tokens, error) {
	rawID, _ := tok.Extra("id_token").(string)
	if rawID == "" {
		// Fall back to access token (some IdPs use access token as identity token).
		rawID = tok.AccessToken
	}

	expiry := tok.Expiry
	if expiry.IsZero() && rawID != "" {
		// Try to extract expiry from the JWT payload.
		if exp, err := jwtExpiry(rawID); err == nil {
			expiry = exp
		}
	}

	return Tokens{
		IDToken:      rawID,
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		Expiry:       expiry,
	}, nil
}

// jwtExpiry decodes the exp claim from a raw JWT without signature verification.
func jwtExpiry(rawJWT string) (time.Time, error) {
	parts := strings.SplitN(rawJWT, ".", 3)
	if len(parts) < 2 {
		return time.Time{}, fmt.Errorf("invalid JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, err
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return time.Time{}, err
	}
	if claims.Exp == 0 {
		return time.Time{}, fmt.Errorf("no exp claim")
	}
	return time.Unix(claims.Exp, 0), nil
}
