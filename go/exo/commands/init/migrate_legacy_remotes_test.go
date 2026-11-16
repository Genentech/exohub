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

// legacyFixtureProfilePayload is the server-rendered profile for the
// exo-cli#197 regression tests: a role-split "remotes" template with an
// instance remote on grants: true, so the atlas remote's migration also
// exercises the grants-sync call.
const legacyFixtureProfilePayload = `{
	"name": "example-profile",
	"vars": {},
	"prompts": [],
	"templates": {
		"remotes": "remotes:\n  - name: catalog\n    type: artifactdb\n    mode: export\n    s3url: s3://example-bucket/_catalog\n    url: https://atlas.example.com/v1/a\n    grants: true\n  - name: catalog-instance\n    type: artifactdb\n    role: instance\n    url: https://adb.example.com/v1/b\n    s3url: s3://example-instance-bucket/example-project\n"
	},
	"actions": []
}`

func legacyFixtureRemotesData() []byte {
	return []byte("remotes:\n" +
		"  - name: catalog\n" +
		"    type: artifactdb\n" +
		"    mode: export\n" +
		"    uuid: catalog-uuid\n" +
		"    s3url: s3://example-bucket/_catalog\n" +
		"    atlas_url: https://atlas.example.com/v1/a\n" +
		"    instance_url: https://adb.example.com/v1/b\n")
}

// legacyFixtureWithCommand mocks the git/git-annex commands the migration
// path shells out to, and returns a pointer to the slice of recorded
// "name arg0 arg1 ..." calls, so tests can assert exactly which commands
// ran (e.g. that reconfigureRemote actually called enableremote).
func legacyFixtureWithCommand(t *testing.T) *[]string {
	t.Helper()
	var calls []string
	withCommand(t, func(name string, args ...string) *exec.Cmd {
		calls = append(calls, name+" "+strings.Join(args, " "))
		if name == "git" && len(args) >= 2 && args[0] == "config" && args[1] == "--get" {
			if strings.HasSuffix(args[2], ".annex-uuid") {
				return exec.Command("printf", "catalog-uuid")
			}
			return exec.Command("sh", "-c", "exit 1")
		}
		if name == "git" && len(args) >= 2 && args[0] == "annex" && args[1] == "wanted" {
			return exec.Command("printf", "include=.exohub/bundles/* or include=.artifactdb/*")
		}
		if name == "git" && len(args) >= 2 && args[0] == "annex" && args[1] == "initremote" {
			t.Fatalf("role: instance remote must never be initremote'd, got: %s %v", name, args)
		}
		return exec.Command("true")
	})
	return &calls
}

