package commandutil

import (
	"testing"
)

func TestIsCredentialError(t *testing.T) {
	tests := []struct {
		name    string
		errMsg  string
		want    bool
	}{
		{
			name:   "ExpiredToken error",
			errMsg: `ERROR "cp file s3://bucket/key": ExpiredToken: The provided token has expired. status code: 400, request id: V3TDTASA40PXXRF3`,
			want:   true,
		},
		{
			name:   "Invalid credentials",
			errMsg: "ERROR: invalid credentials provided",
			want:   true,
		},
		{
			name:   "InvalidAccessKeyId",
			errMsg: "InvalidAccessKeyId: The AWS Access Key Id you provided does not exist",
			want:   true,
		},
		{
			name:   "SignatureDoesNotMatch",
			errMsg: "SignatureDoesNotMatch: The request signature we calculated does not match",
			want:   true,
		},
		{
			name:   "Status code 401",
			errMsg: "ERROR: Request failed with status code: 401",
			want:   true,
		},
		{
			name:   "Status code 403",
			errMsg: "Access denied. status code: 403",
			want:   true,
		},
		{
			name:   "Generic error",
			errMsg: "ERROR: Connection timeout",
			want:   false,
		},
		{
			name:   "File not found",
			errMsg: "ERROR: No such file or directory",
			want:   false,
		},
		{
			name:   "Empty string",
			errMsg: "",
			want:   false,
		},
		{
			name:   "Case insensitive - EXPIREDTOKEN",
			errMsg: "EXPIREDTOKEN: token has expired",
			want:   true,
		},
		{
			name:   "Case insensitive - expired token",
			errMsg: "the token has expired and is no longer valid",
			want:   true,
		},
		{
			name:   "Security token invalid",
			errMsg: "ERROR: The security token included in the request is invalid",
			want:   true,
		},
		{
			name:   "Credentials have expired",
			errMsg: "Your credentials have expired, please refresh",
			want:   true,
		},
		{
			name:   "Not authorized",
			errMsg: "User not authorized to perform this operation",
			want:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsCredentialError(tt.errMsg); got != tt.want {
				t.Errorf("IsCredentialError() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsCredentialError_RealWorldExamples(t *testing.T) {
	// Real error from the user's report
	realError := `export s5-export-studies ef/UKB_23288_EUR_F.parquet
  ERROR "cp .git/annex/objects/Jp/5p/SHA256E-s624062739--ce4fb2deb0432738c41bf6a052253ad4da2fc5336e27bc1fb1a31b2f23f4af4b/SHA256E-s624062739--ce4fb2deb0432738c41bf6a052253ad4da2fc5336e27bc1fb1a31b2f23f4af4b s3://aw-huge-usw2-prd-63b5/gwasdb/_export/main:gwasdb-studies/ef/UKB_23288_EUR_F.parquet": ExpiredToken: The provided token has expired. status code: 400, request id: V3TDTASA40PXXRF3, host id: 14/BDEmGDqsshq2SLXT1KA905MOMm9pUdxRMI9FozLG9sMhA6AlJWJ8P/gH7bSOsKrCYmrT57wg=`

	if !IsCredentialError(realError) {
		t.Errorf("IsCredentialError() should detect real-world ExpiredToken error")
	}
}
