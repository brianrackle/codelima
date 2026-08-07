package codelima

import (
	"fmt"
	"log/slog"
	"math"
	"path/filepath"
	"strings"
	"time"

	"git.sr.ht/~rockorager/vaxis"
	"git.sr.ht/~rockorager/vaxis/widgets/border"

	"github.com/brianrackle/codelima/internal/codelima/terminal"
)

// tuiMessageLogDefaultCap bounds the message ring; older entries are evicted.
const tuiMessageLogDefaultCap = 200

// tuiTreeEntryHeight keeps each node's identity and six properties together
// as one selectable block in the tree.
const tuiTreeEntryHeight = 7

// tuiMessage is one entry in the message surface: when it happened, its severity
// (borrowed from slog so the view can colour it), and the human text.
type tuiMessage struct {
	Time  time.Time
	Level slog.Level
	Text  string
}

// tuiMessageLog is a bounded ring of status/notification messages. It replaces
// the single overwritable status string as the durable record: the footer still
// shows the newest transient status, but every notable message (including
// completed/failed background-operation output that used to be discarded) is
// retained here and browsable in the messages view (ADR 59).
type tuiMessageLog struct {
	entries []tuiMessage
	cap     int
	now     func() time.Time
}

func newTUIMessageLog(capacity int) *tuiMessageLog {
	if capacity <= 0 {
		capacity = tuiMessageLogDefaultCap
	}
	return &tuiMessageLog{cap: capacity, now: time.Now}
}

// Append records a non-empty, whitespace-trimmed message, evicting the oldest
// entry once the ring is full so memory stays bounded.
func (l *tuiMessageLog) Append(level slog.Level, text string) {
	if l == nil {
		return
	}
	text = strings.TrimRight(text, "\r\n")
	if strings.TrimSpace(text) == "" {
		return
	}
	now := time.Now
	if l.now != nil {
		now = l.now
	}
	l.entries = append(l.entries, tuiMessage{Time: now(), Level: level, Text: text})
	if l.cap > 0 && len(l.entries) > l.cap {
		l.entries = append([]tuiMessage(nil), l.entries[len(l.entries)-l.cap:]...)
	}
}

// Entries returns a copy of the retained messages, oldest first.
func (l *tuiMessageLog) Entries() []tuiMessage {
	if l == nil {
		return nil
	}
	return append([]tuiMessage(nil), l.entries...)
}

// Latest returns the newest retained message, if any.
func (l *tuiMessageLog) Latest() (tuiMessage, bool) {
	if l == nil || len(l.entries) == 0 {
		return tuiMessage{}, false
	}
	return l.entries[len(l.entries)-1], true
}

// Len reports the number of retained messages.
func (l *tuiMessageLog) Len() int {
	if l == nil {
		return 0
	}
	return len(l.entries)
}

// tuiMessagesView is the scrollable overlay that renders the message ring. It is
// read-only and reuses the same border/scroll building blocks as the selector:
// Up/Down (or j/k) and PageUp/PageDown scroll; Esc closes it (Ctrl+c/q still
// quit the app, matching the other overlays).
type tuiMessagesView struct {
	messages     []tuiMessage
	scroll       int
	pinToBottom  bool
	lastViewport int
}

func newTUIMessagesView(messages []tuiMessage) *tuiMessagesView {
	return &tuiMessagesView{
		messages:    messages,
		pinToBottom: true,
	}
}

// Update processes one event and reports whether the view should close.
func (v *tuiMessagesView) Update(event vaxis.Event) (done bool, err error) {
	key, ok := event.(vaxis.Key)
	if !ok {
		return false, nil
	}
	if isOverlayCancelKey(key) {
		return true, nil
	}
	switch {
	case key.MatchString("Up"), key.MatchString("k"):
		v.scrollBy(-1)
	case key.MatchString("Down"), key.MatchString("j"):
		v.scrollBy(1)
	case key.MatchString("Page_Up"), key.Matches('b', vaxis.ModCtrl):
		v.scrollBy(-v.pageStep())
	case key.MatchString("Page_Down"), key.Matches('f', vaxis.ModCtrl), key.MatchString(" "):
		v.scrollBy(v.pageStep())
	case key.MatchString("Home"), key.MatchString("g"):
		v.pinToBottom = false
		v.scroll = 0
	case key.MatchString("End"), key.MatchString("G"):
		v.pinToBottom = true
	}
	return false, nil
}

func (v *tuiMessagesView) FooterHint() string {
	return "Up/Down scroll   PgUp/PgDn page   g/G top/bottom   Esc close   Ctrl+c quit"
}

func (v *tuiMessagesView) pageStep() int {
	if v.lastViewport > 1 {
		return v.lastViewport - 1
	}
	return 1
}

