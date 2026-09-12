package tui

import (
	"encoding/base64"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/log"
	"github.com/dustin/go-humanize"
	runewidth "github.com/mattn/go-runewidth"
	"github.com/muesli/reflow/ansi"
	"github.com/muesli/reflow/truncate"
	"github.com/muesli/termenv"

	"github.com/Genentech/exohub/go/exo/internal/defaults"
	"github.com/Genentech/exohub/go/exo/palette"
)

const (
	statusBarHeight = 1
	lineNumberWidth = 4
)

type displayMode int

const (
	displayModeMarkdown displayMode = iota
	displayModeRaw
)

var (
	pagerHelpHeight int

	mintGreen = lipgloss.AdaptiveColor{Light: "#89F0CB", Dark: "#89F0CB"}
	darkGreen = lipgloss.AdaptiveColor{Light: "#1C8760", Dark: "#1C8760"}

	lineNumberFg = lipgloss.AdaptiveColor{Light: "#656565", Dark: "#7D7D7D"}

	statusBarNoteFg = lipgloss.AdaptiveColor{Light: "#656565", Dark: "#7D7D7D"}
	statusBarBg     = lipgloss.AdaptiveColor{Light: "#E6E6E6", Dark: "#242424"}

	statusBarScrollPosStyle = lipgloss.NewStyle().
				Foreground(lipgloss.AdaptiveColor{Light: "#949494", Dark: "#5A5A5A"}).
				Background(statusBarBg).
				Render

	statusBarNoteStyle = lipgloss.NewStyle().
				Foreground(statusBarNoteFg).
				Background(statusBarBg).
				Render

	statusBarHelpStyle = lipgloss.NewStyle().
				Foreground(statusBarNoteFg).
				Background(lipgloss.AdaptiveColor{Light: "#DCDCDC", Dark: "#323232"}).
				Render

	statusBarBtnStyle = toolbarBtnStyle.Render

	statusBarMessageStyle = lipgloss.NewStyle().
				Foreground(mintGreen).
				Background(darkGreen).
				Render

	statusBarMessageScrollPosStyle = lipgloss.NewStyle().
					Foreground(mintGreen).
					Background(darkGreen).
					Render

	statusBarMessageHelpStyle = lipgloss.NewStyle().
					Foreground(lipgloss.Color("#B6FFE4")).
					Background(green).
					Render

	helpViewStyle = lipgloss.NewStyle().
			Foreground(statusBarNoteFg).
			Background(lipgloss.AdaptiveColor{Light: "#f2f2f2", Dark: "#1B1B1B"}).
			Render

	lineNumberStyle = lipgloss.NewStyle().
			Foreground(lineNumberFg).
			Render
)

type (
	contentRenderedMsg string

	// pagerStatusMessage is an ephemeral note displayed in the pager UI.
	pagerStatusMessage struct {
		message string
		isError bool
	}

	// pagerBackMsg signals the parent to navigate back to stash.
	pagerBackMsg struct{}
)

type pagerState int

const (
	pagerStateBrowse pagerState = iota
	pagerStateStatusMessage
)

type pagerModel struct {
	common         *commonModel
	viewport       viewport.Model
	state          pagerState
	showHelp       bool
	showInfo       bool // Field to track whether to show document info
	showHighlights bool // Field to track whether to show highlights pane

	statusMessage      string
	statusMessageTimer *time.Timer

	// Current document being rendered, sans-glamour rendering. We cache
	// it here so we can re-render it on resize.
	currentDocument markdown

	// Atlas link display
	showLink bool
	linkURL  string

	// Display mode (markdown or raw)
	displayMode displayMode
}

