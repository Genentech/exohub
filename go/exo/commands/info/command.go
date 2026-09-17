package info

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/Genentech/exohub/go/exo/commandutil"
	"github.com/Genentech/exohub/go/exo/commands/clone"
	"github.com/Genentech/exohub/go/exo/palette"
)

var command = commandutil.Command

const longText = `Show git-annex repository info.

Displays repository statistics, remote locations, and annex state.
Use --json for machine-readable output, --deep to run a full (non-fast) scan.`

func NewCommand() *cobra.Command {
	var remote string
	var jsonOut bool
	var deep bool

	rootCmd := &cobra.Command{
		Use:   "info",
		Short: "Show git-annex repository info",
		Long:  longText,
		Run: func(cmd *cobra.Command, args []string) {
			if len(args) > 0 {
				_ = cmd.Help()
				os.Exit(1)
			}
			runInfo(remote, jsonOut, deep)
		},
	}

	rootCmd.Flags().StringVar(&remote, "remote", "", "Annex remote name to query")
	rootCmd.Flags().BoolVar(&jsonOut, "json", false, "Output raw JSON instead of friendly text")
	rootCmd.Flags().BoolVar(&deep, "deep", false, "Run full info (no -F fast mode)")

	return rootCmd
}

func runInfo(remote string, jsonOut, deep bool) {
	// Load clone metadata first
	cloneMetadata, _ := clone.LoadCloneMetadata(".")

	// For JSON output, include clone preset info
	if jsonOut {
		args := infoArgs(remote, deep)
		out, err := runCommandCombinedOutput(args)
		if err != nil {
			if strings.TrimSpace(out) != "" {
				fmt.Fprintln(os.Stderr, strings.TrimSpace(out))
			}
			exitOnError(err)
		}
		_, _, details, err := parseInfoPayload(out)
		if err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}

		// Add preferred content to details if querying specific remote
		if remote != "" {
			if pc := getPreferredContent(remote); pc != "" && pc != "standard" {
				details["preferred content"] = pc
			}
		}

		// Add clone preset metadata to JSON output (always include all fields)
		if cloneMetadata != nil {
			clonePresetInfo := map[string]any{
				"preset":       cloneMetadata.Preset,
				"cloned_at":    cloneMetadata.ClonedAt,
				"repo_url":     cloneMetadata.RepoURL,
				"commit":       cloneMetadata.Commit,
				"branch":       cloneMetadata.Branch,
				"sparse_paths": []string{}, // Placeholder - load from current preset
				"git_config":   map[string]string{},
			}

			// Load current preset configuration to get sparse_paths and git_config
			presetsConfig, err := clone.LoadPresetsConfig(".")
			if err == nil && presetsConfig != nil {
				currentPreset := presetsConfig.GetPresetByName(cloneMetadata.Preset)
				if currentPreset != nil {
					clonePresetInfo["sparse_paths"] = currentPreset.SparsePaths
					if currentPreset.SparsePaths == nil {
						clonePresetInfo["sparse_paths"] = []string{}
					}
					clonePresetInfo["git_config"] = currentPreset.GitConfig
					if currentPreset.GitConfig == nil {
						clonePresetInfo["git_config"] = map[string]string{}
					}
				}
			}

			details["clone_preset"] = clonePresetInfo
		}

		// Add annex config to JSON output
		annexCfg := getAnnexConfig()
		details["annex_config"] = map[string]any{
			"addunlocked":   annexCfg.addUnlocked,
			"thin":          annexCfg.thin,
			"gitattributes": annexCfg.hasGitattributes,
		}

		// Re-marshal with preferred content and clone preset
		filteredJSON, err := marshalJSON(details)
		if err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
		fmt.Fprintln(os.Stdout, filteredJSON)
		return
	}

	// Show repo URL
	displayRepoURL(os.Stdout)

	// Text output - show clone preset info first if available
	if cloneMetadata != nil {
		displayClonePresetInfoText(os.Stdout, cloneMetadata)
		fmt.Fprintln(os.Stdout) // Blank line before annex info
	}

	// Show annex config section
	annexCfg := getAnnexConfig()
	displayAnnexConfigText(os.Stdout, annexCfg)
	fmt.Fprintln(os.Stdout)

	args := infoArgs(remote, deep)
	out, err := runCommandCombinedOutput(args)
	if err != nil {
		if strings.TrimSpace(out) != "" {
			fmt.Fprintln(os.Stderr, strings.TrimSpace(out))
		}
		exitOnError(err)
	}
	_, payload, details, err := parseInfoPayload(out)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}

	// Add preferred content to details if querying specific remote
	if remote != "" {
		if pc := getPreferredContent(remote); pc != "" && pc != "standard" {
			details["preferred content"] = pc
		}
	}

	configured := configuredRemotes()
	writeFriendly(os.Stdout, payload, details, configured, remote, deep)
}