func (v *tuiMessagesView) scrollBy(delta int) {
	v.pinToBottom = false
	v.scroll += delta
	v.clampScroll()
}

func (v *tuiMessagesView) clampScroll() {
	if v.scroll < 0 {
		v.scroll = 0
	}
	maxScroll := len(v.messages) - v.lastViewport
	if maxScroll < 0 {
		maxScroll = 0
	}
	if v.scroll > maxScroll {
		v.scroll = maxScroll
	}
}

func (v *tuiMessagesView) Draw(win vaxis.Window, headerStyle, mutedStyle, _ vaxis.Style) {
	body := border.All(win, mutedStyle)
	body.Println(0, vaxis.Segment{Text: fmt.Sprintf("Messages (%d)", len(v.messages)), Style: headerStyle})

	_, height := body.Size()
	footerRow := height - 1
	visibleRows := footerRow - 1
	if visibleRows < 1 {
		visibleRows = 1
	}
	v.lastViewport = visibleRows

	if v.pinToBottom {
		v.scroll = len(v.messages) - visibleRows
	}
	v.clampScroll()

	if len(v.messages) == 0 {
		body.Println(1, vaxis.Segment{Text: "No messages yet.", Style: mutedStyle})
	} else {
		row := 1
		for index := v.scroll; index < len(v.messages) && row <= visibleRows; index++ {
			message := v.messages[index]
			body.Println(row, vaxis.Segment{Text: tuiMessageLine(message), Style: tuiMessageLevelStyle(message.Level, mutedStyle)})
			row++
		}
	}

	body.Println(footerRow, vaxis.Segment{Text: "Up/Down scroll  PgUp/PgDn page  g/G top/bottom  Esc close", Style: mutedStyle})
}

func tuiMessageLine(message tuiMessage) string {
	return fmt.Sprintf("%s %-5s %s", message.Time.Format("15:04:05"), tuiMessageLevelTag(message.Level), message.Text)
}

func tuiMessageLevelTag(level slog.Level) string {
	switch {
	case level >= slog.LevelError:
		return "ERR"
	case level >= slog.LevelWarn:
		return "WARN"
	case level >= slog.LevelInfo:
		return "INFO"
	default:
		return "DEBUG"
	}
}

func tuiMessageLevelStyle(level slog.Level, base vaxis.Style) vaxis.Style {
	if level >= slog.LevelWarn {
		return vaxis.Style{Foreground: vaxis.ColorRed}
	}
	return base
}

// pushMessage is the single nil-safe entry point for recording a message into
// the ring at its true level. Explicit seams (operation results in
// retainOperationMessages, refresh failures in finishDataRefresh) route through
// it; transient footer statuses reach it through setStatus.
func (a *vaxisTUIApp) pushMessage(level slog.Level, text string) {
	if a == nil || a.messages == nil {
		return
	}
	a.messages.Append(level, text)
}

// setStatus sets the transient footer status and records it in the message
// ring at the given level (error paths use slog.LevelError, successes
// slog.LevelInfo). The footer renders statuses at warn level or above in the
// error style; info statuses render in the normal muted style.
func (a *vaxisTUIApp) setStatus(level slog.Level, text string) {
	a.status = text
	a.statusLevel = level
	a.pushMessage(level, text)
}

// clearStatus clears the transient footer status; nothing is recorded.
func (a *vaxisTUIApp) clearStatus() {
	a.status = ""
	a.statusLevel = slog.LevelInfo
}

func (a *vaxisTUIApp) openMessagesView() {
	var entries []tuiMessage
	if a.messages != nil {
		entries = a.messages.Entries()
	}
	a.overlay = newTUIMessagesView(entries)
}

type tuiRect struct {
	col    int
	row    int
	width  int
	height int
}

type tuiBodyLayout struct {
	treeVisible bool
	treeWidth   int
	termCol     int
	termWidth   int
}

func (r tuiRect) contains(col, row int) bool {
	if r.width <= 0 || r.height <= 0 {
		return false
	}

	return col >= r.col && col < r.col+r.width && row >= r.row && row < r.row+r.height
}

func (r tuiRect) translateMouse(mouse vaxis.Mouse) vaxis.Mouse {
	mouse.Col -= r.col
	mouse.Row -= r.row
	return mouse
}

func layoutTUIBody(width int, focus tuiFocus) tuiBodyLayout {
	if focus == tuiFocusTerminal {
		return tuiBodyLayout{
			treeVisible: false,
			treeWidth:   0,
			termCol:     0,
			termWidth:   width,
		}
	}

	treeWidth := width / 3
	if treeWidth < 28 {
		treeWidth = 28
	}
	if treeWidth > 40 {
		treeWidth = 40
	}
	if treeWidth > width-24 {
		treeWidth = width - 24
	}

	return tuiBodyLayout{
		treeVisible: true,
		treeWidth:   treeWidth,
		termCol:     treeWidth + 1,
		termWidth:   width - treeWidth - 1,
	}
}

