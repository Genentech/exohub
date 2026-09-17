package init

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestRemoteConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		remote  RemoteConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid annex remote",
			remote: RemoteConfig{
				Name:  "test-annex",
				Type:  "annex",
				S3URL: "s3://bucket/prefix",
				Chunk: "1GiB",
			},
			wantErr: false,
		},
		{
			name: "valid export remote",
			remote: RemoteConfig{
				Name:           "test-export",
				Type:           "export",
				S3URL:          "s3://bucket/prefix",
				TrackingBranch: "main",
			},
			wantErr: false,
		},
		{
			name: "valid import remote",
			remote: RemoteConfig{
				Name:           "test-import",
				Type:           "import",
				Bucket:         "my-bucket",
				Prefix:         "data/",
				Datacenter:     "us-west-2",
				TrackingBranch: "main",
			},
			wantErr: false,
		},
		{
			name: "valid exospace remote",
			remote: RemoteConfig{
				Name:     "test-exospace",
				Type:     "exospace",
				RsyncURL: "/mnt/data",
			},
			wantErr: false,
		},
		{
			name: "annex with chunk only",
			remote: RemoteConfig{
				Name:  "test",
				Type:  "annex",
				S3URL: "s3://bucket/prefix",
			},
			wantErr: false,
		},
		{
			name: "export with tracking_branch is optional",
			remote: RemoteConfig{
				Name:  "test",
				Type:  "export",
				S3URL: "s3://bucket/prefix",
			},
			wantErr: false,
		},
		{
			name: "import with tracking_branch is optional",
			remote: RemoteConfig{
				Name:   "test",
				Type:   "import",
				Bucket: "my-bucket",
			},
			wantErr: false,
		},
		{
			name: "annex without s3url",
			remote: RemoteConfig{
				Name: "test",
				Type: "annex",
			},
			wantErr: true,
			errMsg:  "s3url is required for annex remotes",
		},
		{
			name: "export without s3url",
			remote: RemoteConfig{
				Name: "test",
				Type: "export",
			},
			wantErr: true,
			errMsg:  "s3url is required for export remotes",
		},
		{
			name: "import without bucket or s3url",
			remote: RemoteConfig{
				Name: "test",
				Type: "import",
			},
			wantErr: true,
			errMsg:  "bucket or s3url is required for import remotes",
		},
		{
			name: "exospace without rsyncurl",
			remote: RemoteConfig{
				Name: "test",
				Type: "exospace",
			},
			wantErr: true,
			errMsg:  "rsyncurl is required for exospace remotes",
		},
		{
			name: "annex with tracking_branch (invalid)",
			remote: RemoteConfig{
				Name:           "test",
				Type:           "annex",
				S3URL:          "s3://bucket/prefix",
				TrackingBranch: "main",
			},
			wantErr: true,
			errMsg:  "tracking_branch is not allowed for annex remotes",
		},
		{
			name: "export with chunk (invalid)",
			remote: RemoteConfig{
				Name:  "test",
				Type:  "export",
				S3URL: "s3://bucket/prefix",
				Chunk: "1GiB",
			},
			wantErr: true,
			errMsg:  "chunk is not allowed for export remotes",
		},
		{
			name: "import with chunk (invalid)",
			remote: RemoteConfig{
				Name:   "test",
				Type:   "import",
				Bucket: "my-bucket",
				Chunk:  "1GiB",
			},
			wantErr: true,
			errMsg:  "chunk is not allowed for import remotes",
		},
		{
			name: "exospace with s3url (invalid)",
			remote: RemoteConfig{
				Name:     "test",
				Type:     "exospace",
				RsyncURL: "/mnt/data",
				S3URL:    "s3://bucket/prefix",
			},
			wantErr: true,
			errMsg:  "s3url is not allowed for exospace remotes",
		},
		{
			name: "exospace with chunk (invalid)",
			remote: RemoteConfig{
				Name:     "test",
				Type:     "exospace",
				RsyncURL: "/mnt/data",
				Chunk:    "1GiB",
			},
			wantErr: true,
			errMsg:  "chunk is not allowed for exospace remotes",
		},
		{
			name: "unknown type without mode (catalog remote, missing mode)",
			remote: RemoteConfig{
				Name: "test",
				Type: "unknown",
			},
			wantErr: true,
			errMsg:  "mode is required for catalog remote type",
		},
		{
			name: "empty name",
			remote: RemoteConfig{
				Type:  "annex",
				S3URL: "s3://bucket/prefix",
			},
			wantErr: true,
			errMsg:  "remote name is required",
		},
		{
			name: "import remote with include patterns (valid)",
			remote: RemoteConfig{
				Name:    "test-import",
				Type:    "import",
				Bucket:  "my-bucket",
				Include: []string{"*.bam", "*.bai"},
			},
			wantErr: false,
		},
		{
			name: "import remote with exclude patterns (valid)",
			remote: RemoteConfig{
				Name:    "test-import",
				Type:    "import",
				Bucket:  "my-bucket",
				Exclude: []string{"*.tmp", "tmp/*"},
			},
			wantErr: false,
		},
		{
			name: "import remote with both include and exclude (valid)",
			remote: RemoteConfig{
				Name:    "test-import",
				Type:    "import",
				Bucket:  "my-bucket",
				Include: []string{"*.fastq.gz"},
				Exclude: []string{"*/archive/*"},
			},
			wantErr: false,
		},
		{
			name: "annex remote with include patterns (valid)",
			remote: RemoteConfig{
				Name:    "test-annex",
				Type:    "annex",
				S3URL:   "s3://bucket/prefix",
				Include: []string{"*.vcf.gz"},
			},
			wantErr: false,
		},
		{
			name: "annex remote with exclude patterns (valid)",
			remote: RemoteConfig{
				Name:    "test-annex",
				Type:    "annex",
				S3URL:   "s3://bucket/prefix",
				Exclude: []string{"*.log"},
			},
			wantErr: false,
		},
		{
			name: "export remote with include patterns (valid)",
			remote: RemoteConfig{
				Name:    "test-export",
				Type:    "export",
				S3URL:   "s3://bucket/prefix",
				Include: []string{"*.csv"},
			},
			wantErr: false,
		},
		{
			name: "export remote with exclude patterns (valid)",
			remote: RemoteConfig{
				Name:    "test-export",
				Type:    "export",
				S3URL:   "s3://bucket/prefix",
				Exclude: []string{"*.tmp"},
			},
			wantErr: false,
		},
		{
			name: "export remote with both include and exclude (valid)",
			remote: RemoteConfig{
				Name:    "test-export",
				Type:    "export",
				S3URL:   "s3://bucket/prefix",
				Include: []string{"*.csv"},
				Exclude: []string{"*.tmp"},
			},
			wantErr: false,
		},
		{
			name: "exospace remote with include patterns (valid)",
			remote: RemoteConfig{
				Name:     "test-exospace",
				Type:     "exospace",
				RsyncURL: "/mnt/data",
				Include:  []string{"*.bam"},
			},
			wantErr: false,
		},
		{
			name: "exospace remote with exclude patterns (valid)",
			remote: RemoteConfig{
				Name:     "test-exospace",
				Type:     "exospace",
				RsyncURL: "/mnt/data",
				Exclude:  []string{"*.tmp"},
			},
			wantErr: false,
		},
		// Exospace permissions tests
		{
			name: "exospace with permissions public (valid)",
			remote: RemoteConfig{
				Name:        "test-exospace",
				Type:        "exospace",
				RsyncURL:    "/mnt/data",
				Permissions: "public",
			},
			wantErr: false,
		},
		{
			name: "exospace with permissions group (valid)",
			remote: RemoteConfig{
				Name:        "test-exospace",
				Type:        "exospace",
				RsyncURL:    "/mnt/data",
				Permissions: "group",
			},
			wantErr: false,
		},
		{
			name: "exospace with permissions private (valid)",
			remote: RemoteConfig{
				Name:        "test-exospace",
				Type:        "exospace",
				RsyncURL:    "/mnt/data",
				Permissions: "private",
			},
			wantErr: false,
		},
		{
			name: "exospace with rsync_options (valid)",
			remote: RemoteConfig{
				Name:         "test-exospace",
				Type:         "exospace",
				RsyncURL:     "/mnt/data",
				RsyncOptions: "--chmod=ug=rwX --bwlimit=500",
			},
			wantErr: false,
		},
		{
			name: "exospace with invalid permissions value",
			remote: RemoteConfig{
				Name:        "test-exospace",
				Type:        "exospace",
				RsyncURL:    "/mnt/data",
				Permissions: "invalid-preset",
			},
			wantErr: true,
			errMsg:  "invalid permissions value 'invalid-preset'",
		},
		{
			name: "exospace with both permissions and rsync_options (invalid)",
			remote: RemoteConfig{
				Name:         "test-exospace",
				Type:         "exospace",
				RsyncURL:     "/mnt/data",
				Permissions:  "public",
				RsyncOptions: "--bwlimit=100",
			},
			wantErr: true,
			errMsg:  "permissions and rsync_options are mutually exclusive",
		},
		{
			name: "annex with permissions (invalid)",
			remote: RemoteConfig{
				Name:        "test-annex",
				Type:        "annex",
				S3URL:       "s3://bucket/prefix",
				Permissions: "public",
			},
			wantErr: true,
			errMsg:  "permissions is only allowed for exospace remotes",
		},
		{
			name: "annex with rsync_options (invalid)",
			remote: RemoteConfig{
				Name:         "test-annex",
				Type:         "annex",
				S3URL:        "s3://bucket/prefix",
				RsyncOptions: "--bwlimit=100",
			},
			wantErr: true,
			errMsg:  "rsync_options is only allowed for exospace remotes",
		},
		{
			name: "export with permissions (invalid)",
			remote: RemoteConfig{
				Name:        "test-export",
				Type:        "export",
				S3URL:       "s3://bucket/prefix",
				Permissions: "public",
			},
			wantErr: true,
			errMsg:  "permissions is only allowed for exospace remotes",
		},
		{
			name: "export with rsync_options (invalid)",
			remote: RemoteConfig{
				Name:         "test-export",
				Type:         "export",
				S3URL:        "s3://bucket/prefix",
				RsyncOptions: "--bwlimit=100",
			},
			wantErr: true,
			errMsg:  "rsync_options is only allowed for exospace remotes",
		},
		{
			name: "import with permissions (invalid)",
			remote: RemoteConfig{
				Name:        "test-import",
				Type:        "import",
				Bucket:      "my-bucket",
				Permissions: "public",
			},
			wantErr: true,
			errMsg:  "permissions is only allowed for exospace remotes",
		},
		{
			name: "import with rsync_options (invalid)",
			remote: RemoteConfig{
				Name:         "test-import",
				Type:         "import",
				Bucket:       "my-bucket",
				RsyncOptions: "--bwlimit=100",
			},
			wantErr: true,
			errMsg:  "rsync_options is only allowed for exospace remotes",
		},
		// host/port/protocol tests
		{
			name: "import with host/port/protocol (valid)",
			remote: RemoteConfig{
				Name:     "test-import",
				Type:     "import",
				Bucket:   "my-bucket",
				Host:     "localhost:9000",
				Port:     "9000",
				Protocol: "http",
			},
			wantErr: false,
		},
		{
			name: "import with host only (valid)",
			remote: RemoteConfig{
				Name:   "test-import",
				Type:   "import",
				Bucket: "my-bucket",
				Host:   "minio.local:9000",
			},
			wantErr: false,
		},
		{
			name: "import with invalid protocol",
			remote: RemoteConfig{
				Name:     "test-import",
				Type:     "import",
				Bucket:   "my-bucket",
				Protocol: "ftp",
			},
			wantErr: true,
			errMsg:  "invalid protocol 'ftp'",
		},
		{
			name: "annex with host (invalid)",
			remote: RemoteConfig{
				Name:  "test-annex",
				Type:  "annex",
				S3URL: "s3://bucket/prefix",
				Host:  "localhost:9000",
			},
			wantErr: true,
			errMsg:  "host/port/protocol are only allowed for import remotes",
		},
		{
			name: "export with protocol (invalid)",
			remote: RemoteConfig{
				Name:     "test-export",
				Type:     "export",
				S3URL:    "s3://bucket/prefix",
				Protocol: "http",
			},
			wantErr: true,
			errMsg:  "host/port/protocol are only allowed for import remotes",
		},
		{
			name: "exospace with host (invalid)",
			remote: RemoteConfig{
				Name:     "test-exospace",
				Type:     "exospace",
				RsyncURL: "/mnt/data",
				Host:     "localhost:9000",
			},
			wantErr: true,
			errMsg:  "host/port/protocol are only allowed for import remotes",
		},
		// catalog remote tests
		{
			name: "valid catalog remote (artifactdb export)",
			remote: RemoteConfig{
				Name:  "catalog",
				Type:  "artifactdb",
				Mode:  "export",
				S3URL: "s3://bucket/path/_artifactdb",
				InstanceURL: "https://catalog.example.com/v1/cancerdb",
			},
			wantErr: false,
		},
		{
			name: "catalog remote missing mode",
			remote: RemoteConfig{
				Name:  "catalog",
				Type:  "artifactdb",
				S3URL: "s3://bucket/path/_artifactdb",
			},
			wantErr: true,
			errMsg:  "mode is required for catalog remote type",
		},
		{
			name: "catalog remote invalid mode",
			remote: RemoteConfig{
				Name:  "catalog",
				Type:  "artifactdb",
				Mode:  "invalid",
				S3URL: "s3://bucket/path/_artifactdb",
			},
			wantErr: true,
			errMsg:  "invalid mode",
		},
		{
			name: "catalog remote missing s3url",
			remote: RemoteConfig{
				Name: "catalog",
				Type: "artifactdb",
				Mode: "export",
			},
			wantErr: true,
			errMsg:  "s3url is required for artifactdb remotes",
		},
		{
			name: "catalog remote without instance_url (valid)",
			remote: RemoteConfig{
				Name:  "catalog",
				Type:  "artifactdb",
				Mode:  "export",
				S3URL: "s3://bucket/path/_artifactdb",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.remote.Validate()
			if tt.wantErr {
				if err == nil {
					t.Errorf("Validate() expected error, got nil")
				} else if tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("Validate() error = %q, want to contain %q", err.Error(), tt.errMsg)
				}
			} else {
				if err != nil {
					t.Errorf("Validate() unexpected error: %v", err)
				}
			}
		})
	}
}

