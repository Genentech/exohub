package init

import (
	"testing"
)

func TestExtractRepoNameFromURL(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		org      string
		expected string
	}{
		{
			name:     "full GitHub URL",
			input:    "https://github.com/lelongs_roche/test-DS000000021",
			org:      "lelongs_roche",
			expected: "test-DS000000021",
		},
		{
			name:     "full GitHub URL without https",
			input:    "github.com/lelongs_roche/test-DS000000021",
			org:      "lelongs_roche",
			expected: "test-DS000000021",
		},
		{
			name:     "org/repo format",
			input:    "lelongs_roche/test-DS000000021",
			org:      "lelongs_roche",
			expected: "test-DS000000021",
		},
		{
			name:     "just repo name",
			input:    "test-DS000000021",
			org:      "lelongs_roche",
			expected: "test-DS000000021",
		},
		{
			name:     "HTTP URL",
			input:    "http://github.com/testorg/myrepo",
			org:      "testorg",
			expected: "myrepo",
		},
		{
			name:     "custom host URL",
			input:    "https://test.example.com/myorg/my-repo",
			org:      "myorg",
			expected: "my-repo",
		},
		{
			name:     "trailing slash",
			input:    "https://github.com/org/repo/",
			org:      "org",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractRepoNameFromURL(tt.input, tt.org)
			if result != tt.expected {
				t.Errorf("extractRepoNameFromURL(%q, %q) = %q, expected %q", tt.input, tt.org, result, tt.expected)
			}
		})
	}
}
