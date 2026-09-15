// Package auth defines the Provider interface for authentication and credential
// acquisition. Exactly one implementation is compiled, selected by build tag:
//
//	//go:build !internal  → provider_oidc.go (generic OIDC device-flow + STS; the public build)
//
// Internal builds may supply an alternate provider via provider_internal.go (//go:build internal).
package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

// Tokens holds the token set returned after a successful login or refresh.
type Tokens struct {
	IDToken      string
	AccessToken  string
	RefreshToken string
	Expiry       time.Time
}

// AWSCredentials holds temporary AWS credentials.
type AWSCredentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
	Expiration      time.Time
}

// OIDCConfig holds OIDC provider settings for the generic build.
type OIDCConfig struct {
	// Issuer is the OIDC discovery URL (required for the generic build).
	Issuer string
	// ClientID is the OIDC client ID (required).
	ClientID string
	// Scopes is the space-separated list of OIDC scopes (default: "openid profile").
	Scopes []string
	// Audience is an optional audience parameter sent to the token endpoint.
	Audience string
}

// AWSConfig holds AWS STS settings for the generic build.
type AWSConfig struct {
	// RoleARN is the IAM role to assume via AssumeRoleWithWebIdentity (required).
	RoleARN string
	// SessionDuration is how long the STS session is valid (default: 3600s).
	SessionDuration int
	// Region is the AWS region for the STS call (default: us-east-1).
	Region string
}

// Config carries all settings needed by auth.New.
// Values are populated by LoadConfig (flag > env > config file > built-in).
type Config struct {
	OIDC OIDCConfig
	AWS  AWSConfig
}

// Provider abstracts the authentication and base-credential operations needed
// by the CLI. Exactly one implementation is compiled per build.
type Provider interface {
	// Login performs an interactive device-flow login and returns the resulting tokens.
	Login(ctx context.Context) (Tokens, error)
	// Refresh exchanges a refresh token for a new token set without user interaction.
	Refresh(ctx context.Context, refreshToken string) (Tokens, error)
	// FetchAWSCredentials exchanges an ID token for temporary AWS credentials.
	FetchAWSCredentials(ctx context.Context, idToken string) (AWSCredentials, error)
	// Name returns a human-readable identifier for diagnostics (e.g. "oidc").
	Name() string
}

// defaultScopes returns the scopes list, substituting the default when empty.
func defaultScopes(scopes []string) []string {
	if len(scopes) == 0 {
		return []string{"openid", "profile"}
	}
	return scopes
}

// splitScopes splits a space-separated scopes string.
func splitScopes(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Fields(s)
}

// UsernameFromToken extracts preferred_username (or sub as fallback) from
// the payload of a raw JWT without signature verification.
// Returns an empty string if the token cannot be decoded.
func UsernameFromToken(rawJWT string) string {
	parts := strings.SplitN(rawJWT, ".", 3)
	if len(parts) < 2 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		PreferredUsername string `json:"preferred_username"`
		Sub               string `json:"sub"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	if claims.PreferredUsername != "" {
		return claims.PreferredUsername
	}
	return claims.Sub
}
