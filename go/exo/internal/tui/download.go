package tui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/log"

	"github.com/Genentech/exohub/go/exo/palette"
	runewidth "github.com/mattn/go-runewidth"
)

// --- Item status ---

type itemStatus int

const (
	itemPending itemStatus = iota
	itemActive
	itemCompleted
	itemSkipped
	itemFailed
)

// --- Download item ---

type downloadItem struct {
	id         string
	filename   string
	downloaded int64
	total      int64
	status     itemStatus
	err        error
}

// --- Dialog state (the popup) ---

type dialogState int

const (
	dialogHidden  dialogState = iota
	dialogPopup               // single file: this file / all in project
	dialogConfirm             // batch confirm
	dialogLoading             // resolving files from commit/bundle
	dialogQuit                // quit confirmation
)

type dialogSection int

const (
	sectionRadio dialogSection = iota
	sectionOutputDir
	sectionForce
	sectionButtons
)

// --- Messages ---

type projectInfoMsg struct {
	count     int
	totalSize int64
	err       error
}

type downloadProgressMsg struct {
	itemIndex  int
	filename   string
	downloaded int64
	total      int64
	done       bool
	skipped    bool
	err        error
}

type pagerDownloadMsg struct{ doc markdown }

// resolveTickMsg triggers a re-render of the loading dialog to show progress.
type resolveTickMsg struct{}

func tickResolveProgress() tea.Cmd {
	return tea.Tick(300*time.Millisecond, func(t time.Time) tea.Msg {
		return resolveTickMsg{}
	})
}

// queueDownloadMsg is sent when the dialog confirms — items to add to queue.
type queueDownloadMsg struct {
	ids       []string // individual artifact IDs
	projectID string   // if non-empty, download project instead
	version   string
	outputDir string
}

// --- Download queue (top-level, persistent) ---

const maxActiveDownloads = 4

type downloadQueue struct {
	items     []downloadItem
	paneOpen  bool
	width     int
	height    int
	serveMode bool // hide output dir and force re-download in web app

	// Dialog state
	dialog      dialogState
	section     dialogSection
	radioChoice int
	buttonFocus int
	outputDir   textinput.Model

	// Dialog context
	batchSource  string // artifact ID that triggered batch (commit/bundle), empty for manual selection
	force        bool   // re-download even if file exists
	artifact     *markdown
	projectID    string
	version      string
	projectCount int
	projectTotal int64
	projectReady bool
	selectedIDs  []string
	batchSize    int64

	// Resolution progress (for large bundles/commits)
	// Pointer so copies of downloadQueue (bubbletea model copies) share the same value.
	resolveFetched *int64
	resolveCancel  context.CancelFunc

	// Concurrency
	cancel    context.CancelFunc
	mu        sync.Mutex
	progCh    chan downloadProgressMsg
	lastError error // set when a download fails, consumed by ui.go
}

func newDownloadQueue(serveMode bool) downloadQueue {
	ti := textinput.New()
	ti.Placeholder = "./"
	ti.SetValue("./")
	ti.CharLimit = 256
	ti.Width = 40
	return downloadQueue{
		outputDir: ti,
		serveMode: serveMode,
		progCh:    make(chan downloadProgressMsg, 16),
	}
}

func (q *downloadQueue) setSize(w, h int) {
	q.width = w
	q.height = h
	inputW := q.popupInnerWidth() - 14
	if inputW < 20 {
		inputW = 20
	}
	q.outputDir.Width = inputW
}

func (q *downloadQueue) popupWidth() int {
	w := q.width * 2 / 3
	if w < 50 {
		w = 50
	}
	if w > 80 {
		w = 80
	}
	return w
}

func (q *downloadQueue) popupInnerWidth() int {
	return q.popupWidth() - 6
}

// paneHeight returns the height of the progress pane when visible.
func (q *downloadQueue) paneHeight() int {
	if !q.paneOpen {
		return 0
	}
	// Header (1) + separator (1) + items (capped) + footer (1) + blank (1)
	nItems := len(q.items)
	if nItems > 6 {
		nItems = 6
	}
	if nItems == 0 {
		nItems = 1 // "No downloads" line
	}
	return nItems + 3
}

func (q *downloadQueue) hasActive() bool {
	for _, it := range q.items {
		if it.status == itemActive || it.status == itemPending {
			return true
		}
	}
	return false
}

func (q *downloadQueue) activeCount() int {
	n := 0
	for _, it := range q.items {
		if it.status == itemActive {
			n++
		}
	}
	return n
}

func (q *downloadQueue) completedCount() int {
	n := 0
	for _, it := range q.items {
		if it.status == itemCompleted {
			n++
		}
	}
	return n
}

// --- Dialog methods ---