func infoArgs(remote string, deep bool) []string {
	args := []string{"git", "annex", "info"}
	if remote != "" {
		args = append(args, remote)
	}
	if !deep {
		args = append(args, "-F")
	}
	args = append(args, "--json")
	return args
}

func writeFriendly(w io.Writer, payload infoPayload, details map[string]any, configured map[string]string, remote string, deep bool) {
	mode := "fast"
	if deep {
		mode = "deep"
	}
	headerStyle := lipgloss.NewStyle().Foreground(palette.Current().Accent.Adaptive()).Bold(true)
	header := "Annex info"
	if remote != "" {
		header = fmt.Sprintf("%s for %s", header, remote)
	}
	fmt.Fprintf(w, "%s\n", headerStyle.Render(fmt.Sprintf("%s (%s)", header, mode)))

	sections := buildRepoSections(payload, configured)
	if len(sections) == 0 {
		printDetails(w, details)
		return
	}

	// Get preferred content and remote types for configured remotes (for general info view only)
	// Only check remotes that are actually in .exohub/remotes
	var preferredContents map[string]string
	var remoteTypes map[string]string
	if remote == "" {
		exohubRemotes := loadExohubRemotes()
		if exohubRemotes != nil {
			preferredContents = make(map[string]string)
			remoteTypes = exohubRemotes // Map of name -> type
			for _, section := range sections {
				if section.title == "Configured remotes" {
					for _, row := range section.rows {
						// Only check preferred content for remotes in .exohub/remotes
						if _, ok := exohubRemotes[row.Name]; ok {
							if pc := getPreferredContent(row.Name); pc != "" && pc != "standard" {
								preferredContents[row.Name] = pc
							}
						}
					}
				}
			}
		}
	}

	fmt.Fprintln(w, "Remotes:")
	legendStyle := lipgloss.NewStyle().Foreground(palette.Current().Dim.Adaptive())
	legendStar := lipgloss.NewStyle().Foreground(palette.Current().Highlight.Adaptive()).Render("*")
	legendHeart := lipgloss.NewStyle().Foreground(palette.Current().Accent.Adaptive()).Render("♥")
	legendWarning := lipgloss.NewStyle().Foreground(palette.Current().Warning.Adaptive()).Render("⚠")

	// Detect duplicate UUIDs across all sections
	duplicateUUIDs := findDuplicateUUIDs(sections)

	fmt.Fprintln(w, legendStyle.Render(fmt.Sprintf("  %s indicates here", legendStar)))
	if len(preferredContents) > 0 {
		fmt.Fprintln(w, legendStyle.Render(fmt.Sprintf("  %s indicates preferred content configured", legendHeart)))
	}
	if len(duplicateUUIDs) > 0 {
		fmt.Fprintln(w, legendStyle.Render(fmt.Sprintf("  %s indicates duplicate remote name", legendWarning)))
	}

	for _, section := range sections {
		fmt.Fprintf(w, "\n%s\n", section.title)
		dimRows := section.title == "Other repositories"
		includeUUID := true // Always show UUID column for both sections
		fmt.Fprintln(w, renderTable(section.rows, dimRows, includeUUID, preferredContents, duplicateUUIDs, remoteTypes))
	}
}

func findDuplicateUUIDs(sections []repoSection) map[string]bool {
	nameCounts := make(map[string]int)
	nameToRows := make(map[string][]repoRow)

	// Count occurrences of each name across all sections
	for _, section := range sections {
		for _, row := range section.rows {
			if row.Name != "" && row.Name != "web" { // Ignore empty and web
				nameCounts[row.Name]++
				nameToRows[row.Name] = append(nameToRows[row.Name], row)
			}
		}
	}

	// Build set of UUIDs that belong to duplicate names
	duplicateUUIDs := make(map[string]bool)
	for name, count := range nameCounts {
		if count > 1 {
			// Mark all UUIDs with this duplicate name
			for _, row := range nameToRows[name] {
				duplicateUUIDs[row.UUID] = true
			}
		}
	}

	return duplicateUUIDs
}

