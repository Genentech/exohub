package init

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// driftEntry is one field-level difference between a repo's current
// RemoteConfig (as stored in .exohub/remotes) and the value a profile would
// render for the same remote. Status is one of:
//
//	"added"      - the profile defines the field, the repo doesn't have it
//	"changed"    - both define the field, with different values
//	"local-only" - the repo has a value the profile doesn't define; never
//	               cleared, reported for visibility only
//	"unresolved" - the remote's prompts couldn't be re-rendered (no
//	               .exohub/profile.lock and no matching current field);
//	               Profile holds the error describing which answers are missing
type driftEntry struct {
	Remote  string `json:"remote"`
	Field   string `json:"field,omitempty"`
	Current string `json:"current,omitempty"`
	Profile string `json:"profile,omitempty"`
	Status  string `json:"status"`
	Hint    string `json:"hint,omitempty"`
}

// s3URLMoveHint is attached to a "changed" drift entry on the s3url field:
// a changed s3url is a location move (exo-cli#166), never applied by --yes
// alone — only --allow-move confirms it's intentional.
const s3URLMoveHint = "s3url change is a location move (exo-cli#166); requires --allow-move, --yes alone does not apply it"

// findRemoteConfig returns the remote named name in cfg, if present.
func findRemoteConfig(cfg *RemotesConfig, name string) (RemoteConfig, bool) {
	for _, r := range cfg.Remotes {
		if r.Name == name {
			return r, true
		}
	}
	return RemoteConfig{}, false
}

// findRemotesTemplate returns the profile's "remotes" template text, if any.
func findRemotesTemplate(spec *ProfileSpec) (string, bool) {
	for filename, tmpl := range spec.Templates {
		if filepath.Base(filename) == "remotes" {
			return tmpl, true
		}
	}
	return "", false
}

// renderProfileRemotes renders the profile's "remotes" template (if any)
// with data and parses it the same way LoadRemotesConfig does — the server
// may send either {remotes: [...]} or a bare list.
func renderProfileRemotes(spec *ProfileSpec, data profileTplData) ([]RemoteConfig, error) {
	tmplText, ok := findRemotesTemplate(spec)
	if !ok {
		return nil, nil
	}
	content, err := renderTemplate("remotes", tmplText, data)
	if err != nil {
		return nil, err
	}

	var cfg RemotesConfig
	if err := yaml.Unmarshal([]byte(content), &cfg); err == nil && len(cfg.Remotes) > 0 {
		return cfg.Remotes, nil
	}
	var list []RemoteConfig
	if err := yaml.Unmarshal([]byte(content), &list); err != nil {
		return nil, fmt.Errorf("rendered remotes template is not valid YAML: %w", err)
	}
	return list, nil
}

// diffableRemoteFields excludes identity fields that are never re-rendered
// from a profile: the remote's name/type are how it's matched, and its
// git-annex UUID is assigned locally, not templated.
var diffableRemoteFields = map[string]bool{"name": false, "type": false, "uuid": false}

// diffRemoteConfig compares current (from .exohub/remotes) against rendered
// (from the profile) field by field and classifies each difference. Fields
// the profile doesn't define (zero value) are never reported as something
// to change — at most as "local-only" if the repo has a value for them.
func diffRemoteConfig(remoteName string, current, rendered RemoteConfig) []driftEntry {
	var entries []driftEntry
	cv := reflect.ValueOf(current)
	rv := reflect.ValueOf(rendered)
	typ := cv.Type()

	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("yaml")
		name := strings.Split(tag, ",")[0]
		if name == "" || name == "-" {
			continue
		}
		if allow, known := diffableRemoteFields[name]; known && !allow {
			continue
		}

		cf := cv.Field(i)
		rf := rv.Field(i)
		if !cf.CanInterface() {
			continue
		}

		curZero := isZeroFieldValue(cf)
		renZero := isZeroFieldValue(rf)
		curStr := fieldValueString(cf)
		renStr := fieldValueString(rf)

		switch {
		case renZero && !curZero:
			entries = append(entries, driftEntry{Remote: remoteName, Field: name, Current: curStr, Status: "local-only"})
		case renZero:
			// Neither side defines it.
		case curZero:
			entries = append(entries, driftEntry{Remote: remoteName, Field: name, Profile: renStr, Status: "added"})
		case curStr != renStr:
			entry := driftEntry{Remote: remoteName, Field: name, Current: curStr, Profile: renStr, Status: "changed"}
			if name == "s3url" {
				entry.Hint = s3URLMoveHint
			}
			entries = append(entries, entry)
		}
	}
	return entries
}

func isZeroFieldValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.String:
		return v.String() == ""
	case reflect.Bool:
		return !v.Bool()
	case reflect.Slice, reflect.Map:
		return v.Len() == 0
	default:
		return v.IsZero()
	}
}

func fieldValueString(v reflect.Value) string {
	switch v.Kind() {
	case reflect.Bool:
		return strconv.FormatBool(v.Bool())
	case reflect.Slice:
		if v.Len() == 0 {
			return ""
		}
		parts := make([]string, v.Len())
		for i := 0; i < v.Len(); i++ {
			parts[i] = fmt.Sprint(v.Index(i).Interface())
		}
		return strings.Join(parts, ",")
	case reflect.Map:
		if v.Len() == 0 {
			return ""
		}
		keys := v.MapKeys()
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%v=%v", k.Interface(), v.MapIndex(k).Interface()))
		}
		return strings.Join(parts, ",")
	default:
		return fmt.Sprint(v.Interface())
	}
}

// remoteConfigFieldMap returns r's non-empty fields keyed by yaml tag name,
// used to reverse-match prompt answers against an already-configured
// remote when no .exohub/profile.lock exists.
func remoteConfigFieldMap(r RemoteConfig) map[string]string {
	out := make(map[string]string)
	v := reflect.ValueOf(r)
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("yaml")
		name := strings.Split(tag, ",")[0]
		if name == "" || name == "-" {
			continue
		}
		if s := fieldValueString(v.Field(i)); s != "" {
			out[name] = s
		}
	}
	return out
}

// resolveRefreshAnswers builds the prompt answers needed to re-render spec's
// templates for current, without re-prompting the user. In order:
//  1. --set key=value overrides.
//  2. .exohub/profile.lock answers, if present and written for this profile.
//  3. Reverse-matched from current's non-empty fields, when a prompt's key
//     happens to be a RemoteConfig yaml field name.
//
// A prompt that can't be resolved by any of these is an error naming
// exactly which keys are missing — never silently guessed.
func resolveRefreshAnswers(spec *ProfileSpec, current RemoteConfig) (map[string]interface{}, error) {
	answers := make(map[string]interface{})
	_, remainingSets := explicitFolderOverride()
	if len(remainingSets) > 0 {
		if err := applySetFlags(spec, answers, remainingSets); err != nil {
			return nil, err
		}
	}

	if lock, err := LoadProfileLock(); err == nil && lock != nil && lock.Profile == spec.Name {
		for k, v := range lock.Answers {
			if _, already := answers[k]; !already {
				answers[k] = v
			}
		}
	}

	remoteFields := remoteConfigFieldMap(current)
	var missing []string
	for _, p := range spec.Prompts {
		if _, already := answers[p.Name]; already {
			continue
		}
		if v, ok := remoteFields[p.Name]; ok {
			answers[p.Name] = v
			continue
		}
		missing = append(missing, p.Name)
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("profile %q has prompts that cannot be resolved for remote %q without .exohub/profile.lock: %s (pass them with --set key=value)",
			spec.Name, current.Name, strings.Join(missing, ", "))
	}
	return answers, nil
}

// applyRemoteFieldDiff returns current with only the fields named in
// entries replaced by rendered's value. Fields not in entries (equal or
// local-only) are left untouched.
func applyRemoteFieldDiff(current, rendered RemoteConfig, entries []driftEntry) RemoteConfig {
	merged := current
	changed := make(map[string]bool, len(entries))
	for _, e := range entries {
		changed[e.Field] = true
	}

	rv := reflect.ValueOf(rendered)
	mv := reflect.ValueOf(&merged).Elem()
	typ := rv.Type()
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("yaml")
		name := strings.Split(tag, ",")[0]
		if !changed[name] {
			continue
		}
		mv.Field(i).Set(rv.Field(i))
	}
	return merged
}

// renderedRemoteFor fetches profileName and renders its "remotes" template
// for the given current remote, resolving answers per resolveRefreshAnswers.
// It returns the rendered RemoteConfig entry matching remoteName.
func renderedRemoteFor(spec *ProfileSpec, current RemoteConfig) (RemoteConfig, error) {
	answers, err := resolveRefreshAnswers(spec, current)
	if err != nil {
		return RemoteConfig{}, err
	}

	unixID := resolveUnixID()
	if locked, ok := resolveUnixIDFromLock(spec.Name); ok {
		unixID = locked
	}
	cwd, err := os.Getwd()
	if err != nil {
		return RemoteConfig{}, fmt.Errorf("could not determine working directory: %w", err)
	}
	explicitFolder, _ := explicitFolderOverride()
	existing := &RemotesConfig{Remotes: []RemoteConfig{current}}
	folder, folderSource := resolveFolder(explicitFolder, spec, unixID, answers, existing, cwd)
	warnIfFolderDiffersFromDirectory(folder, folderSource, cwd)
	data := buildTplData(spec, unixID, folder, resolveEnv(), answers)

	renderedList, err := renderProfileRemotes(spec, data)
	if err != nil {
		return RemoteConfig{}, err
	}
	for _, r := range renderedList {
		if r.Name == current.Name {
			return r, nil
		}
	}
	return RemoteConfig{}, fmt.Errorf("profile %q does not define a remote named %q", spec.Name, current.Name)
}

