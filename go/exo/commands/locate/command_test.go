package locate

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestParseAnnexKey(t *testing.T) {
	tests := []struct {
		name        string
		key         string
		wantBackend string
		wantSize    int64
	}{
		{"SHA256E with size and extension", "SHA256E-s12345--abcdef1234567890.csv", "SHA256E", 12345},
		{"MD5E with size", "MD5E-s999--d41d8cd98f00b204e9800998ecf8427e.txt", "MD5E", 999},
		{"SHA256E with large size", "SHA256E-s1073741824--abcdef.parquet", "SHA256E", 1073741824},
		{"SHA1 backend", "SHA1-s500--da39a3ee5e6b4b0d3255bfef95601890afd80709", "SHA1", 500},
		{"WORM key", "WORM-s1234-m1234567890--file.dat", "WORM", 1234},
		{"empty key", "", "", 0},
		{"no dash in key", "SHA256E", "SHA256E", 0},
		{"key without size field", "URL-something", "URL", 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			backend, size := parseAnnexKey(tc.key)
			if backend != tc.wantBackend {
				t.Errorf("backend = %q, want %q", backend, tc.wantBackend)
			}
			if size != tc.wantSize {
				t.Errorf("size = %d, want %d", size, tc.wantSize)
			}
		})
	}
}

func TestExtractRemoteName(t *testing.T) {
	tests := []struct {
		name string
		desc string
		want string
	}{
		{"simple bracket name", "[origin]", "origin"},
		{"UUID with bracket name", "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee -- some description [backup]", "backup"},
		{"here tag", "[here]", ""},
		{"no brackets", "some description without brackets", ""},
		{"empty string", "", ""},
		{"bracket with spaces", "[ myremote ]", "myremote"},
		{"empty brackets", "[]", ""},
		{"multiple brackets takes last", "uuid -- [old] renamed [newname]", "newname"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := extractRemoteName(tc.desc)
			if got != tc.want {
				t.Errorf("extractRemoteName(%q) = %q, want %q", tc.desc, got, tc.want)
			}
		})
	}
}

func TestFormatSize(t *testing.T) {
	tests := []struct {
		name  string
		bytes int64
		want  string
	}{
		{"zero", 0, ""},
		{"negative", -1, ""},
		{"small bytes", 512, "512 B"},
		{"one KB", 1024, "1.0 KB"},
		{"kilobytes", 1536, "1.5 KB"},
		{"one MB", 1048576, "1.0 MB"},
		{"megabytes", 15728640, "15.0 MB"},
		{"one GB", 1073741824, "1.0 GB"},
		{"gigabytes", 5368709120, "5.0 GB"},
		{"one TB", 1099511627776, "1.0 TB"},
		{"terabytes", 2199023255552, "2.0 TB"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := formatSize(tc.bytes)
			if got != tc.want {
				t.Errorf("formatSize(%d) = %q, want %q", tc.bytes, got, tc.want)
			}
		})
	}
}

func TestFormatRemoteWithEmoji(t *testing.T) {
	tests := []struct {
		name        string
		remoteName  string
		remoteTypes map[string]string
		want        string
	}{
		{"annex type", "s3-annex", map[string]string{"s3-annex": "annex"}, "📦 s3-annex"},
		{"export type", "s3-export", map[string]string{"s3-export": "export"}, "🔴 s3-export"},
		{"import type", "s3-import", map[string]string{"s3-import": "import"}, "🟢 s3-import"},
		{"exospace type", "my-exospace", map[string]string{"my-exospace": "exospace"}, "📁 my-exospace"},
		{"artifactdb type", "atlas", map[string]string{"atlas": "artifactdb"}, "💎 atlas"},
		{"unknown type", "custom", map[string]string{"custom": "unknown"}, "   custom"},
		{"not in map", "other", map[string]string{"s3-annex": "annex"}, "other"},
		{"nil map", "s3-annex", nil, "s3-annex"},
		{"empty type", "s3-annex", map[string]string{"s3-annex": ""}, "s3-annex"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := formatRemoteWithEmoji(tc.remoteName, tc.remoteTypes)
			if got != tc.want {
				t.Errorf("formatRemoteWithEmoji(%q, ...) = %q, want %q", tc.remoteName, got, tc.want)
			}
		})
	}
}

