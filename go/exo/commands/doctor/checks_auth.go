package doctor

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/Genentech/exohub/go/exo/commands/login"
	"github.com/Genentech/exohub/go/exo/configdir"
)

const groupAuth = "Identity & Auth"
const mandatoryGroup = "EXOHUB_USERS"

func runAuthChecks() []CheckResult {
	return []CheckResult{
		checkToken(),
		checkJWTClaims(),
		checkRequiredGroup(),
		checkAWSIdentity(),
	}
}

// jwtClaims holds the fields we decode from the JWT payload.
type jwtClaims struct {
	PreferredUsername string `json:"preferred_username"`
	Sub               string `json:"sub"`
	Iss               string `json:"iss"`
	Aud               any    `json:"aud"` // may be string or []string
	Exp               int64  `json:"exp"`
	Iat               int64  `json:"iat"`
	// CognitoGroups is the standard Cognito claim (real []string).
	CognitoGroups []string `json:"cognito:groups"`
	// Groups is decoded as RawMessage so we can handle all three Cognito forms:
	//   1. Real []string:           ["group1","group2"]
	//   2. JSON-encoded string arr: "[\"group1\",\"group2\"]"  (string containing JSON)
	//   3. Plain string:            "group1"
	Groups json.RawMessage `json:"groups"`
}

// loadDoctorToken is the single canonical way to load the stored token in doctor checks.
// All checks must use this function so token-file resolution is consistent.
// It uses configdir.TokenFile() as the single source of truth for the token path.
func loadDoctorToken() (*oauth2.Token, string, error) {
	tokenFile, err := configdir.TokenFile()
	if err != nil {
		return nil, "", fmt.Errorf("cannot resolve token file: %w", err)
	}
	token, err := login.LoadToken(tokenFile)
	if err != nil {
		return nil, tokenFile, err
	}
	return token, tokenFile, nil
}

func checkToken() CheckResult {
	id := "auth.token"

	token, tokenFile, err := loadDoctorToken()
	if err != nil {
		return fail(id, groupAuth, "No valid token found",
			fmt.Sprintf("Token file: %s — %v", tokenFile, err),
			"Run 'exo login' to authenticate")
	}

	if token.AccessToken == "" {
		return fail(id, groupAuth, "Token file exists but contains no access token",
			fmt.Sprintf("Token file: %s", tokenFile),
			"Run 'exo login' to re-authenticate")
	}

	// Check expiry
	if !token.Expiry.IsZero() && time.Now().After(token.Expiry) {
		// Attempt silent refresh
		if token.RefreshToken != "" {
			newToken, _, err := login.RefreshAuth(token.RefreshToken)
			if err == nil && newToken != nil {
				ttl := time.Until(newToken.Expiry).Round(time.Second)
				return pass(id, groupAuth, fmt.Sprintf("Token refreshed successfully (expires in %s)", ttl))
			}
			return fail(id, groupAuth, "Token expired and silent refresh failed",
				fmt.Sprintf("Refresh error: %v", err),
				"Run 'exo login' to re-authenticate")
		}
		return fail(id, groupAuth, "Token expired and no refresh token available",
			fmt.Sprintf("Expired at: %s", token.Expiry.Format(time.RFC3339)),
			"Run 'exo login' to re-authenticate")
	}

	ttl := "unknown"
	if !token.Expiry.IsZero() {
		ttl = time.Until(token.Expiry).Round(time.Second).String()
	}
	return pass(id, groupAuth, fmt.Sprintf("Token valid (expires in %s)", ttl))
}

func checkJWTClaims() CheckResult {
	id := "auth.jwt-claims"

	token, _, err := loadDoctorToken()
	if err != nil || token == nil || token.AccessToken == "" {
		return skip(id, groupAuth, "JWT claims check skipped (no token)")
	}

	claims, err := decodeJWTClaims(token.AccessToken)
	if err != nil {
		return warn(id, groupAuth, "Could not decode JWT claims",
			Redact(err.Error()),
			"Run 'exo login' to get a fresh token")
	}

	groups := normalizeGroups(claims)
	audStr := audString(claims.Aud)
	expTime := time.Unix(claims.Exp, 0)

	summary := fmt.Sprintf("user=%s iss=%s groups=%d exp=%s",
		claims.PreferredUsername,
		Redact(claims.Iss),
		len(groups),
		expTime.Format(time.RFC3339))

	detail := fmt.Sprintf("preferred_username=%q iss=%q aud=%q groups=%v exp=%s",
		claims.PreferredUsername,
		Redact(claims.Iss),
		Redact(audStr),
		groups,
		expTime.Format(time.RFC3339))

	if claims.PreferredUsername == "" {
		return warn(id, groupAuth, "JWT missing preferred_username claim",
			detail,
			"Run 'exo login' to get a fresh token")
	}

	return CheckResult{
		ID:      id,
		Group:   groupAuth,
		Status:  StatusPass,
		Summary: summary,
		Detail:  detail,
	}
}

