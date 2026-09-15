package unlock

import (
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"

	"github.com/Genentech/exohub/go/exo/commandutil"
)

func NewCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "unlock [flags] [path ...]",
		Short: "Unlock annexed files (make them writable)",
		Long: `Unlock annexed files, replacing read-only symlinks with writable copies.

Wraps git-annex unlock.

Examples:
  exo unlock                   # unlock all annexed files in current directory
  exo unlock data/             # unlock files under data/`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireBins("git-annex"); err != nil {
				exitWithError(err)
			}

			cmdArgs := []string{"git", "annex", "unlock"}

			if len(args) == 0 {
				cmdArgs = append(cmdArgs, ".")
			} else {
				cmdArgs = append(cmdArgs, args...)
			}

			return runCommand(cmdArgs)
		},
	}

	return rootCmd
}

func exitWithError(err error) {
	fmt.Fprintln(os.Stderr, err.Error())
	os.Exit(1)
}

func requireBins(names ...string) error {
	missing := false
	for _, name := range names {
		if !commandExists(name) {
			fmt.Fprintf(os.Stderr, "Required binary '%s' not found in PATH\n", name)
			missing = true
		}
	}
	if missing {
		return errors.New("missing binaries")
	}
	return nil
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func runCommand(args []string) error {
	cmd := commandutil.Command(args[0], args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}
