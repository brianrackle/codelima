package codelima

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"git.sr.ht/~rockorager/vaxis"
	"git.sr.ht/~rockorager/vaxis/widgets/term"

	"github.com/brianrackle/codelima/internal/codelima/terminal"
)

type vaxisTUIRunner struct{}

func newTUIRunner() TUIRunner {
	return &vaxisTUIRunner{}
}

type vaxisTUIApp struct {
	ctx               context.Context
	service           *Service
	vx                *vaxis.Vaxis
	postEvent         func(vaxis.Event)
	treeWorkspaceRoot string
	openLink          func(string) error
	screenHyperlinkAt func(int, int) (string, bool)
	state             *tuiState
	sessions          *tuiSessionStore
	operations        map[string]*tuiOperationState
	operationOrder    []string
	linkRegions       []tuiLinkRegion
	terminalMouse     *tuiTerminalMouseGesture
	// overlay is the single active right-pane override (dialog, menu,
	// selector, or messages view); nil means no overlay is showing.
	overlay          tuiOverlay
	messages         *tuiMessageLog
	status           string
	statusLevel      slog.Level
	refreshInFlight  bool
	clipboardPush    func(string) error
	treeContentRect  tuiRect
	terminalBodyRect tuiRect
}

// Typed views of the active overlay: nil when no overlay of that concrete
// type is showing. Dialog flows and tests use these instead of per-type
// fields.
func (a *vaxisTUIApp) activeDialog() *tuiDialog {
	dialog, _ := a.overlay.(*tuiDialog)
	return dialog
}

func (a *vaxisTUIApp) activeMenu() *tuiMenu {
	menu, _ := a.overlay.(*tuiMenu)
	return menu
}

func (a *vaxisTUIApp) activeSelector() *tuiSelector {
	selector, _ := a.overlay.(*tuiSelector)
	return selector
}

func (a *vaxisTUIApp) activeMessagesView() *tuiMessagesView {
	view, _ := a.overlay.(*tuiMessagesView)
	return view
}

// showSelector displays a selector overlay. A selector opened while a dialog
// is showing is a field picker: the dialog is recorded as the selector's
// parent and restored when the selector is dismissed, preserving the old
// behavior where a.selector stacked over a.dialog.
func (a *vaxisTUIApp) showSelector(selector *tuiSelector) {
	if dialog, ok := a.overlay.(*tuiDialog); ok {
		selector.Parent = dialog
	}
	a.overlay = selector
}

// overlayParent returns the overlay to restore when overlay is dismissed:
// the recorded parent for field-picker selectors, nil for everything else.
func overlayParent(overlay tuiOverlay) tuiOverlay {
	if selector, ok := overlay.(*tuiSelector); ok && selector.Parent != nil {
		return selector.Parent
	}
	return nil
}

