package auth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
)

// driveTokenFile mirrors the token struct in git-annex-remote-drive.
type driveTokenFile struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	TokenType    string    `json:"token_type"`
	Expiry       time.Time `json:"expiry"`
}

const driveScope = "https://www.googleapis.com/auth/drive.file"

// builtinDriveClientID and builtinDriveClientSecret are compiled in via
// -ldflags "-X ...". Google considers installed-app client secrets non-secret.
var builtinDriveClientID = "PLACEHOLDER_CLIENT_ID"
var builtinDriveClientSecret = "PLACEHOLDER_CLIENT_SECRET"

func tokenStorePath(remoteName string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".config", "exo", "drive-tokens", remoteName+".json")
}

func saveToken(path string, tf *driveTokenFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(tf)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func loadToken(path string) (*driveTokenFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var tf driveTokenFile
	if err := json.Unmarshal(data, &tf); err != nil {
		return nil, err
	}
	return &tf, nil
}

func deviceFlowAuth(clientID, clientSecret string) (*driveTokenFile, error) {
	resp, err := http.PostForm("https://oauth2.googleapis.com/device/code", url.Values{
		"client_id": {clientID},
		"scope":     {driveScope},
	})
	if err != nil {
		return nil, fmt.Errorf("device code request failed: %v", err)
	}
	defer resp.Body.Close()

	var dcResp map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&dcResp); err != nil {
		return nil, fmt.Errorf("device code response parse error: %v", err)
	}
	if errCode, ok := dcResp["error"].(string); ok {
		return nil, fmt.Errorf("device code error: %s", errCode)
	}

	deviceCode := dcResp["device_code"].(string)
	userCode := dcResp["user_code"].(string)
	verificationURL := dcResp["verification_url"].(string)
	interval := 5
	if v, ok := dcResp["interval"].(float64); ok {
		interval = int(v)
	}

	fmt.Printf("\nTo authorize exo to access Google Drive:\n")
	fmt.Printf("  1. Go to: %s\n", verificationURL)
	fmt.Printf("  2. Enter code: %s\n\n", userCode)
	fmt.Printf("Waiting for authorization")

	deadline := time.Now().Add(30 * time.Minute)
	for time.Now().Before(deadline) {
		time.Sleep(time.Duration(interval) * time.Second)
		fmt.Printf(".")

		pollResp, err := http.PostForm("https://oauth2.googleapis.com/token", url.Values{
			"client_id":     {clientID},
			"client_secret": {clientSecret},
			"device_code":   {deviceCode},
			"grant_type":    {"urn:ietf:params:oauth:grant-type:device_code"},
		})
		if err != nil {
			continue
		}

		var result map[string]interface{}
		json.NewDecoder(pollResp.Body).Decode(&result)
		pollResp.Body.Close()

		if errCode, ok := result["error"].(string); ok {
			switch errCode {
			case "authorization_pending", "invalid_request":
				continue
			case "slow_down":
				interval += 5
				continue
			case "access_denied":
				return nil, fmt.Errorf("auth error: user denied access")
			case "expired_token":
				return nil, fmt.Errorf("auth error: code expired, please retry")
			default:
				return nil, fmt.Errorf("auth error: %s", errCode)
			}
		}

		fmt.Println(" ✅")
		return &driveTokenFile{
			AccessToken: result["access_token"].(string),
			RefreshToken: func() string {
				if v, ok := result["refresh_token"].(string); ok {
					return v
				}
				return ""
			}(),
			TokenType: "Bearer",
			Expiry:    time.Now().Add(time.Duration(int64(result["expires_in"].(float64))) * time.Second),
		}, nil
	}
	return nil, fmt.Errorf("timeout: user did not authorize within 30 minutes")
}

func NewCommand() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "auth <remote-name>",
		Short: "Authorize a Google Drive remote",
		Long: `Authorize exo to access Google Drive for a specific remote.

Run this before syncing with a drive remote for the first time.
Credentials are stored at ~/.config/exo/drive-tokens/<remote-name>.json
and reused automatically by subsequent exo sync calls.

Examples:
  exo auth drive-export`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			remoteName := args[0]
			tokenPath := tokenStorePath(remoteName)

			// Check if already authorized
			if !force {
				if tf, err := loadToken(tokenPath); err == nil && tf.RefreshToken != "" {
					fmt.Printf("✅ Remote '%s' is already authorized.\n", remoteName)
					fmt.Printf("   Token: %s\n", tokenPath)
					fmt.Printf("   Use --force to re-authorize.\n")
					return nil
				}
			}

			clientID := os.Getenv("GOOGLE_CLIENT_ID")
			if clientID == "" {
				clientID = builtinDriveClientID
			}
			clientSecret := os.Getenv("GOOGLE_CLIENT_SECRET")
			if clientSecret == "" {
				clientSecret = builtinDriveClientSecret
			}
			if clientID == "PLACEHOLDER_CLIENT_ID" || clientSecret == "PLACEHOLDER_CLIENT_SECRET" {
				return fmt.Errorf("no Google OAuth2 credentials available; rebuild with -ldflags or set GOOGLE_CLIENT_ID/GOOGLE_CLIENT_SECRET")
			}

			tf, err := deviceFlowAuth(clientID, clientSecret)
			if err != nil {
				return fmt.Errorf("authorization failed: %w", err)
			}

			if err := saveToken(tokenPath, tf); err != nil {
				return fmt.Errorf("failed to save token: %w", err)
			}

			fmt.Printf("\n✅ Authorized. Token saved to %s\n", tokenPath)
			fmt.Printf("   You can now run: exo sync --with %s\n", remoteName)
			return nil
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "Re-authorize even if credentials already exist")
	return cmd
}
