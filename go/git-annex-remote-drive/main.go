package main

// git-annex-remote-drive: external special remote for Google Drive.
// Implements git-annex external special remote protocol v1 (export mode).
//
// Required init param: drive_path=/My Drive/datasets/mydir
// Optional env:
//   GOOGLE_CLIENT_ID      – override built-in OAuth2 client ID
//   GOOGLE_CLIENT_SECRET  – override built-in OAuth2 client secret
//   EXOHUB_CLI_DEBUG=1    – write debug output to stderr

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

var appVersion = "dev"

// builtinClientID and builtinClientSecret are compiled into the binary.
// Users can override with GOOGLE_CLIENT_ID / GOOGLE_CLIENT_SECRET env vars.
// These placeholders will be replaced before release.
var builtinClientID = "PLACEHOLDER_CLIENT_ID"
var builtinClientSecret = "PLACEHOLDER_CLIENT_SECRET"

const driveScope = "https://www.googleapis.com/auth/drive.file"
const folderMimeType = "application/vnd.google-apps.folder"
const multipartThreshold = 5 * 1024 * 1024 // 5 MB

// tokenFile stores persistent OAuth2 tokens.
type tokenFile struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	TokenType    string    `json:"token_type"`
	Expiry       time.Time `json:"expiry"`
}

// ---- global state ----

var (
	config     = map[string]string{}
	exportName string
	remoteName string // git-annex remote name, used for token file path

	// Drive service
	drv          *drive.Service
	rootFolderID string // resolved from drive_path

	// Exclude patterns (comma-separated globs from exclude config)
	excludePatterns []string

	// In-memory folder path→ID cache
	folderCacheMu sync.Mutex
	folderCache   = map[string]string{} // absolute Drive path → folder ID

	in   = bufio.NewReader(os.Stdin)
	outw = bufio.NewWriter(os.Stdout)
)

// ---- I/O helpers ----

var writeLine = func(line string) {
	outw.WriteString(line)
	outw.WriteByte('\n')
	outw.Flush()
}

var debugProtocol = func(msg string) {
	writeLine("DEBUG " + msg)
}

func debugStderr(format string, args ...any) {
	if os.Getenv("EXOHUB_CLI_DEBUG") == "1" {
		fmt.Fprintf(os.Stderr, "DEBUG: "+format+"\n", args...)
	}
}

func sanitizeMsg(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimSpace(s)
}

// matchesExclude returns true if the export path matches any exclude pattern.
func matchesExclude(exportPath string) bool {
	for _, pattern := range excludePatterns {
		matched, err := path.Match(pattern, exportPath)
		if err == nil && matched {
			return true
		}
		// Handle dir/** patterns: match path == dir or path starts with dir/
		if strings.HasSuffix(pattern, "/**") {
			prefix := strings.TrimSuffix(pattern, "/**")
			if exportPath == prefix || strings.HasPrefix(exportPath, prefix+"/") {
				return true
			}
		}
	}
	return false
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func readLine() (string, bool) {
	s, err := in.ReadString('\n')
	if err != nil {
		if len(s) == 0 {
			return "", false
		}
	}
	return strings.TrimRight(s, "\n"), true
}

// getConfigFromAnnex emits GETCONFIG and reads the VALUE response.
var getConfigFromAnnex = func(key string) string {
	writeLine("GETCONFIG " + key)
	for {
		line, ok := readLine()
		if !ok {
			return ""
		}
		if strings.HasPrefix(line, "VALUE ") {
			val := strings.TrimPrefix(line, "VALUE ")
			config[key] = val
			return val
		}
		if line == "VALUE" {
			config[key] = ""
			return ""
		}
	}
}

// getRemoteName fetches the git-annex remote name for token storage.
func getRemoteName() string {
	if remoteName != "" {
		return remoteName
	}
	writeLine("GETGITREMOTENAME")
	for {
		line, ok := readLine()
		if !ok {
			return "default"
		}
		if strings.HasPrefix(line, "VALUE ") {
			remoteName = strings.TrimPrefix(line, "VALUE ")
			return remoteName
		}
	}
}

// ---- OAuth2 ----

func clientCredentials() (clientID, clientSecret string) {
	clientID = envOr("GOOGLE_CLIENT_ID", builtinClientID)
	clientSecret = envOr("GOOGLE_CLIENT_SECRET", builtinClientSecret)
	return
}

func tokenStorePath(name string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".config", "exo", "drive-tokens", name+".json")
}

