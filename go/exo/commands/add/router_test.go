package add

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	initcmd "github.com/Genentech/exohub/go/exo/commands/init"
)

// chdirTemp creates a temp dir, chdirs into it, and returns a cleanup func
// that restores the original working directory.
func chdirTemp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}
	t.Cleanup(func() {
		os.Chdir(orig)
	})
	return dir
}

// writeFile creates rel (and its parent directories) under the current
// directory with the given content, and returns its os.FileInfo.
func writeFile(t *testing.T, rel string, content []byte) os.FileInfo {
	t.Helper()
	if dir := filepath.Dir(rel); dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("MkdirAll(%s) error = %v", dir, err)
		}
	}
	if err := os.WriteFile(rel, content, 0644); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", rel, err)
	}
	info, err := os.Stat(rel)
	if err != nil {
		t.Fatalf("Stat(%s) error = %v", rel, err)
	}
	return info
}

func newTestRouter(t *testing.T, rules []initcmd.RoutingRule) *Router {
	t.Helper()
	// root="" means Route opens relPath as-is: fine for these tests, since
	// chdirTemp makes the current directory the "repo root" too. See
	// TestRouterRepoRootResolvesContentWhenCWDIsASubdirectory for the case
	// where root matters.
	cfg := &initcmd.ExohubConfig{Annex: &initcmd.AnnexConfig{Routing: rules}}
	router, err := NewRouter(cfg, "", "")
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}
	return router
}

func TestRouterProtectedRulesWin(t *testing.T) {
	chdirTemp(t)
	// A catch-all user rule that would otherwise annex everything.
	router := newTestRouter(t, []initcmd.RoutingRule{
		{LargerThan: "0", To: "annex"},
	})

	for _, rel := range []string{".exohub/config", ".exohub/nested/file", ".gitignore", ".gitattributes", "sub/.gitignore"} {
		info := writeFile(t, rel, []byte("hello"))
		dec, err := router.Route(rel, info)
		if err != nil {
			t.Fatalf("Route(%s) error = %v", rel, err)
		}
		if dec.Backend != Git {
			t.Errorf("Route(%s) = %v, want Git (protected)", rel, dec.Backend)
		}
	}
}

func TestRouterFirstMatchWins(t *testing.T) {
	chdirTemp(t)
	router := newTestRouter(t, []initcmd.RoutingRule{
		{Path: "**/*.json", To: "git"},
		{LargerThan: "1MB", To: "annex"},
	})

	content := bytes.Repeat([]byte("x"), 5*1024*1024)
	info := writeFile(t, "data/big.json", content)

	dec, err := router.Route("data/big.json", info)
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	if dec.Backend != Git {
		t.Errorf("Backend = %v, want Git (first rule wins)", dec.Backend)
	}
	if dec.Rule.Index != 1 {
		t.Errorf("Rule.Index = %d, want 1", dec.Rule.Index)
	}
}

func TestRouterUserRuleOverridesDefault(t *testing.T) {
	chdirTemp(t)
	router := newTestRouter(t, []initcmd.RoutingRule{
		{Path: "**/*.csv", LargerThan: "1KB", To: "annex"},
	})

	info := writeFile(t, "data/a.csv", bytes.Repeat([]byte("a"), 2000))
	dec, err := router.Route("data/a.csv", info)
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	if dec.Backend != Annex {
		t.Errorf("Backend = %v, want Annex", dec.Backend)
	}
	if dec.Rule.Index != 1 {
		t.Errorf("Rule.Index = %d, want 1", dec.Rule.Index)
	}
}

func TestRouterGlobSemantics(t *testing.T) {
	chdirTemp(t)

	tests := []struct {
		name      string
		pattern   string
		path      string
		wantMatch bool
	}{
		{"basename match without slash, nested", "README.md", "docs/README.md", true},
		{"basename match without slash, root", "README.md", "README.md", true},
		{"basename mismatch without slash", "README.md", "docs/OTHER.md", false},
		{"doublestar with slash matches nested", "**/*.csv", "data/sub/a.csv", true},
		{"doublestar with slash matches root", "**/*.csv", "a.csv", true},
		{"slash-anchored pattern requires exact dir", "data/*.csv", "data/a.csv", true},
		{"slash-anchored pattern rejects other dir", "data/*.csv", "other/a.csv", false},
		{"slash-free pattern still basename-matches any depth", "*.md", "docs/README.md", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, err := matchGlob(tt.pattern, tt.path)
			if err != nil {
				t.Fatalf("matchGlob() error = %v", err)
			}
			if ok != tt.wantMatch {
				t.Errorf("matchGlob(%q, %q) = %v, want %v", tt.pattern, tt.path, ok, tt.wantMatch)
			}
		})
	}
}

