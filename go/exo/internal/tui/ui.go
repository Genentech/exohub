package tui

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/log"
	te "github.com/muesli/termenv"
	"slices"
)

const (
	statusMessageTimeout = time.Second * 3 // how long to show status messages like "stashed!"
	ellipsis             = "…"
	listHeight           = 5 // limit to 5 items in the dropdown list
)

var (
	config              Config
	itemStyle           = lipgloss.NewStyle().PaddingLeft(4)
	selectedItemStyle   = lipgloss.NewStyle().PaddingLeft(2).Foreground(lipgloss.Color("170"))
	listPaginationStyle = list.DefaultStyles().PaginationStyle.PaddingLeft(4)
	helpStyle           = list.DefaultStyles().HelpStyle.PaddingLeft(4).PaddingBottom(1)
)

// NewProgram returns a new Tea program.
// The caller must provide implementations of Client, ContextProvider, and
// UIConfigProvider to decouple the TUI from specific backend implementations.
func NewProgram(cfg Config, c Client, cp ContextProvider, ucp UIConfigProvider) *tea.Program {
	log.Debug(
		"Starting tui",
		"high_perf_pager",
		cfg.HighPerformancePager,
		"glamour",
		cfg.GlamourEnabled,
	)

	// Store providers for package-level access
	client = c
	contextProvider = cp
	uiConfigProvider = ucp

	// Load search parameters: CLI flags (already in cfg.DefaultSearchParams) take
	// precedence over saved search_params.json, which takes precedence over UI config defaults.
	hasCLIParams := cfg.DefaultSearchParams.Q != "" ||
		cfg.DefaultSearchParams.Project != "" ||
		cfg.DefaultSearchParams.Version != "" ||
		cfg.DefaultSearchParams.Schema != "" ||
		cfg.DefaultSearchParams.Latest
	searchParams, err := contextProvider.LoadSearchParams()
	if err != nil {
		log.Error("Failed to load search parameters", "err", err)
	} else {
		uiConfig := GetUIConfig(&Extra{})
		if !hasCLIParams {
			// No CLI flags — use saved params
			cfg.DefaultSearchParams = SearchParams{
				Q:      searchParams.Q,
				Fields: searchParams.Fields,
				Size:   searchParams.Size,
				Sort:   searchParams.Sort,
			}
		}
		// Fill empty fields from UI config defaults
		if cfg.DefaultSearchParams.Q == "" {
			cfg.DefaultSearchParams.Q = uiConfig.Search.Query.Value
		}
		if cfg.DefaultSearchParams.Fields == "" {
			cfg.DefaultSearchParams.Fields = uiConfig.Search.Fields.Value
		}
		if cfg.DefaultSearchParams.Size == 0 {
			cfg.DefaultSearchParams.Size = uiConfig.Search.Size.Value
		}
		if cfg.DefaultSearchParams.Sort == "" {
			cfg.DefaultSearchParams.Sort = uiConfig.Search.Sort.Value
		}
		log.Debug("ui.go init",
			"hasCLIParams", hasCLIParams,
			"savedQ", searchParams.Q,
			"uiConfigQueryValue", uiConfig.Search.Query.Value,
			"finalDefaultQ", cfg.DefaultSearchParams.Q,
			"finalDefaultFields", cfg.DefaultSearchParams.Fields,
		)
	}

	config = cfg

	opts := []tea.ProgramOption{tea.WithAltScreen()}
	if cfg.EnableMouse {
		opts = append(opts, tea.WithMouseCellMotion())
	}
	m := newModel(cfg)
	p := tea.NewProgram(m, opts...)

	// In serve mode, watch for SIGUSR1 to open a document (browser forward nav)
	if cfg.ServeMode {
		go watchNavSignal(p)
	}

	return p
}

// openDocMsg is sent when the browser forward button requests opening a document.
type openDocMsg struct{ id string }

