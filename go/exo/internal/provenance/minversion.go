package provenance

import (
	"fmt"
	"strconv"
	"strings"
)

// firstMinVersionRelease is the exo version this change ships in: the
// first release that understands the v1 context terms added after v1's
// original release (design D1, task 2.2's "term -> introduced-in version"
// table).
const firstMinVersionRelease = "0.143.0"

// termIntroducedIn maps every v1 context term added after v1's first
// release to the exo version that introduced it (design D1). A term
// absent from this table is an original v1 term and needs no
// exo:minVersion.
var termIntroducedIn = map[string]string{
	"qualifiedUsage":      firstMinVersionRelease,
	"Usage":               firstMinVersionRelease,
	"qualifiedGeneration": firstMinVersionRelease,
	"Generation":          firstMinVersionRelease,
	"entity":              firstMinVersionRelease,
	"activity":            firstMinVersionRelease,
	"hadRole":             firstMinVersionRelease,
	"value":               firstMinVersionRelease,
	"exo:checksum":        firstMinVersionRelease,
}

// ComputeMinVersion returns the exo:minVersion a record using graph must
// carry (design D1, task 2.2's "writer helper computing exo:minVersion"):
// the highest introduced-in version among the node @types and property
// keys graph uses that appear in termIntroducedIn. ok is false when graph
// uses only original v1 terms, in which case no exo:minVersion is needed.
func ComputeMinVersion(graph []Node) (version string, ok bool) {
	for _, n := range graph {
		for _, typ := range n.Types() {
			if v, found := termIntroducedIn[typ]; found {
				version, ok = maxVersion(version, ok, v)
			}
		}
		for key := range n {
			if key == "@id" || key == "@type" {
				continue
			}
			if v, found := termIntroducedIn[key]; found {
				version, ok = maxVersion(version, ok, v)
			}
		}
	}
	return version, ok
}

func maxVersion(current string, haveCurrent bool, candidate string) (string, bool) {
	if !haveCurrent || compareSemver(candidate, current) > 0 {
		return candidate, true
	}
	return current, haveCurrent
}

// compareSemver compares two bare major.minor.patch versions (design D1:
// "exo:minVersion is compared as semver"), ignoring any
// "-prerelease+build" suffix. It returns -1 if a < b, 0 if equal, 1 if
// a > b.
func compareSemver(a, b string) int {
	ap, bp := semverParts(a), semverParts(b)
	for i := 0; i < 3; i++ {
		if ap[i] != bp[i] {
			if ap[i] < bp[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

func semverParts(v string) [3]int {
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	var parts [3]int
	fields := strings.SplitN(v, ".", 3)
	for i := 0; i < len(fields) && i < 3; i++ {
		n, _ := strconv.Atoi(fields[i])
		parts[i] = n
	}
	return parts
}

// IsDevVersion reports whether version is a development build that reads
// every record regardless of exo:minVersion (design D1: "Development
// builds (0.0.0-dev) read everything and warn").
func IsDevVersion(version string) bool {
	return version == "" || version == "dev" || strings.HasPrefix(version, "0.0.0") || strings.HasPrefix(version, "dev-")
}

// NeedsUpgrade reports whether a record whose exo:minVersion is minVersion
// is refused by a reader running runningVersion (design "Readers refuse
// records that need a newer exo"): minVersion is set, runningVersion is
// not a development build, and minVersion compares higher.
func NeedsUpgrade(minVersion, runningVersion string) bool {
	if minVersion == "" || IsDevVersion(runningVersion) {
		return false
	}
	return compareSemver(minVersion, runningVersion) > 0
}

// MinVersionMessage renders the upgrade message a reader reports for a
// record whose exo:minVersion exceeds the running exo version (spec
// "Readers refuse records that need a newer exo").
func MinVersionMessage(minVersion string) string {
	return fmt.Sprintf("this record needs exo \u2265 %s: run `exo upgrade`", minVersion)
}

// CheckMinVersion returns an error naming rec when rec's exo:minVersion is
// refused by runningVersion (NeedsUpgrade), or nil otherwise.
func CheckMinVersion(rec *Record, runningVersion string) error {
	if !NeedsUpgrade(rec.MinVersion, runningVersion) {
		return nil
	}
	return fmt.Errorf("record %s: %s", rec.ID, MinVersionMessage(rec.MinVersion))
}

// RecordVersion is one provenance record's path, @id and declared
// exo:minVersion, as returned by LoadRecordVersions for the "readers
// refuse records that need a newer exo" check.
type RecordVersion struct {
	Path       string
	ID         string
	MinVersion string
}

// LoadRecordVersions reads every record at reader's checked state, like
// LoadRecords (silently skipping an annexed or malformed file), and
// returns each one's path, @id and exo:minVersion, for commands (exo
// provenance show, exo provenance validate) that need to check the running
// exo version against every record without re-deriving parsedRecord's
// unexported fields themselves.
func LoadRecordVersions(reader *Reader) ([]RecordVersion, error) {
	parsed, err := LoadRecords(reader)
	if err != nil {
		return nil, err
	}
	out := make([]RecordVersion, len(parsed))
	for i, p := range parsed {
		out[i] = RecordVersion{Path: p.path, ID: p.rec.ID, MinVersion: p.rec.MinVersion}
	}
	return out, nil
}