func TestRouterSizeBoundariesAndUnits(t *testing.T) {
	chdirTemp(t)

	// larger_than is inclusive (>=).
	router := newTestRouter(t, []initcmd.RoutingRule{
		{LargerThan: "1KB", To: "annex"},
	})
	exact := writeFile(t, "exact.bin", bytes.Repeat([]byte{0}, 1000)) // 1KB = 1000 bytes
	dec, err := router.Route("exact.bin", exact)
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	if dec.Backend != Annex {
		t.Errorf("larger_than boundary: Backend = %v, want Annex (inclusive)", dec.Backend)
	}

	// Text content, well below the fallback threshold too, so the
	// unmatched-rule case falls through to the default "text" branch.
	below := writeFile(t, "below.txt", bytes.Repeat([]byte("a"), 999))
	dec, err = router.Route("below.txt", below)
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	if dec.Backend != Git {
		t.Errorf("below larger_than: Backend = %v, want Git", dec.Backend)
	}

	// smaller_than is exclusive (<).
	router2 := newTestRouter(t, []initcmd.RoutingRule{
		{SmallerThan: "1KiB", To: "git"},
	})
	atLimit := writeFile(t, "atlimit.bin", bytes.Repeat([]byte{0}, 1024)) // 1KiB = 1024 bytes
	dec, err = router2.Route("atlimit.bin", atLimit)
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	if dec.Backend != Annex {
		t.Errorf("smaller_than boundary: Backend = %v, want Annex (fallback, exclusive)", dec.Backend)
	}

	// KB (1000) vs KiB (1024) must differ.
	router3 := newTestRouter(t, []initcmd.RoutingRule{
		{LargerThan: "1KB", SmallerThan: "1KiB", To: "annex"},
	})
	between := writeFile(t, "between.bin", bytes.Repeat([]byte{0}, 1010))
	dec, err = router3.Route("between.bin", between)
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	if dec.Backend != Annex {
		t.Errorf("KB/KiB range: Backend = %v, want Annex", dec.Backend)
	}
}

func TestRouterContentSniffIsLazy(t *testing.T) {
	chdirTemp(t)

	// A rule that only checks path/size must never need to read the file.
	router := newTestRouter(t, []initcmd.RoutingRule{
		{Path: "**/*.bin", To: "annex"},
	})
	info := writeFile(t, "data.bin", []byte("whatever"))
	if err := os.Remove("data.bin"); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}

	dec, err := router.Route("data.bin", info)
	if err != nil {
		t.Fatalf("Route() unexpected error (content should not have been sniffed): %v", err)
	}
	if dec.Backend != Annex {
		t.Errorf("Backend = %v, want Annex", dec.Backend)
	}

	// A rule that does need content on a now-missing file must error.
	contentRouter := newTestRouter(t, []initcmd.RoutingRule{
		{Content: "text", To: "git"},
	})
	if _, err := contentRouter.Route("data.bin", info); err == nil {
		t.Error("Route() expected error when content sniff is required on a missing file")
	}
}

func TestRouterDefaultFallbackMatchesTodaysBehaviour(t *testing.T) {
	chdirTemp(t)
	router := newTestRouter(t, nil)

	textInfo := writeFile(t, "notes.txt", []byte("plain text"))
	dec, err := router.Route("notes.txt", textInfo)
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	if dec.Backend != Git {
		t.Errorf("text file: Backend = %v, want Git", dec.Backend)
	}

	binaryInfo := writeFile(t, "image.png", []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0, 1, 2, 3})
	dec, err = router.Route("image.png", binaryInfo)
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	if dec.Backend != Annex {
		t.Errorf("binary file: Backend = %v, want Annex", dec.Backend)
	}

	largeInfo := writeFile(t, "large.txt", bytes.Repeat([]byte("x"), largeTextFileThreshold+1))
	dec, err = router.Route("large.txt", largeInfo)
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	if dec.Backend != Annex {
		t.Errorf("large text file: Backend = %v, want Annex", dec.Backend)
	}
}

