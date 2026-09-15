package tui

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"text/template"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/paginator"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/log"
	"github.com/dustin/go-humanize"

	"github.com/Genentech/exohub/go/exo/palette"
	"github.com/muesli/reflow/ansi"
	"github.com/muesli/reflow/truncate"
	"github.com/spf13/viper"
)

const (
	stashIndent                = 1
	stashViewItemHeight        = 3 // height of stash entry, including gap
	stashViewTopPadding        = 7 // logo, search bar, blank, doc count, gaps
	stashViewBottomPadding     = 3 // pagination and gaps, but not help
	stashViewHorizontalPadding = 6
	schemaListHeight           = 10 // maximum height of the schema selection list
	versionListHeight          = 10 // maximum height of the version selection list
)

var stashingStatusMessage = statusMessage{normalStatusMessage, "Stashing..."}

var (
	dividerDot = darkGrayFg.SetString(" • ")
	dividerBar = darkGrayFg.SetString(" │ ")

	logoStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#ECFD65")).
			Background(fuchsia).
			Bold(true)

	infoTitleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#ECFD65")).
			Bold(true)

	stashSpinnerStyle = lipgloss.NewStyle().
				Foreground(gray)
	stashInputPromptStyle = lipgloss.NewStyle().
				Foreground(green).
				MarginRight(1)
	stashInputCursorStyle = lipgloss.NewStyle().
				Foreground(fuchsia).
				MarginRight(1)

	// Search input background style
	searchInputBgStyle = lipgloss.NewStyle().
				Background(lipgloss.Color("#333333"))

	// Schema list styles
	schemaListTitleStyle    = lipgloss.NewStyle().MarginLeft(2)
	schemaItemStyle         = lipgloss.NewStyle().PaddingLeft(4)
	schemaSelectedItemStyle = lipgloss.NewStyle().PaddingLeft(2).Foreground(lipgloss.Color("170"))
	schemaPaginationStyle   = list.DefaultStyles().PaginationStyle.PaddingLeft(4)
	schemaHelpStyle         = list.DefaultStyles().HelpStyle.PaddingLeft(4).PaddingBottom(1)

	dropdownLabelStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#AAAAAA")).
				MarginRight(1)
	dropdownFocusedLabelStyle = lipgloss.NewStyle().
					Foreground(lipgloss.Color("#FFFFFF")).
					MarginRight(1).
					Bold(true)

	// Search form styles
	searchFormTitleStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#ECFD65")).
				Background(fuchsia).
				Bold(true).
				Padding(0, 1)
	searchFormSubtitleStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#AAAAAA"))
)

// Schema item type for the list
type schemaItem struct {
	DisplayValue  string // The value displayed in the list
	InternalValue string // The actual value used internally
}

func (i schemaItem) FilterValue() string {
	return i.DisplayValue
}

// Schema item delegate for rendering items in the list
type schemaItemDelegate struct{}

func (d schemaItemDelegate) Height() int                             { return 1 }
func (d schemaItemDelegate) Spacing() int                            { return 0 }
func (d schemaItemDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }
func (d schemaItemDelegate) Render(w io.Writer, m list.Model, index int, listItem list.Item) {
	i, ok := listItem.(schemaItem)
	if !ok {
		return
	}
	str := i.DisplayValue

	fn := itemStyle.Render
	if index == m.Index() {
		fn = func(s ...string) string {
			return schemaSelectedItemStyle.Render("> " + strings.Join(s, " "))
		}
	}

	fmt.Fprint(w, fn(str))
}

/*
The `filterInput` field in the `stashModel` is used to manage the search query for artifacts.
This process involves the following steps:

1. **Parsing the Search Query**:
   - The `parseSchemaFromSearchQuery` function is used to extract the schema filter from the query.
   - It looks for a pattern like `_extra.$schema:X AND` in the query string.
   - If a schema filter is found, it is removed from the query, and the remaining query is returned.

2. **Extracting the Latest Flag**:
   - The `parseLatestFromSearchQuery` function is used to extract the "latest" flag from the query.
   - It identifies patterns like `_extra.latest:true AND` or `_extra.latest:false AND`.
   - If the flag is found, it is removed from the query, and the remaining query is returned.

3. **Regenerating the Query**:
   - The `generateQuery` function reconstructs the query based on the current state of the `filterInput`, schema selection, and latest flag.
   - If a schema is selected (not "*"), it is added to the query as `_extra.$schema:X`.
   - If the "latest" flag is set to "Yes", it is added as `_extra.latest:true`.
   - The regenerated query ensures that all filters are correctly applied and formatted.

4. **Validation and Reset**:
   - When the user presses "Enter" to validate the search, the query is regenerated and saved.
   - If the user presses "Esc", the filtering state is reset, and the query is cleared or restored to its default state.

This process ensures that the search query is dynamically updated based on user interactions, while maintaining a consistent and valid format.
*/

// extractSearchText extracts the plain user search text from a saved query string,
// stripping any schema and latest filters. Used for one-time migration on init.
func extractSearchText(query string) (text string, schema string, latest bool) {
	_, _, text, schema, latest = extractSearchTextFull(query)
	return
}

func extractSearchTextFull(query string) (project string, version string, text string, schema string, latest bool) {
	schemaRegex := regexp.MustCompile(`_extra\.\$schema:(?:"([^"]+)"|([^\s"]+))(\s+AND\s+)?`)
	latestRegex := regexp.MustCompile(`_extra\.latest:(true|false)(\s+AND\s+)?`)
	projectRegex := regexp.MustCompile(`_extra\.project_id:(?:"([^"]+)"|([^\s"]+))(\s+AND\s+)?`)
	versionRegex := regexp.MustCompile(`_extra\.version:(?:"([^"]+)"|([^\s"]+))(\s+AND\s+)?`)

	if matches := schemaRegex.FindStringSubmatch(query); len(matches) >= 3 {
		schema = matches[1]
		if schema == "" {
			schema = matches[2]
		}
		query = strings.TrimSpace(schemaRegex.ReplaceAllString(query, ""))
	}
	if matches := latestRegex.FindStringSubmatch(query); len(matches) >= 2 {
		latest = matches[1] == "true"
		query = strings.TrimSpace(latestRegex.ReplaceAllString(query, ""))
	}
	if matches := projectRegex.FindStringSubmatch(query); len(matches) >= 3 {
		project = matches[1]
		if project == "" {
			project = matches[2]
		}
		query = strings.TrimSpace(projectRegex.ReplaceAllString(query, ""))
	}
	if matches := versionRegex.FindStringSubmatch(query); len(matches) >= 3 {
		version = matches[1]
		if version == "" {
			version = matches[2]
		}
		query = strings.TrimSpace(versionRegex.ReplaceAllString(query, ""))
	}
	// Clean up leftover AND connectors from stripped filters
	for strings.Contains(query, "AND AND") {
		query = strings.ReplaceAll(query, "AND AND", "AND")
	}
	query = strings.TrimPrefix(query, "AND ")
	query = strings.TrimSuffix(query, " AND")
	query = strings.TrimSpace(query)
	if query == "AND" {
		query = ""
	}
	text = query
	return
}

// MSG

type (
	searchedArtifactMsg string
	fetchedMarkdownMsg  *markdown
	forceRedrawMsg      struct{}
	fetchSchemasMsg     struct{}
	schemasLoadedMsg    []string
	versionsLoadedMsg   struct {
		versions []string
		latest   string
	}
)

// MODEL

// Focus tracks which element has focus during filtering
type filteringFocus int

const (
	focusOnSearch filteringFocus = iota
	focusOnProject
	focusOnVersion
	focusOnSchema
)

// stashViewState is the high-level state of the file listing.
type stashViewState int

const (
	stashStateReady stashViewState = iota
	stashStateLoadingDocument
	stashStateShowingError
)

// The types of documents we are currently showing to the user.
type sectionKey int

const (
	documentsSection = iota
	filterSection
)

// section contains definitions and state information for displaying a tab and
// its contents in the file listing view.
type section struct {
	key       sectionKey
	paginator paginator.Model
	cursor    int
}

// map sections to their associated types.
var sections = map[sectionKey]section{}

// filterState is the current filtering state in the file listing.
type filterState int

const (
	unfiltered    filterState = iota // no filter set
	filtering                        // user is actively setting a filter
	filterApplied                    // a filter is applied and user is not editing filter
)

// statusMessageType adds some context to the status message being sent.
type statusMessageType int

// Types of status messages.
const (
	normalStatusMessage statusMessageType = iota
	subtleStatusMessage
	errorStatusMessage
)

// statusMessage is an ephemeral note displayed in the UI.
type statusMessage struct {
	status  statusMessageType
	message string
}

// Custom text input with support for history
type TextInputWithHistory struct {
	textinput.Model
}

func (tih *TextInputWithHistory) LoadHistory() {
	// Load history once
	historyLines, err := LoadSearchHistory()
	if err != nil {
		log.Error("Unable to load history", "err", err)
		historyLines = []string{}
	}
	tih.SetSuggestions(historyLines)
}