func TestHasFileArgs(t *testing.T) {
	// Create a temp dir for testing
	tmpDir := t.TempDir()
	tmpFile := tmpDir + "/testfile.txt"
	os.WriteFile(tmpFile, []byte("test"), 0644)

	tests := []struct {
		name  string
		paths []string
		want  bool
	}{
		{"empty paths", []string{}, false},
		{"directory only", []string{tmpDir}, false},
		{"file path", []string{tmpFile}, true},
		{"nonexistent path", []string{"/nonexistent/file.txt"}, false},
		{"mixed dir and file", []string{tmpDir, tmpFile}, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := hasFileArgs(tc.paths)
			if got != tc.want {
				t.Errorf("hasFileArgs(%v) = %v, want %v", tc.paths, got, tc.want)
			}
		})
	}
}

func TestExpandPaths(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := tmpDir + "/testfile.txt"
	os.WriteFile(tmpFile, []byte("test"), 0644)

	tests := []struct {
		name  string
		paths []string
		want  int
	}{
		{"empty", []string{}, 0},
		{"file included", []string{tmpFile}, 1},
		{"dir excluded", []string{tmpDir}, 0},
		{"nonexistent excluded", []string{"/no/such/file"}, 0},
		{"mixed", []string{tmpDir, tmpFile, "/no/such/file"}, 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := expandPaths(tc.paths)
			if len(got) != tc.want {
				t.Errorf("expandPaths(%v) returned %d items, want %d", tc.paths, len(got), tc.want)
			}
		})
	}
}

func TestLinePrinterJSONL(t *testing.T) {
	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	printer := &linePrinter{jsonOut: true, remoteTypes: nil}

	printer.printEntry(fileEntry{File: "data/file.csv", Type: "annex", Key: "SHA256E-s100--abc.csv", Present: true, Remotes: []string{"here", "s3-annex"}})
	printer.printEntry(fileEntry{File: "README.md", Type: "git"})
	printer.printEntry(fileEntry{File: "new.txt", Type: "untracked"})

	w.Close()
	var buf bytes.Buffer
	buf.ReadFrom(r)
	os.Stdout = old

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 JSONL lines, got %d: %q", len(lines), buf.String())
	}

	// Verify first line is valid JSON with correct fields
	var entry1 fileEntry
	if err := json.Unmarshal([]byte(lines[0]), &entry1); err != nil {
		t.Fatalf("line 1 not valid JSON: %v", err)
	}
	if entry1.File != "data/file.csv" {
		t.Errorf("line 1 file = %q, want %q", entry1.File, "data/file.csv")
	}
	if entry1.Type != "annex" {
		t.Errorf("line 1 type = %q, want %q", entry1.Type, "annex")
	}
	if !entry1.Present {
		t.Error("line 1 present = false, want true")
	}
	if len(entry1.Remotes) != 2 {
		t.Errorf("line 1 remotes count = %d, want 2", len(entry1.Remotes))
	}

	// Verify second line
	var entry2 fileEntry
	if err := json.Unmarshal([]byte(lines[1]), &entry2); err != nil {
		t.Fatalf("line 2 not valid JSON: %v", err)
	}
	if entry2.Type != "git" {
		t.Errorf("line 2 type = %q, want %q", entry2.Type, "git")
	}
	if entry2.Key != "" {
		t.Errorf("line 2 key should be empty, got %q", entry2.Key)
	}

	// Verify third line
	var entry3 fileEntry
	if err := json.Unmarshal([]byte(lines[2]), &entry3); err != nil {
		t.Fatalf("line 3 not valid JSON: %v", err)
	}
	if entry3.Type != "untracked" {
		t.Errorf("line 3 type = %q, want %q", entry3.Type, "untracked")
	}
}

