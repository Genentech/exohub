package commandutil

import "testing"

// isolateAWSIdentityEnv clears every variable InjectAWSProfileEnv treats as
// a caller-supplied AWS identity (it then deliberately does NOT inject
// AWS_PROFILE=exohub), so the AWS-profile tests don't depend on the
// developer's or runner's environment (e.g. an exported AWS_PROFILE).
func isolateAWSIdentityEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"EXOHUB_AWS_PROFILE",
		"EXO_NO_AWS_PROFILE_INJECT",
		"AWS_PROFILE",
		"AWS_ACCESS_KEY_ID",
		"AWS_SECRET_ACCESS_KEY",
		"AWS_SESSION_TOKEN",
	} {
		t.Setenv(k, "")
	}
}