func (tih *TextInputWithHistory) AddToHistory() {
	suggestions := tih.AvailableSuggestions()
	command := tih.Value()
	tih.SetSuggestions(append(suggestions, command))
	err := AddToHistory(command)
	if err != nil {
		log.Error("Unable to add command to history", "command", command, "err", err)
	}
}

func initSections() {
	sections = map[sectionKey]section{
		documentsSection: {
			key:       documentsSection,
			paginator: newStashPaginator(),
		},
	}
}

// String returns a styled version of the status message appropriate for the
// given context.
func (s statusMessage) String() string {
	switch s.status {
	case subtleStatusMessage:
		return dimGreenFg(s.message)
	case errorStatusMessage:
		return redFg(s.message)
	default:
		return greenFg(s.message)
	}
}

type stashModel struct {
	common             *commonModel
	err                error
	spinner            spinner.Model
	filterInput        *TextInputWithHistory
	projectInput       textinput.Model
	schemaList         list.Model
	schemaListVisible  bool
	versionList        list.Model
	versionListVisible bool
	viewState          stashViewState
	filterState        filterState
	filteringFocus     filteringFocus
	showFullHelp       bool
	showStatusMessage  bool
	statusMessage      statusMessage
	statusMessageTimer *time.Timer
	schemasLoading     bool
	versionsLoading    bool
	selectedSchema     string
	selectedProject    string
	selectedVersion    string // "" = All, "latest" = Latest, or a specific version string

	// Available document sections we can cycle through. We use a slice, rather
	// than a map, because order is important.
	sections []section

	// Index of the section we're currently looking at
	sectionIndex int

	// Tracks if docs were loaded
	loaded bool

	// The master set of markdown documents we're working with.
	markdowns []*markdown
	// scroll element
	scroll        *markdown
	scrollExpired bool

	Total int64

	// Multi-select for batch download
	selected map[int]bool

	// Page we're fetching stash items from on the server, which is different
	// from the local pagination. Generally, the server will return more items
	// than we can display at a time so we can paginate locally without having
	// to fetch every time.
	serverPage int64
}

func (m stashModel) loadingDone() bool {
	return m.loaded
}

func (m stashModel) currentSection() *section {
	return &m.sections[m.sectionIndex]
}

func (m stashModel) paginator() *paginator.Model {
	return &m.currentSection().paginator
}

func (m *stashModel) setPaginator(p paginator.Model) {
	m.currentSection().paginator = p
}

func (m stashModel) cursor() int {
	return m.currentSection().cursor
}

func (m *stashModel) setCursor(i int) {
	m.currentSection().cursor = i
}

// Whether or not the spinner should be spinning.
func (m stashModel) shouldSpin() bool {
	loading := !m.loadingDone() || m.schemasLoading || m.versionsLoading
	openingDocument := m.viewState == stashStateLoadingDocument
	return loading || openingDocument
}

func (m *stashModel) setSize(width, height int) {
	m.common.width = width
	m.common.height = height

	// Set the width of the filter input to full width minus padding and prompt width
	availableWidth := width - stashViewHorizontalPadding*2
	promptWidth := ansi.PrintableRuneWidth(m.filterInput.Prompt)
	m.filterInput.Width = availableWidth - promptWidth - 1

	// Set project input width
	projectPromptWidth := ansi.PrintableRuneWidth(m.projectInput.Prompt)
	m.projectInput.Width = availableWidth - projectPromptWidth - 1

	// Set width of schema list to the same width as the search input
	m.schemaList.SetWidth(availableWidth)
	actualSchemas := len(m.schemaList.Items()) + 6
	m.schemaList.SetHeight(min(actualSchemas, schemaListHeight))

	// Set width of version list
	m.versionList.SetWidth(availableWidth)
	actualVersions := len(m.versionList.Items()) + 4
	m.versionList.SetHeight(min(actualVersions, versionListHeight))

	m.updatePagination()
}

func (m *stashModel) resetFiltering() {
	m.filterState = unfiltered
	m.filteringFocus = focusOnSearch
	m.schemaListVisible = false
	m.versionListVisible = false

	// If the filtered section is present (it's always at the end) slice it out
	// of the sections slice to remove it from the UI.
	if m.sections[len(m.sections)-1].key == filterSection {
		m.sections = m.sections[:len(m.sections)-1]
	}
	// If the current section is out of bounds (it would be if we cut down the
	// slice above) then return to the first section.
	if m.sectionIndex > len(m.sections)-1 {
		m.sectionIndex = 0
	}

	// Update pagination after we've switched sections.
	m.updatePagination()

	// filterInput keeps only the plain search text — schema/latest are separate fields.
}

// Is a filter currently being applied?
func (m stashModel) filterApplied() bool {
	return m.filterState != unfiltered
}

// Should we be updating the filter?
func (m stashModel) shouldUpdateFilter() bool {
	// If we're in the middle of setting a note don't update the filter so that
	// the focus won't jump around.
	return m.filterApplied()
}

// Update pagination according to the amount of markdowns for the current
// state.
func (m *stashModel) updatePagination() {
	_, helpHeight := m.helpView()

	// Calculate additional space for dropdown lists if visible
	schemaListSpace := 0
	if m.schemaListVisible {
		schemaListSpace = schemaListHeight + 2
	}
	if m.versionListVisible {
		schemaListSpace += versionListHeight + 2
	}

	availableHeight := m.common.height -
		stashViewTopPadding -
		helpHeight -
		schemaListSpace -
		stashViewBottomPadding

	m.paginator().PerPage = max(1, availableHeight/stashViewItemHeight)

	if pages := len(m.getVisibleMarkdowns()); pages < 1 {
		m.paginator().SetTotalPages(1)
	} else {
		m.paginator().SetTotalPages(pages)
	}

	// Make sure the page stays in bounds
	if m.paginator().Page >= m.paginator().TotalPages-1 {
		m.paginator().Page = max(0, m.paginator().TotalPages-1)
	}
}

// MarkdownIndex returns the index of the currently selected markdown item.
func (m stashModel) markdownIndex() int {
	return m.paginator().Page*m.paginator().PerPage + m.cursor()
}

// Return the current selected markdown in the stash.
func (m stashModel) selectedMarkdown() *markdown {
	i := m.markdownIndex()

	mds := m.getVisibleMarkdowns()
	if i < 0 || len(mds) == 0 || len(mds) <= i {
		return nil
	}

	return mds[i]
}

// Adds markdown documents to the model.
func (m *stashModel) addMarkdowns(mds ...*markdown) {
	if len(mds) == 0 {
		return
	}

	m.markdowns = append(m.markdowns, mds...)

	m.updatePagination()
}

// Adds markdown documents to the model.
func (m *stashModel) addScroll(md *markdown) {
	m.scroll = md
	m.updatePagination()
}

// Returns the markdowns that should be currently shown.
func (m stashModel) getVisibleMarkdowns() []*markdown {
	// Create a new slice to avoid modifying the original markdowns
	result := make([]*markdown, len(m.markdowns))
	copy(result, m.markdowns)

	if len(result) != 0 && m.scroll != nil {
		result = append(result, m.scroll)
	}
	return result
}

// Command for opening a markdown document in the pager. Note that this also
// alters the model.
func (m *stashModel) openMarkdown(md *markdown) tea.Cmd {
	m.viewState = stashStateLoadingDocument
	cmd := loadMarkdown(md)
	return tea.Batch(cmd, m.spinner.Tick)
}

func (m *stashModel) showTimedStatusMessage(msgType statusMessageType, text string) tea.Cmd {
	m.statusMessage = statusMessage{msgType, text}
	m.showStatusMessage = true
	if m.statusMessageTimer != nil {
		m.statusMessageTimer.Stop()
	}
	m.statusMessageTimer = time.NewTimer(statusMessageTimeout)
	return waitForStatusMessageTimeout(stashContext, m.statusMessageTimer)
}

func (m *stashModel) hideStatusMessage() {
	m.showStatusMessage = false
	m.statusMessage = statusMessage{}
	if m.statusMessageTimer != nil {
		m.statusMessageTimer.Stop()
	}
}

func (m *stashModel) moveCursorUp() {
	m.setCursor(m.cursor() - 1)
	if m.cursor() < 0 && m.paginator().Page == 0 {
		// Stop
		m.setCursor(0)
		return
	}

	if m.cursor() >= 0 {
		return
	}
	// Go to previous page
	m.paginator().PrevPage()

	m.setCursor(m.paginator().ItemsOnPage(len(m.getVisibleMarkdowns())) - 1)
}

func (m *stashModel) moveCursorDown() {
	itemsOnPage := m.paginator().ItemsOnPage(len(m.getVisibleMarkdowns()))

	m.setCursor(m.cursor() + 1)
	if m.cursor() < itemsOnPage {
		return
	}

	if !m.paginator().OnLastPage() {
		m.paginator().NextPage()
		m.setCursor(0)
		return
	}

	// During filtering the cursor position can exceed the number of
	// itemsOnPage. It's more intuitive to start the cursor at the
	// topmost position when moving it down in this scenario.
	if m.cursor() > itemsOnPage {
		m.setCursor(0)
		return
	}
	m.setCursor(itemsOnPage - 1)
}

