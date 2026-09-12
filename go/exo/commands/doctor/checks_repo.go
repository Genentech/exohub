package doctor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/Genentech/exohub/go/exo/commands/login"
	"github.com/Genentech/exohub/go/exo/configdir"
	"github.com/Genentech/exohub/go/exo/internal/defaults"
)

const groupRepo = "Repository Config"

// remoteEntry is used for parsing .exohub/remotes.
type remoteEntry struct {
	Name   string `yaml:"name"`
	Type   string `yaml:"type"`
	UUID   string `yaml:"uuid,omitempty"`
	S3URL  string `yaml:"s3url,omitempty"`
	Grants bool   `yaml:"grants,omitempty"`
}

type remotesFile struct {
	Remotes []remoteEntry `yaml:"remotes"`
}

// permissionsConfig mirrors init.permissionsConfig for JSON parsing.
type permissionsConfig struct {
	Owners      []string `json:"owners" yaml:"owners"`
	Viewers     []string `json:"viewers,omitempty" yaml:"viewers,omitempty"`
	ReadAccess  string   `json:"read_access" yaml:"read_access"`
	WriteAccess string   `json:"write_access" yaml:"write_access"`
}

func runRepoChecks(repoDir string) []CheckResult {
	// Detect if we're inside an ExoHub repo
	remotesPath := filepath.Join(repoDir, ".exohub", "remotes")
	if _, err := os.Stat(remotesPath); os.IsNotExist(err) {
		return []CheckResult{
			skip("repo.remotes", groupRepo, "Not in an ExoHub repository (no .exohub/remotes)"),
			skip("repo.git-annex", groupRepo, "Not in an ExoHub repository"),
			skip("repo.permissions", groupRepo, "Not in an ExoHub repository"),
			skip("repo.registry-drift", groupRepo, "Not in an ExoHub repository"),
		}
	}

	// Check that .git exists (this is a git repo)
	if _, err := os.Stat(filepath.Join(repoDir, ".git")); os.IsNotExist(err) {
		return []CheckResult{
			fail("repo.git", groupRepo, "Directory has .exohub but no .git directory",
				"This does not appear to be a git repository",
				"Run 'git init' or 'exo clone' to initialize the repository"),
		}
	}

	// Change into the repo directory for git commands.
	origDir, _ := os.Getwd()
	if repoDir != "." && repoDir != origDir {
		if err := os.Chdir(repoDir); err != nil {
			return []CheckResult{
				fail("repo.chdir", groupRepo, fmt.Sprintf("Cannot change to repo dir: %s", repoDir), err.Error(), "Check the --repo path"),
			}
		}
		defer os.Chdir(origDir)
	}

	var results []CheckResult
	remotes, remotesErr := checkRepoRemotes(remotesPath)
	results = append(results, remotes...)
	results = append(results, checkGitAnnexInit()...)

	var parsedRemotes []remoteEntry
	if remotesErr == nil {
		data, _ := os.ReadFile(remotesPath)
		var rf remotesFile
		yaml.Unmarshal(data, &rf)
		parsedRemotes = rf.Remotes
	}

	results = append(results, checkPermissions(repoDir, parsedRemotes)...)
	results = append(results, checkRegistryDrift(repoDir, parsedRemotes)...)
	return results
}

