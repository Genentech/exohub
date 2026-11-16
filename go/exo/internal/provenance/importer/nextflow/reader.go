// Package nextflow implements the "nextflow" `exo provenance import` format
// adapter (design D8 of openspec/changes/extend-provenance-import): it
// reads a Nextflow lineage store directly and maps it onto the
// format-neutral importer.Run. The store's on-disk layout is entirely
// private to this package, so a layout change only touches it.
package nextflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Genentech/exohub/go/exo/internal/provenance/importer"
)

const (
	// defaultStoreDir is the lineage store's default location, relative to
	// the current directory (design D8: ".lineage under the launch
	// directory").
	defaultStoreDir = ".lineage"
	// indexFileName is the store's top-level index, declaring the model
	// version and listing its runs.
	indexFileName = "lineage.json"
	// recordsDirName holds one JSON file per record, named after the hex
	// part of its "lid://<hex>" id.
	recordsDirName = "records"
	// supportedVersion is the only lineage model version this adapter
	// understands (design D8).
	supportedVersion = "lineage/v1beta1"
)

// index is the store's lineage.json (design D8: "checks the store's model
// version").
type index struct {
	Version string     `json:"version"`
	Runs    []indexRun `json:"runs"`
}

// indexRun is one run.lineage.json entry, enough to select a run without
// loading its full WorkflowRun record.
type indexRun struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	StartTime time.Time `json:"startTime"`
}

// store reads records from one lineage store directory, keeping a copy of
// every record it reads so Read can return them as the run's source
// documents (spec "Keeping the source lineage", --keep-source).
type store struct {
	dir     string
	sources map[string][]byte // record file name -> content, deduplicated
	order   []string          // sources' keys, in first-read order
}

// Read is the "nextflow" format's adapter entry point (matches
// commands/provenance.Adapter): it opens the lineage store at storeDir
// (defaultStoreDir when empty), checks its model version, selects run by
// "lid://" id or name (the latest run when empty), and maps it onto a
// format-neutral importer.Run. No `nextflow` binary is required.
func Read(storeDir, run string) (importer.Run, error) {
	dir := storeDir
	if dir == "" {
		dir = defaultStoreDir
	}
	s := &store{dir: dir, sources: map[string][]byte{}}

	idx, err := s.readIndex()
	if err != nil {
		return importer.Run{}, err
	}

	selected, err := selectRun(idx, run)
	if err != nil {
		return importer.Run{}, err
	}

	data, typ, err := s.loadRecord(selected.ID)
	if err != nil {
		return importer.Run{}, err
	}
	if typ != "WorkflowRun" {
		return importer.Run{}, fmt.Errorf("%s: expected a WorkflowRun record, got %q", selected.ID, typ)
	}

	mapped, err := s.mapRun(selected.ID, data)
	if err != nil {
		return importer.Run{}, err
	}
	mapped.SourceDocuments = s.sourceDocuments()
	return mapped, nil
}

// readIndex reads and version-checks the store's lineage.json.
func (s *store) readIndex() (index, error) {
	path := filepath.Join(s.dir, indexFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return index{}, fmt.Errorf("no Nextflow lineage store found at %s", s.dir)
		}
		return index{}, fmt.Errorf("read %s: %w", path, err)
	}

	var idx index
	if err := json.Unmarshal(data, &idx); err != nil {
		return index{}, fmt.Errorf("parse %s: %w", path, err)
	}

	if idx.Version != supportedVersion {
		found := idx.Version
		if found == "" {
			found = "(none)"
		}
		return index{}, fmt.Errorf("unsupported Nextflow lineage version %s (supported: %s)", found, supportedVersion)
	}
	return idx, nil
}

// selectRun picks the run named or identified by run (its "lid://" id or
// its name), or the one with the latest start time when run is empty
// (design D8 "A run SHALL be selectable by its lid:// identifier or its
// run name").
func selectRun(idx index, run string) (indexRun, error) {
	if len(idx.Runs) == 0 {
		return indexRun{}, fmt.Errorf("no runs found in the lineage store")
	}
	if run == "" {
		latest := idx.Runs[0]
		for _, r := range idx.Runs[1:] {
			if !r.StartTime.Before(latest.StartTime) {
				latest = r
			}
		}
		return latest, nil
	}
	for _, r := range idx.Runs {
		if r.ID == run || r.Name == run {
			return r, nil
		}
	}
	return indexRun{}, fmt.Errorf("no run %q in the lineage store", run)
}

// recordHeader reads just enough of a record to dispatch on its type.
type recordHeader struct {
	Type string `json:"type"`
}

// loadRecord reads the record referenced by lid ("lid://<hex>"), returns
// its raw JSON plus its "type" discriminator, and keeps a copy of it as a
// source document (spec "Keeping the source lineage").
func (s *store) loadRecord(lid string) (json.RawMessage, string, error) {
	hex, ok := strings.CutPrefix(lid, "lid://")
	if !ok {
		return nil, "", fmt.Errorf("not a lid:// reference: %q", lid)
	}
	name := hex + ".json"
	path := filepath.Join(s.dir, recordsDirName, name)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("read record %s: %w", lid, err)
	}
	var head recordHeader
	if err := json.Unmarshal(data, &head); err != nil {
		return nil, "", fmt.Errorf("parse record %s: %w", lid, err)
	}
	if _, seen := s.sources[name]; !seen {
		s.sources[name] = append([]byte(nil), data...)
		s.order = append(s.order, name)
	}
	return data, head.Type, nil
}

// sourceDocuments returns every record this store read, in the order it
// first read them, as the run's source documents (--keep-source).
func (s *store) sourceDocuments() []importer.SourceDocument {
	if len(s.order) == 0 {
		return nil
	}
	docs := make([]importer.SourceDocument, len(s.order))
	for i, name := range s.order {
		docs[i] = importer.SourceDocument{Name: name, Content: s.sources[name]}
	}
	return docs
}