// Toggles the schema list visibility
func (m *stashModel) toggleSchemaList() {
	m.schemaListVisible = !m.schemaListVisible
	m.updatePagination()
}

// stashDownloadMsg tells the parent to show the download dialog.
type stashDownloadMsg struct {
	artifact *markdown // single file mode
	ids      []string  // batch mode
	size     int64     // batch total size
}

// showDownloadPrompt sends a message to the parent to open the download dialog.
func (m *stashModel) showDownloadPrompt() tea.Cmd {
	if len(m.selected) > 0 {
		// If exactly one item is selected and it's a bundle/commit,
		// treat it as a single artifact so it gets resolved into its files.
		if len(m.selected) == 1 {
			for idx := range m.selected {
				if idx < len(m.markdowns) {
					md := m.markdowns[idx]
					schema := md.Extra.Schema
					if strings.HasPrefix(schema, "exohub-bundle/") || strings.HasPrefix(schema, "exohub-commit/") {
						m.selected = make(map[int]bool)
						return func() tea.Msg {
							return stashDownloadMsg{artifact: md}
						}
					}
				}
			}
		}
		var ids []string
		var totalSize int64
		for idx := range m.selected {
			if idx < len(m.markdowns) {
				md := m.markdowns[idx]
				ids = append(ids, md.Extra.ID)
				totalSize += getFileSize(md)
			}
		}
		m.selected = make(map[int]bool)
		return func() tea.Msg {
			return stashDownloadMsg{ids: ids, size: totalSize}
		}
	}

	md := m.selectedMarkdown()
	if md == nil {
		return nil
	}
	if !isDownloadable(md) {
		return m.showTimedStatusMessage(errorStatusMessage, "This document is not downloadable")
	}
	return func() tea.Msg {
		return stashDownloadMsg{artifact: md}
	}
}

// isDownloadable checks if a document can be downloaded based on schema and metadata.
func isDownloadable(md *markdown) bool {
	schema := md.Extra.Schema
	// exohub-artifact: always allowed (download engine checks locations)
	if strings.HasPrefix(schema, "exohub-artifact/") {
		return true
	}
	// exohub-commit: only if annex_files is present and non-empty
	if strings.HasPrefix(schema, "exohub-commit/") {
		return hasNonEmptySlice(md.Result.Metadata, "annex_files")
	}
	// exohub-bundle: always allowed (resolves commits internally)
	if strings.HasPrefix(schema, "exohub-bundle/") {
		return true
	}
	// Other schemas: downloadable if file_size > 0
	return md.Extra.FileSize > 0
}

// hasNonEmptySlice checks if a map has a non-empty slice at the given key.
func hasNonEmptySlice(m map[string]interface{}, key string) bool {
	v, ok := m[key]
	if !ok {
		return false
	}
	arr, ok := v.([]interface{})
	return ok && len(arr) > 0
}

// INIT

func newStashModel(common *commonModel) stashModel {
	sp := spinner.New()
	sp.Spinner = spinner.Line
	sp.Style = stashSpinnerStyle

	si := textinput.New()
	si.Prompt = "Search:"
	si.PromptStyle = stashInputPromptStyle
	si.Cursor.Style = stashInputCursorStyle
	si.ShowSuggestions = true
	tih := &TextInputWithHistory{
		Model: si,
	}
	tih.LoadHistory()

	// Initialize schema list with default width and just the wildcard for now
	defaultWidth := 30
	defaultItems := []list.Item{schemaItem{DisplayValue: "*", InternalValue: "*"}}
	schemaList := list.New(defaultItems, schemaItemDelegate{}, defaultWidth, schemaListHeight)
	schemaList.SetShowStatusBar(false)
	schemaList.SetFilteringEnabled(false)
	schemaList.SetShowHelp(false)
	schemaList.SetShowTitle(false)
	schemaList.Styles.PaginationStyle = schemaPaginationStyle

	// Initialize project input
	pi := textinput.New()
	pi.Prompt = "Project:"
	pi.PromptStyle = stashInputPromptStyle
	pi.Cursor.Style = stashInputCursorStyle
	pi.Placeholder = "all"

	// Initialize version dropdown with default items (no project selected)
	versionItems := []list.Item{
		versionItem{display: "All", value: ""},
		versionItem{display: "Latest", value: "latest"},
	}
	versionList := list.New(versionItems, versionItemDelegate{}, defaultWidth, 4)
	versionList.SetShowStatusBar(false)
	versionList.SetFilteringEnabled(false)
	versionList.SetShowHelp(false)
	versionList.SetShowTitle(false)
	versionList.SetHeight(3)

	// Restore search params — try saved params first, then config defaults
	searchParams, err := contextProvider.LoadSearchParams()
	if err != nil {
		log.Error("Failed to load search parameters", "err", err)
	}
	log.Debug("stash init",
		"savedQ", searchParams.Q,
		"defaultQ", common.cfg.DefaultSearchParams.Q,
		"commonSearchQ", common.search.Q,
	)

	// Extract plain text, schema, latest, project, and version from the active query.
	// Use DefaultSearchParams.Q (which is either the saved query or empty when CLI flags were provided).
	savedQ := common.cfg.DefaultSearchParams.Q
	savedProject, savedVersion, searchText, savedSchema, savedLatest := extractSearchTextFull(savedQ)
	if searchText == "" {
		searchText = "*"
	}
	tih.SetValue(searchText)

	// CLI flags take precedence
	if common.cfg.DefaultSearchParams.Project != "" {
		savedProject = common.cfg.DefaultSearchParams.Project
	}
	if common.cfg.DefaultSearchParams.Version != "" {
		savedVersion = common.cfg.DefaultSearchParams.Version
	}
	if common.cfg.DefaultSearchParams.Schema != "" {
		savedSchema = common.cfg.DefaultSearchParams.Schema
	}

	// Set project input value
	if savedProject != "" {
		pi.SetValue(savedProject)
	}

	// Determine initial selectedVersion
	var initialVersion string
	if savedVersion != "" {
		initialVersion = savedVersion
	} else if savedLatest {
		initialVersion = "latest"
	}

	// Sync version list selection with initialVersion
	if initialVersion == "latest" {
		versionList.Select(1) // "Latest" is index 1
	}
	// For specific versions, the list only has All/Latest at init;
	// dynamic versions are fetched when the field gets focus

	// Also set common.search so the initial search() uses the right params
	if common.search.Q == "" {
		common.search = common.cfg.DefaultSearchParams
	}

	si.Focus()

	s := []section{
		sections[documentsSection],
	}

	m := stashModel{
		common:            common,
		spinner:           sp,
		filterInput:       tih,
		projectInput:      pi,
		schemaList:        schemaList,
		schemaListVisible: false,
		versionList:       versionList,
		filteringFocus:    focusOnSearch,
		schemasLoading:    false,
		selectedSchema:    savedSchema,
		selectedProject:   savedProject,
		selectedVersion:   initialVersion,
		serverPage:        1,
		sections:          s,
		selected:          make(map[int]bool),
	}

	return m
}

func newStashPaginator() paginator.Model {
	p := paginator.New()
	p.Type = paginator.Dots
	p.ActiveDot = brightGrayFg("•")
	p.InactiveDot = darkGrayFg.Render("•")
	return p
}

// Force a full redraw of the screen
func forceRedraw() tea.Cmd {
	return func() tea.Msg {
		return forceRedrawMsg{}
	}
}

func fetchVersions(projectID string) tea.Cmd {
	return func() tea.Msg {
		versions, latest, err := client.GetProjectVersions(projectID)
		if err != nil {
			log.Error("Failed to fetch versions", "project", projectID, "err", err)
			return versionsLoadedMsg{versions: nil, latest: ""}
		}
		return versionsLoadedMsg{versions: versions, latest: latest}
	}
}

func fetchSchemas() tea.Cmd {
	return func() tea.Msg {
		// Clear schema cache to force a fresh load
		client.ClearSchemaCache()
		// Fetch schemas from the endpoint
		schemas := client.GetSchemas()
		log.Debug("Fetched schemas", "count", len(schemas))

		// Add wildcard option at the beginning
		schemas = append([]string{"*"}, schemas...)

		// Get the current context and UI configuration
		var config UIConfig
		err := viper.UnmarshalKey("ui", &config)
		if err != nil {
			log.Errorf("Error marshalling fields: %v", err)
			return schemasLoadedMsg(schemas) // Return schemas without icons if config fails
		}
		// Create a map of schema to icon from the UI config
		schemaIcons := make(map[string]string)
		for _, item := range config.Views.Items {
			if item.Icon != "" {
				schemaIcons[item.Schema] = item.Icon
			}
		}

		// Separate schemas with and without icons
		var schemasWithIcons []string
		var schemasWithoutIcons []string
		for _, schema := range schemas {
			if schema == "*" {
				continue
			}
			if icon, exists := schemaIcons[schema]; exists && icon != "" {
				schemasWithIcons = append(schemasWithIcons, fmt.Sprintf("%s %s", icon, schema))
			} else {
				schemasWithoutIcons = append(schemasWithoutIcons, schema)
			}
		}

		// Combine the wildcard schema, schemas with icons, and schemas without icons
		var items []string
		items = append(items, "*")
		items = append(items, schemasWithIcons...)
		items = append(items, schemasWithoutIcons...)

		return schemasLoadedMsg(items)
	}
}