func TestLoadSaveRemotesConfig(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "exo-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Change to temp dir
	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	os.Chdir(tmpDir)

	// Create .exohub directory
	if err := os.Mkdir(".exohub", 0755); err != nil {
		t.Fatalf("failed to create .exohub dir: %v", err)
	}

	// Test saving remotes config
	cfg := &RemotesConfig{
		Remotes: []RemoteConfig{
			{
				Name:  "annex-remote",
				Type:  "annex",
				S3URL: "s3://bucket/annex",
				Chunk: "1GiB",
			},
			{
				Name:           "export-remote",
				Type:           "export",
				S3URL:          "s3://bucket/export",
				TrackingBranch: "main",
			},
			{
				Name:           "import-remote",
				Type:           "import",
				Bucket:         "my-bucket",
				Prefix:         "import/",
				Datacenter:     "us-west-2",
				TrackingBranch: "main",
			},
			{
				Name:     "exospace-remote",
				Type:     "exospace",
				RsyncURL: "/mnt/data",
			},
		},
	}

	if err := SaveRemotesConfig(cfg); err != nil {
		t.Fatalf("SaveRemotesConfig() failed: %v", err)
	}

	// Test loading remotes config
	loaded, err := LoadRemotesConfig()
	if err != nil {
		t.Fatalf("LoadRemotesConfig() failed: %v", err)
	}

	if len(loaded.Remotes) != 4 {
		t.Fatalf("expected 4 remotes, got %d", len(loaded.Remotes))
	}

	// Check first remote (annex)
	r := loaded.Remotes[0]
	if r.Name != "annex-remote" || r.Type != "annex" || r.S3URL != "s3://bucket/annex" || r.Chunk != "1GiB" {
		t.Errorf("annex remote mismatch: %+v", r)
	}

	// Check second remote (export)
	r = loaded.Remotes[1]
	if r.Name != "export-remote" || r.Type != "export" || r.S3URL != "s3://bucket/export" || r.TrackingBranch != "main" {
		t.Errorf("export remote mismatch: %+v", r)
	}

	// Check third remote (import)
	r = loaded.Remotes[2]
	if r.Name != "import-remote" || r.Type != "import" || r.Bucket != "my-bucket" || r.Prefix != "import/" || r.TrackingBranch != "main" {
		t.Errorf("import remote mismatch: %+v", r)
	}

	// Check fourth remote (exospace)
	r = loaded.Remotes[3]
	if r.Name != "exospace-remote" || r.Type != "exospace" || r.RsyncURL != "/mnt/data" {
		t.Errorf("exospace remote mismatch: %+v", r)
	}
}

