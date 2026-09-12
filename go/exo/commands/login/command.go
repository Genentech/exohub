package login

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/Genentech/exohub/go/exo/internal/defaults"
	"github.com/Genentech/exohub/go/exo/safe"
)

var (
	force      bool
	showQRCode bool
	jsonOutput bool
)

// confidentialityDisclaimerURL returns the URL to the ExoHub confidentiality page.
func confidentialityDisclaimerURL() string {
	return defaults.ExohubBase() + "/confidentiality"
}

// printConfidentialityDisclaimer prints the confidentiality notice to stdout.
func printConfidentialityDisclaimer() {
	fmt.Println()
	fmt.Println("⚠️  Notice: This system contains Roche confidential data (C3). Access is restricted to authorized personnel.")
	fmt.Printf("   For more information: %s\n", confidentialityDisclaimerURL())
	fmt.Println()
}

// NewLoginCommand creates the login command
func NewLoginCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate via device flow",
		Long: `Authenticate via OIDC device flow.

This command initiates a device authentication flow that allows you to log in
using any device with a web browser. After authentication, credentials are
securely cached for future use.`,
		RunE: runLogin,
	}

	cmd.Flags().BoolVar(&force, "force", false, "Force re-authentication even if valid credentials exist")
	cmd.Flags().BoolVar(&showQRCode, "qrcode", false, "Display QR code for authentication")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output JSON (for MCP/automation)")

	return cmd
}

func runLogin(cmd *cobra.Command, args []string) error {
	if jsonOutput {
		return runLoginJSON()
	}
	return runLoginInteractive()
}

// LoginResult is the JSON output for exo login --json.
type LoginResult struct {
	Status   string `json:"status"`
	Username string `json:"username,omitempty"`
	Message  string `json:"message,omitempty"`
	AuthURL  string `json:"auth_url,omitempty"`
	UserCode string `json:"user_code,omitempty"`
}

func emitJSON(v any) {
	data, _ := json.Marshal(v)
	fmt.Println(string(data))
}

func runLoginJSON() error {
	tokenFile, err := GetTokenFile()
	if err != nil {
		return fmt.Errorf("failed to get token file path: %w", err)
	}

	if force {
		_ = DeleteToken(tokenFile)
	}

	// Always try to refresh when a refresh token exists
	if !force {
		token, err := LoadToken(tokenFile)
		if err == nil && token.RefreshToken != "" {
			refreshed, refreshUser, refreshErr := RefreshAuth(token.RefreshToken)
			if refreshErr == nil {
				if err := SaveToken(tokenFile, refreshed); err != nil {
					return fmt.Errorf("failed to save refreshed credentials: %w", err)
				}
				emitJSON(LoginResult{Status: "ok", Username: refreshUser, Message: "credentials refreshed"})
				return nil
			}
			// Refresh failed — fall through to device flow
		}
	}

	// Device flow — JSON mode
	// Show confidentiality disclaimer (stderr so JSON stdout stays clean)
	fmt.Fprintln(os.Stderr, "Notice: This system contains Roche confidential data (C3). Access is restricted to authorized personnel.")
	fmt.Fprintf(os.Stderr, "For more information: %s\n", confidentialityDisclaimerURL())
	token, username, err := DeviceFlowAuthJSON()
	if err != nil {
		return err
	}

	if err := SaveToken(tokenFile, token); err != nil {
		return fmt.Errorf("failed to save credentials: %w", err)
	}

	// Retrieve ExoSafe credentials (only on full device flow login)
	retrieveSafe(token.AccessToken, true)

	emitJSON(LoginResult{Status: "ok", Username: username, Message: "authenticated"})
	return nil
}

func runLoginInteractive() error {
	tokenFile, err := GetTokenFile()
	if err != nil {
		return fmt.Errorf("failed to get token file path: %w", err)
	}

	// If force flag is set, delete existing credentials
	if force {
		fmt.Println("🗑️  Deleting cached credentials...")
		err := DeleteToken(tokenFile)
		if err != nil {
			return fmt.Errorf("failed to delete cached credentials: %w", err)
		}
	}

	// Always try to refresh when a refresh token exists
	if !force {
		token, err := LoadToken(tokenFile)
		if err == nil && token.RefreshToken != "" {
			fmt.Println("🔄 Refreshing credentials...")
			refreshed, refreshUser, refreshErr := RefreshAuth(token.RefreshToken)
			if refreshErr == nil {
				err = SaveToken(tokenFile, refreshed)
				if err != nil {
					return fmt.Errorf("failed to save refreshed credentials: %w", err)
				}
				fmt.Printf("✅ Credentials refreshed for: %s\n", refreshUser)
				fmt.Printf("📁 Credentials saved to: %s\n", tokenFile)
				return nil
			}
			fmt.Printf("⚠️  Refresh failed: %v\n", refreshErr)
			fmt.Println("🔐 Falling back to interactive login...")
		}
	}

	// Perform device flow authentication
	// Show confidentiality disclaimer before the device flow starts
	printConfidentialityDisclaimer()
	token, username, err := DeviceFlowAuth(showQRCode)
	if err != nil {
		return err
	}

	// Save credentials
	err = SaveToken(tokenFile, token)
	if err != nil {
		return fmt.Errorf("failed to save credentials: %w", err)
	}

	fmt.Printf("\n✅ Successfully authenticated as: %s\n", username)
	fmt.Printf("📁 Credentials saved to: %s\n", tokenFile)

	// Retrieve ExoSafe credentials (only on full device flow login)
	retrieveSafe(token.AccessToken, false)

	return nil
}

// NewLogoutCommand creates the logout command
func NewLogoutCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Clear cached authentication credentials",
		Long: `Remove locally cached authentication credentials.

This command deletes the stored credentials, requiring you to log in again
on the next authenticated request.`,
		RunE: runLogout,
	}

	return cmd
}

func runLogout(cmd *cobra.Command, args []string) error {
	// TODO: Get context information when context support is added
	username := os.Getenv("USER")
	if username == "" {
		username = "unknown"
	}

	tokenFile, err := GetTokenFile()
	if err != nil {
		return fmt.Errorf("failed to get token file path: %w", err)
	}

	err = DeleteToken(tokenFile)
	if err != nil {
		return fmt.Errorf("failed to delete credentials: %w", err)
	}

	// Clean up ExoSafe provisioned credentials
	if err := safe.Cleanup(); err != nil {
		fmt.Printf("⚠️  Failed to clean up ExoSafe credentials: %v\n", err)
	}

	fmt.Println("✅ Successfully logged out. Credentials have been cleared.")
	return nil
}
