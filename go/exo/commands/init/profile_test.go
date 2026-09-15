package init

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// rawJSON creates a json.RawMessage from a literal string.
func rawJSON(s string) json.RawMessage { return json.RawMessage(s) }

// ---- PromptSpec.defaultString ----

func TestDefaultStringBoolTrue(t *testing.T) {
	p := PromptSpec{Default: rawJSON("true")}
	if got := p.defaultString(); got != "true" {
		t.Errorf("got %q want %q", got, "true")
	}
}

func TestDefaultStringBoolFalse(t *testing.T) {
	p := PromptSpec{Default: rawJSON("false")}
	if got := p.defaultString(); got != "false" {
		t.Errorf("got %q want %q", got, "false")
	}
}

func TestDefaultStringJSONString(t *testing.T) {
	p := PromptSpec{Default: rawJSON(`"authenticated"`)}
	if got := p.defaultString(); got != "authenticated" {
		t.Errorf("got %q want %q", got, "authenticated")
	}
}

func TestDefaultStringEmpty(t *testing.T) {
	p := PromptSpec{} // no Default
	if got := p.defaultString(); got != "" {
		t.Errorf("got %q want %q", got, "")
	}
}

// ---- PromptSpec JSON unmarshaling (server field names) ----

func TestPromptSpecUnmarshalServerFields(t *testing.T) {
	// Verify the struct tags match the actual server field names.
	raw := `{
		"key": "create_repo",
		"question": "Create a GitLab repo?",
		"type": "bool",
		"default": false,
		"choices": null
	}`
	var p PromptSpec
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.Name != "create_repo" {
		t.Errorf("Name: got %q want create_repo", p.Name)
	}
	if p.Kind != "bool" {
		t.Errorf("Kind: got %q want bool", p.Kind)
	}
	if p.Label != "Create a GitLab repo?" {
		t.Errorf("Label: got %q", p.Label)
	}
	if p.defaultString() != "false" {
		t.Errorf("defaultString: got %q want false", p.defaultString())
	}
}

func TestPromptSpecUnmarshalChoiceWithStringDefault(t *testing.T) {
	raw := `{
		"key": "read_access",
		"question": "Who can read?",
		"type": "choice",
		"default": "authenticated",
		"choices": ["public", "authenticated", "viewers"]
	}`
	var p PromptSpec
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.Name != "read_access" {
		t.Errorf("Name: got %q", p.Name)
	}
	if p.defaultString() != "authenticated" {
		t.Errorf("defaultString: got %q want authenticated", p.defaultString())
	}
	if len(p.Choices) != 3 {
		t.Errorf("Choices len: got %d want 3", len(p.Choices))
	}
}

// ---- runProfilePrompts with server-format prompts ----

func TestRunProfilePromptsYesDefaults(t *testing.T) {
	spec := &ProfileSpec{
		Prompts: []PromptSpec{
			{Name: "create_repo", Kind: "bool", Label: "Create repo?", Default: rawJSON("false")},
			{Name: "read_access", Kind: "choice", Label: "Who reads?", Choices: []string{"public", "authenticated"}, Default: rawJSON(`"authenticated"`)},
			{Name: "prefix", Kind: "string", Label: "Prefix", Default: rawJSON(`"sandbox"`)},
		},
	}
	answers := make(map[string]interface{})
	if err := runProfilePrompts(spec, answers, true); err != nil {
		t.Fatalf("runProfilePrompts: %v", err)
	}
	if answers["create_repo"] != false {
		t.Errorf("create_repo: got %v want false", answers["create_repo"])
	}
	if answers["read_access"] != "authenticated" {
		t.Errorf("read_access: got %v want authenticated", answers["read_access"])
	}
	if answers["prefix"] != "sandbox" {
		t.Errorf("prefix: got %v want sandbox", answers["prefix"])
	}
}

func TestRunProfilePromptsChoiceDefaultFallbackToFirst(t *testing.T) {
	spec := &ProfileSpec{
		Prompts: []PromptSpec{
			{Name: "env", Kind: "choice", Label: "Env", Choices: []string{"alpha", "beta"}},
		},
	}
	answers := make(map[string]interface{})
	if err := runProfilePrompts(spec, answers, true); err != nil {
		t.Fatalf("runProfilePrompts: %v", err)
	}
	if answers["env"] != "alpha" {
		t.Errorf("env: got %v want alpha", answers["env"])
	}
}