// TestRunLegacyRemoteMigration is the exo-cli#197 regression test: a
// legacy .exohub/remotes + a role-split profile fixture. --dry-run shows
// the plan and changes nothing; the real run produces the role-split
// file (atlas uuid kept, instance never initremote'd), realigns the
// git-annex remote config, and attempts a grants sync (grants: true on
// the rendered atlas remote) without ever calling the owner-only sync
// endpoint silently. A second --dry-run --json afterward reports no
// drift, and .exohub/permissions is byte-identical throughout.
func TestRunLegacyRemoteMigration(t *testing.T) {
	chdirTemp(t)
	calls := legacyFixtureWithCommand(t)

	var grantsSyncCalled bool
	profilePayload := legacyFixtureProfilePayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/profiles/example-profile":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(profilePayload))
		case r.Method == http.MethodPost && r.URL.Path == "/api/grants/verify":
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"in_sync":false,"drift":{"missing_grants":[{}],"extra_grants":[],"permission_mismatches":[]}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/grants/sync":
			grantsSyncCalled = true
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"synced"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("EXOHUB_API_URL", srv.URL+"/api")

	if err := os.MkdirAll(".exohub", 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(".exohub", "remotes"), legacyFixtureRemotesData(), 0644); err != nil {
		t.Fatalf("WriteFile remotes: %v", err)
	}
	permsData := []byte("owners:\n  - testuser\nread_access: viewers\nwrite_access: owners\n")
	if err := os.WriteFile(filepath.Join(".exohub", "permissions"), permsData, 0644); err != nil {
		t.Fatalf("WriteFile permissions: %v", err)
	}

	// JWT whose preferred_username claim is "testuser", so syncGrantsForRemote
	// sees the current user as the sole owner and actually attempts a sync
	// (instead of warning and skipping).
	fakeJWT := "eyJhbGciOiAiUlMyNTYifQ" + "." + "eyJwcmVmZXJyZWRfdXNlcm5hbWUiOiAidGVzdHVzZXIifQ" + "." + "sig"
	credsDir := t.TempDir()
	os.MkdirAll(filepath.Join(credsDir, "exo", "credentials"), 0700)
	os.WriteFile(filepath.Join(credsDir, "exo", "credentials", "token.json"),
		[]byte(`{"access_token":"`+fakeJWT+`","token_type":"Bearer"}`), 0600)
	t.Setenv("XDG_CONFIG_HOME", credsDir)

	lenient, err := LoadRemotesConfigAllowLegacy()
	if err != nil {
		t.Fatalf("LoadRemotesConfigAllowLegacy: %v", err)
	}

	// --- dry-run: must change nothing ---
	oldDryRun, oldYes := flagDryRun, flagYes
	flagDryRun = true
	t.Cleanup(func() { flagDryRun = oldDryRun })

	if err := runLegacyRemoteMigration("example-profile", "catalog", lenient); err != nil {
		t.Fatalf("dry-run runLegacyRemoteMigration: %v", err)
	}

	stillLegacy, err := os.ReadFile(filepath.Join(".exohub", "remotes"))
	if err != nil {
		t.Fatalf("read remotes after dry-run: %v", err)
	}
	if string(stillLegacy) != string(legacyFixtureRemotesData()) {
		t.Errorf("--dry-run must not change .exohub/remotes, got:\n%s", stillLegacy)
	}
	permsAfterDryRun, _ := os.ReadFile(filepath.Join(".exohub", "permissions"))
	if string(permsAfterDryRun) != string(permsData) {
		t.Errorf(".exohub/permissions changed by --dry-run")
	}

	// --- real run ---
	flagDryRun = false
	flagYes = true
	t.Cleanup(func() { flagYes = oldYes })

	if err := runLegacyRemoteMigration("example-profile", "catalog", lenient); err != nil {
		t.Fatalf("runLegacyRemoteMigration: %v", err)
	}

	after, err := LoadRemotesConfig()
	if err != nil {
		t.Fatalf("LoadRemotesConfig after migration: %v", err)
	}
	if len(after.Remotes) != 2 {
		t.Fatalf("expected 2 role-split remotes, got %d: %+v", len(after.Remotes), after.Remotes)
	}
	atlas, ok := findRemoteConfig(after, "catalog")
	if !ok {
		t.Fatal("catalog (role: atlas) missing after migration")
	}
	if atlas.UUID != "catalog-uuid" {
		t.Errorf("atlas uuid not preserved: got %q, want %q", atlas.UUID, "catalog-uuid")
	}
	if atlas.Role != "" && atlas.Role != "atlas" {
		t.Errorf("atlas role = %q, want atlas/empty", atlas.Role)
	}
	instance, ok := findRemoteConfig(after, "catalog-instance")
	if !ok {
		t.Fatal("catalog-instance (role: instance) missing after migration")
	}
	if instance.Role != "instance" {
		t.Errorf("instance role = %q, want instance", instance.Role)
	}
	if instance.DataFrom != "catalog" {
		t.Errorf("instance data_from = %q, want catalog", instance.DataFrom)
	}

	permsAfterRun, _ := os.ReadFile(filepath.Join(".exohub", "permissions"))
	if string(permsAfterRun) != string(permsData) {
		t.Errorf(".exohub/permissions not byte-identical after migration")
	}
	if !grantsSyncCalled {
		t.Errorf("expected a grants sync attempt for the migrated grants: true atlas remote")
	}

	// exo-cli#197 review (MR !359): the annex-config realign must actually
	// run, not just rewrite .exohub/remotes -- assert the real enableremote
	// call ran with the rendered s3url.
	var sawEnableRemote bool
	for _, c := range *calls {
		if strings.Contains(c, "git annex enableremote catalog ") && strings.Contains(c, "s3url=s3://example-bucket/_catalog") {
			sawEnableRemote = true
		}
	}
	if !sawEnableRemote {
		t.Errorf("expected a git annex enableremote catalog ... s3url=s3://example-bucket/_catalog call, got calls: %v", *calls)
	}

	// --- no drift afterward ---
	reloaded, err := LoadRemotesConfig()
	if err != nil {
		t.Fatalf("LoadRemotesConfig (reload): %v", err)
	}
	spec, err := FetchProfile("example-profile")
	if err != nil {
		t.Fatalf("FetchProfile: %v", err)
	}
	entries, err := computeDriftEntries(spec, reloaded, "catalog")
	if err != nil {
		t.Fatalf("computeDriftEntries: %v", err)
	}
	if hasActionableDrift(entries) {
		t.Errorf("expected zero drift after migration, got: %+v", entries)
	}
}