// changedEntries filters entries down to the ones that represent an actual
// drift to apply ("added"/"changed"), excluding "local-only"/"unresolved".
func changedEntries(entries []driftEntry) []driftEntry {
	var out []driftEntry
	for _, e := range entries {
		if e.Status == "added" || e.Status == "changed" {
			out = append(out, e)
		}
	}
	return out
}

// guardAgainstSilentS3URLMove refuses a rendered s3url that differs from a
// remote's current one: physically moving a dataset's content is exo-cli#166's
// job, not a side effect of a mis-resolved .Folder. --yes alone must never
// approve this — only --allow-move, which routes the actual move through
// exo-cli#166's s3url-move handling (reconfigureRemote/
// ensureRemoteConfigS3URL). A first-time s3url (current empty) is not a
// move and is left untouched.
func guardAgainstSilentS3URLMove(remoteName string, current, rendered RemoteConfig) error {
	if current.S3URL == "" || rendered.S3URL == "" || current.S3URL == rendered.S3URL {
		return nil
	}
	if flagAllowMove {
		return nil
	}
	return fmt.Errorf("remote %s: s3url would change %s → %s; to move it intentionally re-run with --allow-move (see exo-cli#166)",
		remoteName, current.S3URL, rendered.S3URL)
}

// runProfileRemoteRefresh re-renders remoteName from profileName and, after
// confirmation (or --yes), updates only the changed/added fields in
// .exohub/remotes and in git-annex's remote config — leaving every other
// remote and field untouched (exo-cli#164).
func runProfileRemoteRefresh(profileName, remoteName string, remotesConfig *RemotesConfig) error {
	current, found := findRemoteConfig(remotesConfig, remoteName)
	if !found {
		return fmt.Errorf("remote %q not found in .exohub/remotes", remoteName)
	}

	spec, err := FetchProfile(profileName)
	if err != nil {
		return err
	}

	rendered, err := renderedRemoteFor(spec, current)
	if err != nil {
		return err
	}

	entries := changedEntries(diffRemoteConfig(remoteName, current, rendered))
	if len(entries) == 0 {
		fmt.Printf("✅ Remote %s is up to date with profile %q\n", SuccessStyle.Render(remoteName), profileName)
		return nil
	}

	fmt.Printf("==> Remote %s drift from profile %q:\n", remoteName, profileName)
	for _, e := range entries {
		if e.Status == "added" {
			fmt.Printf("  + %s: %s\n", e.Field, e.Profile)
		} else {
			fmt.Printf("  ~ %s: %s -> %s\n", e.Field, e.Current, e.Profile)
		}
	}

	if err := guardAgainstSilentS3URLMove(remoteName, current, rendered); err != nil {
		return err
	}

	merged := applyRemoteFieldDiff(current, rendered, entries)

	// --dry-run must change nothing: no .exohub/remotes write, no git-annex
	// reconfigure, no content migration -- only the plan (exo-cli#168).
	if flagDryRun {
		isMove := current.S3URL != "" && rendered.S3URL != "" && current.S3URL != rendered.S3URL
		if isMove && flagMigrateContent {
			if err := migrateRemoteContent(merged, current.S3URL, rendered.S3URL, true, flagJSON); err != nil {
				return err
			}
		}
		if !flagJSON {
			fmt.Println("--dry-run: no changes applied.")
		}
		return nil
	}

	if !flagYes {
		ok, err := askYesNoTUI(fmt.Sprintf("Apply this refresh to remote %q?", remoteName), false)
		if err != nil {
			return err
		}
		if !ok {
			fmt.Println("Aborted.")
			return nil
		}
	}

	if err := mergeRemoteFieldsIntoFile(filepath.Join(".exohub", "remotes"), remoteName, rendered, entries); err != nil {
		return err
	}
	fmt.Printf("📝 Updated %s in .exohub/remotes\n", remoteName)

	if err := reconfigureRemote(merged); err != nil && !errors.Is(err, errRemoteSkipped) {
		return fmt.Errorf("updated .exohub/remotes but git-annex reconfigure failed: %w", err)
	}
	fmt.Printf("✅ Remote %s refreshed from profile %q\n", SuccessStyle.Render(remoteName), profileName)
	return nil
}