func TestLinePrinterTextFormat(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	// Non-TTY so no ANSI codes
	printer := &linePrinter{jsonOut: false, remoteTypes: nil}

	printer.printEntry(fileEntry{File: "data/file.csv", Type: "annex", Remotes: []string{"here", "s3-annex"}})
	printer.printEntry(fileEntry{File: "README.md", Type: "git"})
	printer.printEntry(fileEntry{File: "new.txt", Type: "untracked"})

	w.Close()
	var buf bytes.Buffer
	buf.ReadFrom(r)
	os.Stdout = old

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d: %q", len(lines), buf.String())
	}

	// Annex line should contain type, file, and remotes
	if !strings.Contains(lines[0], "annex") {
		t.Errorf("line 1 missing 'annex': %q", lines[0])
	}
	if !strings.Contains(lines[0], "data/file.csv") {
		t.Errorf("line 1 missing file path: %q", lines[0])
	}
	if !strings.Contains(lines[0], "*here") {
		t.Errorf("line 1 missing '*here': %q", lines[0])
	}
	if !strings.Contains(lines[0], "s3-annex") {
		t.Errorf("line 1 missing remote: %q", lines[0])
	}

	// Git line
	if !strings.Contains(lines[1], "git") {
		t.Errorf("line 2 missing 'git': %q", lines[1])
	}
	if !strings.Contains(lines[1], "README.md") {
		t.Errorf("line 2 missing file path: %q", lines[1])
	}

	// Untracked line
	if !strings.Contains(lines[2], "untracked") {
		t.Errorf("line 3 missing 'untracked': %q", lines[2])
	}
}

func TestLinePrinterTextAlignment(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	printer := &linePrinter{jsonOut: false, remoteTypes: nil}

	printer.printEntry(fileEntry{File: "file1.csv", Type: "annex", Remotes: []string{"here"}})
	printer.printEntry(fileEntry{File: "file2.md", Type: "git"})
	printer.printEntry(fileEntry{File: "file3.txt", Type: "untracked"})

	w.Close()
	var buf bytes.Buffer
	buf.ReadFrom(r)
	os.Stdout = old

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}

	// All type labels should be padded to typeWidth (9 chars)
	// so file paths should start at the same column
	for i, line := range lines {
		// After the type label (9 chars) + "  " (2 chars), the path should start at position 11
		if len(line) < 11 {
			t.Errorf("line %d too short: %q", i+1, line)
			continue
		}
		// The 10th and 11th chars should be spaces (separator)
		if line[9:11] != "  " {
			t.Errorf("line %d: expected spaces at positions 10-11, got %q in %q", i+1, line[9:11], line)
		}
	}
}

func TestLinePrinterLockedAnnex(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	printer := &linePrinter{jsonOut: false, remoteTypes: nil}
	printer.printEntry(fileEntry{File: "locked.bin", Type: "annex", Locked: true, Remotes: []string{"here"}})
	printer.printEntry(fileEntry{File: "unlocked.bin", Type: "annex", Locked: false, Remotes: []string{"here"}})

	w.Close()
	var buf bytes.Buffer
	buf.ReadFrom(r)
	os.Stdout = old

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %q", len(lines), buf.String())
	}

	if !strings.Contains(lines[0], "annex🔒") {
		t.Errorf("locked line missing 'annex🔒': %q", lines[0])
	}
	if strings.Contains(lines[1], "🔒") {
		t.Errorf("unlocked line should not contain lock emoji: %q", lines[1])
	}
}

func TestLinePrinterLockedAlignment(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	printer := &linePrinter{jsonOut: false, remoteTypes: nil}
	printer.printEntry(fileEntry{File: "a.bin", Type: "annex", Locked: true})
	printer.printEntry(fileEntry{File: "b.bin", Type: "annex", Locked: false})
	printer.printEntry(fileEntry{File: "c.txt", Type: "git"})
	printer.printEntry(fileEntry{File: "d.txt", Type: "untracked"})

	w.Close()
	var buf bytes.Buffer
	buf.ReadFrom(r)
	os.Stdout = old

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines, got %d", len(lines))
	}

	// All file paths should start at the same display column.
	// Use lipgloss.Width to compute display width of the prefix before the file path.
	files := []string{"a.bin", "b.bin", "c.txt", "d.txt"}
	displayPositions := make([]int, len(lines))
	for i, line := range lines {
		pos := strings.Index(line, files[i])
		if pos < 0 {
			t.Fatalf("line %d missing file %q: %q", i+1, files[i], line)
		}
		displayPositions[i] = lipgloss.Width(line[:pos])
	}

	for i := 1; i < len(displayPositions); i++ {
		if displayPositions[i] != displayPositions[0] {
			t.Errorf("display column mismatch: line 1 at %d, line %d at %d\nlines:\n%s",
				displayPositions[0], i+1, displayPositions[i], buf.String())
		}
	}
}

