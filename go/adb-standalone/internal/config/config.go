package config

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

// Config holds the full adb-standalone configuration.
type Config struct {
	Prefixes       []string          `mapstructure:"prefixes"`
	GPRN           GPRNConfig        `mapstructure:"gprn"`
	Schema         string            `mapstructure:"schema"`
	InternalSchema string            `mapstructure:"internal_schema"`
	Permissions    PermissionsConfig `mapstructure:"permissions"`
	Tenants        []TenantConfig    `mapstructure:"tenants"`
	Storage        StorageConfig     `mapstructure:"storage"`
	Surreal        SurrealConfig     `mapstructure:"surreal"`
	Semantic       SemanticConfig    `mapstructure:"semantic"`
	Server         ServerConfig      `mapstructure:"server"`
}

type GPRNConfig struct {
	Service     string `mapstructure:"service"`
	Environment string `mapstructure:"environment"`
	Placeholder string `mapstructure:"placeholder"`
}

type PermissionsConfig struct {
	DefaultRead  string `mapstructure:"default_read"`
	DefaultWrite string `mapstructure:"default_write"`
}

type TenantConfig struct {
	Path  string `mapstructure:"path"`
	Alias string `mapstructure:"alias"`
}

type StorageConfig struct {
	S3 S3Config `mapstructure:"s3"`
}

type S3Config struct {
	Endpoint               string `mapstructure:"endpoint"`
	PublicEndpoint         string `mapstructure:"public_endpoint"`          // host override for presigned URLs (client-reachable)
	Bucket                 string `mapstructure:"bucket"`
	Region                 string `mapstructure:"region"`
	AccessKeyID            string `mapstructure:"access_key_id"`
	SecretAccessKey        string `mapstructure:"secret_access_key"` //nolint:gosec
	UsePathStyle           bool   `mapstructure:"use_path_style"`
	PresignedURLExpiration int    `mapstructure:"presigned_url_expiration"` // seconds; default 3600
	SignatureVersion       string `mapstructure:"signature_version"`       // default "v4"
	MetaRedirect           bool   `mapstructure:"meta_redirect"`           // return JSON redirect instead of 307
}

// IsManaged reports whether the S3 config requests the embedded versitygw
// object store rather than an external S3/MinIO endpoint.
func (c *S3Config) IsManaged() bool {
	return c.Endpoint == "" || c.Endpoint == "managed" || c.Endpoint == "embedded"
}

type SurrealConfig struct {
	URL      string `mapstructure:"url"`
	NS       string `mapstructure:"ns"`
	DB       string `mapstructure:"db"`
	Username string `mapstructure:"username"`
	Password string `mapstructure:"password"` //nolint:gosec
}

type SemanticConfig struct {
	ModelPath  string `mapstructure:"model_path"`
	Dimensions int    `mapstructure:"dimensions"`
}

type ServerConfig struct {
	Addr    string `mapstructure:"addr"`
	BaseURL string `mapstructure:"base_url"`
}

// Load reads configuration from the given file path (YAML).
// If path is empty, defaults are used.
func Load(path string) (*Config, error) {
	v := viper.New()

	// Defaults
	v.SetDefault("surreal.url", "ws://localhost:8000")
	v.SetDefault("surreal.ns", "adb")
	v.SetDefault("surreal.db", "adb")
	v.SetDefault("surreal.username", "root")
	v.SetDefault("surreal.password", "root")
	v.SetDefault("server.addr", ":8080")
	v.SetDefault("server.base_url", "http://localhost:8080")
	v.SetDefault("gprn.service", "adb-standalone")
	v.SetDefault("gprn.environment", "local")
	v.SetDefault("gprn.placeholder", "artifact")
	v.SetDefault("permissions.default_read", "public")
	v.SetDefault("permissions.default_write", "owners")
	v.SetDefault("schema", "artifactdb-schema/v1")
	v.SetDefault("internal_schema", "artifactdb-internal/v1")
	v.SetDefault("semantic.dimensions", 256)
	v.SetDefault("storage.s3.presigned_url_expiration", 3600)
	v.SetDefault("storage.s3.signature_version", "v4")

	// Environment variable overrides (e.g. SURREAL_URL)
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	if path != "" {
		v.SetConfigFile(path)
		if err := v.ReadInConfig(); err != nil {
			return nil, fmt.Errorf("read config %q: %w", path, err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}
	return &cfg, nil
}