func TestLoadRemotesConfigNotExists(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "exo-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	os.Chdir(tmpDir)

	// Should return empty config when file doesn't exist
	cfg, err := LoadRemotesConfig()
	if err != nil {
		t.Fatalf("LoadRemotesConfig() failed: %v", err)
	}

	if cfg == nil {
		t.Errorf("expected non-nil config, got nil")
	}
	if len(cfg.Remotes) != 0 {
		t.Errorf("expected empty remotes, got %d", len(cfg.Remotes))
	}
}

func TestLoadRemotesConfigInvalidYAML(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "exo-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	os.Chdir(tmpDir)

	if err := os.Mkdir(".exohub", 0755); err != nil {
		t.Fatalf("failed to create .exohub dir: %v", err)
	}

	// Write invalid YAML
	invalidYAML := "remotes:\n  - name: test\n    invalid yaml {{"
	if err := os.WriteFile(filepath.Join(".exohub", "remotes"), []byte(invalidYAML), 0644); err != nil {
		t.Fatalf("failed to write invalid YAML: %v", err)
	}

	_, err = LoadRemotesConfig()
	if err == nil {
		t.Errorf("LoadRemotesConfig() expected error for invalid YAML, got nil")
	}
}

func TestStringListUnmarshal(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		want    []string
		wantErr bool
	}{
		{
			name: "single string",
			yaml: `include: "*.bam"`,
			want: []string{"*.bam"},
		},
		{
			name: "list of strings",
			yaml: `include: ["*.bam", "*.bai"]`,
			want: []string{"*.bam", "*.bai"},
		},
		{
			name: "empty string",
			yaml: `include: ""`,
			want: nil,
		},
		{
			name: "empty list",
			yaml: `include: []`,
			want: nil,
		},
		{
			name: "list with empty strings",
			yaml: "include:\n  - \"*.bam\"\n  - \"\"\n  - \"*.bai\"",
			want: []string{"*.bam", "*.bai"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var config struct {
				Include stringList `yaml:"include"`
			}
			err := yaml.Unmarshal([]byte(tt.yaml), &config)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if len(config.Include) != len(tt.want) {
				t.Errorf("Include length = %d, want %d", len(config.Include), len(tt.want))
				return
			}
			for i := range tt.want {
				if config.Include[i] != tt.want[i] {
					t.Errorf("Include[%d] = %q, want %q", i, config.Include[i], tt.want[i])
				}
			}
		})
	}
}

