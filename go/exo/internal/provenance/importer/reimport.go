package importer

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	lib "github.com/Genentech/exohub/go/exo/internal/provenance"
)

// ExistingImport is a previously written, non-superseded record that
// already imported a run (spec "Re-import detection").
type ExistingImport struct {
	RecordID string
	Path     string
}

// FindExistingImport looks for a non-superseded worktree record with an
// Activity whose hadPrimarySource equals runID (spec "Re-import
// detection"). It uses the same targeted-grep-then-confirm pattern as `exo
// provenance add`'s duplicate-generation check (commands/provenance/add.go
// findGenerationConflicts): one `git grep` to find candidate record files
// mentioning runID, parsed to confirm an actual hadPrimarySource match,
// then one more `git grep` to find records that supersede a candidate,
// parsed to confirm.
func FindExistingImport(root, runID string) (*ExistingImport, error) {
	files, err := gitGrepRecordFiles(root, []string{runID})
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, nil
	}

	type candidate struct {
		path string
		rec  *lib.Record
	}
	candidates := make([]candidate, 0, len(files))
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", f, err)
		}
		rec, err := lib.Parse(data)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", f, err)
		}
		candidates = append(candidates, candidate{path: f, rec: rec})
	}

	candidateIDs := make([]string, len(candidates))
	for i, c := range candidates {
		candidateIDs[i] = c.rec.ID
	}
	supersedingFiles, err := gitGrepRecordFiles(root, candidateIDs)
	if err != nil {
		return nil, err
	}
	superseded := map[string]bool{}
	for _, f := range supersedingFiles {
		data, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", f, err)
		}
		rec, err := lib.Parse(data)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", f, err)
		}
		for _, s := range rec.Supersedes {
			superseded[s] = true
		}
	}

	for _, c := range candidates {
		if superseded[c.rec.ID] {
			continue
		}
		for _, n := range c.rec.Graph {
			if !hasType(n, "Activity") {
				continue
			}
			if src, _ := n["hadPrimarySource"].(string); src == runID {
				return &ExistingImport{RecordID: c.rec.ID, Path: c.path}, nil
			}
		}
	}
	return nil, nil
}

func hasType(n lib.Node, typ string) bool {
	for _, t := range n.Types() {
		if t == typ {
			return true
		}
	}
	return false
}

// gitGrepRecordFiles returns the repo-root-relative paths of worktree
// files under lib.RecordDir (including untracked ones) whose content
// contains at least one of needles, in one `git grep` invocation
// regardless of how many needles there are. A "no matches" exit (status 1)
// is not an error.
func gitGrepRecordFiles(root string, needles []string) ([]string, error) {
	if len(needles) == 0 {
		return nil, nil
	}
	args := []string{"--literal-pathspecs", "grep", "--untracked", "-F", "-l", "-z"}
	for _, n := range needles {
		args = append(args, "-e", n)
	}
	args = append(args, "--", lib.RecordDir+"/")
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return nil, nil
		}
		return nil, fmt.Errorf("git grep: %w", err)
	}
	trimmed := strings.TrimRight(string(out), "\x00")
	if trimmed == "" {
		return nil, nil
	}
	return strings.Split(trimmed, "\x00"), nil
}