func TestLinePrinterLockedJSON(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	printer := &linePrinter{jsonOut: true, remoteTypes: nil}
	printer.printEntry(fileEntry{File: "locked.bin", Type: "annex", Key: "SHA256E-s100--abc.bin", Locked: true})
	printer.printEntry(fileEntry{File: "unlocked.bin", Type: "annex", Key: "SHA256E-s200--def.bin", Locked: false})

	w.Close()
	var buf bytes.Buffer
	buf.ReadFrom(r)
	os.Stdout = old

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}

	var entry1 fileEntry
	if err := json.Unmarshal([]byte(lines[0]), &entry1); err != nil {
		t.Fatalf("line 1 not valid JSON: %v", err)
	}
	if !entry1.Locked {
		t.Error("line 1 locked = false, want true")
	}

	// Unlocked entry should omit locked field (omitempty)
	if strings.Contains(lines[1], `"locked"`) {
		t.Errorf("unlocked entry should omit 'locked': %s", lines[1])
	}
}

func TestExpandPathsStderr(t *testing.T) {
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	expandPaths([]string{"/no/such/file.txt"})

	w.Close()
	var buf bytes.Buffer
	buf.ReadFrom(r)
	os.Stderr = oldStderr

	output := buf.String()
	if !strings.Contains(output, "locate: /no/such/file.txt: no such file") {
		t.Errorf("expected stderr error for non-existent file, got: %q", output)
	}
}

func TestLoadExohubRemotes(t *testing.T) {
	// Running from a non-repo dir — should return nil gracefully
	oldDir, _ := os.Getwd()
	tmpDir := t.TempDir()
	os.Chdir(tmpDir)
	defer os.Chdir(oldDir)

	result := loadExohubRemotes()
	if result != nil {
		t.Errorf("loadExohubRemotes() in empty dir should return nil, got %v", result)
	}
}

func TestLoadExohubRemotesWithFile(t *testing.T) {
	oldDir, _ := os.Getwd()
	tmpDir := t.TempDir()
	os.Chdir(tmpDir)
	defer os.Chdir(oldDir)

	os.MkdirAll(".exohub", 0755)
	os.WriteFile(".exohub/remotes", []byte(`remotes:
  - name: s3-annex
    type: annex
  - name: s3-export
    type: export
`), 0644)

	result := loadExohubRemotes()
	if result == nil {
		t.Fatal("loadExohubRemotes() returned nil, expected map")
	}
	if result["s3-annex"] != "annex" {
		t.Errorf("s3-annex type = %q, want %q", result["s3-annex"], "annex")
	}
	if result["s3-export"] != "export" {
		t.Errorf("s3-export type = %q, want %q", result["s3-export"], "export")
	}
}

func TestNewCommand(t *testing.T) {
	cmd := NewCommand()

	if cmd.Use != "locate [paths...]" {
		t.Errorf("Use = %q, want %q", cmd.Use, "locate [paths...]")
	}

	jsonFlag := cmd.Flags().Lookup("json")
	if jsonFlag == nil {
		t.Fatal("missing --json flag")
	}

	fastFlag := cmd.Flags().Lookup("fast")
	if fastFlag == nil {
		t.Fatal("missing --fast flag")
	}

	for _, name := range []string{"annex", "git", "untracked", "present", "missing", "locked", "unlocked"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("missing --%s flag", name)
		}
	}
}

