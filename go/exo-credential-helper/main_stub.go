//go:build !exohubapi

package main

import (
	"fmt"
	"os"
)

// printCredsFromServerError prints the error from getCredsFromServer.
func printCredsFromServerError(err error, _ string) {
	fmt.Fprintf(os.Stderr, "Error: Failed to get credentials: %v\n", err)
}

// getCredsFromServer is not available in this build.
// Build with -tags exohubapi to enable the POST /api/grants/credentials fallback.
func getCredsFromServer(_, _ string, _ func(string, ...any)) (*credentialOutput, error) {
	return nil, fmt.Errorf("not built with 'exohubapi' support")
}
