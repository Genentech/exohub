package serve

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Genentech/exohub/go/exo/internal/defaults"
)

func newTestServer() *Server {
	store := NewSessionStore()
	return &Server{
		store:    store,
		staticFS: nil,
	}
}

func TestHandleStatic_InstallCmdPlaceholderReplaced(t *testing.T) {
	s := newTestServer()
	defer s.store.Stop()

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	s.handleStatic(w, req)

	body := w.Body.String()
	if strings.Contains(body, "__EXOHUB_INSTALL_CMD__") {
		t.Errorf("handleStatic() output still contains raw placeholder __EXOHUB_INSTALL_CMD__")
	}
	wantJSON, _ := json.Marshal(defaults.InstallCmd())
	if !strings.Contains(body, string(wantJSON)) {
		t.Errorf("handleStatic() output does not contain the expected install command %s", wantJSON)
	}
}

func TestHandleStatic_InstallCmdEnvOverrideIsJSONEscaped(t *testing.T) {
	t.Setenv("EXOHUB_INSTALL_CMD", `curl 'it'"'"'s' | sh </script>`)

	s := newTestServer()
	defer s.store.Stop()

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	s.handleStatic(w, req)

	body := w.Body.String()
	if strings.Contains(body, "__EXOHUB_INSTALL_CMD__") {
		t.Errorf("handleStatic() output still contains raw placeholder __EXOHUB_INSTALL_CMD__")
	}
	if strings.Contains(body, "sh </script>") {
		t.Errorf("handleStatic() output contains the injected value with an unescaped </script>, JSON escaping failed")
	}
	if !strings.Contains(body, `sh \u003c/script\u003e`) {
		t.Errorf("handleStatic() output does not contain the expected JSON-escaped injected value")
	}
}