func TestRunProfilePromptsInvalidChoiceDefault(t *testing.T) {
	spec := &ProfileSpec{
		Prompts: []PromptSpec{
			{Name: "env", Kind: "choice", Label: "Env", Choices: []string{"dev", "prod"}, Default: rawJSON(`"staging"`)},
		},
	}
	answers := make(map[string]interface{})
	if err := runProfilePrompts(spec, answers, true); err == nil {
		t.Fatal("expected error for invalid default choice")
	}
}

func TestRunProfilePromptsUnknownKind(t *testing.T) {
	spec := &ProfileSpec{
		Prompts: []PromptSpec{
			{Name: "x", Kind: "date", Label: "Pick"},
		},
	}
	answers := make(map[string]interface{})
	if err := runProfilePrompts(spec, answers, true); err == nil {
		t.Fatal("expected error for unknown kind")
	}
}

func TestRunProfilePromptsEmptyKind(t *testing.T) {
	// Server may send a prompt with empty type — should error clearly, not panic.
	spec := &ProfileSpec{
		Prompts: []PromptSpec{
			{Name: "x", Kind: "", Label: "Something"},
		},
	}
	answers := make(map[string]interface{})
	if err := runProfilePrompts(spec, answers, true); err == nil {
		t.Fatal("expected error for empty kind")
	}
}

func TestRunProfilePromptsChoiceNoChoices(t *testing.T) {
	spec := &ProfileSpec{
		Prompts: []PromptSpec{
			{Name: "x", Kind: "choice", Label: "Pick", Choices: nil},
		},
	}
	answers := make(map[string]interface{})
	if err := runProfilePrompts(spec, answers, true); err == nil {
		t.Fatal("expected error for empty choices")
	}
}

// ---- buildTplData ----

func TestBuildTplDataFlatNamespace(t *testing.T) {
	spec := &ProfileSpec{Vars: map[string]string{"bucket": "my-bucket"}}
	answers := map[string]interface{}{"create_repo": true, "read_access": "public"}
	env := map[string]string{"REGION": "us-west-2"}

	data := buildTplData(spec, "jdoe", "myrepo", env, answers)

	if data["UnixID"] != "jdoe" {
		t.Errorf("UnixID: got %v", data["UnixID"])
	}
	if data["Folder"] != "myrepo" {
		t.Errorf("Folder: got %v", data["Folder"])
	}
	if data["bucket"] != "my-bucket" {
		t.Errorf("bucket: got %v", data["bucket"])
	}
	if data["REGION"] != "us-west-2" {
		t.Errorf("REGION: got %v", data["REGION"])
	}
	if data["create_repo"] != true {
		t.Errorf("create_repo: got %v", data["create_repo"])
	}
	if data["read_access"] != "public" {
		t.Errorf("read_access: got %v", data["read_access"])
	}
}

// ---- renderTemplate with flat data ----

func TestRenderTemplateFlatAccess(t *testing.T) {
	// Templates reference .UnixID, .Folder, .read_access directly (no .Answers. prefix)
	data := profileTplData{
		"UnixID":      "jdoe",
		"Folder":      "myrepo",
		"read_access": "public",
	}
	out, err := renderTemplate("permissions", `owner: {{.UnixID}}
folder: {{.Folder}}
read_access: {{.read_access}}`, data)
	if err != nil {
		t.Fatalf("renderTemplate: %v", err)
	}
	if !strings.Contains(out, "owner: jdoe") {
		t.Errorf("missing owner: %q", out)
	}
	if !strings.Contains(out, "read_access: public") {
		t.Errorf("missing read_access: %q", out)
	}
}

