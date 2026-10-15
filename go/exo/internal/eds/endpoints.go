package eds

import (
	"fmt"
	"net/url"
)

// ResolveEndpoint resolves an EDS endpoint against the scheme and host of
// the deployment URL. Endpoint paths advertised in Info.Endpoints are
// origin-relative (e.g. "/eds/huge/api/datasets/download" behind a
// path-prefixed proxy, or "/api/datasets/download" on a host-rooted
// deployment) and must never be appended to the deployment URL's own path,
// which would double the prefix (e.g. "/eds/huge/eds/huge/api/...").
//
// If endpointPath is empty, fallbackPath is resolved the same way against
// the deployment URL's origin.
func ResolveEndpoint(deploymentURL, endpointPath, fallbackPath string) (string, error) {
	base, err := url.Parse(deploymentURL)
	if err != nil {
		return "", fmt.Errorf("invalid deployment URL %q: %w", deploymentURL, err)
	}

	path := endpointPath
	if path == "" {
		path = fallbackPath
	}
	if path == "" {
		return "", fmt.Errorf("no endpoint path available for deployment %q", deploymentURL)
	}

	ref, err := url.Parse(path)
	if err != nil {
		return "", fmt.Errorf("invalid endpoint path %q: %w", path, err)
	}

	origin := &url.URL{Scheme: base.Scheme, Host: base.Host}
	return origin.ResolveReference(ref).String(), nil
}
