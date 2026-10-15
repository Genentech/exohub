package eds

import "testing"

func TestResolveEndpoint_PathPrefixed(t *testing.T) {
	// "huge"-style deployment: behind a shared gateway, endpoints carry the
	// full path prefix already.
	got, err := ResolveEndpoint(
		"https://exohub.example.com/eds/huge",
		"/eds/huge/api/datasets/download",
		"/api/datasets/download",
	)
	if err != nil {
		t.Fatalf("ResolveEndpoint() error = %v", err)
	}
	want := "https://exohub.example.com/eds/huge/api/datasets/download"
	if got != want {
		t.Errorf("ResolveEndpoint() = %q, want %q", got, want)
	}
}

func TestResolveEndpoint_HostRooted(t *testing.T) {
	// my-eds-style deployment: its own host, endpoints are host-rooted.
	got, err := ResolveEndpoint(
		"https://eds.example.com",
		"/api/datasets/download",
		"/api/datasets/download",
	)
	if err != nil {
		t.Fatalf("ResolveEndpoint() error = %v", err)
	}
	want := "https://eds.example.com/api/datasets/download"
	if got != want {
		t.Errorf("ResolveEndpoint() = %q, want %q", got, want)
	}
}

func TestResolveEndpoint_DoesNotDoubleDeploymentPath(t *testing.T) {
	// Regression: must never append the endpoint path onto the deployment
	// URL's own path, which would yield /eds/huge/eds/huge/api/....
	got, err := ResolveEndpoint(
		"https://exohub.example.com/eds/huge",
		"/eds/huge/api/operations/123",
		"",
	)
	if err != nil {
		t.Fatalf("ResolveEndpoint() error = %v", err)
	}
	bad := "https://exohub.example.com/eds/huge/eds/huge/api/operations/123"
	if got == bad {
		t.Fatalf("ResolveEndpoint() doubled the deployment path: %q", got)
	}
	want := "https://exohub.example.com/eds/huge/api/operations/123"
	if got != want {
		t.Errorf("ResolveEndpoint() = %q, want %q", got, want)
	}
}

func TestResolveEndpoint_FallbackUsedWhenEndpointMissing(t *testing.T) {
	got, err := ResolveEndpoint(
		"https://eds.example.com",
		"",
		"/api/datasets/download",
	)
	if err != nil {
		t.Fatalf("ResolveEndpoint() error = %v", err)
	}
	want := "https://eds.example.com/api/datasets/download"
	if got != want {
		t.Errorf("ResolveEndpoint() = %q, want %q", got, want)
	}
}

func TestResolveEndpoint_NoPathAvailable(t *testing.T) {
	_, err := ResolveEndpoint("https://example.com", "", "")
	if err == nil {
		t.Fatal("ResolveEndpoint() expected error when no path is available")
	}
}