func TestFilterOptsShouldShow(t *testing.T) {
	tests := []struct {
		name     string
		filters  filterOpts
		fileType string
		present  bool
		locked   bool
		want     bool
	}{
		{"no filters shows annex", filterOpts{}, "annex", true, false, true},
		{"no filters shows git", filterOpts{}, "git", false, false, true},
		{"no filters shows untracked", filterOpts{}, "untracked", false, false, true},
		{"annex filter shows annex", filterOpts{annex: true, anyFilter: true}, "annex", false, false, true},
		{"annex filter hides git", filterOpts{annex: true, anyFilter: true}, "git", false, false, false},
		{"annex filter hides untracked", filterOpts{annex: true, anyFilter: true}, "untracked", false, false, false},
		{"git filter shows git", filterOpts{git: true, anyFilter: true}, "git", false, false, true},
		{"git filter hides annex", filterOpts{git: true, anyFilter: true}, "annex", true, false, false},
		{"untracked filter shows untracked", filterOpts{untracked: true, anyFilter: true}, "untracked", false, false, true},
		{"untracked filter hides git", filterOpts{untracked: true, anyFilter: true}, "git", false, false, false},
		{"OR: annex+git shows annex", filterOpts{annex: true, git: true, anyFilter: true}, "annex", false, false, true},
		{"OR: annex+git shows git", filterOpts{annex: true, git: true, anyFilter: true}, "git", false, false, true},
		{"OR: annex+git hides untracked", filterOpts{annex: true, git: true, anyFilter: true}, "untracked", false, false, false},
		{"present shows present annex", filterOpts{present: true, anyFilter: true}, "annex", true, false, true},
		{"present hides missing annex", filterOpts{present: true, anyFilter: true}, "annex", false, false, false},
		{"present hides git", filterOpts{present: true, anyFilter: true}, "git", false, false, false},
		{"present hides untracked", filterOpts{present: true, anyFilter: true}, "untracked", false, false, false},
		{"missing shows missing annex", filterOpts{missing: true, anyFilter: true}, "annex", false, false, true},
		{"missing hides present annex", filterOpts{missing: true, anyFilter: true}, "annex", true, false, false},
		{"missing hides git", filterOpts{missing: true, anyFilter: true}, "git", false, false, false},
		{"missing hides untracked", filterOpts{missing: true, anyFilter: true}, "untracked", false, false, false},
		// --locked filter tests
		{"locked shows locked annex", filterOpts{locked: true, anyFilter: true}, "annex", false, true, true},
		{"locked hides unlocked annex", filterOpts{locked: true, anyFilter: true}, "annex", false, false, false},
		{"locked hides git", filterOpts{locked: true, anyFilter: true}, "git", false, false, false},
		{"locked hides untracked", filterOpts{locked: true, anyFilter: true}, "untracked", false, false, false},
		// --unlocked filter tests
		{"unlocked shows unlocked annex", filterOpts{unlocked: true, anyFilter: true}, "annex", false, false, true},
		{"unlocked hides locked annex", filterOpts{unlocked: true, anyFilter: true}, "annex", false, true, false},
		{"unlocked hides git", filterOpts{unlocked: true, anyFilter: true}, "git", false, false, false},
		{"unlocked hides untracked", filterOpts{unlocked: true, anyFilter: true}, "untracked", false, false, false},
		// --locked + --present composition
		{"locked+present shows locked present annex", filterOpts{locked: true, present: true, anyFilter: true}, "annex", true, true, true},
		{"locked+present hides locked missing annex", filterOpts{locked: true, present: true, anyFilter: true}, "annex", false, true, false},
		{"locked+present hides unlocked present annex", filterOpts{locked: true, present: true, anyFilter: true}, "annex", true, false, false},
		// --locked + --missing composition
		{"locked+missing shows locked missing annex", filterOpts{locked: true, missing: true, anyFilter: true}, "annex", false, true, true},
		{"locked+missing hides locked present annex", filterOpts{locked: true, missing: true, anyFilter: true}, "annex", true, true, false},
		// --unlocked + --present composition
		{"unlocked+present shows unlocked present annex", filterOpts{unlocked: true, present: true, anyFilter: true}, "annex", true, false, true},
		{"unlocked+present hides unlocked missing annex", filterOpts{unlocked: true, present: true, anyFilter: true}, "annex", false, false, false},
		{"unlocked+present hides locked present annex", filterOpts{unlocked: true, present: true, anyFilter: true}, "annex", true, true, false},
		// --unlocked + --missing composition
		{"unlocked+missing shows unlocked missing annex", filterOpts{unlocked: true, missing: true, anyFilter: true}, "annex", false, false, true},
		{"unlocked+missing hides unlocked present annex", filterOpts{unlocked: true, missing: true, anyFilter: true}, "annex", true, false, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.filters.shouldShow(tc.fileType, tc.present, tc.locked)
			if got != tc.want {
				t.Errorf("shouldShow(%q, %v, %v) = %v, want %v", tc.fileType, tc.present, tc.locked, got, tc.want)
			}
		})
	}
}

