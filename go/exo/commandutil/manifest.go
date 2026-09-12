package commandutil

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v5"
	"gopkg.in/yaml.v3"

	"github.com/Genentech/exohub/go/exo/internal/defaults"
)

// DefaultAPIBase is the resolved ExoHub API base URL.
// It is initialized at startup from EXOHUB_API_URL env or the built-in default.
// External callers that reference this as a string value continue to compile;
// use defaults.APIBase() for new code so env overrides apply at call time.
var DefaultAPIBase = defaults.APIBase()

const initWorkflowTemplate = `# Exo workflow manifest (used by run.py)
name: my-dataset
url: https://github.com/org/repo.git
ref: main
remote-type: annex
repo-dir: /data/work/my-dataset
# with-remotes:
#   - s3-private
# paths:
#   - data/**
# from: s3-private   # required if remote-type is export
# to: gcs-public     # required if remote-type is export
# activities:
#   mirror-plan:
#     manifest: manifests/mirror-plan.yaml
#   link:
#     manifest: manifests/link.yaml
`

func ReadManifest(path string) (map[string]any, error) {
	content, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := yaml.Unmarshal(content, &payload); err != nil {
		return nil, err
	}
	if payload == nil {
		payload = map[string]any{}
	}
	return payload,
		nil
}

func ValidateManifestFile(manifestType, path string) error {
	if manifestType != "sync" && manifestType != "annex" && manifestType != "export" && manifestType != "workflow" && manifestType != "bundle" {
		return fmt.Errorf("invalid manifest type: %s", manifestType)
	}

	payload, err := ReadManifest(path)
	if err != nil {
		return err
	}
	normalized := NormalizeManifest(payload)
	errors := ValidateManifest(manifestType, normalized)
	if len(errors) == 0 {
		return nil
	}
	return fmt.Errorf("Manifest validation errors (%s): %s", manifestType, strings.Join(errors, "; "))
}

func NormalizeManifest(input map[string]any) map[string]any {
	output := map[string]any{}
	setIfPresent(output, "name", input["name"])
	setIfPresent(output, "url", input["url"])
	setIfPresent(output, "ref", input["ref"])
	setIfPresent(output, "from", input["from"])
	setIfPresent(output, "to", input["to"])
	setIfPresent(output, "queue", input["queue"])
	setIfPresent(output, "repo-dir", input["repo-dir"])

	if val, ok := lookup(input, "remote-type"); ok {
		output["remote-type"] = val
	}
	if val, ok := lookup(input, "with-remotes"); ok {
		output["with-remotes"] = asArray(splitList(val))
	}
	if val, ok := lookup(input, "paths"); ok {
		output["paths"] = asArray(val)
	}
	if val, ok := lookup(input, "jobs"); ok {
		output["jobs"] = val
	}
	if val, ok := lookup(input, "activities"); ok {
		output["activities"] = val
	}

	return output
}

func lookup(input map[string]any, keys ...string) (any, bool) {
	for _, key := range keys {
		if val, ok := input[key]; ok {
			return val, true
		}
	}
	return nil, false
}

func setIfPresent(output map[string]any, key string, val any) {
	if val == nil {
		return
	}
	text := strings.TrimSpace(fmt.Sprint(val))
	if text == "" || text == "<nil>" {
		return
	}
	output[key] = val
}

func firstValue(input map[string]any, keys ...string) any {
	for _, key := range keys {
		if val, ok := input[key]; ok {
			return val
		}
	}
	return nil
}

func splitList(val any) any {
	switch v := val.(type) {
	case string:
		return splitStringList(v)
	case []string:
		return toAnySlice(v)
	case []any:
		return v
	default:
		return val
	}
}

func asArray(val any) any {
	switch v := val.(type) {
	case nil:
		return nil
	case []any:
		return v
	case []string:
		return toAnySlice(v)
	default:
		return []any{val}
	}
}