func (m pagerModel) infoView() (s string) {
	italicFn := lipgloss.NewStyle().Italic(true).Render
	codeFn := lipgloss.NewStyle().
		Background(palette.Current().CodeBg.Adaptive()).
		Foreground(palette.Current().Accent.Adaptive()).
		Render

	totalWidth := m.common.width - 4
	fieldColumnWidth := 30
	s += "\n"
	// Define table columns
	columns := []table.Column{
		{Title: infoTitleStyle.Render("Information"), Width: fieldColumnWidth},
		{Title: "", Width: max(0, totalWidth-fieldColumnWidth)},
	}

	// Prefer top-level "size" over _extra.file_size
	docSize := getFileSize(&m.currentDocument)
	humanReadableSize := humanize.Bytes(uint64(docSize))
	uploadedTime := humanize.Time(m.currentDocument.Extra.Uploaded)
	indexedTime := humanize.Time(m.currentDocument.Extra.MetaIndexed)

	var access string = ""
	switch m.currentDocument.Extra.Permissions.ReadAccess {
	case "public":
		access = "🌎 "
	case "viewers":
		access = "🔒 "
	case "none":
		access = "🙈 "
	}
	// Permissions subfields
	permissionsDetails := []table.Row{
		{indent("Read access", 2), fmt.Sprintf("%s%s", access, m.currentDocument.Extra.Permissions.ReadAccess)},
		{indent("Write access", 2), m.currentDocument.Extra.Permissions.WriteAccess},
		{indent("Owners", 2), strings.Join(m.currentDocument.Extra.Permissions.Owners, ", ")},
		{indent("Viewers", 2), strings.Join(m.currentDocument.Extra.Permissions.Viewers, ", ")},
	}

	var latest string = ""
	if m.currentDocument.Extra.Latest {
		latest = fmt.Sprintf(" (%s)", italicFn("🌟 latest"))
	}
	// Define table rows with placeholder data
	rows := []table.Row{
		{"ID", m.currentDocument.ArfifactDBID},
		{"Project", badgeStyle.Render(badgeProjectStyle.Render(m.currentDocument.Extra.ProjectID))},
		{"Version", badgeStyle.Render(badgeVersionStyle.Render(m.currentDocument.Extra.Version)) + latest},
		{"Schema", codeFn(m.currentDocument.Extra.Schema)},
		{"Size", humanReadableSize},
		{"Uploaded", fmt.Sprintf("%s (%s)", m.currentDocument.Extra.Uploaded.String(), italicFn(uploadedTime))},
		{"Indexed", fmt.Sprintf("%s (%s)", m.currentDocument.Extra.MetaIndexed.String(), italicFn(indexedTime))},
		{"Permissions", ""},
	}

	rows = append(rows, permissionsDetails...)

	// Create the table
	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rows),
		table.WithFocused(false),      // Non-interactive table
		table.WithHeight(len(rows)+1), // Adjust height to fit rows
	)

	// Apply table styles
	styles := table.DefaultStyles()
	t.SetStyles(styles)

	// Render the table
	s += t.View()
	s = indent(s, 2)

	return s
}

func newPagerModel(common *commonModel) pagerModel {
	// Init viewport
	vp := viewport.New(0, 0)
	vp.YPosition = 0
	vp.HighPerformanceRendering = config.HighPerformancePager

	return pagerModel{
		common:      common,
		state:       pagerStateBrowse,
		viewport:    vp,
		displayMode: displayModeMarkdown,
	}
}

func (m *pagerModel) setSize(w, h int) {
	m.viewport.Width = w
	m.viewport.Height = h - statusBarHeight

	if m.showHelp {
		if pagerHelpHeight == 0 {
			pagerHelpHeight = strings.Count(m.helpView(), "\n")
		}
		m.viewport.Height -= (statusBarHeight + pagerHelpHeight)
	}

	if m.showInfo {
		// Info pane sits between viewport and status bar: separator(1) + info content
		m.viewport.Height -= strings.Count(m.infoView(), "\n") + 2
	}

	if m.showHighlights {
		m.viewport.Height -= strings.Count(m.highlightsView(), "\n") + 2
	}
}

func (m *pagerModel) setContent(s string) {
	m.viewport.SetContent(s)
}

func (m *pagerModel) toggleHelp() {
	m.showHelp = !m.showHelp
	m.setSize(m.common.width, m.common.height)
	if m.viewport.PastBottom() {
		m.viewport.GotoBottom()
	}
}