func (a *vaxisTUIApp) renderedHyperlinkAt(col, row int) (string, bool) {
	if a.screenHyperlinkAt != nil {
		return a.screenHyperlinkAt(col, row)
	}
	return renderedHyperlinkAt(a.vx, col, row)
}

func (a *vaxisTUIApp) terminalLinkTargetAt(mouse vaxis.Mouse) (string, bool) {
	if !a.terminalBodyRect.contains(mouse.Col, mouse.Row) {
		return "", false
	}
	if a.state.focus != tuiFocusTerminal && a.state.treePaneMode != tuiTreePaneModeTerminal {
		return "", false
	}

	entry := a.mouseTerminalEntry()
	if !entry.valid() {
		return "", false
	}

	if term, ok := a.sessions.SessionTerminal(a.state.activeSessionKey()); ok {
		localMouse := a.terminalBodyRect.translateMouse(mouse)
		if target, ok := term.HyperlinkAt(localMouse.Col, localMouse.Row); ok {
			return target, true
		}
	}

	return a.renderedHyperlinkAt(mouse.Col, mouse.Row)
}

func (a *vaxisTUIApp) linkTargetAt(col, row int) (string, bool) {
	for _, region := range a.linkRegions {
		if region.rect.contains(col, row) {
			return region.target, true
		}
	}
	return "", false
}

func (a *vaxisTUIApp) printLinkifiedLine(win vaxis.Window, row int, text string, style vaxis.Style) {
	segments := linkifiedSegments(text, style)
	win.PrintTruncate(row, segments...)

	originCol, originRow := win.Origin()
	width, _ := win.Size()
	col := 0
	for _, segment := range segments {
		segmentWidth := renderedTextWidth(a.vx, segment.Text)
		if segment.Style.Hyperlink != "" && segmentWidth > 0 && col < width {
			visibleWidth := segmentWidth
			if col+visibleWidth > width {
				visibleWidth = width - col
			}
			if visibleWidth > 0 {
				a.linkRegions = append(a.linkRegions, tuiLinkRegion{
					rect: tuiRect{
						col:    originCol + col,
						row:    originRow + row,
						width:  visibleWidth,
						height: 1,
					},
					target: segment.Style.Hyperlink,
				})
			}
		}
		col += segmentWidth
		if col >= width {
			break
		}
	}
}

func (a *vaxisTUIApp) rightPaneOverrideActive() bool {
	return a.overlay != nil
}

func (a *vaxisTUIApp) hostTerminalTabActive() bool {
	if a == nil || a.state == nil || a.sessions == nil || a.rightPaneOverrideActive() {
		return false
	}
	if a.state.focus != tuiFocusTerminal && a.state.treePaneMode != tuiTreePaneModeTerminal {
		return false
	}
	session, ok := a.sessions.Session(a.state.activeSessionKey())
	return ok && session.shellKind == terminal.NodeHostShell && session.node.ID != ""
}

func (a *vaxisTUIApp) effectiveLayoutFocus() tuiFocus {
	if a.rightPaneOverrideActive() {
		return tuiFocusTree
	}
	return a.state.focus
}

func (a *vaxisTUIApp) rightPaneShowsInfo() bool {
	return !a.rightPaneOverrideActive() &&
		a.state.focus != tuiFocusTerminal &&
		a.state.treePaneMode == tuiTreePaneModeInfo
}

func (a *vaxisTUIApp) currentPaneTabs(entry tuiTreeEntry) string {
	if !entry.valid() {
		return ""
	}
	if a.rightPaneShowsInfo() {
		return "[Info] Terminal"
	}
	return "Info [Terminal]"
}

func (a *vaxisTUIApp) currentPaneTabSegments(entry tuiTreeEntry, activeStyle, inactiveStyle vaxis.Style) []vaxis.Segment {
	if !entry.valid() {
		return nil
	}
	segments := []vaxis.Segment{}
	if a.rightPaneShowsInfo() {
		segments = append(segments,
			vaxis.Segment{Text: "[Info]", Style: activeStyle},
			vaxis.Segment{Text: " Terminal", Style: inactiveStyle},
		)
	} else {
		segments = append(segments,
			vaxis.Segment{Text: "Info ", Style: inactiveStyle},
			vaxis.Segment{Text: "[Terminal]", Style: activeStyle},
		)
	}

	if a.sessions == nil {
		return segments
	}

	sessionTabs := a.terminalTabSegments(activeStyle, inactiveStyle)
	if len(sessionTabs) == 0 {
		return segments
	}
	segments = append(segments, vaxis.Segment{Text: "  ", Style: inactiveStyle})
	segments = append(segments, sessionTabs...)
	return segments
}

