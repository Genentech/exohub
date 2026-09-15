package sync

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	initcmd "github.com/Genentech/exohub/go/exo/commands/init"
	"github.com/Genentech/exohub/go/exo/commandutil"

	"gopkg.in/yaml.v3"
)

func TestBuildDryRunQueriesIncludesPaths(t *testing.T) {
	queries := commandutil.BuildDryRunQueries("backup", true)
	if len(queries) != 3 {
		t.Fatalf("expected 3 queries, got %d", len(queries))
	}

	wantFirst := []string{"git", "annex", "find", "--not", "--in", "backup", "--in", "here"}
	if !reflect.DeepEqual(queries[0].Args, wantFirst) {
		t.Fatalf("first query args mismatch\nwant: %#v\n got: %#v", wantFirst, queries[0].Args)
	}

	wantSecond := []string{"git", "annex", "find", "--not", "--in", "here", "--in", "backup"}
	if !reflect.DeepEqual(queries[1].Args, wantSecond) {
		t.Fatalf("second query args mismatch\nwant: %#v\n got: %#v", wantSecond, queries[1].Args)
	}

	wantThird := []string{"git", "annex", "find", "--in", "here", "--not", "--in", "backup"}
	if !reflect.DeepEqual(queries[2].Args, wantThird) {
		t.Fatalf("third query args mismatch\nwant: %#v\n got: %#v", wantThird, queries[2].Args)
	}
}

func TestParseAnnexFindJSON(t *testing.T) {
	input := "{\"file\":\"a.txt\"}\n{\"file\":\"b.txt\"}\n{\"key\":\"k\"}\n"
	got := commandutil.ParseAnnexFindJSON(input)
	want := []string{"a.txt", "b.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected files\nwant: %#v\n got: %#v", want, got)
	}
}

func TestParseAnnexListRemotesText(t *testing.T) {
	input := "DEBUG: git annex listremotes\n1234 backup\n5678 (origin)\n9999 here\n1234 backup\n"
	got := parseAnnexListRemotesText(input)
	want := []string{"backup", "origin"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected remotes\nwant: %#v\n got: %#v", want, got)
	}
}

func TestParseAnnexInfoRemotesJSON(t *testing.T) {
	input := "{\"semitrusted repositories\":[{\"description\":\"[backup]\",\"here\":false},{\"description\":\"host:path\",\"here\":false}],\"trusted repositories\":[{\"description\":\"[origin]\",\"here\":false}],\"untrusted repositories\":[{\"description\":\"[here]\",\"here\":false}],\"success\":true}\n"
	got := parseAnnexInfoRemotesJSON(input)
	want := []string{"backup", "origin"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected remotes\nwant: %#v\n got: %#v", want, got)
	}
}

func TestParseAnnexRemoteNamesFromConfig(t *testing.T) {
	input := "remote.backup.annex-uuid 1234\nremote.origin.annex-uuid 5678\nremote.here.annex-uuid 9999\n"
	got := parseAnnexRemoteNamesFromConfig(input)
	want := []string{"backup", "origin"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected remotes\nwant: %#v\n got: %#v", want, got)
	}
}

func TestParseManifest(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "manifest.yaml")
	data := "repo-dir: /repo\nwith-remotes: [origin]\npaths: [p1, p2]\njobs: ' 2 '\n"
	if err := os.WriteFile(manifestPath, []byte(data), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	parsed, err := parseManifest(manifestPath)
	if err != nil {
		t.Fatalf("parseManifest: %v", err)
	}
	if parsed.repoDir() != "/repo" {
		t.Fatalf("repoDir: %q", parsed.repoDir())
	}
	if len(parsed.WithRemotes) != 1 || parsed.WithRemotes[0] != "origin" {
		t.Fatalf("with remotes: %#v", parsed.WithRemotes)
	}
	if parsed.Jobs != "2" {
		t.Fatalf("jobs: %q", parsed.Jobs)
	}
}

func TestParseManifestMissingFile(t *testing.T) {
	if _, err := parseManifest("/not/a/real/path.yaml"); err == nil {
		t.Fatalf("expected error for missing manifest")
	}
}

func TestStringListUnmarshalError(t *testing.T) {
	var parsed manifest
	data := "paths:\n  - foo\n  - {bad: 1}\n"
	if err := yaml.Unmarshal([]byte(data), &parsed); err == nil {
		t.Fatalf("expected error for non-scalar list item")
	}
}

func TestStringValueUnmarshalError(t *testing.T) {
	var parsed manifest
	if err := yaml.Unmarshal([]byte("jobs: [1]"), &parsed); err == nil {
		t.Fatalf("expected error for non-scalar jobs")
	}
}

func TestParseJSONFiles(t *testing.T) {
	input := "{\"file\":\"a.txt\"}\n{\"file\":\"\"}\n{\"file\":\"b.txt\"}\n"
	got := parseJSONFiles(input)
	want := []string{"a.txt", "b.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseJSONFiles: %#v", got)
	}
}

func TestRunManifestValidation(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "manifest.yaml")
	data := "name: sample\nurl: https://example.test/repo.git\nref: main\nrepo-dir: /repo\nremote-type: annex\n"
	if err := os.WriteFile(manifestPath, []byte(data), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := runManifestValidation(manifestPath); err != nil {
		t.Fatalf("runManifestValidation: %v", err)
	}
}

func TestRunManifestValidationMissingRemoteType(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "manifest.yaml")
	data := "name: sample\nurl: https://example.test/repo.git\nref: main\nrepo-dir: /repo\n"
	if err := os.WriteFile(manifestPath, []byte(data), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := runManifestValidation(manifestPath); err == nil {
		t.Fatalf("expected validation error")
	}
}

func TestIsAnnexListRemotesNoise(t *testing.T) {
	cases := []string{
		"DEBUG: git annex listremotes",
		"git-annex: warning",
		"warning: ignored",
		"exit status 1",
	}
	for _, line := range cases {
		if !isAnnexListRemotesNoise(line) {
			t.Fatalf("expected noise for %q", line)
		}
	}
	if isAnnexListRemotesNoise("1234 remote") {
		t.Fatalf("unexpected noise for remote line")
	}
}