func runCommand(args []string) error {
	cmd := command(args[0], args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func runCommandCombinedOutput(args []string) (string, error) {
	cmd := command(args[0], args[1:]...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func exitOnError(err error) {
	if err == nil {
		return
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		os.Exit(exitErr.ExitCode())
	}
	fmt.Fprintln(os.Stderr, err.Error())
	os.Exit(1)
}

const bittorrentUUID = "00000000-0000-0000-0000-000000000002"

type repoEntry struct {
	UUID        string `json:"uuid"`
	Description string `json:"description"`
	Here        bool   `json:"here"`
}

type infoPayload struct {
	Trusted     []repoEntry `json:"trusted repositories"`
	SemiTrusted []repoEntry `json:"semitrusted repositories"`
	Untrusted   []repoEntry `json:"untrusted repositories"`
}

type repoRow struct {
	Name       string
	UUID       string
	Trust      string
	Here       bool
	Configured bool
}

type repoSection struct {
	title string
	rows  []repoRow
}

func parseInfoPayload(output string) (string, infoPayload, map[string]any, error) {
	lines, err := extractJSONLines(output)
	if err != nil {
		return "", infoPayload{}, nil, err
	}
	if len(lines) == 0 {
		return "", infoPayload{}, nil, fmt.Errorf("git annex info produced no JSON output")
	}
	primary := lines[len(lines)-1]
	var payload infoPayload
	if err := json.Unmarshal([]byte(primary), &payload); err != nil {
		return "", infoPayload{}, nil, fmt.Errorf("failed to parse git annex info output: %w", err)
	}
	filtered := filterPayload(payload)
	filteredMap, err := filterJSONMap(primary)
	if err != nil {
		return "", infoPayload{}, nil, err
	}
	infoLines, err := collectInfoLines(lines[:len(lines)-1])
	if err != nil {
		return "", infoPayload{}, nil, err
	}
	if len(infoLines) > 0 {
		filteredMap["remote_info"] = infoLines
	}
	filteredJSON, err := marshalJSON(filteredMap)
	if err != nil {
		return "", infoPayload{}, nil, err
	}
	return filteredJSON, filtered, filteredMap, nil
}

func filterPayload(payload infoPayload) infoPayload {
	payload.Trusted = filterRepoEntries(payload.Trusted)
	payload.SemiTrusted = filterRepoEntries(payload.SemiTrusted)
	payload.Untrusted = filterRepoEntries(payload.Untrusted)
	return payload
}

func filterRepoEntries(entries []repoEntry) []repoEntry {
	filtered := entries[:0]
	for _, entry := range entries {
		if entry.UUID == bittorrentUUID {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

func filterJSONMap(output string) (map[string]any, error) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		return nil, fmt.Errorf("failed to parse git annex info output: %w", err)
	}
	repoKeys := []string{
		"trusted repositories",
		"semitrusted repositories",
		"untrusted repositories",
	}
	for _, key := range repoKeys {
		list, ok := payload[key].([]any)
		if !ok {
			continue
		}
		filtered := make([]any, 0, len(list))
		for _, item := range list {
			entry, ok := item.(map[string]any)
			if !ok {
				filtered = append(filtered, item)
				continue
			}
			uuid, _ := entry["uuid"].(string)
			if uuid == bittorrentUUID {
				continue
			}
			filtered = append(filtered, entry)
		}
		payload[key] = filtered
	}
	return payload, nil
}

func extractJSONLines(output string) ([]string, error) {
	lines := strings.Split(output, "\n")
	var jsonLines []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "{") {
			jsonLines = append(jsonLines, line)
		}
	}
	if len(jsonLines) > 0 {
		return jsonLines, nil
	}
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return nil, fmt.Errorf("git annex info produced no output")
	}
	return nil, fmt.Errorf("failed to parse git annex info output: %s", trimmed)
}

func buildRepoSections(payload infoPayload, configured map[string]string) []repoSection {
	var configuredRows []repoRow
	var other []repoRow
	addRows := func(entries []repoEntry, trust string) {
		for _, entry := range entries {
			name, configuredName, here := parseRepoName(entry.Description, entry.Here)
			if configured != nil {
				if configuredRemote, ok := configured[entry.UUID]; ok && configuredRemote != "" {
					name = configuredRemote
					configuredName = true
				}
			}
			if name == "" {
				name = entry.UUID
			}
			row := repoRow{
				Name:       name,
				UUID:       entry.UUID,
				Trust:      trust,
				Here:       here,
				Configured: configuredName,
			}
			if row.Configured {
				configuredRows = append(configuredRows, row)
			} else {
				other = append(other, row)
			}
		}
	}
	addRows(payload.Trusted, "trusted")
	addRows(payload.SemiTrusted, "semi")
	addRows(payload.Untrusted, "no")

	sortRows := func(rows []repoRow) {
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].Here != rows[j].Here {
				return rows[i].Here
			}
			return strings.ToLower(rows[i].Name) < strings.ToLower(rows[j].Name)
		})
	}
	sortRows(configuredRows)
	sortRows(other)

	var sections []repoSection
	if len(configuredRows) > 0 {
		sections = append(sections, repoSection{title: "Configured remotes", rows: configuredRows})
	}
	if len(other) > 0 {
		sections = append(sections, repoSection{title: "Other repositories", rows: other})
	}
	return sections
}