// watchNavSignal listens for SIGUSR1 and reads the doc ID from the nav file.
func watchNavSignal(p *tea.Program) {
	navDir := os.Getenv("EXO_NAV_DIR")
	if navDir == "" {
		return
	}
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGUSR1)
	for range sigCh {
		navFile := navDir + "/nav"
		data, err := os.ReadFile(navFile)
		if err != nil {
			continue
		}
		os.Remove(navFile)
		docID := strings.TrimSpace(string(data))
		if docID != "" {
			p.Send(openDocMsg{id: docID})
		}
	}
}

type errMsg struct{ err error }

func (e errMsg) Error() string { return e.err.Error() }

type (
	initArtifactSearchMsg struct {
		qs     string
		scroll string
		ch     chan SearchResponse
	}
)

type (
	foundArtifactMsg SearchResult
	foundScrollMsg   SearchResult
	foundErrorMsg    SearchResult

	artifactSearchFinished  struct{}
	statusMessageTimeoutMsg applicationContext
	gotHistorySuccessMsg    []string
)

// applicationContext indicates the area of the application something applies
// to. Occasionally used as an argument to commands and messages.
type applicationContext int

const (
	stashContext applicationContext = iota
	pagerContext
)

// state is the top-level application state.
type state int

const (
	stateShowStash state = iota
	stateShowDocument
)

func (s state) String() string {
	return map[state]string{
		stateShowStash:    "showing file listing",
		stateShowDocument: "showing document",
	}[s]
}

type SearchParams struct {
	Q       string
	Fields  string
	Size    int
	Sort    string
	Latest  bool
	Project string
	Version string
	Schema  string
}

// Common stuff we'll need to access in all models.
type commonModel struct {
	cfg        Config
	qs         string
	scroll     string
	search     SearchParams
	width      int
	height     int
	downloader Downloader
	outputDir  string
}

type model struct {
	common   *commonModel
	state    state
	fatalErr error

	// Sub-models
	stash     stashModel
	pager     pagerModel
	downloads downloadQueue // persistent background download queue

	// Channel that receives ArtifactDB documents
	artifactFinder chan SearchResult
	response       chan SearchResponse
	total          chan int64
}

// unloadDocument unloads a document from the pager. Note that while this
// method alters the model we also need to send along any commands returned.
func (m *model) unloadDocument() []tea.Cmd {
	m.state = stateShowStash
	m.stash.viewState = stashStateReady
	m.pager.unload()
	m.pager.showHelp = false
	if m.common.cfg.ServeMode {
		fmt.Fprint(os.Stdout, "\x1b]777;doc-link-hide\x07")
	}

	var batch []tea.Cmd
	if m.pager.viewport.HighPerformanceRendering {
		batch = append(batch, tea.ClearScrollArea)
	}

	if !m.stash.shouldSpin() {
		batch = append(batch, m.stash.spinner.Tick)
	}
	return batch
}

func newModel(cfg Config) tea.Model {
	initSections()

	if cfg.GlamourStyle == styles.AutoStyle {
		if te.HasDarkBackground() {
			cfg.GlamourStyle = styles.DarkStyle
		} else {
			cfg.GlamourStyle = styles.LightStyle
		}
	}

	common := commonModel{
		cfg:        cfg,
		downloader: downloader,
		outputDir:  outputDir,
	}

	return model{
		common:    &common,
		state:     stateShowStash,
		pager:     newPagerModel(&common),
		stash:     newStashModel(&common),
		downloads: newDownloadQueue(cfg.ServeMode),
	}
}