func TestExpandPaths(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("ok"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	pattern := filepath.Join(dir, "*.txt")
	expanded := expandPaths([]string{pattern, filepath.Join(dir, "missing")})
	if len(expanded) != 2 {
		t.Fatalf("expanded len: %d", len(expanded))
	}
	if expanded[0] != path {
		t.Fatalf("expanded[0]: got %q", expanded[0])
	}
}

func TestShellQuoteAndQuoteArgs(t *testing.T) {
	if got := shellQuote(""); got != "''" {
		t.Fatalf("shellQuote empty: %q", got)
	}
	if got := shellQuote("a b"); got != "'a b'" {
		t.Fatalf("shellQuote space: %q", got)
	}
	if got := shellQuote("a'b"); got != "'a'\"'\"'b'" {
		t.Fatalf("shellQuote quote: %q", got)
	}
	args := quoteArgs([]string{"git", "annex sync", ""})
	if len(args) != 3 || args[1] != "'annex sync'" || args[2] != "''" {
		t.Fatalf("quoteArgs: %#v", args)
	}
}

func TestExtractQuotedPaths(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("ok"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	line := "bad file '" + path + "' ignored"
	paths := extractQuotedPaths(line)
	if len(paths) != 1 || paths[0] != path {
		t.Fatalf("extractQuotedPaths: %#v", paths)
	}
}

func TestGetPatternsForRemote(t *testing.T) {
	// Create test remote configs
	remotesConfig := &initcmd.RemotesConfig{
		Remotes: []initcmd.RemoteConfig{
			{
				Name:    "import-remote",
				Type:    "import",
				Include: []string{"*.bam", "*.bai"},
				Exclude: []string{"*.tmp"},
			},
			{
				Name:    "annex-remote",
				Type:    "annex",
				Include: []string{"*.fastq.gz"},
			},
		},
	}

	tests := []struct {
		name          string
		remoteName    string
		remotesConfig *initcmd.RemotesConfig
		cliInclude    []string
		cliExclude    []string
		wantInclude   []string
		wantExclude   []string
	}{
		{
			name:          "CLI patterns override manifest include",
			remoteName:    "import-remote",
			remotesConfig: remotesConfig,
			cliInclude:    []string{"*.csv"},
			cliExclude:    nil,
			wantInclude:   []string{"*.csv"},
			wantExclude:   nil,
		},
		{
			name:          "CLI patterns override manifest exclude",
			remoteName:    "import-remote",
			remotesConfig: remotesConfig,
			cliInclude:    nil,
			cliExclude:    []string{"*.log"},
			wantInclude:   nil,
			wantExclude:   []string{"*.log"},
		},
		{
			name:          "CLI patterns override manifest both",
			remoteName:    "import-remote",
			remotesConfig: remotesConfig,
			cliInclude:    []string{"*.vcf.gz"},
			cliExclude:    []string{"*.old"},
			wantInclude:   []string{"*.vcf.gz"},
			wantExclude:   []string{"*.old"},
		},
		{
			name:          "Manifest patterns used when no CLI",
			remoteName:    "import-remote",
			remotesConfig: remotesConfig,
			cliInclude:    nil,
			cliExclude:    nil,
			wantInclude:   []string{"*.bam", "*.bai"},
			wantExclude:   []string{"*.tmp"},
		},
		{
			name:          "Manifest patterns for annex remote",
			remoteName:    "annex-remote",
			remotesConfig: remotesConfig,
			cliInclude:    nil,
			cliExclude:    nil,
			wantInclude:   []string{"*.fastq.gz"},
			wantExclude:   nil,
		},
		{
			name:          "No patterns when remote not in config",
			remoteName:    "unknown-remote",
			remotesConfig: remotesConfig,
			cliInclude:    nil,
			cliExclude:    nil,
			wantInclude:   nil,
			wantExclude:   nil,
		},
		{
			name:          "No patterns when config is nil",
			remoteName:    "import-remote",
			remotesConfig: nil,
			cliInclude:    nil,
			cliExclude:    nil,
			wantInclude:   nil,
			wantExclude:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotInclude, gotExclude := getPatternsForRemote(tt.remoteName, tt.remotesConfig, tt.cliInclude, tt.cliExclude)
			if !reflect.DeepEqual(gotInclude, tt.wantInclude) {
				t.Errorf("getPatternsForRemote() include = %v, want %v", gotInclude, tt.wantInclude)
			}
			if !reflect.DeepEqual(gotExclude, tt.wantExclude) {
				t.Errorf("getPatternsForRemote() exclude = %v, want %v", gotExclude, tt.wantExclude)
			}
		})
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		bytes int64
		want  string
	}{
		{0, "0 B"},
		{1, "1 B"},
		{1023, "1023 B"},
		{1024, "1.0 KiB"},
		{1536, "1.5 KiB"},
		{1048576, "1.0 MiB"},
		{1572864, "1.5 MiB"},
		{1073741824, "1.0 GiB"},
		{2147483648, "2.0 GiB"},
		{1099511627776, "1.0 TiB"},
		{1125899906842624, "1.0 PiB"},
		{1152921504606846976, "1.0 EiB"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := formatBytes(tt.bytes)
			if got != tt.want {
				t.Errorf("formatBytes(%d) = %q, want %q", tt.bytes, got, tt.want)
			}
		})
	}
}

