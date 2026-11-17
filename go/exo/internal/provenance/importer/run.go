// Package importer implements the format-neutral import pipeline (design
// D7 of openspec/changes/extend-provenance-import): a format adapter reads
// a Run, a Matcher resolves its files against the repository, and a
// Writer turns the result into an ExoHub provenance Record. Format
// adapters (for example Nextflow) build a Run; everything downstream of
// that is format-neutral.
package importer

import "time"

// Checksum is a content digest recorded by a workflow engine for a file
// (design D7).
type Checksum struct {
	// Algorithm is the engine's name for the hash, for example "sha256" or
	// "md5". exo can only verify algorithms it can itself compute.
	Algorithm string
	Value     string
}

// File is one input or output of a run or task (design D7). Path is set
// when the engine recorded a path local to the run's working directory
// (which the Matcher resolves against the repository); URL is set for a
// file the engine itself recorded as external (not a local path). Exactly
// one of Path or URL is normally set.
type File struct {
	Path           string
	URL            string
	Size           int64
	Checksum       Checksum
	Role           string
	ProducedByTask string
}

// Value is a named parameter or result value (design D7, "value entities").
type Value struct {
	Name  string
	Value any
}

// Environment is the container or conda environment a task ran in (design
// D4 "Environment as an input"). Kind is "container" or "conda", derived
// by the adapter from the IRI scheme; ID is the digest IRI (container) or
// content/hash id (conda); Label is a human-readable tag or name, recorded
// for readability only (never used as the identifier).
type Environment struct {
	Kind  string
	ID    string
	Label string
}

// Task is one step of a run (design D7).
type Task struct {
	Name         string
	Started      time.Time
	Ended        time.Time
	CodeChecksum string
	Inputs       []File
	Outputs      []File
	Params       []Value
	// Results are non-file outputs (design D7, spec "qualified generations ...
	// for non-file outputs (value entities)").
	Results     []Value
	Environment *Environment
}

// Pipeline is the workflow code a run executed (design D5 "Step code as a
// pointer"). Local reports whether the pipeline ran from a local
// checkout rather than a resolvable repository/commit.
type Pipeline struct {
	Name       string
	Checksum   string
	Repository string
	Commit     string
	URL        string
	Local      bool
}

// Engine is the workflow engine that produced a run (design D7).
type Engine struct {
	Name    string
	Version string
}

// SourceDocument is one of a run's source lineage documents, kept as
// evidence with --keep-source (design D7).
type SourceDocument struct {
	// Name is the file name the document is stored under, relative to the
	// run's sources directory.
	Name    string
	Content []byte
}

// Run is the format-neutral shape every import adapter produces (design
// D7). ID is the run's identifier as the engine or adapter names it (for
// example a "lid://..." lineage id), used for hadPrimarySource and
// re-import detection.
type Run struct {
	ID              string
	Name            string
	Started         time.Time
	Ended           time.Time
	Engine          Engine
	Pipeline        Pipeline
	Params          []Value
	Environments    []Environment
	Tasks           []Task
	SourceDocuments []SourceDocument
}
