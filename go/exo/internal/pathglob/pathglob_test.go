package pathglob

import "testing"

func TestMatch(t *testing.T) {
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
			ok, err := Match(tt.pattern, tt.path)
			if err != nil {
				t.Fatalf("Match() error = %v", err)
			}
			if ok != tt.wantMatch {
				t.Errorf("Match(%q, %q) = %v, want %v", tt.pattern, tt.path, ok, tt.wantMatch)
			}
		})
	}
}

func TestValid(t *testing.T) {
	if !Valid("**/*.csv") {
		t.Errorf("Valid(%q) = false, want true", "**/*.csv")
	}
	if Valid("[") {
		t.Errorf("Valid(%q) = true, want false", "[")
	}
}