func (q *downloadQueue) showForArtifact(md *markdown, dl Downloader) tea.Cmd {
	// For commit/bundle docs, show loading dialog while resolving files
	if strings.HasPrefix(md.Extra.Schema, "exohub-commit/") && dl != nil {
		q.batchSource = md.ArfifactDBID
		q.dialog = dialogLoading
		return tea.Batch(resolveCommitFiles(dl, md.ArfifactDBID), tickResolveProgress())
	}
	if strings.HasPrefix(md.Extra.Schema, "exohub-bundle/") && dl != nil {
		q.batchSource = md.ArfifactDBID
		q.dialog = dialogLoading
		return tea.Batch(q.resolveBundleFiles(dl, md.ArfifactDBID), tickResolveProgress())
	}

	q.dialog = dialogPopup
	q.section = sectionRadio
	q.radioChoice = 0
	q.buttonFocus = 0
	q.artifact = md
	q.projectID = md.Extra.ProjectID
	q.version = md.Extra.Version
	q.projectReady = false
	q.projectCount = 0
	q.projectTotal = 0
	q.selectedIDs = nil
	q.outputDir.Blur()

	if dl != nil && q.projectID != "" && q.version != "" {
		return q.fetchProjectInfo(dl, q.projectID, q.version)
	}
	return nil
}

func (q *downloadQueue) showForBatch(ids []string, totalSize int64) {
	q.dialog = dialogConfirm
	q.selectedIDs = ids
	q.batchSize = totalSize
	q.artifact = nil
	if q.serveMode {
		q.section = sectionButtons
		q.buttonFocus = 1 // Cancel focused (OK hidden when over limit)
		if len(ids) <= 20 {
			q.buttonFocus = 0
		}
	} else {
		q.section = sectionOutputDir
		q.buttonFocus = 0
		q.outputDir.Focus()
	}
}

func (q *downloadQueue) showQuitConfirm() {
	q.dialog = dialogQuit
	q.section = sectionButtons
	q.buttonFocus = 1 // default to Cancel
}

func (q *downloadQueue) hideDialog() {
	if q.resolveCancel != nil {
		q.resolveCancel()
		q.resolveCancel = nil
	}
	q.dialog = dialogHidden
	q.artifact = nil
	q.selectedIDs = nil
	q.outputDir.Blur()
}

func (q *downloadQueue) dialogVisible() bool {
	return q.dialog != dialogHidden
}

// --- Queue management ---

// enqueue adds items and starts downloads up to maxActive.
func (q *downloadQueue) enqueue(ids []string) tea.Cmd {
	for _, id := range ids {
		q.items = append(q.items, downloadItem{
			id:     id,
			status: itemPending,
		})
	}
	if !q.serveMode {
		q.paneOpen = true
	}
	return q.startPending()
}

// resolvedProjectMsg carries the file list from a resolved project.
type resolvedProjectMsg struct {
	files  []FileInfo
	outDir string
	err    error
}

// resolvedCommitMsg carries the file list from a resolved commit.
type resolvedCommitMsg struct {
	files []FileInfo
	err   error
}

func resolveCommitFiles(dl Downloader, commitID string) tea.Cmd {
	return func() tea.Msg {
		log.Debug("resolving commit files", "commitID", commitID)
		files, err := dl.ResolveCommitFiles(commitID)
		return resolvedCommitMsg{files: files, err: err}
	}
}

// resolvedBundleMsg carries the file list from a resolved bundle.
type resolvedBundleMsg struct {
	files     []FileInfo
	totalSize int64
	err       error
}

func (q *downloadQueue) resolveBundleFiles(dl Downloader, bundleID string) tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	q.resolveCancel = cancel
	counter := new(int64)
	q.resolveFetched = counter
	return func() tea.Msg {
		log.Debug("resolving bundle files", "bundleID", bundleID)
		files, totalSize, err := dl.ResolveBundleFilesWithProgress(ctx, bundleID, func(fetched int) {
			atomic.StoreInt64(counter, int64(fetched))
		})
		return resolvedBundleMsg{files: files, totalSize: totalSize, err: err}
	}
}

// enqueueProject resolves project files then enqueues each individually.
func (q *downloadQueue) enqueueProject(projectID, version, outDir string) tea.Cmd {
	dl := downloader
	if dl == nil {
		return nil
	}
	if !q.serveMode {
		q.paneOpen = true
	}

	return func() tea.Msg {
		log.Debug("resolving project files", "project", projectID, "version", version)
		files, err := dl.ListProjectFiles(context.Background(), projectID, version)
		return resolvedProjectMsg{files: files, outDir: outDir, err: err}
	}
}

