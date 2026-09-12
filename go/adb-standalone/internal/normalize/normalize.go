// Package normalize implements the 3-bucket response normalization spec from
// docs/adb-standalone/design-testing.md §3.
//
// 3a  VOLATILE — drop entirely (server provenance, non-deterministic)
// 3b  ENV-DEPENDENT — normalize environment-specific parts, keep structure
// 3c  KEEP EXACT — everything else, including git/content timestamps
package normalize

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// volatile3a lists _extra fields that are server-provenance and must be dropped.
var volatile3a = map[string]bool{
	"index_name":    true,
	"meta_indexed":  true,
	"meta_uploaded": true,
	"uploaded":      true,
}

// presignedParams lists AWS presigned URL query params to strip (bucket 3a).
var presignedParams = []string{
	"X-Amz-Algorithm", "X-Amz-Credential", "X-Amz-Date",
	"X-Amz-Expires", "X-Amz-Security-Token", "X-Amz-Signature",
	"X-Amz-SignedHeaders", "Expires", "Signature",
}

// scrollPathRe matches /scroll/<token> — we normalise the token to a sentinel.
var scrollPathRe = regexp.MustCompile(`^/scroll/[^/]+$`)

// Response normalises a raw JSON response body according to the 3-bucket spec.
// The input must be a valid JSON object or array.
// Returns canonical JSON (keys sorted, normalised values).
func Response(raw []byte) ([]byte, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("normalize: unmarshal: %w", err)
	}
	norm := normalizeAt(v, 0)
	return marshalCanonical(norm)
}

// normalizeValue recursively applies normalisation rules (not at root).
func normalizeValue(v any) any {
	return normalizeAt(v, 1) // depth > 0: root-only drops don't apply
}

// normalizeAt applies normalisation with depth awareness.
// depth==0 is the root object of the HTTP response.
func normalizeAt(v any, depth int) any {
	switch val := v.(type) {
	case map[string]any:
		return normalizeObject(val, depth)
	case []any:
		out := make([]any, len(val))
		for i, elem := range val {
			out[i] = normalizeAt(elem, depth+1)
		}
		return out
	case string:
		return normalizeString(val)
	default:
		return v
	}
}

// rootVolatile lists root-level search/scroll envelope fields whose semantics
// differ between ES and SurrealDB (ES total.value vs SurrealDB COUNT) and must
// be dropped for G1. Only applied at depth==0 to avoid stripping nested fields
// named "total" that are part of the document payload.
var rootVolatile = map[string]bool{
	"total": true,
}

func normalizeObject(m map[string]any, depth int) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		// Drop null values — the reference ArtifactDB service omits null fields; SurrealDB returns them.
		if v == nil {
			continue
		}
		// Drop root-only volatile envelope fields (ES vs SurrealDB semantics).
		if depth == 0 && rootVolatile[k] {
			continue
		}
		// Sort "aggs" arrays by their first value so insertion-order vs query-order
		// differences between the reference ArtifactDB service and SurrealDB don't cause G1 failures.
		if k == "aggs" {
			out[k] = normalizeAggs(v)
			continue
		}
		// Drop top-level "id" when it looks like a SurrealDB RecordID struct
		// (map with "ID" and "Table" keys) — this is a server-provenance field.
		if k == "id" {
			if rid, ok := v.(map[string]any); ok {
				if _, hasTable := rid["Table"]; hasTable {
					continue // 3a: drop SurrealDB record ID struct
				}
			}
		}
		if k == "_extra" {
			out[k] = normalizeExtra(v)
			continue
		}
		if k == "next" {
			out[k] = normalizeNext(v)
			continue
		}
		out[k] = normalizeAt(v, depth+1)
	}
	return out
}

// normalizeExtra applies 3a dropping and 3b normalization inside _extra.
func normalizeExtra(v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	out := make(map[string]any, len(m))
	for k, val := range m {
		if volatile3a[k] {
			continue // 3a: drop
		}
		// Drop null values inside _extra — the reference ArtifactDB service omits null fields.
		if val == nil {
			continue
		}
		if k == "location" {
			out[k] = normalizeLocation(val)
			continue
		}
		// _extra is always nested (depth > 0); no root-volatile drops apply here.
		out[k] = normalizeAt(val, 2)
	}
	return out
}

