package add

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Genentech/exohub/go/exo/commands/login"
	"github.com/Genentech/exohub/go/exo/internal/auth"
	"github.com/Genentech/exohub/go/exo/internal/exohub"
	lib "github.com/Genentech/exohub/go/exo/internal/provenance"
)

// exohubImportContext holds what recordExohubImport needs that must be
// resolved before any file is materialised (MR !295 review): without a
// valid exo login token (or outside a git repository), an import cannot be
// recorded, and must fail before it stages files with no provenance rather
// than after.
type exohubImportContext struct {
	root        string
	personID    string
	personLabel string
}

// prepareExohubImportContext resolves the repo root and the importing
// person's identity up front (see exohubImportContext).
func prepareExohubImportContext() (*exohubImportContext, error) {
	root, err := exohubRepoRoot()
	if err != nil {
		return nil, err
	}
	personID, personLabel, err := loginPersonIdentityForImport()
	if err != nil {
		return nil, err
	}
	return &exohubImportContext{root: root, personID: personID, personLabel: personLabel}, nil
}

// recordExohubImport writes and stages the automatic `recorded` import
// provenance record for one resolved `exohub:` reference (design D4, tasks
// 4.2-4.3 of openspec/changes/add-exohub-asset-import): an Activity that
// `used` the source entity (the asset's content id, the project/folder's
// root tree id, or the source collection's id), associated with the
// importing agent; for a project, folder or collection import, the local
// materialised target `wasDerivedFrom` and `hadPrimarySource` the source.
// No `wasGeneratedBy` is ever stated for an imported content id: the
// imported files keep the content ids they have in the source project. It
// also copies, byte-identical, the source's own records that directly
// cover the imported assets (design D4 Q3). ctx comes from
// prepareExohubImportContext, resolved before materialisation; warn
// receives one line per source record skipped while copying.
func recordExohubImport(ctx *exohubImportContext, ref *exohub.Reference, res *exohub.Resolution, toDir string, warn io.Writer) error {
	root := ctx.root

	sourceID, coveredIDs, err := sourceEntityID(res, ref)
	if err != nil {
		return fmt.Errorf("determine source entity id: %w", err)
	}

	graph := map[string]lib.Node{}
	source := lib.Node{
		"@id":            sourceID,
		"@type":          sourceNodeType(sourceID),
		"label":          fmt.Sprintf("%s@%s", res.Project, res.Version),
		"exo:repository": res.RepoURL,
		"exo:commit":     res.Commit,
	}
	if ref.IsAsset() {
		source["exo:path"] = ref.Path
	}
	graph[sourceID] = source

	person := lib.Node{"@id": ctx.personID, "@type": "Person"}
	if ctx.personLabel != "" {
		person["label"] = ctx.personLabel
	}
	graph[ctx.personID] = person

	activityID := lib.NewActivityID()
	graph[activityID] = lib.Node{
		"@id":               activityID,
		"@type":             "Activity",
		"used":              sourceID,
		"wasAssociatedWith": ctx.personID,
	}

	targetID, err := importTargetID(root, ref, toDir)
	if err != nil {
		return fmt.Errorf("determine local import target id: %w", err)
	}
	if targetID != "" {
		graph[targetID] = lib.Node{
			"@id":              targetID,
			"@type":            "Collection",
			"wasDerivedFrom":   sourceID,
			"hadPrimarySource": sourceID,
		}
	}

	generatedAt := time.Now().UTC()
	rec := &lib.Record{
		Context:         lib.ContextIRI,
		ID:              lib.NewRecordID(),
		Assertion:       lib.AssertionRecorded,
		AssertedBy:      ctx.personID,
		GeneratedAtTime: generatedAt.Format(time.RFC3339),
		Graph:           exohubGraphNodes(graph),
	}

	data, err := lib.Serialize(rec)
	if err != nil {
		return fmt.Errorf("build import record: %w", err)
	}
	if _, err := lib.Parse(data); err != nil {
		return fmt.Errorf("built an invalid import record: %w", err)
	}

	path, err := lib.RecordPath(generatedAt, lib.Slugify(ref.Project), rec.ID)
	if err != nil {
		return fmt.Errorf("build import record path: %w", err)
	}
	if err := writeExohubRecordFile(root, path, data); err != nil {
		return err
	}
	if err := stageExohubRecordFile(root, path); err != nil {
		return err
	}

	return copySourceRecords(root, res, coveredIDs, warn)
}

