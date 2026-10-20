package provenance

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	lib "github.com/Genentech/exohub/go/exo/internal/provenance"
)

// docsProvenancePath locates docs/provenance.md from this package's
// directory (go/exo/commands/provenance), four levels below the repo
// root.
func docsProvenancePath(t *testing.T) string {
	t.Helper()
	path := filepath.Join("..", "..", "..", "..", "docs", "provenance.md")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("docs/provenance.md not found at %s: %v", path, err)
	}
	return path
}

// jsonBlockPattern matches a fenced ```json code block — allowing the
// leading indentation markdown uses inside a numbered list item — that is
// not immediately preceded by an "<!-- excerpt -->" marker comment on its
// own line. Review (!259): a marker lets the doc show an intentionally
// incomplete fragment (for illustration) without this test trying to
// validate it as a standalone record.
var (
	jsonBlockPattern   = regexp.MustCompile("(?ms)^([ \t]*)```json[ \t]*\n(.*?)\n[ \t]*```[ \t]*$")
	excerptMarkerAbove = regexp.MustCompile(`<!--\s*excerpt\s*-->\s*$`)
)

// extractJSONExamples returns every complete (non-excerpt) ```json block
// in doc, each dedented to the fence's own indentation so it parses as
// top-level JSON.
func extractJSONExamples(doc string) []string {
	var out []string
	for _, m := range jsonBlockPattern.FindAllStringSubmatchIndex(doc, -1) {
		indent := doc[m[2]:m[3]]
		body := doc[m[4]:m[5]]
		if excerptMarkerAbove.MatchString(doc[:m[0]]) {
			continue // marked "<!-- excerpt -->": a deliberate fragment, not a complete record
		}
		out = append(out, dedent(body, indent))
	}
	return out
}

// dedent strips up to len(indent) leading whitespace characters from each
// line of s (markdown indents a fenced block to match its list item).
func dedent(s, indent string) string {
	if indent == "" {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimPrefix(line, indent)
	}
	return strings.Join(lines, "\n")
}

// TestDocExamples_AreCompleteRecords extracts every complete JSON example
// from docs/provenance.md and checks that it parses, matches the record
// JSON schema, and passes `exo provenance validate` in a fresh repository
// (review: !259 item 1 — a doc example that does not validate should fail
// CI, not ship silently broken). Validate warnings (for example, about
// referenced content that doesn't exist in the throwaway repo) are
// expected and do not fail the test; only errors do.
func TestDocExamples_AreCompleteRecords(t *testing.T) {
	data, err := os.ReadFile(docsProvenancePath(t))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	blocks := extractJSONExamples(string(data))
	if len(blocks) == 0 {
		t.Fatal("found no complete ```json examples in docs/provenance.md; did it lose its example records, or do they all need an <!-- excerpt --> marker?")
	}

	for i, block := range blocks {
		block := block
		t.Run(fmt.Sprintf("example-%d", i+1), func(t *testing.T) {
			rec, err := lib.Parse([]byte(block))
			if err != nil {
				t.Fatalf("lib.Parse() error = %v\nrecord:\n%s", err, block)
			}
			if msgs, err := lib.ValidateSchema([]byte(block)); err != nil {
				t.Fatalf("lib.ValidateSchema() error = %v", err)
			} else if len(msgs) > 0 {
				t.Fatalf("schema violations: %s\nrecord:\n%s", strings.Join(msgs, "; "), block)
			}

			root, run := addTestRepo(t, false)
			chdir(t, root)

			name, err := lib.RecordFileName(time.Now(), "doc-example", rec.ID)
			if err != nil {
				t.Fatalf("lib.RecordFileName(%q) error = %v", rec.ID, err)
			}
			writeFile(t, root, lib.RecordDir+"/"+name, block)
			run("git", "add", ".")
			run("git", "commit", "-q", "-m", "doc example")

			out, verr := runValidateCmd(t)
			if verr != nil {
				t.Fatalf("exo provenance validate rejected a docs/provenance.md example: %v\noutput:\n%s\nrecord:\n%s", verr, out, block)
			}
		})
	}
}