func TestRemoteConfigWithScalarPatterns(t *testing.T) {
	yamlData := `
name: test-import
type: import
bucket: my-bucket
include: "*.bam"
exclude: "*.tmp"
`
	var config RemoteConfig
	err := yaml.Unmarshal([]byte(yamlData), &config)
	if err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if len(config.Include) != 1 || config.Include[0] != "*.bam" {
		t.Errorf("Include = %v, want [\"*.bam\"]", config.Include)
	}
	if len(config.Exclude) != 1 || config.Exclude[0] != "*.tmp" {
		t.Errorf("Exclude = %v, want [\"*.tmp\"]", config.Exclude)
	}
}

func TestRemoteConfigWithListPatterns(t *testing.T) {
	yamlData := `
name: test-import
type: import
bucket: my-bucket
include:
  - "*.bam"
  - "*.bai"
exclude:
  - "*.tmp"
  - "*/archive/*"
`
	var config RemoteConfig
	err := yaml.Unmarshal([]byte(yamlData), &config)
	if err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	wantInclude := []string{"*.bam", "*.bai"}
	if len(config.Include) != len(wantInclude) {
		t.Errorf("Include length = %d, want %d", len(config.Include), len(wantInclude))
	}
	for i, want := range wantInclude {
		if config.Include[i] != want {
			t.Errorf("Include[%d] = %q, want %q", i, config.Include[i], want)
		}
	}

	wantExclude := []string{"*.tmp", "*/archive/*"}
	if len(config.Exclude) != len(wantExclude) {
		t.Errorf("Exclude length = %d, want %d", len(config.Exclude), len(wantExclude))
	}
	for i, want := range wantExclude {
		if config.Exclude[i] != want {
			t.Errorf("Exclude[%d] = %q, want %q", i, config.Exclude[i], want)
		}
	}
}