func TestFileEntryJSONOmitEmpty(t *testing.T) {
	// Git entry should omit annex-specific fields
	gitEntry := fileEntry{File: "README.md", Type: "git"}
	data, err := json.Marshal(gitEntry)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if strings.Contains(s, "key") {
		t.Errorf("git entry JSON should not contain 'key': %s", s)
	}
	if strings.Contains(s, "remotes") {
		t.Errorf("git entry JSON should not contain 'remotes': %s", s)
	}
	if strings.Contains(s, "present") {
		t.Errorf("git entry JSON should not contain 'present': %s", s)
	}

	// Annex entry should include all fields
	annexEntry := fileEntry{
		File:    "data.csv",
		Type:    "annex",
		Key:     "SHA256E-s100--abc.csv",
		Size:    100,
		Present: true,
		Remotes: []string{"here", "s3-annex"},
	}
	data, err = json.Marshal(annexEntry)
	if err != nil {
		t.Fatal(err)
	}
	s = string(data)
	if !strings.Contains(s, `"key"`) {
		t.Errorf("annex entry JSON should contain 'key': %s", s)
	}
	if !strings.Contains(s, `"remotes"`) {
		t.Errorf("annex entry JSON should contain 'remotes': %s", s)
	}
	if !strings.Contains(s, `"present":true`) {
		t.Errorf("annex entry JSON should contain 'present:true': %s", s)
	}
}

func TestLinePrinterAnnexNoRemotes(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	printer := &linePrinter{jsonOut: false, remoteTypes: nil}
	printer.printEntry(fileEntry{File: "orphan.bin", Type: "annex"})

	w.Close()
	var buf bytes.Buffer
	buf.ReadFrom(r)
	os.Stdout = old

	line := strings.TrimSpace(buf.String())
	if !strings.Contains(line, "annex") || !strings.Contains(line, "orphan.bin") {
		t.Errorf("unexpected output: %q", line)
	}
	// Should not have trailing separator when no remotes
	if strings.HasSuffix(line, "  ") {
		t.Errorf("trailing spaces when no remotes: %q", line)
	}
}

func TestLinePrinterHereFirst(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	printer := &linePrinter{jsonOut: false, remoteTypes: nil}
	printer.printEntry(fileEntry{File: "f.csv", Type: "annex", Remotes: []string{"here", "s3-annex", "backup"}})

	w.Close()
	var buf bytes.Buffer
	buf.ReadFrom(r)
	os.Stdout = old

	line := buf.String()
	hereIdx := strings.Index(line, "*here")
	annexIdx := strings.Index(line, "s3-annex")
	if hereIdx < 0 || annexIdx < 0 {
		t.Fatalf("missing expected content: %q", line)
	}
	if hereIdx > annexIdx {
		t.Errorf("*here should appear before s3-annex, got positions %d and %d", hereIdx, annexIdx)
	}
}

func TestLoadExohubRemotesMalformedYAML(t *testing.T) {
	oldDir, _ := os.Getwd()
	tmpDir := t.TempDir()
	os.Chdir(tmpDir)
	defer os.Chdir(oldDir)

	os.MkdirAll(".exohub", 0755)
	os.WriteFile(".exohub/remotes", []byte(`not: valid: yaml: [[[`), 0644)

	result := loadExohubRemotes()
	if result != nil {
		t.Errorf("malformed YAML should return nil, got %v", result)
	}
}

func TestParseAnnexKeyZeroSize(t *testing.T) {
	backend, size := parseAnnexKey("SHA256E-s0--abc.csv")
	if backend != "SHA256E" {
		t.Errorf("backend = %q, want SHA256E", backend)
	}
	if size != 0 {
		t.Errorf("size = %d, want 0", size)
	}
}

func TestExtractRemoteNameNestedBrackets(t *testing.T) {
	got := extractRemoteName("uuid -- desc [with [nested]] [actual]")
	if got != "actual" {
		t.Errorf("got %q, want %q", got, "actual")
	}
}

