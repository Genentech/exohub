//go:build !janus

package auth

import (
	"context"
	"fmt"
	"time"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// startDeviceAuthFlow performs OIDC discovery once, starts the device auth
// request, and returns a DeviceAuthFlow whose Provider reuses the same
// discovered oauth2 config — no second discovery round-trip.
func startDeviceAuthFlow(ctx context.Context, cfg Config) (DeviceAuthFlow, error) {
	if err := cfg.Validate(); err != nil {
		return DeviceAuthFlow{}, err
	}

	discovered, err := gooidc.NewProvider(ctx, cfg.OIDC.Issuer)
	if err != nil {
		return DeviceAuthFlow{}, fmt.Errorf("OIDC discovery failed for issuer %q: %w", cfg.OIDC.Issuer, err)
	}

	o2cfg := &oauth2.Config{
		ClientID: cfg.OIDC.ClientID,
		Scopes:   defaultScopes(cfg.OIDC.Scopes),
		Endpoint: discovered.Endpoint(),
	}

	opts := []oauth2.AuthCodeOption{}
	if cfg.OIDC.Audience != "" {
		opts = append(opts, oauth2.SetAuthURLParam("audience", cfg.OIDC.Audience))
	}

	da, err := o2cfg.DeviceAuth(ctx, opts...)
	if err != nil {
		return DeviceAuthFlow{}, fmt.Errorf("failed to initiate device auth: %w", err)
	}

	verURI := da.VerificationURIComplete
	if verURI == "" {
		verURI = da.VerificationURI
	}

	// Build the provider from the same already-discovered o2cfg so
	// FetchAWSCredentials doesn't need a second discovery round-trip.
	provider := &oidcProvider{
		cfg:     cfg,
		oauth2:  o2cfg,
		pollDur: 2 * time.Minute,
	}

	pollFn := func(pollCtx context.Context) (Tokens, error) {
		tok, err := o2cfg.DeviceAccessToken(pollCtx, da, opts...)
		if err != nil {
			return Tokens{}, err
		}
		return tokensFromOAuth2(tok)
	}

	return DeviceAuthFlow{
		Params:   DeviceAuthParams{VerificationURI: verURI, UserCode: da.UserCode},
		Poll:     pollFn,
		Provider: provider,
	}, nil
}