func parseRepoName(desc string, here bool) (string, bool, bool) {
	trimmed := strings.TrimSpace(desc)
	if strings.HasSuffix(trimmed, " [here]") {
		trimmed = strings.TrimSpace(strings.TrimSuffix(trimmed, " [here]"))
		here = true
	}
	if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
		name := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, "["), "]"))
		return name, true, here
	}
	return trimmed, false, here
}

func configuredRemotes() map[string]string {
	out, err := runCommandCombinedOutput([]string{"git", "config", "--get-regexp", "^remote\\..*\\.annex-uuid$"})
	if err != nil {
		return nil
	}
	lines := strings.Split(out, "\n")
	remotes := make(map[string]string)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		key := parts[0]
		uuid := parts[1]
		name := parseRemoteNameFromKey(key)
		if name == "" || uuid == "" {
			continue
		}
		if _, exists := remotes[uuid]; !exists {
			remotes[uuid] = name
		}
	}
	if len(remotes) == 0 {
		return nil
	}
	return remotes
}

func parseRemoteNameFromKey(key string) string {
	if !strings.HasPrefix(key, "remote.") || !strings.HasSuffix(key, ".annex-uuid") {
		return ""
	}
	trimmed := strings.TrimSuffix(strings.TrimPrefix(key, "remote."), ".annex-uuid")
	return strings.TrimSpace(trimmed)
}

func getPreferredContent(remote string) string {
	cmd := command("git", "annex", "wanted", remote)
	out, err := cmd.Output() // Only capture stdout, ignore stderr
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// remoteConfig represents a remote in .exohub/remotes
type remoteConfig struct {
	Name string `yaml:"name"`
	Type string `yaml:"type"`
}

// remotesConfigFile represents .exohub/remotes file structure
type remotesConfigFile struct {
	Remotes []remoteConfig `yaml:"remotes"`
}

// loadExohubRemotes reads .exohub/remotes and returns a map of remote name -> type
func loadExohubRemotes() map[string]string {
	path := filepath.Join(".exohub", "remotes")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var config remotesConfigFile
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil
	}

	remotes := make(map[string]string)
	for _, r := range config.Remotes {
		remotes[r.Name] = r.Type
	}
	return remotes
}

// formatRemoteType returns a formatted type string with emoji and name, aligned for display.
// Single emojis (📦, 📁) get an extra space to align with two-char emojis (🔴↑, 🟢↓).
func formatRemoteType(remoteType string) string {
	emoji := commandutil.RemoteTypeEmoji(remoteType)
	if emoji == "  " {
		return ""
	}
	switch remoteType {
	case "export":
		return emoji + "↑ " + remoteType
	case "import":
		return emoji + "↓ " + remoteType
	case "artifactdb", "drive":
		return emoji + "↑ " + remoteType
	default:
		return emoji + "  " + remoteType
	}
}

