//go:build !exohubapi

package serve

import (
	"fmt"

	"golang.org/x/oauth2"
)

// AuthResult holds the outcome of a device flow authentication.
type AuthResult struct {
	Token    *oauth2.Token
	Username string
}

// StartDeviceFlow is not available in this build.
// Build with -tags exohubapi to enable server-mode credential vending.
func StartDeviceFlow() (authURL string, userCode string, resultCh <-chan AuthResult, errCh <-chan error, err error) {
	return "", "", nil, nil, fmt.Errorf("not built with 'exohubapi' support")
}