func (q *downloadQueue) startPending() tea.Cmd {
	active := q.activeCount()
	if active >= maxActiveDownloads {
		return nil
	}

	dl := downloader
	if dl == nil {
		return nil
	}

	outDir := q.outputDir.Value()
	if outDir == "" {
		outDir = "./"
	}

	var cmds []tea.Cmd
	for i := range q.items {
		if active >= maxActiveDownloads {
			break
		}
		if q.items[i].status == itemPending {
			q.items[i].status = itemActive
			idx := i
			id := q.items[i].id
			cmds = append(cmds, q.startDownload(dl, idx, id, outDir))
			active++
		}
	}

	if len(cmds) > 0 {
		return tea.Batch(cmds...)
	}
	return nil
}

func (q *downloadQueue) startDownload(dl Downloader, idx int, id, outDir string) tea.Cmd {
	ch := q.progCh
	force := q.force
	return func() tea.Msg {
		log.Debug("starting download", "idx", idx, "id", id, "outDir", outDir, "force", force)
		var wasSkipped bool
		err := dl.DownloadFiles([]string{id}, outDir, force, func(p DownloadProgress) {
			if p.Skipped {
				wasSkipped = true
				return
			}
			ch <- downloadProgressMsg{
				itemIndex:  idx,
				filename:   p.Filename,
				downloaded: p.Downloaded,
				total:      p.Total,
			}
		})
		if err != nil {
			return downloadProgressMsg{itemIndex: idx, done: true, err: err, filename: id}
		}
		return downloadProgressMsg{itemIndex: idx, done: true, skipped: wasSkipped, filename: id}
	}
}

// listenForProgress reads one message from the progress channel.
func (q *downloadQueue) listenForProgress() tea.Cmd {
	ch := q.progCh
	return func() tea.Msg {
		return <-ch
	}
}

func (q *downloadQueue) cancelAll() {
	if q.cancel != nil {
		q.cancel()
	}
}

// --- Update ---

func (q *downloadQueue) update(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case resolveTickMsg:
		// Keep ticking while loading dialog is visible
		if q.dialog == dialogLoading {
			return tickResolveProgress(), true
		}
		return nil, true

	case projectInfoMsg:
		if q.dialog != dialogPopup {
			return nil, true
		}
		if msg.err == nil {
			q.projectCount = msg.count
			q.projectTotal = msg.totalSize
		}
		q.projectReady = true
		return nil, true

	case downloadProgressMsg:
		cmd := q.handleProgress(msg)
		// Keep listening for more progress
		if q.hasActive() {
			return tea.Batch(cmd, q.listenForProgress()), true
		}
		return cmd, true

	case resolvedBundleMsg:
		if msg.err != nil {
			if msg.err == context.Canceled {
				return nil, true // user cancelled — ignore
			}
			q.lastError = msg.err
			return nil, true
		}
		var ids []string
		for _, f := range msg.files {
			ids = append(ids, f.ID)
		}
		q.showForBatch(ids, msg.totalSize)
		return nil, true

	case resolvedCommitMsg:
		if msg.err != nil {
			// No dialog was opened — just show the error on the status bar
			q.lastError = msg.err
			return nil, true
		}
		// Open batch download dialog with resolved files
		var ids []string
		var totalSize int64
		for _, f := range msg.files {
			ids = append(ids, f.ID)
			totalSize += f.Size
		}
		q.showForBatch(ids, totalSize)
		return nil, true

	case resolvedProjectMsg:
		if msg.err != nil {
			q.items = append(q.items, downloadItem{
				id: "project", filename: "project", status: itemFailed, err: msg.err,
			})
			return nil, true
		}
		if q.serveMode && len(msg.files) > 20 {
			// Show the batch dialog with the warning (OK hidden, Cancel only)
			var ids []string
			for _, f := range msg.files {
				ids = append(ids, f.ID)
			}
			q.showForBatch(ids, 0)
			return nil, true
		}
		// Enqueue each file individually
		for _, f := range msg.files {
			q.items = append(q.items, downloadItem{
				id:       f.ID,
				filename: f.Path,
				total:    f.Size,
				status:   itemPending,
			})
		}
		if msg.outDir != "" {
			q.outputDir.SetValue(msg.outDir)
		}
		cmd := q.startPending()
		return tea.Batch(cmd, q.listenForProgress()), true

	case tea.WindowSizeMsg:
		q.setSize(msg.Width, msg.Height)
		return nil, false // don't consume — let children handle too

	case tea.KeyMsg:
		// Dialog keys (modal — consume all keys)
		if q.dialog != dialogHidden {
			return q.updateDialog(msg)
		}

		return nil, false

	case tea.MouseMsg:
		if q.dialog != dialogHidden && msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress {
			return q.handleDialogClick(msg)
		}
		return nil, q.dialog != dialogHidden
	}

	// Textinput blink when dialog output dir focused
	if q.dialog != dialogHidden && q.section == sectionOutputDir {
		var cmd tea.Cmd
		q.outputDir, cmd = q.outputDir.Update(msg)
		return cmd, true
	}

	return nil, false
}