func printDetails(w io.Writer, details map[string]any) {
	if len(details) == 0 {
		fmt.Fprintln(w, "No repository details found.")
		return
	}
	fmt.Fprintln(w, "Details:")
	keys := make([]string, 0, len(details))
	for key := range details {
		if isRepoKey(key) {
			continue
		}
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		fmt.Fprintln(w, "  (no additional details)")
		return
	}
	sort.Strings(keys)
	fieldStyle := lipgloss.NewStyle().Foreground(palette.Current().Label.Adaptive()).Bold(true)
	for _, key := range keys {
		if key == "remote_info" {
			lines := formatInfoLines(details[key])
			if len(lines) == 0 {
				continue
			}
			fmt.Fprintf(w, "  %s:\n", fieldStyle.Render(key))
			for _, line := range lines {
				fmt.Fprintf(w, "    - %s\n", renderInfoLine(line, fieldStyle))
			}
			continue
		}
		value := formatDetailValue(details[key])
		fmt.Fprintf(w, "  %s: %s\n", fieldStyle.Render(key), value)
	}
}

func formatDetailValue(val any) string {
	switch typed := val.(type) {
	case string:
		return typed
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case float64:
		if typed == float64(int64(typed)) {
			return fmt.Sprintf("%d", int64(typed))
		}
		return fmt.Sprintf("%v", typed)
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprintf("%v", typed)
		}
		return string(data)
	}
}

func formatInfoLines(val any) []string {
	switch typed := val.(type) {
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if str, ok := item.(string); ok && strings.TrimSpace(str) != "" {
				out = append(out, str)
			}
		}
		return out
	case []string:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if strings.TrimSpace(item) != "" {
				out = append(out, item)
			}
		}
		return out
	default:
		return nil
	}
}

func renderInfoLine(line string, keyStyle lipgloss.Style) string {
	parts := strings.SplitN(line, ":", 2)
	if len(parts) != 2 {
		return line
	}
	key := strings.TrimSpace(parts[0])
	value := strings.TrimSpace(parts[1])
	if key == "" {
		return line
	}
	if value == "" {
		return keyStyle.Render(key) + ":"
	}
	return fmt.Sprintf("%s: %s", keyStyle.Render(key), value)
}

func isRepoKey(key string) bool {
	switch key {
	case "trusted repositories", "semitrusted repositories", "untrusted repositories":
		return true
	default:
		return false
	}
}

func collectInfoLines(lines []string) ([]string, error) {
	var infoLines []string
	for _, line := range lines {
		var payload map[string]any
		if err := json.Unmarshal([]byte(line), &payload); err != nil {
			return nil, fmt.Errorf("failed to parse git annex info output: %w", err)
		}
		info, ok := payload["info"].(string)
		if !ok || strings.TrimSpace(info) == "" {
			continue
		}
		infoLines = append(infoLines, info)
	}
	return infoLines, nil
}

func marshalJSON(payload map[string]any) (string, error) {
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to format git annex info output: %w", err)
	}
	return string(data), nil
}

