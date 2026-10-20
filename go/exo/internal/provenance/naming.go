package provenance

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// recordFileTimeLayout is the UTC timestamp format used in record file
// names (design D3): "20261014T101200Z".
const recordFileTimeLayout = "20060102T150405Z"

// nonSlugChar matches any run of characters not allowed in a slug.
var nonSlugChar = regexp.MustCompile(`[^a-z0-9]+`)

// Slugify lowercases s and replaces every run of non-alphanumeric
// characters with a single "-", trimming leading and trailing "-".
func Slugify(s string) string {
	slug := nonSlugChar.ReplaceAllString(strings.ToLower(s), "-")
	return strings.Trim(slug, "-")
}

// shortIDLen is the number of hex characters from the record UUID used in
// the file name (design D3 example: "7f3a2c").
const shortIDLen = 6

// RecordFileName returns the file name for a record (design D3):
// ".exohub/provenance/<UTC timestamp>-<slug>-<short-id>.jsonld", for
// example "20261014T101200Z-normalize-7f3a2c.jsonld". generatedAt is
// rendered in UTC; recordID must be a urn:exohub:record:<UUIDv7>, as
// returned by NewRecordID.
//
// short-id is the LAST shortIDLen hex characters of the UUID, not the
// first: a UUIDv7 starts with a 48-bit millisecond timestamp, so the first
// few hex characters are identical for every record created within the
// same ~4.6 hour window, giving no uniqueness. The last characters come
// from the UUID's random bits, so two records generated in the same second
// with the same slug still get different file names (review of !260).
func RecordFileName(generatedAt time.Time, slug, recordID string) (string, error) {
	u, ok := RecordUUID(recordID)
	if !ok {
		return "", fmt.Errorf("invalid record id %q", recordID)
	}
	hex := strings.ReplaceAll(u.String(), "-", "")
	shortID := hex[len(hex)-shortIDLen:]
	cleanSlug := Slugify(slug)
	if cleanSlug == "" {
		return "", fmt.Errorf("slug %q has no valid characters", slug)
	}
	timestamp := generatedAt.UTC().Format(recordFileTimeLayout)
	return fmt.Sprintf("%s-%s-%s.jsonld", timestamp, cleanSlug, shortID), nil
}

// RecordDir is the repository-relative directory records live in (design
// D3).
const RecordDir = ".exohub/provenance"

// RecordPath joins RecordDir and the file name returned by RecordFileName.
func RecordPath(generatedAt time.Time, slug, recordID string) (string, error) {
	name, err := RecordFileName(generatedAt, slug, recordID)
	if err != nil {
		return "", err
	}
	return RecordDir + "/" + name, nil
}