func TestExohubConfigLoadSave(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	os.Chdir(tmpDir)

	// Test missing file returns nil
	cfg, err := LoadExohubConfig()
	if err != nil {
		t.Fatalf("LoadExohubConfig() error = %v", err)
	}
	if cfg != nil {
		t.Errorf("expected nil for missing file, got %+v", cfg)
	}

	// Create .exohub directory
	os.Mkdir(".exohub", 0755)

	// Test save and load with annex.thin=true
	thinTrue := true
	if err := SaveExohubConfig(&ExohubConfig{Annex: &AnnexConfig{Thin: &thinTrue}}); err != nil {
		t.Fatalf("SaveExohubConfig() error = %v", err)
	}

	cfg, err = LoadExohubConfig()
	if err != nil {
		t.Fatalf("LoadExohubConfig() error = %v", err)
	}
	if cfg == nil || cfg.Annex == nil || cfg.Annex.Thin == nil || !*cfg.Annex.Thin {
		t.Errorf("expected annex.thin=true, got %+v", cfg)
	}

	// Test save and load with annex.thin=false
	thinFalse := false
	if err := SaveExohubConfig(&ExohubConfig{Annex: &AnnexConfig{Thin: &thinFalse}}); err != nil {
		t.Fatalf("SaveExohubConfig() error = %v", err)
	}

	cfg, err = LoadExohubConfig()
	if err != nil {
		t.Fatalf("LoadExohubConfig() error = %v", err)
	}
	if cfg == nil || cfg.Annex == nil || cfg.Annex.Thin == nil || *cfg.Annex.Thin {
		t.Errorf("expected annex.thin=false, got %+v", cfg)
	}

	// Test save with no annex settings (omitted from YAML)
	if err := SaveExohubConfig(&ExohubConfig{}); err != nil {
		t.Fatalf("SaveExohubConfig() error = %v", err)
	}

	cfg, err = LoadExohubConfig()
	if err != nil {
		t.Fatalf("LoadExohubConfig() error = %v", err)
	}
	if cfg == nil {
		t.Fatalf("expected non-nil config")
	}
	if cfg.Annex != nil && cfg.Annex.Thin != nil {
		t.Errorf("expected annex.thin=nil, got %v", *cfg.Annex.Thin)
	}
}