func loadStoredToken(path string) (*tokenFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var tf tokenFile
	if err := json.Unmarshal(data, &tf); err != nil {
		return nil, err
	}
	return &tf, nil
}

func saveToken(path string, tf *tokenFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(tf)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// refreshToken exchanges a refresh_token for a new access token.
// Returns (newToken, invalidGrant, error).
func refreshToken(clientID, clientSecret, refreshTok string) (*tokenFile, bool, error) {
	resp, err := http.PostForm("https://oauth2.googleapis.com/token", url.Values{
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"refresh_token": {refreshTok},
		"grant_type":    {"refresh_token"},
	})
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, false, fmt.Errorf("refresh response parse error: %v", err)
	}
	if errCode, ok := result["error"].(string); ok {
		if errCode == "invalid_grant" {
			return nil, true, fmt.Errorf("refresh token revoked or expired")
		}
		return nil, false, fmt.Errorf("token refresh error: %s", errCode)
	}
	tf := &tokenFile{
		AccessToken:  result["access_token"].(string),
		RefreshToken: refreshTok,
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(time.Duration(int64(result["expires_in"].(float64))) * time.Second),
	}
	return tf, false, nil
}

// isTerminal returns true if stderr (used for prompts) is a TTY.
func isTerminal() bool {
	fi, err := os.Stderr.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// openBrowser attempts to open the given URL in a browser.
func openBrowser(u string) error {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
		args = []string{u}
	case "windows":
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", u}
	default:
		cmd = "xdg-open"
		args = []string{u}
	}
	return exec.Command(cmd, args...).Start()
}

// generatePKCE creates code_verifier and code_challenge for PKCE flow.
func generatePKCE() (verifier, challenge string, err error) {
	buf := make([]byte, 32)
	if _, err = rand.Read(buf); err != nil {
		return
	}
	verifier = base64.RawURLEncoding.EncodeToString(buf)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return
}