// terminalTabSegments renders the terminal tabs that belong to the focused
// tree item only; tabs opened for other projects or nodes stay hidden until
// their item is focused again.
func (a *vaxisTUIApp) terminalTabSegments(activeStyle, inactiveStyle vaxis.Style) []vaxis.Segment {
	if a == nil || a.sessions == nil || a.state == nil {
		return nil
	}

	targetKey := a.state.activeTerminalTargetKey()
	keys := a.sessions.TargetSessionKeys(targetKey)
	if len(keys) == 0 {
		return nil
	}

	activeKey := a.state.activeSessionKey()
	segments := make([]vaxis.Segment, 0, len(keys)*2)
	for index, key := range keys {
		session, ok := a.sessions.Session(key)
		if !ok {
			continue
		}
		if len(segments) > 0 {
			segments = append(segments, vaxis.Segment{Text: " ", Style: inactiveStyle})
		}
		label := terminalTabLabel(session, index, len(keys))
		if key == activeKey {
			segments = append(segments, vaxis.Segment{Text: "[" + label + "]", Style: activeStyle})
			continue
		}
		segments = append(segments, vaxis.Segment{Text: label, Style: inactiveStyle})
	}
	return segments
}

func terminalTabLabel(session *tuiSession, index, total int) string {
	if session == nil {
		return ""
	}
	label := strings.TrimSpace(session.label)
	if label == "" {
		label = strings.TrimSpace(session.key)
	}
	if session.shellKind == terminal.NodeHostShell && label != "" {
		label = "host:" + label
	}
	if total > 1 {
		label = fmt.Sprintf("%s %d", label, index+1)
	}
	return label
}

func (a *vaxisTUIApp) drawRightPane(win vaxis.Window, entry tuiTreeEntry, headerStyle, mutedStyle, errorStyle vaxis.Style) {
	if a.overlay != nil {
		a.overlay.Draw(win, headerStyle, mutedStyle, errorStyle)
		return
	}
	if a.rightPaneShowsInfo() {
		a.drawDetails(win, entry, headerStyle, mutedStyle)
		return
	}
	a.drawTerminalSurface(win, entry, headerStyle, mutedStyle, errorStyle)
}

func (a *vaxisTUIApp) activePaneFooter(entry tuiTreeEntry, focus tuiFocus) string {
	if a.overlay != nil {
		return a.overlay.FooterHint()
	}
	return renderFooter(focus, a.state.treePaneMode, entry)
}

func (a *vaxisTUIApp) headerLogoText() string {
	if a.logoAnimation == nil {
		return tuiLogoText
	}
	return a.logoAnimation.text()
}

func (a *vaxisTUIApp) headerStyle() vaxis.Style {
	style := vaxis.Style{Attribute: vaxis.AttrBold}
	if a.hostTerminalTabActive() {
		style.Foreground = vaxis.ColorRed
	}
	return style
}

// drawHeaderLogo updates only the eight fixed-width logo cells. Startup
// animation frames therefore avoid clearing and rebuilding the tree and
// terminal surfaces while still flowing through the single-owner event loop.
func (a *vaxisTUIApp) drawHeaderLogo() {
	if a.vx == nil {
		return
	}
	window := a.vx.Window()
	width, height := window.Size()
	if width < 60 || height < 14 {
		return
	}
	window.Println(0, vaxis.Segment{Text: a.headerLogoText(), Style: a.headerStyle()})
	a.vx.Render()
}

