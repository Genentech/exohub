//go:build !internal

package defaults

import "testing"

func TestInstallCmd_OSSBuiltin(t *testing.T) {
	t.Setenv("EXOHUB_INSTALL_CMD", "")
	got := InstallCmd()
	want := "curl -fsSL https://github.com/Genentech/exohub/releases/latest/download/install.sh | sh"
	if got != want {
		t.Errorf("InstallCmd() = %q, want OSS builtin %q", got, want)
	}
}

func TestDocsURL_OSSBuiltin(t *testing.T) {
	t.Setenv("EXOHUB_DOCS_URL", "")
	got := DocsURL()
	want := "https://genentech.github.io/exohub/llms.txt"
	if got != want {
		t.Errorf("DocsURL() = %q, want OSS builtin %q", got, want)
	}
}
