package codelima

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"git.sr.ht/~rockorager/vaxis"
	"git.sr.ht/~rockorager/vaxis/widgets/border"
	"git.sr.ht/~rockorager/vaxis/widgets/term"

	"github.com/brianrackle/test_lima/internal/codelima/terminal"
)

type vaxisTUIRunner struct{}

func newTUIRunner() TUIRunner {
	return &vaxisTUIRunner{}
}

// tuiMessageLogDefaultCap bounds the message ring; older entries are evicted.
const tuiMessageLogDefaultCap = 200

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
func (v *tuiMessagesView) Update(event vaxis.Event) (closed bool) {
	key, ok := event.(vaxis.Key)
	if !ok {
		return false
	}
	if isOverlayCancelKey(key) {
		return true
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
	return false
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

func (v *tuiMessagesView) Draw(win vaxis.Window, headerStyle, mutedStyle vaxis.Style) {
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

// pushMessage is the single nil-safe entry point for recording a non-status
// message into the ring at its true level. Explicit seams (operation results in
// retainOperationMessages, refresh failures in finishDataRefresh) route through
// it; transient footer statuses are mirrored separately, at info level, by
// captureStatusMessage.
func (a *vaxisTUIApp) pushMessage(level slog.Level, text string) {
	if a == nil || a.messages == nil {
		return
	}
	a.messages.Append(level, text)
}

// captureStatusMessage mirrors transient footer status changes into the durable
// message ring. It runs on every draw so any code path that sets a.status (there
// are ~40) is retained without threading a setter through all of them. Clearing
// the status to "" records nothing and re-arms capture for the next message; a
// caller that has already recorded a message at its true level sets
// lastCapturedStatus to suppress a duplicate info-level capture here.
func (a *vaxisTUIApp) captureStatusMessage() {
	if a == nil || a.messages == nil {
		return
	}
	text := strings.TrimSpace(a.status)
	if text == "" {
		a.lastCapturedStatus = ""
		return
	}
	if text == a.lastCapturedStatus {
		return
	}
	a.lastCapturedStatus = text
	a.messages.Append(slog.LevelInfo, a.status)
}

func (a *vaxisTUIApp) openMessagesView() {
	var entries []tuiMessage
	if a.messages != nil {
		entries = a.messages.Entries()
	}
	a.messagesView = newTUIMessagesView(entries)
}

type tuiSession struct {
	key        string
	target     string
	kind       tuiTreeEntryKind
	label      string
	project    Project
	node       Node
	terminalID terminal.TerminalID
}

type tuiSessionStore struct {
	ctx           context.Context
	service       *Service
	postEvent     func(vaxis.Event)
	sessions      map[string]*tuiSession
	sessionErrors map[string]error
	sessionOrder  []string
	tabCounters   map[string]int
	registry      *terminal.TerminalRuntimeRegistry[tuiTerminal]

	preferredCols int
	preferredRows int

	nodeShellExecutable    string
	nodeShellExecutableErr error
}

func newTUISessionStore(ctx context.Context, service *Service, postEvent func(vaxis.Event)) *tuiSessionStore {
	executable, executableErr := os.Executable()
	if executableErr == nil {
		executable = resolveCodelimaExecutablePath(executable)
	}
	return &tuiSessionStore{
		ctx:                    ctx,
		service:                service,
		postEvent:              postEvent,
		sessions:               map[string]*tuiSession{},
		sessionErrors:          map[string]error{},
		tabCounters:            map[string]int{},
		registry:               terminal.NewTerminalRuntimeRegistry[tuiTerminal](),
		nodeShellExecutable:    executable,
		nodeShellExecutableErr: executableErr,
	}
}

var newSessionTUITerminal = newTUITerminal

func (s *tuiSessionStore) HasSession(sessionKey string) bool {
	_, ok := s.sessions[sessionKey]
	return ok
}

// formatSessionKey renders a terminal tab (session) key for a target as
// "<targetKey>#<n>". The "#n" suffix is purely a per-target ordering/display
// discriminator; nothing ever parses it back into a target (session→target
// resolution uses the stored session.target field — see ADR 61). Production
// (nextSessionKey) and the test fake share this single formatter.
func formatSessionKey(targetKey string, counter int) string {
	return fmt.Sprintf("%s#%d", targetKey, counter)
}

// nextSessionKey allocates a unique tab key for the target. Each explicit
// open-tab command produces a fresh session keyed "<target>#<n>".
func (s *tuiSessionStore) nextSessionKey(targetKey string) string {
	if s.tabCounters == nil {
		s.tabCounters = map[string]int{}
	}
	s.tabCounters[targetKey]++
	return formatSessionKey(targetKey, s.tabCounters[targetKey])
}

// TargetSessionKeys lists the open terminal tabs that belong to a single
// project or node target, in the order they were opened.
func (s *tuiSessionStore) TargetSessionKeys(targetKey string) []string {
	if targetKey == "" {
		return nil
	}
	keys := make([]string, 0, 2)
	for _, key := range s.sessionOrder {
		session, ok := s.sessions[key]
		if ok && session.target == targetKey {
			keys = append(keys, key)
		}
	}
	return keys
}

func (s *tuiSessionStore) SetPreferredTerminalSize(cols, rows int) {
	if cols <= 0 || rows <= 0 {
		return
	}

	s.preferredCols = cols
	s.preferredRows = rows
}

func (s *tuiSessionStore) OpenProjectTab(project Project) (string, error) {
	targetKey := terminal.ProjectTarget(project.ID).String()
	delete(s.sessionErrors, targetKey)

	if strings.TrimSpace(project.WorkspacePath) == "" {
		err := fmt.Errorf("project workspace path is not configured")
		s.sessionErrors[targetKey] = err
		return "", err
	}
	info, err := os.Stat(project.WorkspacePath)
	if err != nil {
		err = fmt.Errorf("project workspace path is unavailable: %w", err)
		s.sessionErrors[targetKey] = err
		return "", err
	}
	if !info.IsDir() {
		err := fmt.Errorf("project workspace path is not a directory: %s", project.WorkspacePath)
		s.sessionErrors[targetKey] = err
		return "", err
	}

	args := interactiveShellLaunchCommand()
	command := exec.CommandContext(s.ctx, args[0], args[1:]...)
	command.Env = os.Environ()
	command.Dir = project.WorkspacePath

	key := s.nextSessionKey(targetKey)
	term := newSessionTUITerminal(key, s.postEvent)
	if s.preferredCols > 0 && s.preferredRows > 0 {
		term.Resize(s.preferredCols, s.preferredRows)
	}
	if err := term.Start(command); err != nil {
		s.sessionErrors[targetKey] = err
		return "", err
	}

	runtime := s.registry.Allocate(term)
	s.putSession(&tuiSession{
		key:        key,
		target:     targetKey,
		kind:       tuiTreeEntryProject,
		label:      project.Slug,
		project:    project,
		terminalID: runtime.ID,
	})
	s.service.log().Debug("terminal opened", "kind", "project", "target", targetKey, "session", key)
	return key, nil
}

func (s *tuiSessionStore) OpenNodeTab(node Node) (string, error) {
	targetKey := terminal.NodeTarget(node.ID).String()
	delete(s.sessionErrors, targetKey)

	executable, err := s.nodeTabExecutable()
	if err != nil {
		s.sessionErrors[targetKey] = err
		return "", err
	}

	command := exec.CommandContext(s.ctx, executable, "--home", s.service.cfg.MetadataRoot, "shell", node.ID)
	command.Env = os.Environ()

	key := s.nextSessionKey(targetKey)
	term := newSessionTUITerminal(key, s.postEvent)
	if s.preferredCols > 0 && s.preferredRows > 0 {
		term.Resize(s.preferredCols, s.preferredRows)
	}
	if err := term.Start(command); err != nil {
		err = nodeTabStartError(executable, err)
		s.sessionErrors[targetKey] = err
		return "", err
	}

	runtime := s.registry.Allocate(term)
	s.putSession(&tuiSession{
		key:        key,
		target:     targetKey,
		kind:       tuiTreeEntryNode,
		label:      node.Slug,
		node:       node,
		terminalID: runtime.ID,
	})
	s.service.log().Debug("terminal opened", "kind", "node", "target", targetKey, "session", key)
	return key, nil
}

func resolveCodelimaExecutablePath(executable string) string {
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		return resolved
	}
	return executable
}

func (s *tuiSessionStore) nodeTabExecutable() (string, error) {
	if s.nodeShellExecutableErr != nil {
		return "", fmt.Errorf("resolve codelima executable: %w", s.nodeShellExecutableErr)
	}
	if strings.TrimSpace(s.nodeShellExecutable) == "" {
		return "", fmt.Errorf("resolve codelima executable: empty path")
	}
	return s.nodeShellExecutable, nil
}

func nodeTabStartError(executable string, err error) error {
	if errors.Is(err, syscall.ENOEXEC) {
		return fmt.Errorf("binary at %q is not compatible with this platform; run make build on this platform and restart codelima: %w", executable, err)
	}
	return fmt.Errorf("start node shell with codelima executable %q: %w", executable, err)
}

func (s *tuiSessionStore) putSession(session *tuiSession) {
	if session == nil || session.key == "" {
		return
	}
	if _, ok := s.sessions[session.key]; !ok {
		s.sessionOrder = append(s.sessionOrder, session.key)
	}
	s.sessions[session.key] = session
}

func (s *tuiSessionStore) Session(targetKey string) (*tuiSession, bool) {
	session, ok := s.sessions[targetKey]
	return session, ok
}

func (s *tuiSessionStore) SessionError(targetKey string) error {
	return s.sessionErrors[targetKey]
}

// ClearSessionError drops any recorded open error for a target. It is the
// sanctioned accessor so callers do not reach into the sessionErrors map.
func (s *tuiSessionStore) ClearSessionError(targetKey string) {
	delete(s.sessionErrors, targetKey)
}

// terminalFor resolves a session's live terminal backend through the runtime
// registry. It returns false when the session is nil or its runtime is gone.
func (s *tuiSessionStore) terminalFor(session *tuiSession) (tuiTerminal, bool) {
	if session == nil {
		return nil, false
	}
	runtime, ok := s.registry.Lookup(session.terminalID)
	if !ok {
		return nil, false
	}
	return runtime.Backend, true
}

// SessionTerminal resolves the live terminal backend for a session key. It is
// the sanctioned accessor so callers do not reach into the sessions map or the
// session's terminal handle.
func (s *tuiSessionStore) SessionTerminal(sessionKey string) (tuiTerminal, bool) {
	session, ok := s.sessions[sessionKey]
	if !ok {
		return nil, false
	}
	return s.terminalFor(session)
}

// SyncFocus focuses the terminal for activeSessionKey when focusActive is set
// and blurs every other open terminal. It owns the sessions-map iteration so
// the app does not reach into store internals.
func (s *tuiSessionStore) SyncFocus(activeSessionKey string, focusActive bool) {
	for sessionKey, session := range s.sessions {
		term, ok := s.terminalFor(session)
		if !ok {
			continue
		}
		if focusActive && sessionKey == activeSessionKey {
			term.Focus()
			continue
		}
		term.Blur()
	}
}

// PruneStaleSessions closes every open session whose target no longer exists
// (per keep) and drops every recorded target error whose target is likewise
// gone. It owns the sessions/sessionErrors iteration so the app does not reach
// into store internals.
func (s *tuiSessionStore) PruneStaleSessions(keep func(targetKey string) bool) {
	var orphans []string
	for sessionKey, session := range s.sessions {
		if !keep(session.target) {
			orphans = append(orphans, sessionKey)
		}
	}
	for _, sessionKey := range orphans {
		s.CloseSession(sessionKey)
	}
	for targetKey := range s.sessionErrors {
		if !keep(targetKey) {
			delete(s.sessionErrors, targetKey)
		}
	}
}

func (s *tuiSessionStore) RemoveSession(sessionKey string) (*tuiSession, bool) {
	session := s.sessions[sessionKey]
	if session == nil {
		return nil, false
	}
	delete(s.sessions, sessionKey)
	s.removeSessionOrder(sessionKey)
	// The terminal exited on its own (finish path); just forget the runtime.
	s.registry.Remove(session.terminalID)
	return session, true
}

func (s *tuiSessionStore) Close() {
	for sessionKey, session := range s.sessions {
		if runtime, ok := s.registry.Remove(session.terminalID); ok {
			runtime.Backend.Close()
		}
		s.service.log().Debug("terminal closed", "target", session.target, "session", sessionKey, "reason", "shutdown")
		delete(s.sessions, sessionKey)
	}
	s.sessionOrder = nil
}

func (s *tuiSessionStore) CloseSession(sessionKey string) {
	session, ok := s.sessions[sessionKey]
	if !ok {
		return
	}

	delete(s.sessions, sessionKey)
	s.removeSessionOrder(sessionKey)
	if runtime, ok := s.registry.Remove(session.terminalID); ok {
		runtime.Backend.Close()
	}
	s.service.log().Debug("terminal closed", "target", session.target, "session", sessionKey, "reason", "tab-close")
}

// CloseTargetSessions closes every open terminal tab for a project or node
// target and clears the target's recorded open error.
func (s *tuiSessionStore) CloseTargetSessions(targetKey string) {
	for _, sessionKey := range s.TargetSessionKeys(targetKey) {
		s.CloseSession(sessionKey)
	}
	delete(s.sessionErrors, targetKey)
}

func (s *tuiSessionStore) CloseNode(nodeID string) {
	s.CloseTargetSessions(terminal.NodeTarget(nodeID).String())
}

func (s *tuiSessionStore) removeSessionOrder(targetKey string) {
	for index, key := range s.sessionOrder {
		if key != targetKey {
			continue
		}
		s.sessionOrder = append(s.sessionOrder[:index], s.sessionOrder[index+1:]...)
		return
	}
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

type tuiTerminalMouseGesture struct {
	targetKey string
	startCol  int
	startRow  int
	dragged   bool
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

type vaxisTUIApp struct {
	ctx                context.Context
	service            *Service
	vx                 *vaxis.Vaxis
	postEvent          func(vaxis.Event)
	treeWorkspaceRoot  string
	openLink           func(string) error
	screenHyperlinkAt  func(int, int) (string, bool)
	state              *tuiState
	sessions           *tuiSessionStore
	operations         map[string]*tuiOperationState
	operationOrder     []string
	linkRegions        []tuiLinkRegion
	terminalMouse      *tuiTerminalMouseGesture
	dialog             *tuiDialog
	menu               *tuiMenu
	selector           *tuiSelector
	messagesView       *tuiMessagesView
	messages           *tuiMessageLog
	lastCapturedStatus string
	status             string
	refreshInFlight    bool
	clipboardPush      func(string) error
	treeContentRect    tuiRect
	terminalBodyRect   tuiRect
}

const (
	terminalViewToggleFooterHint = "Opt-`/F6"
	terminalViewToggleTextHint   = "Opt-` or F6"
	hostTerminalToggleFooterHint = "Opt-Shift-`"
	infoViewToggleFooterHint     = "[i]"
	terminalTabOpenFooterHint    = "Opt-t"
	terminalTabNextFooterHint    = "Opt-Right"
	terminalTabPrevFooterHint    = "Opt-Left"
	terminalTabCloseFooterHint   = "Opt-w"
	tuiAutoRefreshInterval       = 2 * time.Second
)

func (r *vaxisTUIRunner) Run(ctx context.Context, service *Service, workspaceRoot string) error {
	// TUI mode logs to a file sink (CODELIMA_HOME/_logs/codelima.log) instead of
	// stderr so structured logs never corrupt the rendered chrome. This also
	// routes the libghostty stderr capture to the same file (ADR 59). A failure
	// to open the file leaves the discard/CLI logger in place rather than
	// aborting the TUI.
	if closeLog, err := service.enableFileLogging(); err == nil {
		defer func() { _ = closeLog() }()
	}

	tree, err := loadTUIProjectTree(ctx, service, workspaceRoot)
	if err != nil {
		return err
	}

	vx, err := vaxis.New(vaxis.Options{})
	if err != nil {
		return err
	}
	defer vx.Close()

	sessions := newTUISessionStore(ctx, service, vx.PostEvent)
	defer sessions.Close()

	state, err := newTUIState(tree, sessions)
	if err != nil {
		return err
	}

	app := &vaxisTUIApp{
		ctx:               ctx,
		service:           service,
		vx:                vx,
		postEvent:         vx.PostEvent,
		treeWorkspaceRoot: workspaceRoot,
		state:             state,
		sessions:          sessions,
		operations:        map[string]*tuiOperationState{},
		messages:          newTUIMessageLog(tuiMessageLogDefaultCap),
	}
	winWidth, winHeight := vx.Window().Size()
	cols, rows := tuiEmbeddedTerminalSize(winWidth, winHeight, tuiFocusTree)
	sessions.SetPreferredTerminalSize(cols, rows)
	if err := state.openInitialTerminalTab(); err != nil {
		app.status = err.Error()
	}
	app.syncSessionFocus()
	app.draw()

	stopRefresh := startTUIAutoRefresh(ctx, vx.PostEvent, tuiAutoRefreshInterval)
	defer stopRefresh()

	return app.serve(vx.Events())
}

// serve runs the TUI event loop until the context is cancelled, the event
// channel closes, or a handler requests quit. It owns the guaranteed teardown
// of the live terminal sessions: draining them is deferred here so that a
// cancelled context (e.g. Ctrl+C wired through signal.NotifyContext in main)
// still closes every terminal — group-killing the VM shells — instead of the
// process exiting through a happy-path-only cleanup. Run keeps its own
// sessions.Close() defer as a backstop for failures before the loop starts
// (Close drains the session map, so the second call is a no-op), and keeps
// host-terminal restoration (vx.Close) and auto-refresh cancellation deferred
// there, keyed to the resources it owns.
func (a *vaxisTUIApp) serve(events chan vaxis.Event) error {
	defer a.sessions.Close()

	for {
		select {
		case <-a.ctx.Done():
			return a.ctx.Err()
		case event, ok := <-events:
			if !ok {
				return nil
			}
			quit, err := a.handleEvent(event)
			if err != nil {
				return err
			}
			if quit {
				return nil
			}
		}
	}
}

func loadTUIProjectTree(ctx context.Context, service *Service, workspaceRoot string) ([]ProjectTreeNode, error) {
	if strings.TrimSpace(workspaceRoot) != "" {
		return service.ProjectTreeByWorkspaceRoot(ctx, workspaceRoot, false)
	}
	return service.ProjectTree(ctx, "", false)
}

func (a *vaxisTUIApp) handleEvent(event vaxis.Event) (bool, error) {
	switch event := event.(type) {
	case tuiRefreshTickEvent:
		a.startDataRefresh()
		return false, nil
	case tuiRefreshCompleteEvent:
		a.finishDataRefresh(event)
		a.draw()
		return false, nil
	case tuiClipboardEvent:
		if err := a.copyToHostClipboard(event.Text); err != nil {
			a.status = err.Error()
		} else {
			a.status = "synced VM clipboard to host clipboard"
		}
		a.draw()
		return false, nil
	case tuiOperationProgressEvent:
		a.appendOperationLine(event.OperationID, event.Line)
		a.draw()
		return false, nil
	case tuiOperationCompleteEvent:
		a.finishOperation(event)
		a.draw()
		return false, nil
	case tuiTerminalClosedEvent:
		a.handleTerminalClosed(event)
		a.draw()
		return false, nil
	case tuiTerminalErrorEvent:
		a.status = event.Err.Error()
		a.draw()
		return false, nil
	}

	if key, ok := event.(vaxis.Key); ok && isQuitKey(key) && (a.dialog != nil || a.menu != nil || a.selector != nil || a.messagesView != nil) {
		return true, nil
	}

	if a.messagesView != nil {
		if a.messagesView.Update(event) {
			a.messagesView = nil
		}
		a.draw()
		return false, nil
	}

	if a.selector != nil {
		completed, cancelled, err := a.selector.Update(event)
		if err != nil {
			a.status = err.Error()
		}
		if completed || cancelled || err != nil {
			a.selector = nil
		}
		a.draw()
		return false, nil
	}

	if a.dialog != nil {
		completed, cancelled, err := a.dialog.Update(event)
		if err != nil {
			return false, err
		}
		if completed || cancelled {
			a.dialog = nil
		}
		a.draw()
		return false, nil
	}

	if a.menu != nil {
		completed, cancelled, err := a.menu.Update(event)
		if err != nil {
			a.status = err.Error()
		}
		if completed || cancelled || err != nil {
			a.menu = nil
		}
		a.draw()
		return false, nil
	}

	switch event := event.(type) {
	case vaxis.Key:
		quit, err := a.handleKey(event)
		a.draw()
		return quit, err
	case vaxis.Mouse:
		err := a.handleMouse(event)
		a.draw()
		return false, err
	case vaxis.PasteStartEvent:
		a.forwardTerminalEvent(event)
		a.draw()
	case vaxis.PasteEndEvent:
		a.forwardTerminalEvent(event)
		a.draw()
	case vaxis.ColorThemeUpdate:
		a.forwardTerminalEvent(event)
		a.draw()
	case vaxis.Resize:
		a.handleResize(event)
		a.draw()
	case vaxis.Redraw:
		a.draw()
	case vaxis.SyncFunc:
		event()
		a.draw()
	case term.EventNotify:
		a.vx.Notify(event.Title, event.Body)
	case vaxis.QuitEvent:
		return true, nil
	}

	return false, nil
}

func (a *vaxisTUIApp) handleKey(key vaxis.Key) (bool, error) {
	if isTerminalTabOpenKey(key) {
		if err := a.openTerminalTab(); err != nil {
			a.status = err.Error()
			return false, nil
		}
		a.status = ""
		a.syncSessionFocus()
		return false, nil
	}

	if isTerminalTabNextKey(key) {
		if err := a.switchTerminalTab(1); err != nil {
			a.status = err.Error()
			return false, nil
		}
		a.status = ""
		a.syncSessionFocus()
		return false, nil
	}

	if isTerminalTabPreviousKey(key) {
		if err := a.switchTerminalTab(-1); err != nil {
			a.status = err.Error()
			return false, nil
		}
		a.status = ""
		a.syncSessionFocus()
		return false, nil
	}

	if isTerminalTabCloseKey(key) {
		if err := a.closeTerminalTab(); err != nil {
			a.status = err.Error()
			return false, nil
		}
		a.status = ""
		a.syncSessionFocus()
		return false, nil
	}

	if isHostTerminalToggleKey(key) {
		if err := a.state.toggleHostTerminal(); err != nil {
			a.status = err.Error()
			return false, nil
		}
		a.status = ""
		a.syncSessionFocus()
		return false, nil
	}

	if isTerminalViewToggleKey(key) {
		if err := a.state.toggleFocus(); err != nil {
			a.status = err.Error()
			return false, nil
		}
		a.status = ""
		a.syncSessionFocus()
		return false, nil
	}

	if a.state.focus == tuiFocusTree && key.MatchString("i") {
		if err := a.state.toggleTreePaneMode(); err != nil {
			a.status = err.Error()
			return false, nil
		}
		a.status = ""
		a.syncSessionFocus()
		return false, nil
	}

	if a.state.focus == tuiFocusTree && key.MatchString("m") {
		a.openMessagesView()
		return false, nil
	}

	if a.state.focus == tuiFocusTerminal {
		a.forwardTerminalEvent(key)
		return false, nil
	}

	if action, ok := a.matchAction(key); ok {
		if err := a.performAction(action); err != nil {
			a.status = err.Error()
			return false, nil
		}
		return false, nil
	}

	var err error
	switch {
	case key.MatchString("q"), isQuitKey(key):
		return true, nil
	case key.MatchString("Up"):
		err = a.state.moveSelection(-1)
	case key.MatchString("Down"):
		err = a.state.moveSelection(1)
	case key.MatchString("Left"):
		a.state.collapseSelection()
	case key.MatchString("Right"):
		a.state.expandSelection()
	default:
		return false, nil
	}

	if err != nil {
		a.status = err.Error()
	} else {
		a.status = ""
	}
	a.syncSessionFocus()
	return false, nil
}

func (a *vaxisTUIApp) matchAction(key vaxis.Key) (tuiActionSpec, bool) {
	if normalizedKeyModifiers(key.Modifiers) != 0 {
		return tuiActionSpec{}, false
	}

	pressed := []rune(strings.ToLower(key.Text))
	if len(pressed) == 0 {
		return tuiActionSpec{}, false
	}

	for _, action := range availableTUIActions(a.state.selectedEntry()) {
		if action.Hotkey == pressed[0] {
			return action, true
		}
	}

	return tuiActionSpec{}, false
}

func (a *vaxisTUIApp) actionResourceKeys(action tuiActionSpec, entry tuiTreeEntry) []string {
	switch action.ID {
	case tuiActionProjectCreate:
		return []string{"projects"}
	case tuiActionProjectCreateNode:
		return []string{terminal.ProjectTarget(entry.project.ID).String()}
	case tuiActionProjectUpdate, tuiActionProjectDelete:
		return []string{terminal.ProjectTarget(entry.project.ID).String()}
	case tuiActionNodeStart, tuiActionNodeStop, tuiActionNodeDelete:
		return []string{terminal.NodeTarget(entry.node.ID).String()}
	case tuiActionNodeClone:
		return []string{terminal.NodeTarget(entry.node.ID).String(), terminal.ProjectTarget(entry.project.ID).String()}
	default:
		return nil
	}
}

func (a *vaxisTUIApp) ensureActionNotConflicting(action tuiActionSpec, entry tuiTreeEntry) error {
	if conflict := a.conflictingOperation(a.actionResourceKeys(action, entry)); conflict != nil {
		return fmt.Errorf("%s is already in progress", strings.ToLower(conflict.Title))
	}
	return nil
}

func (a *vaxisTUIApp) performAction(action tuiActionSpec) error {
	entry := a.state.selectedEntry()
	if err := a.ensureActionNotConflicting(action, entry); err != nil {
		return err
	}
	switch action.ID {
	case tuiActionProjectCreate:
		a.openCreateProjectDialog()
	case tuiActionEnvironmentConfigManage:
		return a.openEnvironmentConfigsMenu()
	case tuiActionProjectCreateNode:
		a.openCreateNodeDialog(entry.project)
	case tuiActionProjectUpdate:
		a.openUpdateProjectDialog(entry.project)
	case tuiActionProjectDelete:
		a.openDeleteProjectDialog(entry.project)
	case tuiActionNodeStart:
		return a.startOperation(tuiOperationRequest{
			Title:         "Starting " + entry.node.Slug,
			DisplayStatus: "starting",
			ResourceKeys:  []string{terminal.NodeTarget(entry.node.ID).String()},
			EntryKeys:     []string{terminal.NodeTarget(entry.node.ID).String()},
			Run: func(ctx context.Context, service *Service) (tuiOperationResult, error) {
				node, err := service.NodeStart(ctx, entry.node.ID)
				if err != nil {
					return tuiOperationResult{}, err
				}
				return tuiOperationResult{
					Status:       "started node " + node.Slug,
					PreferredKey: terminal.NodeTarget(node.ID).String(),
					ReloadData:   true,
				}, nil
			},
		})
	case tuiActionNodeStop:
		return a.startOperation(tuiOperationRequest{
			Title:         "Stopping " + entry.node.Slug,
			DisplayStatus: "stopping",
			ResourceKeys:  []string{terminal.NodeTarget(entry.node.ID).String()},
			EntryKeys:     []string{terminal.NodeTarget(entry.node.ID).String()},
			Run: func(ctx context.Context, service *Service) (tuiOperationResult, error) {
				node, err := service.NodeStop(ctx, entry.node.ID)
				if err != nil {
					return tuiOperationResult{}, err
				}
				return tuiOperationResult{
					Status:       "stopped node " + node.Slug,
					PreferredKey: terminal.NodeTarget(node.ID).String(),
					CloseNodeID:  node.ID,
					ReloadData:   true,
				}, nil
			},
		})
	case tuiActionNodeDelete:
		a.openDeleteNodeDialog(entry.node)
	case tuiActionNodeClone:
		a.openCloneNodeDialog(entry.node, entry.project)
	}

	return nil
}

func commaSeparatedValues(values []string) string {
	return strings.Join(values, ",")
}

func parseCommaSeparatedValues(value string) []string {
	if strings.TrimSpace(value) == "" {
		return []string{}
	}

	parts := strings.Split(value, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		values = append(values, part)
	}

	return values
}

func environmentConfigSelectionSummary(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ", ")
}

func (a *vaxisTUIApp) environmentConfigSelectorOptions() ([]tuiSelectorOption, error) {
	configs, err := a.service.EnvironmentConfigList(false)
	if err != nil {
		return nil, err
	}

	options := make([]tuiSelectorOption, 0, len(configs))
	for _, config := range configs {
		label := config.Slug
		if len(config.BootstrapCommands) > 0 {
			label = fmt.Sprintf("%s (%d bootstrap commands)", config.Slug, len(config.BootstrapCommands))
		}
		options = append(options, tuiSelectorOption{
			Label: label,
			Value: config.Slug,
		})
	}
	return options, nil
}

func (a *vaxisTUIApp) openEnvironmentConfigSelector(title string, description []string, current []string, multi bool, onSubmit func(values []string) error) error {
	options, err := a.environmentConfigSelectorOptions()
	if err != nil {
		return err
	}
	if !multi && len(options) == 0 {
		return fmt.Errorf("no environment configs configured")
	}
	if multi && len(options) == 0 {
		description = append(description, "No reusable environment configs configured. Press Enter to keep none assigned.")
	}
	a.selector = newTUISelector(title, description, options, current, multi, onSubmit)
	return nil
}

func commandSelectorOptions(commands []string) []tuiSelectorOption {
	options := make([]tuiSelectorOption, 0, len(commands))
	for index, command := range commands {
		options = append(options, tuiSelectorOption{
			Label: fmt.Sprintf("%d. %s", index+1, command),
			Value: strconv.Itoa(index),
		})
	}
	return options
}

func parseSelectorIndices(values []string, length int) ([]int, error) {
	indices := make([]int, 0, len(values))
	seen := map[int]bool{}
	for _, value := range values {
		index, err := strconv.Atoi(value)
		if err != nil {
			return nil, fmt.Errorf("invalid command selection")
		}
		if index < 0 || index >= length {
			return nil, fmt.Errorf("selected command is out of range")
		}
		if seen[index] {
			continue
		}
		indices = append(indices, index)
		seen[index] = true
	}
	sort.Ints(indices)
	return indices, nil
}

func removeCommandsByIndex(commands []string, indices []int) []string {
	if len(indices) == 0 {
		return append([]string(nil), commands...)
	}

	filtered := make([]string, 0, len(commands)-len(indices))
	selected := map[int]bool{}
	for _, index := range indices {
		selected[index] = true
	}
	for index, command := range commands {
		if selected[index] {
			continue
		}
		filtered = append(filtered, command)
	}
	return filtered
}

func moveCommand(commands []string, index int, delta int) []string {
	target := index + delta
	if index < 0 || index >= len(commands) || target < 0 || target >= len(commands) {
		return append([]string(nil), commands...)
	}

	moved := append([]string(nil), commands...)
	moved[index], moved[target] = moved[target], moved[index]
	return moved
}

func (a *vaxisTUIApp) reopenEnvironmentConfigCommandMenu(configID string) error {
	config, err := a.service.EnvironmentConfigShow(configID)
	if err != nil {
		return err
	}
	a.openEnvironmentConfigCommandMenu(config)
	return nil
}

func (a *vaxisTUIApp) handleMouse(mouse vaxis.Mouse) error {
	if mouse.EventType == vaxis.EventPress && mouse.Button == vaxis.MouseLeftButton {
		if target, ok := a.linkTargetAt(mouse.Col, mouse.Row); ok {
			if err := a.openHyperlink(target); err != nil {
				a.status = err.Error()
				return nil
			}
			a.status = "opened " + target
			return nil
		}
	}

	if a.treeContentRect.contains(mouse.Col, mouse.Row) && mouse.EventType == vaxis.EventPress && mouse.Button == vaxis.MouseLeftButton {
		a.cancelTerminalMouseGesture()
		a.state.focusTree()
		if err := a.state.selectTreeRow(mouse.Row-a.treeContentRect.row, a.treeContentRect.height); err != nil {
			a.status = err.Error()
			return nil
		}
		a.status = ""
		a.syncSessionFocus()
		return nil
	}

	if !a.terminalBodyRect.contains(mouse.Col, mouse.Row) {
		if mouse.EventType == vaxis.EventRelease && mouse.Button == vaxis.MouseLeftButton {
			a.cancelTerminalMouseGesture()
		}
		return nil
	}

	if a.state.focus != tuiFocusTerminal && a.state.treePaneMode != tuiTreePaneModeTerminal {
		return nil
	}

	entry := a.mouseTerminalEntry()
	if entry.kind != tuiTreeEntryProject && entry.kind != tuiTreeEntryNode {
		return nil
	}

	sessionKey := a.state.activeSessionKey()
	term, ok := a.sessions.SessionTerminal(sessionKey)
	if !ok {
		return nil
	}

	translated := a.terminalBodyRect.translateMouse(mouse)
	if mouse.Button == vaxis.MouseWheelUp || mouse.Button == vaxis.MouseWheelDown {
		a.forwardSessionEvent(sessionKey, translated)
		return nil
	}

	if !term.CapturesMouse() {
		if err := a.handleTerminalMouseGesture(sessionKey, mouse, translated); err != nil {
			a.status = err.Error()
		}
		return nil
	}

	a.cancelTerminalMouseGesture()
	if mouse.EventType != vaxis.EventPress || mouse.Button != vaxis.MouseLeftButton {
		if a.state.focus == tuiFocusTerminal {
			a.forwardSessionEvent(sessionKey, translated)
		}
		return nil
	}

	if a.state.focus != tuiFocusTerminal {
		if err := a.state.focusTerminal(); err != nil {
			a.status = err.Error()
			return nil
		}
		a.syncSessionFocus()
	}
	a.status = ""
	a.forwardSessionEvent(sessionKey, translated)
	return nil
}

func (a *vaxisTUIApp) handleResize(event vaxis.Resize) {
	width := event.Cols
	height := event.Rows
	if (width <= 0 || height <= 0) && a.vx != nil {
		width, height = a.vx.Window().Size()
	}
	if width <= 0 || height <= 0 || a.sessions == nil || a.state == nil {
		return
	}

	focus := a.effectiveLayoutFocus()
	cols, rows := a.activeTerminalSize(width, height, focus)
	if cols <= 0 || rows <= 0 {
		return
	}
	a.sessions.SetPreferredTerminalSize(cols, rows)

	term, ok := a.sessions.SessionTerminal(a.state.activeSessionKey())
	if !ok || term == nil {
		return
	}
	term.Resize(cols, rows)
}

func (a *vaxisTUIApp) mouseTerminalEntry() tuiTreeEntry {
	if a.state.focus == tuiFocusTerminal {
		return a.state.activeTerminalEntry()
	}
	return a.state.selectedEntry()
}

func (a *vaxisTUIApp) handleTerminalMouseGesture(targetKey string, mouse vaxis.Mouse, translated vaxis.Mouse) error {
	switch mouse.EventType {
	case vaxis.EventPress:
		if mouse.Button == vaxis.MouseLeftButton {
			a.beginTerminalMouseGesture(targetKey, translated)
		}
		return nil
	case vaxis.EventMotion:
		a.updateTerminalMouseGesture(targetKey, translated)
		return nil
	case vaxis.EventRelease:
		if mouse.Button != vaxis.MouseLeftButton {
			return nil
		}
		if a.finishTerminalMouseGesture(targetKey, translated) {
			return nil
		}
		if target, ok := a.terminalLinkTargetAt(mouse); ok {
			if err := a.openHyperlink(target); err != nil {
				return err
			}
			a.status = "opened " + target
			return nil
		}
		if a.state.focus != tuiFocusTerminal {
			if err := a.state.focusTerminal(); err != nil {
				return err
			}
			a.syncSessionFocus()
		}
		a.status = ""
	}
	return nil
}

func (a *vaxisTUIApp) handleTerminalClosed(event tuiTerminalClosedEvent) {
	session, ok := a.sessions.Session(event.TargetKey)
	if !ok {
		return
	}

	targetKey := session.target
	keys := a.sessions.TargetSessionKeys(targetKey)
	a.sessions.RemoveSession(event.TargetKey)
	if a.state.activeTabKeys[targetKey] == event.TargetKey {
		if nextKey := nextActiveTerminalTabAfterClose(keys, event.TargetKey); nextKey != "" {
			a.state.setActiveTab(targetKey, nextKey)
		} else {
			delete(a.state.activeTabKeys, targetKey)
		}
	}
	if a.state.focus == tuiFocusTerminal &&
		a.state.terminalTarget == targetKey &&
		len(a.sessions.TargetSessionKeys(targetKey)) == 0 {
		a.state.focusTree()
	}

	message := fmt.Sprintf("shell exited for %s", session.label)
	if event.Err != nil {
		message = fmt.Sprintf("%s: %s", message, event.Err)
	}
	a.status = message
	a.syncSessionFocus()
}

func (a *vaxisTUIApp) openHyperlink(target string) error {
	if a.openLink != nil {
		return a.openLink(target)
	}
	return openHyperlink(target)
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
	if entry.kind != tuiTreeEntryProject && entry.kind != tuiTreeEntryNode {
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

func (a *vaxisTUIApp) reloadData(preferredKey string) error {
	tree, err := loadTUIProjectTree(a.ctx, a.service, a.treeWorkspaceRoot)
	if err != nil {
		return err
	}
	return a.applyReloadedTree(tree, preferredKey)
}

func (a *vaxisTUIApp) applyReloadedTree(tree []ProjectTreeNode, preferredKey string) error {
	if err := a.state.replaceTree(tree, preferredKey); err != nil {
		return err
	}
	a.sessions.PruneStaleSessions(a.targetKeyStillExists)

	a.syncSessionFocus()
	return nil
}

// targetKeyStillExists reports whether targetKey resolves to a live project or
// node in the current tree. A key that does not parse as a target is treated as
// still existing (left untouched), matching the prior prefix-switch default arm.
func (a *vaxisTUIApp) targetKeyStillExists(targetKey string) bool {
	target, err := terminal.ParseTargetKey(targetKey)
	if err != nil {
		return true
	}
	switch target.Kind {
	case terminal.TargetNode:
		_, ok := a.state.nodesByID[target.ID]
		return ok
	case terminal.TargetProject:
		_, ok := a.state.projectsByID[target.ID]
		return ok
	default:
		return true
	}
}

func (a *vaxisTUIApp) startDataRefresh() {
	if a.refreshInFlight {
		return
	}

	a.refreshInFlight = true
	if a.postEvent == nil {
		tree, err := loadTUIProjectTree(a.ctx, a.service, a.treeWorkspaceRoot)
		a.finishDataRefresh(tuiRefreshCompleteEvent{Tree: tree, Err: err})
		return
	}

	go func() {
		tree, err := loadTUIProjectTree(a.ctx, a.service, a.treeWorkspaceRoot)
		a.postEvent(tuiRefreshCompleteEvent{Tree: tree, Err: err})
	}()
}

func (a *vaxisTUIApp) finishDataRefresh(event tuiRefreshCompleteEvent) {
	a.refreshInFlight = false
	if event.Err != nil {
		// Auto-refresh errors were previously swallowed silently; surface them at
		// the log seam (warn, not per-tick success) without disturbing the UI,
		// and retain them in the message ring at their true level so a failed
		// background refresh is visible in the messages view. A persistently
		// failing 2s auto-refresh would flood the ring with identical entries,
		// so consecutive duplicates are collapsed.
		a.service.log().Warn("tui refresh failed", "error", event.Err.Error())
		message := "refresh failed: " + event.Err.Error()
		if latest, ok := a.messages.Latest(); !ok || latest.Text != message {
			a.pushMessage(slog.LevelWarn, message)
		}
		return
	}
	if err := a.applyReloadedTree(event.Tree, ""); err != nil && a.status == "" {
		a.service.log().Warn("tui tree reload failed", "error", err.Error())
		a.status = err.Error()
	}
}

func startTUIAutoRefresh(ctx context.Context, postEvent func(vaxis.Event), interval time.Duration) func() {
	if postEvent == nil || interval <= 0 {
		return func() {}
	}

	refreshCtx, cancel := context.WithCancel(ctx)
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-refreshCtx.Done():
				return
			case <-ticker.C:
				postEvent(tuiRefreshTickEvent{})
			}
		}
	}()
	return cancel
}

func (a *vaxisTUIApp) startOperation(request tuiOperationRequest) error {
	if strings.TrimSpace(request.Title) == "" {
		return fmt.Errorf("operation title is required")
	}
	if request.Run == nil {
		return fmt.Errorf("operation runner is required")
	}
	if conflict := a.conflictingOperation(request.ResourceKeys); conflict != nil {
		return fmt.Errorf("%s is already in progress", strings.ToLower(conflict.Title))
	}

	if a.postEvent == nil {
		result, err := request.Run(a.ctx, a.service)
		if err != nil {
			return err
		}
		return a.applyOperationResult(result.PreferredKey, result)
	}

	if a.operations == nil {
		a.operations = map[string]*tuiOperationState{}
	}

	operationID := newID()
	a.operations[operationID] = &tuiOperationState{
		ID:            operationID,
		Title:         request.Title,
		DisplayStatus: request.DisplayStatus,
		SelectionKey:  a.state.selectedEntry().key(),
		EntryKeys:     append([]string(nil), request.EntryKeys...),
		ResourceKeys:  append([]string(nil), request.ResourceKeys...),
		Lines:         []string{"waiting for command output..."},
	}
	a.operationOrder = append(a.operationOrder, operationID)

	go func() {
		progress := newTUIProgressWriter(a.postEvent, operationID)
		service := a.service.withIO(progress, progress)
		result, err := request.Run(a.ctx, service)
		progress.Flush()
		a.postEvent(tuiOperationCompleteEvent{OperationID: operationID, Result: result, Err: err})
	}()

	return nil
}

func (a *vaxisTUIApp) applyOperationResult(selectedKey string, result tuiOperationResult) error {
	if result.CloseNodeID != "" {
		a.sessions.CloseNode(result.CloseNodeID)
	}
	if result.ReloadData {
		if err := a.reloadData(selectedKey); err != nil {
			return err
		}
	}
	if result.ShowTerminalPane {
		a.state.treePaneMode = tuiTreePaneModeTerminal
	}
	if result.Status != "" {
		a.status = result.Status
	}
	return nil
}

func (a *vaxisTUIApp) conflictingOperation(resourceKeys []string) *tuiOperationState {
	if len(resourceKeys) == 0 {
		return nil
	}

	for _, operationID := range a.operationOrder {
		operation := a.operations[operationID]
		if operation == nil {
			continue
		}
		if operationConflicts(operation.ResourceKeys, resourceKeys) {
			return operation
		}
	}

	return nil
}

func operationConflicts(active []string, requested []string) bool {
	if len(active) == 0 || len(requested) == 0 {
		return false
	}

	activeKeys := map[string]bool{}
	for _, key := range active {
		if strings.TrimSpace(key) == "" {
			continue
		}
		activeKeys[key] = true
	}
	for _, key := range requested {
		if activeKeys[key] {
			return true
		}
	}
	return false
}

func (a *vaxisTUIApp) appendOperationLine(operationID string, line string) {
	if strings.TrimSpace(line) == "" {
		return
	}
	operation := a.operations[operationID]
	if operation == nil {
		return
	}

	operation.Lines = append(operation.Lines, line)
	if len(operation.Lines) > 200 {
		operation.Lines = operation.Lines[len(operation.Lines)-200:]
	}
}

func (a *vaxisTUIApp) finishOperation(event tuiOperationCompleteEvent) {
	operation := a.operations[event.OperationID]
	delete(a.operations, event.OperationID)
	for index, operationID := range a.operationOrder {
		if operationID != event.OperationID {
			continue
		}
		a.operationOrder = append(a.operationOrder[:index], a.operationOrder[index+1:]...)
		break
	}

	// Retain the finished operation's output in the message ring at its true
	// level before it is discarded, so failed background operations stay
	// inspectable in the messages view (ADR 59).
	a.retainOperationMessages(operation, event)

	if event.Err != nil {
		a.status = event.Err.Error()
	} else if err := a.applyOperationResult(a.resultSelectionKey(operation, event.Result), event.Result); err != nil {
		a.status = err.Error()
	}

	// The result was already recorded at its proper level above; suppress the
	// draw-time info-level capture of the same status string.
	a.lastCapturedStatus = strings.TrimSpace(a.status)
}

const tuiRetainedOperationOutputLines = 40

// retainOperationMessages copies a finished operation's summary and captured
// output into the message ring. Success records at info, failure at error; the
// per-operation output is capped to its tail so one noisy operation cannot evict
// the entire ring.
func (a *vaxisTUIApp) retainOperationMessages(operation *tuiOperationState, event tuiOperationCompleteEvent) {
	if operation == nil {
		return
	}

	title := strings.TrimSpace(operation.Title)
	if title == "" {
		title = "background operation"
	}

	level := slog.LevelInfo
	summary := title + " completed"
	if event.Err != nil {
		level = slog.LevelError
		summary = title + " failed: " + event.Err.Error()
	} else if status := strings.TrimSpace(event.Result.Status); status != "" {
		summary = title + ": " + status
	}

	a.pushMessage(level, summary)
	for _, line := range retainedOperationOutput(operation.Lines) {
		a.pushMessage(level, title+" | "+line)
	}
}

func retainedOperationOutput(lines []string) []string {
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || trimmed == "waiting for command output..." {
			continue
		}
		filtered = append(filtered, line)
	}
	if len(filtered) > tuiRetainedOperationOutputLines {
		filtered = filtered[len(filtered)-tuiRetainedOperationOutputLines:]
	}
	return filtered
}

func (a *vaxisTUIApp) resultSelectionKey(operation *tuiOperationState, result tuiOperationResult) string {
	if a.state == nil {
		return result.PreferredKey
	}

	currentKey := a.state.selectedEntry().key()
	if operation == nil {
		if currentKey != "" {
			return currentKey
		}
		return result.PreferredKey
	}

	if currentKey != "" && currentKey != operation.SelectionKey {
		return currentKey
	}

	if result.PreferredKey != "" {
		return result.PreferredKey
	}
	if currentKey != "" {
		return currentKey
	}
	return operation.SelectionKey
}

func (a *vaxisTUIApp) openCreateProjectDialog() {
	description := []string{
		"Create a top-level project rooted at a host workspace.",
		"Use project fork when you want a child project copied from an existing workspace snapshot.",
		"Use the Environment Configs field to choose shared defaults for future nodes from the selector.",
	}

	dialog := newTUIDialog(
		"Create Project",
		"Create",
		description,
		[]tuiDialogField{
			newTUIInputField("slug", "Project Slug", "", false),
			newTUIInputField("workspace_path", "Workspace Path", "", true),
			newTUISelectorField("environment_configs", "Environment Configs", "", false, nil),
		},
		func(values map[string]string) error {
			title := "Creating project"
			if values["slug"] != "" {
				title += " " + values["slug"]
			}
			return a.startOperation(tuiOperationRequest{
				Title:         title,
				DisplayStatus: "creating",
				ResourceKeys:  []string{"projects"},
				EntryKeys:     []string{"projects"},
				Run: func(ctx context.Context, service *Service) (tuiOperationResult, error) {
					project, err := service.ProjectCreate(ctx, ProjectCreateInput{
						Slug:               values["slug"],
						WorkspacePath:      values["workspace_path"],
						EnvironmentConfigs: parseCommaSeparatedValues(values["environment_configs"]),
					})
					if err != nil {
						return tuiOperationResult{}, err
					}
					return tuiOperationResult{
						Status:       "created project " + project.Slug,
						PreferredKey: terminal.ProjectTarget(project.ID).String(),
						ReloadData:   true,
					}, nil
				},
			})
		},
	)
	dialog.Fields[2].Value = commaSeparatedValues([]string{})
	dialog.Fields[2].Display = func(value string) string {
		return environmentConfigSelectionSummary(parseCommaSeparatedValues(value))
	}
	dialog.Fields[2].Activate = func() error {
		return a.openEnvironmentConfigSelector(
			"Select Environment Configs",
			[]string{"Choose reusable environment configs to assign shared defaults for future nodes in this project."},
			parseCommaSeparatedValues(dialog.Fields[2].Value),
			true,
			func(values []string) error {
				dialog.SetFieldValue("environment_configs", commaSeparatedValues(values))
				return nil
			},
		)
	}
	a.dialog = dialog
}

func (a *vaxisTUIApp) openCreateNodeDialog(project Project) {
	dialog := newTUIDialog(
		"Create Node",
		"Create",
		[]string{
			"Selected project: " + project.Slug,
		},
		[]tuiDialogField{
			newTUIInputField("slug", "Node Slug", project.Slug+"-node", true),
			newTUIValueSelectorField("workspace_mode", "Workspace Mode", WorkspaceModeCopy, true, workspaceModeDisplay, nil),
			newTUIInputField("lima_commands_file", "Lima Commands File (optional)", "", false),
		},
		func(values map[string]string) error {
			limaCommands, err := loadOptionalLimaCommandsFile(values["lima_commands_file"])
			if err != nil {
				return err
			}
			return a.startOperation(tuiOperationRequest{
				Title:         "Creating node " + values["slug"],
				DisplayStatus: "creating",
				ResourceKeys:  []string{terminal.ProjectTarget(project.ID).String()},
				EntryKeys:     []string{terminal.ProjectTarget(project.ID).String()},
				Run: func(ctx context.Context, service *Service) (tuiOperationResult, error) {
					node, err := service.NodeCreate(ctx, NodeCreateInput{
						Project:       project.ID,
						Slug:          values["slug"],
						WorkspaceMode: values["workspace_mode"],
						LimaCommands:  limaCommands,
					})
					if err != nil {
						return tuiOperationResult{}, err
					}
					return tuiOperationResult{
						Status:           "created node " + node.Slug,
						PreferredKey:     terminal.NodeTarget(node.ID).String(),
						ReloadData:       true,
						ShowTerminalPane: true,
					}, nil
				},
			})
		},
	)
	dialog.Fields[1].Activate = func() error {
		return a.openWorkspaceModeSelector(
			dialog.Fields[1].rawValue(),
			func(value string) error {
				dialog.SetFieldValue("workspace_mode", value)
				return nil
			},
		)
	}
	a.dialog = dialog
}

func (a *vaxisTUIApp) openWorkspaceModeSelector(current string, onSubmit func(value string) error) error {
	options := []tuiSelectorOption{
		{Label: workspaceModeDisplay(WorkspaceModeCopy), Value: WorkspaceModeCopy},
		{Label: workspaceModeDisplay(WorkspaceModeMounted), Value: WorkspaceModeMounted},
	}
	a.selector = newTUISelector(
		"Workspace Mode",
		nil,
		options,
		[]string{coalesce(current, WorkspaceModeCopy)},
		false,
		func(values []string) error {
			if len(values) == 0 {
				return fmt.Errorf("select a workspace mode")
			}
			return onSubmit(values[0])
		},
	)
	return nil
}

func workspaceModeDisplay(mode string) string {
	switch normalizeWorkspaceMode(mode) {
	case WorkspaceModeMounted:
		return "mounted: writable host workspace mount"
	default:
		return "copy: isolated guest workspace copy"
	}
}

func (a *vaxisTUIApp) openUpdateProjectDialog(project Project) {
	dialog := newTUIDialog(
		"Update Project",
		"Update",
		[]string{
			"Update the selected project slug, workspace path, and assigned environment configs.",
			"Edit the project file shown in the right pane when you need advanced per-project settings such as Lima command overrides.",
		},
		[]tuiDialogField{
			newTUIInputField("slug", "Project Slug", project.Slug, true),
			newTUIInputField("workspace_path", "Workspace Path", project.WorkspacePath, true),
			newTUISelectorField("environment_configs", "Environment Configs", commaSeparatedValues(project.EnvironmentConfigs), false, nil),
		},
		func(values map[string]string) error {
			slug := values["slug"]
			workspacePath := values["workspace_path"]
			return a.startOperation(tuiOperationRequest{
				Title:         "Saving project " + project.Slug,
				DisplayStatus: "updating",
				ResourceKeys:  []string{terminal.ProjectTarget(project.ID).String()},
				EntryKeys:     []string{terminal.ProjectTarget(project.ID).String()},
				Run: func(_ context.Context, service *Service) (tuiOperationResult, error) {
					updated, err := service.ProjectUpdate(project.ID, ProjectUpdateInput{
						Slug:               &slug,
						WorkspacePath:      &workspacePath,
						EnvironmentConfigs: parseCommaSeparatedValues(values["environment_configs"]),
					})
					if err != nil {
						return tuiOperationResult{}, err
					}
					return tuiOperationResult{
						Status:       "updated project " + updated.Slug,
						PreferredKey: terminal.ProjectTarget(updated.ID).String(),
						ReloadData:   true,
					}, nil
				},
			})
		},
	)
	dialog.Fields[2].Display = func(value string) string {
		return environmentConfigSelectionSummary(parseCommaSeparatedValues(value))
	}
	dialog.Fields[2].Activate = func() error {
		return a.openEnvironmentConfigSelector(
			"Select Environment Configs",
			[]string{"Choose reusable environment configs to keep assigned to this project."},
			parseCommaSeparatedValues(dialog.Fields[2].Value),
			true,
			func(values []string) error {
				dialog.SetFieldValue("environment_configs", commaSeparatedValues(values))
				return nil
			},
		)
	}
	a.dialog = dialog
}

func (a *vaxisTUIApp) openEnvironmentConfigsMenu() error {
	configs, err := a.service.EnvironmentConfigList(false)
	if err != nil {
		return err
	}

	description := []string{
		"No environment configs configured.",
	}
	if len(configs) > 0 {
		description = description[:0]
		description = append(description, "Configured defaults:")
		for _, config := range configs {
			description = append(description, fmt.Sprintf("- %s (%d bootstrap commands)", config.Slug, len(config.BootstrapCommands)))
		}
	} else {
		description = append(description[:0], "No environment configs configured.")
	}

	entries := []tuiMenuEntry{
		{Key: 'c', Label: "Create Config", Action: func() error { a.openCreateEnvironmentConfigDialog(); return nil }},
	}
	if len(configs) > 0 {
		entries = append(entries, tuiMenuEntry{Key: 'm', Label: "Manage Config", Action: func() error { return a.openManageEnvironmentConfigDialog(configs[0].Slug) }})
	}

	a.menu = &tuiMenu{
		Title:       "Environment Configs",
		Description: description,
		Entries:     entries,
	}

	return nil
}

func (a *vaxisTUIApp) openCreateEnvironmentConfigDialog() {
	a.dialog = newTUIDialog(
		"Create Environment Config",
		"Create",
		[]string{
			"Create a reusable environment config for project bootstrap commands.",
			"Create the config first, then add or reorder as many commands as you need from the command editor.",
		},
		[]tuiDialogField{
			newTUIInputField("slug", "Config Slug", "", true),
		},
		func(values map[string]string) error {
			config, err := a.service.EnvironmentConfigCreate(EnvironmentConfigCreateInput{
				Slug: values["slug"],
			})
			if err != nil {
				return err
			}
			a.status = "created environment config " + config.Slug
			a.openEnvironmentConfigCommandMenu(config)
			return nil
		},
	)
}

func (a *vaxisTUIApp) openManageEnvironmentConfigDialog(defaultSlug string) error {
	selected := []string{}
	if strings.TrimSpace(defaultSlug) != "" {
		selected = append(selected, defaultSlug)
	}
	return a.openEnvironmentConfigSelector(
		"Manage Environment Config",
		[]string{"Choose an environment config to edit its commands or delete it."},
		selected,
		false,
		func(values []string) error {
			if len(values) == 0 {
				return fmt.Errorf("select an environment config")
			}
			config, err := a.service.EnvironmentConfigShow(values[0])
			if err != nil {
				return err
			}
			a.openEnvironmentConfigCommandMenu(config)
			return nil
		},
	)
}

func (a *vaxisTUIApp) openEnvironmentConfigCommandMenu(config EnvironmentConfig) {
	description := []string{
		"No bootstrap commands configured.",
	}
	if len(config.BootstrapCommands) > 0 {
		description = description[:0]
		description = append(description, "Configured bootstrap commands:")
		for index, command := range config.BootstrapCommands {
			description = append(description, fmt.Sprintf("%d. %s", index+1, command))
		}
	} else {
		description = append(description[:0], "No bootstrap commands configured.")
	}

	a.menu = &tuiMenu{
		Title:       "Environment Config: " + config.Slug,
		Description: description,
		Entries: []tuiMenuEntry{
			{Key: 'a', Label: "Add Bootstrap Command", Action: func() error { a.openAddEnvironmentConfigCommandDialog(config); return nil }},
			{Key: 'r', Label: "Remove Bootstrap Command", Action: func() error { return a.openRemoveEnvironmentConfigCommandDialog(config) }},
			{Key: 'm', Label: "Move Bootstrap Command", Action: func() error { return a.openMoveEnvironmentConfigCommandDialog(config) }},
			{Key: 'c', Label: "Clear Bootstrap Commands", Action: func() error { a.openClearEnvironmentConfigCommandsDialog(config); return nil }},
			{Key: 'd', Label: "Delete Config", Action: func() error { a.openDeleteEnvironmentConfigDialog(config); return nil }},
		},
	}
}

func (a *vaxisTUIApp) openAddEnvironmentConfigCommandDialog(config EnvironmentConfig) {
	a.dialog = newTUIDialog(
		"Add Environment Config Bootstrap Command",
		"Add",
		[]string{"Add a bootstrap command to the reusable environment config."},
		[]tuiDialogField{
			newTUIInputField("command", "Command", "", true),
		},
		func(values map[string]string) error {
			commands := append(append([]string(nil), config.BootstrapCommands...), values["command"])
			updated, err := a.service.EnvironmentConfigUpdate(config.ID, EnvironmentConfigUpdateInput{BootstrapCommands: commands})
			if err != nil {
				return err
			}
			a.status = "updated environment config " + updated.Slug
			return a.reopenEnvironmentConfigCommandMenu(updated.ID)
		},
	)
}

func (a *vaxisTUIApp) openRemoveEnvironmentConfigCommandDialog(config EnvironmentConfig) error {
	if len(config.BootstrapCommands) == 0 {
		return fmt.Errorf("environment config %s has no commands", config.Slug)
	}

	a.selector = newTUISelector(
		"Remove Environment Config Bootstrap Commands",
		[]string{"Choose one or more reusable environment config bootstrap commands to remove."},
		commandSelectorOptions(config.BootstrapCommands),
		nil,
		true,
		func(values []string) error {
			indices, err := parseSelectorIndices(values, len(config.BootstrapCommands))
			if err != nil {
				return err
			}
			if len(indices) == 0 {
				return fmt.Errorf("select at least one command to remove")
			}

			description := []string{"Remove the selected reusable environment config commands?"}
			for _, index := range indices {
				description = append(description, fmt.Sprintf("%d. %s", index+1, config.BootstrapCommands[index]))
			}

			a.dialog = newTUIDialog(
				"Remove Environment Config Bootstrap Commands",
				"Remove",
				description,
				nil,
				func(map[string]string) error {
					updated, err := a.service.EnvironmentConfigUpdate(config.ID, EnvironmentConfigUpdateInput{
						BootstrapCommands: removeCommandsByIndex(config.BootstrapCommands, indices),
					})
					if err != nil {
						return err
					}
					a.status = "updated environment config " + updated.Slug
					return a.reopenEnvironmentConfigCommandMenu(updated.ID)
				},
			)
			return nil
		},
	)

	return nil
}

func (a *vaxisTUIApp) openMoveEnvironmentConfigCommandDialog(config EnvironmentConfig) error {
	if len(config.BootstrapCommands) < 2 {
		return fmt.Errorf("environment config %s needs at least two commands to change order", config.Slug)
	}

	a.selector = newTUISelector(
		"Move Environment Config Bootstrap Command",
		[]string{"Choose a reusable environment config bootstrap command to move up or down."},
		commandSelectorOptions(config.BootstrapCommands),
		nil,
		false,
		func(values []string) error {
			indices, err := parseSelectorIndices(values, len(config.BootstrapCommands))
			if err != nil {
				return err
			}
			if len(indices) != 1 {
				return fmt.Errorf("select a single command to move")
			}
			index := indices[0]
			command := config.BootstrapCommands[index]

			entries := []tuiMenuEntry{}
			if index > 0 {
				entries = append(entries, tuiMenuEntry{Key: 'u', Label: "Move Up", Action: func() error {
					updated, err := a.service.EnvironmentConfigUpdate(config.ID, EnvironmentConfigUpdateInput{
						BootstrapCommands: moveCommand(config.BootstrapCommands, index, -1),
					})
					if err != nil {
						return err
					}
					a.status = "updated environment config " + updated.Slug
					return a.reopenEnvironmentConfigCommandMenu(updated.ID)
				}})
			}
			if index < len(config.BootstrapCommands)-1 {
				entries = append(entries, tuiMenuEntry{Key: 'd', Label: "Move Down", Action: func() error {
					updated, err := a.service.EnvironmentConfigUpdate(config.ID, EnvironmentConfigUpdateInput{
						BootstrapCommands: moveCommand(config.BootstrapCommands, index, 1),
					})
					if err != nil {
						return err
					}
					a.status = "updated environment config " + updated.Slug
					return a.reopenEnvironmentConfigCommandMenu(updated.ID)
				}})
			}

			a.menu = &tuiMenu{
				Title:       "Move Environment Config Bootstrap Command: " + command,
				Description: []string{"Choose how to reposition the selected reusable environment config bootstrap command."},
				Entries:     entries,
			}
			return nil
		},
	)
	return nil
}

func (a *vaxisTUIApp) openClearEnvironmentConfigCommandsDialog(config EnvironmentConfig) {
	a.dialog = newTUIDialog(
		"Clear Environment Config Bootstrap Commands",
		"Clear",
		[]string{"Remove all bootstrap commands from environment config " + config.Slug + "."},
		nil,
		func(_ map[string]string) error {
			updated, err := a.service.EnvironmentConfigUpdate(config.ID, EnvironmentConfigUpdateInput{ClearBootstrapCommands: true})
			if err != nil {
				return err
			}
			a.status = "cleared environment config " + updated.Slug
			return a.reopenEnvironmentConfigCommandMenu(updated.ID)
		},
	)
}

func (a *vaxisTUIApp) openDeleteEnvironmentConfigDialog(config EnvironmentConfig) {
	selectedKey := a.state.selectedEntry().key()
	a.dialog = newTUIDialog(
		"Delete Environment Config",
		"Delete",
		[]string{"Delete reusable environment config " + config.Slug + "."},
		nil,
		func(_ map[string]string) error {
			deleted, err := a.service.EnvironmentConfigDelete(config.ID)
			if err != nil {
				return err
			}
			a.status = "deleted environment config " + deleted.Slug
			return a.reloadData(selectedKey)
		},
	)
}

func (a *vaxisTUIApp) openDeleteProjectDialog(project Project) {
	a.dialog = newTUIDialog(
		"Delete Project",
		"Delete",
		[]string{
			"Delete project " + project.Slug + ".",
			"This only succeeds if the project has no live nodes or child projects.",
		},
		nil,
		func(_ map[string]string) error {
			return a.startOperation(tuiOperationRequest{
				Title:         "Deleting project " + project.Slug,
				DisplayStatus: "deleting",
				ResourceKeys:  []string{terminal.ProjectTarget(project.ID).String()},
				EntryKeys:     []string{terminal.ProjectTarget(project.ID).String()},
				Run: func(_ context.Context, service *Service) (tuiOperationResult, error) {
					deleted, err := service.ProjectDelete(project.ID)
					if err != nil {
						return tuiOperationResult{}, err
					}
					return tuiOperationResult{
						Status:     "deleted project " + deleted.Slug,
						ReloadData: true,
					}, nil
				},
			})
		},
	)
}

func (a *vaxisTUIApp) openDeleteNodeDialog(node Node) {
	a.dialog = newTUIDialog(
		"Delete Node",
		"Delete",
		[]string{
			"Delete node " + node.Slug + ".",
			"The associated Lima instance will be terminated.",
		},
		nil,
		func(_ map[string]string) error {
			return a.startOperation(tuiOperationRequest{
				Title:         "Deleting node " + node.Slug,
				DisplayStatus: "deleting",
				ResourceKeys:  []string{terminal.NodeTarget(node.ID).String()},
				EntryKeys:     []string{terminal.NodeTarget(node.ID).String()},
				Run: func(ctx context.Context, service *Service) (tuiOperationResult, error) {
					deleted, err := service.NodeDelete(ctx, node.ID)
					if err != nil {
						return tuiOperationResult{}, err
					}
					return tuiOperationResult{
						Status:      "deleted node " + deleted.Slug,
						CloseNodeID: deleted.ID,
						ReloadData:  true,
					}, nil
				},
			})
		},
	)
}

func (a *vaxisTUIApp) openCloneNodeDialog(node Node, project Project) {
	a.dialog = newTUIDialog(
		"Clone Node",
		"Clone",
		[]string{
			"Clone the selected node into another node in the same project.",
			"The cloned VM keeps the same guest workspace path and bootstrap state as the source.",
		},
		[]tuiDialogField{
			newTUIInputField("node_slug", "Cloned Node Slug", node.Slug+"-clone", true),
			newTUIInputField("lima_commands_file", "Lima Commands File (optional)", "", false),
		},
		func(values map[string]string) error {
			limaCommands, err := loadOptionalLimaCommandsFile(values["lima_commands_file"])
			if err != nil {
				return err
			}
			return a.startOperation(tuiOperationRequest{
				Title:         "Cloning node " + node.Slug,
				DisplayStatus: "cloning",
				ResourceKeys:  []string{terminal.NodeTarget(node.ID).String(), terminal.ProjectTarget(project.ID).String()},
				EntryKeys:     []string{terminal.NodeTarget(node.ID).String(), terminal.ProjectTarget(project.ID).String()},
				Run: func(ctx context.Context, service *Service) (tuiOperationResult, error) {
					childNode, err := service.NodeClone(ctx, NodeCloneInput{
						SourceNode:   node.ID,
						NodeSlug:     values["node_slug"],
						LimaCommands: limaCommands,
					})
					if err != nil {
						return tuiOperationResult{}, err
					}
					return tuiOperationResult{
						Status:       "cloned node " + node.Slug + " to " + childNode.Slug + " in " + project.Slug,
						PreferredKey: terminal.NodeTarget(childNode.ID).String(),
						ReloadData:   true,
					}, nil
				},
			})
		},
	)
}

// terminalContextEntry is the entry whose terminal tabs the tab keybindings
// operate on: the fullscreen-focused entry in terminal focus, otherwise the
// entry selected in the tree.
func (a *vaxisTUIApp) terminalContextEntry() tuiTreeEntry {
	if a.state.focus == tuiFocusTerminal {
		return a.state.activeTerminalEntry()
	}
	return a.state.selectedEntry()
}

func (a *vaxisTUIApp) openTerminalTab() error {
	entry := a.terminalContextEntry()
	if entry.kind != tuiTreeEntryProject && entry.kind != tuiTreeEntryNode {
		return fmt.Errorf("select a project or node to open a terminal tab")
	}

	if _, err := a.state.openTerminalTabEntry(entry); err != nil {
		return err
	}
	if a.state.focus != tuiFocusTerminal {
		a.state.treePaneMode = tuiTreePaneModeTerminal
	}
	return nil
}

func (a *vaxisTUIApp) switchTerminalTab(delta int) error {
	targetKey := a.state.activeTerminalTargetKey()
	keys := a.sessions.TargetSessionKeys(targetKey)
	if len(keys) == 0 {
		return fmt.Errorf("no terminal tabs are open for the focused item")
	}
	if delta == 0 {
		return nil
	}

	current := a.state.activeSessionKey()
	index := -1
	for i, key := range keys {
		if key == current {
			index = i
			break
		}
	}
	if index < 0 {
		index = 0
	} else {
		index = (index + delta) % len(keys)
		if index < 0 {
			index += len(keys)
		}
	}
	a.state.setActiveTab(targetKey, keys[index])
	return nil
}

func (a *vaxisTUIApp) closeTerminalTab() error {
	targetKey := a.state.activeTerminalTargetKey()
	sessionKey := a.state.activeSessionKey()
	if targetKey == "" || sessionKey == "" {
		return fmt.Errorf("no terminal tab is open for the focused item")
	}

	keys := a.sessions.TargetSessionKeys(targetKey)
	nextKey := nextActiveTerminalTabAfterClose(keys, sessionKey)

	a.sessions.CloseSession(sessionKey)
	a.sessions.ClearSessionError(targetKey)

	if nextKey != "" {
		a.state.setActiveTab(targetKey, nextKey)
		return nil
	}

	delete(a.state.activeTabKeys, targetKey)
	if a.state.hostTerminalReturnKey != "" && isTargetKind(targetKey, terminal.TargetProject) {
		a.state.hostTerminalReturnKey = ""
	}
	if a.state.focus == tuiFocusTerminal && a.state.terminalTarget == targetKey {
		a.state.focusTree()
	}
	return nil
}

func nextActiveTerminalTabAfterClose(keys []string, closingKey string) string {
	for index, key := range keys {
		if key != closingKey {
			continue
		}
		if index+1 < len(keys) {
			return keys[index+1]
		}
		if index > 0 {
			return keys[index-1]
		}
		return ""
	}
	return ""
}

func (a *vaxisTUIApp) forwardTerminalEvent(event vaxis.Event) {
	if a.state.focus != tuiFocusTerminal {
		return
	}

	a.forwardSessionEvent(a.state.activeSessionKey(), event)
}

func (a *vaxisTUIApp) forwardSessionEvent(sessionKey string, event vaxis.Event) {
	term, ok := a.sessions.SessionTerminal(sessionKey)
	if !ok {
		return
	}
	event = normalizeTUITerminalEvent(event)
	term.Update(event)
}

func (a *vaxisTUIApp) beginTerminalMouseGesture(targetKey string, mouse vaxis.Mouse) {
	a.terminalMouse = &tuiTerminalMouseGesture{
		targetKey: targetKey,
		startCol:  mouse.Col,
		startRow:  mouse.Row,
	}
}

func (a *vaxisTUIApp) updateTerminalMouseGesture(targetKey string, mouse vaxis.Mouse) {
	if a.terminalMouse == nil || a.terminalMouse.targetKey != targetKey {
		return
	}
	if mouse.Col != a.terminalMouse.startCol || mouse.Row != a.terminalMouse.startRow {
		a.terminalMouse.dragged = true
	}
}

func (a *vaxisTUIApp) finishTerminalMouseGesture(targetKey string, mouse vaxis.Mouse) bool {
	if a.terminalMouse == nil || a.terminalMouse.targetKey != targetKey {
		return false
	}
	if mouse.Col != a.terminalMouse.startCol || mouse.Row != a.terminalMouse.startRow {
		a.terminalMouse.dragged = true
	}
	dragged := a.terminalMouse.dragged
	a.terminalMouse = nil
	return dragged
}

func (a *vaxisTUIApp) cancelTerminalMouseGesture() {
	a.terminalMouse = nil
}

func (a *vaxisTUIApp) syncSessionFocus() {
	a.sessions.SyncFocus(a.state.activeSessionKey(), a.effectiveLayoutFocus() == tuiFocusTerminal)
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
	return a.menu != nil || a.dialog != nil || a.selector != nil || a.messagesView != nil
}

func (a *vaxisTUIApp) hostTerminalOverrideActive() bool {
	return a != nil &&
		a.state != nil &&
		a.state.hostTerminalReturnKey != "" &&
		isTargetKind(a.state.activeTerminalTargetKey(), terminal.TargetProject)
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
	switch entry.kind {
	case tuiTreeEntryProject, tuiTreeEntryNode:
		if a.rightPaneShowsInfo() {
			return "[Info] Terminal"
		}
		return "Info [Terminal]"
	default:
		return ""
	}
}

func (a *vaxisTUIApp) currentPaneTabSegments(entry tuiTreeEntry, activeStyle, inactiveStyle vaxis.Style) []vaxis.Segment {
	segments := []vaxis.Segment{}
	switch entry.kind {
	case tuiTreeEntryProject, tuiTreeEntryNode:
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
	default:
		return nil
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
	if session.kind == tuiTreeEntryProject && label != "" {
		label = "host:" + label
	}
	if total > 1 {
		label = fmt.Sprintf("%s %d", label, index+1)
	}
	return label
}

func (a *vaxisTUIApp) drawRightPane(win vaxis.Window, entry tuiTreeEntry, headerStyle, mutedStyle, errorStyle vaxis.Style) {
	switch {
	case a.messagesView != nil:
		a.messagesView.Draw(win, headerStyle, mutedStyle)
	case a.selector != nil:
		a.selector.Draw(win, headerStyle, mutedStyle)
	case a.dialog != nil:
		a.dialog.Draw(win, headerStyle, mutedStyle, errorStyle)
	case a.menu != nil:
		a.menu.Draw(win, headerStyle, mutedStyle)
	default:
		if a.rightPaneShowsInfo() {
			a.drawDetails(win, entry, headerStyle, mutedStyle)
			return
		}
		a.drawTerminalSurface(win, entry, headerStyle, mutedStyle, errorStyle)
	}
}

func (a *vaxisTUIApp) activePaneFooter(entry tuiTreeEntry, focus tuiFocus) string {
	switch {
	case a.messagesView != nil:
		return "Up/Down scroll   PgUp/PgDn page   g/G top/bottom   Esc close   Ctrl+c quit"
	case a.selector != nil:
		if a.selector.Multi {
			return "Up/Down move   Space toggle   Enter confirm   Ctrl+u clear   Esc cancel   Ctrl+c quit"
		}
		return "Up/Down move   Enter confirm   Esc cancel   Ctrl+c quit"
	case a.dialog != nil:
		return "Tab/Up/Down move   Enter submit/open   Right choose   Ctrl+s submit   Esc cancel   Ctrl+c quit"
	case a.menu != nil:
		return "Press a key to choose   Esc cancel   Ctrl+c quit"
	default:
		return renderFooter(focus, a.state.treePaneMode, entry)
	}
}

func (a *vaxisTUIApp) draw() {
	if a.vx == nil {
		return
	}

	a.captureStatusMessage()

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

	headerStyle := vaxis.Style{Attribute: vaxis.AttrBold}
	selectedStyle := tuiSelectedStyle()
	mutedStyle := tuiMutedStyle()
	errorStyle := vaxis.Style{Foreground: vaxis.ColorRed, Attribute: vaxis.AttrBold}

	layoutFocus := a.effectiveLayoutFocus()
	hostIndicator := a.hostTerminalOverrideActive()
	if hostIndicator {
		headerStyle = errorStyle
	}
	bodyTop := 1
	bodyHeight := height - bodyTop - 1
	layout := layoutTUIBody(width, layoutFocus)
	entry := a.state.selectedEntry()
	if layoutFocus == tuiFocusTerminal {
		entry = a.state.activeTerminalEntry()
	}

	projectSlug := "none"
	switch entry.kind {
	case tuiTreeEntryProject:
		projectSlug = entry.project.Slug
	case tuiTreeEntryNode:
		projectSlug = entry.project.Slug
	default:
	}
	if projectSlug == "" {
		projectSlug = "none"
	}
	if projectSlug == "none" {
		if project, ok := a.state.activeProject(); ok {
			projectSlug = project.Slug
		}
	}

	headerSegments := []vaxis.Segment{
		{Text: "Project: " + projectSlug, Style: headerStyle},
	}
	if entry.kind == tuiTreeEntryNode {
		headerSegments = append(headerSegments,
			vaxis.Segment{Text: "  Node: " + entry.node.Slug, Style: headerStyle},
			vaxis.Segment{Text: "  Mode: " + nodeWorkspaceMode(entry.node), Style: headerStyle},
		)
	}
	window.Println(0, headerSegments...)

	termOuter := window.New(layout.termCol, bodyTop, layout.termWidth, bodyHeight)

	if layout.treeVisible {
		treeOuter := window.New(0, bodyTop, layout.treeWidth, bodyHeight)
		treeInner := border.All(treeOuter, mutedStyle)

		treeInner.Println(0, vaxis.Segment{Text: "Projects / Nodes", Style: headerStyle})
		treeInnerWidth, treeInnerHeight := treeInner.Size()
		treeContentHeight := treeInnerHeight - 1
		if treeContentHeight < 0 {
			treeContentHeight = 0
		}
		treeContent := treeInner.New(0, 1, treeInnerWidth, treeContentHeight)
		treeOriginCol, treeOriginRow := treeContent.Origin()
		a.treeContentRect = tuiRect{col: treeOriginCol, row: treeOriginRow, width: treeInnerWidth, height: treeContentHeight}

		for row, entry := range a.state.visibleEntries(treeContentHeight) {
			index := a.state.viewportStart(treeContentHeight) + row
			style := mutedStyle
			if index == a.state.selection {
				style = selectedStyle
			}

			label := a.treeEntryLabel(entry)
			treeContent.Println(row, vaxis.Segment{Text: label, Style: style})
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
		footerStyle = errorStyle
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
	if entry.kind != tuiTreeEntryNode {
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
	if operationStatus := a.operationDisplayStatus(tuiTreeEntry{kind: tuiTreeEntryNode, node: node}); operationStatus != "" {
		return operationStatus
	}
	return nodeVMStatus(node)
}

func (a *vaxisTUIApp) treeEntryLabel(entry tuiTreeEntry) string {
	status := ""
	if entry.kind == tuiTreeEntryNode {
		status = a.nodeStatusText(entry.node)
	}
	return tuiEntryLabelWithStatus(entry, status)
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
	if entry.kind == "" || len(a.operationOrder) == 0 {
		return a.globalOperations()
	}

	entryKey := entry.key()
	operations := make([]*tuiOperationState, 0, len(a.operationOrder))
	for _, operationID := range a.operationOrder {
		operation := a.operations[operationID]
		if operation == nil {
			continue
		}
		if containsString(operation.EntryKeys, entryKey) || containsString(operation.EntryKeys, "projects") {
			operations = append(operations, operation)
		}
	}
	return operations
}

func (a *vaxisTUIApp) globalOperations() []*tuiOperationState {
	operations := make([]*tuiOperationState, 0, len(a.operationOrder))
	for _, operationID := range a.operationOrder {
		operation := a.operations[operationID]
		if operation == nil || !containsString(operation.EntryKeys, "projects") {
			continue
		}
		operations = append(operations, operation)
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
	switch entry.kind {
	case tuiTreeEntryProject:
		win.Println(row, vaxis.Segment{Text: "Project controls", Style: headerStyle})
		row++
		win.Println(row, vaxis.Segment{Text: "Slug: " + entry.project.Slug})
		row++
		a.printLinkifiedLine(win, row, "Workspace: "+entry.project.WorkspacePath, vaxis.Style{})
		row++
		projectPath := ""
		if a.service != nil && a.service.store != nil {
			projectPath = a.service.store.projectPath(entry.project.ID)
		}
		if projectPath != "" {
			a.printLinkifiedLine(win, row, "Project file: "+projectPath, vaxis.Style{})
			row++
		}
		row++
		if len(entry.project.EnvironmentConfigs) == 0 {
			win.Println(row, vaxis.Segment{Text: "Environment configs: none", Style: mutedStyle})
			row++
		} else {
			win.Println(row, vaxis.Segment{Text: "Environment configs: " + commaSeparatedValues(entry.project.EnvironmentConfigs), Style: mutedStyle})
			row++
		}
		if len(entry.project.LimaCommands.Bootstrap) == 0 {
			win.Println(row, vaxis.Segment{Text: "Project bootstrap commands: none", Style: mutedStyle})
		} else {
			win.Println(row, vaxis.Segment{Text: "Project bootstrap commands: configured", Style: mutedStyle})
		}
		row++
		row++
		win.Println(row, vaxis.Segment{Text: "Create nodes, update the project binding, or edit the project file directly for advanced settings such as Lima command overrides.", Style: mutedStyle})
		row++
		a.drawEntryOperations(win, row, entry, headerStyle, mutedStyle)
	case tuiTreeEntryNode:
		win.Println(row, vaxis.Segment{Text: "Node controls", Style: headerStyle})
		row++
		win.Println(row, vaxis.Segment{Text: "Slug: " + entry.node.Slug})
		row++
		win.Println(row, vaxis.Segment{Text: "Status: " + a.nodeStatusText(entry.node)})
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
		if workspace := nodeWorkspacePath(entry.node); workspace != "" {
			a.printLinkifiedLine(win, row, "Workspace: "+workspace, vaxis.Style{})
			row++
		}
		row++
		if nodeAutoStartsSession(entry.node) {
			win.Println(row, vaxis.Segment{Text: fmt.Sprintf("Node is running. Press %s to open a terminal tab or %s to focus its terminal.", terminalTabOpenFooterHint, terminalViewToggleTextHint), Style: mutedStyle})
		} else {
			win.Println(row, vaxis.Segment{Text: "Start the node before opening its terminal tabs, or edit the node file directly for advanced per-node Lima command overrides.", Style: mutedStyle})
		}
		row++
		a.drawEntryOperations(win, row, entry, headerStyle, mutedStyle)
	default:
		win.Println(0, vaxis.Segment{Text: "Press [a] to create a project or select a project or node in the tree.", Style: mutedStyle})
		_ = a.drawEntryOperations(win, 2, entry, headerStyle, mutedStyle)
	}
}

func (a *vaxisTUIApp) drawEntryOperations(win vaxis.Window, row int, entry tuiTreeEntry, headerStyle, mutedStyle vaxis.Style) int {
	operations := a.entryOperations(entry)
	if len(operations) == 0 {
		return row
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
	return row
}

func (a *vaxisTUIApp) drawTerminalSurface(win vaxis.Window, entry tuiTreeEntry, headerStyle, mutedStyle, errorStyle vaxis.Style) {
	if term, ok := a.sessions.SessionTerminal(a.state.activeSessionKey()); ok {
		term.Draw(win)
		return
	}

	row := 0
	switch entry.kind {
	case tuiTreeEntryProject:
		a.printLinkifiedLine(win, row, "Workspace: "+entry.project.WorkspacePath, vaxis.Style{})
		row += 2
		if err := a.sessions.SessionError(entry.key()); err != nil {
			win.Println(row, vaxis.Segment{Text: "Unable to start the local workspace shell.", Style: errorStyle})
			row++
			win.Println(row, vaxis.Segment{Text: err.Error(), Style: mutedStyle})
			row++
		} else {
			win.Println(row, vaxis.Segment{Text: fmt.Sprintf("Press %s to open a workspace terminal tab.", terminalTabOpenFooterHint), Style: mutedStyle})
			row++
			win.Println(row, vaxis.Segment{Text: fmt.Sprintf("Press %s to show project info.", infoViewToggleFooterHint), Style: mutedStyle})
		}
		row++
		a.drawEntryOperations(win, row, entry, headerStyle, mutedStyle)
	case tuiTreeEntryNode:
		win.Println(row, vaxis.Segment{Text: "Status: " + a.nodeStatusText(entry.node), Style: headerStyle})
		row += 2
		if err := a.sessions.SessionError(entry.key()); err != nil {
			win.Println(row, vaxis.Segment{Text: "Unable to start the node terminal.", Style: errorStyle})
			row++
			win.Println(row, vaxis.Segment{Text: err.Error(), Style: mutedStyle})
			row++
		} else if nodeAutoStartsSession(entry.node) {
			win.Println(row, vaxis.Segment{Text: fmt.Sprintf("No terminal tab is open. Press %s to open one.", terminalTabOpenFooterHint), Style: mutedStyle})
			row++
		} else {
			win.Println(row, vaxis.Segment{Text: "Start the node with [s] before opening a terminal tab.", Style: mutedStyle})
			row++
		}
		win.Println(row, vaxis.Segment{Text: fmt.Sprintf("Press %s to show node info.", infoViewToggleFooterHint), Style: mutedStyle})
		row++
		a.drawEntryOperations(win, row, entry, headerStyle, mutedStyle)
	case "":
		win.Println(0, vaxis.Segment{Text: "Select a project or node in the tree.", Style: mutedStyle})
	default:
		a.drawDetails(win, entry, headerStyle, mutedStyle)
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
			terminalTabCloseFooterHint + " close tab",
		}
		if entry.kind == tuiTreeEntryNode || entry.kind == tuiTreeEntryProject {
			parts = append(parts, hostTerminalToggleFooterHint+" host/node")
		}
		parts = append(parts, "q quit")
		return strings.Join(parts, "   ")
	}

	parts := make([]string, 0, 12)
	if entry.kind != "" {
		parts = append(parts, "Up/Down move", "Left/Right collapse")
		if entry.kind == tuiTreeEntryProject || (entry.kind == tuiTreeEntryNode && nodeAutoStartsSession(entry.node)) {
			parts = append(parts, terminalViewToggleFooterHint+" shell focus")
			parts = append(parts, terminalTabOpenFooterHint+" new tab")
		}
		if paneMode == tuiTreePaneModeTerminal {
			parts = append(parts, infoViewToggleFooterHint+" info")
			parts = append(parts, terminalTabPrevFooterHint+"/"+terminalTabNextFooterHint+" switch tab")
			parts = append(parts, terminalTabCloseFooterHint+" close tab")
		} else {
			parts = append(parts, infoViewToggleFooterHint+" terminal")
		}
		if entry.kind == tuiTreeEntryNode {
			parts = append(parts, hostTerminalToggleFooterHint+" host terminal")
		}
	}

	for _, action := range availableTUIActions(entry) {
		parts = append(parts, fmt.Sprintf("[%c] %s", action.Hotkey, strings.ToLower(action.Label)))
	}
	parts = append(parts, "q quit")
	return strings.Join(parts, "   ")
}

func tuiEntryLabelWithStatus(entry tuiTreeEntry, statusOverride string) string {
	indent := strings.Repeat("  ", entry.depth)
	switch entry.kind {
	case tuiTreeEntryProject:
		marker := "▶"
		if entry.expanded {
			marker = "▼"
		}
		if !entry.hasChildren {
			marker = "•"
		}
		return indent + marker + " " + entry.project.Slug
	case tuiTreeEntryNode:
		status := statusOverride
		if strings.TrimSpace(status) == "" {
			status = nodeVMStatus(entry.node)
		}
		status = strings.ToUpper(status)
		return indent + "• " + entry.node.Slug + "  " + status
	default:
		return ""
	}
}

func isTerminalViewToggleKey(key vaxis.Key) bool {
	if isHostTerminalToggleKey(key) {
		return false
	}
	return keyMatchesTerminalModifier(key, '`') || key.Matches(vaxis.KeyF06)
}

func isHostTerminalToggleKey(key vaxis.Key) bool {
	if !hasTerminalModifier(normalizedKeyModifiers(key.Modifiers), vaxis.ModShift) {
		return false
	}
	return key.Keycode == '`' || key.ShiftedCode == '~' || key.BaseLayoutCode == '`' || key.Text == "~"
}

func isTerminalTabOpenKey(key vaxis.Key) bool {
	return keyMatchesOptionShortcut(key, 't', "†")
}

func isTerminalTabNextKey(key vaxis.Key) bool {
	return keyMatchesTerminalModifier(key, vaxis.KeyRight) ||
		keyMatchesTerminalModifier(key, 'f')
}

func isTerminalTabPreviousKey(key vaxis.Key) bool {
	return keyMatchesTerminalModifier(key, vaxis.KeyLeft) ||
		keyMatchesTerminalModifier(key, 'b')
}

func isTerminalTabCloseKey(key vaxis.Key) bool {
	return keyMatchesOptionShortcut(key, 'w', "∑")
}

func keyMatchesOptionShortcut(key vaxis.Key, code rune, optionTexts ...string) bool {
	if keyMatchesTerminalModifier(key, code) {
		return true
	}

	modifiers := normalizedKeyModifiers(key.Modifiers)
	if modifiers != 0 && !hasTerminalModifier(modifiers, 0) {
		return false
	}
	for _, text := range optionTexts {
		if key.Text == text {
			return true
		}
		runes := []rune(text)
		if len(runes) == 1 && key.Keycode == runes[0] {
			return true
		}
	}
	return false
}

func keyMatchesTerminalModifier(key vaxis.Key, code rune) bool {
	return key.Matches(code, vaxis.ModAlt) ||
		key.Matches(code, vaxis.ModMeta) ||
		key.Matches(code, vaxis.ModAlt|vaxis.ModMeta)
}

func hasTerminalModifier(modifiers vaxis.ModifierMask, extra vaxis.ModifierMask) bool {
	terminalModifiers := []vaxis.ModifierMask{
		vaxis.ModAlt,
		vaxis.ModMeta,
		vaxis.ModAlt | vaxis.ModMeta,
	}
	for _, terminalModifier := range terminalModifiers {
		if modifiers == terminalModifier|extra {
			return true
		}
	}
	return false
}

func normalizedKeyModifiers(modifiers vaxis.ModifierMask) vaxis.ModifierMask {
	return modifiers &^ vaxis.ModCapsLock &^ vaxis.ModNumLock
}

func isQuitKey(key vaxis.Key) bool {
	return key.Matches('c', vaxis.ModCtrl)
}