func (a *vaxisTUIApp) draw() {
	a.drawPasses++
	if a.vx == nil {
		return
	}

	window := a.vx.Window()
	window.Clear()
	a.vx.HideCursor()

	width, height := window.Size()
	a.treeContentRect = tuiRect{}
	a.terminalBodyRect = tuiRect{}
	a.linkRegions = nil
	if width < 60 || height < 14 {
		window.Println(0, vaxis.Segment{Text: "CodeLima TUI requires at least 60x14 terminal cells."})
		window.Println(1, vaxis.Segment{Text: fmt.Sprintf("Current size: %dx%d", width, height)})
		a.vx.Render()
		return
	}

	headerStyle := a.headerStyle()
	selectedStyle := tuiSelectedStyle()
	mutedStyle := tuiMutedStyle()
	errorStyle := vaxis.Style{Foreground: vaxis.ColorRed, Attribute: vaxis.AttrBold}

	layoutFocus := a.effectiveLayoutFocus()
	bodyTop := 1
	bodyHeight := height - bodyTop - 1
	layout := layoutTUIBody(width, layoutFocus)
	entry := a.state.selectedEntry()
	if layoutFocus == tuiFocusTerminal {
		entry = a.state.activeTerminalEntry()
	}

	headerSegments := []vaxis.Segment{{Text: a.headerLogoText(), Style: headerStyle}}
	if entry.valid() {
		headerSegments = append(headerSegments,
			vaxis.Segment{Text: "  Node: " + entry.node.Slug, Style: headerStyle},
			vaxis.Segment{Text: "  Configuration: " + entry.node.ConfigurationSlug, Style: headerStyle},
			vaxis.Segment{Text: "  Mode: " + nodeWorkspaceMode(entry.node), Style: headerStyle},
		)
	}
	window.Println(0, headerSegments...)

	termOuter := window.New(layout.termCol, bodyTop, layout.termWidth, bodyHeight)

	if layout.treeVisible {
		treeOuter := window.New(0, bodyTop, layout.treeWidth, bodyHeight)
		treeInner := border.All(treeOuter, mutedStyle)

		treeInner.Println(0, vaxis.Segment{Text: "Nodes", Style: headerStyle})
		treeInnerWidth, treeInnerHeight := treeInner.Size()
		treeContentHeight := treeInnerHeight - 1
		if treeContentHeight < 0 {
			treeContentHeight = 0
		}
		treeContent := treeInner.New(0, 1, treeInnerWidth, treeContentHeight)
		treeOriginCol, treeOriginRow := treeContent.Origin()
		a.treeContentRect = tuiRect{col: treeOriginCol, row: treeOriginRow, width: treeInnerWidth, height: treeContentHeight}

		treeEntryCapacity := tuiTreeEntryCapacity(treeContentHeight)
		treeViewportStart := a.state.viewportStart(treeEntryCapacity)
		for visibleIndex, entry := range a.state.visibleEntries(treeEntryCapacity) {
			index := treeViewportStart + visibleIndex
			style := mutedStyle
			if index == a.state.selection {
				style = selectedStyle
			}

			startRow := visibleIndex * tuiTreeEntryHeight
			for lineOffset, line := range a.treeEntryLines(entry) {
				treeContent.Println(startRow+lineOffset, vaxis.Segment{Text: line, Style: style})
			}
		}
	}

	if a.sessions != nil {
		cols, rows := a.activeTerminalSize(width, height, layoutFocus)
		a.sessions.SetPreferredTerminalSize(cols, rows)
	}

	if a.rightPaneOverrideActive() {
		a.drawRightPane(termOuter, entry, headerStyle, mutedStyle, errorStyle)
	} else {
		termBody := drawTerminalPane(termOuter, a.currentPaneTabSegments(entry, selectedStyle, mutedStyle), mutedStyle)
		termInnerWidth, termInnerHeight := termBody.Size()
		termOriginCol, termOriginRow := termBody.Origin()
		a.terminalBodyRect = tuiRect{col: termOriginCol, row: termOriginRow, width: termInnerWidth, height: termInnerHeight}
		a.drawRightPane(termBody, entry, headerStyle, mutedStyle, errorStyle)
	}

	footer := a.activePaneFooter(entry, layoutFocus)
	if summary := a.backgroundOperationSummary(); summary != "" {
		footer += "   " + summary
	}
	footerStyle := mutedStyle
	if a.status != "" {
		footer = a.status
		// Only warnings and errors render in the error style; success
		// statuses keep the normal footer style.
		if a.statusLevel >= slog.LevelWarn {
			footerStyle = errorStyle
		}
	}
	window.Println(height-1, vaxis.Segment{Text: footer, Style: footerStyle})

	a.vx.Render()
}

func tuiEmbeddedTerminalSize(width int, height int, focus tuiFocus) (cols, rows int) {
	return tuiTerminalPaneBodySize(width, height, focus)
}

func (a *vaxisTUIApp) activeTerminalSize(width int, height int, focus tuiFocus) (cols, rows int) {
	return tuiTerminalPaneBodySize(width, height, focus)
}

func tuiTerminalPaneBodySize(width int, height int, focus tuiFocus) (cols, rows int) {
	if width <= 0 || height <= 0 {
		return 0, 0
	}

	bodyTop := 1
	bodyHeight := height - bodyTop - 1
	if bodyHeight <= 0 {
		return 0, 0
	}

	layout := layoutTUIBody(width, focus)
	termWidth, termHeight := layout.termWidth, bodyHeight

	rows = termHeight
	if termHeight > 2 {
		rows = termHeight - 2
	}

	if termWidth <= 0 || rows <= 0 {
		return 0, 0
	}
	return termWidth, rows
}