func (q *downloadQueue) handleProgress(msg downloadProgressMsg) tea.Cmd {
	if msg.itemIndex < 0 || msg.itemIndex >= len(q.items) {
		return nil
	}
	it := &q.items[msg.itemIndex]
	if msg.filename != "" {
		it.filename = msg.filename
	}
	it.downloaded = msg.downloaded
	it.total = msg.total

	if msg.done {
		if msg.err != nil {
			it.status = itemFailed
			it.err = msg.err
			q.lastError = msg.err
		} else if msg.skipped {
			it.status = itemSkipped
		} else {
			it.status = itemCompleted
		}
		// Start next pending
		return q.startPending()
	}
	return nil
}

func (q *downloadQueue) updateDialog(msg tea.KeyMsg) (tea.Cmd, bool) {
	if q.dialog == dialogQuit {
		switch msg.String() {
		case "esc", "n":
			q.hideDialog()
			return nil, true
		case "enter":
			if q.buttonFocus == 0 {
				// OK — quit
				q.cancelAll()
				q.hideDialog()
				return tea.Quit, true
			}
			q.hideDialog()
			return nil, true
		case "left", "right":
			q.buttonFocus = 1 - q.buttonFocus
			return nil, true
		case "y":
			q.cancelAll()
			q.hideDialog()
			return tea.Quit, true
		}
		return nil, true
	}

	switch msg.String() {
	case "esc":
		if q.resolveCancel != nil {
			q.resolveCancel()
			q.resolveCancel = nil
		}
		q.hideDialog()
		return nil, true

	case "tab":
		// When on buttons, cycle OK→Cancel before moving to next section
		if q.section == sectionButtons && q.buttonFocus == 0 {
			q.buttonFocus = 1
		} else {
			q.buttonFocus = 0
			q.cycleSection(1)
		}
		return nil, true
	case "shift+tab":
		if q.section == sectionButtons && q.buttonFocus == 1 {
			q.buttonFocus = 0
		} else {
			q.buttonFocus = 1
			q.cycleSection(-1)
		}
		return nil, true

	case "up", "k":
		if q.section == sectionRadio && q.dialog == dialogPopup {
			if q.radioChoice > 0 {
				q.radioChoice--
			}
		}
		return nil, true

	case "down", "j":
		if q.section == sectionRadio && q.dialog == dialogPopup {
			if q.radioChoice < 1 {
				q.radioChoice++
			}
		}
		return nil, true

	case "left":
		if q.section == sectionButtons {
			q.buttonFocus = 0
			return nil, true
		}
	case "right":
		if q.section == sectionButtons {
			q.buttonFocus = 1
			return nil, true
		}

	case " ":
		if q.section == sectionForce {
			q.force = !q.force
			return nil, true
		}

	case "enter":
		if q.section == sectionButtons && q.buttonFocus == 1 {
			q.hideDialog()
			return nil, true
		}
		if q.section == sectionOutputDir || q.section == sectionForce {
			q.cycleSection(1)
			return nil, true
		}
		return q.confirmDialog()
	}

	if q.section == sectionOutputDir {
		var cmd tea.Cmd
		q.outputDir, cmd = q.outputDir.Update(msg)
		return cmd, true
	}

	return nil, true
}

func (q *downloadQueue) cycleSection(dir int) {
	var sections []dialogSection
	if q.dialog == dialogPopup {
		sections = []dialogSection{sectionRadio}
		if !q.serveMode {
			sections = append(sections, sectionOutputDir, sectionForce)
		}
		sections = append(sections, sectionButtons)
	} else {
		if !q.serveMode {
			sections = []dialogSection{sectionOutputDir, sectionForce, sectionButtons}
		} else {
			sections = []dialogSection{sectionButtons}
		}
	}
	idx := 0
	for i, s := range sections {
		if s == q.section {
			idx = i
			break
		}
	}
	idx = (idx + dir + len(sections)) % len(sections)
	q.section = sections[idx]
	if q.section == sectionOutputDir {
		q.outputDir.Focus()
	} else {
		q.outputDir.Blur()
	}
}