func (m *pagerModel) toggleInfo() {
	m.showInfo = !m.showInfo
	m.setSize(m.common.width, m.common.height)
}

func (m *pagerModel) toggleHighlights() {
	m.showHighlights = !m.showHighlights
	m.setSize(m.common.width, m.common.height)
	if m.viewport.PastBottom() {
		m.viewport.GotoBottom()
	}
}

func (m pagerModel) highlightsView() string {
	if len(m.currentDocument.Highlights) == 0 {
		return ""
	}

	pp := palette.Current()
	bg := pp.SearchHighlightBg.Dark
	fg := pp.SearchHighlightFg.Dark
	if palette.CurrentMode() == palette.ModeLight {
		bg = pp.SearchHighlightBg.Light
		fg = pp.SearchHighlightFg.Light
	}
	if themeHighlightBg != "" {
		bg = themeHighlightBg
	}
	if themeHighlightFg != "" {
		fg = themeHighlightFg
	}
	hlStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(bg)).
		Foreground(lipgloss.Color(fg)).
		Bold(true)
	fieldStyle := lipgloss.NewStyle().
		Foreground(pp.Accent.Adaptive()).
		Bold(true)
	emRegex := regexp.MustCompile(`<em>([^<]+)</em>`)

	var lines []string
	for field, snippets := range m.currentDocument.Highlights {
		for _, snippet := range snippets {
			// Replace <em>term</em> with highlighted term
			rendered := emRegex.ReplaceAllStringFunc(snippet, func(match string) string {
				sub := emRegex.FindStringSubmatch(match)
				if len(sub) > 1 {
					return hlStyle.Render(sub[1])
				}
				return match
			})
			// Strip any remaining HTML tags
			rendered = strings.ReplaceAll(rendered, "<em>", "")
			rendered = strings.ReplaceAll(rendered, "</em>", "")
			lines = append(lines, fmt.Sprintf("  %s  %s", fieldStyle.Render(field), rendered))
		}
	}

	// Cap height to avoid overwhelming the viewport
	maxLines := 8
	if len(lines) > maxLines {
		lines = append(lines[:maxLines], fmt.Sprintf("  ... and %d more", len(lines)-maxLines))
	}

	return strings.Join(lines, "\n")
}

func (m *pagerModel) toggleDisplayMode() tea.Cmd {
	if !m.currentDocument.TemplateAvailable {
		// If no template is available, always stay in RAW mode
		return nil
	}

	if m.displayMode == displayModeMarkdown {
		m.displayMode = displayModeRaw
		return renderWithGlamour(*m, m.currentDocument.RawContent)
	} else {
		m.displayMode = displayModeMarkdown
		body := string(RemoveFrontmatter([]byte(m.currentDocument.Body)))
		return renderWithGlamour(*m, body)
	}
}

// Perform stuff that needs to happen after a successful markdown stash. Note
// that the the returned command should be sent back the through the pager
// update function.
func (m *pagerModel) showStatusMessage(msg pagerStatusMessage) tea.Cmd {
	// Show a success message to the user
	m.state = pagerStateStatusMessage
	m.statusMessage = msg.message
	if m.statusMessageTimer != nil {
		m.statusMessageTimer.Stop()
	}
	m.statusMessageTimer = time.NewTimer(statusMessageTimeout)

	return waitForStatusMessageTimeout(pagerContext, m.statusMessageTimer)
}

func (m *pagerModel) unload() {
	if m.showHelp {
		m.toggleHelp()
	}
	if m.statusMessageTimer != nil {
		m.statusMessageTimer.Stop()
	}
	m.state = pagerStateBrowse
	m.showLink = false
	m.viewport.SetContent("")
	m.viewport.YOffset = 0
}