// checkRequiredGroup verifies the token carries the mandatory EXOHUB_USERS group.
// Membership is an exact match — look-alikes such as EXOHUB_USERS_OLD do not count.
func checkRequiredGroup() CheckResult {
	id := "auth.required-group"

	token, _, err := loadDoctorToken()
	if err != nil || token == nil || token.AccessToken == "" {
		return skip(id, groupAuth, "Group membership check skipped (no token)")
	}

	claims, err := decodeJWTClaims(token.AccessToken)
	if err != nil {
		return warn(id, groupAuth, "Could not decode JWT claims for group check",
			Redact(err.Error()),
			"Run 'exo login' to get a fresh token")
	}

	groups := normalizeGroups(claims)
	for _, g := range groups {
		if g == mandatoryGroup {
			return pass(id, groupAuth,
				fmt.Sprintf("Member of mandatory group %q", mandatoryGroup))
		}
	}

	return fail(id, groupAuth,
		fmt.Sprintf("Not a member of mandatory group %q", mandatoryGroup),
		fmt.Sprintf("Token groups: %v", groups),
		fmt.Sprintf("Subscribe to the %s CIDM group, then run 'exo login' to refresh your token", mandatoryGroup))
}

func checkAWSIdentity() CheckResult {
	id := "auth.aws-identity"

	profile := os.Getenv("EXOHUB_AWS_PROFILE")
	if profile == "" {
		profile = "exohub"
	}
	// STS is a global service; us-east-1 is the standard region for GetCallerIdentity.
	// We also respect EXOHUB_GRANTS_REGION for consistency.
	region := os.Getenv("EXOHUB_GRANTS_REGION")
	if region == "" {
		region = "us-east-1"
	}

	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithSharedConfigProfile(profile),
		config.WithRegion(region),
	)
	if err != nil {
		return fail(id, groupAuth,
			fmt.Sprintf("AWS profile %q does not resolve", profile),
			err.Error(),
			"Run 'exo login' to refresh AWS credentials")
	}

	stsClient := sts.NewFromConfig(cfg)
	identity, err := stsClient.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return fail(id, groupAuth,
			fmt.Sprintf("sts:GetCallerIdentity failed for profile %q", profile),
			Redact(err.Error()),
			"Run 'exo login' to refresh AWS credentials, or check VPN/network")
	}

	arn := ""
	if identity.Arn != nil {
		arn = *identity.Arn
	}
	account := ""
	if identity.Account != nil {
		account = *identity.Account
	}

	// Try to extract username from JWT and compare to ARN
	token, _, _ := loadDoctorToken()
	jwtUser := ""
	if token != nil && token.AccessToken != "" {
		if claims, err := decodeJWTClaims(token.AccessToken); err == nil {
			jwtUser = claims.PreferredUsername
		}
	}

	if jwtUser != "" && !strings.Contains(arn, jwtUser) {
		return warn(id, groupAuth,
			fmt.Sprintf("AWS identity ARN does not contain JWT username %q", jwtUser),
			fmt.Sprintf("ARN=%s account=%s", Redact(arn), account),
			"Run 'exo login' to sync credentials")
	}

	return pass(id, groupAuth,
		fmt.Sprintf("AWS identity OK (account=%s, principal=...%s)", account, lastN(Redact(arn), 30)))
}

// decodeJWTClaims decodes the payload of a raw JWT (no signature verification).
func decodeJWTClaims(rawJWT string) (*jwtClaims, error) {
	parts := strings.SplitN(rawJWT, ".", 3)
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid JWT format")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("failed to decode JWT payload: %w", err)
	}
	var claims jwtClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JWT claims: %w", err)
	}
	return &claims, nil
}

// decodeBase64URL decodes a base64 URL-encoded string (with or without padding).
func decodeBase64URL(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}

// normalizeGroups extracts the groups claim from JWT claims, handling all three forms:
//  1. cognito:groups as a real []string
//  2. groups as a real []string
//  3. groups as a JSON-encoded string containing a JSON array: "[\"g1\",\"g2\"]"
//  4. groups as a plain string: "group1"
func normalizeGroups(claims *jwtClaims) []string {
	// Form 1: standard Cognito cognito:groups claim (real []string)
	if len(claims.CognitoGroups) > 0 {
		return claims.CognitoGroups
	}

	if len(claims.Groups) == 0 {
		return nil
	}

	// Form 2: real []string — try to unmarshal as []string first
	var groupSlice []string
	if err := json.Unmarshal(claims.Groups, &groupSlice); err == nil {
		return groupSlice
	}

	// Form 3 & 4: it's a JSON string — unwrap the string value
	var groupStr string
	if err := json.Unmarshal(claims.Groups, &groupStr); err != nil {
		return nil
	}

	// Form 3: the string itself is a JSON array
	if err := json.Unmarshal([]byte(groupStr), &groupSlice); err == nil && len(groupSlice) > 0 {
		return groupSlice
	}

	// Form 4: plain string (single group)
	if groupStr != "" {
		return []string{groupStr}
	}

	return nil
}

// audString converts the aud claim (string or []string) to a display string.
func audString(aud any) string {
	switch v := aud.(type) {
	case string:
		return v
	case []interface{}:
		var parts []string
		for _, a := range v {
			if s, ok := a.(string); ok {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, ", ")
	default:
		return fmt.Sprintf("%v", aud)
	}
}

// lastN returns the last n chars of s (or all of s if shorter).
func lastN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n:]
}