func checkRepoRemotes(remotesPath string) ([]CheckResult, error) {
	id := "repo.remotes"
	data, err := os.ReadFile(remotesPath)
	if err != nil {
		return []CheckResult{fail(id, groupRepo, "Cannot read .exohub/remotes", err.Error(), "Check file permissions")}, err
	}

	var rf remotesFile
	if err := yaml.Unmarshal(data, &rf); err != nil {
		return []CheckResult{fail(id, groupRepo, "Failed to parse .exohub/remotes", err.Error(), "Fix YAML syntax in .exohub/remotes")}, err
	}

	if len(rf.Remotes) == 0 {
		return []CheckResult{warn(id, groupRepo, ".exohub/remotes exists but has no remotes defined", "", "Add a remote with 'exo init remote'")}, nil
	}

	// Check UUID matches in git-annex
	uuidMap := buildUUIDMap()
	var notFound []string
	var nameMismatch []string
	for _, r := range rf.Remotes {
		if r.UUID == "" {
			continue
		}
		if name, ok := uuidMap[r.UUID]; !ok {
			notFound = append(notFound, fmt.Sprintf("remote %q UUID %s not found in git-annex", r.Name, r.UUID))
		} else if name != r.Name {
			nameMismatch = append(nameMismatch, fmt.Sprintf("remote %q UUID %s is registered as %q in git-annex", r.Name, r.UUID, name))
		}
	}

	var results []CheckResult
	if len(notFound) > 0 {
		results = append(results, fail(id+".uuid-missing", groupRepo,
			fmt.Sprintf(".exohub/remotes has %d remote(s) whose UUID is not recognised by this clone", len(notFound)),
			strings.Join(notFound, "; "),
			"Run 'exo sync' on the machine where the dataset was originally set up, then re-clone or re-run 'exo sync' here. "+
				"If the dataset is brand new and no data has been uploaded yet, you may instead remove the 'uuid:' line "+
				"for the affected remote in .exohub/remotes and re-run 'exo init'."))
	}
	if len(nameMismatch) > 0 {
		results = append(results, warn(id+".uuid-name", groupRepo,
			fmt.Sprintf(".exohub/remotes has %d remote(s) whose UUID is registered under a different name in git-annex", len(nameMismatch)),
			strings.Join(nameMismatch, "; "),
			"The remote name in .exohub/remotes does not match what git-annex recorded. "+
				"Check .exohub/remotes for a stale or incorrect name and correct it, "+
				"or run 'exo sync' on the source repo to propagate the authoritative name."))
	}
	if len(results) > 0 {
		return results, nil
	}

	return []CheckResult{pass(id, groupRepo, fmt.Sprintf(".exohub/remotes OK (%d remote(s))", len(rf.Remotes)))}, nil
}

func checkGitAnnexInit() []CheckResult {
	var results []CheckResult

	// Check for real git remotes (fetch URL pointing at a git server).
	// git-annex special remotes (S3, rsync…) appear in `git remote` but have no
	// remote.<name>.url — they are driven by git-annex, not git push/fetch.
	// We use `git config --get-regexp remote\..*\.url` to list only remotes that
	// have a real fetch URL, then filter to http/https/git/ssh schemes.
	remoteID := "repo.git-remote"
	gitRemotes := listGitFetchRemotes()
	allRemoteOut, _ := exec.Command("git", "remote").Output()
	allRemoteNames := strings.Fields(strings.TrimSpace(string(allRemoteOut)))

	switch {
	case len(gitRemotes) > 0:
		results = append(results, pass(remoteID, groupRepo,
			fmt.Sprintf("git remote(s): %s", strings.Join(gitRemotes, ", "))))
	case len(allRemoteNames) > 0:
		// Only git-annex special remotes exist — no git push/pull target
		results = append(results, warn(remoteID, groupRepo,
			fmt.Sprintf("No git fetch remote found (found annex remote(s): %s)",
				strings.Join(allRemoteNames, ", ")),
			"All configured remotes are git-annex special remotes (S3/rsync/…); no GitLab/GitHub remote is set",
			"Add a git remote with 'git remote add origin <url>' or re-clone with 'exo clone'"))
	default:
		results = append(results, warn(remoteID, groupRepo,
			"No git remotes configured",
			"'git remote' returned no remotes — push/pull will not work",
			"Add a remote with 'git remote add origin <url>' or re-clone with 'exo clone'"))
	}

	id := "repo.git-annex"
	out, err := exec.Command("git", "annex", "version", "--raw").Output()
	if err != nil || strings.TrimSpace(string(out)) == "" {
		results = append(results, warn(id, groupRepo, "git-annex may not be initialized in this repo",
			"'git annex version' failed or returned empty",
			"Run 'git annex init' or 'exo init'"))
		return results
	}

	// Check that annex branch exists
	branchOut, _ := exec.Command("git", "rev-parse", "--verify", "git-annex").Output()
	if strings.TrimSpace(string(branchOut)) == "" {
		results = append(results, warn(id, groupRepo, "git-annex branch not found",
			"The git-annex branch is missing",
			"Run 'git annex init'"))
		return results
	}
	results = append(results, pass(id, groupRepo, "git-annex initialized"))
	return results
}

