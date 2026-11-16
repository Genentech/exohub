package init

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// legacyMigrationPlan is one legacy remote's conversion into the profile's
// role-split shape: atlas carries over the legacy remote's name/uuid (same
// git-annex special remote). Instance is a brand-new entry to insert (nil
// when the profile defines no role: instance remote for this legacy entry,
// or when one already exists in .exohub/remotes — see ExistingInstance).
type legacyMigrationPlan struct {
	LegacyName string
	Atlas      RemoteConfig
	Instance   *RemoteConfig

	// ExistingInstance is set instead of Instance when .exohub/remotes
	// already has a remote matching the profile's rendered instance entry
	// (same name, or role: instance with data_from naming LegacyName):
	// that existing entry is left untouched rather than duplicated.
	ExistingInstance *RemoteConfig
}

// renderedLegacyMigration renders profile's remotes template for current (a
// legacy remote carrying atlas_url/instance_url) and splits the result into
// a legacyMigrationPlan: the rendered role: atlas entry matching current by
// name keeps current's existing uuid (same annex special remote); the
// rendered role: instance entry, if any, names current as its data_from
// when the profile left data_from empty, so a later sync/publish's
// ResolveDelegationAtlas resolves it without ambiguity. allRemotes is the
// full .exohub/remotes list (current's siblings), consulted so an instance
// entry that already exists there (matching by name or by role: instance +
// data_from naming current) is reported as ExistingInstance instead of
// being inserted again as a duplicate.
func renderedLegacyMigration(spec *ProfileSpec, current RemoteConfig, allRemotes []RemoteConfig) (*legacyMigrationPlan, error) {
	rendered, err := renderedRemotesFor(spec, current)
	if err != nil {
		return nil, err
	}
	if legacyRemoteKeysError(rendered) != nil {
		return nil, fmt.Errorf("profile %q is itself still legacy (atlas_url/instance_url); update the profile before migrating %q", spec.Name, current.Name)
	}

	var atlas *RemoteConfig
	var instance *RemoteConfig
	for i := range rendered {
		r := rendered[i]
		if r.Name == current.Name && r.Type == "artifactdb" && (r.Role == "" || r.Role == "atlas") {
			atlas = &r
			continue
		}
		if r.Type == "artifactdb" && r.Role == "instance" && (r.DataFrom == "" || r.DataFrom == current.Name) {
			instance = &r
		}
	}
	if atlas == nil {
		return nil, fmt.Errorf("profile %q does not render a role: atlas remote named %q", spec.Name, current.Name)
	}
	atlas.UUID = current.UUID
	if instance != nil && instance.DataFrom == "" {
		instance.DataFrom = current.Name
	}

	plan := &legacyMigrationPlan{LegacyName: current.Name, Atlas: *atlas}
	if instance != nil {
		if existing := findExistingInstanceEntry(allRemotes, current.Name, *instance); existing != nil {
			plan.ExistingInstance = existing
		} else {
			plan.Instance = instance
		}
	}
	return plan, nil
}

// findExistingInstanceEntry returns the remote in allRemotes that already
// covers rendered's role: instance entry for legacyName — either the exact
// same name, or any role: instance remote whose data_from names
// legacyName — so rewriteLegacyRemoteInFile never inserts a duplicate.
func findExistingInstanceEntry(allRemotes []RemoteConfig, legacyName string, rendered RemoteConfig) *RemoteConfig {
	for i := range allRemotes {
		r := allRemotes[i]
		if r.Name == rendered.Name {
			return &r
		}
		if r.Type == "artifactdb" && r.Role == "instance" && r.DataFrom == legacyName {
			return &r
		}
	}
	return nil
}

// legacyMigrationNode builds the yaml.Node for a plan's single role-split
// entry (atlas or instance), encoded the same way remoteConfigYAMLFields
// does for mergeRemoteFieldsIntoFile, but as a full new mapping node
// (every non-empty field), not just a patch of specific keys.
func legacyMigrationNode(r RemoteConfig) (*yaml.Node, error) {
	var node yaml.Node
	if err := node.Encode(r); err != nil {
		return nil, fmt.Errorf("encode remote %q: %w", r.Name, err)
	}
	return &node, nil
}

