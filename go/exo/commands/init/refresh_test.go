package init

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiffRemoteConfigAddedChangedLocalOnly(t *testing.T) {
	current := RemoteConfig{
		Name:        "exohub-atlas",
		Type:        "artifactdb",
		Mode:        "export",
		S3URL:       "s3://bucket/_catalog",
		InstanceURL: "https://old-instance.example.com",
		Permissions: "", // not relevant for artifactdb, kept zero on both sides
	}
	current.RsyncOptions = "custom opts" // local-only: profile below doesn't define it

	rendered := RemoteConfig{
		Name:        "exohub-atlas",
		Type:        "artifactdb",
		Mode:        "export",
		S3URL:       "s3://bucket/_catalog",
		InstanceURL: "https://new-instance.example.com",
		AtlasURL:    "https://atlas.example.com",
	}

	entries := diffRemoteConfig("exohub-atlas", current, rendered)

	byField := make(map[string]driftEntry, len(entries))
	for _, e := range entries {
		byField[e.Field] = e
	}

	if e, ok := byField["atlas_url"]; !ok || e.Status != "added" || e.Profile != "https://atlas.example.com" {
		t.Errorf("atlas_url entry = %+v, want added https://atlas.example.com", e)
	}
	if e, ok := byField["instance_url"]; !ok || e.Status != "changed" || e.Current != "https://old-instance.example.com" || e.Profile != "https://new-instance.example.com" {
		t.Errorf("instance_url entry = %+v, want changed", e)
	}
	if e, ok := byField["rsync_options"]; !ok || e.Status != "local-only" || e.Current != "custom opts" {
		t.Errorf("rsync_options entry = %+v, want local-only custom opts", e)
	}
	if _, ok := byField["s3url"]; ok {
		t.Error("s3url should not be reported: both sides agree")
	}
	if _, ok := byField["name"]; ok {
		t.Error("name should never be diffed")
	}
	if _, ok := byField["type"]; ok {
		t.Error("type should never be diffed")
	}
}

func TestDiffRemoteConfigNoDrift(t *testing.T) {
	r := RemoteConfig{Name: "r", Type: "annex", S3URL: "s3://bucket/_annex", Chunk: "1GiB"}
	entries := diffRemoteConfig("r", r, r)
	if len(entries) != 0 {
		t.Errorf("expected no drift entries, got %+v", entries)
	}
}

func TestApplyRemoteFieldDiffOnlyTouchesListedFields(t *testing.T) {
	current := RemoteConfig{
		Name:        "exohub-atlas",
		Type:        "artifactdb",
		Mode:        "export",
		S3URL:       "s3://bucket/_catalog",
		InstanceURL: "https://old-instance.example.com",
	}
	rendered := RemoteConfig{
		Name:        "exohub-atlas",
		Type:        "artifactdb",
		Mode:        "export",
		S3URL:       "s3://bucket/_catalog",
		InstanceURL: "https://new-instance.example.com",
		AtlasURL:    "https://atlas.example.com",
	}
	entries := changedEntries(diffRemoteConfig("exohub-atlas", current, rendered))

	merged := applyRemoteFieldDiff(current, rendered, entries)
	if merged.AtlasURL != "https://atlas.example.com" {
		t.Errorf("AtlasURL not applied: got %q", merged.AtlasURL)
	}
	if merged.InstanceURL != "https://new-instance.example.com" {
		t.Errorf("InstanceURL not applied: got %q", merged.InstanceURL)
	}
	if merged.S3URL != current.S3URL {
		t.Errorf("S3URL should be untouched: got %q", merged.S3URL)
	}
	if merged.Name != current.Name || merged.Type != current.Type || merged.Mode != current.Mode {
		t.Error("identity/unrelated fields must not change")
	}
}

func TestResolveRefreshAnswersReverseMatchesFromCurrentRemote(t *testing.T) {
	spec := &ProfileSpec{
		Name:    "my-profile",
		Prompts: []PromptSpec{{Name: "s3url", Kind: "string"}},
	}
	current := RemoteConfig{Name: "exohub-atlas", Type: "artifactdb", S3URL: "s3://bucket/_catalog"}

	answers, err := resolveRefreshAnswers(spec, current)
	if err != nil {
		t.Fatalf("resolveRefreshAnswers: %v", err)
	}
	if answers["s3url"] != "s3://bucket/_catalog" {
		t.Errorf("s3url answer = %v, want s3://bucket/_catalog", answers["s3url"])
	}
}