func renderTable(rows []repoRow, dimRows bool, includeUUID bool, preferredContents map[string]string, duplicateUUIDs map[string]bool, remoteTypes map[string]string) string {
	headerStyle := lipgloss.NewStyle().
		Foreground(palette.Current().LogoFg.Adaptive()).
		Bold(true)
	hereStar := lipgloss.NewStyle().Foreground(palette.Current().Highlight.Adaptive()).Render("*")
	hereName := lipgloss.NewStyle().Foreground(palette.Current().Highlight.Adaptive()).Render
	hereTag := lipgloss.NewStyle().Foreground(palette.Current().Highlight.Adaptive()).Render
	heartIcon := lipgloss.NewStyle().Foreground(palette.Current().Accent.Adaptive()).Render("♥")
	warningIcon := lipgloss.NewStyle().Foreground(palette.Current().Warning.Adaptive()).Render("⚠")
	baseStyle := lipgloss.NewStyle().Align(lipgloss.Left).Padding(0, 1)
	dimStyle := baseStyle.Copy().Faint(true)
	headerStyle = headerStyle.Copy().Padding(0, 1).Align(lipgloss.Left)

	// Check if alignment is needed
	// - Configured remotes: align if any have preferred content or duplicates
	// - Other repositories: align if any have "here" marker or duplicates
	needsAlignment := false
	if !dimRows && preferredContents != nil && len(preferredContents) > 0 {
		needsAlignment = true
	} else if dimRows {
		// Check if any row has "here" marker for alignment
		for _, row := range rows {
			if row.Here {
				needsAlignment = true
				break
			}
		}
	}
	// Also need alignment if any duplicates exist
	if !needsAlignment && len(duplicateUUIDs) > 0 {
		for _, row := range rows {
			if duplicateUUIDs[row.UUID] {
				needsAlignment = true
				break
			}
		}
	}

	// Check if we should show the type column (only for configured remotes with type info)
	showTypeColumn := !dimRows && remoteTypes != nil && len(remoteTypes) > 0

	data := make([][]string, 0, len(rows))
	for _, row := range rows {
		name := row.Name

		// Add prefix/spacing for alignment
		if needsAlignment {
			if !dimRows {
				// Configured remotes: heart icon for preferred content
				if _, hasPC := preferredContents[row.Name]; hasPC {
					name = heartIcon + " " + name
				} else {
					name = "  " + name
				}
			} else if row.Here {
				// Other repositories: star for here marker (will be added below)
				// No prefix needed, star will be added in the "if row.Here" block
			} else {
				// Add spacing to align with star marker (2 characters: star + space)
				name = "  " + name
			}
		}

		if row.Here {
			name = fmt.Sprintf("%s %s %s", hereStar, hereName(name), hereTag("[here]"))
		}

		// Add warning icon for duplicate UUIDs
		if duplicateUUIDs[row.UUID] {
			name = warningIcon + " " + name
		}

		// Show full UUID
		uuid := row.UUID

		// Get remote type with emoji
		typeCol := ""
		if showTypeColumn {
			if remoteType, ok := remoteTypes[row.Name]; ok {
				typeCol = formatRemoteType(remoteType)
			}
		}

		if includeUUID && showTypeColumn {
			data = append(data, []string{name, typeCol, uuid})
		} else if includeUUID {
			data = append(data, []string{name, uuid})
		} else if showTypeColumn {
			data = append(data, []string{name, typeCol})
		} else {
			data = append(data, []string{name})
		}
	}

	// Create border style using branding pink color
	borderStyle := lipgloss.NewStyle().
		Foreground(palette.Current().Accent.Adaptive())

	// Style headers with orange bold
	headerStyleOrange := lipgloss.NewStyle().
		Foreground(palette.Current().Label.Adaptive()).
		Bold(true)
	styledNameHeader := headerStyleOrange.Render("name")
	styledTypeHeader := headerStyleOrange.Render("type")
	styledUuidHeader := headerStyleOrange.Render("uuid")

	styleFunc := func(row, col int) lipgloss.Style {
		if dimRows {
			return dimStyle
		}
		// For data rows, use clean style to preserve inline styling
		return baseStyle
	}

	t := table.New()
	if includeUUID && showTypeColumn {
		t.Headers(styledNameHeader, styledTypeHeader, styledUuidHeader)
	} else if includeUUID {
		t.Headers(styledNameHeader, styledUuidHeader)
	} else if showTypeColumn {
		t.Headers(styledNameHeader, styledTypeHeader)
	} else {
		t.Headers(styledNameHeader)
	}
	t.Rows(data...).
		Border(lipgloss.ThickBorder()).
		BorderStyle(borderStyle).
		StyleFunc(styleFunc)

	return strings.TrimRight(t.Render(), "\n")
}