func (m pagerModel) update(msg tea.Msg) (pagerModel, tea.Cmd) {
	var (
		cmd  tea.Cmd
		cmds []tea.Cmd
	)

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", keyEsc:
			if m.state != pagerStateBrowse {
				m.state = pagerStateBrowse
				return m, nil
			}
		case "home", "g":
			m.viewport.GotoTop()
			if m.viewport.HighPerformanceRendering {
				cmds = append(cmds, viewport.Sync(m.viewport))
			}
		case "end", "G":
			m.viewport.GotoBottom()
			if m.viewport.HighPerformanceRendering {
				cmds = append(cmds, viewport.Sync(m.viewport))
			}

			// TODO ADB: it would nice to be able to edit the metadata doc...
		//case "e":
		//	lineno := int(math.RoundToEven(float64(m.viewport.TotalLineCount()) * m.viewport.ScrollPercent()))
		//	if m.viewport.AtTop() {
		//		lineno = 0
		//	}
		//	log.Info(
		//		"opening editor",
		//		"file", m.currentDocument.Result.Extra.ID,
		//		"line", fmt.Sprintf("%d/%d", lineno, m.viewport.TotalLineCount()),
		//	)
		//	return m, openEditor(m.currentDocument.Result.Extra.ID, lineno)

		case "i":
			m.toggleInfo()
			if m.viewport.HighPerformanceRendering {
				cmds = append(cmds, viewport.Sync(m.viewport))
			}

		case "h":
			if len(m.currentDocument.Highlights) > 0 {
				m.toggleHighlights()
				if m.viewport.HighPerformanceRendering {
					cmds = append(cmds, viewport.Sync(m.viewport))
				}
			}

		case "c":
			if m.common.cfg.ServeMode {
				encoded := base64.StdEncoding.EncodeToString([]byte(m.currentDocument.Body))
				fmt.Fprintf(os.Stdout, "\x1b]777;copy-content;%s\x07", encoded)
			} else {
				termenv.Copy(m.currentDocument.Body)
				_ = clipboard.WriteAll(m.currentDocument.Body)
			}
			cmds = append(cmds, m.showStatusMessage(pagerStatusMessage{"Copied contents", false}))

		case "r":
			return m, loadMarkdown(&m.currentDocument)

		case "tab":
			if cmd = m.toggleDisplayMode(); cmd != nil {
				cmds = append(cmds, cmd)
			}

		case "?":
			m.toggleHelp()
			if m.viewport.HighPerformanceRendering {
				cmds = append(cmds, viewport.Sync(m.viewport))
			}

		case "y":
			docID := m.currentDocument.ArfifactDBID
			atlasURL := buildAtlasURL(docID)
			if m.showLink {
				m.showLink = false
			} else if atlasURL != "" {
				m.linkURL = atlasURL
				m.showLink = true
				if m.common.cfg.ServeMode {
					fmt.Fprintf(os.Stdout, "\x1b]777;clipboard;%s\x07", docID)
				} else {
					_ = clipboard.WriteAll(atlasURL)
				}
			}

		}

	case tea.MouseMsg:
		if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress {
			// Status bar is always at a fixed position from the bottom
			statusBarY := m.common.height - statusBarHeight
			if m.showHelp {
				statusBarY -= pagerHelpHeight + 1
			}

			if msg.Y == statusBarY {
				// Clicked on the status bar
				// Layout: logo | note | space | info | [highlights] | reload | mode | scroll | help
				gap := 1
				helpW := ansi.PrintableRuneWidth(renderToolbarBtn("?", "Help"))
				scrollW := 7
				reloadW := ansi.PrintableRuneWidth(renderToolbarBtn("r", "Reload"))
				highlightsW := 0
				if len(m.currentDocument.Highlights) > 0 {
					highlightsW = ansi.PrintableRuneWidth(renderToolbarBtn("h", "Highlights"))
				}
				infoW := ansi.PrintableRuneWidth(renderToolbarBtn("i", "Info"))

				// Walk from right edge: help | scroll | mode | reload | [highlights] | info | ... | logo
				r := m.common.width
				if msg.X >= r-helpW {
					m.toggleHelp()
					if m.viewport.HighPerformanceRendering {
						cmds = append(cmds, viewport.Sync(m.viewport))
					}
				} else if r -= helpW + gap; msg.X >= r-scrollW {
					// scroll percent — no action
				} else {
					r -= scrollW + gap
					// MD|RAW toggle
					var modeW int
					if m.currentDocument.TemplateAvailable {
						modeW = ansi.PrintableRuneWidth(modeToggleStyle.Render(
							fmt.Sprintf("%s|%s", activeTabStyle.Render("MD"), inactiveTabStyle.Render("RAW"))))
					} else {
						modeW = ansi.PrintableRuneWidth(modeToggleStyle.Render(activeTabStyle.Render("RAW")))
					}
					if modeW > 0 && msg.X >= r-modeW && msg.X < r {
						if cmd = m.toggleDisplayMode(); cmd != nil {
							cmds = append(cmds, cmd)
						}
					} else if r -= modeW + gap; msg.X >= r-reloadW && msg.X < r {
						cmds = append(cmds, loadMarkdown(&m.currentDocument))
					} else if highlightsW > 0 {
						if r -= reloadW + gap; msg.X >= r-highlightsW && msg.X < r {
							m.toggleHighlights()
							if m.viewport.HighPerformanceRendering {
								cmds = append(cmds, viewport.Sync(m.viewport))
							}
						} else if r -= highlightsW + gap; msg.X >= r-infoW && msg.X < r {
							m.toggleInfo()
							if m.viewport.HighPerformanceRendering {
								cmds = append(cmds, viewport.Sync(m.viewport))
							}
						} else {
							// Logo or title click → go back to stash
							r -= infoW + gap
							if msg.X < r {
								return m, func() tea.Msg { return pagerBackMsg{} }
							}
						}
					} else if r -= reloadW + gap; msg.X >= r-infoW && msg.X < r {
						m.toggleInfo()
						if m.viewport.HighPerformanceRendering {
							cmds = append(cmds, viewport.Sync(m.viewport))
						}
					} else {
						// Logo or title click → go back to stash
						r -= infoW + gap
						if msg.X < r {
							return m, func() tea.Msg { return pagerBackMsg{} }
						}
					}
				}
			}
		}

	case contentRenderedMsg:
		m.setContent(string(msg))
		if m.viewport.HighPerformanceRendering {
			cmds = append(cmds, viewport.Sync(m.viewport))
		}

	// We've finished editing the document, potentially making changes. Let's
	// retrieve the latest version of the document so that we display
	// up-to-date contents.
	case editorFinishedMsg:
		return m, loadMarkdown(&m.currentDocument)

	// We've received terminal dimensions, either for the first time or
	// after a resize
	case tea.WindowSizeMsg:
		if m.displayMode == displayModeMarkdown {
			return m, renderWithGlamour(m, m.currentDocument.Body)
		} else {
			return m, renderWithGlamour(m, m.currentDocument.RawContent)
		}

	case statusMessageTimeoutMsg:
		m.state = pagerStateBrowse
	}

	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