func (m model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.stash.spinner.Tick}

	// Trigger the initial search using searchArtifacts
	cmds = append(cmds, searchArtifacts(m.stash))

	// If an initial document was specified, open it directly
	if m.common.cfg.InitialDocument != "" {
		md := &markdown{
			ArfifactDBID: m.common.cfg.InitialDocument,
			Extra: Extra{
				ID: m.common.cfg.InitialDocument,
			},
		}
		cmds = append(cmds, m.stash.openMarkdown(md))
	}

	return tea.Batch(cmds...)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// If there's been an error, any key exits
	if m.fatalErr != nil {
		if _, ok := msg.(tea.KeyMsg); ok {
			return m, tea.Quit
		}
	}

	var cmds []tea.Cmd

	// Handle download queue at top level (dialog + progress messages)
	{
		dialogWasOpen := m.downloads.dialogVisible()
		cmd, handled := m.downloads.update(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		// If dialog was just dismissed, restore pager viewport
		if dialogWasOpen && !m.downloads.dialogVisible() {
			if m.state == stateShowDocument && m.pager.viewport.HighPerformanceRendering {
				cmds = append(cmds, viewport.Sync(m.pager.viewport))
			}
		}
		// Surface download errors on the stash status bar (auto-hide after 3s)
		if m.downloads.lastError != nil {
			cmd := m.stash.showTimedStatusMessage(errorStatusMessage, m.downloads.lastError.Error())
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
			m.downloads.lastError = nil
		}
		if handled {
			return m, tea.Batch(cmds...)
		}
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			// Close download pane if open
			if m.downloads.paneOpen {
				m.downloads.paneOpen = false
				m.stash.setSize(m.common.width, m.common.height)
				m.pager.setSize(m.common.width, m.common.height)
				if m.state == stateShowDocument && m.pager.viewport.HighPerformanceRendering {
					cmds = append(cmds, viewport.Sync(m.pager.viewport))
				}
				return m, tea.Batch(cmds...)
			}
			if m.state == stateShowDocument || m.stash.viewState == stashStateLoadingDocument {
				batch := m.unloadDocument()
				return m, tea.Batch(batch...)
			}

		case "t":
			// Toggle download pane/panel — but not when a text input is focused
			if m.state == stateShowStash && m.stash.filterState == filtering && (m.stash.filteringFocus == focusOnSearch || m.stash.filteringFocus == focusOnProject) {
				break // let the stash handle it as text input
			}
			if m.downloads.serveMode {
				fmt.Fprint(os.Stdout, "\x1b]777;toggle-downloads\x07")
			} else {
				m.downloads.paneOpen = !m.downloads.paneOpen
			}
			return m, tea.Batch(cmds...)

		case "v":
			// Toggle select mode (mouse off for text selection) — serve mode only
			if m.state == stateShowStash && m.stash.filterState == filtering && (m.stash.filteringFocus == focusOnSearch || m.stash.filteringFocus == focusOnProject) {
				break // let the stash handle it as text input
			}
			if m.common.cfg.ServeMode {
				fmt.Fprint(os.Stdout, "\x1b]777;toggle-select\x07")
			}
			return m, tea.Batch(cmds...)

		case "q":
			// Quit with confirmation if downloads active
			if m.downloads.hasActive() {
				m.downloads.showQuitConfirm()
				if m.state == stateShowDocument && m.pager.viewport.HighPerformanceRendering {
					cmds = append(cmds, tea.ClearScrollArea)
				}
				return m, tea.Batch(cmds...)
			}

			var cmd tea.Cmd
			switch m.state {
			case stateShowStash:
				if m.stash.filterState == filtering {
					m.stash, cmd = m.stash.update(msg)
					return m, cmd
				}
			}
			return m, tea.Quit

		case "left":
			if m.state == stateShowDocument {
				cmds = append(cmds, m.unloadDocument()...)
				return m, tea.Batch(cmds...)
			}

		case "ctrl+z":
			return m, tea.Suspend

		case "ctrl+c":
			if m.downloads.hasActive() {
				m.downloads.showQuitConfirm()
				if m.state == stateShowDocument && m.pager.viewport.HighPerformanceRendering {
					cmds = append(cmds, tea.ClearScrollArea)
				}
				return m, tea.Batch(cmds...)
			}
			return m, tea.Quit
		}

	// Logo click in pager → go back to stash
	case pagerBackMsg:
		if m.state == stateShowDocument {
			batch := m.unloadDocument()
			return m, tea.Batch(batch...)
		}

	// Browser forward button — open a document by ID
	case openDocMsg:
		md := &markdown{
			ArfifactDBID: msg.id,
			Extra:        Extra{ID: msg.id},
		}
		cmds = append(cmds, m.stash.openMarkdown(md))
		return m, tea.Batch(cmds...)

	// Window size is received when starting up and on every resize
	case tea.WindowSizeMsg:
		m.common.width = msg.Width
		m.common.height = msg.Height
		m.downloads.setSize(msg.Width, msg.Height)
		paneH := m.downloads.paneHeight()
		m.stash.setSize(msg.Width, msg.Height-paneH)
		m.pager.setSize(msg.Width, msg.Height-paneH)

	case initArtifactSearchMsg:
		m.response = msg.ch
		m.common.qs = msg.qs
		m.common.scroll = msg.scroll
		m.artifactFinder = make(chan SearchResult)
		m.total = make(chan int64)
		m.stash.err = nil
		go func() {
			defer close(m.artifactFinder)
			defer close(m.total)
			for response := range m.response {
				for _, result := range response.Results {
					m.artifactFinder <- result
					m.total <- response.Total
				}
				// Signify end of results, need to scroll for more
				if response.Next != "" {
					m.artifactFinder <- SearchResult{
						Next: response.Next,
					}
				}
				if response.Error != "" {
					m.artifactFinder <- SearchResult{
						Error: response.Error,
					}
					m.total <- 0
					return
				}
			}
		}()
		cmds = append(cmds, findNextArtifact(m))

	case fetchedMarkdownMsg:
		// We've loaded a markdown file's contents for rendering
		m.pager.currentDocument = *msg

		// Auto-close highlights pane if new document has no highlights
		if m.pager.showHighlights && len(msg.Highlights) == 0 {
			m.pager.showHighlights = false
		}

		// If template isn't available, default to RAW mode
		if !msg.TemplateAvailable {
			m.pager.displayMode = displayModeRaw
			cmds = append(cmds, renderWithGlamour(m.pager, msg.RawContent))
		} else {
			body := string(RemoveFrontmatter([]byte(msg.Body)))
			cmds = append(cmds, renderWithGlamour(m.pager, body))
		}

	case contentRenderedMsg:
		m.state = stateShowDocument
		if m.common.cfg.ServeMode {
			fmt.Fprintf(os.Stdout, "\x1b]777;doc-link;%s\x07", m.pager.currentDocument.ArfifactDBID)
		}

	case artifactSearchFinished:
		// Always pass these messages to the stash so we can keep it updated
		// about network activity, even if the user isn't currently viewing
		// the stash.
		stashModel, cmd := m.stash.update(msg)
		m.stash = stashModel
		// Update browser URL with search params in serve mode
		if m.common.cfg.ServeMode {
			project, version, text, schema, latest := extractSearchTextFull(m.common.search.Q)
			params := url.Values{}
			if text != "" && text != "*" {
				params.Set("q", text)
			}
			if schema != "" {
				params.Set("schema", schema)
			}
			if project != "" {
				params.Set("project", project)
			}
			if version != "" {
				params.Set("version", version)
			} else if latest {
				params.Set("latest", "true")
			}
			fmt.Fprintf(os.Stdout, "\x1b]777;search-params;%s\x07", params.Encode())
		}
		return m, cmd

	case foundArtifactMsg:
		newMd := artifactToMarkdown(m.common.cfg.RootURL, SearchResult(msg))
		m.stash.addMarkdowns(newMd)
		// TODO: each time we add an artifact, we update the total, we should be able to
		// do that only once since the total is returned for one scroll, but I've had issue
		// dealing with 2 channel (results + total) that are fed in the same loop but consume
		// recursively through findNextArtifact(...), causing blocking call
		resTotal, _ := <-m.total
		m.stash.Total = resTotal
		cmds = append(cmds, findNextArtifact(m))

	case foundScrollMsg:
		newMd := scrollToMarkdown(SearchResult(msg))
		m.stash.addScroll(newMd)
		cmds = append(cmds, findNextArtifact(m))

	case foundErrorMsg:
		log.Debug("Processing foundErrorMsg", "error", msg.Error)

		// Set the error in the stash model so it can be displayed with "!"
		m.stash.err = fmt.Errorf("scroll error: %s", msg.Error)

		// Create a scroll item with error information that replaces "Select to load more results..."
		errorScroll := &markdown{
			ArfifactDBID: "", // Make sure this is empty for scroll items
			Result: SearchResult{
				Error: msg.Error,
				// Make sure Next is empty for error scrolls
				Next: "",
			},
			ScrollError: msg.Error,
			// Make sure other fields are empty/default
			Extra:      Extra{},
			Highlights: make(map[string][]string),
		}

		log.Debug("Adding error scroll", "errorScroll", errorScroll)
		m.stash.addScroll(errorScroll)

		// Continue processing to finish the artifact search
		cmds = append(cmds, findNextArtifact(m))

	case stashDownloadMsg:
		// Stash requested download dialog
		if msg.artifact != nil {
			cmd := m.downloads.showForArtifact(msg.artifact, m.common.downloader)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		} else if len(msg.ids) > 0 {
			m.downloads.showForBatch(msg.ids, msg.size)
		}
		return m, tea.Batch(cmds...)

	case pagerDownloadMsg:
		// Pager requested download — show popup via download queue
		cmd := m.downloads.showForArtifact(&msg.doc, m.common.downloader)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		if m.pager.viewport.HighPerformanceRendering {
			cmds = append(cmds, tea.ClearScrollArea)
		}
		return m, tea.Batch(cmds...)

	case queueDownloadMsg:
		// Dialog confirmed — enqueue downloads
		if msg.projectID != "" {
			cmd := m.downloads.enqueueProject(msg.projectID, msg.version, msg.outputDir)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		} else {
			cmd := m.downloads.enqueue(msg.ids)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		// Resize to account for newly opened pane
		paneH := m.downloads.paneHeight()
		m.stash.setSize(m.common.width, m.common.height-paneH)
		m.pager.setSize(m.common.width, m.common.height-paneH)
		// Start listening for progress
		cmds = append(cmds, m.downloads.listenForProgress())
		return m, tea.Batch(cmds...)

	case searchedArtifactMsg:
		uiConfig := GetUIConfig(&Extra{})
		m.common.search.Q = string(msg)
		// Note: changing fields from a form in the CLI could 1) break views, 2) not necessary
		m.common.search.Fields = uiConfig.Search.Fields.Value
		// Note: same here, not necessary to take value from a form
		m.common.search.Size = uiConfig.Search.Size.Value
		// TODO: could take value from a form. For now from config
		m.common.search.Sort = uiConfig.Search.Sort.Value
		//m.common.search.Latest = true
		m.stash.markdowns = nil
		m.stash.scroll = nil // Clear the scroll to ensure old results aren't displayed
		m.stash.Total = 0    // Reset the total counter when starting a new search
		// normalize/sanitize, ensuring _extra is always their
		m.common.search.Fields = SanitizeFields(m.common.search.Fields)
		cmds = append(cmds, search(*m.common))

	}

	// Process children — skip when dialog is active to prevent viewport redraws
	if !m.downloads.dialogVisible() {
		switch m.state {
		case stateShowStash:
			newStashModel, cmd := m.stash.update(msg)
			m.stash = newStashModel
			cmds = append(cmds, cmd)

		case stateShowDocument:
			newPagerModel, cmd := m.pager.update(msg)
			m.pager = newPagerModel
			cmds = append(cmds, cmd)
		}
	}

	return m, tea.Batch(cmds...)
}

