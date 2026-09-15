//go:build !artifactdb

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "error: not built with 'artifactdb' support")
	fmt.Fprintln(os.Stderr, "Rebuild with -tags artifactdb to enable ArtifactDB integration.")
	os.Exit(1)
}