func (m pagerModel) View() string {
	var b strings.Builder

	fmt.Fprint(&b, m.viewport.View()+"\n")

	// Info pane between viewport and status bar, with separator
	if m.showInfo {
		sep := strings.Repeat("─", m.common.width)
		fmt.Fprint(&b, statusBarNoteStyle(sep)+"\n")
		fmt.Fprint(&b, m.infoView()+"\n")
	}

	// Highlights pane between viewport and status bar
	if m.showHighlights {
		title := "── Highlights "
		titleW := runewidth.StringWidth(title)
		pad := max(0, m.common.width-titleW)
		header := title + strings.Repeat("─", pad)
		fmt.Fprint(&b, paneHeaderStyle.Render(header)+"\n")
		fmt.Fprint(&b, m.highlightsView()+"\n")
	}

	// Status bar — always at the bottom
	m.statusBarView(&b)

	if m.showHelp {
		fmt.Fprint(&b, "\n"+m.helpView())
	}

	return b.String()
}

func (m pagerModel) statusBarView(b *strings.Builder) {
	const (
		minPercent               float64 = 0.0
		maxPercent               float64 = 1.0
		percentToStringMagnitude float64 = 100.0
	)

	showStatusMessage := m.state == pagerStateStatusMessage

	// Logo
	logo := artifactDBLogoView()

	// Scroll percent
	percent := math.Max(minPercent, math.Min(maxPercent, m.viewport.ScrollPercent()))
	scrollPercent := fmt.Sprintf(" %3.f%% ", percent*percentToStringMagnitude)
	if showStatusMessage {
		scrollPercent = statusBarMessageScrollPosStyle(scrollPercent)
	} else {
		scrollPercent = statusBarScrollPosStyle(scrollPercent)
	}

	// "Help" button
	var helpNote string
	if showStatusMessage {
		helpNote = statusBarMessageHelpStyle(" ? Help ")
	} else {
		helpNote = renderToolbarBtn("?", "Help")
	}

	// Note
	var note string
	if m.showLink {
		note = m.linkURL
	} else if showStatusMessage {
		note = m.statusMessage
	} else {
		note = m.currentDocument.Note
	}
	note = truncate.StringWithTail(" "+note+" ", uint(max(0,
		m.common.width-
			ansi.PrintableRuneWidth(logo)-
			ansi.PrintableRuneWidth(scrollPercent)-
			ansi.PrintableRuneWidth(helpNote),
	)), ellipsis)
	if showStatusMessage {
		note = statusBarMessageStyle(note)
	} else {
		note = statusBarNoteStyle(note)
	}

	// Add mode indicator at the top
	modeIndicator := ""
	if m.currentDocument.TemplateAvailable {
		mdStyle := inactiveTabStyle
		rawStyle := inactiveTabStyle

		if m.displayMode == displayModeMarkdown {
			mdStyle = activeTabStyle
		} else {
			rawStyle = activeTabStyle
		}

		modeIndicator = modeToggleStyle.Render(
			fmt.Sprintf("%s|%s",
				mdStyle.Render("MD"),
				rawStyle.Render("RAW")))
	} else {
		// Only RAW available
		modeIndicator = modeToggleStyle.Render(activeTabStyle.Render("RAW"))
	}

	// Action buttons (styled as toolbar buttons)
	infoBtn := renderToolbarBtn("i", "Info")
	reloadBtn := renderToolbarBtn("r", "Reload")

	// Highlights button — only shown when highlights exist
	highlightsBtn := ""
	hasHighlights := len(m.currentDocument.Highlights) > 0
	if hasHighlights {
		highlightsBtn = renderToolbarBtn("h", "Highlights")
	}

	// Button separator
	btnGap := statusBarNoteStyle(" ")

	// Empty space
	btnGapW := ansi.PrintableRuneWidth(btnGap)
	gapCount := 4 // gaps between: mode|info|reload|scroll|help
	if hasHighlights {
		gapCount++ // extra gap for highlights button
	}
	padding := max(0,
		m.common.width-
			ansi.PrintableRuneWidth(logo)-
			ansi.PrintableRuneWidth(note)-
			ansi.PrintableRuneWidth(scrollPercent)-
			ansi.PrintableRuneWidth(modeIndicator)-
			ansi.PrintableRuneWidth(infoBtn)-
			ansi.PrintableRuneWidth(highlightsBtn)-
			ansi.PrintableRuneWidth(reloadBtn)-
			ansi.PrintableRuneWidth(helpNote)-
			btnGapW*gapCount,
	)
	emptySpace := strings.Repeat(" ", padding)
	if showStatusMessage {
		emptySpace = statusBarMessageStyle(emptySpace)
	} else {
		emptySpace = statusBarNoteStyle(emptySpace)
	}

	if hasHighlights {
		fmt.Fprintf(b, "%s%s%s%s%s%s%s%s%s%s%s%s%s%s",
			logo,
			note,
			emptySpace,
			infoBtn, btnGap,
			highlightsBtn, btnGap,
			reloadBtn, btnGap,
			modeIndicator, btnGap,
			scrollPercent, btnGap,
			helpNote,
		)
	} else {
		fmt.Fprintf(b, "%s%s%s%s%s%s%s%s%s%s%s%s",
			logo,
			note,
			emptySpace,
			infoBtn, btnGap,
			reloadBtn, btnGap,
			modeIndicator, btnGap,
			scrollPercent, btnGap,
			helpNote,
		)
	}
}

