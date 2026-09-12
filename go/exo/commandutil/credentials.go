package commandutil

import (
	"fmt"
	"os"
	"strings"

	"github.com/Genentech/exohub/go/exo/commands/login"
)

// IsCredentialError checks if an error message indicates AWS credential issues
func IsCredentialError(errMsg string) bool {
	if errMsg == "" {
		return false
	}

	errLower := strings.ToLower(errMsg)

	// Common AWS credential error patterns
	credErrorPatterns := []string{
		"expiredtoken",
		"the provided token has expired",
		"token has expired",
		"expired",
		"invalid credentials",
		"invalidaccesskeyid",
		"signaturedoesnotmatch",
		"the security token included in the request is invalid",
		"credentials have expired",
		"not authorized",
		"access denied",
		"status code: 400",
		"status code: 401",
		"status code: 403",
	}

	for _, pattern := range credErrorPatterns {
		if strings.Contains(errLower, pattern) {
			return true
		}
	}

	return false
}

// HandleCredentialError prompts user to re-login and retries the operation
// Returns true if login was successful and operation should be retried
func HandleCredentialError(errMsg string) (bool, error) {
	if !IsCredentialError(errMsg) {
		return false, nil
	}

	fmt.Fprintln(os.Stderr, "\n⚠️  AWS credential error detected:")
	fmt.Fprintf(os.Stderr, "   %s\n\n", strings.TrimSpace(errMsg))
	fmt.Fprintln(os.Stderr, "🔄 Attempting to refresh credentials...")

	tokenFile, err := login.GetTokenFile()
	if err != nil {
		return false, fmt.Errorf("failed to get token file path: %w", err)
	}

	// Try silent refresh first using stored refresh token
	existingToken, loadErr := login.LoadToken(tokenFile)
	if loadErr == nil && existingToken.RefreshToken != "" {
		refreshed, username, refreshErr := login.RefreshAuth(existingToken.RefreshToken)
		if refreshErr == nil {
			if err := login.SaveToken(tokenFile, refreshed); err != nil {
				return false, fmt.Errorf("failed to save refreshed credentials: %w", err)
			}
			fmt.Printf("\n✅ Successfully refreshed credentials for: %s\n", username)
			fmt.Println("🔄 Retrying operation with fresh credentials...")
			return true, nil
		}
		fmt.Fprintf(os.Stderr, "⚠️  Token refresh failed: %v\n", refreshErr)
		fmt.Fprintln(os.Stderr, "🔐 Falling back to interactive login...")
	}

	// Fall back to full device flow authentication
	token, username, err := login.DeviceFlowAuth(false)
	if err != nil {
		return false, fmt.Errorf("failed to refresh credentials: %w", err)
	}

	err = login.SaveToken(tokenFile, token)
	if err != nil {
		return false, fmt.Errorf("failed to save credentials: %w", err)
	}

	fmt.Printf("\n✅ Successfully authenticated as: %s\n", username)
	fmt.Println("🔄 Retrying operation with fresh credentials...")

	return true, nil
}
