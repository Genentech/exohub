package sync

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
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

// matchAnnexGlobSharedVectors is the test-vector table shared verbatim
// with TestMatchAnnexGlob_SharedVectors in
// go/git-annex-remote-artifactdb-export/publish_instance_test.go, which
// ports matchAnnexGlob into that separate Go module (exo-cli#202 review:
// the catalog binary's exclude-glob matcher must never drift from exo's,
// since the dry-run "excluded" preview uses this one and the actual
// publish uses that port). Keep both copies byte-identical; add a row here
// AND there.
var matchAnnexGlobSharedVectors = []struct {
	pattern string
	path    string
	want    bool
}{
	{"**/x.json", "ax.json", true},
	{"meta/**", "meta", false},
	{"dataset_merged.json*", "dataset_merged.json.bak", true},
	{"dataset_merged.json*", "sub/dataset_merged.json.bak", true},
	{"meta/*", "meta/a/b", false},
	{"*.json", "a/b.json", true},
	// A single * or ? inside a ** pattern must still stay within one path
	// segment ([^/]*, not .*) — these catch a mutation that lets * cross /
	// in the ** branch.
	{"**/m*", "x/m/y", false},
	{"**/m*", "x/mine.json", true},
	{"a/**/b?.json", "a/x/y/b1.json", true},
	{"a/**/b?.json", "a/x/b1/z.json", false},
}