// rewriteLegacyRemoteInFile replaces plan.LegacyName's single mapping node
// in path's remotes list with plan.Atlas's rendered node, inserting
// plan.Instance's node immediately after when non-nil. Every other
// remote/content in the file (comments, quoting, order) is left untouched
// — this edits the yaml.Node document tree in place, the same approach
// mergeRemoteFieldsIntoFile uses for a single-field patch, extended here to
// replace one whole entry with up to two.
func rewriteLegacyRemoteInFile(path string, plan *legacyMigrationPlan) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", path, err)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("failed to parse %s: %w", path, err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("%s: expected a YAML mapping at the top level", path)
	}
	root := doc.Content[0]

	remotesSeq := yamlMappingValue(root, "remotes")
	if remotesSeq == nil || remotesSeq.Kind != yaml.SequenceNode {
		return fmt.Errorf("%s: no 'remotes' list found", path)
	}

	idx := -1
	for i, item := range remotesSeq.Content {
		if item.Kind != yaml.MappingNode {
			continue
		}
		if nameNode := yamlMappingValue(item, "name"); nameNode != nil && nameNode.Value == plan.LegacyName {
			idx = i
			break
		}
	}
	if idx == -1 {
		return fmt.Errorf("%s: remote %q not found", path, plan.LegacyName)
	}

	atlasNode, err := legacyMigrationNode(plan.Atlas)
	if err != nil {
		return err
	}
	replacement := []*yaml.Node{atlasNode}
	if plan.Instance != nil {
		instanceNode, err := legacyMigrationNode(*plan.Instance)
		if err != nil {
			return err
		}
		replacement = append(replacement, instanceNode)
	}

	content := make([]*yaml.Node, 0, len(remotesSeq.Content)+len(replacement)-1)
	content = append(content, remotesSeq.Content[:idx]...)
	content = append(content, replacement...)
	content = append(content, remotesSeq.Content[idx+1:]...)
	remotesSeq.Content = content

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return fmt.Errorf("failed to re-encode %s: %w", path, err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("failed to re-encode %s: %w", path, err)
	}

	return os.WriteFile(path, buf.Bytes(), 0644)
}

// printLegacyMigrationPlan prints the before/after for a legacy→role-split
// migration: diffRemoteConfig-style drift for the (same-name) atlas entry,
// and the new role: instance entry in full, if the profile renders one.
func printLegacyMigrationPlan(current RemoteConfig, plan *legacyMigrationPlan) {
	fmt.Printf("==> Remote %s carries legacy atlas_url/instance_url; migrating to role-split configuration:\n", plan.LegacyName)
	entries := diffRemoteConfig(plan.LegacyName, current, plan.Atlas)
	for _, e := range entries {
		if e.Status == "added" {
			fmt.Printf("  + %s: %s\n", e.Field, e.Profile)
		} else if e.Status == "changed" {
			fmt.Printf("  ~ %s: %s -> %s\n", e.Field, e.Current, e.Profile)
		}
	}
	if plan.Instance != nil {
		fmt.Printf("  + new remote %q (role: instance, not a git-annex special remote):\n", plan.Instance.Name)
		fmt.Printf("      url: %s\n", plan.Instance.URL)
		fmt.Printf("      s3url: %s\n", plan.Instance.S3URL)
		if plan.Instance.DataFrom != "" {
			fmt.Printf("      data_from: %s\n", plan.Instance.DataFrom)
		}
	}
	if plan.ExistingInstance != nil {
		fmt.Printf("  = remote %q already covers the profile's role: instance entry; left untouched\n", plan.ExistingInstance.Name)
	}
}

// runLegacyRemoteMigration converts legacyName from its legacy
// atlas_url/instance_url shape into profileName's role-split rendering:
// the atlas entry keeps legacyName's existing fields/uuid except what the
// profile renders differently, the instance entry (if any) is a new entry
// never initremote'd. Mirrors runProfileRemoteRefresh's diff/confirm/
// --dry-run/--yes/--allow-move flow (exo-cli#197).
func runLegacyRemoteMigration(profileName, legacyName string, remotesConfig *RemotesConfig) error {
	current, found := findRemoteConfig(remotesConfig, legacyName)
	if !found {
		return fmt.Errorf("remote %q not found in .exohub/remotes", legacyName)
	}

	spec, err := FetchProfile(profileName)
	if err != nil {
		return err
	}

	plan, err := renderedLegacyMigration(spec, current, remotesConfig.Remotes)
	if err != nil {
		return err
	}

	printLegacyMigrationPlan(current, plan)

	if err := guardAgainstSilentS3URLMove(legacyName, current, plan.Atlas); err != nil {
		return err
	}

	if flagDryRun {
		if !flagJSON {
			fmt.Println("--dry-run: no changes applied.")
		}
		return nil
	}

	if !flagYes {
		ok, err := askYesNoTUI(fmt.Sprintf("Migrate remote %q off atlas_url/instance_url?", legacyName), false)
		if err != nil {
			return err
		}
		if !ok {
			fmt.Println("Aborted.")
			return nil
		}
	}

	if err := rewriteLegacyRemoteInFile(filepath.Join(".exohub", "remotes"), plan); err != nil {
		return err
	}
	fmt.Printf("📝 Migrated %s to role-split configuration in .exohub/remotes\n", legacyName)

	if err := reconfigureRemote(plan.Atlas); err != nil && !errors.Is(err, errRemoteSkipped) {
		return fmt.Errorf("updated .exohub/remotes but git-annex reconfigure failed: %w", err)
	}

	// A moved/newly-split remote's grants need syncing the same way
	// handleRemoteValidation's existing-repo path does (exo-cli#197): a
	// profile refresh never ran this before. syncGrantsForRemote itself
	// never skips silently (WARNING on every skip branch, grants.go).
	if plan.Atlas.Grants && plan.Atlas.S3URL != "" {
		if err := syncGrantsForRemote(plan.Atlas.S3URL); err != nil {
			return fmt.Errorf("migrated .exohub/remotes but grants sync failed: %w", err)
		}
	}

	if plan.Instance != nil {
		fmt.Printf("✅ Remote %s is role: instance (not a git-annex special remote); nothing to initialize\n", plan.Instance.Name)
	}

	fmt.Printf("✅ Remote %s migrated from profile %q\n", SuccessStyle.Render(legacyName), profileName)
	return nil
}