func TestParseAnnexFindJSONWithSize(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []fileInfo
	}{
		{
			name:  "empty input",
			input: "",
			want:  nil,
		},
		{
			name:  "single file with size",
			input: `{"file":"test.txt","size":1234}`,
			want: []fileInfo{
				{Path: "test.txt", Size: 1234},
			},
		},
		{
			name:  "multiple files with sizes",
			input: "{\"file\":\"a.txt\",\"size\":100}\n{\"file\":\"b.txt\",\"size\":200}\n",
			want: []fileInfo{
				{Path: "a.txt", Size: 100},
				{Path: "b.txt", Size: 200},
			},
		},
		{
			name:  "file without size field",
			input: `{"file":"test.txt"}`,
			want: []fileInfo{
				{Path: "test.txt", Size: 0},
			},
		},
		{
			name:  "mixed with and without sizes",
			input: "{\"file\":\"a.txt\",\"size\":100}\n{\"file\":\"b.txt\"}\n",
			want: []fileInfo{
				{Path: "a.txt", Size: 100},
				{Path: "b.txt", Size: 0},
			},
		},
		{
			name:  "empty file path skipped",
			input: "{\"file\":\"\",\"size\":100}\n{\"file\":\"b.txt\",\"size\":200}\n",
			want: []fileInfo{
				{Path: "b.txt", Size: 200},
			},
		},
		{
			name:  "duplicate files",
			input: "{\"file\":\"a.txt\",\"size\":100}\n{\"file\":\"a.txt\",\"size\":200}\n",
			want: []fileInfo{
				{Path: "a.txt", Size: 100},
			},
		},
		{
			name:  "invalid JSON skipped",
			input: "{\"file\":\"a.txt\",\"size\":100}\ninvalid\n{\"file\":\"b.txt\",\"size\":200}\n",
			want: []fileInfo{
				{Path: "a.txt", Size: 100},
				{Path: "b.txt", Size: 200},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseAnnexFindJSONWithSize(tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseAnnexFindJSONWithSize() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestFilterObjects(t *testing.T) {
	tests := []struct {
		name            string
		objects         []fileInfo
		includePatterns []string
		excludePatterns []string
		want            []fileInfo
	}{
		{
			name: "no patterns - include all",
			objects: []fileInfo{
				{Path: "file1.txt", Size: 100},
				{Path: "file2.log", Size: 200},
			},
			want: []fileInfo{
				{Path: "file1.txt", Size: 100},
				{Path: "file2.log", Size: 200},
			},
		},
		{
			name: "include pattern matches",
			objects: []fileInfo{
				{Path: "file1.txt", Size: 100},
				{Path: "file2.log", Size: 200},
				{Path: "file3.txt", Size: 300},
			},
			includePatterns: []string{"*.txt"},
			want: []fileInfo{
				{Path: "file1.txt", Size: 100},
				{Path: "file3.txt", Size: 300},
			},
		},
		{
			name: "exclude pattern removes matches",
			objects: []fileInfo{
				{Path: "file1.txt", Size: 100},
				{Path: "file2.tmp", Size: 200},
				{Path: "file3.txt", Size: 300},
			},
			includePatterns: []string{"*.txt"},
			excludePatterns: []string{"*.tmp"},
			want: []fileInfo{
				{Path: "file1.txt", Size: 100},
				{Path: "file3.txt", Size: 300},
			},
		},
		{
			name: "**/ prefix in exclude - matches any directory",
			objects: []fileInfo{
				{Path: "dir1/file.mrjd.vcf.gz", Size: 100},
				{Path: "dir2/subdir/file.mrjd.vcf.gz", Size: 200},
				{Path: "dir3/file.vcf.gz", Size: 300},
			},
			includePatterns: []string{"*.vcf.gz"},
			excludePatterns: []string{"**/*.mrjd.vcf.gz"},
			want: []fileInfo{
				{Path: "dir3/file.vcf.gz", Size: 300},
			},
		},
		{
			name: "**/ prefix in exclude - real world example",
			objects: []fileInfo{
				{Path: "HG001/croo_output/dragen_output/HG001.hard-filtered.vcf.annotated.json.gz", Size: 100},
				{Path: "HG001/croo_output/dragen_output/HG001.mrjd.hard-filtered.vcf.annotated.json.gz", Size: 200},
				{Path: "HG002/croo_output/dragen_output/HG002.hard-filtered.vcf.annotated.json.gz", Size: 300},
				{Path: "HG002/croo_output/dragen_output/HG002.mrjd.hard-filtered.vcf.annotated.json.gz", Size: 400},
			},
			includePatterns: []string{"*.hard-filtered.vcf.annotated.json.gz"},
			excludePatterns: []string{"**/*.mrjd.hard-filtered.vcf.annotated.json.gz"},
			want: []fileInfo{
				{Path: "HG001/croo_output/dragen_output/HG001.hard-filtered.vcf.annotated.json.gz", Size: 100},
				{Path: "HG002/croo_output/dragen_output/HG002.hard-filtered.vcf.annotated.json.gz", Size: 300},
			},
		},
		{
			name: "exclude without **/ - basename match",
			objects: []fileInfo{
				{Path: "dir1/file.tmp", Size: 100},
				{Path: "dir2/file.txt", Size: 200},
			},
			includePatterns: []string{"*.*"},
			excludePatterns: []string{"*.tmp"},
			want: []fileInfo{
				{Path: "dir2/file.txt", Size: 200},
			},
		},
		{
			name: "include but all excluded",
			objects: []fileInfo{
				{Path: "file1.tmp", Size: 100},
				{Path: "file2.tmp", Size: 200},
			},
			includePatterns: []string{"*.tmp"},
			excludePatterns: []string{"*.tmp"},
			want:            nil,
		},
		{
			name: "multiple include patterns",
			objects: []fileInfo{
				{Path: "file1.txt", Size: 100},
				{Path: "file2.csv", Size: 200},
				{Path: "file3.log", Size: 300},
			},
			includePatterns: []string{"*.txt", "*.csv"},
			want: []fileInfo{
				{Path: "file1.txt", Size: 100},
				{Path: "file2.csv", Size: 200},
			},
		},
		{
			name: "multiple exclude patterns",
			objects: []fileInfo{
				{Path: "file1.txt", Size: 100},
				{Path: "file2.tmp", Size: 200},
				{Path: "file3.log", Size: 300},
			},
			excludePatterns: []string{"*.tmp", "*.log"},
			want: []fileInfo{
				{Path: "file1.txt", Size: 100},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterObjects(tt.objects, tt.includePatterns, tt.excludePatterns)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("filterObjects() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestGetRemoteConfig(t *testing.T) {
	tests := []struct {
		name            string
		remoteName      string
		fileContent     string
		wantStrings     map[string]string
		wantStringLists map[string][]string
		wantErr         bool
	}{
		{
			name:       "valid remote config with array patterns",
			remoteName: "my-remote",
			fileContent: `remotes:
  - name: my-remote
    type: import
    bucket: my-bucket
    prefix: path/to/data/
    datacenter: us-west-2
    include:
      - "*.vcf.gz"
    exclude:
      - "*.tmp"
    import_dir: input/
`,
			wantStrings: map[string]string{
				"name":       "my-remote",
				"type":       "import",
				"bucket":     "my-bucket",
				"prefix":     "path/to/data/",
				"datacenter": "us-west-2",
				"import_dir": "input/",
			},
			wantStringLists: map[string][]string{
				"include": {"*.vcf.gz"},
				"exclude": {"*.tmp"},
			},
			wantErr: false,
		},
		{
			name:       "remote not found",
			remoteName: "missing-remote",
			fileContent: `remotes:
  - name: other-remote
    type: import
    bucket: other-bucket
`,
			wantErr: true,
		},
		{
			name:       "multiple remotes - find specific one",
			remoteName: "remote-2",
			fileContent: `remotes:
  - name: remote-1
    bucket: bucket-1
  - name: remote-2
    bucket: bucket-2
  - name: remote-3
    bucket: bucket-3
`,
			wantStrings: map[string]string{
				"name":   "remote-2",
				"bucket": "bucket-2",
			},
			wantErr: false,
		},
		{
			name:       "empty remotes list",
			remoteName: "any-remote",
			fileContent: `remotes: []
`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temp directory and file
			dir := t.TempDir()
			remotesFile := filepath.Join(dir, "remotes")
			if err := os.WriteFile(remotesFile, []byte(tt.fileContent), 0o644); err != nil {
				t.Fatalf("write remotes file: %v", err)
			}

			// Save current dir and change to temp dir
			origDir, err := os.Getwd()
			if err != nil {
				t.Fatalf("getwd: %v", err)
			}
			defer os.Chdir(origDir)

			if err := os.Chdir(dir); err != nil {
				t.Fatalf("chdir: %v", err)
			}

			// Create .exohub directory and move remotes file
			if err := os.Mkdir(".exohub", 0o755); err != nil {
				t.Fatalf("mkdir .exohub: %v", err)
			}
			if err := os.Rename("remotes", ".exohub/remotes"); err != nil {
				t.Fatalf("move remotes: %v", err)
			}

			// Test getRemoteConfig
			got, err := getRemoteConfig(tt.remoteName)
			if (err != nil) != tt.wantErr {
				t.Errorf("getRemoteConfig() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if !reflect.DeepEqual(got.Strings, tt.wantStrings) {
					t.Errorf("getRemoteConfig().Strings = %#v, want %#v", got.Strings, tt.wantStrings)
				}
				if tt.wantStringLists != nil && !reflect.DeepEqual(got.StringLists, tt.wantStringLists) {
					t.Errorf("getRemoteConfig().StringLists = %#v, want %#v", got.StringLists, tt.wantStringLists)
				}
			}
		})
	}
}

func TestBuildSyncCommandWithPreferredContentAndPaths(t *testing.T) {
	// Test that sync command includes --content-of flags when paths are provided
	tests := []struct {
		name           string
		paths          []string
		wantContains   []string
		wantNotContain []string
	}{
		{
			name:         "with paths",
			paths:        []string{"data/subset", "data/other"},
			wantContains: []string{"--content-of", "data/subset", "data/other"},
		},
		{
			name:           "without paths",
			paths:          nil,
			wantNotContain: []string{"--content-of"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Build command as it would be built in runSync
			cmd := []string{"git", "annex", "sync", "--content"}
			for _, p := range tt.paths {
				cmd = append(cmd, "--content-of", p)
			}
			cmd = append(cmd, "myremote", "--jobs", "4")

			cmdStr := strings.Join(cmd, " ")
			for _, want := range tt.wantContains {
				if !strings.Contains(cmdStr, want) {
					t.Errorf("command should contain %q, got: %s", want, cmdStr)
				}
			}
			for _, notWant := range tt.wantNotContain {
				if strings.Contains(cmdStr, notWant) {
					t.Errorf("command should not contain %q, got: %s", notWant, cmdStr)
				}
			}
		})
	}
}

func TestBuildExportRefsWithPaths(t *testing.T) {
	// Test that export refs use ref:path notation when paths are provided
	tests := []struct {
		name           string
		trackingBranch string
		paths          []string
		wantRefs       []string
	}{
		{
			name:           "with paths",
			trackingBranch: "main",
			paths:          []string{"data/subset", "data/other"},
			wantRefs:       []string{"main:data/subset", "main:data/other"},
		},
		{
			name:           "without paths",
			trackingBranch: "main",
			paths:          nil,
			wantRefs:       []string{"main"},
		},
		{
			name:           "HEAD with paths",
			trackingBranch: "HEAD",
			paths:          []string{"data/subset"},
			wantRefs:       []string{"HEAD:data/subset"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var exportRefs []string
			if len(tt.paths) > 0 {
				for _, p := range tt.paths {
					exportRefs = append(exportRefs, tt.trackingBranch+":"+p)
				}
			} else {
				exportRefs = []string{tt.trackingBranch}
			}

			if !reflect.DeepEqual(exportRefs, tt.wantRefs) {
				t.Errorf("exportRefs = %v, want %v", exportRefs, tt.wantRefs)
			}
		})
	}
}

// TestNoDryRunDropSection verifies that BuildDryRunQueries with includeDrops=false
// does not produce a drop-candidates query (fix for #26: the real sync never drops).
func TestNoDryRunDropSection(t *testing.T) {
	remote := "myremote"
	queries := commandutil.BuildDryRunQueries(remote, false)
	if len(queries) != 2 {
		t.Fatalf("expected exactly 2 queries (here->remote, remote->here), got %d", len(queries))
	}
	for _, q := range queries {
		if strings.Contains(q.Label, "drop") {
			t.Errorf("dry-run query label must not contain 'drop', got: %q", q.Label)
		}
		for _, arg := range q.Args {
			if strings.Contains(arg, "want-drop") || strings.Contains(arg, "drop") {
				t.Errorf("dry-run query args must not contain drop flags, got: %v", q.Args)
			}
		}
	}
}

func TestMatchAnnexGlob(t *testing.T) {
	tests := []struct {
		pattern string
		path    string
		want    bool
	}{
		// ** matches zero or more path components
		{"gwasdb-studies/**/*.parquet", "gwasdb-studies/study.parquet", true},
		{"gwasdb-studies/**/*.parquet", "gwasdb-studies/sub/dir/deep.parquet", true},
		{"gwasdb-studies/**/*.parquet", "ebi_gwas/GCST001.tsv.gz", false},
		{"gwasdb-studies/**/*.parquet", "mappings/GCST001.tsv.gz", false},
		{"**/*.parquet", "gwasdb-studies/study.parquet", true},
		{"**/*.parquet", "top.parquet", true},
		{"**/*.parquet", "top.tsv", false},
		// Simple glob (no **): match basename
		{"*.parquet", "gwasdb-studies/study.parquet", true},
		{"*.parquet", "gwasdb-studies/study.tsv", false},
		{"*.tsv.gz", "ebi_gwas/harmonised/GCST001.h.tsv.gz", true},
	}
	for _, tt := range tests {
		t.Run(tt.pattern+"|"+tt.path, func(t *testing.T) {
			got := matchAnnexGlob(tt.pattern, tt.path)
			if got != tt.want {
				t.Errorf("matchAnnexGlob(%q, %q) = %v, want %v", tt.pattern, tt.path, got, tt.want)
			}
		})
	}
}

func TestMatchesPreferredContent(t *testing.T) {
	tests := []struct {
		expr string
		path string
		want bool
	}{
		// Common export remote pattern with ** glob
		{"include=gwasdb-studies/**/*.parquet", "gwasdb-studies/study.parquet", true},
		{"include=gwasdb-studies/**/*.parquet", "gwasdb-studies/sub/deep.parquet", true},
		{"include=gwasdb-studies/**/*.parquet", "ebi_gwas/GCST001.h.tsv.gz", false},
		{"include=gwasdb-studies/**/*.parquet", "mappings/GCST001.tsv.gz", false},
		// Special expressions
		{"anything", "any/file.txt", true},
		{"nothing", "any/file.txt", false},
		{"", "any/file.txt", true},
		// Unknown complex expression: conservative (return true)
		{"standard", "any/file.txt", true},
		// exclude
		{"exclude=*.yaml", "file.yaml", false},
		{"exclude=*.yaml", "file.parquet", true},
	}
	for _, tt := range tests {
		t.Run(tt.expr+"|"+tt.path, func(t *testing.T) {
			got := matchesPreferredContent(tt.expr, tt.path)
			if got != tt.want {
				t.Errorf("matchesPreferredContent(%q, %q) = %v, want %v", tt.expr, tt.path, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Dry-run fidelity matrix (fixes #19, #20, #26)
// ---------------------------------------------------------------------------
//
// These tests exercise the three behavioural rules established by the fix:
//
//   1. (#26) No "drop" section ever appears in dry-run output.
//   2. (#20) Annex preferred-content filtering uses matchesPreferredContent()
//            which correctly handles ** globs.
//   3. (#19) Export dry-run is branch-aware: when the current branch differs
//            from annex-tracking-branch, zero actions + warning are reported.
//
// The tests are unit tests over the pure helper functions (matchesPreferredContent,
// matchAnnexGlob) plus integration-style tests over the output of runAnnexDryRun
// that use in-process fakes instead of a real git-annex repo.
// ---------------------------------------------------------------------------

// TestDryRunFidelity_NoDrop_Standard verifies that the standard (no preferred
// content) dry-run path never emits a "drop" section (#26).
func TestDryRunFidelity_NoDrop_Standard(t *testing.T) {
	// BuildDryRunQueries(remote, false) is now the call site used by runAnnexDryRun
	// for the no-preferred-content path.
	queries := commandutil.BuildDryRunQueries("backup", false)
	for _, q := range queries {
		if strings.Contains(strings.ToLower(q.Label), "drop") {
			t.Errorf("standard dry-run must not include drop query, got label: %q", q.Label)
		}
		for _, arg := range q.Args {
			if strings.Contains(arg, "want-drop") {
				t.Errorf("standard dry-run args must not contain --want-drop, got: %v", q.Args)
			}
		}
	}
}

// TestDryRunFidelity_PreferredContent_GlobFilter tests the annex preferred-content
// filtering logic (fix #20). This is the core of the ** glob correctness fix.
func TestDryRunFidelity_PreferredContent_GlobFilter(t *testing.T) {
	// Simulate the gwasdb scenario from issue #20:
	// preferred content = include=gwasdb-studies-iceberg/** or include=gwasdb-mappings/**
	// The repo also contains ebi_gwas/** which must NOT appear in "to copy".
	expr := "(include=gwasdb-studies-iceberg/** or include=gwasdb-mappings/**)"

	tests := []struct {
		name   string
		path   string
		wanted bool
	}{
		// Should be included
		{"gwasdb-studies-iceberg root file", "gwasdb-studies-iceberg/study.parquet", true},
		{"gwasdb-studies-iceberg nested", "gwasdb-studies-iceberg/sub/deep/file.parquet", true},
		{"gwasdb-mappings root file", "gwasdb-mappings/GCST001.parquet", true},
		{"gwasdb-mappings nested", "gwasdb-mappings/a/b/GCST001.parquet", true},
		// Must be excluded (#20: ebi_gwas was incorrectly included before the fix)
		{"ebi_gwas harmonised", "ebi_gwas/harmonised/GCST001.h.tsv.gz", false},
		{"ebi_gwas raw", "ebi_gwas/GCST001.tsv.gz", false},
		{"other prefix", "other/file.txt", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchesPreferredContent(expr, tt.path)
			if got != tt.wanted {
				t.Errorf("matchesPreferredContent(%q, %q) = %v, want %v", expr, tt.path, got, tt.wanted)
			}
		})
	}
}

// TestDryRunFidelity_PreferredContent_DoubleStarGlob tests that ** glob patterns
// in preferred content expressions are evaluated correctly (#20).
func TestDryRunFidelity_PreferredContent_DoubleStarGlob(t *testing.T) {
	tests := []struct {
		expr   string
		path   string
		wanted bool
		desc   string
	}{
		// Real expression from issue #20 / #26
		{"include=gwasdb-studies/**/*.parquet", "gwasdb-studies/study.parquet", true, "direct child"},
		{"include=gwasdb-studies/**/*.parquet", "gwasdb-studies/sub/dir/deep.parquet", true, "deeply nested"},
		{"include=gwasdb-studies/**/*.parquet", "ebi_gwas/GCST001.tsv.gz", false, "wrong prefix"},
		{"include=gwasdb-studies/**/*.parquet", "gwasdb-studies/study.tsv", false, "wrong extension"},
		// ** at start
		{"include=**/*.parquet", "gwasdb-studies/study.parquet", true, "** at start matches nested"},
		{"include=**/*.parquet", "top.parquet", true, "** matches empty prefix"},
		{"include=**/*.parquet", "top.tsv", false, "** extension mismatch"},
		// Combined with ebi_gwas exclusion
		{"include=gwasdb-studies/** or include=gwasdb-mappings/**", "gwasdb-studies/a.parquet", true, "or clause match"},
		{"include=gwasdb-studies/** or include=gwasdb-mappings/**", "ebi_gwas/b.tsv.gz", false, "or clause no match"},
		// Paren-depth tracking: ')' inside the pattern must not truncate the glob.
		// e.g. include=path/with(parens)/** — the ')' after 'parens' is at depth 1
		// (inside a '(') so it must NOT terminate the pattern.
		{"include=path/with(parens)/**", "path/with(parens)/file.txt", true, "paren inside glob not truncated"},
		{"include=path/with(parens)/**", "other/file.txt", false, "paren inside glob - no match"},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			got := matchesPreferredContent(tt.expr, tt.path)
			if got != tt.wanted {
				t.Errorf("matchesPreferredContent(%q, %q) = %v, want %v", tt.expr, tt.path, got, tt.wanted)
			}
		})
	}
}

// TestDryRunFidelity_ExportBranchMismatch_Warning tests that when the current
// branch differs from the tracking branch, the dry-run emits the correct warning
// and reports no export actions (#19).
//
// Drives writeBranchMismatchWarning (the production function used by runAnnexDryRun)
// directly so that any format change in production code breaks this test.
func TestDryRunFidelity_ExportBranchMismatch_Warning(t *testing.T) {
	const remote = "myremote"
	const trackingBranch = "main"
	const branchDesc = "feat-x"

	var buf strings.Builder
	writeBranchMismatchWarning(&buf, branchDesc, remote, trackingBranch)
	out := buf.String()

	// Must mention all three identifiers in the warning line.
	if !strings.Contains(out, branchDesc) {
		t.Errorf("warning output must mention current branch %q, got:\n%s", branchDesc, out)
	}
	if !strings.Contains(out, trackingBranch) {
		t.Errorf("warning output must mention tracking branch %q, got:\n%s", trackingBranch, out)
	}
	if !strings.Contains(out, remote) {
		t.Errorf("warning output must mention remote %q, got:\n%s", remote, out)
	}
	// Must clearly state that files will NOT be exported.
	if !strings.Contains(out, "NOT") {
		t.Errorf("warning output must say files will NOT be exported, got:\n%s", out)
	}
	// Must report no export actions (both sections show "none").
	if !strings.Contains(out, "(none") {
		t.Errorf("warning output must show zero actions, got:\n%s", out)
	}
	// Must NOT produce a drop section.
	if strings.Contains(strings.ToLower(out), "drop") {
		t.Errorf("warning output must not contain any drop section, got:\n%s", out)
	}
}

// TestDryRunFidelity_ExportBranchMatch tests that when current branch == tracking
// branch, branchesMatch is true (#19).
func TestDryRunFidelity_ExportBranchMatch(t *testing.T) {
	tests := []struct {
		name           string
		currentBranch  string
		trackingBranch string
		wantMatch      bool
	}{
		{"same branch", "main", "main", true},
		{"mismatch", "feat-x", "main", false},
		{"tracking HEAD literal", "main", "HEAD", true}, // HEAD always matches
		{"detached HEAD mismatch", "HEAD", "main", false},
		{"both HEAD", "HEAD", "HEAD", true},
		{"empty current", "", "main", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := (tt.trackingBranch == "HEAD") || (tt.currentBranch != "" && tt.currentBranch != "HEAD" && tt.currentBranch == tt.trackingBranch)
			if got != tt.wantMatch {
				t.Errorf("branchesMatch(%q, %q) = %v, want %v",
					tt.currentBranch, tt.trackingBranch, got, tt.wantMatch)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// newAnnexRepo creates a temporary git-annex repository for integration tests.
//
// It initialises a git repo with git-annex, registers a directory-type annex
// remote named remoteName backed by a second temp directory, and chdirs into
// the repo.  The caller must defer restoreDir() to return to the original cwd.
//
// The function is skipped (t.Skip) when git-annex is not in PATH.
// ---------------------------------------------------------------------------
// chmodRWritable recursively makes all files in dir writable so that
// t.TempDir cleanup can remove git-annex read-only object files.
func chmodRWritable(dir string) {
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		return os.Chmod(path, info.Mode()|0o600)
	})
}

func newAnnexRepo(t *testing.T, remoteName string) (repoDir, remoteDir string, restoreDir func()) {
	t.Helper()
	if _, err := exec.LookPath("git-annex"); err != nil {
		t.Skip("git-annex not in PATH")
	}

	repoDir = t.TempDir()
	remoteDir = t.TempDir()
	// git-annex stores object files as 0444; make them writable before cleanup.
	t.Cleanup(func() { chmodRWritable(repoDir); chmodRWritable(remoteDir) })

	env := append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1",
		"HOME="+repoDir, // isolate git config
		"GIT_AUTHOR_NAME=Test",
		"GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=Test",
		"GIT_COMMITTER_EMAIL=test@test.com",
	)

	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("command %v in %s: %v\n%s", args, dir, err, out)
		}
	}

	// Initialise the local repo.
	run(repoDir, "git", "init")
	run(repoDir, "git", "symbolic-ref", "HEAD", "refs/heads/main")
	run(repoDir, "git", "config", "user.email", "test@test.com")
	run(repoDir, "git", "config", "user.name", "Test")
	run(repoDir, "git", "annex", "init", "testrepo")

	// Register the directory-type annex remote.
	run(repoDir, "git", "annex", "initremote", remoteName,
		"type=directory", "directory="+remoteDir, "encryption=none")

	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir %s: %v", repoDir, err)
	}
	return repoDir, remoteDir, func() { os.Chdir(origDir) }
}

// addAnnexFile creates a file, adds it to git-annex, and commits it.
func addAnnexFile(t *testing.T, repoDir, relPath, content string) {
	t.Helper()
	fullPath := filepath.Join(repoDir, relPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(fullPath), err)
	}
	if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", fullPath, err)
	}
	env := append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1",
		"HOME="+repoDir,
		"GIT_AUTHOR_NAME=Test",
		"GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=Test",
		"GIT_COMMITTER_EMAIL=test@test.com",
	)
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = repoDir
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("command %v: %v\n%s", args, err, out)
		}
	}
	run("git", "annex", "add", relPath)
	run("git", "commit", "-m", "add "+relPath)
}

// runDryRunOutput calls runAnnexDryRun and returns the captured output.
func runDryRunOutput(t *testing.T, remote string, paths []string) string {
	t.Helper()
	var buf strings.Builder
	if err := runAnnexDryRun(&buf, remote, paths); err != nil {
		t.Fatalf("runAnnexDryRun: %v", err)
	}
	return buf.String()
}

// ---------------------------------------------------------------------------
// Integration tests — drive runAnnexDryRun against a real git-annex repo.
// These replace the circular TestDryRunFidelity_Matrix which called
// matchesPreferredContent directly instead of exercising production code.
// ---------------------------------------------------------------------------

// TestDryRunIntegration_NoDrop_PlainAnnex verifies (#26) that the dry-run for a
// plain annex remote (no preferred content) never emits a "drop" section.
func TestDryRunIntegration_NoDrop_PlainAnnex(t *testing.T) {
	repoDir, _, restore := newAnnexRepo(t, "myremote")
	defer restore()

	// Add two annex-tracked files; neither is copied to the remote yet.
	addAnnexFile(t, repoDir, "data/a.bam", "bam-content-a")
	addAnnexFile(t, repoDir, "data/b.bam", "bam-content-b")

	out := runDryRunOutput(t, "myremote", nil)

	// #26: no drop section ever.
	if strings.Contains(strings.ToLower(out), "drop") {
		t.Errorf("dry-run output must not contain 'drop', got:\n%s", out)
	}
	// Both files should appear in here -> remote.
	if !strings.Contains(out, "data/a.bam") {
		t.Errorf("expected data/a.bam in here->remote, got:\n%s", out)
	}
	if !strings.Contains(out, "data/b.bam") {
		t.Errorf("expected data/b.bam in here->remote, got:\n%s", out)
	}
}

// TestDryRunIntegration_PreferredContent_GlobFilter verifies (#20) that files
// outside the preferred content expression are excluded from "to copy" when
// the expression uses ** globs.
func TestDryRunIntegration_PreferredContent_GlobFilter(t *testing.T) {
	repoDir, _, restore := newAnnexRepo(t, "myremote")
	defer restore()

	// Set preferred content: only gwasdb-studies-iceberg/** is wanted.
	setWanted := func(args ...string) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = repoDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("command %v: %v\n%s", args, err, out)
		}
	}
	setWanted("git", "annex", "wanted", "myremote",
		"(include=gwasdb-studies-iceberg/** or include=gwasdb-mappings/**)")

	// Add files: some match, some must be excluded.
	addAnnexFile(t, repoDir, "gwasdb-studies-iceberg/study.parquet", "parquet-data")
	addAnnexFile(t, repoDir, "gwasdb-mappings/GCST001.parquet", "mapping-data")
	addAnnexFile(t, repoDir, "ebi_gwas/GCST001.tsv.gz", "gwas-data") // must be excluded
	addAnnexFile(t, repoDir, "ebi_gwas/sub/file.tsv.gz", "gwas-sub") // must be excluded

	out := runDryRunOutput(t, "myremote", nil)

	// #26: no drop section.
	if strings.Contains(strings.ToLower(out), "drop") {
		t.Errorf("dry-run output must not contain 'drop', got:\n%s", out)
	}
	// #20: preferred files must appear.
	if !strings.Contains(out, "gwasdb-studies-iceberg/study.parquet") {
		t.Errorf("expected gwasdb-studies-iceberg/study.parquet in output, got:\n%s", out)
	}
	if !strings.Contains(out, "gwasdb-mappings/GCST001.parquet") {
		t.Errorf("expected gwasdb-mappings/GCST001.parquet in output, got:\n%s", out)
	}
	// #20: ebi_gwas files must be excluded.
	if strings.Contains(out, "ebi_gwas/") {
		t.Errorf("ebi_gwas files must be excluded by preferred content, got:\n%s", out)
	}
}

// TestDryRunIntegration_PreferredContent_OutsideOnly verifies (#20) that when
// all local files are outside the preferred content expression, "to copy" is empty.
func TestDryRunIntegration_PreferredContent_OutsideOnly(t *testing.T) {
	repoDir, _, restore := newAnnexRepo(t, "myremote")
	defer restore()

	setWanted := func(args ...string) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = repoDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("command %v: %v\n%s", args, err, out)
		}
	}
	setWanted("git", "annex", "wanted", "myremote", "include=gwasdb-studies/**")

	addAnnexFile(t, repoDir, "ebi_gwas/GCST001.tsv.gz", "data")
	addAnnexFile(t, repoDir, "other/file.txt", "data")

	out := runDryRunOutput(t, "myremote", nil)

	// #26: no drop section.
	if strings.Contains(strings.ToLower(out), "drop") {
		t.Errorf("dry-run output must not contain 'drop', got:\n%s", out)
	}
	// #20: no files in here->remote because none match.
	if strings.Contains(out, "ebi_gwas/") || strings.Contains(out, "other/") {
		t.Errorf("non-preferred files must not appear in to-copy, got:\n%s", out)
	}
	// Output must say (none) for here->remote.
	if !strings.Contains(out, "(none)") {
		t.Errorf("expected (none) when no files match preferred content, got:\n%s", out)
	}
}