func (q *downloadQueue) confirmDialog() (tea.Cmd, bool) {
	outDir := q.outputDir.Value()
	if outDir == "" {
		outDir = "./"
	}

	switch q.dialog {
	case dialogPopup:
		if q.artifact == nil {
			q.hideDialog()
			return nil, true
		}
		artifactID := q.artifact.Extra.ID
		pid := q.projectID
		ver := q.version
		q.hideDialog()

		log.Debug("confirmDialog", "radioChoice", q.radioChoice, "artifactID", artifactID, "pid", pid, "ver", ver)
		if q.radioChoice == 0 {
			cmd := q.enqueue([]string{artifactID})
			return tea.Batch(cmd, q.listenForProgress()), true
		}
		cmd := q.enqueueProject(pid, ver, outDir)
		return tea.Batch(cmd, q.listenForProgress()), true

	case dialogConfirm:
		if q.serveMode && len(q.selectedIDs) > 20 {
			// Over limit — OK is hidden, shouldn't reach here
			q.hideDialog()
			return nil, true
		}
		ids := make([]string, len(q.selectedIDs))
		copy(ids, q.selectedIDs)
		q.hideDialog()
		cmd := q.enqueue(ids)
		return tea.Batch(cmd, q.listenForProgress()), true
	}

	q.hideDialog()
	return nil, true
}

// --- Views ---

var (
	paneHeaderStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#B8A038")).
			Bold(true)
	paneItemDone   = lipgloss.NewStyle().Foreground(lipgloss.Color("#04B575"))
	paneItemFailed = lipgloss.NewStyle().Foreground(lipgloss.Color("#CC4444"))
	paneItemActive = lipgloss.NewStyle().Foreground(fuchsia).Bold(true)
	paneBarStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#B8A038"))
	paneDimStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
)

// dialogView renders the popup overlay (for download dialog or quit confirm).
func (q *downloadQueue) dialogView(width, height int) string {
	if q.dialog == dialogHidden {
		return ""
	}

	var content string
	switch q.dialog {
	case dialogPopup:
		content = q.popupView()
	case dialogConfirm:
		content = q.batchConfirmView()
	case dialogLoading:
		var fetched int64
		if q.resolveFetched != nil {
			fetched = atomic.LoadInt64(q.resolveFetched)
		}
		if fetched > 0 {
			content = grayFg(fmt.Sprintf("Resolving files... %d found", fetched))
		} else {
			content = grayFg("Resolving files...")
		}
	case dialogQuit:
		content = q.quitConfirmView()
	}

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(fuchsia).
		Padding(1, 2).
		Width(q.popupWidth())

	box := boxStyle.Render(content)

	// Add CLI hint below the box, centered
	hint := q.cliHint()
	if hint != "" {
		box = box + "\n\n" + lipgloss.PlaceHorizontal(q.popupWidth()+4, lipgloss.Center, grayFg("Or run:")) +
			"\n" + lipgloss.PlaceHorizontal(q.popupWidth()+4, lipgloss.Center, hint)
	}

	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}

// cliHint returns the CLI command for the current dialog context.
func (q *downloadQueue) cliHint() string {
	switch q.dialog {
	case dialogPopup:
		if q.artifact != nil {
			if q.radioChoice == 1 {
				return codeFg(fmt.Sprintf("exo download \"%s@%s\"", q.projectID, q.version))
			}
			return codeFg(fmt.Sprintf("exo download \"%s\"", q.artifact.ArfifactDBID))
		}
	case dialogConfirm:
		if q.batchSource != "" {
			return codeFg(fmt.Sprintf("exo download \"%s\"", q.batchSource))
		}
	}
	return ""
}

// paneView renders the bottom progress pane.
func (q *downloadQueue) paneView(width int) string {
	if !q.paneOpen {
		return ""
	}

	var b strings.Builder

	// Header line
	completed := q.completedCount()
	total := len(q.items)
	var totalBytes, downloadedBytes int64
	for _, it := range q.items {
		totalBytes += it.total
		if it.status == itemCompleted {
			downloadedBytes += it.total
		} else {
			downloadedBytes += it.downloaded
		}
	}

	header := fmt.Sprintf("── Downloads (%d/%d) ", completed, total)
	sizeInfo := fmt.Sprintf(" %s / %s ──", formatSize(downloadedBytes), formatSize(totalBytes))
	headerWidth := runewidth.StringWidth(header)
	sizeInfoWidth := runewidth.StringWidth(sizeInfo)
	padding := width - headerWidth - sizeInfoWidth
	if padding < 1 {
		padding = 1
	}
	line := header + strings.Repeat("─", padding) + sizeInfo
	b.WriteString(paneHeaderStyle.Render(line))
	b.WriteString("\n")

	// Items (show last N to fit)
	maxItems := 6
	items := q.items
	startIdx := 0
	if len(items) > maxItems {
		// Show: some completed (tail), all active, some pending (head)
		startIdx = len(items) - maxItems
	}
	shown := items[startIdx:]

	for _, it := range shown {
		line := q.renderItem(it, width-2)
		b.WriteString(line)
		b.WriteString("\n")
	}

	if len(items) == 0 {
		b.WriteString(paneDimStyle.Render("  No downloads"))
		b.WriteString("\n")
	}

	// Footer
	footer := paneDimStyle.Render("  t toggle · esc close")
	b.WriteString(footer)

	return b.String()
}