func (m model) View() string {
	if m.fatalErr != nil {
		return errorView(m.fatalErr, true)
	}

	// Dialog overlay takes over the entire screen
	if m.downloads.dialogVisible() {
		return m.downloads.dialogView(m.common.width, m.common.height)
	}

	var view string
	switch m.state {
	case stateShowDocument:
		view = m.pager.View()
	default:
		view = m.stash.view()
	}

	// Append download progress pane at the bottom
	if pane := m.downloads.paneView(m.common.width); pane != "" {
		view += "\n" + pane
	}

	return view
}

func errorView(err error, fatal bool) string {
	exitMsg := "press any key to "
	if fatal {
		exitMsg += "exit"
	} else {
		exitMsg += "return"
	}
	s := fmt.Sprintf("%s\n\n%v\n\n%s",
		errorTitleStyle.Render("ERROR"),
		err,
		subtleStyle.Render(exitMsg),
	)
	return "\n" + indent(s, 3)
}

// COMMANDS

func search(m commonModel) tea.Cmd {
	// TODO ADB: /search
	return func() tea.Msg {
		var searchQuery string
		log.Error(m.search)
		// Check if m.search is empty
		if isSearchParamsEmpty(m.search) {
			params := SearchParams{
				Q:      DEFAULT_SEARCH_Q,
				Fields: DEFAULT_SEARCH_FIELDS,
				Size:   DEFAULT_SEARCH_SIZE,
				Sort:   DEFAULT_SEARCH_SORT,
				Latest: DEFAULT_SEARCH_LATEST,
			}
			m.search = params
		}
		if m.search.Size == 0 {
			m.search.Size = 10
		}
		if m.search.Sort == "" {
			m.search.Sort = "-_extra.uploaded,_extra.id"
		}
		// ensure _extra always there
		m.search.Fields = SanitizeFields(m.search.Fields)
		size := strconv.FormatInt(int64(m.search.Size), 10) // Base 10 for decimal
		params := url.Values{}
		params.Add("q", m.search.Q)
		params.Add("fields", m.search.Fields)
		params.Add("sort", m.search.Sort)
		params.Add("size", size)
		params.Add("latest", strconv.FormatBool(m.search.Latest))
		params.Add("highlight", "*")
		log.Debug("params", "params", params.Encode())
		searchQuery = params.Encode()
		var (
			err   error
			ch    chan SearchResponse
			errCh chan error
		)

		log.Debug("search", "params", searchQuery)

		// Switch between FindFiles and FindAllFiles to bypass .gitignore rules
		ch, errCh = client.Search(searchQuery)
		err = <-errCh
		if err != nil {
			log.Error("error fetching search results", "error", err)
			return errMsg{err}
		}

		return initArtifactSearchMsg{ch: ch, qs: searchQuery}
	}
}