// UPDATE

func (m stashModel) Init() tea.Cmd {
	// Immediately fetch schemas when the model initializes
	return tea.Batch(
		m.spinner.Tick,
		fetchSchemas(),
	)
}

func (m stashModel) update(msg tea.Msg) (stashModel, tea.Cmd) {
	var cmds []tea.Cmd

	// Check if we should process this message with the schema list first
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		// If version list is visible and focused, let it handle navigation keys first
		if m.versionListVisible && m.filteringFocus == focusOnVersion {
			switch keyMsg.String() {
			case "up", "down", "k", "j", "pgup", "pgdown", "home", "end":
				var cmd tea.Cmd
				m.versionList, cmd = m.versionList.Update(msg)
				if cmd != nil {
					cmds = append(cmds, cmd)
				}
				return m, tea.Batch(cmds...)
			}
		}

		// If schema list is visible and focused, let it handle navigation keys first
		if m.schemaListVisible && m.filteringFocus == focusOnSchema {
			// Handle navigation keys specifically in the schema list when it's focused
			switch keyMsg.String() {
			case "up", "down", "k", "j", "pgup", "pgdown", "home", "end":
				var cmd tea.Cmd
				m.schemaList, cmd = m.schemaList.Update(msg)
				if cmd != nil {
					cmds = append(cmds, cmd)
				}
				// Return early to avoid double-processing of these keys
				return m, tea.Batch(cmds...)
			}
		}
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// When the window size changes, update schema list dimensions
		m.setSize(msg.Width, msg.Height)

	case errMsg:
		m.err = msg

	case schemasLoadedMsg:
		// Mark schemas as loaded
		m.schemasLoading = false

		// Convert schema strings to list items with internal and display values
		var items []list.Item
		for _, schema := range msg {
			if schema == "*" {
				items = append(items, schemaItem{DisplayValue: "*", InternalValue: "*"})
			} else if strings.Contains(schema, " ") {
				parts := strings.SplitN(schema, " ", 2)
				items = append(items, schemaItem{DisplayValue: schema, InternalValue: parts[1]})
			} else {
				items = append(items, schemaItem{DisplayValue: schema, InternalValue: schema})
			}
		}

		// Update the schema list with fetched schemas
		m.schemaList.SetItems(items)
		log.Debug("Updated schema list with items", "count", len(items))

		// Restore the selected schema from the search query
		if m.selectedSchema != "" {
			for i, item := range m.schemaList.Items() {
				if schemaItem, ok := item.(schemaItem); ok && schemaItem.InternalValue == m.selectedSchema {
					m.schemaList.Select(i)
					break
				}
			}
		}

	case versionsLoadedMsg:
		m.versionsLoading = false
		var items []list.Item
		items = append(items, versionItem{display: "All", value: ""})
		if msg.latest == "" {
			// No known latest — keep generic "Latest" option
			items = append(items, versionItem{display: "Latest", value: "latest"})
		}
		for _, v := range msg.versions {
			display := v
			if msg.latest != "" && v == msg.latest {
				display = v + " (latest)"
			}
			items = append(items, versionItem{display: display, value: v})
		}
		// Move the latest version to first position (after "All")
		if msg.latest != "" {
			for i, item := range items {
				if vi, ok := item.(versionItem); ok && vi.value == msg.latest {
					if i > 1 {
						reordered := make([]list.Item, 0, len(items))
						reordered = append(reordered, items[0]) // "All"
						reordered = append(reordered, items[i]) // latest version
						reordered = append(reordered, items[1:i]...)
						reordered = append(reordered, items[i+1:]...)
						items = reordered
					}
					break
				}
			}
			// If selectedVersion was "latest", map it to the actual version
			if m.selectedVersion == "latest" {
				m.selectedVersion = msg.latest
			}
		}
		m.versionList.SetItems(items)
		// Restore selection to match selectedVersion
		for i, item := range m.versionList.Items() {
			if vi, ok := item.(versionItem); ok && vi.value == m.selectedVersion {
				m.versionList.Select(i)
				break
			}
		}
		actualVersions := len(m.versionList.Items()) + 4
		m.versionList.SetHeight(min(actualVersions, versionListHeight))
		log.Debug("Updated version list", "count", len(m.versionList.Items()))

	case artifactSearchFinished:
		// We're finished searching for local files
		m.loaded = true

		// If no results were found, force a redraw to clear any stale UI elements
		if len(m.markdowns) == 0 {
			cmds = append(cmds, forceRedraw())
		}

	case forceRedrawMsg:
		// This message just triggers a redraw, nothing else to do

	case searchedArtifactMsg:
		// Make sure we reset the state properly when a new search is performed
		m.loaded = false
		m.markdowns = nil       // Explicitly clear markdowns here
		m.scroll = nil          // Explicitly clear scroll here
		m.scrollExpired = false // Reset scroll expired state
		m.setCursor(0)
		m.paginator().Page = 0               // Reset to first page
		m.updatePagination()                 // Force pagination update to reflect empty state
		cmds = append(cmds, tea.ClearScreen) // Force clearing the screen
		return m, tea.Batch(cmds...)

	case spinner.TickMsg:
		if m.shouldSpin() {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			cmds = append(cmds, cmd)
		}

	case statusMessageTimeoutMsg:
		if applicationContext(msg) == stashContext {
			m.hideStatusMessage()
		}
	}

	// For non-navigation keys, pass updates to list components when visible and focused.
	if m.schemaListVisible && m.filteringFocus == focusOnSchema {
		if keyMsg, ok := msg.(tea.KeyMsg); ok && keyMsg.String() != keyEsc {
			var cmd tea.Cmd
			m.schemaList, cmd = m.schemaList.Update(msg)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	}
	if m.versionListVisible && m.filteringFocus == focusOnVersion {
		if keyMsg, ok := msg.(tea.KeyMsg); ok && keyMsg.String() != keyEsc {
			var cmd tea.Cmd
			m.versionList, cmd = m.versionList.Update(msg)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	}

	if m.filterState == filtering {
		cmds = append(cmds, m.handleSearching(msg))
		return m, tea.Batch(cmds...)
	}

	// Updates per the current state
	switch m.viewState {
	case stashStateReady:
		cmds = append(cmds, m.handleDocumentBrowsing(msg))
	case stashStateShowingError:
		// Any key exists the error view
		if _, ok := msg.(tea.KeyMsg); ok {
			m.viewState = stashStateReady
		}
	}

	return m, tea.Batch(cmds...)
}

// Updates for when a user is browsing the markdown listing.
func (m *stashModel) handleDocumentBrowsing(msg tea.Msg) tea.Cmd {
	var cmds []tea.Cmd

	numDocs := len(m.getVisibleMarkdowns())

	switch msg := msg.(type) {
	// Handle keys
	case tea.KeyMsg:
		switch msg.String() {
		case "k", "ctrl+k", "up":
			// Only handle these keys if schema list is not focused
			if !(m.schemaListVisible && m.filteringFocus == focusOnSchema) {
				m.moveCursorUp()
			}

		case "j", "ctrl+j", "down":
			// Only handle these keys if schema list is not focused
			if !(m.schemaListVisible && m.filteringFocus == focusOnSchema) {
				m.moveCursorDown()
			}

		// Go to the very start
		case "home", "g":
			// Only handle these keys if schema list is not focused
			if !(m.schemaListVisible && m.filteringFocus == focusOnSchema) {
				m.paginator().Page = 0
				m.setCursor(0)
			}

		// Go to the very end
		case "end", "G":
			// Only handle these keys if schema list is not focused
			if !(m.schemaListVisible && m.filteringFocus == focusOnSchema) {
				m.paginator().Page = m.paginator().TotalPages - 1
				m.setCursor(m.paginator().ItemsOnPage(numDocs) - 1)
			}

		// Clear filter (if applicable)
		case keyEsc:
			if m.schemaListVisible {
				m.schemaListVisible = false
				m.updatePagination() // Update pagination after closing list
				return nil
			} else if m.filterApplied() {
				m.resetFiltering()
			}

		// Select schema and close list
		case keyEnter:
			m.hideStatusMessage()

			// If schema list is visible and focused, select the current option and close
			if m.schemaListVisible && m.filteringFocus == focusOnSchema {
				m.schemaListVisible = false
				m.updatePagination() // Update pagination after closing list

				// If selected schema is not "*", automatically run a search with this schema
				if i, ok := m.schemaList.SelectedItem().(schemaItem); ok && i.InternalValue != "*" {
					return searchArtifacts(*m)
				}

				return nil
			}

			// If version list is visible, save the selection
			if m.versionListVisible {
				m.versionListVisible = false
				m.updatePagination()

				return nil
			}

			if numDocs == 0 {
				break
			}

			// Load the document from the server. We'll handle the message
			// that comes back in the main update function.
			md := m.selectedMarkdown()

			cmds = append(cmds, m.openMarkdown(md))

		// Filter your notes
		case "/":
			m.hideStatusMessage()
			m.schemaListVisible = false
			m.paginator().Page = 0
			m.setCursor(0)
			m.filterState = filtering
			m.filteringFocus = focusOnSearch

			// Open form with current field values (no query string parsing needed)
			m.filterInput.CursorEnd()
			m.filterInput.Focus()

			// Make sure schemas are loaded when entering search mode
			if !m.schemasLoading && (m.schemaList.Items() == nil || len(m.schemaList.Items()) <= 1) {
				m.schemasLoading = true
				cmds = append(cmds, fetchSchemas())
			}

			return tea.Batch(append(cmds, textinput.Blink)...)

		// Toggle selection for batch download, then advance cursor
		case " ":
			if numDocs > 0 {
				md := m.selectedMarkdown()
				if md != nil && isDownloadable(md) {
					idx := m.markdownIndex()
					if m.selected[idx] {
						delete(m.selected, idx)
					} else {
						m.selected[idx] = true
					}
					m.moveCursorDown()
				}
			}

		// Download
		case "d":
			if numDocs > 0 && m.common.downloader != nil {
				if cmd := m.showDownloadPrompt(); cmd != nil {
					cmds = append(cmds, cmd)
				}
				return tea.Batch(cmds...)
			}

		// Refresh: re-run current search
		case "r":
			m.markdowns = nil
			m.scroll = nil
			m.Total = 0
			m.selected = make(map[int]bool)
			return searchArtifacts(*m)

		// Toggle full help
		case "?":
			m.showFullHelp = !m.showFullHelp
			m.updatePagination()

		// Show errors
		case "!":
			var errorToShow error

			// Check for scroll errors first
			if m.scroll != nil && m.scroll.ScrollError != "" {
				errorToShow = fmt.Errorf("scroll error: %s", m.scroll.ScrollError)
			} else if m.err != nil {
				errorToShow = m.err
			}

			if errorToShow != nil && m.viewState == stashStateReady {
				m.err = errorToShow // Set the error to be displayed
				m.viewState = stashStateShowingError
				return nil
			}
		}

	// Handle mouse clicks on search result items and help bar
	case tea.MouseMsg:
		if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress {
			_, helpHeight := m.helpView()
			// The help bar is near the bottom. Account for possible off-by-one
			// from indent()'s trailing newline by accepting a 2-row click zone.
			helpBarY := m.common.height - helpHeight - 1

			if msg.Y == 3 {
				// Clicked on the search bar — open form
				m.hideStatusMessage()
				m.schemaListVisible = false
				m.paginator().Page = 0
				m.setCursor(0)
				m.filterState = filtering
				m.filteringFocus = focusOnSearch
				m.filterInput.CursorEnd()
				m.filterInput.Focus()
				if !m.schemasLoading && (m.schemaList.Items() == nil || len(m.schemaList.Items()) <= 1) {
					m.schemasLoading = true
					cmds = append(cmds, fetchSchemas())
				}
				return tea.Batch(append(cmds, textinput.Blink)...)
			} else if msg.Y >= helpBarY && !m.showFullHelp {
				// Clicked on the mini help bar — map X to action
				if action := m.helpBarAction(msg.X); action != "" {
					switch action {
					case "/":
						m.hideStatusMessage()
						m.schemaListVisible = false
						m.paginator().Page = 0
						m.setCursor(0)
						m.filterState = filtering
						m.filteringFocus = focusOnSearch
						m.filterInput.CursorEnd()
						m.filterInput.Focus()
						return tea.Batch(append(cmds, textinput.Blink)...)
					case "d":
						if numDocs > 0 && m.common.downloader != nil {
							if cmd := m.showDownloadPrompt(); cmd != nil {
								cmds = append(cmds, cmd)
							}
							return tea.Batch(cmds...)
						}
					case "?":
						m.showFullHelp = !m.showFullHelp
						m.updatePagination()
					case "r":
						m.markdowns = nil
						m.scroll = nil
						m.Total = 0
						m.selected = make(map[int]bool)
						return searchArtifacts(*m)
					case "q":
						return tea.Quit
					case "!":
						if m.err != nil && m.viewState == stashStateReady {
							m.viewState = stashStateShowingError
							return nil
						}
					}
				}
			} else if numDocs > 0 {
				// Clicked on a search result item
				clickY := msg.Y - stashViewTopPadding
				if clickY >= 0 {
					clickedItem := clickY / stashViewItemHeight
					start, _ := m.paginator().GetSliceBounds(len(m.getVisibleMarkdowns()))
					absIndex := start + clickedItem
					mds := m.getVisibleMarkdowns()
					if absIndex >= 0 && absIndex < len(mds) {
						m.setCursor(clickedItem)
						md := mds[absIndex]
						// Scroll item — load more results instead of opening
						if md.Result.Next != "" || md.ScrollError != "" {
							if md.ScrollError == "" && m.scroll != nil {
								m.scroll = nil
								cmds = append(cmds, scroll(*m.common))
							}
						} else if md.ArfifactDBID != "" {
							cmds = append(cmds, m.openMarkdown(md))
						}
					}
				}
			}
		}
	}

	// Update paginator. Pagination key handling is done here, but it could
	// also be moved up to this level, in which case we'd use model methods
	// like model.PageUp().
	newPaginatorModel, cmd := m.paginator().Update(msg)
	m.setPaginator(newPaginatorModel)
	cmds = append(cmds, cmd)

	// Extra paginator keystrokes
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "b":
			// Only handle these keys if schema list is not focused
			if !(m.schemaListVisible && m.filteringFocus == focusOnSchema) {
				m.paginator().PrevPage()
			}
		case "f":
			// Only handle these keys if schema list is not focused
			if !(m.schemaListVisible && m.filteringFocus == focusOnSchema) {
				m.paginator().NextPage()
			}
		}
	}

	// Keep the index in bounds when paginating
	itemsOnPage := m.paginator().ItemsOnPage(len(m.getVisibleMarkdowns()))
	if m.cursor() > itemsOnPage-1 {
		m.setCursor(max(0, itemsOnPage-1))
	}

	// Determine if cursor is over a scroll element, in which case we trigger a scroll command
	items := m.paginator().ItemsOnPage(len(m.getVisibleMarkdowns()))
	if m.paginator().OnLastPage() && m.cursor() == items-1 {
		if m.scroll != nil {
			log.Debug("scroll", "scroll", m.scroll)

			// Check if scroll has an error - if so, don't attempt to scroll
			if m.scroll.ScrollError != "" {
				return nil
			}

			// Normal scroll logic
			m.scroll = nil
			cmds = append(cmds, scroll(*m.common))
		}
	}

	return tea.Batch(cmds...)
}

