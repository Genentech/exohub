package add

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Genentech/exohub/go/exo/internal/exohub"
)

// runExohubAdd implements `exo add exohub:...`
// (openspec/changes/add-exohub-asset-import): each reference is resolved
// (local clone, then catalog, then git remote; design D2) and materialised
// (design D3) in turn.
func runExohubAdd(cmd *cobra.Command, refs []*exohub.Reference, toDir, repoURL string, verify bool) error {
	for _, ref := range refs {
		if err := addExohubReference(cmd, ref, toDir, repoURL, verify); err != nil {
			return fmt.Errorf("%s: %w", ref.Raw, err)
		}
	}
	return nil
}

func addExohubReference(cmd *cobra.Command, ref *exohub.Reference, toDir, repoURL string, verify bool) error {
	// Resolve everything recording the import needs but materialising
	// files does not touch (the repo root, the importer's identity) before
	// writing anything: an import that can't be recorded should fail before
	// it stages files with no provenance, not after (MR !295 review).
	importCtx, err := prepareExohubImportContext()
	if err != nil {
		return err
	}

	res, err := exohub.Resolve(ref, exohub.ResolveOptions{RepoURL: repoURL, Verify: verify})
	if err != nil {
		return err
	}
	defer res.Close()
	if res.Note != "" {
		fmt.Fprintln(cmd.ErrOrStderr(), res.Note)
	}

	for _, f := range res.Files {
		target, err := exohubTargetPath(ref, f, toDir)
		if err != nil {
			return fmt.Errorf("%s: %w", f.Path, err)
		}
		if err := materializeFile(res, f, target); err != nil {
			return fmt.Errorf("%s: %w", f.Path, err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s\n", target)
	}

	if err := recordExohubImport(importCtx, ref, res, toDir, cmd.ErrOrStderr()); err != nil {
		return fmt.Errorf("record import provenance: %w", err)
	}
	return nil
}

// exohubTargetPath computes the local destination for one resolved file
// (design D3 task 3.3): a single asset defaults to its base name, or
// follows the `cp` convention under an explicit --to (task per exo-cli#152
// review); a folder or whole-project import defaults to its repo-relative
// path under --to (or <project>/ for a whole project), and a collection
// keeps each member's own relative path under --to. f.Path comes from the
// catalog, a git tree, or a collection manifest -- all untrusted remote
// input -- so every joined path is checked to make sure it cannot escape
// its base directory (exo-cli#152 review).
func exohubTargetPath(ref *exohub.Reference, f exohub.ResolvedFile, toDir string) (string, error) {
	switch {
	case ref.IsAsset():
		return assetTargetPath(f, toDir)
	case ref.IsFolder():
		base := toDir
		if base == "" {
			base = "."
		}
		rel := strings.TrimPrefix(f.Path, ref.Path)
		return safeJoinUnderBase(base, rel)
	case ref.IsCollection():
		base := toDir
		if base == "" {
			base = "."
		}
		return safeJoinUnderBase(base, f.Path)
	default: // whole project
		base := toDir
		if base == "" {
			base = ref.Project
		}
		return safeJoinUnderBase(base, f.Path)
	}
}

// assetTargetPath implements the `cp`-like --to convention for a single
// asset (exo-cli#152 review): with no --to, the target is the file's own
// base name in the current directory; if --to ends with a path separator
// or already names an existing directory, the base name is placed inside
// it; otherwise --to is the exact target file path.
func assetTargetPath(f exohub.ResolvedFile, toDir string) (string, error) {
	base := filepath.Base(f.Path)
	if toDir == "" {
		return safeJoinUnderBase(".", base)
	}
	if strings.HasSuffix(toDir, "/") || strings.HasSuffix(toDir, string(filepath.Separator)) || isExistingDir(toDir) {
		return safeJoinUnderBase(toDir, base)
	}
	return filepath.Clean(toDir), nil
}

func isExistingDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// safeJoinUnderBase joins base and rel and rejects any result that would
// resolve outside of base (exo-cli#152 review: rel can come from a catalog
// document, a collection manifest, or a git tree -- all untrusted remote
// input -- so a ".." component or an absolute path must never be allowed
// to write outside the chosen target directory or the repository root).
func safeJoinUnderBase(base, rel string) (string, error) {
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("refusing to materialize %q: absolute paths are not allowed", rel)
	}
	joined := filepath.Join(base, rel)
	relToBase, err := filepath.Rel(filepath.Clean(base), joined)
	if err != nil || relToBase == ".." || strings.HasPrefix(relToBase, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("refusing to materialize %q: target escapes %s", rel, base)
	}
	return joined, nil
}