// pkceFlow runs the PKCE (browser-based) OAuth2 flow.
func pkceFlow(clientID string) (*tokenFile, error) {
	verifier, challenge, err := generatePKCE()
	if err != nil {
		return nil, fmt.Errorf("PKCE generation failed: %v", err)
	}

	// Start a localhost server to receive the redirect
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("cannot start local server: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/callback", port)

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)
	srv := &http.Server{}
	http.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		if code == "" {
			errMsg := r.URL.Query().Get("error")
			errCh <- fmt.Errorf("no code in callback: %s", errMsg)
			fmt.Fprintf(w, "<html><body><p>Authorization failed. You may close this tab.</p></body></html>")
			return
		}
		codeCh <- code
		fmt.Fprintf(w, "<html><body><p>Authorization complete! You may close this tab.</p></body></html>")
	})
	go func() { _ = srv.Serve(listener) }()

	authURL := "https://accounts.google.com/o/oauth2/v2/auth?" + url.Values{
		"client_id":             {clientID},
		"redirect_uri":          {redirectURI},
		"response_type":         {"code"},
		"scope":                 {driveScope},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"access_type":           {"offline"},
		"prompt":                {"consent"},
	}.Encode()

	fmt.Fprintf(os.Stderr, "\nOpening browser for Google Drive authorization...\n")
	fmt.Fprintf(os.Stderr, "If the browser does not open, visit:\n  %s\n\n", authURL)
	_ = openBrowser(authURL)

	var code string
	select {
	case code = <-codeCh:
	case err = <-errCh:
		_ = srv.Close()
		return nil, err
	case <-time.After(5 * time.Minute):
		_ = srv.Close()
		return nil, fmt.Errorf("timeout waiting for browser authorization")
	}
	_ = srv.Close()

	// Exchange code for tokens
	resp, err := http.PostForm("https://oauth2.googleapis.com/token", url.Values{
		"client_id":     {clientID},
		"redirect_uri":  {redirectURI},
		"code":          {code},
		"code_verifier": {verifier},
		"grant_type":    {"authorization_code"},
	})
	if err != nil {
		return nil, fmt.Errorf("token exchange failed: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("token response parse error: %v", err)
	}
	if errCode, ok := result["error"].(string); ok {
		return nil, fmt.Errorf("token exchange error: %s", errCode)
	}
	return &tokenFile{
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

// deviceFlow runs the device authorization flow (headless).
func deviceFlow(clientID, clientSecret string) (*tokenFile, error) {
	resp, err := http.PostForm("https://oauth2.googleapis.com/device/code", url.Values{
		"client_id": {clientID},
		"scope":     {driveScope},
	})
	if err != nil {
		return nil, fmt.Errorf("device code request failed: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var dcResp map[string]interface{}
	if err := json.Unmarshal(body, &dcResp); err != nil {
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

	fmt.Fprintf(os.Stderr, "\nTo authorize exo to access Google Drive:\n")
	fmt.Fprintf(os.Stderr, "  1. Go to: %s\n", verificationURL)
	fmt.Fprintf(os.Stderr, "  2. Enter code: %s\n\n", userCode)

	// Poll until authorized
	deadline := time.Now().Add(30 * time.Minute)
	for time.Now().Before(deadline) {
		time.Sleep(time.Duration(interval) * time.Second)

		pollResp, err := http.PostForm("https://oauth2.googleapis.com/token", url.Values{
			"client_id":     {clientID},
			"client_secret": {clientSecret},
			"device_code":   {deviceCode},
			"grant_type":    {"urn:ietf:params:oauth:grant-type:device_code"},
		})
		if err != nil {
			continue
		}
		pollBody, _ := io.ReadAll(pollResp.Body)
		pollResp.Body.Close()

		var pollResult map[string]interface{}
		if err := json.Unmarshal(pollBody, &pollResult); err != nil {
			continue
		}

		if errCode, ok := pollResult["error"].(string); ok {
			switch errCode {
			case "authorization_pending", "invalid_request":
				continue
			case "slow_down":
				interval += 5
				continue
			case "access_denied":
				return nil, fmt.Errorf("device flow error: user denied access")
			case "expired_token":
				return nil, fmt.Errorf("device flow error: code expired, please retry")
			default:
				return nil, fmt.Errorf("device flow error: %s", errCode)
			}
		}

		return &tokenFile{
			AccessToken: pollResult["access_token"].(string),
			RefreshToken: func() string {
				if v, ok := pollResult["refresh_token"].(string); ok {
					return v
				}
				return ""
			}(),
			TokenType: "Bearer",
			Expiry:    time.Now().Add(time.Duration(int64(pollResult["expires_in"].(float64))) * time.Second),
		}, nil
	}
	return nil, fmt.Errorf("device flow timeout: user did not authorize within 30 minutes")
}

// acquireToken loads a stored token or triggers an OAuth2 flow.
func acquireToken(tokenPath string) (*tokenFile, error) {
	clientID, clientSecret := clientCredentials()

	// Try loading existing token
	tf, err := loadStoredToken(tokenPath)
	if err == nil && tf.RefreshToken != "" {
		// Check expiry – refresh if needed
		if time.Until(tf.Expiry) < 5*time.Minute {
			debugStderr("token: refreshing expired access token")
			newTf, invalidGrant, rerr := refreshToken(clientID, clientSecret, tf.RefreshToken)
			if rerr != nil {
				if invalidGrant {
					debugStderr("token: refresh token revoked, re-triggering consent")
					// Fall through to new consent flow below
					goto newConsent
				}
				return nil, fmt.Errorf("token refresh failed: %v", rerr)
			}
			if newTf.RefreshToken == "" {
				newTf.RefreshToken = tf.RefreshToken
			}
			if serr := saveToken(tokenPath, newTf); serr != nil {
				debugStderr("token: could not save refreshed token: %v", serr)
			}
			return newTf, nil
		}
		return tf, nil
	}

newConsent:
	// Need new consent
	var newTf *tokenFile
	if isTerminal() {
		newTf, err = pkceFlow(clientID)
	} else {
		newTf, err = deviceFlow(clientID, clientSecret)
	}
	if err != nil {
		return nil, fmt.Errorf("authorization failed: %v", err)
	}
	if serr := saveToken(tokenPath, newTf); serr != nil {
		debugStderr("token: could not save token: %v", serr)
	}
	return newTf, nil
}

// ---- Drive service ----

// newDriveService creates an authorized *drive.Service from a token.
func newDriveService(tf *tokenFile) (*drive.Service, error) {
	clientID, clientSecret := clientCredentials()
	conf := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Scopes:       []string{driveScope},
		Endpoint: oauth2.Endpoint{
			TokenURL: "https://oauth2.googleapis.com/token",
		},
	}
	tok := &oauth2.Token{
		AccessToken:  tf.AccessToken,
		RefreshToken: tf.RefreshToken,
		TokenType:    tf.TokenType,
		Expiry:       tf.Expiry,
	}
	ts := conf.TokenSource(context.Background(), tok)
	svc, err := drive.NewService(context.Background(), option.WithTokenSource(ts))
	if err != nil {
		return nil, err
	}
	return svc, nil
}

// ---- Rate-limit backoff ----

// withBackoff runs fn up to maxRetries times, backing off on 403/429.
func withBackoff(fn func() error) error {
	const maxRetries = 7
	for attempt := 0; attempt < maxRetries; attempt++ {
		err := fn()
		if err == nil {
			return nil
		}
		if gerr, ok := err.(*googleapi.Error); ok {
			if gerr.Code == 403 || gerr.Code == 429 {
				delay := time.Duration(math.Pow(2, float64(attempt)))*time.Second +
					jitter(500*time.Millisecond)
				debugStderr("rate limit: backing off %v (attempt %d)", delay, attempt+1)
				time.Sleep(delay)
				continue
			}
		}
		return err
	}
	return fmt.Errorf("rate limit: max retries exceeded")
}

func jitter(max time.Duration) time.Duration {
	n, _ := rand.Int(rand.Reader, big.NewInt(int64(max)))
	return time.Duration(n.Int64())
}

// ---- Folder cache & resolution ----

// folderID returns the cached folder ID for the given Drive path, or "".
func cachedFolderID(path string) string {
	folderCacheMu.Lock()
	defer folderCacheMu.Unlock()
	return folderCache[path]
}

func cacheFolderID(path, id string) {
	folderCacheMu.Lock()
	defer folderCacheMu.Unlock()
	folderCache[path] = id
}

// findOrCreateFolder returns the folder ID for name under parentID,
// creating the folder if it doesn't exist.
func findOrCreateFolder(name, parentID string) (string, error) {
	var found string
	err := withBackoff(func() error {
		q := fmt.Sprintf("name = %q and %q in parents and mimeType = %q and trashed = false",
			name, parentID, folderMimeType)
		r, err := drv.Files.List().Q(q).Fields("files(id)").Do()
		if err != nil {
			return err
		}
		if len(r.Files) > 0 {
			found = r.Files[0].Id
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("folder lookup failed: %v", err)
	}
	if found != "" {
		return found, nil
	}

	// Create folder
	var created *drive.File
	err = withBackoff(func() error {
		var cerr error
		created, cerr = drv.Files.Create(&drive.File{
			Name:     name,
			MimeType: folderMimeType,
			Parents:  []string{parentID},
		}).Fields("id").Do()
		return cerr
	})
	if err != nil {
		return "", fmt.Errorf("folder creation failed for %q: %v", name, err)
	}
	return created.Id, nil
}

// resolvePath resolves a slash-separated Drive path to a folder ID,
// creating missing folders as needed. An empty path returns rootFolderID.
//
// The path should be relative to rootFolderID (not the full drive_path).
func resolvePath(relPath string) (string, error) {
	if relPath == "" || relPath == "." {
		return rootFolderID, nil
	}

	parts := strings.Split(strings.Trim(relPath, "/"), "/")
	current := rootFolderID
	accumulated := ""
	for _, part := range parts {
		if part == "" {
			continue
		}
		if accumulated == "" {
			accumulated = part
		} else {
			accumulated += "/" + part
		}
		if id := cachedFolderID(accumulated); id != "" {
			current = id
			continue
		}
		id, err := findOrCreateFolder(part, current)
		if err != nil {
			return "", err
		}
		cacheFolderID(accumulated, id)
		current = id
	}
	return current, nil
}

// resolveRootDrivePath resolves the top-level drive_path config to a folder ID.
// The path may look like "/My Drive/datasets/exohub" or just a folder ID.
//
// Uses "root" as the literal parent alias for top-level folders so that the
// drive.file scope works correctly — the API accepts "root" as a parent in
// queries even without full drive access.
func resolveRootDrivePath(drivePath string) (string, error) {
	// If it looks like a raw folder ID (no slashes, no spaces), use directly.
	if !strings.Contains(drivePath, "/") && !strings.Contains(drivePath, " ") {
		return drivePath, nil
	}

	// Strip leading slash and split
	parts := strings.Split(strings.Trim(drivePath, "/"), "/")
	if len(parts) == 0 {
		return "", fmt.Errorf("drive_path is empty")
	}

	// Skip "My Drive" at the root (it is the default root)
	if strings.EqualFold(parts[0], "my drive") {
		parts = parts[1:]
	}
	if len(parts) == 0 {
		// drive_path was just "/My Drive" — return the root alias
		return "root", nil
	}

	// Walk the path using "root" as the literal parent alias for the first
	// level. The Drive API accepts "root" in parent queries under drive.file.
	current := "root"
	accumulated := ""
	for _, part := range parts {
		if part == "" {
			continue
		}
		if accumulated == "" {
			accumulated = part
		} else {
			accumulated += "/" + part
		}

		if id := cachedFolderID("root/" + accumulated); id != "" {
			current = id
			continue
		}

		id, err := findOrCreateFolder(part, current)
		if err != nil {
			return "", fmt.Errorf("cannot resolve path component %q: %v", part, err)
		}
		cacheFolderID("root/"+accumulated, id)
		current = id
	}
	return current, nil
}

// ---- File operations ----

// findFile returns the file ID for name in parentID, or "" if not found.
func findFile(name, parentID string) (string, error) {
	var found string
	err := withBackoff(func() error {
		q := fmt.Sprintf("name = %q and %q in parents and mimeType != %q and trashed = false",
			name, parentID, folderMimeType)
		r, err := drv.Files.List().Q(q).Fields("files(id)").Do()
		if err != nil {
			return err
		}
		if len(r.Files) > 0 {
			found = r.Files[0].Id
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return found, nil
}

// uploadFile uploads filePath to parentID with the given name.
// Uses multipart for files < 5MB, resumable for larger files.
func uploadFile(name, parentID, filePath string) error {
	fi, err := os.Stat(filePath)
	if err != nil {
		return fmt.Errorf("stat %q: %v", filePath, err)
	}

	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("open %q: %v", filePath, err)
	}
	defer f.Close()

	meta := &drive.File{
		Name:    name,
		Parents: []string{parentID},
	}

	// Check if file already exists; if so, update instead of create
	existingID, err := findFile(name, parentID)
	if err != nil {
		return fmt.Errorf("pre-upload lookup: %v", err)
	}

	if fi.Size() < multipartThreshold {
		return withBackoff(func() error {
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				return err
			}
			if existingID != "" {
				// Update existing file
				_, err := drv.Files.Update(existingID, &drive.File{}).
					Media(f).Do()
				return err
			}
			_, err := drv.Files.Create(meta).Media(f).Do()
			return err
		})
	}

	// Resumable upload for large files
	return withBackoff(func() error {
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return err
		}
		if existingID != "" {
			_, err := drv.Files.Update(existingID, &drive.File{}).
				ResumableMedia(context.Background(), f, fi.Size(), "application/octet-stream").Do()
			return err
		}
		_, err := drv.Files.Create(meta).
			ResumableMedia(context.Background(), f, fi.Size(), "application/octet-stream").Do()
		return err
	})
}

// downloadFile downloads fileID to destPath.
func downloadFile(fileID, destPath string) error {
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return fmt.Errorf("mkdir: %v", err)
	}

	var resp *http.Response
	err := withBackoff(func() error {
		var err error
		resp, err = drv.Files.Get(fileID).Download()
		return err
	})
	if err != nil {
		return fmt.Errorf("download: %v", err)
	}
	defer resp.Body.Close()

	tmp := destPath + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("create temp file: %v", err)
	}
	if _, err := io.Copy(out, resp.Body); err != nil {
		out.Close()
		os.Remove(tmp)
		return fmt.Errorf("write: %v", err)
	}
	out.Close()
	return os.Rename(tmp, destPath)
}

// deleteFile permanently deletes fileID from Drive.
func deleteFile(fileID string) error {
	return withBackoff(func() error {
		return drv.Files.Delete(fileID).Do()
	})
}

// ---- Protocol handlers ----

func parseExcludePatterns() {
	raw := config["exclude"]
	if raw == "" {
		raw = getConfigFromAnnex("exclude")
	}
	excludePatterns = nil
	for _, p := range strings.Split(raw, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		// Normalize trailing "/" to "/**" so ".exohub/" matches ".exohub/bundle" etc.
		if strings.HasSuffix(p, "/") {
			p = p + "**"
		}
		excludePatterns = append(excludePatterns, p)
	}
	debugStderr("exclude patterns: %v", excludePatterns)
}

func handleInitRemote() {
	if config["drive_path"] == "" {
		_ = getConfigFromAnnex("drive_path")
	}
	if config["drive_path"] == "" {
		writeLine("INITREMOTE-FAILURE missing required config: drive_path")
		return
	}
	parseExcludePatterns()
	writeLine("INITREMOTE-SUCCESS")
}

func handlePrepare() {
	if config["drive_path"] == "" {
		_ = getConfigFromAnnex("drive_path")
	}
	if config["drive_path"] == "" {
		writeLine("PREPARE-FAILURE missing required config: drive_path")
		return
	}

	parseExcludePatterns()

	name := getRemoteName()
	tokenPath := tokenStorePath(name)
	debugStderr("prepare: loading token from %s", tokenPath)

	// Fail fast if no stored token exists — interactive auth must be done
	// upfront via `exo auth <remote-name>` before syncing.
	if _, err := loadStoredToken(tokenPath); err != nil {
		writeLine("PREPARE-FAILURE Google Drive not authorized. Run: exo auth " + name)
		return
	}

	tf, err := acquireToken(tokenPath)
	if err != nil {
		writeLine("PREPARE-FAILURE " + sanitizeMsg(err.Error()))
		return
	}

	svc, err := newDriveService(tf)
	if err != nil {
		writeLine("PREPARE-FAILURE " + sanitizeMsg(err.Error()))
		return
	}
	drv = svc

	folderID, err := resolveRootDrivePath(config["drive_path"])
	if err != nil {
		writeLine("PREPARE-FAILURE " + sanitizeMsg(err.Error()))
		return
	}
	rootFolderID = folderID
	debugStderr("prepare: root folder ID = %s", rootFolderID)
	writeLine("PREPARE-SUCCESS")
}

func handleExport(name string) {
	exportName = name
	debugStderr("EXPORT name=%s", name)
}

// exportParentAndFile returns the parent folder ID and filename for the
// current export name.
func exportParentAndFile() (parentID, fileName string, err error) {
	if exportName == "" {
		return "", "", fmt.Errorf("no export name set")
	}
	dir := filepath.Dir(exportName)
	if dir == "." || dir == "/" {
		return rootFolderID, filepath.Base(exportName), nil
	}
	parentID, err = resolvePath(dir)
	if err != nil {
		return "", "", fmt.Errorf("resolving parent %q: %v", dir, err)
	}
	return parentID, filepath.Base(exportName), nil
}

func handleTransferExportStore(key, filePath string) {
	if matchesExclude(exportName) {
		debugStderr("TRANSFEREXPORT STORE skipping excluded: %s", exportName)
		writeLine("TRANSFER-SUCCESS STORE " + key)
		return
	}
	parentID, fileName, err := exportParentAndFile()
	if err != nil {
		writeLine("TRANSFER-FAILURE STORE " + key + " " + sanitizeMsg(err.Error()))
		return
	}
	debugStderr("TRANSFEREXPORT STORE key=%s file=%s name=%s parentID=%s", key, filePath, fileName, parentID)

	if fi, err := os.Stat(filePath); err != nil || fi.IsDir() {
		writeLine("TRANSFER-FAILURE STORE " + key + " local file not found")
		return
	}

	if err := uploadFile(fileName, parentID, filePath); err != nil {
		writeLine("TRANSFER-FAILURE STORE " + key + " " + sanitizeMsg(err.Error()))
		return
	}
	writeLine("TRANSFER-SUCCESS STORE " + key)
}

func handleTransferExportRetrieve(key, filePath string) {
	parentID, fileName, err := exportParentAndFile()
	if err != nil {
		writeLine("TRANSFER-FAILURE RETRIEVE " + key + " " + sanitizeMsg(err.Error()))
		return
	}
	debugStderr("TRANSFEREXPORT RETRIEVE key=%s file=%s name=%s parentID=%s", key, filePath, fileName, parentID)

	fileID, err := findFile(fileName, parentID)
	if err != nil {
		writeLine("TRANSFER-FAILURE RETRIEVE " + key + " " + sanitizeMsg(err.Error()))
		return
	}
	if fileID == "" {
		writeLine("TRANSFER-FAILURE RETRIEVE " + key + " file not found in Drive")
		return
	}

	if err := downloadFile(fileID, filePath); err != nil {
		writeLine("TRANSFER-FAILURE RETRIEVE " + key + " " + sanitizeMsg(err.Error()))
		return
	}
	writeLine("TRANSFER-SUCCESS RETRIEVE " + key)
}

func handleCheckPresentExport(key string) {
	if matchesExclude(exportName) {
		debugStderr("CHECKPRESENTEXPORT skipping excluded: %s", exportName)
		writeLine("CHECKPRESENT-FAILURE " + key)
		return
	}
	parentID, fileName, err := exportParentAndFile()
	if err != nil {
		writeLine("CHECKPRESENT-UNKNOWN " + key + " " + sanitizeMsg(err.Error()))
		return
	}
	debugStderr("CHECKPRESENTEXPORT key=%s name=%s parentID=%s", key, fileName, parentID)

	fileID, err := findFile(fileName, parentID)
	if err != nil {
		writeLine("CHECKPRESENT-UNKNOWN " + key + " " + sanitizeMsg(err.Error()))
		return
	}
	if fileID != "" {
		writeLine("CHECKPRESENT-SUCCESS " + key)
	} else {
		writeLine("CHECKPRESENT-FAILURE " + key)
	}
}

func handleRemoveExport(key string) {
	if matchesExclude(exportName) {
		debugStderr("REMOVEEXPORT skipping excluded: %s", exportName)
		writeLine("REMOVE-SUCCESS " + key)
		return
	}
	parentID, fileName, err := exportParentAndFile()
	if err != nil {
		writeLine("REMOVE-FAILURE " + key + " " + sanitizeMsg(err.Error()))
		return
	}
	debugStderr("REMOVEEXPORT key=%s name=%s parentID=%s", key, fileName, parentID)

	fileID, err := findFile(fileName, parentID)
	if err != nil {
		writeLine("REMOVE-FAILURE " + key + " " + sanitizeMsg(err.Error()))
		return
	}
	if fileID == "" {
		// Already gone — success
		writeLine("REMOVE-SUCCESS " + key)
		return
	}

	if err := deleteFile(fileID); err != nil {
		writeLine("REMOVE-FAILURE " + key + " " + sanitizeMsg(err.Error()))
		return
	}
	writeLine("REMOVE-SUCCESS " + key)
}

func handleRemoveExportDirectoryWhenEmpty(directory string) {
	debugStderr("REMOVEEXPORTDIRECTORYWHENEMPTY dir=%s", directory)

	parentID, err := resolvePath(filepath.Dir(directory))
	if err != nil {
		// Can't resolve parent — skip silently
		writeLine("REMOVEEXPORTDIRECTORY-SUCCESS")
		return
	}
	dirName := filepath.Base(directory)

	// Find the folder
	var folderID string
	err = withBackoff(func() error {
		q := fmt.Sprintf("name = %q and %q in parents and mimeType = %q and trashed = false",
			dirName, parentID, folderMimeType)
		r, e := drv.Files.List().Q(q).Fields("files(id)").Do()
		if e != nil {
			return e
		}
		if len(r.Files) > 0 {
			folderID = r.Files[0].Id
		}
		return nil
	})
	if err != nil || folderID == "" {
		writeLine("REMOVEEXPORTDIRECTORY-SUCCESS")
		return
	}

	// Check if folder is empty
	var hasChildren bool
	err = withBackoff(func() error {
		q := fmt.Sprintf("%q in parents and trashed = false", folderID)
		r, e := drv.Files.List().Q(q).Fields("files(id)").PageSize(1).Do()
		if e != nil {
			return e
		}
		hasChildren = len(r.Files) > 0
		return nil
	})
	if err != nil || hasChildren {
		writeLine("REMOVEEXPORTDIRECTORY-SUCCESS")
		return
	}

	// Delete the empty folder
	_ = deleteFile(folderID)

	// Evict from cache
	folderCacheMu.Lock()
	delete(folderCache, directory)
	folderCacheMu.Unlock()

	writeLine("REMOVEEXPORTDIRECTORY-SUCCESS")
}

// ---- main ----

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-version") {
		fmt.Println(appVersion)
		return
	}

	writeLine("VERSION 1")

	for {
		line, ok := readLine()
		if !ok {
			break
		}
		if line == "" {
			continue
		}
		if line == "EXIT" {
			break
		}

		switch {
		case line == "EXTENSIONS" || strings.HasPrefix(line, "EXTENSIONS "):
			writeLine("EXTENSIONS")

		case strings.HasPrefix(line, "VERSION "):
			// Accept but ignore

		case line == "LISTCONFIGS":
			writeLine("CONFIG drive_path Drive folder path (e.g. /My Drive/datasets/mydir)")
			writeLine("CONFIG exclude Comma-separated glob patterns to exclude (e.g. .exohub/**,.codex)")
			writeLine("CONFIGEND")

		case strings.HasPrefix(line, "SETCONFIG "):
			rest := strings.TrimPrefix(line, "SETCONFIG ")
			kv := strings.SplitN(rest, " ", 2)
			if len(kv) == 2 {
				config[kv[0]] = kv[1]
			}

		case line == "INITREMOTE":
			handleInitRemote()

		case line == "PREPARE":
			handlePrepare()

		case line == "EXPORTSUPPORTED":
			writeLine("EXPORTSUPPORTED-SUCCESS")

		case strings.HasPrefix(line, "EXPORT "):
			name := strings.TrimPrefix(line, "EXPORT ")
			handleExport(name)

		case strings.HasPrefix(line, "TRANSFEREXPORT "):
			rest := strings.TrimPrefix(line, "TRANSFEREXPORT ")
			parts := strings.SplitN(rest, " ", 3)
			if len(parts) != 3 {
				writeLine("ERROR malformed TRANSFEREXPORT")
				continue
			}
			op, key, file := parts[0], parts[1], parts[2]
			switch op {
			case "STORE":
				handleTransferExportStore(key, file)
			case "RETRIEVE":
				handleTransferExportRetrieve(key, file)
			default:
				writeLine("ERROR unknown TRANSFEREXPORT op: " + op)
			}

		case strings.HasPrefix(line, "CHECKPRESENTEXPORT "):
			key := strings.TrimPrefix(line, "CHECKPRESENTEXPORT ")
			handleCheckPresentExport(key)

		case strings.HasPrefix(line, "REMOVEEXPORT "):
			key := strings.TrimPrefix(line, "REMOVEEXPORT ")
			handleRemoveExport(key)

		case strings.HasPrefix(line, "REMOVEEXPORTDIRECTORYWHENEMPTY "):
			dir := strings.TrimPrefix(line, "REMOVEEXPORTDIRECTORYWHENEMPTY ")
			handleRemoveExportDirectoryWhenEmpty(dir)

		case line == "GETCOST":
			writeLine("COST 250")

		case line == "GETAVAILABILITY":
			writeLine("AVAILABILITY GLOBAL")

		case line == "INFO", line == "GETINFO":
			writeLine("INFO driver: google-drive-v3")
			writeLine("INFO availability: global")
			writeLine("INFOEND")

		case line == "GETGITREMOTENAME":
			writeLine("VALUE " + remoteName)

		default:
			writeLine("UNSUPPORTED-REQUEST")
		}
	}
}
