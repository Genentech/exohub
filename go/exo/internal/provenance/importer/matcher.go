package importer

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"

	lib "github.com/Genentech/exohub/go/exo/internal/provenance"
)

// Matched is the outcome of resolving one run File to repository content
// (design D7's matcher, spec "Matching run files to repository content").
type Matched struct {
	File File
	// ID is the resolved content identifier (an annex or git blob id) when
	// the file matched repository content, or the external IRI (the
	// engine's path or URL) otherwise.
	ID string
	// External reports whether ID is an external IRI rather than a
	// repository content identifier.
	External bool
	// Verified is "checksum" or "size" when the match was confirmed against
	// the engine's recorded checksum or size, "" when there was nothing to
	// verify against or the file is external.
	Verified string
	// Unmatched, when non-empty, is the reason the file could not be
	// matched to repository content.
	Unmatched string
}

// Matcher resolves run files against one repository's tracked content
// (design D7).
type Matcher struct {
	reader *lib.Reader
}

// NewMatcher returns a Matcher that resolves files against reader's
// checked state.
func NewMatcher(reader *lib.Reader) *Matcher {
	return &Matcher{reader: reader}
}

// Match resolves one File (design D7, spec scenarios "Output matched by
// checksum", "Changed file is not matched", "Input outside the
// repository").
func (m *Matcher) Match(f File) Matched {
	if f.Path == "" {
		return Matched{File: f, ID: f.URL, External: true}
	}
	relPath := strings.TrimPrefix(f.Path, "/")
	id, err := m.reader.ResolveContentID(relPath)
	if err != nil {
		if isUntrackedError(err) {
			return Matched{
				File:      f,
				ID:        externalPathID(f.Path),
				External:  true,
				Unmatched: "untracked; run \"exo add " + relPath + "\" first",
			}
		}
		return Matched{File: f, ID: externalPathID(f.Path), External: true, Unmatched: err.Error()}
	}

	verified, matches := m.verify(relPath, id, f)
	if !matches {
		return Matched{File: f, ID: externalPathID(f.Path), External: true, Unmatched: "content differs from the run"}
	}
	return Matched{File: f, ID: id, Verified: verified}
}

// externalPathID turns a run-local path that could not be matched to
// repository content into an absolute-IRI-shaped external identifier
// (design D7: "referenced by an external IRI"; every record entity id
// must be an absolute IRI or a recognized urn:exohub: kind). path is kept
// verbatim, under the custom "exo-path:" scheme, unless it already looks
// like an IRI.
func externalPathID(path string) string {
	if looksLikeIRI(path) {
		return path
	}
	return "exo-path:" + path
}

// looksLikeIRI reports whether s should be treated as an external
// identifier (a URL or urn:) rather than a bare path.
func looksLikeIRI(s string) bool {
	return strings.Contains(s, "://") || strings.HasPrefix(s, "urn:")
}

// verify checks id's actual size/checksum against what the engine recorded
// for f, when exo can determine both (design D7's matcher): checksum when
// the engine's algorithm is one exo can compute (sha256, md5), size
// otherwise. It returns matches=true when there is nothing to compare
// against, so an unverifiable match is still accepted.
func (m *Matcher) verify(relPath, id string, f File) (verified string, matches bool) {
	algo := strings.ToLower(f.Checksum.Algorithm)
	if (algo == "sha256" || algo == "md5") && f.Checksum.Value != "" {
		if actual, ok := m.actualChecksum(relPath, id, algo); ok {
			if strings.EqualFold(actual, f.Checksum.Value) {
				return "checksum", true
			}
			return "", false
		}
	}
	if f.Size > 0 {
		if actual, ok := m.actualSize(relPath, id); ok {
			if actual == f.Size {
				return "size", true
			}
			return "", false
		}
	}
	return "", true
}

// actualChecksum returns the repository content's digest for algo, using
// the git-annex key's embedded hash when id is an annex key (no content
// read needed), or by hashing the git blob's content otherwise.
func (m *Matcher) actualChecksum(relPath, id, algo string) (string, bool) {
	if key, ok := strings.CutPrefix(id, lib.AnnexURNPrefix); ok {
		info, ok := parseAnnexKey(key)
		if !ok || info.algo != algo || info.hex == "" {
			return "", false
		}
		return info.hex, true
	}
	if strings.HasPrefix(id, lib.GitBlobURNPrefix) {
		data, err := m.reader.ReadFile(relPath)
		if err != nil {
			return "", false
		}
		switch algo {
		case "sha256":
			sum := sha256.Sum256(data)
			return hex.EncodeToString(sum[:]), true
		case "md5":
			sum := md5.Sum(data)
			return hex.EncodeToString(sum[:]), true
		}
	}
	return "", false
}

// actualSize returns the repository content's size in bytes, using the
// git-annex key's embedded size when id is an annex key, or the git blob's
// length otherwise.
func (m *Matcher) actualSize(relPath, id string) (int64, bool) {
	if key, ok := strings.CutPrefix(id, lib.AnnexURNPrefix); ok {
		info, ok := parseAnnexKey(key)
		if !ok || !info.hasSize {
			return 0, false
		}
		return info.size, true
	}
	if strings.HasPrefix(id, lib.GitBlobURNPrefix) {
		data, err := m.reader.ReadFile(relPath)
		if err != nil {
			return 0, false
		}
		return int64(len(data)), true
	}
	return 0, false
}

// annexKeyInfo is the size and hash exo can recover from a git-annex key
// without reading the file's content (most git-annex backends embed both
// in the key itself).
type annexKeyInfo struct {
	hasSize bool
	size    int64
	algo    string // "sha256" or "md5", "" if the backend isn't one of those.
	hex     string
}

// parseAnnexKey extracts the size and, for a sha256/md5-backed key, the
// hash from a git-annex key of the form "<Backend>-s<size>[-...]--<name>"
// (for example "SHA256E-s2048--<64 hex chars>.csv" or
// "MD5-s1024--<32 hex chars>"). The "E" suffix on a backend name means the
// key preserves the file extension in its name field, which does not
// affect the leading hash.
func parseAnnexKey(key string) (annexKeyInfo, bool) {
	idx := strings.Index(key, "--")
	if idx < 0 {
		return annexKeyInfo{}, false
	}
	prefix, name := key[:idx], key[idx+2:]
	fields := strings.Split(prefix, "-")
	if len(fields) == 0 {
		return annexKeyInfo{}, false
	}
	backend := fields[0]

	var info annexKeyInfo
	for _, f := range fields[1:] {
		if rest, ok := strings.CutPrefix(f, "s"); ok {
			if n, err := strconv.ParseInt(rest, 10, 64); err == nil {
				info.size, info.hasSize = n, true
			}
		}
	}

	hashBackend := strings.TrimSuffix(backend, "E")
	switch hashBackend {
	case "SHA256":
		if len(name) >= 64 {
			info.algo, info.hex = "sha256", strings.ToLower(name[:64])
		}
	case "MD5":
		if len(name) >= 32 {
			info.algo, info.hex = "md5", strings.ToLower(name[:32])
		}
	}

	return info, info.hasSize || info.hex != ""
}

// isUntrackedError reports whether err from Reader.ResolveContentID means
// the path is untracked or does not exist at the checked state (spec
// "Untracked repository files SHALL be reported with a hint to run exo add
// first").
func isUntrackedError(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "not tracked in the index") ||
		strings.Contains(msg, "not found") ||
		strings.Contains(msg, "not present at")
}