func TestExohubConfigInvalidYAML(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	os.Chdir(tmpDir)

	os.Mkdir(".exohub", 0755)
	os.WriteFile(filepath.Join(".exohub", "config"), []byte("thin: {invalid"), 0644)

	_, err := LoadExohubConfig()
	if err == nil {
		t.Errorf("LoadExohubConfig() expected error for invalid YAML")
	}
}

func TestParseRepoURL(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		wantHost string
		wantOrg  string
		wantRepo string
		wantErr  bool
	}{
		{
			name:     "HTTPS basic",
			url:      "https://github.com/org/data-repos/mynewrepo",
			wantHost: "https://github.com",
			wantOrg:  "org/data-repos",
			wantRepo: "mynewrepo",
		},
		{
			name:     "HTTPS with .git suffix",
			url:      "https://github.com/org/repo.git",
			wantHost: "https://github.com",
			wantOrg:  "org",
			wantRepo: "repo",
		},
		{
			name:     "HTTPS multi-level org",
			url:      "https://gitlab.example.com/group/subgroup/project.git",
			wantHost: "https://gitlab.example.com",
			wantOrg:  "group/subgroup",
			wantRepo: "project",
		},
		{
			name:     "SSH SCP format",
			url:      "git@gitlab.com:org/data-repos/mynewrepo.git",
			wantHost: "https://gitlab.com",
			wantOrg:  "org/data-repos",
			wantRepo: "mynewrepo",
		},
		{
			name:     "SSH SCP simple",
			url:      "git@github.com:org/repo.git",
			wantHost: "https://github.com",
			wantOrg:  "org",
			wantRepo: "repo",
		},
		{
			name:     "SSH SCP without .git",
			url:      "git@github.com:org/repo",
			wantHost: "https://github.com",
			wantOrg:  "org",
			wantRepo: "repo",
		},
		{
			name:     "SSH URL format",
			url:      "ssh://git@gitlab.com/org/data-repos/mynewrepo.git",
			wantHost: "https://gitlab.com",
			wantOrg:  "org/data-repos",
			wantRepo: "mynewrepo",
		},
		{
			name:    "invalid - no host",
			url:     "just-a-name",
			wantErr: true,
		},
		{
			name:    "invalid - no org/repo path",
			url:     "https://github.com/",
			wantErr: true,
		},
		{
			name:    "invalid - only host, no path",
			url:     "https://github.com",
			wantErr: true,
		},
		{
			name:    "invalid - single path segment",
			url:     "https://github.com/onlyone",
			wantErr: true,
		},
		{
			name:     "SSH SCP with ssh. prefix",
			url:      "git@ssh.gitlab.com:org/myrepo.git",
			wantHost: "https://gitlab.com",
			wantOrg:  "org",
			wantRepo: "myrepo",
		},
		{
			name:     "SSH URL with port",
			url:      "ssh://git@gitlab.example.com:30022/huge/gwasdb.git",
			wantHost: "https://gitlab.example.com",
			wantOrg:  "huge",
			wantRepo: "gwasdb",
		},
		{
			name:    "invalid SSH - missing colon path",
			url:     "git@github.com",
			wantErr: true,
		},
		{
			name:    "invalid SSH - empty path",
			url:     "git@github.com:",
			wantErr: true,
		},
		{
			name:    "invalid SSH - single path segment",
			url:     "git@github.com:onlyone",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host, org, repo, err := ParseRepoURL(tt.url)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ParseRepoURL(%q) expected error, got host=%q org=%q repo=%q", tt.url, host, org, repo)
				}
				return
			}
			if err != nil {
				t.Errorf("ParseRepoURL(%q) unexpected error: %v", tt.url, err)
				return
			}
			if host != tt.wantHost {
				t.Errorf("ParseRepoURL(%q) host = %q, want %q", tt.url, host, tt.wantHost)
			}
			if org != tt.wantOrg {
				t.Errorf("ParseRepoURL(%q) org = %q, want %q", tt.url, org, tt.wantOrg)
			}
			if repo != tt.wantRepo {
				t.Errorf("ParseRepoURL(%q) repo = %q, want %q", tt.url, repo, tt.wantRepo)
			}
		})
	}
}

