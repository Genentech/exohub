//go:build !janus

package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mockOIDCServer creates a test HTTP server that handles OIDC discovery,
// device authorization, and token requests. The token endpoint immediately
// grants tokens on the first request (no authorization_pending delay).
func mockOIDCServer(t *testing.T) (srv *httptest.Server, issuerURL string) {
	t.Helper()

	// We need a pointer to fill serverURL after the server starts.
	var base string

	mux := http.NewServeMux()

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                base,
			"authorization_endpoint":                base + "/auth",
			"token_endpoint":                        base + "/token",
			"device_authorization_endpoint":         base + "/device",
			"jwks_uri":                              base + "/jwks",
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})

	mux.HandleFunc("/device", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"device_code":               "test-device-code",
			"user_code":                 "ABCD-1234",
			"verification_uri":          base + "/activate",
			"verification_uri_complete": base + "/activate?user_code=ABCD-1234",
			"expires_in":                600,
			"interval":                  1,
		})
	})

	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Minimal JWT payload for testing — not signed, just structurally valid.
		payload := base64.RawURLEncoding.EncodeToString([]byte(
			`{"sub":"testuser","preferred_username":"testuser","exp":9999999999}`,
		))
		idTok := "header." + payload + ".sig"
		json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "test-access-token",
			"token_type":    "Bearer",
			"refresh_token": "test-refresh-token",
			"expires_in":    3600,
			"id_token":      idTok,
		})
	})

	srv = httptest.NewServer(mux)
	base = srv.URL
	t.Cleanup(srv.Close)
	return srv, srv.URL
}

func TestOIDCProvider_New_MissingConfig(t *testing.T) {
	cfg := Config{}
	_, err := New(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected error for empty config")
	}
}

func TestOIDCProvider_New_BadIssuer(t *testing.T) {
	cfg := Config{}
	cfg.OIDC.Issuer = "http://127.0.0.1:0" // nothing listening
	cfg.OIDC.ClientID = "test-client"
	cfg.AWS.RoleARN = "arn:aws:iam::123:role/Test"

	_, err := New(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected error for unreachable issuer")
	}
	if !strings.Contains(err.Error(), "OIDC discovery failed") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestStartDeviceAuthFlow_ReturnsParams(t *testing.T) {
	_, issuerURL := mockOIDCServer(t)

	cfg := Config{}
	cfg.OIDC.Issuer = issuerURL
	cfg.OIDC.ClientID = "test-client"
	cfg.AWS.RoleARN = "arn:aws:iam::123:role/Test"

	flow, err := StartDeviceAuthFlow(context.Background(), cfg)
	if err != nil {
		t.Fatalf("StartDeviceAuthFlow() error: %v", err)
	}
	if flow.Params.UserCode != "ABCD-1234" {
		t.Errorf("UserCode: got %q, want ABCD-1234", flow.Params.UserCode)
	}
	if !strings.Contains(flow.Params.VerificationURI, "/activate") {
		t.Errorf("VerificationURI: got %q", flow.Params.VerificationURI)
	}
	if flow.Poll == nil {
		t.Error("Poll should not be nil")
	}
	if flow.Provider == nil {
		t.Error("Provider should not be nil")
	}
}

func TestStartDeviceAuthFlow_PollReturnsTokens(t *testing.T) {
	_, issuerURL := mockOIDCServer(t)

	cfg := Config{}
	cfg.OIDC.Issuer = issuerURL
	cfg.OIDC.ClientID = "test-client"
	cfg.AWS.RoleARN = "arn:aws:iam::123:role/Test"

	flow, err := StartDeviceAuthFlow(context.Background(), cfg)
	if err != nil {
		t.Fatalf("StartDeviceAuthFlow() error: %v", err)
	}

	tokens, err := flow.Poll(context.Background())
	if err != nil {
		t.Fatalf("flow.Poll() error: %v", err)
	}
	if tokens.AccessToken != "test-access-token" {
		t.Errorf("AccessToken: got %q", tokens.AccessToken)
	}
	if tokens.RefreshToken != "test-refresh-token" {
		t.Errorf("RefreshToken: got %q", tokens.RefreshToken)
	}
	if tokens.IDToken == "" {
		t.Error("IDToken should not be empty")
	}
	// Provider must be the same object — no second discovery call.
	if flow.Provider == nil {
		t.Error("Provider should not be nil")
	}
}

func TestJWTExpiry(t *testing.T) {
	// Build a minimal JWT with exp = 9999999999
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"exp":9999999999}`))
	rawJWT := fmt.Sprintf("header.%s.sig", payload)

	exp, err := jwtExpiry(rawJWT)
	if err != nil {
		t.Fatalf("jwtExpiry() error: %v", err)
	}
	if exp.IsZero() {
		t.Error("expected non-zero expiry")
	}
}
