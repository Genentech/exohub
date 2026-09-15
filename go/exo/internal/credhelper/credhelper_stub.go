//go:build !exohubapi

package credhelper

import (
	"context"
	"fmt"
)

// GetCredsFromServer is not available in this build.
// Build with -tags exohubapi to enable the POST /api/grants/credentials fallback.
func GetCredsFromServer(_ context.Context, _, _ string, _ func() (string, error), _ func(string, ...any)) (*Credentials, error) {
	return nil, fmt.Errorf("not built with 'exohubapi' support")
}