// buildAtlasURL derives the Atlas web app URL from EXOHUB_API_URL.
// e.g. https://your-exohub-server.example.com/api → https://your-exohub-server.example.com/atlas?doc=...
func buildAtlasURL(docID string) string {
	base := strings.TrimSuffix(defaults.APIBase(), "/api")
	base = strings.TrimSuffix(base, "/")
	return base + "/atlas?doc=" + docID
}

func (m pagerModel) helpView() (s string) {
	col1 := []string{
		"g/home  go to top",
		"G/end   go to bottom",
		"y       copy link",
		"i       information",
		"h       highlights",
		"r       reload",
		"esc     back to results",
		"q       quit",
	}

	s += "\n"
	s += "k/↑      up                  " + col1[0] + "\n"
	s += "j/↓      down                " + col1[1] + "\n"
	s += "b/pgup   page up             " + col1[2] + "\n"
	s += "f/pgdn   page down           " + col1[3] + "\n"
	s += "                             " + col1[4] + "\n"
	s += "                             " + col1[5] + "\n"
	s += "                             " + col1[6] + "\n"
	s += "                             " + col1[7] + "\n"

	// Add tab key help if template is available
	if m.currentDocument.TemplateAvailable {
		s += "\ntab      toggle md/raw view"
	}

	s = indent(s, 2)

	// Fill up empty cells with spaces for background coloring
	if m.common.width > 0 {
		lines := strings.Split(s, "\n")
		for i := 0; i < len(lines); i++ {
			l := runewidth.StringWidth(lines[i])
			n := max(m.common.width-l, 0)
			lines[i] += strings.Repeat(" ", n)
		}

		s = strings.Join(lines, "\n")
	}

	return helpViewStyle(s)
}