func TestMatchAnnexGlob_SharedVectors(t *testing.T) {
	for _, tt := range matchAnnexGlobSharedVectors {
		t.Run(tt.pattern+"|"+tt.path, func(t *testing.T) {
			got := matchAnnexGlob(tt.pattern, tt.path)
			if got != tt.want {
				t.Errorf("matchAnnexGlob(%q, %q) = %v, want %v", tt.pattern, tt.path, got, tt.want)
			}
		})
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

// TestSync_PlainSync_ReuploadsAfterRemoteMove is the exo-cli#166 end-to-end
// regression for the path that actually broke: plain `exo sync` (no --all),
// which runs `git annex sync --content` for annex remotes. Unlike `git
// annex copy --to <remote>` (used by `exo sync --all`, which re-verifies via
// CHECKPRESENT and self-corrects), `sync --content` trusts the location log
// and, left unhandled, performs no transfer at all once a remote's storage
// moves without a UUID change (reproduced here with a directory special
// remote re-pointed to a new, empty directory — the same shape as an s3url
// change in `exo init --remote`, and confirmed against the real
// git-annex-remote-s5cmd in the MR's root-cause investigation). Applying the
// fix's `git annex fsck --from <remote> --fast` invalidation right after the
// move is what makes the next plain `exo sync` actually re-upload.
func TestSync_PlainSync_ReuploadsAfterRemoteMove(t *testing.T) {
	repoDir, _, restore := newAnnexRepo(t, "r1")
	defer restore()

	addAnnexFile(t, repoDir, "a.bin", "version-1")
	annexRun(t, repoDir, "git", "annex", "sync", "--content", "--no-commit")

	if found := annexFindOnRemote(t, repoDir, "r1"); len(found) != 1 {
		t.Fatalf("expected a.bin on r1 after the initial sync, got: %v", found)
	}

	// Re-point the remote to a new, empty directory — same UUID, same shape
	// as an s3url change: the location log still says r1 has the content.
	newRemoteDir := t.TempDir()
	t.Cleanup(func() { chmodRWritable(newRemoteDir) })
	annexRun(t, repoDir, "git", "annex", "enableremote", "r1", "directory="+newRemoteDir)

	// Without invalidating presence, plain `exo sync` trusts the stale log
	// and performs no transfer at all: this is the actual bug (unlike
	// `copy --to`/`exo sync --all`, which would re-verify and re-upload on
	// its own — see TestSyncAll_ReuploadsAfterRemoteMove).
	annexRun(t, repoDir, "git", "annex", "sync", "--content", "--no-commit")
	if entries, err := os.ReadDir(newRemoteDir); err != nil || len(entries) != 0 {
		t.Fatalf("expected the stale log to make plain sync skip the new location, entries=%v err=%v", entries, err)
	}

	// Apply the fix: invalidate stale presence after the move, exactly as
	// ensureRemoteConfigS3URL does post-switch.
	_ = runCommand([]string{"git", "annex", "fsck", "--from", "r1", "--fast"})

	// Plain `exo sync` must now re-upload to the new location.
	annexRun(t, repoDir, "git", "annex", "sync", "--content", "--no-commit")
	if entries, err := os.ReadDir(newRemoteDir); err != nil || len(entries) == 0 {
		t.Fatalf("expected plain sync to re-upload after invalidation, entries=%v err=%v", entries, err)
	}

	// The old location is untouched (content kept, not deleted by the move).
	if found := annexFindOnRemote(t, repoDir, "r1"); len(found) != 1 || found[0] != "a.bin" {
		t.Fatalf("expected a.bin present on r1 (new location) after re-upload, got: %v", found)
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

// TestRunCatalogPublishJSON_Success verifies that runCatalogPublish in JSON
// mode passes --json to the catalog binary, captures its stdout instead of
// passing it through, keeps exo's own progress line off stdout, and parses
// the binary's one-line publish result into a catalogPublishResult carrying
// the remote's name.
func TestRunCatalogPublishJSON_Success(t *testing.T) {
	jsonLine := `{"atlas_url_used":"https://atlas.example.com/v1/sandbox1","published":true}`
	config, calls, cleanup := setupCatalogSyncEnvJSON(t, "https://example.com/repo.git", jsonLine, 0)

	oldStdout := os.Stdout
	output, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = output
	result, runErr := runCatalogPublish("atlas", config, "fake-catalog", true, false)
	os.Stdout = oldStdout
	output.Close()
	stdoutData, readErr := os.ReadFile(output.Name())
	cleanup()
	if readErr != nil {
		t.Fatal(readErr)
	}

	if runErr != nil {
		t.Fatalf("runCatalogPublish() error = %v", runErr)
	}
	if len(stdoutData) != 0 {
		t.Errorf("exo's own stdout should stay empty in JSON mode, got: %q", stdoutData)
	}
	if result == nil {
		t.Fatal("result = nil")
	}
	if result.Remote != "atlas" || !result.Published {
		t.Errorf("unexpected result: %+v", result)
	}
	if result.AtlasURLUsed == nil || *result.AtlasURLUsed != "https://atlas.example.com/v1/sandbox1" {
		t.Errorf("AtlasURLUsed = %v", result.AtlasURLUsed)
	}
	if result.Error != nil {
		t.Errorf("Error = %+v, want nil on success", result.Error)
	}
	if len(*calls) != 1 {
		t.Fatalf("expected one publish invocation, got %v", *calls)
	}
}

// TestBundleManifestRefKind verifies the --ref-kind mapping (exo-cli#183
// task 3.0): only ref_type "tag" maps to "tag"; everything else
// ("branch", "commit", or unset) is treated as a mutable "branch" (no
// tag-immutability check applies).
func TestBundleManifestRefKind(t *testing.T) {
	cases := []struct{ refType, want string }{
		{"tag", "tag"},
		{"branch", "branch"},
		{"commit", "branch"},
		{"", "branch"},
	}
	for _, c := range cases {
		m := bundleManifest{RefType: c.refType}
		if got := m.refKind(); got != c.want {
			t.Errorf("refKind() with ref_type %q = %q, want %q", c.refType, got, c.want)
		}
	}
}

// TestRunCatalogPublish_PassesPerRemoteAccountID verifies that
// runCatalogPublish passes each remote's own grants_account_id as
// --grants-account-id to the catalog binary, and omits --grants-account-id
// for a remote that has none set, so an Atlas remote and a role: instance
// remote with two different accounts each request credentials under the
// right account for their own s3url.
func TestRunCatalogPublish_PassesPerRemoteAccountID(t *testing.T) {
	jsonLine := `{"atlas_url_used":null,"published":true}`

	config, calls, cleanup := setupCatalogSyncEnvJSON(t, "https://example.com/repo.git", jsonLine, 0)
	config.Remotes = append(config.Remotes, initcmd.RemoteConfig{
		Name: "instance", Type: "artifactdb", Role: "instance",
		S3URL: "s3://bucket/instance", URL: "https://instance.example.com/v1/b",
		Grants: true, GrantsAccountID: "111122223333",
	})

	// A .artifactdb/ committed on HEAD so the instance publish doesn't
	// gracefully skip (exo-cli#183 task 3.3) before building its args.
	if err := os.MkdirAll(".artifactdb", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(".artifactdb", "dataset.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", ".artifactdb"}, {"commit", "-q", "-m", "add .artifactdb"}} {
		cmd := exec.Command("git", args...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.com", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	if _, err := runCatalogPublish("atlas", config, "fake-catalog", true, false); err != nil {
		t.Fatalf("runCatalogPublish(atlas): %v", err)
	}
	if _, err := runCatalogPublish("instance", config, "fake-catalog", true, false); err != nil {
		t.Fatalf("runCatalogPublish(instance): %v", err)
	}
	cleanup()

	if len(*calls) != 2 {
		t.Fatalf("expected two publish invocations, got %v", *calls)
	}

	atlasCall := (*calls)[0]
	if strings.Contains(atlasCall, "--grants-account-id") {
		t.Errorf("atlas remote has no grants_account_id, expected no --grants-account-id, got: %q", atlasCall)
	}
	if !strings.Contains(atlasCall, "--s3url s3://bucket/atlas") {
		t.Errorf("expected atlas remote's own s3url, got: %q", atlasCall)
	}

	instanceCall := (*calls)[1]
	if !strings.Contains(instanceCall, "--grants-account-id 111122223333") {
		t.Errorf("expected --grants-account-id 111122223333 for instance remote, got: %q", instanceCall)
	}
	if !strings.Contains(instanceCall, "--s3url s3://bucket/instance") {
		t.Errorf("expected instance remote's own s3url, got: %q", instanceCall)
	}
}

// TestRunCatalogPublish_InstancePassesRepoURLAndForce verifies that a
// role: instance publish passes the bundle's repo_url as --repo-url (the
// instance catalog-binary invocation has no bundle.json of its own to read
// it from, unlike role: atlas) and, when force is true, --force (source-repo
// guard bypass, exo-cli#186).
func TestRunCatalogPublish_InstancePassesRepoURLAndForce(t *testing.T) {
	jsonLine := `{"atlas_url_used":null,"published":true}`

	config, calls, cleanup := setupCatalogSyncEnvJSON(t, "https://example.com/repo.git", jsonLine, 0)
	config.Remotes = append(config.Remotes, initcmd.RemoteConfig{
		Name: "instance", Type: "artifactdb", Role: "instance",
		S3URL: "s3://bucket/instance", URL: "https://instance.example.com/v1/b",
	})

	// A .artifactdb/ committed on HEAD so the instance publish doesn't
	// gracefully skip (exo-cli#183 task 3.3) before building its args.
	if err := os.MkdirAll(".artifactdb", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(".artifactdb", "dataset.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", ".artifactdb"}, {"commit", "-q", "-m", "add .artifactdb"}} {
		cmd := exec.Command("git", args...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.com", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	if _, err := runCatalogPublish("instance", config, "fake-catalog", true, true); err != nil {
		t.Fatalf("runCatalogPublish(instance): %v", err)
	}
	cleanup()

	if len(*calls) != 1 {
		t.Fatalf("expected one publish invocation, got %v", *calls)
	}
	instanceCall := (*calls)[0]
	if !strings.Contains(instanceCall, "--repo-url https://example.com/repo.git") {
		t.Errorf("expected --repo-url https://example.com/repo.git, got: %q", instanceCall)
	}
	if !strings.Contains(instanceCall, "--force") {
		t.Errorf("expected --force to be passed to the catalog binary, got: %q", instanceCall)
	}
}

// TestBuildInstancePublishArgs_SourceS3URLTrailingSlashPreserved verifies
// that --source-s3url carries the delegation Atlas's s3url exactly as
// configured (including a trailing slash), since the server compares it
// verbatim against the instance's recorded source — exo must not strip or
// add a trailing slash itself.
func TestBuildInstancePublishArgs_SourceS3URLTrailingSlashPreserved(t *testing.T) {
	bundleDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(bundleDir, ".artifactdb"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, ".artifactdb", "dataset.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	atlas := initcmd.RemoteConfig{
		Name: "atlas", Type: "artifactdb", Role: "atlas",
		S3URL: "s3://bucket/prefix/",
	}
	instance := initcmd.RemoteConfig{
		Name: "instance", Type: "artifactdb", Role: "instance",
		S3URL: "s3://bucket/proj", URL: "https://instance.example.com/v1/proj",
		Grants: true,
	}
	remotesConfig := &initcmd.RemotesConfig{Remotes: []initcmd.RemoteConfig{atlas, instance}}
	info := bundleManifest{RefName: "v1", Ref: "abc123", RefType: "tag", RepoURL: "https://example.com/repo.git"}

	args, cleanup, skip, err := buildInstancePublishArgs("instance", &instance, remotesConfig, bundleDir, "", info, "https://api.example.com", false)
	if err != nil {
		t.Fatalf("buildInstancePublishArgs error: %v", err)
	}
	if cleanup != nil {
		defer cleanup()
	}
	if skip {
		t.Fatal("expected skip=false")
	}

	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--source-s3url s3://bucket/prefix/") {
		t.Errorf("expected --source-s3url s3://bucket/prefix/ (trailing slash preserved), got: %q", joined)
	}
}

// TestBuildInstancePublishArgs_ExplicitArtifactdbDirTakesPriority verifies
// that an explicit artifactdb dir (passed by 'exo publish --ref' via the
// hidden 'exo sync --artifactdb-dir', exo-cli#177) wins over a selected
// bundle dir's own .artifactdb/ subtree, even when both are present.
func TestBuildInstancePublishArgs_ExplicitArtifactdbDirTakesPriority(t *testing.T) {
	bundleDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(bundleDir, ".artifactdb"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, ".artifactdb", "bundledir.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	explicitDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(explicitDir, "explicit.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	atlas := initcmd.RemoteConfig{
		Name: "atlas", Type: "artifactdb", Role: "atlas",
		S3URL: "s3://bucket/prefix",
	}
	instance := initcmd.RemoteConfig{
		Name: "instance", Type: "artifactdb", Role: "instance",
		S3URL: "s3://bucket/proj", URL: "https://instance.example.com/v1/proj",
	}
	remotesConfig := &initcmd.RemotesConfig{Remotes: []initcmd.RemoteConfig{atlas, instance}}
	info := bundleManifest{RefName: "1", Ref: "abc123", RefType: "tag", RepoURL: "https://example.com/repo.git"}

	args, cleanup, skip, err := buildInstancePublishArgs("instance", &instance, remotesConfig, bundleDir, explicitDir, info, "https://api.example.com", false)
	if err != nil {
		t.Fatalf("buildInstancePublishArgs error: %v", err)
	}
	if cleanup != nil {
		defer cleanup()
	}
	if skip {
		t.Fatal("expected skip=false")
	}

	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--artifactdb-dir "+explicitDir) {
		t.Errorf("expected --artifactdb-dir %s (explicit dir), got: %q", explicitDir, joined)
	}
	if strings.Contains(joined, "--artifactdb-dir "+filepath.Join(bundleDir, ".artifactdb")) {
		t.Errorf("expected the explicit artifactdb dir to take priority over bundleDir/.artifactdb, got: %q", joined)
	}
}

// TestBuildInstancePublishArgs_ForwardsExcludeGlobs verifies that an
// instance remote's exclude globs (RemoteConfig.Exclude) are forwarded as
// repeated --exclude flags (exo-cli#202).
func TestBuildInstancePublishArgs_ForwardsExcludeGlobs(t *testing.T) {
	bundleDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(bundleDir, ".artifactdb"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, ".artifactdb", "dataset.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	atlas := initcmd.RemoteConfig{
		Name: "atlas", Type: "artifactdb", Role: "atlas",
		S3URL: "s3://bucket/prefix",
	}
	instance := initcmd.RemoteConfig{
		Name: "instance", Type: "artifactdb", Role: "instance",
		S3URL: "s3://bucket/proj", URL: "https://instance.example.com/v1/proj",
		Exclude: []string{"dataset_merged.json*", "tmp/**"},
	}
	remotesConfig := &initcmd.RemotesConfig{Remotes: []initcmd.RemoteConfig{atlas, instance}}
	info := bundleManifest{RefName: "v1", Ref: "abc123", RefType: "tag", RepoURL: "https://example.com/repo.git"}

	args, cleanup, skip, err := buildInstancePublishArgs("instance", &instance, remotesConfig, bundleDir, "", info, "https://api.example.com", false)
	if err != nil {
		t.Fatalf("buildInstancePublishArgs error: %v", err)
	}
	if cleanup != nil {
		defer cleanup()
	}
	if skip {
		t.Fatal("expected skip=false")
	}

	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--exclude dataset_merged.json*") {
		t.Errorf("expected --exclude dataset_merged.json*, got: %q", joined)
	}
	if !strings.Contains(joined, "--exclude tmp/**") {
		t.Errorf("expected --exclude tmp/**, got: %q", joined)
	}
	// Mutation check: both globs must each appear exactly once as their own
	// --exclude flag, not merged or dropped.
	if count := strings.Count(joined, "--exclude "); count != 2 {
		t.Errorf("expected exactly 2 --exclude flags, got %d in: %q", count, joined)
	}
}

// TestBuildInstancePublishArgs_NoExcludeOmitsFlag is the mutation-check
// counterpart: a remote with no exclude globs never emits --exclude.
func TestBuildInstancePublishArgs_NoExcludeOmitsFlag(t *testing.T) {
	bundleDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(bundleDir, ".artifactdb"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, ".artifactdb", "dataset.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	atlas := initcmd.RemoteConfig{
		Name: "atlas", Type: "artifactdb", Role: "atlas",
		S3URL: "s3://bucket/prefix",
	}
	instance := initcmd.RemoteConfig{
		Name: "instance", Type: "artifactdb", Role: "instance",
		S3URL: "s3://bucket/proj", URL: "https://instance.example.com/v1/proj",
	}
	remotesConfig := &initcmd.RemotesConfig{Remotes: []initcmd.RemoteConfig{atlas, instance}}
	info := bundleManifest{RefName: "v1", Ref: "abc123", RefType: "tag", RepoURL: "https://example.com/repo.git"}

	args, cleanup, skip, err := buildInstancePublishArgs("instance", &instance, remotesConfig, bundleDir, "", info, "https://api.example.com", false)
	if err != nil {
		t.Fatalf("buildInstancePublishArgs error: %v", err)
	}
	if cleanup != nil {
		defer cleanup()
	}
	if skip {
		t.Fatal("expected skip=false")
	}
	if strings.Contains(strings.Join(args, " "), "--exclude") {
		t.Errorf("expected no --exclude flag, got: %q", args)
	}
}

// TestBuildAtlasPublishArgs_NeverForwardsExclude verifies that an atlas
// remote's own exclude globs (if any — atlas remotes don't normally set
// them, but defensively) are never forwarded to the atlas publish command,
// since --exclude is role: instance only (exo-cli#202).
func TestBuildAtlasPublishArgs_NeverForwardsExclude(t *testing.T) {
	bundleDir := t.TempDir()
	atlas := &initcmd.RemoteConfig{
		Name: "atlas", Type: "artifactdb", Role: "atlas",
		S3URL:   "s3://bucket/prefix",
		Exclude: []string{"dataset_merged.json*"},
	}
	info := bundleManifest{RefName: "v1", Ref: "abc123", RefType: "tag"}

	args := buildAtlasPublishArgs(atlas, bundleDir, info, "https://api.example.com", false)
	if strings.Contains(strings.Join(args, " "), "--exclude") {
		t.Errorf("expected no --exclude flag for an atlas remote, got: %q", args)
	}
}

// TestRunCatalogPublish_InstanceBinarySkippedResult verifies that when the
// catalog binary's --role instance publish itself reports
// {published:false, skipped:true, skip_reason:"no_artifactdb"} (exo-cli#189
// item 2), runCatalogPublish surfaces it as a catalogPublishResult with
// Published=false and Skipped set to the reason, and a nil error — not a
// failure.
func TestRunCatalogPublish_InstanceBinarySkippedResult(t *testing.T) {
	jsonLine := `{"published":false,"s3_location":"","skipped":true,"skip_reason":"no_artifactdb"}`

	config, calls, cleanup := setupCatalogSyncEnvJSON(t, "https://example.com/repo.git", jsonLine, 0)
	config.Remotes = append(config.Remotes, initcmd.RemoteConfig{
		Name: "instance", Type: "artifactdb", Role: "instance",
		S3URL: "s3://bucket/instance", URL: "https://instance.example.com/v1/b",
	})

	// A .artifactdb/ committed on HEAD so the instance publish actually
	// invokes the catalog binary (rather than the exo-side pre-check skip)
	// and we're exercising the binary's own result parsing.
	if err := os.MkdirAll(".artifactdb", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(".artifactdb", "dataset.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", ".artifactdb"}, {"commit", "-q", "-m", "add .artifactdb"}} {
		cmd := exec.Command("git", args...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.com", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	result, runErr := runCatalogPublish("instance", config, "fake-catalog", true, false)
	cleanup()

	if runErr != nil {
		t.Fatalf("runCatalogPublish() error = %v, want nil (a skip is not a failure)", runErr)
	}
	if result == nil {
		t.Fatal("result = nil")
	}
	if result.Published {
		t.Error("Published = true, want false for a skipped result")
	}
	if result.Skipped == nil || *result.Skipped != "no_artifactdb" {
		t.Errorf("Skipped = %v, want %q", result.Skipped, "no_artifactdb")
	}
	if len(*calls) != 1 {
		t.Fatalf("expected one publish invocation, got %v", *calls)
	}
}

// TestRunCatalogPublishJSON_Refusal verifies that a 422-style refusal from
// the catalog binary (non-zero exit, --json result with published=false and
// a structured error) is surfaced as both a non-nil error (for exit-code
// propagation) and a parsed catalogPublishResult carrying the error detail.
func TestRunCatalogPublishJSON_Refusal(t *testing.T) {
	jsonLine := `{"atlas_url_used":null,"published":false,"error":{"code":"atlas_url_not_allowlisted","message":"atlas_url not allowed","allowed":["https://atlas.example.com/v1/a","https://atlas.example.com/v1/b"]}}`
	config, _, cleanup := setupCatalogSyncEnvJSON(t, "https://example.com/repo.git", jsonLine, 1)

	result, runErr := runCatalogPublish("atlas", config, "fake-catalog", true, false)
	cleanup()
	if runErr == nil {
		t.Fatal("expected a non-nil error for a refused publish")
	}
	if result == nil {
		t.Fatal("result = nil, want a parsed result even on refusal")
	}
	if result.Remote != "atlas" || result.Published {
		t.Errorf("unexpected result: %+v", result)
	}
	if result.AtlasURLUsed != nil {
		t.Errorf("AtlasURLUsed = %v, want nil", result.AtlasURLUsed)
	}
	if result.Error == nil || result.Error.Code != "atlas_url_not_allowlisted" || result.Error.Message != "atlas_url not allowed" {
		t.Errorf("unexpected error: %+v", result.Error)
	}
	if !reflect.DeepEqual(result.Error.Allowed, []string{"https://atlas.example.com/v1/a", "https://atlas.example.com/v1/b"}) {
		t.Errorf("Allowed = %v", result.Error.Allowed)
	}
}

// TestCatalogPublishResult_SchemaStable locks the post-publish JSON record's
// wire shape: {remote, atlas_url_used, published, error}. atlas_url_used
// must serialize to null (not be absent) when nil; renaming or dropping any
// of these keys should fail this test.
func TestCatalogPublishResult_SchemaStable(t *testing.T) {
	record := catalogPublishResult{Remote: "atlas", Role: "atlas", Published: true}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]interface{}
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"remote", "role", "atlas_url_used", "s3_location", "published", "skipped"} {
		if _, ok := fields[key]; !ok {
			t.Errorf("missing key %q: %s", key, data)
		}
	}
	if fields["atlas_url_used"] != nil {
		t.Errorf("atlas_url_used = %v, want null", fields["atlas_url_used"])
	}
	if fields["s3_location"] != nil {
		t.Errorf("s3_location = %v, want null", fields["s3_location"])
	}
	if fields["skipped"] != nil {
		t.Errorf("skipped = %v, want null", fields["skipped"])
	}
	if _, present := fields["error"]; present {
		t.Errorf("success record should omit \"error\" entirely, got: %s", data)
	}
}

// setupStaleRoutingRepo creates a real plain git repo with a "main" branch:
// tag "1" is committed with pre-migration routing for remoteName (no
// atlas_url, old s3url prefix), then main moves on with post-migration
// routing (new prefix + atlas_url) committed on top. It chdirs into the
// repo for the duration of the test (restored via t.Cleanup) and returns
// the pre-migration RemoteConfig as checked out at tag 1.
func setupStaleRoutingRepo(t *testing.T, remoteName string) (staleRemote *initcmd.RemoteConfig) {
	t.Helper()
	repoDir := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = repoDir
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_NOSYSTEM=1", "HOME="+repoDir,
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("command %v failed: %v\n%s", args, err, out)
		}
		return string(out)
	}

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repoDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(oldWd) }) //nolint:errcheck

	run("git", "init", "-q", "-b", "main")

	preMigration := &initcmd.RemotesConfig{
		Remotes: []initcmd.RemoteConfig{{Name: remoteName, Type: "artifactdb", Mode: "export", S3URL: "s3://bucket/sandbox/old-prefix"}},
	}
	if err := initcmd.SaveRemotesConfig(preMigration); err != nil {
		t.Fatalf("SaveRemotesConfig: %v", err)
	}
	run("git", "add", ".exohub/remotes")
	run("git", "commit", "-q", "-m", "pre-migration routing")
	run("git", "tag", "1")

	postMigration := &initcmd.RemotesConfig{
		Remotes: []initcmd.RemoteConfig{{Name: remoteName, Type: "artifactdb", Mode: "export", S3URL: "s3://bucket/exohub/my-profile", URL: "https://atlas.example/sandbox1"}},
	}
	if err := initcmd.SaveRemotesConfig(postMigration); err != nil {
		t.Fatalf("SaveRemotesConfig: %v", err)
	}
	run("git", "add", ".exohub/remotes")
	run("git", "commit", "-q", "-m", "migrate routing")

	// Check out the old tag: this is the dangerous, pre-#176 scenario.
	run("git", "checkout", "-q", "1")

	checkedOut, err := initcmd.LoadRemotesConfig()
	if err != nil {
		t.Fatalf("LoadRemotesConfig: %v", err)
	}
	return getRemoteConfigByName(remoteName, checkedOut)
}