func tuiSelectedStyle() vaxis.Style {
	return vaxis.Style{
		Foreground: vaxis.ColorWhite,
		Background: vaxis.ColorBlue,
		Attribute:  vaxis.AttrBold,
	}
}

func tuiMutedStyle() vaxis.Style {
	return vaxis.Style{Foreground: vaxis.ColorSilver}
}

func (a *vaxisTUIApp) operationDisplayStatus(entry tuiTreeEntry) string {
	if !entry.valid() {
		return ""
	}

	entryKey := entry.key()
	for _, operationID := range a.operationOrder {
		operation := a.operations[operationID]
		if operation == nil || !containsString(operation.EntryKeys, entryKey) {
			continue
		}
		if operation.DisplayStatus != "" {
			return operation.DisplayStatus
		}
	}
	return ""
}

func (a *vaxisTUIApp) nodeStatusText(node Node) string {
	if operationStatus := a.operationDisplayStatus(tuiTreeEntry{node: node}); operationStatus != "" {
		return operationStatus
	}
	return nodeVMStatus(node)
}

func tuiTreeEntryCapacity(height int) int {
	if height <= 0 {
		return 0
	}
	capacity := height / tuiTreeEntryHeight
	if capacity == 0 {
		return 1
	}
	return capacity
}

func (a *vaxisTUIApp) treeEntryLines(entry tuiTreeEntry) []string {
	status := ""
	if entry.valid() {
		status = a.nodeStatusText(entry.node)
		if root := strings.TrimSpace(a.treeWorkspaceRoot); root != "" && entry.node.DirectoryPath != "" {
			if relative, err := filepath.Rel(root, entry.node.DirectoryPath); err == nil && pathWithinRoot(root, entry.node.DirectoryPath) {
				entry.node.DirectoryPath = relative
			}
		}
	}
	return tuiEntryLinesWithStatus(entry, status)
}

func (a *vaxisTUIApp) backgroundOperationSummary() string {
	count := len(a.operationOrder)
	if count == 0 {
		return ""
	}
	if count == 1 {
		return "1 background task running"
	}
	return fmt.Sprintf("%d background tasks running", count)
}