func splitStringList(value string) []any {
	re := regexp.MustCompile(`[\s,]+`)
	parts := re.Split(strings.TrimSpace(value), -1)
	var out []any
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

func toAnySlice(input []string) []any {
	out := make([]any, 0, len(input))
	for _, item := range input {
		out = append(out, item)
	}
	return out
}

func InferManifestType(normalized map[string]any) (string, error) {
	if _, ok := normalized["presets"]; ok {
		return "bundle", nil
	}
	if _, ok := normalized["activities"]; ok {
		return "workflow", nil
	}
	remoteType, present, multi := resolveRemoteType(normalized)
	if !present {
		return "", fmt.Errorf("remote-type is required to infer manifest type")
	}
	if multi {
		return "", fmt.Errorf("remote-type must be a single value ('annex' or 'export')")
	}
	if remoteType != "annex" && remoteType != "export" {
		return "", fmt.Errorf("remote-type must be 'annex' or 'export'")
	}
	return remoteType, nil
}

func ValidateSchema(manifestType, schemaPath string, normalized map[string]any) ([]string, error) {
	if schemaPath != "" {
		data, err := os.ReadFile(filepath.Clean(schemaPath))
		if err != nil {
			return nil, fmt.Errorf("schema not found: %s", schemaPath)
		}
		return ValidateWithSchemaBytes(data, normalized)
	}

	if manifestType == "workflow" {
		return nil, fmt.Errorf("workflow schema is not available; provide --schema")
	}

	schemaData, err := FetchSchema(manifestType)
	if err != nil {
		return nil, err
	}

	return ValidateWithSchemaBytes(schemaData, normalized)
}

func ValidateWithSchemaBytes(schemaData []byte, normalized map[string]any) ([]string, error) {
	compiler := jsonschema.NewCompiler()
	compiler.Draft = jsonschema.Draft7
	if err := compiler.AddResource("schema.json", bytes.NewReader(schemaData)); err != nil {
		return nil, err
	}
	schema, err := compiler.Compile("schema.json")
	if err != nil {
		return nil, err
	}
	if err := schema.Validate(normalized); err != nil {
		if validationErr, ok := err.(*jsonschema.ValidationError); ok {
			return flattenValidationErrors(validationErr), nil
		}
		return nil, err
	}
	return nil, nil
}

func flattenValidationErrors(err *jsonschema.ValidationError) []string {
	if err == nil {
		return nil
	}
	if len(err.Causes) == 0 {
		return []string{err.Message}
	}
	var out []string
	for _, cause := range err.Causes {
		out = append(out, flattenValidationErrors(cause)...)
	}
	return out
}

func FetchSchema(manifestType string) ([]byte, error) {
	schemaName, err := SchemaNameForType(manifestType)
	if err != nil {
		return nil, err
	}
	apiBase := defaults.APIBase()
	schemaURL, err := BuildSchemaURL(apiBase, schemaName)
	if err != nil {
		return nil, err
	}
	DebugHTTP("GET", schemaURL)
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(schemaURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch schema: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			msg = resp.Status
		}
		return nil, fmt.Errorf("schema fetch failed: %s", msg)
	}
	return io.ReadAll(resp.Body)
}

func SchemaNameForType(manifestType string) (string, error) {
	switch manifestType {
	case "annex":
		return "annex-manifest.schema.json", nil
	case "export":
		return "export-manifest.schema.json", nil
	case "bundle":
		return "bundle-manifest.schema.json", nil
	default:
		return "", fmt.Errorf("unsupported manifest type for schema validation: %s", manifestType)
	}
}

func BuildSchemaURL(apiBase, schemaName string) (string, error) {
	base := strings.TrimRight(apiBase, "/")
	endpoint := base + "/schemas/" + schemaName
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("Invalid EXOHUB_API_URL: %s", apiBase)
	}
	return parsed.String(), nil
}

func ValidateManifest(manifestType string, input map[string]any) []string {
	var errors []string
	if val, ok := input["with-remotes"]; ok {
		if !isStringArray(val) {
			errors = append(errors, "with-remotes must be array of strings")
		}
	}
	if val, ok := input["paths"]; ok {
		if !isStringArray(val) {
			errors = append(errors, "paths must be array of strings")
		}
	}
	if val, ok := input["jobs"]; ok {
		if num, ok := intValue(val); !ok || num < 1 {
			errors = append(errors, "jobs must be integer >= 1")
		}
	}
	if val, ok := input["queue"]; ok {
		if queueStr, ok := val.(string); !ok {
			errors = append(errors, "queue must be a string")
		} else {
			if _, ok := (map[string]struct{}{
				"exohub-sync-aws":  {},
				"exohub-sync-shpc": {},
			})[queueStr]; !ok {
				errors = append(errors, fmt.Sprintf("unsupported queue: %s", queueStr))
			}
		}
	}

	normalizedType := manifestType
	if normalizedType == "sync" {
		normalizedType = "annex"
	}
	remoteType, present, multi := resolveRemoteType(input)
	if !present {
		errors = append(errors, "remote-type is required")
	} else if multi {
		errors = append(errors, "remote-type must be a single value ('annex' or 'export')")
	} else if remoteType != "annex" && remoteType != "export" {
		errors = append(errors, "remote-type must be 'annex' or 'export'")
	}

	switch normalizedType {
	case "annex":
		if !hasNonEmpty(input, "name") {
			errors = append(errors, "name is required")
		}
		if !hasNonEmpty(input, "url") {
			errors = append(errors, "url is required")
		}
		if !hasNonEmpty(input, "ref") {
			errors = append(errors, "ref is required")
		}
		if present && !multi && remoteType != "annex" {
			errors = append(errors, "remote-type must be 'annex'")
		}
	case "export":
		if !hasNonEmpty(input, "name") {
			errors = append(errors, "name is required")
		}
		if !hasNonEmpty(input, "url") {
			errors = append(errors, "url is required")
		}
		if !hasNonEmpty(input, "ref") {
			errors = append(errors, "ref is required")
		}
		if !hasNonEmpty(input, "to") {
			errors = append(errors, "to is required")
		}
		if present && !multi && remoteType != "export" {
			errors = append(errors, "remote-type must be 'export'")
		}
	case "workflow":
		if !hasNonEmpty(input, "name") {
			errors = append(errors, "name is required")
		}
		if !hasNonEmpty(input, "url") {
			errors = append(errors, "url is required")
		}
		if !hasNonEmpty(input, "ref") {
			errors = append(errors, "ref is required")
		}
		if present && !multi && remoteType == "export" {
			if !hasNonEmpty(input, "from") || !hasNonEmpty(input, "to") {
				errors = append(errors, "from and to are required when remote-type is export")
			}
		}
	}

	return errors
}