func TestResolveRefreshAnswersErrorsOnUnresolvablePrompt(t *testing.T) {
	spec := &ProfileSpec{
		Name:    "my-profile",
		Prompts: []PromptSpec{{Name: "read_access", Kind: "choice", Choices: []string{"public", "authenticated"}}},
	}
	current := RemoteConfig{Name: "exohub-atlas", Type: "artifactdb", S3URL: "s3://bucket/_catalog"}

	_, err := resolveRefreshAnswers(spec, current)
	if err == nil {
		t.Fatal("expected error for unresolvable prompt")
	}
	if !strings.Contains(err.Error(), "read_access") {
		t.Errorf("error should name the missing prompt: %v", err)
	}
	if !strings.Contains(err.Error(), "--set") {
		t.Errorf("error should suggest --set: %v", err)
	}
}

func TestResolveRefreshAnswersSetFlagOverridesReverseMatch(t *testing.T) {
	spec := &ProfileSpec{
		Name:    "my-profile",
		Prompts: []PromptSpec{{Name: "s3url", Kind: "string"}},
	}
	current := RemoteConfig{Name: "exohub-atlas", Type: "artifactdb", S3URL: "s3://bucket/_catalog"}

	oldSet := flagSet
	flagSet = []string{"s3url=s3://override/_catalog"}
	t.Cleanup(func() { flagSet = oldSet })

	answers, err := resolveRefreshAnswers(spec, current)
	if err != nil {
		t.Fatalf("resolveRefreshAnswers: %v", err)
	}
	if answers["s3url"] != "s3://override/_catalog" {
		t.Errorf("s3url answer = %v, want override", answers["s3url"])
	}
}

func TestResolveRefreshAnswersUsesProfileLock(t *testing.T) {
	chdirTemp(t)

	spec := &ProfileSpec{
		Name:    "my-profile",
		Prompts: []PromptSpec{{Name: "read_access", Kind: "choice", Choices: []string{"public", "authenticated"}}},
	}
	if err := SaveProfileLock(&ProfileLock{
		Profile: "my-profile",
		Answers: map[string]interface{}{"read_access": "authenticated"},
	}); err != nil {
		t.Fatalf("SaveProfileLock: %v", err)
	}

	current := RemoteConfig{Name: "exohub-atlas", Type: "artifactdb", S3URL: "s3://bucket/_catalog"}
	answers, err := resolveRefreshAnswers(spec, current)
	if err != nil {
		t.Fatalf("resolveRefreshAnswers: %v", err)
	}
	if answers["read_access"] != "authenticated" {
		t.Errorf("read_access answer = %v, want authenticated (from profile.lock)", answers["read_access"])
	}
}