// Updates for when a user is in the filter editing interface.
func (m *stashModel) handleSearching(msg tea.Msg) tea.Cmd {
	var cmds []tea.Cmd

	// Handle mouse clicks in the search form
	if mouseMsg, ok := msg.(tea.MouseMsg); ok {
		if mouseMsg.Button == tea.MouseButtonLeft && mouseMsg.Action == tea.MouseActionPress {
			rendered := searchFormView(*m)
			lines := strings.Split(rendered, "\n")

			for lineIdx, line := range lines {
				if mouseMsg.Y != lineIdx {
					continue
				}

				// OK/Cancel buttons
				if strings.Contains(line, "OK") && strings.Contains(line, "Cancel") {
					okW := 8
					if mouseMsg.X >= 3 && mouseMsg.X < 3+okW {
						m.filterInput.Blur()
						m.projectInput.Blur()
						m.filterState = filterApplied
						if item, ok := m.schemaList.SelectedItem().(schemaItem); ok && item.InternalValue != "*" {
							m.selectedSchema = item.InternalValue
						} else {
							m.selectedSchema = ""
						}
						m.selectedProject = strings.TrimSpace(m.projectInput.Value())
						if item, ok := m.versionList.SelectedItem().(versionItem); ok {
							m.selectedVersion = item.value
						}
						if m.filterInput.Value() != "" {
							m.filterInput.AddToHistory()
						}
						cmds = append(cmds, tea.ClearScreen)
						cmds = append(cmds, searchArtifacts(*m))
						return tea.Batch(cmds...)
					} else if mouseMsg.X >= 3+okW+2 {
						m.resetFiltering()
						return nil
					}
					break
				}

				// Version dropdown items
				if m.versionListVisible {
					items := m.versionList.Items()
					for idx, item := range items {
						vi, ok := item.(versionItem)
						if !ok {
							continue
						}
						if strings.Contains(line, vi.display) {
							m.versionList.Select(idx)
							m.versionListVisible = false
							m.filteringFocus = focusOnSearch
							m.filterInput.Focus()
							m.updatePagination()
							return nil
						}
					}
				}

				// Schema list items
				if m.schemaListVisible {
					// Check if this line matches a schema list item (has ">" or schema names)
					items := m.schemaList.Items()
					for idx, item := range items {
						si, ok := item.(schemaItem)
						if !ok {
							continue
						}
						if strings.Contains(line, si.DisplayValue) {
							m.schemaList.Select(idx)
							m.schemaListVisible = false
							m.filteringFocus = focusOnSearch
							m.filterInput.Focus()
							m.updatePagination()
							return nil
						}
					}
				}

				// Click on the label line (Project/Version/Schema)
				if strings.Contains(line, "Version:") && strings.Contains(line, "Schema:") {
					versionPrompt := stashInputPromptStyle.Render("Version:")
					versionW := ansi.PrintableRuneWidth("  " + versionPrompt + " " + "All" + "    ")
					if mouseMsg.X >= versionW {
						if !m.schemaListVisible {
							m.filteringFocus = focusOnSchema
							m.filterInput.Blur()
							m.projectInput.Blur()
							m.versionListVisible = false
							m.schemaListVisible = true
							if !m.schemasLoading && (m.schemaList.Items() == nil || len(m.schemaList.Items()) <= 1) {
								m.schemasLoading = true
								cmds = append(cmds, fetchSchemas())
							}
							m.updatePagination()
							return tea.Batch(cmds...)
						}
					} else {
						if !m.versionListVisible {
							m.filteringFocus = focusOnVersion
							m.filterInput.Blur()
							m.projectInput.Blur()
							m.schemaListVisible = false
							m.versionListVisible = true
							projectVal := strings.TrimSpace(m.projectInput.Value())
							if projectVal != "" {
								m.versionsLoading = true
								cmds = append(cmds, fetchVersions(projectVal))
							}
							m.updatePagination()
							return tea.Batch(cmds...)
						}
					}
					break
				}

				// Click on Project input line
				if strings.Contains(line, "Project:") {
					m.filteringFocus = focusOnProject
					m.filterInput.Blur()
					m.projectInput.Focus()
					m.schemaListVisible = false
					m.versionListVisible = false
					m.updatePagination()
					return nil
				}

				// Click on search input line — focus it
				if strings.Contains(line, "Search:") {
					m.filteringFocus = focusOnSearch
					m.filterInput.Focus()
					m.projectInput.Blur()
					m.schemaListVisible = false
					m.versionListVisible = false
					m.updatePagination()
					return nil
				}

				break
			}
		}
		return nil
	}

	// Handle keys
	if msg, ok := msg.(tea.KeyMsg); ok {
		switch msg.String() {
		case "ctrl+k", "ctrl+u":
			if m.filteringFocus == focusOnSearch {
				m.filterInput.Reset()
			}
			if m.filteringFocus == focusOnProject {
				m.projectInput.Reset()
			}
		case keyEsc:
			// Always go back to results
			m.schemaListVisible = false
			m.versionListVisible = false
			m.resetFiltering()
		case keyEnter:
			m.hideStatusMessage()

			// If focus is on schema list and it's visible
			if m.filteringFocus == focusOnSchema && m.schemaListVisible {
				m.schemaListVisible = false
				m.updatePagination()
				// After selecting a schema option, move focus back to search input
				m.filteringFocus = focusOnSearch
				m.filterInput.Focus()
				return nil
			}

			// If focus is on version list and it's visible
			if m.filteringFocus == focusOnVersion && m.versionListVisible {
				m.versionListVisible = false
				m.updatePagination()
				m.filteringFocus = focusOnSearch
				m.filterInput.Focus()
				return nil
			}

			m.filterInput.Blur()
			m.projectInput.Blur()
			m.filterState = filterApplied

			// Sync separate fields from list/input selections
			if item, ok := m.schemaList.SelectedItem().(schemaItem); ok && item.InternalValue != "*" {
				m.selectedSchema = item.InternalValue
			} else {
				m.selectedSchema = ""
			}
			m.selectedProject = strings.TrimSpace(m.projectInput.Value())
			if item, ok := m.versionList.SelectedItem().(versionItem); ok {
				m.selectedVersion = item.value
			}

			if m.filterInput.Value() == "" {
				m.resetFiltering()
			} else {
				m.filterInput.AddToHistory()
			}

			// Force screen clear when starting a new search
			cmds = append(cmds, tea.ClearScreen)
			cmds = append(cmds, searchArtifacts(*m))

		case "tab":
			switch m.filteringFocus {
			case focusOnSearch:
				m.filteringFocus = focusOnProject
				m.filterInput.Blur()
				m.projectInput.Focus()
				m.schemaListVisible = false
				m.versionListVisible = false

			case focusOnProject:
				m.filteringFocus = focusOnVersion
				m.projectInput.Blur()
				m.schemaListVisible = false
				m.versionListVisible = true
				// Fetch versions dynamically if project is set
				projectVal := strings.TrimSpace(m.projectInput.Value())
				if projectVal != "" {
					m.versionsLoading = true
					cmds = append(cmds, fetchVersions(projectVal))
				} else {
					// Reset to static All/Latest
					m.versionList.SetItems([]list.Item{
						versionItem{display: "All", value: ""},
						versionItem{display: "Latest", value: "latest"},
					})
				}

			case focusOnVersion:
				m.filteringFocus = focusOnSchema
				m.versionListVisible = false
				m.schemaListVisible = true
				if !m.schemasLoading && (m.schemaList.Items() == nil || len(m.schemaList.Items()) <= 1) {
					m.schemasLoading = true
					cmds = append(cmds, fetchSchemas())
				}

			case focusOnSchema:
				m.filteringFocus = focusOnSearch
				m.versionListVisible = false
				m.filterInput.Focus()
				m.schemaListVisible = false
			}

			m.updatePagination()
			return tea.Batch(cmds...)

		case "shift+tab":
			switch m.filteringFocus {
			case focusOnSearch:
				m.filterInput.Blur()
				m.filteringFocus = focusOnSchema
				m.versionListVisible = false
				m.schemaListVisible = true
				if !m.schemasLoading && (m.schemaList.Items() == nil || len(m.schemaList.Items()) <= 1) {
					m.schemasLoading = true
					cmds = append(cmds, fetchSchemas())
				}

			case focusOnProject:
				m.filteringFocus = focusOnSearch
				m.projectInput.Blur()
				m.filterInput.Focus()
				m.schemaListVisible = false
				m.versionListVisible = false

			case focusOnVersion:
				m.filteringFocus = focusOnProject
				m.versionListVisible = false
				m.projectInput.Focus()
				m.schemaListVisible = false

			case focusOnSchema:
				m.filteringFocus = focusOnVersion
				m.schemaListVisible = false
				m.versionListVisible = true
				projectVal := strings.TrimSpace(m.projectInput.Value())
				if projectVal != "" {
					m.versionsLoading = true
					cmds = append(cmds, fetchVersions(projectVal))
				} else {
					m.versionList.SetItems([]list.Item{
						versionItem{display: "All", value: ""},
						versionItem{display: "Latest", value: "latest"},
					})
				}
			}

			m.updatePagination()
			return tea.Batch(cmds...)

		case "down":
			// Trigger auto-completion for the next suggestion in the search input
			if m.filteringFocus == focusOnSearch {
				m.filterInput.SuggestNext()
				return nil
			}

		case "up":
			// Trigger auto-completion for the previous suggestion in the search input
			if m.filteringFocus == focusOnSearch {
				m.filterInput.SuggestPrevious()
				return nil
			}

		case "r":
			// Refresh schema list only if in filtering state
			if m.filteringFocus == focusOnSchema && !m.schemasLoading {
				m.schemasLoading = true
				return fetchSchemas()
			}
		}
	}

	// Update the filter text input component if focus is on search
	if m.filteringFocus == focusOnSearch {
		newFilterInputModel, inputCmd := m.filterInput.Update(msg)
		m.filterInput.Model = newFilterInputModel
		cmds = append(cmds, inputCmd)
	}
	// Update project input if focused
	if m.filteringFocus == focusOnProject {
		newProjectInputModel, inputCmd := m.projectInput.Update(msg)
		m.projectInput = newProjectInputModel
		cmds = append(cmds, inputCmd)
	}

	// Update pagination
	m.updatePagination()

	return tea.Batch(cmds...)
}

