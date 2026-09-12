package credhelper

import (
	"errors"
	"testing"
)

func TestIsAccessDeniedError(t *testing.T) {
	cases := []struct {
		msg  string
		want bool
	}{
		{"GetDataAccess failed: AccessDenied: ...", true},
		{"StatusCode: 403", true},
		{"access denied", true},
		{"some other error", false},
		{"NoCredentialProviders", false},
	}
	for _, tc := range cases {
		got := IsAccessDeniedError(errors.New(tc.msg))
		if got != tc.want {
			t.Errorf("IsAccessDeniedError(%q) = %v, want %v", tc.msg, got, tc.want)
		}
	}
}

func TestIsMissingCredentialsError(t *testing.T) {
	cases := []struct {
		msg  string
		want bool
	}{
		{"failed to get shared config profile, exohub", true},
		{"SharedConfigProfileNotExistError", true},
		{"NoCredentialProviders", true},
		{"AccessDenied", false},
		{"some random error", false},
	}
	for _, tc := range cases {
		got := IsMissingCredentialsError(errors.New(tc.msg))
		if got != tc.want {
			t.Errorf("IsMissingCredentialsError(%q) = %v, want %v", tc.msg, got, tc.want)
		}
	}
}

func TestResolveEnv(t *testing.T) {
	t.Setenv("EXOHUB_GRANTS_ACCOUNT_ID", "123456789")
	got := resolveEnv("EXOHUB_GRANTS_ACCOUNT_ID", DefaultAccountID)
	if got != "123456789" {
		t.Errorf("resolveEnv = %q, want 123456789", got)
	}
	got = resolveEnv("EXOHUB_GRANTS_ACCOUNT_ID_MISSING", DefaultAccountID)
	if got != DefaultAccountID {
		t.Errorf("resolveEnv fallback = %q, want %q", got, DefaultAccountID)
	}
}
