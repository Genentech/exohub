package provenance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
)

// URN prefixes for content-addressed and agent identifiers (design D5,
// D1 of add-explicit-collections for CollectionURNPrefix).
const (
	AnnexURNPrefix         = "urn:exohub:annex:"
	GitBlobURNPrefix       = "urn:exohub:git:blob:"
	GitTreeURNPrefix       = "urn:exohub:git:tree:"
	GitCommitURNPrefix     = "urn:exohub:git:commit:"
	RecordURNPrefix        = "urn:exohub:record:"
	ActivityURNPrefix      = "urn:exohub:activity:"
	PersonAgentURNPrefix   = "urn:exohub:agent:person:"
	SoftwareAgentURNPrefix = "urn:exohub:agent:software:"
	CollectionURNPrefix    = "urn:exohub:collection:sha256:"
	UsageURNPrefix         = "urn:exohub:usage:"
	GenerationURNPrefix    = "urn:exohub:generation:"
	ValueURNPrefix         = "urn:exohub:value:sha256:"
)

// AnnexEntityID returns the identifier for content stored in git-annex
// under key (design D5).
func AnnexEntityID(key string) string {
	return AnnexURNPrefix + key
}

// GitBlobEntityID returns the identifier for a git-tracked file with blob
// object id oid (design D5).
func GitBlobEntityID(oid string) string {
	return GitBlobURNPrefix + oid
}

// GitTreeEntityID returns the identifier for a folder collection with tree
// object id oid (design D13).
func GitTreeEntityID(oid string) string {
	return GitTreeURNPrefix + oid
}

// GitCommitID returns the identifier for a commit referenced by a record
// (never the record's own commit; design D5).
func GitCommitID(sha string) string {
	return GitCommitURNPrefix + sha
}

// PersonAgentID returns the identifier for a Person agent, built from the
// exo login token's preferred_username claim (the unix id; design D5).
func PersonAgentID(unixID string) string {
	return PersonAgentURNPrefix + unixID
}

// SoftwareAgentID returns the identifier for a SoftwareAgent agent (a tool,
// workflow engine or AI agent name; design D5).
func SoftwareAgentID(name string) string {
	return SoftwareAgentURNPrefix + name
}

// CollectionID returns the identifier for a named collection whose current
// members have the given content ids (design D1 of add-explicit-collections):
// the lowercase hex SHA-256 digest of every member's own content id,
// sorted lexicographically and joined with "\n". Sorting and a fixed
// digest algorithm make the id deterministic regardless of declaration
// order or which exo process computes it; memberIDs is not mutated.
func CollectionID(memberIDs []string) string {
	sorted := append([]string(nil), memberIDs...)
	sort.Strings(sorted)
	digest := sha256.Sum256([]byte(strings.Join(sorted, "\n")))
	return CollectionURNPrefix + hex.EncodeToString(digest[:])
}

// NewRecordID returns a fresh record identifier using a UUIDv7 (design D5).
func NewRecordID() string {
	return RecordURNPrefix + uuid.Must(uuid.NewV7()).String()
}

// NewActivityID returns a fresh activity identifier using a UUIDv7 (design
// D5).
func NewActivityID() string {
	return ActivityURNPrefix + uuid.Must(uuid.NewV7()).String()
}

// NewUsageID returns a fresh qualifiedUsage node identifier using a UUIDv7
// (design D2 "Roles via qualified usage and generation").
func NewUsageID() string {
	return UsageURNPrefix + uuid.Must(uuid.NewV7()).String()
}

// NewGenerationID returns a fresh qualifiedGeneration node identifier using
// a UUIDv7 (design D2 "Roles via qualified usage and generation").
func NewGenerationID() string {
	return GenerationURNPrefix + uuid.Must(uuid.NewV7()).String()
}

// ValueEntityID returns the deterministic id of a parameter or result value
// entity (design D3 "Parameters and results as value entities"):
// urn:exohub:value:sha256 of the role name and the value's canonical JSON,
// so identical parameters across records dedupe in the compiled graph.
func ValueEntityID(role string, value any) (string, error) {
	canonical, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("marshal value: %w", err)
	}
	digest := sha256.Sum256(append([]byte(role+"\x00"), canonical...))
	return ValueURNPrefix + hex.EncodeToString(digest[:]), nil
}

// RecordUUID extracts the UUID from a record identifier
// (urn:exohub:record:<uuid>), and reports whether id has that form and the
// UUID is valid.
func RecordUUID(id string) (uuid.UUID, bool) {
	rest, ok := strings.CutPrefix(id, RecordURNPrefix)
	if !ok {
		return uuid.UUID{}, false
	}
	u, err := uuid.Parse(rest)
	if err != nil {
		return uuid.UUID{}, false
	}
	return u, true
}

// IsUUIDv7 reports whether id is a record identifier
// (urn:exohub:record:<uuid>) whose UUID is version 7 (design D5). Callers
// that need to report a malformed or non-UUIDv7 record id precisely (for
// example `exo provenance validate`) can use this instead of the looser
// prefix-and-pattern check Parse performs.
func IsUUIDv7(id string) bool {
	u, ok := RecordUUID(id)
	return ok && u.Version() == 7
}