func generateQuery(m *stashModel) string {
	searchValue := m.filterInput.Value()
	if searchValue == "" {
		searchValue = "*"
	}
	if searchValue != "*" &&
		!strings.HasPrefix(searchValue, "\"") &&
		!strings.Contains(searchValue, ":") &&
		strings.ContainsAny(searchValue, "/-+.!(){}[]^~\\") {
		searchValue = fmt.Sprintf("%q", searchValue)
	}

	var parts []string

	// Project filter — quote if it contains special chars, but not if it has wildcards
	if m.selectedProject != "" {
		if strings.ContainsAny(m.selectedProject, "*?") {
			parts = append(parts, fmt.Sprintf("_extra.project_id:%s", m.selectedProject))
		} else if strings.ContainsAny(m.selectedProject, "/-+.!(){}[]^~\\:") {
			parts = append(parts, fmt.Sprintf("_extra.project_id:\"%s\"", m.selectedProject))
		} else {
			parts = append(parts, fmt.Sprintf("_extra.project_id:%s", m.selectedProject))
		}
	}

	// Version filter
	switch m.selectedVersion {
	case "latest":
		parts = append(parts, "_extra.latest:true")
	case "":
		// "All" — no filter
	default:
		parts = append(parts, fmt.Sprintf("_extra.version:\"%s\"", m.selectedVersion))
	}

	// Schema filter
	if m.selectedSchema != "" && m.selectedSchema != "*" {
		parts = append(parts, fmt.Sprintf("_extra.$schema:\"%s\"", m.selectedSchema))
	}

	parts = append(parts, searchValue)

	generatedSearchValue := strings.Join(parts, " AND ")
	log.Debug(generatedSearchValue)
	return generatedSearchValue
}