func TestRenderTemplateS3URL(t *testing.T) {
	// Matches the actual sandbox profile template pattern
	data := profileTplData{
		"UnixID": "jdoe",
		"Folder": "myrepo",
	}
	tmpl := `s3://bucket/sandbox/{{.UnixID}}/{{.Folder}}/_annex`
	out, err := renderTemplate("s3url", tmpl, data)
	if err != nil {
		t.Fatalf("renderTemplate: %v", err)
	}
	want := "s3://bucket/sandbox/jdoe/myrepo/_annex"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

// ---- evalWhenGuard ----

func TestEvalWhenGuardEmpty(t *testing.T) {
	data := profileTplData{}
	run, err := evalWhenGuard("", data)
	if err != nil || !run {
		t.Errorf("empty guard should return true, got run=%v err=%v", run, err)
	}
}

func TestEvalWhenGuardBareNameTrue(t *testing.T) {
	// Server sends when: "create_repo" (bare key, no dot)
	data := profileTplData{"create_repo": true}
	run, err := evalWhenGuard("create_repo", data)
	if err != nil {
		t.Fatalf("evalWhenGuard: %v", err)
	}
	if !run {
		t.Error("expected true when create_repo=true")
	}
}

func TestEvalWhenGuardBareNameFalse(t *testing.T) {
	data := profileTplData{"create_repo": false}
	run, err := evalWhenGuard("create_repo", data)
	if err != nil {
		t.Fatalf("evalWhenGuard: %v", err)
	}
	if run {
		t.Error("expected false when create_repo=false")
	}
}

func TestEvalWhenGuardDotPrefix(t *testing.T) {
	// Also support .create_repo with dot
	data := profileTplData{"create_repo": true}
	run, err := evalWhenGuard(".create_repo", data)
	if err != nil {
		t.Fatalf("evalWhenGuard: %v", err)
	}
	if !run {
		t.Error("expected true")
	}
}

func TestEvalWhenGuardMissingKey(t *testing.T) {
	// Missing key with missingkey=zero → false (not an error)
	data := profileTplData{}
	run, err := evalWhenGuard("create_repo", data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run {
		t.Error("missing key should evaluate to false")
	}
}

// ---- validateRenderedFile ----

func TestValidateRenderedFileRemotesList(t *testing.T) {
	// Server sends remotes as a YAML list, not a map
	content := "- name: s3-annex\n  type: annex\n  s3url: s3://bucket/prefix\n"
	if err := validateRenderedFile("remotes", content); err != nil {
		t.Errorf("unexpected error for list remotes: %v", err)
	}
}

func TestValidateRenderedFilePermissions(t *testing.T) {
	content := "owners:\n  - jdoe\nread_access: public\nwrite_access: owners\n"
	if err := validateRenderedFile("permissions", content); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateRenderedFileBadYAML(t *testing.T) {
	if err := validateRenderedFile("remotes", ": bad: [\n"); err == nil {
		t.Error("expected error for bad YAML")
	}
}

// ---- renderAndWrite ----

func TestRenderAndWriteDryRun(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	_ = os.Chdir(dir)
	t.Cleanup(func() { _ = os.Chdir(orig) })

	spec := &ProfileSpec{
		Templates: map[string]string{"permissions": "owners:\n  - {{.UnixID}}\n"},
	}
	data := profileTplData{"UnixID": "jdoe"}
	if err := renderAndWrite(spec, data, true, false); err != nil {
		t.Fatalf("renderAndWrite dry-run: %v", err)
	}
	if _, err := os.Stat(".exohub"); err == nil {
		t.Error(".exohub/ must not be created in dry-run mode")
	}
}

func TestRenderAndWriteCreatesFiles(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	_ = os.Chdir(dir)
	t.Cleanup(func() { _ = os.Chdir(orig) })

	spec := &ProfileSpec{
		Templates: map[string]string{"permissions": "owners:\n  - jdoe\nread_access: public\nwrite_access: owners\n"},
	}
	data := profileTplData{"UnixID": "jdoe"}
	if err := renderAndWrite(spec, data, false, false); err != nil {
		t.Fatalf("renderAndWrite: %v", err)
	}
	if _, err := os.Stat(filepath.Join(".exohub", "permissions")); err != nil {
		t.Errorf(".exohub/permissions not created: %v", err)
	}
}

func TestRenderAndWriteExistingExohubWithoutForce(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	_ = os.Chdir(dir)
	t.Cleanup(func() { _ = os.Chdir(orig) })

	_ = os.Mkdir(".exohub", 0755)
	spec := &ProfileSpec{Templates: map[string]string{"permissions": "owners: []\n"}}
	data := profileTplData{}
	if err := renderAndWrite(spec, data, false, false); err == nil {
		t.Fatal("expected error when .exohub exists without --force")
	}
}

func TestRenderAndWriteForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	_ = os.Chdir(dir)
	t.Cleanup(func() { _ = os.Chdir(orig) })

	_ = os.Mkdir(".exohub", 0755)
	spec := &ProfileSpec{Templates: map[string]string{"permissions": "owners: []\nread_access: public\nwrite_access: owners\n"}}
	data := profileTplData{}
	if err := renderAndWrite(spec, data, false, true); err != nil {
		t.Fatalf("renderAndWrite with force: %v", err)
	}
}

// ---- runProfileActions ----

func TestRunProfileActionsUnknownType(t *testing.T) {
	spec := &ProfileSpec{Actions: []ActionSpec{{Type: "unknown_action"}}}
	if err := runProfileActions(spec, profileTplData{}, false); err == nil {
		t.Fatal("expected error for unknown action type")
	}
}

func TestRunProfileActionsSkippedByWhenFalse(t *testing.T) {
	spec := &ProfileSpec{Actions: []ActionSpec{{Type: "unknown_action", When: "create_repo"}}}
	data := profileTplData{"create_repo": false}
	// Guard false → action skipped → no error despite unknown type
	if err := runProfileActions(spec, data, false); err != nil {
		t.Fatalf("skipped action should not error: %v", err)
	}
}

func TestRunProfileActionsSkippedByWhenMissingKey(t *testing.T) {
	spec := &ProfileSpec{Actions: []ActionSpec{{Type: "unknown_action", When: "create_repo"}}}
	// Missing key → zero value → false → skip
	if err := runProfileActions(spec, profileTplData{}, false); err != nil {
		t.Fatalf("missing key should skip action: %v", err)
	}
}

func TestRunProfileActionsCreateRepoDryRun(t *testing.T) {
	spec := &ProfileSpec{Actions: []ActionSpec{{Type: "create_repo", RepoURL: "https://example.com/org/repo"}}}
	if err := runProfileActions(spec, profileTplData{}, true); err != nil {
		t.Fatalf("dry-run create_repo: %v", err)
	}
}

// ---- resolveEnv ----

func TestResolveEnvFiltersPrefix(t *testing.T) {
	t.Setenv("EXOHUB_TPL_REGION", "eu-west-1")
	t.Setenv("OTHER_VAR", "should-not-appear")
	env := resolveEnv()
	if env["REGION"] != "eu-west-1" {
		t.Errorf("REGION: got %q", env["REGION"])
	}
	if _, ok := env["OTHER_VAR"]; ok {
		t.Error("OTHER_VAR should not be in env map")
	}
}

// ---- fetchProfile / listProfiles ----

func TestFetchProfile(t *testing.T) {
	// Use actual server field names in the mock response
	payload := `{
		"name": "sandbox",
		"vars": {"bucket": "my-bucket"},
		"prompts": [
			{"key": "create_repo", "question": "Create repo?", "type": "bool", "default": false},
			{"key": "read_access", "question": "Who reads?", "type": "choice", "default": "authenticated", "choices": ["public","authenticated"]}
		],
		"templates": {"permissions": "owners:\n  - {{.UnixID}}\n"},
		"actions": [{"type": "create_repo", "when": "create_repo"}]
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/profiles/sandbox" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(payload))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	t.Setenv("EXOHUB_API_URL", srv.URL+"/api")
	spec, err := fetchProfile("sandbox")
	if err != nil {
		t.Fatalf("fetchProfile: %v", err)
	}
	if spec.Name != "sandbox" {
		t.Errorf("Name: got %q", spec.Name)
	}
	if len(spec.Prompts) != 2 {
		t.Fatalf("Prompts len: got %d want 2", len(spec.Prompts))
	}
	// Verify server field mapping
	if spec.Prompts[0].Name != "create_repo" {
		t.Errorf("Prompts[0].Name: got %q want create_repo", spec.Prompts[0].Name)
	}
	if spec.Prompts[0].Kind != "bool" {
		t.Errorf("Prompts[0].Kind: got %q want bool", spec.Prompts[0].Kind)
	}
	if spec.Prompts[0].defaultString() != "false" {
		t.Errorf("Prompts[0].defaultString: got %q want false", spec.Prompts[0].defaultString())
	}
	if spec.Prompts[1].defaultString() != "authenticated" {
		t.Errorf("Prompts[1].defaultString: got %q want authenticated", spec.Prompts[1].defaultString())
	}
}

func TestFetchProfileNotFound(t *testing.T) {
	summaries := []ProfileSummary{{Name: "sandbox"}, {Name: "dev"}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/profiles" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(summaries)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	t.Setenv("EXOHUB_API_URL", srv.URL+"/api")
	_, err := fetchProfile("missing")
	if err == nil {
		t.Fatal("expected error for missing profile")
	}
}

func TestFetchProfileURLEncoding(t *testing.T) {
	var receivedURI string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedURI = r.RequestURI
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ProfileSpec{Name: "my profile"})
	}))
	defer srv.Close()

	t.Setenv("EXOHUB_API_URL", srv.URL+"/api")
	_, _ = fetchProfile("my profile")
	if receivedURI != "/api/profiles/my%20profile" {
		t.Errorf("expected URL-encoded request URI, got %q", receivedURI)
	}
}

func TestFetchProfileBodyLimitTruncates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[" + string(make([]byte, profileBodyLimit+1024)) + "]"))
	}))
	defer srv.Close()

	t.Setenv("EXOHUB_API_URL", srv.URL+"/api")
	_, err := fetchProfile("big")
	if err == nil {
		t.Fatal("expected error when response body exceeds limit")
	}
}

func TestListProfiles(t *testing.T) {
	summaries := []ProfileSummary{
		{Name: "sandbox", Description: "Sandbox profile"},
		{Name: "prod", Description: "Production profile"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/profiles" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(summaries)
	}))
	defer srv.Close()

	t.Setenv("EXOHUB_API_URL", srv.URL+"/api")
	got, err := listProfiles()
	if err != nil {
		t.Fatalf("listProfiles: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len: got %d want 2", len(got))
	}
}

func TestListProfilesServerNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	t.Setenv("EXOHUB_API_URL", srv.URL+"/api")
	got, err := listProfiles()
	if err != nil || got != nil {
		t.Errorf("expected nil,nil for 404; got %v, %v", got, err)
	}
}

// ---- end-to-end: runProfileInit with a mock server ----

func TestRunProfileInitNamedProfile(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	_ = os.Chdir(dir)
	t.Cleanup(func() { _ = os.Chdir(orig) })

	// Mock server returning the sandbox profile (server field names)
	payload := `{
		"name": "sandbox",
		"vars": {},
		"prompts": [
			{"key": "read_access", "question": "Who reads?", "type": "choice", "default": "authenticated", "choices": ["public","authenticated"]}
		],
		"templates": {
			"permissions": "owners:\n  - {{.UnixID}}\nread_access: {{.read_access}}\nwrite_access: owners\n"
		},
		"actions": []
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/profiles/sandbox" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(payload))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	t.Setenv("EXOHUB_API_URL", srv.URL+"/api")

	// Temporarily set flagYes so no TUI is shown
	old := flagYes
	flagYes = true
	t.Cleanup(func() { flagYes = old })

	if err := runProfileInit("sandbox", nil, nil); err != nil {
		t.Fatalf("runProfileInit: %v", err)
	}

	// Verify .exohub/permissions was written with the correct content
	data, err := os.ReadFile(filepath.Join(".exohub", "permissions"))
	if err != nil {
		t.Fatalf("read .exohub/permissions: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "read_access: authenticated") {
		t.Errorf("expected read_access: authenticated in permissions, got:\n%s", content)
	}
}

func TestRunProfileInitWhenGuardSkipsAction(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	_ = os.Chdir(dir)
	t.Cleanup(func() { _ = os.Chdir(orig) })

	payload := `{
		"name": "test",
		"vars": {},
		"prompts": [
			{"key": "create_repo", "question": "Create repo?", "type": "bool", "default": false}
		],
		"templates": {},
		"actions": [{"type": "create_repo", "when": "create_repo"}]
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(payload))
	}))
	defer srv.Close()

	t.Setenv("EXOHUB_API_URL", srv.URL+"/api")

	old := flagYes
	flagYes = true
	t.Cleanup(func() { flagYes = old })

	// Default is false → create_repo action should be skipped (not error on unknown URL)
	if err := runProfileInit("test", nil, nil); err != nil {
		t.Fatalf("runProfileInit: %v", err)
	}
}

func TestRunProfileInitUnknownActionErrors(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	_ = os.Chdir(dir)
	t.Cleanup(func() { _ = os.Chdir(orig) })

	payload := `{
		"name": "test",
		"vars": {},
		"prompts": [],
		"templates": {},
		"actions": [{"type": "unknown_action"}]
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(payload))
	}))
	defer srv.Close()

	t.Setenv("EXOHUB_API_URL", srv.URL+"/api")

	old := flagYes
	flagYes = true
	t.Cleanup(func() { flagYes = old })

	if err := runProfileInit("test", nil, nil); err == nil {
		t.Fatal("expected error for unknown action type")
	}
}

// TestRunProfileInitExistingListFormatRemotes tests that runProfileInit succeeds
// on a second run even when the existing .exohub/remotes is in list format
// (server-side bug, exohub-workers#25).
//
// The root cause: LoadRemotesConfig is called at the top of runInit before
// profile logic runs. With a list-format remotes file it would fail, blocking
// the profile from rewriting it. The fix defers the error to non-profile paths.
// This test uses --force so renderAndWrite overwrites the existing .exohub/.
func TestRunProfileInitExistingListFormatRemotes(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	_ = os.Chdir(dir)
	t.Cleanup(func() { _ = os.Chdir(orig) })

	// Pre-existing .exohub/ with list-format remotes (simulates server bug output)
	_ = os.Mkdir(".exohub", 0755)
	listRemotes := "- name: origin\n  type: annex\n  url: s3://bucket/prefix\n"
	if err := os.WriteFile(filepath.Join(".exohub", "remotes"), []byte(listRemotes), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Profile with templates — same as sandbox (will overwrite .exohub/)
	payload := `{
		"name": "test",
		"vars": {},
		"prompts": [],
		"templates": {
			"permissions": "owners:\n  - jdoe\nread_access: public\nwrite_access: owners\n"
		},
		"actions": []
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(payload))
	}))
	defer srv.Close()

	t.Setenv("EXOHUB_API_URL", srv.URL+"/api")

	oldYes := flagYes
	flagYes = true
	t.Cleanup(func() { flagYes = oldYes })

	oldForce := flagForce
	flagForce = true
	t.Cleanup(func() { flagForce = oldForce })

	// Must succeed — the list-format remotes file must not block the profile apply
	if err := runProfileInit("test", nil, nil); err != nil {
		t.Fatalf("runProfileInit with existing list-format remotes: %v", err)
	}
}

// TestRunProfileInitDryRunSkipsAnnexInit verifies that --dry-run prevents git-annex
// initialization after the profile files are previewed.
func TestRunProfileInitDryRunSkipsAnnexInit(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	_ = os.Chdir(dir)
	t.Cleanup(func() { _ = os.Chdir(orig) })

	// Initialize a git repo so we don't hit the "not a git repo" branch.
	if err := runCommand([]string{"git", "init"}); err != nil {
		t.Fatalf("git init: %v", err)
	}

	payload := `{
		"name": "test",
		"vars": {},
		"prompts": [],
		"templates": {
			"permissions": "owners:\n  - jdoe\nread_access: public\nwrite_access: owners\n"
		},
		"actions": []
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(payload))
	}))
	defer srv.Close()

	t.Setenv("EXOHUB_API_URL", srv.URL+"/api")

	oldYes := flagYes
	flagYes = true
	t.Cleanup(func() { flagYes = oldYes })

	oldDryRun := flagDryRun
	flagDryRun = true
	t.Cleanup(func() { flagDryRun = oldDryRun })

	oldProfile := flagProfile
	flagProfile = "test"
	t.Cleanup(func() { flagProfile = oldProfile })

	if err := runInit(nil); err != nil {
		t.Fatalf("runInit dry-run: %v", err)
	}

	// .exohub/ must NOT have been written (dry-run)
	if _, err := os.Stat(".exohub"); err == nil {
		t.Error(".exohub/ must not be created in dry-run mode")
	}
	// git-annex must NOT be initialized
	if err := runCommand([]string{"git", "config", "--get", "annex.uuid"}); err == nil {
		t.Error("git-annex must not be initialized in dry-run mode")
	}
}