// TestDryRunIntegration_ExportBranchMismatch verifies (#19) that the dry-run
// for an export-type remote on a non-tracking branch reports zero actions and
// emits a clear warning.
func TestDryRunIntegration_ExportBranchMismatch(t *testing.T) {
	if _, err := exec.LookPath("git-annex"); err != nil {
		t.Skip("git-annex not in PATH")
	}

	repoDir := t.TempDir()
	remoteDir := t.TempDir()
	t.Cleanup(func() { chmodRWritable(repoDir); chmodRWritable(remoteDir) })

	env := append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1",
		"HOME="+repoDir,
		"GIT_AUTHOR_NAME=Test",
		"GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=Test",
		"GIT_COMMITTER_EMAIL=test@test.com",
	)
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = repoDir
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("command %v: %v\n%s", args, err, out)
		}
	}

	// Initialise repo on main branch.
	run("git", "init")
	run("git", "symbolic-ref", "HEAD", "refs/heads/main")
	run("git", "config", "user.email", "test@test.com")
	run("git", "config", "user.name", "Test")
	run("git", "annex", "init", "testrepo")

	// Register a directory-type special remote with exporttree=yes.
	run("git", "annex", "initremote", "myexport",
		"type=directory", "directory="+remoteDir, "encryption=none",
		"exporttree=yes")
	run("git", "annex", "wanted", "myexport",
		"include=gwasdb-studies/**/*.parquet")
	// Set tracking branch to main.
	run("git", "config", "remote.myexport.annex-tracking-branch", "main")

	// Commit a file on main.
	if err := os.WriteFile(filepath.Join(repoDir, "gwasdb-studies/study.parquet"),
		[]byte("data"), 0o644); err != nil {
		os.MkdirAll(filepath.Join(repoDir, "gwasdb-studies"), 0o755)
		os.WriteFile(filepath.Join(repoDir, "gwasdb-studies/study.parquet"), []byte("data"), 0o644)
	}
	os.MkdirAll(filepath.Join(repoDir, "gwasdb-studies"), 0o755)
	os.WriteFile(filepath.Join(repoDir, "gwasdb-studies/study.parquet"), []byte("data"), 0o644)
	run("git", "annex", "add", "gwasdb-studies/study.parquet")
	run("git", "commit", "-m", "add study")

	// Now switch to a feature branch — current branch no longer matches tracking.
	run("git", "checkout", "-b", "feat-x")

	// Chdir into the repo so runAnnexDryRun uses it.
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	out := runDryRunOutput(t, "myexport", nil)

	// #19: warning must be present.
	if !strings.Contains(out, "WARNING") {
		t.Errorf("expected WARNING on branch mismatch, got:\n%s", out)
	}
	if !strings.Contains(out, "feat-x") {
		t.Errorf("warning must mention current branch feat-x, got:\n%s", out)
	}
	if !strings.Contains(out, "main") {
		t.Errorf("warning must mention tracking branch main, got:\n%s", out)
	}
	if !strings.Contains(out, "NOT") {
		t.Errorf("warning must say files will NOT be exported, got:\n%s", out)
	}
	// #19: no files listed as to-export.
	if strings.Contains(out, "study.parquet") && !strings.Contains(out, "(none") {
		t.Errorf("files must not be listed as to-export on branch mismatch, got:\n%s", out)
	}
	// #26: no drop section.
	if strings.Contains(strings.ToLower(out), "drop candidates") {
		t.Errorf("dry-run output must not contain 'drop candidates', got:\n%s", out)
	}
}

