package lock

import (
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"

	"github.com/Genentech/exohub/go/exo/commandutil"
)

func NewCommand() *cobra.Command {
	var forceFlag bool

	rootCmd := &cobra.Command{
		Use:   "lock [flags] [path ...]",
		Short: "Lock annexed files (switch back to read-only symlinks)",
		Long: `Lock annexed files, switching them back to read-only symlinks.

Wraps git-annex lock.

Examples:
  exo lock                     # lock all annexed files in current directory
  exo lock data/               # lock files under data/
  exo lock --force .           # lock even if files have unsaved modifications`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireBins("git-annex"); err != nil {
				exitWithError(err)
			}

			cmdArgs := []string{"git", "annex", "lock"}

			if forceFlag {
				cmdArgs = append(cmdArgs, "--force")
			}

			if len(args) == 0 {
				cmdArgs = append(cmdArgs, ".")
			} else {
				cmdArgs = append(cmdArgs, args...)
			}

			return runCommand(cmdArgs)
		},
	}

	rootCmd.Flags().BoolVar(&forceFlag, "force", false, "Lock even with unsaved modifications")

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