// TestRunProfileInitContinuesIntoFullInit verifies that after a profile writes .exohub/,
// runInit continues into git-annex initialization.
func TestRunProfileInitContinuesIntoFullInit(t *testing.T) {
	if _, err := exec.LookPath("git-annex"); err != nil {
		t.Skip("git-annex not available")
	}

	dir := t.TempDir()
	orig, _ := os.Getwd()
	_ = os.Chdir(dir)
	t.Cleanup(func() { _ = os.Chdir(orig) })

	// Initialize a git repo.
	if err := runCommand([]string{"git", "init"}); err != nil {
		t.Fatalf("git init: %v", err)
	}

	// Profile with no remotes — just writes permissions.
	payload := `{
		"name": "test",
		"vars": {},
		"prompts": [],
		"templates": {
			"permissions": "owners:\n  - jdoe\nread_access: public\nwrite_access: owners\n"
		},
		"actions": []
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(payload))
	}))
	defer srv.Close()

	t.Setenv("EXOHUB_API_URL", srv.URL+"/api")

	oldYes := flagYes
	flagYes = true
	t.Cleanup(func() { flagYes = oldYes })

	oldProfile := flagProfile
	flagProfile = "test"
	t.Cleanup(func() { flagProfile = oldProfile })

	if err := runInit(nil); err != nil {
		t.Fatalf("runInit with profile: %v", err)
	}

	// .exohub/permissions must be written.
	if _, err := os.Stat(filepath.Join(".exohub", "permissions")); err != nil {
		t.Errorf(".exohub/permissions not written: %v", err)
	}
	// git-annex must be initialized (annex.uuid present in git config).
	if err := runCommand([]string{"git", "config", "--get", "annex.uuid"}); err != nil {
		t.Error("git-annex was not initialized after profile init")
	}
}

// TestRunProfileInitFreshNonGitDir verifies that in a directory without a git repo,
// runInit with --profile auto-initializes git before proceeding.
func TestRunProfileInitFreshNonGitDir(t *testing.T) {
	if _, err := exec.LookPath("git-annex"); err != nil {
		t.Skip("git-annex not available")
	}

	dir := t.TempDir()
	orig, _ := os.Getwd()
	_ = os.Chdir(dir)
	t.Cleanup(func() { _ = os.Chdir(orig) })

	// No git init — fresh directory.
	payload := `{
		"name": "test",
		"vars": {},
		"prompts": [],
		"templates": {
			"permissions": "owners:\n  - jdoe\nread_access: public\nwrite_access: owners\n"
		},
		"actions": []
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(payload))
	}))
	defer srv.Close()

	t.Setenv("EXOHUB_API_URL", srv.URL+"/api")

	oldYes := flagYes
	flagYes = true
	t.Cleanup(func() { flagYes = oldYes })

	oldProfile := flagProfile
	flagProfile = "test"
	t.Cleanup(func() { flagProfile = oldProfile })

	if err := runInit(nil); err != nil {
		t.Fatalf("runInit in fresh dir: %v", err)
	}

	// git repo must have been auto-initialized.
	if _, err := os.Stat(".git"); err != nil {
		t.Error(".git directory not created — ensureGitRepo was not called")
	}
	// git-annex must be initialized.
	if err := runCommand([]string{"git", "config", "--get", "annex.uuid"}); err != nil {
		t.Error("git-annex was not initialized after profile init in fresh dir")
	}
}