// sourceEntityID returns ref's source entity id (design D4: the asset's
// content id, the project/folder's root tree id, or the source
// collection's id) and the full set of ids the import covers (every
// member's own content id plus the source id itself), used to find the
// source's directly-covering records (design D4 Q3).
func sourceEntityID(res *exohub.Resolution, ref *exohub.Reference) (sourceID string, coveredIDs map[string]bool, err error) {
	if err := exohub.FillGitOIDs(res); err != nil {
		return "", nil, err
	}

	memberIDs := make([]string, len(res.Files))
	coveredIDs = make(map[string]bool, len(res.Files)+1)
	for i, f := range res.Files {
		id, err := exohubEntityID(f)
		if err != nil {
			return "", nil, err
		}
		memberIDs[i] = id
		coveredIDs[id] = true
	}

	switch {
	case ref.IsAsset():
		sourceID = memberIDs[0]
	case ref.IsCollection():
		sourceID = lib.CollectionID(memberIDs)
	default: // whole project or folder
		oid, err := exohub.RootTreeID(res, ref.Path)
		if err != nil {
			return "", nil, err
		}
		sourceID = lib.GitTreeEntityID(oid)
	}
	coveredIDs[sourceID] = true
	return sourceID, coveredIDs, nil
}

// exohubEntityID returns f's content identifier (design D5): an annex key
// gets an annex entity id, a git blob gets a git blob entity id. f.GitOID
// must already be resolved for a ContentGit file (see exohub.FillGitOIDs).
func exohubEntityID(f exohub.ResolvedFile) (string, error) {
	if f.Kind == exohub.ContentAnnex {
		return lib.AnnexEntityID(f.Key), nil
	}
	if f.GitOID == "" {
		return "", fmt.Errorf("%s: no git blob id resolved", f.Path)
	}
	return lib.GitBlobEntityID(f.GitOID), nil
}

// sourceNodeType picks the source node's @type: an Entity for a single
// asset's content id, a Collection for a project/folder's tree id or a
// collection id.
func sourceNodeType(id string) string {
	if strings.HasPrefix(id, lib.AnnexURNPrefix) || strings.HasPrefix(id, lib.GitBlobURNPrefix) {
		return "Entity"
	}
	return "Collection"
}

// importTargetID resolves the local materialised target's current content
// id for a project, folder or collection import (design D4): the folder
// `exo add exohub:...` placed files under, read at the git index (the same
// state `exo provenance add --generated <folder>/` reads, so the import
// record and a later manual record agree). It returns "" when the import
// has no discrete local folder to name: a single asset, or a folder or
// collection import with no --to, which places files directly in the
// current directory rather than under a folder of their own.
func importTargetID(root string, ref *exohub.Reference, toDir string) (string, error) {
	dir := importLocalDir(ref, toDir)
	if dir == "" || dir == "." {
		return "", nil
	}
	rel, err := exohubRepoRelative(root, dir)
	if err != nil {
		return "", err
	}
	if rel == "" || rel == "." {
		return "", nil
	}
	return lib.NewWorktreeReader(root).ResolveContentID(rel + "/")
}

// importLocalDir mirrors exohubTargetPath's --to defaulting (exohub.go) to
// find the base directory an import's files land under, or "" when they
// land directly in the current directory without a folder of their own.
func importLocalDir(ref *exohub.Reference, toDir string) string {
	switch {
	case ref.IsAsset():
		return ""
	case ref.IsProject():
		if toDir == "" {
			return ref.Project
		}
		return toDir
	default: // folder or collection
		return toDir
	}
}

func exohubRepoRoot() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("not inside a git repository: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// exohubRepoRelative resolves dir (relative to the current working
// directory, or absolute) to a path relative to root, rejecting one that
// resolves outside it.
func exohubRepoRelative(root, dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return "", err
	}
	rel = filepath.ToSlash(rel)
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("%s is outside the repository", dir)
	}
	return rel, nil
}

// loginPersonIdentityForImport resolves the importing person's identity
// from the stored exo login token (design D4: "associated with the
// importing agent (the logged-in person, or the MCP client acting on their
// behalf)"), mirroring go/exo/commands/provenance/add.go's
// loginPersonIdentity.
func loginPersonIdentityForImport() (personID, label string, err error) {
	tokenFile, tErr := login.GetTokenFile()
	if tErr != nil {
		return "", "", fmt.Errorf("no exo login token found; run \"exo login\"")
	}
	token, lErr := login.LoadToken(tokenFile)
	if lErr != nil || token.AccessToken == "" {
		return "", "", fmt.Errorf("no exo login token found; run \"exo login\"")
	}
	unixID, lbl, ok := auth.PersonIdentityFromToken(token.AccessToken)
	if !ok {
		return "", "", fmt.Errorf("could not determine the logged-in identity from the exo login token")
	}
	return lib.PersonAgentID(unixID), lbl, nil
}

