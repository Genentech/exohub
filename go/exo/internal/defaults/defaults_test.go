package defaults

import (
	"testing"
)

func TestAPIBase_EnvOverride(t *testing.T) {
	t.Setenv("EXOHUB_API_URL", "https://custom.example.com/api")
	got := APIBase()
	if got != "https://custom.example.com/api" {
		t.Errorf("APIBase() = %q, want %q", got, "https://custom.example.com/api")
	}
}

func TestAPIBase_Builtin(t *testing.T) {
	t.Setenv("EXOHUB_API_URL", "")
	got := APIBase()
	if got != builtinAPIBase {
		t.Errorf("APIBase() = %q, want builtin %q", got, builtinAPIBase)
	}
}

func TestCatalogURL_EnvOverride(t *testing.T) {
	t.Setenv("EXOHUB_CATALOG_URL", "https://catalog.example.com/v1/db")
	got := CatalogURL()
	if got != "https://catalog.example.com/v1/db" {
		t.Errorf("CatalogURL() = %q, want %q", got, "https://catalog.example.com/v1/db")
	}
}

func TestCatalogURL_Builtin(t *testing.T) {
	t.Setenv("EXOHUB_CATALOG_URL", "")
	got := CatalogURL()
	if got != builtinCatalogURL {
		t.Errorf("CatalogURL() = %q, want builtin %q", got, builtinCatalogURL)
	}
}

func TestGitHost_EnvOverride(t *testing.T) {
	t.Setenv("EXOHUB_GIT_HOST", "https://git.example.com/")
	got := GitHost()
	if got != "https://git.example.com/" {
		t.Errorf("GitHost() = %q, want %q", got, "https://git.example.com/")
	}
}

func TestGitHost_Builtin(t *testing.T) {
	t.Setenv("EXOHUB_GIT_HOST", "")
	got := GitHost()
	if got != builtinGitHost {
		t.Errorf("GitHost() = %q, want builtin %q", got, builtinGitHost)
	}
}

func TestExohubBase_EnvOverride(t *testing.T) {
	t.Setenv("EXOHUB_API_URL", "https://custom.example.com/api")
	got := ExohubBase()
	if got != "https://custom.example.com" {
		t.Errorf("ExohubBase() = %q, want %q", got, "https://custom.example.com")
	}
}

func TestExohubBase_Builtin(t *testing.T) {
	t.Setenv("EXOHUB_API_URL", "")
	got := ExohubBase()
	if got != builtinExohubBase {
		t.Errorf("ExohubBase() = %q, want builtin %q", got, builtinExohubBase)
	}
}

func TestVersionBaseURL_EnvOverride(t *testing.T) {
	t.Setenv("EXOHUB_VERSION_CHECK_URL", "https://catalog.example.com/v1/demo")
	got := VersionBaseURL()
	if got != "https://catalog.example.com/v1/demo" {
		t.Errorf("VersionBaseURL() = %q, want %q", got, "https://catalog.example.com/v1/demo")
	}
}

func TestVersionBaseURL_Builtin(t *testing.T) {
	t.Setenv("EXOHUB_VERSION_CHECK_URL", "")
	got := VersionBaseURL()
	if got != builtinVersionBaseURL {
		t.Errorf("VersionBaseURL() = %q, want builtin %q", got, builtinVersionBaseURL)
	}
}

func TestVersionBaseURL_NotCoupledToCatalogURL(t *testing.T) {
	// Setting EXOHUB_CATALOG_URL must NOT affect version checks.
	t.Setenv("EXOHUB_CATALOG_URL", "https://catalog.example.com/v1/exohub")
	t.Setenv("EXOHUB_VERSION_CHECK_URL", "")
	got := VersionBaseURL()
	if got != builtinVersionBaseURL {
		t.Errorf("VersionBaseURL() = %q (affected by EXOHUB_CATALOG_URL), want builtin %q", got, builtinVersionBaseURL)
	}
}

func TestUpgradeURL_EnvOverride(t *testing.T) {
	t.Setenv("EXO_UPGRADE_URL", "https://upgrade.example.com/exocli")
	got := UpgradeURL()
	if got != "https://upgrade.example.com/exocli" {
		t.Errorf("UpgradeURL() = %q, want %q", got, "https://upgrade.example.com/exocli")
	}
}

func TestUpgradeURL_Builtin(t *testing.T) {
	t.Setenv("EXO_UPGRADE_URL", "")
	got := UpgradeURL()
	if got != builtinUpgradeURL {
		t.Errorf("UpgradeURL() = %q, want builtin %q", got, builtinUpgradeURL)
	}
}
