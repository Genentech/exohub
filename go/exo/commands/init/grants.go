package init

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"gopkg.in/yaml.v3"

	"github.com/Genentech/exohub/go/exo/commands/login"
	"github.com/Genentech/exohub/go/exo/commandutil"
	"github.com/Genentech/exohub/go/exo/internal/defaults"
	"github.com/Genentech/exohub/go/exo/palette"
)

// permissionsConfig represents .exohub/permissions file
type permissionsConfig struct {
	Owners      []string `json:"owners" yaml:"owners"`
	Viewers     []string `json:"viewers,omitempty" yaml:"viewers,omitempty"`
	ReadAccess  string   `json:"read_access" yaml:"read_access"`
	WriteAccess string   `json:"write_access" yaml:"write_access"`
}

// grantsSyncRequest is the payload for POST /api/grants/sync
type grantsSyncRequest struct {
	S3URL       string            `json:"s3url"`
	Permissions permissionsConfig `json:"permissions"`
}

// hasGrantsWriteAccess probes the credential helper to check if the current
// user has READWRITE access to the given s3url via S3 Access Grants.
func hasGrantsWriteAccess(s3url string) bool {
	credHelper := os.Getenv("EXO_CREDENTIAL_HELPER_BIN")
	if credHelper == "" {
		credHelper = "exo-credential-helper"
	}
	cmd := command(credHelper, "--s3url", s3url, "--permission", "READWRITE")
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return false
	}
	return true
}

// syncGrantsForRemote provisions S3 Access Grants for a remote with grants: true.
// It reads .exohub/permissions (or creates a default with the current user as owner),
// checks for drift via GET /api/grants/verify, and only syncs if needed.
//
// Grants are synced PER REMOTE (each remote's _grants.json is co-located with its
// own s3url), not consolidated to a common prefix: the grants API only ever sees a
// single remote's s3url, so co-locating the registry keeps lookup deterministic even
// when a dataset's remotes live in different folders or buckets.
func syncGrantsForRemote(s3url string) error {
	perms, err := loadOrCreatePermissions()
	if err != nil {
		return fmt.Errorf("failed to load permissions: %w", err)
	}

	// Verify the current user is listed as an owner before provisioning.
	// This prevents users from locking themselves out by provisioning
	// grants with someone else as the sole owner.
	username := currentUsername()
	isOwner := false
	for _, owner := range perms.Owners {
		if owner == username {
			isOwner = true
			break
		}
	}
	if !isOwner {
		if perms.ReadAccess == "public" {
			// Public datasets: on-demand credentials are provisioned by the credential
			// helper via POST /api/grants/credentials on first access. Nothing to do here.
			if commandutil.IsDebug() {
				fmt.Fprintf(os.Stderr, "DEBUG: skipping grants sync for %s (read_access: public, not an owner)\n", s3url)
			}
			return nil
		}
		orangeStyle := lipgloss.NewStyle().Foreground(palette.Current().Label.Adaptive()).Bold(true)
		fmt.Printf("⚠️ You (%s) are not listed as an owner in .exohub/permissions.\n", orangeStyle.Render(username))
		fmt.Println("   Provisioning grants without being an owner will lock you out.")
		fmt.Println()
		if flagYes {
			// --yes forces proceed (for admins/service accounts)
		} else {
			proceed, err := askYesNoTUI("Proceed anyway?", false)
			if err != nil || !proceed {
				return fmt.Errorf("Aborted: add yourself to .exohub/permissions owners or use --yes to force")
			}
		}
	}

	apiBase := grantsAPIBase()

	// Check for drift first
	inSync, err := verifyGrants(apiBase, s3url, perms)
	if err != nil {
		// Verify failed (API down, 404, etc.) — fall through to sync
		if commandutil.IsDebug() {
			fmt.Fprintf(os.Stderr, "DEBUG: grants verify failed, falling through to sync: %v\n", err)
		}
	} else if inSync {
		return nil
	}

	return doGrantsSync(apiBase, s3url, perms)
}

// grantsAPIBase returns the base URL for the grants API.
func grantsAPIBase() string {
	return strings.TrimRight(defaults.APIBase(), "/")
}