// displayClonePresetInfoText displays clone preset information in text format (clean output)
func displayClonePresetInfoText(w io.Writer, metadata *clone.CloneMetadata) {
	fmt.Fprintln(w, "Clone Preset:")

	// Preset name
	fmt.Fprintf(w, "  Preset: %s", metadata.Preset)
	if metadata.Preset == "full-clone" {
		fmt.Fprint(w, " (full clone)")
	}
	fmt.Fprintln(w)

	// Cloned at (formatted)
	clonedAt, err := time.Parse(time.RFC3339, metadata.ClonedAt)
	if err == nil {
		fmt.Fprintf(w, "  Cloned: %s\n", clonedAt.Format("2006-01-02"))
	} else {
		fmt.Fprintf(w, "  Cloned: %s\n", metadata.ClonedAt)
	}

	// Commit SHA (short form) and branch
	shortCommit := metadata.Commit
	if len(shortCommit) > 7 {
		shortCommit = shortCommit[:7]
	}
	fmt.Fprintf(w, "  Preset commit: %s (%s)\n", shortCommit, metadata.Branch)

	// Load current preset to show sparse paths and git config (clean output - only if set)
	presetsConfig, err := clone.LoadPresetsConfig(".")
	if err == nil && presetsConfig != nil {
		currentPreset := presetsConfig.GetPresetByName(metadata.Preset)
		if currentPreset != nil {
			// Only show sparse checkout if paths are configured
			if len(currentPreset.SparsePaths) > 0 {
				fmt.Fprintln(w, "  Sparse checkout:")
				for _, path := range currentPreset.SparsePaths {
					fmt.Fprintf(w, "    - %s\n", path)
				}
			}

			// Only show git config if parameters are set
			if len(currentPreset.GitConfig) > 0 {
				fmt.Fprintln(w, "  Git config:")
				// Sort keys for consistent output
				keys := make([]string, 0, len(currentPreset.GitConfig))
				for k := range currentPreset.GitConfig {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, k := range keys {
					fmt.Fprintf(w, "    %s = %s\n", k, currentPreset.GitConfig[k])
				}
			}
		}
	}
}

type annexConfig struct {
	addUnlocked      bool
	thin             bool
	hasGitattributes bool
}

func displayRepoURL(w io.Writer) {
	out, err := command("git", "remote", "get-url", "origin").Output()
	if err != nil {
		return
	}
	url := strings.TrimSpace(string(out))
	if url == "" {
		return
	}
	labelStyle := lipgloss.NewStyle().Foreground(palette.Current().Accent.Adaptive()).Bold(true)
	valueStyle := lipgloss.NewStyle().Foreground(palette.Current().Highlight.Adaptive())
	fmt.Fprintf(w, "%s %s\n\n", labelStyle.Render("Data Repo:"), valueStyle.Render(url))
}

func getAnnexConfig() annexConfig {
	cfg := annexConfig{}

	// Check annex.addunlocked (stored on git-annex branch)
	if out, err := command("git", "annex", "config", "--get", "annex.addunlocked").Output(); err == nil {
		cfg.addUnlocked = strings.TrimSpace(string(out)) == "true"
	}

	// Check annex.thin (local git config)
	if out, err := command("git", "config", "annex.thin").Output(); err == nil {
		cfg.thin = strings.TrimSpace(string(out)) == "true"
	}

	// Check if .gitattributes has active (uncommented) annex rules
	if data, err := os.ReadFile(".gitattributes"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if !strings.HasPrefix(trimmed, "#") && strings.Contains(trimmed, "annex.") {
				cfg.hasGitattributes = true
				break
			}
		}
	}

	return cfg
}

func displayAnnexConfigText(w io.Writer, cfg annexConfig) {
	sectionStyle := lipgloss.NewStyle().Foreground(palette.Current().Accent.Adaptive()).Bold(true)
	fieldStyle := lipgloss.NewStyle().Foreground(palette.Current().Label.Adaptive()).Bold(true)
	valueStyle := lipgloss.NewStyle().Foreground(palette.Current().Highlight.Adaptive())
	dimStyle := lipgloss.NewStyle()

	fmt.Fprintln(w, sectionStyle.Render("Annex Config:"))

	if cfg.addUnlocked {
		fmt.Fprintf(w, "  %s %s %s\n", fieldStyle.Render("Unlocked:"), valueStyle.Render("yes"), dimStyle.Render("(files are editable without git annex unlock)"))
	} else {
		fmt.Fprintf(w, "  %s %s %s\n", fieldStyle.Render("Unlocked:"), valueStyle.Render("no"), dimStyle.Render("(files are symlinks, use git annex unlock to edit)"))
	}

	if cfg.thin {
		fmt.Fprintf(w, "  %s %s %s\n", fieldStyle.Render("Thin:    "), valueStyle.Render("yes"), dimStyle.Render("(hard links, saves disk but git restore won't work)"))
	} else {
		fmt.Fprintf(w, "  %s %s %s\n", fieldStyle.Render("Thin:    "), valueStyle.Render("no"), dimStyle.Render("(copies, git restore works)"))
	}

	if cfg.hasGitattributes {
		fmt.Fprintf(w, "  %s %s %s\n", fieldStyle.Render("Routing: "), valueStyle.Render("active"), dimStyle.Render("(.gitattributes may affect how files are routed to git vs annex)"))
	}
}