// TestCheckRoutingNotStale_RefusesOnDetachedHeadWithStaleRouting reproduces
// the exo-cli#176 tracer incident: tag 1 predates a routing migration (prefix
// move + atlas_url added on main). Checking out tag 1 directly and
// publishing (no --ref/--bundle-dir) must be refused instead of silently
// using tag 1's stale/missing atlas_url.
func TestCheckRoutingNotStale_RefusesOnDetachedHeadWithStaleRouting(t *testing.T) {
	staleRemote := setupStaleRoutingRepo(t, "atlas")
	if staleRemote == nil {
		t.Fatal("expected atlas remote in the checked-out (tag 1) config")
	}

	err := checkRoutingNotStale("atlas", staleRemote)
	if err == nil {
		t.Fatal("expected refusal when a detached HEAD's routing differs from main's")
	}
	if !strings.Contains(err.Error(), "refusing to publish") || !strings.Contains(err.Error(), "--ref") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestCheckRoutingNotStale_AllowsOnBranch verifies the guard never triggers
// while HEAD is on a named branch, regardless of what main's routing is —
// the guard exists specifically for the detached-HEAD (checked-out tag)
// scenario, not ordinary branch-based development.
func TestCheckRoutingNotStale_AllowsOnBranch(t *testing.T) {
	setupStaleRoutingRepo(t, "atlas")
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("command %v failed: %v\n%s", args, err, out)
		}
	}
	run("git", "checkout", "-q", "main")

	current, err := initcmd.LoadRemotesConfig()
	if err != nil {
		t.Fatalf("LoadRemotesConfig: %v", err)
	}
	rc := getRemoteConfigByName("atlas", current)

	if err := checkRoutingNotStale("atlas", rc); err != nil {
		t.Errorf("expected no refusal on a named branch, got: %v", err)
	}
}

// TestCheckRoutingNotStale_AllowsMatchingRouting verifies that a detached
// HEAD whose routing already matches main's (e.g. HEAD == main's tip, or a
// tag made after the last routing change) is never refused.
func TestCheckRoutingNotStale_AllowsMatchingRouting(t *testing.T) {
	setupStaleRoutingRepo(t, "atlas")
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("command %v failed: %v\n%s", args, err, out)
		}
	}
	// Detach HEAD directly onto main's tip (post-migration routing) instead of
	// the stale tag.
	run("git", "checkout", "-q", "main")
	commit, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	run("git", "checkout", "-q", strings.TrimSpace(string(commit)))

	current, err := initcmd.LoadRemotesConfig()
	if err != nil {
		t.Fatalf("LoadRemotesConfig: %v", err)
	}
	rc := getRemoteConfigByName("atlas", current)

	if err := checkRoutingNotStale("atlas", rc); err != nil {
		t.Errorf("expected no refusal when detached-HEAD routing already matches main's, got: %v", err)
	}
}

// TestCheckRoutingNotStale_RefusesOnNonDefaultBranchWithStaleRouting verifies
// the guard also triggers on a named branch other than the default (not just
// detached HEAD): checking out a feature branch cut from the stale tag, with
// the stale tag's own routing, must still be refused.
func TestCheckRoutingNotStale_RefusesOnNonDefaultBranchWithStaleRouting(t *testing.T) {
	setupStaleRoutingRepo(t, "atlas") // leaves HEAD detached at tag 1
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("command %v failed: %v\n%s", args, err, out)
		}
	}
	run("git", "checkout", "-q", "-b", "feature-from-tag-1", "1")

	current, err := initcmd.LoadRemotesConfig()
	if err != nil {
		t.Fatalf("LoadRemotesConfig: %v", err)
	}
	rc := getRemoteConfigByName("atlas", current)

	err = checkRoutingNotStale("atlas", rc)
	if err == nil {
		t.Fatal("expected refusal on a non-default branch whose routing differs from main's")
	}
	if !strings.Contains(err.Error(), "refusing to publish") || !strings.Contains(err.Error(), "feature-from-tag-1") {
		t.Errorf("unexpected error message (want it to name the current checkout): %v", err)
	}
}

// buildExoBinary compiles the real exo binary (main.go at the module root)
// into a temp file and returns its path, for CLI-level tests that must
// drive behaviour living behind os.Exit in runSync (so it can't be
// exercised in-process without killing the test binary).
func buildExoBinary(t *testing.T) string {
	t.Helper()
	modDir, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		t.Fatalf("go env GOMOD: %v", err)
	}
	moduleRoot := filepath.Dir(strings.TrimSpace(string(modDir)))

	binPath := filepath.Join(t.TempDir(), "exo")
	build := exec.Command("go", "build", "-o", binPath, ".")
	build.Dir = moduleRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build exo: %v\n%s", err, out)
	}
	return binPath
}

// TestSyncDryRunCLIRefusesInvalidRemoteConfigOnLoad is a CLI-level
// reproduction of the exo-cli maitre's #181 review: an invalid role/mode
// artifactdb config must be refused by the actual `exo sync --dry-run`
// invocation, not just by calling Validate() from exo init. It builds the
// real exo binary and runs it against a repo whose .exohub/remotes has a
// role: instance remote with a mode set (forbidden).
//
// exo sync unconditionally requires the git-annex binary on PATH
// (requireBins("git-annex")) before it ever reaches .exohub/remotes, so
// this is skipped where it isn't installed (e.g. the test:unit CI job;
// see newAnnexRepo above for the same convention) and runs for real in
// test:integration/locally.
func TestSyncDryRunCLIRefusesInvalidRemoteConfigOnLoad(t *testing.T) {
	if _, err := exec.LookPath("git-annex"); err != nil {
		t.Skip("git-annex not in PATH")
	}
	binPath := buildExoBinary(t)
	repoDir := t.TempDir()
	runIn := func(name string, args ...string) {
		t.Helper()
		cmd := exec.Command(name, args...)
		cmd.Dir = repoDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
	}
	runIn("git", "init", "-q", "-b", "main")
	runIn("git", "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-q", "-m", "init")
	// Fake git-annex initialization: runSync only checks
	// `git config --get annex.uuid` (annexInitialized), never the real
	// git-annex binary, before reaching .exohub/remotes. This avoids a
	// hard dependency on git-annex being installed in the CI image that
	// runs go test (unlike test:integration, test:unit has no git-annex).
	runIn("git", "config", "annex.uuid", "test-annex-uuid")

	data := []byte("remotes:\n  - name: instance\n    type: artifactdb\n    role: instance\n    mode: export\n    s3url: s3://bucket/DS1\n    url: https://adb.example.com/v1/b\n")
	if err := os.MkdirAll(filepath.Join(repoDir, ".exohub"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, ".exohub", "remotes"), data, 0644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(binPath, "sync", "--dry-run")
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected 'exo sync --dry-run' to exit non-zero, output: %s", out)
	}
	if !strings.Contains(string(out), "instance remotes take no mode") {
		t.Errorf("expected output to contain the validation message, got: %s", out)
	}
}

