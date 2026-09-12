package auth

import (
	"strings"
	"testing"
)

func TestLoadConfig_Defaults(t *testing.T) {
	// Clear all relevant env vars.
	for _, k := range []string{
		"EXO_OIDC_ISSUER", "EXO_OIDC_CLIENT_ID", "EXO_OIDC_SCOPES",
		"EXO_OIDC_AUDIENCE", "EXO_AWS_ROLE_ARN", "EXO_AWS_REGION",
		"EXO_AWS_SESSION_DURATION",
	} {
		t.Setenv(k, "")
	}

	cfg := LoadConfig()
	if cfg.AWS.SessionDuration != 3600 {
		t.Errorf("default SessionDuration: got %d, want 3600", cfg.AWS.SessionDuration)
	}
	if cfg.AWS.Region != "us-east-1" {
		t.Errorf("default Region: got %q, want us-east-1", cfg.AWS.Region)
	}
	if len(cfg.OIDC.Scopes) == 0 {
		t.Error("default Scopes should not be empty")
	}
}

func TestLoadConfig_EnvOverrides(t *testing.T) {
	t.Setenv("EXO_OIDC_ISSUER", "https://example.com")
	t.Setenv("EXO_OIDC_CLIENT_ID", "my-client")
	t.Setenv("EXO_OIDC_SCOPES", "openid profile email")
	t.Setenv("EXO_OIDC_AUDIENCE", "api.example.com")
	t.Setenv("EXO_AWS_ROLE_ARN", "arn:aws:iam::123456789012:role/MyRole")
	t.Setenv("EXO_AWS_REGION", "eu-west-1")
	t.Setenv("EXO_AWS_SESSION_DURATION", "7200")

	cfg := LoadConfig()
	if cfg.OIDC.Issuer != "https://example.com" {
		t.Errorf("OIDC.Issuer: got %q", cfg.OIDC.Issuer)
	}
	if cfg.OIDC.ClientID != "my-client" {
		t.Errorf("OIDC.ClientID: got %q", cfg.OIDC.ClientID)
	}
	if len(cfg.OIDC.Scopes) != 3 {
		t.Errorf("OIDC.Scopes: got %v", cfg.OIDC.Scopes)
	}
	if cfg.OIDC.Audience != "api.example.com" {
		t.Errorf("OIDC.Audience: got %q", cfg.OIDC.Audience)
	}
	if cfg.AWS.RoleARN != "arn:aws:iam::123456789012:role/MyRole" {
		t.Errorf("AWS.RoleARN: got %q", cfg.AWS.RoleARN)
	}
	if cfg.AWS.Region != "eu-west-1" {
		t.Errorf("AWS.Region: got %q", cfg.AWS.Region)
	}
	if cfg.AWS.SessionDuration != 7200 {
		t.Errorf("AWS.SessionDuration: got %d", cfg.AWS.SessionDuration)
	}
}

func TestConfig_Validate_MissingAll(t *testing.T) {
	cfg := Config{}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for empty config")
	}
	msg := err.Error()
	for _, want := range []string{"EXO_OIDC_ISSUER", "EXO_OIDC_CLIENT_ID", "EXO_AWS_ROLE_ARN"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error should mention %q; got: %s", want, msg)
		}
	}
}

func TestConfig_Validate_MissingPartial(t *testing.T) {
	cfg := Config{}
	cfg.OIDC.Issuer = "https://example.com"
	cfg.OIDC.ClientID = "my-client"
	// RoleARN missing

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error when RoleARN missing")
	}
	if !strings.Contains(err.Error(), "EXO_AWS_ROLE_ARN") {
		t.Errorf("error should mention EXO_AWS_ROLE_ARN; got: %s", err.Error())
	}
}

func TestConfig_Validate_AllPresent(t *testing.T) {
	cfg := Config{}
	cfg.OIDC.Issuer = "https://example.com"
	cfg.OIDC.ClientID = "my-client"
	cfg.AWS.RoleARN = "arn:aws:iam::123:role/R"

	if err := cfg.Validate(); err != nil {
		t.Errorf("unexpected validation error: %v", err)
	}
}

func TestResolveEnvInt_InvalidString(t *testing.T) {
	t.Setenv("EXO_AWS_SESSION_DURATION", "not-a-number")
	got := resolveEnvInt("EXO_AWS_SESSION_DURATION", 3600)
	if got != 3600 {
		t.Errorf("resolveEnvInt with non-numeric value: got %d, want 3600 (default)", got)
	}
}

func TestResolveEnvInt_NegativePassThrough(t *testing.T) {
	// Negative values are passed through (parse succeeds); callers handle validation.
	t.Setenv("EXO_AWS_SESSION_DURATION", "-1")
	got := resolveEnvInt("EXO_AWS_SESSION_DURATION", 3600)
	if got != -1 {
		t.Errorf("resolveEnvInt with negative value: got %d, want -1", got)
	}
}
