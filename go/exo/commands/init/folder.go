package init

import (
	"fmt"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Sources resolveFolder/resolveFolderForRefresh can report, used in
// user-facing messages.
const (
	folderSourceOverride       = "explicit override"
	folderSourceLock           = ".exohub/profile.lock"
	folderSourceExistingRemote = "existing remote in .exohub/remotes"
	folderSourceGitOrigin      = "git origin remote"
	folderSourceDirectory      = "current directory name"
)

// resolveFolder resolves the .Folder template variable from the repository's
// identity rather than the local checkout directory name, in priority
// order:
//
//  1. explicit - an explicit --folder or --set Folder=<name> override.
//  2. the Folder recorded in .exohub/profile.lock, if present for this
//     profile — the authoritative, persisted decision from a prior
//     `exo init --profile` run (exo-cli#164, exo-cli#167).
//  3. the dataset name already baked into an existing remote's rendered
//     fields (e.g. its s3url), recovered by re-rendering the profile's
//     "remotes" template with a sentinel in place of .Folder and solving
//     for the substring the sentinel maps to — for repos initialized
//     before profile.lock recorded Folder (existing may be nil, e.g. a
//     brand-new repo with no .exohub/remotes yet).
//  4. the git `origin` remote's repository name (last path segment, `.git`
//     stripped).
//  5. the current directory's base name, with a warning — used only when
//     none of the above can confirm the repo's identity.
//
// This same order applies to both a full `exo init --profile` (where
// existing is the repo's current .exohub/remotes, if any, e.g. on a
// --force re-apply) and a `--remote` refresh / `--dry-run --json` drift
// report (where existing wraps just the one remote being refreshed).
func resolveFolder(explicit string, spec *ProfileSpec, unixID string, answers map[string]interface{}, existing *RemotesConfig, cwd string) (folder, source string) {
	if explicit != "" {
		return explicit, folderSourceOverride
	}

	if lock, err := LoadProfileLock(); err == nil && lock != nil && lock.Profile == spec.Name && lock.Folder != "" {
		return lock.Folder, folderSourceLock
	}

	if existing != nil {
		for _, remote := range existing.Remotes {
			if name, ok := folderFromExistingRemote(spec, unixID, answers, remote); ok {
				return name, folderSourceExistingRemote
			}
		}
	}

	if name, ok := folderFromGitOrigin(); ok {
		return name, folderSourceGitOrigin
	}

	return filepath.Base(cwd), folderSourceDirectory
}

// folderFromGitOrigin returns the git origin remote's repository name (last
// path segment, ".git" stripped), or false if there is no origin remote or
// its URL can't be parsed.
func folderFromGitOrigin() (string, bool) {
	out, err := command("git", "remote", "get-url", "origin").Output()
	if err != nil {
		return "", false
	}
	rawURL := strings.TrimSpace(string(out))
	if rawURL == "" {
		return "", false
	}
	_, _, repo, err := ParseRepoURL(rawURL)
	if err != nil || repo == "" {
		return "", false
	}
	return repo, true
}

// folderFromExistingRemote recovers the .Folder value already baked into
// current's rendered fields by rendering the profile's "remotes" template
// with a unique sentinel substituted for .Folder, then solving for the
// substring of current's actual value that the sentinel's position maps to.
// Returns false if the profile has no "remotes" template, current isn't in
// it, or none of its rendered fields are Folder-dependent.
//
// unixID and answers fill in every other template variable as best-known
// (locked/live UnixID, locked/reverse-matched prompt answers); the match
// requires exact prefix/suffix equality against current's actual value, so
// if either is wrong (e.g. a profile also templates .UnixID into the same
// field, and the live/locked UnixID isn't the one the remote was originally
// rendered with) the match simply fails closed and falls through to the
// next resolution source — it never recovers an incorrect Folder.
func folderFromExistingRemote(spec *ProfileSpec, unixID string, answers map[string]interface{}, current RemoteConfig) (string, bool) {
	tmplText, ok := findRemotesTemplate(spec)
	if !ok {
		return "", false
	}

	const sentinel = "exo-folder-sentinel-pBjK2qYwZx"
	data := buildTplData(spec, unixID, sentinel, resolveEnv(), answers)
	content, err := renderTemplate("remotes", tmplText, data)
	if err != nil {
		return "", false
	}

	var list []RemoteConfig
	var cfg RemotesConfig
	if err := yaml.Unmarshal([]byte(content), &cfg); err == nil && len(cfg.Remotes) > 0 {
		list = cfg.Remotes
	} else if err := yaml.Unmarshal([]byte(content), &list); err != nil {
		return "", false
	}

	for _, rendered := range list {
		if rendered.Name != current.Name {
			continue
		}
		if folder, ok := recoverSentinelValue(rendered.S3URL, current.S3URL, sentinel); ok {
			return folder, true
		}
	}
	return "", false
}

// recoverSentinelValue returns the substring of actual that corresponds to
// sentinel's position in template, if template's surrounding prefix/suffix
// both match actual exactly. Returns false if sentinel isn't in template,
// actual doesn't match the surrounding text, or the recovered value is
// empty.
func recoverSentinelValue(template, actual, sentinel string) (string, bool) {
	idx := strings.Index(template, sentinel)
	if idx < 0 {
		return "", false
	}
	prefix, suffix := template[:idx], template[idx+len(sentinel):]
	if !strings.HasPrefix(actual, prefix) || !strings.HasSuffix(actual, suffix) {
		return "", false
	}
	if len(actual) < len(prefix)+len(suffix) {
		return "", false
	}
	value := actual[len(prefix) : len(actual)-len(suffix)]
	if value == "" {
		return "", false
	}
	return value, true
}

// resolveUnixIDFromLock returns the UnixID recorded in .exohub/profile.lock
// for specName, if present. A shared remote's rendered prefix must not
// change depending on which user happens to run a refresh or drift check.
func resolveUnixIDFromLock(specName string) (string, bool) {
	lock, err := LoadProfileLock()
	if err != nil || lock == nil || lock.Profile != specName || lock.UnixID == "" {
		return "", false
	}
	return lock.UnixID, true
}

// answersFromLock returns the prompt answers recorded in .exohub/profile.lock
// for specName, if present, or an empty map otherwise. Used as a best-effort
// fill-in for folderFromExistingRemote's reverse-match before a full `exo
// init --profile` run's own prompts have executed (e.g. during a --force
// re-apply on an already-initialized repo).
func answersFromLock(specName string) map[string]interface{} {
	lock, err := LoadProfileLock()
	if err != nil || lock == nil || lock.Profile != specName {
		return map[string]interface{}{}
	}
	return lock.Answers
}

// explicitFolderOverride returns an explicit .Folder override from --folder
// or --set Folder=<name>, plus the remaining --set entries with any
// Folder=... stripped out. Folder is a built-in template variable, not a
// profile prompt, so it must never reach applySetFlags (which rejects any
// key not declared in the profile's prompts).
func explicitFolderOverride() (folder string, remainingSets []string) {
	folder = flagFolder
	for _, kv := range flagSet {
		key, value, found := strings.Cut(kv, "=")
		if found && key == "Folder" {
			if folder == "" {
				folder = value
			}
			continue
		}
		remainingSets = append(remainingSets, kv)
	}
	return folder, remainingSets
}

// warnIfFolderDiffersFromDirectory prints a notice when the resolved Folder
// differs from the current directory's base name, so the user/automation
// knows the repo's identity (not the cwd) decided the rendered prefix.
func warnIfFolderDiffersFromDirectory(folder, source, cwd string) {
	dirFolder := filepath.Base(cwd)
	if folder == dirFolder {
		return
	}
	fmt.Printf("ℹ️  Using dataset name %q (from %s), which differs from the current directory %q\n", folder, source, dirFolder)
}