// normalizeLocation strips bucket names (3b) from location objects.
func normalizeLocation(v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	out := make(map[string]any, len(m))
	for k, val := range m {
		if k == "s3" {
			if s3m, ok := val.(map[string]any); ok {
				s3out := make(map[string]any, len(s3m))
				for sk, sv := range s3m {
					if sk == "bucket" {
						s3out[sk] = "<bucket>" // 3b: normalise
						continue
					}
					s3out[sk] = normalizeValue(sv)
				}
				out[k] = s3out
				continue
			}
		}
		out[k] = normalizeValue(val)
	}
	return out
}

// normalizeAggs sorts the aggs array by its first string value so ordering
// differences between the reference ArtifactDB service and SurrealDB don't cause G1 failures.
func normalizeAggs(v any) any {
	arr, ok := v.([]any)
	if !ok {
		return normalizeValue(v)
	}
	norm := make([]any, len(arr))
	for i, elem := range arr {
		norm[i] = normalizeValue(elem)
	}
	sort.Slice(norm, func(i, j int) bool {
		return aggKey(norm[i]) < aggKey(norm[j])
	})
	return norm
}

func aggKey(v any) string {
	if m, ok := v.(map[string]any); ok {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if len(keys) > 0 {
			return fmt.Sprintf("%v", m[keys[0]])
		}
	}
	return fmt.Sprintf("%v", v)
}

// normalizeNext normalises the scroll-id token inside next (3a).
// /scroll/<any> → /scroll/<scroll-id>
func normalizeNext(v any) any {
	if v == nil {
		return nil
	}
	s, ok := v.(string)
	if !ok {
		return v
	}
	if scrollPathRe.MatchString(s) {
		return "/scroll/<scroll-id>"
	}
	return s
}

// rfc3339ZRe matches timestamps ending in Z (UTC shorthand), with optional fractional seconds.
var rfc3339ZRe = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?)Z$`)

// normalizeString strips presigned URL query params from absolute URL strings (3a/3b)
// and normalises RFC3339 UTC timestamps (Z → +00:00) so server/client format differences
// don't cause false G1 failures.
func normalizeString(s string) string {
	// Normalise RFC3339 timezone: "Z" and "+00:00" are equivalent.
	if m := rfc3339ZRe.FindStringSubmatch(s); m != nil {
		return m[1] + "+00:00"
	}
	if !strings.HasPrefix(s, "http://") && !strings.HasPrefix(s, "https://") {
		return s
	}
	u, err := url.Parse(s)
	if err != nil {
		return s
	}
	q := u.Query()
	changed := false
	for _, p := range presignedParams {
		if q.Has(p) {
			q.Del(p)
			changed = true
		}
	}
	if !changed {
		// Still normalise host for 3b env-dependent URLs.
		u.Host = "<host>"
		u.Scheme = "http"
		return u.String()
	}
	u.RawQuery = q.Encode()
	u.Host = "<host>"
	u.Scheme = "http"
	return u.String()
}

// marshalCanonical produces JSON with sorted object keys.
func marshalCanonical(v any) ([]byte, error) {
	return json.Marshal(canonicalize(v))
}

// canonicalize converts maps to sortedMap so encoding/json outputs sorted keys.
func canonicalize(v any) any {
	switch val := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make(sortedMap, 0, len(val))
		for _, k := range keys {
			out = append(out, kv{k, canonicalize(val[k])})
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, elem := range val {
			out[i] = canonicalize(elem)
		}
		return out
	default:
		return v
	}
}

type kv struct {
	Key   string
	Value any
}

type sortedMap []kv

func (s sortedMap) MarshalJSON() ([]byte, error) {
	var sb strings.Builder
	sb.WriteByte('{')
	for i, pair := range s {
		if i > 0 {
			sb.WriteByte(',')
		}
		keyBytes, err := json.Marshal(pair.Key)
		if err != nil {
			return nil, err
		}
		sb.Write(keyBytes)
		sb.WriteByte(':')
		valBytes, err := json.Marshal(pair.Value)
		if err != nil {
			return nil, err
		}
		sb.Write(valBytes)
	}
	sb.WriteByte('}')
	return []byte(sb.String()), nil
}