func (q *downloadQueue) renderItem(it downloadItem, width int) string {
	nameWidth := 35
	if width > 80 {
		nameWidth = 45
	}

	name := it.filename
	if name == "" {
		name = it.id
	}
	if len(name) > nameWidth {
		name = name[:nameWidth-3] + "..."
	}
	paddedName := fmt.Sprintf("%-*s", nameWidth, name)

	switch it.status {
	case itemSkipped:
		return fmt.Sprintf(" %s %s %s",
			paneDimStyle.Render("⊘"),
			paneDimStyle.Render(paddedName),
			paneDimStyle.Render("skipped"),
		)
	case itemCompleted:
		size := formatSize(it.total)
		return fmt.Sprintf(" %s %s %s",
			paneItemDone.Render("✓"),
			paneDimStyle.Render(paddedName),
			paneDimStyle.Render(fmt.Sprintf("%8s", size)),
		)
	case itemFailed:
		errStr := "error"
		if it.err != nil {
			errStr = it.err.Error()
			if len(errStr) > width-nameWidth-10 {
				errStr = errStr[:width-nameWidth-13] + "..."
			}
		}
		return fmt.Sprintf(" %s %s %s",
			paneItemFailed.Render("✗"),
			paneDimStyle.Render(paddedName),
			paneItemFailed.Render(errStr),
		)
	case itemActive:
		pct := float64(0)
		if it.total > 0 {
			pct = float64(it.downloaded) / float64(it.total) * 100
		}
		barWidth := 12
		filled := int(pct / 100 * float64(barWidth))
		if filled > barWidth {
			filled = barWidth
		}
		bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)
		sizeInfo := fmt.Sprintf("%s / %s", formatSize(it.downloaded), formatSize(it.total))
		return fmt.Sprintf(" %s %s %s %s",
			paneItemActive.Render("↓"),
			paneItemActive.Render(paddedName),
			paneBarStyle.Render(fmt.Sprintf("[%s] %3.0f%%", bar, pct)),
			paneDimStyle.Render(sizeInfo),
		)
	default: // pending
		return fmt.Sprintf(" %s %s",
			paneDimStyle.Render("·"),
			paneDimStyle.Render(paddedName),
		)
	}
}

