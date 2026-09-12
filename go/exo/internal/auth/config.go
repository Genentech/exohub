package auth

import (
	"fmt"
	"os"
	"strconv"
)

// LoadConfig builds a Config from the environment.
// Precedence: env > built-in defaults.
// Required fields (for the generic/OIDC build) are not validated here;
// the provider constructor calls Validate() when it needs them.
func LoadConfig() Config {
	cfg := Config{}

	cfg.OIDC.Issuer = resolveEnv("EXO_OIDC_ISSUER", "")
	cfg.OIDC.ClientID = resolveEnv("EXO_OIDC_CLIENT_ID", "")
	cfg.OIDC.Audience = resolveEnv("EXO_OIDC_AUDIENCE", "")
	cfg.OIDC.Scopes = defaultScopes(splitScopes(resolveEnv("EXO_OIDC_SCOPES", "")))

	cfg.AWS.RoleARN = resolveEnv("EXO_AWS_ROLE_ARN", "")
	cfg.AWS.Region = resolveEnv("EXO_AWS_REGION", "us-east-1")
	cfg.AWS.SessionDuration = resolveEnvInt("EXO_AWS_SESSION_DURATION", 3600)

	return cfg
}

// Validate checks that all required OIDC + AWS fields are present and returns
// actionable errors if any are missing. Called by the OIDC provider (the
// janus-tag build uses its own constants and does not call this).
func (c Config) Validate() error {
	var missing []string
	if c.OIDC.Issuer == "" {
		missing = append(missing, "EXO_OIDC_ISSUER (OIDC issuer/discovery URL)")
	}
	if c.OIDC.ClientID == "" {
		missing = append(missing, "EXO_OIDC_CLIENT_ID (OIDC client ID)")
	}
	if c.AWS.RoleARN == "" {
		missing = append(missing, "EXO_AWS_ROLE_ARN (IAM role ARN for STS AssumeRoleWithWebIdentity)")
	}
	if len(missing) > 0 {
		msg := "missing required auth configuration; set the following environment variables:\n"
		for _, m := range missing {
			msg += "  " + m + "\n"
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}

func resolveEnv(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

func resolveEnvInt(key string, defaultVal int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return defaultVal
}