const (
	terminalViewToggleFooterHint  = "Opt-`/F6"
	terminalViewToggleTextHint    = "Opt-` or F6"
	hostTerminalTabOpenFooterHint = "Opt-Shift-t"
	infoViewToggleFooterHint      = "[i]"
	terminalTabOpenFooterHint     = "Opt-t"
	terminalTabNextFooterHint     = "Opt-Right"
	terminalTabPrevFooterHint     = "Opt-Left"
	terminalTabCloseFooterHint    = "Opt-w"
	tuiAutoRefreshInterval        = 2 * time.Second
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

	nodes, err := loadTUINodes(ctx, service, workspaceRoot)
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

	state, err := newTUIState(nodes, sessions)
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
		app.setStatus(slog.LevelError, err.Error())
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
// sessions.Close() defer as a backstop for failures before the loop starts.
// Daemon-backed runtimes detach here and keep their PTYs alive; local fallback
// runtimes still close and group-kill their child trees. Close drains the
// session map, so a second call is a no-op. Run also keeps
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

func loadTUINodes(ctx context.Context, service *Service, workspaceRoot string) ([]Node, error) {
	return service.NodeListByDirectoryRoot(ctx, workspaceRoot, false)
}

// serve() contract even while every current handler routes errors to status.
//
//nolint:unparam // the error result is the event-loop abort channel; it is part of the
func (a *vaxisTUIApp) handleEvent(event vaxis.Event) (bool, error) {
	switch event := event.(type) {
	case tuiDaemonTerminalDirtyEvent:
		a.sessions.markDaemonTerminalDirty(event.TerminalID, a.state.activeSessionKey())
		return false, nil
	case tuiRefreshTickEvent:
		a.startDataRefresh()
		return false, nil
	case tuiRefreshCompleteEvent:
		a.finishDataRefresh(event)
		a.draw()
		return false, nil
	case tuiClipboardEvent:
		if err := a.copyToHostClipboard(event.Text); err != nil {
			a.setStatus(slog.LevelError, err.Error())
		} else {
			a.setStatus(slog.LevelInfo, "synced VM clipboard to host clipboard")
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
		a.setStatus(slog.LevelError, event.Err.Error())
		a.draw()
		return false, nil
	case vaxis.FocusIn:
		// A newer TUI can revoke this request connection while both windows stay
		// open. Window focus is an explicit user handoff, so reclaim ownership
		// before the next key or mouse event can mutate a terminal.
		if a.service != nil && a.service.daemonClient != nil {
			if err := takeTUIDaemonInput(a.ctx, a.service.daemonClient); err != nil {
				a.setStatus(slog.LevelError, fmt.Sprintf("reclaim terminal input ownership: %v", err))
			}
		}
		a.draw()
		return false, nil
	}

	if key, ok := event.(vaxis.Key); ok && isQuitKey(key) && a.overlay != nil {
		return true, nil
	}

	if a.overlay != nil {
		overlay := a.overlay
		done, err := overlay.Update(event)
		if err != nil {
			// Overlay Update errors are non-fatal: they surface in the footer
			// status and dismiss the overlay. (Previously dialog errors aborted
			// the whole TUI while selector/menu errors went to status; status
			// is now the one policy.)
			a.setStatus(slog.LevelError, err.Error())
		}
		// An overlay handler may have opened a replacement overlay (menus open
		// selectors, selectors open confirmation dialogs); only dismiss when
		// the dispatched overlay is still the active one.
		if (done || err != nil) && a.overlay == overlay {
			a.overlay = overlayParent(overlay)
		}
		a.draw()
		return false, nil
	}

	switch event := event.(type) {
	case vaxis.Key:
		if a.state != nil && a.state.focus == tuiFocusTerminal && isTUITerminalPayloadKey(event) {
			a.forwardTerminalEvent(event)
			return false, nil
		}
		quit := a.handleKey(event)
		a.draw()
		return quit, nil
	case vaxis.Mouse:
		a.handleMouse(event)
		a.draw()
		return false, nil
	case vaxis.PasteStartEvent:
		a.forwardTerminalEvent(event)
	case vaxis.PasteEndEvent:
		a.forwardTerminalEvent(event)
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

func (a *vaxisTUIApp) handleTerminalClosed(event tuiTerminalClosedEvent) {
	session, ok := a.sessions.Session(event.SessionKey)
	if !ok {
		return
	}

	targetKey := session.target
	keys := a.sessions.TargetSessionKeys(targetKey)
	a.sessions.RemoveSession(event.SessionKey)
	if a.state.activeTab(targetKey) == event.SessionKey {
		if nextKey := nextActiveTerminalTabAfterClose(keys, event.SessionKey); nextKey != "" {
			a.state.setActiveTab(targetKey, nextKey)
		} else {
			a.state.clearActiveTab(targetKey)
		}
	}
	if a.state.focus == tuiFocusTerminal &&
		a.state.terminalTarget == targetKey &&
		len(a.sessions.TargetSessionKeys(targetKey)) == 0 {
		a.state.focusTree()
	}

	level := slog.LevelInfo
	message := fmt.Sprintf("shell exited for %s", session.label)
	if event.Err != nil {
		level = slog.LevelError
		message = fmt.Sprintf("%s: %s", message, event.Err)
	}
	a.setStatus(level, message)
	a.syncSessionFocus()
}

func (a *vaxisTUIApp) openHyperlink(target string) error {
	if a.openLink != nil {
		return a.openLink(target)
	}
	return openHyperlink(target)
}

func (a *vaxisTUIApp) reloadData(preferredKey string) error {
	nodes, err := loadTUINodes(a.ctx, a.service, a.treeWorkspaceRoot)
	if err != nil {
		return err
	}
	return a.applyReloadedNodes(nodes, preferredKey)
}

func (a *vaxisTUIApp) applyReloadedNodes(nodes []Node, preferredKey string) error {
	if err := a.state.replaceNodes(nodes, preferredKey); err != nil {
		return err
	}
	a.sessions.PruneStaleSessions(a.targetKeyStillExists)

	a.syncSessionFocus()
	return nil
}

// targetKeyStillExists reports whether targetKey resolves to a live node in
// either this window's scoped list or the global node store. A path-scoped TUI
// must not close daemon tabs merely because their nodes belong to another TUI
// window's directory scope. Ambiguous store failures also retain tabs: only a
// confirmed missing or deleted node is destructive. A key that does not parse
// as a target is treated as still existing (left untouched).
func (a *vaxisTUIApp) targetKeyStillExists(targetKey string) bool {
	target, err := terminal.ParseTargetKey(targetKey)
	if err != nil {
		return true
	}
	switch target.Kind {
	case terminal.TargetNode:
		if _, ok := a.state.nodesByID[target.ID]; ok {
			return true
		}
		if a.service == nil || a.service.store == nil {
			return false
		}
		node, loadErr := a.service.store.NodeByID(target.ID)
		if loadErr != nil {
			return !IsNotFound(loadErr)
		}
		return node.DeletedAt == nil && node.Status != NodeStatusTerminated
	case terminal.TargetProject:
		// Project targets belong to the retired project-terminal model. Treat
		// any restored bookkeeping for them as stale in schema v3.
		return false
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
		nodes, err := loadTUINodes(a.ctx, a.service, a.treeWorkspaceRoot)
		a.finishDataRefresh(tuiRefreshCompleteEvent{Nodes: nodes, Err: err})
		return
	}

	go func() {
		nodes, err := loadTUINodes(a.ctx, a.service, a.treeWorkspaceRoot)
		a.postEvent(tuiRefreshCompleteEvent{Nodes: nodes, Err: err})
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
	if err := a.applyReloadedNodes(event.Nodes, ""); err != nil && a.status == "" {
		a.service.log().Warn("tui node reload failed", "error", err.Error())
		a.setStatus(slog.LevelError, err.Error())
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
	if !entry.valid() {
		return fmt.Errorf("select a node to open a terminal tab")
	}

	if _, err := a.state.openTerminalTabEntry(entry); err != nil {
		return err
	}
	if a.state.focus != tuiFocusTerminal {
		a.state.treePaneMode = tuiTreePaneModeTerminal
	}
	return nil
}

func (a *vaxisTUIApp) openHostTerminalTab() error {
	entry := a.terminalContextEntry()
	if _, err := a.state.openHostTerminalTabEntry(entry); err != nil {
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

	a.state.clearActiveTab(targetKey)
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

func (a *vaxisTUIApp) syncSessionFocus() {
	a.sessions.SyncFocus(a.state.activeSessionKey(), a.effectiveLayoutFocus() == tuiFocusTerminal)
}
