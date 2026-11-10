// Package exohub parses and resolves `exohub:` references (design D1 of
// openspec/changes/add-exohub-asset-import): `exo add exohub:...` reuses an
// asset, folder, whole project, or named collection from another ExoHub
// project, pinned to a version.
package exohub

import (
	"fmt"
	"strings"
)

// Scheme is the fixed reference prefix `exo add` recognizes alongside local
// paths and http/https/ftp/magnet URLs.
const Scheme = "exohub:"

// Reference is a parsed `exohub:` reference (design D1):
//
//	exohub:<project>[:<path>]@<version>                 # asset, or whole project when <path> is omitted
//	exohub:<project>:collection:<name>@<version>        # collection defined in <project>
type Reference struct {
	// Raw is the original reference string, as given on the command line.
	Raw string
	// Project is the catalog project id.
	Project string
	// Version is the catalog version (e.g. "main", "v1"). Always non-empty.
	Version string
	// Path is the asset or folder path, repo-root-relative. Empty for a
	// whole-project or collection reference. A folder path ends in "/".
	Path string
	// CollectionName is non-empty for a `:collection:<name>` reference.
	CollectionName string
}

// IsFolder reports whether Path names a folder (design D1: a path ending
// in "/" imports a folder collection).
func (r *Reference) IsFolder() bool {
	return r.Path != "" && strings.HasSuffix(r.Path, "/")
}

// IsCollection reports whether the reference names a collection defined in
// Project.
func (r *Reference) IsCollection() bool {
	return r.CollectionName != ""
}

// IsProject reports whether the reference names a whole project version
// (no path, no collection).
func (r *Reference) IsProject() bool {
	return r.Path == "" && r.CollectionName == ""
}

// IsAsset reports whether the reference names a single file (a path not
// ending in "/").
func (r *Reference) IsAsset() bool {
	return r.Path != "" && !r.IsFolder()
}

// CatalogID returns the catalog identifier for an asset or folder reference
// (the `_extra.id` form, `<project>:<path>@<version>`, design D1 D4).
func (r *Reference) CatalogID() string {
	if r.Path == "" {
		return fmt.Sprintf("%s@%s", r.Project, r.Version)
	}
	return fmt.Sprintf("%s:%s@%s", r.Project, r.Path, r.Version)
}

// IsReference reports whether s looks like an `exohub:` reference, so
// callers can route it separately from local paths and URLs before
// parsing it in full.
func IsReference(s string) bool {
	return strings.HasPrefix(s, Scheme)
}

// ParseReference parses s as an `exohub:` reference (design D1). `@<version>`
// is mandatory; a reference missing it is rejected rather than defaulting to
// "latest".
func ParseReference(s string) (*Reference, error) {
	if !strings.HasPrefix(s, Scheme) {
		return nil, fmt.Errorf("not an exohub reference: %q", s)
	}
	rest := strings.TrimPrefix(s, Scheme)
	if rest == "" {
		return nil, fmt.Errorf("exohub reference %q is missing a project", s)
	}

	atIdx := strings.LastIndex(rest, "@")
	if atIdx < 0 {
		return nil, fmt.Errorf("exohub reference %q is missing the mandatory @<version>", s)
	}
	version := rest[atIdx+1:]
	if version == "" {
		return nil, fmt.Errorf("exohub reference %q has an empty version", s)
	}
	head := rest[:atIdx]
	if head == "" {
		return nil, fmt.Errorf("exohub reference %q is missing a project", s)
	}

	project, remainder, hasColon := strings.Cut(head, ":")
	if project == "" {
		return nil, fmt.Errorf("exohub reference %q is missing a project", s)
	}

	ref := &Reference{Raw: s, Project: project, Version: version}

	if !hasColon {
		return ref, nil // whole project
	}
	if remainder == "" {
		return nil, fmt.Errorf("exohub reference %q has an empty path after %q", s, project+":")
	}
	if name, ok := strings.CutPrefix(remainder, "collection:"); ok {
		if name == "" {
			return nil, fmt.Errorf("exohub reference %q is missing a collection name", s)
		}
		ref.CollectionName = name
		return ref, nil
	}

	ref.Path = remainder
	return ref, nil
}