// TestRunProfileRemoteRefreshAddsAtlasURL reproduces the issue's "Done when":
// a repo initialized with an older profile version gets atlas_url added to
// the targeted remote, other remotes and fields untouched.
func TestRunProfileRemoteRefreshAddsAtlasURL(t *testing.T) {
	chdirTemp(t)

	withCommand(t, func(name string, args ...string) *exec.Cmd {
		if name == "git" && len(args) >= 2 && args[0] == "config" && args[1] == "--get" {
			if strings.HasSuffix(args[2], ".annex-uuid") {
				return exec.Command("printf", "catalog-uuid")
			}
			return exec.Command("sh", "-c", "exit 1")
		}
		if name == "git" && len(args) >= 2 && args[0] == "annex" && args[1] == "wanted" {
			return exec.Command("printf", "include=.exohub/bundles/* or include=.artifactdb/*")
		}
		return exec.Command("true")
	})

	payload := `{
		"name": "my-profile",
		"vars": {},
		"prompts": [],
		"templates": {
			"remotes": "remotes:\n  - name: exohub-atlas\n    type: artifactdb\n    mode: export\n    s3url: s3://bucket/_catalog\n    atlas_url: https://atlas.example.com\n"
		},
		"actions": []
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/profiles/my-profile" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(payload))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	t.Setenv("EXOHUB_API_URL", srv.URL+"/api")

	oldYes := flagYes
	flagYes = true
	t.Cleanup(func() { flagYes = oldYes })

	cfg := &RemotesConfig{Remotes: []RemoteConfig{
		{Name: "exohub-annex", Type: "annex", S3URL: "s3://bucket/_annex", Chunk: "1GiB"},
		{Name: "exohub-atlas", Type: "artifactdb", Mode: "export", S3URL: "s3://bucket/_catalog"},
	}}
	if err := SaveRemotesConfig(cfg); err != nil {
		t.Fatalf("SaveRemotesConfig: %v", err)
	}

	reloaded, err := LoadRemotesConfig()
	if err != nil {
		t.Fatalf("LoadRemotesConfig: %v", err)
	}

	if err := runProfileRemoteRefresh("my-profile", "exohub-atlas", reloaded); err != nil {
		t.Fatalf("runProfileRemoteRefresh: %v", err)
	}

	after, err := LoadRemotesConfig()
	if err != nil {
		t.Fatalf("LoadRemotesConfig (after): %v", err)
	}
	if len(after.Remotes) != 2 {
		t.Fatalf("expected 2 remotes, got %d", len(after.Remotes))
	}

	annex, ok := findRemoteConfig(after, "exohub-annex")
	if !ok {
		t.Fatal("exohub-annex missing after refresh")
	}
	if annex.S3URL != "s3://bucket/_annex" || annex.Chunk != "1GiB" {
		t.Errorf("exohub-annex should be byte-identical, got %+v", annex)
	}

	atlas, ok := findRemoteConfig(after, "exohub-atlas")
	if !ok {
		t.Fatal("exohub-atlas missing after refresh")
	}
	if atlas.AtlasURL != "https://atlas.example.com" {
		t.Errorf("AtlasURL not refreshed: got %q", atlas.AtlasURL)
	}
	if atlas.S3URL != "s3://bucket/_catalog" {
		t.Errorf("S3URL should be unchanged: got %q", atlas.S3URL)
	}
}

func TestRunProfileRemoteRefreshNoDriftIsNoop(t *testing.T) {
	chdirTemp(t)

	payload := `{
		"name": "my-profile",
		"vars": {},
		"prompts": [],
		"templates": {
			"remotes": "remotes:\n  - name: exohub-atlas\n    type: artifactdb\n    mode: export\n    s3url: s3://bucket/_catalog\n"
		},
		"actions": []
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/profiles/my-profile" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(payload))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	t.Setenv("EXOHUB_API_URL", srv.URL+"/api")

	cfg := &RemotesConfig{Remotes: []RemoteConfig{
		{Name: "exohub-atlas", Type: "artifactdb", Mode: "export", S3URL: "s3://bucket/_catalog"},
	}}
	if err := SaveRemotesConfig(cfg); err != nil {
		t.Fatalf("SaveRemotesConfig: %v", err)
	}
	before, err := os.ReadFile(filepath.Join(".exohub", "remotes"))
	if err != nil {
		t.Fatalf("read before: %v", err)
	}

	reloaded, err := LoadRemotesConfig()
	if err != nil {
		t.Fatalf("LoadRemotesConfig: %v", err)
	}
	if err := runProfileRemoteRefresh("my-profile", "exohub-atlas", reloaded); err != nil {
		t.Fatalf("runProfileRemoteRefresh: %v", err)
	}

	after, err := os.ReadFile(filepath.Join(".exohub", "remotes"))
	if err != nil {
		t.Fatalf("read after: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf(".exohub/remotes changed with no drift:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// TestRunProfileRemoteRefreshPreservesCommentsAndUnknownKeys covers the MR
// !307 review: a struct remarshal (yaml.Marshal(*RemotesConfig)) drops
// comments and any key the struct doesn't model, and reformats quoting, for
// the WHOLE file, not just the refreshed remote. Editing the file as a
// yaml.Node document instead must leave everything but the inserted field
// byte-identical.
func TestRunProfileRemoteRefreshPreservesCommentsAndUnknownKeys(t *testing.T) {
	chdirTemp(t)

	withCommand(t, func(name string, args ...string) *exec.Cmd {
		if name == "git" && len(args) >= 2 && args[0] == "config" && args[1] == "--get" {
			if strings.HasSuffix(args[2], ".annex-uuid") {
				return exec.Command("printf", "catalog-uuid")
			}
			return exec.Command("sh", "-c", "exit 1")
		}
		if name == "git" && len(args) >= 2 && args[0] == "annex" && args[1] == "wanted" {
			return exec.Command("printf", "include=.exohub/bundles/* or include=.artifactdb/*")
		}
		return exec.Command("true")
	})

	payload := `{
		"name": "my-profile",
		"vars": {},
		"prompts": [],
		"templates": {
			"remotes": "remotes:\n  - name: store\n    type: annex\n    s3url: s3://bucket/ds/annex/\n  - name: atlas\n    type: artifactdb\n    mode: export\n    s3url: s3://bucket/ds/_catalog/\n    atlas_url: https://atlas.example.com/\n"
		},
		"actions": []
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/profiles/my-profile" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(payload))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	t.Setenv("EXOHUB_API_URL", srv.URL+"/api")

	oldYes := flagYes
	flagYes = true
	t.Cleanup(func() { flagYes = oldYes })

	handEdited := "remotes:\n" +
		"  # primary annex store\n" +
		"  - name: store\n" +
		"    type: annex\n" +
		"    s3url: s3://bucket/ds/annex/\n" +
		"    owner_note: keep-this # hand-added key\n" +
		"  - name: atlas\n" +
		"    type: artifactdb\n" +
		"    mode: export\n" +
		"    s3url: \"s3://bucket/ds/_catalog/\"\n"
	if err := os.MkdirAll(".exohub", 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	remotesPath := filepath.Join(".exohub", "remotes")
	if err := os.WriteFile(remotesPath, []byte(handEdited), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	reloaded, err := LoadRemotesConfig()
	if err != nil {
		t.Fatalf("LoadRemotesConfig: %v", err)
	}

	if err := runProfileRemoteRefresh("my-profile", "atlas", reloaded); err != nil {
		t.Fatalf("runProfileRemoteRefresh: %v", err)
	}

	after, err := os.ReadFile(remotesPath)
	if err != nil {
		t.Fatalf("read after: %v", err)
	}

	want := "remotes:\n" +
		"  # primary annex store\n" +
		"  - name: store\n" +
		"    type: annex\n" +
		"    s3url: s3://bucket/ds/annex/\n" +
		"    owner_note: keep-this # hand-added key\n" +
		"  - name: atlas\n" +
		"    type: artifactdb\n" +
		"    mode: export\n" +
		"    s3url: \"s3://bucket/ds/_catalog/\"\n" +
		"    atlas_url: https://atlas.example.com/\n"
	if string(after) != want {
		t.Errorf(".exohub/remotes not byte-identical except the inserted line:\ngot:\n%s\nwant:\n%s", after, want)
	}
}

func TestRunProfileRemoteRefreshUnknownRemoteErrors(t *testing.T) {
	chdirTemp(t)
	cfg := &RemotesConfig{Remotes: []RemoteConfig{{Name: "other", Type: "annex", S3URL: "s3://bucket/_annex"}}}
	err := runProfileRemoteRefresh("my-profile", "exohub-atlas", cfg)
	if err == nil {
		t.Fatal("expected error for unknown remote")
	}
}

// TestRunProfileRemoteRefreshUsesLockedFolderNotDirectory reproduces the
// exo-cli#167 fix: once .exohub/profile.lock records the repo's resolved
// .Folder, a later `--remote` refresh run from a differently-named/renamed
// scratch directory must keep rendering the same S3 prefix instead of
// silently re-pointing the remote at a new one.
func TestRunProfileRemoteRefreshUsesLockedFolderNotDirectory(t *testing.T) {
	chdirTemp(t)

	withCommand(t, func(name string, args ...string) *exec.Cmd {
		return exec.Command("sh", "-c", "exit 1")
	})

	if err := SaveProfileLock(&ProfileLock{Profile: "my-profile", Folder: "DS000000001"}); err != nil {
		t.Fatalf("SaveProfileLock: %v", err)
	}

	payload := `{
		"name": "my-profile",
		"vars": {},
		"prompts": [],
		"templates": {
			"remotes": "remotes:\n  - name: exohub-annex\n    type: annex\n    s3url: s3://exohub-sandbox-uat/styx/my-profile/{{.Folder}}/_annex\n    chunk: 1GiB\n"
		},
		"actions": []
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/profiles/my-profile" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(payload))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	t.Setenv("EXOHUB_API_URL", srv.URL+"/api")

	oldYes := flagYes
	flagYes = true
	t.Cleanup(func() { flagYes = oldYes })

	// Current .exohub/remotes already has the correct, dataset-identity
	// prefix — not the (irrelevant, randomly-named) test tmpdir.
	cfg := &RemotesConfig{Remotes: []RemoteConfig{
		{Name: "exohub-annex", Type: "annex", S3URL: "s3://exohub-sandbox-uat/styx/my-profile/DS000000001/_annex", Chunk: "1GiB"},
	}}
	if err := SaveRemotesConfig(cfg); err != nil {
		t.Fatalf("SaveRemotesConfig: %v", err)
	}
	before, err := os.ReadFile(filepath.Join(".exohub", "remotes"))
	if err != nil {
		t.Fatalf("read before: %v", err)
	}

	reloaded, err := LoadRemotesConfig()
	if err != nil {
		t.Fatalf("LoadRemotesConfig: %v", err)
	}
	if err := runProfileRemoteRefresh("my-profile", "exohub-annex", reloaded); err != nil {
		t.Fatalf("runProfileRemoteRefresh: %v", err)
	}

	after, err := os.ReadFile(filepath.Join(".exohub", "remotes"))
	if err != nil {
		t.Fatalf("read after: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf(".exohub/remotes prefix changed silently due to cwd, before:\n%s\nafter:\n%s", before, after)
	}
}

// TestRunProfileRemoteRefreshFolderOverride verifies --folder overrides the
// locked Folder for a one-off refresh, and that the resulting s3url change
// (a location move) is only applied with --allow-move.
func TestRunProfileRemoteRefreshFolderOverride(t *testing.T) {
	chdirTemp(t)

	withCommand(t, func(name string, args ...string) *exec.Cmd {
		if name == "git" && len(args) >= 2 && args[0] == "config" && args[1] == "--get" {
			return exec.Command("sh", "-c", "exit 1")
		}
		return exec.Command("true")
	})

	if err := SaveProfileLock(&ProfileLock{Profile: "my-profile", Folder: "DS000000001"}); err != nil {
		t.Fatalf("SaveProfileLock: %v", err)
	}

	payload := `{
		"name": "my-profile",
		"vars": {},
		"prompts": [],
		"templates": {
			"remotes": "remotes:\n  - name: exohub-annex\n    type: annex\n    s3url: s3://bucket/{{.Folder}}/_annex\n    chunk: 1GiB\n"
		},
		"actions": []
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/profiles/my-profile" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(payload))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	t.Setenv("EXOHUB_API_URL", srv.URL+"/api")

	oldYes := flagYes
	flagYes = true
	t.Cleanup(func() { flagYes = oldYes })

	oldAllowMove := flagAllowMove
	flagAllowMove = true
	t.Cleanup(func() { flagAllowMove = oldAllowMove })

	oldFolder := flagFolder
	flagFolder = "DS000000002"
	t.Cleanup(func() { flagFolder = oldFolder })

	cfg := &RemotesConfig{Remotes: []RemoteConfig{
		{Name: "exohub-annex", Type: "annex", S3URL: "s3://bucket/DS000000001/_annex", Chunk: "1GiB"},
	}}
	if err := SaveRemotesConfig(cfg); err != nil {
		t.Fatalf("SaveRemotesConfig: %v", err)
	}

	reloaded, err := LoadRemotesConfig()
	if err != nil {
		t.Fatalf("LoadRemotesConfig: %v", err)
	}
	if err := runProfileRemoteRefresh("my-profile", "exohub-annex", reloaded); err != nil {
		t.Fatalf("runProfileRemoteRefresh: %v", err)
	}

	after, err := LoadRemotesConfig()
	if err != nil {
		t.Fatalf("LoadRemotesConfig (after): %v", err)
	}
	annex, ok := findRemoteConfig(after, "exohub-annex")
	if !ok {
		t.Fatal("exohub-annex missing after refresh")
	}
	if annex.S3URL != "s3://bucket/DS000000002/_annex" {
		t.Errorf("S3URL = %q, want s3://bucket/DS000000002/_annex (from --folder override)", annex.S3URL)
	}
}

// TestRunProfileRemoteRefreshS3URLChangeRefusedWithoutAllowMove verifies
// that --yes alone never approves an s3url change: a changed s3url is a
// location move (exo-cli#166) and must be refused until --allow-move is
// also passed, with the old -> new values shown in the error.
func TestRunProfileRemoteRefreshS3URLChangeRefusedWithoutAllowMove(t *testing.T) {
	chdirTemp(t)

	payload := `{
		"name": "my-profile",
		"vars": {},
		"prompts": [],
		"templates": {
			"remotes": "remotes:\n  - name: exohub-annex\n    type: annex\n    s3url: s3://bucket/{{.Folder}}/_annex\n    chunk: 1GiB\n"
		},
		"actions": []
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/profiles/my-profile" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(payload))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	t.Setenv("EXOHUB_API_URL", srv.URL+"/api")

	oldYes := flagYes
	flagYes = true
	t.Cleanup(func() { flagYes = oldYes })

	oldFolder := flagFolder
	flagFolder = "DS000000002"
	t.Cleanup(func() { flagFolder = oldFolder })

	cfg := &RemotesConfig{Remotes: []RemoteConfig{
		{Name: "exohub-annex", Type: "annex", S3URL: "s3://bucket/DS000000001/_annex", Chunk: "1GiB"},
	}}
	if err := SaveRemotesConfig(cfg); err != nil {
		t.Fatalf("SaveRemotesConfig: %v", err)
	}
	before, err := os.ReadFile(filepath.Join(".exohub", "remotes"))
	if err != nil {
		t.Fatalf("read before: %v", err)
	}

	reloaded, err := LoadRemotesConfig()
	if err != nil {
		t.Fatalf("LoadRemotesConfig: %v", err)
	}
	err = runProfileRemoteRefresh("my-profile", "exohub-annex", reloaded)
	if err == nil {
		t.Fatal("expected refusal without --allow-move")
	}
	if !strings.Contains(err.Error(), "--allow-move") ||
		!strings.Contains(err.Error(), "s3://bucket/DS000000001/_annex") ||
		!strings.Contains(err.Error(), "s3://bucket/DS000000002/_annex") {
		t.Errorf("error = %q, want mention of --allow-move and old -> new s3url", err)
	}

	after, err := os.ReadFile(filepath.Join(".exohub", "remotes"))
	if err != nil {
		t.Fatalf("read after: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf(".exohub/remotes changed despite refusal:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// TestRunProfileRemoteRefreshNonS3URLChangeDoesNotRequireAllowMove verifies
// the --allow-move gate is scoped to s3url only: other field changes still
// apply with plain --yes.
func TestRunProfileRemoteRefreshNonS3URLChangeDoesNotRequireAllowMove(t *testing.T) {
	chdirTemp(t)

	withCommand(t, func(name string, args ...string) *exec.Cmd {
		if name == "git" && len(args) >= 2 && args[0] == "config" && args[1] == "--get" {
			return exec.Command("sh", "-c", "exit 1")
		}
		return exec.Command("true")
	})

	payload := `{
		"name": "my-profile",
		"vars": {},
		"prompts": [],
		"templates": {
			"remotes": "remotes:\n  - name: exohub-annex\n    type: annex\n    s3url: s3://bucket/_annex\n    chunk: 2GiB\n"
		},
		"actions": []
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/profiles/my-profile" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(payload))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	t.Setenv("EXOHUB_API_URL", srv.URL+"/api")

	oldYes := flagYes
	flagYes = true
	t.Cleanup(func() { flagYes = oldYes })

	cfg := &RemotesConfig{Remotes: []RemoteConfig{
		{Name: "exohub-annex", Type: "annex", S3URL: "s3://bucket/_annex", Chunk: "1GiB"},
	}}
	if err := SaveRemotesConfig(cfg); err != nil {
		t.Fatalf("SaveRemotesConfig: %v", err)
	}

	reloaded, err := LoadRemotesConfig()
	if err != nil {
		t.Fatalf("LoadRemotesConfig: %v", err)
	}
	if err := runProfileRemoteRefresh("my-profile", "exohub-annex", reloaded); err != nil {
		t.Fatalf("runProfileRemoteRefresh: %v (chunk-only change must not require --allow-move)", err)
	}

	after, err := LoadRemotesConfig()
	if err != nil {
		t.Fatalf("LoadRemotesConfig (after): %v", err)
	}
	annex, ok := findRemoteConfig(after, "exohub-annex")
	if !ok || annex.Chunk != "2GiB" {
		t.Errorf("exohub-annex chunk = %+v, want 2GiB", annex)
	}
}

// TestResolveFolderFromExistingRemoteViaRefresh verifies .Folder is
// recovered from an existing remote's rendered s3url when no
// .exohub/profile.lock Folder is recorded yet (e.g. a repo initialized
// before exo-cli#167), taking priority over the git origin remote and the
// (irrelevant) checkout directory name.
func TestResolveFolderFromExistingRemoteViaRefresh(t *testing.T) {
	chdirTemp(t)

	withCommand(t, func(name string, args ...string) *exec.Cmd {
		if name == "git" && len(args) >= 2 && args[0] == "remote" && args[1] == "get-url" {
			return exec.Command("printf", "git@git.example.com:org/should-not-be-used.git")
		}
		return exec.Command("sh", "-c", "exit 1")
	})

	spec := &ProfileSpec{
		Name: "my-profile",
		Templates: map[string]string{
			"remotes": "remotes:\n  - name: exohub-annex\n    type: annex\n    s3url: s3://exohub-sandbox-uat/styx/my-profile/{{.Folder}}/_annex\n",
		},
	}
	current := RemoteConfig{Name: "exohub-annex", Type: "annex", S3URL: "s3://exohub-sandbox-uat/styx/my-profile/DS000000001/_annex"}

	existing := &RemotesConfig{Remotes: []RemoteConfig{current}}
	folder, source := resolveFolder("", spec, "jdoe", map[string]interface{}{}, existing, "/some/scratch-dir")
	if folder != "DS000000001" || source != folderSourceExistingRemote {
		t.Errorf("resolveFolder = (%q, %q), want (DS000000001, %q)", folder, source, folderSourceExistingRemote)
	}
}

// TestResolveFolderLockWinsOverExistingRemoteViaRefresh verifies the
// .exohub/profile.lock Folder takes priority over recovering it from the
// existing remote's rendered fields.
func TestResolveFolderLockWinsOverExistingRemoteViaRefresh(t *testing.T) {
	chdirTemp(t)

	if err := SaveProfileLock(&ProfileLock{Profile: "my-profile", Folder: "LOCKED-NAME"}); err != nil {
		t.Fatalf("SaveProfileLock: %v", err)
	}

	spec := &ProfileSpec{
		Name: "my-profile",
		Templates: map[string]string{
			"remotes": "remotes:\n  - name: exohub-annex\n    type: annex\n    s3url: s3://bucket/{{.Folder}}/_annex\n",
		},
	}
	current := RemoteConfig{Name: "exohub-annex", Type: "annex", S3URL: "s3://bucket/DS000000001/_annex"}

	existing := &RemotesConfig{Remotes: []RemoteConfig{current}}
	folder, source := resolveFolder("", spec, "jdoe", map[string]interface{}{}, existing, "/some/scratch-dir")
	if folder != "LOCKED-NAME" || source != folderSourceLock {
		t.Errorf("resolveFolder = (%q, %q), want (LOCKED-NAME, %q)", folder, source, folderSourceLock)
	}
}

// TestRenderedRemoteForUsesLockedUnixIDNotCurrentUser verifies a shared
// remote's rendered prefix does not depend on which user runs the refresh:
// .exohub/profile.lock's recorded UnixID wins over resolveUnixID()'s live
// value.
func TestRenderedRemoteForUsesLockedUnixIDNotCurrentUser(t *testing.T) {
	chdirTemp(t)

	actualUnixID := resolveUnixID()
	lockedUnixID := actualUnixID + "-locked-owner"

	if err := SaveProfileLock(&ProfileLock{Profile: "my-profile", Folder: "DS000000001", UnixID: lockedUnixID}); err != nil {
		t.Fatalf("SaveProfileLock: %v", err)
	}

	spec := &ProfileSpec{
		Name: "my-profile",
		Templates: map[string]string{
			"remotes": "remotes:\n  - name: exohub-annex\n    type: annex\n    s3url: s3://bucket/{{.UnixID}}/{{.Folder}}/_annex\n",
		},
	}
	current := RemoteConfig{Name: "exohub-annex", Type: "annex", S3URL: "s3://bucket/" + lockedUnixID + "/DS000000001/_annex"}

	rendered, err := renderedRemoteFor(spec, current)
	if err != nil {
		t.Fatalf("renderedRemoteFor: %v", err)
	}
	if rendered.S3URL != current.S3URL {
		t.Errorf("S3URL = %q, want %q (locked UnixID %q, not live %q)", rendered.S3URL, current.S3URL, lockedUnixID, actualUnixID)
	}
}
