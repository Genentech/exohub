package importer

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	lib "github.com/Genentech/exohub/go/exo/internal/provenance"
)

// importerTestRepo creates a temp git repository and returns its root plus
// a helper to run commands in it.
func importerTestRepo(t *testing.T) (root string, run func(args ...string) string) {
	t.Helper()
	root = t.TempDir()
	run = func(args ...string) string {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+root)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("command %v failed: %v\n%s", args, err, out)
		}
		return string(out)
	}
	run("git", "init", "-q")
	run("git", "config", "user.email", "test@test.com")
	run("git", "config", "user.name", "Test")
	return root, run
}

func writeTestFile(t *testing.T, root, relPath, content string) {
	t.Helper()
	full := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func gitBlobID(t *testing.T, run func(args ...string) string, path string) string {
	t.Helper()
	out := run("git", "ls-files", "--stage", "--", path)
	fields := splitFields(out)
	if len(fields) < 2 {
		t.Fatalf("unexpected ls-files output for %s: %q", path, out)
	}
	return fields[1]
}

func splitFields(s string) []string {
	var fields []string
	cur := ""
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' {
			if cur != "" {
				fields = append(fields, cur)
				cur = ""
			}
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		fields = append(fields, cur)
	}
	return fields
}

func byID(graph []lib.Node) map[string]lib.Node {
	out := map[string]lib.Node{}
	for _, n := range graph {
		out[n.ID()] = n
	}
	return out
}
