//go:build !artifactdb

package init

import "fmt"

func createArtifactDBRemoteTUI(_ *tuiContext) error {
	return fmt.Errorf("not built with 'artifactdb' support")
}
