//go:build artifactdb

package download

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// edsReservedParamKeys are --eds-params keys that come from the positional
// dataset argument (project_id, version) or are reserved for a future
// repository@ref addressing scheme (repository, ref). Setting any of them
// via --eds-params is an error: they aren't free-form options.
var edsReservedParamKeys = []string{"project_id", "version", "repository", "ref"}

// parseEDSParams decodes the value of --eds-params into a JSON object.
// raw is either an inline JSON object or, prefixed with "@", a path to a
// file containing one. Numbers decode via json.Number (UseNumber) so large
// integers round-trip without float precision loss. A non-object value or
// invalid JSON is rejected with a clear error.
func parseEDSParams(raw string) (map[string]any, error) {
	data := []byte(raw)
	source := "--eds-params"
	if strings.HasPrefix(raw, "@") {
		path := strings.TrimPrefix(raw, "@")
		source = path
		var err error
		data, err = os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading --eds-params file %s: %w", path, err)
		}
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return nil, fmt.Errorf("parsing %s as JSON: %w", source, err)
	}

	// Reject anything after the JSON object other than whitespace (e.g. a
	// second concatenated object, or trailing garbage) — json.Decoder only
	// decodes the first value, so "{...} junk" or "{...}{...}" would
	// otherwise silently drop everything past the first object, and the
	// user would think a second object's keys (e.g. a trailing "force")
	// were sent when they weren't. A trailing newline (common in files) is
	// still accepted.
	if err := dec.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parsing %s as JSON: unexpected data after the JSON object", source)
	}

	obj, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("--eds-params must be a JSON object, got %s", jsonKind(value))
	}

	for _, reserved := range edsReservedParamKeys {
		if _, present := obj[reserved]; present {
			return nil, fmt.Errorf("--eds-params cannot set %q; it comes from the dataset argument", reserved)
		}
	}

	return obj, nil
}

// checkEDSParamConflicts errors if any key in params was also set by a
// typed flag (or --scope); the issue is ambiguity about which value wins,
// not that the EDS would reject it, so this is checked client-side before
// any request is sent.
func checkEDSParamConflicts(params map[string]any, typedKeys map[string]bool) error {
	var conflicts []string
	for key := range params {
		if typedKeys[key] {
			conflicts = append(conflicts, key)
		}
	}
	if len(conflicts) == 0 {
		return nil
	}
	sort.Strings(conflicts)
	return fmt.Errorf("--eds-params sets %s, which is already set by a flag; remove one", strings.Join(conflicts, ", "))
}

func jsonKind(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "a boolean"
	case json.Number:
		return "a number"
	case string:
		return "a string"
	case []any:
		return "an array"
	default:
		return "that type"
	}
}
