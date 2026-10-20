package provenance

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// hookBeginMarker and hookEndMarker delimit the block newInstallHookCommand
// manages in the pre-commit hook file, so re-running the command updates
// only that block and leaves the rest of the hook untouched (design D14
// "Pre-commit hook").
const (
	hookBeginMarker = "# >>> exo provenance >>>"
	hookEndMarker   = "# <<< exo provenance <<<"
)

var hookBlockLines = []string{
	hookBeginMarker,
	"# Installed by \"exo provenance install-hook\"; edits are overwritten.",
	"if ! exo provenance validate --staged; then",
	"  echo \"exo provenance validate --staged failed; see above\" >&2",
	"  exit 1",
	"fi",
	hookEndMarker,
}

// newInstallHookCommand returns `exo provenance install-hook` (design D14).
func newInstallHookCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "install-hook",
		Short: "Install a pre-commit hook that runs exo provenance validate --staged",
		Long: `Install a git pre-commit hook that runs "exo provenance validate --staged"
before every commit: a staged file matching provenance.require
without a covering record blocks the commit.

The hook is added as a marked block inserted right after the shebang line
of .git/hooks/pre-commit (or the repository's configured hooks directory).
Existing hook content is kept; running this command again updates the
block in place instead of duplicating it.

pre-commit framework: add this to .pre-commit-config.yaml instead of
running this command:

  - repo: local
    hooks:
      - id: exo-provenance-validate
        name: exo provenance validate --staged
        entry: exo provenance validate --staged
        language: system
        pass_filenames: false`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInstallHook(cmd)
		},
	}
	return cmd
}

func runInstallHook(cmd *cobra.Command) error {
	path, err := preCommitHookPath()
	if err != nil {
		return err
	}

	// core.hooksPath may point to a directory that does not exist yet (for
	// example a repository-tracked hooks/ directory nobody has created on
	// disk).
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}

	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", path, err)
	}

	updated, changed := installHookBlock(string(existing))
	if err := os.WriteFile(path, []byte(updated), 0o755); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o755); err != nil {
		return fmt.Errorf("make %s executable: %w", path, err)
	}

	if changed {
		fmt.Fprintf(cmd.OutOrStdout(), "Installed the provenance pre-commit hook in %s\n", path)
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "The provenance pre-commit hook in %s is already up to date\n", path)
	}
	return nil
}

// preCommitHookPath resolves the repository's pre-commit hook file,
// honoring core.hooksPath.
func preCommitHookPath() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--git-path", "hooks/pre-commit").Output()
	if err != nil {
		return "", fmt.Errorf("not inside a git repository: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// installHookBlock returns existing with the marked provenance block
// inserted or, if already present, replaced in place, and whether the
// result differs from existing. A new hook file gets a "#!/bin/sh"
// shebang. When inserting a new block (not replacing an existing one), it
// goes right after the shebang line, not at the end: many existing hooks
// end with `exec ...` or `exit 0`, and a block appended after that would
// never run (review: "insert the block right after the shebang line").
// Existing content is otherwise never modified outside the marked block.
func installHookBlock(existing string) (updated string, changed bool) {
	block := strings.Join(hookBlockLines, "\n")

	if begin := strings.Index(existing, hookBeginMarker); begin >= 0 {
		if endOfMarker := strings.Index(existing[begin:], hookEndMarker); endOfMarker >= 0 {
			end := begin + endOfMarker + len(hookEndMarker)
			updated = existing[:begin] + block + existing[end:]
			return updated, updated != existing
		}
	}

	if existing == "" {
		return "#!/bin/sh\n" + block + "\n", true
	}

	pos := shebangEnd(existing)
	before, after := existing[:pos], existing[pos:]
	if before != "" && !strings.HasSuffix(before, "\n") {
		before += "\n"
	}
	updated = before + block + "\n" + after
	return updated, updated != existing
}

// shebangEnd returns the index right after content's shebang line ("#!..."
// plus its trailing newline), or 0 if content has no shebang.
func shebangEnd(content string) int {
	if !strings.HasPrefix(content, "#!") {
		return 0
	}
	if nl := strings.IndexByte(content, '\n'); nl >= 0 {
		return nl + 1
	}
	return len(content)
}
