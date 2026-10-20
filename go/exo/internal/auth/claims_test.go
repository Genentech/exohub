package auth

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func encodeBase64URL(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}

func makeJWT(t *testing.T, payload map[string]any) string {
	t.Helper()
	pb, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	header := "eyJhbGciOiJub25lIn0" // {"alg":"none"}
	return header + "." + encodeBase64URL(pb) + ".sig"
}

func TestDecodeJWTClaims_Table(t *testing.T) {
	cases := []struct {
		name    string
		payload map[string]any
		wantErr bool
		check   func(t *testing.T, c *JWTClaims)
	}{
		{
			name: "all fields",
			payload: map[string]any{
				"preferred_username": "jdoe",
				"sub":                "sub-123",
				"name":               "Jane Doe",
				"email":              "jdoe@example.com",
				"iss":                "https://idp.example.com",
				"aud":                "client-id",
				"exp":                int64(9999999999),
				"department":         "Engineering",
				"cost_center":        "CC-100",
				"manager_username":   "mgr",
				"cognito:groups":     []string{"group1", "group2"},
			},
			check: func(t *testing.T, c *JWTClaims) {
				if c.Username() != "jdoe" {
					t.Errorf("Username() = %q, want jdoe", c.Username())
				}
				if c.Name != "Jane Doe" || c.Email != "jdoe@example.com" {
					t.Errorf("unexpected name/email: %+v", c)
				}
				if c.Department != "Engineering" || c.CostCenter != "CC-100" || c.ManagerUsername != "mgr" {
					t.Errorf("unexpected org claims: %+v", c)
				}
				groups := NormalizeGroups(c)
				if len(groups) != 2 || groups[0] != "group1" {
					t.Errorf("unexpected groups: %v", groups)
				}
			},
		},
		{
			name: "missing optional claims",
			payload: map[string]any{
				"preferred_username": "jdoe",
				"iss":                "https://idp.example.com",
				"exp":                int64(9999999999),
			},
			check: func(t *testing.T, c *JWTClaims) {
				if c.Name != "" || c.Email != "" || c.Department != "" {
					t.Errorf("expected empty optional claims, got %+v", c)
				}
				if groups := NormalizeGroups(c); len(groups) != 0 {
					t.Errorf("expected no groups, got %v", groups)
				}
			},
		},
		{
			name: "expired token",
			payload: map[string]any{
				"preferred_username": "jdoe",
				"exp":                int64(1),
			},
			check: func(t *testing.T, c *JWTClaims) {
				if c.Exp != 1 {
					t.Errorf("Exp = %d, want 1", c.Exp)
				}
			},
		},
		{
			name: "sub fallback",
			payload: map[string]any{
				"sub": "sub-only",
			},
			check: func(t *testing.T, c *JWTClaims) {
				if c.Username() != "sub-only" {
					t.Errorf("Username() = %q, want sub-only", c.Username())
				}
			},
		},
		{
			name: "groups as JSON array",
			payload: map[string]any{
				"groups": []string{"EXOHUB_USERS", "EXOHUB_ADMINS"},
			},
			check: func(t *testing.T, c *JWTClaims) {
				groups := NormalizeGroups(c)
				if len(groups) != 2 || groups[0] != "EXOHUB_USERS" {
					t.Errorf("unexpected groups: %v", groups)
				}
			},
		},
		{
			name: "groups as quoted JSON-encoded string array",
			payload: map[string]any{
				"groups": `["EXOHUB_USERS","EXOHUB_ADMINS"]`,
			},
			check: func(t *testing.T, c *JWTClaims) {
				groups := NormalizeGroups(c)
				if len(groups) != 2 || groups[0] != "EXOHUB_USERS" {
					t.Errorf("unexpected groups: %v", groups)
				}
			},
		},
		{
			name: "groups as plain quoted string",
			payload: map[string]any{
				"groups": "EXOHUB_USERS",
			},
			check: func(t *testing.T, c *JWTClaims) {
				groups := NormalizeGroups(c)
				if len(groups) != 1 || groups[0] != "EXOHUB_USERS" {
					t.Errorf("unexpected groups: %v", groups)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rawJWT := makeJWT(t, tc.payload)
			claims, err := DecodeJWTClaims(rawJWT)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("DecodeJWTClaims error: %v", err)
			}
			if tc.check != nil {
				tc.check(t, claims)
			}
		})
	}
}

func TestDecodeJWTClaims_InvalidFormat(t *testing.T) {
	if _, err := DecodeJWTClaims("not-a-jwt"); err == nil {
		t.Fatal("expected error for malformed JWT")
	}
}

func TestAudString(t *testing.T) {
	cases := []struct {
		aud  any
		want string
	}{
		{"single-aud", "single-aud"},
		{[]interface{}{"aud1", "aud2"}, "aud1, aud2"},
	}
	for _, tc := range cases {
		if got := AudString(tc.aud); got != tc.want {
			t.Errorf("AudString(%v) = %q, want %q", tc.aud, got, tc.want)
		}
	}
}

func TestUsernameFromToken(t *testing.T) {
	rawJWT := makeJWT(t, map[string]any{"preferred_username": "jdoe", "sub": "sub-123"})
	if got := UsernameFromToken(rawJWT); got != "jdoe" {
		t.Errorf("UsernameFromToken() = %q, want jdoe", got)
	}

	rawJWT = makeJWT(t, map[string]any{"sub": "sub-only"})
	if got := UsernameFromToken(rawJWT); got != "sub-only" {
		t.Errorf("UsernameFromToken() = %q, want sub-only", got)
	}

	if got := UsernameFromToken("not-a-jwt"); got != "" {
		t.Errorf("UsernameFromToken() = %q, want empty for invalid token", got)
	}
}