func checkPermissions(repoDir string, remotes []remoteEntry) []CheckResult {
	id := "repo.permissions"
	permsPath := filepath.Join(repoDir, ".exohub", "permissions")
	data, err := os.ReadFile(permsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []CheckResult{warn(id, groupRepo, ".exohub/permissions not found",
				"The permissions file is missing",
				"Run 'exo init' to create it")}
		}
		return []CheckResult{fail(id, groupRepo, "Cannot read .exohub/permissions", err.Error(), "Check file permissions")}
	}

	var perms permissionsConfig
	if err := yaml.Unmarshal(data, &perms); err != nil {
		return []CheckResult{fail(id, groupRepo, "Failed to parse .exohub/permissions", err.Error(), "Fix YAML syntax")}
	}

	username := currentUsername()
	isOwner := false
	isViewer := false
	for _, o := range perms.Owners {
		if o == username {
			isOwner = true
			break
		}
	}
	if !isOwner {
		for _, v := range perms.Viewers {
			if v == username {
				isViewer = true
				break
			}
		}
	}

	role := "none"
	if isOwner {
		role = "owner"
	} else if isViewer {
		role = "viewer"
	}

	detail := fmt.Sprintf("caller=%q role=%s owners=%v read_access=%s write_access=%s",
		username, role, perms.Owners, perms.ReadAccess, perms.WriteAccess)

	if !isOwner && !isViewer && perms.ReadAccess != "public" {
		return []CheckResult{warn(id, groupRepo,
			fmt.Sprintf("Caller %q is not in .exohub/permissions", username),
			detail,
			"Ask a dataset owner to add you via 'exo init', or request access")}
	}

	return []CheckResult{CheckResult{
		ID:      id,
		Group:   groupRepo,
		Status:  StatusPass,
		Summary: fmt.Sprintf("Permissions OK (caller=%q role=%s)", username, role),
		Detail:  detail,
	}}
}

