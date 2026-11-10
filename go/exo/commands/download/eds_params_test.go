//go:build artifactdb

package download

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseEDSParams_InlineObject(t *testing.T) {
	got, err := parseEDSParams(`{"foo": "bar", "n": 7}`)
	if err != nil {
		t.Fatalf("parseEDSParams() error = %v", err)
	}
	if got["foo"] != "bar" {
		t.Errorf("foo = %v", got["foo"])
	}
	if n, ok := got["n"].(json.Number); !ok || n.String() != "7" {
		t.Errorf("n = %v (%T), want json.Number(7)", got["n"], got["n"])
	}
}

func TestParseEDSParams_FileReference(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opts.json")
	if err := os.WriteFile(path, []byte(`{"foo": "bar"}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := parseEDSParams("@" + path)
	if err != nil {
		t.Fatalf("parseEDSParams() error = %v", err)
	}
	if got["foo"] != "bar" {
		t.Errorf("foo = %v", got["foo"])
	}
}

func TestParseEDSParams_MissingFile(t *testing.T) {
	_, err := parseEDSParams("@/no/such/file.json")
	if err == nil {
		t.Fatal("parseEDSParams() expected error for missing file")
	}
}

func TestParseEDSParams_NonObjectRejected(t *testing.T) {
	cases := []string{`["a", "b"]`, `"a string"`, `42`, `true`, `null`}
	for _, c := range cases {
		if _, err := parseEDSParams(c); err == nil {
			t.Errorf("parseEDSParams(%q) expected error, got none", c)
		} else if !strings.Contains(err.Error(), "JSON object") {
			t.Errorf("parseEDSParams(%q) error = %v, want mention of JSON object", c, err)
		}
	}
}

func TestParseEDSParams_InvalidJSONRejected(t *testing.T) {
	if _, err := parseEDSParams(`{not valid`); err == nil {
		t.Fatal("parseEDSParams() expected error for invalid JSON")
	}
}

func TestParseEDSParams_TrailingGarbageRejected(t *testing.T) {
	if _, err := parseEDSParams(`{"content": false} junk`); err == nil {
		t.Fatal("parseEDSParams() expected error for trailing garbage after the JSON object")
	}
}

func TestParseEDSParams_ConcatenatedObjectsRejected(t *testing.T) {
	_, err := parseEDSParams(`{"content": false}{"force": true}`)
	if err == nil {
		t.Fatal("parseEDSParams() expected error for a second concatenated JSON object")
	}
}

func TestParseEDSParams_FileWithTrailingNewlineAccepted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opts.json")
	if err := os.WriteFile(path, []byte("{\"foo\": \"bar\"}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := parseEDSParams("@" + path)
	if err != nil {
		t.Fatalf("parseEDSParams() error = %v, want trailing newline accepted", err)
	}
	if got["foo"] != "bar" {
		t.Errorf("foo = %v", got["foo"])
	}
}

func TestParseEDSParams_ReservedKeysRejected(t *testing.T) {
	for _, key := range edsReservedParamKeys {
		body := `{"` + key + `": "x"}`
		_, err := parseEDSParams(body)
		if err == nil || !strings.Contains(err.Error(), key) {
			t.Errorf("parseEDSParams(%q) error = %v, want %q rejection", body, err, key)
		}
	}
}

func TestCheckEDSParamConflicts_NoOverlap(t *testing.T) {
	err := checkEDSParamConflicts(
		map[string]any{"future_option": "x"},
		map[string]bool{"content": true},
	)
	if err != nil {
		t.Errorf("checkEDSParamConflicts() error = %v, want nil", err)
	}
}

func TestCheckEDSParamConflicts_Overlap(t *testing.T) {
	err := checkEDSParamConflicts(
		map[string]any{"content": true},
		map[string]bool{"content": true},
	)
	if err == nil || !strings.Contains(err.Error(), "content") {
		t.Errorf("checkEDSParamConflicts() error = %v, want content conflict", err)
	}
}