func scroll(m commonModel) tea.Cmd {
	// TODO ADB: /search
	return func() tea.Msg {
		var (
			err   error
			ch    chan SearchResponse
			errCh chan error
		)

		// Switch between FindFiles and FindAllFiles to bypass .gitignore rules
		log.Debug("scroll", "scroll", m.scroll)
		ch, errCh = client.Scroll(m.scroll)
		err = <-errCh

		if err != nil {
			log.Error("error fetching scroll results", "error", err)
			return errMsg{err}
		}

		return initArtifactSearchMsg{ch: ch, scroll: m.scroll}
	}
}

func findNextArtifact(m model) tea.Cmd {
	return func() tea.Msg {
		res, ok := <-m.artifactFinder

		if ok {
			m.common.scroll = res.Next
			if res.Next != "" {
				log.Debug("Found scroll", "scroll", res.Next)
				return foundScrollMsg(res)
			} else if res.Error != "" {
				return foundErrorMsg(res)
			} else {
				// Okay now find the next one
				return foundArtifactMsg(res)
			}
		}
		// We're done
		return artifactSearchFinished{}
	}
}

func waitForStatusMessageTimeout(appCtx applicationContext, t *time.Timer) tea.Cmd {
	return func() tea.Msg {
		<-t.C
		return statusMessageTimeoutMsg(appCtx)
	}
}

