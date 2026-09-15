package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
)

// tokenFile holds the parsed contents of token.json.
type tokenFile struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

// fetchProfileCreds loads the named AWS profile and retrieves its current credentials.
// Returns an error if the profile does not exist or the credentials cannot be retrieved.
// Used by getBaseCreds in both the OSS and internal builds.
func fetchProfileCreds(ctx context.Context, profile, region string, duration int, debugf func(string, ...any)) (*credentialOutput, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(region),
		awsconfig.WithSharedConfigProfile(profile),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS profile %q: %w", profile, err)
	}

	creds, err := cfg.Credentials.Retrieve(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve credentials from profile %q: %w", profile, err)
	}

	expiration := ""
	if !creds.Expires.IsZero() {
		expiration = creds.Expires.UTC().Format(time.RFC3339)
	} else {
		expiration = time.Now().Add(time.Duration(duration) * time.Second).UTC().Format(time.RFC3339)
	}

	debugf("base-creds: profile %q credentials retrieved (provider: %s)", profile, creds.Source)
	return &credentialOutput{
		Version:         1,
		AccessKeyID:     creds.AccessKeyID,
		SecretAccessKey: creds.SecretAccessKey,
		SessionToken:    creds.SessionToken,
		Expiration:      expiration,
	}, nil
}

// loadIDToken reads the id_token field from the stored token file.
// Falls back to empty string on any error.
func loadIDToken() string {
	tok, _ := loadTokenFileData()
	return tok.IDToken
}

// loadTokenFileData reads the token file and returns its parsed contents.
// Returns a zero-value tokenFile on any error.
func loadTokenFileData() (tokenFile, string) {
	var raw []byte
	var err error
	var filePath string

	if tf := os.Getenv("EXO_TOKEN_FILE"); tf != "" {
		filePath = tf
		raw, err = os.ReadFile(tf)
	} else if inline := os.Getenv("EXO_TOKEN"); inline != "" {
		raw = []byte(inline)
	} else {
		filePath = tokenFilePath()
		if filePath == "" {
			return tokenFile{}, ""
		}
		raw, err = os.ReadFile(filePath)
	}
	if err != nil || len(raw) == 0 {
		return tokenFile{}, filePath
	}

	var tok tokenFile
	if parseErr := json.Unmarshal(raw, &tok); parseErr != nil {
		return tokenFile{}, filePath
	}
	return tok, filePath
}

// tokenFilePath returns the path to the stored token file.
func tokenFilePath() string {
	if configRoot := os.Getenv("EXO_CONFIG_DIR"); configRoot != "" {
		return filepath.Join(configRoot, "exo", "credentials", "token.json")
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(configDir, "exo", "credentials", "token.json")
}

// idTokenExp parses a JWT id_token (without verification) and returns its exp claim.
// Returns zero time on any parse error.
func idTokenExp(idToken string) time.Time {
	// JWT = header.payload.signature — we only need the payload.
	parts := splitN(idToken, '.', 3)
	if len(parts) < 2 {
		return time.Time{}
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Exp == 0 {
		return time.Time{}
	}
	return time.Unix(claims.Exp, 0)
}

// splitN splits s on sep up to n parts (avoids importing strings just for this).
func splitN(s string, sep byte, n int) []string {
	var out []string
	for len(out) < n-1 {
		i := indexByte(s, sep)
		if i < 0 {
			break
		}
		out = append(out, s[:i])
		s = s[i+1:]
	}
	out = append(out, s)
	return out
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

// persistTokenFile writes updated id_token and refresh_token fields back to filePath,
// preserving all other fields already in the file.
func persistTokenFile(filePath, idToken, refreshToken string) error {
	if filePath == "" {
		return nil
	}

	// Read existing file to preserve all fields.
	raw, _ := os.ReadFile(filePath)
	m := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &m)
	}
	m["id_token"] = idToken
	if refreshToken != "" {
		m["refresh_token"] = refreshToken
	}

	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(filePath, data, 0600)
}

// refreshIDTokenIfNeeded checks whether the id_token in the token file is expired
// or within 5 minutes of expiry. If so and a refresh_token is present, it obtains
// a fresh id_token via the build-specific refresh mechanism (see
// main_basecreds_refresh_janus.go and main_basecreds_refresh.go), persists the
// updated tokens, and returns the fresh id_token. Otherwise it returns the current
// id_token unchanged. On any error the original id_token is returned so callers
// can proceed with whatever they have.
func refreshIDTokenIfNeeded(ctx context.Context, debugf func(string, ...any)) string {
	tok, filePath := loadTokenFileData()
	if tok.IDToken == "" {
		return ""
	}

	exp := idTokenExp(tok.IDToken)
	if exp.IsZero() || time.Until(exp) > 5*time.Minute {
		// Not expired / not expiring soon — use as-is.
		return tok.IDToken
	}

	if tok.RefreshToken == "" {
		debugf("base-creds: id_token expiring but no refresh_token present, using current token")
		return tok.IDToken
	}

	debugf("base-creds: id_token expiring (exp=%s), refreshing via refresh_token", exp.UTC().Format(time.RFC3339))
	newIDToken, newRefreshToken, err := doRefreshIDToken(ctx, tok.RefreshToken, debugf)
	if err != nil {
		debugf("base-creds: id_token refresh failed: %v — proceeding with current token", err)
		return tok.IDToken
	}

	if persistErr := persistTokenFile(filePath, newIDToken, newRefreshToken); persistErr != nil {
		debugf("base-creds: failed to persist refreshed token: %v", persistErr)
	}

	return newIDToken
}
