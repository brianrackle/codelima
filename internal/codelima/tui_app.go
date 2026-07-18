package codelima

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"git.sr.ht/~rockorager/vaxis"
	"git.sr.ht/~rockorager/vaxis/widgets/term"

	"github.com/brianrackle/test_lima/internal/codelima/terminal"
)

type vaxisTUIRunner struct{}

func newTUIRunner() TUIRunner {
	return &vaxisTUIRunner{}
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

func loadTUIProjectTree(ctx context.Context, service *Service, workspaceRoot string) ([]ProjectTreeNode, error) {
	nodes, err := service.NodeListByDirectoryRoot(ctx, workspaceRoot, false)
	if err != nil {
		return nil, err
	}
	return []ProjectTreeNode{{Project: Project{ID: "flat-nodes", Slug: "nodes"}, Nodes: nodes}}, nil
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

func (a *vaxisTUIApp) handleTerminalClosed(event tuiTerminalClosedEvent) {
	session, ok := a.sessions.Session(event.TargetKey)
	if !ok {
		return
	}

	targetKey := session.target
	keys := a.sessions.TargetSessionKeys(targetKey)
	a.sessions.RemoveSession(event.TargetKey)
	if a.state.activeTab(targetKey) == event.TargetKey {
		if nextKey := nextActiveTerminalTabAfterClose(keys, event.TargetKey); nextKey != "" {
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

// targetKeyStillExists reports whether targetKey resolves to a live node in the
// current list. A key that does not parse as a target is treated as
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
