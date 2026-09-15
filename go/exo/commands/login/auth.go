package login

import (
	"context"
	"fmt"
	"os"
	"time"

	"golang.org/x/oauth2"

	"github.com/Genentech/exohub/go/exo/internal/auth"
)

const pollTimeout = 2 * time.Minute

// DeviceFlowAuth performs interactive device flow authentication and writes AWS credentials.
func DeviceFlowAuth(_ bool) (*oauth2.Token, string, error) {
	cfg := auth.LoadConfig()

	// StartDeviceAuthFlow does OIDC discovery once and returns a Provider
	// that reuses the same discovered config — no second round-trip needed.
	flow, err := auth.StartDeviceAuthFlow(context.Background(), cfg)
	if err != nil {
		return nil, "", fmt.Errorf("failed to start device auth: %w", err)
	}

	fmt.Printf("\n🔐 Please visit %s and enter the code: %s\n\n", flow.Params.VerificationURI, flow.Params.UserCode)

	pollCtx, cancel := context.WithTimeout(context.Background(), pollTimeout)
	defer cancel()

	fmt.Println("⏳ Waiting for authentication...")
	tokens, err := flow.Poll(pollCtx)
	if err != nil {
		if pollCtx.Err() == context.DeadlineExceeded {
			return nil, "", fmt.Errorf("authentication timed out after %v", pollTimeout)
		}
		return nil, "", fmt.Errorf("authentication failed: %w", err)
	}

	fmt.Println("🔑 Fetching AWS credentials...")
	awsCreds, err := flow.Provider.FetchAWSCredentials(context.Background(), tokens.IDToken)
	if err != nil {
		return nil, "", fmt.Errorf("failed to fetch AWS credentials: %w", err)
	}

	credPath, profile, writeErr := auth.WriteAWSCredentialsWithInfo(awsCreds)
	if writeErr != nil {
		return nil, "", fmt.Errorf("failed to write AWS credentials: %w", writeErr)
	}
	fmt.Printf("✅ AWS credentials written to %s [profile: %s]\n", credPath, profile)

	oauthToken := &oauth2.Token{
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		Expiry:       tokens.Expiry,
	}
	return oauthToken, auth.UsernameFromToken(tokens.AccessToken), nil
}

// DeviceFlowAuthJSON performs device flow authentication, emitting JSON events to stdout.
func DeviceFlowAuthJSON() (*oauth2.Token, string, error) {
	cfg := auth.LoadConfig()

	flow, err := auth.StartDeviceAuthFlow(context.Background(), cfg)
	if err != nil {
		return nil, "", fmt.Errorf("failed to start device auth: %w", err)
	}

	// Emit auth URL as JSON — stdout is the JSON stream; progress goes to stderr.
	emitJSON(LoginResult{
		Status:   "waiting",
		AuthURL:  flow.Params.VerificationURI,
		UserCode: flow.Params.UserCode,
		Message:  "Please visit the URL and enter the code to authenticate",
	})

	pollCtx, cancel := context.WithTimeout(context.Background(), pollTimeout)
	defer cancel()

	tokens, err := flow.Poll(pollCtx)
	if err != nil {
		if pollCtx.Err() == context.DeadlineExceeded {
			return nil, "", fmt.Errorf("authentication timed out after %v", pollTimeout)
		}
		return nil, "", fmt.Errorf("authentication failed: %w", err)
	}

	fmt.Fprintln(os.Stderr, "Fetching AWS credentials...")
	awsCreds, err := flow.Provider.FetchAWSCredentials(context.Background(), tokens.IDToken)
	if err != nil {
		return nil, "", fmt.Errorf("failed to fetch AWS credentials: %w", err)
	}

	if err := auth.WriteAWSCredentials(awsCreds); err != nil {
		return nil, "", fmt.Errorf("failed to write AWS credentials: %w", err)
	}
	// Confirmation goes to stderr so it doesn't corrupt the JSON stream.
	fmt.Fprintln(os.Stderr, "AWS credentials written.")

	oauthToken := &oauth2.Token{
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		Expiry:       tokens.Expiry,
	}
	return oauthToken, auth.UsernameFromToken(tokens.AccessToken), nil
}

// RefreshAuth uses the stored refresh token to obtain new tokens and AWS credentials.
func RefreshAuth(refreshToken string) (*oauth2.Token, string, error) {
	cfg := auth.LoadConfig()
	provider, err := auth.New(context.Background(), cfg)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create auth provider: %w", err)
	}

	tokens, err := provider.Refresh(context.Background(), refreshToken)
	if err != nil {
		return nil, "", err
	}

	awsCreds, err := provider.FetchAWSCredentials(context.Background(), tokens.IDToken)
	if err != nil {
		return nil, "", fmt.Errorf("failed to fetch AWS credentials after refresh: %w", err)
	}

	if err := auth.WriteAWSCredentials(awsCreds); err != nil {
		return nil, "", fmt.Errorf("failed to write AWS credentials: %w", err)
	}

	oauthToken := &oauth2.Token{
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		Expiry:       tokens.Expiry,
	}

	return oauthToken, auth.UsernameFromToken(tokens.AccessToken), nil
}

// RefreshTokenOnly refreshes the JWT without fetching AWS credentials.
// Used in serve mode where AWS creds are not needed and stdout goes through the PTY.
func RefreshTokenOnly(refreshToken string) (*oauth2.Token, string, error) {
	cfg := auth.LoadConfig()
	provider, err := auth.New(context.Background(), cfg)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create auth provider: %w", err)
	}

	tokens, err := provider.Refresh(context.Background(), refreshToken)
	if err != nil {
		return nil, "", err
	}

	oauthToken := &oauth2.Token{
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		Expiry:       tokens.Expiry,
	}

	return oauthToken, auth.UsernameFromToken(tokens.AccessToken), nil
}

// ExohubAWSProfile returns the AWS profile name used by exo.
func ExohubAWSProfile() string {
	return auth.ExohubAWSProfile()
}