func searchArtifacts(m stashModel) tea.Cmd {
	return func() tea.Msg {

		generatedSearchValue := generateQuery(&m)

		// Save search parameters to the ctxDir
		searchParams := PersistSearchParams{
			Q:      generatedSearchValue,
			Fields: m.common.search.Fields,
			Size:   m.common.search.Size,
			Sort:   m.common.search.Sort,
		}
		// Ensure all fields are saved
		if searchParams.Q == "" {
			searchParams.Q = m.common.cfg.DefaultSearchParams.Q
		}
		if searchParams.Fields == "" {
			searchParams.Fields = m.common.cfg.DefaultSearchParams.Fields
		}
		if searchParams.Size == 0 {
			searchParams.Size = m.common.cfg.DefaultSearchParams.Size
		}
		if searchParams.Sort == "" {
			searchParams.Sort = m.common.cfg.DefaultSearchParams.Sort
		}
		err := contextProvider.SaveSearchParams(searchParams)
		if err != nil {
			log.Error("Failed to save search parameters", "err", err)
		}

		return searchedArtifactMsg(generatedSearchValue)
	}
}

// VIEW

func (m stashModel) view() string {
	// When in filtering state, show the search form view instead of regular view
	if m.filterState == filtering {
		return searchFormView(m)
	}

	var s string
	switch m.viewState {
	case stashStateShowingError:
		return errorView(m.err, false)
	case stashStateLoadingDocument:
		s += " " + m.spinner.View() + " Loading document..."
	case stashStateReady:
		loadingIndicator := " "
		if m.shouldSpin() {
			loadingIndicator = m.spinner.View()
		}

		// Search bar + document count
		searchBar := m.searchBarView()
		docCount := m.docCountView()

		// Rules for the logo and status message
		logoOrStatus := " "
		if m.showStatusMessage {
			logoOrStatus += m.statusMessage.String()
		} else {
			logoOrStatus += artifactDBLogoView()
		}

		// Add error indicator if there's an error (including scroll errors)
		if m.err != nil {
			logoOrStatus += " " + redFg("! errors")
		}

		// Only truncate the first line
		logoOrStatus = truncate.StringWithTail(logoOrStatus, uint(m.common.width-1), ellipsis)

		help, helpHeight := m.helpView()

		populatedView := m.populatedView()
		populatedViewHeight := strings.Count(populatedView, "\n") + 2

		// We need to fill any empty height with newlines so the footer reaches
		// the bottom.
		availHeight := m.common.height -
			stashViewTopPadding -
			populatedViewHeight -
			helpHeight -
			stashViewBottomPadding
		blankLines := strings.Repeat("\n", max(0, availHeight))

		var pagination string
		if m.paginator().TotalPages > 1 {
			pagination = m.paginator().View()

			// If the dot pagination is wider than the width of the window
			// use the arabic paginator.
			if ansi.PrintableRuneWidth(pagination) > m.common.width-stashViewHorizontalPadding {
				// Copy the paginator since m.paginator() returns a pointer to
				// the active paginator and we don't want to mutate it. In
				// normal cases, where the paginator is not a pointer, we could
				// safely change the model parameters for rendering here as the
				// current model is discarded after reuturning from a View().
				// One could argue, in fact, that using pointers in
				// a functional framework is an antipattern and our use of
				// pointers in our model should be refactored away.
				p := *(m.paginator())
				p.Type = paginator.Arabic
				pagination = paginationStyle.Render(p.View())
			}
		}

		s += fmt.Sprintf(
			"%s%s\n\n%s\n\n%s\n\n%s\n\n%s  %s\n\n%s",
			loadingIndicator,
			logoOrStatus,
			searchBar,
			docCount,
			populatedView,
			blankLines,
			pagination,
			help,
		)
	}
	return "\n" + indent(s, stashIndent)
}

// searchFormView creates the search form view.
func searchFormView(m stashModel) string {
	var b strings.Builder

	// Title
	title := searchFormTitleStyle.Render(" Search ")
	b.WriteString(fmt.Sprintf("\n\n  %s\n\n", title))

	// Search input (full width, own line)
	b.WriteString(fmt.Sprintf("  %s\n\n", m.filterInput.View()))

	// Project input (full width, own line)
	b.WriteString(fmt.Sprintf("  %s\n\n", m.projectInput.View()))

	// Version + Schema on the same line
	var selectedVersion string
	if item, ok := m.versionList.SelectedItem().(versionItem); ok {
		selectedVersion = item.display
	} else {
		selectedVersion = "All"
	}

	var selectedSchema string
	if item, ok := m.schemaList.SelectedItem().(schemaItem); ok {
		selectedSchema = item.DisplayValue
	} else {
		selectedSchema = "*"
	}
	var schemaDisplay string
	if m.schemasLoading {
		schemaDisplay = selectedSchema + " " + m.spinner.View()
	} else {
		schemaDisplay = selectedSchema
	}
	var versionDisplay string
	if m.versionsLoading {
		versionDisplay = selectedVersion + " " + m.spinner.View()
	} else {
		versionDisplay = selectedVersion
	}

	versionPrompt := stashInputPromptStyle.Render("Version:")
	schemaLabel := stashInputPromptStyle.Render("Schema:") + " " + schemaDisplay

	if m.versionListVisible {
		// Render version list in-place
		versionPromptLabel := stashInputPromptStyle.Render("Version:")
		prefix := "  " + versionPromptLabel + " "
		prefixW := ansi.PrintableRuneWidth(prefix)
		padStr := strings.Repeat(" ", prefixW)
		listLines := strings.Split(m.versionList.View(), "\n")
		first := true
		for _, line := range listLines {
			if strings.TrimSpace(line) == "" {
				continue
			}
			if first {
				b.WriteString(prefix + line + "    " + schemaLabel + "\n")
				first = false
			} else {
				b.WriteString(padStr + line + "\n")
			}
		}
	} else {
		versionPart := versionPrompt + " " + versionDisplay + "    "
		if m.schemaListVisible {
			schemaPrompt := stashInputPromptStyle.Render("Schema:")
			prefix := "  " + versionPart + schemaPrompt + " "
			prefixW := ansi.PrintableRuneWidth(prefix)
			padStr := strings.Repeat(" ", prefixW)
			listLines := strings.Split(m.schemaList.View(), "\n")
			first := true
			for _, line := range listLines {
				if strings.TrimSpace(line) == "" {
					continue
				}
				if first {
					b.WriteString(prefix + line + "\n")
					first = false
				} else {
					b.WriteString(padStr + line + "\n")
				}
			}
		} else {
			b.WriteString(fmt.Sprintf("  %s%s\n", versionPart, schemaLabel))
		}
	}

	// OK / Cancel buttons
	p := palette.Current()
	okStyle := lipgloss.NewStyle().Padding(0, 3).Background(p.Success.Adaptive()).Foreground(p.CodeBg.Adaptive()).Bold(true)
	cancelStyle := lipgloss.NewStyle().Padding(0, 3).Background(p.CodeBg.Adaptive()).Foreground(p.Dim.Adaptive())
	b.WriteString(fmt.Sprintf("\n  %s  %s\n", okStyle.Render("OK"), cancelStyle.Render("Cancel")))

	// Help text
	helpText := []string{
		"Enter/OK: search",
		"Tab: switch fields",
		"Esc/Cancel: back",
	}
	if m.filteringFocus == focusOnSchema {
		helpText = append(helpText, "↑/↓: navigate schemas")
	}
	if m.filteringFocus == focusOnVersion {
		helpText = append(helpText, "↑/↓: navigate versions")
	}
	b.WriteString(fmt.Sprintf("\n  %s\n", subtleStyle.Render(strings.Join(helpText, "  •  "))))

	return indent(b.String(), stashIndent)
}