// TestRunLegacyRemoteMigrationRefusesWhenProfileStillLegacy verifies the
// migration refuses (does not touch .exohub/remotes) when the fetched
// profile's own rendered template is itself still legacy
// (atlas_url/instance_url) — the one case exo-cli#197 says must still
// refuse.
func TestRunLegacyRemoteMigrationRefusesWhenProfileStillLegacy(t *testing.T) {
	chdirTemp(t)
	legacyFixtureWithCommand(t)

	payload := `{
		"name": "still-legacy-profile",
		"vars": {},
		"prompts": [],
		"templates": {
			"remotes": "remotes:\n  - name: catalog\n    type: artifactdb\n    mode: export\n    s3url: s3://example-bucket/_catalog\n    atlas_url: https://atlas.example.com/v1/a\n"
		},
		"actions": []
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/profiles/still-legacy-profile" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(payload))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("EXOHUB_API_URL", srv.URL+"/api")

	if err := os.MkdirAll(".exohub", 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(".exohub", "remotes"), legacyFixtureRemotesData(), 0644); err != nil {
		t.Fatalf("WriteFile remotes: %v", err)
	}

	lenient, err := LoadRemotesConfigAllowLegacy()
	if err != nil {
		t.Fatalf("LoadRemotesConfigAllowLegacy: %v", err)
	}

	oldYes := flagYes
	flagYes = true
	t.Cleanup(func() { flagYes = oldYes })

	err = runLegacyRemoteMigration("still-legacy-profile", "catalog", lenient)
	if err == nil {
		t.Fatal("expected refusal when the profile's own template is still legacy")
	}
	if !strings.Contains(err.Error(), "itself still legacy") {
		t.Errorf("error = %q, want it to explain the profile is still legacy", err.Error())
	}

	unchanged, err := os.ReadFile(filepath.Join(".exohub", "remotes"))
	if err != nil {
		t.Fatalf("read remotes: %v", err)
	}
	if string(unchanged) != string(legacyFixtureRemotesData()) {
		t.Errorf(".exohub/remotes must be untouched on refusal, got:\n%s", unchanged)
	}
}

// TestRunInitMigratesLegacyRemoteOnProfilePath is the MR !359 review's M1
// regression: a runInit-level test (not calling runLegacyRemoteMigration
// directly) that drives the actual command the legacy-key refusal
// recommends -- `exo init --profile <p> --remote <r> --yes` -- end to end
// on a legacy fixture, then a follow-up `--dry-run --json` confirming zero
// drift. If runInit stops being lenient on this path (reverts to the
// refusal LoadRemotesConfig() returns), this test fails because runInit
// itself returns the refusal error instead of migrating.
func TestRunInitMigratesLegacyRemoteOnProfilePath(t *testing.T) {
	chdirTemp(t)
	legacyFixtureWithCommand(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/profiles/example-profile" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(legacyFixtureProfilePayload))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("EXOHUB_API_URL", srv.URL+"/api")

	if err := os.Mkdir(".git", 0755); err != nil {
		t.Fatalf("Mkdir .git: %v", err)
	}
	if err := os.MkdirAll(".exohub", 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(".exohub", "remotes"), legacyFixtureRemotesData(), 0644); err != nil {
		t.Fatalf("WriteFile remotes: %v", err)
	}

	oldProfile, oldRemote, oldYes := flagProfile, flagRemote, flagYes
	flagProfile, flagRemote, flagYes = "example-profile", "catalog", true
	t.Cleanup(func() { flagProfile, flagRemote, flagYes = oldProfile, oldRemote, oldYes })

	// This is the exact command the legacy-key refusal tells users to run.
	if err := runInit(nil); err != nil {
		t.Fatalf("runInit (exo init --profile example-profile --remote catalog --yes): %v", err)
	}

	after, err := LoadRemotesConfig()
	if err != nil {
		t.Fatalf("LoadRemotesConfig after runInit: %v", err)
	}
	if _, ok := findRemoteConfig(after, "catalog-instance"); !ok {
		t.Fatal("expected catalog-instance (role: instance) after exo init --profile --remote --yes")
	}
	atlas, ok := findRemoteConfig(after, "catalog")
	if !ok || atlas.UUID != "catalog-uuid" {
		t.Fatalf("expected catalog (role: atlas) with uuid preserved, got %+v (found=%v)", atlas, ok)
	}

	// Follow-up: --dry-run --json on the now role-split remote must report
	// zero drift (no legacy keys left, so this goes through the normal
	// runProfileDriftReport path, not the migration one). No actionable
	// drift means printDriftEntriesJSON never calls os.Exit, so this is
	// safe to call in-process.
	oldDryRun, oldJSON := flagDryRun, flagJSON
	flagDryRun, flagJSON = true, true
	t.Cleanup(func() { flagDryRun, flagJSON = oldDryRun, oldJSON })

	if err := runInit(nil); err != nil {
		t.Fatalf("runInit (exo init --profile example-profile --remote catalog --dry-run --json): %v", err)
	}
}

// TestRunLegacyRemoteMigrationDoesNotDuplicateExistingInstance is the
// MR !359 review's item 4: if .exohub/remotes already has a remote
// matching the profile's rendered role: instance entry (by name, or by
// role: instance + data_from naming the legacy remote), the migration
// must leave it untouched instead of inserting a duplicate.
func TestRunLegacyRemoteMigrationDoesNotDuplicateExistingInstance(t *testing.T) {
	chdirTemp(t)
	legacyFixtureWithCommand(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/profiles/example-profile" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(legacyFixtureProfilePayload))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("EXOHUB_API_URL", srv.URL+"/api")

	// catalog-instance already exists, hand-customized (grants_account_id
	// set) -- a value the profile's rendered template does not set, so if
	// the migration overwrote/duplicated this entry the customization
	// would be lost or a second entry would appear.
	data := []byte("remotes:\n" +
		"  - name: catalog\n" +
		"    type: artifactdb\n" +
		"    mode: export\n" +
		"    uuid: catalog-uuid\n" +
		"    s3url: s3://example-bucket/_catalog\n" +
		"    atlas_url: https://atlas.example.com/v1/a\n" +
		"    instance_url: https://adb.example.com/v1/b\n" +
		"  - name: catalog-instance\n" +
		"    type: artifactdb\n" +
		"    role: instance\n" +
		"    url: https://adb.example.com/v1/b\n" +
		"    s3url: s3://example-instance-bucket/example-project\n" +
		"    grants_account_id: hand-customized\n")
	if err := os.MkdirAll(".exohub", 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(".exohub", "remotes"), data, 0644); err != nil {
		t.Fatalf("WriteFile remotes: %v", err)
	}

	lenient, err := LoadRemotesConfigAllowLegacy()
	if err != nil {
		t.Fatalf("LoadRemotesConfigAllowLegacy: %v", err)
	}

	oldYes := flagYes
	flagYes = true
	t.Cleanup(func() { flagYes = oldYes })

	if err := runLegacyRemoteMigration("example-profile", "catalog", lenient); err != nil {
		t.Fatalf("runLegacyRemoteMigration: %v", err)
	}

	after, err := LoadRemotesConfig()
	if err != nil {
		t.Fatalf("LoadRemotesConfig: %v", err)
	}

	var instanceCount int
	for _, r := range after.Remotes {
		if r.Name == "catalog-instance" {
			instanceCount++
		}
	}
	if instanceCount != 1 {
		t.Fatalf("expected exactly 1 catalog-instance entry, got %d: %+v", instanceCount, after.Remotes)
	}

	instance, ok := findRemoteConfig(after, "catalog-instance")
	if !ok {
		t.Fatal("catalog-instance missing")
	}
	if instance.GrantsAccountID != "hand-customized" {
		t.Errorf("existing catalog-instance was overwritten: grants_account_id = %q, want %q", instance.GrantsAccountID, "hand-customized")
	}
}