func isStringArray(val any) bool {
	switch v := val.(type) {
	case []string:
		return true
	case []any:
		for _, item := range v {
			if _, ok := item.(string); !ok {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func resolveRemoteType(input map[string]any) (string, bool, bool) {
	val, ok := input["remote-type"]
	if !ok || val == nil {
		return "", false, false
	}
	switch v := val.(type) {
	case string:
		v = strings.TrimSpace(v)
		if v == "" {
			return "", false, false
		}
		return v, true, false
	case []string:
		if len(v) == 1 {
			return strings.TrimSpace(v[0]), true, false
		}
		if len(v) > 1 {
			return "", true, true
		}
		return "", true, false
	case []any:
		if len(v) == 1 {
			if s, ok := v[0].(string); ok {
				return strings.TrimSpace(s), true, false
			}
			return "", true, false
		}
		if len(v) > 1 {
			return "", true, true
		}
		return "", true, false
	default:
		return "", true, false
	}
}

func stringSlice(val any) ([]string, bool) {
	switch v := val.(type) {
	case []string:
		return v, true
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			str, ok := item.(string)
			if !ok {
				return nil, false
			}
			out = append(out, str)
		}
		return out, true
	default:
		return nil, false
	}
}

func arrayLength(val any) (int, bool) {
	switch v := val.(type) {
	case []string:
		return len(v), true
	case []any:
		return len(v), true
	default:
		return 0, false
	}
}

func intValue(val any) (int64, bool) {
	switch v := val.(type) {
	case int:
		return int64(v), true
	case int8:
		return int64(v), true
	case int16:
		return int64(v), true
	case int32:
		return int64(v), true
	case int64:
		return v, true
	case uint:
		return int64(v), true
	case uint8:
		return int64(v), true
	case uint16:
		return int64(v), true
	case uint32:
		return int64(v), true
	case uint64:
		if v > uint64(^uint64(0)>>1) {
			return 0, false
		}
		return int64(v), true
	case float32:
		if v != float32(int64(v)) {
			return 0, false
		}
		return int64(v), true
	case float64:
		if v != float64(int64(v)) {
			return 0, false
		}
		return int64(v), true
	default:
		return 0, false
	}
}

func hasNonEmpty(input map[string]any, key string) bool {
	val, ok := input[key]
	if !ok || val == nil {
		return false
	}
	text := strings.TrimSpace(fmt.Sprint(val))
	return text != "" && text != "<nil>"
}

// ManifestHash computes a 12-character hash of the manifest content.
// This is used for metrics isolation - different manifests get different
// metrics directories. The hash is computed from canonical YAML.
func ManifestHash(manifestPath string) (string, error) {
	payload, err := ReadManifest(manifestPath)
	if err != nil {
		return "", err
	}
	return ManifestHashFromPayload(payload), nil
}

// ManifestHashFromPayload computes hash from already-parsed manifest data.
func ManifestHashFromPayload(payload map[string]any) string {
	canonical := canonicalJSON(payload)
	hash := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(hash[:])[:12]
}

// canonicalJSON produces deterministic JSON output with sorted keys.
// Go's encoding/json sorts map[string]any keys automatically.
// This matches the Python implementation in exohub-workers/manifests.py.
func canonicalJSON(data map[string]any) string {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(data)
	return strings.TrimSuffix(buf.String(), "\n")
}