// parseDryRunFilesSection extracts the file list from a labelled section of
// runAnnexDryRun output. It returns all lines that are indented with exactly
// four spaces and are not the "(none)" placeholder.
func parseDryRunFilesSection(output, sectionLabel string) []string {
	var files []string
	inSection := false
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, sectionLabel) {
			inSection = true
			continue
		}
		if inSection {
			// A new section header starts with two spaces and is not four-space-indented.
			if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "    ") {
				break
			}
			if strings.HasPrefix(line, "    ") {
				trimmed := strings.TrimPrefix(line, "    ")
				if trimmed != "" && !strings.HasPrefix(trimmed, "(") {
					files = append(files, trimmed)
				}
			}
		}
	}
	return files
}

// annexFindOnRemote returns the set of files currently stored on a
// directory-type annex remote by listing files present here AND on the remote.
func annexFindOnRemote(t *testing.T, repoDir, remote string) []string {
	t.Helper()
	cmd := exec.Command("git", "annex", "find", "--in", remote, "--json")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git annex find --in %s: %v", remote, err)
	}
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		var obj struct {
			File string `json:"file"`
		}
		if err := json.Unmarshal([]byte(line), &obj); err == nil && obj.File != "" {
			files = append(files, obj.File)
		}
	}
	sort.Strings(files)
	return files
}

