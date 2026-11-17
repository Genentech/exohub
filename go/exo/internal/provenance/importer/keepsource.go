package importer

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// SourcesDir is the repository-relative directory a run's kept source
// lineage documents are written under (spec "Keeping the source lineage").
const SourcesDir = ".exohub/provenance/sources"

// KeepSource writes run's source documents under
// SourcesDir/<run-id-hash>/<name> and stages them as git blobs regardless
// of annex.largefiles (same as `exo provenance add`'s own record staging):
// a kept source document is evidence, never annexed content. It returns
// their repository-relative paths, for the record's exo:evidence.
func KeepSource(root string, run Run) ([]string, error) {
	if len(run.SourceDocuments) == 0 {
		return nil, nil
	}
	digest := sha256.Sum256([]byte(run.ID))
	dir := SourcesDir + "/" + hex.EncodeToString(digest[:])[:16]

	paths := make([]string, 0, len(run.SourceDocuments))
	for _, doc := range run.SourceDocuments {
		path := dir + "/" + doc.Name
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return nil, fmt.Errorf("create %s: %w", filepath.Dir(path), err)
		}
		if err := os.WriteFile(full, doc.Content, 0o644); err != nil {
			return nil, fmt.Errorf("write %s: %w", path, err)
		}
		paths = append(paths, path)
	}

	args := append([]string{"-c", "annex.largefiles=nothing", "add"}, paths...)
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("stage source documents: %w\n%s", err, out)
	}
	return paths, nil
}