func (q *downloadQueue) popupView() string {
	var b strings.Builder
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(palette.Current().Highlight.Adaptive())

	b.WriteString(titleStyle.Render("Download"))
	b.WriteString("\n\n")

	innerW := q.popupInnerWidth()
	sectionActive := q.section == sectionRadio

	// Option 1: This file
	fileSize := ""
	if q.artifact != nil {
		if s := getFileSize(q.artifact); s > 0 {
			fileSize = fmt.Sprintf("  (%s)", formatSize(s))
		}
	}
	b.WriteString(q.renderRadio(0, "This file"+fileSize, sectionActive))
	b.WriteString("\n")

	// Option 2: All files in project@version
	projectLabel := fmt.Sprintf("All files in %s@%s", q.projectID, q.version)
	if q.projectReady {
		projectLabel += fmt.Sprintf("  (%d files, %s)", q.projectCount, formatSize(q.projectTotal))
	} else {
		projectLabel += "  (loading...)"
	}
	if len(projectLabel) > innerW-4 {
		projectLabel = projectLabel[:innerW-7] + "..."
	}
	b.WriteString(q.renderRadio(1, projectLabel, sectionActive))
	b.WriteString("\n")

	// Output dir and force checkbox (hidden in serve mode)
	if !q.serveMode {
		b.WriteString("\n")
		dirActive := q.section == sectionOutputDir
		dirPrefix := "  "
		if dirActive {
			dirPrefix = fuchsiaFg("> ")
		}
		b.WriteString(dirPrefix + grayFg("Output dir: ") + q.outputDir.View())
		b.WriteString("\n")

		b.WriteString(q.renderForceCheckbox())
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(q.renderButtons())

	return b.String()
}

func (q *downloadQueue) batchConfirmView() string {
	var b strings.Builder
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(palette.Current().Highlight.Adaptive())

	if q.batchSize > 0 {
		b.WriteString(titleStyle.Render(fmt.Sprintf("Download %d files (%s)?", len(q.selectedIDs), formatSize(q.batchSize))))
	} else {
		b.WriteString(titleStyle.Render(fmt.Sprintf("Download %d files?", len(q.selectedIDs))))
	}
	b.WriteString("\n")

	if q.serveMode && len(q.selectedIDs) > 20 {
		warnStyle := lipgloss.NewStyle().Foreground(palette.Current().Error.Adaptive())
		b.WriteString(warnStyle.Render(fmt.Sprintf("  Too many files for browser download. Use the CLI instead.")))
		b.WriteString("\n")
	}
	b.WriteString("\n")

	if !q.serveMode {
		dirActive := q.section == sectionOutputDir
		dirPrefix := "  "
		if dirActive {
			dirPrefix = fuchsiaFg("> ")
		}
		b.WriteString(dirPrefix + grayFg("Output dir: ") + q.outputDir.View())
		b.WriteString("\n")

		b.WriteString(q.renderForceCheckbox())
		b.WriteString("\n")
	}

	b.WriteString("\n")
	if q.serveMode && len(q.selectedIDs) > 20 {
		// Only show Cancel when over the limit
		cancelStyle := lipgloss.NewStyle().Padding(0, 3).Background(palette.Current().Error.Adaptive()).Foreground(palette.Current().CodeBg.Adaptive()).Bold(true)
		b.WriteString("  " + cancelStyle.Render("Cancel"))
	} else {
		b.WriteString(q.renderButtons())
	}

	return b.String()
}

func (q *downloadQueue) quitConfirmView() string {
	var b strings.Builder
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(palette.Current().Highlight.Adaptive())

	active := 0
	for _, it := range q.items {
		if it.status == itemActive || it.status == itemPending {
			active++
		}
	}

	b.WriteString(titleStyle.Render(fmt.Sprintf("%d downloads in progress.", active)))
	b.WriteString("\n")
	b.WriteString("Quit anyway?")
	b.WriteString("\n\n")
	b.WriteString(q.renderButtons())

	return b.String()
}

func (q *downloadQueue) renderRadio(idx int, label string, sectionActive bool) string {
	selected := q.radioChoice == idx
	focused := sectionActive && selected

	bullet := "○"
	if selected {
		bullet = "●"
	}

	if focused {
		return fuchsiaFg("> " + bullet + " " + label)
	}
	if selected {
		return fuchsiaFg("  "+bullet+" ") + label
	}
	return "  " + grayFg(bullet) + " " + label
}

func (q *downloadQueue) renderForceCheckbox() string {
	active := q.section == sectionForce
	prefix := "  "
	if active {
		prefix = fuchsiaFg("> ")
	}
	check := "[ ]"
	if q.force {
		check = "[✓]"
	}
	if active {
		return prefix + fuchsiaFg(check+" Force re-download")
	}
	return prefix + grayFg(check) + " " + grayFg("Force re-download")
}

func (q *downloadQueue) renderButtons() string {
	btnActive := q.section == sectionButtons
	pp := palette.Current()
	dimBtn := lipgloss.NewStyle().Padding(0, 3).
		Background(pp.CodeBg.Adaptive()).
		Foreground(pp.Dim.Adaptive())

	okStyle := dimBtn
	cancelStyle := dimBtn
	if btnActive && q.buttonFocus == 0 {
		okStyle = lipgloss.NewStyle().Padding(0, 3).
			Background(pp.Success.Adaptive()).
			Foreground(pp.CodeBg.Adaptive()).Bold(true)
	}
	if btnActive && q.buttonFocus == 1 {
		cancelStyle = lipgloss.NewStyle().Padding(0, 3).
			Background(pp.Error.Adaptive()).
			Foreground(pp.CodeBg.Adaptive()).Bold(true)
	}

	return "  " + okStyle.Render("OK") + "  " + cancelStyle.Render("Cancel")
}

// handleDialogClick handles mouse clicks inside the download dialog popup.
func (q *downloadQueue) handleDialogClick(msg tea.MouseMsg) (tea.Cmd, bool) {
	popupW := q.popupWidth()

	// Render content to compute dimensions
	var content string
	switch q.dialog {
	case dialogPopup:
		content = q.popupView()
	case dialogConfirm:
		content = q.batchConfirmView()
	case dialogQuit:
		content = q.quitConfirmView()
	case dialogLoading:
		return nil, true
	}
	contentLines := strings.Count(content, "\n") + 1

	// Render the full dialog output (same as dialogView) to get total height.
	// lipgloss.Place centers the entire output including CLI hint lines below the box.
	boxH := contentLines + 4 // border(1) + padding(1) + content + padding(1) + border(1)
	totalH := boxH
	if hint := q.cliHint(); hint != "" {
		totalH += 3 // "\n\n" + "Or run:" + "\n" + hint
	}

	boxX := (q.width - popupW) / 2
	boxY := (q.height - totalH) / 2 // centered based on total height including hint

	// Content area inside border+padding
	contentY := boxY + 2 // border(1) + padding(1)

	relY := msg.Y - contentY

	// Click outside the box — dismiss
	if msg.X < boxX || msg.X >= boxX+popupW || msg.Y < boxY || msg.Y >= boxY+boxH {
		if q.dialog != dialogQuit {
			q.hideDialog()
		}
		return nil, true
	}

	// Click inside border/padding but outside content
	if relY < 0 || relY >= contentLines {
		return nil, true
	}

	contentX := boxX + 3 // border(1) + padding(2)
	relX := msg.X - contentX

	switch q.dialog {
	case dialogPopup:
		return q.handlePopupClick(relY, relX)
	case dialogConfirm:
		return q.handleBatchConfirmClick(relY, relX)
	case dialogQuit:
		return q.handleQuitConfirmClick(relY, relX)
	}

	return nil, true
}

func (q *downloadQueue) handlePopupClick(relY, relX int) (tea.Cmd, bool) {
	// Popup content layout:
	// 0: Title ("Download")
	// 1: blank
	// 2: Radio option 0 (This file)
	// 3: Radio option 1 (All files in project)
	// If !serveMode:
	//   4: blank
	//   5: Output dir
	//   6: Force checkbox
	//   7: blank
	//   8: Buttons (OK / Cancel)
	// If serveMode:
	//   4: blank
	//   5: Buttons

	switch {
	case relY == 2:
		q.radioChoice = 0
		q.section = sectionRadio
		return nil, true
	case relY == 3:
		q.radioChoice = 1
		q.section = sectionRadio
		return nil, true
	}

	if q.serveMode {
		if relY == 5 {
			return q.handleButtonClick(relX)
		}
	} else {
		switch {
		case relY == 5:
			q.section = sectionOutputDir
			q.outputDir.Focus()
			return nil, true
		case relY == 6:
			q.section = sectionForce
			q.force = !q.force
			return nil, true
		case relY == 8:
			return q.handleButtonClick(relX)
		}
	}

	return nil, true
}

func (q *downloadQueue) handleBatchConfirmClick(relY, relX int) (tea.Cmd, bool) {
	// Batch confirm layout:
	// 0: Title
	// 1: (optional warning if >20 files in serve mode)
	// 2: blank
	// Then if !serveMode: output dir, force, blank, buttons
	// If serveMode: buttons

	content := q.batchConfirmView()
	lines := strings.Split(content, "\n")
	lastLine := len(lines) - 1

	// Buttons are always on the last non-empty line
	if relY == lastLine || relY == lastLine-1 {
		if q.serveMode && len(q.selectedIDs) > 20 {
			// Only cancel is visible
			q.hideDialog()
			return nil, true
		}
		return q.handleButtonClick(relX)
	}

	return nil, true
}

func (q *downloadQueue) handleQuitConfirmClick(relY, relX int) (tea.Cmd, bool) {
	// Quit confirm layout:
	// 0: Title
	// 1: "Quit anyway?"
	// 2: blank
	// 3: Buttons
	if relY == 3 {
		return q.handleButtonClick(relX)
	}
	return nil, true
}

func (q *downloadQueue) handleButtonClick(relX int) (tea.Cmd, bool) {
	// Button layout: "  " + [   OK   ] + "  " + [   Cancel   ]
	// OK:     starts at X=2, width=8 (padding 3 + "OK" + padding 3)
	// Cancel: starts at X=12, width=12 (padding 3 + "Cancel" + padding 3)
	q.section = sectionButtons
	if relX >= 12 {
		// Clicked Cancel
		q.hideDialog()
		return nil, true
	}
	if relX >= 2 && relX < 10 {
		// Clicked OK
		q.buttonFocus = 0
		return q.confirmDialog()
	}
	return nil, true
}

// --- Helpers ---

func formatSize(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

func getFileSize(md *markdown) int64 {
	// For exohub-artifact, use the top-level "size" field (actual file size)
	if strings.HasPrefix(md.Extra.Schema, "exohub-artifact/") && md.Result.Metadata != nil {
		if v, ok := md.Result.Metadata["size"]; ok {
			switch s := v.(type) {
			case float64:
				if int64(s) > 0 {
					return int64(s)
				}
			case int64:
				if s > 0 {
					return s
				}
			}
		}
	}
	// Fallback to _extra.file_size for other schemas
	if s := int64(md.Extra.FileSize); s > 0 {
		return s
	}
	return 0
}

func (q *downloadQueue) fetchProjectInfo(dl Downloader, projectID, version string) tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	q.resolveCancel = cancel
	return func() tea.Msg {
		files, err := dl.ListProjectFiles(ctx, projectID, version)
		if err != nil {
			return projectInfoMsg{err: err}
		}
		var totalSize int64
		for _, f := range files {
			totalSize += f.Size
		}
		return projectInfoMsg{count: len(files), totalSize: totalSize}
	}
}