func checkRegistryDrift(repoDir string, remotes []remoteEntry) []CheckResult {
	id := "repo.registry-drift"

	apiBase := strings.TrimRight(defaults.APIBase(), "/")
	if apiBase == "" {
		return []CheckResult{skip(id, groupRepo, "Registry drift check skipped: no API endpoint configured (set EXOHUB_API_URL)")}
	}

	permsPath := filepath.Join(repoDir, ".exohub", "permissions")
	permsData, err := os.ReadFile(permsPath)
	if err != nil {
		return []CheckResult{skip(id, groupRepo, "Registry drift check skipped (no .exohub/permissions)")}
	}

	// Pick the first grants-enabled remote with an s3url for the drift check.
	var s3url string
	for _, r := range remotes {
		if r.Grants && r.S3URL != "" {
			s3url = r.S3URL
			break
		}
	}
	if s3url == "" {
		return []CheckResult{skip(id, groupRepo, "Registry drift check skipped (no grants-enabled remote)")}
	}

	type verifyRequest struct {
		S3URL       string            `json:"s3url"`
		Permissions permissionsConfig `json:"permissions"`
	}
	var perms permissionsConfig
	yaml.Unmarshal(permsData, &perms)

	body, _ := json.Marshal(verifyRequest{S3URL: s3url, Permissions: perms})
	endpoint := apiBase + "/grants/verify"
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return []CheckResult{warn(id, groupRepo, "Could not build drift check request", err.Error(), "")}
	}
	req.Header.Set("Content-Type", "application/json")

	// Attach auth token
	if tokenFile, err := configdir.TokenFile(); err == nil {
		if token, err := login.LoadToken(tokenFile); err == nil && token.AccessToken != "" {
			req.Header.Set("Authorization", "Bearer "+token.AccessToken)
		}
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return []CheckResult{warn(id, groupRepo,
			"Registry drift check failed (API unreachable)",
			Redact(err.Error()),
			"Check VPN/network and try again")}
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == 404 {
		return []CheckResult{warn(id, groupRepo,
			"No _grants.json found for this remote (registry not initialized)",
			fmt.Sprintf("s3url=%s", s3url),
			"Run 'exo init' to provision grants")}
	}
	if resp.StatusCode != 200 {
		return []CheckResult{warn(id, groupRepo,
			fmt.Sprintf("Drift check returned HTTP %d", resp.StatusCode),
			Redact(string(respBody)),
			"Run 'exo init' to sync")}
	}

	var result struct {
		InSync bool `json:"in_sync"`
		Drift  struct {
			RegistryDrift  []json.RawMessage `json:"registry_drift"`
			MetadataDrift  []json.RawMessage `json:"metadata_drift"`
			MissingGrants  []json.RawMessage `json:"missing_grants"`
			ExtraGrants    []json.RawMessage `json:"extra_grants"`
		} `json:"drift"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return []CheckResult{warn(id, groupRepo, "Could not parse drift check response", Redact(string(respBody)), "")}
	}

	if !result.InSync {
		driftCount := len(result.Drift.RegistryDrift) + len(result.Drift.MetadataDrift)
		return []CheckResult{warn(id, groupRepo,
			fmt.Sprintf("Registry drift detected (%d field(s) differ)", driftCount),
			fmt.Sprintf("missing_grants=%d extra_grants=%d", len(result.Drift.MissingGrants), len(result.Drift.ExtraGrants)),
			"Run 'exo init' to sync local .exohub/permissions with S3 _grants.json")}
	}

	return []CheckResult{pass(id, groupRepo, "Local permissions match S3 registry (_grants.json)")}
}

// buildUUIDMap reads git config for annex UUIDs (same logic as init.buildUUIDToNameMap).
func buildUUIDMap() map[string]string {
	out, err := exec.Command("git", "config", "--get-regexp", `remote\..*\.annex-uuid`).Output()
	if err != nil {
		return map[string]string{}
	}
	m := make(map[string]string)
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) != 2 {
			continue
		}
		key := parts[0]
		uuid := parts[1]
		if !strings.HasPrefix(key, "remote.") || !strings.HasSuffix(key, ".annex-uuid") {
			continue
		}
		name := strings.TrimPrefix(key, "remote.")
		name = strings.TrimSuffix(name, ".annex-uuid")
		m[uuid] = name
	}
	return m
}

// listGitFetchRemotes returns the names of git remotes that have a real fetch
// URL (http/https/git/ssh schemes). git-annex special remotes (S3, rsync…) are
// stored in git config with annex-specific keys but no remote.<name>.url, so
// they do not appear in this list.
func listGitFetchRemotes() []string {
	out, err := exec.Command("git", "config", "--get-regexp", `remote\..*\.url`).Output()
	if err != nil {
		return nil
	}
	var names []string
	seen := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) != 2 {
			continue
		}
		// key is like remote.origin.url, value is the URL
		key, url := parts[0], parts[1]
		if !strings.HasPrefix(key, "remote.") || !strings.HasSuffix(key, ".url") {
			continue
		}
		// Only count remotes whose URL looks like a git server (not s3://, not nourl)
		lowerURL := strings.ToLower(url)
		if !strings.HasPrefix(lowerURL, "http") &&
			!strings.HasPrefix(lowerURL, "git@") &&
			!strings.HasPrefix(lowerURL, "ssh://") &&
			!strings.HasPrefix(lowerURL, "git://") {
			continue
		}
		name := strings.TrimPrefix(key, "remote.")
		name = strings.TrimSuffix(name, ".url")
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	return names
}

// currentUsername resolves the caller's username from the JWT or OS.
func currentUsername() string {
	tokenFile, err := configdir.TokenFile()
	if err != nil {
		return os.Getenv("USER")
	}
	token, err := login.LoadToken(tokenFile)
	if err != nil || token == nil || token.AccessToken == "" {
		return os.Getenv("USER")
	}
	claims, err := decodeJWTClaims(token.AccessToken)
	if err != nil || claims.PreferredUsername == "" {
		return os.Getenv("USER")
	}
	return claims.PreferredUsername
}
