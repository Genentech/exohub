package templates

import _ "embed"

var (
	//go:embed annex-manifest.yaml
	annexTemplate string
	//go:embed export-manifest.yaml
	exportTemplate string
	//go:embed permissions.yaml
	permissionsTemplate string
	//go:embed bundle-manifest.yaml
	bundleTemplate string
)

// Annex returns the embedded annex manifest template.
func Annex() string {
	return annexTemplate
}

// Export returns the embedded export manifest template.
func Export() string {
	return exportTemplate
}

// Permissions returns the embedded permissions manifest template.
func Permissions() string {
	return permissionsTemplate
}

// Bundle returns the embedded bundle manifest template.
func Bundle() string {
	return bundleTemplate
}
