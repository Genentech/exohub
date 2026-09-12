package auth

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/ini.v1"

	"github.com/Genentech/exohub/go/exo/configdir"
)

// ExohubAWSProfile returns the AWS profile name used by exo.
// Resolution: $EXOHUB_AWS_PROFILE → "exohub".
func ExohubAWSProfile() string {
	if p := os.Getenv("EXOHUB_AWS_PROFILE"); p != "" {
		return p
	}
	return "exohub"
}

// WriteAWSCredentials writes AWSCredentials to the shared AWS credentials file
// under the exohub profile. Both auth providers use this helper
// so the profile format is identical. It does not print any output; callers
// should print confirmation in the appropriate output mode (stdout vs stderr).
func WriteAWSCredentials(creds AWSCredentials) error {
	profile := ExohubAWSProfile()

	credPath, err := configdir.AWSCredentialsFile()
	if err != nil {
		return fmt.Errorf("failed to resolve AWS credentials path: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(credPath), 0700); err != nil {
		return fmt.Errorf("failed to create .aws directory: %w", err)
	}

	cfg, err := ini.LooseLoad(credPath)
	if err != nil {
		return fmt.Errorf("failed to load credentials file: %w", err)
	}

	section := cfg.Section(profile)
	for _, key := range section.Keys() {
		section.DeleteKey(key.Name())
	}

	section.NewKey("aws_access_key_id", creds.AccessKeyID)
	section.NewKey("aws_secret_access_key", creds.SecretAccessKey)
	section.NewKey("aws_session_token", creds.SessionToken)

	if err := cfg.SaveTo(credPath); err != nil {
		return fmt.Errorf("failed to save credentials file: %w", err)
	}

	if err := os.Chmod(credPath, 0600); err != nil {
		return fmt.Errorf("failed to set file permissions: %w", err)
	}

	return nil
}

// WriteAWSCredentialsWithInfo writes credentials and returns the path and profile
// name so the caller can emit a confirmation message in the appropriate mode.
func WriteAWSCredentialsWithInfo(creds AWSCredentials) (credPath, profile string, err error) {
	profile = ExohubAWSProfile()
	credPath, err = configdir.AWSCredentialsFile()
	if err != nil {
		return "", "", fmt.Errorf("failed to resolve AWS credentials path: %w", err)
	}
	if err := WriteAWSCredentials(creds); err != nil {
		return "", "", err
	}
	return credPath, profile, nil
}
