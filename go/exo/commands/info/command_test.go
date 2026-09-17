package info

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestInfoArgs(t *testing.T) {
	cases := []struct {
		name   string
		remote string
		deep   bool
		want   []string
	}{
		{
			name: "default",
			want: []string{"git", "annex", "info", "-F", "--json"},
		},
		{
			name:   "remote",
			remote: "backup",
			want:   []string{"git", "annex", "info", "backup", "-F", "--json"},
		},
		{
			name: "json",
			want: []string{"git", "annex", "info", "-F", "--json"},
		},
		{
			name: "deep",
			deep: true,
			want: []string{"git", "annex", "info", "--json"},
		},
		{
			name:   "remote-deep-json",
			remote: "backup",
			deep:   true,
			want:   []string{"git", "annex", "info", "backup", "--json"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := infoArgs(tc.remote, tc.deep)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("infoArgs: got %#v want %#v", got, tc.want)
			}
		})
	}
}

func TestWriteFriendly(t *testing.T) {
	var buf bytes.Buffer
	payload := infoPayload{
		SemiTrusted: []repoEntry{
			{
				UUID:        "uuid-1",
				Description: "host:path [here]",
				Here:        true,
			},
		},
		Untrusted: []repoEntry{
			{
				UUID:        "uuid-2",
				Description: "[s5-export]",
			},
		},
	}
	writeFriendly(&buf, payload, nil, nil, "origin", true)
	got := buf.String()
	if !strings.HasPrefix(got, "Annex info for origin (deep)\n") {
		t.Fatalf("header: %q", got)
	}
	if !strings.Contains(got, "Configured remotes") {
		t.Fatalf("output missing: %q", got)
	}
}

func TestFormatInfoLines(t *testing.T) {
	out := formatInfoLines([]any{"a: 1", " ", 42, "b: 2"})
	if len(out) != 2 || out[0] != "a: 1" || out[1] != "b: 2" {
		t.Fatalf("formatInfoLines: %#v", out)
	}
	out = formatInfoLines([]string{"", "x: y"})
	if len(out) != 1 || out[0] != "x: y" {
		t.Fatalf("formatInfoLines strings: %#v", out)
	}
	if got := formatInfoLines("nope"); got != nil {
		t.Fatalf("formatInfoLines default: %#v", got)
	}
}

func TestRenderInfoLine(t *testing.T) {
	style := lipgloss.NewStyle()
	got := renderInfoLine("driver: s5cmd", style)
	if got != "driver: s5cmd" {
		t.Fatalf("renderInfoLine: %q", got)
	}
	got = renderInfoLine("no-colon", style)
	if got != "no-colon" {
		t.Fatalf("renderInfoLine no-colon: %q", got)
	}
	got = renderInfoLine(": missing", style)
	if got != ": missing" {
		t.Fatalf("renderInfoLine missing key: %q", got)
	}
}

func TestParseInfoPayloadRemoteInfo(t *testing.T) {
	input := strings.Join([]string{
		`{"info":"driver: s5cmd"}`,
		`{"info":"availability: global"}`,
		`{"trusted repositories":[],"semitrusted repositories":[],"untrusted repositories":[]}`,
	}, "\n")
	_, _, details, err := parseInfoPayload(input)
	if err != nil {
		t.Fatalf("parseInfoPayload: %v", err)
	}
	raw, ok := details["remote_info"]
	if !ok {
		t.Fatalf("missing remote_info")
	}
	lines := formatInfoLines(raw)
	if len(lines) != 2 || lines[0] != "driver: s5cmd" || lines[1] != "availability: global" {
		t.Fatalf("remote_info lines: %#v", lines)
	}
}

func TestLoadExohubRemotes(t *testing.T) {
	// Test when .exohub/remotes doesn't exist
	remotes := loadExohubRemotes()
	if remotes != nil {
		t.Fatalf("expected nil for non-existent .exohub/remotes, got %v", remotes)
	}
}

func TestDisplayAnnexConfigText(t *testing.T) {
	var buf bytes.Buffer

	// Test unlocked + no thin + gitattributes
	displayAnnexConfigText(&buf, annexConfig{
		addUnlocked:    true,
		thin:           false,
		hasGitattributes: true,
	})
	out := buf.String()
	if !strings.Contains(out, "Unlocked:") {
		t.Errorf("expected Unlocked line, got: %s", out)
	}
	if !strings.Contains(out, "Routing:") {
		t.Errorf("expected Routing line when gitattributes active, got: %s", out)
	}

	// Test locked + thin + no gitattributes
	buf.Reset()
	displayAnnexConfigText(&buf, annexConfig{
		addUnlocked: false,
		thin:        true,
	})
	out = buf.String()
	if !strings.Contains(out, "Unlocked: no") {
		t.Errorf("expected 'Unlocked: no', got: %s", out)
	}
	if !strings.Contains(out, "Thin:     yes") {
		t.Errorf("expected 'Thin:     yes', got: %s", out)
	}
	if strings.Contains(out, "Routing:") {
		t.Errorf("should not show routing when largefiles is empty, got: %s", out)
	}
}