// COMMANDS

func renderWithGlamour(m pagerModel, md string) tea.Cmd {
	terms := m.currentDocument.Terms
	return func() tea.Msg {
		s, err := glamourRender(m, md)
		if err != nil {
			log.Error("error rendering with Glamour", "error", err)
			return errMsg{err}
		}
		// Apply search term highlighting after glamour rendering
		s = highlightTermsInANSI(s, terms)
		return contentRenderedMsg(s)
	}
}

// This is where the magic happens.
func glamourRender(m pagerModel, markdown string) (string, error) {
	trunc := lipgloss.NewStyle().MaxWidth(m.viewport.Width - lineNumberWidth).Render

	if !config.GlamourEnabled {
		return markdown, nil
	}

	isCode := !IsMarkdownFile(m.currentDocument.Note)
	width := max(0, min(int(m.common.cfg.GlamourMaxWidth), m.viewport.Width))
	if isCode {
		width = 0
	}

	options := []glamour.TermRendererOption{
		GlamourStyle(m.common.cfg.GlamourStyle, isCode),
		glamour.WithWordWrap(width),
	}

	if m.common.cfg.PreserveNewLines {
		options = append(options, glamour.WithPreservedNewLines())
	}
	r, err := glamour.NewTermRenderer(options...)
	if err != nil {
		return "", err
	}

	if isCode {
		markdown = WrapCodeBlock(markdown, filepath.Ext(m.currentDocument.Note))
	}

	out, err := r.Render(markdown)
	if err != nil {
		return "", err
	}

	if isCode {
		out = strings.TrimSpace(out)
	}

	// trim lines
	lines := strings.Split(out, "\n")

	var content strings.Builder
	for i, s := range lines {
		if isCode || m.common.cfg.ShowLineNumbers {
			content.WriteString(lineNumberStyle(fmt.Sprintf("%"+fmt.Sprint(lineNumberWidth)+"d", i+1)))
			content.WriteString(trunc(s))
		} else {
			content.WriteString(s)
		}

		// don't add an artificial newline after the last split
		if i+1 < len(lines) {
			content.WriteRune('\n')
		}
	}

	return content.String(), nil
}
