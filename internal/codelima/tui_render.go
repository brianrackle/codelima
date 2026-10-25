package codelima

import (
	"fmt"
	"log/slog"
	"math"
	"path/filepath"
	"strings"
	"time"

	"go.rockorager.dev/vaxis"
	"go.rockorager.dev/vaxis/widgets/border"

	"github.com/brianrackle/codelima/internal/codelima/terminal"
)

// tuiMessageLogDefaultCap bounds the message ring; older entries are evicted.
const tuiMessageLogDefaultCap = 200

const (
	tuiMinWidth  = 60
	tuiMinHeight = 14
)

// tuiMessage is one entry in the message surface: when it happened, its severity
// (borrowed from slog so the view can colour it), and the human text.
type tuiMessage struct {
	ID    string
	Time  time.Time
	Level slog.Level
	Text  string
	ack   activityMessageAck
}

// tuiMessageLog bounds local status/notification history and pending journal
// writes. Only an acknowledged entry is durable (ADR 165); the footer can
// expire without discarding this window's pending messages.
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
	l.entries = append(l.entries, tuiMessage{ID: newID(), Time: now(), Level: level, Text: text})
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
	if !ok || !tuiKeyActivates(key, false) {
		return false, nil
	}
	if isOverlayCancelKey(key) {
		return tuiKeyActivates(key, true), nil
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
	a.startActivityMessageDelivery()
}

// setStatus sets the transient footer status and records it in the message
// ring at the given level (error paths use slog.LevelError, successes
// slog.LevelInfo). The footer renders statuses at warn level or above in the
// error style; info statuses render in the normal muted style.
func (a *vaxisTUIApp) setStatus(level slog.Level, text string) {
	a.setFooterStatus(level, text)
	a.pushMessage(level, text)
}

func (a *vaxisTUIApp) setFooterStatus(level slog.Level, text string) {
	a.status, a.statusLevel, a.statusAt = text, level, time.Now()
}

// clearStatus clears the transient footer status; nothing is recorded.
func (a *vaxisTUIApp) clearStatus() {
	a.status = ""
	a.statusLevel = slog.LevelInfo
}

type tuiRect struct {
	col    int
	row    int
	width  int
	height int
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
	for _, key := range keys {
		label, ok := a.terminalTabLabelForKey(key)
		if !ok {
			continue
		}
		if len(segments) > 0 {
			segments = append(segments, vaxis.Segment{Text: " ", Style: inactiveStyle})
		}
		if key == activeKey {
			segments = append(segments, vaxis.Segment{Text: "[" + label + "]", Style: activeStyle})
			continue
		}
		segments = append(segments, vaxis.Segment{Text: label, Style: inactiveStyle})
	}
	return segments
}

func (a *vaxisTUIApp) terminalTabLabelForKey(key string) (string, bool) {
	session, ok := a.sessions.Session(key)
	if !ok {
		return "", false
	}
	metadata := a.terminalMetadata(key)
	if metadata.BellCount < session.acknowledgedBells {
		session.acknowledgedBells = 0
	}
	if key == a.state.activeSessionKey() && a.sessions.windowFocused.Load() && !a.rightPaneShowsInfo() && !a.rightPaneOverrideActive() {
		session.acknowledgedBells = metadata.BellCount
	}
	if metadata.BellCount <= session.acknowledgedBells {
		metadata.BellCount = 0
	}
	return terminalTabLabel(session, metadata), true
}

func terminalTabLabel(session *tuiSession, metadata TerminalMetadata) string {
	if session == nil {
		return ""
	}
	// Classify before truncating so a long username or a trailing task title
	// cannot change whether this is recognized as an ordinary shell title.
	title := terminalMetadataText(metadata.Title, len(metadata.Title))
	label := "shell"
	if session.shellKind == terminal.NodeHostShell {
		label = "host"
	}
	if !isDefaultShellTitle(title) {
		label = terminalMetadataText(title, 40)
		if session.shellKind == terminal.NodeHostShell {
			label = "host:" + label
		}
	}
	if badge := terminalMetadataBadge(metadata); badge != "" {
		label += " · " + badge
	}
	if metadata.BellCount > 0 {
		label += " 🔔"
	}
	return label
}

func (a *vaxisTUIApp) headerLogoText() string {
	if a.logoAnimation == nil {
		return tuiLogoText
	}
	return a.logoAnimation.text()
}

func (a *vaxisTUIApp) headerStyle() vaxis.Style {
	theme := newTUITheme(a.vx != nil && a.vx.CanRGB())
	style := theme.mint
	style.Background = theme.chrome.Background
	style.Attribute = vaxis.AttrBold
	if a.hostTerminalTabActive() {
		style.Foreground = theme.coral.Foreground
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
	a.beginGraphicsFrame()
	defer a.finishGraphicsFrame()
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
	a.recordTerminalNotices()
	if a.vx == nil {
		return
	}

	window := a.vx.Window()
	window.Clear()
	a.vx.HideCursor()

	width, height := window.Size()
	a.treeContentRect = tuiRect{}
	a.terminalBodyRect = tuiRect{}
	a.detailTargets = nil
	a.nodeBackRect = tuiRect{}
	a.embedded.clearTargets()
	a.linkRegions = nil
	if width < tuiMinWidth || height < tuiMinHeight {
		window.Println(0, vaxis.Segment{Text: fmt.Sprintf("CodeLima TUI requires at least %dx%d terminal cells.", tuiMinWidth, tuiMinHeight)})
		window.Println(1, vaxis.Segment{Text: fmt.Sprintf("Current size: %dx%d", width, height)})
		a.vx.Render()
		return
	}

	a.drawManager(window)

	a.vx.Render()
}

// tuiEmbeddedTerminalSize is shared by initial session sizing and later resizes.
// The manager header, identity, detail tabs, session strip, frame and footers
// reserve thirteen rows and four columns; focus never changes that geometry.
func tuiEmbeddedTerminalSize(width int, height int, _ tuiFocus) (cols, rows int) {
	return max(0, width-4), max(0, height-13)
}

func (a *vaxisTUIApp) activeTerminalSize(width int, height int, focus tuiFocus) (cols, rows int) {
	return tuiEmbeddedTerminalSize(width, height, focus)
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
	for _, operation := range a.activity.page.Running {
		if operation.NodeID != entry.node.ID {
			continue
		}
		if operation.State == "canceling" {
			return "canceling"
		}
		switch operation.Request.Kind {
		case "start", "retry":
			return "starting"
		case "restart":
			if operation.CurrentPhase == "stop" {
				return "stopping"
			}
			return "starting"
		case "stop":
			return "stopping"
		case "delete":
			return "deleting"
		case "clone":
			return "cloning"
		}
	}

	if member, reserved := a.batchReservation(entry.node.ID); reserved {
		if member.State == "queued" {
			return "queued"
		}
		return "batch reserved"
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
		if terminal, ok := term.(*daemonTUITerminal); ok {
			a.drawTerminalGraphics(win, terminal)
		}
		a.drawTerminalSearch(win)
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
		} else if nodeGuestShellReady(entry.node) {
			win.Println(row, vaxis.Segment{Text: fmt.Sprintf("No terminal tab is open. Press %s to open one.", terminalTabOpenFooterHint), Style: mutedStyle})
			row++
		} else if reason := nodeGuestShellBlocked(entry.node); reason != "" {
			win.Println(row, vaxis.Segment{Text: reason + ".", Style: mutedStyle})
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