// verifyGrants checks if the current grants match the desired permissions.
// Returns (true, nil) if in sync, (false, nil) if drift detected, or (false, err) on failure.
func verifyGrants(apiBase, s3url string, perms *permissionsConfig) (bool, error) {
	endpoint := apiBase + "/grants/verify"

	payload := grantsSyncRequest{
		S3URL:       s3url,
		Permissions: *perms,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return false, err
	}

	commandutil.DebugHTTP(http.MethodPost, endpoint)

	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	if err := setAuthHeader(req); err != nil {
		// Continue without auth — verify may still work
	}
	req.Header.Set("X-Exohub-User", currentUsername())

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return false, fmt.Errorf("verify returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var result struct {
		InSync bool `json:"in_sync"`
		Drift  struct {
			MissingGrants        []json.RawMessage `json:"missing_grants"`
			ExtraGrants          []json.RawMessage `json:"extra_grants"`
			PermissionMismatches []json.RawMessage `json:"permission_mismatches"`
			RegistryDrift        []json.RawMessage `json:"registry_drift"`
			MetadataDrift        []json.RawMessage `json:"metadata_drift"`
		} `json:"drift"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, err
	}

	if !result.InSync {
		if len(result.Drift.MissingGrants) > 0 {
			fmt.Printf("Grants drift: %d missing grant(s)\n", len(result.Drift.MissingGrants))
		}
		if len(result.Drift.ExtraGrants) > 0 {
			fmt.Printf("Grants drift: %d extra grant(s)\n", len(result.Drift.ExtraGrants))
		}
		if len(result.Drift.PermissionMismatches) > 0 {
			fmt.Printf("Grants drift: %d permission mismatch(es)\n", len(result.Drift.PermissionMismatches))
		}

		registryDrift := result.Drift.RegistryDrift
		if len(registryDrift) == 0 {
			registryDrift = result.Drift.MetadataDrift
		}
		if len(registryDrift) > 0 {
			fmt.Printf("Grants drift: registry (_grants.json) differs — %d field(s) changed\n", len(registryDrift))
			for _, raw := range registryDrift {
				var entry struct {
					Grantee    string `json:"grantee"`
					Permission string `json:"permission"`
				}
				if err := json.Unmarshal(raw, &entry); err == nil && entry.Grantee != "" {
					fmt.Printf("  %s: %s\n", entry.Grantee, entry.Permission)
				}
			}
		}
	}

	return result.InSync, nil
}

// doGrantsSync calls POST /api/grants/sync to provision grants.
func doGrantsSync(apiBase, s3url string, perms *permissionsConfig) error {
	endpoint := apiBase + "/grants/sync"

	payload := grantsSyncRequest{
		S3URL:       s3url,
		Permissions: *perms,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal grants request: %w", err)
	}

	commandutil.DebugHTTP(http.MethodPost, endpoint)

	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	if err := setAuthHeader(req); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: no auth token available: %v\n", err)
	}
	req.Header.Set("X-Exohub-User", currentUsername())

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: grants API unreachable: %v\n", err)
		return nil
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		fmt.Printf("Grants synced for %s\n", s3url)
		if commandutil.IsDebug() {
			fmt.Fprintf(os.Stderr, "DEBUG: grants sync response: %s\n", strings.TrimSpace(string(respBody)))
		}
		return nil
	case resp.StatusCode == 400:
		msg := strings.TrimSpace(string(respBody))
		return fmt.Errorf("grants sync rejected: %s", msg)
	case resp.StatusCode == 403:
		return fmt.Errorf("Permission denied: you are not an owner of %s\nAsk an existing owner to add you to .exohub/permissions", s3url)
	default:
		msg := strings.TrimSpace(string(respBody))
		if msg == "" {
			msg = resp.Status
		}
		fmt.Fprintf(os.Stderr, "Warning: grants sync failed (%s): %s\n", resp.Status, msg)
		return nil
	}
}

// loadOrCreatePermissions reads .exohub/permissions or creates a default config
// with the current user as owner.
func loadOrCreatePermissions() (*permissionsConfig, error) {
	path := filepath.Join(".exohub", "permissions")
	data, err := os.ReadFile(path)
	if err == nil {
		var perms permissionsConfig
		if err := yaml.Unmarshal(data, &perms); err != nil {
			return nil, fmt.Errorf("failed to parse %s: %w", path, err)
		}
		if len(perms.Owners) == 0 {
			return nil, fmt.Errorf("%s: owners list is required", path)
		}
		return &perms, nil
	}

	if !os.IsNotExist(err) {
		return nil, err
	}

	// No permissions file — create default with current user as owner
	username := currentUsername()
	perms := &permissionsConfig{
		Owners:      []string{username},
		Viewers:     []string{},
		ReadAccess:  "viewers",
		WriteAccess: "owners",
	}

	// Write to disk so it can be committed and shared
	if err := writePermissionsFile(path, perms); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not write %s: %v\n", path, err)
	} else {
		fmt.Printf("Created %s (owner: %s)\n", path, username)
	}

	return perms, nil
}

// setAuthHeader adds the Bearer token from the credential store.
// If the token is expired and a refresh token is available, it attempts
// a silent refresh before falling back to the stored (possibly stale) token.
func setAuthHeader(req *http.Request) error {
	tokenFile, err := login.GetTokenFile()
	if err != nil {
		return err
	}
	token, err := login.LoadToken(tokenFile)
	if err != nil {
		return err
	}

	// If token is expired and we have a refresh token, try to refresh
	if !token.Valid() && token.RefreshToken != "" {
		refreshed, _, refreshErr := login.RefreshAuth(token.RefreshToken)
		if refreshErr == nil {
			_ = login.SaveToken(tokenFile, refreshed)
			token = refreshed
		}
	}

	if token.AccessToken == "" {
		return fmt.Errorf("empty access token")
	}
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	return nil
}

// writePermissionsFile writes a permissions config to disk as YAML.
func writePermissionsFile(path string, perms *permissionsConfig) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(perms); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0644)
}

// currentUsername returns the username from the stored JWT token (preferred_username claim),
// falling back to the OS username if no valid token is available.
func currentUsername() string {
	if name := usernameFromToken(); name != "" {
		return name
	}
	u, err := user.Current()
	if err != nil {
		return "unknown"
	}
	return u.Username
}

// usernameFromToken extracts preferred_username from the stored JWT ID token.
func usernameFromToken() string {
	tokenFile, err := login.GetTokenFile()
	if err != nil {
		return ""
	}
	token, err := login.LoadToken(tokenFile)
	if err != nil || token.AccessToken == "" {
		return ""
	}

	// The AccessToken is actually the raw JWT (ID token).
	// Decode the payload (second segment) to extract claims.
	parts := strings.SplitN(token.AccessToken, ".", 3)
	if len(parts) < 2 {
		return ""
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}

	var claims struct {
		PreferredUsername string `json:"preferred_username"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	return claims.PreferredUsername
}
