// Package defaults provides endpoint defaults for the exo CLI.
// Resolution precedence: flag > env > config > built-in.
// OSS built-ins are empty; internal built-ins are provided by defaults_internal.go
// (guarded by //go:build internal).
package defaults

import (
	"os"
	"strings"
)

// builtinAPIBase is the built-in fallback for EXOHUB_API_URL.
// Overridden to an internal URL by defaults_internal.go when built with -tags internal.
var builtinAPIBase = ""

// builtinCatalogURL is the built-in fallback for EXOHUB_CATALOG_URL.
var builtinCatalogURL = ""

// builtinGitHost is the built-in fallback for EXOHUB_GIT_HOST.
var builtinGitHost = ""

// builtinExohubBase is the built-in fallback for the ExoHub web app base URL.
var builtinExohubBase = ""

// builtinVersionBaseURL is the built-in fallback for the version-check catalog URL.
var builtinVersionBaseURL = ""

// builtinDryRunReportSchema is the built-in JSON schema URL for dry-run export reports.
var builtinDryRunReportSchema = ""

// builtinUpgradeURL is the built-in fallback for EXO_UPGRADE_URL.
// Overridden to an internal URL by defaults_internal.go when built with -tags internal.
var builtinUpgradeURL = ""

// APIBase returns the ExoHub API base URL.
// Precedence: EXOHUB_API_URL env → built-in.
func APIBase() string {
	if v := strings.TrimSpace(os.Getenv("EXOHUB_API_URL")); v != "" {
		return v
	}
	return builtinAPIBase
}

// CatalogURL returns the ArtifactDB catalog URL.
// Precedence: EXOHUB_CATALOG_URL env → built-in.
func CatalogURL() string {
	if v := strings.TrimSpace(os.Getenv("EXOHUB_CATALOG_URL")); v != "" {
		return v
	}
	return builtinCatalogURL
}

// GitHost returns the default git host URL.
// Precedence: EXOHUB_GIT_HOST env → built-in.
func GitHost() string {
	if v := strings.TrimSpace(os.Getenv("EXOHUB_GIT_HOST")); v != "" {
		return v
	}
	return builtinGitHost
}

// ExohubBase returns the ExoHub web app base URL (without /api suffix).
// Precedence: EXOHUB_API_URL env (with /api stripped) → built-in.
func ExohubBase() string {
	if v := strings.TrimSpace(os.Getenv("EXOHUB_API_URL")); v != "" {
		base := strings.TrimSuffix(strings.TrimRight(v, "/"), "/api")
		return base
	}
	return builtinExohubBase
}

// VersionBaseURL returns the catalog URL used for version-update checks.
// Precedence: EXOHUB_VERSION_CHECK_URL env → built-in.
// A separate env var is used to decouple version checks from the data catalog
// (builtins differ: /v1/demo vs /v1/exohub).
func VersionBaseURL() string {
	if v := strings.TrimSpace(os.Getenv("EXOHUB_VERSION_CHECK_URL")); v != "" {
		return v
	}
	return builtinVersionBaseURL
}

// DryRunReportSchema returns the JSON schema URL for dry-run export reports.
func DryRunReportSchema() string {
	return builtinDryRunReportSchema
}

// UpgradeURL returns the URL of the exo upgrade script.
// Precedence: EXO_UPGRADE_URL env → built-in.
func UpgradeURL() string {
	if v := strings.TrimSpace(os.Getenv("EXO_UPGRADE_URL")); v != "" {
		return v
	}
	return builtinUpgradeURL
}
