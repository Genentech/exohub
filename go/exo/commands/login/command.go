package login

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/Genentech/exohub/go/exo/configdir"
	"github.com/Genentech/exohub/go/exo/internal/defaults"
	"github.com/Genentech/exohub/go/exo/safe"
)

var (
	force      bool
	showQRCode bool
	jsonOutput bool
)

// confidentialityNotice is the one-line notice printed before the device flow.
// Empty in the OSS build; set to the appropriate message by the internal overlay
// (command_internal.go).
var confidentialityNotice = ""

// registerExtraLoginFlags is an optional hook letting internal builds add
// extra flags to the login command (e.g. --roche-group). Nil in the OSS build.
var registerExtraLoginFlags func(cmd *cobra.Command)

// resolveLoginGroupFlag is an optional hook invoked once per `exo login` run
// with the parsed command and the resolved --force value, letting internal
// builds persist or clear a credential-scoping group selector. Nil in the OSS build.
var resolveLoginGroupFlag func(cmd *cobra.Command, force bool)

// activeGroupForDisplay is an optional hook returning a short label for the
// currently active credential-scoping group, shown in login output (and, until
// a dedicated whoami command exists, in `exo doctor`). Nil / empty in the OSS build.
var activeGroupForDisplay func() string

// onLogoutCleanup is an optional hook invoked during `exo logout`, letting
// internal builds clear any persisted provider-specific state. Nil in the OSS build.
var onLogoutCleanup func()

// extraLoginHelp is an optional paragraph appended to the login command's Long
// description, letting internal builds document internal-only flags/behaviour
// (e.g. --roche-group precedence) without the OSS build needing a build tag
// here. Empty in the OSS build.
var extraLoginHelp string

// onLoginSuccess is an optional hook invoked only after a login or refresh has
// fully succeeded (tokens fetched, AWS credentials fetched, token.json saved),
// letting internal builds persist any group selected via resolveLoginGroupFlag.
// Deferring persistence to this point means a failed or forbidden credential
// fetch (e.g. the user is not a member of the requested group) never leaves
// the group file out of sync with reality. Nil in the OSS build.
var onLoginSuccess func()

// confidentialityDisclaimerURL returns the URL to the ExoHub confidentiality page.
func confidentialityDisclaimerURL() string {
	return defaults.ExohubBase() + "/confidentiality"
}

// printConfidentialityDisclaimer prints the confidentiality notice to stdout.
// Does nothing when confidentialityNotice is empty (OSS build).
func printConfidentialityDisclaimer() {
	if confidentialityNotice == "" {
		return
	}
	fmt.Println()
	fmt.Println(confidentialityNotice)
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
securely cached for future use.` + extraLoginHelp,
		RunE: runLogin,
	}

	cmd.Flags().BoolVar(&force, "force", false, "Force re-authentication even if valid credentials exist")
	cmd.Flags().BoolVar(&showQRCode, "qrcode", false, "Display QR code for authentication")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output JSON (for MCP/automation)")

	if registerExtraLoginFlags != nil {
		registerExtraLoginFlags(cmd)
	}

	return cmd
}

func runLogin(cmd *cobra.Command, args []string) error {
	if resolveLoginGroupFlag != nil {
		resolveLoginGroupFlag(cmd, force)
	}
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
	Group    string `json:"group,omitempty"`
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
		_ = configdir.PurgeCredHelperCache()
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
				if onLoginSuccess != nil {
					onLoginSuccess()
				}
				group := ""
				if activeGroupForDisplay != nil {
					group = activeGroupForDisplay()
				}
				emitJSON(LoginResult{Status: "ok", Username: refreshUser, Message: "credentials refreshed", Group: group})
				return nil
			}
			// Refresh failed — fall through to device flow
		}
	}

	// Device flow — JSON mode
	// Show confidentiality disclaimer (stderr so JSON stdout stays clean)
	if confidentialityNotice != "" {
		fmt.Fprintln(os.Stderr, confidentialityNotice)
		fmt.Fprintf(os.Stderr, "For more information: %s\n", confidentialityDisclaimerURL())
	}
	token, username, err := DeviceFlowAuthJSON()
	if err != nil {
		return err
	}

	if err := SaveToken(tokenFile, token); err != nil {
		return fmt.Errorf("failed to save credentials: %w", err)
	}
	if onLoginSuccess != nil {
		onLoginSuccess()
	}

	// Retrieve ExoSafe credentials (only on full device flow login)
	retrieveSafe(token.AccessToken, true)

	group := ""
	if activeGroupForDisplay != nil {
		group = activeGroupForDisplay()
	}
	emitJSON(LoginResult{Status: "ok", Username: username, Message: "authenticated", Group: group})
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
		if err := configdir.PurgeCredHelperCache(); err != nil {
			fmt.Printf("⚠️  Failed to purge S3 credential cache: %v\n", err)
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
				if onLoginSuccess != nil {
					onLoginSuccess()
				}
				fmt.Printf("✅ Credentials refreshed for: %s\n", refreshUser)
				fmt.Printf("📁 Credentials saved to: %s\n", tokenFile)
				printActiveGroup()
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
	if onLoginSuccess != nil {
		onLoginSuccess()
	}

	fmt.Printf("\n✅ Successfully authenticated as: %s\n", username)
	fmt.Printf("📁 Credentials saved to: %s\n", tokenFile)
	printActiveGroup()

	// Retrieve ExoSafe credentials (only on full device flow login)
	retrieveSafe(token.AccessToken, false)

	return nil
}

// printActiveGroup prints the active credential-scoping group, if any is set.
// No-op in the OSS build (activeGroupForDisplay is nil).
func printActiveGroup() {
	if activeGroupForDisplay == nil {
		return
	}
	if group := activeGroupForDisplay(); group != "" {
		fmt.Printf("🔖 Active group: %s\n", group)
	}
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

	if err := configdir.PurgeCredHelperCache(); err != nil {
		fmt.Printf("⚠️  Failed to purge S3 credential cache: %v\n", err)
	}

	if onLogoutCleanup != nil {
		onLogoutCleanup()
	}

	// Clean up ExoSafe provisioned credentials
	if err := safe.Cleanup(); err != nil {
		fmt.Printf("⚠️  Failed to clean up ExoSafe credentials: %v\n", err)
	}

	fmt.Println("✅ Successfully logged out. Credentials have been cleared.")
	return nil
}
