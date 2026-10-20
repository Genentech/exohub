package provenance

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

//go:embed schema/record.schema.json
var recordSchemaDocument []byte

// RecordSchemaDocument returns a copy of the embedded JSON Schema for the
// compact record profile (design D2).
func RecordSchemaDocument() []byte {
	out := make([]byte, len(recordSchemaDocument))
	copy(out, recordSchemaDocument)
	return out
}

var compiledRecordSchema *jsonschema.Schema

func recordSchema() (*jsonschema.Schema, error) {
	if compiledRecordSchema != nil {
		return compiledRecordSchema, nil
	}
	compiler := jsonschema.NewCompiler()
	compiler.Draft = jsonschema.Draft7
	if err := compiler.AddResource("record.schema.json", bytes.NewReader(recordSchemaDocument)); err != nil {
		return nil, fmt.Errorf("load embedded provenance record schema: %w", err)
	}
	schema, err := compiler.Compile("record.schema.json")
	if err != nil {
		return nil, fmt.Errorf("compile embedded provenance record schema: %w", err)
	}
	compiledRecordSchema = schema
	return compiledRecordSchema, nil
}

// ValidateSchema checks raw record JSON bytes against the compact record
// profile's JSON Schema. It returns the flattened list of schema validation
// messages; a nil, empty slice means the document conforms. A non-nil error
// means data was not valid JSON or the embedded schema itself failed to
// compile.
func ValidateSchema(data []byte) ([]string, error) {
	var doc any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	schema, err := recordSchema()
	if err != nil {
		return nil, err
	}
	if err := schema.Validate(doc); err != nil {
		if validationErr, ok := err.(*jsonschema.ValidationError); ok {
			return flattenSchemaErrors(validationErr), nil
		}
		return nil, err
	}
	return nil, nil
}

func flattenSchemaErrors(err *jsonschema.ValidationError) []string {
	if err == nil {
		return nil
	}
	if len(err.Causes) == 0 {
		return []string{err.Message}
	}
	var out []string
	for _, cause := range err.Causes {
		out = append(out, flattenSchemaErrors(cause)...)
	}
	return out
}