func (a *vaxisTUIApp) entryOperations(entry tuiTreeEntry) []*tuiOperationState {
	if !entry.valid() || len(a.operationOrder) == 0 {
		return nil
	}

	entryKey := entry.key()
	operations := make([]*tuiOperationState, 0, len(a.operationOrder))
	for _, operationID := range a.operationOrder {
		operation := a.operations[operationID]
		if operation == nil {
			continue
		}
		if containsString(operation.EntryKeys, entryKey) || containsString(operation.EntryKeys, "nodes") || containsString(operation.EntryKeys, "configurations") {
			operations = append(operations, operation)
		}
	}
	return operations
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func (a *vaxisTUIApp) drawDetails(win vaxis.Window, entry tuiTreeEntry, headerStyle, mutedStyle vaxis.Style) {
	row := 0
	switch {
	case entry.valid():
		win.Println(row, vaxis.Segment{Text: "Node controls", Style: headerStyle})
		row++
		win.Println(row, vaxis.Segment{Text: "Slug: " + entry.node.Slug})
		row++
		win.Println(row, vaxis.Segment{Text: "Configuration: " + entry.node.ConfigurationSlug})
		row++
		win.Println(row, vaxis.Segment{Text: "Status: " + a.nodeStatusText(entry.node)})
		row++
		win.Println(row, vaxis.Segment{Text: "CPU usage: " + nodeCPUUsageText(entry.node)})
		row++
		win.Println(row, vaxis.Segment{Text: "Memory usage: " + nodeMemoryUsageText(entry.node)})
		row++
		win.Println(row, vaxis.Segment{Text: "Disk usage: " + nodeDiskUsageText(entry.node)})
		row++
		nodePath := ""
		if a.service != nil && a.service.store != nil {
			nodePath = a.service.store.nodePath(entry.node.ID)
		}
		if nodePath != "" {
			a.printLinkifiedLine(win, row, "Node file: "+nodePath, vaxis.Style{})
			row++
		}
		win.Println(row, vaxis.Segment{Text: "Workspace mode: " + nodeWorkspaceMode(entry.node)})
		row++
		if entry.node.DirectoryPath != "" {
			a.printLinkifiedLine(win, row, "Directory: "+entry.node.DirectoryPath, vaxis.Style{})
			row++
		}
		win.Println(row, vaxis.Segment{Text: fmt.Sprintf("Resources: %d CPU, %d MiB memory, %d MiB disk", entry.node.VCPUs, entry.node.MemoryMiB, entry.node.DiskMiB), Style: mutedStyle})
		row++
		row++
		if nodeIsRunning(entry.node) {
			win.Println(row, vaxis.Segment{Text: fmt.Sprintf("Node is running. Press %s to open a terminal tab or %s to focus its terminal.", terminalTabOpenFooterHint, terminalViewToggleTextHint), Style: mutedStyle})
		} else {
			win.Println(row, vaxis.Segment{Text: "Start the node before opening its terminal tabs.", Style: mutedStyle})
		}
		row++
		a.drawEntryOperations(win, row, entry, headerStyle, mutedStyle)
	default:
		win.Println(0, vaxis.Segment{Text: "Press [n] to create a node or [a] to manage configurations.", Style: mutedStyle})
		a.drawEntryOperations(win, 2, entry, headerStyle, mutedStyle)
	}
}

func (a *vaxisTUIApp) drawEntryOperations(win vaxis.Window, row int, entry tuiTreeEntry, headerStyle, mutedStyle vaxis.Style) {
	operations := a.entryOperations(entry)
	if len(operations) == 0 {
		return
	}

	win.Println(row, vaxis.Segment{Text: "Background tasks", Style: headerStyle})
	row++
	for _, operation := range operations {
		win.Println(row, vaxis.Segment{Text: "• " + operation.Title, Style: mutedStyle})
		row++
		if len(operation.Lines) > 0 {
			a.printLinkifiedLine(win, row, "latest: "+operation.Lines[len(operation.Lines)-1], mutedStyle)
			row++
		}
	}
}

func (a *vaxisTUIApp) drawTerminalSurface(win vaxis.Window, entry tuiTreeEntry, headerStyle, mutedStyle, errorStyle vaxis.Style) {
	if term, ok := a.sessions.SessionTerminal(a.state.activeSessionKey()); ok {
		term.Draw(win)
		return
	}

	row := 0
	switch {
	case entry.valid():
		win.Println(row, vaxis.Segment{Text: "Status: " + a.nodeStatusText(entry.node), Style: headerStyle})
		row++
		win.Println(row, vaxis.Segment{Text: "CPU usage: " + nodeCPUUsageText(entry.node), Style: mutedStyle})
		row++
		win.Println(row, vaxis.Segment{Text: "Memory usage: " + nodeMemoryUsageText(entry.node), Style: mutedStyle})
		row++
		win.Println(row, vaxis.Segment{Text: "Disk usage: " + nodeDiskUsageText(entry.node), Style: mutedStyle})
		row += 2
		if err := a.sessions.SessionError(entry.key()); err != nil {
			win.Println(row, vaxis.Segment{Text: "Unable to start the node terminal.", Style: errorStyle})
			row++
			win.Println(row, vaxis.Segment{Text: err.Error(), Style: mutedStyle})
			row++
		} else if nodeIsRunning(entry.node) {
			win.Println(row, vaxis.Segment{Text: fmt.Sprintf("No terminal tab is open. Press %s to open one.", terminalTabOpenFooterHint), Style: mutedStyle})
			row++
		} else {
			win.Println(row, vaxis.Segment{Text: "Start the node with [s] before opening a terminal tab.", Style: mutedStyle})
			row++
		}
		win.Println(row, vaxis.Segment{Text: fmt.Sprintf("Press %s to show node info.", infoViewToggleFooterHint), Style: mutedStyle})
		row++
		a.drawEntryOperations(win, row, entry, headerStyle, mutedStyle)
	default:
		win.Println(0, vaxis.Segment{Text: "Select a node in the list.", Style: mutedStyle})
	}
}

func drawTerminalPane(win vaxis.Window, tabs []vaxis.Segment, style vaxis.Style) vaxis.Window {
	width, height := win.Size()
	if width <= 0 || height <= 0 {
		return win
	}

	borderCell := vaxis.Cell{
		Character: vaxis.Character{Grapheme: "─", Width: 1},
		Style:     style,
	}
	for col := range width {
		win.SetCell(col, 0, borderCell)
		if height > 1 {
			win.SetCell(col, height-1, borderCell)
		}
	}
	if len(tabs) > 0 {
		borderTabs := make([]vaxis.Segment, 0, len(tabs)+2)
		borderTabs = append(borderTabs, vaxis.Segment{Text: " ", Style: style})
		borderTabs = append(borderTabs, tabs...)
		borderTabs = append(borderTabs, vaxis.Segment{Text: " ", Style: style})
		win.Println(0, borderTabs...)
	}

	if height <= 2 {
		return win.New(0, 0, width, height)
	}
	return win.New(0, 1, width, height-2)
}

func renderFooter(focus tuiFocus, paneMode tuiTreePaneMode, entry tuiTreeEntry) string {
	if focus == tuiFocusTerminal {
		parts := []string{
			terminalViewToggleFooterHint + " tree focus",
			terminalTabOpenFooterHint + " new tab",
			terminalTabPrevFooterHint + "/" + terminalTabNextFooterHint + " switch tab",
			terminalTabMovePrevFooterHint + "/" + terminalTabMoveNextFooterHint + " move tab",
			terminalTabCloseFooterHint + " close tab",
		}
		if entry.valid() {
			parts = append(parts, hostTerminalTabOpenFooterHint+" host tab")
		}
		parts = append(parts, "q quit")
		return strings.Join(parts, "   ")
	}

	parts := make([]string, 0, 12)
	if entry.valid() {
		parts = append(parts, "Up/Down move")
		if nodeIsRunning(entry.node) {
			parts = append(parts, terminalViewToggleFooterHint+" shell focus")
			parts = append(parts, terminalTabOpenFooterHint+" new tab")
		}
		if paneMode == tuiTreePaneModeTerminal {
			parts = append(parts, infoViewToggleFooterHint+" info")
			parts = append(parts, terminalTabPrevFooterHint+"/"+terminalTabNextFooterHint+" switch tab")
			parts = append(parts, terminalTabMovePrevFooterHint+"/"+terminalTabMoveNextFooterHint+" move tab")
			parts = append(parts, terminalTabCloseFooterHint+" close tab")
		} else {
			parts = append(parts, infoViewToggleFooterHint+" terminal")
		}
		parts = append(parts, hostTerminalTabOpenFooterHint+" host tab")
	}

	for _, action := range availableTUIActions(entry) {
		parts = append(parts, fmt.Sprintf("[%c] %s", action.Hotkey, strings.ToLower(action.Label)))
	}
	parts = append(parts, "q quit")
	return strings.Join(parts, "   ")
}

func tuiEntryLinesWithStatus(entry tuiTreeEntry, statusOverride string) []string {
	if !entry.valid() {
		return nil
	}
	status := statusOverride
	if strings.TrimSpace(status) == "" {
		status = nodeVMStatus(entry.node)
	}
	status = strings.ToUpper(status)
	configuration := entry.node.ConfigurationSlug
	if configuration == "" {
		configuration = DefaultConfigurationSlug
	}
	directory := entry.node.DirectoryPath
	if directory != "" {
		directory = filepath.Clean(directory)
	}
	return []string{
		"• " + entry.node.Slug,
		"  Config: " + configuration,
		"  CWD: " + directory,
		"  Status: " + status,
		"  CPU: " + nodeCPUUsageText(entry.node),
		"  Memory: " + nodeMemoryUsageText(entry.node),
		"  Disk: " + nodeDiskUsageText(entry.node),
	}
}

func nodeCPUUsageText(node Node) string {
	if !nodeIsRunning(node) || node.LastRuntimeObservation == nil || node.LastRuntimeObservation.CPUUsagePercent == nil {
		return "--"
	}
	usage := *node.LastRuntimeObservation.CPUUsagePercent
	if math.IsNaN(usage) || math.IsInf(usage, 0) || usage < 0 || usage > 100 {
		return "--"
	}
	return fmt.Sprintf("%.1f%%", usage)
}

func nodeMemoryUsageText(node Node) string {
	if node.LastRuntimeObservation == nil {
		return "--"
	}
	return nodeByteUsageText(node, node.LastRuntimeObservation.MemoryUsedBytes, node.LastRuntimeObservation.MemoryTotalBytes)
}

func nodeDiskUsageText(node Node) string {
	if node.LastRuntimeObservation == nil {
		return "--"
	}
	return nodeByteUsageText(node, node.LastRuntimeObservation.DiskUsedBytes, node.LastRuntimeObservation.DiskTotalBytes)
}

func nodeByteUsageText(node Node, usedBytes, totalBytes *uint64) string {
	if !nodeIsRunning(node) || usedBytes == nil || totalBytes == nil || *totalBytes == 0 || *usedBytes > *totalBytes {
		return "--"
	}
	unitBytes, unitName := float64(1<<30), "GiB"
	switch {
	case *totalBytes >= 1<<30:
	case *totalBytes >= 1<<20:
		unitBytes, unitName = 1<<20, "MiB"
	case *totalBytes >= 1<<10:
		unitBytes, unitName = 1<<10, "KiB"
	default:
		unitBytes, unitName = 1, "B"
	}
	return fmt.Sprintf("%.1f/%.1f %s", float64(*usedBytes)/unitBytes, float64(*totalBytes)/unitBytes, unitName)
}
