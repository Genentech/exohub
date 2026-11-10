package upgrade

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeVersion(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"bare semver gets v prefix", "0.134.0", "v0.134.0"},
		{"already v-prefixed unchanged", "v0.134.0", "v0.134.0"},
		{"bare semver with prerelease", "0.134.0-rc1", "v0.134.0-rc1"},
		{"bare semver with build metadata", "0.134.0+build5", "v0.134.0+build5"},
		{"latest unchanged", "latest", "latest"},
		{"empty unchanged", "", ""},
		{"non-semver string unchanged", "banana", "banana"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeVersion(tt.input); got != tt.want {
				t.Errorf("normalizeVersion(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestUpgradeCommandNormalizesVersionInScriptArgv stubs the upgrade script
// download (via http.DefaultTransport) and the script execution (the
// downloaded "script" just records its own argv to a file) to verify that
// both the positional argument and --version produce a v-prefixed
// --version in the argv passed to the script.
func TestUpgradeCommandNormalizesVersionInScriptArgv(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"positional", []string{"0.134.0"}},
		{"flag", []string{"--version", "0.134.0"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recordFile := filepath.Join(t.TempDir(), "argv.txt")
			scriptBody := fmt.Sprintf("#!/bin/sh\necho \"$@\" > %q\n", recordFile)

			previous := http.DefaultTransport
			http.DefaultTransport = roundTripper(func(r *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Status:     http.StatusText(http.StatusOK),
					Body:       io.NopCloser(strings.NewReader(scriptBody)),
					Header:     make(http.Header),
				}, nil
			})
			defer func() { http.DefaultTransport = previous }()

			t.Setenv("EXO_UPGRADE_URL", "https://upgrade.test/script")

			cmd := NewCommand()
			cmd.SetArgs(tc.args)
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("cmd.Execute: %v", err)
			}

			data, err := os.ReadFile(recordFile)
			if err != nil {
				t.Fatalf("read record file: %v", err)
			}
			argv := strings.TrimSpace(string(data))
			if !strings.Contains(argv, "--version v0.134.0") {
				t.Errorf("script argv = %q, want it to contain %q", argv, "--version v0.134.0")
			}
		})
	}
}

func TestDownloadUpgradeScript(t *testing.T) {
	previous := http.DefaultTransport
	http.DefaultTransport = roundTripper(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     http.StatusText(http.StatusOK),
			Body:       io.NopCloser(strings.NewReader("#!/bin/sh\necho ok\n")),
			Header:     make(http.Header),
		}, nil
	})
	defer func() { http.DefaultTransport = previous }()

	path, cleanup, err := downloadUpgradeScript("http://upgrade.test/script")
	if err != nil {
		t.Fatalf("downloadUpgradeScript: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("script missing: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read script: %v", err)
	}
	if string(data) == "" {
		t.Fatalf("script content empty")
	}
	cleanup()
	if _, err := os.Stat(path); err == nil {
		t.Fatalf("expected script removed after cleanup")
	}
}

func TestDownloadUpgradeScriptRejectsNon2xx(t *testing.T) {
	previous := http.DefaultTransport
	http.DefaultTransport = roundTripper(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Status:     http.StatusText(http.StatusBadRequest),
			Body:       io.NopCloser(strings.NewReader("bad")),
			Header:     make(http.Header),
		}, nil
	})
	defer func() { http.DefaultTransport = previous }()

	if _, _, err := downloadUpgradeScript("http://upgrade.test/bad"); err == nil {
		t.Fatalf("expected error for non-2xx")
	}
}

func TestCurrentBinaryDir(t *testing.T) {
	dir, err := currentBinaryDir()
	if err != nil {
		t.Fatalf("currentBinaryDir: %v", err)
	}
	if dir == "" {
		t.Fatalf("currentBinaryDir empty")
	}
	if _, err := os.Stat(filepath.Clean(dir)); err != nil {
		t.Fatalf("currentBinaryDir missing: %v", err)
	}
}

type roundTripper func(*http.Request) (*http.Response, error)

func (rt roundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	return rt(r)
}