func exohubGraphNodes(graph map[string]lib.Node) []lib.Node {
	out := make([]lib.Node, 0, len(graph))
	for _, n := range graph {
		out = append(out, n)
	}
	return out
}

// writeExohubRecordFile writes data to repo-root-relative path, failing if
// the file already exists: a record file is never overwritten.
func writeExohubRecordFile(root, path string, data []byte) error {
	full := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	f, err := os.OpenFile(full, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	return f.Close()
}

// stageExohubRecordFile stages path as a git blob regardless of
// annex.largefiles, like `exo provenance add` does for record files.
func stageExohubRecordFile(root, path string) error {
	cmd := exec.Command("git", "-c", "annex.largefiles=nothing", "add", path)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("stage %s: %w\n%s", path, err, out)
	}
	return nil
}

// validSourceRecordPath reports whether path is safe to join under the
// importer's repo root and copy as a provenance record: directly under
// lib.RecordDir (no nested subdirectory), no ".." segment, a ".jsonld"
// file -- the same restriction Reader's own record listing applies (see
// internal/provenance/state.go). path comes from the source repository's
// git tree, an untrusted remote (design D2), same untrusted-input rule as
// MR !293.
func validSourceRecordPath(path string) bool {
	rel := strings.TrimPrefix(path, lib.RecordDir+"/")
	if rel == path || rel == "" {
		return false
	}
	if strings.Contains(rel, "/") || strings.Contains(rel, "..") {
		return false
	}
	return strings.HasSuffix(rel, ".jsonld")
}

// copySourceRecords copies, byte-identical, res's own records that
// directly cover coveredIDs into the importer's .exohub/provenance/
// (design D4 Q3, "closest level only"): a record already present locally
// (same repo-relative path, hence the same record, since a record file
// name is derived from its own id and generation time) is left untouched,
// never duplicated. A source record superseded by another source record is
// never copied on its own; if the superseding record itself directly
// covers coveredIDs it is copied instead (MR !295 review: an importer must
// not carry a stale, corrected generation claim). Every source file that
// isn't a validSourceRecordPath, or doesn't parse as a record, is skipped
// with one warning line each rather than failing the import.
func copySourceRecords(root string, res *exohub.Resolution, coveredIDs map[string]bool, warn io.Writer) error {
	files, err := exohub.SourceRecordFiles(res)
	if err != nil {
		return fmt.Errorf("list source provenance records: %w", err)
	}

	type sourceRecord struct {
		path string
		data []byte
		rec  *lib.Record
	}
	var records []sourceRecord
	for path, data := range files {
		if !validSourceRecordPath(path) {
			fmt.Fprintf(warn, "exohub import: skipping source record %s: not a valid provenance record path\n", path)
			continue
		}
		rec, err := lib.Parse(data)
		if err != nil {
			fmt.Fprintf(warn, "exohub import: skipping source record %s: %v\n", path, err)
			continue
		}
		records = append(records, sourceRecord{path: path, data: data, rec: rec})
	}

	superseded := map[string]bool{}
	for _, r := range records {
		for _, s := range r.rec.Supersedes {
			superseded[s] = true
		}
	}

	var toStage []string
	for _, r := range records {
		if superseded[r.rec.ID] {
			continue // a correction replaces it; the correction is copied instead, if it directly covers coveredIDs
		}
		if !recordDirectlyCoversAny(r.rec, coveredIDs) {
			continue
		}
		full := filepath.Join(root, r.path)
		if _, err := os.Stat(full); err == nil {
			continue // already present: not duplicated
		}
		if err := writeExohubRecordFile(root, r.path, r.data); err != nil {
			return fmt.Errorf("copy source record %s: %w", r.path, err)
		}
		toStage = append(toStage, r.path)
	}
	if len(toStage) == 0 {
		return nil
	}
	args := append([]string{"-c", "annex.largefiles=nothing", "add", "--"}, toStage...)
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("stage copied source records: %w\n%s", err, out)
	}
	return nil
}

// recordDirectlyCoversAny reports whether rec documents the generation of
// one of coveredIDs: a @graph node whose @id is covered and which states
// wasGeneratedBy (design D4 Q3 "closest level only": the activity's own
// inputs come along as part of the same record).
func recordDirectlyCoversAny(rec *lib.Record, coveredIDs map[string]bool) bool {
	for _, n := range rec.Graph {
		if !coveredIDs[n.ID()] {
			continue
		}
		if _, ok := n["wasGeneratedBy"]; ok {
			return true
		}
	}
	return false
}