func artifactDBLogoView() string {
	var instanceInfo InstanceInfo
	if config.InstanceName == "" {
		ctx := contextProvider.GetContextOrDie()
		response := contextProvider.MakeRequest("GET", ctx.Url, nil, map[string]string{}).
			AsJSON(&instanceInfo)
		if response.Err == nil {
			config.InstanceName = instanceInfo.Name
		} else {
			config.InstanceName = "ArtifactDB"
		}
	}

	return logoStyle.Render(fmt.Sprintf(" %s ", config.InstanceName))
}

func (m stashModel) searchBarView() string {
	searchText := m.filterInput.Value()
	if searchText == "" {
		searchText = "(empty)"
	}

	projectLabel := "(all)"
	if m.selectedProject != "" {
		projectLabel = m.selectedProject
	}

	versionLabel := "All"
	switch m.selectedVersion {
	case "latest":
		versionLabel = "Latest"
	case "":
		versionLabel = "All"
	default:
		versionLabel = m.selectedVersion
	}

	schemaLabel := "*"
	if m.selectedSchema != "" {
		schemaLabel = m.selectedSchema
	}

	searchBtn := renderToolbarBtn("/", "Search: "+searchText)
	projectBtn := renderToolbarBtn("", "Project: "+projectLabel)
	versionBtn := renderToolbarBtn("", "Version: "+versionLabel)
	schemaBtn := renderToolbarBtn("", "Schema: "+schemaLabel)

	return "  " + searchBtn + " " + projectBtn + " " + versionBtn + " " + schemaBtn
}

func (m stashModel) docCountView() string {
	localCount := len(m.markdowns)
	totalStr := humanize.Comma(m.Total)
	if !m.loadingDone() {
		if localCount > 0 {
			return grayFg(fmt.Sprintf("  %s Loading... %s fetched (%s total)", m.spinner.View(), humanize.Comma(int64(localCount)), totalStr))
		}
		return grayFg(fmt.Sprintf("  %s Searching...", m.spinner.View()))
	}
	return grayFg(fmt.Sprintf("  %s documents (%s total)", humanize.Comma(int64(localCount)), totalStr))
}

func (m stashModel) populatedView() string {
	mds := m.getVisibleMarkdowns()

	var b strings.Builder

	// Empty states
	if len(mds) == 0 {
		f := func(s string) {
			b.WriteString("  " + grayFg(s))
		}

		switch m.sections[m.sectionIndex].key {
		case documentsSection:
			if m.loadingDone() {
				m.Total = 0
				f("No artifacts found.")
			} else {
				b.WriteString(dimGreenFg(m.common.search.Q))
				f("Searching... ")
			}
		case filterSection:
			return ""
		}
	}

	if len(mds) > 0 {
		start, end := m.paginator().GetSliceBounds(len(mds))
		docs := mds[start:end]

		for i, md := range docs {
			absIdx := start + i
			// Check if this is a scroll item (either normal scroll or error scroll)
			if md.Result.Next != "" || md.ScrollError != "" {
				// This is a scroll item (either normal or error)
				scrollItemView(&b, m, i, md)
			} else if md.ArfifactDBID != "" {
				// This is a regular artifact item
				artifactItemView(&b, m, i, md, absIdx)
			} else {
				// Fallback - shouldn't happen, but log it
				log.Warn("Unknown item type", "md", md)
				artifactItemView(&b, m, i, md, absIdx)
			}
			if i != len(docs)-1 {
				fmt.Fprintf(&b, "\n\n")
			}
		}
	}

	// If there aren't enough items to fill up this page (always the last page)
	// then we need to add some newlines to fill up the space where stash items
	// would have been.
	itemsOnPage := m.paginator().ItemsOnPage(len(mds))
	if itemsOnPage < m.paginator().PerPage {
		n := (m.paginator().PerPage - itemsOnPage) * stashViewItemHeight
		if len(mds) == 0 {
			n -= stashViewItemHeight - 1
		}
		for i := 0; i < n; i++ {
			fmt.Fprint(&b, "\n")
		}
	}

	return b.String()
}

// COMMANDS

func jsonToMarkdown(doc map[string]interface{}, templateFile string) (string, error) {
	log.Info("Loading template", "tpl", templateFile)
	var content []byte
	var err error

	if templateFile == "_default_" {
		content = []byte(DefaultTemplate)
	} else {
		content, err = os.ReadFile(templateFile)
		if err != nil {
			log.Error("Error reading template:", "err", err)
			return "", err
		}
	}
	var mdOutput bytes.Buffer

	tmpl, err := template.New("markdown").Funcs(GetTemplateFuncMap()).Parse(string(content))
	if err != nil {
		log.Error("Error parsing template:", "err", err)
		return "", err
	}

	// Execute the template with the document and write to the buffer
	err = tmpl.Execute(&mdOutput, doc)
	if err != nil {
		log.Error("Error executing template", "err", err)
		return "", err
	}

	return mdOutput.String(), nil

}

func loadMarkdown(md *markdown) tea.Cmd {
	return func() tea.Msg {
		if md.ArfifactDBID == "" {
			return errMsg{errors.New("could not load file: missing url")}
		}

		tenant := ""
		if md.Extra.Tenant != nil {
			tenant = md.Extra.Tenant.Alias
		}
		log.Debug("Fetching", "id", md.ArfifactDBID, "tenant", tenant)
		document, err := client.GetFileMetadata(md.ArfifactDBID, tenant, false, true)
		if err != nil {
			log.Debug("error fetching document", "error", err)
			return errMsg{err}
		}

		// _extra is common for all doc, build a dedicated struct
		extraData, ok := document["_extra"]
		if !ok {
			log.Errorf("Key '_extra' not found in JSON")
			return errMsg{err}
		}

		// Marshal the "_extra" data back to JSON
		extraJSON, err := json.Marshal(extraData)
		if err != nil {
			log.Errorf("Error marshaling '_extra' to JSON: %v", err)
			return errMsg{err}
		}

		var extra *Extra
		err = json.Unmarshal(extraJSON, &extra)
		if err != nil {
			log.Errorf("Error unmarshalling _extra field: %v\n", err)
			return errMsg{err}
		}

		// Select template based on extra content (mainly schema)
		uiConfig := GetUIConfig(extra)

		templateFile := uiConfig.Artifact.TemplateFile
		rawTemplateFile := "_default_"
		if templateFile == "" {
			log.Warn("No template file found, raw rendering only")
			md.TemplateAvailable = false
		} else {
			md.TemplateAvailable = true
		}

		// always render raw
		var rawContent string
		rawContent, err = jsonToMarkdown(document, rawTemplateFile)
		if err != nil {
			log.Error("Error converting JSON to raw outpout", "err", err)
			humanErr := fmt.Errorf("Error converting JSON to raw output, check logs\n%s", err)
			return errMsg{humanErr}
		}
		md.RawContent = rawContent

		var mdContent string
		if templateFile != "" {
			mdContent, err = jsonToMarkdown(document, templateFile)
			if err != nil {
				log.Error("Error converting JSON to Markdown", "err", err)
				humanErr := fmt.Errorf("Error converting JSON to markdown, template file '%s' not found, or unable to use (check logs)\n%s", templateFile, err)
				return errMsg{humanErr}
			}
			md.Terms = extractHighlightTerms(md.Highlights)
			md.Body = mdContent
		}

		md.Extra = *extra

		return fetchedMarkdownMsg(md)
	}
}

// extractHighlightTerms extracts unique search terms from highlight snippets.
// Highlights contain <em>term</em> markers from the search API.
func extractHighlightTerms(highlights map[string][]string) []string {
	seen := make(map[string]bool)
	var terms []string
	emRegex := regexp.MustCompile(`<em>([^<]+)</em>`)
	for _, snippets := range highlights {
		for _, snippet := range snippets {
			for _, match := range emRegex.FindAllStringSubmatch(snippet, -1) {
				term := match[1]
				lower := strings.ToLower(term)
				if !seen[lower] {
					seen[lower] = true
					terms = append(terms, term)
				}
			}
		}
	}
	return terms
}

type versionItem struct {
	display string
	value   string
}

func (i versionItem) FilterValue() string { return i.display }

type versionItemDelegate struct{}

func (d versionItemDelegate) Height() int                             { return 1 }
func (d versionItemDelegate) Spacing() int                            { return 0 }
func (d versionItemDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }
func (d versionItemDelegate) Render(w io.Writer, m list.Model, index int, listItem list.Item) {
	i, ok := listItem.(versionItem)
	if !ok {
		return
	}

	fn := itemStyle.Render
	if index == m.Index() {
		fn = func(s ...string) string {
			return selectedItemStyle.Render("> " + strings.Join(s, " "))
		}
	}

	fmt.Fprint(w, fn(i.display))
}