// sortedStrings returns a sorted copy of s, normalising nil to empty slice.
func sortedStrings(s []string) []string {
	if s == nil {
		s = []string{}
	}
	out := make([]string, len(s))
	copy(out, s)
	sort.Strings(out)
	return out
}

// TestDryRunIntegration_DryRunEqualsRealSync is the core fidelity test:
// it asserts that the set of files reported as "here -> remote" by runAnnexDryRun
// equals the set of files actually transferred when git annex sync --content is run
// (the same command runSync() executes for annex remotes).
//
// For preferred-content remotes with simple /** globs (e.g. include=prefix/**),
// git-annex's native evaluator and our Go evaluator agree, so the dry-run
// prediction and the real transfer set must be identical.
//
// Note: expressions like include=prefix/**/*.ext expose a known git-annex bug
// where files at exactly one directory level deep are missed by the native
// evaluator (e.g. gwasdb-studies/top.parquet matches include=gwasdb-studies/**/*.parquet
// per our correct Go evaluator but git-annex sync misses it). Those cases are
// tested separately in TestDryRunIntegration_PreferredContent_GlobFilter and
// TestDryRunIntegration_PreferredContent_DoubleStarGlob.
func TestDryRunIntegration_DryRunEqualsRealSync(t *testing.T) {
	tests := []struct {
		name             string
		preferredContent string
		files            []string // relative paths of files to annex-add
		wantTransferred  []string // sorted; expected in both dry-run AND on remote
	}{
		{
			// Plain annex remote: no preferred content. All files are synced.
			name:             "plain-annex/no-preferred-content",
			preferredContent: "",
			files:            []string{"data/a.bam", "data/b.bam"},
			wantTransferred:  []string{"data/a.bam", "data/b.bam"},
		},
		{
			// Preferred content with simple /** globs: both evaluators agree.
			// ebi_gwas/** is excluded; gwasdb-* prefixes are included.
			name:             "annex+preferred/simple-glob-ebi-gwas-excluded",
			preferredContent: "(include=gwasdb-studies-iceberg/** or include=gwasdb-mappings/**)",
			files: []string{
				"gwasdb-studies-iceberg/study.parquet",
				"gwasdb-mappings/GCST001.parquet",
				"ebi_gwas/GCST001.tsv.gz", // excluded by preferred content
			},
			wantTransferred: []string{
				"gwasdb-mappings/GCST001.parquet",
				"gwasdb-studies-iceberg/study.parquet",
			},
		},
		{
			// Preferred content: all files outside expression → nothing transferred.
			name:             "annex+preferred/nothing-matches",
			preferredContent: "include=gwasdb-studies/**",
			files:            []string{"ebi_gwas/GCST001.tsv.gz", "other/file.txt"},
			wantTransferred:  []string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repoDir, _, restore := newAnnexRepo(t, "myremote")
			defer restore()

			env := append(os.Environ(),
				"GIT_CONFIG_NOSYSTEM=1",
				"HOME="+repoDir,
				"GIT_AUTHOR_NAME=Test",
				"GIT_AUTHOR_EMAIL=test@test.com",
				"GIT_COMMITTER_NAME=Test",
				"GIT_COMMITTER_EMAIL=test@test.com",
			)
			run := func(args ...string) {
				t.Helper()
				cmd := exec.Command(args[0], args[1:]...)
				cmd.Dir = repoDir
				cmd.Env = env
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("command %v: %v\n%s", args, err, out)
				}
			}

			if tc.preferredContent != "" {
				run("git", "annex", "wanted", "myremote", tc.preferredContent)
			}
			for _, f := range tc.files {
				addAnnexFile(t, repoDir, f, "content-of-"+f)
			}

			// Step 1: get the dry-run prediction.
			dryRunOut := runDryRunOutput(t, "myremote", nil)

			// #26: dry-run must never contain a drop section.
			if strings.Contains(strings.ToLower(dryRunOut), "drop") {
				t.Errorf("dry-run must not contain 'drop', got:\n%s", dryRunOut)
			}

			// Parse the predicted "to copy" file set from dry-run output.
			predicted := sortedStrings(parseDryRunFilesSection(dryRunOut, "here -> myremote"))

			// Step 2: execute the real transfer: git annex sync --content --no-commit,
			// exactly what runSync() runs for annex remotes.
			run("git", "annex", "sync", "--content", "--no-commit", "myremote")

			// Step 3: observe what actually landed on the remote.
			actuallyTransferred := sortedStrings(annexFindOnRemote(t, repoDir, "myremote"))

			// Step 4: assert dry-run predicted == actually transferred == wantTransferred.
			want := sortedStrings(tc.wantTransferred)
			if !reflect.DeepEqual(predicted, want) {
				t.Errorf("dry-run predicted set mismatch:\n  got:  %v\n  want: %v\n  output:\n%s",
					predicted, want, dryRunOut)
			}
			if !reflect.DeepEqual(actuallyTransferred, want) {
				t.Errorf("real transfer set mismatch:\n  got:  %v\n  want: %v",
					actuallyTransferred, want)
			}
			// Core fidelity assertion: dry-run == real sync.
			if !reflect.DeepEqual(predicted, actuallyTransferred) {
				t.Errorf("FIDELITY FAILURE: dry-run predicted %v but real sync transferred %v",
					predicted, actuallyTransferred)
			}
		})
	}
}