// ETC

// Convert a models result to an internal representation of a markdown
// document. Note that we could be doing things like checking if the file is
// a directory, but we trust that models has already done that.
func artifactToMarkdown(url string, res SearchResult) *markdown {
	md := &markdown{
		ArfifactDBID: res.Extra.ID,
		Result:       res,
		Extra:        res.Extra,
		Highlights:   res.Highlights,
	}

	return md
}

func scrollToMarkdown(res SearchResult) *markdown {
	md := &markdown{
		// We just propagate the result page, which contains the scroll field
		Result:      res,
		ScrollError: res.Error, // Capture any scroll error
	}
	return md
}

// Lightweight version of reflow's indent function.
func indent(s string, n int) string {
	if n <= 0 || s == "" {
		return s
	}
	l := strings.Split(s, "\n")
	b := strings.Builder{}
	i := strings.Repeat(" ", n)
	for _, v := range l {
		fmt.Fprintf(&b, "%s%s\n", i, v)
	}
	return b.String()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// markdownItem implements list.Item for the list UI
type markdownItem struct {
	md *markdown
}

func (i markdownItem) FilterValue() string {
	if i.md != nil && i.md.Extra.ID != "" {
		return i.md.Extra.ID
	}
	return ""
}

// markdownItemDelegate implements the list item delegate interface for markdown items
type markdownItemDelegate struct{}

func (d markdownItemDelegate) Height() int                             { return 1 }
func (d markdownItemDelegate) Spacing() int                            { return 0 }
func (d markdownItemDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }
func (d markdownItemDelegate) Render(w io.Writer, m list.Model, index int, listItem list.Item) {
	i, ok := listItem.(markdownItem)
	if !ok || i.md == nil {
		return
	}

	displayText := getMarkdownTitle(i.md)

	fn := itemStyle.Render
	if index == m.Index() {
		fn = func(s ...string) string {
			return selectedItemStyle.Render("> " + strings.Join(s, " "))
		}
	}

	fmt.Fprint(w, fn(displayText))
}

// Helper function to get a display title for markdown items
func getMarkdownTitle(md *markdown) string {
	if md == nil {
		return "Unknown"
	}

	id := md.Extra.ID
	if id == "" {
		return "No ID available"
	}

	// Use shorter ID or add more fields as needed
	parts := strings.Split(id, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}

	return id
}
func (tih *TextInputWithHistory) SuggestPrevious() {
	// If there are no suggestions, do nothing
	if len(tih.AvailableSuggestions()) == 0 {
		return
	}

	// Get the current value and suggestions
	currentValue := tih.Value()
	suggestions := tih.AvailableSuggestions()
	slices.Reverse(suggestions)
	suggestions = append([]string{""}, suggestions...) // Add blank suggestion at the start

	// Find the next suggestion that matches the current value
	for i, suggestion := range suggestions {
		if suggestion == currentValue {
			// Move to the next suggestion in the list, stop at the last suggestion
			if i+1 < len(suggestions) {
				tih.SetValue(suggestions[i+1])
				tih.CursorEnd() // Move cursor to the end of the suggestion
			}
			return
		}
	}
}
func (tih *TextInputWithHistory) SuggestNext() {
	// If there are no suggestions, do nothing
	if len(tih.AvailableSuggestions()) == 0 {
		return
	}

	// Get the current value and suggestions
	currentValue := tih.Value()
	suggestions := tih.AvailableSuggestions()
	slices.Reverse(suggestions)
	suggestions = append([]string{""}, suggestions...) // Add blank suggestion at the start

	// Find the previous suggestion that matches the current value
	for i, suggestion := range suggestions {
		if suggestion == currentValue {
			// Move to the previous suggestion in the list, stop at the first suggestion
			if i-1 >= 0 {
				tih.SetValue(suggestions[i-1])
				tih.CursorEnd() // Move cursor to the end of the suggestion
			}
			return
		}
	}
}
func isSearchParamsEmpty(params SearchParams) bool {
	return params.Q == "" && params.Fields == "" && params.Size == 0 && params.Sort == "" && !params.Latest
}