func TestLinePrinterJSONLOmitsEmptyRemotes(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	printer := &linePrinter{jsonOut: true, remoteTypes: nil}
	printer.printEntry(fileEntry{File: "f.txt", Type: "git"})

	w.Close()
	var buf bytes.Buffer
	buf.ReadFrom(r)
	os.Stdout = old

	s := strings.TrimSpace(buf.String())
	if strings.Contains(s, "remotes") {
		t.Errorf("git JSONL should omit remotes: %s", s)
	}
	if strings.Contains(s, "key") {
		t.Errorf("git JSONL should omit key: %s", s)
	}
}

func TestWhereisEntryUnmarshalUntrusted(t *testing.T) {
	input := `{"file":"data/file.parquet","key":"SHA256E-s100--abc.parquet","whereis":[{"uuid":"aaa","description":"user@host:/path [here]","here":true}],"untrusted":[{"uuid":"bbb","description":"[s5-export-studies]","here":false}]}`

	var entry whereisEntry
	if err := json.Unmarshal([]byte(input), &entry); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if len(entry.Whereis) != 1 {
		t.Errorf("Whereis count = %d, want 1", len(entry.Whereis))
	}
	if len(entry.Untrusted) != 1 {
		t.Errorf("Untrusted count = %d, want 1", len(entry.Untrusted))
	}
	if entry.Untrusted[0].Description != "[s5-export-studies]" {
		t.Errorf("Untrusted[0].Description = %q, want %q", entry.Untrusted[0].Description, "[s5-export-studies]")
	}
}

func TestBuildRemotesIncludesUntrusted(t *testing.T) {
	w := whereisEntry{
		File: "data/file.parquet",
		Key:  "SHA256E-s100--abc.parquet",
		Whereis: []whereisRemote{
			{UUID: "aaa", Description: "user@host:/path [here]", Here: true},
		},
		Untrusted: []whereisRemote{
			{UUID: "bbb", Description: "[s5-export-studies]", Here: false},
		},
	}

	present := false
	var remotes []string
	for _, r := range append(w.Whereis, w.Untrusted...) {
		if r.Here {
			present = true
			continue
		}
		name := extractRemoteName(r.Description)
		if name != "" {
			remotes = append(remotes, name)
		}
	}
	if present {
		remotes = append([]string{"here"}, remotes...)
	}

	if !present {
		t.Error("present should be true")
	}
	if len(remotes) != 2 {
		t.Fatalf("remotes count = %d, want 2; got %v", len(remotes), remotes)
	}
	if remotes[0] != "here" {
		t.Errorf("remotes[0] = %q, want %q", remotes[0], "here")
	}
	if remotes[1] != "s5-export-studies" {
		t.Errorf("remotes[1] = %q, want %q", remotes[1], "s5-export-studies")
	}
}

func TestBuildRemotesUntrustedOnly(t *testing.T) {
	w := whereisEntry{
		File: "data/missing.parquet",
		Key:  "SHA256E-s200--def.parquet",
		Untrusted: []whereisRemote{
			{UUID: "ccc", Description: "[s5-export-studies]", Here: false},
			{UUID: "ddd", Description: "[s5-export-backup]", Here: false},
		},
	}

	present := false
	var remotes []string
	for _, r := range append(w.Whereis, w.Untrusted...) {
		if r.Here {
			present = true
			continue
		}
		name := extractRemoteName(r.Description)
		if name != "" {
			remotes = append(remotes, name)
		}
	}
	if present {
		remotes = append([]string{"here"}, remotes...)
	}

	if present {
		t.Error("present should be false")
	}
	if len(remotes) != 2 {
		t.Fatalf("remotes count = %d, want 2; got %v", len(remotes), remotes)
	}
	if remotes[0] != "s5-export-studies" {
		t.Errorf("remotes[0] = %q, want %q", remotes[0], "s5-export-studies")
	}
	if remotes[1] != "s5-export-backup" {
		t.Errorf("remotes[1] = %q, want %q", remotes[1], "s5-export-backup")
	}
}

func TestAnnexEntryNotPresentOmitsPresent(t *testing.T) {
	entry := fileEntry{
		File:    "data.csv",
		Type:    "annex",
		Key:     "SHA256E-s100--abc.csv",
		Present: false,
		Remotes: []string{"s3-annex"},
	}
	data, _ := json.Marshal(entry)
	s := string(data)
	// present is omitempty and false is the zero value, so it should be omitted
	if strings.Contains(s, `"present"`) {
		t.Errorf("annex entry with present=false should omit 'present': %s", s)
	}
}