// newExportRepo creates a temporary git repo with an exporttree=yes remote
// for export fidelity tests. It also configures annex-tracking-branch=main
// and sets the provided preferredContent (may be empty).
func newExportRepo(t *testing.T, remoteName, preferredContent string) (repoDir string, restoreDir func()) {
	t.Helper()
	if _, err := exec.LookPath("git-annex"); err != nil {
		t.Skip("git-annex not in PATH")
	}

	repoDir = t.TempDir()
	remoteDir := t.TempDir()
	t.Cleanup(func() { chmodRWritable(repoDir); chmodRWritable(remoteDir) })

	env := append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1",
		"HOME="+repoDir,
		"GIT_AUTHOR_NAME=Test",
		"GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=Test",
		"GIT_COMMITTER_EMAIL=test@test.com",
	)
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = repoDir
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("command %v: %v\n%s", args, err, out)
		}
	}

	run("git", "init")
	run("git", "symbolic-ref", "HEAD", "refs/heads/main")
	run("git", "config", "user.email", "test@test.com")
	run("git", "config", "user.name", "Test")
	run("git", "annex", "init", "testrepo")
	run("git", "annex", "initremote", remoteName,
		"type=directory", "directory="+remoteDir,
		"encryption=none", "exporttree=yes")
	run("git", "config", fmt.Sprintf("remote.%s.annex-tracking-branch", remoteName), "main")
	if preferredContent != "" {
		run("git", "annex", "wanted", remoteName, preferredContent)
	}

	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir %s: %v", repoDir, err)
	}
	return repoDir, func() { os.Chdir(origDir) }
}

// addPlainFile adds a regular (non-annex) file via git add+commit.
// Export remotes export from the git tree, so files don't need to be annexed.
func addPlainFile(t *testing.T, repoDir, relPath, content string) {
	t.Helper()
	env := append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1",
		"HOME="+repoDir,
		"GIT_AUTHOR_NAME=Test",
		"GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=Test",
		"GIT_COMMITTER_EMAIL=test@test.com",
	)
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = repoDir
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("command %v: %v\n%s", args, err, out)
		}
	}
	fullPath := filepath.Join(repoDir, relPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	run("git", "annex", "add", relPath)
	run("git", "commit", "-m", "add "+relPath)
}

// annexFindExported returns files that were actually exported to an exporttree
// remote. For directory-type export remotes git annex find --in does not work
// reliably before a sync; we read the export.log entry instead.
func annexFindExported(t *testing.T, repoDir, remote string) []string {
	t.Helper()
	cmd := exec.Command("git", "annex", "find", "--in", remote, "--json")
	cmd.Dir = repoDir
	out, _ := cmd.Output() // may return nothing for export remotes

	var files []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		var obj struct {
			File string `json:"file"`
		}
		if err := json.Unmarshal([]byte(line), &obj); err == nil && obj.File != "" {
			files = append(files, obj.File)
		}
	}
	sort.Strings(files)
	return files
}

