package tui

import (
	"encoding/json"
	"strconv"
	"time"
)

// Domain types for the TUI. These are the types that the TUI owns as part of
// its interface contract. Consumers (adb, exo) adapt their internal types
// to these.

// PermissionsBody represents access control for an artifact.
type PermissionsBody struct {
	ReadAccess  string   `json:"read_access"`
	WriteAccess string   `json:"write_access"`
	Scope       string   `json:"scope"`
	Owners      []string `json:"owners"`
	Viewers     []string `json:"viewers"`
}

// FlexibleInt64 handles JSON values that may be int or string.
type FlexibleInt64 int64

func (f *FlexibleInt64) UnmarshalJSON(data []byte) error {
	var intValue int64
	if err := json.Unmarshal(data, &intValue); err == nil {
		*f = FlexibleInt64(intValue)
		return nil
	}

	var stringValue string
	if err := json.Unmarshal(data, &stringValue); err == nil {
		parsedValue, err := strconv.ParseInt(stringValue, 10, 64)
		if err != nil {
			*f = 0
			return nil
		}
		*f = FlexibleInt64(parsedValue)
		return nil
	}

	*f = 0
	return nil
}

// TenantInfo identifies the tenant a document belongs to.
// Present only on multi-tenant ArtifactDB instances.
type TenantInfo struct {
	Alias string `json:"alias"`
	Path  string `json:"path"`
}

// Extra contains the _extra metadata common to all ArtifactDB documents.
type Extra struct {
	Schema      string          `json:"$schema"`
	ID          string          `json:"id"`
	ProjectID   string          `json:"project_id"`
	Version     string          `json:"version"`
	Permissions PermissionsBody `json:"permissions"`
	MetaIndexed time.Time       `json:"meta_indexed"`
	Uploaded    time.Time       `json:"uploaded"`
	Latest      bool            `json:"latest"`
	FileSize    FlexibleInt64   `json:"file_size"`
	Tenant      *TenantInfo     `json:"tenant,omitempty"`
}

// SearchResult represents a single search hit.
type SearchResult struct {
	Extra
	Highlights map[string][]string
	Metadata   map[string]interface{}
	Next       string
	Error      string
}

// UnmarshalJSON implements custom unmarshalling for SearchResult.
func (sr *SearchResult) UnmarshalJSON(data []byte) error {
	var tempMap map[string]interface{}
	if err := json.Unmarshal(data, &tempMap); err != nil {
		return err
	}

	if extraData, ok := tempMap["_extra"]; ok {
		extraJSON, err := json.Marshal(extraData)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(extraJSON, &sr.Extra); err != nil {
			return err
		}
		delete(tempMap, "_extra")
	}

	if highlightsData, ok := tempMap["_highlight"]; ok {
		highlightsJSON, err := json.Marshal(highlightsData)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(highlightsJSON, &sr.Highlights); err != nil {
			return err
		}
		delete(tempMap, "_highlight")
	}

	sr.Metadata = tempMap
	return nil
}

// SearchResponse represents a page of search results.
type SearchResponse struct {
	Results []SearchResult `json:"results"`
	Count   int64          `json:"count"`
	Total   int64          `json:"total"`
	Next    string         `json:"next"`
	Error   string
}

// PersistSearchParams holds search query parameters for persistence.
// This matches the format used by the ContextProvider interface.
type PersistSearchParams struct {
	Q      string `json:"q"`
	Fields string `json:"fields"`
	Size   int    `json:"size"`
	Sort   string `json:"sort"`
}

// Context represents an ArtifactDB instance context.
type Context struct {
	Id   string `yaml:"id"`
	Name string `yaml:"name"`
	Url  string `yaml:"url"`
}

// InstanceInfo holds information about an ArtifactDB instance.
type InstanceInfo struct {
	Name string `json:"name"`
}

// NameValuePair is a generic name/value config entry.
type NameValuePair struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}

// SearchUIConfig holds search-related UI configuration.
type SearchUIConfig struct {
	Fields []NameValuePair `yaml:"fields"`
	Latest []NameValuePair `yaml:"latest"`
	Query  []NameValuePair `yaml:"query"`
	Sort   []NameValuePair `yaml:"sort"`
	Size   []NameValuePair `yaml:"size"`
}

// ArtifactUIConfig defines how an artifact type is rendered.
type ArtifactUIConfig struct {
	Schema       string `yaml:"schema"`
	TemplatePath string `yaml:"template_path" mapstructure:"template_path"`
	TemplateFile string `yaml:"template_file" mapstructure:"template_file"`
}

// ItemUIConfig defines how a search result item is rendered.
type ItemUIConfig struct {
	Schema      string `yaml:"schema"`
	Author      string `yaml:"author"`
	Date        string `yaml:"date"`
	Description string `yaml:"description"`
	Icon        string `yaml:"icon"`
	Link        string `yaml:"link"`
	Title       string `yaml:"title"`
	Project     string `yaml:"project"`
	Version     string `yaml:"version"`
	Path        string `yaml:"path"`
	Access      string `yaml:"access"`
}

// ViewsUIConfig holds artifact and item view configurations.
type ViewsUIConfig struct {
	Artifacts []ArtifactUIConfig `yaml:"artifacts"`
	Items     []ItemUIConfig     `yaml:"items"`
}

// UIConfig is the top-level UI configuration.
type UIConfig struct {
	Search SearchUIConfig `yaml:"search"`
	Views  ViewsUIConfig  `yaml:"views"`
}

// UICLIConfig wraps UIConfig for YAML parsing of the CLI config file.
type UICLIConfig struct {
	UI UIConfig `yaml:"ui"`
}

// UITypes represents the available UI type configs from the /ui endpoint.
type UITypes struct {
	CLI UITypeConfig `json:"cli"`
}

// UITypeConfig represents a single UI type configuration.
type UITypeConfig struct {
	Description string `json:"description"`
	Path        string `json:"path"`
}

// ResponseWrapper wraps an HTTP response for convenient parsing.
type ResponseWrapper struct {
	Body []byte
	Err  error
}

// AsJSON unmarshals the response body into the given value.
func (rw *ResponseWrapper) AsJSON(v interface{}) *ResponseWrapper {
	if rw.Err != nil {
		return rw
	}
	err := json.Unmarshal(rw.Body, v)
	if err != nil {
		rw.Err = err
	}
	return rw
}
