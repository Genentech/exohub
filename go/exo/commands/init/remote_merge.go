package init

import (
	"bytes"
	"fmt"
	"os"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

// mergeRemoteFieldsIntoFile updates only remoteName's fields named in
// entries (from rendered) in the real .exohub/remotes file, editing it as a
// yaml.Node document instead of remarshalling the parsed RemotesConfig
// struct. A struct round trip (yaml.Marshal(*RemotesConfig)) only reproduces
// what the struct models: it silently drops comments and any key the
// struct doesn't define (e.g. a hand-added note), and reformats every
// remote's quoting — breaking the "everything else byte-identical"
// guarantee for the whole file, not just the refreshed remote
// (exo-cli#164 review). Editing the existing Node tree and only replacing
// or inserting the changed/added keys' value nodes leaves every other
// node — and its comments, quoting, and order — untouched.
func mergeRemoteFieldsIntoFile(path, remoteName string, rendered RemoteConfig, entries []driftEntry) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", path, err)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("failed to parse %s: %w", path, err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("%s: expected a YAML mapping at the top level", path)
	}
	root := doc.Content[0]

	remotesSeq := yamlMappingValue(root, "remotes")
	if remotesSeq == nil || remotesSeq.Kind != yaml.SequenceNode {
		return fmt.Errorf("%s: no 'remotes' list found", path)
	}

	var target *yaml.Node
	for _, item := range remotesSeq.Content {
		if item.Kind != yaml.MappingNode {
			continue
		}
		if nameNode := yamlMappingValue(item, "name"); nameNode != nil && nameNode.Value == remoteName {
			target = item
			break
		}
	}
	if target == nil {
		return fmt.Errorf("%s: remote %q not found", path, remoteName)
	}

	fieldByName := remoteConfigYAMLFields(rendered)
	for _, e := range entries {
		fv, ok := fieldByName[e.Field]
		if !ok {
			continue
		}
		var value yaml.Node
		if err := value.Encode(fv); err != nil {
			return fmt.Errorf("encode %s: %w", e.Field, err)
		}
		yamlSetMappingValue(target, e.Field, &value)
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return fmt.Errorf("failed to re-encode %s: %w", path, err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("failed to re-encode %s: %w", path, err)
	}

	return os.WriteFile(path, buf.Bytes(), 0644)
}

// remoteConfigYAMLFields returns r's fields keyed by yaml tag name, as
// values suitable for yaml.Node.Encode.
func remoteConfigYAMLFields(r RemoteConfig) map[string]interface{} {
	out := make(map[string]interface{})
	v := reflect.ValueOf(r)
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("yaml")
		name := strings.Split(tag, ",")[0]
		if name == "" || name == "-" {
			continue
		}
		out[name] = v.Field(i).Interface()
	}
	return out
}

// yamlMappingValue returns the value node for key in mapping node m, or nil.
func yamlMappingValue(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// yamlSetMappingValue replaces key's value node in mapping node m if
// present (keeping the existing key node, and its comments, untouched), or
// appends a new key/value pair at the end otherwise.
func yamlSetMappingValue(m *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = value
			return
		}
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	m.Content = append(m.Content, keyNode, value)
}