func TestParseS3URL(t *testing.T) {
	tests := []struct {
		name        string
		s3url       string
		wantBucket  string
		wantPrefix  string
		wantErr     bool
		errContains string
	}{
		{
			name:       "basic bucket only",
			s3url:      "s3://my-bucket",
			wantBucket: "my-bucket",
			wantPrefix: "",
			wantErr:    false,
		},
		{
			name:       "bucket with prefix",
			s3url:      "s3://my-bucket/data",
			wantBucket: "my-bucket",
			wantPrefix: "data/",
			wantErr:    false,
		},
		{
			name:       "bucket with prefix already with trailing slash",
			s3url:      "s3://my-bucket/data/",
			wantBucket: "my-bucket",
			wantPrefix: "data/",
			wantErr:    false,
		},
		{
			name:       "bucket with nested prefix",
			s3url:      "s3://my-bucket/path/to/data",
			wantBucket: "my-bucket",
			wantPrefix: "path/to/data/",
			wantErr:    false,
		},
		{
			name:       "bucket with nested prefix and trailing slash",
			s3url:      "s3://my-bucket/path/to/data/",
			wantBucket: "my-bucket",
			wantPrefix: "path/to/data/",
			wantErr:    false,
		},
		{
			name:        "missing s3:// prefix",
			s3url:       "my-bucket/prefix",
			wantErr:     true,
			errContains: "must start with s3://",
		},
		{
			name:        "empty bucket name",
			s3url:       "s3:///prefix",
			wantErr:     true,
			errContains: "bucket name cannot be empty",
		},
		{
			name:        "just s3://",
			s3url:       "s3://",
			wantErr:     true,
			errContains: "bucket name cannot be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bucket, prefix, err := parseS3URL(tt.s3url)
			if tt.wantErr {
				if err == nil {
					t.Errorf("parseS3URL() expected error, got nil")
				} else if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("parseS3URL() error = %q, want to contain %q", err.Error(), tt.errContains)
				}
				return
			}

			if err != nil {
				t.Errorf("parseS3URL() unexpected error: %v", err)
				return
			}

			if bucket != tt.wantBucket {
				t.Errorf("parseS3URL() bucket = %q, want %q", bucket, tt.wantBucket)
			}
			if prefix != tt.wantPrefix {
				t.Errorf("parseS3URL() prefix = %q, want %q", prefix, tt.wantPrefix)
			}
		})
	}
}
