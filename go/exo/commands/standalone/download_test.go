package standalone

import (
	"fmt"
	"strings"
	"testing"
)

// TestAssetNames pins the exact GitHub release asset filenames for each stack
// binary. These names are dictated by the upstream projects' release tooling
// and differ subtly between them:
//
//   - versitygw: KEEPS the leading "v" -> versitygw_v1.8.0_Darwin_arm64.tar.gz
//   - surreal:   KEEPS the leading "v" -> surreal-v3.2.4.darwin-arm64.tgz
//   - adb-standalone: NO leading "v"   -> adb-standalone_0.117.0_darwin_arm64.tar.gz
//     (goreleaser's {{ .Version }} strips it)
//
// A regression here produces a confusing HTTP 404 at `exo standalone up` time
// on a user's machine, so assert the strings exactly.
func TestAssetNames(t *testing.T) {
	tests := []struct {
		binary  string
		version string
		goos    string
		goarch  string
		want    string
	}{
		// versitygw — the "v" must be retained (regression: it was stripped,
		// yielding versitygw_1.8.0_Darwin_arm64.tar.gz -> 404 on macOS).
		{"versitygw", "v1.8.0", "darwin", "arm64", "versitygw_v1.8.0_Darwin_arm64.tar.gz"},
		{"versitygw", "v1.8.0", "darwin", "amd64", "versitygw_v1.8.0_Darwin_x86_64.tar.gz"},
		{"versitygw", "v1.8.0", "linux", "amd64", "versitygw_v1.8.0_Linux_x86_64.tar.gz"},
		{"versitygw", "v1.8.0", "linux", "arm64", "versitygw_v1.8.0_Linux_arm64.tar.gz"},

		// surreal
		{"surreal", "v3.2.4", "darwin", "arm64", "surreal-v3.2.4.darwin-arm64.tgz"},
		{"surreal", "v3.2.4", "darwin", "amd64", "surreal-v3.2.4.darwin-amd64.tgz"},
		{"surreal", "v3.2.4", "linux", "amd64", "surreal-v3.2.4.linux-amd64.tgz"},

		// adb-standalone — version arrives without a leading "v".
		{"adb-standalone", "0.117.0", "darwin", "arm64", "adb-standalone_0.117.0_darwin_arm64.tar.gz"},
		{"adb-standalone", "0.117.0", "linux", "amd64", "adb-standalone_0.117.0_linux_amd64.tar.gz"},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s_%s_%s", tt.binary, tt.goos, tt.goarch), func(t *testing.T) {
			spec, ok := binarySpecs[tt.binary]
			if !ok {
				t.Fatalf("no binarySpec for %q", tt.binary)
			}
			got, err := spec.assetName(tt.version, tt.goos, tt.goarch)
			if err != nil {
				t.Fatalf("assetName(%q, %q, %q): unexpected error: %v", tt.version, tt.goos, tt.goarch, err)
			}
			if got != tt.want {
				t.Errorf("assetName(%q, %q, %q)\n got: %s\nwant: %s", tt.version, tt.goos, tt.goarch, got, tt.want)
			}
		})
	}
}

// TestAssetNameUnsupportedPlatform ensures we fail with a helpful message
// rather than composing a bogus URL for platforms with no published asset.
func TestAssetNameUnsupportedPlatform(t *testing.T) {
	for _, binary := range []string{"versitygw", "surreal", "adb-standalone"} {
		t.Run(binary, func(t *testing.T) {
			spec := binarySpecs[binary]
			if _, err := spec.assetName("v1.0.0", "windows", "arm64"); err == nil {
				t.Fatalf("%s: expected error for windows/arm64, got nil", binary)
			}
		})
	}
}

// TestPinnedVersions guards the pinned upstream versions. versitygw must stay
// on a release whose assets follow the "v"-prefixed naming asserted above.
func TestPinnedVersions(t *testing.T) {
	if versitygwVersion != "v1.8.0" {
		t.Errorf("versitygwVersion = %s; update TestAssetNames if this is an intentional bump", versitygwVersion)
	}
	for name, ver := range map[string]string{"versitygw": versitygwVersion, "surreal": surrealVersion} {
		if !strings.HasPrefix(ver, "v") {
			t.Errorf("%s version %q should carry a leading \"v\"", name, ver)
		}
	}
}

// TestChecksumAssetName pins the adb-standalone checksums filename, which must
// track the asset naming (no leading "v").
func TestChecksumAssetName(t *testing.T) {
	got := binarySpecs["adb-standalone"].checksumAsset("0.117.0")
	want := "adb-standalone_0.117.0_checksums.txt"
	if got != want {
		t.Errorf("checksumAsset\n got: %s\nwant: %s", got, want)
	}
}

// TestReleaseURLShape guards the tag segment of the download URL: every repo
// here tags releases as "v<semver>", regardless of whether the asset filename
// carries the "v".
func TestReleaseURLShape(t *testing.T) {
	tests := []struct {
		binary  string
		version string
		wantTag string
	}{
		{"versitygw", "v1.8.0", "/releases/download/v1.8.0/"},
		{"surreal", "v3.2.4", "/releases/download/v3.2.4/"},
		{"adb-standalone", "0.117.0", "/releases/download/v0.117.0/"},
	}

	for _, tt := range tests {
		t.Run(tt.binary, func(t *testing.T) {
			spec := binarySpecs[tt.binary]
			var url string
			if tt.binary == "versitygw" || tt.binary == "surreal" {
				url = fmt.Sprintf("https://github.com/%s/releases/download/%s/x", spec.repo, tt.version)
			} else {
				url = fmt.Sprintf("https://github.com/%s/releases/download/v%s/x", spec.repo, strings.TrimPrefix(tt.version, "v"))
			}
			if !strings.Contains(url, tt.wantTag) {
				t.Errorf("%s URL %q missing tag segment %q", tt.binary, url, tt.wantTag)
			}
		})
	}
}
