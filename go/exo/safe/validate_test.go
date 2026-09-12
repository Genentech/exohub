package safe

import (
	"strings"
	"testing"
)

func TestValidateSSHPrivateKey(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid openssh private key",
			content: "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAAA\n-----END OPENSSH PRIVATE KEY-----\n",
			wantErr: false,
		},
		{
			name:    "valid RSA private key",
			content: "-----BEGIN RSA PRIVATE KEY-----\nMIIEpAIBAAKCAQEA\n-----END RSA PRIVATE KEY-----\n",
			wantErr: false,
		},
		{
			name:    "valid EC private key",
			content: "-----BEGIN EC PRIVATE KEY-----\nMHQCAQEEI...\n-----END EC PRIVATE KEY-----\n",
			wantErr: false,
		},
		{
			name:    "valid PKCS8 private key",
			content: "-----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgkq\n-----END PRIVATE KEY-----\n",
			wantErr: false,
		},
		{
			name:    "public key rejected",
			content: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI... user@host",
			wantErr: true,
			errMsg:  "public key",
		},
		{
			name:    "PEM public key rejected",
			content: "-----BEGIN PUBLIC KEY-----\nMIIBIjANBgkq\n-----END PUBLIC KEY-----\n",
			wantErr: true,
			errMsg:  "public key",
		},
		{
			name:    "unknown PEM block",
			content: "-----BEGIN CERTIFICATE-----\nMIIDXTCCAkW\n-----END CERTIFICATE-----\n",
			wantErr: true,
			errMsg:  "does not appear to be an SSH private key",
		},
		{
			name:    "plain text rejected",
			content: "ghp_1234567890abcdef",
			wantErr: true,
			errMsg:  "does not contain an SSH private key",
		},
		{
			name:    "empty rejected",
			content: "",
			wantErr: true,
			errMsg:  "does not contain an SSH private key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSSHPrivateKey([]byte(tt.content))
			if tt.wantErr && err == nil {
				t.Error("expected error but got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if tt.wantErr && err != nil && tt.errMsg != "" {
				if !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("error %q should contain %q", err.Error(), tt.errMsg)
				}
			}
		})
	}
}
