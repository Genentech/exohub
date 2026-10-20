package mcp

import (
	"context"
	"testing"

	gomcp "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// fakeClientSession is a minimal server.SessionWithClientInfo for tests.
type fakeClientSession struct {
	info gomcp.Implementation
}

func (f fakeClientSession) SessionID() string                                     { return "test-session" }
func (f fakeClientSession) NotificationChannel() chan<- gomcp.JSONRPCNotification { return nil }
func (f fakeClientSession) Initialize()                                           {}
func (f fakeClientSession) Initialized() bool                                     { return true }
func (f fakeClientSession) GetClientInfo() gomcp.Implementation                   { return f.info }
func (f fakeClientSession) SetClientInfo(gomcp.Implementation)                    {}
func (f fakeClientSession) GetClientCapabilities() gomcp.ClientCapabilities {
	return gomcp.ClientCapabilities{}
}
func (f fakeClientSession) SetClientCapabilities(gomcp.ClientCapabilities) {}

var _ server.SessionWithClientInfo = fakeClientSession{}

func contextWithClientName(name string) context.Context {
	s := server.NewMCPServer("test", "0.0.0")
	return s.WithContext(context.Background(), fakeClientSession{info: gomcp.Implementation{Name: name}})
}

func TestDefaultSoftwareAgent(t *testing.T) {
	if got := defaultSoftwareAgent(context.Background()); got != "" {
		t.Errorf("defaultSoftwareAgent(no session) = %q, want \"\"", got)
	}
	for _, tc := range []struct {
		clientName string
		want       string
	}{
		{"claude-code", "software:claude-code"},
		{"Claude Code", "software:claude-code"},
		{"Visual Studio Code", "software:visual-studio-code"},
		{"Claude Desktop", "software:claude-desktop"},
		{"My/Agent", "software:my-agent"},
	} {
		ctx := contextWithClientName(tc.clientName)
		if got := defaultSoftwareAgent(ctx); got != tc.want {
			t.Errorf("defaultSoftwareAgent(client=%q) = %q, want %q", tc.clientName, got, tc.want)
		}
	}
}

func TestNormalizeAgentName(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
	}{
		{"claude-code", "claude-code"},
		{"Claude Code", "claude-code"},
		{"Visual Studio Code", "visual-studio-code"},
		{"  spaced  ", "spaced"},
		{"a/b\\c", "a-b-c"},
		{"", ""},
		{"   ", ""},
		{"---", ""},
	} {
		if got := normalizeAgentName(tc.in); got != tc.want {
			t.Errorf("normalizeAgentName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestHandleProvenanceAddRequiresRepoDir(t *testing.T) {
	result, err := handleProvenanceAdd(context.Background(), mustRequest(map[string]any{
		"generated": []any{"out.csv"},
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected error result, got %+v", result)
	}
}

func TestHandleProvenanceAddRequiresGenerated(t *testing.T) {
	dir := fakeGitRepo(t)
	result, err := handleProvenanceAdd(context.Background(), mustRequest(map[string]any{"repo_dir": dir}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected error result for missing generated, got %+v", result)
	}
}

func TestHandleProvenanceAddDefaultsAgentFromClientInfo(t *testing.T) {
	dir := fakeGitRepo(t)
	calls := withFakeCommand(t, `echo '{"path":"out.csv"}'`)

	ctx := contextWithClientName("my-agent")
	result, err := handleProvenanceAdd(ctx, mustRequest(map[string]any{
		"repo_dir":  dir,
		"generated": []any{"out.csv"},
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %+v", result)
	}

	call := lastExoCall(t, calls)
	if !argPairPresent(call, "--agent", "software:my-agent") {
		t.Errorf("exo provenance add args = %v, missing --agent software:my-agent", call)
	}
	// on_behalf_of is intentionally left to exo provenance add's own default
	// (the logged-in user) when not given explicitly.
	if argPresent(call, "--on-behalf-of") {
		t.Errorf("exo provenance add args = %v, should not set --on-behalf-of when not given", call)
	}
}

func TestHandleProvenanceAddExplicitAgentOverridesClientInfo(t *testing.T) {
	dir := fakeGitRepo(t)
	calls := withFakeCommand(t, `echo '{"path":"out.csv"}'`)

	ctx := contextWithClientName("my-agent")
	_, err := handleProvenanceAdd(ctx, mustRequest(map[string]any{
		"repo_dir":     dir,
		"generated":    []any{"out.csv"},
		"agent":        "person:jdoe",
		"on_behalf_of": "",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	call := lastExoCall(t, calls)
	if !argPairPresent(call, "--agent", "person:jdoe") {
		t.Errorf("exo provenance add args = %v, missing explicit --agent person:jdoe", call)
	}
}

func TestHandleProvenanceAddFullArgs(t *testing.T) {
	dir := fakeGitRepo(t)
	calls := withFakeCommand(t, `echo '{"path":"out.csv"}'`)

	_, err := handleProvenanceAdd(context.Background(), mustRequest(map[string]any{
		"repo_dir":        dir,
		"generated":       []any{"out.csv"},
		"used":            []any{"ref.csv"},
		"derived_from":    []any{"in.csv"},
		"revision_of":     []any{"old.csv"},
		"plan":            "pipeline.py",
		"model":           "gpt-5",
		"agent":           "software:runner",
		"on_behalf_of":    "person:jdoe",
		"started":         "2024-01-01T00:00:00Z",
		"ended":           "2024-01-01T01:00:00Z",
		"assertion":       "asserted",
		"evidence":        "manual review",
		"supersedes":      []any{"rec-1"},
		"skip_existing":   true,
		"dry_run":         true,
		"no_derived_from": false,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	call := lastExoCall(t, calls)
	for _, pair := range [][2]string{
		{"--generated", "out.csv"},
		{"--used", "ref.csv"},
		{"--derived-from", "in.csv"},
		{"--revision-of", "old.csv"},
		{"--plan", "pipeline.py"},
		{"--model", "gpt-5"},
		{"--agent", "software:runner"},
		{"--on-behalf-of", "person:jdoe"},
		{"--started", "2024-01-01T00:00:00Z"},
		{"--ended", "2024-01-01T01:00:00Z"},
		{"--assertion", "asserted"},
		{"--evidence", "manual review"},
		{"--supersedes", "rec-1"},
	} {
		if !argPairPresent(call, pair[0], pair[1]) {
			t.Errorf("exo provenance add args = %v, missing %s %s", call, pair[0], pair[1])
		}
	}
	for _, flag := range []string{"--skip-existing", "--dry-run"} {
		if !argPresent(call, flag) {
			t.Errorf("exo provenance add args = %v, missing %s", call, flag)
		}
	}
	if argPresent(call, "--no-derived-from") {
		t.Errorf("exo provenance add args = %v, should not set --no-derived-from when false", call)
	}
}

func argPresent(call []string, flag string) bool {
	for _, a := range call {
		if a == flag {
			return true
		}
	}
	return false
}

func argPairPresent(call []string, flag, value string) bool {
	for i := 0; i+1 < len(call); i++ {
		if call[i] == flag && call[i+1] == value {
			return true
		}
	}
	return false
}