func TestResolveThresholdPrecedence(t *testing.T) {
	t.Run("flag wins over env and default", func(t *testing.T) {
		t.Setenv("EXOHUB_LARGE_TEXT_THRESHOLD", "1MB")
		v, err := resolveThreshold("100KB")
		if err != nil {
			t.Fatalf("resolveThreshold() error = %v", err)
		}
		if v != 100*1000 {
			t.Errorf("threshold = %d, want %d", v, 100*1000)
		}
	})

	t.Run("env wins over default", func(t *testing.T) {
		t.Setenv("EXOHUB_LARGE_TEXT_THRESHOLD", "2MiB")
		v, err := resolveThreshold("")
		if err != nil {
			t.Fatalf("resolveThreshold() error = %v", err)
		}
		if v != 2*1024*1024 {
			t.Errorf("threshold = %d, want %d", v, 2*1024*1024)
		}
	})

	t.Run("default when neither set", func(t *testing.T) {
		t.Setenv("EXOHUB_LARGE_TEXT_THRESHOLD", "")
		v, err := resolveThreshold("")
		if err != nil {
			t.Fatalf("resolveThreshold() error = %v", err)
		}
		if v != uint64(largeTextFileThreshold) {
			t.Errorf("threshold = %d, want %d", v, largeTextFileThreshold)
		}
	})

	t.Run("invalid flag value errors", func(t *testing.T) {
		if _, err := resolveThreshold("lots"); err == nil {
			t.Error("resolveThreshold() expected error for invalid flag value")
		}
	})

	t.Run("invalid env value errors", func(t *testing.T) {
		t.Setenv("EXOHUB_LARGE_TEXT_THRESHOLD", "lots")
		if _, err := resolveThreshold(""); err == nil {
			t.Error("resolveThreshold() expected error for invalid env value")
		}
	})
}

func TestRouterEnvOverrideAffectsOnlyFallback(t *testing.T) {
	chdirTemp(t)
	t.Setenv("EXOHUB_LARGE_TEXT_THRESHOLD", "1KB")

	// An explicit rule must not be affected by the lowered fallback threshold.
	router := newTestRouter(t, []initcmd.RoutingRule{
		{Path: "**/*.md", To: "git"},
	})
	info := writeFile(t, "notes.md", bytes.Repeat([]byte("x"), 10*1000)) // 10KB, well above 1KB
	dec, err := router.Route("notes.md", info)
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	if dec.Backend != Git {
		t.Errorf("Backend = %v, want Git (explicit rule, env override must not apply)", dec.Backend)
	}

	// The fallback, with no matching rule, must honor the lowered threshold.
	fallbackRouter := newTestRouter(t, nil)
	textInfo := writeFile(t, "plain.txt", bytes.Repeat([]byte("x"), 2000)) // 2KB, above 1KB
	dec, err = fallbackRouter.Route("plain.txt", textInfo)
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	if dec.Backend != Annex {
		t.Errorf("Backend = %v, want Annex (fallback honors lowered threshold)", dec.Backend)
	}
}

func TestNewRouterInvalidConfigErrors(t *testing.T) {
	cfg := &initcmd.ExohubConfig{
		Annex: &initcmd.AnnexConfig{
			Routing: []initcmd.RoutingRule{
				{Path: "**/*.md", To: "bogus"},
			},
		},
	}
	if _, err := NewRouter(cfg, "", ""); err == nil {
		t.Error("NewRouter() expected error for invalid rule")
	}
}