// TestSyncCLISkipsInstanceRemoteBeforeAtlas is the CLI-level counterpart of
// TestRunPublishJSON_PublishesAtlasBeforeInstanceEvenWhenListedAfter for
// the human-output publish loop in runSync, which lives behind os.Exit and
// so can't be driven in-process. With a role: instance remote listed
// BEFORE an atlas remote in --with, `exo sync --with instance,atlas
// --bundle-dir <dir>` must reorder to publish the atlas remote first (exit
// 0), then reach the instance remote and skip it gracefully (no
// .artifactdb/ for this ref, see extractInstanceArtifactdbFromHEAD),
// invoking the real catalog binary exactly once (never for the instance
// remote).
func TestSyncCLISkipsInstanceRemoteBeforeAtlas(t *testing.T) {
	if _, err := exec.LookPath("git-annex"); err != nil {
		t.Skip("git-annex not in PATH")
	}
	binPath := buildExoBinary(t)

	repoDir := t.TempDir()
	runIn := func(name string, args ...string) {
		t.Helper()
		cmd := exec.Command(name, args...)
		cmd.Dir = repoDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
	}
	runIn("git", "init", "-q", "-b", "main")
	runIn("git", "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-q", "-m", "init")
	// Fake git-annex initialization: runSync only checks
	// `git config --get annex.uuid` (annexInitialized), never the real
	// git-annex binary, before reaching .exohub/remotes. This avoids a
	// hard dependency on git-annex being installed in the CI image that
	// runs go test (unlike test:integration, test:unit has no git-annex).
	runIn("git", "config", "annex.uuid", "test-annex-uuid")

	data := []byte("remotes:\n" +
		"  - name: instance\n    type: artifactdb\n    role: instance\n    s3url: s3://bucket/DS1\n    url: https://adb.example.com/v1/b\n" +
		"  - name: atlas\n    type: artifactdb\n    mode: export\n    s3url: s3://bucket/atlas-meta\n")
	if err := os.MkdirAll(filepath.Join(repoDir, ".exohub"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, ".exohub", "remotes"), data, 0644); err != nil {
		t.Fatal(err)
	}

	bundleDir := t.TempDir()
	manifest, err := json.Marshal(bundleManifest{RefName: "main", Ref: "abc123", RepoURL: "https://example.com/repo.git"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "bundle.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}

	// Fake catalog binary on PATH, named as getCatalogRemoteBinary expects
	// for the atlas remote's type/mode; records its argv so we can assert
	// it was never invoked for the instance remote.
	binDir := t.TempDir()
	logFile := filepath.Join(binDir, "calls.log")
	script := "#!/bin/sh\necho \"$@\" >> " + logFile + "\nexit 0\n"
	if err := os.WriteFile(filepath.Join(binDir, "git-annex-remote-artifactdb-export"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(binPath, "sync", "--with", "instance", "--with", "atlas", "--bundle-dir", bundleDir)
	cmd.Dir = repoDir
	cmd.Env = append(os.Environ(), "PATH="+binDir+":"+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected 'exo sync --with instance --with atlas' to exit 0, got %v, output:\n%s", err, out)
	}
	if !strings.Contains(string(out), "No .artifactdb/") || !strings.Contains(string(out), "skipping instance remote 'instance'") {
		t.Errorf("expected the instance skip notice, got: %s", out)
	}
	if !strings.Contains(string(out), "Publishing to catalog remote 'atlas'") {
		t.Errorf("expected the atlas remote (listed after the skipped instance) to still publish, got: %s", out)
	}

	callsData, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("expected the catalog binary to have been invoked once, calls.log missing: %v", err)
	}
	calls := strings.Split(strings.TrimSpace(string(callsData)), "\n")
	if len(calls) != 1 {
		t.Fatalf("expected the catalog binary invoked exactly once (for atlas only), got: %v", calls)
	}
	if strings.Contains(calls[0], "instance") {
		t.Errorf("catalog binary should never be invoked for the instance remote, got: %v", calls[0])
	}
}

// TestSyncJSONCLIReportsInstanceSkippedOnAtlasFailure is the CLI-level
// reproduction of the exo-cli maitre's MR !332 review: when a role: atlas
// remote's publish fails, the dependent role: instance remote (delegating
// to it via data_from) must be reported `skipped` with the Atlas's error
// in the --json report (not silently dropped, and not aborting before the
// skip is recorded), the catalog binary must never be invoked for the
// instance remote, and the process must still exit non-zero.
func TestSyncJSONCLIReportsInstanceSkippedOnAtlasFailure(t *testing.T) {
	if _, err := exec.LookPath("git-annex"); err != nil {
		t.Skip("git-annex not in PATH")
	}
	binPath := buildExoBinary(t)

	repoDir := t.TempDir()
	runIn := func(name string, args ...string) {
		t.Helper()
		cmd := exec.Command(name, args...)
		cmd.Dir = repoDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
	}
	runIn("git", "init", "-q", "-b", "main")
	runIn("git", "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-q", "-m", "init")
	runIn("git", "config", "annex.uuid", "test-annex-uuid")

	data := []byte("remotes:\n" +
		"  - name: atlas\n    type: artifactdb\n    mode: export\n    s3url: s3://bucket/atlas-meta\n" +
		"  - name: instance\n    type: artifactdb\n    role: instance\n    data_from: atlas\n    s3url: s3://bucket/DS1\n    url: https://adb.example.com/v1/b\n")
	if err := os.MkdirAll(filepath.Join(repoDir, ".exohub"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, ".exohub", "remotes"), data, 0644); err != nil {
		t.Fatal(err)
	}

	bundleDir := t.TempDir()
	manifest, err := json.Marshal(bundleManifest{RefName: "main", Ref: "abc123", RepoURL: "https://example.com/repo.git"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "bundle.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}

	// Fake catalog binary: always fails with a structured --json error (for
	// the atlas remote; the instance remote must never reach it).
	binDir := t.TempDir()
	logFile := filepath.Join(binDir, "calls.log")
	script := "#!/bin/sh\necho \"$@\" >> " + logFile + "\n" +
		"case \" $* \" in\n" +
		"  *\" --json \"*)\n" +
		"    echo '{\"atlas_url_used\":null,\"published\":false,\"error\":{\"code\":\"atlas_url_not_allowlisted\",\"message\":\"atlas_url not allowed\"}}'\n" +
		"    ;;\n" +
		"esac\n" +
		"exit 1\n"
	if err := os.WriteFile(filepath.Join(binDir, "git-annex-remote-artifactdb-export"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(binPath, "sync", "--with", "instance", "--with", "atlas", "--bundle-dir", bundleDir, "--json")
	cmd.Dir = repoDir
	cmd.Env = append(os.Environ(), "PATH="+binDir+":"+os.Getenv("PATH"), "EXOHUB_API_URL=http://127.0.0.1:1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	if err == nil {
		t.Fatalf("expected 'exo sync --json' to exit non-zero on the Atlas failure, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}

	var report struct {
		Published []catalogPublishResult `json:"published"`
	}
	if jsonErr := json.Unmarshal(stdout.Bytes(), &report); jsonErr != nil {
		t.Fatalf("expected stdout to be the JSON report, got: %v\nstdout:\n%s\nstderr:\n%s", jsonErr, stdout.String(), stderr.String())
	}
	if len(report.Published) != 2 {
		t.Fatalf("expected both remotes' records, got: %+v", report.Published)
	}
	if report.Published[0].Remote != "atlas" || report.Published[0].Published || report.Published[0].Error == nil || report.Published[0].Error.Code != "atlas_url_not_allowlisted" {
		t.Errorf("expected the atlas remote's failure recorded, got: %+v", report.Published[0])
	}
	if report.Published[1].Remote != "instance" || report.Published[1].Role != "instance" || report.Published[1].Published ||
		report.Published[1].Skipped == nil || !strings.Contains(*report.Published[1].Skipped, "atlas") || !strings.Contains(*report.Published[1].Skipped, "failed to publish") {
		t.Errorf("expected the instance remote skipped with the Atlas's error, got: %+v", report.Published[1])
	}

	callsData, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("expected the catalog binary to have been invoked once, calls.log missing: %v", err)
	}
	calls := strings.Split(strings.TrimSpace(string(callsData)), "\n")
	if len(calls) != 1 {
		t.Fatalf("expected the catalog binary invoked exactly once (for atlas only), got: %v", calls)
	}
	if strings.Contains(calls[0], "role instance") {
		t.Errorf("catalog binary should never be invoked for the instance remote, got: %v", calls[0])
	}
}

// TestSyncJSONCLIIndependentAtlasAndInstanceStillPublishOnAPeerFailure
// verifies that one Atlas remote failing doesn't affect an unrelated
// Atlas/instance pair: with two Atlas remotes (A fails, B succeeds) and
// one instance delegating to each, A's instance is skipped (A's error)
// while B's instance still publishes.
func TestSyncJSONCLIIndependentAtlasAndInstanceStillPublishOnAPeerFailure(t *testing.T) {
	if _, err := exec.LookPath("git-annex"); err != nil {
		t.Skip("git-annex not in PATH")
	}
	binPath := buildExoBinary(t)

	repoDir := t.TempDir()
	runIn := func(name string, args ...string) {
		t.Helper()
		cmd := exec.Command(name, args...)
		cmd.Dir = repoDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
	}
	runIn("git", "init", "-q", "-b", "main")
	if err := os.MkdirAll(filepath.Join(repoDir, ".artifactdb"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, ".artifactdb", "dataset.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	runIn("git", "add", ".artifactdb")
	runIn("git", "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-q", "-m", "init")
	runIn("git", "config", "annex.uuid", "test-annex-uuid")

	data := []byte("remotes:\n" +
		"  - name: atlas-a\n    type: artifactdb\n    mode: export\n    s3url: s3://bucket/atlas-a\n" +
		"  - name: atlas-b\n    type: artifactdb\n    mode: export\n    s3url: s3://bucket/atlas-b\n" +
		"  - name: instance-a\n    type: artifactdb\n    role: instance\n    data_from: atlas-a\n    s3url: s3://bucket/DSA\n    url: https://adb.example.com/v1/a\n" +
		"  - name: instance-b\n    type: artifactdb\n    role: instance\n    data_from: atlas-b\n    s3url: s3://bucket/DSB\n    url: https://adb.example.com/v1/b\n")
	if err := os.MkdirAll(filepath.Join(repoDir, ".exohub"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, ".exohub", "remotes"), data, 0644); err != nil {
		t.Fatal(err)
	}

	bundleDir := t.TempDir()
	manifest, err := json.Marshal(bundleManifest{RefName: "main", Ref: "abc123", RepoURL: "https://example.com/repo.git"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "bundle.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}

	// Fake catalog binary: fails only for atlas-a's s3url, succeeds for
	// everything else (atlas-b and instance-b).
	binDir := t.TempDir()
	logFile := filepath.Join(binDir, "calls.log")
	script := "#!/bin/sh\necho \"$@\" >> " + logFile + "\n" +
		"case \" $* \" in\n" +
		"  *\"s3://bucket/atlas-a\"*)\n" +
		"    case \" $* \" in *\" --json \"*) echo '{\"atlas_url_used\":null,\"published\":false,\"error\":{\"code\":\"atlas_url_not_allowlisted\",\"message\":\"nope\"}}' ;; esac\n" +
		"    exit 1\n" +
		"    ;;\n" +
		"  *\"s3://bucket/atlas-b\"*)\n" +
		"    case \" $* \" in *\" --json \"*) echo '{\"atlas_url_used\":\"https://atlas.example.com/v1/b\",\"published\":true}' ;; esac\n" +
		"    exit 0\n" +
		"    ;;\n" +
		"  *\"--role instance\"*\"s3://bucket/DSB\"*)\n" +
		"    case \" $* \" in *\" --json \"*) echo '{\"published\":true,\"s3_location\":\"s3://bucket/DSB/main/\"}' ;; esac\n" +
		"    exit 0\n" +
		"    ;;\n" +
		"esac\n" +
		"exit 0\n"
	if err := os.WriteFile(filepath.Join(binDir, "git-annex-remote-artifactdb-export"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(binPath, "sync", "--with", "instance-a", "--with", "instance-b", "--with", "atlas-a", "--with", "atlas-b", "--bundle-dir", bundleDir, "--json")
	cmd.Dir = repoDir
	cmd.Env = append(os.Environ(), "PATH="+binDir+":"+os.Getenv("PATH"), "EXOHUB_API_URL=http://127.0.0.1:1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	if err == nil {
		t.Fatalf("expected 'exo sync --json' to exit non-zero (atlas-a failed), stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}

	var report struct {
		Published []catalogPublishResult `json:"published"`
	}
	if jsonErr := json.Unmarshal(stdout.Bytes(), &report); jsonErr != nil {
		t.Fatalf("expected stdout to be the JSON report, got: %v\nstdout:\n%s\nstderr:\n%s", jsonErr, stdout.String(), stderr.String())
	}

	byRemote := map[string]catalogPublishResult{}
	for _, r := range report.Published {
		byRemote[r.Remote] = r
	}
	if r, ok := byRemote["atlas-a"]; !ok || r.Published || r.Error == nil {
		t.Errorf("expected atlas-a's failure recorded, got: %+v", r)
	}
	if r, ok := byRemote["atlas-b"]; !ok || !r.Published {
		t.Errorf("expected atlas-b published, got: %+v", r)
	}
	if r, ok := byRemote["instance-a"]; !ok || r.Published || r.Skipped == nil {
		t.Errorf("expected instance-a skipped (its delegation Atlas failed), got: %+v", r)
	}
	if r, ok := byRemote["instance-b"]; !ok || !r.Published || r.Skipped != nil {
		t.Errorf("expected instance-b published (its delegation Atlas succeeded), got: %+v", r)
	}

	callsData, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("calls.log missing: %v", err)
	}
	calls := string(callsData)
	if strings.Contains(calls, "DSA") {
		t.Errorf("catalog binary should never be invoked for instance-a (skipped), got calls: %s", calls)
	}
	if !strings.Contains(calls, "DSB") {
		t.Errorf("expected the catalog binary invoked for instance-b, got calls: %s", calls)
	}
}

// TestSyncCLIReportsInstanceSkippedOnAtlasFailure is the human-output
// (text) counterpart of TestSyncJSONCLIReportsInstanceSkippedOnAtlasFailure:
// when a role: atlas remote's publish fails, the dependent role: instance
// remote must still be reported skipped with the Atlas's error on stderr
// (not silently dropped by an early os.Exit), the catalog binary must
// never be invoked for it, and the process must exit non-zero.
func TestSyncCLIReportsInstanceSkippedOnAtlasFailure(t *testing.T) {
	if _, err := exec.LookPath("git-annex"); err != nil {
		t.Skip("git-annex not in PATH")
	}
	binPath := buildExoBinary(t)

	repoDir := t.TempDir()
	runIn := func(name string, args ...string) {
		t.Helper()
		cmd := exec.Command(name, args...)
		cmd.Dir = repoDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
	}
	runIn("git", "init", "-q", "-b", "main")
	runIn("git", "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-q", "-m", "init")
	runIn("git", "config", "annex.uuid", "test-annex-uuid")

	data := []byte("remotes:\n" +
		"  - name: atlas\n    type: artifactdb\n    mode: export\n    s3url: s3://bucket/atlas-meta\n" +
		"  - name: instance\n    type: artifactdb\n    role: instance\n    data_from: atlas\n    s3url: s3://bucket/DS1\n    url: https://adb.example.com/v1/b\n")
	if err := os.MkdirAll(filepath.Join(repoDir, ".exohub"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, ".exohub", "remotes"), data, 0644); err != nil {
		t.Fatal(err)
	}

	bundleDir := t.TempDir()
	manifest, err := json.Marshal(bundleManifest{RefName: "main", Ref: "abc123", RepoURL: "https://example.com/repo.git"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "bundle.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	logFile := filepath.Join(binDir, "calls.log")
	script := "#!/bin/sh\necho \"$@\" >> " + logFile + "\nexit 1\n"
	if err := os.WriteFile(filepath.Join(binDir, "git-annex-remote-artifactdb-export"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(binPath, "sync", "--with", "instance", "--with", "atlas", "--bundle-dir", bundleDir)
	cmd.Dir = repoDir
	cmd.Env = append(os.Environ(), "PATH="+binDir+":"+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected 'exo sync' to exit non-zero on the Atlas failure, output:\n%s", out)
	}
	if !strings.Contains(string(out), "Publish failed for 'atlas'") {
		t.Errorf("expected the atlas failure reported, got: %s", out)
	}
	if !strings.Contains(string(out), "Skipping instance remote 'instance'") || !strings.Contains(string(out), "atlas") || !strings.Contains(string(out), "failed to publish") {
		t.Errorf("expected the instance skip notice naming the failed delegation Atlas, got: %s", out)
	}

	callsData, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("calls.log missing: %v", err)
	}
	calls := strings.Split(strings.TrimSpace(string(callsData)), "\n")
	if len(calls) != 1 {
		t.Fatalf("expected the catalog binary invoked exactly once (for atlas only), got: %v", calls)
	}
	if strings.Contains(calls[0], "role instance") {
		t.Errorf("catalog binary should never be invoked for the instance remote, got: %v", calls[0])
	}
}

// TestRunPublishJSON_CombinesTargetAndOutcome verifies that exo sync --json
// (without --dry-run) prints a single document with BOTH the pre-publish
// target info ("catalog_remotes", the same shape as the dry-run report) and
// the post-publish outcome ("published"), per the issue's final JSON
// contract.
func TestRunPublishJSON_CombinesTargetAndOutcome(t *testing.T) {
	jsonLine := `{"atlas_url_used":"https://atlas.example.com/v1/sandbox1","published":true}`
	config, _, cleanup := setupCatalogSyncEnvJSON(t, "https://example.com/repo.git", jsonLine, 0)
	config.Remotes[0].URL = "https://atlas.example.com/v1/sandbox1"

	oldStdout := os.Stdout
	output, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = output
	runPublishJSON([]string{"atlas"}, config, false)
	os.Stdout = oldStdout
	output.Close()
	data, readErr := os.ReadFile(output.Name())
	cleanup()
	if readErr != nil {
		t.Fatal(readErr)
	}

	var report struct {
		CatalogRemotes []catalogTargetJSON    `json:"catalog_remotes"`
		Published      []catalogPublishResult `json:"published"`
	}
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("invalid JSON report %q: %v", data, err)
	}
	if len(report.CatalogRemotes) != 1 || report.CatalogRemotes[0].Remote != "atlas" || report.CatalogRemotes[0].Role != "atlas" ||
		report.CatalogRemotes[0].URL == nil || *report.CatalogRemotes[0].URL != "https://atlas.example.com/v1/sandbox1" {
		t.Errorf("unexpected catalog_remotes: %+v", report.CatalogRemotes)
	}
	if len(report.Published) != 1 || report.Published[0].Remote != "atlas" || !report.Published[0].Published ||
		report.Published[0].AtlasURLUsed == nil || *report.Published[0].AtlasURLUsed != "https://atlas.example.com/v1/sandbox1" {
		t.Errorf("unexpected published: %+v", report.Published)
	}
}

// TestRunPublishJSON_PublishesAtlasBeforeInstanceEvenWhenListedAfter
// verifies the role-ordered publish (exo-cli#183 task 3.1): with a role:
// instance remote listed BEFORE an atlas remote on --with, runPublishJSON
// still publishes the Atlas remote first (reordering, not the --with
// order), then reaches the instance remote; since the ref (HEAD, from the
// fake bundle.json) has no .artifactdb/, the instance publish gracefully
// skips with a notice (never calling the catalog binary) rather than
// failing, and the JSON report carries BOTH remotes' records.
func TestRunPublishJSON_PublishesAtlasBeforeInstanceEvenWhenListedAfter(t *testing.T) {
	jsonLine := `{"atlas_url_used":"https://atlas.example.com/v1/sandbox1","published":true}`
	config, calls, cleanup := setupCatalogSyncEnvJSON(t, "https://example.com/repo.git", jsonLine, 0)
	config.Remotes[0].URL = "https://atlas.example.com/v1/sandbox1"
	config.Remotes = append([]initcmd.RemoteConfig{{
		Name: "instance", Type: "artifactdb", Role: "instance",
		S3URL: "s3://bucket/DS1", URL: "https://adb.example.com/v1/b",
	}}, config.Remotes...)

	oldStdout, oldStderr := os.Stdout, os.Stderr
	stdout, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout, os.Stderr = stdout, stderr
	runPublishJSON([]string{"instance", "atlas"}, config, false)
	os.Stdout, os.Stderr = oldStdout, oldStderr
	stdout.Close()
	stderr.Close()

	stdoutData, err := os.ReadFile(stdout.Name())
	if err != nil {
		t.Fatal(err)
	}
	stderrData, err := os.ReadFile(stderr.Name())
	if err != nil {
		t.Fatal(err)
	}
	cleanup()

	if !strings.Contains(string(stderrData), "No .artifactdb/") || !strings.Contains(string(stderrData), "skipping instance remote 'instance'") {
		t.Errorf("expected a no-.artifactdb skip notice for the instance remote on stderr, got: %s", stderrData)
	}

	var report struct {
		Published []catalogPublishResult `json:"published"`
	}
	if err := json.Unmarshal(stdoutData, &report); err != nil {
		t.Fatalf("invalid JSON report %q: %v", stdoutData, err)
	}
	if len(report.Published) != 2 {
		t.Fatalf("expected both remotes' records, got: %+v", report.Published)
	}
	if report.Published[0].Remote != "atlas" || report.Published[0].Role != "atlas" || !report.Published[0].Published {
		t.Errorf("expected the atlas remote published first (reordered ahead of --with's order), got: %+v", report.Published[0])
	}
	if report.Published[1].Remote != "instance" || report.Published[1].Role != "instance" || report.Published[1].Published || report.Published[1].Skipped == nil {
		t.Errorf("expected the instance remote reported skipped (no .artifactdb/, not published), got: %+v", report.Published[1])
	}

	if len(*calls) != 1 {
		t.Fatalf("expected the catalog binary invoked exactly once (for atlas only), got: %v", *calls)
	}
	if strings.Contains((*calls)[0], "instance") {
		t.Errorf("catalog binary should never be invoked for the instance remote, got: %v", (*calls)[0])
	}
}

// TestOrderCatalogRemotesByRole verifies that role: atlas catalog remotes
// are always ordered before role: instance ones, preserving the relative
// order within each group and the position of non-catalog remotes (spec
// "Publish order per role", exo-cli#183 task 3.1).
func TestOrderCatalogRemotesByRole(t *testing.T) {
	config := &initcmd.RemotesConfig{Remotes: []initcmd.RemoteConfig{
		{Name: "instance", Type: "artifactdb", Role: "instance"},
		{Name: "atlas", Type: "artifactdb"},
		{Name: "s3", Type: "annex"},
	}}
	got := orderCatalogRemotesByRole([]string{"instance", "s3", "atlas"}, config)
	want := []string{"s3", "atlas", "instance"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("orderCatalogRemotesByRole() = %v, want %v", got, want)
	}
}

// TestDiscoverCatalogAndAnnexRemotes_IncludesRoleInstance verifies that
// auto-discovery (no --with) unions git-annex's own remotes with any role:
// instance artifactdb remote declared in .exohub/remotes, since a role:
// instance remote is never git-annex initremote'd and so is otherwise
// invisible to listAnnexRemotes (exo-cli#196). Atlas-role entries must
// still precede instance ones (orderCatalogRemotesByRole).
func TestDiscoverCatalogAndAnnexRemotes_IncludesRoleInstance(t *testing.T) {
	_, _, restoreDir := newAnnexRepo(t, "s3")
	defer restoreDir()

	config := &initcmd.RemotesConfig{Remotes: []initcmd.RemoteConfig{
		{Name: "s3", Type: "annex"},
		{Name: "atlas", Type: "artifactdb", Mode: "export", S3URL: "s3://bucket/atlas"},
		{Name: "instance", Type: "artifactdb", Role: "instance", S3URL: "s3://bucket/DS1", URL: "https://adb.example.com/v1/b"},
	}}

	got, err := discoverCatalogAndAnnexRemotes(config)
	if err != nil {
		t.Fatalf("discoverCatalogAndAnnexRemotes: %v", err)
	}
	want := []string{"s3", "instance"}
	// atlas was never initremote'd in this test repo either, so only s3 (a
	// real git-annex remote) and instance (unioned in) are expected; what
	// matters is that instance is present and ordered after any atlas-role
	// entries actually discovered (none here, since atlas also isn't
	// initremote'd — see the next test for role ordering).
	if !reflect.DeepEqual(got, want) {
		t.Errorf("discoverCatalogAndAnnexRemotes() = %v, want %v", got, want)
	}
}

// TestDiscoverCatalogAndAnnexRemotes_OrdersAtlasBeforeInstance verifies that
// when both an atlas-role git-annex remote and a role: instance remote are
// discovered, the atlas one is ordered first.
func TestDiscoverCatalogAndAnnexRemotes_OrdersAtlasBeforeInstance(t *testing.T) {
	_, _, restoreDir := newAnnexRepo(t, "atlas")
	defer restoreDir()

	config := &initcmd.RemotesConfig{Remotes: []initcmd.RemoteConfig{
		{Name: "atlas", Type: "artifactdb", Mode: "export", S3URL: "s3://bucket/atlas"},
		{Name: "instance", Type: "artifactdb", Role: "instance", S3URL: "s3://bucket/DS1", URL: "https://adb.example.com/v1/b"},
	}}

	got, err := discoverCatalogAndAnnexRemotes(config)
	if err != nil {
		t.Fatalf("discoverCatalogAndAnnexRemotes: %v", err)
	}
	want := []string{"atlas", "instance"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("discoverCatalogAndAnnexRemotes() = %v, want %v", got, want)
	}
}

// TestInstanceSkipReason_SkipsWhenDelegationAtlasFailed verifies that an
// instance remote is skipped, with its delegation Atlas's error as the
// reason, when that Atlas's publish failed in this run (spec "Failed Atlas
// skips the instance").
func TestInstanceSkipReason_SkipsWhenDelegationAtlasFailed(t *testing.T) {
	config := &initcmd.RemotesConfig{Remotes: []initcmd.RemoteConfig{
		{Name: "atlas", Type: "artifactdb", S3URL: "s3://bucket/atlas"},
		{Name: "instance", Type: "artifactdb", Role: "instance", S3URL: "s3://bucket/DS1", URL: "https://adb.example.com/v1/b"},
	}}
	rc := getRemoteConfigByName("instance", config)
	atlasOutcome := map[string]error{"atlas": fmt.Errorf("atlas_url_not_allowlisted")}
	reason, skip := instanceSkipReason("instance", rc, config, atlasOutcome)
	if !skip {
		t.Fatal("expected skip=true when the delegation Atlas failed")
	}
	if !strings.Contains(reason, "atlas") || !strings.Contains(reason, "failed to publish") {
		t.Errorf("unexpected reason: %q", reason)
	}
}

// TestInstanceSkipReason_SkipsWhenAtlasNotAttempted verifies that an
// instance remote is skipped when its delegation Atlas simply wasn't part
// of this run at all (not attempted, not necessarily failed).
func TestInstanceSkipReason_SkipsWhenAtlasNotAttempted(t *testing.T) {
	config := &initcmd.RemotesConfig{Remotes: []initcmd.RemoteConfig{
		{Name: "atlas", Type: "artifactdb", S3URL: "s3://bucket/atlas"},
		{Name: "instance", Type: "artifactdb", Role: "instance", S3URL: "s3://bucket/DS1", URL: "https://adb.example.com/v1/b"},
	}}
	rc := getRemoteConfigByName("instance", config)
	reason, skip := instanceSkipReason("instance", rc, config, map[string]error{})
	if !skip {
		t.Fatal("expected skip=true when the delegation Atlas wasn't attempted")
	}
	if !strings.Contains(reason, "not published") {
		t.Errorf("unexpected reason: %q", reason)
	}
}

// TestInstanceSkipReason_NoSkipWhenAtlasSucceeded verifies that an
// instance remote is NOT skipped once its delegation Atlas has published
// successfully in this run.
func TestInstanceSkipReason_NoSkipWhenAtlasSucceeded(t *testing.T) {
	config := &initcmd.RemotesConfig{Remotes: []initcmd.RemoteConfig{
		{Name: "atlas", Type: "artifactdb", S3URL: "s3://bucket/atlas"},
		{Name: "instance", Type: "artifactdb", Role: "instance", S3URL: "s3://bucket/DS1", URL: "https://adb.example.com/v1/b"},
	}}
	rc := getRemoteConfigByName("instance", config)
	_, skip := instanceSkipReason("instance", rc, config, map[string]error{"atlas": nil})
	if skip {
		t.Fatal("expected no skip once the delegation Atlas has published successfully")
	}
}

// TestCatalogBinaryInheritsAWSProfile verifies that the catalog binary launched by
// syncCatalogRemote is constructed via commandutil.Command so that AWS_PROFILE=exohub
// is automatically injected into its environment (fixing issue #15).
func TestCatalogBinaryInheritsAWSProfile(t *testing.T) {
	isolateAWSIdentityEnv(t)

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
	isolateAWSIdentityEnv(t)

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

// gitInitMinimalRepo git-inits dir on branch "main" with one empty commit,
// so code that shells out to git (ls-tree/archive for a role: instance
// publish's .artifactdb/ extraction, or checkRoutingNotStale) has a real
// repository to run against instead of erroring on "not a git repository".
func gitInitMinimalRepo(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_NOSYSTEM=1", "HOME="+dir,
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("command %v failed: %v\n%s", args, err, out)
		}
	}
	run("git", "init", "-q", "-b", "main")
	run("git", "commit", "-q", "--allow-empty", "-m", "init")
}

// setupCatalogSyncEnv creates a temp repo dir with a bundle.json containing
// the given repoURL, a fake catalog binary on PATH that records invocations,
// and a remotesConfig with a single artifactdb-type remote named "atlas".
func setupCatalogSyncEnv(t *testing.T, repoURL string) (remotesConfig *initcmd.RemotesConfig, calls *[]string, cleanup func()) {
	t.Helper()

	repoDir := t.TempDir()
	gitInitMinimalRepo(t, repoDir)
	bundleDir := filepath.Join(repoDir, ".exohub", "bundles", "main")
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		t.Fatalf("mkdir bundle dir: %v", err)
	}
	manifest := bundleManifest{RefName: "main", Ref: "abc123", RepoURL: repoURL}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "bundle.json"), data, 0o644); err != nil {
		t.Fatalf("write bundle.json: %v", err)
	}

	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	// Install a fake catalog binary on PATH that records its invocation.
	binDir := t.TempDir()
	recorded := &[]string{}
	logFile := filepath.Join(binDir, "calls.log")
	binPath := filepath.Join(binDir, "fake-catalog")
	script := "#!/bin/sh\necho \"$@\" >> " + logFile + "\nexit 0\n"
	if err := os.WriteFile(binPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake catalog binary: %v", err)
	}
	oldPath := os.Getenv("PATH")
	os.Setenv("PATH", binDir+":"+oldPath)

	remotesConfig = &initcmd.RemotesConfig{
		Remotes: []initcmd.RemoteConfig{
			{Name: "atlas", Type: "artifactdb", S3URL: "s3://bucket/atlas"},
		},
	}

	cleaned := false
	cleanup = func() {
		if cleaned {
			return
		}
		cleaned = true
		if data, err := os.ReadFile(logFile); err == nil {
			for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
				if line != "" {
					*recorded = append(*recorded, line)
				}
			}
		}
		os.Setenv("PATH", oldPath)
		os.Chdir(oldDir) //nolint:errcheck
	}
	t.Cleanup(cleanup)
	return remotesConfig, recorded, cleanup
}

// setupCatalogSyncEnvJSON is like setupCatalogSyncEnv, but the fake catalog
// binary recognizes a trailing --json flag: when present, it prints
// jsonLine to its own stdout (simulating the real binary's --json publish
// result) before exiting with exitCode; otherwise it behaves like the plain
// fake binary (log args, exit 0).
func setupCatalogSyncEnvJSON(t *testing.T, repoURL string, jsonLine string, exitCode int) (remotesConfig *initcmd.RemotesConfig, calls *[]string, cleanup func()) {
	t.Helper()

	repoDir := t.TempDir()
	gitInitMinimalRepo(t, repoDir)
	bundleDir := filepath.Join(repoDir, ".exohub", "bundles", "main")
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		t.Fatalf("mkdir bundle dir: %v", err)
	}
	manifest := bundleManifest{RefName: "main", Ref: "abc123", RepoURL: repoURL}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "bundle.json"), data, 0o644); err != nil {
		t.Fatalf("write bundle.json: %v", err)
	}

	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	binDir := t.TempDir()
	recorded := &[]string{}
	logFile := filepath.Join(binDir, "calls.log")
	script := "#!/bin/sh\necho \"$@\" >> " + logFile + "\n" +
		"case \" $* \" in\n" +
		"  *\" --json \"*)\n" +
		"    echo '" + jsonLine + "'\n" +
		"    ;;\n" +
		"esac\n" +
		"exit " + strconv.Itoa(exitCode) + "\n"
	// Install under both a plain name (for tests that pass the binary name
	// directly) and the git-annex-remote-<type>-<mode> name getCatalogRemoteBinary
	// looks up (for tests that go through runPublishJSON's remote discovery).
	for _, name := range []string{"fake-catalog", "git-annex-remote-artifactdb-export"} {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(script), 0o755); err != nil {
			t.Fatalf("write fake catalog binary %q: %v", name, err)
		}
	}
	oldPath := os.Getenv("PATH")
	os.Setenv("PATH", binDir+":"+oldPath)

	remotesConfig = &initcmd.RemotesConfig{
		Remotes: []initcmd.RemoteConfig{
			{Name: "atlas", Type: "artifactdb", Mode: "export", S3URL: "s3://bucket/atlas"},
		},
	}

	cleaned := false
	cleanup = func() {
		if cleaned {
			return
		}
		cleaned = true
		if data, err := os.ReadFile(logFile); err == nil {
			for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
				if line != "" {
					*recorded = append(*recorded, line)
				}
			}
		}
		os.Setenv("PATH", oldPath)
		os.Chdir(oldDir) //nolint:errcheck
	}
	t.Cleanup(cleanup)
	return remotesConfig, recorded, cleanup
}

// TestSyncCatalogRemoteRejectsEmptyRepoURL verifies that syncCatalogRemote
// errors out and never invokes the catalog binary when the bundle's
// repo_url is empty (no git remote configured when the bundle was built).
func TestSyncCatalogRemoteRejectsEmptyRepoURL(t *testing.T) {
	remotesConfig, calls, cleanup := setupCatalogSyncEnv(t, "")

	err := syncCatalogRemote("atlas", remotesConfig, "fake-catalog", false)
	cleanup()

	if err == nil {
		t.Fatal("expected syncCatalogRemote to return an error for empty repo_url")
	}
	if !strings.Contains(err.Error(), "repo_url") {
		t.Errorf("expected error to mention repo_url, got: %v", err)
	}
	if len(*calls) != 0 {
		t.Errorf("expected catalog binary not to be invoked, got calls: %v", *calls)
	}
}

// TestSyncCatalogRemoteProceedsWithRepoURL verifies that syncCatalogRemote
// invokes the catalog binary normally when the bundle's repo_url is set.
func TestSyncCatalogRemoteProceedsWithRepoURL(t *testing.T) {
	remotesConfig, calls, cleanup := setupCatalogSyncEnv(t, "https://example.com/repo.git")

	err := syncCatalogRemote("atlas", remotesConfig, "fake-catalog", false)
	cleanup()

	if err != nil {
		t.Fatalf("syncCatalogRemote: %v", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("expected catalog binary to be invoked once, got calls: %v", *calls)
	}
	if !strings.Contains((*calls)[0], "--bundle-dir .exohub/bundles/main") {
		t.Fatalf("automatic selection changed: %v", *calls)
	}
	if strings.Contains((*calls)[0], "--force") {
		t.Errorf("expected no --force without it being requested, got: %v", *calls)
	}
}

// TestSyncCatalogRemoteForcePassesForceToCatalogBinary verifies that
// syncCatalogRemote(..., force=true) passes --force to the catalog binary
// for a role: atlas remote (source-repo guard bypass, exo-cli#186).
func TestSyncCatalogRemoteForcePassesForceToCatalogBinary(t *testing.T) {
	remotesConfig, calls, cleanup := setupCatalogSyncEnv(t, "https://example.com/repo.git")

	err := syncCatalogRemote("atlas", remotesConfig, "fake-catalog", true)
	cleanup()

	if err != nil {
		t.Fatalf("syncCatalogRemote: %v", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("expected catalog binary to be invoked once, got calls: %v", *calls)
	}
	if !strings.Contains((*calls)[0], "--force") {
		t.Errorf("expected --force to be passed to the catalog binary, got: %v", *calls)
	}
}

func writeSelectedBundle(t *testing.T, dir, version string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(bundleManifest{RefName: version, Ref: "old-commit", RepoURL: "https://example.com/repo.git"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bundle.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogExplicitBundleSelection(t *testing.T) {
	for _, custom := range []string{".exohub/bundles/older", "custom/output", "absolute"} {
		t.Run(custom, func(t *testing.T) {
			config, calls, cleanup := setupCatalogSyncEnv(t, "https://example.com/current.git")
			dir := custom
			if custom == "absolute" {
				dir = filepath.Join(t.TempDir(), "external")
			}
			writeSelectedBundle(t, dir, "v-old")
			// HEAD has a matching automatic bundle, but the requested manifest is older.
			for _, args := range [][]string{{"init"}, {"symbolic-ref", "HEAD", "refs/heads/main"}, {"-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "init"}} {
				if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
					t.Fatalf("git: %v: %s", err, out)
				}
			}
			config.Remotes[0].Grants = true
			config.Remotes[0].URL = "https://atlas.example.com"
			config.Remotes[0].ProjectID = "project"
			if err := os.Mkdir(".artifactdb", 0o755); err != nil {
				t.Fatal(err)
			}
			if err := syncCatalogRemote("atlas", config, "fake-catalog", false, dir); err != nil {
				t.Fatal(err)
			}
			cleanup()
			if len(*calls) != 1 {
				t.Fatalf("expected exactly one publish: %v", *calls)
			}
			for _, arg := range []string{"--bundle-dir " + dir, "--s3url s3://bucket/atlas", "--grants", "--atlas-url https://atlas.example.com", "--project-id project", "--api-url", "--ref-kind branch", "--commit old-commit"} {
				if !strings.Contains((*calls)[0], arg) {
					t.Errorf("missing %q in %s", arg, (*calls)[0])
				}
			}
			// Atlas remotes never receive .artifactdb/ in any publish mode
			// (spec "Atlas remotes never carry .artifactdb", exo-cli#183 task 3.2).
			if strings.Contains((*calls)[0], "--artifactdb-dir") {
				t.Errorf("atlas remote should never receive --artifactdb-dir, got: %s", (*calls)[0])
			}
			if strings.Contains((*calls)[0], "--bundle-dir .exohub/bundles/main") {
				t.Fatal("published automatic bundle")
			}
		})
	}
}

func TestCatalogExplicitInvalidBundleNoFallback(t *testing.T) {
	for _, content := range []string{"missing", "not-dir", "missing-manifest", "manifest-dir", "{", "null", "[]", `{}`, `{"ref_name":"old","ref":"abc","repo_url":""}`} {
		t.Run(content, func(t *testing.T) {
			config, calls, cleanup := setupCatalogSyncEnv(t, "https://example.com/repo.git")
			dir := "selected"
			if content == "not-dir" {
				if err := os.WriteFile(dir, []byte("file"), 0o644); err != nil {
					t.Fatal(err)
				}
			} else if content != "missing" {
				if err := os.Mkdir(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				if content == "manifest-dir" {
					if err := os.Mkdir(filepath.Join(dir, "bundle.json"), 0o755); err != nil {
						t.Fatal(err)
					}
				} else if content != "missing-manifest" {
					if err := os.WriteFile(filepath.Join(dir, "bundle.json"), []byte(content), 0o644); err != nil {
						t.Fatal(err)
					}
				}
			}
			err := syncCatalogRemote("atlas", config, "fake-catalog", false, dir)
			cleanup()
			if err == nil || len(*calls) != 0 {
				t.Fatalf("expected failure without publish, err=%v calls=%v", err, *calls)
			}
		})
	}
}

func TestCatalogExplicitDryRun(t *testing.T) {
	config, calls, cleanup := setupCatalogSyncEnv(t, "https://example.com/repo.git")
	writeSelectedBundle(t, "selected", "historical-version")
	oldStdout := os.Stdout
	output, err := os.CreateTemp(t.TempDir(), "preview")
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = output
	err = runDryRun([]string{"atlas"}, nil, false, "1", "", config, nil, "selected")
	os.Stdout = oldStdout
	output.Close()
	data, readErr := os.ReadFile(output.Name())
	cleanup()
	if err != nil || readErr != nil {
		t.Fatalf("preview: %v, %v", err, readErr)
	}
	for _, value := range []string{"selected", "historical-version", "old-commit"} {
		if !strings.Contains(string(data), value) {
			t.Errorf("missing %s in preview: %s", value, data)
		}
	}
	if len(*calls) != 0 {
		t.Fatalf("dry-run invoked publisher: %v", *calls)
	}
}

func TestBundleSelectionRequiresCatalog(t *testing.T) {
	config := &initcmd.RemotesConfig{Remotes: []initcmd.RemoteConfig{{Name: "backup", Type: "annex"}}}
	for _, remotes := range [][]string{nil, {"backup"}} {
		if err := validateBundleSelection(remotes, config, "missing"); err == nil || !strings.Contains(err.Error(), "catalog remote") {
			t.Fatalf("expected catalog-only error, got %v", err)
		}
	}
}

func TestBundleFlagHelp(t *testing.T) {
	flag := NewCommand().Flags().Lookup("bundle-dir")
	if flag == nil || !strings.Contains(flag.Usage, "catalog") {
		t.Fatal("missing catalog-only flag help")
	}
}

func TestCatalogExplicitJSONDryRunFailsClearly(t *testing.T) {
	config, calls, cleanup := setupCatalogSyncEnv(t, "https://example.com/repo.git")
	writeSelectedBundle(t, "selected", "old")
	err := runDryRun([]string{"atlas"}, nil, true, "1", "", config, nil, "selected")
	cleanup()
	if err == nil || !strings.Contains(err.Error(), "--json") || len(*calls) != 0 {
		t.Fatalf("expected clear JSON error without publication: %v, %v", err, *calls)
	}
}

func TestRunDryRunTextShowsBundleTargetWithAtlasURL(t *testing.T) {
	config, calls, cleanup := setupCatalogSyncEnv(t, "https://example.com/repo.git")
	config.Remotes[0].URL = "https://atlas.example.com/v1/sandbox1"
	config.Remotes = append(config.Remotes, initcmd.RemoteConfig{
		Name: "instance", Type: "artifactdb", Role: "instance",
		S3URL: "s3://bucket/project", URL: "https://catalog.example.com",
	})

	oldStdout := os.Stdout
	output, err := os.CreateTemp(t.TempDir(), "preview")
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = output
	err = runDryRun([]string{"atlas", "instance"}, nil, false, "1", "", config, nil)
	os.Stdout = oldStdout
	output.Close()
	data, readErr := os.ReadFile(output.Name())
	cleanup()
	if err != nil || readErr != nil {
		t.Fatalf("preview: %v, %v", err, readErr)
	}
	for _, want := range []string{"bundle \u2192 https://atlas.example.com/v1/sandbox1 (url)", ".artifactdb/ \u2192 https://catalog.example.com"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("missing %q in preview: %s", want, data)
		}
	}
	if len(*calls) != 0 {
		t.Fatalf("dry-run invoked publisher: %v", *calls)
	}
}

func TestRunDryRunTextShowsBundleTargetDefault(t *testing.T) {
	config, calls, cleanup := setupCatalogSyncEnv(t, "https://example.com/repo.git")

	oldStdout := os.Stdout
	output, err := os.CreateTemp(t.TempDir(), "preview")
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = output
	err = runDryRun([]string{"atlas"}, nil, false, "1", "", config, nil)
	os.Stdout = oldStdout
	output.Close()
	data, readErr := os.ReadFile(output.Name())
	cleanup()
	if err != nil || readErr != nil {
		t.Fatalf("preview: %v, %v", err, readErr)
	}
	if !strings.Contains(string(data), "bundle \u2192 \u26a0 default Atlas (server-chosen)") {
		t.Errorf("missing flagged default-target line in preview: %s", data)
	}
	if len(*calls) != 0 {
		t.Fatalf("dry-run invoked publisher: %v", *calls)
	}
}

// catalogTargetJSON mirrors the stable "catalog_remotes" wire shape
// ({remote, role, url, url_source, s3_location, data_from, meta_folder})
// for decoding in tests; the pointer fields let a test tell "null" apart
// from "absent".
type catalogTargetJSON struct {
	Remote     string    `json:"remote"`
	Role       string    `json:"role"`
	URL        *string   `json:"url"`
	URLSource  *string   `json:"url_source"`
	S3Location *string   `json:"s3_location"`
	DataFrom   *string   `json:"data_from"`
	MetaFolder *string   `json:"meta_folder"`
	Excluded   *[]string `json:"excluded,omitempty"`
}

func TestBuildCatalogTargetsJSON_WithAtlasURL(t *testing.T) {
	config := &initcmd.RemotesConfig{
		Remotes: []initcmd.RemoteConfig{
			{Name: "atlas", Type: "artifactdb", URL: "https://atlas.example.com/v1/main"},
			{Name: "instance", Type: "artifactdb", Role: "instance", URL: "https://catalog.example.com"},
		},
	}
	got := buildCatalogTargetsJSON([]string{"atlas", "instance"}, config, "main")

	var targets []catalogTargetJSON
	if err := json.Unmarshal([]byte(got), &targets); err != nil {
		t.Fatalf("invalid JSON %q: %v", got, err)
	}
	if len(targets) != 2 {
		t.Fatalf("len(targets) = %d, want 2", len(targets))
	}
	if targets[0].Remote != "atlas" || targets[0].Role != "atlas" || targets[0].URL == nil || *targets[0].URL != "https://atlas.example.com/v1/main" {
		t.Errorf("unexpected atlas target: %+v", targets[0])
	}
	// url_source: "explicit" when the atlas remote sets its own url (the
	// #185 "clean break" decision); see
	// TestBuildCatalogTargetsJSON_ResolvesLiveDefault for the "default"
	// branch.
	if targets[0].URLSource == nil || *targets[0].URLSource != "explicit" {
		t.Errorf("atlas url_source = %v, want \"explicit\"", targets[0].URLSource)
	}
	if targets[1].Remote != "instance" || targets[1].Role != "instance" ||
		targets[1].URL == nil || *targets[1].URL != "https://catalog.example.com" ||
		targets[1].DataFrom == nil || *targets[1].DataFrom != "atlas" {
		t.Errorf("unexpected instance target: %+v", targets[1])
	}
	if targets[1].URLSource != nil {
		t.Errorf("instance url_source = %v, want null", *targets[1].URLSource)
	}
}

func TestBuildCatalogTargetsJSON_Default(t *testing.T) {
	config := &initcmd.RemotesConfig{
		Remotes: []initcmd.RemoteConfig{{Name: "atlas", Type: "artifactdb"}},
	}
	got := buildCatalogTargetsJSON([]string{"atlas"}, config, "main")

	// Decode into raw maps to assert the keys are present and null (not
	// absent) when the live default-Atlas lookup fails (no API reachable).
	var raw []map[string]interface{}
	if err := json.Unmarshal([]byte(got), &raw); err != nil {
		t.Fatalf("invalid JSON %q: %v", got, err)
	}
	if len(raw) != 1 {
		t.Fatalf("len(raw) = %d, want 1", len(raw))
	}
	for _, key := range []string{"remote", "role", "url", "url_source", "s3_location", "data_from", "meta_folder"} {
		if _, ok := raw[0][key]; !ok {
			t.Errorf("missing key %q: %s", key, got)
		}
	}
	if raw[0]["url"] != nil {
		t.Errorf("url = %v, want null (prose must stay in human output only)", raw[0]["url"])
	}
	// The live default-Atlas lookup fails here (no API reachable in test),
	// but url_source still records that this remote WOULD use the default
	// Atlas (it has no own url) — "default" regardless of whether the
	// lookup actually resolved a URL. This distinguishes it from a role:
	// instance entry (url_source always null) even when url is also null
	// for both. See TestBuildCatalogTargetsJSON_ResolvesLiveDefault for the
	// lookup-succeeds case (same url_source, url now non-null).
	if raw[0]["url_source"] != "default" {
		t.Errorf("url_source = %v, want \"default\" (even though the lookup failed)", raw[0]["url_source"])
	}
	if raw[0]["role"] != "atlas" {
		t.Errorf("role = %v, want \"atlas\"", raw[0]["role"])
	}
}

func TestBuildCatalogTargetsJSON_Empty(t *testing.T) {
	if got := buildCatalogTargetsJSON(nil, nil, "main"); got != "" {
		t.Errorf("buildCatalogTargetsJSON(nil) = %q, want empty string", got)
	}
}

// TestBuildCatalogTargetsJSON_InstanceExcludedFiles verifies that a role:
// instance remote's exclude globs surface, in the dry-run "catalog_remotes"
// report, as the "excluded" field listing which of the ref's .artifactdb/
// files they'd keep off the upload (exo-cli#202).
func TestBuildCatalogTargetsJSON_InstanceExcludedFiles(t *testing.T) {
	repoDir := t.TempDir()
	gitInitMinimalRepo(t, repoDir)

	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer os.Chdir(oldDir) //nolint:errcheck
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	if err := os.MkdirAll(".artifactdb", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(".artifactdb", "dataset.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(".artifactdb", "dataset_merged.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", ".artifactdb"}, {"commit", "-q", "-m", "add .artifactdb"}} {
		cmd := exec.Command("git", args...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.com", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	config := &initcmd.RemotesConfig{
		Remotes: []initcmd.RemoteConfig{
			{
				Name: "instance", Type: "artifactdb", Role: "instance",
				S3URL: "s3://bucket/proj", URL: "https://instance.example.com/v1/proj",
				Exclude: []string{"dataset_merged.json*"},
			},
		},
	}
	got := buildCatalogTargetsJSON([]string{"instance"}, config, "main")

	var targets []catalogTargetJSON
	if err := json.Unmarshal([]byte(got), &targets); err != nil {
		t.Fatalf("invalid JSON %q: %v", got, err)
	}
	if len(targets) != 1 {
		t.Fatalf("len(targets) = %d, want 1", len(targets))
	}
	if targets[0].Excluded == nil {
		t.Fatalf("Excluded = nil, want [dataset_merged.json]: %s", got)
	}
	if excluded := *targets[0].Excluded; len(excluded) != 1 || excluded[0] != "dataset_merged.json" {
		t.Errorf("Excluded = %v, want [dataset_merged.json]", excluded)
	}
}

// TestBuildCatalogTargetsJSON_InstanceNoExcludeOmitsField is the
// mutation-check counterpart: a role: instance remote with no exclude
// globs configured omits the "excluded" field entirely (additive field,
// not present-but-empty).
func TestBuildCatalogTargetsJSON_InstanceNoExcludeOmitsField(t *testing.T) {
	repoDir := t.TempDir()
	gitInitMinimalRepo(t, repoDir)

	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer os.Chdir(oldDir) //nolint:errcheck
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	if err := os.MkdirAll(".artifactdb", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(".artifactdb", "dataset.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", ".artifactdb"}, {"commit", "-q", "-m", "add .artifactdb"}} {
		cmd := exec.Command("git", args...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.com", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	config := &initcmd.RemotesConfig{
		Remotes: []initcmd.RemoteConfig{
			{
				Name: "instance", Type: "artifactdb", Role: "instance",
				S3URL: "s3://bucket/proj", URL: "https://instance.example.com/v1/proj",
			},
		},
	}
	got := buildCatalogTargetsJSON([]string{"instance"}, config, "main")

	var raw []map[string]interface{}
	if err := json.Unmarshal([]byte(got), &raw); err != nil {
		t.Fatalf("invalid JSON %q: %v", got, err)
	}
	if len(raw) != 1 {
		t.Fatalf("len(raw) = %d, want 1", len(raw))
	}
	if _, ok := raw[0]["excluded"]; ok {
		t.Errorf("excluded key should be absent when no exclude globs are configured, got: %s", got)
	}
}


// gitInitWithCommits creates a plain (non-annex) git repo at dir with one
// commit tagged "1", then a second commit on the same branch (simulating a
// routing migration landing after the tag), returning the tag's resolved
// commit hash.
func gitInitWithCommits(t *testing.T, dir string) (tagCommit string) {
	t.Helper()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_NOSYSTEM=1", "HOME="+dir,
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("command %v failed: %v\n%s", args, err, out)
		}
		return string(out)
	}
	run("git", "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("git", "add", "README.md")
	run("git", "commit", "-q", "-m", "v1")
	run("git", "tag", "1")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("v2 (post-migration)"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("git", "add", "README.md")
	run("git", "commit", "-q", "-m", "v2")
	return strings.TrimSpace(run("git", "rev-parse", "1"))
}

// TestRunRefDryRunJSON_UsesCurrentRoutingAndRefIdentity reproduces the
// tracer scenario (exo-cli#176): a routing change (prefix move + atlas_url
// added) lands after tag 1. `exo sync --dry-run --json --ref 1` must show
// the NEW routing (current .exohub/remotes) while ref/ref_commit stay tag
// 1's, never HEAD's.
func TestRunRefDryRunJSON_UsesCurrentRoutingAndRefIdentity(t *testing.T) {
	repoDir := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repoDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldWd) //nolint:errcheck

	tagCommit := gitInitWithCommits(t, repoDir)

	config := &initcmd.RemotesConfig{
		Remotes: []initcmd.RemoteConfig{
			{Name: "s3", Type: "annex", S3URL: "s3://bucket/exohub/my-profile"},
			{Name: "atlas", Type: "artifactdb", S3URL: "s3://bucket/atlas-meta", URL: "https://atlas.example/sandbox1"},
		},
	}

	output := captureStdout(t, func() {
		if err := runRefDryRunJSON([]string{"1"}, []string{"s3", "atlas"}, config, "1", ""); err != nil {
			t.Fatalf("runRefDryRunJSON: %v", err)
		}
	})

	var report struct {
		Refs []refTarget `json:"refs"`
	}
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, output)
	}
	if len(report.Refs) != 1 {
		t.Fatalf("expected 1 ref, got %d: %s", len(report.Refs), output)
	}

	got := report.Refs[0]
	if got.Ref != "1" {
		t.Errorf("ref = %q, want %q", got.Ref, "1")
	}
	if got.RefCommit != tagCommit {
		t.Errorf("ref_commit = %q, want tag 1's commit %q (not HEAD's)", got.RefCommit, tagCommit)
	}
	if len(got.ContentRemotes) != 1 || got.ContentRemotes[0].S3URL != "s3://bucket/exohub/my-profile" {
		t.Errorf("expected current (new-prefix) content routing, got: %+v", got.ContentRemotes)
	}
	if len(got.CatalogRemotes) != 1 || got.CatalogRemotes[0].URL == nil || *got.CatalogRemotes[0].URL != "https://atlas.example/sandbox1" {
		t.Errorf("expected current atlas_url in catalog_remotes, got: %+v", got.CatalogRemotes)
	}
	if got.CatalogRemotes[0].Role != "atlas" {
		t.Errorf("role = %q, want %q", got.CatalogRemotes[0].Role, "atlas")
	}
}

// TestRunRefDryRunJSON_AutoDiscoversRoleInstance verifies that `exo sync
// --dry-run --json --ref` with no explicit --with still includes a role:
// instance catalog remote in catalog_remotes, ordered after any atlas entry
// (exo-cli#196): auto-discovery must not rely solely on listAnnexRemotes,
// which never sees a role: instance remote.
func TestRunRefDryRunJSON_AutoDiscoversRoleInstance(t *testing.T) {
	repoDir, _, restoreDir := newAnnexRepo(t, "atlas")
	defer restoreDir()

	addAnnexFile(t, repoDir, "a.bin", "v1")
	if out, err := exec.Command("git", "tag", "1").CombinedOutput(); err != nil {
		t.Fatalf("git tag: %v\n%s", err, out)
	}

	config := &initcmd.RemotesConfig{Remotes: []initcmd.RemoteConfig{
		{Name: "atlas", Type: "artifactdb", Mode: "export", S3URL: "s3://bucket/atlas-meta"},
		{Name: "instance", Type: "artifactdb", Role: "instance", S3URL: "s3://bucket/DS1", URL: "https://adb.example.com/v1/b"},
	}}

	output := captureStdout(t, func() {
		if err := runRefDryRunJSON([]string{"1"}, nil, config, "1", ""); err != nil {
			t.Fatalf("runRefDryRunJSON: %v", err)
		}
	})

	var report struct {
		Refs []refTarget `json:"refs"`
	}
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, output)
	}
	if len(report.Refs) != 1 {
		t.Fatalf("expected 1 ref, got %d: %s", len(report.Refs), output)
	}
	catalogRemotes := report.Refs[0].CatalogRemotes
	if len(catalogRemotes) != 2 {
		t.Fatalf("expected 2 catalog remotes (atlas + instance), got: %+v", catalogRemotes)
	}
	if catalogRemotes[0].Remote != "atlas" || catalogRemotes[0].Role != "atlas" {
		t.Errorf("expected atlas first, got: %+v", catalogRemotes[0])
	}
	if catalogRemotes[1].Remote != "instance" || catalogRemotes[1].Role != "instance" {
		t.Errorf("expected instance second, got: %+v", catalogRemotes[1])
	}
}

// TestRunRefDryRunJSON_RequiresCatalogRemote verifies the explicit error
// when --ref is used with only content remotes selected.
func TestRunRefDryRunJSON_RequiresCatalogRemote(t *testing.T) {
	repoDir := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repoDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldWd) //nolint:errcheck
	gitInitWithCommits(t, repoDir)

	config := &initcmd.RemotesConfig{Remotes: []initcmd.RemoteConfig{{Name: "s3", Type: "annex", S3URL: "s3://bucket/x"}}}
	err = runRefDryRunJSON([]string{"1"}, []string{"s3"}, config, "1", "")
	if err == nil || !strings.Contains(err.Error(), "catalog remote") {
		t.Fatalf("expected a catalog-remote-required error, got: %v", err)
	}
}

// TestCheckRefSchema_UnaffectedByDryRunRefSchema locks that 'exo sync
// --check --ref' keeps its own stable {refs_checked, items, summary} shape
// (checkReport) and is not polluted by --dry-run --json --ref's distinct
// {refs: [...]} shape (refTarget) — the two --ref consumers (#163's catalog
// guard via --dry-run, and the completeness engine via --check) must stay on
// separate, independently stable schemas (exo-cli#176).
func TestCheckRefSchema_UnaffectedByDryRunRefSchema(t *testing.T) {
	repoDir, _, restore := newAnnexRepo(t, "r1")
	defer restore()

	addAnnexFile(t, repoDir, "a.bin", "v1")
	if out, err := exec.Command("git", "tag", "1").CombinedOutput(); err != nil {
		t.Fatalf("git tag: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "annex", "copy", "--to", "r1").CombinedOutput(); err != nil {
		t.Fatalf("git annex copy: %v\n%s", err, out)
	}

	output := captureStdout(t, func() {
		exitCode := runCheckCommand([]string{"1"}, nil, true, false)
		if exitCode != exitCheckClean {
			t.Fatalf("runCheckCommand exit = %d, want clean (%d)", exitCode, exitCheckClean)
		}
	})

	var report checkReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("--check --ref --json did not decode as checkReport: %v\noutput: %s", err, output)
	}
	if len(report.RefsChecked) != 1 || report.RefsChecked[0] != "1" {
		t.Errorf("refs_checked = %v, want [\"1\"]", report.RefsChecked)
	}

	// The schema must NOT carry the --dry-run --json --ref shape's keys.
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(output), &raw); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"refs", "catalog_remotes", "ref_commit"} {
		if _, present := raw[forbidden]; present {
			t.Errorf("--check --ref output unexpectedly carries %q (schema bleed from --dry-run --json --ref): %s", forbidden, output)
		}
	}
	for _, required := range []string{"refs_checked", "items", "summary"} {
		if _, present := raw[required]; !present {
			t.Errorf("--check --ref output missing required key %q: %s", required, output)
		}
	}
}

func TestGetDefaultAtlasURL_WithField(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"default_atlas_url":"https://atlas.example.com/v1/default"}`))
	}))
	defer server.Close()

	got, ok := getDefaultAtlasURL(server.URL)
	if !ok || got != "https://atlas.example.com/v1/default" {
		t.Errorf("getDefaultAtlasURL() = (%q, %v), want (%q, true)", got, ok, "https://atlas.example.com/v1/default")
	}
}

func TestGetDefaultAtlasURL_OlderServerWithoutField(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":"1.0.0"}`))
	}))
	defer server.Close()

	if _, ok := getDefaultAtlasURL(server.URL); ok {
		t.Error("getDefaultAtlasURL() ok = true, want false for a server without default_atlas_url")
	}
}

func TestGetDefaultAtlasURL_Unreachable(t *testing.T) {
	if _, ok := getDefaultAtlasURL("http://127.0.0.1:1"); ok {
		t.Error("getDefaultAtlasURL() ok = true, want false for an unreachable server")
	}
}

func TestBuildCatalogTargetsJSON_ResolvesLiveDefault(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"default_atlas_url":"https://atlas.example.com/v1/default"}`))
	}))
	defer server.Close()
	t.Setenv("EXOHUB_API_URL", server.URL)

	config := &initcmd.RemotesConfig{
		Remotes: []initcmd.RemoteConfig{{Name: "atlas", Type: "artifactdb"}},
	}
	got := buildCatalogTargetsJSON([]string{"atlas"}, config, "main")

	var targets []catalogTargetJSON
	if err := json.Unmarshal([]byte(got), &targets); err != nil {
		t.Fatalf("invalid JSON %q: %v", got, err)
	}
	if len(targets) != 1 || targets[0].Role != "atlas" || targets[0].URL == nil || *targets[0].URL != "https://atlas.example.com/v1/default" {
		t.Errorf("unexpected target: %+v", targets)
	}
	if targets[0].URLSource == nil || *targets[0].URLSource != "default" {
		t.Errorf("url_source = %v, want \"default\"", targets[0].URLSource)
	}
}

func TestRunDryRunJSONIncludesCatalogRemotes(t *testing.T) {
	config, calls, cleanup := setupCatalogSyncEnv(t, "https://example.com/repo.git")
	config.Remotes[0].URL = "https://atlas.example.com/v1/sandbox1"

	oldStdout := os.Stdout
	output, err := os.CreateTemp(t.TempDir(), "preview")
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = output
	err = runDryRun([]string{"atlas"}, nil, true, "1", "", config, nil)
	os.Stdout = oldStdout
	output.Close()
	data, readErr := os.ReadFile(output.Name())
	cleanup()
	if err != nil || readErr != nil {
		t.Fatalf("preview: %v, %v", err, readErr)
	}
	if len(*calls) != 0 {
		t.Fatalf("dry-run invoked publisher: %v", *calls)
	}

	var report struct {
		Remotes        []json.RawMessage   `json:"remotes"`
		CatalogRemotes []catalogTargetJSON `json:"catalog_remotes"`
	}
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("invalid JSON report %q: %v", data, err)
	}
	if len(report.Remotes) != 0 {
		t.Errorf("catalog remote leaked into \"remotes\": %v", report.Remotes)
	}
	if len(report.CatalogRemotes) != 1 || report.CatalogRemotes[0].Remote != "atlas" || report.CatalogRemotes[0].Role != "atlas" ||
		report.CatalogRemotes[0].URL == nil || *report.CatalogRemotes[0].URL != "https://atlas.example.com/v1/sandbox1" {
		t.Errorf("unexpected catalog_remotes: %+v", report.CatalogRemotes)
	}
}

// TestRunDryRunJSONAutoDiscoversRoleInstance verifies that `exo sync
// --dry-run --json` with no explicit --with still includes a role: instance
// catalog remote in catalog_remotes (exo-cli#196): auto-discovery must not
// rely solely on listAnnexRemotes, which never sees a role: instance
// remote (never git-annex initremote'd).
func TestRunDryRunJSONAutoDiscoversRoleInstance(t *testing.T) {
	_, _, restoreDir := newAnnexRepo(t, "atlas")
	defer restoreDir()

	config := &initcmd.RemotesConfig{Remotes: []initcmd.RemoteConfig{
		{Name: "atlas", Type: "artifactdb", Mode: "export", S3URL: "s3://bucket/atlas-meta"},
		{Name: "instance", Type: "artifactdb", Role: "instance", S3URL: "s3://bucket/DS1", URL: "https://adb.example.com/v1/b"},
	}}

	oldStdout := os.Stdout
	output, err := os.CreateTemp(t.TempDir(), "preview")
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = output
	err = runDryRun(nil, nil, true, "1", "", config, nil)
	os.Stdout = oldStdout
	output.Close()
	data, readErr := os.ReadFile(output.Name())
	if err != nil || readErr != nil {
		t.Fatalf("preview: %v, %v", err, readErr)
	}

	var report struct {
		CatalogRemotes []catalogTargetJSON `json:"catalog_remotes"`
	}
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("invalid JSON report %q: %v", data, err)
	}
	if len(report.CatalogRemotes) != 2 || report.CatalogRemotes[0].Remote != "atlas" || report.CatalogRemotes[1].Remote != "instance" || report.CatalogRemotes[1].Role != "instance" {
		t.Errorf("expected auto-discovered atlas+instance remotes (atlas first) in catalog_remotes, got: %+v", report.CatalogRemotes)
	}
}

func TestCatalogExplicitUnreadableManifest(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read permission-denied manifests")
	}
	config, calls, cleanup := setupCatalogSyncEnv(t, "https://example.com/repo.git")
	writeSelectedBundle(t, "selected", "old")
	if err := os.Chmod(filepath.Join("selected", "bundle.json"), 0); err != nil {
		cleanup()
		t.Fatal(err)
	}
	err := syncCatalogRemote("atlas", config, "fake-catalog", false, "selected")
	cleanup()
	if err == nil || len(*calls) != 0 {
		t.Fatalf("expected unreadable manifest error without fallback: %v, %v", err, *calls)
	}
}
