package auth

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// JWTClaims holds the fields decoded from the payload of a raw JWT (no
// signature verification). Shared by `exo doctor` and `exo whoami` so the
// decoding logic lives in exactly one place.
type JWTClaims struct {
	PreferredUsername string `json:"preferred_username"`
	Sub               string `json:"sub"`
	Name              string `json:"name"`
	GivenName         string `json:"given_name"`
	FamilyName        string `json:"family_name"`
	Email             string `json:"email"`
	Iss               string `json:"iss"`
	Aud               any    `json:"aud"` // may be string or []string
	Exp               int64  `json:"exp"`
	Iat               int64  `json:"iat"`
	Department        string `json:"department"`
	CostCenter        string `json:"cost_center"`
	ManagerUsername   string `json:"manager_username"`
	// CognitoGroups is the standard Cognito claim (real []string).
	CognitoGroups []string `json:"cognito:groups"`
	// Groups is decoded as RawMessage so we can handle all three Cognito forms:
	//   1. Real []string:           ["group1","group2"]
	//   2. JSON-encoded string arr: "[\"group1\",\"group2\"]"  (string containing JSON)
	//   3. Plain string:            "group1"
	Groups json.RawMessage `json:"groups"`
}

// DecodeJWTClaims decodes the payload of a raw JWT (no signature verification).
func DecodeJWTClaims(rawJWT string) (*JWTClaims, error) {
	parts := strings.SplitN(rawJWT, ".", 3)
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid JWT format")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("failed to decode JWT payload: %w", err)
	}
	var claims JWTClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JWT claims: %w", err)
	}
	return &claims, nil
}

// Username returns preferred_username, falling back to sub.
func (c *JWTClaims) Username() string {
	if c.PreferredUsername != "" {
		return c.PreferredUsername
	}
	return c.Sub
}

// NormalizeGroups extracts the groups claim from JWT claims, handling all three forms:
//  1. cognito:groups as a real []string
//  2. groups as a real []string
//  3. groups as a JSON-encoded string containing a JSON array: "[\"g1\",\"g2\"]"
//  4. groups as a plain string: "group1"
func NormalizeGroups(claims *JWTClaims) []string {
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

// AudString converts the aud claim (string or []string) to a display string.
func AudString(aud any) string {
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