// TestRouterRepoRootResolvesContentWhenCWDIsASubdirectory ensures Route's
// relPath (repo-relative, used for glob matching per design D2) and the
// path used to open the file for a content sniff are correctly
// disentangled: exo add enumerates files without --full-name, so from a
// subdirectory the paths it works with are cwd-relative, not
// repo-relative. The Router must be told the repo root so it can resolve
// a repo-relative relPath to a real filesystem path regardless of cwd.
func TestRouterRepoRootResolvesContentWhenCWDIsASubdirectory(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	full := filepath.Join(sub, "data.bin")
	// Binary content, so the content-sniffing rule below actually depends
	// on reading the right file.
	if err := os.WriteFile(full, []byte{0, 1, 2, 3, 0xFF}, 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	info, err := os.Stat(full)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}

	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(sub); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}
	t.Cleanup(func() { os.Chdir(orig) })

	cfg := &initcmd.ExohubConfig{Annex: &initcmd.AnnexConfig{Routing: []initcmd.RoutingRule{
		{Content: "binary", To: "annex"},
	}}}
	router, err := NewRouter(cfg, "", root)
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	// relPath is repo-relative ("sub/data.bin"), even though cwd is the
	// "sub" directory itself. Without root-aware resolution this would
	// try to open "sub/sub/data.bin" relative to cwd and fail.
	dec, err := router.Route("sub/data.bin", info)
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	if dec.Backend != Annex {
		t.Errorf("Backend = %v, want Annex (content rule matched via repo-root-resolved path)", dec.Backend)
	}
	if dec.Rule.Kind != KindUser || dec.Rule.Index != 1 {
		t.Errorf("Rule = %+v, want KindUser rule 1", dec.Rule)
	}
}

// TestRouterGoldenNoRulesMatchesTodaysClassification is the golden fixture
// proving that, with no annex.routing configured, the Router's decisions
// are identical to classifySingleFile's today, across all three of its
// buckets (git, annex, large-text) rather than collapsing the latter two
// into one. The large-text bucket matters on its own because design D3/
// task 2.3 keep the TTY prompt only for that specific case
// (Decision.Rule.LargeTextFallback), so a regression that quietly moved a
// file between "binary" and "large text" would otherwise go unnoticed.
func TestRouterGoldenNoRulesMatchesTodaysClassification(t *testing.T) {
	chdirTemp(t)

	files := map[string][]byte{
		"readme.md":      []byte("# hello\nplain text file\n"),
		"data/a.csv":     []byte("a,b,c\n1,2,3\n"),
		"empty.txt":      {},
		"image.png":      {0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0, 1, 2, 3, 4, 5},
		"data/large.txt": bytes.Repeat([]byte("x"), largeTextFileThreshold+1),
		// Exactly at the threshold (inclusive boundary) vs. one byte under.
		"at_threshold.txt":    bytes.Repeat([]byte("x"), largeTextFileThreshold),
		"below_threshold.txt": bytes.Repeat([]byte("x"), largeTextFileThreshold-1),
		// Proves removing the dead application/json/application/xml
		// exemption in classifySingleFile changed nothing: both are
		// sniffed as text/plain by http.DetectContentType and were never
		// actually hitting that branch.
		"data.json": []byte(`{"a": 1, "b": [1, 2, 3]}`),
		"data.xml":  []byte(`<?xml version="1.0"?><root><a>1</a></root>`),
		// A binary file nested in a subdirectory.
		"nested/dir/blob.bin": {0x00, 0x01, 0x02, 0xFF, 0xFE, 0x00, 0x00, 0x00},
	}

	router := newTestRouter(t, nil)

	for rel, content := range files {
		info := writeFile(t, rel, content)

		var gitFiles, annexFiles, largeTextFiles []string
		classifySingleFile(rel, info, &gitFiles, &annexFiles, &largeTextFiles)

		var wantBackend Backend
		var wantLargeTextFallback bool
		switch {
		case len(gitFiles) == 1:
			wantBackend = Git
		case len(annexFiles) == 1:
			wantBackend = Annex
		case len(largeTextFiles) == 1:
			wantBackend = Annex
			wantLargeTextFallback = true
		default:
			t.Fatalf("classifySingleFile did not classify %s", rel)
		}

		dec, err := router.Route(rel, info)
		if err != nil {
			t.Fatalf("Route(%s) error = %v", rel, err)
		}
		if dec.Backend != wantBackend {
			t.Errorf("Route(%s).Backend = %v, want %v (today's classification)", rel, dec.Backend, wantBackend)
		}
		if dec.Rule.LargeTextFallback != wantLargeTextFallback {
			t.Errorf("Route(%s).Rule.LargeTextFallback = %v, want %v", rel, dec.Rule.LargeTextFallback, wantLargeTextFallback)
		}
	}
}