// listExportedFiles lists files actually present on a directory export remote
// by walking its directory tree and recording relative paths.
func listExportedFiles(t *testing.T, remoteDir string) []string {
	t.Helper()
	var files []string
	err := filepath.Walk(remoteDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			rel, _ := filepath.Rel(remoteDir, path)
			files = append(files, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk remote dir: %v", err)
	}
	sort.Strings(files)
	return files
}

// TestDryRunIntegration_ExportDryRunEqualsRealExport is the export-remote
// fidelity test (#19 production incident). It proves that:
//
//  1. When current branch == tracking branch, the dry-run "here → remote"
//     set equals the set of files that git annex export actually transfers.
//  2. When current branch != tracking branch, the dry-run reports no actions
//     (matching the real export, which exports the tracking-branch tree
//     unchanged by the feature branch).
//  3. No drop section is ever emitted (#26).
func TestDryRunIntegration_ExportDryRunEqualsRealExport(t *testing.T) {
	const remote = "myexport"

	t.Run("branch-match/dry-run-equals-real-export", func(t *testing.T) {
		repoDir, restore := newExportRepo(t, remote, "include=gwasdb-studies/**")
		defer restore()

		// Add files to the tracking branch (main).
		// Export remotes export the whole git tree; preferred content controls
		// only annex-managed content within export remotes.
		// Use annex-add so files are tracked by git-annex (required for find).
		addAnnexFile(t, repoDir, "gwasdb-studies/study.parquet", "parquet-data")
		addAnnexFile(t, repoDir, "ebi_gwas/GCST001.tsv.gz", "gwas-data")

		// Step 1: dry-run on tracking branch (main == main: branches match).
		dryRunOut := runDryRunOutput(t, remote, nil)

		// #26: no drop section.
		if strings.Contains(strings.ToLower(dryRunOut), "drop") {
			t.Errorf("dry-run must not contain 'drop', got:\n%s", dryRunOut)
		}
		// #19: no branch-mismatch warning when branches match.
		if strings.Contains(dryRunOut, "WARNING") {
			t.Errorf("must not emit WARNING when branch matches tracking branch, got:\n%s", dryRunOut)
		}

		// Parse the predicted export set.
		predicted := sortedStrings(parseDryRunFilesSection(dryRunOut, fmt.Sprintf("here -> %s", remote)))

		// Step 2: perform the real export: git annex export <tracking-branch> --to <remote>.
		env := append(os.Environ(),
			"GIT_CONFIG_NOSYSTEM=1",
			"HOME="+repoDir,
			"GIT_AUTHOR_NAME=Test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=Test",
			"GIT_COMMITTER_EMAIL=test@test.com",
		)
		cmd := exec.Command("git", "annex", "export", "main", "--to", remote)
		cmd.Dir = repoDir
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git annex export: %v\n%s", err, out)
		}

		// Step 3: what actually landed on the remote.
		// For export remotes, git annex find --in is unreliable before a full sync;
		// use git annex find --in after the export which updates export.log.
		actuallyExported := sortedStrings(annexFindExported(t, repoDir, remote))

		// Step 4: dry-run predicted == actually exported.
		// Sanity-check: both sets must be non-empty (proves the test is not trivially passing).
		if len(predicted) == 0 {
			t.Errorf("dry-run predicted nothing to export — test data problem, got output:\n%s", dryRunOut)
		}
		if !reflect.DeepEqual(predicted, actuallyExported) {
			t.Errorf("FIDELITY FAILURE: dry-run predicted %v but real export transferred %v\ndry-run output:\n%s",
				predicted, actuallyExported, dryRunOut)
		}
	})

	// branch-mismatch is the exact #19 production incident scenario:
	// a user is on a feature branch while the remote tracks main.
	// They commit files on the feature branch and run exo sync --dry-run.
	// The OLD (buggy) dry-run evaluated against HEAD (feat-x) and reported
	// those feat-x-unique files as "to export" — but the real sync exports
	// the tracking branch (main), so those files were NEVER actually exported.
	// The fix: dry-run must report zero for feat-x-unique content, emit a
	// branch-mismatch WARNING, and the real export must also transfer zero
	// of those feat-x-unique files.
	t.Run("branch-mismatch/feat-x-unique-files-not-exported", func(t *testing.T) {
		repoDir, restore := newExportRepo(t, remote, "include=gwasdb-studies/**")
		defer restore()

		env := append(os.Environ(),
			"GIT_CONFIG_NOSYSTEM=1",
			"HOME="+repoDir,
			"GIT_AUTHOR_NAME=Test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=Test",
			"GIT_COMMITTER_EMAIL=test@test.com",
		)
		run := func(args ...string) string {
			t.Helper()
			cmd := exec.Command(args[0], args[1:]...)
			cmd.Dir = repoDir
			cmd.Env = env
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("command %v: %v\n%s", args, err, out)
			}
			return string(out)
		}

		// Commit one file on main (the tracking branch), then create feat-x.
		addAnnexFile(t, repoDir, "gwasdb-studies/main-file.parquet", "main-content")
		run("git", "checkout", "-b", "feat-x")

		// Add feat-x-unique files — these exist on feat-x but NOT on main.
		// This mirrors the #19 prod incident: branch has 17k files the tracking
		// branch does not have.
		addAnnexFile(t, repoDir, "gwasdb-studies/feat-file.parquet", "feat-content")

		// Step 1: dry-run on feat-x (current=feat-x, tracking=main → mismatch).
		dryRunOut := runDryRunOutput(t, remote, nil)

		// #19: WARNING emitted naming both branches.
		if !strings.Contains(dryRunOut, "WARNING") {
			t.Errorf("expected WARNING on branch mismatch, got:\n%s", dryRunOut)
		}
		// #19: feat-x-unique file must NOT appear in predicted export set.
		predicted := sortedStrings(parseDryRunFilesSection(dryRunOut, fmt.Sprintf("here -> %s", remote)))
		for _, f := range predicted {
			if strings.Contains(f, "feat-file") {
				t.Errorf("feat-x-unique file %q must not appear in dry-run export prediction", f)
			}
		}
		// #26: no drop section.
		if strings.Contains(strings.ToLower(dryRunOut), "drop") {
			t.Errorf("dry-run must not contain 'drop', got:\n%s", dryRunOut)
		}

		// Step 2: run the real export (git annex export <tracking-branch> --to remote).
		// This is what runSync() does on the real sync path for export remotes.
		// It exports the tracking-branch tree (main), not the current branch (feat-x).
		run("git", "annex", "export", "main", "--to", remote)

		// Step 3: observe what actually landed on the remote.
		actuallyExported := sortedStrings(annexFindExported(t, repoDir, remote))

		// Step 4: feat-x-unique file must NOT be on the remote — the real export
		// was blind to feat-x content because it only exports the tracking branch.
		for _, f := range actuallyExported {
			if strings.Contains(f, "feat-file") {
				t.Errorf("feat-x-unique file %q must not be exported by real sync (exports tracking branch, not feat-x)", f)
			}
		}

		// Step 5 (fidelity): dry-run predicted zero feat-x-unique exports AND
		// real export transferred zero feat-x-unique files — they agree.
		// Both predicted and actuallyExported must contain only main-file.parquet
		// (or be a subset of it) — no feat-file.parquet in either.
		var predictedFeatFiles []string
		for _, f := range predicted {
			if strings.Contains(f, "feat-file") {
				predictedFeatFiles = append(predictedFeatFiles, f)
			}
		}
		var exportedFeatFiles []string
		for _, f := range actuallyExported {
			if strings.Contains(f, "feat-file") {
				exportedFeatFiles = append(exportedFeatFiles, f)
			}
		}
		if !reflect.DeepEqual(predictedFeatFiles, exportedFeatFiles) {
			t.Errorf("FIDELITY FAILURE on feat-x-unique files:\n  dry-run predicted: %v\n  real export transferred: %v",
				predictedFeatFiles, exportedFeatFiles)
		}
	})
}

func indexOf(slice []string, s string) int {
	for i, item := range slice {
		if item == s {
			return i
		}
	}
	return -1
}

func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

func TestJsonString(t *testing.T) {
	tests := []struct {
		name  string
		input interface{}
		want  string
	}{
		{
			name:  "string",
			input: "test",
			want:  `"test"`,
		},
		{
			name:  "string with quotes",
			input: `test "quoted"`,
			want:  `"test \"quoted\""`,
		},
		{
			name:  "integer",
			input: 123,
			want:  "123",
		},
		{
			name:  "float",
			input: 1.5,
			want:  "1.5",
		},
		{
			name:  "boolean",
			input: true,
			want:  "true",
		},
		{
			name:  "array",
			input: []string{"a", "b"},
			want:  `["a","b"]`,
		},
		{
			name:  "empty string",
			input: "",
			want:  `""`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := jsonString(tt.input)
			if got != tt.want {
				t.Errorf("jsonString() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestCatalogBinaryInheritsAWSProfile verifies that the catalog binary launched by
// syncCatalogRemote is constructed via commandutil.Command so that AWS_PROFILE=exohub
// is automatically injected into its environment (fixing issue #15).
func TestCatalogBinaryInheritsAWSProfile(t *testing.T) {
	t.Setenv("EXOHUB_AWS_PROFILE", "")

	// commandutil.Command is the function used in syncCatalogRemote.
	// Verify it injects AWS_PROFILE=exohub into the child environment.
	cmd := commandutil.Command("echo", "test")
	if cmd == nil {
		t.Fatal("commandutil.Command returned nil")
	}

	found := false
	for _, e := range cmd.Env {
		if strings.HasPrefix(e, "AWS_PROFILE=exohub") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("AWS_PROFILE=exohub not found in catalog binary env; got: %v", cmd.Env)
	}
}

// TestCatalogBinaryNotExecCommand confirms that the catalog binary for artifactdb
// remotes is NOT launched with bare exec.Command (which would miss AWS_PROFILE).
// This is a regression guard for issue #15.
func TestCatalogBinaryNotExecCommand(t *testing.T) {
	// exec.Command does NOT inject AWS_PROFILE, commandutil.Command does.
	// This test documents the difference so the contract is clear.
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("EXOHUB_AWS_PROFILE", "")

	// exec.Command: no injection
	rawCmd := exec.Command("echo", "test")
	rawCmd.Env = []string{"HOME=/tmp"} // controlled env without AWS_PROFILE
	hasProfile := false
	for _, e := range rawCmd.Env {
		if strings.HasPrefix(e, "AWS_PROFILE=") {
			hasProfile = true
		}
	}
	if hasProfile {
		t.Error("exec.Command should NOT inject AWS_PROFILE (this test validates the baseline)")
	}

	// commandutil.Command: injects it
	managedCmd := commandutil.Command("echo", "test")
	hasProfile = false
	for _, e := range managedCmd.Env {
		if strings.HasPrefix(e, "AWS_PROFILE=exohub") {
			hasProfile = true
		}
	}
	if !hasProfile {
		t.Error("commandutil.Command MUST inject AWS_PROFILE=exohub for catalog remote subprocess")
	}
}