// legacyMigrationDriftEntries reports plan as driftEntry values, in the
// same shape computeDriftEntries/runProfileDriftReport use for a normal
// profile refresh: diffRemoteConfig's field-level drift for the atlas
// entry, plus the new instance entry (if any) as a single "added" entry.
func legacyMigrationDriftEntries(current RemoteConfig, plan *legacyMigrationPlan) []driftEntry {
	entries := diffRemoteConfig(plan.LegacyName, current, plan.Atlas)
	if plan.Instance != nil {
		entries = append(entries, driftEntry{
			Remote: plan.Instance.Name,
			Status: "added",
			Profile: fmt.Sprintf("role: instance, url: %s, s3url: %s",
				plan.Instance.URL, plan.Instance.S3URL),
		})
	}
	if plan.ExistingInstance != nil {
		entries = append(entries, driftEntry{
			Remote: plan.ExistingInstance.Name,
			Status: "local-only",
			Current: "already covers the profile's role: instance entry; left untouched",
		})
	}
	return entries
}

// runLegacyRemoteMigrationDriftReport is runProfileDriftReport's
// counterpart for a legacy remote: it never writes files, prints the
// legacy→role-split drift as a JSON array, and exits with driftExitCode
// when there's anything to migrate (there always is, migrating off
// atlas_url/instance_url is itself the drift).
func runLegacyRemoteMigrationDriftReport(profileName, legacyName string, remotesConfig *RemotesConfig) error {
	current, found := findRemoteConfig(remotesConfig, legacyName)
	if !found {
		return fmt.Errorf("remote %q not found in .exohub/remotes", legacyName)
	}
	spec, err := FetchProfile(profileName)
	if err != nil {
		return err
	}
	plan, err := renderedLegacyMigration(spec, current, remotesConfig.Remotes)
	if err != nil {
		return err
	}
	return printDriftEntriesJSON(legacyMigrationDriftEntries(current, plan))
}

// legacyRemoteKeysExamplesFromProfile fetches profileName and renders its
// role-split example YAML for each legacy remote in remotes, best-effort:
// a fetch/render failure for any remote simply omits it from the result
// (its refusal falls back to the generic placeholder example), so the
// refusal always still prints — even offline or with a server outage —
// rather than failing a command that's about to fail anyway (exo-cli#197).
func legacyRemoteKeysExamplesFromProfile(profileName string, remotes []RemoteConfig) map[string]string {
	spec, err := FetchProfile(profileName)
	if err != nil {
		return nil
	}
	examples := make(map[string]string)
	for _, r := range remotes {
		if !r.hasLegacyKeys() {
			continue
		}
		plan, err := renderedLegacyMigration(spec, r, remotes)
		if err != nil {
			continue
		}
		examples[r.Name] = legacyMigrationExampleYAML(plan)
	}
	return examples
}

// legacyMigrationExampleYAML renders plan's atlas/instance entries as the
// role-split YAML block shown in the legacy-key refusal, in the same
// "  - name: ...\n    ..." shape RemoteConfig.genericLegacyExample uses.
func legacyMigrationExampleYAML(plan *legacyMigrationPlan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "  - name: %s\n    type: artifactdb\n    role: atlas\n    url: %s\n    s3url: %s\n", plan.Atlas.Name, plan.Atlas.URL, plan.Atlas.S3URL)
	if plan.Instance != nil {
		fmt.Fprintf(&b, "  - name: %s\n    type: artifactdb\n    role: instance\n    url: %s\n    s3url: %s\n", plan.Instance.Name, plan.Instance.URL, plan.Instance.S3URL)
		if plan.Instance.DataFrom != "" {
			fmt.Fprintf(&b, "    data_from: %s\n", plan.Instance.DataFrom)
		}
	}
	return b.String()
}
