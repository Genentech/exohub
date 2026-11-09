package tui

import (
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// fakeClient is a minimal Client for driving the model in tests.
type fakeClient struct {
	documents map[string]map[string]interface{}
}

func (f *fakeClient) Search(qs string) (chan SearchResponse, chan error) {
	results := make(chan SearchResponse)
	errs := make(chan error)
	close(results)
	close(errs)
	return results, errs
}

func (f *fakeClient) Scroll(scrollID string) (chan SearchResponse, chan error) {
	return f.Search("")
}

func (f *fakeClient) GetFileMetadata(fileID string, tenant string, raw bool, followLink bool) (map[string]interface{}, error) {
	return f.documents[fileID], nil
}

func (f *fakeClient) GetSchemas() []string { return nil }
func (f *fakeClient) ClearSchemaCache()    {}
func (f *fakeClient) GetProjectVersions(projectID string) ([]string, string, error) {
	return nil, "", nil
}

// fakeContextProvider is a minimal ContextProvider for driving the model in tests.
type fakeContextProvider struct{}

func (f *fakeContextProvider) GetContext() (*Context, error) { return &Context{}, nil }
func (f *fakeContextProvider) GetContextOrDie() *Context     { return &Context{} }
func (f *fakeContextProvider) LoadSearchParams() (PersistSearchParams, error) {
	return PersistSearchParams{}, nil
}
func (f *fakeContextProvider) SaveSearchParams(params PersistSearchParams) error { return nil }
func (f *fakeContextProvider) MakeRequest(method string, url string, payload interface{}, headers map[string]string) *ResponseWrapper {
	return nil
}

// fakeUIConfigProvider is a minimal UIConfigProvider for driving the model
// in tests. configFile points under a test temp dir so history/config side
// effects never land in the repo.
type fakeUIConfigProvider struct {
	configFile string
}

func (f *fakeUIConfigProvider) InitConfig()               {}
func (f *fakeUIConfigProvider) GetMainConfigFile() string { return f.configFile }

// drain executes cmd (and recursively any tea.BatchMsg it produces) and
// returns every plain tea.Msg it yields, in order.
func drain(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if msg == nil {
		return nil
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, drain(c)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

// findMsg returns the first message of type T in msgs.
func findMsg[T any](msgs []tea.Msg) (T, bool) {
	for _, m := range msgs {
		if v, ok := m.(T); ok {
			return v, true
		}
	}
	var zero T
	return zero, false
}

func TestModelUsedByKeyTriggersSearch(t *testing.T) {
	SetProviders(&fakeClient{}, &fakeContextProvider{}, &fakeUIConfigProvider{configFile: filepath.Join(t.TempDir(), "config.yaml")})

	m := newModel(Config{}).(model)

	origin := artifactDoc("SHA256E", "SHA256E-s1--abc", "fastq/SRR6357070_1.fastq.gz")
	origin.Result.Metadata["lineage"] = map[string]interface{}{
		"collection": map[string]interface{}{"id": "urn:exohub:git:tree:deadbeef"},
	}
	m.pager.currentDocument = *origin
	m.state = stateShowDocument

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'u'}})
	m = updated.(model)

	msg, ok := findMsg[usedBySearchMsg](drain(cmd))
	if !ok {
		t.Fatalf("expected 'u' to produce a usedBySearchMsg")
	}

	wantQuery := `(lineage.inputs.id:"urn:exohub:annex:SHA256E-s1--abc" OR lineage.inputs.id:"urn:exohub:git:tree:deadbeef")`
	if msg.query != wantQuery {
		t.Errorf("usedBySearchMsg.query = %q, want %q", msg.query, wantQuery)
	}
	wantTitle := "Used by fastq/SRR6357070_1.fastq.gz"
	if msg.title != wantTitle {
		t.Errorf("usedBySearchMsg.title = %q, want %q", msg.title, wantTitle)
	}
	if msg.originID != origin.ArfifactDBID {
		t.Errorf("usedBySearchMsg.originID = %q, want %q", msg.originID, origin.ArfifactDBID)
	}

	updated, _ = m.Update(msg)
	m = updated.(model)

	if m.state != stateShowStash {
		t.Errorf("state after usedBySearchMsg = %v, want stateShowStash", m.state)
	}
	if got := m.stash.filterInput.Value(); got != wantQuery {
		t.Errorf("stash filter query = %q, want %q", got, wantQuery)
	}
	if m.stash.resultsTitle != wantTitle {
		t.Errorf("stash.resultsTitle = %q, want %q", m.stash.resultsTitle, wantTitle)
	}
	if m.stash.usedByReturnID != origin.ArfifactDBID {
		t.Errorf("stash.usedByReturnID = %q, want %q", m.stash.usedByReturnID, origin.ArfifactDBID)
	}
}

func TestModelUsedByEscReturnsToOriginDocument(t *testing.T) {
	originID := "proj:fastq/SRR6357070_1.fastq.gz@main"
	client := &fakeClient{documents: map[string]map[string]interface{}{
		originID: {
			"_extra":  map[string]interface{}{"id": originID, "$schema": "exohub-artifact/v1.json"},
			"backend": "SHA256E",
			"key":     "SHA256E-s1--abc",
			"path":    "fastq/SRR6357070_1.fastq.gz",
		},
	}}
	SetProviders(client, &fakeContextProvider{}, &fakeUIConfigProvider{configFile: filepath.Join(t.TempDir(), "config.yaml")})

	m := newModel(Config{}).(model)
	m.state = stateShowStash
	m.stash.usedByReturnID = originID
	m.stash.resultsTitle = "Used by fastq/SRR6357070_1.fastq.gz"
	m.stash.filterState = filterApplied

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(model)

	if m.stash.usedByReturnID != "" {
		t.Errorf("stash.usedByReturnID after esc = %q, want empty", m.stash.usedByReturnID)
	}
	if m.stash.resultsTitle != "" {
		t.Errorf("stash.resultsTitle after esc = %q, want empty", m.stash.resultsTitle)
	}

	msgs := drain(cmd)
	fetched, ok := findMsg[fetchedMarkdownMsg](msgs)
	if !ok {
		t.Fatalf("expected esc to reload the origin document, got msgs: %#v", msgs)
	}
	if fetched.ArfifactDBID != originID {
		t.Errorf("reloaded document id = %q, want %q", fetched.ArfifactDBID, originID)
	}

	updated, cmd = m.Update(fetched)
	m = updated.(model)

	rendered, ok := findMsg[contentRenderedMsg](drain(cmd))
	if !ok {
		t.Fatalf("expected fetchedMarkdownMsg to lead to a rendered document")
	}

	updated, _ = m.Update(rendered)
	m = updated.(model)

	if m.state != stateShowDocument {
		t.Errorf("state after esc round-trip = %v, want stateShowDocument", m.state)
	}
	if m.pager.currentDocument.ArfifactDBID != originID {
		t.Errorf("pager.currentDocument.ArfifactDBID = %q, want %q", m.pager.currentDocument.ArfifactDBID, originID)
	}
}
